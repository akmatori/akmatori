package testhelpers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWebhookRequestBuilder_Basic(t *testing.T) {
	req := NewWebhookRequest(t).
		WithInstanceUUID("test-uuid-123").
		Build()

	if req.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", req.Method)
	}

	if req.URL.Path != "/webhook/alert/test-uuid-123" {
		t.Errorf("expected path '/webhook/alert/test-uuid-123', got %s", req.URL.Path)
	}
}

func TestWebhookRequestBuilder_WithJSONBody(t *testing.T) {
	body := map[string]string{"status": "firing"}

	req := NewWebhookRequest(t).
		WithInstanceUUID("test").
		WithJSONBody(body).
		Build()

	contentType := req.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %s", contentType)
	}
}

func TestWebhookRequestBuilder_WithHeaders(t *testing.T) {
	req := NewWebhookRequest(t).
		WithHeader("X-Custom", "value").
		WithHeader("X-Another", "value2").
		Build()

	if req.Header.Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom header 'value'")
	}
	if req.Header.Get("X-Another") != "value2" {
		t.Errorf("expected X-Another header 'value2'")
	}
}

func TestWebhookRequestBuilder_WithSlackSignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"test": true}`)
	ts := time.Unix(1705315800, 0)

	req := NewWebhookRequest(t).
		WithBody(body).
		WithTimestamp(ts).
		WithSlackSignature(secret).
		Build()

	// Verify timestamp header
	tsHeader := req.Header.Get("X-Slack-Request-Timestamp")
	if tsHeader != "1705315800" {
		t.Errorf("expected timestamp '1705315800', got %s", tsHeader)
	}

	// Verify signature
	sig := req.Header.Get("X-Slack-Signature")
	if !strings.HasPrefix(sig, "v0=") {
		t.Errorf("expected signature to start with 'v0=', got %s", sig)
	}

	// Verify signature is valid
	baseString := "v0:" + tsHeader + ":" + string(body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(baseString))
	expectedSig := "v0=" + hex.EncodeToString(h.Sum(nil))

	if sig != expectedSig {
		t.Errorf("signature mismatch\nexpected: %s\ngot: %s", expectedSig, sig)
	}
}

func TestWebhookRequestBuilder_WithPagerDutySignature(t *testing.T) {
	secret := "pd-secret"
	body := []byte(`{"event": "trigger"}`)

	req := NewWebhookRequest(t).
		WithBody(body).
		WithPagerDutySignature(secret).
		Build()

	sig := req.Header.Get("X-PagerDuty-Signature")
	if !strings.HasPrefix(sig, "v1=") {
		t.Errorf("expected signature to start with 'v1=', got %s", sig)
	}

	// Verify signature
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	expectedSig := "v1=" + hex.EncodeToString(h.Sum(nil))

	if sig != expectedSig {
		t.Errorf("signature mismatch\nexpected: %s\ngot: %s", expectedSig, sig)
	}
}

func TestWebhookRequestBuilder_WithGrafanaSignature(t *testing.T) {
	secret := "grafana-secret"
	body := []byte(`{"state": "alerting"}`)

	req := NewWebhookRequest(t).
		WithBody(body).
		WithGrafanaSignature(secret).
		Build()

	sig := req.Header.Get("X-Grafana-Signature")

	// Verify signature
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	expectedSig := hex.EncodeToString(h.Sum(nil))

	if sig != expectedSig {
		t.Errorf("signature mismatch\nexpected: %s\ngot: %s", expectedSig, sig)
	}
}

func TestWebhookRequestBuilder_WithBearerToken(t *testing.T) {
	req := NewWebhookRequest(t).
		WithBearerToken("my-token").
		Build()

	auth := req.Header.Get("Authorization")
	if auth != "Bearer my-token" {
		t.Errorf("expected 'Bearer my-token', got %s", auth)
	}
}

func TestWebhookRequestBuilder_WithMethod(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}

	for _, method := range methods {
		req := NewWebhookRequest(t).
			WithMethod(method).
			Build()

		if req.Method != method {
			t.Errorf("expected method %s, got %s", method, req.Method)
		}
	}
}

func TestWebhookRequestBuilder_BuildWithRecorder(t *testing.T) {
	req, rec := NewWebhookRequest(t).
		WithInstanceUUID("test").
		BuildWithRecorder()

	if req == nil {
		t.Fatal("request should not be nil")
	}
	if rec == nil {
		t.Fatal("recorder should not be nil")
	}
}

func TestAlertmanagerPayload_Basic(t *testing.T) {
	payload := NewAlertmanagerPayload().Build(t)

	if len(payload) == 0 {
		t.Error("payload should not be empty")
	}

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Errorf("payload is not valid JSON: %v", err)
	}

	if result["version"] != "4" {
		t.Errorf("expected version '4', got %v", result["version"])
	}
	if result["status"] != "firing" {
		t.Errorf("expected status 'firing', got %v", result["status"])
	}
}

func TestAlertmanagerPayload_WithAlerts(t *testing.T) {
	payload := NewAlertmanagerPayload().
		WithFiringAlert("HighCPU", "critical").
		WithFiringAlert("LowMemory", "warning").
		WithResolvedAlert("DiskFull").
		Build(t)

	var result AlertmanagerPayload
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if len(result.Alerts) != 3 {
		t.Errorf("expected 3 alerts, got %d", len(result.Alerts))
	}

	// Check first alert
	if result.Alerts[0].Labels["alertname"] != "HighCPU" {
		t.Errorf("expected first alert name 'HighCPU'")
	}
	if result.Alerts[0].Status != "firing" {
		t.Errorf("expected first alert status 'firing'")
	}

	// Check resolved alert
	if result.Alerts[2].Status != "resolved" {
		t.Errorf("expected third alert status 'resolved'")
	}
}

func TestAlertmanagerPayload_WithStatus(t *testing.T) {
	payload := NewAlertmanagerPayload().
		WithStatus("resolved").
		WithResolvedAlert("TestAlert").
		Build(t)

	var result AlertmanagerPayload
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if result.Status != "resolved" {
		t.Errorf("expected status 'resolved', got %s", result.Status)
	}
}

// Integration test: build a complete webhook request
func TestWebhookRequestBuilder_CompleteFlow(t *testing.T) {
	// Build payload
	payload := NewAlertmanagerPayload().
		WithFiringAlert("HighCPU", "critical").
		Build(t)

	// Build request with signature
	req, rec := NewWebhookRequest(t).
		WithInstanceUUID("prod-alertmanager").
		WithBody(payload).
		WithContentType("application/json").
		WithSlackSignature("webhook-secret").
		BuildWithRecorder()

	// Verify request
	if req.URL.Path != "/webhook/alert/prod-alertmanager" {
		t.Errorf("unexpected path: %s", req.URL.Path)
	}

	if req.Header.Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	if req.Header.Get("X-Slack-Signature") == "" {
		t.Error("X-Slack-Signature should be set")
	}

	// Recorder is ready for use
	if rec == nil {
		t.Error("recorder should not be nil")
	}
}

// Benchmark payload building
func BenchmarkAlertmanagerPayload_Build(b *testing.B) {
	t := &testing.T{}
	for i := 0; i < b.N; i++ {
		NewAlertmanagerPayload().
			WithFiringAlert("Alert1", "critical").
			WithFiringAlert("Alert2", "warning").
			Build(t)
	}
}

func BenchmarkWebhookRequest_Build(b *testing.B) {
	t := &testing.T{}
	body := []byte(`{"status": "firing"}`)

	for i := 0; i < b.N; i++ {
		NewWebhookRequest(t).
			WithInstanceUUID("test-uuid").
			WithBody(body).
			WithSlackSignature("secret").
			Build()
	}
}
