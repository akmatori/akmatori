package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// TestAlertHandler_HandleWebhook_MethodValidation tests HTTP method validation
func TestAlertHandler_HandleWebhook_MethodValidation(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		method         string
		expectedStatus int
	}{
		{http.MethodGet, http.StatusMethodNotAllowed},
		{http.MethodPut, http.StatusMethodNotAllowed},
		{http.MethodPatch, http.StatusMethodNotAllowed},
		{http.MethodDelete, http.StatusMethodNotAllowed},
		{http.MethodHead, http.StatusMethodNotAllowed},
		{http.MethodOptions, http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/webhook/alert/test-uuid", nil)
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("HandleWebhook(%s) = %d, want %d", tt.method, w.Code, tt.expectedStatus)
			}
		})
	}
}

// TestAlertHandler_HandleWebhook_PathExtraction tests UUID extraction from path
func TestAlertHandler_HandleWebhook_PathExtraction(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Only test cases that don't require alertService (empty UUID check)
	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "empty UUID with trailing slash",
			path:           "/webhook/alert/",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("{}"))
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("HandleWebhook(%s) = %d, want %d. Body: %s",
					tt.path, w.Code, tt.expectedStatus, w.Body.String())
			}
		})
	}
}

// TestAlertHandler_HandleWebhook_EmptyUUIDMessage tests error message for empty UUID
func TestAlertHandler_HandleWebhook_EmptyUUIDMessage(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert/", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Missing instance UUID") {
		t.Errorf("expected 'Missing instance UUID' in body, got: %s", body)
	}
}

// TestAlertHandler_BuildInvestigationPrompt tests prompt building
func TestAlertHandler_BuildInvestigationPrompt(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name           string
		alert          alerts.NormalizedAlert
		instance       *database.AlertSourceInstance
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "basic alert",
			alert: alerts.NormalizedAlert{
				AlertName:     "HighCPU",
				TargetHost:    "server-01",
				TargetService: "nginx",
				Severity:      database.AlertSeverityCritical,
				Summary:       "CPU usage above 90%",
				Description:   "The CPU has been above threshold for 5 minutes",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{DisplayName: "Prometheus"},
			},
			wantContains: []string{
				"Prometheus",
				"HighCPU",
				"server-01",
				"nginx",
				"critical",
				"CPU usage above 90%",
				"CPU has been above threshold",
			},
		},
		{
			name: "alert with metric data",
			alert: alerts.NormalizedAlert{
				AlertName:   "DiskFull",
				TargetHost:  "storage-01",
				Severity:    database.AlertSeverityWarning,
				Summary:     "Disk space low",
				Description: "Disk usage is high",
				MetricName:  "disk_usage_percent",
				MetricValue: "95",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{DisplayName: "Grafana"},
			},
			wantContains: []string{
				"Metric: disk_usage_percent = 95",
			},
		},
		{
			name: "alert with runbook",
			alert: alerts.NormalizedAlert{
				AlertName:   "ServiceDown",
				TargetHost:  "app-01",
				Severity:    database.AlertSeverityCritical,
				Summary:     "Service not responding",
				Description: "Health check failed",
				RunbookURL:  "https://wiki.example.com/runbook/service-down",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{DisplayName: "PagerDuty"},
			},
			wantContains: []string{
				"Runbook: https://wiki.example.com/runbook/service-down",
			},
		},
		{
			name: "alert without optional fields",
			alert: alerts.NormalizedAlert{
				AlertName:   "TestAlert",
				TargetHost:  "test-host",
				Severity:    database.AlertSeverityInfo,
				Summary:     "Test summary",
				Description: "Test description",
			},
			instance: &database.AlertSourceInstance{
				AlertSourceType: database.AlertSourceType{DisplayName: "Test"},
			},
			wantNotContain: []string{
				"Metric:",
				"Runbook:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.buildInvestigationPrompt(tt.alert, tt.instance)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildInvestigationPrompt() missing %q in:\n%s", want, result)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("buildInvestigationPrompt() should not contain %q", notWant)
				}
			}

			// All prompts should contain investigation steps
			requiredSteps := []string{
				"Check if this is a known issue",
				"Analyze available metrics and logs",
				"Identify potential root causes",
				"Suggest remediation steps",
				"Assess urgency and impact",
			}
			for _, step := range requiredSteps {
				if !strings.Contains(result, step) {
					t.Errorf("buildInvestigationPrompt() missing required step: %q", step)
				}
			}
		})
	}
}

// Note: isSlackEnabled requires database access, tested in integration tests

// TestAlertHandler_GetBaseURL tests base URL retrieval
func TestAlertHandler_GetBaseURL(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name     string
		envValue string
		expected string
	}{
		{
			name:     "default when not set",
			envValue: "",
			expected: "http://localhost:3000",
		},
		{
			name:     "custom URL",
			envValue: "https://akmatori.example.com",
			expected: "https://akmatori.example.com",
		},
		{
			name:     "URL with port",
			envValue: "http://akmatori.internal:8080",
			expected: "http://akmatori.internal:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set/unset env var
			if tt.envValue != "" {
				t.Setenv("AKMATORI_BASE_URL", tt.envValue)
			}

			result := h.getBaseURL()
			if result != tt.expected {
				t.Errorf("getBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestBuildSlackResponse_EdgeCases tests slack response building edge cases
func TestBuildSlackResponse_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		reasoningLog string
		response     string
		wantContains []string
		wantEmpty    bool
	}{
		{
			name:         "both empty",
			reasoningLog: "",
			response:     "",
			wantEmpty:    true,
		},
		{
			name:         "only response",
			reasoningLog: "",
			response:     "Final answer",
			wantContains: []string{"Final answer"},
		},
		{
			name:         "only reasoning (whitespace)",
			reasoningLog: "   ",
			response:     "Done",
			wantContains: []string{"Done"},
		},
		{
			name: "exactly 15 lines",
			reasoningLog: "line1\nline2\nline3\nline4\nline5\n" +
				"line6\nline7\nline8\nline9\nline10\n" +
				"line11\nline12\nline13\nline14\nline15",
			response:     "Result",
			wantContains: []string{"line1", "line15", "Result"},
		},
		{
			name:         "response with markdown",
			reasoningLog: "Checking status...",
			response:     "## Summary\n- Item 1\n- Item 2\n```bash\necho hello\n```",
			wantContains: []string{"## Summary", "echo hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSlackResponse(tt.reasoningLog, tt.response)

			if tt.wantEmpty && result != "" {
				t.Errorf("expected empty result, got %q", result)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("buildSlackResponse() = %q, want to contain %q", result, want)
				}
			}
		})
	}
}

// TestTruncateLogForSlack_Comprehensive tests log truncation comprehensively
func TestTruncateLogForSlack_Comprehensive(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		maxLen       int
		wantPrefix   string
		wantMaxLen   int
		checkContent func(string) bool
	}{
		{
			name:       "empty input",
			input:      "",
			maxLen:     100,
			wantMaxLen: 0,
		},
		{
			name:       "exactly at limit",
			input:      strings.Repeat("a", 100),
			maxLen:     100,
			wantMaxLen: 100,
		},
		{
			name:       "one over limit",
			input:      strings.Repeat("a", 101),
			maxLen:     100,
			wantPrefix: "...(truncated)",
			wantMaxLen: 120, // some overhead for prefix
		},
		{
			name:       "very large input",
			input:      strings.Repeat("x\n", 10000),
			maxLen:     500,
			wantPrefix: "...(truncated)",
			wantMaxLen: 520,
		},
		{
			name:       "preserves full lines",
			input:      "line1\nline2\nline3\nline4\n" + strings.Repeat("x", 1000),
			maxLen:     100,
			wantPrefix: "...(truncated)",
			checkContent: func(s string) bool {
				// Should not have partial lines after truncation marker
				lines := strings.Split(s, "\n")
				for _, line := range lines[1:] { // skip truncation marker line
					if len(line) > 0 && len(line) < 5 && !strings.HasPrefix(line, "line") && line != strings.Repeat("x", len(line)) {
						return false
					}
				}
				return true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateLogForSlack(tt.input, tt.maxLen)

			if tt.wantPrefix != "" && !strings.HasPrefix(result, tt.wantPrefix) {
				t.Errorf("truncateLogForSlack() prefix = %q, want %q", result[:min(len(result), 20)], tt.wantPrefix)
			}

			if tt.wantMaxLen > 0 && len(result) > tt.wantMaxLen {
				t.Errorf("truncateLogForSlack() len = %d, want <= %d", len(result), tt.wantMaxLen)
			}

			if tt.checkContent != nil && !tt.checkContent(result) {
				t.Errorf("truncateLogForSlack() content check failed: %q", result)
			}
		})
	}
}

// TestAlertHandler_WithTestHelpers demonstrates using testhelpers
func TestAlertHandler_WithTestHelpers(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Using HTTPTestContext for cleaner test setup - test method not allowed
	ctx := testhelpers.NewHTTPTestContext(t, http.MethodGet, "/webhook/alert/test-uuid", nil)
	ctx.ExecuteFunc(h.HandleWebhook).
		AssertStatus(http.StatusMethodNotAllowed)

	// Test POST with empty UUID (trailing slash only)
	ctx = testhelpers.NewHTTPTestContext(t, http.MethodPost, "/webhook/alert/", strings.NewReader(""))
	ctx.ExecuteFunc(h.HandleWebhook).
		AssertStatus(http.StatusBadRequest).
		AssertBodyContains("Missing instance UUID")
}

// TestAlertHandler_MockAdapter tests using mock adapter
func TestAlertHandler_MockAdapter(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Create mock adapter using testhelpers
	mockAdapter := testhelpers.NewMockAlertAdapter("test-source")

	// Configure mock to return specific alerts
	now := time.Now()
	mockAdapter.WithAlerts(alerts.NormalizedAlert{
		AlertName:  "TestAlert",
		Severity:   database.AlertSeverityCritical,
		Status:     database.AlertStatusFiring,
		TargetHost: "test-host",
		Summary:    "Test alert fired",
		StartedAt:  &now,
	})

	h.RegisterAdapter(mockAdapter)

	// Verify adapter was registered
	if _, ok := h.adapters["test-source"]; !ok {
		t.Error("mock adapter should be registered")
	}

	// Verify adapter returns expected alerts
	alerts, err := mockAdapter.ParsePayload([]byte("{}"), nil)
	testhelpers.AssertNoError(t, err, "ParsePayload")
	testhelpers.AssertEqual(t, 1, len(alerts), "alert count")
	testhelpers.AssertEqual(t, "TestAlert", alerts[0].AlertName, "alert name")
}

// TestAlertHandler_SlackProgressInterval verifies interval constant
func TestAlertHandler_SlackProgressInterval(t *testing.T) {
	// Ensure progress interval is reasonable (not too fast, not too slow)
	if slackProgressInterval < 2*time.Second {
		t.Errorf("slackProgressInterval too fast: %v (may hit rate limits)", slackProgressInterval)
	}
	if slackProgressInterval > 30*time.Second {
		t.Errorf("slackProgressInterval too slow: %v (poor UX)", slackProgressInterval)
	}
}

// min helper for older Go versions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
