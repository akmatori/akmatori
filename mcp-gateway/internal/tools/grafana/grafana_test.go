package grafana

import (
	"context"
	"encoding/json"
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

func TestDoPost_EmptyURL(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	// Pre-populate config cache with empty URL
	config := &GrafanaConfig{
		URL:       "",
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   30,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	_, err := tool.doPost(context.Background(), "test-incident", "/api/annotations", map[string]interface{}{
		"text": "test",
	})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "Grafana URL not configured") {
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

// --- SearchDashboards tests ---

func TestSearchDashboards_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			t.Errorf("expected path /api/search, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"abc","title":"CPU Dashboard","type":"dash-db"}]`)
	})

	result, err := tool.SearchDashboards(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "CPU Dashboard") {
		t.Errorf("expected result to contain 'CPU Dashboard', got %s", result)
	}
}

func TestSearchDashboards_WithQueryAndTag(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "cpu" {
			t.Errorf("expected query=cpu, got %s", q.Get("query"))
		}
		if q.Get("tag") != "production" {
			t.Errorf("expected tag=production, got %s", q.Get("tag"))
		}
		if q.Get("type") != "dash-db" {
			t.Errorf("expected type=dash-db, got %s", q.Get("type"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.SearchDashboards(context.Background(), "test-incident", map[string]interface{}{
		"query": "cpu",
		"tag":   "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchDashboards_WithTypeOverride(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "dash-folder" {
			t.Errorf("expected type=dash-folder, got %s", r.URL.Query().Get("type"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.SearchDashboards(context.Background(), "test-incident", map[string]interface{}{
		"type": "dash-folder",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchDashboards_WithFolderIDAndLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("folderIds") != "42" {
			t.Errorf("expected folderIds=42, got %s", q.Get("folderIds"))
		}
		if q.Get("limit") != "10" {
			t.Errorf("expected limit=10, got %s", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.SearchDashboards(context.Background(), "test-incident", map[string]interface{}{
		"folder_id": float64(42),
		"limit":     float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchDashboards_LimitClamped(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "5000" {
			t.Errorf("expected limit clamped to 5000, got %s", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.SearchDashboards(context.Background(), "test-incident", map[string]interface{}{
		"limit": float64(99999),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchDashboards_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"abc"}]`)
	})

	ctx := context.Background()
	args := map[string]interface{}{"query": "cpu"}

	_, err := tool.SearchDashboards(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.SearchDashboards(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit on second), got %d", counter.Load())
	}
}

// --- GetDashboardByUID tests ---

func TestGetDashboardByUID_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dashboards/uid/abc123" {
			t.Errorf("expected path /api/dashboards/uid/abc123, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"dashboard":{"uid":"abc123","title":"My Dashboard"},"meta":{"slug":"my-dashboard"}}`)
	})

	result, err := tool.GetDashboardByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "My Dashboard") {
		t.Errorf("expected result to contain 'My Dashboard', got %s", result)
	}
}

func TestGetDashboardByUID_MissingUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetDashboardByUID(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing uid")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetDashboardByUID_EmptyUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetDashboardByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "",
	})
	if err == nil {
		t.Fatal("expected error for empty uid")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetDashboardByUID_SpecialCharsInUID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		// url.PathEscape encodes slashes and special chars
		if !strings.HasPrefix(r.URL.Path, "/api/dashboards/uid/") {
			t.Errorf("expected path to start with /api/dashboards/uid/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"dashboard":{"uid":"a/b","title":"Test"}}`)
	})

	_, err := tool.GetDashboardByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "a/b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetDashboardByUID_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"dashboard":{"uid":"abc"}}`)
	})

	ctx := context.Background()
	args := map[string]interface{}{"uid": "abc"}

	_, err := tool.GetDashboardByUID(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.GetDashboardByUID(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}

// --- GetDashboardPanels tests ---

func TestGetDashboardPanels_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"dashboard":{"panels":[{"id":1,"title":"CPU Usage","type":"graph","datasource":"Prometheus"},{"id":2,"title":"Memory","type":"stat","datasource":"InfluxDB"}]}}`)
	})

	result, err := tool.GetDashboardPanels(context.Background(), "test-incident", map[string]interface{}{
		"uid": "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "CPU Usage") {
		t.Errorf("expected 'CPU Usage' in result, got %s", result)
	}
	if !strings.Contains(result, "Memory") {
		t.Errorf("expected 'Memory' in result, got %s", result)
	}
	if !strings.Contains(result, "graph") {
		t.Errorf("expected panel type 'graph' in result, got %s", result)
	}
}

func TestGetDashboardPanels_MissingUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetDashboardPanels(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing uid")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetDashboardPanels_NoPanels(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"dashboard":{"panels":[]}}`)
	})

	result, err := tool.GetDashboardPanels(context.Background(), "test-incident", map[string]interface{}{
		"uid": "empty-dash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return null or empty array JSON
	if result != "null" && result != "[]" {
		t.Errorf("expected null or [] for empty panels, got %s", result)
	}
}

func TestGetDashboardPanels_NestedRowPanels(t *testing.T) {
	dashJSON := `{
		"dashboard": {
			"panels": [
				{
					"id": 1,
					"title": "Row",
					"type": "row",
					"panels": [
						{"id": 2, "title": "Nested CPU", "type": "graph", "datasource": "Prometheus"},
						{"id": 3, "title": "Nested Mem", "type": "stat", "datasource": "Prometheus"}
					]
				},
				{"id": 4, "title": "Top Level", "type": "gauge"}
			]
		}
	}`
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, dashJSON)
	})

	result, err := tool.GetDashboardPanels(context.Background(), "test-incident", map[string]interface{}{
		"uid": "row-dash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain both the row panel and its nested panels, plus top level
	if !strings.Contains(result, "Row") {
		t.Error("expected 'Row' panel in result")
	}
	if !strings.Contains(result, "Nested CPU") {
		t.Error("expected 'Nested CPU' in result")
	}
	if !strings.Contains(result, "Nested Mem") {
		t.Error("expected 'Nested Mem' in result")
	}
	if !strings.Contains(result, "Top Level") {
		t.Error("expected 'Top Level' in result")
	}

	// Verify we get 4 panels total (row + 2 nested + top level)
	var panels []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &panels); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(panels) != 4 {
		t.Errorf("expected 4 panels, got %d", len(panels))
	}
}

func TestGetDashboardPanels_InvalidDashboardJSON(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `not-json`)
	})

	_, err := tool.GetDashboardPanels(context.Background(), "test-incident", map[string]interface{}{
		"uid": "bad-dash",
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "failed to parse dashboard") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- GetAlertRules tests ---

func TestGetAlertRules_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/provisioning/alert-rules" {
			t.Errorf("expected path /api/v1/provisioning/alert-rules, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"rule-1","title":"HighCPU","condition":"A","data":[]}]`)
	})

	result, err := tool.GetAlertRules(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "HighCPU") {
		t.Errorf("expected result to contain 'HighCPU', got %s", result)
	}
	if !strings.Contains(result, "rule-1") {
		t.Errorf("expected result to contain 'rule-1', got %s", result)
	}
}

func TestGetAlertRules_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"rule-1"}]`)
	})

	ctx := context.Background()
	args := map[string]interface{}{}

	_, err := tool.GetAlertRules(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.GetAlertRules(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}

func TestGetAlertRules_WithLogicalName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"uid":"rule-1"}]`)
	})

	// Pre-populate logical name config
	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	tool.configCache.Set("creds:logical:grafana:prod-grafana", cached)

	result, err := tool.GetAlertRules(context.Background(), "test-incident", map[string]interface{}{
		"logical_name": "prod-grafana",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "rule-1") {
		t.Errorf("expected result to contain 'rule-1', got %s", result)
	}
}

// --- GetAlertInstances tests ---

func TestGetAlertInstances_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/alertmanager/grafana/api/v2/alerts" {
			t.Errorf("expected path /api/alertmanager/grafana/api/v2/alerts, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"labels":{"alertname":"HighCPU"},"status":{"state":"active"}}]`)
	})

	result, err := tool.GetAlertInstances(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "HighCPU") {
		t.Errorf("expected result to contain 'HighCPU', got %s", result)
	}
	if !strings.Contains(result, "active") {
		t.Errorf("expected result to contain 'active', got %s", result)
	}
}

func TestGetAlertInstances_WithFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filter") != `alertname="HighCPU"` {
			t.Errorf("expected filter param, got %s", q.Get("filter"))
		}
		if q.Get("silenced") != "false" {
			t.Errorf("expected silenced=false, got %s", q.Get("silenced"))
		}
		if q.Get("active") != "true" {
			t.Errorf("expected active=true, got %s", q.Get("active"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.GetAlertInstances(context.Background(), "test-incident", map[string]interface{}{
		"filter":   `alertname="HighCPU"`,
		"silenced": false,
		"active":   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetAlertInstances_WithInhibited(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("inhibited") != "true" {
			t.Errorf("expected inhibited=true, got %s", q.Get("inhibited"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.GetAlertInstances(context.Background(), "test-incident", map[string]interface{}{
		"inhibited": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetAlertInstances_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"labels":{"alertname":"test"}}]`)
	})

	ctx := context.Background()
	args := map[string]interface{}{}

	_, err := tool.GetAlertInstances(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.GetAlertInstances(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}

// --- GetAlertRuleByUID tests ---

func TestGetAlertRuleByUID_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/provisioning/alert-rules/rule-abc" {
			t.Errorf("expected path /api/v1/provisioning/alert-rules/rule-abc, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"uid":"rule-abc","title":"HighMemory","condition":"B","for":"5m"}`)
	})

	result, err := tool.GetAlertRuleByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "rule-abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "HighMemory") {
		t.Errorf("expected result to contain 'HighMemory', got %s", result)
	}
	if !strings.Contains(result, "rule-abc") {
		t.Errorf("expected result to contain 'rule-abc', got %s", result)
	}
}

func TestGetAlertRuleByUID_MissingUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetAlertRuleByUID(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing uid")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAlertRuleByUID_EmptyUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetAlertRuleByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "",
	})
	if err == nil {
		t.Fatal("expected error for empty uid")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAlertRuleByUID_SpecialCharsInUID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/provisioning/alert-rules/") {
			t.Errorf("expected path prefix /api/v1/provisioning/alert-rules/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"uid":"a/b","title":"Test Rule"}`)
	})

	_, err := tool.GetAlertRuleByUID(context.Background(), "test-incident", map[string]interface{}{
		"uid": "a/b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetAlertRuleByUID_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"uid":"rule-1","title":"Test"}`)
	})

	ctx := context.Background()
	args := map[string]interface{}{"uid": "rule-1"}

	_, err := tool.GetAlertRuleByUID(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.GetAlertRuleByUID(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}

// --- SilenceAlert tests ---

func TestSilenceAlert_Success(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"silenceID":"silence-123"}`)
	})

	result, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers": []interface{}{
			map[string]interface{}{"name": "alertname", "value": "HighCPU", "isRegex": false, "isEqual": true},
		},
		"starts_at":  "2026-03-27T00:00:00Z",
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "akmatori-agent",
		"comment":    "Silencing during maintenance",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/api/alertmanager/grafana/api/v2/silences" {
		t.Errorf("expected silences path, got %s", receivedPath)
	}
	if !strings.Contains(string(result), "silence-123") {
		t.Errorf("expected result to contain silence ID, got %s", result)
	}

	// Verify body fields
	if receivedBody["createdBy"] != "akmatori-agent" {
		t.Errorf("expected createdBy=akmatori-agent, got %v", receivedBody["createdBy"])
	}
	if receivedBody["comment"] != "Silencing during maintenance" {
		t.Errorf("expected comment, got %v", receivedBody["comment"])
	}
	if receivedBody["startsAt"] != "2026-03-27T00:00:00Z" {
		t.Errorf("expected startsAt=2026-03-27T00:00:00Z, got %v", receivedBody["startsAt"])
	}
	if receivedBody["endsAt"] != "2026-03-28T00:00:00Z" {
		t.Errorf("expected endsAt=2026-03-28T00:00:00Z, got %v", receivedBody["endsAt"])
	}
	matchers, ok := receivedBody["matchers"].([]interface{})
	if !ok || len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %v", receivedBody["matchers"])
	}
	matcher, ok := matchers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected matcher to be a map, got %T", matchers[0])
	}
	if matcher["name"] != "alertname" || matcher["value"] != "HighCPU" {
		t.Errorf("unexpected matcher: %v", matcher)
	}
}

func TestSilenceAlert_InvalidStartsAt(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "not-a-timestamp",
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid starts_at")
	}
	if !strings.Contains(err.Error(), "starts_at must be a valid RFC3339") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_InvalidEndsAt(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "2026-03-27T00:00:00Z",
		"ends_at":    "invalid",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid ends_at")
	}
	if !strings.Contains(err.Error(), "ends_at must be a valid RFC3339") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_EndsAtBeforeStartsAt(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "2026-03-28T00:00:00Z",
		"ends_at":    "2026-03-27T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error when ends_at is before starts_at")
	}
	if !strings.Contains(err.Error(), "ends_at must be after starts_at") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_MissingMatchers(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"starts_at":  "2026-03-27T00:00:00Z",
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error for missing matchers")
	}
	if !strings.Contains(err.Error(), "matchers is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_MissingStartsAt(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error for missing starts_at")
	}
	if !strings.Contains(err.Error(), "starts_at is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_MissingEndsAt(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "2026-03-27T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	})
	if err == nil {
		t.Fatal("expected error for missing ends_at")
	}
	if !strings.Contains(err.Error(), "ends_at is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_MissingCreatedBy(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":  []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at": "2026-03-27T00:00:00Z",
		"ends_at":   "2026-03-28T00:00:00Z",
		"comment":   "test",
	})
	if err == nil {
		t.Fatal("expected error for missing created_by")
	}
	if !strings.Contains(err.Error(), "created_by is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_MissingComment(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.SilenceAlert(context.Background(), "test-incident", map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "2026-03-27T00:00:00Z",
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "agent",
	})
	if err == nil {
		t.Fatal("expected error for missing comment")
	}
	if !strings.Contains(err.Error(), "comment is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSilenceAlert_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"silenceID":"s1"}`)
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"matchers":   []interface{}{map[string]interface{}{"name": "alertname", "value": "test"}},
		"starts_at":  "2026-03-27T00:00:00Z",
		"ends_at":    "2026-03-28T00:00:00Z",
		"created_by": "agent",
		"comment":    "test",
	}

	_, err := tool.SilenceAlert(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.SilenceAlert(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both calls should hit the server (write op, no caching)
	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests (no caching for POST), got %d", counter.Load())
	}
}

// --- ListDataSources tests ---

func TestListDataSources_Success(t *testing.T) {
	var receivedPath string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"id":1,"uid":"prom-1","name":"Prometheus","type":"prometheus","url":"http://prometheus:9090"},{"id":2,"uid":"loki-1","name":"Loki","type":"loki","url":"http://loki:3100"}]`)
	})

	result, err := tool.ListDataSources(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedPath != "/api/datasources" {
		t.Errorf("expected /api/datasources, got %s", receivedPath)
	}
	if !strings.Contains(result, "prom-1") {
		t.Error("expected result to contain prometheus datasource")
	}
	if !strings.Contains(result, "loki-1") {
		t.Error("expected result to contain loki datasource")
	}
}

func TestListDataSources_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"id":1,"uid":"prom-1"}]`)
	})

	ctx := context.Background()
	args := map[string]interface{}{}

	_, err := tool.ListDataSources(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.ListDataSources(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}

func TestListDataSources_WithLogicalName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"id":1,"uid":"prom-1"}]`)
	})

	// Set up logical name config
	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	tool.configCache.Set("creds:logical:grafana:prod-grafana", cached)

	result, err := tool.ListDataSources(context.Background(), "test-incident", map[string]interface{}{
		"logical_name": "prod-grafana",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "prom-1") {
		t.Error("expected result to contain datasource")
	}
}

// --- QueryDataSource tests ---

func TestQueryDataSource_Success(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[{"data":{"values":[[1,2,3]]}}]}}}`)
	})

	result, err := tool.QueryDataSource(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
		"queries": []interface{}{
			map[string]interface{}{
				"refId":      "A",
				"datasource": map[string]interface{}{"uid": "prom-1", "type": "prometheus"},
				"expr":       "up",
			},
		},
		"from": "now-1h",
		"to":   "now",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/api/ds/query" {
		t.Errorf("expected /api/ds/query, got %s", receivedPath)
	}
	if !strings.Contains(result, "frames") {
		t.Error("expected result to contain query results")
	}
	if receivedBody["from"] != "now-1h" {
		t.Errorf("expected from=now-1h, got %v", receivedBody["from"])
	}
	if receivedBody["to"] != "now" {
		t.Errorf("expected to=now, got %v", receivedBody["to"])
	}
}

func TestQueryDataSource_InjectsDatasourceUID(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{}}`)
	})

	// Query without datasource field - should get injected
	_, err := tool.QueryDataSource(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
		"queries": []interface{}{
			map[string]interface{}{
				"refId": "A",
				"expr":  "up",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	ds, ok := q["datasource"].(map[string]interface{})
	if !ok {
		t.Fatal("expected datasource to be injected into query")
	}
	if ds["uid"] != "prom-1" {
		t.Errorf("expected injected datasource uid=prom-1, got %v", ds["uid"])
	}
}

func TestQueryDataSource_OverridesEmbeddedDatasource(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{}}`)
	})

	// Query with explicit datasource field - MUST be overridden by top-level datasource_uid
	_, err := tool.QueryDataSource(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
		"queries": []interface{}{
			map[string]interface{}{
				"refId":      "A",
				"datasource": map[string]interface{}{"uid": "other-ds", "type": "prometheus"},
				"expr":       "up",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	ds := q["datasource"].(map[string]interface{})
	if ds["uid"] != "prom-1" {
		t.Errorf("expected embedded datasource uid to be overridden to prom-1, got %v", ds["uid"])
	}
}

func TestQueryDataSource_MissingDatasourceUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryDataSource(context.Background(), "test-incident", map[string]interface{}{
		"queries": []interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for missing datasource_uid")
	}
	if !strings.Contains(err.Error(), "datasource_uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryDataSource_MissingQueries(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryDataSource(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
	})
	if err == nil {
		t.Fatal("expected error for missing queries")
	}
	if !strings.Contains(err.Error(), "queries is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryDataSource_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{}}`)
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"datasource_uid": "prom-1",
		"queries":        []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
	}

	_, err := tool.QueryDataSource(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.QueryDataSource(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests (no caching for POST), got %d", counter.Load())
	}
}

// --- QueryPrometheus tests ---

func TestQueryPrometheus_Success(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[{"data":{"values":[[1]]}}]}}}`)
	})

	result, err := tool.QueryPrometheus(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
		"expr":           "node_cpu_seconds_total{mode=\"idle\"}",
		"instant":        true,
		"from":           "now-5m",
		"to":             "now",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "frames") {
		t.Error("expected result to contain query frames")
	}

	// Verify the query structure
	queries, ok := receivedBody["queries"].([]interface{})
	if !ok || len(queries) == 0 {
		t.Fatal("expected queries array in request body")
	}
	q := queries[0].(map[string]interface{})
	if q["expr"] != "node_cpu_seconds_total{mode=\"idle\"}" {
		t.Errorf("unexpected expr: %v", q["expr"])
	}
	ds := q["datasource"].(map[string]interface{})
	if ds["uid"] != "prom-1" {
		t.Errorf("expected datasource uid=prom-1, got %v", ds["uid"])
	}
	if ds["type"] != "prometheus" {
		t.Errorf("expected datasource type=prometheus, got %v", ds["type"])
	}
	if q["instant"] != true {
		t.Error("expected instant=true in query")
	}
}

func TestQueryPrometheus_RangeQuery(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[]}}}`)
	})

	_, err := tool.QueryPrometheus(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
		"expr":           "rate(http_requests_total[5m])",
		"range":          true,
		"start":          "2026-03-27T00:00:00Z",
		"end":            "2026-03-27T01:00:00Z",
		"step":           "60s",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	if q["range"] != true {
		t.Error("expected range=true")
	}
	if q["start"] != "2026-03-27T00:00:00Z" {
		t.Errorf("expected start time, got %v", q["start"])
	}
	if q["step"] != "60s" {
		t.Errorf("expected step=60s, got %v", q["step"])
	}
}

func TestQueryPrometheus_MissingDatasourceUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryPrometheus(context.Background(), "test-incident", map[string]interface{}{
		"expr": "up",
	})
	if err == nil {
		t.Fatal("expected error for missing datasource_uid")
	}
	if !strings.Contains(err.Error(), "datasource_uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryPrometheus_MissingExpr(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryPrometheus(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "prom-1",
	})
	if err == nil {
		t.Fatal("expected error for missing expr")
	}
	if !strings.Contains(err.Error(), "expr is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- QueryLoki tests ---

func TestQueryLoki_Success(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[{"data":{"values":[["log line 1","log line 2"]]}}]}}}`)
	})

	result, err := tool.QueryLoki(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "loki-1",
		"expr":           "{app=\"nginx\"} |= \"error\"",
		"limit":          float64(100),
		"direction":      "backward",
		"from":           "now-1h",
		"to":             "now",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "log line") {
		t.Error("expected result to contain log data")
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	if q["expr"] != "{app=\"nginx\"} |= \"error\"" {
		t.Errorf("unexpected expr: %v", q["expr"])
	}
	ds := q["datasource"].(map[string]interface{})
	if ds["uid"] != "loki-1" {
		t.Errorf("expected datasource uid=loki-1, got %v", ds["uid"])
	}
	if ds["type"] != "loki" {
		t.Errorf("expected datasource type=loki, got %v", ds["type"])
	}
	// maxLines is marshalled as float64 from JSON
	if q["maxLines"] != float64(100) {
		t.Errorf("expected maxLines=100, got %v", q["maxLines"])
	}
	if q["direction"] != "backward" {
		t.Errorf("expected direction=backward, got %v", q["direction"])
	}
}

func TestQueryLoki_MissingDatasourceUID(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryLoki(context.Background(), "test-incident", map[string]interface{}{
		"expr": "{app=\"nginx\"}",
	})
	if err == nil {
		t.Fatal("expected error for missing datasource_uid")
	}
	if !strings.Contains(err.Error(), "datasource_uid is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryLoki_MissingExpr(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.QueryLoki(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "loki-1",
	})
	if err == nil {
		t.Fatal("expected error for missing expr")
	}
	if !strings.Contains(err.Error(), "expr is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestQueryLoki_MinimalArgs(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[]}}}`)
	})

	_, err := tool.QueryLoki(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "loki-1",
		"expr":           "{app=\"test\"}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	// Should not have optional fields when not provided
	if _, exists := q["maxLines"]; exists {
		t.Error("maxLines should not be present when limit not provided")
	}
	if _, exists := q["direction"]; exists {
		t.Error("direction should not be present when not provided")
	}
}

func TestQueryLoki_MaxLinesClamped(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":{"A":{"frames":[]}}}`)
	})

	_, err := tool.QueryLoki(context.Background(), "test-incident", map[string]interface{}{
		"datasource_uid": "loki-1",
		"expr":           "{app=\"test\"}",
		"limit":          float64(100000),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := receivedBody["queries"].([]interface{})
	q := queries[0].(map[string]interface{})
	if q["maxLines"] != float64(5000) {
		t.Errorf("expected maxLines to be clamped to 5000, got %v", q["maxLines"])
	}
}

// --- CreateAnnotation tests ---

func TestCreateAnnotation_Success(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":42,"message":"Annotation added"}`)
	})

	result, err := tool.CreateAnnotation(context.Background(), "test-incident", map[string]interface{}{
		"text":         "Deployment started",
		"dashboard_id": float64(5),
		"panel_id":     float64(2),
		"tags":         []interface{}{"deploy", "production"},
		"time":         float64(1711497600000),
		"time_end":     float64(1711501200000),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/api/annotations" {
		t.Errorf("expected /api/annotations, got %s", receivedPath)
	}
	if !strings.Contains(result, "42") {
		t.Error("expected result to contain annotation ID")
	}
	if receivedBody["text"] != "Deployment started" {
		t.Errorf("expected text=Deployment started, got %v", receivedBody["text"])
	}
	// JSON numbers are float64
	if receivedBody["dashboardId"] != float64(5) {
		t.Errorf("expected dashboardId=5, got %v", receivedBody["dashboardId"])
	}
	if receivedBody["panelId"] != float64(2) {
		t.Errorf("expected panelId=2, got %v", receivedBody["panelId"])
	}
}

func TestCreateAnnotation_MissingText(t *testing.T) {
	tool := NewGrafanaTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.CreateAnnotation(context.Background(), "test-incident", map[string]interface{}{
		"dashboard_id": float64(5),
	})
	if err == nil {
		t.Fatal("expected error for missing text")
	}
	if !strings.Contains(err.Error(), "text is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateAnnotation_MinimalArgs(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	})

	_, err := tool.CreateAnnotation(context.Background(), "test-incident", map[string]interface{}{
		"text": "Global annotation",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedBody["text"] != "Global annotation" {
		t.Errorf("expected text, got %v", receivedBody["text"])
	}
	// Optional fields should not be present
	if _, exists := receivedBody["dashboardId"]; exists {
		t.Error("dashboardId should not be present when not provided")
	}
	if _, exists := receivedBody["panelId"]; exists {
		t.Error("panelId should not be present when not provided")
	}
}

func TestCreateAnnotation_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":1}`)
	})

	ctx := context.Background()
	args := map[string]interface{}{"text": "test annotation"}

	_, err := tool.CreateAnnotation(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.CreateAnnotation(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests (no caching for POST), got %d", counter.Load())
	}
}

// --- GetAnnotations tests ---

func TestGetAnnotations_Success(t *testing.T) {
	var receivedPath string
	var receivedParams url.Values
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedParams = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"id":1,"text":"deploy","dashboardId":5},{"id":2,"text":"incident","dashboardId":5}]`)
	})

	result, err := tool.GetAnnotations(context.Background(), "test-incident", map[string]interface{}{
		"from":         float64(1711497600000),
		"to":           float64(1711501200000),
		"dashboard_id": float64(5),
		"limit":        float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedPath != "/api/annotations" {
		t.Errorf("expected /api/annotations, got %s", receivedPath)
	}
	if receivedParams.Get("from") != "1711497600000" {
		t.Errorf("expected from param, got %s", receivedParams.Get("from"))
	}
	if receivedParams.Get("to") != "1711501200000" {
		t.Errorf("expected to param, got %s", receivedParams.Get("to"))
	}
	if receivedParams.Get("dashboardId") != "5" {
		t.Errorf("expected dashboardId param, got %s", receivedParams.Get("dashboardId"))
	}
	if receivedParams.Get("limit") != "50" {
		t.Errorf("expected limit param, got %s", receivedParams.Get("limit"))
	}
	if !strings.Contains(result, "deploy") {
		t.Error("expected result to contain annotation data")
	}
}

func TestGetAnnotations_WithAllFilters(t *testing.T) {
	var receivedParams url.Values
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedParams = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.GetAnnotations(context.Background(), "test-incident", map[string]interface{}{
		"from":         float64(1000),
		"to":           float64(2000),
		"dashboard_id": float64(3),
		"panel_id":     float64(7),
		"tags":         "deploy,production",
		"limit":        float64(100),
		"type":         "annotation",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedParams.Get("panelId") != "7" {
		t.Errorf("expected panelId=7, got %s", receivedParams.Get("panelId"))
	}
	tagValues := receivedParams["tags"]
	if len(tagValues) != 2 || tagValues[0] != "deploy" || tagValues[1] != "production" {
		t.Errorf("expected tags=[deploy, production], got %v", tagValues)
	}
	if receivedParams.Get("type") != "annotation" {
		t.Errorf("expected type=annotation, got %s", receivedParams.Get("type"))
	}
}

func TestGetAnnotations_LimitClamped(t *testing.T) {
	var receivedParams url.Values
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedParams = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.GetAnnotations(context.Background(), "test-incident", map[string]interface{}{
		"limit": float64(99999),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedParams.Get("limit") != "5000" {
		t.Errorf("expected limit clamped to 5000, got %s", receivedParams.Get("limit"))
	}
}

func TestGetAnnotations_NoFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	result, err := tool.GetAnnotations(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "[]" {
		t.Errorf("expected empty array, got %s", result)
	}
}

func TestGetAnnotations_Cached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"id":1}]`)
	})

	ctx := context.Background()
	args := map[string]interface{}{"from": float64(1000), "to": float64(2000)}

	_, err := tool.GetAnnotations(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.GetAnnotations(ctx, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (cache hit), got %d", counter.Load())
	}
}
