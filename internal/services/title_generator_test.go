package services

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeOneShotLLMCaller is a configurable test double for OneShotLLMCaller.
type fakeOneShotLLMCaller struct {
	calls      int32
	lastSystem string
	lastUser   string
	lastMaxTok int
	lastTemp   float64
	lastLLM    *LLMSettingsForWorker
	respond    func(ctx context.Context) (string, error)
	// responses provides per-call responses indexed by call order (0-based).
	// When the call index is within bounds, responses[idx] is used. When
	// exhausted or nil, respond is used as the fallback.
	responses   []func(ctx context.Context) (string, error)
	contextSeen context.Context
}

func (f *fakeOneShotLLMCaller) OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	idx := int(atomic.AddInt32(&f.calls, 1)) - 1
	f.lastSystem = system
	f.lastUser = user
	f.lastMaxTok = maxTokens
	f.lastTemp = temperature
	f.lastLLM = llm
	f.contextSeen = ctx
	if len(f.responses) > 0 && idx < len(f.responses) {
		return f.responses[idx](ctx)
	}
	if f.respond == nil {
		return "", nil
	}
	return f.respond(ctx)
}

func (f *fakeOneShotLLMCaller) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
}

func TestTitleGenerator_GenerateFallbackTitle(t *testing.T) {
	gen := NewTitleGenerator(nil)

	tests := []struct {
		name     string
		message  string
		source   string
		expected string
	}{
		{name: "simple message", message: "Server is down", source: "Slack", expected: "Server is down"},
		{name: "empty message", message: "", source: "PagerDuty", expected: "Incident from PagerDuty"},
		{name: "whitespace only message", message: "   \n\t  ", source: "Zabbix", expected: "Incident from Zabbix"},
		{name: "Alert: prefix", message: "Alert: CPU usage critical", source: "Prometheus", expected: "CPU usage critical"},
		{name: "alert: lowercase prefix", message: "alert: Disk space low", source: "Grafana", expected: "Disk space low"},
		{name: "Incident: prefix", message: "Incident: Database connection failure", source: "Datadog", expected: "Database connection failure"},
		{name: "incident: lowercase prefix", message: "incident: API gateway timeout", source: "OpsGenie", expected: "API gateway timeout"},
		{name: "multiline first line only", message: "First line title\nSecond line details\nThird line", source: "Slack", expected: "First line title"},
		{name: "long message under new cap kept whole", message: "This is a very long alert title that needs to be truncated because it exceeds the maximum allowed length for titles", source: "Alertmanager", expected: "This is a very long alert title that needs to be truncated because it exceeds the maximum allowed length for titles"},
		{name: "long message no good boundary", message: strings.Repeat("x", 210), source: "Custom", expected: strings.Repeat("x", 197) + "..."},
		{name: "exactly 200 chars", message: strings.Repeat("a", 200), source: "Test", expected: strings.Repeat("a", 200)},
		{name: "201 chars truncated", message: strings.Repeat("a", 201), source: "Test", expected: strings.Repeat("a", 197) + "..."},
		{name: "multiline with prefix", message: "Alert: Server outage\nDetails: Production cluster\nTime: 10:30 UTC", source: "Slack", expected: "Server outage"},
		{name: "leading/trailing whitespace", message: "  Important alert  ", source: "Test", expected: "Important alert"},
		{name: "double prefix only first removed", message: "Alert: Incident: Double prefix", source: "Test", expected: "Incident: Double prefix"},
		{name: "Unicode characters", message: "服务器警报: CPU过高", source: "Monitoring", expected: "服务器警报: CPU过高"},
		{name: "emoji in message", message: "🚨 Critical: Production down", source: "Slack", expected: "🚨 Critical: Production down"},
		{name: "long unicode truncates on rune boundary", message: strings.Repeat("日", 210), source: "Webhook", expected: strings.Repeat("日", 197) + "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gen.GenerateFallbackTitle(tt.message, tt.source)
			if result != tt.expected {
				t.Errorf("GenerateFallbackTitle(%q, %q) = %q, want %q", tt.message, tt.source, result, tt.expected)
			}
		})
	}
}

func TestTitleGenerator_GenerateFallbackTitle_LongUnicodeIsValidUTF8(t *testing.T) {
	gen := NewTitleGenerator(nil)

	got := gen.GenerateFallbackTitle(strings.Repeat("日", 210), "Webhook")
	if !utf8.ValidString(got) {
		t.Fatalf("GenerateFallbackTitle returned invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) > 200 {
		t.Fatalf("GenerateFallbackTitle returned %d runes, want <= 200", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("GenerateFallbackTitle() = %q, want ellipsis suffix", got)
	}
}

func TestTruncateForPrompt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{name: "short string", input: "hello", maxLen: 10, expected: "hello"},
		{name: "exact length", input: "hello", maxLen: 5, expected: "hello"},
		{name: "long string truncated", input: "hello world", maxLen: 8, expected: "hello..."},
		{name: "empty string", input: "", maxLen: 10, expected: ""},
		{name: "maxLen 3 edge case", input: "hello", maxLen: 3, expected: "..."},
		{name: "maxLen 4", input: "hello world", maxLen: 4, expected: "h..."},
		{name: "unicode truncation", input: "你好世界", maxLen: 3, expected: "..."},
		{name: "long unicode remains valid", input: strings.Repeat("日", 12), maxLen: 10, expected: strings.Repeat("日", 7) + "..."},
		{name: "very long string", input: strings.Repeat("a", 5000), maxLen: 100, expected: strings.Repeat("a", 97) + "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateForPrompt(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateForPrompt(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
			if !utf8.ValidString(result) {
				t.Errorf("truncateForPrompt(%q, %d) returned invalid UTF-8: %q", tt.input, tt.maxLen, result)
			}
		})
	}
}

func TestNewTitleGenerator(t *testing.T) {
	gen := NewTitleGenerator(nil)
	if gen == nil {
		t.Fatal("NewTitleGenerator() returned nil")
	}
	if gen.caller != nil {
		t.Error("expected nil caller when constructed with nil")
	}

	caller := &fakeOneShotLLMCaller{}
	gen2 := NewTitleGenerator(caller)
	if gen2.caller == nil {
		t.Error("expected non-nil caller when constructed with caller")
	}
}

func setupTitleGeneratorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate llm_settings: %v", err)
	}
	database.DB = db
	return db
}

func TestTitleGenerator_GenerateTitle(t *testing.T) {
	setupTitleGeneratorTestDB(t)

	seedSettings := func(t *testing.T, settings database.LLMSettings) {
		t.Helper()
		if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
			t.Fatalf("clear llm_settings: %v", err)
		}
		if err := database.DB.Create(&settings).Error; err != nil {
			t.Fatalf("seed llm_settings: %v", err)
		}
	}

	tests := []struct {
		name             string
		message          string
		source           string
		settings         database.LLMSettings
		caller           *fakeOneShotLLMCaller
		nilCaller        bool
		want             string
		wantCallerCalled bool
	}{
		{
			name:    "short message uses fallback without database lookup",
			message: "too short",
			source:  "Slack",
			caller:  &fakeOneShotLLMCaller{},
			want:    "too short",
		},
		{
			name:      "nil caller falls back",
			message:   "The database connection pool is saturated and requests are timing out for multiple users.",
			source:    "Slack",
			settings:  database.LLMSettings{Name: "openai", Provider: database.LLMProviderOpenAI, APIKey: "test-key", Enabled: true, Active: true},
			nilCaller: true,
			want:      "The database connection pool is saturated and requests are timing out for multiple users.",
		},
		{
			name:    "missing api key falls back",
			message: "The database connection pool is saturated and requests are timing out for multiple users.",
			source:  "PagerDuty",
			settings: database.LLMSettings{
				Name:     "openai-empty-key",
				Provider: database.LLMProviderOpenAI,
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				t.Fatal("caller must not be invoked when API key is empty")
				return "", nil
			}},
			want: "The database connection pool is saturated and requests are timing out for multiple users.",
		},
		{
			name:    "non-openai provider round-trips through caller (no fallback)",
			message: "Customer reported that runbook execution is stuck while waiting for a tool result.",
			source:  "Slack",
			settings: database.LLMSettings{
				Name:     "anthropic",
				Provider: database.LLMProviderAnthropic,
				APIKey:   "test-key",
				Model:    "claude-sonnet-4",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return "Runbook execution stalled on tool result", nil
			}},
			want:             "Runbook execution stalled on tool result",
			wantCallerCalled: true,
		},
		{
			name:    "caller error falls back",
			message: "HTTP connector deployment failed because the upstream returned repeated 503 responses.",
			source:  "Alertmanager",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return "", errors.New("boom")
			}},
			want:             "HTTP connector deployment failed because the upstream returned repeated 503 responses.",
			wantCallerCalled: true,
		},
		{
			name:    "ErrWorkerNotConnected falls back",
			message: "The agent worker disconnected mid-incident which left the dispatcher unable to react.",
			source:  "API",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return "", ErrWorkerNotConnected
			}},
			want:             "The agent worker disconnected mid-incident which left the dispatcher unable to react.",
			wantCallerCalled: true,
		},
		{
			name:    "empty caller response falls back",
			message: "A customer webhook produced malformed JSON and the parser rejected the payload before routing.",
			source:  "Webhook",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return "   ", nil
			}},
			want:             "A customer webhook produced malformed JSON and the parser rejected the payload before routing.",
			wantCallerCalled: true,
		},
		{
			name:    "successful response trims surrounding quotes",
			message: "Production alerting latency increased after a queue backlog built up in the dispatcher.",
			source:  "Slack",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return "\"Dispatcher backlog increased alert latency\"", nil
			}},
			want:             "Dispatcher backlog increased alert latency",
			wantCallerCalled: true,
		},
		{
			name:    "successful response truncates long title",
			message: "The monitoring pipeline kept duplicating the same alert payload as retries piled up across regions.",
			source:  "Grafana",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return strings.Repeat("x", 260), nil
			}},
			want:             strings.Repeat("x", 252) + "...",
			wantCallerCalled: true,
		},
		{
			name:    "successful unicode response truncates on rune boundary",
			message: "The LLM returned a long unicode title that still needs to be safe for storage and UI rendering.",
			source:  "Slack",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			caller: &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
				return strings.Repeat("日", 260), nil
			}},
			want:             strings.Repeat("日", 252) + "...",
			wantCallerCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.settings.Name != "" {
				seedSettings(t, tt.settings)
			} else {
				if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
					t.Fatalf("clear llm_settings: %v", err)
				}
			}

			var caller OneShotLLMCaller
			if !tt.nilCaller && tt.caller != nil {
				caller = tt.caller
			}
			gen := NewTitleGenerator(caller)

			got, err := gen.GenerateTitle(tt.message, tt.source)
			if err != nil {
				t.Fatalf("GenerateTitle() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("GenerateTitle() = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("GenerateTitle() returned invalid UTF-8: %q", got)
			}
			if tt.caller != nil {
				called := tt.caller.callCount() > 0
				if called != tt.wantCallerCalled {
					t.Fatalf("caller invoked = %v, want %v", called, tt.wantCallerCalled)
				}
				if called {
					if tt.caller.lastMaxTok != 50 {
						t.Errorf("max tokens = %d, want 50", tt.caller.lastMaxTok)
					}
					if tt.caller.lastTemp != 0.3 {
						t.Errorf("temperature = %v, want 0.3", tt.caller.lastTemp)
					}
					if !strings.Contains(tt.caller.lastSystem, "concise title generator") {
						t.Errorf("system prompt missing expected text: %q", tt.caller.lastSystem)
					}
					if !strings.Contains(tt.caller.lastUser, tt.source) {
						t.Errorf("user prompt missing source %q: %q", tt.source, tt.caller.lastUser)
					}
					if tt.caller.lastLLM == nil {
						t.Errorf("expected LLM settings to be forwarded, got nil")
					} else if tt.caller.lastLLM.APIKey != tt.settings.APIKey {
						t.Errorf("forwarded API key = %q, want %q", tt.caller.lastLLM.APIKey, tt.settings.APIKey)
					}
				}
			}
		})
	}
}

// Benchmark tests for performance
func BenchmarkGenerateFallbackTitle_Short(b *testing.B) {
	gen := NewTitleGenerator(nil)
	msg := "Short alert message"

	for i := 0; i < b.N; i++ {
		gen.GenerateFallbackTitle(msg, "Test")
	}
}

func BenchmarkGenerateFallbackTitle_Long(b *testing.B) {
	gen := NewTitleGenerator(nil)
	msg := strings.Repeat("This is a long alert message. ", 100)

	for i := 0; i < b.N; i++ {
		gen.GenerateFallbackTitle(msg, "Test")
	}
}

func BenchmarkTruncateForPrompt(b *testing.B) {
	input := strings.Repeat("a", 5000)

	for i := 0; i < b.N; i++ {
		truncateForPrompt(input, 2000)
	}
}
