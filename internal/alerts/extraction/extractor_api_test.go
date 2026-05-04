package extraction

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// fakeOneShotLLMCaller is a configurable test double for OneShotLLMCaller.
type fakeOneShotLLMCaller struct {
	calls      int32
	lastSystem string
	lastUser   string
	lastMaxTok int
	lastTemp   float64
	lastLLM    *services.LLMSettingsForWorker
	respond    func(ctx context.Context) (string, error)
}

func (f *fakeOneShotLLMCaller) OneShotLLM(ctx context.Context, llm *services.LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	f.lastSystem = system
	f.lastUser = user
	f.lastMaxTok = maxTokens
	f.lastTemp = temperature
	f.lastLLM = llm
	if f.respond == nil {
		return "", nil
	}
	return f.respond(ctx)
}

func (f *fakeOneShotLLMCaller) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
}

func TestExtract_LLMSettingsError(t *testing.T) {
	caller := &fakeOneShotLLMCaller{}
	settingsErr := errors.New("database error")

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return nil, settingsErr
	})

	alert, err := extractor.Extract(context.Background(), "Test alert message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert == nil {
		t.Fatal("Expected fallback alert, got nil")
	}
	if alert.AlertName != "Test alert message" {
		t.Errorf("Expected fallback alert name, got %q", alert.AlertName)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected extraction_mode to be fallback")
	}
	if caller.callCount() != 0 {
		t.Errorf("Expected no LLM calls, got %d", caller.callCount())
	}
}

func TestExtract_NoAPIKey(t *testing.T) {
	caller := &fakeOneShotLLMCaller{}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback when no API key")
	}
	if caller.callCount() != 0 {
		t.Errorf("Expected no LLM calls, got %d", caller.callCount())
	}
}

func TestExtract_NilCaller(t *testing.T) {
	extractor := NewAlertExtractorWithDeps(nil, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback when caller is nil")
	}
}

func TestExtract_CallerError(t *testing.T) {
	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return "", errors.New("network error")
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on caller error")
	}
}

func TestExtract_WorkerNotConnected(t *testing.T) {
	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return "", services.ErrWorkerNotConnected
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on ErrWorkerNotConnected")
	}
}

func TestExtract_EmptyResponse(t *testing.T) {
	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return "   ", nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on empty caller response")
	}
}

func TestExtract_InvalidJSON(t *testing.T) {
	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return "this is not json", nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on invalid JSON response")
	}
}

func TestExtract_SuccessfulExtraction(t *testing.T) {
	extractedJSON := `{
		"alert_name": "High CPU Usage",
		"severity": "critical",
		"status": "firing",
		"summary": "CPU at 95%",
		"description": "Production server experiencing high CPU usage",
		"target_host": "prod-web-01",
		"target_service": "web-api",
		"source_system": "Prometheus"
	}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "CPU is at 95% on prod-web-01")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "High CPU Usage" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "High CPU Usage")
	}
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Severity = %v, want %v", alert.Severity, database.AlertSeverityCritical)
	}
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Status = %v, want %v", alert.Status, database.AlertStatusFiring)
	}
	if alert.TargetHost != "prod-web-01" {
		t.Errorf("TargetHost = %q, want %q", alert.TargetHost, "prod-web-01")
	}
	if alert.TargetService != "web-api" {
		t.Errorf("TargetService = %q, want %q", alert.TargetService, "web-api")
	}

	if caller.lastMaxTok != 500 {
		t.Errorf("Expected max_tokens = 500, got %d", caller.lastMaxTok)
	}
	if caller.lastTemp != 0.1 {
		t.Errorf("Expected temperature = 0.1, got %v", caller.lastTemp)
	}
	if caller.lastLLM == nil {
		t.Fatal("Expected forwarded LLM settings, got nil")
	}
	if caller.lastLLM.APIKey != "test-key" {
		t.Errorf("Forwarded API key = %q, want %q", caller.lastLLM.APIKey, "test-key")
	}
}

func TestExtract_NonOpenAIProviderRoundTrips(t *testing.T) {
	extractedJSON := `{
		"alert_name": "Anthropic-Routed Alert",
		"severity": "high",
		"status": "firing"
	}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderAnthropic,
			Model:    "claude-sonnet-4",
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Some alert text")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] == "fallback" {
		t.Fatal("Expected non-OpenAI provider to round-trip through caller, not fall back")
	}
	if alert.AlertName != "Anthropic-Routed Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Anthropic-Routed Alert")
	}
	if caller.callCount() != 1 {
		t.Errorf("Expected 1 caller call, got %d", caller.callCount())
	}
	if caller.lastLLM == nil || caller.lastLLM.Provider != string(database.LLMProviderAnthropic) {
		t.Errorf("Expected provider 'anthropic' to be forwarded, got %+v", caller.lastLLM)
	}
}

func TestExtract_JSONWithCodeBlock(t *testing.T) {
	extractedJSON := "```json\n" + `{
		"alert_name": "Memory Alert",
		"severity": "warning",
		"status": "firing"
	}` + "\n```"

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Memory is high")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Memory Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Memory Alert")
	}
}

func TestExtract_ResolvedStatus(t *testing.T) {
	extractedJSON := `{
		"alert_name": "Issue Resolved",
		"severity": "info",
		"status": "resolved"
	}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Issue is now resolved")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.Status != database.AlertStatusResolved {
		t.Errorf("Status = %v, want %v", alert.Status, database.AlertStatusResolved)
	}
}

func TestExtractWithPrompt_CustomPrompt(t *testing.T) {
	extractedJSON := `{
		"alert_name": "Custom Alert",
		"severity": "high"
	}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	customPrompt := "Extract with custom instructions: %s"
	alert, err := extractor.ExtractWithPrompt(context.Background(), "Test message", customPrompt)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Custom Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Custom Alert")
	}
	if !strings.Contains(caller.lastUser, "custom instructions") {
		t.Errorf("Expected custom prompt to be used, got %q", caller.lastUser)
	}
}

func TestExtract_LongMessageTruncation(t *testing.T) {
	longMessage := strings.Repeat("x", 5000)

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return `{"alert_name": "Long Test"}`, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	_, _ = extractor.Extract(context.Background(), longMessage)

	if caller.callCount() != 1 {
		t.Fatalf("Expected 1 call, got %d", caller.callCount())
	}

	// The 5000 char message should be truncated, not appear in full in user prompt
	if strings.Count(caller.lastUser, "x") >= 5000 {
		t.Error("Expected long message to be truncated")
	}
}

func TestExtract_EmptyProviderTreatsAsActiveSettings(t *testing.T) {
	// Settings with empty provider — BuildLLMSettingsForWorker requires the
	// settings to be active (Enabled + Active) to forward them.
	extractedJSON := `{"alert_name": "Empty Provider Test"}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: "", // Empty provider
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Empty Provider Test" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Empty Provider Test")
	}
	if caller.callCount() != 1 {
		t.Errorf("Expected exactly 1 caller call, got %d", caller.callCount())
	}
}

func TestExtract_InactiveSettingsFallback(t *testing.T) {
	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			t.Fatal("caller must not be invoked when LLM settings are inactive")
			return "", nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  false,
			Active:   false,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback when LLM settings inactive")
	}
}

// TestExtract_ContextCancellation tests that context cancellation is respected
func TestExtract_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return "", context.Canceled
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	alert, err := extractor.Extract(ctx, "Test")

	// Should return fallback, not propagate error
	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on context cancellation")
	}
}

// Benchmark for Extract with mocked dependencies
func BenchmarkExtract_WithMock(b *testing.B) {
	extractedJSON := `{
		"alert_name": "Benchmark Alert",
		"severity": "warning",
		"status": "firing",
		"summary": "Test summary"
	}`

	caller := &fakeOneShotLLMCaller{
		respond: func(ctx context.Context) (string, error) {
			return extractedJSON, nil
		},
	}

	extractor := NewAlertExtractorWithDeps(caller, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
			Enabled:  true,
			Active:   true,
		}, nil
	})

	ctx := context.Background()
	msg := "Production server CPU at 95%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = extractor.Extract(ctx, msg)
	}
}
