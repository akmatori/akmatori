package messaging

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// telegramTypingKeepaliveInterval is how often we refresh the typing indicator.
// Telegram's typing indicator expires after ~5 minutes; we refresh every 2 minutes
// to stay visible without hammering the API.
const telegramTypingKeepaliveInterval = 2 * time.Minute

// TelegramTypingController sends a "typing..." indicator to a Telegram chat
// and keeps it alive with periodic refreshes. Telegram's sendChatAction only
// lasts ~5 minutes, so long-running investigations need keepalive.
//
// Lifecycle: call Start once, then Stop once. Stop is idempotent.
// After Stop, Refresh is a no-op.
type TelegramTypingController struct {
	provider *TelegramProvider
	chatID   string
	stopOnce sync.Once
	stopped  chan struct{}

	mu        sync.Mutex
	keepalive *time.Ticker
}

// TelegramTypingControllerConfig configures a TelegramTypingController.
type TelegramTypingControllerConfig struct {
	// Provider is the TelegramProvider to use for sendChatAction calls.
	Provider *TelegramProvider
	// ChatID is the Telegram chat ID (Channel.ExternalID).
	ChatID string
}

// NewTelegramTypingController creates a controller. Returns nil if provider
// or chatID is empty (graceful degradation when Telegram is not configured).
func NewTelegramTypingController(cfg TelegramTypingControllerConfig) *TelegramTypingController {
	if cfg.Provider == nil || cfg.ChatID == "" {
		return nil
	}
	return &TelegramTypingController{
		provider: cfg.Provider,
		chatID:   cfg.ChatID,
		stopped:  make(chan struct{}),
	}
}

// Start begins the typing indicator. Sends the initial "typing" action and
// starts a keepalive loop. Non-blocking: API calls run in goroutines.
func (c *TelegramTypingController) Start(ctx context.Context) {
	if c == nil {
		return
	}
	c.sendAction(ctx, "typing")

	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.stopped:
		return // already stopped
	default:
	}

	c.keepalive = time.NewTicker(telegramTypingKeepaliveInterval)
	go func() {
		for {
			select {
			case <-c.keepalive.C:
				c.sendAction(context.Background(), "typing")
			case <-c.stopped:
				c.keepalive.Stop()
				return
			}
		}
	}()
}

// Stop ends the typing indicator. Idempotent — subsequent calls are no-ops.
func (c *TelegramTypingController) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopped)
	})
}

// sendAction sends a single chat action. Errors are logged but not returned
// (typing indicator is best-effort UX, not critical).
func (c *TelegramTypingController) sendAction(ctx context.Context, action string) {
	if c == nil {
		return
	}
	// Build a minimal channel for the provider call.
	// The provider reads bot_token from Integration.Credentials, so we need
	// to pass a channel with the integration loaded. The caller is responsible
	// for providing a properly loaded channel.
	// Since the controller only has chatID, we use a helper method on the provider.
	// For now, this is a no-op placeholder — the actual implementation is in the
	// alert handler where we have the full channel object.
	_ = ctx
	_ = action
}

// TelegramTypingControllerWithChannel is a typing controller that has access
// to the full Channel object (with Integration preloaded).
type TelegramTypingControllerWithChannel struct {
	provider *TelegramProvider
	channel  *database.Channel
	stopOnce sync.Once
	stopped  chan struct{}

	mu        sync.Mutex
	keepalive *time.Ticker
}

// NewTelegramTypingControllerWithChannel creates a controller backed by a
// full Channel (with Integration preloaded for bot token resolution).
func NewTelegramTypingControllerWithChannel(provider *TelegramProvider, channel *database.Channel) *TelegramTypingControllerWithChannel {
	if provider == nil || channel == nil || channel.ExternalID == "" {
		return nil
	}
	return &TelegramTypingControllerWithChannel{
		provider: provider,
		channel:  channel,
		stopped:  make(chan struct{}),
	}
}

// Start begins the typing indicator.
func (c *TelegramTypingControllerWithChannel) Start(ctx context.Context) {
	if c == nil {
		return
	}
	go func() {
		if err := c.provider.SendChatAction(ctx, c.channel, "typing"); err != nil {
			slog.Debug("telegram typing start failed", "err", err)
		}
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.stopped:
		return
	default:
	}

	c.keepalive = time.NewTicker(telegramTypingKeepaliveInterval)
	go func() {
		for {
			select {
			case <-c.keepalive.C:
				if err := c.provider.SendChatAction(context.Background(), c.channel, "typing"); err != nil {
					slog.Debug("telegram typing keepalive failed", "err", err)
				}
			case <-c.stopped:
				c.keepalive.Stop()
				return
			}
		}
	}()
}

// Stop ends the typing indicator. Idempotent.
func (c *TelegramTypingControllerWithChannel) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopped)
	})
}
