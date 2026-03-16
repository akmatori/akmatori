package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/alerts/adapters"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// ========================================
// Webhook Flow Integration Tests
// ========================================

// TestWebhookFlow_AlertmanagerPayloadParsing tests end-to-end alertmanager payload parsing
func TestWebhookFlow_AlertmanagerPayloadParsing(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()

	tests := []struct {
		name           string
		payload        string
		expectedAlerts int
		validateFirst  func(*testing.T, alerts.NormalizedAlert)
	}{
		{
			name: "single critical firing alert",
			payload: `{
				"version": "4",
				"status": "firing",
				"alerts": [{
					"status": "firing",
					"labels": {
						"alertname": "HighCPUUsage",
						"severity": "critical",
						"instance": "prod-web-01:9100",
						"job": "node"
					},
					"annotations": {
						"summary": "CPU usage exceeds 95%",
						"description": "Production web server CPU is critically high",
						"runbook_url": "https://wiki.example.com/runbooks/high-cpu"
					},
					"startsAt": "2024-01-15T10:30:00Z",
					"fingerprint": "abc123"
				}]
			}`,
			expectedAlerts: 1,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, "HighCPUUsage", a.AlertName, "AlertName")
				testhelpers.AssertEqual(t, database.AlertSeverityCritical, a.Severity, "Severity")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "Status")
				testhelpers.AssertEqual(t, "prod-web-01:9100", a.TargetHost, "TargetHost")
				testhelpers.AssertEqual(t, "node", a.TargetService, "TargetService")
				testhelpers.AssertContains(t, a.Summary, "CPU usage exceeds 95%", "Summary")
			},
		},
		{
			name: "mixed firing and resolved alerts",
			payload: `{
				"version": "4",
				"alerts": [
					{"status": "firing", "labels": {"alertname": "DiskFull", "severity": "warning"}, "annotations": {}, "fingerprint": "fp1"},
					{"status": "resolved", "labels": {"alertname": "MemoryHigh", "severity": "info"}, "annotations": {}, "fingerprint": "fp2", "endsAt": "2024-01-15T11:00:00Z"},
					{"status": "firing", "labels": {"alertname": "NetworkLatency", "severity": "high"}, "annotations": {}, "fingerprint": "fp3"}
				]
			}`,
			expectedAlerts: 3,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, "DiskFull", a.AlertName, "First alert name")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "First alert status")
			},
		},
		{
			name: "alert with all annotations",
			payload: `{
				"alerts": [{
					"status": "firing",
					"labels": {
						"alertname": "ServiceDown",
						"severity": "critical",
						"instance": "api-server:8080",
						"job": "api",
						"environment": "production",
						"team": "platform"
					},
					"annotations": {
						"summary": "API service is not responding",
						"description": "Health check failures for 5 minutes",
						"runbook_url": "https://docs.example.com/runbook/api-down",
						"dashboard_url": "https://grafana.example.com/d/api"
					},
					"startsAt": "2024-01-15T09:00:00Z",
					"fingerprint": "service-down-fp"
				}]
			}`,
			expectedAlerts: 1,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, "ServiceDown", a.AlertName, "AlertName")
				testhelpers.AssertEqual(t, "https://docs.example.com/runbook/api-down", a.RunbookURL, "RunbookURL")
				testhelpers.AssertContains(t, a.Description, "Health check failures", "Description")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{FieldMappings: nil}
			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)

			testhelpers.AssertNoError(t, err, "ParsePayload")
			testhelpers.AssertEqual(t, tt.expectedAlerts, len(alerts), "alert count")

			if tt.validateFirst != nil && len(alerts) > 0 {
				tt.validateFirst(t, alerts[0])
			}
		})
	}
}

// TestWebhookFlow_GrafanaPayloadParsing tests Grafana adapter payload parsing
func TestWebhookFlow_GrafanaPayloadParsing(t *testing.T) {
	adapter := adapters.NewGrafanaAdapter()

	tests := []struct {
		name           string
		payload        string
		expectedAlerts int
		validateFirst  func(*testing.T, alerts.NormalizedAlert)
	}{
		{
			name: "unified alerting v1 payload",
			payload: `{
				"version": "1",
				"status": "firing",
				"alerts": [{
					"status": "firing",
					"labels": {
						"alertname": "High disk usage",
						"grafana_folder": "Infrastructure"
					},
					"annotations": {
						"summary": "Disk usage above threshold"
					},
					"startsAt": "2024-01-15T10:00:00Z",
					"fingerprint": "grafana-fp-1"
				}]
			}`,
			expectedAlerts: 1,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, "High disk usage", a.AlertName, "AlertName")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "Status")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{}
			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)

			testhelpers.AssertNoError(t, err, "ParsePayload")
			testhelpers.AssertEqual(t, tt.expectedAlerts, len(alerts), "alert count")

			if tt.validateFirst != nil && len(alerts) > 0 {
				tt.validateFirst(t, alerts[0])
			}
		})
	}
}

// TestWebhookFlow_DatadogPayloadParsing tests Datadog adapter payload parsing
func TestWebhookFlow_DatadogPayloadParsing(t *testing.T) {
	adapter := adapters.NewDatadogAdapter()

	tests := []struct {
		name           string
		payload        string
		expectedAlerts int
	}{
		{
			name: "datadog monitor alert",
			payload: `{
				"id": "12345",
				"title": "High Memory Alert",
				"body": "Memory usage exceeded 90% on host web-01",
				"alert_type": "error",
				"priority": "P1",
				"tags": ["env:production", "team:platform"]
			}`,
			expectedAlerts: 1,
		},
		{
			name: "datadog with host tags",
			payload: `{
				"id": "67890",
				"title": "Disk Space Warning",
				"body": "Disk /data is 85% full",
				"alert_type": "warning",
				"tags": ["host:storage-01", "service:database"]
			}`,
			expectedAlerts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{}
			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)

			testhelpers.AssertNoError(t, err, "ParsePayload")
			testhelpers.AssertEqual(t, tt.expectedAlerts, len(alerts), "alert count")
		})
	}
}

// TestWebhookFlow_ZabbixPayloadParsing tests Zabbix adapter payload parsing
func TestWebhookFlow_ZabbixPayloadParsing(t *testing.T) {
	adapter := adapters.NewZabbixAdapter()

	tests := []struct {
		name           string
		payload        string
		expectedAlerts int
		validateFirst  func(*testing.T, alerts.NormalizedAlert)
	}{
		{
			name: "zabbix problem event",
			payload: `{
				"event_id": "123456",
				"event_status": "PROBLEM",
				"alert_name": "High load average",
				"priority": "4",
				"hardware": "server-01.example.com",
				"trigger_expression": "{server-01:system.cpu.load[percpu,avg1].avg(5m)}>5",
				"event_time": "2024-01-15 10:30:00"
			}`,
			expectedAlerts: 1,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, "High load average", a.AlertName, "AlertName")
				testhelpers.AssertEqual(t, "server-01.example.com", a.TargetHost, "TargetHost")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "Status")
			},
		},
		{
			name: "zabbix resolved event",
			payload: `{
				"event_id": "123457",
				"event_status": "OK",
				"alert_name": "High load average",
				"priority": "4",
				"hardware": "server-01.example.com"
			}`,
			expectedAlerts: 1,
			validateFirst: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertStatusResolved, a.Status, "Status")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{}
			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)

			testhelpers.AssertNoError(t, err, "ParsePayload")
			testhelpers.AssertEqual(t, tt.expectedAlerts, len(alerts), "alert count")

			if tt.validateFirst != nil && len(alerts) > 0 {
				tt.validateFirst(t, alerts[0])
			}
		})
	}
}

// TestWebhookFlow_PagerDutyPayloadParsing tests PagerDuty adapter payload parsing
func TestWebhookFlow_PagerDutyPayloadParsing(t *testing.T) {
	adapter := adapters.NewPagerDutyAdapter()

	tests := []struct {
		name           string
		payload        string
		expectedAlerts int
	}{
		{
			name: "pagerduty v2 webhook",
			payload: `{
				"event": {
					"event_type": "incident.triggered",
					"data": {
						"id": "PD123",
						"title": "Critical: Database connection failures",
						"description": "Multiple database connection timeouts detected",
						"service": {"name": "API Service"},
						"priority": {"summary": "P1"}
					}
				}
			}`,
			expectedAlerts: 1,
		},
		{
			name: "pagerduty incident acknowledged",
			payload: `{
				"event": {
					"event_type": "incident.acknowledged",
					"data": {
						"id": "PD456",
						"title": "Warning: High latency",
						"service": {"name": "Web Frontend"}
					}
				}
			}`,
			expectedAlerts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{}
			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)

			testhelpers.AssertNoError(t, err, "ParsePayload")
			testhelpers.AssertEqual(t, tt.expectedAlerts, len(alerts), "alert count")
		})
	}
}

// ========================================
// Webhook Secret Validation Integration Tests
// ========================================

// TestWebhookFlow_SecretValidation tests secret validation across adapters
func TestWebhookFlow_SecretValidation(t *testing.T) {
	adapterConfigs := []struct {
		name           string
		adapter        alerts.AlertAdapter
		headerName     string
		secretValue    string
		supportBearer  bool
	}{
		{"alertmanager", adapters.NewAlertmanagerAdapter(), "X-Alertmanager-Secret", "am-secret", true},
		{"grafana", adapters.NewGrafanaAdapter(), "X-Grafana-Secret", "grafana-secret", true},
		{"datadog", adapters.NewDatadogAdapter(), "X-Datadog-Signature", "dd-secret", true},
		{"zabbix", adapters.NewZabbixAdapter(), "X-Zabbix-Secret", "zabbix-secret", false}, // Zabbix doesn't support bearer
	}

	for _, tc := range adapterConfigs {
		t.Run(tc.name+"_valid_secret", func(t *testing.T) {
			instance := &database.AlertSourceInstance{WebhookSecret: tc.secretValue}
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header.Set(tc.headerName, tc.secretValue)

			err := tc.adapter.ValidateWebhookSecret(req, instance)
			testhelpers.AssertNoError(t, err, "ValidateWebhookSecret with valid secret")
		})

		t.Run(tc.name+"_invalid_secret", func(t *testing.T) {
			instance := &database.AlertSourceInstance{WebhookSecret: tc.secretValue}
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header.Set(tc.headerName, "wrong-secret")

			err := tc.adapter.ValidateWebhookSecret(req, instance)
			testhelpers.AssertError(t, err, "ValidateWebhookSecret with invalid secret")
		})

		t.Run(tc.name+"_no_secret_configured", func(t *testing.T) {
			instance := &database.AlertSourceInstance{WebhookSecret: ""}
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)

			err := tc.adapter.ValidateWebhookSecret(req, instance)
			testhelpers.AssertNoError(t, err, "ValidateWebhookSecret with no secret configured")
		})

		if tc.supportBearer {
			t.Run(tc.name+"_bearer_token", func(t *testing.T) {
				instance := &database.AlertSourceInstance{WebhookSecret: tc.secretValue}
				req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
				req.Header.Set("Authorization", "Bearer "+tc.secretValue)

				err := tc.adapter.ValidateWebhookSecret(req, instance)
				testhelpers.AssertNoError(t, err, "ValidateWebhookSecret with bearer token")
			})
		}
	}
}

// ========================================
// Alert Handler HTTP Flow Tests
// ========================================

// TestAlertHandler_WebhookEndpoint_HTTPFlow tests HTTP handling flow
func TestAlertHandler_WebhookEndpoint_HTTPFlow(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Register mock adapter
	mockAdapter := testhelpers.NewMockAlertAdapter("test-adapter")
	now := time.Now()
	mockAdapter.WithAlerts(alerts.NormalizedAlert{
		AlertName:  "TestAlert",
		Severity:   database.AlertSeverityCritical,
		Status:     database.AlertStatusFiring,
		TargetHost: "test-host",
		Summary:    "Test summary",
		StartedAt:  &now,
	})
	h.RegisterAdapter(mockAdapter)

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		contentType    string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET not allowed",
			method:         http.MethodGet,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT not allowed",
			method:         http.MethodPut,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE not allowed",
			method:         http.MethodDelete,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "empty UUID returns bad request",
			method:         http.MethodPost,
			path:           "/webhook/alert/",
			body:           "{}",
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing instance UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *bytes.Reader
			if tt.body != "" {
				body = bytes.NewReader([]byte(tt.body))
			} else {
				body = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tt.method, tt.path, body)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			testhelpers.AssertEqual(t, tt.expectedStatus, w.Code, "status code")
			if tt.expectedBody != "" {
				testhelpers.AssertContains(t, w.Body.String(), tt.expectedBody, "response body")
			}
		})
	}
}

// ========================================
// Concurrent Webhook Processing Tests
// ========================================

// TestWebhookFlow_ConcurrentAdapterRegistration tests concurrent adapter registration
func TestWebhookFlow_ConcurrentAdapterRegistration(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			adapter := testhelpers.NewMockAlertAdapter("adapter-" + string(rune('a'+n%26)) + string(rune('0'+n/26)))
			h.RegisterAdapter(adapter)
		}(i)
	}

	wg.Wait()

	// Should have registered all unique adapters without panic
	if len(h.adapters) == 0 {
		t.Error("expected adapters to be registered")
	}
}

// TestWebhookFlow_ConcurrentMethodValidation tests concurrent method not allowed responses
func TestWebhookFlow_ConcurrentMethodValidation(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	var wg sync.WaitGroup
	numRequests := 50

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Use GET which should return method not allowed
			req := httptest.NewRequest(http.MethodGet, "/webhook/alert/test-uuid", nil)
			w := httptest.NewRecorder()
			h.HandleWebhook(w, req)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405, got %d", w.Code)
			}
		}()
	}

	wg.Wait()
}

// ========================================
// Alert Normalization Flow Tests
// ========================================

// TestAlertNormalization_SeverityMapping tests severity mapping consistency
func TestAlertNormalization_SeverityMapping(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	severityTests := []struct {
		input    string
		expected database.AlertSeverity
	}{
		{"critical", database.AlertSeverityCritical},
		{"CRITICAL", database.AlertSeverityCritical},
		{"high", database.AlertSeverityHigh},
		{"HIGH", database.AlertSeverityHigh},
		{"error", database.AlertSeverityHigh},
		{"warning", database.AlertSeverityWarning},
		{"warn", database.AlertSeverityWarning},
		{"info", database.AlertSeverityInfo},
		{"informational", database.AlertSeverityInfo},
		{"unknown", database.AlertSeverityWarning}, // default
		{"", database.AlertSeverityWarning},        // empty defaults to warning
	}

	for _, tc := range severityTests {
		t.Run("severity_"+tc.input, func(t *testing.T) {
			payload := `{
				"alerts": [{
					"status": "firing",
					"labels": {"alertname": "Test", "severity": "` + tc.input + `"},
					"annotations": {},
					"fingerprint": "test-fp"
				}]
			}`

			alerts, err := adapter.ParsePayload([]byte(payload), instance)
			testhelpers.AssertNoError(t, err, "ParsePayload")
			if len(alerts) > 0 {
				testhelpers.AssertEqual(t, tc.expected, alerts[0].Severity, "severity for input "+tc.input)
			}
		})
	}
}

// TestAlertNormalization_StatusMapping tests status mapping consistency
func TestAlertNormalization_StatusMapping(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	statusTests := []struct {
		input    string
		expected database.AlertStatus
	}{
		{"firing", database.AlertStatusFiring},
		{"FIRING", database.AlertStatusFiring},
		{"resolved", database.AlertStatusResolved},
		{"RESOLVED", database.AlertStatusResolved},
	}

	for _, tc := range statusTests {
		t.Run("status_"+tc.input, func(t *testing.T) {
			payload := `{
				"alerts": [{
					"status": "` + tc.input + `",
					"labels": {"alertname": "Test"},
					"annotations": {},
					"fingerprint": "test-fp"
				}]
			}`

			alerts, err := adapter.ParsePayload([]byte(payload), instance)
			testhelpers.AssertNoError(t, err, "ParsePayload")
			if len(alerts) > 0 {
				testhelpers.AssertEqual(t, tc.expected, alerts[0].Status, "status for input "+tc.input)
			}
		})
	}
}

// ========================================
// Error Handling Integration Tests
// ========================================

// TestWebhookFlow_ErrorHandling tests various error scenarios
func TestWebhookFlow_ErrorHandling(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	errorTests := []struct {
		name    string
		payload string
	}{
		{"invalid json", `{not valid json}`},
		{"truncated json", `{"alerts": [`},
		{"wrong type for alerts", `{"alerts": "not an array"}`},
	}

	for _, tc := range errorTests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adapter.ParsePayload([]byte(tc.payload), instance)
			testhelpers.AssertError(t, err, "ParsePayload should error for "+tc.name)
		})
	}

	// null payload returns empty alerts (not an error)
	t.Run("null payload returns empty", func(t *testing.T) {
		alerts, err := adapter.ParsePayload([]byte(`null`), instance)
		testhelpers.AssertNoError(t, err, "null payload should not error")
		testhelpers.AssertEqual(t, 0, len(alerts), "null payload should return empty alerts")
	})
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkWebhookFlow_AlertmanagerParsing(b *testing.B) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}
	payload := []byte(`{
		"version": "4",
		"status": "firing",
		"alerts": [
			{"status": "firing", "labels": {"alertname": "Alert1", "severity": "critical"}, "annotations": {"summary": "Test"}, "fingerprint": "fp1"},
			{"status": "firing", "labels": {"alertname": "Alert2", "severity": "warning"}, "annotations": {"summary": "Test"}, "fingerprint": "fp2"},
			{"status": "resolved", "labels": {"alertname": "Alert3", "severity": "info"}, "annotations": {}, "fingerprint": "fp3"}
		]
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.ParsePayload(payload, instance)
	}
}

func BenchmarkWebhookFlow_GrafanaParsing(b *testing.B) {
	adapter := adapters.NewGrafanaAdapter()
	instance := &database.AlertSourceInstance{}
	payload := []byte(`{
		"version": "1",
		"status": "firing",
		"alerts": [
			{"status": "firing", "labels": {"alertname": "GrafanaAlert"}, "annotations": {}, "fingerprint": "g1"}
		]
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.ParsePayload(payload, instance)
	}
}

func BenchmarkWebhookFlow_ZabbixParsing(b *testing.B) {
	adapter := adapters.NewZabbixAdapter()
	instance := &database.AlertSourceInstance{}
	payload := []byte(`{
		"event_id": "123456",
		"event_status": "PROBLEM",
		"alert_name": "Test Alert",
		"priority": "4",
		"hardware": "test-server"
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = adapter.ParsePayload(payload, instance)
	}
}

func BenchmarkAlertHandler_ConcurrentRegistration(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
		for j := 0; j < 10; j++ {
			h.RegisterAdapter(&mockAlertAdapter{sourceType: "type-" + string(rune('0'+j))})
		}
	}
}
