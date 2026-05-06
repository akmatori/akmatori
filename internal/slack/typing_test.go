package slack

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type fakeTypingClient struct {
	mu sync.Mutex

	statusCalls    []slack.AssistantThreadsSetStatusParameters
	addReactions   []reactionCall
	removeReacts   []reactionCall
	statusErr      error
	statusErrAfter int // return statusErr only after this many calls (0 = always)
	addReactionErr error
	removeErr      error

	// blockSetStatus, when set, blocks the SetAssistantThreadsStatus call
	// for the given duration on the first call. Used to verify that callers
	// do not deadlock when a Slack API is slow.
	blockSetStatus time.Duration
	blockOnce      sync.Once
}

type reactionCall struct {
	name string
	item slack.ItemRef
}

func (f *fakeTypingClient) SetAssistantThreadsStatusContext(ctx context.Context, params slack.AssistantThreadsSetStatusParameters) error {
	if f.blockSetStatus > 0 {
		f.blockOnce.Do(func() {
			time.Sleep(f.blockSetStatus)
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, params)
	if f.statusErr != nil && len(f.statusCalls) > f.statusErrAfter {
		return f.statusErr
	}
	return nil
}

func (f *fakeTypingClient) AddReaction(name string, item slack.ItemRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addReactions = append(f.addReactions, reactionCall{name: name, item: item})
	return f.addReactionErr
}

func (f *fakeTypingClient) RemoveReaction(name string, item slack.ItemRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeReacts = append(f.removeReacts, reactionCall{name: name, item: item})
	return f.removeErr
}

func (f *fakeTypingClient) snapshot() ([]slack.AssistantThreadsSetStatusParameters, []reactionCall, []reactionCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	statusCopy := append([]slack.AssistantThreadsSetStatusParameters(nil), f.statusCalls...)
	addCopy := append([]reactionCall(nil), f.addReactions...)
	removeCopy := append([]reactionCall(nil), f.removeReacts...)
	return statusCopy, addCopy, removeCopy
}

func newTestController(client TypingClient, opts ...func(*TypingControllerConfig)) *typingController {
	cfg := TypingControllerConfig{
		Client:            client,
		ChannelID:         "C123",
		ThreadTS:          "1700000000.000100",
		ReactionRef:       slack.ItemRef{Channel: "C123", Timestamp: "1700000000.000100"},
		Reaction:          DefaultTypingReaction,
		StatusText:        DefaultTypingStatusText,
		KeepaliveInterval: 20 * time.Millisecond,
		SafetyTTL:         5 * time.Second,
		MaxStatusFailures: 2,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewTypingController(cfg).(*typingController)
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", timeout, msg)
}

func TestTypingController_StartFiresBothSignals(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc)

	c.Start(context.Background())
	defer c.Stop()

	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial setStatus + AddReaction")

	statuses, adds, _ := fc.snapshot()
	if statuses[0].Status != DefaultTypingStatusText {
		t.Fatalf("expected status text %q, got %q", DefaultTypingStatusText, statuses[0].Status)
	}
	if adds[0].name != DefaultTypingReaction {
		t.Fatalf("expected reaction %q, got %q", DefaultTypingReaction, adds[0].name)
	}
}

func TestTypingController_StopClearsBothSignals(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc)

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Stop()

	waitForCondition(t, time.Second, func() bool {
		statuses, _, removes := fc.snapshot()
		// Look for an empty-status clear call AND a RemoveReaction.
		var sawClear bool
		for _, s := range statuses {
			if s.Status == "" {
				sawClear = true
				break
			}
		}
		return sawClear && len(removes) >= 1
	}, "stop clear signals")
}

func TestTypingController_StopIsIdempotent(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc)

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Stop()
	c.Stop()
	c.Stop()

	// Give all goroutines a chance to flush.
	time.Sleep(100 * time.Millisecond)

	statuses, _, removes := fc.snapshot()
	clearCount := 0
	for _, s := range statuses {
		if s.Status == "" {
			clearCount++
		}
	}
	if clearCount != 1 {
		t.Fatalf("expected exactly 1 clear setStatus, got %d", clearCount)
	}
	if len(removes) != 1 {
		t.Fatalf("expected exactly 1 RemoveReaction, got %d", len(removes))
	}
}

func TestTypingController_StopBeforeStartIsNoOp(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc)

	c.Stop() // No Start was ever called.

	time.Sleep(50 * time.Millisecond)
	statuses, adds, removes := fc.snapshot()
	if len(statuses)+len(adds)+len(removes) != 0 {
		t.Fatalf("expected no API calls, got statuses=%d adds=%d removes=%d",
			len(statuses), len(adds), len(removes))
	}
}

func TestTypingController_KeepaliveTicks(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 10 * time.Millisecond
	})

	c.Start(context.Background())
	defer c.Stop()

	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 4
	}, "keepalive ticks fired")

	statuses, _, _ := fc.snapshot()
	for i, s := range statuses {
		if s.Status != DefaultTypingStatusText {
			t.Fatalf("call %d: expected non-empty status, got %q", i, s.Status)
		}
	}
}

func TestTypingController_DoubleStartIsNoOp(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour // freeze keepalive
	})

	c.Start(context.Background())
	c.Start(context.Background())
	c.Start(context.Background())

	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	time.Sleep(50 * time.Millisecond)
	statuses, adds, _ := fc.snapshot()
	// Exactly one initial setStatus and one AddReaction.
	if len(statuses) != 1 {
		t.Fatalf("expected 1 setStatus, got %d", len(statuses))
	}
	if len(adds) != 1 {
		t.Fatalf("expected 1 AddReaction, got %d", len(adds))
	}
	c.Stop()
}

func TestTypingController_CircuitBreakerTripsOnConsecutiveFailures(t *testing.T) {
	fc := &fakeTypingClient{
		statusErr: errors.New("internal_error"),
	}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 5 * time.Millisecond
		cfg.MaxStatusFailures = 2
	})

	c.Start(context.Background())
	defer c.Stop()

	// Wait for circuit breaker to trip.
	waitForCondition(t, time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.statusDisabled
	}, "circuit breaker trips")

	// Snapshot status calls so far, then wait and confirm no further setStatus
	// happens.
	statusesBefore, _, _ := fc.snapshot()
	time.Sleep(80 * time.Millisecond) // many keepalive ticks
	statusesAfter, addsAfter, _ := fc.snapshot()

	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("expected no further setStatus after circuit breaker trip, before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
	// Reaction lifecycle should still be working (AddReaction fired on Start).
	if len(addsAfter) == 0 {
		t.Fatalf("expected reaction to still be managed despite setStatus failures")
	}
}

func TestTypingController_ReactionFailuresDoNotTripStatus(t *testing.T) {
	fc := &fakeTypingClient{
		addReactionErr: errors.New("already_reacted"),
		removeErr:      errors.New("no_reaction"),
	}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 5 * time.Millisecond
	})

	c.Start(context.Background())
	defer c.Stop()

	// Let many keepalive ticks fire.
	time.Sleep(80 * time.Millisecond)

	c.mu.Lock()
	disabled := c.statusDisabled
	c.mu.Unlock()
	if disabled {
		t.Fatal("setStatus circuit should not trip from reaction errors")
	}

	statuses, _, _ := fc.snapshot()
	if len(statuses) < 3 {
		t.Fatalf("expected continued setStatus calls despite reaction errors, got %d", len(statuses))
	}
}

func TestTypingController_PermanentErrorTripsCircuitImmediately(t *testing.T) {
	fc := &fakeTypingClient{
		statusErr: slack.SlackErrorResponse{Err: "feature_not_enabled"},
	}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour // freeze keepalive
		// Set MaxStatusFailures high to prove the trip is from the
		// permanent-error classification, not the consecutive-failure count.
		cfg.MaxStatusFailures = 999
	})

	c.Start(context.Background())
	defer c.Stop()

	waitForCondition(t, time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.statusDisabled
	}, "circuit trips on permanent error after first failure")
}

func TestTypingController_SafetyTTLAutoStops(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.SafetyTTL = 30 * time.Millisecond
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())

	waitForCondition(t, time.Second, func() bool {
		statuses, _, removes := fc.snapshot()
		var sawClear bool
		for _, s := range statuses {
			if s.Status == "" {
				sawClear = true
				break
			}
		}
		return sawClear && len(removes) >= 1
	}, "TTL auto-stop fires cleanup")
}

func TestTypingController_StartDoesNotBlockOnSlowSlackAPI(t *testing.T) {
	fc := &fakeTypingClient{
		blockSetStatus: 500 * time.Millisecond,
	}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	var startReturned atomic.Bool
	go func() {
		c.Start(context.Background())
		startReturned.Store(true)
	}()

	waitForCondition(t, 100*time.Millisecond, func() bool {
		return startReturned.Load()
	}, "Start returned despite slow Slack API")

	c.Stop()
	// Wait for the in-flight blocked call to drain so the test doesn't leak.
	time.Sleep(600 * time.Millisecond)
}

func TestTypingController_NilClientIsNoOp(t *testing.T) {
	c := NewTypingController(TypingControllerConfig{
		Client:            nil,
		ChannelID:         "C123",
		ThreadTS:          "1700000000.000100",
		ReactionRef:       slack.ItemRef{Channel: "C123", Timestamp: "1700000000.000100"},
		Reaction:          DefaultTypingReaction,
		StatusText:        DefaultTypingStatusText,
		KeepaliveInterval: 1 * time.Hour,
	})

	// Should not panic.
	c.Start(context.Background())
	c.Stop()
}

func TestTypingController_DefaultsApplied(t *testing.T) {
	fc := &fakeTypingClient{}
	cfg := TypingControllerConfig{
		Client:      fc,
		ChannelID:   "C123",
		ThreadTS:    "1700000000.000100",
		ReactionRef: slack.ItemRef{Channel: "C123", Timestamp: "1700000000.000100"},
	}
	c := NewTypingController(cfg).(*typingController)

	if c.cfg.Reaction != DefaultTypingReaction {
		t.Fatalf("expected default reaction, got %q", c.cfg.Reaction)
	}
	if c.cfg.StatusText != DefaultTypingStatusText {
		t.Fatalf("expected default status text, got %q", c.cfg.StatusText)
	}
	if c.cfg.KeepaliveInterval != DefaultTypingKeepaliveInterval {
		t.Fatalf("expected default keepalive, got %s", c.cfg.KeepaliveInterval)
	}
	if c.cfg.SafetyTTL != DefaultTypingSafetyTTL {
		t.Fatalf("expected default TTL, got %s", c.cfg.SafetyTTL)
	}
	if c.cfg.MaxStatusFailures != DefaultTypingMaxStatusFailures {
		t.Fatalf("expected default max failures, got %d", c.cfg.MaxStatusFailures)
	}
}
