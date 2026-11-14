package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

const zulipDefaultTopic = "Akmatori"

// ZulipProvider sends messages through Zulip's REST API (outbound only).
// Credentials come from the channel's Integration: site_url, email and
// api_key of a bot, plus an optional default topic. A channel's ExternalID is
// the target stream (channel) name or id.
//
// Text is provider-neutral Markdown; it is adapted for Zulip and split into
// messages of at most ZulipMaxMessageChars.
type ZulipProvider struct {
	botHTTP
	limit int
}

// NewZulipProvider returns the production Zulip provider.
func NewZulipProvider() *ZulipProvider {
	return &ZulipProvider{}
}

// Name reports the canonical provider id.
func (p *ZulipProvider) Name() database.MessagingProvider {
	return database.MessagingProviderZulip
}

// zulipAPIError is a non-success Zulip response.
type zulipAPIError struct {
	Status  int
	Code    string
	Message string
}

func (e *zulipAPIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("zulip api error %d (%s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("zulip api error %d: %s", e.Status, e.Message)
}

// zulipClient is one bot's connection to a Zulip organization.
type zulipClient struct {
	p       *ZulipProvider
	siteURL string
	email   string
	apiKey  string
}

func credentialString(credentials database.JSONB, key string) string {
	value, _ := credentials[key].(string)
	return strings.TrimSpace(value)
}

func (p *ZulipProvider) clientForIntegration(integration *database.Integration) (*zulipClient, error) {
	if integration == nil {
		return nil, errors.New("zulip: integration is missing")
	}
	creds := integration.Credentials
	siteURL := credentialString(creds, "site_url")
	email := credentialString(creds, "email")
	apiKey := credentialString(creds, "api_key")
	if siteURL == "" || email == "" || apiKey == "" {
		return nil, errors.New("zulip: site_url, email, and api_key credentials are required")
	}
	parsed, err := url.Parse(siteURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("zulip: invalid site_url %q (want https://your-org.zulipchat.com)", siteURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery, parsed.Fragment = "", ""
	return &zulipClient{p: p, siteURL: parsed.String(), email: email, apiKey: apiKey}, nil
}

// prepare resolves the client, stream and default topic of a channel.
func (p *ZulipProvider) prepare(channel *database.Channel) (*zulipClient, string, string, error) {
	if channel == nil {
		return nil, "", "", errors.New("zulip: channel is nil")
	}
	stream := strings.TrimSpace(channel.ExternalID)
	if stream == "" {
		return nil, "", "", fmt.Errorf("zulip: channel %q has no external_id", channel.DisplayName)
	}
	integration, err := p.integrationFor(channel)
	if errors.Is(err, errMissingBotToken) {
		return nil, "", "", errors.New("zulip: site_url, email, and api_key credentials are required")
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("zulip: %w", err)
	}
	client, err := p.clientForIntegration(integration)
	if err != nil {
		return nil, "", "", err
	}
	topic := credentialString(integration.Credentials, "topic")
	if topic == "" {
		topic = zulipDefaultTopic
	}
	return client, stream, zulipTopicName(topic), nil
}

func (p *ZulipProvider) chunks(text string) ([]string, error) {
	limit := p.limit
	if limit <= 0 {
		limit = ZulipMaxMessageChars
	}
	chunks := RenderZulip(text, limit)
	if len(chunks) == 0 {
		return nil, errors.New("zulip: message text is empty")
	}
	return chunks, nil
}

// zulipResponse is the envelope of every Zulip API response.
type zulipResponse struct {
	Result     string  `json:"result"`
	Msg        string  `json:"msg"`
	Code       string  `json:"code"`
	RetryAfter float64 `json:"retry-after"`
	ID         int64   `json:"id"`
	// GET /messages/{id}
	Message *struct {
		Type             string          `json:"type"`
		DisplayRecipient json.RawMessage `json:"display_recipient"`
		Subject          string          `json:"subject"`
		StreamID         int64           `json:"stream_id"`
	} `json:"message"`
	// GET /users/me
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// request sends one form-encoded API request (GET: query string) and decodes
// the envelope. A 429 is retried once after retry-after. Errors never carry
// the API key.
func (c *zulipClient) request(ctx context.Context, method, path string, values url.Values) (*zulipResponse, error) {
	endpoint := c.siteURL + path
	var payload zulipResponse
	resp, err := c.p.withRateLimitRetry(ctx, func() (*botResponse, time.Duration, error) {
		r, err := c.do(ctx, method, endpoint, values)
		if err != nil {
			return nil, 0, err
		}
		payload = zulipResponse{}
		_ = json.Unmarshal(r.body, &payload)
		var retryAfter time.Duration
		if r.status == http.StatusTooManyRequests {
			if payload.RetryAfter > 0 {
				retryAfter = time.Duration(payload.RetryAfter * float64(time.Second))
			} else if s, perr := strconv.ParseFloat(r.header.Get("Retry-After"), 64); perr == nil {
				retryAfter = time.Duration(s * float64(time.Second))
			}
		}
		return r, retryAfter, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.status < 200 || resp.status > 299 || payload.Result != "success" {
		msg := payload.Msg
		if msg == "" {
			msg = http.StatusText(resp.status)
		}
		return nil, &zulipAPIError{Status: resp.status, Code: payload.Code, Message: msg}
	}
	return &payload, nil
}

func (c *zulipClient) do(ctx context.Context, method, endpoint string, values url.Values) (*botResponse, error) {
	var body io.Reader
	if method == http.MethodGet {
		if len(values) > 0 {
			endpoint += "?" + values.Encode()
		}
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("zulip: build request failed")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.SetBasicAuth(c.email, c.apiKey)
	resp, err := c.p.client().Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("zulip: %s request failed: %w", method, uerr.Err)
		}
		return nil, fmt.Errorf("zulip: %s request failed", method)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("zulip: read response: %w", err)
	}
	return &botResponse{status: resp.StatusCode, body: data, header: resp.Header}, nil
}

func (c *zulipClient) send(ctx context.Context, stream, topic, content string) (string, error) {
	values := url.Values{"type": {"stream"}, "to": {stream}, "topic": {topic}, "content": {content}}
	resp, err := c.request(ctx, http.MethodPost, "/api/v1/messages", values)
	if err != nil {
		return "", err
	}
	if resp.ID == 0 {
		return "", errors.New("zulip: response has no message id")
	}
	return strconv.FormatInt(resp.ID, 10), nil
}

func (c *zulipClient) sendChunks(ctx context.Context, stream, topic string, chunks []string) (string, error) {
	first := ""
	for i, chunk := range chunks {
		id, err := c.send(ctx, stream, topic, chunk)
		if err != nil {
			if i == 0 {
				return "", fmt.Errorf("zulip send message: %w", err)
			}
			return first, fmt.Errorf("zulip send message part %d/%d: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			first = id
		}
	}
	return first, nil
}

// messageThread returns the stream and topic of a stream message.
func (c *zulipClient) messageThread(ctx context.Context, messageID string) (string, string, error) {
	resp, err := c.request(ctx, http.MethodGet, "/api/v1/messages/"+url.PathEscape(messageID), url.Values{"apply_markdown": {"false"}})
	if err != nil {
		return "", "", err
	}
	if resp.Message == nil {
		return "", "", errors.New("response has no message")
	}
	// display_recipient is the stream name for a stream message and a list
	// of users for a direct message.
	var stream string
	if err := json.Unmarshal(resp.Message.DisplayRecipient, &stream); err != nil || stream == "" || resp.Message.Subject == "" {
		return "", "", errors.New("not a stream message")
	}
	return stream, resp.Message.Subject, nil
}

// PostMessage sends text to the channel's stream under the integration's
// topic; MessageID is the first message.
func (p *ZulipProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	client, stream, topic, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	id, err := client.sendChunks(ctx, stream, topic, chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// PostThreadReply posts to the stream and topic of parentMessageID: a Zulip
// thread is a stream + topic, not a parent message. When the parent cannot be
// read, the reply goes to the channel's stream and topic.
func (p *ZulipProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if strings.TrimSpace(parentMessageID) == "" {
		return nil, errors.New("zulip: parent message id is required for thread reply")
	}
	client, stream, topic, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	if s, t, terr := client.messageThread(ctx, parentMessageID); terr == nil {
		stream, topic = s, t
	} else {
		slog.Warn("zulip: parent message thread not found, replying to the channel topic",
			"message_id", parentMessageID, "err", terr)
	}
	id, err := client.sendChunks(ctx, stream, topic, chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// UpdateMessage edits messageID with the first chunk; further chunks are
// posted to the same topic.
func (p *ZulipProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("zulip: message id is required for update")
	}
	client, stream, topic, err := p.prepare(channel)
	if err != nil {
		return err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return err
	}
	if _, err := client.request(ctx, http.MethodPatch, "/api/v1/messages/"+url.PathEscape(messageID), url.Values{"content": {chunks[0]}}); err != nil {
		return fmt.Errorf("zulip edit message: %w", err)
	}
	if len(chunks) > 1 {
		if s, t, terr := client.messageThread(ctx, messageID); terr == nil {
			stream, topic = s, t
		}
		if _, err := client.sendChunks(ctx, stream, topic, chunks[1:]); err != nil {
			return err
		}
	}
	return nil
}

// TestCredentials calls GET /api/v1/users/me and returns the bot's name.
func (p *ZulipProvider) TestCredentials(ctx context.Context, integration *database.Integration) (string, error) {
	client, err := p.clientForIntegration(integration)
	if err != nil {
		return "", err
	}
	me, err := client.request(ctx, http.MethodGet, "/api/v1/users/me", nil)
	if err != nil {
		return "", err
	}
	if me.FullName != "" {
		return me.FullName, nil
	}
	return me.Email, nil
}
