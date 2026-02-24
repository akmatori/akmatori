package testhelpers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func TestHTTPTestContext_NewAndExecute(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	
	if ctx.T == nil {
		t.Error("T should not be nil")
	}
	if ctx.Recorder == nil {
		t.Error("Recorder should not be nil")
	}
	if ctx.Request == nil {
		t.Error("Request should not be nil")
	}
	if ctx.Request.Method != http.MethodGet {
		t.Errorf("expected method GET, got %s", ctx.Request.Method)
	}
}

func TestHTTPTestContext_WithHeader(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	ctx.WithHeader("X-Custom", "value")
	
	if ctx.Request.Header.Get("X-Custom") != "value" {
		t.Error("header not set correctly")
	}
}

func TestHTTPTestContext_WithAPIKey(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	ctx.WithAPIKey("test-key")
	
	if ctx.Request.Header.Get("X-API-Key") != "test-key" {
		t.Error("API key header not set correctly")
	}
}

func TestHTTPTestContext_WithBearerToken(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	ctx.WithBearerToken("my-token")
	
	expected := "Bearer my-token"
	if ctx.Request.Header.Get("Authorization") != expected {
		t.Errorf("expected %q, got %q", expected, ctx.Request.Header.Get("Authorization"))
	}
}

func TestHTTPTestContext_ExecuteFunc(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}
	
	ctx.ExecuteFunc(handler)
	
	if ctx.Recorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", ctx.Recorder.Code)
	}
	if ctx.Recorder.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", ctx.Recorder.Body.String())
	}
}

func TestHTTPTestContext_WithJSONBody(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodPost, "/test", nil)
	
	body := map[string]string{"key": "value"}
	ctx.WithJSONBody(body)
	
	contentType := ctx.Request.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

func TestHTTPTestContext_DecodeJSON(t *testing.T) {
	ctx := NewHTTPTestContext(t, http.MethodGet, "/test", nil)
	
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
	}
	
	ctx.ExecuteFunc(handler)
	
	var result map[string]string
	ctx.DecodeJSON(&result)
	
	if result["result"] != "ok" {
		t.Errorf("expected result 'ok', got %q", result["result"])
	}
}

func TestMockAlertAdapter_Basic(t *testing.T) {
	mock := NewMockAlertAdapter("prometheus")
	
	if mock.GetSourceType() != "prometheus" {
		t.Errorf("expected source type 'prometheus', got %s", mock.GetSourceType())
	}
	
	alerts, err := mock.ParsePayload([]byte("{}"), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !mock.ParsePayloadCalled {
		t.Error("ParsePayloadCalled should be true")
	}
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}

func TestMockAlertAdapter_WithAlerts(t *testing.T) {
	alert := NewAlertBuilder().WithName("TestAlert").Build()
	mock := NewMockAlertAdapter("grafana").WithAlerts(alert)
	
	alerts, err := mock.ParsePayload(nil, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertName != "TestAlert" {
		t.Errorf("expected alert name 'TestAlert', got %s", alerts[0].AlertName)
	}
}

func TestMockAlertAdapter_WithParseError(t *testing.T) {
	expectedErr := errors.New("parse failed")
	mock := NewMockAlertAdapter("datadog").WithParseError(expectedErr)
	
	_, err := mock.ParsePayload(nil, nil)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestMockAlertAdapter_ValidateWebhookSecret(t *testing.T) {
	mock := NewMockAlertAdapter("pagerduty")
	
	err := mock.ValidateWebhookSecret(nil, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !mock.ValidateSecretCalled {
		t.Error("ValidateSecretCalled should be true")
	}
	
	// Test with error
	expectedErr := errors.New("invalid secret")
	mock.WithValidationError(expectedErr)
	
	err = mock.ValidateWebhookSecret(nil, nil)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestNormalizedAlertBuilder(t *testing.T) {
	alert := NewAlertBuilder().
		WithName("HighCPU").
		WithSeverity(database.AlertSeverityCritical).
		WithStatus(database.AlertStatusFiring).
		WithHost("prod-server-1").
		WithService("nginx").
		WithLabel("env", "production").
		WithLabel("team", "sre").
		WithSummary("CPU usage above 90%").
		Build()
	
	if alert.AlertName != "HighCPU" {
		t.Errorf("expected AlertName 'HighCPU', got %s", alert.AlertName)
	}
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("expected Severity 'critical', got %s", alert.Severity)
	}
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("expected Status 'firing', got %s", alert.Status)
	}
	if alert.TargetHost != "prod-server-1" {
		t.Errorf("expected TargetHost 'prod-server-1', got %s", alert.TargetHost)
	}
	if alert.TargetService != "nginx" {
		t.Errorf("expected TargetService 'nginx', got %s", alert.TargetService)
	}
	if alert.TargetLabels["env"] != "production" {
		t.Errorf("expected label env='production', got %s", alert.TargetLabels["env"])
	}
	if alert.TargetLabels["team"] != "sre" {
		t.Errorf("expected label team='sre', got %s", alert.TargetLabels["team"])
	}
}

func TestIncidentBuilder(t *testing.T) {
	incident := NewIncidentBuilder().
		WithID(42).
		WithUUID("custom-uuid-123").
		WithTitle("Database outage").
		WithStatus(database.IncidentStatusRunning).
		WithSource("slack").
		Build()
	
	if incident.ID != 42 {
		t.Errorf("expected ID 42, got %d", incident.ID)
	}
	if incident.UUID != "custom-uuid-123" {
		t.Errorf("expected UUID 'custom-uuid-123', got %s", incident.UUID)
	}
	if incident.Title != "Database outage" {
		t.Errorf("expected Title 'Database outage', got %s", incident.Title)
	}
	if incident.Status != database.IncidentStatusRunning {
		t.Errorf("expected Status 'running', got %s", incident.Status)
	}
	if incident.Source != "slack" {
		t.Errorf("expected Source 'slack', got %s", incident.Source)
	}
}

func TestMustCompleteWithin_Success(t *testing.T) {
	mockT := &testing.T{}
	
	MustCompleteWithin(mockT, time.Second, func() {
		time.Sleep(10 * time.Millisecond)
	})
	
	if mockT.Failed() {
		t.Error("test should not have failed")
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "foo", false},
		{"hello", "hello", true},
		{"hello", "", true},
		{"", "hello", false},
		{"", "", true},
	}
	
	for _, tt := range tests {
		got := containsString(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("containsString(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

// Benchmark the HTTP test context creation
func BenchmarkHTTPTestContext_New(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewHTTPTestContext(&testing.T{}, http.MethodPost, "/api/v1/alerts", nil)
	}
}

func BenchmarkMockAlertAdapter_ParsePayload(b *testing.B) {
	mock := NewMockAlertAdapter("prometheus").
		WithAlerts(NewAlertBuilder().Build())
	
	body := []byte(`{"alerts": [{"labels": {"alertname": "test"}}]}`)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mock.ParsePayload(body, nil)
	}
}

func BenchmarkAlertBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewAlertBuilder().
			WithName("HighCPU").
			WithSeverity(database.AlertSeverityCritical).
			WithHost("prod-1").
			WithLabel("env", "prod").
			Build()
	}
}
