// Package testhelpers provides reusable testing utilities for Akmatori.
//
// This package contains:
// - HTTP test helpers (creating test servers, requests)
// - Mock implementations (alert adapters, database, etc.)
// - Test fixtures loaders
// - Assertion helpers
package testhelpers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// ========================================
// HTTP Test Helpers
// ========================================

// HTTPTestContext holds components for HTTP handler testing
type HTTPTestContext struct {
	T        *testing.T
	Recorder *httptest.ResponseRecorder
	Request  *http.Request
}

// NewHTTPTestContext creates a new HTTP test context
func NewHTTPTestContext(t *testing.T, method, path string, body io.Reader) *HTTPTestContext {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	return &HTTPTestContext{
		T:        t,
		Recorder: httptest.NewRecorder(),
		Request:  req,
	}
}

// WithHeader adds a header to the request
func (ctx *HTTPTestContext) WithHeader(key, value string) *HTTPTestContext {
	ctx.Request.Header.Set(key, value)
	return ctx
}

// WithJSONBody sets JSON body on the request
func (ctx *HTTPTestContext) WithJSONBody(v interface{}) *HTTPTestContext {
	ctx.T.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		ctx.T.Fatalf("failed to marshal JSON body: %v", err)
	}
	ctx.Request = httptest.NewRequest(ctx.Request.Method, ctx.Request.URL.String(), bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx
}

// WithAPIKey adds X-API-Key header
func (ctx *HTTPTestContext) WithAPIKey(key string) *HTTPTestContext {
	return ctx.WithHeader("X-API-Key", key)
}

// WithBearerToken adds Authorization Bearer header
func (ctx *HTTPTestContext) WithBearerToken(token string) *HTTPTestContext {
	return ctx.WithHeader("Authorization", "Bearer "+token)
}

// Execute runs the handler and returns the response
func (ctx *HTTPTestContext) Execute(handler http.Handler) *HTTPTestContext {
	handler.ServeHTTP(ctx.Recorder, ctx.Request)
	return ctx
}

// ExecuteFunc runs the handler func and returns the response
func (ctx *HTTPTestContext) ExecuteFunc(handler http.HandlerFunc) *HTTPTestContext {
	handler(ctx.Recorder, ctx.Request)
	return ctx
}

// AssertStatus checks the response status code
func (ctx *HTTPTestContext) AssertStatus(expected int) *HTTPTestContext {
	ctx.T.Helper()
	if ctx.Recorder.Code != expected {
		ctx.T.Errorf("expected status %d, got %d. Body: %s", expected, ctx.Recorder.Code, ctx.Recorder.Body.String())
	}
	return ctx
}

// AssertBodyContains checks if response body contains substring
func (ctx *HTTPTestContext) AssertBodyContains(substr string) *HTTPTestContext {
	ctx.T.Helper()
	body := ctx.Recorder.Body.String()
	if !containsString(body, substr) {
		ctx.T.Errorf("expected body to contain %q, got: %s", substr, body)
	}
	return ctx
}

// AssertHeader checks response header value
func (ctx *HTTPTestContext) AssertHeader(key, expected string) *HTTPTestContext {
	ctx.T.Helper()
	got := ctx.Recorder.Header().Get(key)
	if got != expected {
		ctx.T.Errorf("expected header %s=%q, got %q", key, expected, got)
	}
	return ctx
}

// DecodeJSON decodes response body as JSON
func (ctx *HTTPTestContext) DecodeJSON(v interface{}) *HTTPTestContext {
	ctx.T.Helper()
	if err := json.NewDecoder(ctx.Recorder.Body).Decode(v); err != nil {
		ctx.T.Fatalf("failed to decode JSON response: %v", err)
	}
	return ctx
}

// ========================================
// Mock Alert Adapter
// ========================================

// MockAlertAdapter implements alerts.AlertAdapter for testing
type MockAlertAdapter struct {
	SourceType         string
	ParsedAlerts       []alerts.NormalizedAlert
	ParseError         error
	ValidateSecretErr  error
	DefaultMappings    database.JSONB
	ParsePayloadCalled bool
	ValidateSecretCalled bool
}

// NewMockAlertAdapter creates a new mock adapter
func NewMockAlertAdapter(sourceType string) *MockAlertAdapter {
	return &MockAlertAdapter{
		SourceType:      sourceType,
		ParsedAlerts:    []alerts.NormalizedAlert{},
		DefaultMappings: database.JSONB{},
	}
}

// GetSourceType returns the source type
func (m *MockAlertAdapter) GetSourceType() string {
	return m.SourceType
}

// ParsePayload parses the alert payload
func (m *MockAlertAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	m.ParsePayloadCalled = true
	if m.ParseError != nil {
		return nil, m.ParseError
	}
	return m.ParsedAlerts, nil
}

// ValidateWebhookSecret validates the webhook secret
func (m *MockAlertAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	m.ValidateSecretCalled = true
	return m.ValidateSecretErr
}

// GetDefaultMappings returns default field mappings
func (m *MockAlertAdapter) GetDefaultMappings() database.JSONB {
	return m.DefaultMappings
}

// WithAlerts configures alerts to return from ParsePayload
func (m *MockAlertAdapter) WithAlerts(alerts ...alerts.NormalizedAlert) *MockAlertAdapter {
	m.ParsedAlerts = alerts
	return m
}

// WithParseError configures ParsePayload to return an error
func (m *MockAlertAdapter) WithParseError(err error) *MockAlertAdapter {
	m.ParseError = err
	return m
}

// WithValidationError configures ValidateWebhookSecret to return an error
func (m *MockAlertAdapter) WithValidationError(err error) *MockAlertAdapter {
	m.ValidateSecretErr = err
	return m
}

// ========================================
// Test Fixture Helpers
// ========================================

// LoadFixture loads a test fixture file from tests/fixtures/
func LoadFixture(t *testing.T, path string) []byte {
	t.Helper()
	
	// Try both relative and absolute paths
	paths := []string{
		filepath.Join("tests", "fixtures", path),
		filepath.Join("..", "..", "tests", "fixtures", path),
		filepath.Join("..", "..", "..", "tests", "fixtures", path),
	}
	
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return data
		}
	}
	
	t.Fatalf("failed to load fixture %s", path)
	return nil
}

// LoadJSONFixture loads and unmarshals a JSON fixture
func LoadJSONFixture(t *testing.T, path string, v interface{}) {
	t.Helper()
	data := LoadFixture(t, path)
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("failed to unmarshal fixture %s: %v", path, err)
	}
}

// ========================================
// Sample Data Builders
// ========================================

// NormalizedAlertBuilder builds NormalizedAlert instances for testing
type NormalizedAlertBuilder struct {
	alert alerts.NormalizedAlert
}

// NewAlertBuilder creates a new alert builder with defaults
func NewAlertBuilder() *NormalizedAlertBuilder {
	now := time.Now()
	return &NormalizedAlertBuilder{
		alert: alerts.NormalizedAlert{
			AlertName:    "TestAlert",
			Severity:     database.AlertSeverityWarning,
			Status:       database.AlertStatusFiring,
			Summary:      "Test alert summary",
			Description:  "Test alert description",
			TargetLabels: map[string]string{},
			StartedAt:    &now,
		},
	}
}

// WithName sets the alert name
func (b *NormalizedAlertBuilder) WithName(name string) *NormalizedAlertBuilder {
	b.alert.AlertName = name
	return b
}

// WithSeverity sets the severity
func (b *NormalizedAlertBuilder) WithSeverity(severity database.AlertSeverity) *NormalizedAlertBuilder {
	b.alert.Severity = severity
	return b
}

// WithStatus sets the status
func (b *NormalizedAlertBuilder) WithStatus(status database.AlertStatus) *NormalizedAlertBuilder {
	b.alert.Status = status
	return b
}

// WithHost sets the target host
func (b *NormalizedAlertBuilder) WithHost(host string) *NormalizedAlertBuilder {
	b.alert.TargetHost = host
	return b
}

// WithService sets the target service
func (b *NormalizedAlertBuilder) WithService(service string) *NormalizedAlertBuilder {
	b.alert.TargetService = service
	return b
}

// WithLabel adds a label to TargetLabels
func (b *NormalizedAlertBuilder) WithLabel(key, value string) *NormalizedAlertBuilder {
	if b.alert.TargetLabels == nil {
		b.alert.TargetLabels = map[string]string{}
	}
	b.alert.TargetLabels[key] = value
	return b
}

// WithSummary sets the summary
func (b *NormalizedAlertBuilder) WithSummary(summary string) *NormalizedAlertBuilder {
	b.alert.Summary = summary
	return b
}

// Build returns the constructed alert
func (b *NormalizedAlertBuilder) Build() alerts.NormalizedAlert {
	return b.alert
}

// IncidentBuilder builds Incident instances for testing
type IncidentBuilder struct {
	incident database.Incident
}

// NewIncidentBuilder creates a new incident builder
func NewIncidentBuilder() *IncidentBuilder {
	return &IncidentBuilder{
		incident: database.Incident{
			UUID:      "test-incident-" + time.Now().Format("20060102150405"),
			Title:     "Test Incident",
			Status:    database.IncidentStatusPending,
			Source:    "test",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

// WithID sets the incident ID
func (b *IncidentBuilder) WithID(id uint) *IncidentBuilder {
	b.incident.ID = id
	return b
}

// WithUUID sets the incident UUID
func (b *IncidentBuilder) WithUUID(uuid string) *IncidentBuilder {
	b.incident.UUID = uuid
	return b
}

// WithTitle sets the title
func (b *IncidentBuilder) WithTitle(title string) *IncidentBuilder {
	b.incident.Title = title
	return b
}

// WithStatus sets the status
func (b *IncidentBuilder) WithStatus(status database.IncidentStatus) *IncidentBuilder {
	b.incident.Status = status
	return b
}

// WithSource sets the source
func (b *IncidentBuilder) WithSource(source string) *IncidentBuilder {
	b.incident.Source = source
	return b
}

// Build returns the constructed incident
func (b *IncidentBuilder) Build() database.Incident {
	return b.incident
}

// ========================================
// Assertion Helpers
// ========================================

// AssertEqual checks equality with a helpful error message
func AssertEqual(t *testing.T, expected, actual interface{}, msg string) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %v, got %v", msg, expected, actual)
	}
}

// AssertNil checks that value is nil
func AssertNil(t *testing.T, v interface{}, msg string) {
	t.Helper()
	if v != nil {
		t.Errorf("%s: expected nil, got %v", msg, v)
	}
}

// AssertNotNil checks that value is not nil
func AssertNotNil(t *testing.T, v interface{}, msg string) {
	t.Helper()
	if v == nil {
		t.Errorf("%s: expected non-nil value", msg)
	}
}

// AssertError checks that an error occurred
func AssertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error, got nil", msg)
	}
}

// AssertNoError checks that no error occurred
func AssertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: unexpected error: %v", msg, err)
	}
}

// AssertContains checks if string contains substring
func AssertContains(t *testing.T, s, substr string, msg string) {
	t.Helper()
	if !containsString(s, substr) {
		t.Errorf("%s: expected %q to contain %q", msg, s, substr)
	}
}

// ========================================
// Timing Helpers
// ========================================

// MustCompleteWithin fails the test if the function takes longer than the timeout
func MustCompleteWithin(t *testing.T, timeout time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	
	select {
	case <-done:
		return
	case <-time.After(timeout):
		t.Fatalf("function did not complete within %v", timeout)
	}
}

// ========================================
// Internal helpers
// ========================================

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
