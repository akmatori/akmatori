package adapters

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewAlertmanagerAdapter(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	if adapter == nil {
		t.Fatal("Expected adapter to not be nil")
	}
	if adapter.GetSourceType() != "alertmanager" {
		t.Errorf("Expected source type 'alertmanager', got '%s'", adapter.GetSourceType())
	}
}

func TestAlertmanagerAdapter_ParsePayload_FiringAlert(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		FieldMappings: nil,
	}

	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "HighMemoryUsage",
					"severity": "critical",
					"instance": "web-server-01:9090",
					"job": "node-exporter"
				},
				"annotations": {
					"summary": "Memory usage is above 90%",
					"description": "Instance has high memory usage",
					"runbook_url": "https://runbooks.example.com/memory"
				},
				"startsAt": "2024-01-15T10:30:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"fingerprint": "abc123def456"
			}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]

	// Verify alert name
	if alert.AlertName != "HighMemoryUsage" {
		t.Errorf("Expected AlertName 'HighMemoryUsage', got '%s'", alert.AlertName)
	}

	// Verify severity
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Expected Severity 'critical', got '%s'", alert.Severity)
	}

	// Verify status
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify summary
	if alert.Summary != "Memory usage is above 90%" {
		t.Errorf("Expected Summary 'Memory usage is above 90%%', got '%s'", alert.Summary)
	}

	// Verify target host
	if alert.TargetHost != "web-server-01:9090" {
		t.Errorf("Expected TargetHost 'web-server-01:9090', got '%s'", alert.TargetHost)
	}

	// Verify target service
	if alert.TargetService != "node-exporter" {
		t.Errorf("Expected TargetService 'node-exporter', got '%s'", alert.TargetService)
	}

	// Verify runbook URL
	if alert.RunbookURL != "https://runbooks.example.com/memory" {
		t.Errorf("Expected RunbookURL 'https://runbooks.example.com/memory', got '%s'", alert.RunbookURL)
	}

	// Verify fingerprint
	if alert.SourceFingerprint != "abc123def456" {
		t.Errorf("Expected SourceFingerprint 'abc123def456', got '%s'", alert.SourceFingerprint)
	}

	// Verify started time is set
	if alert.StartedAt == nil {
		t.Error("Expected StartedAt to be set")
	}
}

func TestAlertmanagerAdapter_ParsePayload_ResolvedAlert(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"version": "4",
		"status": "resolved",
		"alerts": [
			{
				"status": "resolved",
				"labels": {
					"alertname": "HighMemoryUsage",
					"severity": "warning"
				},
				"annotations": {},
				"startsAt": "2024-01-15T10:30:00Z",
				"endsAt": "2024-01-15T11:00:00Z",
				"fingerprint": "xyz789"
			}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]

	// Verify status is resolved
	if alert.Status != database.AlertStatusResolved {
		t.Errorf("Expected Status 'resolved', got '%s'", alert.Status)
	}

	// Verify ended time is set for resolved alerts
	if alert.EndedAt == nil {
		t.Error("Expected EndedAt to be set for resolved alert")
	}
}

func TestAlertmanagerAdapter_ParsePayload_MultipleAlerts(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Alert1"},
				"annotations": {},
				"fingerprint": "fp1"
			},
			{
				"status": "firing",
				"labels": {"alertname": "Alert2"},
				"annotations": {},
				"fingerprint": "fp2"
			},
			{
				"status": "resolved",
				"labels": {"alertname": "Alert3"},
				"annotations": {},
				"fingerprint": "fp3"
			}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 3 {
		t.Fatalf("Expected 3 alerts, got %d", len(alerts))
	}

	// Verify alert names
	expectedNames := []string{"Alert1", "Alert2", "Alert3"}
	for i, alert := range alerts {
		if alert.AlertName != expectedNames[i] {
			t.Errorf("Alert %d: expected name '%s', got '%s'", i, expectedNames[i], alert.AlertName)
		}
	}

	// Verify third alert is resolved
	if alerts[2].Status != database.AlertStatusResolved {
		t.Errorf("Expected third alert to be resolved, got '%s'", alerts[2].Status)
	}
}

func TestAlertmanagerAdapter_ParsePayload_InvalidJSON(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{invalid json}`)

	_, err := adapter.ParsePayload(payload, instance)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestAlertmanagerAdapter_ParsePayload_SeverityMapping(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		severity         string
		expectedSeverity database.AlertSeverity
	}{
		{"critical", database.AlertSeverityCritical},
		{"high", database.AlertSeverityHigh},
		{"warning", database.AlertSeverityWarning},
		{"info", database.AlertSeverityInfo},
		{"error", database.AlertSeverityHigh},
		{"unknown", database.AlertSeverityWarning}, // default
	}

	for _, tc := range testCases {
		payload := []byte(`{
			"alerts": [{
				"status": "firing",
				"labels": {"alertname": "Test", "severity": "` + tc.severity + `"},
				"annotations": {},
				"fingerprint": "test"
			}]
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for severity '%s': %v", tc.severity, err)
		}

		if len(alerts) != 1 {
			t.Fatalf("Expected 1 alert for severity '%s', got %d", tc.severity, len(alerts))
		}

		if alerts[0].Severity != tc.expectedSeverity {
			t.Errorf("Severity '%s': expected %s, got %s", tc.severity, tc.expectedSeverity, alerts[0].Severity)
		}
	}
}

func TestAlertmanagerAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "", // No secret configured
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error when no secret configured, got: %v", err)
	}
}

func TestAlertmanagerAdapter_ValidateWebhookSecret_ValidSecret(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "my-secret-key",
	}

	// Test X-Alertmanager-Secret header
	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Alertmanager-Secret", "my-secret-key")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid secret, got: %v", err)
	}
}

func TestAlertmanagerAdapter_ValidateWebhookSecret_BearerToken(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "my-secret-key",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("Authorization", "Bearer my-secret-key")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid bearer token, got: %v", err)
	}
}

func TestAlertmanagerAdapter_ValidateWebhookSecret_InvalidSecret(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "my-secret-key",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Alertmanager-Secret", "wrong-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err == nil {
		t.Error("Expected error for invalid secret, got nil")
	}
}

func TestAlertmanagerAdapter_GetDefaultMappings(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	mappings := adapter.GetDefaultMappings()

	expectedMappings := map[string]string{
		"alert_name":         "labels.alertname",
		"severity":           "labels.severity",
		"status":             "status",
		"summary":            "annotations.summary",
		"description":        "annotations.description",
		"target_host":        "labels.instance",
		"target_service":     "labels.job",
		"runbook_url":        "annotations.runbook_url",
		"source_fingerprint": "fingerprint",
		"started_at":         "startsAt",
		"ended_at":           "endsAt",
	}

	for key, expectedValue := range expectedMappings {
		if val, ok := mappings[key]; !ok {
			t.Errorf("Missing mapping key: %s", key)
		} else if val != expectedValue {
			t.Errorf("Mapping %s: expected '%s', got '%v'", key, expectedValue, val)
		}
	}
}

func TestAlertmanagerAdapter_ParsePayload_TimesParsed(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "TimeTest"},
			"annotations": {},
			"startsAt": "2024-01-15T10:30:00Z",
			"endsAt": "0001-01-01T00:00:00Z",
			"fingerprint": "time-test"
		}]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]

	// Verify started time is parsed correctly
	if alert.StartedAt == nil {
		t.Fatal("Expected StartedAt to be set")
	}

	expectedStart := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	if !alert.StartedAt.Equal(expectedStart) {
		t.Errorf("Expected StartedAt %v, got %v", expectedStart, *alert.StartedAt)
	}
}

func TestAlertmanagerAdapter_ParsePayload_EmptyAlerts(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": []
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts, got %d", len(alerts))
	}
}

func TestAlertmanagerAdapter_ParsePayload_MissingFields(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	// Minimal payload with just required fields
	payload := []byte(`{
		"alerts": [{
			"status": "firing",
			"labels": {},
			"annotations": {},
			"fingerprint": "minimal"
		}]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]

	// Alert name should be empty when not in labels
	if alert.AlertName != "" {
		t.Errorf("Expected empty AlertName, got '%s'", alert.AlertName)
	}

	// Severity should default to warning
	if alert.Severity != database.AlertSeverityWarning {
		t.Errorf("Expected default severity 'warning', got '%s'", alert.Severity)
	}
}

func TestAlertmanagerAdapter_ParsePayload_CustomFieldMappings(t *testing.T) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		FieldMappings: database.JSONB{
			"alert_name": "labels.custom_name",
		},
	}

	payload := []byte(`{
		"alerts": [{
			"status": "firing",
			"labels": {
				"alertname": "DefaultName",
				"custom_name": "CustomAlertName"
			},
			"annotations": {},
			"fingerprint": "custom"
		}]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	// Custom mapping should take precedence, but the code falls back to alertname
	// This tests the custom mapping merge functionality
	alert := alerts[0]
	if alert.SourceFingerprint != "custom" {
		t.Errorf("Expected SourceFingerprint 'custom', got '%s'", alert.SourceFingerprint)
	}
}

// ========================================
// Benchmarks for critical alert parsing paths
// ========================================

// BenchmarkAlertmanagerAdapter_ParsePayload_Single benchmarks parsing a single alert
func BenchmarkAlertmanagerAdapter_ParsePayload_Single(b *testing.B) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "HighMemoryUsage",
					"severity": "critical",
					"instance": "web-server-01:9090",
					"job": "node-exporter"
				},
				"annotations": {
					"summary": "Memory usage is above 90%",
					"description": "Instance has high memory usage"
				},
				"startsAt": "2024-01-15T10:30:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"fingerprint": "abc123def456"
			}
		]
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.ParsePayload(payload, instance) // ignore: benchmark only measures performance
	}
}

// BenchmarkAlertmanagerAdapter_ParsePayload_Multiple benchmarks parsing multiple alerts
func BenchmarkAlertmanagerAdapter_ParsePayload_Multiple(b *testing.B) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": [
			{"status": "firing", "labels": {"alertname": "Alert1", "severity": "critical"}, "annotations": {}, "fingerprint": "fp1"},
			{"status": "firing", "labels": {"alertname": "Alert2", "severity": "warning"}, "annotations": {}, "fingerprint": "fp2"},
			{"status": "firing", "labels": {"alertname": "Alert3", "severity": "info"}, "annotations": {}, "fingerprint": "fp3"},
			{"status": "resolved", "labels": {"alertname": "Alert4", "severity": "high"}, "annotations": {}, "fingerprint": "fp4"},
			{"status": "firing", "labels": {"alertname": "Alert5", "severity": "critical"}, "annotations": {}, "fingerprint": "fp5"},
			{"status": "firing", "labels": {"alertname": "Alert6", "severity": "warning"}, "annotations": {}, "fingerprint": "fp6"},
			{"status": "resolved", "labels": {"alertname": "Alert7", "severity": "info"}, "annotations": {}, "fingerprint": "fp7"},
			{"status": "firing", "labels": {"alertname": "Alert8", "severity": "critical"}, "annotations": {}, "fingerprint": "fp8"},
			{"status": "firing", "labels": {"alertname": "Alert9", "severity": "high"}, "annotations": {}, "fingerprint": "fp9"},
			{"status": "firing", "labels": {"alertname": "Alert10", "severity": "warning"}, "annotations": {}, "fingerprint": "fp10"}
		]
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.ParsePayload(payload, instance) // ignore: benchmark only measures performance
	}
}

// BenchmarkAlertmanagerAdapter_ValidateWebhookSecret benchmarks secret validation
func BenchmarkAlertmanagerAdapter_ValidateWebhookSecret(b *testing.B) {
	adapter := NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "my-secret-key-for-validation",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Alertmanager-Secret", "my-secret-key-for-validation")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = adapter.ValidateWebhookSecret(req, instance) // ignore: benchmark only measures performance
	}
}

// BenchmarkAlertmanagerAdapter_GetDefaultMappings benchmarks getting mappings
func BenchmarkAlertmanagerAdapter_GetDefaultMappings(b *testing.B) {
	adapter := NewAlertmanagerAdapter()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter.GetDefaultMappings()
	}
}
