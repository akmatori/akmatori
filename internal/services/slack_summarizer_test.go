package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func setupSummarizerTestDB(t *testing.T) {
	t.Helper()
	// Reuse the title-generator helper: it migrates LLMSettings into an
	// in-memory sqlite db and rebinds database.DB.
	setupTitleGeneratorTestDB(t)
}

func seedSummarizerSettings(t *testing.T, settings database.LLMSettings) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
		t.Fatalf("clear llm_settings: %v", err)
	}
	if err := database.DB.Create(&settings).Error; err != nil {
		t.Fatalf("seed llm_settings: %v", err)
	}
}

func TestSummarizeForSlack_UnderBudgetPassthrough(t *testing.T) {
	setupSummarizerTestDB(t)
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when input fits the budget")
		return "", nil
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), "short body", 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "short body" {
		t.Errorf("expected passthrough, got %q", got)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestSummarizeForSlack_NilCallerUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	long := strings.Repeat("y", 500) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: All good.\n[/FINAL_RESULT]"

	s := NewSlackSummarizer(nil)
	got, err := s.SummarizeForSlack(context.Background(), long, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 200 {
		t.Errorf("expected len(got)<=200, got %d", len(got))
	}
}

func TestSummarizeForSlack_OverBudgetLLMSummary(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "anthropic-active",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4",
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("noise ", 500) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: failover succeeded\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "✅ *Resolved*\nFailover succeeded.\nView reasoning log in the Akmatori UI.", nil
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Failover succeeded") {
		t.Errorf("expected LLM summary in output, got %q", got)
	}
	if len(got) > 400 {
		t.Errorf("expected len(got)<=400, got %d", len(got))
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
	if caller.lastMaxTok != 600 {
		t.Errorf("expected max tokens 600, got %d", caller.lastMaxTok)
	}
	if caller.lastTemp != 0.2 {
		t.Errorf("expected temperature 0.2, got %v", caller.lastTemp)
	}
	if caller.lastLLM == nil || caller.lastLLM.APIKey != "test-key" {
		t.Errorf("expected forwarded API key 'test-key', got %+v", caller.lastLLM)
	}
}

func TestSummarizeForSlack_LLMReturnsOverBudgetUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "openai",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "test-key",
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("x", 1000) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: ok\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		// LLM produces output that is larger than the budget — must be
		// rejected and the deterministic fallback used instead.
		return strings.Repeat("LLM-overshoot ", 200), nil
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 250 {
		t.Errorf("expected len(got)<=250, got %d", len(got))
	}
	if strings.Contains(got, "LLM-overshoot") {
		t.Errorf("expected fallback output (not LLM overshoot), got %q", got)
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
}

func TestSummarizeForSlack_CallerErrorUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "openai",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "test-key",
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("z", 1000) + "\n[FINAL_RESULT]\nstatus: unresolved\nsummary: still investigating\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "", errors.New("transient LLM error")
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 300 {
		t.Errorf("expected len(got)<=300, got %d", len(got))
	}
	if !strings.Contains(got, "still investigating") {
		t.Errorf("expected fallback to include FINAL_RESULT summary, got %q", got)
	}
}

func TestSummarizeForSlack_WorkerNotConnectedUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "openai",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "test-key",
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("a", 1000) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: handled\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 250 {
		t.Errorf("expected len(got)<=250, got %d", len(got))
	}
	if !strings.Contains(got, "handled") {
		t.Errorf("expected fallback to include FINAL_RESULT summary, got %q", got)
	}
}

func TestSummarizeForSlack_MissingAPIKeyUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "openai-no-key",
		Provider: database.LLMProviderOpenAI,
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("a", 1000) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: handled\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when API key is missing")
		return "", nil
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 250 {
		t.Errorf("expected len(got)<=250, got %d", len(got))
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestSummarizeForSlack_ZeroBudget(t *testing.T) {
	setupSummarizerTestDB(t)
	caller := &fakeOneShotLLMCaller{}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), "anything", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for zero budget, got %q", got)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestSummarizeForSlack_EmptyLLMResponseUsesFallback(t *testing.T) {
	setupSummarizerTestDB(t)
	seedSummarizerSettings(t, database.LLMSettings{
		Name:     "openai",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "test-key",
		Enabled:  true,
		Active:   true,
	})

	long := strings.Repeat("q", 1000) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: ok\n[/FINAL_RESULT]"
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "   ", nil
	}}

	s := NewSlackSummarizer(caller)
	got, err := s.SummarizeForSlack(context.Background(), long, 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 300 {
		t.Errorf("expected len(got)<=300, got %d", len(got))
	}
	if got == "" {
		t.Errorf("expected non-empty fallback output")
	}
}
