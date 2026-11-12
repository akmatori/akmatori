// Package telegram lets the agent send messages to the Telegram chats of the
// Telegram connector. It reads the bot token and the chats from the
// connector's Integration and Channel rows on the gateway's own DB
// connection, so there is nothing to configure twice. The agent can only send
// to chats configured as enabled, postable Telegram channels.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"gorm.io/gorm"
)

const (
	// APIBaseURL is the default Telegram Bot API endpoint.
	APIBaseURL = "https://api.telegram.org"
	// MaxMessageChars is Telegram's limit for one message text.
	MaxMessageChars = 4096
	// maxParts bounds how many messages one send_message call may post.
	maxParts = 4
	// maxSendsPerRun caps send_message calls per agent run.
	maxSendsPerRun = 10
	// httpTimeout bounds one Bot API request.
	httpTimeout = 20 * time.Second
	// maxRetryAfter caps the wait before the single retry on a 429.
	maxRetryAfter = 30 * time.Second
	// maxResponseBytes caps a Bot API response body.
	maxResponseBytes = 1 << 20
	// counterTTL drops per-run counters of runs that ended long ago.
	counterTTL = 6 * time.Hour
)

// Run statuses that may no longer send (mirrors the API's AgentRunStatus).
var finishedRunStatuses = map[string]bool{"completed": true, "failed": true, "superseded": true}

// Tool serves telegram.*.
type Tool struct {
	db     *gorm.DB
	logger *log.Logger

	// Test seams; zero values use the production behaviour.
	baseURL    string
	httpClient func(ctx context.Context) *http.Client
	sleep      func(ctx context.Context, d time.Duration) error
	now        func() time.Time

	mu       sync.Mutex
	counters map[string]*runCounter // run id -> sends
}

type runCounter struct {
	sends int
	seen  time.Time
}

// NewTool creates the Telegram tool.
func NewTool(db *gorm.DB, logger *log.Logger) *Tool {
	return &Tool{db: db, logger: logger, counters: map[string]*runCounter{}}
}

// chat is one Telegram chat the agent may send to.
type chat struct {
	Chat    string `json:"chat"`
	Name    string `json:"name,omitempty"`
	Default bool   `json:"default,omitempty"`

	token string
}

// chats loads every enabled, postable Telegram channel of an enabled
// Telegram integration that has a bot token. Default chat first, then by name.
func (t *Tool) chats(ctx context.Context) ([]chat, error) {
	if t.db == nil {
		return nil, errors.New("telegram: database is not available")
	}
	var integrations []database.Integration
	if err := t.db.WithContext(ctx).Where("provider = ? AND enabled = ?", "telegram", true).
		Order("id ASC").Find(&integrations).Error; err != nil {
		return nil, fmt.Errorf("telegram: load integrations: %w", err)
	}
	tokens := map[uint]string{}
	ids := make([]uint, 0, len(integrations))
	for _, in := range integrations {
		token, _ := in.Credentials["bot_token"].(string)
		if token = strings.TrimSpace(token); token == "" {
			continue
		}
		tokens[in.ID] = token
		ids = append(ids, in.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var channels []database.Channel
	if err := t.db.WithContext(ctx).Where("integration_id IN ? AND enabled = ? AND can_post = ?", ids, true, true).
		Order("id ASC").Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("telegram: load channels: %w", err)
	}
	out := make([]chat, 0, len(channels))
	seen := map[string]bool{}
	for _, ch := range channels {
		ext := strings.TrimSpace(ch.ExternalID)
		if ext == "" || seen[ext] {
			continue
		}
		seen[ext] = true
		out = append(out, chat{Chat: ext, Name: ch.DisplayName, Default: ch.IsDefaultPost, token: tokens[ch.IntegrationID]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// errNoChats is returned when the Telegram connector has no chat to send to.
var errNoChats = errors.New("no Telegram chat is configured: the operator adds chats on the Telegram connector (Connectors page)")

// ListChats returns the chats send_message accepts.
func (t *Tool) ListChats(ctx context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	chats, err := t.chats(ctx)
	if err != nil {
		return nil, err
	}
	if len(chats) == 0 {
		return nil, errNoChats
	}
	return map[string]interface{}{"chats": chats, "count": len(chats)}, nil
}

// pick resolves the chat argument: the chat id, or the chat name (case
// insensitive). Empty picks the default chat, or the only chat.
func pick(chats []chat, want string) (*chat, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		for i := range chats {
			if chats[i].Default {
				return &chats[i], nil
			}
		}
		if len(chats) == 1 {
			return &chats[0], nil
		}
		return nil, fmt.Errorf("chat is required: there are %d chats and none is the default (see telegram.list_chats)", len(chats))
	}
	for i := range chats {
		if chats[i].Chat == want {
			return &chats[i], nil
		}
	}
	var byName []int
	for i := range chats {
		if chats[i].Name != "" && strings.EqualFold(chats[i].Name, want) {
			byName = append(byName, i)
		}
	}
	switch len(byName) {
	case 1:
		return &chats[byName[0]], nil
	case 0:
		return nil, fmt.Errorf("chat %q is not a configured Telegram chat (see telegram.list_chats)", want)
	default:
		return nil, fmt.Errorf("chat name %q matches %d chats: pass the chat id (see telegram.list_chats)", want, len(byName))
	}
}

// requireRun checks the X-Run-ID of a send: the run exists, belongs to the
// scope, is still running and is not proposal chat.
func (t *Tool) requireRun(ctx context.Context, scopeID string) (string, error) {
	runID := mcp.RunIDFromContext(ctx)
	if runID == "" || scopeID == "" {
		return "", errors.New("telegram.send_message needs the run id (X-Run-ID) and the scope")
	}
	var run database.AgentRun
	if err := t.db.WithContext(ctx).Select("id", "run_id", "scope_uuid", "source_kind", "status").
		Where("run_id = ?", runID).Take(&run).Error; err != nil {
		return "", errors.New("run does not belong to this scope: unknown run id")
	}
	if run.ScopeUUID != scopeID {
		return "", errors.New("run does not belong to this scope")
	}
	if finishedRunStatuses[run.Status] {
		return "", fmt.Errorf("run %s is %s; sends are refused", runID, run.Status)
	}
	if run.SourceKind == "proposal" {
		return "", errors.New("proposal chat does not send Telegram messages")
	}
	return runID, nil
}

func (t *Tool) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *Tool) hasQuota(runID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.counters[runID]
	return c == nil || c.sends < maxSendsPerRun
}

func (t *Tool) takeQuota(runID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	for id, c := range t.counters {
		if now.Sub(c.seen) > counterTTL {
			delete(t.counters, id)
		}
	}
	c := t.counters[runID]
	if c == nil {
		c = &runCounter{}
		t.counters[runID] = c
	}
	c.sends++
	c.seen = now
}

// splitText cuts text into parts of at most limit runes, preferring a line
// break, then a space, as the cut point.
func splitText(text string, limit int) []string {
	var parts []string
	for utf8.RuneCountInString(text) > limit {
		runes := []rune(text)
		cut := limit
		if i := lastIndexRune(runes[:limit], '\n'); i > limit/2 {
			cut = i + 1
		} else if i := lastIndexRune(runes[:limit], ' '); i > limit/2 {
			cut = i + 1
		}
		parts = append(parts, strings.TrimRight(string(runes[:cut]), " \n"))
		text = strings.TrimLeft(string(runes[cut:]), " \n")
	}
	if strings.TrimSpace(text) != "" {
		parts = append(parts, text)
	}
	return parts
}

func lastIndexRune(runes []rune, r rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

// target splits a chat into chat id and optional forum topic id
// ("-1001234567890:42"), like the API's Telegram provider.
func target(c string) (string, int64) {
	if i := strings.LastIndex(c, ":"); i > 0 {
		if topic, err := strconv.ParseInt(c[i+1:], 10, 64); err == nil {
			return c[:i], topic
		}
	}
	return c, 0
}

// SendMessage posts plain text to one configured chat.
// Args: text (required), chat (chat id or name; default: the default chat).
func (t *Tool) SendMessage(ctx context.Context, scopeID string, args map[string]interface{}) (interface{}, error) {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("text is required")
	}
	if n := utf8.RuneCountInString(text); n > maxParts*MaxMessageChars {
		return nil, fmt.Errorf("text is %d characters; the limit is %d: send a summary", n, maxParts*MaxMessageChars)
	}
	runID, err := t.requireRun(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	if !t.hasQuota(runID) {
		return nil, fmt.Errorf("this run already sent %d Telegram messages, the limit per run", maxSendsPerRun)
	}
	chats, err := t.chats(ctx)
	if err != nil {
		return nil, err
	}
	if len(chats) == 0 {
		return nil, errNoChats
	}
	wantChat, _ := args["chat"].(string)
	c, err := pick(chats, wantChat)
	if err != nil {
		return nil, err
	}

	chatID, topic := target(c.Chat)
	parts := splitText(text, MaxMessageChars)
	ids := make([]int64, 0, len(parts))
	for i, p := range parts {
		payload := map[string]interface{}{
			"chat_id":                  chatID,
			"text":                     p,
			"disable_web_page_preview": true,
		}
		if topic != 0 {
			payload["message_thread_id"] = topic
		}
		id, err := t.sendOne(ctx, c.token, payload)
		if err != nil {
			if len(ids) > 0 {
				t.takeQuota(runID)
				return nil, fmt.Errorf("telegram: sent %d of %d parts to %s, then: %w", i, len(parts), c.Chat, err)
			}
			return nil, fmt.Errorf("telegram: send to %s: %w", c.Chat, err)
		}
		ids = append(ids, id)
	}
	t.takeQuota(runID)
	if t.logger != nil {
		t.logger.Printf("telegram.send_message: run %s sent %d message(s) to %s", runID, len(ids), c.Chat)
	}
	return map[string]interface{}{"chat": c.Chat, "name": c.Name, "message_ids": ids}, nil
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// sendOne calls sendMessage and returns the message id. A 429 is retried
// once after retry_after (at most maxRetryAfter).
func (t *Tool) sendOne(ctx context.Context, token string, payload map[string]interface{}) (int64, error) {
	for attempt := 0; ; attempt++ {
		env, status, err := t.call(ctx, token, "sendMessage", payload)
		if err != nil {
			return 0, err
		}
		if env.OK {
			var msg struct {
				MessageID int64 `json:"message_id"`
			}
			if err := json.Unmarshal(env.Result, &msg); err != nil {
				return 0, fmt.Errorf("decode result: %w", err)
			}
			return msg.MessageID, nil
		}
		code := env.ErrorCode
		if code == 0 {
			code = status
		}
		if code == http.StatusTooManyRequests && attempt == 0 {
			wait := time.Duration(env.Parameters.RetryAfter) * time.Second
			if wait <= 0 {
				wait = time.Second
			}
			if wait <= maxRetryAfter {
				if err := t.wait(ctx, wait); err != nil {
					return 0, err
				}
				continue
			}
		}
		desc := env.Description
		if desc == "" {
			desc = http.StatusText(status)
		}
		return 0, fmt.Errorf("telegram api error %d: %s", code, desc)
	}
}

// call posts one Bot API request. Errors never carry the request URL: it
// holds the bot token.
func (t *Tool) call(ctx context.Context, token, method string, payload interface{}) (*envelope, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	base := strings.TrimRight(t.baseURL, "/")
	if base == "" {
		base = APIBaseURL
	}
	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("build request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client(ctx).Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("unexpected response (HTTP %d)", resp.StatusCode)
	}
	return &env, resp.StatusCode, nil
}

func (t *Tool) wait(ctx context.Context, d time.Duration) error {
	if t.sleep != nil {
		return t.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// client returns an HTTP client that honours the configured HTTP proxy. Like
// the API's messaging providers, the Slack proxy toggle covers Telegram.
func (t *Tool) client(ctx context.Context) *http.Client {
	if t.httpClient != nil {
		if c := t.httpClient(ctx); c != nil {
			return c
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if t.db != nil {
		var ps database.ProxySettings
		if err := t.db.WithContext(ctx).First(&ps).Error; err == nil && ps.ProxyURL != "" && ps.SlackEnabled {
			if proxyURL, perr := url.Parse(ps.ProxyURL); perr == nil {
				transport.Proxy = http.ProxyURL(proxyURL)
			} else if t.logger != nil {
				t.logger.Printf("telegram: invalid proxy URL, connecting directly")
			}
		}
	}
	return &http.Client{Timeout: httpTimeout, Transport: transport}
}
