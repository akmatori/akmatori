// Package testhelpers provides webhook request builders for testing
package testhelpers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ========================================
// Webhook Request Builder
// ========================================

// WebhookRequestBuilder builds HTTP requests for webhook testing
type WebhookRequestBuilder struct {
	t         *testing.T
	method    string
	path      string
	body      []byte
	headers   map[string]string
	timestamp time.Time
}

// NewWebhookRequest creates a new webhook request builder
func NewWebhookRequest(t *testing.T) *WebhookRequestBuilder {
	t.Helper()
	return &WebhookRequestBuilder{
		t:         t,
		method:    http.MethodPost,
		path:      "/webhook/alert/",
		headers:   make(map[string]string),
		timestamp: time.Now(),
	}
}

// WithInstanceUUID sets the alert source instance UUID in the path
func (b *WebhookRequestBuilder) WithInstanceUUID(uuid string) *WebhookRequestBuilder {
	b.path = "/webhook/alert/" + uuid
	return b
}

// WithPath sets a custom path
func (b *WebhookRequestBuilder) WithPath(path string) *WebhookRequestBuilder {
	b.path = path
	return b
}

// WithMethod sets the HTTP method (default: POST)
func (b *WebhookRequestBuilder) WithMethod(method string) *WebhookRequestBuilder {
	b.method = method
	return b
}

// WithBody sets the request body as bytes
func (b *WebhookRequestBuilder) WithBody(body []byte) *WebhookRequestBuilder {
	b.body = body
	return b
}

// WithJSONBody marshals and sets the request body as JSON
func (b *WebhookRequestBuilder) WithJSONBody(v interface{}) *WebhookRequestBuilder {
	body, err := json.Marshal(v)
	if err != nil {
		b.t.Fatalf("failed to marshal JSON body: %v", err)
	}
	b.body = body
	b.headers["Content-Type"] = "application/json"
	return b
}

// WithHeader adds a header
func (b *WebhookRequestBuilder) WithHeader(key, value string) *WebhookRequestBuilder {
	b.headers[key] = value
	return b
}

// WithContentType sets the Content-Type header
func (b *WebhookRequestBuilder) WithContentType(ct string) *WebhookRequestBuilder {
	return b.WithHeader("Content-Type", ct)
}

// WithTimestamp sets the timestamp for signature generation
func (b *WebhookRequestBuilder) WithTimestamp(ts time.Time) *WebhookRequestBuilder {
	b.timestamp = ts
	return b
}

// WithSlackSignature adds Slack-style webhook signature headers
// Uses HMAC-SHA256 with format: v0=hmac(v0:timestamp:body)
func (b *WebhookRequestBuilder) WithSlackSignature(secret string) *WebhookRequestBuilder {
	ts := fmt.Sprintf("%d", b.timestamp.Unix())
	baseString := "v0:" + ts + ":" + string(b.body)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(baseString))
	sig := "v0=" + hex.EncodeToString(h.Sum(nil))

	b.headers["X-Slack-Request-Timestamp"] = ts
	b.headers["X-Slack-Signature"] = sig
	return b
}

// WithPagerDutySignature adds PagerDuty-style webhook signature
// Uses HMAC-SHA256 of the body
func (b *WebhookRequestBuilder) WithPagerDutySignature(secret string) *WebhookRequestBuilder {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(b.body)
	sig := "v1=" + hex.EncodeToString(h.Sum(nil))

	b.headers["X-PagerDuty-Signature"] = sig
	return b
}

// WithGrafanaSignature adds Grafana-style webhook signature
func (b *WebhookRequestBuilder) WithGrafanaSignature(secret string) *WebhookRequestBuilder {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(b.body)
	sig := hex.EncodeToString(h.Sum(nil))

	b.headers["X-Grafana-Signature"] = sig
	return b
}

// WithBasicAuth adds Basic authentication header
func (b *WebhookRequestBuilder) WithBasicAuth(username, password string) *WebhookRequestBuilder {
	auth := username + ":" + password
	encoded := "Basic " + encodeBase64(auth)
	return b.WithHeader("Authorization", encoded)
}

// WithBearerToken adds Bearer token authentication
func (b *WebhookRequestBuilder) WithBearerToken(token string) *WebhookRequestBuilder {
	return b.WithHeader("Authorization", "Bearer "+token)
}

// Build constructs the HTTP request
func (b *WebhookRequestBuilder) Build() *http.Request {
	b.t.Helper()

	var req *http.Request
	if b.body != nil {
		req = httptest.NewRequest(b.method, b.path, bytes.NewReader(b.body))
	} else {
		req = httptest.NewRequest(b.method, b.path, nil)
	}

	for k, v := range b.headers {
		req.Header.Set(k, v)
	}

	return req
}

// BuildWithRecorder constructs the request and returns it with a response recorder
func (b *WebhookRequestBuilder) BuildWithRecorder() (*http.Request, *httptest.ResponseRecorder) {
	b.t.Helper()
	return b.Build(), httptest.NewRecorder()
}

// ========================================
// Alert Payload Builders
// ========================================

// AlertmanagerPayload builds an Alertmanager webhook payload
type AlertmanagerPayload struct {
	Version           string                 `json:"version"`
	GroupKey          string                 `json:"groupKey"`
	Status            string                 `json:"status"`
	Receiver          string                 `json:"receiver"`
	GroupLabels       map[string]string      `json:"groupLabels"`
	CommonLabels      map[string]string      `json:"commonLabels"`
	CommonAnnotations map[string]string      `json:"commonAnnotations"`
	ExternalURL       string                 `json:"externalURL"`
	Alerts            []AlertmanagerAlert    `json:"alerts"`
}

// AlertmanagerAlert represents a single alert in the payload
type AlertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// NewAlertmanagerPayload creates a new Alertmanager payload builder
func NewAlertmanagerPayload() *AlertmanagerPayload {
	return &AlertmanagerPayload{
		Version:           "4",
		GroupKey:          "{}:{alertname=\"TestAlert\"}",
		Status:            "firing",
		Receiver:          "akmatori",
		GroupLabels:       map[string]string{"alertname": "TestAlert"},
		CommonLabels:      map[string]string{"alertname": "TestAlert", "severity": "warning"},
		CommonAnnotations: map[string]string{"summary": "Test alert"},
		ExternalURL:       "http://alertmanager:9093",
		Alerts:            []AlertmanagerAlert{},
	}
}

// WithStatus sets the overall status
func (p *AlertmanagerPayload) WithStatus(status string) *AlertmanagerPayload {
	p.Status = status
	return p
}

// WithAlert adds an alert to the payload
func (p *AlertmanagerPayload) WithAlert(alert AlertmanagerAlert) *AlertmanagerPayload {
	p.Alerts = append(p.Alerts, alert)
	return p
}

// WithFiringAlert adds a firing alert with the given name and severity
func (p *AlertmanagerPayload) WithFiringAlert(name, severity string) *AlertmanagerPayload {
	return p.WithAlert(AlertmanagerAlert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": name,
			"severity":  severity,
		},
		Annotations: map[string]string{
			"summary": name + " alert",
		},
		StartsAt:    time.Now().Format(time.RFC3339),
		EndsAt:      "0001-01-01T00:00:00Z",
		Fingerprint: randomHex(12),
	})
}

// WithResolvedAlert adds a resolved alert
func (p *AlertmanagerPayload) WithResolvedAlert(name string) *AlertmanagerPayload {
	return p.WithAlert(AlertmanagerAlert{
		Status: "resolved",
		Labels: map[string]string{
			"alertname": name,
		},
		Annotations: map[string]string{
			"summary": name + " resolved",
		},
		StartsAt:    time.Now().Add(-time.Hour).Format(time.RFC3339),
		EndsAt:      time.Now().Format(time.RFC3339),
		Fingerprint: randomHex(12),
	})
}

// Build returns the payload as JSON bytes
func (p *AlertmanagerPayload) Build(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("failed to marshal Alertmanager payload: %v", err)
	}
	return data
}

// ========================================
// Helper functions
// ========================================

func encodeBase64(s string) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	result := make([]byte, 0, len(s)*2)
	padding := (3 - len(s)%3) % 3

	bytes := []byte(s)
	for i := 0; i < len(bytes); i += 3 {
		var n uint32
		for j := 0; j < 3; j++ {
			n <<= 8
			if i+j < len(bytes) {
				n |= uint32(bytes[i+j])
			}
		}

		for j := 3; j >= 0; j-- {
			if i*4/3+3-j < len(s)*4/3+4-padding {
				result = append(result, base64Chars[(n>>(uint(j)*6))&0x3F])
			}
		}
	}

	for i := 0; i < padding; i++ {
		result = append(result, '=')
	}

	return string(result)
}

func randomHex(n int) string {
	// Use timestamp-based pseudo-random for testing (deterministic enough)
	h := sha256.Sum256([]byte(time.Now().String()))
	if n > len(h)*2 {
		n = len(h) * 2
	}
	return hex.EncodeToString(h[:])[:n]
}
