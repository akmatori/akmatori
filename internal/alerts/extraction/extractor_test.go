package extraction

import (
	"encoding/json"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewAlertExtractor(t *testing.T) {
	extractor := NewAlertExtractor()
	if extractor == nil {
		t.Error("NewAlertExtractor() returned nil")
	}
	if extractor.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestAlertExtractor_createFallbackAlert(t *testing.T) {
	extractor := NewAlertExtractor()

	tests := []struct {
		name           string
		messageText    string
		expectedName   string
		expectedStatus database.AlertStatus
	}{
		{
			name:           "simple message",
			messageText:    "Server is down",
			expectedName:   "Server is down",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "message with alert emoji",
			messageText:    ":alert: CPU usage critical on prod-web-01",
			expectedName:   "CPU usage critical on prod-web-01",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "message with warning emoji",
			messageText:    ":warning: High memory usage detected",
			expectedName:   "High memory usage detected",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "message with rotating light emoji",
			messageText:    ":rotating_light: Production database slow queries",
			expectedName:   "Production database slow queries",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "multiline message",
			messageText:    "Alert: API Gateway Error\nDetails: 500 errors detected\nService: payment-api",
			expectedName:   "Alert: API Gateway Error",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "long first line",
			messageText:    "This is a very long alert message that exceeds the maximum allowed length for the alert name field and should be truncated properly",
			expectedName:   "This is a very long alert message that exceeds the maximum allowed length for the alert name fiel...",
			expectedStatus: database.AlertStatusFiring,
		},
		{
			name:           "empty message",
			messageText:    "",
			expectedName:   "Slack Alert",
			expectedStatus: database.AlertStatusFiring,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := extractor.createFallbackAlert(tt.messageText)

			if alert.AlertName != tt.expectedName {
				t.Errorf("AlertName = %q, want %q", alert.AlertName, tt.expectedName)
			}
			if alert.Status != tt.expectedStatus {
				t.Errorf("Status = %v, want %v", alert.Status, tt.expectedStatus)
			}
			if alert.Severity != database.AlertSeverityWarning {
				t.Errorf("Severity = %v, want %v", alert.Severity, database.AlertSeverityWarning)
			}
			if alert.Description != tt.messageText {
				t.Errorf("Description = %q, want %q", alert.Description, tt.messageText)
			}
			if alert.RawPayload == nil {
				t.Error("RawPayload should not be nil")
			}
			if alert.RawPayload["extraction_mode"] != "fallback" {
				t.Error("RawPayload should contain extraction_mode = fallback")
			}
		})
	}
}

func TestAlertExtractor_toNormalizedAlert(t *testing.T) {
	extractor := NewAlertExtractor()

	tests := []struct {
		name            string
		extracted       ExtractedAlert
		originalMessage string
		expectedName    string
		expectedSev     database.AlertSeverity
		expectedStatus  database.AlertStatus
	}{
		{
			name: "complete extraction",
			extracted: ExtractedAlert{
				AlertName:     "High CPU Usage",
				Severity:      "critical",
				Status:        "firing",
				Summary:       "CPU at 95%",
				Description:   "Production server experiencing high CPU usage",
				TargetHost:    "prod-web-01",
				TargetService: "web-api",
				SourceSystem:  "Prometheus",
			},
			originalMessage: "Alert from monitoring",
			expectedName:    "High CPU Usage",
			expectedSev:     database.AlertSeverityCritical,
			expectedStatus:  database.AlertStatusFiring,
		},
		{
			name: "resolved status",
			extracted: ExtractedAlert{
				AlertName: "Memory Issue",
				Severity:  "warning",
				Status:    "resolved",
			},
			originalMessage: "Memory back to normal",
			expectedName:    "Memory Issue",
			expectedSev:     database.AlertSeverityWarning,
			expectedStatus:  database.AlertStatusResolved,
		},
		{
			name: "empty alert name fallback",
			extracted: ExtractedAlert{
				AlertName: "",
				Severity:  "high",
			},
			originalMessage: "Some alert",
			expectedName:    "Slack Alert",
			expectedSev:     database.AlertSeverityHigh,
			expectedStatus:  database.AlertStatusFiring,
		},
		{
			name: "info severity",
			extracted: ExtractedAlert{
				AlertName: "Deployment Complete",
				Severity:  "info",
				Status:    "firing",
			},
			originalMessage: "Deployment finished",
			expectedName:    "Deployment Complete",
			expectedSev:     database.AlertSeverityInfo,
			expectedStatus:  database.AlertStatusFiring,
		},
		{
			name: "unknown severity defaults to warning",
			extracted: ExtractedAlert{
				AlertName: "Unknown Issue",
				Severity:  "unknown",
			},
			originalMessage: "Something happened",
			expectedName:    "Unknown Issue",
			expectedSev:     database.AlertSeverityWarning,
			expectedStatus:  database.AlertStatusFiring,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := extractor.toNormalizedAlert(tt.extracted, tt.originalMessage)

			if alert.AlertName != tt.expectedName {
				t.Errorf("AlertName = %q, want %q", alert.AlertName, tt.expectedName)
			}
			if alert.Severity != tt.expectedSev {
				t.Errorf("Severity = %v, want %v", alert.Severity, tt.expectedSev)
			}
			if alert.Status != tt.expectedStatus {
				t.Errorf("Status = %v, want %v", alert.Status, tt.expectedStatus)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		name     string
		msg      string
		maxLen   int
		expected string
	}{
		{
			name:     "short message",
			msg:      "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length",
			msg:      "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "truncate with word boundary",
			msg:      "hello world this is a test",
			maxLen:   15,
			expected: "hello world...",
		},
		{
			name:     "truncate no word boundary",
			msg:      "helloworldthisisatest",
			maxLen:   15,
			expected: "helloworldth...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateMessage(tt.msg, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateMessage(%q, %d) = %q, want %q", tt.msg, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestExtractedAlert_JSONMarshaling(t *testing.T) {
	// Test that ExtractedAlert can be properly unmarshaled from JSON
	// This is important because we expect OpenAI to return this format
	jsonData := `{
		"alert_name": "Test Alert",
		"severity": "critical",
		"status": "firing",
		"summary": "Test summary",
		"description": "Test description",
		"target_host": "server01",
		"target_service": "api",
		"source_system": "Prometheus"
	}`

	var alert ExtractedAlert
	if err := json.Unmarshal([]byte(jsonData), &alert); err != nil {
		t.Fatalf("Failed to unmarshal ExtractedAlert: %v", err)
	}

	if alert.AlertName != "Test Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Test Alert")
	}
	if alert.Severity != "critical" {
		t.Errorf("Severity = %q, want %q", alert.Severity, "critical")
	}
	if alert.Status != "firing" {
		t.Errorf("Status = %q, want %q", alert.Status, "firing")
	}
	if alert.TargetHost != "server01" {
		t.Errorf("TargetHost = %q, want %q", alert.TargetHost, "server01")
	}
}

func TestExtractedAlert_JSONWithNulls(t *testing.T) {
	// Test that ExtractedAlert handles null values (which OpenAI may return)
	jsonData := `{
		"alert_name": "Test Alert",
		"severity": "warning",
		"status": null,
		"summary": null,
		"description": "Test description",
		"target_host": null,
		"target_service": null,
		"source_system": null
	}`

	var alert ExtractedAlert
	if err := json.Unmarshal([]byte(jsonData), &alert); err != nil {
		t.Fatalf("Failed to unmarshal ExtractedAlert with nulls: %v", err)
	}

	if alert.AlertName != "Test Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Test Alert")
	}
	if alert.Status != "" {
		t.Errorf("Status = %q, want empty string", alert.Status)
	}
	if alert.TargetHost != "" {
		t.Errorf("TargetHost = %q, want empty string", alert.TargetHost)
	}
}
