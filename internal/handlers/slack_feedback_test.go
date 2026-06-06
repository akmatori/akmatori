package handlers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/slack-go/slack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSlugFromUUID(t *testing.T) {
	cases := map[string]string{
		"abc-123-def-456":                   "abc123de",
		"":                                  "",
		"!!!@@@##":                          "",
		"550e8400-e29b-41d4-a716-446655440000": "550e8400",
	}
	for in, want := range cases {
		if got := slugFromUUID(in); got != want {
			t.Errorf("slugFromUUID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateBytesUTF8Safe_FitsBudget(t *testing.T) {
	// ASCII just-too-long case.
	in := strings.Repeat("x", 510)
	got := truncateBytesUTF8Safe(in, 500)
	if len(got) > 500 {
		t.Errorf("got %d bytes, want ≤ 500", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got[len(got)-5:])
	}
}

func TestTruncateBytesUTF8Safe_NoTruncationWhenShort(t *testing.T) {
	in := "short string"
	if got := truncateBytesUTF8Safe(in, 500); got != in {
		t.Errorf("got %q, want %q", got, in)
	}
}

func TestTruncateBytesUTF8Safe_PreservesRuneBoundaries(t *testing.T) {
	// 3-byte UTF-8 chars; ensure we don't slice mid-character.
	in := strings.Repeat("日本", 200) // 6 bytes per pair
	got := truncateBytesUTF8Safe(in, 100)
	if len(got) > 100 {
		t.Errorf("got %d bytes, want ≤ 100", len(got))
	}
	// The result should still be valid UTF-8 (Go strings carry bytes, but the
	// suffix "…" is 3 bytes and the prefix should land on a rune boundary).
	if got != "" && !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got tail %q", got[len(got)-6:])
	}
}

func TestBuildFeedbackMemory_DerivedFields(t *testing.T) {
	verdict := services.FeedbackVerdict{
		IsFeedback: true,
		Summary:    "data dir is /mnt/data",
		Confidence: 0.95,
	}
	mem := buildFeedbackMemory("Postgres data dir is on /mnt/data, not /var/lib/postgresql", verdict, "abc-123")

	if mem.Scope != services.MemoryScopeGlobal {
		t.Errorf("scope = %q, want global", mem.Scope)
	}
	if mem.Type != services.MemoryTypeFeedback {
		t.Errorf("type = %q, want feedback", mem.Type)
	}
	if mem.IncidentUUID != "abc-123" {
		t.Errorf("incident UUID = %q", mem.IncidentUUID)
	}
	if mem.CreatedBy != services.MemoryCreatedByOperator {
		t.Errorf("created_by = %q, want operator", mem.CreatedBy)
	}
	if !strings.Contains(mem.Body, "/mnt/data") {
		t.Errorf("body should preserve original message, got %q", mem.Body)
	}
	if !strings.HasSuffix(mem.Name, "-abc123") {
		t.Errorf("name should end with UUID prefix, got %q", mem.Name)
	}
	if !strings.HasPrefix(mem.Description, "data dir") {
		t.Errorf("description should start with summary, got %q", mem.Description)
	}
}

func TestBuildFeedbackMemory_FallsBackToTextWhenSummaryEmpty(t *testing.T) {
	verdict := services.FeedbackVerdict{IsFeedback: true, Summary: "  ", Confidence: 0.9}
	mem := buildFeedbackMemory("the data dir is /mnt/data", verdict, "u")
	if !strings.Contains(mem.Description, "data dir") {
		t.Errorf("expected description to fall back to message text, got %q", mem.Description)
	}
}

func TestBuildFeedbackMemory_LongMultibyteBodyStaysValidUTF8(t *testing.T) {
	// Regression: previously the body was sliced by raw byte count, which
	// could split a multi-byte UTF-8 rune. Postgres would then reject the
	// INSERT. Same input shape as the HTTP feedback regression test.
	long := strings.Repeat("日", (services.MemoryBodyMaxBytes/3)+10)
	verdict := services.FeedbackVerdict{IsFeedback: true, Summary: "long jp note", Confidence: 0.9}

	mem := buildFeedbackMemory(long, verdict, "abc-123")

	if len(mem.Body) > services.MemoryBodyMaxBytes {
		t.Errorf("body len = %d, want ≤ %d", len(mem.Body), services.MemoryBodyMaxBytes)
	}
	if len(mem.Body)%3 != 0 {
		t.Errorf("body len %d not on a 3-byte UTF-8 boundary — body was sliced mid-rune", len(mem.Body))
	}
}

func TestLookupIncidentByThread_DMOriginated(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db

	if err := db.Create(&database.Incident{
		UUID:     "uuid-dm",
		Source:   "slack",
		SourceID: "T1",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	incident, err := lookupIncidentByThread("T1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if incident.UUID != "uuid-dm" {
		t.Errorf("got UUID %q", incident.UUID)
	}
}

func TestLookupIncidentByThread_AlertChannel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db

	if err := db.Create(&database.Incident{
		UUID:           "uuid-alert",
		Source:         "zabbix",
		SourceID:       "alert-99",
		SlackMessageTS: "T2",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	incident, err := lookupIncidentByThread("T2")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if incident.UUID != "uuid-alert" {
		t.Errorf("got UUID %q", incident.UUID)
	}
}

func TestLookupIncidentByThread_NoMatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db

	if _, err := lookupIncidentByThread("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown thread")
	}
}

// TestMaybeCaptureSlackFeedback_PersistsConfidentFeedback exercises the full
// integration with mock dependencies. We don't drive a real slack.Client —
// the handler tolerates a nil client (skipping the reaction/post calls).
func TestMaybeCaptureSlackFeedback_PersistsConfidentFeedback(t *testing.T) {
	mock := newMockMemoryService()
	caller := &fakeOneShotLLMCallerH{response: `{"is_feedback": true, "summary": "data dir is /mnt/data", "confidence": 0.92}`}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}, &database.LLMSettings{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	if err := db.Create(&database.LLMSettings{
		Name: "t", Provider: database.LLMProviderAnthropic, APIKey: "k",
		Model: "claude-sonnet-4-6", Active: true, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed llm: %v", err)
	}
	if err := db.Create(&database.Incident{
		UUID: "inc-99", Source: "slack", SourceID: "TX", Title: "Postgres outage",
		Response: "agent investigated postgres",
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	classifier := services.NewFeedbackClassifier(caller)
	h := &SlackHandler{
		memoryManager:      mock,
		feedbackClassifier: classifier,
		botUserID:          "BOT",
	}

	h.maybeCaptureSlackFeedback("C", "TX", "M-1", "the postgres data dir is /mnt/data not /var/lib/postgresql", "U")

	if mock.lastUpserted == nil {
		t.Fatalf("expected memory upserted")
	}
	if mock.lastUpserted.IncidentUUID != "inc-99" {
		t.Errorf("incident UUID not propagated: %+v", mock.lastUpserted)
	}
	if !strings.Contains(mock.lastUpserted.Description, "data dir") {
		t.Errorf("description should reflect summary, got %q", mock.lastUpserted.Description)
	}
}

// TestMaybeCaptureSlackFeedback_NotConfidentDoesNothing verifies the silent-
// on-negatives behavior — chatty replies must NEVER write a memory.
func TestMaybeCaptureSlackFeedback_NotConfidentDoesNothing(t *testing.T) {
	mock := newMockMemoryService()
	caller := &fakeOneShotLLMCallerH{response: `{"is_feedback": false, "summary": "casual chat", "confidence": 0.95}`}

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&database.Incident{}, &database.LLMSettings{})
	database.DB = db
	_ = db.Create(&database.LLMSettings{
		Name: "t", Provider: database.LLMProviderAnthropic, APIKey: "k",
		Model: "x", Active: true, Enabled: true,
	}).Error
	_ = db.Create(&database.Incident{UUID: "i", Source: "slack", SourceID: "T"}).Error

	classifier := services.NewFeedbackClassifier(caller)
	h := &SlackHandler{
		memoryManager:      mock,
		feedbackClassifier: classifier,
	}

	h.maybeCaptureSlackFeedback("C", "T", "M", "any update?", "U")

	if mock.lastUpserted != nil {
		t.Errorf("expected NO memory written for non-feedback, got %+v", mock.lastUpserted)
	}
}

func TestMaybeCaptureSlackFeedback_MissingDepsIsNoOp(t *testing.T) {
	// No classifier wired — should not panic, should not call DB.
	h := &SlackHandler{}
	h.maybeCaptureSlackFeedback("C", "T", "M", "anything", "U")

	// Memory manager nil should also short-circuit.
	mock := newMockMemoryService()
	h2 := &SlackHandler{memoryManager: mock} // classifier nil
	h2.maybeCaptureSlackFeedback("C", "T", "M", "anything", "U")
	if mock.lastUpserted != nil {
		t.Error("nil classifier should skip memory write")
	}
}

func TestMaybeCaptureSlackFeedback_BotMessageSkipped(t *testing.T) {
	mock := newMockMemoryService()
	caller := &fakeOneShotLLMCallerH{response: `{"is_feedback": true, "summary": "x", "confidence": 0.99}`}
	classifier := services.NewFeedbackClassifier(caller)
	h := &SlackHandler{
		memoryManager:      mock,
		feedbackClassifier: classifier,
		botUserID:          "BOT",
	}

	// User == botUserID — should bail before classifying.
	h.maybeCaptureSlackFeedback("C", "T", "M", "anything", "BOT")

	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls when message is from bot, got %d", caller.calls)
	}
}

// fakeOneShotLLMCallerH is a small inline OneShotLLMCaller test double for
// the handler-package tests. Mirrors fakeOneShotLLMCaller in the services
// package; we redefine it here because cross-package use isn't possible.
// The mutex guards `calls` and `lastUser` so router tests can read them from
// the main goroutine after the classifier ran on a worker goroutine.
type fakeOneShotLLMCallerH struct {
	mu       sync.Mutex
	calls    int
	lastUser string
	response string
	err      error
}

func (f *fakeOneShotLLMCallerH) OneShotLLM(_ context.Context, _ *services.LLMSettingsForWorker, _, user string, _ int, _ float64) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastUser = user
	resp, err := f.response, f.err
	f.mu.Unlock()
	return resp, err
}

func (f *fakeOneShotLLMCallerH) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeOneShotLLMCallerH) lastUserPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastUser
}

func (f *fakeOneShotLLMCallerH) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeOneShotLLMCallerH) setResponse(resp string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = resp
}

// fakeFeedbackAcker is a feedbackAcker test double recording reaction and
// text-post calls so ack behaviour can be asserted without a live
// *slack.Client. The mutex guards the counters because acks may run on a
// worker goroutine.
type fakeFeedbackAcker struct {
	mu           sync.Mutex
	reactions    int
	posts        int
	lastReaction string
	lastPostText string
	reactErr     error
	postErr      error
}

func (f *fakeFeedbackAcker) AddReaction(name string, _ slack.ItemRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions++
	f.lastReaction = name
	return f.reactErr
}

func (f *fakeFeedbackAcker) PostThreadText(_, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts++
	f.lastPostText = text
	return f.postErr
}

func (f *fakeFeedbackAcker) reactionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reactions
}

func (f *fakeFeedbackAcker) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

// TestNewSlackHandler_NilClientLeavesAckerNil verifies the graceful-degradation
// contract: a client-less handler does not wire a feedbackAcker (persist-only,
// no nil-pointer panic on ack).
func TestNewSlackHandler_NilClientLeavesAckerNil(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)
	if h.feedbackAcker != nil {
		t.Errorf("expected nil feedbackAcker for nil client, got %#v", h.feedbackAcker)
	}
}

// TestNewSlackHandler_NonNilClientWiresAdapter verifies the default adapter is
// wired when a real client is present.
func TestNewSlackHandler_NonNilClientWiresAdapter(t *testing.T) {
	client := slack.New("xoxb-test-token")
	h := NewSlackHandler(client, nil, nil, nil, nil)
	if h.feedbackAcker == nil {
		t.Fatal("expected feedbackAcker to be wired for non-nil client")
	}
	if _, ok := h.feedbackAcker.(slackFeedbackAcker); !ok {
		t.Errorf("expected slackFeedbackAcker adapter, got %T", h.feedbackAcker)
	}
}

// routeFixture wires a SlackHandler with stub deps + a seeded sqlite-backed
// incident at the given thread TS. Tests use it to exercise
// routeBotMentionThreadReply and the surrounding wiring without spinning up
// a real Slack client or agent runtime. The agent fall-through is captured
// in an atomic counter so the goroutine-driven router can be asserted on
// without races.
type routeFixture struct {
	handler    *SlackHandler
	mockMem    *mockMemoryService
	caller     *fakeOneShotLLMCallerH
	agentCalls *int32
}

func newRouteFixture(t *testing.T, threadTS string) *routeFixture {
	t.Helper()

	mock := newMockMemoryService()
	caller := &fakeOneShotLLMCallerH{
		response: `{"is_feedback": true, "summary": "data dir is /mnt/data", "confidence": 0.92}`,
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}, &database.LLMSettings{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	if err := db.Create(&database.LLMSettings{
		Name: "t", Provider: database.LLMProviderAnthropic, APIKey: "k",
		Model: "claude-sonnet-4-6", Active: true, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed llm: %v", err)
	}
	if threadTS != "" {
		if err := db.Create(&database.Incident{
			UUID:     "inc-99",
			Source:   "slack",
			SourceID: threadTS,
			Title:    "outage",
			Response: "agent investigated",
		}).Error; err != nil {
			t.Fatalf("seed incident: %v", err)
		}
	}

	var agentCalls int32
	h := &SlackHandler{
		memoryManager:      mock,
		feedbackClassifier: services.NewFeedbackClassifier(caller),
		botUserID:          "BOT",
		runMentionContinuation: func(_, _, _, _, _ string) {
			atomic.AddInt32(&agentCalls, 1)
		},
	}

	return &routeFixture{
		handler:    h,
		mockMem:    mock,
		caller:     caller,
		agentCalls: &agentCalls,
	}
}

func (f *routeFixture) agentCallCount() int32 {
	return atomic.LoadInt32(f.agentCalls)
}

// --- routeBotMentionThreadReply tests ---

// TestRouteBotMentionThreadReply_FeedbackShortCircuits verifies the core
// classify-first contract: a confident feedback verdict on an incident-thread
// @mention writes a memory and does NOT invoke the agent.
func TestRouteBotMentionThreadReply_FeedbackShortCircuits(t *testing.T) {
	fx := newRouteFixture(t, "TX")

	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> the data dir is /mnt/data, not /var/lib", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.mockMem.lastUpsertedSnap() != nil
	}, "memory should be upserted on confident feedback")

	if got := fx.agentCallCount(); got != 0 {
		t.Errorf("agent fall-through called %d times, want 0", got)
	}
	if got := fx.mockMem.lastUpsertedSnap(); got == nil || got.IncidentUUID != "inc-99" {
		t.Errorf("incident UUID not propagated: %+v", got)
	}
}

// TestRouteBotMentionThreadReply_NotConfidentFallsThroughToAgent ensures a
// low-confidence verdict falls through to the agent path and does NOT persist
// a memory.
func TestRouteBotMentionThreadReply_NotConfidentFallsThroughToAgent(t *testing.T) {
	fx := newRouteFixture(t, "TX")
	fx.caller.setResponse(`{"is_feedback": true, "summary": "x", "confidence": 0.5}`)

	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> any update?", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.agentCallCount() == 1
	}, "agent should be called on low confidence")

	if got := fx.mockMem.lastUpsertedSnap(); got != nil {
		t.Errorf("expected NO memory persist for low-confidence verdict, got %+v", got)
	}
}

// TestRouteBotMentionThreadReply_ClassifierErrorFallsThrough verifies that a
// generic classifier error routes to the agent — we never want to drop the
// user's @mention because of a transient LLM failure.
func TestRouteBotMentionThreadReply_ClassifierErrorFallsThrough(t *testing.T) {
	fx := newRouteFixture(t, "TX")
	fx.caller.setError(errors.New("boom"))

	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> hi", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.agentCallCount() == 1
	}, "agent should run when classifier errors")

	if got := fx.mockMem.lastUpsertedSnap(); got != nil {
		t.Errorf("expected NO memory persist on classifier error, got %+v", got)
	}
}

// TestRouteBotMentionThreadReply_WorkerOfflineFallsThrough verifies that
// ErrWorkerNotConnected (the worker disconnected sentinel) routes to the
// agent path just like any other error — same fall-through, no warn-level
// spam.
func TestRouteBotMentionThreadReply_WorkerOfflineFallsThrough(t *testing.T) {
	fx := newRouteFixture(t, "TX")
	fx.caller.setError(services.ErrWorkerNotConnected)

	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> hi", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.agentCallCount() == 1
	}, "agent should run when worker offline")

	if got := fx.mockMem.lastUpsertedSnap(); got != nil {
		t.Errorf("expected NO memory persist when worker offline, got %+v", got)
	}
}

// TestRouteBotMentionThreadReply_NoIncidentMatchFallsThrough verifies that
// when the thread doesn't map to any incident, the classifier is NOT
// invoked (saves an LLM call) and the agent path runs.
func TestRouteBotMentionThreadReply_NoIncidentMatchFallsThrough(t *testing.T) {
	fx := newRouteFixture(t, "TX")

	// Thread "OTHER" has no incident — lookupIncidentByThread returns error
	// before classifier runs.
	fx.handler.routeBotMentionThreadReply("C", "OTHER", "M-1", "<@BOT> hi", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.agentCallCount() == 1
	}, "agent should run when no incident matches the thread")

	if got := fx.caller.callCount(); got != 0 {
		t.Errorf("classifier should NOT have been called, got %d calls", got)
	}
	if got := fx.mockMem.lastUpsertedSnap(); got != nil {
		t.Errorf("expected NO memory persist when no incident matches, got %+v", got)
	}
}

// TestHandleBotMentionInThread_DoesNotShortCircuitOnPreClaimedDedup is a
// regression test for the dedup-collision bug: routeBotMentionThreadReply
// claims the dedup key BEFORE invoking handleBotMentionInThread on the
// fall-through. If handleBotMentionInThread also dedups the same key, the
// fall-through is silently dropped — the agent never runs. This asserts the
// inner function proceeds past the dedup point when the key is already set.
//
// The proof is reachability: with no Slack client wired, fetchThreadParentText
// panics on the nil pointer. If handleBotMentionInThread short-circuited at
// a dedup gate, no panic would occur and we'd silently return.
func TestHandleBotMentionInThread_DoesNotShortCircuitOnPreClaimedDedup(t *testing.T) {
	h := &SlackHandler{botUserID: "BOT"}
	h.processedMsgs.Store("C:M-1", struct{}{})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("handleBotMentionInThread returned cleanly with a pre-claimed dedup key — router fall-through would be silently dropped in production")
		}
	}()

	h.handleBotMentionInThread("C", "T", "M-1", "<@BOT> hi", "U")
}

// TestRouteBotMentionThreadReply_DedupIdempotent verifies that two calls with
// the same channel+messageTS short-circuit on the second — classifier runs
// at most once and the agent fall-through is called at most once.
func TestRouteBotMentionThreadReply_DedupIdempotent(t *testing.T) {
	fx := newRouteFixture(t, "TX")
	// Low confidence so first call falls through to agent.
	fx.caller.setResponse(`{"is_feedback": false, "summary": "x", "confidence": 0.5}`)

	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> hi", "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.agentCallCount() == 1
	}, "first call should reach the agent")

	// Second call: identical dedup key → must be a no-op.
	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", "<@BOT> hi", "U")

	// Give any rogue goroutine a moment to run; counters must stay at 1.
	time.Sleep(50 * time.Millisecond)

	if got := fx.agentCallCount(); got != 1 {
		t.Errorf("agent calls = %d, want 1 (dedup failed)", got)
	}
	if got := fx.caller.callCount(); got != 1 {
		t.Errorf("classifier calls = %d, want 1 (dedup failed)", got)
	}
}

// TestClassifyThreadReplyForFeedback_StripsMention asserts the two-text
// contract: the classifier sees mention-stripped text, but the persisted
// memory body carries the original (un-stripped) text so operators can later
// audit the literal Slack message.
func TestClassifyThreadReplyForFeedback_StripsMention(t *testing.T) {
	fx := newRouteFixture(t, "TX")

	raw := "<@BOT> the data dir is /mnt/data, not /var/lib"
	fx.handler.routeBotMentionThreadReply("C", "TX", "M-1", raw, "U")

	testhelpers.AssertEventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		return fx.mockMem.lastUpsertedSnap() != nil
	}, "memory should be persisted on confident feedback")

	prompt := fx.caller.lastUserPrompt()
	if strings.Contains(prompt, "<@BOT>") {
		t.Errorf("classifier user prompt should NOT contain bot mention, got %q", prompt)
	}
	if !strings.Contains(prompt, "the data dir is /mnt/data") {
		t.Errorf("classifier user prompt should contain the message text, got %q", prompt)
	}

	if got := fx.mockMem.lastUpsertedSnap(); got == nil || !strings.Contains(got.Body, "<@BOT>") {
		t.Errorf("persisted memory body should retain original mention text, got %+v", got)
	}
}
