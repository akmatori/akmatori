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
		return `{"status":"resolved","summary":"Default prompt output.","actions_taken":[],"recommendations":[]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if !strings.Contains(got, "Resolved") {
		t.Errorf("expected rendered output via default prompt, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call when prompt is blank (default applied), got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastSystem, strings.TrimSpace(database.DefaultFormattingPrompt)) {
		t.Errorf("expected blank stored prompt to fall back to DefaultFormattingPrompt, got %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, formatterJSONInstruction) {
		t.Errorf("expected system prompt to contain JSON instruction suffix, got %q", caller.lastSystem)
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
		return `{"status":"resolved","summary":"Failover succeeded.","actions_taken":["Restarted service"],"recommendations":["Monitor logs"]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "Raw final answer.", "step 1\nstep 2")
	if !strings.Contains(got, "*Resolved*") {
		t.Errorf("expected rendered Slack output with status, got %q", got)
	}
	if !strings.Contains(got, "*Summary*") {
		t.Errorf("expected rendered Slack output with summary section, got %q", got)
	}
	if !strings.Contains(got, "Failover succeeded.") {
		t.Errorf("expected summary text in rendered output, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastSystem, "You are a strict JSON formatter.") {
		t.Errorf("system prompt not forwarded: %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, formatterJSONInstruction) {
		t.Errorf("system prompt missing JSON instruction suffix: %q", caller.lastSystem)
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
		return `{"status":"resolved","summary":"formatted","actions_taken":[],"recommendations":[]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw", "log")
	if got == "" || got == "raw" {
		t.Errorf("expected rendered formatted output, got %q", got)
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
		return `{"status":"resolved","summary":"ok","actions_taken":[],"recommendations":[]}`, nil
	}}

	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
	defer cancel()

	f := NewResponseFormatter(caller)
	if got := f.Format(parent, "raw", "log"); got == "" || got == "raw" {
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
		return `{"status":"resolved","summary":"ok","actions_taken":[],"recommendations":[]}`, nil
	}}

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), "raw", "log"); got == "" || got == "raw" {
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
		return `{"status":"resolved","summary":"truncation test","actions_taken":[],"recommendations":[]}`, nil
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
	if got := f.Format(context.Background(), rawResp, fullLog); got == "" || got == rawResp {
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
		return `{"status":"resolved","summary":"raw truncation test","actions_taken":[],"recommendations":[]}`, nil
	}}

	// Build a raw response that alone exceeds the cap. The opener marker must
	// be dropped (head truncated) and the trailer marker preserved so the
	// final answer stays inside the budget the LLM sees.
	opener := "RAW-OPENER-MUST-BE-DROPPED"
	filler := strings.Repeat("rawfiller ", 12000)
	trailer := "RAW-TRAILER-MUST-BE-KEPT"
	rawResp := opener + filler + trailer

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), rawResp, "step 1"); got == "" {
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
		return `{"status":"resolved","summary":"ok","actions_taken":[],"recommendations":[]}`, nil
	}}

	f := NewResponseFormatter(caller)
	if got := f.Format(context.Background(), "raw only", ""); got == "" || got == "raw only" {
		t.Fatalf("unexpected output: %q", got)
	}
	if strings.Contains(caller.lastUser, "--- Full reasoning ---") {
		t.Errorf("expected no reasoning section when fullLog empty, got: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "raw only") {
		t.Errorf("expected raw response in user prompt, got: %q", caller.lastUser)
	}
}

func TestResponseFormatter_RetryOnValidationFailure(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{
		responses: []func(ctx context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return "not valid json at all", nil
			},
			func(ctx context.Context) (string, error) {
				return `{"status":"resolved","summary":"Fixed after retry.","actions_taken":[],"recommendations":[]}`, nil
			},
		},
	}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if caller.callCount() != 2 {
		t.Fatalf("expected 2 LLM calls (initial + retry), got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastUser, "validation errors") {
		t.Errorf("expected retry user prompt to contain 'validation errors', got %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "Return only corrected JSON") {
		t.Errorf("expected retry user prompt to contain corrective instruction, got %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "raw output") {
		t.Errorf("expected retry user prompt to contain original raw response, got %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "not valid json at all") {
		t.Errorf("expected retry user prompt to include the failed first response, got %q", caller.lastUser)
	}
	if !strings.Contains(got, "Resolved") {
		t.Errorf("expected rendered Slack output after successful retry, got %q", got)
	}
	if !strings.Contains(got, "Fixed after retry.") {
		t.Errorf("expected summary text in rendered output, got %q", got)
	}
}

func TestResponseFormatter_FallbackWhenRetryReturnsEmpty(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{
		responses: []func(ctx context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return "not valid json", nil
			},
			func(ctx context.Context) (string, error) {
				return "   ", nil
			},
		},
	}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if caller.callCount() != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", caller.callCount())
	}
	if got != "raw output" {
		t.Errorf("expected raw fallback when retry returns empty, got %q", got)
	}
}

func TestResponseFormatter_FallbackAfterTwoValidationFailures(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{
		responses: []func(ctx context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return "still not json", nil
			},
			func(ctx context.Context) (string, error) {
				return "also not json", nil
			},
		},
	}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if caller.callCount() != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", caller.callCount())
	}
	if got != "raw output" {
		t.Errorf("expected raw fallback after two validation failures, got %q", got)
	}
}

func TestResponseFormatter_FallbackOnRetryCallError(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{
		responses: []func(ctx context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return "not json", nil
			},
			func(ctx context.Context) (string, error) {
				return "", errors.New("boom")
			},
		},
	}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if caller.callCount() != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", caller.callCount())
	}
	if got != "raw output" {
		t.Errorf("expected raw fallback on retry error, got %q", got)
	}
}

func TestResponseFormatter_MissingRequiredFieldTriggersRetry(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{Enabled: true, SystemPrompt: "Reformat.", MaxTokens: 1000, Temperature: 0.2})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{
		responses: []func(ctx context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return `{"status":"resolved","summary":"","actions_taken":[],"recommendations":[]}`, nil
			},
			func(ctx context.Context) (string, error) {
				return `{"status":"resolved","summary":"Good summary.","actions_taken":[],"recommendations":[]}`, nil
			},
		},
	}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "full log")
	if caller.callCount() != 2 {
		t.Fatalf("expected 2 LLM calls (missing field triggers retry), got %d", caller.callCount())
	}
	if !strings.Contains(got, "Resolved") {
		t.Errorf("expected rendered output after retry, got %q", got)
	}
	if !strings.Contains(got, "Good summary.") {
		t.Errorf("expected summary in rendered output, got %q", got)
	}
}

func TestValidateFormatterResult(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantNil  bool
		wantErrs []string
	}{
		{
			name: "valid JSON",
			raw:  `{"status":"resolved","summary":"All good.","actions_taken":["action1"],"recommendations":["rec1"]}`,
		},
		{
			name:     "invalid JSON",
			raw:      "not json at all",
			wantNil:  true,
			wantErrs: []string{"invalid JSON"},
		},
		{
			name: "fenced JSON stripped",
			raw:  "```json\n{\"status\":\"resolved\",\"summary\":\"ok\",\"actions_taken\":[],\"recommendations\":[]}\n```",
		},
		{
			name:     "missing status",
			raw:      `{"status":"","summary":"ok","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"status" must be a non-empty string`},
		},
		{
			name:     "missing summary",
			raw:      `{"status":"resolved","summary":"","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"summary" must be a non-empty string`},
		},
		{
			name: "empty arrays ok",
			raw:  `{"status":"resolved","summary":"all good","actions_taken":[],"recommendations":[]}`,
		},
		{
			name:     "completely empty string",
			raw:      "",
			wantNil:  true,
			wantErrs: []string{"invalid JSON"},
		},
		{
			name:     "whitespace-only status",
			raw:      `{"status":"   ","summary":"ok","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"status" must be a non-empty string`},
		},
		{
			name:     "both required fields empty",
			raw:      `{"status":"","summary":"","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"status" must be a non-empty string`, `"summary" must be a non-empty string`},
		},
		{
			name: "fenced JSON with trailing text stripped",
			raw:  "```json\n{\"status\":\"resolved\",\"summary\":\"ok\",\"actions_taken\":[],\"recommendations\":[]}\n```\nSome trailing comment.",
		},
		{
			name:     "invalid status enum value",
			raw:      `{"status":"escalated","summary":"ok","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"status" must be one of`},
		},
		{
			name:     "status with trailing period fails enum check",
			raw:      `{"status":"resolved.","summary":"ok","actions_taken":[],"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"status" must be one of`},
		},
		{
			name: "status enum check is case-insensitive",
			raw:  `{"status":"Resolved","summary":"ok","actions_taken":[],"recommendations":[]}`,
		},
		{
			name:     "missing actions_taken",
			raw:      `{"status":"resolved","summary":"ok","recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"actions_taken" must be a JSON array`},
		},
		{
			name:     "null actions_taken",
			raw:      `{"status":"resolved","summary":"ok","actions_taken":null,"recommendations":[]}`,
			wantNil:  true,
			wantErrs: []string{`"actions_taken" must be a JSON array`},
		},
		{
			name:     "missing recommendations",
			raw:      `{"status":"resolved","summary":"ok","actions_taken":[]}`,
			wantNil:  true,
			wantErrs: []string{`"recommendations" must be a JSON array`},
		},
		{
			name:     "null recommendations",
			raw:      `{"status":"resolved","summary":"ok","actions_taken":[],"recommendations":null}`,
			wantNil:  true,
			wantErrs: []string{`"recommendations" must be a JSON array`},
		},
		{
			name:     "missing both list fields",
			raw:      `{"status":"resolved","summary":"ok"}`,
			wantNil:  true,
			wantErrs: []string{`"actions_taken" must be a JSON array`, `"recommendations" must be a JSON array`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, errs := validateFormatterResult(tt.raw)
			if tt.wantNil {
				if r != nil {
					t.Errorf("expected nil result, got %+v", r)
				}
				if len(errs) == 0 {
					t.Error("expected validation errors, got none")
				}
				for _, wantErr := range tt.wantErrs {
					found := false
					for _, e := range errs {
						if strings.Contains(e, wantErr) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got %v", wantErr, errs)
					}
				}
			} else {
				if r == nil {
					t.Errorf("expected non-nil result, got nil with errors: %v", errs)
				}
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %v", errs)
				}
			}
		})
	}
}

func TestRenderFormatterResult(t *testing.T) {
	t.Run("nil input returns empty string", func(t *testing.T) {
		got := renderFormatterResult(nil)
		if got != "" {
			t.Errorf("expected empty string for nil input, got %q", got)
		}
	})

	t.Run("valid input returns rendered output with status and summary", func(t *testing.T) {
		actionsTaken := []string{"Restarted pod"}
		recommendations := []string{"Monitor logs"}
		r := &formatterResult{
			Status:          "resolved",
			Summary:         "The incident was resolved successfully.",
			ActionsTaken:    &actionsTaken,
			Recommendations: &recommendations,
		}
		got := renderFormatterResult(r)
		if got == "" {
			t.Fatal("expected non-empty rendered output for valid input")
		}
		if !strings.Contains(got, "Resolved") {
			t.Errorf("expected status in rendered output, got %q", got)
		}
		if !strings.Contains(got, "The incident was resolved successfully.") {
			t.Errorf("expected summary in rendered output, got %q", got)
		}
	})
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
