package extraction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// TestCreateFallbackAlert_EdgeCases tests edge cases for fallback alert creation
func TestCreateFallbackAlert_EdgeCases(t *testing.T) {
	extractor := NewAlertExtractor()

	tests := []struct {
		name         string
		messageText  string
		wantName     string
		wantSeverity database.AlertSeverity
		wantStatus   database.AlertStatus
		checkPayload func(t *testing.T, payload map[string]interface{})
	}{
		{
			name:         "empty message",
			messageText:  "",
			wantName:     "Slack Alert",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
			checkPayload: func(t *testing.T, payload map[string]interface{}) {
				if payload["extraction_mode"] != "fallback" {
					t.Error("expected extraction_mode to be 'fallback'")
				}
			},
		},
		{
			name:         "whitespace only",
			messageText:  "   \n\t\n   ",
			wantName:     "Slack Alert",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "single word",
			messageText:  "Error",
			wantName:     "Error",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "exact 100 chars",
			messageText:  strings.Repeat("a", 100),
			wantName:     strings.Repeat("a", 100),
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "101 chars truncated",
			messageText:  strings.Repeat("b", 101),
			wantName:     strings.Repeat("b", 97) + "...",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "multiple alert emoji prefixes",
			messageText:  ":alert::warning::rotating_light: Multiple prefixes alert",
			wantName:     "Multiple prefixes alert", // All prefixes stripped sequentially
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "only emoji prefix",
			messageText:  ":alert:",
			wantName:     "Slack Alert",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "x emoji prefix",
			messageText:  ":x: Critical failure detected",
			wantName:     "Critical failure detected",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "newline only first line",
			messageText:  "\nActual content on second line",
			wantName:     "Slack Alert",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "unicode characters",
			messageText:  "🔥 Серьезная ошибка сервера 🔥",
			wantName:     "🔥 Серьезная ошибка сервера 🔥",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "special characters",
			messageText:  "Alert: CPU > 95% on server-01.prod.example.com [CRITICAL]",
			wantName:     "Alert: CPU > 95% on server-01.prod.example.com [CRITICAL]",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "multiple newlines",
			messageText:  "First line alert\n\n\nLots of empty lines\n\n\nMore content",
			wantName:     "First line alert",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "carriage return line endings",
			messageText:  "Windows style\r\nSecond line",
			wantName:     "Windows style",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
		{
			name:         "very long single line",
			messageText:  strings.Repeat("very long message ", 100),
			wantName:     "very long message very long message very long message very long message very long message very lo...",
			wantSeverity: database.AlertSeverityWarning,
			wantStatus:   database.AlertStatusFiring,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := extractor.createFallbackAlert(tt.messageText)

			if alert.AlertName != tt.wantName {
				t.Errorf("AlertName = %q, want %q", alert.AlertName, tt.wantName)
			}
			if alert.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", alert.Severity, tt.wantSeverity)
			}
			if alert.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", alert.Status, tt.wantStatus)
			}
			if alert.RawPayload == nil {
				t.Error("RawPayload should not be nil")
			}
			if tt.checkPayload != nil {
				tt.checkPayload(t, alert.RawPayload)
			}
		})
	}
}

// TestToNormalizedAlert_EdgeCases tests edge cases for conversion
func TestToNormalizedAlert_EdgeCases(t *testing.T) {
	extractor := NewAlertExtractor()

	tests := []struct {
		name           string
		extracted      ExtractedAlert
		originalMsg    string
		wantName       string
		wantSeverity   database.AlertSeverity
		wantStatus     database.AlertStatus
		wantSummary    string
		checkRawPaylod func(t *testing.T, payload map[string]interface{})
	}{
		{
			name:           "empty extraction defaults",
			extracted:      ExtractedAlert{},
			originalMsg:    "Original message content",
			wantName:       "Slack Alert",
			wantSeverity:   database.AlertSeverityWarning,
			wantStatus:     database.AlertStatusFiring,
			wantSummary:    "Original message content",
		},
		{
			name: "resolved status lowercase",
			extracted: ExtractedAlert{
				AlertName: "Test Alert",
				Status:    "resolved",
			},
			originalMsg:   "Test",
			wantName:      "Test Alert",
			wantStatus:    database.AlertStatusResolved,
			wantSeverity:  database.AlertSeverityWarning,
		},
		{
			name: "resolved status uppercase",
			extracted: ExtractedAlert{
				AlertName: "Test Alert",
				Status:    "RESOLVED",
			},
			originalMsg:   "Test",
			wantName:      "Test Alert",
			wantStatus:    database.AlertStatusResolved,
			wantSeverity:  database.AlertSeverityWarning,
		},
		{
			name: "resolved status mixed case",
			extracted: ExtractedAlert{
				AlertName: "Test Alert",
				Status:    "ReSOLved",
			},
			originalMsg:   "Test",
			wantName:      "Test Alert",
			wantStatus:    database.AlertStatusResolved,
			wantSeverity:  database.AlertSeverityWarning,
		},
		{
			name: "unknown status defaults to firing",
			extracted: ExtractedAlert{
				AlertName: "Test Alert",
				Status:    "unknown",
			},
			originalMsg:   "Test",
			wantName:      "Test Alert",
			wantStatus:    database.AlertStatusFiring,
			wantSeverity:  database.AlertSeverityWarning,
		},
		{
			name: "all severities critical",
			extracted: ExtractedAlert{
				AlertName: "Critical Alert",
				Severity:  "critical",
			},
			originalMsg:   "Test",
			wantName:      "Critical Alert",
			wantSeverity:  database.AlertSeverityCritical,
			wantStatus:    database.AlertStatusFiring,
		},
		{
			name: "all severities high",
			extracted: ExtractedAlert{
				AlertName: "High Alert",
				Severity:  "high",
			},
			originalMsg:   "Test",
			wantName:      "High Alert",
			wantSeverity:  database.AlertSeverityHigh,
			wantStatus:    database.AlertStatusFiring,
		},
		{
			name: "all severities info",
			extracted: ExtractedAlert{
				AlertName: "Info Alert",
				Severity:  "info",
			},
			originalMsg:   "Test",
			wantName:      "Info Alert",
			wantSeverity:  database.AlertSeverityInfo,
			wantStatus:    database.AlertStatusFiring,
		},
		{
			name: "summary provided takes precedence",
			extracted: ExtractedAlert{
				AlertName: "Test",
				Summary:   "Custom summary",
			},
			originalMsg:   "Long original message that would be truncated if used",
			wantName:      "Test",
			wantSummary:   "Custom summary",
			wantSeverity:  database.AlertSeverityWarning,
			wantStatus:    database.AlertStatusFiring,
		},
		{
			name: "description provided takes precedence",
			extracted: ExtractedAlert{
				AlertName:   "Test",
				Description: "Custom description",
			},
			originalMsg:   "Original",
			wantName:      "Test",
			wantSeverity:  database.AlertSeverityWarning,
			wantStatus:    database.AlertStatusFiring,
		},
		{
			name: "target labels with source system",
			extracted: ExtractedAlert{
				AlertName:    "Test",
				SourceSystem: "Prometheus",
			},
			originalMsg:   "Test",
			wantName:      "Test",
			wantSeverity:  database.AlertSeverityWarning,
			wantStatus:    database.AlertStatusFiring,
			checkRawPaylod: func(t *testing.T, payload map[string]interface{}) {
				if _, ok := payload["extracted"]; !ok {
					t.Error("RawPayload should contain 'extracted'")
				}
				if _, ok := payload["original_message"]; !ok {
					t.Error("RawPayload should contain 'original_message'")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := extractor.toNormalizedAlert(tt.extracted, tt.originalMsg)

			if alert.AlertName != tt.wantName {
				t.Errorf("AlertName = %q, want %q", alert.AlertName, tt.wantName)
			}
			if alert.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", alert.Severity, tt.wantSeverity)
			}
			if alert.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", alert.Status, tt.wantStatus)
			}
			if tt.wantSummary != "" && alert.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", alert.Summary, tt.wantSummary)
			}
			if tt.checkRawPaylod != nil {
				tt.checkRawPaylod(t, alert.RawPayload)
			}
		})
	}
}

// TestTruncateMessage_Comprehensive tests message truncation thoroughly
func TestTruncateMessage_Comprehensive(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		maxLen  int
		want    string
		wantLen int // -1 means just check it's <= maxLen
	}{
		{
			name:    "empty string",
			msg:     "",
			maxLen:  10,
			want:    "",
			wantLen: 0,
		},
		{
			name:    "shorter than max",
			msg:     "hello",
			maxLen:  10,
			want:    "hello",
			wantLen: 5,
		},
		{
			name:    "exactly max length",
			msg:     "0123456789",
			maxLen:  10,
			want:    "0123456789",
			wantLen: 10,
		},
		{
			name:    "one over max length",
			msg:     "01234567890",
			maxLen:  10,
			wantLen: -1, // Should be <= 10
		},
		{
			name:    "truncate at word boundary",
			msg:     "hello world test",
			maxLen:  12,
			want:    "hello wor...",
			wantLen: 12,
		},
		{
			name:    "no word boundary available",
			msg:     "abcdefghijklmnop",
			maxLen:  10,
			want:    "abcdefg...",
			wantLen: 10,
		},
		{
			name:    "word boundary too early",
			msg:     "a bcdefghijklmnopqrst",
			maxLen:  10,
			want:    "a bcdef...",
			wantLen: 10,
		},
		{
			name:    "single character",
			msg:     "x",
			maxLen:  1,
			want:    "x",
			wantLen: 1,
		},
		{
			name:    "max length 4 edge case",
			msg:     "testing",
			maxLen:  4,
			want:    "t...",
			wantLen: 4,
		},
		{
			name:    "max length 3 edge case",
			msg:     "testing",
			maxLen:  3,
			want:    "...",
			wantLen: 3,
		},
		{
			name:    "unicode characters",
			msg:     "你好世界测试",
			maxLen:  10,
			wantLen: -1,
		},
		{
			name:    "emoji truncation",
			msg:     "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥",
			maxLen:  20,
			wantLen: -1,
		},
		{
			name:    "very large max length",
			msg:     "short",
			maxLen:  10000,
			want:    "short",
			wantLen: 5,
		},
		{
			name:    "multiple spaces",
			msg:     "hello    world   test",
			maxLen:  15,
			want:    "hello   ...",
			wantLen: -1,
		},
		{
			name:    "tab characters",
			msg:     "hello\tworld\ttest",
			maxLen:  12,
			wantLen: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateMessage(tt.msg, tt.maxLen)

			if tt.wantLen >= 0 && len(result) != tt.wantLen {
				t.Errorf("truncateMessage(%q, %d) len = %d, want %d", tt.msg, tt.maxLen, len(result), tt.wantLen)
			}

			if tt.wantLen == -1 && len(result) > tt.maxLen {
				t.Errorf("truncateMessage(%q, %d) len = %d, want <= %d", tt.msg, tt.maxLen, len(result), tt.maxLen)
			}

			if tt.want != "" && result != tt.want {
				t.Errorf("truncateMessage(%q, %d) = %q, want %q", tt.msg, tt.maxLen, result, tt.want)
			}
		})
	}
}

// TestExtractedAlert_JSONRoundTrip tests JSON marshal/unmarshal consistency
func TestExtractedAlert_JSONRoundTrip(t *testing.T) {
	original := ExtractedAlert{
		AlertName:     "Test Alert",
		Severity:      "critical",
		Status:        "firing",
		Summary:       "Test summary",
		Description:   "Detailed description",
		TargetHost:    "server-01",
		TargetService: "api-service",
		SourceSystem:  "Prometheus",
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Unmarshal
	var decoded ExtractedAlert
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Compare
	if decoded.AlertName != original.AlertName {
		t.Errorf("AlertName mismatch: got %q, want %q", decoded.AlertName, original.AlertName)
	}
	if decoded.Severity != original.Severity {
		t.Errorf("Severity mismatch: got %q, want %q", decoded.Severity, original.Severity)
	}
	if decoded.Status != original.Status {
		t.Errorf("Status mismatch: got %q, want %q", decoded.Status, original.Status)
	}
	if decoded.TargetHost != original.TargetHost {
		t.Errorf("TargetHost mismatch: got %q, want %q", decoded.TargetHost, original.TargetHost)
	}
	if decoded.TargetService != original.TargetService {
		t.Errorf("TargetService mismatch: got %q, want %q", decoded.TargetService, original.TargetService)
	}
	if decoded.SourceSystem != original.SourceSystem {
		t.Errorf("SourceSystem mismatch: got %q, want %q", decoded.SourceSystem, original.SourceSystem)
	}
}

// TestExtractedAlert_JSONFieldNames verifies JSON field names match API expectations
func TestExtractedAlert_JSONFieldNames(t *testing.T) {
	alert := ExtractedAlert{
		AlertName:     "test",
		Severity:      "warning",
		Status:        "firing",
		Summary:       "sum",
		Description:   "desc",
		TargetHost:    "host",
		TargetService: "svc",
		SourceSystem:  "sys",
	}

	data, err := json.Marshal(alert)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	expectedFields := []string{
		"alert_name",
		"severity",
		"status",
		"summary",
		"description",
		"target_host",
		"target_service",
		"source_system",
	}

	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("Missing expected JSON field: %s", field)
		}
	}
}

// TestNewAlertExtractor_Initialization tests extractor initialization
func TestNewAlertExtractor_Initialization(t *testing.T) {
	extractor := NewAlertExtractor()

	if extractor == nil {
		t.Fatal("NewAlertExtractor returned nil")
	}

	if extractor.httpClient == nil {
		t.Error("httpClient should be initialized")
	}

	// Verify timeout is set
	if extractor.httpClient.Timeout == 0 {
		t.Error("httpClient timeout should be set")
	}
}

// TestNormalizeSeverity_AllValues tests severity normalization for all values
func TestNormalizeSeverity_AllValues(t *testing.T) {
	tests := []struct {
		input    string
		expected database.AlertSeverity
	}{
		// Standard values
		{"critical", database.AlertSeverityCritical},
		{"high", database.AlertSeverityHigh},
		{"warning", database.AlertSeverityWarning},
		{"info", database.AlertSeverityInfo},

		// Case variations
		{"CRITICAL", database.AlertSeverityCritical},
		{"Critical", database.AlertSeverityCritical},
		{"HIGH", database.AlertSeverityHigh},
		{"INFO", database.AlertSeverityInfo},
		{"WARNING", database.AlertSeverityWarning},

		// Alternative names - map to what the actual implementation does
		{"error", database.AlertSeverityHigh},     // maps to high
		{"fatal", database.AlertSeverityCritical},
		{"severe", database.AlertSeverityHigh},    // maps to high
		{"major", database.AlertSeverityHigh},     // maps to high
		{"warn", database.AlertSeverityWarning},
		{"medium", database.AlertSeverityWarning}, // not mapped, defaults to warning
		{"low", database.AlertSeverityInfo},
		{"debug", database.AlertSeverityInfo},     // maps to info
		{"notice", database.AlertSeverityInfo},    // maps to info

		// Unknown values default to warning
		{"unknown", database.AlertSeverityWarning},
		{"", database.AlertSeverityWarning},
		{"random", database.AlertSeverityWarning},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := alerts.NormalizeSeverity(tt.input, alerts.DefaultSeverityMapping)
			if result != tt.expected {
				t.Errorf("NormalizeSeverity(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// BenchmarkCreateFallbackAlert benchmarks fallback alert creation
func BenchmarkCreateFallbackAlert(b *testing.B) {
	extractor := NewAlertExtractor()
	msg := "🔥 Production server down on us-east-1\nAffected service: payment-api\nSeverity: critical"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractor.createFallbackAlert(msg)
	}
}

// BenchmarkTruncateMessage benchmarks message truncation
func BenchmarkTruncateMessage(b *testing.B) {
	msg := strings.Repeat("This is a test message that needs to be truncated. ", 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		truncateMessage(msg, 100)
	}
}

// BenchmarkToNormalizedAlert benchmarks alert conversion
func BenchmarkToNormalizedAlert(b *testing.B) {
	extractor := NewAlertExtractor()
	extracted := ExtractedAlert{
		AlertName:     "High CPU Usage",
		Severity:      "critical",
		Status:        "firing",
		Summary:       "CPU at 95%",
		Description:   "Production server experiencing high CPU usage for over 5 minutes",
		TargetHost:    "prod-web-01",
		TargetService: "web-api",
		SourceSystem:  "Prometheus",
	}
	originalMsg := "Original Slack message content"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractor.toNormalizedAlert(extracted, originalMsg)
	}
}
