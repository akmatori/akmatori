package handlers

import (
	"os"
	"testing"
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
