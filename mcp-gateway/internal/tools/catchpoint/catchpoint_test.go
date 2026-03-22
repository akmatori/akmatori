package catchpoint

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

// --- Helper functions ---

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// newTestTool creates a CatchpointTool with an httptest server's URL pre-populated in the config cache.
// Returns the tool, the test server, and a request counter.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*CatchpointTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewCatchpointTool(testLogger(), nil)
	config := &CatchpointConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	// Pre-populate config cache so getConfig doesn't hit the database
	tool.configCache.Set(configCacheKey("test-incident"), config)

	t.Cleanup(func() {
		tool.Stop()
		server.Close()
	})

	return tool, server, counter
}

// --- Constructor and lifecycle tests ---

func TestNewCatchpointTool(t *testing.T) {
	logger := testLogger()
	tool := NewCatchpointTool(logger, nil)

	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.configCache == nil {
		t.Error("expected non-nil configCache")
	}
	if tool.responseCache == nil {
		t.Error("expected non-nil responseCache")
	}
	if tool.rateLimiter != nil {
		t.Error("expected nil rateLimiter when none provided")
	}

	tool.Stop()
}

func TestNewCatchpointTool_WithRateLimiter(t *testing.T) {
	logger := testLogger()
	limiter := ratelimit.New(10, 20)
	tool := NewCatchpointTool(logger, limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	logger := testLogger()
	tool := NewCatchpointTool(logger, nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:catchpoint"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"testIds": []string{"1,2"}}
	params2 := url.Values{"testIds": []string{"3,4"}}

	key1 := responseCacheKey("/v4/tests", params1)
	key2 := responseCacheKey("/v4/tests", params2)
	key3 := responseCacheKey("/v4/tests", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- getConfig tests ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	expected := &CatchpointConfig{
		URL:       "https://catchpoint.example.com/api",
		APIToken:  "my-token",
		VerifySSL: true,
		Timeout:   30,
	}
	tool.configCache.Set(configCacheKey("incident-1"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
	if config.APIToken != expected.APIToken {
		t.Errorf("expected APIToken %q, got %q", expected.APIToken, config.APIToken)
	}
}

func TestGetConfig_CacheHitByLogicalName(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	expected := &CatchpointConfig{
		URL:      "https://catchpoint-prod.example.com/api",
		APIToken: "prod-token",
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "catchpoint", "prod-cp"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", "prod-cp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
	if config.APIToken != expected.APIToken {
		t.Errorf("expected APIToken %q, got %q", expected.APIToken, config.APIToken)
	}
}

// --- Timeout clamping tests ---

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero is clamped to default", 0, 30},
		{"negative is clamped to default", -5, 30},
		{"just below minimum is clamped to 5", 1, 5},
		{"near minimum is clamped to 5", 4, 5},
		{"excessive is clamped to max", 999, 300},
		{"301 is clamped to max", 301, 300},
		{"valid timeout 5 is kept", 5, 5},
		{"valid timeout 30 is kept", 30, 30},
		{"valid timeout 300 is kept", 300, 300},
		{"valid timeout 60 is kept", 60, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampTimeout(tt.input)
			if got != tt.want {
				t.Errorf("clampTimeout(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- doRequest tests ---

func TestDoRequest_BearerTokenAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": "ok"}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{
		URL:      server.URL,
		APIToken: "test-token-123",
		Timeout:  5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Bearer test-token-123"
	if receivedAuth != expected {
		t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
	}
}

func TestDoRequest_EmptyToken(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{
		URL:      "http://localhost:9999",
		APIToken: "",
		Timeout:  5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty API token")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("expected error about missing token, got: %v", err)
	}
}

func TestDoRequest_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{URL: server.URL, APIToken: "token", Timeout: 5}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status code, got %q", err.Error())
	}
}

func TestDoRequest_ResponseSizeLimit(t *testing.T) {
	// Create a server that returns a response larger than 5MB
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write 6MB of data
		data := make([]byte, 6*1024*1024)
		for i := range data {
			data[i] = 'x'
		}
		w.Write(data) //nolint:errcheck
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{URL: server.URL, APIToken: "token", Timeout: 10}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}

func TestDoRequest_ProxyDisabledExplicitly(t *testing.T) {
	var receivedReq bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedReq = true
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{
		URL:      server.URL,
		APIToken: "token",
		Timeout:  5,
		UseProxy: false,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receivedReq {
		t.Error("expected request to reach server without proxy")
	}
}

func TestDoRequest_SSLVerificationDisabled(t *testing.T) {
	// Just verify the request succeeds with VerifySSL=false against an HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{
		URL:       server.URL,
		APIToken:  "token",
		VerifySSL: false,
		Timeout:   5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_RateLimiting(t *testing.T) {
	callCount := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	limiter := ratelimit.New(100, 100) // High limit to avoid blocking in test
	tool := NewCatchpointTool(testLogger(), limiter)
	defer tool.Stop()

	config := &CatchpointConfig{URL: server.URL, APIToken: "token", Timeout: 5}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestDoRequest_RateLimitCancelledContext(t *testing.T) {
	limiter := ratelimit.New(1, 1) // Very restrictive
	tool := NewCatchpointTool(testLogger(), limiter)
	defer tool.Stop()

	// Exhaust the limiter
	ctx := context.Background()
	config := &CatchpointConfig{URL: "http://localhost:9999", APIToken: "token", Timeout: 5}

	// Use a cancelled context
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := tool.doRequest(cancelledCtx, config, http.MethodGet, "/v4/tests", nil, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestDoRequest_QueryParams(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{URL: server.URL, APIToken: "token", Timeout: 5}
	params := url.Values{"testIds": []string{"1,2,3"}, "severity": []string{"critical"}}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/v4/tests/alerts", params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedURL, "testIds=") {
		t.Errorf("expected URL to contain 'testIds=', got %q", receivedURL)
	}
	if !strings.Contains(receivedURL, "severity=critical") {
		t.Errorf("expected URL to contain 'severity=critical', got %q", receivedURL)
	}
}

func TestDoRequest_ContentTypeForBodyRequests(t *testing.T) {
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{URL: server.URL, APIToken: "token", Timeout: 5}

	body := strings.NewReader(`{"action":"acknowledge"}`)
	_, err := tool.doRequest(context.Background(), config, http.MethodPatch, "/v4/tests/alerts", nil, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", receivedContentType)
	}
}

// --- cachedGet tests ---

func TestCachedGet_CacheMiss(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts": []}`)
	})

	result, err := tool.cachedGet(context.Background(), "test-incident", "/v4/tests/alerts", nil, AlertsCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(result), "alerts") {
		t.Errorf("expected result to contain 'alerts', got %s", result)
	}
	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

func TestCachedGet_CacheHit(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts": []}`)
	})

	// First call - cache miss
	_, err := tool.cachedGet(context.Background(), "test-incident", "/v4/tests/alerts", nil, AlertsCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call - should be cache hit
	result, err := tool.cachedGet(context.Background(), "test-incident", "/v4/tests/alerts", nil, AlertsCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error on cached call: %v", err)
	}

	if !strings.Contains(string(result), "alerts") {
		t.Errorf("expected cached result to contain 'alerts', got %s", result)
	}
	if counter.Load() != 1 {
		t.Errorf("expected only 1 request (second should be cached), got %d", counter.Load())
	}
}

func TestCachedGet_DifferentParamsNotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": "ok"}`)
	})

	params1 := url.Values{"testIds": []string{"1"}}
	params2 := url.Values{"testIds": []string{"2"}}

	_, err := tool.cachedGet(context.Background(), "test-incident", "/v4/tests", params1, InventoryCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.cachedGet(context.Background(), "test-incident", "/v4/tests", params2, InventoryCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 requests for different params, got %d", counter.Load())
	}
}

// --- Page size clamping tests ---

func TestClampPageSize(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero returns zero", 0, 0},
		{"negative returns zero", -1, 0},
		{"small value kept", 10, 10},
		{"max value kept", 100, 100},
		{"over max clamped", 101, 100},
		{"way over max clamped", 1000, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampPageSize(tt.input)
			if got != tt.want {
				t.Errorf("clampPageSize(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- extractLogicalName tests ---

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-cp"}, "prod-cp"},
		{"absent", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 123}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLogicalName(tt.args)
			if got != tt.want {
				t.Errorf("extractLogicalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Read-only tool method tests ---

func TestGetAlerts_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/tests/alerts" {
			t.Errorf("expected path /v4/tests/alerts, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts": [{"id": 1, "severity": "critical"}]}`)
	})

	result, err := tool.GetAlerts(context.Background(), "test-incident", map[string]interface{}{
		"severity": "critical",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "critical") {
		t.Errorf("expected result to contain 'critical', got %s", result)
	}
}

func TestGetAlerts_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "Unauthorized")
	})

	_, err := tool.GetAlerts(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got: %v", err)
	}
}

func TestGetAlertDetails_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v4/tests/alerts/") {
			t.Errorf("expected path prefix /v4/tests/alerts/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alert": {"id": 123}}`)
	})

	result, err := tool.GetAlertDetails(context.Background(), "test-incident", map[string]interface{}{
		"alert_ids": "123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "123") {
		t.Errorf("expected result to contain '123', got %s", result)
	}
}

func TestGetAlertDetails_MissingRequired(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetAlertDetails(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing alert_ids")
	}
	if !strings.Contains(err.Error(), "alert_ids is required") {
		t.Errorf("expected 'alert_ids is required' error, got %q", err.Error())
	}
}

func TestGetTestPerformance_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/tests/explorer/aggregated" {
			t.Errorf("expected path /v4/tests/explorer/aggregated, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("testIds") != "1,2" {
			t.Errorf("expected testIds=1,2, got %s", r.URL.Query().Get("testIds"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": {"metrics": []}}`)
	})

	result, err := tool.GetTestPerformance(context.Background(), "test-incident", map[string]interface{}{
		"test_ids": "1,2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "metrics") {
		t.Errorf("expected result to contain 'metrics', got %s", result)
	}
}

func TestGetTestPerformance_MissingRequired(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetTestPerformance(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing test_ids")
	}
	if !strings.Contains(err.Error(), "test_ids is required") {
		t.Errorf("expected 'test_ids is required' error, got %q", err.Error())
	}
}

func TestGetTestPerformanceRaw_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/tests/explorer/raw" {
			t.Errorf("expected path /v4/tests/explorer/raw, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": {"raw": []}}`)
	})

	result, err := tool.GetTestPerformanceRaw(context.Background(), "test-incident", map[string]interface{}{
		"test_ids": "5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "raw") {
		t.Errorf("expected result to contain 'raw', got %s", result)
	}
}

func TestGetTestPerformanceRaw_MissingRequired(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetTestPerformanceRaw(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing test_ids")
	}
}

func TestGetTests_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/tests" {
			t.Errorf("expected path /v4/tests, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"tests": [{"id": 1}]}`)
	})

	result, err := tool.GetTests(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "tests") {
		t.Errorf("expected result to contain 'tests', got %s", result)
	}
}

func TestGetTests_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Forbidden")
	})

	_, err := tool.GetTests(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 error, got: %v", err)
	}
}

func TestGetTestDetails_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v4/tests/") {
			t.Errorf("expected path prefix /v4/tests/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"test": {"id": 42, "name": "Homepage Check"}}`)
	})

	result, err := tool.GetTestDetails(context.Background(), "test-incident", map[string]interface{}{
		"test_ids": "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Homepage Check") {
		t.Errorf("expected result to contain 'Homepage Check', got %s", result)
	}
}

func TestGetTestDetails_MissingRequired(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetTestDetails(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing test_ids")
	}
}

func TestGetTestErrors_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/tests/errors/raw" {
			t.Errorf("expected path /v4/tests/errors/raw, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"errors": []}`)
	})

	result, err := tool.GetTestErrors(context.Background(), "test-incident", map[string]interface{}{
		"test_ids": "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "errors") {
		t.Errorf("expected result to contain 'errors', got %s", result)
	}
}

func TestGetTestErrors_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "Bad Request")
	})

	_, err := tool.GetTestErrors(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetInternetOutages_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/iw/outages" {
			t.Errorf("expected path /v4/iw/outages, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"outages": []}`)
	})

	result, err := tool.GetInternetOutages(context.Background(), "test-incident", map[string]interface{}{
		"country": "US",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "outages") {
		t.Errorf("expected result to contain 'outages', got %s", result)
	}
}

func TestGetInternetOutages_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "Service Unavailable")
	})

	_, err := tool.GetInternetOutages(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetNodes_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/nodes/all" {
			t.Errorf("expected path /v4/nodes/all, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"nodes": [{"id": 1, "name": "NYC"}]}`)
	})

	result, err := tool.GetNodes(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "NYC") {
		t.Errorf("expected result to contain 'NYC', got %s", result)
	}
}

func TestGetNodes_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	})

	_, err := tool.GetNodes(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetNodeAlerts_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/node/alerts" {
			t.Errorf("expected path /v4/node/alerts, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts": []}`)
	})

	result, err := tool.GetNodeAlerts(context.Background(), "test-incident", map[string]interface{}{
		"node_ids": "1,2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alerts") {
		t.Errorf("expected result to contain 'alerts', got %s", result)
	}
}

func TestGetNodeAlerts_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
	})

	_, err := tool.GetNodeAlerts(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Write operation tests ---

func TestAcknowledgeAlerts_Success(t *testing.T) {
	var receivedMethod string
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		if r.URL.Path != "/v4/tests/alerts" {
			t.Errorf("expected path /v4/tests/alerts, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success": true}`)
	})

	result, err := tool.AcknowledgeAlerts(context.Background(), "test-incident", map[string]interface{}{
		"alert_ids": "123,456",
		"action":    "acknowledge",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPatch {
		t.Errorf("expected PATCH method, got %s", receivedMethod)
	}
	if !strings.Contains(receivedBody, "acknowledge") {
		t.Errorf("expected body to contain 'acknowledge', got %s", receivedBody)
	}
	if !strings.Contains(result, "success") {
		t.Errorf("expected result to contain 'success', got %s", result)
	}
}

func TestAcknowledgeAlerts_MissingAlertIDs(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.AcknowledgeAlerts(context.Background(), "test", map[string]interface{}{
		"action": "acknowledge",
	})
	if err == nil {
		t.Fatal("expected error for missing alert_ids")
	}
	if !strings.Contains(err.Error(), "alert_ids is required") {
		t.Errorf("expected 'alert_ids is required', got %q", err.Error())
	}
}

func TestAcknowledgeAlerts_MissingAction(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.AcknowledgeAlerts(context.Background(), "test", map[string]interface{}{
		"alert_ids": "123",
	})
	if err == nil {
		t.Fatal("expected error for missing action")
	}
	if !strings.Contains(err.Error(), "action is required") {
		t.Errorf("expected 'action is required', got %q", err.Error())
	}
}

func TestAcknowledgeAlerts_InvalidAction(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.AcknowledgeAlerts(context.Background(), "test", map[string]interface{}{
		"alert_ids": "123",
		"action":    "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(err.Error(), "invalid action") {
		t.Errorf("expected 'invalid action', got %q", err.Error())
	}
}

func TestAcknowledgeAlerts_AssignWithoutAssignee(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.AcknowledgeAlerts(context.Background(), "test", map[string]interface{}{
		"alert_ids": "123",
		"action":    "assign",
	})
	if err == nil {
		t.Fatal("expected error for assign without assignee")
	}
	if !strings.Contains(err.Error(), "assignee is required") {
		t.Errorf("expected 'assignee is required', got %q", err.Error())
	}
}

func TestAcknowledgeAlerts_WithAssignee(t *testing.T) {
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success": true}`)
	})

	_, err := tool.AcknowledgeAlerts(context.Background(), "test-incident", map[string]interface{}{
		"alert_ids": "123",
		"action":    "assign",
		"assignee":  "user@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedBody, "user@example.com") {
		t.Errorf("expected body to contain assignee, got %s", receivedBody)
	}
}

func TestAcknowledgeAlerts_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success": true}`)
	})

	args := map[string]interface{}{
		"alert_ids": "123",
		"action":    "acknowledge",
	}

	// Call twice - both should hit the server (no caching)
	_, err := tool.AcknowledgeAlerts(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.AcknowledgeAlerts(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 requests (write ops should not be cached), got %d", counter.Load())
	}
}

func TestRunInstantTest_Success(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status": "triggered"}`)
	})

	result, err := tool.RunInstantTest(context.Background(), "test-incident", map[string]interface{}{
		"test_id": "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST method, got %s", receivedMethod)
	}
	if receivedPath != "/v4/instanttests/42" {
		t.Errorf("expected path /v4/instanttests/42, got %s", receivedPath)
	}
	if !strings.Contains(result, "triggered") {
		t.Errorf("expected result to contain 'triggered', got %s", result)
	}
}

func TestRunInstantTest_MissingTestID(t *testing.T) {
	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.RunInstantTest(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing test_id")
	}
	if !strings.Contains(err.Error(), "test_id is required") {
		t.Errorf("expected 'test_id is required', got %q", err.Error())
	}
}

func TestRunInstantTest_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status": "triggered"}`)
	})

	args := map[string]interface{}{"test_id": "42"}

	// Call twice - both should hit the server (no caching)
	_, err := tool.RunInstantTest(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.RunInstantTest(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 requests (write ops should not be cached), got %d", counter.Load())
	}
}

func TestRunInstantTest_Error(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error": "test not found"}`)
	})

	_, err := tool.RunInstantTest(context.Background(), "test-incident", map[string]interface{}{
		"test_id": "99999",
	})
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

// --- Pagination param tests ---

func TestAddPaginationParams(t *testing.T) {
	params := url.Values{}
	args := map[string]interface{}{
		"page_number": float64(2),
		"page_size":   float64(50),
	}
	addPaginationParams(params, args)

	if params.Get("pageNumber") != "2" {
		t.Errorf("expected pageNumber=2, got %q", params.Get("pageNumber"))
	}
	if params.Get("pageSize") != "50" {
		t.Errorf("expected pageSize=50, got %q", params.Get("pageSize"))
	}
}

func TestAddPaginationParams_PageSizeClamped(t *testing.T) {
	params := url.Values{}
	args := map[string]interface{}{
		"page_size": float64(200),
	}
	addPaginationParams(params, args)

	if params.Get("pageSize") != "100" {
		t.Errorf("expected pageSize clamped to 100, got %q", params.Get("pageSize"))
	}
}

func TestAddPaginationParams_NotSet(t *testing.T) {
	params := url.Values{}
	addPaginationParams(params, map[string]interface{}{})

	if params.Get("pageNumber") != "" {
		t.Error("expected pageNumber to not be set")
	}
	if params.Get("pageSize") != "" {
		t.Error("expected pageSize to not be set")
	}
}

// --- Cache verification via request counter for read methods ---

func TestGetAlerts_CacheVerification(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts": []}`)
	})

	args := map[string]interface{}{"severity": "critical"}

	// First call
	_, err := tool.GetAlerts(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call should be cached
	_, err = tool.GetAlerts(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 request (second should be cached), got %d", counter.Load())
	}
}

// --- Time params test ---

func TestAddTimeParams(t *testing.T) {
	params := url.Values{}
	args := map[string]interface{}{
		"start_time": "2024-01-01T00:00:00Z",
		"end_time":   "2024-01-02T00:00:00Z",
	}
	addTimeParams(params, args)

	if params.Get("startTime") != "2024-01-01T00:00:00Z" {
		t.Errorf("expected startTime, got %q", params.Get("startTime"))
	}
	if params.Get("endTime") != "2024-01-02T00:00:00Z" {
		t.Errorf("expected endTime, got %q", params.Get("endTime"))
	}
}

// Test that GetAlerts respects the 15s cache TTL bucket (different from inventory 60s)
func TestGetAlerts_UsesDifferentCacheFromTests(t *testing.T) {
	alertCalls := &atomic.Int32{}
	testCalls := &atomic.Int32{}

	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/tests/alerts" {
			alertCalls.Add(1)
		} else if r.URL.Path == "/v4/tests" {
			testCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": []}`)
	})

	// Call both endpoints
	_, _ = tool.GetAlerts(context.Background(), "test-incident", map[string]interface{}{})
	_, _ = tool.GetTests(context.Background(), "test-incident", map[string]interface{}{})

	// Both should have been called once (different endpoints, different cache keys)
	if alertCalls.Load() != 1 {
		t.Errorf("expected 1 alert call, got %d", alertCalls.Load())
	}
	if testCalls.Load() != 1 {
		t.Errorf("expected 1 test call, got %d", testCalls.Load())
	}

	// Call again - both should be cached
	_, _ = tool.GetAlerts(context.Background(), "test-incident", map[string]interface{}{})
	_, _ = tool.GetTests(context.Background(), "test-incident", map[string]interface{}{})

	if alertCalls.Load() != 1 {
		t.Errorf("expected alert calls still 1 (cached), got %d", alertCalls.Load())
	}
	if testCalls.Load() != 1 {
		t.Errorf("expected test calls still 1 (cached), got %d", testCalls.Load())
	}
}

// Verify all 3 valid AcknowledgeAlerts actions
func TestAcknowledgeAlerts_ValidActions(t *testing.T) {
	for _, action := range []string{"acknowledge", "assign", "drop"} {
		t.Run(action, func(t *testing.T) {
			tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success": true}`)
			})

			args := map[string]interface{}{
				"alert_ids": "1",
				"action":    action,
			}
			if action == "assign" {
				args["assignee"] = "oncall@example.com"
			}
			_, err := tool.AcknowledgeAlerts(context.Background(), "test-incident", args)
			if err != nil {
				t.Fatalf("unexpected error for action %q: %v", action, err)
			}
		})
	}
}

// TestGetTests_WithAllOptionalParams verifies optional params (test_type, folder_id, status) are sent
func TestGetTests_WithAllOptionalParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("testType") != "web" {
			t.Errorf("expected testType=web, got %q", q.Get("testType"))
		}
		if q.Get("folderId") != "42" {
			t.Errorf("expected folderId=42, got %q", q.Get("folderId"))
		}
		if q.Get("status") != "active" {
			t.Errorf("expected status=active, got %q", q.Get("status"))
		}
		if q.Get("testIds") != "1,2,3" {
			t.Errorf("expected testIds=1,2,3, got %q", q.Get("testIds"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"tests":[]}`)
	})

	_, err := tool.GetTests(context.Background(), "test-incident", map[string]interface{}{
		"test_ids":  "1,2,3",
		"test_type": "web",
		"folder_id": "42",
		"status":    "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDoRequest_WithProxyURL verifies proxy URL is set when configured
func TestDoRequest_WithProxyURL(t *testing.T) {
	// We can't easily verify the proxy was set on the transport, but we can verify
	// the request goes through without error when UseProxy is true with valid URL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	config := &CatchpointConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
		UseProxy:  true,
		ProxyURL:  "http://127.0.0.1:9999", // non-listening proxy - request will fail
	}

	// With a non-listening proxy, the request should fail (proving proxy was used)
	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error when proxy is not reachable")
	}
}

// TestDoRequest_WithInvalidProxyURL verifies invalid proxy URL is handled gracefully
func TestDoRequest_WithInvalidProxyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	config := &CatchpointConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
		UseProxy:  true,
		ProxyURL:  "://invalid-url",
	}

	// With an invalid proxy URL, it falls back to no proxy
	resp, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatalf("expected no error (should fall back to no proxy), got: %v", err)
	}
	if string(resp) != `{"ok":true}` {
		t.Errorf("unexpected response: %s", resp)
	}
}

// TestCachedGet_WithLogicalName verifies logical name cache key prefix
func TestCachedGet_WithLogicalName(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data":"from-server"}`)
	}))
	defer server.Close()

	tool := NewCatchpointTool(testLogger(), nil)
	defer tool.Stop()

	config := &CatchpointConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	// Pre-populate both possible cache key formats for logical name
	tool.configCache.Set("creds:logical:catchpoint:prod-catchpoint", config)

	params := url.Values{"testIds": {"1"}}
	_, err := tool.cachedGet(context.Background(), "test-incident", "/v4/tests", params, 30*time.Second, "prod-catchpoint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call with same logical name should be cached
	_, err = tool.cachedGet(context.Background(), "test-incident", "/v4/tests", params, 30*time.Second, "prod-catchpoint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 server call (second should be cached), got %d", callCount.Load())
	}
}

// TestGetTestPerformance_WithOptionalParams verifies optional params are passed
func TestGetTestPerformance_WithOptionalParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("metrics") != "timing,availability" {
			t.Errorf("expected metrics param, got %q", q.Get("metrics"))
		}
		if q.Get("dimensions") != "city,isp" {
			t.Errorf("expected dimensions param, got %q", q.Get("dimensions"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data":[]}`)
	})

	_, err := tool.GetTestPerformance(context.Background(), "test-incident", map[string]interface{}{
		"test_ids":   "1",
		"metrics":    "timing,availability",
		"dimensions": "city,isp",
		"start_time": "2026-01-01T00:00:00Z",
		"end_time":   "2026-01-02T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGetTestPerformanceRaw_WithOptionalParams verifies optional params
func TestGetTestPerformanceRaw_WithOptionalParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("nodeIds") != "10,20" {
			t.Errorf("expected nodeIds=10,20, got %q", q.Get("nodeIds"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data":[]}`)
	})

	_, err := tool.GetTestPerformanceRaw(context.Background(), "test-incident", map[string]interface{}{
		"test_ids": "1",
		"node_ids": "10,20",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGetAlerts_WithOptionalParams verifies alert filter params
func TestGetAlerts_WithOptionalParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("severity") != "critical" {
			t.Errorf("expected severity=critical, got %q", q.Get("severity"))
		}
		if q.Get("testIds") != "5,6" {
			t.Errorf("expected testIds=5,6, got %q", q.Get("testIds"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts":[]}`)
	})

	_, err := tool.GetAlerts(context.Background(), "test-incident", map[string]interface{}{
		"severity":   "critical",
		"test_ids":   "5,6",
		"start_time": "2026-01-01",
		"end_time":   "2026-01-02",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGetInternetOutages_WithOptionalParams verifies outage filter params
func TestGetInternetOutages_WithOptionalParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("asn") != "15169" {
			t.Errorf("expected asn=15169, got %q", q.Get("asn"))
		}
		if q.Get("country") != "US" {
			t.Errorf("expected country=US, got %q", q.Get("country"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"outages":[]}`)
	})

	_, err := tool.GetInternetOutages(context.Background(), "test-incident", map[string]interface{}{
		"asn":     "15169",
		"country": "US",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEscapeCSVPathSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single ID", "123", "123"},
		{"multiple IDs", "123,456,789", "123,456,789"},
		{"whitespace trimmed", " 123 , 456 ", "123,456"},
		{"path traversal escaped", "123/../456", "123%2F..%2F456"},
		{"special chars escaped", "a b/c", "a%20b%2Fc"},
		{"empty string", "", ""},
		{"single comma", ",", ","},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeCSVPathSegment(tt.input)
			if got != tt.want {
				t.Errorf("escapeCSVPathSegment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
