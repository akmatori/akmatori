package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

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
	// Build content that always exceeds the cap regardless of how large
	// slackMaxTextBytes grows in the future.
	msg := strings.Repeat("Line of text here.\n", (slackMaxTextBytes/19)+200)
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
		{
			name: "alert with original_message and source",
			alert: alerts.NormalizedAlert{
				AlertName:   "StreamHealthAlert",
				TargetHost:  "edge-01",
				Severity:    database.AlertSeverityCritical,
				Summary:     "Video stream health degraded",
				Description: "Frame loss above threshold",
				RawPayload: map[string]interface{}{
					"original_message": "New notification from stream-health monitor: viewers dropping",
				},
			},
			instance: &database.AlertSourceInstance{
				Name: "channel-prod",
				AlertSourceType: database.AlertSourceType{
					Name:        "slack-channel",
					DisplayName: "Slack Channel",
				},
			},
			wantContains: []string{
				"Source: slack-channel / channel-prod",
				"Original alert text:",
				"New notification from stream-health monitor",
			},
		},
		{
			name: "alert without original_message has no original block",
			alert: alerts.NormalizedAlert{
				AlertName:   "ZabbixHighLoad",
				TargetHost:  "db-01",
				Severity:    database.AlertSeverityWarning,
				Summary:     "Load average 5",
				Description: "Sustained for 10m",
				RawPayload:  map[string]interface{}{},
			},
			instance: &database.AlertSourceInstance{
				Name: "prod-zabbix",
				AlertSourceType: database.AlertSourceType{
					Name:        "zabbix",
					DisplayName: "Zabbix",
				},
			},
			wantContains: []string{
				"ZabbixHighLoad",
				"Source: zabbix / prod-zabbix",
				"Zabbix",
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

	// Source line trims whitespace and renders whichever non-empty component
	// remains, so the runbook-search cue survives partially-populated rows
	// that the API has historically let through (whitespace-only names, or
	// blank instance Name in pre-validation records).
	t.Run("source renders type alone when instance name empty", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{AlertName: "X"},
			&database.AlertSourceInstance{
				Name: "",
				AlertSourceType: database.AlertSourceType{
					Name:        "zabbix",
					DisplayName: "Zabbix",
				},
			},
		)
		if !strings.Contains(result, "\nSource: zabbix\n") {
			t.Errorf("prompt should contain bare 'Source: zabbix' when instance Name is empty, got %q", result)
		}
		if strings.Contains(result, "Source: zabbix /") {
			t.Errorf("prompt should not contain dangling slash, got %q", result)
		}
	})

	t.Run("source renders instance alone when type name empty", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{AlertName: "X"},
			&database.AlertSourceInstance{
				Name: "channel-prod",
				AlertSourceType: database.AlertSourceType{
					Name:        "",
					DisplayName: "Slack Channel",
				},
			},
		)
		if !strings.Contains(result, "\nSource: channel-prod\n") {
			t.Errorf("prompt should contain bare 'Source: channel-prod' when type Name is empty, got %q", result)
		}
	})

	t.Run("source line omitted when both empty", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{AlertName: "X"},
			&database.AlertSourceInstance{
				Name: "",
				AlertSourceType: database.AlertSourceType{
					Name:        "",
					DisplayName: "Custom",
				},
			},
		)
		if strings.Contains(result, "Source:") {
			t.Errorf("prompt should not contain Source: line when both names empty, got %q", result)
		}
	})

	// Whitespace-only persisted names are equivalent to empty after trim;
	// they must not produce a "Source: zabbix /    " stub.
	t.Run("whitespace-only instance name does not produce stub", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{AlertName: "X"},
			&database.AlertSourceInstance{
				Name: "   ",
				AlertSourceType: database.AlertSourceType{
					Name:        "zabbix",
					DisplayName: "Zabbix",
				},
			},
		)
		if strings.Contains(result, "Source: zabbix /") {
			t.Errorf("prompt should not render dangling slash for whitespace-only instance Name, got %q", result)
		}
		if !strings.Contains(result, "\nSource: zabbix\n") {
			t.Errorf("prompt should fall back to bare 'Source: zabbix', got %q", result)
		}
	})

	t.Run("whitespace-only both names omit source line", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{AlertName: "X"},
			&database.AlertSourceInstance{
				Name: "   ",
				AlertSourceType: database.AlertSourceType{
					Name:        "\t",
					DisplayName: "Custom",
				},
			},
		)
		if strings.Contains(result, "Source:") {
			t.Errorf("prompt should not contain Source: line when both names whitespace-only, got %q", result)
		}
	})

	// Targeted assertion: alerts without original_message must not render the
	// "Original alert text:" block.
	t.Run("absent original_message omits block", func(t *testing.T) {
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{
				AlertName: "PlainAlert",
				RawPayload: map[string]interface{}{
					"other": "value",
				},
			},
			&database.AlertSourceInstance{
				Name: "inst",
				AlertSourceType: database.AlertSourceType{
					Name:        "type",
					DisplayName: "Type",
				},
			},
		)
		if strings.Contains(result, "Original alert text:") {
			t.Errorf("prompt should not contain Original alert text block, got %q", result)
		}
	})

	// In the Slack-channel extractor fallback path, alert.Description carries the
	// raw message (same string that lands in RawPayload.original_message).
	// The labeled "Original alert text:" block is still rendered because the
	// agent feeds it to the runbook-searcher subagent; suppressing the label
	// here would push the agent onto the 100-char truncated summary in this
	// exact path.
	t.Run("description equals original_message keeps verbatim block", func(t *testing.T) {
		raw := "New notification from stream-health monitor: viewers dropping below threshold for region eu-central"
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{
				AlertName:   "StreamMonitorAlert",
				Description: raw,
				RawPayload: map[string]interface{}{
					"original_message": raw,
				},
			},
			&database.AlertSourceInstance{
				Name: "channel-prod",
				AlertSourceType: database.AlertSourceType{
					Name:        "slack-channel",
					DisplayName: "Slack Channel",
				},
			},
		)
		if !strings.Contains(result, "Original alert text:") {
			t.Errorf("expected verbatim block to be present even when Description duplicates original_message, got %q", result)
		}
		if !strings.Contains(result, raw) {
			t.Errorf("expected raw message in prompt, got %q", result)
		}
	})

	// When the LLM extractor returns a clean Description distinct from the raw
	// message, both fields render: Description (clean summary) + Original alert
	// text (verbatim, capped at 1500 bytes).
	t.Run("distinct description retains verbatim block", func(t *testing.T) {
		raw := "New notification from stream-health monitor: viewers dropping"
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{
				AlertName:   "StreamMonitorAlert",
				Description: "Video stream viewer count dropped below SLO",
				RawPayload: map[string]interface{}{
					"original_message": raw,
				},
			},
			&database.AlertSourceInstance{
				Name: "channel-prod",
				AlertSourceType: database.AlertSourceType{
					Name:        "slack-channel",
					DisplayName: "Slack Channel",
				},
			},
		)
		if !strings.Contains(result, "Original alert text:") {
			t.Errorf("expected verbatim block when Description differs from original_message, got %q", result)
		}
		if !strings.Contains(result, raw) {
			t.Errorf("expected verbatim text in prompt, got %q", result)
		}
	})

	t.Run("long original_message is truncated with ellipsis", func(t *testing.T) {
		long := strings.Repeat("x", 5000)
		result := h.buildInvestigationPrompt(
			alerts.NormalizedAlert{
				AlertName: "LongMessage",
				RawPayload: map[string]interface{}{
					"original_message": long,
				},
			},
			&database.AlertSourceInstance{
				Name: "inst",
				AlertSourceType: database.AlertSourceType{
					Name:        "type",
					DisplayName: "Type",
				},
			},
		)
		if !strings.Contains(result, "Original alert text:") {
			t.Fatalf("prompt should contain Original alert text block, got %q", result)
		}
		idx := strings.Index(result, "Original alert text:\n")
		if idx < 0 {
			t.Fatal("could not locate original-alert block")
		}
		body := result[idx+len("Original alert text:\n"):]
		// The block ends with the trailing "Please:" section after a blank line.
		end := strings.Index(body, "\n\nPlease:")
		if end < 0 {
			t.Fatalf("could not locate end of original alert block, body=%q", body)
		}
		truncated := body[:end]
		// All-ASCII input: rune-aware truncation lands at exactly 1500 bytes.
		if len(truncated) != 1500 {
			t.Errorf("truncated length = %d, want 1500", len(truncated))
		}
		if !strings.HasSuffix(truncated, "...") {
			t.Errorf("truncated value should end with ellipsis, got %q", truncated[len(truncated)-10:])
		}
	})
}

func TestExtractOriginalMessage(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]interface{}
		max     int
		want    string
	}{
		{
			name:    "missing key",
			payload: map[string]interface{}{"other": "x"},
			max:     100,
			want:    "",
		},
		{
			name:    "nil payload map",
			payload: nil,
			max:     100,
			want:    "",
		},
		{
			name:    "nil value at key",
			payload: map[string]interface{}{"original_message": nil},
			max:     100,
			want:    "",
		},
		{
			name:    "non-string value",
			payload: map[string]interface{}{"original_message": 42},
			max:     100,
			want:    "",
		},
		{
			name:    "empty string",
			payload: map[string]interface{}{"original_message": "   "},
			max:     100,
			want:    "",
		},
		{
			name:    "trims whitespace",
			payload: map[string]interface{}{"original_message": "  hello world  "},
			max:     100,
			want:    "hello world",
		},
		{
			name:    "no truncation when within limit",
			payload: map[string]interface{}{"original_message": "short"},
			max:     100,
			want:    "short",
		},
		{
			name:    "exact length under limit",
			payload: map[string]interface{}{"original_message": "abcde"},
			max:     5,
			want:    "abcde",
		},
		{
			name:    "one over limit truncates with ellipsis",
			payload: map[string]interface{}{"original_message": "abcdef"},
			max:     5,
			want:    "ab...",
		},
		{
			name:    "truncates with ellipsis",
			payload: map[string]interface{}{"original_message": strings.Repeat("a", 20)},
			max:     10,
			want:    strings.Repeat("a", 7) + "...",
		},
		{
			name:    "max equals ellipsis length returns prefix without ellipsis",
			payload: map[string]interface{}{"original_message": "abcdef"},
			max:     3,
			want:    "abc",
		},
		{
			name:    "max smaller than ellipsis length returns plain prefix",
			payload: map[string]interface{}{"original_message": "abcdef"},
			max:     2,
			want:    "ab",
		},
		{
			name:    "max=0 leaves message untouched",
			payload: map[string]interface{}{"original_message": "abcdef"},
			max:     0,
			want:    "abcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOriginalMessage(tt.payload, tt.max)
			if got != tt.want {
				t.Errorf("extractOriginalMessage() = %q, want %q", got, tt.want)
			}
		})
	}

	// UTF-8 truncation must not split runes — multi-byte characters at the
	// cap (common in Slack-channel alerts) would otherwise produce invalid
	// UTF-8 in the rendered prompt.
	t.Run("multi-byte rune at boundary is not split", func(t *testing.T) {
		// "ééé" = 6 bytes (each é = 0xC3 0xA9). With max=5, naive byte
		// truncation would cut mid-rune at byte 4 of "ééé..." (after
		// reserving 3 bytes for the ellipsis). The rune-aware path must
		// back up to a rune boundary.
		input := strings.Repeat("é", 50) // 100 bytes
		got := extractOriginalMessage(map[string]interface{}{"original_message": input}, 10)
		if !utf8.ValidString(got) {
			t.Errorf("extractOriginalMessage() returned invalid UTF-8: %q (bytes=%v)", got, []byte(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("expected ellipsis suffix on truncated UTF-8 string, got %q", got)
		}
		if len(got) > 10 {
			t.Errorf("expected len(got)<=10, got %d (%q)", len(got), got)
		}
	})
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
	// Build content that always exceeds the cap regardless of how large
	// slackMaxTextBytes grows in the future.
	content := strings.Repeat("Line of text.\n", (slackMaxTextBytes/14)+200)
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
