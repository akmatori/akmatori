package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func setupFormatterTestDB(t *testing.T) {
	t.Helper()
	// Reuse the title-generator helper: it migrates LLMSettings into an
	// in-memory sqlite db and rebinds database.DB.
	setupTitleGeneratorTestDB(t)
	if err := database.DB.AutoMigrate(&database.FormattingSettings{}); err != nil {
		t.Fatalf("migrate formatting_settings: %v", err)
	}
}

func seedFormatterSettings(t *testing.T, settings database.FormattingSettings) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM formatting_settings").Error; err != nil {
		t.Fatalf("clear formatting_settings: %v", err)
	}
	if settings.SingletonKey == "" {
		settings.SingletonKey = "default"
	}
	if err := database.DB.Create(&settings).Error; err != nil {
		t.Fatalf("seed formatting_settings: %v", err)
	}
}

func seedFormatterLLM(t *testing.T, settings database.LLMSettings) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
		t.Fatalf("clear llm_settings: %v", err)
	}
	if err := database.DB.Create(&settings).Error; err != nil {
		t.Fatalf("seed llm_settings: %v", err)
	}
}

func defaultLLMForFormatter() database.LLMSettings {
	return database.LLMSettings{
		Name:     "anthropic-active",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4",
		Enabled:  true,
		Active:   true,
	}
}

func TestResponseFormatter_NilCallerPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat please.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	f := NewResponseFormatter(nil)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResponseFormatter_DisabledPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: false, SystemPrompt: "Should not be called.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when formatting is disabled")
		return "", nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough, got %q", got)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestResponseFormatter_EmptyPromptUsesDefaultPrompt(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "   \n\t  ", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "formatted-default", nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "formatted-default" {
		t.Errorf("expected formatted output via default prompt, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call when prompt is blank (default applied), got %d", caller.callCount())
	}
	if caller.lastSystem != strings.TrimSpace(database.DefaultFormattingPrompt) {
		t.Errorf("expected blank stored prompt to fall back to DefaultFormattingPrompt, got %q", caller.lastSystem)
	}
}

func TestResponseFormatter_MissingLLMSettingsPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat please.", MaxTokens: 1000, Temperature: 0.2})
	// No LLM settings seeded → GetLLMSettings returns an error, formatter falls back.
	if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
		t.Fatalf("clear llm_settings: %v", err)
	}

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when LLM settings are missing")
		return "", nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResponseFormatter_MissingAPIKeyPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat please.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, database.LLMSettings{
		Name:     "openai-no-key",
		Provider: database.LLMProviderOpenAI,
		Enabled:  true,
		Active:   true,
	})

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when API key is empty")
		return "", nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestResponseFormatter_HappyPathReturnsLLMOutput(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:      true,
		SystemPrompt: "You are a strict JSON formatter.",
		MaxTokens:    1234,
		Temperature:  0.4,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"status":"resolved","summary":"Failover succeeded."}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "Raw final answer.", "step 1\nstep 2")
	if got != `{"status":"resolved","summary":"Failover succeeded."}` {
		t.Errorf("expected formatted output, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", caller.callCount())
	}
	if caller.lastSystem != "You are a strict JSON formatter." {
		t.Errorf("system prompt not forwarded: %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastUser, "--- Raw response ---") {
		t.Errorf("user prompt missing raw response delimiter: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "Raw final answer.") {
		t.Errorf("user prompt missing raw response: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "--- Full reasoning ---") {
		t.Errorf("user prompt missing reasoning delimiter: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "step 1") {
		t.Errorf("user prompt missing reasoning content: %q", caller.lastUser)
	}
	if caller.lastMaxTok != 1234 {
		t.Errorf("max tokens = %d, want 1234", caller.lastMaxTok)
	}
	if caller.lastTemp != 0.4 {
		t.Errorf("temperature = %v, want 0.4", caller.lastTemp)
	}
	if caller.lastLLM == nil || caller.lastLLM.APIKey != "test-key" {
		t.Errorf("expected forwarded API key 'test-key', got %+v", caller.lastLLM)
	}
}

func TestResponseFormatter_DefaultsMaxTokensWhenZero(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:      true,
		SystemPrompt: "Reformat.",
		MaxTokens:    0,
		Temperature:  0.0,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "formatted", nil
	}}

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), "raw", "log"); got != "formatted" {
		t.Errorf("expected formatted output, got %q", got)
	}
	if caller.lastMaxTok != 1500 {
		t.Errorf("expected default max tokens 1500 when settings has 0, got %d", caller.lastMaxTok)
	}
}

func TestResponseFormatter_WorkerNotConnectedPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough on ErrWorkerNotConnected, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call attempt, got %d", caller.callCount())
	}
}

func TestResponseFormatter_GenericErrorPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "", errors.New("transient LLM error")
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough on generic error, got %q", got)
	}
}

func TestResponseFormatter_EmptyResultPassthrough(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "   \n\t   ", nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if got != "raw output" {
		t.Errorf("expected passthrough on empty/whitespace result, got %q", got)
	}
}

func TestResponseFormatter_PropagatesCallerDeadline(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}

	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
	defer cancel()

	f := NewResponseFormatter(caller)
	if got := f.Format(parent, "raw", "log"); got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
	if caller.contextSeen == nil {
		t.Fatal("expected the formatter to forward the parent context")
	}
	deadline, ok := caller.contextSeen.Deadline()
	if !ok {
		t.Fatal("expected forwarded context to retain a deadline")
	}
	// The forwarded deadline should match the parent's (within drift tolerance),
	// confirming the formatter did NOT install its own timeout when the caller
	// already had one.
	parentDeadline, _ := parent.Deadline()
	if deadline.Sub(parentDeadline) > 50*time.Millisecond || parentDeadline.Sub(deadline) > 50*time.Millisecond {
		t.Errorf("expected forwarded deadline %v to match parent %v", deadline, parentDeadline)
	}
}

func TestResponseFormatter_AppliesDefaultTimeout(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), "raw", "log"); got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
	if caller.contextSeen == nil {
		t.Fatal("expected the formatter to forward a context")
	}
	deadline, ok := caller.contextSeen.Deadline()
	if !ok {
		t.Fatal("expected forwarded context to have an installed deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > responseFormatterTimeout+time.Second {
		t.Errorf("expected default timeout near %v, got remaining=%v", responseFormatterTimeout, remaining)
	}
}

func TestResponseFormatter_TruncatesLargeFullLog(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}

	rawResp := "FINAL ANSWER"
	// Build a giant log with a uniquely-identifiable opener and a
	// uniquely-identifiable trailer. The opener must be dropped (truncated
	// from the start) and the trailer preserved.
	opener := "OPENER-MARKER-EARLIEST-LINE"
	filler := strings.Repeat("filler ", 12000)
	trailer := "TRAILER-MARKER-FINAL"
	fullLog := opener + filler + trailer

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), rawResp, fullLog); got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
	prompt := caller.lastUser
	if len(prompt) > responseFormatterMaxInputBytes+200 {
		t.Errorf("expected user prompt to fit near the input cap; got %d bytes (cap %d)", len(prompt), responseFormatterMaxInputBytes)
	}
	if !strings.Contains(prompt, "FINAL ANSWER") {
		t.Errorf("expected raw response to be preserved")
	}
	if !strings.Contains(prompt, trailer) {
		t.Errorf("expected reasoning trailer %q to be preserved", trailer)
	}
	if !strings.Contains(prompt, "earlier reasoning truncated") {
		t.Errorf("expected truncation note when log overflows")
	}
	if strings.Contains(prompt, opener) {
		t.Errorf("expected earliest reasoning marker %q to be dropped after truncation", opener)
	}
}

func TestResponseFormatter_TruncatesLargeRawResponse(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}

	// Build a raw response that alone exceeds the cap. The opener marker must
	// be dropped (head truncated) and the trailer marker preserved so the
	// final answer stays inside the budget the LLM sees.
	opener := "RAW-OPENER-MUST-BE-DROPPED"
	filler := strings.Repeat("rawfiller ", 12000)
	trailer := "RAW-TRAILER-MUST-BE-KEPT"
	rawResp := opener + filler + trailer

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), rawResp, "step 1"); got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
	prompt := caller.lastUser
	if len(prompt) > responseFormatterMaxInputBytes {
		t.Errorf("expected user prompt to stay within the input cap; got %d bytes (cap %d)", len(prompt), responseFormatterMaxInputBytes)
	}
	if !strings.Contains(prompt, trailer) {
		t.Errorf("expected raw response trailer %q to survive truncation", trailer)
	}
	if strings.Contains(prompt, opener) {
		t.Errorf("expected raw response opener %q to be dropped after truncation", opener)
	}
	if !strings.Contains(prompt, "earlier response truncated") {
		t.Errorf("expected response-truncation note when raw response overflows the cap")
	}
}

// TestResponseFormatter_DropsReasoningWhenBudgetSmallerThanTruncationNote
// pins the codex finding: when rawResponse leaves a positive but sub-note-sized
// remainder for reasoning, the assembled prompt must still respect maxBytes.
// The earlier implementation appended the full truncationNote anyway, which
// pushed the prompt over the cap.
func TestResponseFormatter_DropsReasoningWhenBudgetSmallerThanTruncationNote(t *testing.T) {
	caller := &fakeOneShotLLMCaller{}

	// Choose a maxBytes / rawResponse pair so 0 < budgetForLog < len(truncationNote).
	// truncationNote = "[... earlier reasoning truncated ...]\n" (38 bytes).
	const maxBytes = 1000
	const truncationNote = "[... earlier reasoning truncated ...]\n"
	overhead := lenFormatterOverheadWithReasoning()
	// Aim for budgetForLog = 10 → rawResponse length = maxBytes - overhead - 10.
	rawLen := maxBytes - overhead - 10
	if rawLen <= 0 {
		t.Fatalf("test setup: maxBytes (%d) too small for overhead (%d)", maxBytes, overhead)
	}
	rawResp := strings.Repeat("a", rawLen)
	fullLog := strings.Repeat("b", 5000)

	prompt := buildFormatterUserPrompt(rawResp, fullLog, maxBytes)
	if len(prompt) > maxBytes {
		t.Errorf("prompt exceeded maxBytes: got %d, cap %d", len(prompt), maxBytes)
	}
	// The reasoning section must be dropped entirely — neither the label nor
	// the note belongs in the output when the remainder cannot even fit the note.
	if strings.Contains(prompt, "--- Full reasoning ---") {
		t.Error("expected reasoning section to be dropped when budget < truncation note")
	}
	if strings.Contains(prompt, truncationNote) {
		t.Error("expected truncation note to be omitted when reasoning section is dropped")
	}
	_ = caller
}

// lenFormatterOverheadWithReasoning mirrors the fixed prompt scaffolding used
// by buildFormatterUserPrompt when reasoning is included. Kept in sync with
// the constants there so the budget arithmetic in tests stays accurate.
func lenFormatterOverheadWithReasoning() int {
	const (
		header         = "Reformat the agent's incident report using the configured output structure. The reasoning trace is provided as supporting context only — do not include it verbatim in the output.\n\n"
		responseLabel  = "--- Raw response ---\n"
		reasoningLabel = "\n\n--- Full reasoning ---\n"
	)
	return len(header) + len(responseLabel) + len(reasoningLabel)
}

func TestResponseFormatter_OmitsReasoningSectionWhenEmpty(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok", nil
	}}

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), "raw only", ""); got != "ok" {
		t.Fatalf("unexpected output: %q", got)
	}
	if strings.Contains(caller.lastUser, "--- Full reasoning ---") {
		t.Errorf("expected no reasoning section when fullLog empty, got: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "raw only") {
		t.Errorf("expected raw response in user prompt, got: %q", caller.lastUser)
	}
}

func TestNewResponseFormatter(t *testing.T) {
	if f := NewResponseFormatter(nil); f == nil {
		t.Fatal("NewResponseFormatter(nil) returned nil")
	} else if f.caller != nil {
		t.Error("expected nil caller when constructed with nil")
	}

	caller := &fakeOneShotLLMCaller{}
	f := NewResponseFormatter(caller)
	if f == nil || f.caller == nil {
		t.Fatal("expected non-nil formatter+caller when constructed with caller")
	}
}
