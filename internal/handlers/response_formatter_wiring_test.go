package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeFormatterCaller is a stub OneShotLLMCaller used to drive the
// ResponseFormatter from the handler-level wiring tests below.
type fakeFormatterCaller struct {
	calls    int
	respond  func() (string, error)
	lastUser string
}

func (f *fakeFormatterCaller) OneShotLLM(ctx context.Context, llm *services.LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	f.calls++
	f.lastUser = user
	if f.respond == nil {
		return "", nil
	}
	return f.respond()
}

func setupFormatterWiringDB(t *testing.T) func() {
	t.Helper()
	prev := database.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}, &database.FormattingSettings{}); err != nil {
		t.Fatalf("migrate sqlite db: %v", err)
	}
	database.DB = db
	return func() { database.DB = prev }
}

func seedFormatterWiringLLM(t *testing.T) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
		t.Fatalf("clear llm_settings: %v", err)
	}
	if err := database.DB.Create(&database.LLMSettings{
		Name:     "active",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-test",
		Model:    "claude-sonnet-4",
		Enabled:  true,
		Active:   true,
	}).Error; err != nil {
		t.Fatalf("seed llm_settings: %v", err)
	}
}

func seedFormatterWiringSettings(t *testing.T, fs database.FormattingSettings) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM formatting_settings").Error; err != nil {
		t.Fatalf("clear formatting_settings: %v", err)
	}
	if fs.SingletonKey == "" {
		fs.SingletonKey = "default"
	}
	if err := database.DB.Create(&fs).Error; err != nil {
		t.Fatalf("seed formatting_settings: %v", err)
	}
}

// TestApplyResponseFormatter_SkipsOnError verifies that the wiring helper
// passes through the raw response when the agent reported an error, so we
// never feed error messages to the LLM formatter.
func TestApplyResponseFormatter_SkipsOnError(t *testing.T) {
	caller := &fakeFormatterCaller{respond: func() (string, error) {
		t.Fatal("formatter must not be invoked when hasError=true")
		return "", nil
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, true, "❌ Error: agent crashed", "reasoning")
	if got != "❌ Error: agent crashed" {
		t.Errorf("expected error response unchanged, got %q", got)
	}
	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls on error path, got %d", caller.calls)
	}
}

// TestApplyResponseFormatter_SkipsOnEmpty verifies that the helper does
// not call the LLM when there is no response to format.
func TestApplyResponseFormatter_SkipsOnEmpty(t *testing.T) {
	caller := &fakeFormatterCaller{respond: func() (string, error) {
		t.Fatal("formatter must not be invoked when response is empty")
		return "", nil
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, false, "", "reasoning")
	if got != "" {
		t.Errorf("expected empty response unchanged, got %q", got)
	}
	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls on empty path, got %d", caller.calls)
	}
}

// TestApplyResponseFormatter_NilFormatterPassthrough verifies that the
// helper is safe to call with a nil formatter (early-startup state) — it
// returns the raw response unchanged.
func TestApplyResponseFormatter_NilFormatterPassthrough(t *testing.T) {
	got := applyResponseFormatter(context.Background(), nil, false, "raw response", "reasoning")
	if got != "raw response" {
		t.Errorf("expected raw response unchanged when formatter is nil, got %q", got)
	}
}

// TestApplyResponseFormatter_DisabledPassthrough verifies that when the
// FormattingSettings.Enabled flag is false the helper returns the raw
// response without invoking the LLM.
func TestApplyResponseFormatter_DisabledPassthrough(t *testing.T) {
	cleanup := setupFormatterWiringDB(t)
	defer cleanup()
	seedFormatterWiringLLM(t)
	seedFormatterWiringSettings(t, database.FormattingSettings{Enabled: false, SystemPrompt: "Reformat", MaxTokens: 1000, Temperature: 0.2})

	caller := &fakeFormatterCaller{respond: func() (string, error) {
		t.Fatal("formatter must not be invoked when settings disabled")
		return "", nil
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, false, "raw response", "reasoning trace")
	if got != "raw response" {
		t.Errorf("expected passthrough on disabled settings, got %q", got)
	}
	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls when disabled, got %d", caller.calls)
	}
}

// TestApplyResponseFormatter_AppliedHappyPath verifies that when settings
// are enabled and the LLM responds successfully, the formatted output
// replaces the raw response and the reasoning trace is forwarded as the
// supporting context.
func TestApplyResponseFormatter_AppliedHappyPath(t *testing.T) {
	cleanup := setupFormatterWiringDB(t)
	defer cleanup()
	seedFormatterWiringLLM(t)
	seedFormatterWiringSettings(t, database.FormattingSettings{
		Enabled:      true,
		SystemPrompt: "Reformat as JSON.",
		MaxTokens:    1500,
		Temperature:  0.2,
	})

	caller := &fakeFormatterCaller{respond: func() (string, error) {
		return `{"status":"resolved","summary":"All clear."}`, nil
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, false, "Investigation finished. No issues.", "step 1\nstep 2")
	if got != `{"status":"resolved","summary":"All clear."}` {
		t.Errorf("expected formatted output, got %q", got)
	}
	if caller.calls != 1 {
		t.Errorf("expected exactly 1 LLM call, got %d", caller.calls)
	}
	if !strings.Contains(caller.lastUser, "Investigation finished. No issues.") {
		t.Errorf("user prompt missing raw response: %q", caller.lastUser)
	}
	if !strings.Contains(caller.lastUser, "step 1") {
		t.Errorf("user prompt missing reasoning trace: %q", caller.lastUser)
	}
}

// TestApplyResponseFormatter_FallbackOnCallerError verifies that when the
// LLM call errors out, the helper falls back to the raw response so
// finalization is never blocked by formatter problems.
func TestApplyResponseFormatter_FallbackOnCallerError(t *testing.T) {
	cleanup := setupFormatterWiringDB(t)
	defer cleanup()
	seedFormatterWiringLLM(t)
	seedFormatterWiringSettings(t, database.FormattingSettings{
		Enabled:      true,
		SystemPrompt: "Reformat",
		MaxTokens:    1500,
		Temperature:  0.2,
	})

	caller := &fakeFormatterCaller{respond: func() (string, error) {
		return "", errors.New("transient LLM failure")
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, false, "Investigation completed.", "reasoning")
	if got != "Investigation completed." {
		t.Errorf("expected raw response on caller error, got %q", got)
	}
	if caller.calls != 1 {
		t.Errorf("expected 1 LLM call attempt before fallback, got %d", caller.calls)
	}
}

// TestApplyResponseFormatter_WorkerNotConnectedFallback verifies that when
// the worker is offline the helper falls back to the raw response without
// blocking the incident finalization path.
func TestApplyResponseFormatter_WorkerNotConnectedFallback(t *testing.T) {
	cleanup := setupFormatterWiringDB(t)
	defer cleanup()
	seedFormatterWiringLLM(t)
	seedFormatterWiringSettings(t, database.FormattingSettings{
		Enabled:      true,
		SystemPrompt: "Reformat",
		MaxTokens:    1500,
		Temperature:  0.2,
	})

	caller := &fakeFormatterCaller{respond: func() (string, error) {
		return "", services.ErrWorkerNotConnected
	}}
	formatter := services.NewResponseFormatter(caller)

	got := applyResponseFormatter(context.Background(), formatter, false, "Done.", "reasoning")
	if got != "Done." {
		t.Errorf("expected raw response when worker disconnected, got %q", got)
	}
}

// TestSlackHandler_SetResponseFormatter verifies that the setter wires
// the formatter onto the SlackHandler so the three downstream call sites
// pick it up.
func TestSlackHandler_SetResponseFormatter(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)
	if h.responseFormatter != nil {
		t.Fatal("expected responseFormatter to start nil before SetResponseFormatter")
	}

	formatter := services.NewResponseFormatter(&fakeFormatterCaller{})
	h.SetResponseFormatter(formatter)
	if h.responseFormatter != formatter {
		t.Errorf("SetResponseFormatter did not wire the formatter onto the handler")
	}
}

// TestAlertHandler_SetResponseFormatter verifies that the setter wires
// the formatter onto the AlertHandler so runInvestigation and
// runSlackChannelInvestigation pick it up.
func TestAlertHandler_SetResponseFormatter(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	if h.responseFormatter != nil {
		t.Fatal("expected responseFormatter to start nil before SetResponseFormatter")
	}

	formatter := services.NewResponseFormatter(&fakeFormatterCaller{})
	h.SetResponseFormatter(formatter)
	if h.responseFormatter != formatter {
		t.Errorf("SetResponseFormatter did not wire the formatter onto the handler")
	}
}
