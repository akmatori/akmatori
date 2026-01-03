package adapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewDatadogAdapter(t *testing.T) {
	adapter := NewDatadogAdapter()
	if adapter == nil {
		t.Fatal("Expected adapter to not be nil")
	}
	if adapter.GetSourceType() != "datadog" {
		t.Errorf("Expected source type 'datadog', got '%s'", adapter.GetSourceType())
	}
}

func TestDatadogAdapter_ParsePayload_TriggeredAlert(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"id": "event-dd-123",
		"title": "API Latency Alert",
		"body": "API response time has exceeded 500ms threshold",
		"alert_type": "error",
		"event_type": "monitor.alert",
		"priority": "normal",
		"alert_id": "alert-dd-456",
		"alert_title": "High API Latency Detected",
		"alert_status": "Triggered",
		"hostname": "api-gateway-01",
		"org_id": "org-123",
		"org_name": "AcmeCorp",
		"date": 1705315800,
		"tags": [
			"service:api-gateway",
			"env:production",
			"host:api-gateway-01"
		],
		"event_links": [
			{
				"url": "https://runbooks.example.com/api-latency",
				"name": "Runbook"
			}
		],
		"alert_cycle_key": "cycle-abc123",
		"alert_metric": "trace.api.request.duration",
		"alert_query": "avg:trace.api.request.duration > 500"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]

	// Verify alert name (alert_title takes precedence)
	if alert.AlertName != "High API Latency Detected" {
		t.Errorf("Expected AlertName 'High API Latency Detected', got '%s'", alert.AlertName)
	}

	// Verify severity (error = critical)
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Expected Severity 'critical', got '%s'", alert.Severity)
	}

	// Verify status
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify target host
	if alert.TargetHost != "api-gateway-01" {
		t.Errorf("Expected TargetHost 'api-gateway-01', got '%s'", alert.TargetHost)
	}

	// Verify target service (from tags)
	if alert.TargetService != "api-gateway" {
		t.Errorf("Expected TargetService 'api-gateway', got '%s'", alert.TargetService)
	}

	// Verify summary
	if alert.Summary != "API response time has exceeded 500ms threshold" {
		t.Errorf("Expected Summary, got '%s'", alert.Summary)
	}

	// Verify runbook URL (from event_links with "Runbook" name)
	if alert.RunbookURL != "https://runbooks.example.com/api-latency" {
		t.Errorf("Expected RunbookURL, got '%s'", alert.RunbookURL)
	}

	// Verify metric name
	if alert.MetricName != "trace.api.request.duration" {
		t.Errorf("Expected MetricName, got '%s'", alert.MetricName)
	}

	// Verify source ID (alert_id takes precedence)
	if alert.SourceAlertID != "alert-dd-456" {
		t.Errorf("Expected SourceAlertID 'alert-dd-456', got '%s'", alert.SourceAlertID)
	}

	// Verify fingerprint
	if alert.SourceFingerprint != "cycle-abc123" {
		t.Errorf("Expected SourceFingerprint 'cycle-abc123', got '%s'", alert.SourceFingerprint)
	}
}

func TestDatadogAdapter_ParsePayload_RecoveredAlert(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"id": "event-recovered",
		"title": "Test Alert",
		"body": "Alert recovered",
		"alert_type": "success",
		"alert_status": "Recovered",
		"hostname": "test-host"
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].Status != database.AlertStatusResolved {
		t.Errorf("Expected Status 'resolved', got '%s'", alerts[0].Status)
	}
}

func TestDatadogAdapter_ParsePayload_AlertTypeMapping(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		alertType        string
		priority         string
		expectedSeverity database.AlertSeverity
	}{
		{"error", "normal", database.AlertSeverityCritical},
		{"warning", "normal", database.AlertSeverityWarning},
		{"info", "normal", database.AlertSeverityInfo},
		{"success", "normal", database.AlertSeverityInfo},
		{"", "normal", database.AlertSeverityWarning}, // Default with normal priority
		{"", "low", database.AlertSeverityInfo},       // Low priority
	}

	for _, tc := range testCases {
		payload := []byte(`{
			"id": "test",
			"title": "Test",
			"alert_type": "` + tc.alertType + `",
			"priority": "` + tc.priority + `",
			"hostname": "test"
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for alert_type '%s': %v", tc.alertType, err)
		}

		if alerts[0].Severity != tc.expectedSeverity {
			t.Errorf("AlertType '%s', Priority '%s': expected severity %s, got %s",
				tc.alertType, tc.priority, tc.expectedSeverity, alerts[0].Severity)
		}
	}
}

func TestDatadogAdapter_ParsePayload_AlertStatusMapping(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		alertStatus    string
		expectedStatus database.AlertStatus
	}{
		{"Triggered", database.AlertStatusFiring},
		{"Alert", database.AlertStatusFiring},
		{"Recovered", database.AlertStatusResolved},
		{"Resolved", database.AlertStatusResolved},
		{"OK", database.AlertStatusResolved},
		{"Unknown", database.AlertStatusFiring}, // Default
		{"", database.AlertStatusFiring},        // Empty = default
	}

	for _, tc := range testCases {
		payload := []byte(`{
			"id": "test",
			"title": "Test",
			"alert_status": "` + tc.alertStatus + `",
			"hostname": "test"
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for alert_status '%s': %v", tc.alertStatus, err)
		}

		if alerts[0].Status != tc.expectedStatus {
			t.Errorf("AlertStatus '%s': expected status %s, got %s",
				tc.alertStatus, tc.expectedStatus, alerts[0].Status)
		}
	}
}

func TestDatadogAdapter_ParsePayload_InvalidJSON(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{invalid json}`)

	_, err := adapter.ParsePayload(payload, instance)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestDatadogAdapter_ParsePayload_TagsParsing(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"id": "test",
		"title": "Test",
		"hostname": "",
		"tags": [
			"service:my-service",
			"env:production",
			"host:my-host",
			"standalone-tag"
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	labels := alerts[0].TargetLabels

	// Verify key:value tags
	if labels["service"] != "my-service" {
		t.Errorf("Expected service 'my-service', got '%s'", labels["service"])
	}
	if labels["env"] != "production" {
		t.Errorf("Expected env 'production', got '%s'", labels["env"])
	}

	// Verify standalone tag (should be "true")
	if labels["standalone-tag"] != "true" {
		t.Errorf("Expected standalone-tag 'true', got '%s'", labels["standalone-tag"])
	}

	// Verify host fallback (hostname empty, should use tag)
	if alerts[0].TargetHost != "my-host" {
		t.Errorf("Expected TargetHost 'my-host' from tags, got '%s'", alerts[0].TargetHost)
	}

	// Verify service from tags
	if alerts[0].TargetService != "my-service" {
		t.Errorf("Expected TargetService 'my-service' from tags, got '%s'", alerts[0].TargetService)
	}
}

func TestDatadogAdapter_ParsePayload_EventLinksRunbook(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	// Test explicit runbook link
	payload := []byte(`{
		"id": "test",
		"title": "Test",
		"event_links": [
			{"url": "https://monitor.example.com", "name": "Monitor"},
			{"url": "https://runbook.example.com/fix", "name": "Runbook"},
			{"url": "https://docs.example.com", "name": "Docs"}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].RunbookURL != "https://runbook.example.com/fix" {
		t.Errorf("Expected RunbookURL from 'Runbook' link, got '%s'", alerts[0].RunbookURL)
	}
}

func TestDatadogAdapter_ParsePayload_EventLinksFallback(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	// Test fallback to first link when no runbook
	payload := []byte(`{
		"id": "test",
		"title": "Test",
		"event_links": [
			{"url": "https://first-link.example.com", "name": "First"},
			{"url": "https://second-link.example.com", "name": "Second"}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	// Should use first link as fallback
	if alerts[0].RunbookURL != "https://first-link.example.com" {
		t.Errorf("Expected RunbookURL from first link, got '%s'", alerts[0].RunbookURL)
	}
}

func TestDatadogAdapter_ParsePayload_TitleFallback(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	// When alert_title is empty, should use title
	payload := []byte(`{
		"id": "test",
		"title": "Fallback Title",
		"alert_title": ""
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].AlertName != "Fallback Title" {
		t.Errorf("Expected AlertName 'Fallback Title', got '%s'", alerts[0].AlertName)
	}
}

func TestDatadogAdapter_ParsePayload_IDFallback(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{}

	// When alert_id is empty, should use id
	payload := []byte(`{
		"id": "event-id-123",
		"title": "Test",
		"alert_id": ""
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if alerts[0].SourceAlertID != "event-id-123" {
		t.Errorf("Expected SourceAlertID 'event-id-123', got '%s'", alerts[0].SourceAlertID)
	}
}

func TestDatadogAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error when no secret configured, got: %v", err)
	}
}

func TestDatadogAdapter_ValidateWebhookSecret_DDAPIKey(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "dd-api-key",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("DD-API-KEY", "dd-api-key")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid DD-API-KEY, got: %v", err)
	}
}

func TestDatadogAdapter_ValidateWebhookSecret_Signature(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "dd-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("X-Datadog-Signature", "dd-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid signature, got: %v", err)
	}
}

func TestDatadogAdapter_ValidateWebhookSecret_BearerToken(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "dd-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("Authorization", "Bearer dd-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid bearer token, got: %v", err)
	}
}

func TestDatadogAdapter_ValidateWebhookSecret_InvalidSecret(t *testing.T) {
	adapter := NewDatadogAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "correct-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("DD-API-KEY", "wrong-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err == nil {
		t.Error("Expected error for invalid secret, got nil")
	}
}

func TestDatadogAdapter_GetDefaultMappings(t *testing.T) {
	adapter := NewDatadogAdapter()
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
