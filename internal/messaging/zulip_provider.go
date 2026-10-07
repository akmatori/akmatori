package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
)

const zulipDefaultTopic = "Akmatori"

// ZulipProvider sends messages through Zulip's REST API. A channel's
// ExternalID is the target stream name; credentials live on its Integration.
type ZulipProvider struct {
	client *http.Client
}

// NewZulipProvider constructs the provider with the default HTTP client.
func NewZulipProvider() *ZulipProvider {
	return &ZulipProvider{client: http.DefaultClient}
}

func (ZulipProvider) Name() database.MessagingProvider { return database.MessagingProviderZulip }

func (p *ZulipProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	client, err := p.clientFor(channel)
	if err != nil {
		return nil, err
	}
	return client.postMessage(ctx, channel.ExternalID, zulipTopic(channel), text)
}

// PostThreadReply retrieves the root message's stream and topic, then posts a
// new message to that Zulip topic. Zulip threads are identified by a stream and
// topic rather than a parent message ID.
func (p *ZulipProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if strings.TrimSpace(parentMessageID) == "" {
		return nil, fmt.Errorf("zulip: parent message id is required for thread reply")
	}
	client, err := p.clientFor(channel)
	if err != nil {
		return nil, err
	}
	stream, topic, err := client.messageThread(ctx, parentMessageID)
	if err != nil {
		return nil, err
	}
	return client.postMessage(ctx, stream, topic, text)
}

func (p *ZulipProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("zulip: message id is required for update")
	}
	client, err := p.clientFor(channel)
	if err != nil {
		return err
	}
	return client.updateMessage(ctx, messageID, text)
}

func (p *ZulipProvider) clientFor(channel *database.Channel) (*zulipClient, error) {
	if channel == nil {
		return nil, fmt.Errorf("zulip: channel is nil")
	}
	if strings.TrimSpace(channel.ExternalID) == "" {
		return nil, fmt.Errorf("zulip: channel %q has no external_id", channel.DisplayName)
	}
	credentials := channel.Integration.Credentials
	siteURL := credentialString(credentials, "site_url")
	email := credentialString(credentials, "email")
	apiKey := credentialString(credentials, "api_key")
	if siteURL == "" || email == "" || apiKey == "" {
		return nil, fmt.Errorf("zulip: site_url, email, and api_key credentials are required")
	}
	parsed, err := url.Parse(siteURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("zulip: invalid site_url %q", siteURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &zulipClient{httpClient: p.client, siteURL: parsed.String(), email: email, apiKey: apiKey}, nil
}

func credentialString(credentials database.JSONB, key string) string {
	value, _ := credentials[key].(string)
	return strings.TrimSpace(value)
}

func zulipTopic(channel *database.Channel) string {
	if topic := credentialString(channel.Integration.Credentials, "topic"); topic != "" {
		return topic
	}
	return zulipDefaultTopic
}

type zulipClient struct {
	httpClient *http.Client
	siteURL    string
	email      string
	apiKey     string
}

type zulipResponse struct {
	Result  string          `json:"result"`
	Msg     string          `json:"msg"`
	ID      json.RawMessage `json:"id"`
	Stream  string          `json:"stream"`
	Topic   string          `json:"topic"`
	Subject string          `json:"subject"`
}

func (c *zulipClient) postMessage(ctx context.Context, stream, topic, content string) (*PostedMessage, error) {
	values := url.Values{"type": {"stream"}, "to": {stream}, "topic": {topic}, "content": {content}}
	response, err := c.request(ctx, http.MethodPost, "/api/v1/messages", values)
	if err != nil {
		return nil, fmt.Errorf("zulip post message: %w", err)
	}
	if len(response.ID) == 0 {
		return nil, fmt.Errorf("zulip post message: response has no message id")
	}
	var id interface{}
	if err := json.Unmarshal(response.ID, &id); err != nil {
		return nil, fmt.Errorf("zulip post message: decode message id: %w", err)
	}
	return &PostedMessage{MessageID: fmt.Sprint(id)}, nil
}

func (c *zulipClient) messageThread(ctx context.Context, messageID string) (string, string, error) {
	response, err := c.request(ctx, http.MethodGet, "/api/v1/messages/"+url.PathEscape(messageID), nil)
	if err != nil {
		return "", "", fmt.Errorf("zulip get message %s: %w", messageID, err)
	}
	topic := response.Topic
	if topic == "" {
		topic = response.Subject
	}
	if response.Stream == "" || topic == "" {
		return "", "", fmt.Errorf("zulip get message %s: response has no stream or topic", messageID)
	}
	return response.Stream, topic, nil
}

func (c *zulipClient) updateMessage(ctx context.Context, messageID, content string) error {
	_, err := c.request(ctx, http.MethodPatch, "/api/v1/messages/"+url.PathEscape(messageID), url.Values{"content": {content}})
	if err != nil {
		return fmt.Errorf("zulip update message: %w", err)
	}
	return nil
}

func (c *zulipClient) request(ctx context.Context, method, path string, values url.Values) (*zulipResponse, error) {
	endpoint := c.siteURL + path
	var body *strings.Reader
	if method == http.MethodGet {
		if len(values) > 0 {
			endpoint += "?" + values.Encode()
		}
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.SetBasicAuth(c.email, c.apiKey)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload zulipResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || payload.Result != "success" {
		message := payload.Msg
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("API error: %s", message)
	}
	return &payload, nil
}
