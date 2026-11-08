package adapters

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewPagerDutyAdapter(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	if adapter == nil {
		t.Fatal("Expected adapter to not be nil")
	}
	if adapter.GetSourceType() != "pagerduty" {
		t.Errorf("Expected source type 'pagerduty', got '%s'", adapter.GetSourceType())
	}
}

func TestPagerDutyAdapter_ParsePayload_TriggeredIncident(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event": {
			"id": "event-abc123",
			"event_type": "incident.triggered",
			"data": {
				"id": "incident-xyz789",
				"type": "incident",
				"title": "Database Connection Pool Exhausted",
				"description": "The connection pool has been exhausted.",
				"status": "triggered",
				"urgency": "high",
				"priority": {
					"id": "priority-001",
					"summary": "P1 - Critical"
				},
				"service": {
					"id": "service-db-001",
					"name": "Database Service",
					"summary": "Main Database"
				},
				"source": "db-primary.example.com",
				"body": {
					"type": "incident.body",
					"details": {
						"runbook": "https://runbooks.example.com/db-pool"
					}
				}
			}
		}
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
	if alert.AlertName != "Database Connection Pool Exhausted" {
		t.Errorf("Expected AlertName 'Database Connection Pool Exhausted', got '%s'", alert.AlertName)
	}

	// Verify severity (P1 = Critical)
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Expected Severity 'critical', got '%s'", alert.Severity)
	}

	// Verify status
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Expected Status 'firing', got '%s'", alert.Status)
	}

	// Verify target host
	if alert.TargetHost != "db-primary.example.com" {
		t.Errorf("Expected TargetHost 'db-primary.example.com', got '%s'", alert.TargetHost)
	}

	// Verify target service
	if alert.TargetService != "Database Service" {
		t.Errorf("Expected TargetService 'Database Service', got '%s'", alert.TargetService)
	}

	// Verify runbook URL
	if alert.RunbookURL != "https://runbooks.example.com/db-pool" {
		t.Errorf("Expected RunbookURL, got '%s'", alert.RunbookURL)
	}

	// Verify source ID
	if alert.SourceAlertID != "incident-xyz789" {
		t.Errorf("Expected SourceAlertID 'incident-xyz789', got '%s'", alert.SourceAlertID)
	}
}

func TestPagerDutyAdapter_ParsePayload_ResolvedIncident(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event": {
			"id": "event-resolved",
			"event_type": "incident.resolved",
			"data": {
				"id": "incident-resolved-123",
				"title": "Test Incident",
				"description": "Test",
				"status": "resolved",
				"urgency": "low",
				"service": {"name": "Test Service"}
			}
		}
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	if alerts[0].Status != database.AlertStatusResolved {
		t.Errorf("Expected Status 'resolved', got '%s'", alerts[0].Status)
	}
}

func TestPagerDutyAdapter_ParsePayload_NonLifecycleEventsIgnored(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	// Acknowledged means a person took the page, not that the problem is
	// gone; the others do not change whether the incident is open.
	for _, eventType := range []string{
		"incident.acknowledged",
		"incident.unacknowledged",
		"incident.annotated",
		"incident.reassigned",
		"incident.escalated",
		"incident.priority_updated",
		"pagey.ping",
	} {
		t.Run(eventType, func(t *testing.T) {
			payload := []byte(`{"event":{"id":"ev-1","event_type":"` + eventType + `","data":{"id":"incident-1","title":"Disk full","urgency":"high"}}}`)
			alerts, err := adapter.ParsePayload(payload, instance)
			if err != nil {
				t.Fatalf("ParsePayload returned error: %v", err)
			}
			if len(alerts) != 0 {
				t.Errorf("expected no alerts for %s, got %d with status %q", eventType, len(alerts), alerts[0].Status)
			}
		})
	}
}

func TestPagerDutyAdapter_ParsePayload_ReopenedIncidentFires(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	payload := []byte(`{"event":{"id":"ev-2","event_type":"incident.reopened","data":{"id":"incident-2","title":"Disk full","urgency":"high"}}}`)

	alerts, err := adapter.ParsePayload(payload, &database.AlertSourceInstance{})
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Status != database.AlertStatusFiring {
		t.Fatalf("expected one firing alert, got %+v", alerts)
	}
}

func TestPagerDutyAdapter_ParsePayload_UrgencyMapping(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	testCases := []struct {
		urgency          string
		priority         string
		expectedSeverity database.AlertSeverity
	}{
		{"high", "", database.AlertSeverityHigh},
		{"low", "", database.AlertSeverityInfo},
		{"", "P1", database.AlertSeverityCritical},
		{"", "P2", database.AlertSeverityHigh},
		{"low", "P1 - Critical", database.AlertSeverityCritical}, // Priority takes precedence
		{"high", "critical", database.AlertSeverityCritical},
		{"", "", database.AlertSeverityWarning}, // Default
	}

	for _, tc := range testCases {
		priorityBlock := ""
		if tc.priority != "" {
			priorityBlock = `"priority": {"id": "p1", "summary": "` + tc.priority + `"},`
		}

		payload := []byte(`{
			"event": {
				"id": "test",
				"event_type": "incident.triggered",
				"data": {
					"id": "test-incident",
					"title": "Test",
					"urgency": "` + tc.urgency + `",
					` + priorityBlock + `
					"service": {"name": "Test"}
				}
			}
		}`)

		alerts, err := adapter.ParsePayload(payload, instance)
		if err != nil {
			t.Fatalf("ParsePayload returned error for urgency '%s', priority '%s': %v", tc.urgency, tc.priority, err)
		}

		if alerts[0].Severity != tc.expectedSeverity {
			t.Errorf("Urgency '%s', Priority '%s': expected severity %s, got %s",
				tc.urgency, tc.priority, tc.expectedSeverity, alerts[0].Severity)
		}
	}
}

func TestPagerDutyAdapter_ParsePayload_InvalidJSON(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{not valid json}`)

	_, err := adapter.ParsePayload(payload, instance)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestPagerDutyAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error when no secret configured, got: %v", err)
	}
}

func TestPagerDutyAdapter_ValidateWebhookSecret_AuthorizationHeader(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "pd-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("Authorization", "pd-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid authorization, got: %v", err)
	}
}

func TestPagerDutyAdapter_ValidateWebhookSecret_BearerToken(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "pd-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", nil)
	req.Header.Set("Authorization", "Bearer pd-secret")

	err := adapter.ValidateWebhookSecret(req, instance)
	if err != nil {
		t.Errorf("Expected no error for valid bearer token, got: %v", err)
	}
}

func pagerDutySign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestPagerDutyAdapter_ValidateWebhookSecret_Signature(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{WebhookSecret: "pd-secret"}
	body := `{"event":{"event_type":"incident.triggered"}}`

	tests := []struct {
		name    string
		header  string
		wantErr bool
	}{
		{"valid signature", pagerDutySign("pd-secret", body), false},
		{"valid signature during secret rotation", pagerDutySign("old-secret", body) + "," + pagerDutySign("pd-secret", body), false},
		{"any v1= value is not enough", "v1=abc123", true},
		{"signature with the wrong secret", pagerDutySign("other-secret", body), true},
		{"signature of a different body", pagerDutySign("pd-secret", body+" "), true},
		{"invalid format", "invalid-format", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook/alert", strings.NewReader(body))
			req.Header.Set("X-PagerDuty-Signature", tt.header)

			err := adapter.ValidateWebhookSecret(req, instance)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateWebhookSecret() error = %v, wantErr %v", err, tt.wantErr)
			}

			// The handler reads the body after validation: it must be intact.
			rest, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				t.Fatalf("read body after validation: %v", readErr)
			}
			if string(rest) != body {
				t.Errorf("body after validation = %q, want %q", rest, body)
			}
		})
	}
}

func TestPagerDutyAdapter_ValidateWebhookSecret_MissingHeaders(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{WebhookSecret: "pd-secret"}

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", strings.NewReader("{}"))
	if err := adapter.ValidateWebhookSecret(req, instance); err == nil {
		t.Error("expected error without signature or authorization, got nil")
	}

	req = httptest.NewRequest(http.MethodPost, "/webhook/alert", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer wrong")
	if err := adapter.ValidateWebhookSecret(req, instance); err == nil {
		t.Error("expected error for a wrong bearer token, got nil")
	}
}

func TestPagerDutyAdapter_GetDefaultMappings(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	mappings := adapter.GetDefaultMappings()

	expectedKeys := []string{
		"alert_name",
		"severity",
		"status",
		"summary",
		"target_host",
		"target_service",
		"runbook_url",
		"source_alert_id",
	}

	for _, key := range expectedKeys {
		if _, ok := mappings[key]; !ok {
			t.Errorf("Missing expected mapping key: %s", key)
		}
	}
}

func TestPagerDutyAdapter_ParsePayload_TargetLabels(t *testing.T) {
	adapter := NewPagerDutyAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"event": {
			"id": "test",
			"event_type": "incident.triggered",
			"data": {
				"id": "test-incident",
				"title": "Test",
				"urgency": "high",
				"priority": {"id": "p1", "summary": "P1"},
				"service": {"id": "svc-123", "name": "TestService"}
			}
		}
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	labels := alerts[0].TargetLabels
	if labels["service_id"] != "svc-123" {
		t.Errorf("Expected service_id 'svc-123', got '%s'", labels["service_id"])
	}
	if labels["service_name"] != "TestService" {
		t.Errorf("Expected service_name 'TestService', got '%s'", labels["service_name"])
	}
	if labels["urgency"] != "high" {
		t.Errorf("Expected urgency 'high', got '%s'", labels["urgency"])
	}
	if labels["priority_id"] != "p1" {
		t.Errorf("Expected priority_id 'p1', got '%s'", labels["priority_id"])
	}
}
