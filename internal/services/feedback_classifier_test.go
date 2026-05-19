package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupClassifierTest(t *testing.T) *fakeOneShotLLMCaller {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	if err := db.Create(&database.LLMSettings{
		Name:     "test",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4-6",
		Active:   true,
		Enabled:  true,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &fakeOneShotLLMCaller{}
}

func TestFeedbackClassifier_ConfidentFeedback(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return `{"is_feedback": true, "summary": "data dir is /mnt/data not /var/lib/postgresql", "confidence": 0.95}`, nil
	}
	c := NewFeedbackClassifier(caller)

	verdict, err := c.Classify(context.Background(), "the postgres data dir is /mnt/data not /var/lib/postgresql", &database.Incident{Title: "Postgres outage"})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !verdict.IsConfidentFeedback() {
		t.Errorf("expected confident feedback, got %+v", verdict)
	}
	if !strings.Contains(verdict.Summary, "data dir") {
		t.Errorf("summary not preserved: %+v", verdict)
	}

	if !strings.Contains(caller.lastSystem, "OPERATOR FEEDBACK") {
		t.Errorf("system prompt missing taxonomy")
	}
	if !strings.Contains(caller.lastUser, "Incident title: Postgres outage") {
		t.Errorf("user prompt missing incident title: %s", caller.lastUser)
	}
}

func TestFeedbackClassifier_BelowThresholdNotConfident(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return `{"is_feedback": true, "summary": "maybe useful", "confidence": 0.4}`, nil
	}
	c := NewFeedbackClassifier(caller)

	verdict, err := c.Classify(context.Background(), "thanks for the help", &database.Incident{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if verdict.IsConfidentFeedback() {
		t.Errorf("expected NOT confident at 0.4, got IsConfidentFeedback=true")
	}
}

func TestFeedbackClassifier_NotFeedback(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return `{"is_feedback": false, "summary": "casual chat", "confidence": 0.9}`, nil
	}
	c := NewFeedbackClassifier(caller)

	verdict, err := c.Classify(context.Background(), "any update yet?", &database.Incident{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if verdict.IsConfidentFeedback() {
		t.Error("expected NOT feedback")
	}
}

func TestFeedbackClassifier_WorkerNotConnected(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}
	c := NewFeedbackClassifier(caller)

	_, err := c.Classify(context.Background(), "msg", &database.Incident{})
	if err == nil || err != ErrWorkerNotConnected {
		t.Errorf("expected ErrWorkerNotConnected, got %v", err)
	}
}

func TestFeedbackClassifier_NoCallerIsSilent(t *testing.T) {
	c := NewFeedbackClassifier(nil)
	_, err := c.Classify(context.Background(), "msg", &database.Incident{})
	if err != ErrWorkerNotConnected {
		t.Errorf("nil caller should report worker disconnected, got %v", err)
	}
}

func TestFeedbackClassifier_NilReceiverIsSilent(t *testing.T) {
	var c *FeedbackClassifier
	_, err := c.Classify(context.Background(), "msg", &database.Incident{})
	if err != ErrWorkerNotConnected {
		t.Errorf("nil classifier should report worker disconnected, got %v", err)
	}
}

func TestFeedbackClassifier_NilIncidentErrorsBeforeLLMCall(t *testing.T) {
	caller := setupClassifierTest(t)
	c := NewFeedbackClassifier(caller)

	_, err := c.Classify(context.Background(), "the data dir is /mnt/data", nil)
	if err == nil || !strings.Contains(err.Error(), "incident is nil") {
		t.Fatalf("expected nil incident error, got %v", err)
	}
	if caller.callCount() != 0 {
		t.Errorf("nil incident should skip LLM call entirely, got %d calls", caller.callCount())
	}
}

func TestFeedbackClassifier_LLMErrorIsWrapped(t *testing.T) {
	caller := setupClassifierTest(t)
	llmErr := errors.New("provider timeout")
	caller.respond = func(ctx context.Context) (string, error) {
		return "", llmErr
	}
	c := NewFeedbackClassifier(caller)

	_, err := c.Classify(context.Background(), "postgres data dir is /mnt/data", &database.Incident{Title: "Postgres outage"})
	if !errors.Is(err, llmErr) {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}
	if !strings.Contains(err.Error(), "classify: llm call") {
		t.Errorf("expected classify context in error, got %v", err)
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
}

func TestFeedbackClassifier_EmptyMessageIsNotCalled(t *testing.T) {
	caller := setupClassifierTest(t)
	c := NewFeedbackClassifier(caller)

	verdict, err := c.Classify(context.Background(), "   ", &database.Incident{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if verdict.IsConfidentFeedback() {
		t.Errorf("blank input should not be confident feedback")
	}
	if caller.callCount() != 0 {
		t.Errorf("blank input should skip LLM call entirely, got %d calls", caller.callCount())
	}
}

func TestFeedbackClassifier_InvalidJSONIsSilent(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return "not json at all", nil
	}
	c := NewFeedbackClassifier(caller)

	verdict, err := c.Classify(context.Background(), "msg", &database.Incident{})
	if err != nil {
		t.Errorf("invalid JSON should return zero verdict + nil error, got %v", err)
	}
	if verdict.IsConfidentFeedback() {
		t.Error("invalid JSON should not yield feedback")
	}
}

func TestFeedbackClassifier_NoAPIKeyIsWorkerOffline(t *testing.T) {
	caller := setupClassifierTest(t)
	if err := database.DB.Model(&database.LLMSettings{}).Where("active = ?", true).Update("api_key", "").Error; err != nil {
		t.Fatalf("clear api key: %v", err)
	}
	c := NewFeedbackClassifier(caller)

	_, err := c.Classify(context.Background(), "msg", &database.Incident{Title: "x"})
	if err != ErrWorkerNotConnected {
		t.Errorf("expected ErrWorkerNotConnected when no api key, got %v", err)
	}
}

func TestParseFeedbackVerdict_StripsCodeFence(t *testing.T) {
	v, err := parseFeedbackVerdict("```json\n{\"is_feedback\":true,\"summary\":\"x\",\"confidence\":0.8}\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !v.IsFeedback || v.Confidence != 0.8 {
		t.Errorf("got %+v", v)
	}
}

func TestParseFeedbackVerdict_TruncatesSummaryAtRuneBoundary(t *testing.T) {
	// Regression: byte-slicing a multibyte summary at byte index 137 would
	// split a UTF-8 rune. The split summary then propagated through to the
	// memory description (since 137 < 500-byte cap, no further truncation
	// kicked in), and Postgres rejected the INSERT with "invalid byte
	// sequence". Use a 200-rune Japanese summary so the cap lands mid-rune
	// under the old logic.
	long := strings.Repeat("日", 200)
	raw := fmt.Sprintf(`{"is_feedback": true, "summary": "%s", "confidence": 0.9}`, long)

	v, err := parseFeedbackVerdict(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !utf8.ValidString(v.Summary) {
		t.Errorf("summary contains invalid UTF-8 after truncation")
	}
	if utf8.RuneCountInString(v.Summary) > 140 {
		t.Errorf("summary has %d runes, want ≤ 140", utf8.RuneCountInString(v.Summary))
	}
	if !strings.HasSuffix(v.Summary, "...") {
		t.Errorf("expected trailing ellipsis, got tail %q", v.Summary[max(0, len(v.Summary)-12):])
	}
}

func TestParseFeedbackVerdict_ClampsConfidence(t *testing.T) {
	v, _ := parseFeedbackVerdict(`{"is_feedback":true,"confidence":1.7,"summary":"x"}`)
	if v.Confidence != 1.0 {
		t.Errorf("expected confidence clamped to 1.0, got %f", v.Confidence)
	}
	v2, _ := parseFeedbackVerdict(`{"is_feedback":false,"confidence":-0.3,"summary":"x"}`)
	if v2.Confidence != 0 {
		t.Errorf("expected confidence clamped to 0, got %f", v2.Confidence)
	}
}

func TestBuildFeedbackUserPrompt_TruncatesLongInputs(t *testing.T) {
	long := strings.Repeat("y", 5000)
	got := buildFeedbackUserPrompt(long, &database.Incident{Title: "T", Response: long})
	if len(got) > 5000 { // generous upper bound — both fields capped, plus some scaffolding
		t.Errorf("prompt too long: %d", len(got))
	}
}

func TestBuildFeedbackUserPrompt_HandlesEmptyResponse(t *testing.T) {
	got := buildFeedbackUserPrompt("hello", &database.Incident{Title: "T"})
	if !strings.Contains(got, "(no agent response yet)") {
		t.Errorf("expected placeholder for empty response: %s", got)
	}
}

func TestBuildFeedbackUserPrompt_HandlesEmptyTitle(t *testing.T) {
	got := buildFeedbackUserPrompt("hello", &database.Incident{Response: "agent response"})
	if !strings.Contains(got, "Incident title: (no title)") {
		t.Errorf("expected placeholder for empty title: %s", got)
	}
}

func TestFeedbackVerdict_IsConfidentFeedback(t *testing.T) {
	cases := []struct {
		v    FeedbackVerdict
		want bool
	}{
		{FeedbackVerdict{IsFeedback: true, Confidence: 0.95}, true},
		{FeedbackVerdict{IsFeedback: true, Confidence: FeedbackConfidenceThreshold}, true},
		{FeedbackVerdict{IsFeedback: true, Confidence: 0.5}, false},
		{FeedbackVerdict{IsFeedback: false, Confidence: 0.99}, false},
	}
	for _, tc := range cases {
		if got := tc.v.IsConfidentFeedback(); got != tc.want {
			t.Errorf("IsConfidentFeedback(%+v) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
