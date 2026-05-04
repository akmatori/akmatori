package services

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTitleGenerator_GenerateFallbackTitle(t *testing.T) {
	gen := NewTitleGenerator()

	tests := []struct {
		name     string
		message  string
		source   string
		expected string
	}{
		{
			name:     "simple message",
			message:  "Server is down",
			source:   "Slack",
			expected: "Server is down",
		},
		{
			name:     "empty message",
			message:  "",
			source:   "PagerDuty",
			expected: "Incident from PagerDuty",
		},
		{
			name:     "whitespace only message",
			message:  "   \n\t  ",
			source:   "Zabbix",
			expected: "Incident from Zabbix",
		},
		{
			name:     "message with Alert: prefix",
			message:  "Alert: CPU usage critical",
			source:   "Prometheus",
			expected: "CPU usage critical",
		},
		{
			name:     "message with alert: lowercase prefix",
			message:  "alert: Disk space low",
			source:   "Grafana",
			expected: "Disk space low",
		},
		{
			name:     "message with Incident: prefix",
			message:  "Incident: Database connection failure",
			source:   "Datadog",
			expected: "Database connection failure",
		},
		{
			name:     "message with incident: lowercase prefix",
			message:  "incident: API gateway timeout",
			source:   "OpsGenie",
			expected: "API gateway timeout",
		},
		{
			name:     "multiline message - takes first line only",
			message:  "First line title\nSecond line details\nThird line",
			source:   "Slack",
			expected: "First line title",
		},
		{
			name:     "long message - truncated with word boundary",
			message:  "This is a very long alert title that needs to be truncated because it exceeds the maximum allowed length for titles",
			source:   "Alertmanager",
			expected: "This is a very long alert title that needs to be truncated because it exceeds...",
		},
		{
			name:     "long message - truncated without good word boundary",
			message:  "ThisIsAVeryLongAlertTitleWithNoSpacesThatNeedsToBetruncatedBecauseItExceedsTheMaximumAllowedLengthForTitles",
			source:   "Custom",
			expected: "ThisIsAVeryLongAlertTitleWithNoSpacesThatNeedsToBetruncatedBecauseItExceedsTh...",
		},
		{
			name:     "exactly 80 chars - no truncation",
			message:  strings.Repeat("a", 80),
			source:   "Test",
			expected: strings.Repeat("a", 80),
		},
		{
			name:     "81 chars - minimal truncation",
			message:  strings.Repeat("a", 81),
			source:   "Test",
			expected: strings.Repeat("a", 77) + "...",
		},
		{
			name:     "multiline with prefix",
			message:  "Alert: Server outage\nDetails: Production cluster\nTime: 10:30 UTC",
			source:   "Slack",
			expected: "Server outage",
		},
		{
			name:     "message with leading/trailing whitespace",
			message:  "  Important alert  ",
			source:   "Test",
			expected: "Important alert",
		},
		{
			name:     "message with multiple prefixes - only first removed",
			message:  "Alert: Incident: Double prefix",
			source:   "Test",
			expected: "Incident: Double prefix",
		},
		{
			name:     "Unicode characters",
			message:  "服务器警报: CPU过高",
			source:   "Monitoring",
			expected: "服务器警报: CPU过高",
		},
		{
			name:     "emoji in message",
			message:  "🚨 Critical: Production down",
			source:   "Slack",
			expected: "🚨 Critical: Production down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gen.GenerateFallbackTitle(tt.message, tt.source)
			if result != tt.expected {
				t.Errorf("GenerateFallbackTitle(%q, %q) = %q, want %q",
					tt.message, tt.source, result, tt.expected)
			}
		})
	}
}

func TestTruncateForPrompt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string - no truncation",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length - no truncation",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string - truncated",
			input:    "hello world",
			maxLen:   8,
			expected: "hello...",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "maxLen less than 3 - edge case",
			input:    "hello",
			maxLen:   3,
			expected: "...",
		},
		{
			name:     "maxLen of 4",
			input:    "hello world",
			maxLen:   4,
			expected: "h...",
		},
		{
			name:     "unicode string truncation",
			input:    "你好世界",
			maxLen:   3,
			expected: "...",
		},
		{
			name:     "very long string",
			input:    strings.Repeat("a", 5000),
			maxLen:   100,
			expected: strings.Repeat("a", 97) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateForPrompt(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateForPrompt(%q, %d) = %q, want %q",
					tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestNewTitleGenerator(t *testing.T) {
	gen := NewTitleGenerator()

	if gen == nil {
		t.Fatal("NewTitleGenerator() returned nil")
	}

	if gen.httpClient == nil {
		t.Error("httpClient should not be nil")
	}

	if gen.httpClient.Timeout == 0 {
		t.Error("httpClient.Timeout should be set")
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
		name           string
		message        string
		source         string
		settings       database.LLMSettings
		transport      roundTripFunc
		want           string
		wantErr        string
		wantHTTPCalled bool
	}{
		{
			name:    "short message uses fallback without database lookup",
			message: "too short",
			source:  "Slack",
			want:    "too short",
		},
		{
			name: "missing api key falls back",
			message: "The database connection pool is saturated and requests are timing out for multiple users.",
			source:  "PagerDuty",
			settings: database.LLMSettings{
				Name:     "openai-empty-key",
				Provider: database.LLMProviderOpenAI,
				Enabled:  true,
				Active:   true,
			},
			want: "The database connection pool is saturated and requests are timing out for...",
		},
		{
			name: "non-openai provider falls back",
			message: "Customer reported that runbook execution is stuck while waiting for a tool result.",
			source:  "Slack",
			settings: database.LLMSettings{
				Name:     "anthropic",
				Provider: database.LLMProviderAnthropic,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			want: "Customer reported that runbook execution is stuck while waiting for a tool...",
		},
		{
			name: "transport error falls back",
			message: "HTTP connector deployment failed because the upstream returned repeated 503 responses.",
			source:  "Alertmanager",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			transport: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("boom")
			},
			want:           "HTTP connector deployment failed because the upstream returned repeated 503...",
			wantHTTPCalled: true,
		},
		{
			name: "api error payload falls back",
			message: "The agent worker failed to start after the runtime configuration changed during deploy.",
			source:  "API",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)), Header: make(http.Header)}, nil
			},
			want:           "The agent worker failed to start after the runtime configuration changed during...",
			wantHTTPCalled: true,
		},
		{
			name: "empty choices falls back",
			message: "A customer webhook produced malformed JSON and the parser rejected the payload before routing.",
			source:  "Webhook",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`)), Header: make(http.Header)}, nil
			},
			want:           "A customer webhook produced malformed JSON and the parser rejected the payload...",
			wantHTTPCalled: true,
		},
		{
			name: "successful response trims quotes",
			message: "Production alerting latency increased after a queue backlog built up in the dispatcher.",
			source:  "Slack",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			transport: func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					return nil, fmt.Errorf("method = %s, want POST", req.Method)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
					return nil, fmt.Errorf("authorization = %q, want Bearer test-key", got)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"\"Dispatcher backlog increased alert latency\""}}]}`)), Header: make(http.Header)}, nil
			},
			want:           "Dispatcher backlog increased alert latency",
			wantHTTPCalled: true,
		},
		{
			name: "successful response truncates long title",
			message: "The monitoring pipeline kept duplicating the same alert payload as retries piled up across regions.",
			source:  "Grafana",
			settings: database.LLMSettings{
				Name:     "openai",
				Provider: database.LLMProviderOpenAI,
				APIKey:   "test-key",
				Enabled:  true,
				Active:   true,
			},
			transport: func(req *http.Request) (*http.Response, error) {
				content := strings.Repeat("x", 260)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content))), Header: make(http.Header)}, nil
			},
			want:           strings.Repeat("x", 252) + "...",
			wantHTTPCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.settings.Name != "" {
				seedSettings(t, tt.settings)
			}

			gen := NewTitleGenerator()
			httpCalled := false
			if tt.transport != nil {
				gen.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
					httpCalled = true
					return tt.transport(req)
				})
			}

			got, err := gen.GenerateTitle(tt.message, tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("GenerateTitle() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GenerateTitle() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("GenerateTitle() = %q, want %q", got, tt.want)
			}
			if httpCalled != tt.wantHTTPCalled {
				t.Fatalf("HTTP called = %v, want %v", httpCalled, tt.wantHTTPCalled)
			}
		})
	}
}

// Benchmark tests for performance
func BenchmarkGenerateFallbackTitle_Short(b *testing.B) {
	gen := NewTitleGenerator()
	msg := "Short alert message"

	for i := 0; i < b.N; i++ {
		gen.GenerateFallbackTitle(msg, "Test")
	}
}

func BenchmarkGenerateFallbackTitle_Long(b *testing.B) {
	gen := NewTitleGenerator()
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
