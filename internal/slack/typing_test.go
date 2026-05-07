package slack

import (
	"context"
	"errors"
	"strings"
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

func TestTypingController_DiscardSkipsSlackCleanup(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Discard()
	// The deferred Stop pattern: callers Discard then later Stop via defer.
	// The shared stopOnce must guarantee Stop becomes a no-op.
	c.Stop()

	time.Sleep(80 * time.Millisecond)

	statuses, _, removes := fc.snapshot()
	for _, s := range statuses {
		if s.Status == "" {
			t.Fatalf("Discard must not issue setStatus(\"\"); the replacement controller owns the thread state")
		}
	}
	if len(removes) != 0 {
		t.Fatalf("Discard must not RemoveReaction; the replacement controller owns the reaction. got %d", len(removes))
	}
}

func TestTypingController_DiscardStopsKeepalive(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 5 * time.Millisecond
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 2
	}, "keepalive ticks before discard")

	c.Discard()

	statusesBefore, _, _ := fc.snapshot()
	time.Sleep(60 * time.Millisecond)
	statusesAfter, _, _ := fc.snapshot()

	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("expected no further setStatus after Discard, before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
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

func TestTypingController_UpdateLoadingMessageBeforeStartIsNoOp(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc)

	c.UpdateLoadingMessage("🤔 thinking about something")

	time.Sleep(50 * time.Millisecond)
	statuses, _, _ := fc.snapshot()
	if len(statuses) != 0 {
		t.Fatalf("expected no setStatus calls before Start, got %d", len(statuses))
	}
}

func TestTypingController_UpdateLoadingMessagePushesFreshSetStatus(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour // freeze keepalive so we can count cleanly
	})

	c.Start(context.Background())
	defer c.Stop()
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.UpdateLoadingMessage("🤔 reasoning step one")

	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		for _, s := range statuses {
			if len(s.LoadingMessages) == 1 && s.LoadingMessages[0] == "reasoning step one" {
				return true
			}
		}
		return false
	}, "setStatus with the loading message arrived")
}

func TestTypingController_UpdateLoadingMessageDedupesConsecutive(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	defer c.Stop()
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 1
	}, "initial setStatus")

	c.UpdateLoadingMessage("🤔 same line")
	c.UpdateLoadingMessage("🤔 same line")
	c.UpdateLoadingMessage("🤔 same line")

	time.Sleep(80 * time.Millisecond)

	statuses, _, _ := fc.snapshot()
	withLine := 0
	for _, s := range statuses {
		if len(s.LoadingMessages) == 1 && s.LoadingMessages[0] == "same line" {
			withLine++
		}
	}
	if withLine != 1 {
		t.Fatalf("expected exactly 1 setStatus carrying the deduped line, got %d", withLine)
	}
}

func TestTypingController_UpdateLoadingMessageEmptyIsDropped(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	defer c.Stop()
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 1
	}, "initial setStatus")

	statusesBefore, _, _ := fc.snapshot()
	c.UpdateLoadingMessage("")
	c.UpdateLoadingMessage("   ")
	c.UpdateLoadingMessage("\n\t  \n")
	time.Sleep(60 * time.Millisecond)

	statusesAfter, _, _ := fc.snapshot()
	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("expected no further setStatus calls for empty/whitespace input, before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
}

func TestTypingController_UpdateLoadingMessageSkippedWhenCircuitBreakerTripped(t *testing.T) {
	fc := &fakeTypingClient{
		statusErr: slack.SlackErrorResponse{Err: "feature_not_enabled"},
	}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	defer c.Stop()
	waitForCondition(t, time.Second, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.statusDisabled
	}, "circuit breaker trips on permanent error")

	statusesBefore, _, _ := fc.snapshot()
	c.UpdateLoadingMessage("🤔 should be skipped")
	time.Sleep(60 * time.Millisecond)

	statusesAfter, _, _ := fc.snapshot()
	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("expected no setStatus after circuit trip, before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
}

func TestTypingController_KeepaliveIncludesLatestLoadingMessage(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 15 * time.Millisecond
	})

	c.Start(context.Background())
	defer c.Stop()

	c.UpdateLoadingMessage("🤔 latest thought")

	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		count := 0
		for _, s := range statuses {
			if len(s.LoadingMessages) == 1 && s.LoadingMessages[0] == "latest thought" {
				count++
			}
		}
		// At least 2 setStatus calls carry the loading message: the
		// event-driven push from UpdateLoadingMessage and one or more
		// keepalive ticks.
		return count >= 2
	}, "keepalive ticks pick up latestLoadingMessage")
}

func TestTypingController_StopClearSendsNoLoadingMessages(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 1
	}, "initial setStatus")

	c.UpdateLoadingMessage("🤔 mid-flight")
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		for _, s := range statuses {
			if len(s.LoadingMessages) == 1 {
				return true
			}
		}
		return false
	}, "loading message pushed")

	c.Stop()

	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		for _, s := range statuses {
			if s.Status == "" {
				if len(s.LoadingMessages) != 0 {
					return false // wrong: clear should carry no loading messages
				}
				return true
			}
		}
		return false
	}, "stop clear sends empty status without loading messages")
}

// TestTypingController_UpdateLoadingMessageNoOpAfterDiscard verifies that a
// late progress flush after Discard cannot push a stale reasoning line on
// top of the replacement run's banner. Without this, the displaced run's
// progressStreamer.Flush() (called BEFORE the superseded check in
// slack_processor.go / alert_processor.go) would issue one last setStatus
// against the shared thread, overwriting the new run's current thought.
func TestTypingController_UpdateLoadingMessageNoOpAfterDiscard(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Discard()

	statusesBefore, _, _ := fc.snapshot()
	c.UpdateLoadingMessage("🤔 stale flush from displaced run")
	time.Sleep(80 * time.Millisecond)

	statusesAfter, _, _ := fc.snapshot()
	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("UpdateLoadingMessage must be inert after Discard; before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
}

// TestTypingController_UpdateLoadingMessageNoOpAfterStop verifies the same
// inert-after-teardown contract for Stop: any UpdateLoadingMessage call
// arriving after Stop has tripped is silently dropped instead of issuing a
// new setStatus that would race the in-flight clear call.
func TestTypingController_UpdateLoadingMessageNoOpAfterStop(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Stop()
	// Drain the cleanup goroutine so Stop's clear setStatus has been recorded.
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		for _, s := range statuses {
			if s.Status == "" {
				return true
			}
		}
		return false
	}, "stop clear setStatus recorded")

	statusesBefore, _, _ := fc.snapshot()
	c.UpdateLoadingMessage("🤔 too late")
	time.Sleep(80 * time.Millisecond)

	statusesAfter, _, _ := fc.snapshot()
	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("UpdateLoadingMessage must be inert after Stop; before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
}

// TestTypingController_FireSetStatusBailsAfterDiscard covers the in-flight
// goroutine race the *AfterDiscard tests above do NOT exercise: a keepalive
// tick that won case <-ticker.C: before done was closed, an
// UpdateLoadingMessage goroutine that started before Discard, or the
// initial Start goroutine when Discard fires immediately. In all those
// scenarios the goroutine has already been launched and is about to call
// fireSetStatus when stopped flips to true. fireSetStatus must short-circuit
// before reaching SetAssistantThreadsStatusContext so the displaced run
// cannot push one last status to the shared (channel, threadTS) and erase
// the replacement controller's banner. The Stop clear path (status=="") is
// exempt because Stop's cleanup goroutine fires it AFTER setting stopped.
func TestTypingController_FireSetStatusBailsAfterDiscard(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour // freeze keepalive
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Discard()

	statusesBefore, _, _ := fc.snapshot()
	// Simulate an already-launched goroutine reaching fireSetStatus after
	// Discard tripped stopped=true. Calling fireSetStatus directly mirrors
	// what the keepalive's case <-ticker.C: branch does after losing the
	// select race, what UpdateLoadingMessage's spawned goroutine does after
	// the lock-free dispatch, and what Start's initial goroutine does on
	// an immediately-discarded controller.
	c.fireSetStatus(context.Background(), DefaultTypingStatusText)

	statusesAfter, _, _ := fc.snapshot()
	if len(statusesAfter) != len(statusesBefore) {
		t.Fatalf("fireSetStatus must bail when stopped is set; before=%d after=%d",
			len(statusesBefore), len(statusesAfter))
	}
}

// TestTypingController_FireSetStatusClearStillFiresAfterStop pins the
// invariant that the empty-status clear path is NOT short-circuited by
// stopped=true — Stop's cleanup goroutine sets stopped first and then
// fires fireSetStatus(ctx, "") to wipe the banner. Regression guard for
// the race-fix above accidentally dropping this call.
func TestTypingController_FireSetStatusClearStillFiresAfterStop(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	waitForCondition(t, time.Second, func() bool {
		statuses, adds, _ := fc.snapshot()
		return len(statuses) >= 1 && len(adds) >= 1
	}, "initial signals")

	c.Stop()

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
	}, "Stop's clear setStatus must still fire after stopped=true")
}

func TestTypingController_UpdateLoadingMessageTruncatesLongInput(t *testing.T) {
	fc := &fakeTypingClient{}
	c := newTestController(fc, func(cfg *TypingControllerConfig) {
		cfg.KeepaliveInterval = 1 * time.Hour
	})

	c.Start(context.Background())
	defer c.Stop()
	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		return len(statuses) >= 1
	}, "initial setStatus")

	// Slack rejects any loading_messages entry over 50 bytes. Pass a long
	// reasoning line and assert the wire payload was truncated under cap.
	long := "🤔 The alert is essentially a test message with no real content; let me search for runbooks first as required."
	c.UpdateLoadingMessage(long)

	waitForCondition(t, time.Second, func() bool {
		statuses, _, _ := fc.snapshot()
		for _, s := range statuses {
			if len(s.LoadingMessages) == 1 && len(s.LoadingMessages[0]) > 0 && len(s.LoadingMessages[0]) <= loadingMessageMaxBytes {
				return true
			}
		}
		return false
	}, "loading message truncated under Slack's 50-byte cap")

	// Verify the truncated line ends with the ellipsis marker.
	statuses, _, _ := fc.snapshot()
	var sentLine string
	for _, s := range statuses {
		if len(s.LoadingMessages) == 1 {
			sentLine = s.LoadingMessages[0]
		}
	}
	if !strings.HasSuffix(sentLine, "…") {
		t.Fatalf("expected truncation ellipsis suffix, got %q (len=%d)", sentLine, len(sentLine))
	}
}

func TestTruncateForLoadingMessage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short passes through", "hi", 50, "hi"},
		{"exact fits", "12345", 5, "12345"},
		{"one over truncates", "1234567", 5, "12…"},
		{"empty stays empty", "", 50, ""},
		{"zero max passes through", "anything", 0, "anything"},
		{"multibyte safe", "ñññññññññ", 5, "ñ…"}, // each ñ is 2 bytes
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateForLoadingMessage(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncateForLoadingMessage(%q,%d) = %q, want %q",
					tc.in, tc.max, got, tc.want)
			}
		})
	}
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
