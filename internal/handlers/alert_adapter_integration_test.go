package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/alerts/adapters"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// ========================================
// Cross-Adapter Integration Tests
// ========================================

// TestAlertAdapters_ConsistentNormalization tests that all adapters produce consistent normalized alerts
func TestAlertAdapters_ConsistentNormalization(t *testing.T) {
	// Define a set of common alert scenarios that all adapters should handle
	tests := []struct {
		name           string
		adapterFactory func() alerts.AlertAdapter
		payload        string
		expectedFields func(*testing.T, alerts.NormalizedAlert)
	}{
		{
			name:           "alertmanager_critical_firing",
			adapterFactory: func() alerts.AlertAdapter { return adapters.NewAlertmanagerAdapter() },
			payload: `{
				"alerts": [{
					"status": "firing",
					"labels": {"alertname": "HighCPU", "severity": "critical", "instance": "server-01"},
					"annotations": {"summary": "CPU is high"},
					"fingerprint": "fp-am-1"
				}]
			}`,
			expectedFields: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertSeverityCritical, a.Severity, "severity")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "status")
			},
		},
		{
			name:           "grafana_warning_resolved",
			adapterFactory: func() alerts.AlertAdapter { return adapters.NewGrafanaAdapter() },
			payload: `{
				"alerts": [{
					"status": "resolved",
					"labels": {"alertname": "DiskFull", "severity": "warning"},
					"annotations": {},
					"fingerprint": "fp-grafana-1"
				}]
			}`,
			expectedFields: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertSeverityWarning, a.Severity, "severity")
				testhelpers.AssertEqual(t, database.AlertStatusResolved, a.Status, "status")
			},
		},
		{
			name:           "zabbix_problem_high",
			adapterFactory: func() alerts.AlertAdapter { return adapters.NewZabbixAdapter() },
			payload: `{
				"event_id": "12345",
				"event_status": "PROBLEM",
				"alert_name": "Memory Alert",
				"priority": "4",
				"hardware": "db-server-01"
			}`,
			expectedFields: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "status")
				testhelpers.AssertEqual(t, "db-server-01", a.TargetHost, "target host")
			},
		},
		{
			name:           "datadog_error_triggered",
			adapterFactory: func() alerts.AlertAdapter { return adapters.NewDatadogAdapter() },
			payload: `{
				"id": "dd-123",
				"title": "API Latency High",
				"body": "Latency exceeded threshold",
				"alert_type": "error",
				"alert_status": "Triggered",
				"hostname": "api-gateway-01"
			}`,
			expectedFields: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertSeverityCritical, a.Severity, "severity")
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "status")
				testhelpers.AssertEqual(t, "api-gateway-01", a.TargetHost, "target host")
			},
		},
		{
			name:           "pagerduty_incident_triggered",
			adapterFactory: func() alerts.AlertAdapter { return adapters.NewPagerDutyAdapter() },
			payload: `{
				"event": {
					"event_type": "incident.triggered",
					"data": {
						"id": "PD-456",
						"title": "Database Connection Pool Exhausted",
						"description": "Connection pool at 100%",
						"service": {"name": "user-service"}
					}
				}
			}`,
			expectedFields: func(t *testing.T, a alerts.NormalizedAlert) {
				testhelpers.AssertEqual(t, database.AlertStatusFiring, a.Status, "status")
				testhelpers.AssertContains(t, a.AlertName, "Database Connection", "alert name")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := tt.adapterFactory()
			instance := &database.AlertSourceInstance{}

			alerts, err := adapter.ParsePayload([]byte(tt.payload), instance)
			testhelpers.AssertNoError(t, err, "ParsePayload")

			if len(alerts) == 0 {
				t.Fatal("expected at least one alert")
			}

			// Run adapter-specific validations
			tt.expectedFields(t, alerts[0])

			// Common validations for all normalized alerts
			if alerts[0].SourceFingerprint == "" && alerts[0].SourceAlertID == "" {
				t.Log("Warning: alert has no fingerprint or source ID")
			}
		})
	}
}

// TestAlertAdapters_MalformedPayloadRecovery tests adapter resilience to malformed data
func TestAlertAdapters_MalformedPayloadRecovery(t *testing.T) {
	adapterFactories := map[string]func() alerts.AlertAdapter{
		"alertmanager": func() alerts.AlertAdapter { return adapters.NewAlertmanagerAdapter() },
		"grafana":      func() alerts.AlertAdapter { return adapters.NewGrafanaAdapter() },
		"zabbix":       func() alerts.AlertAdapter { return adapters.NewZabbixAdapter() },
		"datadog":      func() alerts.AlertAdapter { return adapters.NewDatadogAdapter() },
		"pagerduty":    func() alerts.AlertAdapter { return adapters.NewPagerDutyAdapter() },
	}

	malformedPayloads := []struct {
		name        string
		payload     string
		shouldError bool
	}{
		{"empty_json", `{}`, false},           // Most adapters should handle gracefully
		{"empty_array", `[]`, true},           // Array instead of object
		{"invalid_json", `{not valid}`, true}, // Invalid JSON
		{"truncated", `{"alerts": [`, true},   // Truncated JSON
		{"wrong_type", `"string"`, true},      // String instead of object
		{"null", `null`, false},               // Null value (should return empty)
	}

	for adapterName, factory := range adapterFactories {
		for _, mp := range malformedPayloads {
			t.Run(adapterName+"_"+mp.name, func(t *testing.T) {
				adapter := factory()
				instance := &database.AlertSourceInstance{}

				_, err := adapter.ParsePayload([]byte(mp.payload), instance)

				if mp.shouldError && err == nil {
					t.Errorf("expected error for malformed payload %s, got nil", mp.name)
				}
				// Note: not erroring on expected errors is acceptable
			})
		}
	}
}

// TestAlertAdapters_ConcurrentParsing tests thread safety of adapter parsing
func TestAlertAdapters_ConcurrentParsing(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "ConcurrencyTest", "severity": "warning"},
			"annotations": {"summary": "Testing concurrent access"},
			"fingerprint": "concurrent-fp"
		}]
	}`)

	var wg sync.WaitGroup
	numGoroutines := 100
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alerts, err := adapter.ParsePayload(payload, instance)
			if err != nil {
				errors <- err
				return
			}
			if len(alerts) != 1 {
				errors <- errMock("unexpected alert count")
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent parsing error: %v", err)
	}
}

// ========================================
// Webhook Secret Validation Integration Tests
// ========================================

// TestWebhookSecretValidation_AllAdapters tests secret validation consistency
func TestWebhookSecretValidation_AllAdapters(t *testing.T) {
	tests := []struct {
		name           string
		adapter        alerts.AlertAdapter
		secret         string
		headerName     string
		headerValue    string
		expectValid    bool
	}{
		// Alertmanager variations
		{"alertmanager_custom_header", adapters.NewAlertmanagerAdapter(), "secret123", "X-Alertmanager-Secret", "secret123", true},
		{"alertmanager_bearer", adapters.NewAlertmanagerAdapter(), "secret123", "Authorization", "Bearer secret123", true},
		{"alertmanager_wrong_secret", adapters.NewAlertmanagerAdapter(), "secret123", "X-Alertmanager-Secret", "wrong", false},
		{"alertmanager_no_secret_no_header", adapters.NewAlertmanagerAdapter(), "", "", "", true},

		// Grafana variations
		{"grafana_custom_header", adapters.NewGrafanaAdapter(), "grafana-key", "X-Grafana-Secret", "grafana-key", true},
		{"grafana_bearer", adapters.NewGrafanaAdapter(), "grafana-key", "Authorization", "Bearer grafana-key", true},

		// Datadog variations
		{"datadog_api_key", adapters.NewDatadogAdapter(), "dd-api-key", "DD-API-KEY", "dd-api-key", true},
		{"datadog_signature", adapters.NewDatadogAdapter(), "dd-secret", "X-Datadog-Signature", "dd-secret", true},

		// Zabbix variations (only supports X-Zabbix-Secret header)
		{"zabbix_custom_header", adapters.NewZabbixAdapter(), "zabbix-key", "X-Zabbix-Secret", "zabbix-key", true},
		{"zabbix_wrong_header", adapters.NewZabbixAdapter(), "zabbix-key", "Authorization", "zabbix-key", false},

		// PagerDuty variations (signature format: v1=<hmac> or Bearer token)
		{"pagerduty_signature", adapters.NewPagerDutyAdapter(), "pd-token", "X-PagerDuty-Signature", "v1=abc123", true},
		{"pagerduty_bearer", adapters.NewPagerDutyAdapter(), "pd-token", "Authorization", "Bearer pd-token", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &database.AlertSourceInstance{
				WebhookSecret: tt.secret,
			}

			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			if tt.headerName != "" {
				req.Header.Set(tt.headerName, tt.headerValue)
			}

			err := tt.adapter.ValidateWebhookSecret(req, instance)

			if tt.expectValid && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tt.expectValid && err == nil {
				t.Error("expected error for invalid secret, got nil")
			}
		})
	}
}

// ========================================
// Alert Handler Registration Integration Tests
// ========================================

// TestAlertHandler_AdapterRegistration tests dynamic adapter registration
func TestAlertHandler_AdapterRegistration(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Register all built-in adapters
	adaptersToRegister := []alerts.AlertAdapter{
		adapters.NewAlertmanagerAdapter(),
		adapters.NewGrafanaAdapter(),
		adapters.NewZabbixAdapter(),
		adapters.NewDatadogAdapter(),
		adapters.NewPagerDutyAdapter(),
	}

	for _, adapter := range adaptersToRegister {
		h.RegisterAdapter(adapter)
	}

	// Verify all adapters are registered
	if len(h.adapters) != len(adaptersToRegister) {
		t.Errorf("expected %d adapters, got %d", len(adaptersToRegister), len(h.adapters))
	}

	// Verify adapters are retrievable by type
	expectedTypes := []string{"alertmanager", "grafana", "zabbix", "datadog", "pagerduty"}
	for _, adapterType := range expectedTypes {
		if _, exists := h.adapters[adapterType]; !exists {
			t.Errorf("adapter %q not found in registry", adapterType)
		}
	}
}

// TestAlertHandler_AdapterOverwrite tests that re-registering an adapter overwrites
func TestAlertHandler_AdapterOverwrite(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Register first adapter
	adapter1 := testhelpers.NewMockAlertAdapter("custom")
	h.RegisterAdapter(adapter1)

	// Register second adapter with same type
	adapter2 := testhelpers.NewMockAlertAdapter("custom")
	h.RegisterAdapter(adapter2)

	// Should have only one adapter
	if len(h.adapters) != 1 {
		t.Errorf("expected 1 adapter after overwrite, got %d", len(h.adapters))
	}
}

// ========================================
// Alert Payload Size Integration Tests
// ========================================

// TestAlertAdapters_LargePayloadHandling tests handling of large alert payloads
func TestAlertAdapters_LargePayloadHandling(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	// Generate a large payload with many alerts
	var alerts []interface{}
	for i := 0; i < 100; i++ {
		alerts = append(alerts, map[string]interface{}{
			"status": "firing",
			"labels": map[string]string{
				"alertname":  "BatchAlert",
				"severity":   "warning",
				"instance":   "server-" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10)),
				"long_label": strings.Repeat("x", 500), // Long label value
			},
			"annotations": map[string]string{
				"summary":     "Batch alert " + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10)),
				"description": strings.Repeat("Long description text. ", 50),
			},
			"fingerprint": "batch-fp-" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10)),
		})
	}

	payload, _ := json.Marshal(map[string]interface{}{"alerts": alerts})

	parsedAlerts, err := adapter.ParsePayload(payload, instance)
	testhelpers.AssertNoError(t, err, "ParsePayload")
	testhelpers.AssertEqual(t, 100, len(parsedAlerts), "alert count")
}

// ========================================
// Alert Time Parsing Integration Tests
// ========================================

// TestAlertAdapters_TimestampParsing tests various timestamp formats
func TestAlertAdapters_TimestampParsing(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	tests := []struct {
		name       string
		startsAt   string
		endsAt     string
		shouldHave bool
	}{
		{
			name:       "ISO8601_UTC",
			startsAt:   "2024-01-15T10:30:00Z",
			endsAt:     "2024-01-15T11:00:00Z",
			shouldHave: true,
		},
		{
			name:       "ISO8601_with_offset",
			startsAt:   "2024-01-15T10:30:00+05:00",
			endsAt:     "2024-01-15T11:00:00+05:00",
			shouldHave: true,
		},
		{
			name:       "zero_time_for_ongoing",
			startsAt:   "2024-01-15T10:30:00Z",
			endsAt:     "0001-01-01T00:00:00Z",
			shouldHave: true, // StartedAt should be set, EndedAt should be nil
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{
				"alerts": [{
					"status": "firing",
					"labels": {"alertname": "TimeTest"},
					"annotations": {},
					"startsAt": "` + tt.startsAt + `",
					"endsAt": "` + tt.endsAt + `",
					"fingerprint": "time-fp"
				}]
			}`)

			alerts, err := adapter.ParsePayload(payload, instance)
			testhelpers.AssertNoError(t, err, "ParsePayload")

			if len(alerts) == 0 {
				t.Fatal("expected at least one alert")
			}

			alert := alerts[0]
			if tt.shouldHave && alert.StartedAt == nil {
				t.Error("expected StartedAt to be set")
			}
		})
	}
}

// ========================================
// Webhook Handler Error Recovery Tests
// ========================================

// TestWebhookHandler_ErrorRecovery tests graceful error handling
func TestWebhookHandler_ErrorRecovery(t *testing.T) {
	// Note: This test is commented out because AlertHandler.HandleWebhook
	// requires database access (alertService.GetInstanceByUUID) which panics
	// when the service is nil. This is intentional - the handler should have
	// all required dependencies injected.
	//
	// Instead, we test the HTTP method validation which doesn't require DB access.
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		{
			name:           "get_method_not_allowed",
			method:         http.MethodGet,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "put_method_not_allowed",
			method:         http.MethodPut,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "delete_method_not_allowed",
			method:         http.MethodDelete,
			path:           "/webhook/alert/test-uuid",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			h.HandleWebhook(w, req)

			testhelpers.AssertEqual(t, tt.expectedStatus, w.Code, "status code")
		})
	}
}

// ========================================
// Benchmarks for Integration Flows
// ========================================

func BenchmarkAlertAdapters_AllParsePayload(b *testing.B) {
	adaptersToTest := map[string]struct {
		adapter alerts.AlertAdapter
		payload []byte
	}{
		"alertmanager": {
			adapter: adapters.NewAlertmanagerAdapter(),
			payload: []byte(`{"alerts": [{"status": "firing", "labels": {"alertname": "Test"}, "annotations": {}, "fingerprint": "fp"}]}`),
		},
		"grafana": {
			adapter: adapters.NewGrafanaAdapter(),
			payload: []byte(`{"alerts": [{"status": "firing", "labels": {"alertname": "Test"}, "annotations": {}, "fingerprint": "fp"}]}`),
		},
		"zabbix": {
			adapter: adapters.NewZabbixAdapter(),
			payload: []byte(`{"event_id": "123", "event_status": "PROBLEM", "alert_name": "Test", "priority": "3"}`),
		},
		"datadog": {
			adapter: adapters.NewDatadogAdapter(),
			payload: []byte(`{"id": "123", "title": "Test", "alert_type": "error"}`),
		},
	}

	instance := &database.AlertSourceInstance{}

	for name, tc := range adaptersToTest {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = tc.adapter.ParsePayload(tc.payload, instance)
			}
		})
	}
}

func BenchmarkWebhookSecretValidation_Parallel(b *testing.B) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{
		WebhookSecret: "benchmark-secret-key-12345",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header.Set("X-Alertmanager-Secret", "benchmark-secret-key-12345")
			_ = adapter.ValidateWebhookSecret(req, instance)
		}
	})
}

func BenchmarkAlertHandler_ConcurrentRequests(b *testing.B) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	adapter := adapters.NewAlertmanagerAdapter()
	h.RegisterAdapter(adapter)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/webhook/alert/test", nil)
			w := httptest.NewRecorder()
			h.HandleWebhook(w, req)
		}
	})
}

// ========================================
// Alert Deduplication Integration Tests
// ========================================

// TestAlertDeduplication_ByFingerprint tests alert deduplication logic
func TestAlertDeduplication_ByFingerprint(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	// Send same alert multiple times
	payload := []byte(`{
		"alerts": [
			{"status": "firing", "labels": {"alertname": "DupeTest"}, "annotations": {}, "fingerprint": "same-fingerprint"},
			{"status": "firing", "labels": {"alertname": "DupeTest"}, "annotations": {}, "fingerprint": "same-fingerprint"},
			{"status": "firing", "labels": {"alertname": "Different"}, "annotations": {}, "fingerprint": "different-fingerprint"}
		]
	}`)

	alerts, err := adapter.ParsePayload(payload, instance)
	testhelpers.AssertNoError(t, err, "ParsePayload")

	// All alerts should be parsed (deduplication happens at a higher level)
	testhelpers.AssertEqual(t, 3, len(alerts), "alert count")

	// But we can verify fingerprints are correctly set
	fingerprintCount := make(map[string]int)
	for _, alert := range alerts {
		fingerprintCount[alert.SourceFingerprint]++
	}

	testhelpers.AssertEqual(t, 2, fingerprintCount["same-fingerprint"], "same fingerprint count")
	testhelpers.AssertEqual(t, 1, fingerprintCount["different-fingerprint"], "different fingerprint count")
}

// ========================================
// Timing Tests
// ========================================

// TestAlertParsing_ResponseTime tests that parsing completes within expected time
func TestAlertParsing_ResponseTime(t *testing.T) {
	adapter := adapters.NewAlertmanagerAdapter()
	instance := &database.AlertSourceInstance{}

	payload := []byte(`{
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "TimeTest", "severity": "critical"},
			"annotations": {"summary": "Test summary"},
			"fingerprint": "timing-fp"
		}]
	}`)

	start := time.Now()
	_, err := adapter.ParsePayload(payload, instance)
	duration := time.Since(start)

	testhelpers.AssertNoError(t, err, "ParsePayload")

	// Parsing should complete in under 10ms for a simple payload
	if duration > 10*time.Millisecond {
		t.Errorf("parsing took too long: %v (expected < 10ms)", duration)
	}
}
