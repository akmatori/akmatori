package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/output"
	"github.com/slack-go/slack"
)

// SlackMaxTextBytes is the byte budget the Slack provider enforces on every
// outbound message. chat.postMessage accepts ~40,000 chars; Akmatori keeps
// messages at 8000 bytes so they stay readable in a thread (same cap as the
// alert flow's slackMaxTextBytes).
const SlackMaxTextBytes = 8000

// RenderSlack converts provider-neutral Markdown into Slack mrkdwn and
// enforces SlackMaxTextBytes. Already-mrkdwn input passes through unchanged
// apart from the byte cap (MarkdownToSlack is idempotent on mrkdwn).
func RenderSlack(markdown string) string {
	return output.TruncateSlackBytes(output.MarkdownToSlack(markdown), SlackMaxTextBytes)
}

// SlackClient is the subset of *slack.Client the SlackProvider depends on.
// Pulling it out as an interface lets tests substitute a fake without
// constructing a real Slack HTTP client (and lets the production provider
// receive a live *slack.Client unchanged).
type SlackClient interface {
	PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
	UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
}

// SlackClientProvider wraps the live slack manager so the provider can read
// the current client at call time. The manager swaps clients on credential
// reloads, so the provider must NOT cache a *slack.Client snapshot.
type SlackClientProvider interface {
	GetClient() *slack.Client
}

// SlackProvider is the Provider implementation backed by an existing slack-go
// client. It is a thin wrapper: routing decisions (which channel, which
// integration) happen upstream in ChannelService, leaving this provider with
// just the transport responsibility.
type SlackProvider struct {
	clientFn func() SlackClient
	// apiURL overrides the Slack Web API base URL for TestCredentials
	// (tests only; empty = slack-go default).
	apiURL string
}

// NewSlackProvider builds a slack provider that resolves its underlying
// client via the supplied manager (typically *slack.Manager) at every call.
// This keeps the provider hot-reload safe: when the slack manager swaps
// clients (credential change), the next post sees the new client.
func NewSlackProvider(manager SlackClientProvider) *SlackProvider {
	return &SlackProvider{
		clientFn: func() SlackClient {
			if manager == nil {
				return nil
			}
			c := manager.GetClient()
			if c == nil {
				return nil
			}
			// *slack.Client satisfies SlackClient via the methods listed
			// in the interface; the cast keeps the test seam in place.
			return slackClientShim{c}
		},
	}
}

// newSlackProviderFromClient is used in tests to inject a fake SlackClient
// directly. Kept package-private so production callers always go through the
// manager-based constructor.
func newSlackProviderFromClient(c SlackClient) *SlackProvider {
	return &SlackProvider{
		clientFn: func() SlackClient { return c },
	}
}

// slackClientShim adapts a live *slack.Client to the SlackClient test seam.
type slackClientShim struct {
	c *slack.Client
}

func (s slackClientShim) PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
	return s.c.PostMessageContext(ctx, channelID, options...)
}

func (s slackClientShim) UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	return s.c.UpdateMessageContext(ctx, channelID, timestamp, options...)
}

// Name reports the canonical provider id used in Integration.Provider rows.
func (p *SlackProvider) Name() database.MessagingProvider { return database.MessagingProviderSlack }

// errSlackClientUnavailable is returned when the slack manager has no live
// client (Slack disabled, credentials missing, or socket-mode loop not yet
// running). Callers degrade to provider-absent behaviour.
var errSlackClientUnavailable = errors.New("slack client is not available")

func (p *SlackProvider) client() (SlackClient, error) {
	if p.clientFn == nil {
		return nil, errSlackClientUnavailable
	}
	c := p.clientFn()
	if c == nil {
		return nil, errSlackClientUnavailable
	}
	return c, nil
}

func validateSlackChannel(channel *database.Channel) error {
	if channel == nil {
		return fmt.Errorf("slack: channel is nil")
	}
	if channel.ExternalID == "" {
		return fmt.Errorf("slack: channel %q has no external_id", channel.DisplayName)
	}
	return nil
}

// PostMessage posts text to the slack channel identified by Channel.ExternalID
// (which the operator entered as a channel ID like C012345; resolution from
// names lives elsewhere in the Slack manager). text is provider-neutral
// Markdown; it is rendered to mrkdwn and byte-capped here.
func (p *SlackProvider) PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error) {
	if err := validateSlackChannel(channel); err != nil {
		return nil, err
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	_, ts, err := c.PostMessageContext(ctx, channel.ExternalID, slack.MsgOptionText(RenderSlack(text), false))
	if err != nil {
		return nil, fmt.Errorf("slack post message: %w", err)
	}
	return &PostedMessage{MessageID: ts}, nil
}

// PostThreadReply posts text as a thread reply under parentMessageID (a Slack
// message ts). The thread root must already exist; callers are responsible
// for stashing the ts returned by PostMessage on creation.
func (p *SlackProvider) PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error) {
	if err := validateSlackChannel(channel); err != nil {
		return nil, err
	}
	if parentMessageID == "" {
		return nil, fmt.Errorf("slack: parent message id is required for thread reply")
	}
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	_, ts, err := c.PostMessageContext(
		ctx,
		channel.ExternalID,
		slack.MsgOptionText(RenderSlack(text), false),
		slack.MsgOptionTS(parentMessageID),
	)
	if err != nil {
		return nil, fmt.Errorf("slack post thread reply: %w", err)
	}
	return &PostedMessage{MessageID: ts}, nil
}

// UpdateMessage rewrites an existing message identified by messageID.
func (p *SlackProvider) UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error {
	if err := validateSlackChannel(channel); err != nil {
		return err
	}
	if messageID == "" {
		return fmt.Errorf("slack: message id is required for update")
	}
	c, err := p.client()
	if err != nil {
		return err
	}
	if _, _, _, err := c.UpdateMessageContext(ctx, channel.ExternalID, messageID, slack.MsgOptionText(RenderSlack(text), false)); err != nil {
		return fmt.Errorf("slack update message: %w", err)
	}
	return nil
}

// TestCredentials calls auth.test with the integration's bot_token and
// returns "<bot user> @ <workspace>".
func (p *SlackProvider) TestCredentials(ctx context.Context, integration *database.Integration) (string, error) {
	token, err := botTokenOf(integration)
	if err != nil {
		return "", err
	}
	opts := []slack.Option{slack.OptionHTTPClient(defaultBotHTTPClient())}
	if p.apiURL != "" {
		opts = append(opts, slack.OptionAPIURL(p.apiURL))
	}
	resp, err := slack.New(token, opts...).AuthTestContext(ctx)
	if err != nil {
		return "", fmt.Errorf("slack auth.test: %w", err)
	}
	if resp.Team != "" {
		return resp.User + " @ " + resp.Team, nil
	}
	return resp.User, nil
}
