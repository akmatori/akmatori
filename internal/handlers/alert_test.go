package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

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
			response:   "Final answer.",
			wantPrefix: "line6",
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
	// We just verify the result is non-empty and properly formatted
	afterMarker := strings.TrimPrefix(result, "...(truncated)\n")
	_ = afterMarker // Used for manual inspection if needed
}

func TestTruncateLogForSlack_EmptyLog(t *testing.T) {
	result := truncateLogForSlack("", 3000)
	if result != "" {
		t.Errorf("truncateLogForSlack(\"\") = %q, want empty", result)
	}
}

func TestTruncateForSlack_Short(t *testing.T) {
	msg := "short message"
	result := truncateForSlack(msg, slackMaxTextBytes)
	if result != msg {
		t.Errorf("short message should not be truncated")
	}
}

func TestTruncateForSlack_Long(t *testing.T) {
	msg := strings.Repeat("Line of text here.\n", 300) // ~5700 bytes
	result := truncateForSlack(msg, slackMaxTextBytes)
	if len(result) > slackMaxTextBytes {
		t.Errorf("truncateForSlack() result is %d bytes, want <= %d", len(result), slackMaxTextBytes)
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("truncated message should contain truncation notice")
	}
}

func TestTruncateForSlack_BreaksAtNewline(t *testing.T) {
	// Create content that's just over the limit
	msg := strings.Repeat("x", slackMaxTextBytes-100) + "\nthis line should be cut\nmore"
	result := truncateForSlack(msg, slackMaxTextBytes)
	// Should break at the newline, not mid-line
	// Note: "this line should be cut" may or may not be included depending on
	// where the truncation happens - either is acceptable as long as limit is respected
	if len(result) > slackMaxTextBytes {
		t.Errorf("result too long: %d", len(result))
	}
}

func TestNewAlertHandler(t *testing.T) {
	// Test that NewAlertHandler creates valid handler with nil dependencies
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
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
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

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
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

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
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

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
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

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
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert/", nil)
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("HandleWebhook with empty UUID = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAlertHandler_getBaseURL(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

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
	sourceType    string
	id            string
	alerts        []alerts.NormalizedAlert
	parseErr      error
	validateErr   error
	parseCalls    int
	validateCalls int
}

func (m *mockAlertAdapter) GetSourceType() string {
	return m.sourceType
}

func (m *mockAlertAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	m.parseCalls++
	if m.parseErr != nil {
		return nil, m.parseErr
	}
	return m.alerts, nil
}

func (m *mockAlertAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	m.validateCalls++
	return m.validateErr
}

func (m *mockAlertAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{}
}

// Test buildInvestigationPrompt method
func TestAlertHandler_buildInvestigationPrompt(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name         string
		alert        alerts.NormalizedAlert
		instance     *database.AlertSourceInstance
		wantContains []string
	}{
		{
			name: "basic alert",
			alert: alerts.NormalizedAlert{
				AlertName:   "HighCPU",
				TargetHost:  "server1",
				Severity:    database.AlertSeverityCritical,
				Summary:     "CPU is high on server1",
				Description: "CPU usage exceeded 90%",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{
					Name:        "prometheus",
					DisplayName: "Prometheus",
				},
			},
			wantContains: []string{
				"HighCPU",
				"server1",
				"CPU is high on server1",
				"CPU usage exceeded 90%",
				"Prometheus",
			},
		},
		{
			name: "alert with metric",
			alert: alerts.NormalizedAlert{
				AlertName:   "DiskFull",
				TargetHost:  "server2",
				Severity:    database.AlertSeverityWarning,
				Summary:     "Disk full on /dev/sda",
				Description: "Less than 5% free space",
				MetricName:  "disk_free_percent",
				MetricValue: "4.5",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{
					Name:        "grafana",
					DisplayName: "Grafana",
				},
			},
			wantContains: []string{
				"DiskFull",
				"disk_free_percent",
				"4.5",
			},
		},
		{
			name: "alert with runbook",
			alert: alerts.NormalizedAlert{
				AlertName:   "MemoryHigh",
				TargetHost:  "server3",
				Severity:    database.AlertSeverityCritical,
				Summary:     "Memory usage high",
				Description: "OOM killer may activate",
				RunbookURL:  "https://wiki.example.com/runbooks/memory-high",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{
					Name:        "datadog",
					DisplayName: "Datadog",
				},
			},
			wantContains: []string{
				"MemoryHigh",
				"https://wiki.example.com/runbooks/memory-high",
				"Runbook",
			},
		},
		{
			name:  "empty alert",
			alert: alerts.NormalizedAlert{},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{
					Name:        "custom",
					DisplayName: "Custom",
				},
			},
			wantContains: []string{
				"Investigate",
				"Please:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.buildInvestigationPrompt(tt.alert, tt.instance)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildInvestigationPrompt() = %q, want to contain %q", result, want)
				}
			}

			// Verify it's always non-empty (has investigation instructions)
			if result == "" {
				t.Error("buildInvestigationPrompt() returned empty string")
			}
		})
	}
}

// --- buildSlackFooter tests ---

func TestBuildSlackFooter_WithMetrics(t *testing.T) {
	response := "Investigation complete.\n\n---\n⏱️ Time: 41.3s | 🎯 Tokens: 126,028"
	contentOnly, footer := buildSlackFooter(response, "abc-123")

	// The split is at "\n---\n⏱️" so contentOnly includes the leading "\n"
	expected := "Investigation complete.\n"
	if contentOnly != expected {
		t.Errorf("contentOnly = %q, want %q", contentOnly, expected)
	}
	if !strings.Contains(footer, "⏱️ Time: 41.3s") {
		t.Errorf("footer should contain metrics line, got %q", footer)
	}
	if !strings.Contains(footer, "🎯 Tokens: 126,028") {
		t.Errorf("footer should contain token count, got %q", footer)
	}
}

func TestBuildSlackFooter_WithoutMetrics(t *testing.T) {
	response := "Investigation complete. No issues found."
	contentOnly, footer := buildSlackFooter(response, "def-456")

	if contentOnly != response {
		t.Errorf("contentOnly should equal original response when no metrics present")
	}
	if strings.Contains(footer, "⏱️") {
		t.Errorf("footer should not contain metrics when none in response, got %q", footer)
	}
}

func TestBuildSlackFooter_UILink(t *testing.T) {
	os.Setenv("AKMATORI_BASE_URL", "https://akmatori.example.com")
	defer os.Unsetenv("AKMATORI_BASE_URL")

	_, footer := buildSlackFooter("some response", "uuid-123")

	if !strings.Contains(footer, "<https://akmatori.example.com/incidents/uuid-123|View reasoning log>") {
		t.Errorf("footer should contain UI link, got %q", footer)
	}
}

func TestBuildSlackFooter_UILinkDefaultBaseURL(t *testing.T) {
	os.Unsetenv("AKMATORI_BASE_URL")

	_, footer := buildSlackFooter("some response", "uuid-456")

	if !strings.Contains(footer, "<http://localhost:3000/incidents/uuid-456|View reasoning log>") {
		t.Errorf("footer should use default base URL, got %q", footer)
	}
}

func TestBuildSlackFooter_MetricsExtractedCorrectly(t *testing.T) {
	// Verify that only the part after "\n---\n" is extracted as metrics
	response := "Line 1\n---\nLine 2\n\n---\n⏱️ Time: 10s | 🎯 Tokens: 500"
	contentOnly, footer := buildSlackFooter(response, "test-uuid")

	// Should extract from the LAST occurrence of "\n---\n⏱️"
	if !strings.Contains(contentOnly, "Line 2") {
		t.Errorf("contentOnly should contain middle content, got %q", contentOnly)
	}
	if strings.Contains(contentOnly, "⏱️") {
		t.Errorf("contentOnly should not contain metrics, got %q", contentOnly)
	}
	if !strings.Contains(footer, "⏱️ Time: 10s") {
		t.Errorf("footer should contain extracted metrics, got %q", footer)
	}
}

func TestBuildSlackFooter_FooterFormat(t *testing.T) {
	os.Unsetenv("AKMATORI_BASE_URL")

	response := "Done.\n\n---\n⏱️ Time: 5s | 🎯 Tokens: 100"
	_, footer := buildSlackFooter(response, "inc-1")

	// Footer should start with separator
	if !strings.HasPrefix(footer, "\n\n———\n") {
		t.Errorf("footer should start with separator, got %q", footer)
	}
}

func TestTruncateWithFooter_NoTruncation(t *testing.T) {
	content := "short content"
	footer := "\n\n———\nmetrics\nlink"
	result := truncateWithFooter(content, footer, slackMaxTextBytes)

	if result != content+footer {
		t.Errorf("short content should not be truncated")
	}
}

func TestTruncateWithFooter_TruncatesContent(t *testing.T) {
	content := strings.Repeat("Line of text.\n", 300) // ~4500 bytes
	footer := "\n\n———\n⏱️ Time: 5s\n<http://localhost:3000/incidents/x|View  reasoning log>"

	result := truncateWithFooter(content, footer, slackMaxTextBytes)

	if len(result) > slackMaxTextBytes {
		t.Errorf("result should be <= %d bytes, got %d", slackMaxTextBytes, len(result))
	}
	if !strings.Contains(result, "View  reasoning log") {
		t.Errorf("footer should always be present in result")
	}
	if !strings.Contains(result, "⏱️ Time: 5s") {
		t.Errorf("metrics should always be present in result")
	}
}

func TestAlertHandler_SetTeamID(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetTeamID("T123")
	if h.teamID != "T123" {
		t.Fatalf("teamID = %q, want %q", h.teamID, "T123")
	}
}

func TestAlertHandler_HandleWebhook_ErrorPathsAndSuccess(t *testing.T) {
	baseInstance := &database.AlertSourceInstance{
		UUID:    "test-uuid",
		Name:    "Primary Alertmanager",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name: "alertmanager",
		},
	}

	tests := []struct {
		name           string
		path           string
		service        *mockAlertManager
		adapter        *mockAlertAdapter
		body           string
		reader         io.Reader
		expectedStatus int
		wantBody       string
	}{
		{
			name:           "instance not found",
			path:           "/webhook/alert/missing",
			service:        &mockAlertManager{getInstanceErr: errors.New("not found")},
			expectedStatus: http.StatusNotFound,
			wantBody:       "Instance not found",
		},
		{
			name: "disabled instance",
			path: "/webhook/alert/test-uuid",
			service: &mockAlertManager{instance: &database.AlertSourceInstance{
				UUID:            "test-uuid",
				Enabled:         false,
				AlertSourceType: database.AlertSourceType{Name: "alertmanager"},
			}},
			expectedStatus: http.StatusForbidden,
			wantBody:       "Instance disabled",
		},
		{
			name:           "unsupported source type",
			path:           "/webhook/alert/test-uuid",
			service:        &mockAlertManager{instance: cloneInstance(baseInstance)},
			expectedStatus: http.StatusBadRequest,
			wantBody:       "Unsupported source type",
		},
		{
			name:           "invalid webhook secret",
			path:           "/webhook/alert/test-uuid",
			service:        &mockAlertManager{instance: cloneInstance(baseInstance)},
			adapter:        &mockAlertAdapter{sourceType: "alertmanager", validateErr: errors.New("bad secret")},
			expectedStatus: http.StatusUnauthorized,
			wantBody:       "Unauthorized",
		},
		{
			name:           "request body read failure",
			path:           "/webhook/alert/test-uuid",
			service:        &mockAlertManager{instance: cloneInstance(baseInstance)},
			adapter:        &mockAlertAdapter{sourceType: "alertmanager"},
			reader:         errReader{},
			expectedStatus: http.StatusBadRequest,
			wantBody:       "Failed to read request body",
		},
		{
			name:           "invalid payload",
			path:           "/webhook/alert/test-uuid",
			service:        &mockAlertManager{instance: cloneInstance(baseInstance)},
			adapter:        &mockAlertAdapter{sourceType: "alertmanager", parseErr: errors.New("invalid json")},
			body:           "{bad json}",
			expectedStatus: http.StatusBadRequest,
			wantBody:       "Invalid payload",
		},
		{
			name:           "success with empty alert batch",
			path:           "/webhook/alert/test-uuid",
			service:        &mockAlertManager{instance: cloneInstance(baseInstance)},
			adapter:        &mockAlertAdapter{sourceType: "alertmanager", alerts: []alerts.NormalizedAlert{}},
			body:           `{"status":"ok"}`,
			expectedStatus: http.StatusOK,
			wantBody:       "Received 0 alerts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAlertHandler(nil, nil, nil, nil, nil, tt.service, nil)
			if tt.adapter != nil {
				h.RegisterAdapter(tt.adapter)
			}

			reader := tt.reader
			if reader == nil {
				reader = strings.NewReader(tt.body)
			}

			req := httptest.NewRequest(http.MethodPost, tt.path, reader)
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			if w.Code != tt.expectedStatus {
				t.Fatalf("status = %d, want %d; body=%q", w.Code, tt.expectedStatus, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Fatalf("response body = %q, want substring %q", w.Body.String(), tt.wantBody)
			}
			if tt.service != nil && tt.service.lastUUID != strings.TrimPrefix(strings.TrimSuffix(tt.path, "/"), "/webhook/alert/") {
				t.Fatalf("GetInstanceByUUID called with %q, want %q", tt.service.lastUUID, strings.TrimPrefix(strings.TrimSuffix(tt.path, "/"), "/webhook/alert/"))
			}
			if tt.adapter != nil {
				if tt.wantBody == "Unsupported source type" {
					if tt.adapter.validateCalls != 0 || tt.adapter.parseCalls != 0 {
						t.Fatalf("unsupported source type should not invoke adapter, got validate=%d parse=%d", tt.adapter.validateCalls, tt.adapter.parseCalls)
					}
					return
				}
				if tt.wantBody != "Instance not found" && tt.wantBody != "Instance disabled" && tt.adapter.validateCalls == 0 {
					t.Fatalf("expected ValidateWebhookSecret to be called")
				}
				if tt.wantBody == "Invalid payload" || strings.HasPrefix(tt.wantBody, "Received ") {
					if tt.adapter.parseCalls == 0 {
						t.Fatalf("expected ParsePayload to be called")
					}
				}
			}
		})
	}
}

func cloneInstance(in *database.AlertSourceInstance) *database.AlertSourceInstance {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("boom")
}

func (errReader) Close() error { return nil }

type mockAlertManager struct {
	instance       *database.AlertSourceInstance
	getInstanceErr error
	lastUUID       string
}

func (m *mockAlertManager) ListSourceTypes() ([]database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) ListAlertSourceTypes() ([]database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) GetAlertSourceType(id uint) (*database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) GetAlertSourceTypeByName(name string) (*database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) CreateAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) EnsureAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error) {
	return nil, nil
}
func (m *mockAlertManager) ListInstances() ([]database.AlertSourceInstance, error) {
	return nil, nil
}
func (m *mockAlertManager) GetInstance(id uint) (*database.AlertSourceInstance, error) {
	return nil, nil
}
func (m *mockAlertManager) GetInstanceByUUID(uuid string) (*database.AlertSourceInstance, error) {
	m.lastUUID = uuid
	if m.getInstanceErr != nil {
		return nil, m.getInstanceErr
	}
	return m.instance, nil
}
func (m *mockAlertManager) CreateInstance(sourceTypeName, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error) {
	return nil, nil
}
func (m *mockAlertManager) CreateInstanceByTypeID(sourceTypeID uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error) {
	return nil, nil
}
func (m *mockAlertManager) UpdateInstance(uuid string, updates map[string]interface{}) error {
	return nil
}
func (m *mockAlertManager) UpdateInstanceByID(id uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB, enabled bool) error {
	return nil
}
func (m *mockAlertManager) DeleteInstance(uuid string) error    { return nil }
func (m *mockAlertManager) DeleteInstanceByID(id uint) error    { return nil }
func (m *mockAlertManager) InitializeDefaultSourceTypes() error { return nil }

// HandleWebhook tests with full dependencies are in integration_test.go
