package slack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const (
	DefaultTypingStatusText        = "is investigating..."
	DefaultTypingReaction          = "hourglass_flowing_sand"
	DefaultTypingKeepaliveInterval = 30 * time.Second
	DefaultTypingSafetyTTL         = 60 * time.Minute
	DefaultTypingMaxStatusFailures = 2
)

// TypingClient is the subset of the Slack client API used by TypingController.
// Defined as an interface so unit tests can supply a fake without standing up
// a real *slack.Client.
type TypingClient interface {
	SetAssistantThreadsStatusContext(ctx context.Context, params slack.AssistantThreadsSetStatusParameters) error
	AddReaction(name string, item slack.ItemRef) error
	RemoveReaction(name string, item slack.ItemRef) error
}

// TypingControllerConfig configures a single TypingController instance bound
// to one (channel, threadTS, reaction-target) tuple. A controller is meant to
// live for one agent run.
type TypingControllerConfig struct {
	Client      TypingClient
	ChannelID   string
	ThreadTS    string
	ReactionRef slack.ItemRef
	Reaction    string
	StatusText  string

	KeepaliveInterval time.Duration
	SafetyTTL         time.Duration
	MaxStatusFailures int
}

// TypingController shows a "is investigating..." indicator in Slack while an
// agent run is active. It signals two things in parallel:
//
//   - assistant.threads.setStatus banner in the thread header (best-effort;
//     silently no-ops when the Slack app does not have the AI Assistant
//     feature enabled in its manifest).
//   - A reaction emoji on the triggering message, added on Start and removed
//     on Stop.
//
// Lifecycle: call Start once, then Stop once. Both are non-blocking; Slack
// HTTP calls run in goroutines so a slow API never blocks the caller. Stop is
// idempotent.
type TypingController interface {
	Start(ctx context.Context)
	Stop()
}

// NewTypingController constructs a controller. Defaults are filled in for
// any zero-valued config field.
func NewTypingController(cfg TypingControllerConfig) TypingController {
	if cfg.Reaction == "" {
		cfg.Reaction = DefaultTypingReaction
	}
	if cfg.StatusText == "" {
		cfg.StatusText = DefaultTypingStatusText
	}
	if cfg.KeepaliveInterval <= 0 {
		cfg.KeepaliveInterval = DefaultTypingKeepaliveInterval
	}
	if cfg.SafetyTTL <= 0 {
		cfg.SafetyTTL = DefaultTypingSafetyTTL
	}
	if cfg.MaxStatusFailures <= 0 {
		cfg.MaxStatusFailures = DefaultTypingMaxStatusFailures
	}
	return &typingController{cfg: cfg}
}

type typingController struct {
	cfg TypingControllerConfig

	mu             sync.Mutex
	started        bool
	statusFailures int
	statusDisabled bool

	stopOnce sync.Once
	done     chan struct{}
	ttlTimer *time.Timer
}

func (t *typingController) Start(ctx context.Context) {
	if t == nil || t.cfg.Client == nil {
		return
	}

	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return
	}
	t.started = true
	t.done = make(chan struct{})
	doneCh := t.done
	t.mu.Unlock()

	// Fire the initial signals in a goroutine so a slow Slack API never
	// blocks the caller (typically a Slack event handler kicking off an
	// agent run).
	go func() {
		t.fireSetStatus(ctx, t.cfg.StatusText)
		t.fireAddReaction()
	}()

	go t.keepaliveLoop(ctx, doneCh)

	t.mu.Lock()
	t.ttlTimer = time.AfterFunc(t.cfg.SafetyTTL, func() {
		slog.Warn("typing controller safety TTL expired, auto-stopping",
			"channel", t.cfg.ChannelID, "thread_ts", t.cfg.ThreadTS,
			"ttl", t.cfg.SafetyTTL)
		t.Stop()
	})
	t.mu.Unlock()
}

func (t *typingController) Stop() {
	if t == nil || t.cfg.Client == nil {
		return
	}

	t.stopOnce.Do(func() {
		t.mu.Lock()
		// If Stop is called before Start, there's nothing to clean up.
		if !t.started {
			t.mu.Unlock()
			return
		}
		doneCh := t.done
		ttlTimer := t.ttlTimer
		t.ttlTimer = nil
		t.mu.Unlock()

		if doneCh != nil {
			close(doneCh)
		}
		if ttlTimer != nil {
			ttlTimer.Stop()
		}

		// Cleanup HTTP calls happen in a goroutine for the same reason as
		// Start: Stop must not block the caller. Use a fresh background
		// context so cleanup is not cancelled by a parent context that is
		// often already cancelled by the time Stop runs (e.g. agent run
		// finished, request context tearing down).
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			t.fireSetStatus(ctx, "")
			t.fireRemoveReaction()
		}()
	})
}

// keepaliveLoop re-issues the setStatus call every KeepaliveInterval until
// done is closed. The reaction does not need keepalive — once added it
// persists in Slack until removed. The setStatus banner also persists, but
// keepalive guards against transient Slack-side state loss across reconnects
// or app reinstalls.
func (t *typingController) keepaliveLoop(ctx context.Context, done <-chan struct{}) {
	ticker := time.NewTicker(t.cfg.KeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			t.mu.Lock()
			disabled := t.statusDisabled
			t.mu.Unlock()
			if disabled {
				continue
			}
			t.fireSetStatus(ctx, t.cfg.StatusText)
		}
	}
}

// fireSetStatus issues a single setStatus call. Errors increment the
// consecutive-failure counter; on success the counter resets. When the
// counter crosses MaxStatusFailures the circuit breaker trips and future
// setStatus calls are skipped (the reaction continues working independently).
//
// The ChannelID-or-ThreadTS guard is mostly defensive: setStatus requires
// both, and we don't want to spam errors when one was never available.
func (t *typingController) fireSetStatus(ctx context.Context, status string) {
	if t.cfg.ChannelID == "" || t.cfg.ThreadTS == "" {
		return
	}
	err := t.cfg.Client.SetAssistantThreadsStatusContext(ctx, slack.AssistantThreadsSetStatusParameters{
		ChannelID: t.cfg.ChannelID,
		ThreadTS:  t.cfg.ThreadTS,
		Status:    status,
	})

	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.statusFailures = 0
		return
	}
	if t.statusDisabled {
		return
	}
	t.statusFailures++
	if isPermanentSetStatusError(err) || t.statusFailures >= t.cfg.MaxStatusFailures {
		t.statusDisabled = true
		slog.Warn("typing controller disabling setStatus path",
			"channel", t.cfg.ChannelID, "thread_ts", t.cfg.ThreadTS,
			"failures", t.statusFailures, "err", err)
		return
	}
	slog.Debug("typing controller setStatus error",
		"channel", t.cfg.ChannelID, "thread_ts", t.cfg.ThreadTS,
		"failures", t.statusFailures, "err", err)
}

func (t *typingController) fireAddReaction() {
	if t.cfg.Reaction == "" || t.cfg.ReactionRef.Channel == "" || t.cfg.ReactionRef.Timestamp == "" {
		return
	}
	if err := t.cfg.Client.AddReaction(t.cfg.Reaction, t.cfg.ReactionRef); err != nil {
		// already_reacted is a no-op success for our purposes — typing was
		// already showing. Anything else is logged at debug; reactions are
		// best-effort UX, not load-bearing.
		slog.Debug("typing controller add reaction error",
			"channel", t.cfg.ReactionRef.Channel, "ts", t.cfg.ReactionRef.Timestamp,
			"reaction", t.cfg.Reaction, "err", err)
	}
}

func (t *typingController) fireRemoveReaction() {
	if t.cfg.Reaction == "" || t.cfg.ReactionRef.Channel == "" || t.cfg.ReactionRef.Timestamp == "" {
		return
	}
	if err := t.cfg.Client.RemoveReaction(t.cfg.Reaction, t.cfg.ReactionRef); err != nil {
		slog.Debug("typing controller remove reaction error",
			"channel", t.cfg.ReactionRef.Channel, "ts", t.cfg.ReactionRef.Timestamp,
			"reaction", t.cfg.Reaction, "err", err)
	}
}

// isPermanentSetStatusError trips the circuit breaker on the first failure
// for errors we know cannot succeed by retrying — e.g. the Slack app does
// not have the AI Assistant feature enabled, the bot token type is wrong,
// or the channel/thread is invalid. Other errors fall through to the
// consecutive-failure counter.
func isPermanentSetStatusError(err error) bool {
	if err == nil {
		return false
	}
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		switch slackErr.Err {
		case "feature_not_enabled",
			"not_allowed_token_type",
			"invalid_arguments",
			"channel_not_found",
			"thread_not_found":
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "feature_not_enabled") ||
		strings.Contains(msg, "not_allowed_token_type")
}
