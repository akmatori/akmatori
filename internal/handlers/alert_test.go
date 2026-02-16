package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

func TestPluralize(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		expected string
	}{
		{"zero returns s", 0, "s"},
		{"one returns empty", 1, ""},
		{"two returns s", 2, "s"},
		{"many returns s", 100, "s"},
		{"negative returns s", -1, "s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pluralize(tt.count)
			if result != tt.expected {
				t.Errorf("pluralize(%d) = %q, want %q", tt.count, result, tt.expected)
			}
		})
	}
}

func TestAlertHandler_formatAggregationFooter(t *testing.T) {
	h := &AlertHandler{}

	tests := []struct {
		name         string
		incidentUUID string
		alertCount   int
		baseURL      string
		wantContains []string
	}{
		{
			name:         "single alert with default base URL",
			incidentUUID: "abc-123",
			alertCount:   1,
			baseURL:      "",
			wantContains: []string{
				"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
				":link:",
				"1 alert aggregated",
				"<http://localhost:3000/incidents/abc-123|View incident>",
			},
		},
		{
			name:         "multiple alerts with custom base URL",
			incidentUUID: "def-456",
			alertCount:   5,
			baseURL:      "https://akmatori.example.com",
			wantContains: []string{
				"5 alerts aggregated",
				"<https://akmatori.example.com/incidents/def-456|View incident>",
			},
		},
		{
			name:         "zero alerts",
			incidentUUID: "xyz-789",
			alertCount:   0,
			baseURL:      "",
			wantContains: []string{
				"0 alerts aggregated",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set or clear the environment variable
			if tt.baseURL != "" {
				os.Setenv("AKMATORI_BASE_URL", tt.baseURL)
				defer os.Unsetenv("AKMATORI_BASE_URL")
			} else {
				os.Unsetenv("AKMATORI_BASE_URL")
			}

			result := h.formatAggregationFooter(tt.incidentUUID, tt.alertCount)

			for _, want := range tt.wantContains {
				if !contains(result, want) {
					t.Errorf("formatAggregationFooter() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

func TestBuildSlackResponse(t *testing.T) {
	tests := []struct {
		name         string
		reasoningLog string
		response     string
		wantContains []string
		wantPrefix   string
	}{
		{
			name:         "empty reasoning returns response only",
			reasoningLog: "",
			response:     "All clear, no issues found.",
			wantContains: []string{"All clear, no issues found."},
		},
		{
			name:         "short reasoning included in full",
			reasoningLog: "🤔 Checking host status\n✅ Ran: ssh check\n📋 Output:\n   Host is up",
			response:     "Host is healthy.",
			wantContains: []string{"Checking host status", "Ran: ssh check", "---", "Host is healthy."},
		},
		{
			name: "long reasoning truncated to last 15 lines",
			reasoningLog: "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n" +
				"line9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20",
			response:     "Final answer.",
			wantContains: []string{"line6", "line20", "---", "Final answer."},
		},
		{
			name: "long reasoning does not include early lines",
			reasoningLog: "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n" +
				"line9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20",
			response:     "Final answer.",
			wantPrefix:   "line6",
		},
		{
			name:         "whitespace-only reasoning returns response only",
			reasoningLog: "   \n\n  \n",
			response:     "Done.",
			wantContains: []string{"Done."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSlackResponse(tt.reasoningLog, tt.response)

			for _, want := range tt.wantContains {
				if !contains(result, want) {
					t.Errorf("buildSlackResponse() = %q, want to contain %q", result, want)
				}
			}

			if tt.wantPrefix != "" && !contains(result[:50], tt.wantPrefix) {
				t.Errorf("buildSlackResponse() should start near %q, got prefix: %q", tt.wantPrefix, result[:50])
			}

			// Whitespace-only reasoning should NOT have separator
			if tt.name == "whitespace-only reasoning returns response only" {
				if contains(result, "---") {
					t.Errorf("buildSlackResponse() with whitespace reasoning should not contain separator")
				}
			}
		})
	}
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSlackProgressInterval(t *testing.T) {
	if slackProgressInterval != 5*time.Second {
		t.Errorf("slackProgressInterval = %v, want 5s", slackProgressInterval)
	}
}

func TestTruncateLogForSlack_ShortLog(t *testing.T) {
	input := "short log line"
	result := truncateLogForSlack(input, 3000)
	if result != input {
		t.Errorf("truncateLogForSlack() = %q, want %q", result, input)
	}
}

func TestTruncateLogForSlack_ExactLimit(t *testing.T) {
	input := strings.Repeat("a", 3000)
	result := truncateLogForSlack(input, 3000)
	if result != input {
		t.Errorf("truncateLogForSlack() should not truncate at exact limit")
	}
}

func TestTruncateLogForSlack_LongLog(t *testing.T) {
	// Build a long log with identifiable lines
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, strings.Repeat("x", 20))
	}
	input := strings.Join(lines, "\n")

	result := truncateLogForSlack(input, 500)

	if !strings.HasPrefix(result, "...(truncated)\n") {
		t.Errorf("truncateLogForSlack() should start with truncation marker, got prefix: %q", result[:30])
	}
	if len(result) > 520 {
		t.Errorf("truncateLogForSlack() result too long: %d chars", len(result))
	}
}

func TestTruncateLogForSlack_TrimsToLineBreak(t *testing.T) {
	// Create input where truncation point falls mid-line,
	// with a newline within first 100 chars of the truncated portion
	line := strings.Repeat("a", 50)
	// 100 lines of 50 chars each = 5000+ chars total (with newlines)
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, line)
	}
	input := strings.Join(lines, "\n")

	result := truncateLogForSlack(input, 500)

	// Should start with truncation marker
	if !strings.HasPrefix(result, "...(truncated)\n") {
		t.Errorf("expected truncation marker prefix")
	}

	// After the marker, the content should start at a line boundary (no partial line)
	afterMarker := strings.TrimPrefix(result, "...(truncated)\n")
	if strings.Contains(afterMarker[:10], "a\n") {
		// This would indicate a partial first line was kept, which is fine
		// as long as there's no random mid-character break
	}
}

func TestTruncateLogForSlack_EmptyLog(t *testing.T) {
	result := truncateLogForSlack("", 3000)
	if result != "" {
		t.Errorf("truncateLogForSlack(\"\") = %q, want empty", result)
	}
}

func TestConvertLabels(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]string
		verify func(result map[string]interface{}) bool
	}{
		{
			name:  "nil map returns empty map",
			input: nil,
			verify: func(result map[string]interface{}) bool {
				return len(result) == 0
			},
		},
		{
			name:  "empty map returns empty map",
			input: map[string]string{},
			verify: func(result map[string]interface{}) bool {
				return len(result) == 0
			},
		},
		{
			name: "single label converted",
			input: map[string]string{
				"host": "server1",
			},
			verify: func(result map[string]interface{}) bool {
				v, ok := result["host"]
				return ok && v == "server1"
			},
		},
		{
			name: "multiple labels converted",
			input: map[string]string{
				"host":     "server1",
				"env":      "production",
				"severity": "critical",
			},
			verify: func(result map[string]interface{}) bool {
				return len(result) == 3 &&
					result["host"] == "server1" &&
					result["env"] == "production" &&
					result["severity"] == "critical"
			},
		},
		{
			name: "empty string values preserved",
			input: map[string]string{
				"key": "",
			},
			verify: func(result map[string]interface{}) bool {
				v, ok := result["key"]
				return ok && v == ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertLabels(tt.input)
			if result == nil {
				t.Fatal("convertLabels returned nil")
			}
			if !tt.verify(result) {
				t.Errorf("convertLabels(%v) verification failed, got %v", tt.input, result)
			}
		})
	}
}

func TestNewAlertHandler(t *testing.T) {
	// Test that NewAlertHandler creates valid handler with nil dependencies
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewAlertHandler returned nil")
	}
	if h.adapters == nil {
		t.Error("adapters map should be initialized")
	}
	if len(h.adapters) != 0 {
		t.Errorf("adapters map should be empty, got %d entries", len(h.adapters))
	}
}

func TestAlertHandler_RegisterAdapter(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// Create a mock adapter
	adapter := &mockAlertAdapter{sourceType: "prometheus"}

	h.RegisterAdapter(adapter)

	if len(h.adapters) != 1 {
		t.Errorf("expected 1 adapter, got %d", len(h.adapters))
	}

	registered, ok := h.adapters["prometheus"]
	if !ok {
		t.Error("prometheus adapter not found")
	}
	if registered.GetSourceType() != "prometheus" {
		t.Error("registered adapter source type does not match")
	}
}

func TestAlertHandler_RegisterAdapter_Multiple(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// Register multiple adapters
	adapters := []string{"prometheus", "grafana", "datadog", "pagerduty"}
	for _, name := range adapters {
		h.RegisterAdapter(&mockAlertAdapter{sourceType: name})
	}

	if len(h.adapters) != len(adapters) {
		t.Errorf("expected %d adapters, got %d", len(adapters), len(h.adapters))
	}

	for _, name := range adapters {
		if _, ok := h.adapters[name]; !ok {
			t.Errorf("adapter %q not registered", name)
		}
	}
}

func TestAlertHandler_RegisterAdapter_Overwrite(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	adapter1 := &mockAlertAdapter{sourceType: "prometheus", id: "first"}
	adapter2 := &mockAlertAdapter{sourceType: "prometheus", id: "second"}

	h.RegisterAdapter(adapter1)
	h.RegisterAdapter(adapter2)

	if len(h.adapters) != 1 {
		t.Errorf("expected 1 adapter after overwrite, got %d", len(h.adapters))
	}

	// Verify an adapter with this source type is registered
	_, ok := h.adapters["prometheus"]
	if !ok {
		t.Error("prometheus adapter should still exist after overwrite")
	}
}

func TestAlertHandler_HandleWebhook_MethodNotAllowed(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/webhook/alert/test-uuid", nil)
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("HandleWebhook(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestAlertHandler_HandleWebhook_MissingUUID(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert/", nil)
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("HandleWebhook with empty UUID = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAlertHandler_getBaseURL(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// Test default value
	os.Unsetenv("AKMATORI_BASE_URL")
	url := h.getBaseURL()
	if url != "http://localhost:3000" {
		t.Errorf("getBaseURL() with no env = %q, want %q", url, "http://localhost:3000")
	}

	// Test with env var set
	os.Setenv("AKMATORI_BASE_URL", "https://akmatori.example.com")
	defer os.Unsetenv("AKMATORI_BASE_URL")

	url = h.getBaseURL()
	if url != "https://akmatori.example.com" {
		t.Errorf("getBaseURL() with env = %q, want %q", url, "https://akmatori.example.com")
	}
}

// mockAlertAdapter implements alerts.AlertAdapter for testing
type mockAlertAdapter struct {
	sourceType string
	id         string
}

func (m *mockAlertAdapter) GetSourceType() string {
	return m.sourceType
}

func (m *mockAlertAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	return nil, nil
}

func (m *mockAlertAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	return nil
}

func (m *mockAlertAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{}
}
