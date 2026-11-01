package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// TelegramAPIBaseURL is the default Telegram Bot API endpoint.
const TelegramAPIBaseURL = "https://api.telegram.org"

// TelegramProvider posts to Telegram chats through the Bot API (outbound
// only). Credentials come from the channel's Integration ({"bot_token"}); the
// Channel external_id is the chat id ("-1001234567890" or "@channel"),
// optionally with a forum topic suffix ("-1001234567890:42").
//
// Text is provider-neutral Markdown; it is rendered to Telegram HTML and
// split into messages of at most TelegramMaxMessageChars.
type TelegramProvider struct {
	botHTTP
	limit int
}

// NewTelegramProvider returns the production Telegram provider.
func NewTelegramProvider() *TelegramProvider {
	return &TelegramProvider{botHTTP: botHTTP{baseURL: TelegramAPIBaseURL}}
}

// Name reports the canonical provider id.
func (p *TelegramProvider) Name() database.MessagingProvider {
	return database.MessagingProviderTelegram
}

// telegramAPIError is a non-ok Bot API response.
type telegramAPIError struct {
	Code        int
	Description string
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

func isTelegramParseError(err error) bool {
	var apiErr *telegramAPIError
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(apiErr.Description), "can't parse entities")
}

func isTelegramNotModified(err error) bool {
	var apiErr *telegramAPIError
	return errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Description), "message is not modified")
}

type telegramEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call invokes a Bot API method and decodes "result" into out (may be nil).
// A 429 is retried once after retry_after.
func (p *TelegramProvider) call(ctx context.Context, token, method string, payload any, out any) error {
	base := strings.TrimRight(p.baseURL, "/")
	if base == "" {
		base = TelegramAPIBaseURL
	}
	endpoint := base + "/bot" + token + "/" + method
	var env telegramEnvelope
	resp, err := p.withRateLimitRetry(ctx, func() (*botResponse, time.Duration, error) {
		r, err := p.doJSON(ctx, http.MethodPost, endpoint, nil, payload)
		if err != nil {
			return nil, 0, fmt.Errorf("telegram %s: %w", method, err)
		}
		env = telegramEnvelope{}
		_ = json.Unmarshal(r.body, &env)
		return r, time.Duration(env.Parameters.RetryAfter) * time.Second, nil
	})
	if err != nil {
		return err
	}
	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = resp.status
		}
		desc := env.Description
		if desc == "" {
			desc = http.StatusText(resp.status)
		}
		return &telegramAPIError{Code: code, Description: desc}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// telegramTarget splits a Channel external_id into chat id and optional
// forum topic id ("chat:topic").
func telegramTarget(externalID string) (chatID string, topicID int64) {
	externalID = strings.TrimSpace(externalID)
	if i := strings.LastIndex(externalID, ":"); i > 0 {
		if t, err := strconv.ParseInt(externalID[i+1:], 10, 64); err == nil {
			return externalID[:i], t
		}
	}
	return externalID, 0
}

func (p *TelegramProvider) prepare(channel *database.Channel) (token, chatID string, topicID int64, err error) {
	if channel == nil {
		return "", "", 0, errors.New("telegram: channel is nil")
	}
	chatID, topicID = telegramTarget(channel.ExternalID)
	if chatID == "" {
		return "", "", 0, fmt.Errorf("telegram: channel %q has no external_id", channel.DisplayName)
	}
	token, err = p.tokenFor(channel)
	if err != nil {
		return "", "", 0, fmt.Errorf("telegram: %w", err)
	}
	return token, chatID, topicID, nil
}

func (p *TelegramProvider) chunks(text string) ([]string, error) {
	limit := p.limit
	if limit <= 0 {
		limit = TelegramMaxMessageChars
	}
	chunks := RenderTelegram(text, limit)
	if len(chunks) == 0 {
		return nil, errors.New("telegram: message text is empty")
	}
	return chunks, nil
}

type telegramMessage struct {
	MessageID int64 `json:"message_id"`
}

// send posts one HTML chunk, retrying once as plain text when Telegram
// refuses the HTML.
func (p *TelegramProvider) send(ctx context.Context, token, chatID string, topicID int64, replyTo, htmlText string) (string, error) {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     htmlText,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if topicID != 0 {
		payload["message_thread_id"] = topicID
	}
	if replyTo != "" {
		if id, err := strconv.ParseInt(replyTo, 10, 64); err == nil {
			payload["reply_parameters"] = map[string]any{"message_id": id, "allow_sending_without_reply": true}
		}
	}
	var msg telegramMessage
	err := p.call(ctx, token, "sendMessage", payload, &msg)
	if isTelegramParseError(err) {
		delete(payload, "parse_mode")
		payload["text"] = telegramHTMLToPlain(htmlText)
		err = p.call(ctx, token, "sendMessage", payload, &msg)
	}
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(msg.MessageID, 10), nil
}

// sendChunks posts chunks in order; each one replies to replyTo when set.
// Returns the message id of the first chunk.
func (p *TelegramProvider) sendChunks(ctx context.Context, token, chatID string, topicID int64, replyTo string, chunks []string) (string, error) {
	first := ""
	for i, c := range chunks {
		id, err := p.send(ctx, token, chatID, topicID, replyTo, c)
		if err != nil {
			if i == 0 {
				return "", fmt.Errorf("telegram send message: %w", err)
			}
			return first, fmt.Errorf("telegram send message part %d/%d: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			first = id
		}
	}
	return first, nil
}

// PostMessage sends text as one or more messages; MessageID is the first.
func (p *TelegramProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	token, chatID, topicID, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	id, err := p.sendChunks(ctx, token, chatID, topicID, "", chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// PostThreadReply sends text as reply(s) to parentMessageID. Telegram has no
// Slack-style threads; a reply keeps the conversation linked.
func (p *TelegramProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if parentMessageID == "" {
		return nil, errors.New("telegram: parent message id is required for thread reply")
	}
	token, chatID, topicID, err := p.prepare(channel)
	if err != nil {
		return nil, err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return nil, err
	}
	id, err := p.sendChunks(ctx, token, chatID, topicID, parentMessageID, chunks)
	if id == "" {
		return nil, err
	}
	return &PostedMessage{MessageID: id}, err
}

// UpdateMessage edits messageID with the first chunk; any further chunks are
// posted as replies to it.
func (p *TelegramProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if messageID == "" {
		return errors.New("telegram: message id is required for update")
	}
	id, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid message id %q", messageID)
	}
	token, chatID, topicID, err := p.prepare(channel)
	if err != nil {
		return err
	}
	chunks, err := p.chunks(text)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":               id,
		"text":                     chunks[0],
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	err = p.call(ctx, token, "editMessageText", payload, nil)
	if isTelegramParseError(err) {
		delete(payload, "parse_mode")
		payload["text"] = telegramHTMLToPlain(chunks[0])
		err = p.call(ctx, token, "editMessageText", payload, nil)
	}
	if err != nil && !isTelegramNotModified(err) {
		return fmt.Errorf("telegram edit message: %w", err)
	}
	if len(chunks) > 1 {
		if _, err := p.sendChunks(ctx, token, chatID, topicID, messageID, chunks[1:]); err != nil {
			return err
		}
	}
	return nil
}

// TestCredentials calls getMe and returns the bot's @username.
func (p *TelegramProvider) TestCredentials(ctx context.Context, integration *database.Integration) (string, error) {
	token, err := botTokenOf(integration)
	if err != nil {
		return "", err
	}
	var me struct {
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := p.call(ctx, token, "getMe", nil, &me); err != nil {
		return "", err
	}
	if me.Username != "" {
		return "@" + me.Username, nil
	}
	return me.FirstName, nil
}
