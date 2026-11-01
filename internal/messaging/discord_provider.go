package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// DiscordAPIBaseURL is the default Discord REST API (v10) endpoint.
const DiscordAPIBaseURL = "https://discord.com/api/v10"

// DiscordProvider posts to Discord channels through the REST API with a bot
// token (outbound only). Credentials come from the channel's Integration
// ({"bot_token"}); the Channel external_id is the Discord channel id.
//
// Text is provider-neutral Markdown; it is adapted for Discord and split into
// messages of at most DiscordMaxMessageChars.
type DiscordProvider struct {
	botHTTP
	limit int
}

// NewDiscordProvider returns the production Discord provider.
func NewDiscordProvider() *DiscordProvider {
	return &DiscordProvider{botHTTP: botHTTP{baseURL: DiscordAPIBaseURL}}
}

// Name reports the canonical provider id.
func (p *DiscordProvider) Name() database.MessagingProvider {
	return database.MessagingProviderDiscord
}

// discordAPIError is a non-2xx Discord response.
type discordAPIError struct {
	Status  int
	Code    int
	Message string
}

func (e *discordAPIError) Error() string {
	return fmt.Sprintf("discord api error %d (code %d): %s", e.Status, e.Code, e.Message)
}

// call sends one REST request and decodes a 2xx JSON body into out. A 429 is
// retried once after retry_after.
func (p *DiscordProvider) call(ctx context.Context, token, method, path string, payload, out any) error {
	base := strings.TrimRight(p.baseURL, "/")
	if base == "" {
		base = DiscordAPIBaseURL
	}
	headers := map[string]string{
		"Authorization": "Bot " + token,
		"User-Agent":    "DiscordBot (https://akmatori.com, 1.0)",
	}
	resp, err := p.withRateLimitRetry(ctx, func() (*botResponse, time.Duration, error) {
		r, err := p.doJSON(ctx, method, base+path, headers, payload)
		if err != nil {
			return nil, 0, fmt.Errorf("discord: %w", err)
		}
		var retryAfter time.Duration
		if r.status == http.StatusTooManyRequests {
			var body struct {
				RetryAfter float64 `json:"retry_after"`
			}
			if json.Unmarshal(r.body, &body) == nil && body.RetryAfter > 0 {
				retryAfter = time.Duration(body.RetryAfter * float64(time.Second))
			} else if s, perr := strconv.ParseFloat(r.header.Get("Retry-After"), 64); perr == nil {
				retryAfter = time.Duration(s * float64(time.Second))
			}
		}
		return r, retryAfter, nil
	})
	if err != nil {
		return err
	}
	if resp.status < 200 || resp.status > 299 {
		var body struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(resp.body, &body)
		if body.Message == "" {
			body.Message = http.StatusText(resp.status)
		}
		return &discordAPIError{Status: resp.status, Code: body.Code, Message: body.Message}
	}
	if out != nil && len(resp.body) > 0 {
		if err := json.Unmarshal(resp.body, out); err != nil {
			return fmt.Errorf("discord: decode response: %w", err)
		}
	}
	return nil
}

func (p *DiscordProvider) prepare(channel *database.Channel) (token, channelID string, err error) {
	if channel == nil {
		return "", "", errors.New("discord: channel is nil")
	}
	channelID = strings.TrimSpace(channel.ExternalID)
	if channelID == "" {
		return "", "", fmt.Errorf("discord: channel %q has no external_id", channel.DisplayName)
	}
	token, err = p.tokenFor(channel)
	if err != nil {
		return "", "", fmt.Errorf("discord: %w", err)
	}
	return token, channelID, nil
}

func (p *DiscordProvider) chunks(text string) ([]string, error) {
	limit := p.limit
	if limit <= 0 {
		limit = DiscordMaxMessageChars
	}
	chunks := RenderDiscord(text, limit)
	if len(chunks) == 0 {
		return nil, errors.New("discord: message text is empty")
	}
	return chunks, nil
}

// noMentions stops @everyone / role / user pings from agent-written text.
var noMentions = map[string]any{"parse": []string{}}

func (p *DiscordProvider) send(ctx context.Context, token, channelID, replyTo, content string) (string, error) {
	payload := map[string]any{"content": content, "allowed_mentions": noMentions}
	if replyTo != "" {
		payload["message_reference"] = map[string]any{"message_id": replyTo, "fail_if_not_exists": false}
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := p.call(ctx, token, http.MethodPost, "/channels/"+url.PathEscape(channelID)+"/messages", payload, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (p *DiscordProvider) sendChunks(ctx context.Context, token, channelID, replyTo string, chunks []string) (string, error) {
	first := ""
	for i, c := range chunks {
		id, err := p.send(ctx, token, channelID, replyTo, c)
		if err != nil {
			if i == 0 {
				return "", fmt.Errorf("discord send message: %w", err)
			}
			return first, fmt.Errorf("discord send message part %d/%d: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			first = id
		}
	}
	return first, nil
}

// PostMessage sends text as one or more messages; MessageID is the first.
func (p *DiscordProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	token, channelID, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	id, err := p.sendChunks(ctx, token, channelID, "", chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// PostThreadReply sends text as reply(s) referencing parentMessageID. It does
// not create Discord threads.
func (p *DiscordProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if parentMessageID == "" {
		return nil, errors.New("discord: parent message id is required for thread reply")
	}
	token, channelID, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	id, err := p.sendChunks(ctx, token, channelID, parentMessageID, chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// UpdateMessage edits messageID with the first chunk; further chunks are
// posted as replies to it.
func (p *DiscordProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if messageID == "" {
		return errors.New("discord: message id is required for update")
	}
	token, channelID, err := p.prepare(channel)
	if err != nil {
		return err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return err
	}
	path := "/channels/" + url.PathEscape(channelID) + "/messages/" + url.PathEscape(messageID)
	payload := map[string]any{"content": chunks[0], "allowed_mentions": noMentions}
	if err := p.call(ctx, token, http.MethodPatch, path, payload, nil); err != nil {
		return fmt.Errorf("discord edit message: %w", err)
	}
	if len(chunks) > 1 {
		if _, err := p.sendChunks(ctx, token, channelID, messageID, chunks[1:]); err != nil {
			return err
		}
	}
	return nil
}

// TestCredentials calls GET /users/@me and returns the bot's username.
func (p *DiscordProvider) TestCredentials(ctx context.Context, integration *database.Integration) (string, error) {
	token, err := botTokenOf(integration)
	if err != nil {
		return "", err
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := p.call(ctx, token, http.MethodGet, "/users/@me", nil, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}
