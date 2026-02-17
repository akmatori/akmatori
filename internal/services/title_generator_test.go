package services

import (
	"strings"
	"testing"
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
