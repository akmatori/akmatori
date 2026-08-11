package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// telegramAPIBase is the base URL for the Telegram Bot API.
// Exported as a var (not const) so tests can override it with a mock server.
var telegramAPIBase = "https://api.telegram.org"

// telegramRequestTimeout is the default timeout for Telegram API calls.
const telegramRequestTimeout = 15 * time.Second

// TelegramProvider is the Provider implementation backed by the Telegram Bot API.
// It reads the bot token from Integration.Credentials["bot_token"] at call time
// and the chat ID from Channel.ExternalID. No long-lived connection is needed —
// each method makes a plain HTTP POST to api.telegram.org.
type TelegramProvider struct {
	// httpClient is the HTTP client used for API calls.
	// Defaults to http.DefaultClient if nil (tests can override).
	httpClient *http.Client
}

// NewTelegramProvider returns a provider ready for use. The bot token is
// resolved per-call from the Channel's parent Integration credentials, so
// credential changes take effect immediately without re-registration.
func NewTelegramProvider() *TelegramProvider {
	return &TelegramProvider{
		httpClient: &http.Client{Timeout: telegramRequestTimeout},
	}
}

// newTelegramProviderWithClient is used in tests to inject a custom HTTP client.
func newTelegramProviderWithClient(client *http.Client) *TelegramProvider {
	return &TelegramProvider{httpClient: client}
}

// Name reports the canonical provider id used in Integration.Provider rows.
func (p *TelegramProvider) Name() database.MessagingProvider {
	return database.MessagingProviderTelegram
}

// telegramBotToken extracts the bot token from the Integration credentials.
func telegramBotToken(channel *database.Channel) (string, error) {
	if channel == nil {
		return "", fmt.Errorf("telegram: channel is nil")
	}
	if channel.Integration.ID == 0 {
		return "", fmt.Errorf("telegram: channel %q has no parent integration", channel.DisplayName)
	}
	token, ok := channel.Integration.Credentials["bot_token"].(string)
	if !ok || token == "" {
		return "", fmt.Errorf("telegram: integration %q has no bot_token configured", channel.Integration.Name)
	}
	return token, nil
}

// validateTelegramChannel checks that the channel has a chat ID and an
// integration with a bot token.
func validateTelegramChannel(channel *database.Channel) error {
	if channel == nil {
		return fmt.Errorf("telegram: channel is nil")
	}
	if channel.ExternalID == "" {
		return fmt.Errorf("telegram: channel %q has no external_id (chat ID)", channel.DisplayName)
	}
	if _, err := telegramBotToken(channel); err != nil {
		return err
	}
	return nil
}

// telegramAPIEndpoint builds the full URL for a Telegram Bot API method.
func telegramAPIEndpoint(token, method string) string {
	return fmt.Sprintf("%s/bot%s/%s", telegramAPIBase, token, method)
}

// telegramResponse is the common envelope returned by all Telegram Bot API methods.
type telegramResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// telegramMessage is the "Message" object returned by sendMessage.
type telegramMessage struct {
	MessageID int `json:"message_id"`
}

// telegramSendMessageRequest is the payload for the sendMessage method.
type telegramSendMessageRequest struct {
	ChatID                any    `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode,omitempty"`
	ReplyToMessageID      int    `json:"reply_to_message_id,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// telegramSendChatActionRequest is the payload for the sendChatAction method.
type telegramSendChatActionRequest struct {
	ChatID string `json:"chat_id"`
	Action string `json:"action"`
}

// telegramEditMessageRequest is the payload for the editMessageText method.
type telegramEditMessageRequest struct {
	ChatID    any    `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// postJSON sends a JSON POST to the Telegram Bot API and returns the parsed
// response. Returns ErrNotImplemented if the response indicates a permanent
// API error (e.g. wrong token), and wraps transient errors.
func (p *TelegramProvider) postJSON(ctx context.Context, url string, body any) (*telegramResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("telegram: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("telegram: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("telegram: read response: %w", err)
	}

	var tr telegramResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("telegram: parse response: %w (%s)", err, strings.TrimSpace(string(respBody)))
	}

	if !tr.OK {
		slog.Warn("telegram API error", "description", tr.Description, "url", url)
		return nil, fmt.Errorf("telegram API error: %s", tr.Description)
	}

	return &tr, nil
}

// PostMessage posts text to the Telegram chat identified by Channel.ExternalID
// (the numeric chat ID, e.g. -1001234567890). Text is sent as MarkdownV2.
func (p *TelegramProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	if err := validateTelegramChannel(channel); err != nil {
		return nil, err
	}

	token, _ := telegramBotToken(channel)
	chatID := channel.ExternalID

	// Telegram chat IDs are numeric (possibly with a leading dash).
	// The API accepts them as either string or number; we send as string
	// to avoid JSON number precision issues with large IDs.
	req := telegramSendMessageRequest{
		ChatID:                chatID,
		Text:                  text,
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
	}

	tr, err := p.postJSON(ctx, telegramAPIEndpoint(token, "sendMessage"), req)
	if err != nil {
		return nil, fmt.Errorf("telegram PostMessage: %w", err)
	}

	var msg telegramMessage
	if err := json.Unmarshal(tr.Result, &msg); err != nil {
		return nil, fmt.Errorf("telegram PostMessage: parse result: %w", err)
	}

	return &PostedMessage{MessageID: strconv.Itoa(msg.MessageID)}, nil
}

// PostThreadReply posts text as a reply to the message identified by
// parentMessageID. Telegram doesn't have Slack-style threads, so this
// creates a regular message with reply_to_message_id set.
func (p *TelegramProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if err := validateTelegramChannel(channel); err != nil {
		return nil, err
	}
	if parentMessageID == "" {
		return nil, fmt.Errorf("telegram: parent message id is required for reply")
	}

	replyToID, err := strconv.Atoi(parentMessageID)
	if err != nil {
		return nil, fmt.Errorf("telegram: invalid parent message id %q: %w", parentMessageID, err)
	}

	token, _ := telegramBotToken(channel)

	req := telegramSendMessageRequest{
		ChatID:                channel.ExternalID,
		Text:                  text,
		ParseMode:             "MarkdownV2",
		ReplyToMessageID:      replyToID,
		DisableWebPagePreview: true,
	}

	tr, err := p.postJSON(ctx, telegramAPIEndpoint(token, "sendMessage"), req)
	if err != nil {
		return nil, fmt.Errorf("telegram PostThreadReply: %w", err)
	}

	var msg telegramMessage
	if err := json.Unmarshal(tr.Result, &msg); err != nil {
		return nil, fmt.Errorf("telegram PostThreadReply: parse result: %w", err)
	}

	return &PostedMessage{MessageID: strconv.Itoa(msg.MessageID)}, nil
}

// UpdateMessage rewrites an existing message identified by messageID.
func (p *TelegramProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if err := validateTelegramChannel(channel); err != nil {
		return err
	}
	if messageID == "" {
		return fmt.Errorf("telegram: message id is required for update")
	}

	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("telegram: invalid message id %q: %w", messageID, err)
	}

	token, _ := telegramBotToken(channel)

	req := telegramEditMessageRequest{
		ChatID:    channel.ExternalID,
		MessageID: msgID,
		Text:      text,
		ParseMode: "MarkdownV2",
	}

	_, err = p.postJSON(ctx, telegramAPIEndpoint(token, "editMessageText"), req)
	if err != nil {
		return fmt.Errorf("telegram UpdateMessage: %w", err)
	}

	return nil
}

// SendChatAction sends a typing indicator to the chat. Telegram's typing
// indicator lasts ~5 minutes; callers should refresh periodically for
// long-running operations.
func (p *TelegramProvider) SendChatAction(ctx context.Context, channel *database.Channel, action string) error {
	if err := validateTelegramChannel(channel); err != nil {
		return err
	}

	token, _ := telegramBotToken(channel)

	req := telegramSendChatActionRequest{
		ChatID: channel.ExternalID,
		Action: action,
	}

	_, err := p.postJSON(ctx, telegramAPIEndpoint(token, "sendChatAction"), req)
	if err != nil {
		return fmt.Errorf("telegram SendChatAction: %w", err)
	}

	return nil
}
