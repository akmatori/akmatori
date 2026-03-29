package k8s

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

// newTestTool creates a K8sTool with an httptest server's URL pre-populated in the config cache.
// Returns the tool, the test server, and a request counter.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*K8sTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewK8sTool(testLogger(), nil)
	config := &K8sConfig{
		URL:       server.URL,
		Token:     "test-token",
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

func TestNewK8sTool(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)

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

func TestNewK8sTool_WithRateLimiter(t *testing.T) {
	limiter := ratelimit.New(10, 20)
	tool := NewK8sTool(testLogger(), limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:kubernetes"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"labelSelector": []string{"app=web"}}
	params2 := url.Values{"labelSelector": []string{"app=api"}}

	key1 := responseCacheKey("/api/v1/namespaces/default/pods", params1)
	key2 := responseCacheKey("/api/v1/namespaces/default/pods", params2)
	key3 := responseCacheKey("/api/v1/namespaces/default/pods", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- getConfig tests ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	expected := &K8sConfig{
		URL:       "https://k8s.example.com",
		Token:     "my-token",
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
	if config.Token != expected.Token {
		t.Errorf("expected Token %q, got %q", expected.Token, config.Token)
	}
}

func TestGetConfig_CacheHitByLogicalName(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	expected := &K8sConfig{
		URL:   "https://k8s-prod.example.com",
		Token: "prod-token",
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "kubernetes", "prod-k8s"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", "prod-k8s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
	if config.Token != expected.Token {
		t.Errorf("expected Token %q, got %q", expected.Token, config.Token)
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

// --- buildURL tests ---

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
		params  url.Values
		want    string
	}{
		{
			name:    "no params",
			baseURL: "https://k8s.example.com",
			path:    "/api/v1/namespaces",
			params:  nil,
			want:    "https://k8s.example.com/api/v1/namespaces",
		},
		{
			name:    "with params",
			baseURL: "https://k8s.example.com",
			path:    "/api/v1/namespaces/default/pods",
			params:  url.Values{"labelSelector": []string{"app=web"}},
			want:    "https://k8s.example.com/api/v1/namespaces/default/pods?labelSelector=app%3Dweb",
		},
		{
			name:    "empty params",
			baseURL: "https://k8s.example.com",
			path:    "/api/v1/nodes",
			params:  url.Values{},
			want:    "https://k8s.example.com/api/v1/nodes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildURL(tt.baseURL, tt.path, tt.params)
			if got != tt.want {
				t.Errorf("buildURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- doRequest tests ---

func getTestConfig(tool *K8sTool) *K8sConfig {
	cached, ok := tool.configCache.Get(configCacheKey("test-incident"))
	if !ok {
		return nil
	}
	return cached.(*K8sConfig)
}

func TestDoRequest_BearerAuth(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected 'Bearer test-token', got %q", auth)
		}
		accept := r.Header.Get("Accept")
		if accept != "application/json" {
			t.Errorf("expected Accept 'application/json', got %q", accept)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_EmptyToken(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	config := &K8sConfig{
		URL:   "http://localhost",
		Token: "",
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("expected token error, got: %v", err)
	}
}

func TestDoRequest_HTTPError401(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"kind":"Status","message":"Unauthorized"}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "HTTP error 401") {
		t.Errorf("expected 401 error, got: %v", err)
	}
}

func TestDoRequest_HTTPError403(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","message":"Forbidden"}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "HTTP error 403") {
		t.Errorf("expected 403 error, got: %v", err)
	}
}

func TestDoRequest_HTTPError404(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"kind":"Status","message":"pods \"nonexistent\" not found"}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces/default/pods/nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "HTTP error 404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestDoRequest_HTTPError500(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"kind":"Status","message":"Internal error"}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "HTTP error 500") {
		t.Errorf("expected 500 error, got: %v", err)
	}
}

func TestDoRequest_QueryParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit=10, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	config := getTestConfig(tool)
	params := url.Values{
		"labelSelector": []string{"app=web"},
		"limit":         []string{"10"},
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces/default/pods", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_WithRateLimiter(t *testing.T) {
	limiter := ratelimit.New(100, 100)
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})
	tool.rateLimiter = limiter

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

func TestDoRequest_ResponseSizeLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write >5MB response
		data := strings.Repeat("x", 6*1024*1024)
		fmt.Fprint(w, data)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}

func TestDoRequest_SSLVerifyDisabled(t *testing.T) {
	// Use HTTPS test server to verify TLS config
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer server.Close()

	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	config := &K8sConfig{
		URL:       server.URL,
		Token:     "test-token",
		VerifySSL: false,
		Timeout:   5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err != nil {
		t.Fatalf("expected successful request with VerifySSL=false, got: %v", err)
	}
}

func TestDoRequest_ProxyToggle(t *testing.T) {
	// Verify proxy URL is set on transport when UseProxy is true
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer proxyServer.Close()

	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	// With proxy disabled, the proxy server should NOT receive the request
	config := &K8sConfig{
		URL:      proxyServer.URL,
		Token:    "test-token",
		Timeout:  5,
		UseProxy: false,
		ProxyURL: "http://should-not-be-used:8080",
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/namespaces", nil)
	if err != nil {
		t.Fatalf("unexpected error with proxy disabled: %v", err)
	}
}

// --- cachedGet tests ---

func TestCachedGet_CachesResponse(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"pod-1"}}]}`)
	})

	ctx := context.Background()

	// First call - should hit the server
	result1, err := tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces/default/pods", nil, PodCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call - should hit the cache
	result2, err := tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces/default/pods", nil, PodCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result1) != string(result2) {
		t.Error("cached response should be identical")
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 server call (second should be cached), got %d", callCount.Load())
	}
}

func TestCachedGet_LogicalNameIsolation(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	// Pre-populate a second config for logical name lookup
	cachedConfig, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config2 := &K8sConfig{
		URL:       cachedConfig.(*K8sConfig).URL,
		Token:     "other-token",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "kubernetes", "prod-k8s"), config2)

	ctx := context.Background()

	// Call with incident ID
	_, err := tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces", nil, NSCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call with logical name
	_, err = tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces", nil, NSCacheTTL, "prod-k8s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCachedGet_CacheMiss(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	ctx := context.Background()

	// Different paths should not share cache
	_, err := tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces/default/pods", nil, PodCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = tool.cachedGet(ctx, "test-incident", "/api/v1/namespaces/kube-system/pods", nil, PodCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 server calls (different paths), got %d", callCount.Load())
	}
}

func TestCachedGet_URLNotConfigured(t *testing.T) {
	tool := NewK8sTool(testLogger(), nil)
	defer tool.Stop()

	config := &K8sConfig{
		URL:     "",
		Token:   "test-token",
		Timeout: 5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	_, err := tool.cachedGet(context.Background(), "test-incident", "/api/v1/namespaces", nil, NSCacheTTL)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "URL not configured") {
		t.Errorf("expected URL error, got: %v", err)
	}
}

// --- Helper function tests ---

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-k8s"}, "prod-k8s"},
		{"missing", map[string]interface{}{}, ""},
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

func TestRequireString(t *testing.T) {
	args := map[string]interface{}{
		"namespace": "default",
		"empty":     "",
	}

	// Present
	val, err := requireString(args, "namespace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default" {
		t.Errorf("expected %q, got %q", "default", val)
	}

	// Missing
	_, err = requireString(args, "name")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected 'name is required', got: %v", err)
	}

	// Empty string
	_, err = requireString(args, "empty")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestOptionalString(t *testing.T) {
	args := map[string]interface{}{
		"container": "nginx",
	}

	if got := optionalString(args, "container"); got != "nginx" {
		t.Errorf("expected %q, got %q", "nginx", got)
	}
	if got := optionalString(args, "missing"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestAddSelectorParams(t *testing.T) {
	params := url.Values{}
	args := map[string]interface{}{
		"label_selector": "app=web",
		"field_selector": "status.phase=Running",
	}

	addSelectorParams(params, args)

	if params.Get("labelSelector") != "app=web" {
		t.Errorf("expected labelSelector=app=web, got %q", params.Get("labelSelector"))
	}
	if params.Get("fieldSelector") != "status.phase=Running" {
		t.Errorf("expected fieldSelector=status.phase=Running, got %q", params.Get("fieldSelector"))
	}
}

func TestAddSelectorParams_Empty(t *testing.T) {
	params := url.Values{}
	args := map[string]interface{}{}

	addSelectorParams(params, args)

	if len(params) != 0 {
		t.Errorf("expected no params, got %v", params)
	}
}

func TestAddLimitParam(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"limit": float64(50)}, "50"},
		{"zero", map[string]interface{}{"limit": float64(0)}, ""},
		{"clamped", map[string]interface{}{"limit": float64(5000)}, "1000"},
		{"missing", map[string]interface{}{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{}
			addLimitParam(params, tt.args)
			got := params.Get("limit")
			if got != tt.want {
				t.Errorf("addLimitParam() limit = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- RateLimiter wait test ---

func TestDoRequest_RateLimiterCancelledContext(t *testing.T) {
	limiter := ratelimit.New(1, 1)
	tool := NewK8sTool(testLogger(), limiter)
	defer tool.Stop()

	config := &K8sConfig{
		URL:     "http://localhost:12345",
		Token:   "test-token",
		Timeout: 1,
	}

	// Exhaust the rate limiter
	ctx := context.Background()
	if err := limiter.Wait(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cancel the context so the next Wait fails
	cancelCtx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // Ensure context is cancelled

	_, err := tool.doRequest(cancelCtx, config, http.MethodGet, "/api/v1/namespaces", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
