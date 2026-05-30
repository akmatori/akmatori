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
	if !strings.Contains(got, "*Status:*") {
		t.Errorf("expected rendered output via default prompt, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call when prompt is blank (default applied), got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastSystem, strings.TrimSpace(database.DefaultFormattingPrompt)) {
		t.Errorf("expected blank stored prompt to fall back to DefaultFormattingPrompt, got %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, "Return ONLY a single JSON object") {
		t.Errorf("expected system prompt to contain schema instruction, got %q", caller.lastSystem)
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
	customSchema := `{"severity":"high","summary":"1-3 sentences.","affected_hosts":["host1"]}`
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:             true,
		SystemPrompt:        "You are a strict JSON formatter.",
		MaxTokens:           1234,
		Temperature:         0.4,
		OutputSchemaExample: customSchema,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"severity":"high","summary":"Failover succeeded.","affected_hosts":["web-01","web-02"]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "Raw final answer.", "step 1\nstep 2")
	if !strings.Contains(got, "*Severity:*") {
		t.Errorf("expected rendered Slack output with custom severity field, got %q", got)
	}
	if !strings.Contains(got, "*Summary:*") {
		t.Errorf("expected rendered Slack output with summary section, got %q", got)
	}
	if !strings.Contains(got, "Failover succeeded.") {
		t.Errorf("expected summary text in rendered output, got %q", got)
	}
	if !strings.Contains(got, "*Affected Hosts:*") {
		t.Errorf("expected rendered list heading for affected_hosts, got %q", got)
	}
	if !strings.Contains(got, "web-01") {
		t.Errorf("expected host entry in rendered output, got %q", got)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastSystem, "You are a strict JSON formatter.") {
		t.Errorf("system prompt not forwarded: %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, "Return ONLY a single JSON object") {
		t.Errorf("system prompt missing schema instruction: %q", caller.lastSystem)
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
	if !strings.Contains(got, "*Status:*") {
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
			// Missing "actions_taken" key → validateAgainstSpecs returns an error.
			func(ctx context.Context) (string, error) {
				return `{"status":"resolved","summary":"Good summary.","recommendations":[]}`, nil
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
	if !strings.Contains(got, "*Status:*") {
		t.Errorf("expected rendered output after retry, got %q", got)
	}
	if !strings.Contains(got, "Good summary.") {
		t.Errorf("expected summary in rendered output, got %q", got)
	}
}

func TestParseAndValidateResponse(t *testing.T) {
	specs := []fieldSpec{
		{Name: "status", Kind: "string"},
		{Name: "summary", Kind: "string"},
		{Name: "actions_taken", Kind: "list_string"},
	}

	t.Run("valid JSON passes", func(t *testing.T) {
		parsed, errs := parseAndValidateResponse(`{"status":"resolved","summary":"ok","actions_taken":["a"]}`, specs)
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		if parsed["status"] != "resolved" {
			t.Errorf("unexpected parsed status: %v", parsed["status"])
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, errs := parseAndValidateResponse("not json at all", specs)
		if len(errs) == 0 {
			t.Fatal("expected error for invalid JSON")
		}
		if !strings.Contains(errs[0], "invalid JSON") {
			t.Errorf("expected 'invalid JSON' error, got %v", errs)
		}
	})

	t.Run("fenced JSON is stripped", func(t *testing.T) {
		raw := "```json\n{\"status\":\"resolved\",\"summary\":\"ok\",\"actions_taken\":[]}\n```"
		_, errs := parseAndValidateResponse(raw, specs)
		if len(errs) != 0 {
			t.Fatalf("expected fenced JSON to be accepted after stripping, got errors: %v", errs)
		}
	})

	t.Run("missing required key returns error", func(t *testing.T) {
		_, errs := parseAndValidateResponse(`{"status":"resolved","summary":"ok"}`, specs)
		if len(errs) == 0 {
			t.Fatal("expected error for missing actions_taken")
		}
		found := false
		for _, e := range errs {
			if strings.Contains(e, "actions_taken") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected error mentioning 'actions_taken', got %v", errs)
		}
	})

	t.Run("wrong type returns error", func(t *testing.T) {
		_, errs := parseAndValidateResponse(`{"status":123,"summary":"ok","actions_taken":[]}`, specs)
		if len(errs) == 0 {
			t.Fatal("expected error for wrong type on status")
		}
		if !strings.Contains(errs[0], "status") {
			t.Errorf("expected error mentioning 'status', got %v", errs)
		}
	})

	t.Run("extra keys are tolerated", func(t *testing.T) {
		_, errs := parseAndValidateResponse(`{"status":"ok","summary":"ok","actions_taken":[],"extra_key":"ignored"}`, specs)
		if len(errs) != 0 {
			t.Fatalf("expected extra keys to be tolerated, got errors: %v", errs)
		}
	})
}

func TestResponseFormatter_CustomSchemaHappyPath(t *testing.T) {
	customSchema := `{"severity":"high","summary":"one-liner.","affected_hosts":["host1"]}`
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:             true,
		SystemPrompt:        "You are a JSON formatter.",
		MaxTokens:           1000,
		Temperature:         0.2,
		OutputSchemaExample: customSchema,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"severity":"critical","summary":"Disk full on web-01.","affected_hosts":["web-01","db-02"]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "")
	if !strings.Contains(got, "*Severity:*") {
		t.Errorf("expected custom severity field in output, got %q", got)
	}
	if !strings.Contains(got, "Disk full on web-01.") {
		t.Errorf("expected summary text in output, got %q", got)
	}
	if !strings.Contains(got, "web-01") {
		t.Errorf("expected host list in output, got %q", got)
	}
	// buildSchemaInstruction pretty-prints the schema, so check for a field name from the custom schema.
	if !strings.Contains(caller.lastSystem, `"severity"`) {
		t.Errorf("expected custom schema field 'severity' in system prompt, got %q", caller.lastSystem)
	}
	if strings.Contains(caller.lastSystem, `"status"`) {
		t.Errorf("expected default schema field 'status' to NOT be in system prompt when custom schema is set, got %q", caller.lastSystem)
	}
}

func TestResponseFormatter_EmptySchemaExampleUsesBuiltinDefault(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:             true,
		SystemPrompt:        "You are a formatter.",
		MaxTokens:           1000,
		Temperature:         0.2,
		OutputSchemaExample: "",
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"status":"unresolved","summary":"Still investigating.","actions_taken":["Checked logs"],"recommendations":["Page oncall"]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "")
	if !strings.Contains(got, "*Status:*") {
		t.Errorf("expected status field from built-in schema, got %q", got)
	}
	if !strings.Contains(got, "Still investigating.") {
		t.Errorf("expected summary text in output, got %q", got)
	}
	if !strings.Contains(got, "Checked logs") {
		t.Errorf("expected actions_taken entry in output, got %q", got)
	}
	// buildSchemaInstruction pretty-prints the schema; check for field names from the built-in default.
	if !strings.Contains(caller.lastSystem, `"actions_taken"`) {
		t.Errorf("expected built-in default schema field 'actions_taken' in system prompt, got %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, `"recommendations"`) {
		t.Errorf("expected built-in default schema field 'recommendations' in system prompt, got %q", caller.lastSystem)
	}
}

func TestResponseFormatter_EmptyRenderFallback(t *testing.T) {
	// A schema with only a list field; if the LLM returns an empty array, RenderForSlack
	// returns an empty string which should fall back to rawResponse.
	customSchema := `{"tags":["example"]}`
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:             true,
		SystemPrompt:        "You are a formatter.",
		MaxTokens:           1000,
		Temperature:         0.2,
		OutputSchemaExample: customSchema,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"tags":[]}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "")
	if got != "raw output" {
		t.Errorf("expected rawResponse fallback when render is empty, got %q", got)
	}
}

func TestResponseFormatter_LegacyDefaultPromptTreatedAsDefault(t *testing.T) {
	// Simulate an existing install that has the pre-schema-feature default prompt
	// stored in the DB. When a custom OutputSchemaExample is set, the formatter
	// must replace the stale field-specific guidance with DefaultFormattingPrompt
	// so there is no conflict between the old field names and the new schema.
	legacyPrompt := "You are a senior incident-response writer. Reformat the agent's investigation into a structured incident summary aimed at on-call engineers.\n\nUse the full reasoning trace as context but base the output on the agent's final response. Do not invent facts that are not supported by the trace.\n\nField guidance:\n- Status (\"status\"): one of \"resolved\", \"unresolved\", or \"escalate\" — choose the word that best matches the outcome. Use exactly one of the three values with no additional text.\n- Summary (\"summary\"): 1-3 sentences describing what happened and the suspected root cause. Be factual and concise; preserve specific identifiers (hosts, services, timestamps, error codes).\n- Actions taken (\"actions_taken\"): each entry is one concrete step the agent performed. Use past tense. Omit steps with no observable effect. Empty array is valid.\n- Recommendations (\"recommendations\"): each entry is one actionable next step for a human. Omit if none apply. Empty array is valid.\n\nKeep the tone factual and concise. The JSON output schema is enforced automatically — focus on accurate, useful content."
	customSchema := `{"severity":"high","summary":"one-liner."}`

	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingSettings{
		Enabled:             true,
		SystemPrompt:        legacyPrompt,
		MaxTokens:           1000,
		Temperature:         0.2,
		OutputSchemaExample: customSchema,
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"severity":"critical","summary":"Disk full."}`, nil
	}}

	f := NewResponseFormatter(caller)
	got := f.Format(context.Background(), "raw output", "")
	if got == "raw output" {
		t.Fatalf("expected formatted output, not raw fallback")
	}
	// The system prompt sent to the LLM must use the new schema-agnostic prompt,
	// not the legacy field-specific guidance.
	if strings.Contains(caller.lastSystem, `"status"`) && strings.Contains(caller.lastSystem, "Actions taken") {
		t.Errorf("legacy field guidance leaked into system prompt: %q", caller.lastSystem)
	}
	if !strings.Contains(caller.lastSystem, strings.TrimSpace(database.DefaultFormattingPrompt)) {
		t.Errorf("expected new DefaultFormattingPrompt in system prompt, got %q", caller.lastSystem)
	}
	// Custom schema instruction must still be present.
	if !strings.Contains(caller.lastSystem, `"severity"`) {
		t.Errorf("expected custom schema field 'severity' in system prompt, got %q", caller.lastSystem)
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
