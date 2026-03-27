package grafana

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

// newTestTool creates a GrafanaTool with an httptest server's URL pre-populated in the config cache.
// Returns the tool, the test server, and a request counter.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*GrafanaTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewGrafanaTool(testLogger(), nil)
	config := &GrafanaConfig{
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

func TestNewGrafanaTool(t *testing.T) {
	logger := testLogger()
	tool := NewGrafanaTool(logger, nil)

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

func TestNewGrafanaTool_WithRateLimiter(t *testing.T) {
	logger := testLogger()
	limiter := ratelimit.New(10, 20)
	tool := NewGrafanaTool(logger, limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	logger := testLogger()
	tool := NewGrafanaTool(logger, nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:grafana"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"query": []string{"cpu"}}
	params2 := url.Values{"query": []string{"memory"}}

	key1 := responseCacheKey("/api/search", params1)
	key2 := responseCacheKey("/api/search", params2)
	key3 := responseCacheKey("/api/search", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

func TestResponseCacheKey_DifferentPaths(t *testing.T) {
	params := url.Values{"query": []string{"test"}}
	key1 := responseCacheKey("/api/search", params)
	key2 := responseCacheKey("/api/dashboards/uid/abc", params)

	if key1 == key2 {
		t.Error("different paths should produce different keys")
	}
}

// --- getConfig tests ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	expected := &GrafanaConfig{
		URL:       "https://grafana.example.com",
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
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	expected := &GrafanaConfig{
		URL:       "https://grafana-prod.example.com",
		APIToken:  "prod-token",
		VerifySSL: true,
		Timeout:   30,
	}
	tool.configCache.Set("creds:logical:grafana:prod-grafana", expected)

	config, err := tool.getConfig(context.Background(), "any-incident", "prod-grafana")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
}

// --- Helper function tests ---

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod"}, "prod"},
		{"absent", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 123}, ""},
		{"nil args", nil, ""},
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

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 30},
		{-1, 30},
		{3, 5},
		{5, 5},
		{30, 30},
		{300, 300},
		{301, 300},
		{1000, 300},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input_%d", tt.input), func(t *testing.T) {
			got := clampTimeout(tt.input)
			if got != tt.want {
				t.Errorf("clampTimeout(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- doRequest tests ---

func TestDoRequest_BearerToken(t *testing.T) {
	var receivedAuth string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	config := &GrafanaConfig{
		URL:       "http://localhost", // will be overwritten by test server
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	// Use actual server URL from cache
	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config.URL = cached.(*GrafanaConfig).URL

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/health", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedAuth != "Bearer test-token" {
		t.Errorf("expected 'Bearer test-token', got %q", receivedAuth)
	}
}

func TestDoRequest_EmptyToken(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	config := &GrafanaConfig{
		URL:       "http://localhost:9999",
		APIToken:  "",
		VerifySSL: true,
		Timeout:   5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/health", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDoRequest_HTTPError(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"invalid API key"}`)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/health", nil, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to contain '401', got: %v", err)
	}
}

func TestDoRequest_QueryParams(t *testing.T) {
	var receivedURL string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	params := url.Values{"query": []string{"cpu"}, "type": []string{"dash-db"}}
	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/search", params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedURL, "query=cpu") {
		t.Errorf("expected query param in URL, got %s", receivedURL)
	}
	if !strings.Contains(receivedURL, "type=dash-db") {
		t.Errorf("expected type param in URL, got %s", receivedURL)
	}
}

func TestDoRequest_ContentTypeOnBody(t *testing.T) {
	var receivedContentType string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	body := strings.NewReader(`{"text":"test annotation"}`)
	_, err := tool.doRequest(context.Background(), config, http.MethodPost, "/api/annotations", nil, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected 'application/json', got %q", receivedContentType)
	}
}

func TestDoRequest_NoContentTypeWithoutBody(t *testing.T) {
	var receivedContentType string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/search", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedContentType != "" {
		t.Errorf("expected empty Content-Type, got %q", receivedContentType)
	}
}

func TestDoRequest_WithRateLimiter(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})
	tool.rateLimiter = ratelimit.New(100, 100)

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/health", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

func TestDoRequest_ErrorTruncation(t *testing.T) {
	longMessage := strings.Repeat("x", 1000)
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, longMessage)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/health", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Error("expected truncated error message for long responses")
	}
}

// --- cachedGet tests ---

func TestCachedGet_CacheHit(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"abc"}]`)
	})

	ctx := context.Background()

	// First call - cache miss
	result1, err := tool.cachedGet(ctx, "test-incident", "/api/search", nil, DashboardCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(result1), "abc") {
		t.Error("expected response to contain 'abc'")
	}

	// Second call - should be cache hit
	result2, err := tool.cachedGet(ctx, "test-incident", "/api/search", nil, DashboardCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result1) != string(result2) {
		t.Error("cached result should match original")
	}

	// Only 1 actual HTTP request should have been made
	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit on second), got %d", counter.Load())
	}
}

func TestCachedGet_DifferentPathsDifferentCache(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"path":"%s"}`, r.URL.Path)
	})

	ctx := context.Background()

	_, err := tool.cachedGet(ctx, "test-incident", "/api/search", nil, DashboardCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.cachedGet(ctx, "test-incident", "/api/dashboards/uid/abc", nil, DashboardCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests for different paths, got %d", counter.Load())
	}
}

func TestCachedGet_LogicalNameCacheKey(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	// Also populate config cache for logical name lookup
	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	tool.configCache.Set("creds:logical:grafana:prod-grafana", cached)

	ctx := context.Background()

	// Call with logical name
	_, err := tool.cachedGet(ctx, "test-incident", "/api/search", nil, DashboardCacheTTL, "prod-grafana")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Same path, same logical name - should be cache hit
	_, err = tool.cachedGet(ctx, "test-incident", "/api/search", nil, DashboardCacheTTL, "prod-grafana")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (logical name cache hit), got %d", counter.Load())
	}
}

func TestCachedGet_EmptyURL(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	config := &GrafanaConfig{
		URL:       "",
		APIToken:  "token",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	_, err := tool.cachedGet(context.Background(), "test-incident", "/api/search", nil, DashboardCacheTTL)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "URL not configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- doPost tests ---

func TestDoPost_SendsJSON(t *testing.T) {
	var receivedMethod string
	var receivedBody string
	var receivedContentType string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	})

	reqBody := map[string]interface{}{
		"text":        "test annotation",
		"dashboardId": float64(1),
	}

	result, err := tool.doPost(context.Background(), "test-incident", "/api/annotations", reqBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected application/json, got %s", receivedContentType)
	}
	if !strings.Contains(receivedBody, "test annotation") {
		t.Error("expected request body to contain 'test annotation'")
	}
	if !strings.Contains(string(result), `"id":1`) {
		t.Error("expected response to contain id")
	}
}

func TestDoPost_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	})

	ctx := context.Background()
	reqBody := map[string]interface{}{"text": "annotation"}

	_, err := tool.doPost(ctx, "test-incident", "/api/annotations", reqBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.doPost(ctx, "test-incident", "/api/annotations", reqBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both calls should hit the server (no caching for write ops)
	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests (no caching for POST), got %d", counter.Load())
	}
}

// --- Response size limit test ---

func TestDoRequest_ResponseSizeLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write > 5MB
		data := strings.Repeat("x", 6*1024*1024)
		fmt.Fprint(w, data)
	})

	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config := cached.(*GrafanaConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/search", nil, nil)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Cache expiry test ---

func TestCachedGet_CacheExpiry(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"call":%d}`, callCount.Load())
	})

	ctx := context.Background()

	// First call - cache miss
	_, err := tool.cachedGet(ctx, "test-incident", "/api/search", nil, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second call after expiry - should be cache miss again
	_, err = tool.cachedGet(ctx, "test-incident", "/api/search", nil, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 HTTP requests after cache expiry, got %d", callCount.Load())
	}
}
