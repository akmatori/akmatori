package adapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewGrafanaAdapter(t *testing.T) {
	adapter := NewGrafanaAdapter()
	if adapter == nil {
		t.Fatal("Expected adapter to not be nil")
	}
	if adapter.GetSourceType() != "grafana" {
		t.Errorf("Expected source type 'grafana', got '%s'", adapter.GetSourceType())
	}
}

func TestGrafanaAdapter_ParsePayload_UnifiedAlerting_Firing(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"receiver": "akmatori",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "DiskSpaceLow",
					"severity": "warning",
					"instance": "storage-01:9100",
					"job": "node-exporter",
					"disk": "/dev/sda1"
				},
				"annotations": {
					"summary": "Disk space is below 10%",
					"description": "Storage server has low disk space",
					"runbook_url": "https://runbooks.example.com/disk"
				},
				"startsAt": "2024-01-15T10:30:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"fingerprint": "gra123",
				"generatorURL": "http://grafana:3000/alerting"
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
	if alert.AlertName != "DiskSpaceLow" {
		t.Errorf("Expected AlertName 'DiskSpaceLow', got '%s'", alert.AlertName)
	}

	// Verify severity
	if alert.Severity != database.AlertSeverityWarning {
		t.Errorf("Expected Severity 'warning', got '%s'", alert.Severity)
	}

	// Verify status
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify target host
	if alert.TargetHost != "storage-01:9100" {
		t.Errorf("Expected TargetHost 'storage-01:9100', got '%s'", alert.TargetHost)
	}

	// Verify target service
	if alert.TargetService != "node-exporter" {
		t.Errorf("Expected TargetService 'node-exporter', got '%s'", alert.TargetService)
	}

	// Verify summary
	if alert.Summary != "Disk space is below 10%" {
		t.Errorf("Expected Summary, got '%s'", alert.Summary)
	}

	// Verify runbook URL
	if alert.RunbookURL != "https://runbooks.example.com/disk" {
		t.Errorf("Expected RunbookURL, got '%s'", alert.RunbookURL)
	}

	// Verify fingerprint
	if alert.SourceFingerprint != "gra123" {
		t.Errorf("Expected SourceFingerprint 'gra123', got '%s'", alert.SourceFingerprint)
	}
}

func TestGrafanaAdapter_ParsePayload_UnifiedAlerting_Resolved(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"receiver": "akmatori",
		"status": "resolved",
		"alerts": [
			{
				"status": "resolved",
				"labels": {"alertname": "TestAlert"},
				"annotations": {},
				"fingerprint": "res123"
			}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].Status != database.AlertStatusResolved {
		t.Errorf("Expected Status 'resolved', got '%s'", alerts[0].Status)
	}
}

func TestGrafanaAdapter_ParsePayload_UnifiedAlerting_MultipleAlerts(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"receiver": "akmatori",
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
}

func TestGrafanaAdapter_ParsePayload_LegacyAlerting(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"ruleName": "CPU Alert",
		"state": "alerting",
		"message": "CPU usage is high",
		"ruleUrl": "http://grafana:3000/d/abc123",
		"ruleId": 42,
		"title": "CPU Alert Title",
		"orgId": 1,
		"dashboardId": 10,
		"panelId": 5,
		"evalMatches": [
			{
				"value": 95.5,
				"metric": "cpu_usage",
				"tags": {
					"instance": "web-01:9100",
					"job": "node"
				}
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

	// Verify alert name (from ruleName)
	if alert.AlertName != "CPU Alert" {
		t.Errorf("Expected AlertName 'CPU Alert', got '%s'", alert.AlertName)
	}

	// Verify status (alerting = firing)
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify severity (alerting state = critical)
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Expected Severity 'critical' for alerting state, got '%s'", alert.Severity)
	}

	// Verify target host from evalMatches
	if alert.TargetHost != "web-01:9100" {
		t.Errorf("Expected TargetHost 'web-01:9100', got '%s'", alert.TargetHost)
	}

	// Verify metric value
	if alert.MetricValue != "95.5" {
		t.Errorf("Expected MetricValue '95.5', got '%s'", alert.MetricValue)
	}

	// Verify runbook URL (ruleUrl)
	if alert.RunbookURL != "http://grafana:3000/d/abc123" {
		t.Errorf("Expected RunbookURL, got '%s'", alert.RunbookURL)
	}
}

func TestGrafanaAdapter_ParsePayload_LegacyAlerting_States(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		state            string
		expectedStatus   database.AlertStatus
		expectedSeverity database.AlertSeverity
	}{
		{"alerting", database.AlertStatusFiring, database.AlertSeverityCritical},
		{"pending", database.AlertStatusFiring, database.AlertSeverityWarning},
		{"ok", database.AlertStatusResolved, database.AlertSeverityInfo},
		{"no_data", database.AlertStatusResolved, database.AlertSeverityInfo},
		{"paused", database.AlertStatusResolved, database.AlertSeverityInfo},
	}

	for _, tc := range testCases {
		payload := []byte(`{
			"ruleName": "Test",
			"state": "` + tc.state + `",
			"ruleId": 1
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for state '%s': %v", tc.state, err)
		}

		if alerts[0].Status != tc.expectedStatus {
			t.Errorf("State '%s': expected status %s, got %s", tc.state, tc.expectedStatus, alerts[0].Status)
		}

		if alerts[0].Severity != tc.expectedSeverity {
			t.Errorf("State '%s': expected severity %s, got %s", tc.state, tc.expectedSeverity, alerts[0].Severity)
		}
	}
}

func TestGrafanaAdapter_ParsePayload_InvalidJSON(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{invalid}`)

	_, err := adapter.ParsePayload(payload, instance)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestGrafanaAdapter_ParsePayload_EmptyAlerts(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	// Empty alerts array should fall back to legacy format
	payload := []byte(`{
		"receiver": "test",
		"status": "firing",
		"alerts": [],
		"ruleName": "FallbackRule",
		"state": "alerting",
		"ruleId": 99
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert from legacy fallback, got %d", len(alerts))
	}

	if alerts[0].AlertName != "FallbackRule" {
		t.Errorf("Expected AlertName 'FallbackRule', got '%s'", alerts[0].AlertName)
	}
}

func TestGrafanaAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error when no secret configured, got: %v", err)
	}
}

func TestGrafanaAdapter_ValidateWebhookSecret_ValidHeader(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "grafana-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Grafana-Secret", "grafana-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid secret, got: %v", err)
	}
}

func TestGrafanaAdapter_ValidateWebhookSecret_BearerToken(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "grafana-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("Authorization", "Bearer grafana-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid bearer token, got: %v", err)
	}
}

func TestGrafanaAdapter_ValidateWebhookSecret_InvalidSecret(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "correct-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Grafana-Secret", "wrong-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err == nil {
		t.Error("Expected error for invalid secret, got nil")
	}
}

func TestGrafanaAdapter_GetDefaultMappings(t *testing.T) {
	adapter := NewGrafanaAdapter()
	mappings := adapter.GetDefaultMappings()

	expectedKeys := []string{
		"alert_name",
		"severity",
		"status",
		"summary",
		"target_host",
		"runbook_url",
		"source_alert_id",
	}

	for _, key := range expectedKeys {
		if _, ok := mappings[key]; !ok {
			t.Errorf("Missing expected mapping key: %s", key)
		}
	}
}

func TestGrafanaAdapter_ParsePayload_UnifiedAlerting_MissingAlertname(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alerts": [
			{
				"status": "firing",
				"labels": {},
				"annotations": {},
				"fingerprint": "nolabel"
			}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	// Should default to "Grafana Alert"
	if alerts[0].AlertName != "Grafana Alert" {
		t.Errorf("Expected default AlertName 'Grafana Alert', got '%s'", alerts[0].AlertName)
	}
}

func TestGrafanaAdapter_ParsePayload_LegacyAlerting_TitleFallback(t *testing.T) {
	adapter := NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}

	// When ruleName is empty, should use title
	payload := []byte(`{
		"ruleName": "",
		"title": "Alert Title",
		"state": "alerting",
		"ruleId": 1
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].AlertName != "Alert Title" {
		t.Errorf("Expected AlertName 'Alert Title' (from title), got '%s'", alerts[0].AlertName)
	}
}
