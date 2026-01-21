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
