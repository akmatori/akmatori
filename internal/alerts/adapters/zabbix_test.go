package adapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewZabbixAdapter(t *testing.T) {
	adapter := NewZabbixAdapter()
	if adapter == nil {
		t.Fatal("Expected adapter to not be nil")
	}
	if adapter.GetSourceType() != "zabbix" {
		t.Errorf("Expected source type 'zabbix', got '%s'", adapter.GetSourceType())
	}
}

func TestZabbixAdapter_ParsePayload_ProblemAlert(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event_time": "2024-01-15 10:30:00",
		"alert_name": "CPU Load High",
		"severity": "High",
		"priority": "4",
		"metric_name": "system.cpu.load[percpu,avg1]",
		"metric_value": "8.5",
		"trigger_expression": "{host:system.cpu.load[percpu,avg1].avg(5m)}>5",
		"pending_duration": "5m",
		"event_id": "12345",
		"hardware": "db-server-01",
		"event_status": "PROBLEM",
		"runbook_url": "https://runbooks.example.com/cpu-load"
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
	if alert.AlertName != "CPU Load High" {
		t.Errorf("Expected AlertName 'CPU Load High', got '%s'", alert.AlertName)
	}

	// Verify severity (priority 4 = High)
	if alert.Severity != database.AlertSeverityHigh {
		t.Errorf("Expected Severity 'high', got '%s'", alert.Severity)
	}

	// Verify status
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify target host
	if alert.TargetHost != "db-server-01" {
		t.Errorf("Expected TargetHost 'db-server-01', got '%s'", alert.TargetHost)
	}

	// Verify metric name and value
	if alert.MetricName != "system.cpu.load[percpu,avg1]" {
		t.Errorf("Expected MetricName 'system.cpu.load[percpu,avg1]', got '%s'", alert.MetricName)
	}
	if alert.MetricValue != "8.5" {
		t.Errorf("Expected MetricValue '8.5', got '%s'", alert.MetricValue)
	}

	// Verify runbook URL
	if alert.RunbookURL != "https://runbooks.example.com/cpu-load" {
		t.Errorf("Expected RunbookURL, got '%s'", alert.RunbookURL)
	}

	// Verify source alert ID
	if alert.SourceAlertID != "12345" {
		t.Errorf("Expected SourceAlertID '12345', got '%s'", alert.SourceAlertID)
	}

	// Verify started time is set
	if alert.StartedAt == nil {
		t.Error("Expected StartedAt to be set")
	}
}

func TestZabbixAdapter_ParsePayload_ResolvedAlert(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event_time": "2024-01-15 10:30:00",
		"alert_name": "Disk Space Low",
		"priority": "3",
		"event_id": "54321",
		"hardware": "storage-01",
		"event_status": "RESOLVED"
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
}

func TestZabbixAdapter_ParsePayload_OKStatus(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alert_name": "Test Alert",
		"event_id": "99999",
		"hardware": "test-host",
		"event_status": "OK"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	if alerts[0].Status != database.AlertStatusResolved {
		t.Errorf("Expected Status 'resolved' for OK, got '%s'", alerts[0].Status)
	}
}

func TestZabbixAdapter_ParsePayload_PriorityToSeverity(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		priority         string
		expectedSeverity database.AlertSeverity
	}{
		{"5", database.AlertSeverityCritical}, // Disaster
		{"4", database.AlertSeverityHigh},     // High
		{"3", database.AlertSeverityWarning},  // Average
		{"2", database.AlertSeverityInfo},     // Warning
		{"1", database.AlertSeverityInfo},     // Information
		{"0", database.AlertSeverityWarning},  // Not classified (default)
		{"", database.AlertSeverityWarning},   // Empty (default)
	}

	for _, tc := range testCases {
		payload := []byte(`{
			"alert_name": "Test",
			"event_id": "test",
			"hardware": "test",
			"event_status": "PROBLEM",
			"priority": "` + tc.priority + `"
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for priority '%s': %v", tc.priority, err)
		}

		if len(alerts) != 1 {
			t.Fatalf("Expected 1 alert for priority '%s', got %d", tc.priority, len(alerts))
		}

		if alerts[0].Severity != tc.expectedSeverity {
			t.Errorf("Priority '%s': expected severity %s, got %s", tc.priority, tc.expectedSeverity, alerts[0].Severity)
		}
	}
}

func TestZabbixAdapter_ParsePayload_InvalidJSON(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{invalid json}`)

	_, err := adapter.ParsePayload(payload, instance)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestZabbixAdapter_ParsePayload_RFC3339Time(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event_time": "2024-01-15T10:30:00Z",
		"alert_name": "Test",
		"event_id": "123",
		"hardware": "test",
		"event_status": "PROBLEM"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].StartedAt == nil {
		t.Error("Expected StartedAt to be set for RFC3339 time format")
	}
}

func TestZabbixAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error when no secret configured, got: %v", err)
	}
}

func TestZabbixAdapter_ValidateWebhookSecret_ValidSecret(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "zabbix-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Zabbix-Secret", "zabbix-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid secret, got: %v", err)
	}
}

func TestZabbixAdapter_ValidateWebhookSecret_InvalidSecret(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "correct-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Zabbix-Secret", "wrong-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err == nil {
		t.Error("Expected error for invalid secret, got nil")
	}
}

func TestZabbixAdapter_GetDefaultMappings(t *testing.T) {
	adapter := NewZabbixAdapter()
	mappings := adapter.GetDefaultMappings()

	expectedKeys := []string{
		"alert_name",
		"severity",
		"status",
		"summary",
		"target_host",
		"metric_name",
		"metric_value",
		"runbook_url",
		"source_alert_id",
		"started_at",
	}

	for _, key := range expectedKeys {
		if _, ok := mappings[key]; !ok {
			t.Errorf("Missing expected mapping key: %s", key)
		}
	}
}

func TestZabbixAdapter_ParsePayload_TargetLabels(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alert_name": "Test",
		"event_id": "123",
		"hardware": "test-host",
		"trigger_expression": "expr>5",
		"pending_duration": "10m",
		"event_status": "PROBLEM"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	labels := alerts[0].TargetLabels
	if labels["hardware"] != "test-host" {
		t.Errorf("Expected hardware label 'test-host', got '%s'", labels["hardware"])
	}
	if labels["trigger_expression"] != "expr>5" {
		t.Errorf("Expected trigger_expression label, got '%s'", labels["trigger_expression"])
	}
	if labels["pending_duration"] != "10m" {
		t.Errorf("Expected pending_duration label '10m', got '%s'", labels["pending_duration"])
	}
}

func TestZabbixAdapter_ParsePayload_ExtraFieldsPreserved(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	// Payload includes fields not in ZabbixPayload struct (event_tags, custom_field)
	payload := []byte(`{
		"alert_name": "Test",
		"event_id": "123",
		"hardware": "test-host",
		"event_status": "PROBLEM",
		"event_tags": "[{\"tag\":\"scope\",\"value\":\"availability\"}]",
		"custom_field": "custom_value"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	raw := alerts[0].RawPayload
	if raw["event_tags"] == nil {
		t.Error("Expected event_tags to be preserved in RawPayload")
	}
	if raw["custom_field"] == nil {
		t.Error("Expected custom_field to be preserved in RawPayload")
	}
	if raw["custom_field"] != "custom_value" {
		t.Errorf("Expected custom_field 'custom_value', got '%v'", raw["custom_field"])
	}
}

func TestZabbixAdapter_ParsePayload_Description(t *testing.T) {
	adapter := NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alert_name": "Test",
		"event_id": "123",
		"hardware": "test",
		"metric_name": "cpu.load",
		"metric_value": "95",
		"trigger_expression": "cpu.load>80",
		"event_status": "PROBLEM"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	// Description should contain metric info
	desc := alerts[0].Description
	if desc == "" {
		t.Error("Expected Description to be set")
	}
	// Description format: "Metric: cpu.load = 95\nTrigger: cpu.load>80"
	if len(desc) < 10 {
		t.Errorf("Expected detailed description, got '%s'", desc)
	}
}
