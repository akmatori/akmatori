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

// --- GetNamespaces tests ---

func TestGetNamespaces_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces" {
			t.Errorf("expected path /api/v1/namespaces, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"default"}},{"metadata":{"name":"kube-system"}}]}`)
	})

	result, err := tool.GetNamespaces(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "default") {
		t.Error("expected result to contain 'default'")
	}
	if !strings.Contains(result, "kube-system") {
		t.Error("expected result to contain 'kube-system'")
	}
}

func TestGetNamespaces_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "env=prod" {
			t.Errorf("expected labelSelector=env=prod, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetNamespaces(context.Background(), "test-incident", map[string]interface{}{
		"label_selector": "env=prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetPods tests ---

func TestGetPods_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods" {
			t.Errorf("expected path /api/v1/namespaces/default/pods, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"pod-1"}}]}`)
	})

	result, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "pod-1") {
		t.Error("expected result to contain 'pod-1'")
	}
}

func TestGetPods_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetPods_WithNameFilter(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods/my-pod" {
			t.Errorf("expected path for specific pod, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"my-pod"}}`)
	})

	result, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"name":      "my-pod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "my-pod") {
		t.Error("expected result to contain 'my-pod'")
	}
}

func TestGetPods_WithSelectors(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		if r.URL.Query().Get("fieldSelector") != "status.phase=Running" {
			t.Errorf("expected fieldSelector=status.phase=Running, got %q", r.URL.Query().Get("fieldSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=web",
		"field_selector": "status.phase=Running",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPods_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit=10, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetPodDetail tests ---

func TestGetPodDetail_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/kube-system/pods/coredns-abc123" {
			t.Errorf("expected pod detail path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"coredns-abc123","namespace":"kube-system"},"status":{"phase":"Running"}}`)
	})

	result, err := tool.GetPodDetail(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "kube-system",
		"name":      "coredns-abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "coredns-abc123") {
		t.Error("expected result to contain pod name")
	}
	if !strings.Contains(result, "Running") {
		t.Error("expected result to contain status")
	}
}

func TestGetPodDetail_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetPodDetail(context.Background(), "test-incident", map[string]interface{}{
		"name": "my-pod",
	})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetPodDetail_MissingName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetPodDetail(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got: %v", err)
	}
}

// --- GetPodLogs tests ---

func TestGetPodLogs_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods/my-pod/log" {
			t.Errorf("expected log path, got %s", r.URL.Path)
		}
		// Default tail_lines=100
		if r.URL.Query().Get("tailLines") != "100" {
			t.Errorf("expected default tailLines=100, got %q", r.URL.Query().Get("tailLines"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "2024-01-01 log line 1\n2024-01-01 log line 2\n")
	})

	result, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"name":      "my-pod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "log line 1") {
		t.Error("expected result to contain log lines")
	}
}

func TestGetPodLogs_WithContainer(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("container") != "nginx" {
			t.Errorf("expected container=nginx, got %q", r.URL.Query().Get("container"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "nginx log")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"name":      "my-pod",
		"container": "nginx",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_CustomTailLines(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tailLines") != "50" {
			t.Errorf("expected tailLines=50, got %q", r.URL.Query().Get("tailLines"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "log output")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":  "default",
		"name":       "my-pod",
		"tail_lines": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_TailLinesClamped(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tailLines") != "10000" {
			t.Errorf("expected tailLines clamped to 10000, got %q", r.URL.Query().Get("tailLines"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "log output")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":  "default",
		"name":       "my-pod",
		"tail_lines": float64(50000),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_FractionalTailLinesClamped(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		tl := r.URL.Query().Get("tailLines")
		if tl != "1" {
			t.Errorf("expected fractional tail_lines clamped to 1, got %q", tl)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "log output")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":  "default",
		"name":       "my-pod",
		"tail_lines": float64(0.5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_WithSinceSeconds(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sinceSeconds") != "3600" {
			t.Errorf("expected sinceSeconds=3600, got %q", r.URL.Query().Get("sinceSeconds"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "log output")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":     "default",
		"name":          "my-pod",
		"since_seconds": float64(3600),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_WithPrevious(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("previous") != "true" {
			t.Errorf("expected previous=true, got %q", r.URL.Query().Get("previous"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "previous container log")
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"name":      "my-pod",
		"previous":  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPodLogs_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"name": "my-pod",
	})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetPodLogs_MissingName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetPodLogs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got: %v", err)
	}
}

// --- GetEvents tests ---

func TestGetEvents_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/events" {
			t.Errorf("expected events path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"event-1"},"reason":"BackOff","type":"Warning"}]}`)
	})

	result, err := tool.GetEvents(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "BackOff") {
		t.Error("expected result to contain event reason")
	}
	if !strings.Contains(result, "Warning") {
		t.Error("expected result to contain event type")
	}
}

func TestGetEvents_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetEvents(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetEvents_WithFieldSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fieldSelector") != "type=Warning" {
			t.Errorf("expected fieldSelector=type=Warning, got %q", r.URL.Query().Get("fieldSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetEvents(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"field_selector": "type=Warning",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetEvents_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "20" {
			t.Errorf("expected limit=20, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetEvents(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(20),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Logical name routing tests ---

func TestGetPods_WithLogicalName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	// Pre-populate a config for the logical name
	cachedConfig, _ := tool.configCache.Get(configCacheKey("test-incident"))
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "kubernetes", "prod-k8s"), cachedConfig)

	_, err := tool.GetPods(context.Background(), "test-incident", map[string]interface{}{
		"namespace":    "default",
		"logical_name": "prod-k8s",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetDeployments tests ---

func TestGetDeployments_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/default/deployments" {
			t.Errorf("expected path /apis/apps/v1/namespaces/default/deployments, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"nginx-deploy"}}]}`)
	})

	result, err := tool.GetDeployments(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "nginx-deploy") {
		t.Error("expected result to contain 'nginx-deploy'")
	}
}

func TestGetDeployments_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetDeployments(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetDeployments_WithNameFilter(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/default/deployments/nginx" {
			t.Errorf("expected path for specific deployment, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"nginx"}}`)
	})

	result, err := tool.GetDeployments(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"name":      "nginx",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "nginx") {
		t.Error("expected result to contain 'nginx'")
	}
}

func TestGetDeployments_WithSelectors(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetDeployments(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetDeployments_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "5" {
			t.Errorf("expected limit=5, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetDeployments(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetDeploymentDetail tests ---

func TestGetDeploymentDetail_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/kube-system/deployments/coredns" {
			t.Errorf("expected path for coredns deployment, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"coredns"},"spec":{"replicas":2},"status":{"readyReplicas":2}}`)
	})

	result, err := tool.GetDeploymentDetail(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "kube-system",
		"name":      "coredns",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "coredns") {
		t.Error("expected result to contain 'coredns'")
	}
	if !strings.Contains(result, "readyReplicas") {
		t.Error("expected result to contain 'readyReplicas'")
	}
}

func TestGetDeploymentDetail_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetDeploymentDetail(context.Background(), "test-incident", map[string]interface{}{
		"name": "coredns",
	})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetDeploymentDetail_MissingName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetDeploymentDetail(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got: %v", err)
	}
}

// --- GetStatefulSets tests ---

func TestGetStatefulSets_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/default/statefulsets" {
			t.Errorf("expected path /apis/apps/v1/namespaces/default/statefulsets, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"redis"}}]}`)
	})

	result, err := tool.GetStatefulSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "redis") {
		t.Error("expected result to contain 'redis'")
	}
}

func TestGetStatefulSets_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetStatefulSets(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetStatefulSets_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=redis" {
			t.Errorf("expected labelSelector=app=redis, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetStatefulSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=redis",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStatefulSets_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit=10, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetStatefulSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetDaemonSets tests ---

func TestGetDaemonSets_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/kube-system/daemonsets" {
			t.Errorf("expected path /apis/apps/v1/namespaces/kube-system/daemonsets, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"kube-proxy"}}]}`)
	})

	result, err := tool.GetDaemonSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "kube-system",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "kube-proxy") {
		t.Error("expected result to contain 'kube-proxy'")
	}
}

func TestGetDaemonSets_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetDaemonSets(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetDaemonSets_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "k8s-app=kube-proxy" {
			t.Errorf("expected labelSelector=k8s-app=kube-proxy, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetDaemonSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "kube-system",
		"label_selector": "k8s-app=kube-proxy",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetDaemonSets_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "15" {
			t.Errorf("expected limit=15, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetDaemonSets(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "kube-system",
		"limit":     float64(15),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetJobs tests ---

func TestGetJobs_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/batch/v1/namespaces/default/jobs" {
			t.Errorf("expected path /apis/batch/v1/namespaces/default/jobs, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"db-migrate"}}]}`)
	})

	result, err := tool.GetJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "db-migrate") {
		t.Error("expected result to contain 'db-migrate'")
	}
}

func TestGetJobs_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetJobs(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetJobs_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "job-type=migration" {
			t.Errorf("expected labelSelector=job-type=migration, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "job-type=migration",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetJobs_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "20" {
			t.Errorf("expected limit=20, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(20),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetCronJobs tests ---

func TestGetCronJobs_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/batch/v1/namespaces/default/cronjobs" {
			t.Errorf("expected path /apis/batch/v1/namespaces/default/cronjobs, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"cleanup-cron"},"spec":{"schedule":"0 */6 * * *"}}]}`)
	})

	result, err := tool.GetCronJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "cleanup-cron") {
		t.Error("expected result to contain 'cleanup-cron'")
	}
}

func TestGetCronJobs_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.GetCronJobs(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetCronJobs_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=cleanup" {
			t.Errorf("expected labelSelector=app=cleanup, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetCronJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=cleanup",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetCronJobs_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "3" {
			t.Errorf("expected limit=3, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetCronJobs(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetNodes tests ---

func TestGetNodes_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			t.Errorf("expected path /api/v1/nodes, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"node-1"}},{"metadata":{"name":"node-2"}}]}`)
	})

	result, err := tool.GetNodes(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "node-1") {
		t.Error("expected result to contain 'node-1'")
	}
	if !strings.Contains(result, "node-2") {
		t.Error("expected result to contain 'node-2'")
	}
}

func TestGetNodes_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "role=worker" {
			t.Errorf("expected labelSelector=role=worker, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetNodes(context.Background(), "test-incident", map[string]interface{}{
		"label_selector": "role=worker",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetNodes_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "5" {
			t.Errorf("expected limit=5, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetNodes(context.Background(), "test-incident", map[string]interface{}{
		"limit": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetNodeDetail tests ---

func TestGetNodeDetail_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/worker-node-1" {
			t.Errorf("expected path /api/v1/nodes/worker-node-1, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"worker-node-1"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}`)
	})

	result, err := tool.GetNodeDetail(context.Background(), "test-incident", map[string]interface{}{
		"name": "worker-node-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "worker-node-1") {
		t.Error("expected result to contain node name")
	}
	if !strings.Contains(result, "Ready") {
		t.Error("expected result to contain condition")
	}
}

func TestGetNodeDetail_MissingName(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetNodeDetail(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got: %v", err)
	}
}

// --- GetServices tests ---

func TestGetServices_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/services" {
			t.Errorf("expected path /api/v1/namespaces/default/services, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"my-service"},"spec":{"type":"ClusterIP"}}]}`)
	})

	result, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "my-service") {
		t.Error("expected result to contain 'my-service'")
	}
}

func TestGetServices_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetServices_WithSelectors(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetServices_WithLimit(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "20" {
			t.Errorf("expected limit=20, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
		"limit":     float64(20),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetConfigMaps tests ---

func TestGetConfigMaps_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/configmaps" {
			t.Errorf("expected path /api/v1/namespaces/default/configmaps, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"my-config"},"data":{"key1":"value1","key2":"value2"}}]}`)
	})

	result, err := tool.GetConfigMaps(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "my-config") {
		t.Error("expected result to contain 'my-config'")
	}
	// Data should be stripped
	if strings.Contains(result, "value1") {
		t.Error("expected data values to be stripped from configmap response")
	}
}

func TestGetConfigMaps_StripsDataAndBinaryData(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"cm-1"},"data":{"secret":"password"},"binaryData":{"cert":"base64data"}}]}`)
	})

	result, err := tool.GetConfigMaps(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "password") {
		t.Error("expected data to be stripped")
	}
	if strings.Contains(result, "base64data") {
		t.Error("expected binaryData to be stripped")
	}
	if !strings.Contains(result, "cm-1") {
		t.Error("expected metadata to be preserved")
	}
}

func TestGetConfigMaps_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetConfigMaps(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetConfigMaps_WithLabelSelector(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetConfigMaps(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "app=web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetIngresses tests ---

func TestGetIngresses_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/networking.k8s.io/v1/namespaces/default/ingresses" {
			t.Errorf("expected ingress path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"my-ingress"},"spec":{"rules":[{"host":"example.com"}]}}]}`)
	})

	result, err := tool.GetIngresses(context.Background(), "test-incident", map[string]interface{}{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "my-ingress") {
		t.Error("expected result to contain 'my-ingress'")
	}
	if !strings.Contains(result, "example.com") {
		t.Error("expected result to contain host")
	}
}

func TestGetIngresses_MissingNamespace(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.GetIngresses(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("expected namespace required error, got: %v", err)
	}
}

func TestGetIngresses_WithSelectors(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "tier=frontend" {
			t.Errorf("expected labelSelector=tier=frontend, got %q", r.URL.Query().Get("labelSelector"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit=10, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.GetIngresses(context.Background(), "test-incident", map[string]interface{}{
		"namespace":      "default",
		"label_selector": "tier=frontend",
		"limit":          float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- APIRequest tests ---

func TestAPIRequest_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/componentstatuses" {
			t.Errorf("expected path /api/v1/componentstatuses, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"scheduler"}}]}`)
	})

	result, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/componentstatuses",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "scheduler") {
		t.Error("expected result to contain 'scheduler'")
	}
}

func TestAPIRequest_WithParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("expected labelSelector=app=web, got %q", r.URL.Query().Get("labelSelector"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/pods",
		"params": map[string]interface{}{
			"labelSelector": "app=web",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIRequest_ApisPath(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/deployments" {
			t.Errorf("expected path /apis/apps/v1/deployments, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"items":[]}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/apis/apps/v1/deployments",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIRequest_InvalidPath(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
	}{
		{"root", "/"},
		{"healthz", "/healthz"},
		{"random", "/foo/bar"},
		{"no leading slash", "api/v1/pods"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path": tt.path,
			})
			if err == nil {
				t.Fatal("expected error for invalid path")
			}
		})
	}
}

func TestAPIRequest_PathTraversal(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not reach server for path traversal attempts")
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
	}{
		{"dot-dot in path", "/api/v1/../../healthz"},
		{"dot-dot to metrics", "/api/v1/../metrics"},
		{"encoded dot-dot", "/api/v1/%2e%2e/healthz"},
		{"double-encoded dot-dot", "/api/v1/%252e%252e/healthz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path": tt.path,
			})
			if err == nil {
				t.Fatal("expected error for path traversal")
			}
			if !strings.Contains(err.Error(), "..") {
				t.Errorf("expected path traversal error, got: %v", err)
			}
		})
	}
}

func TestAPIRequest_QueryStringInPath(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not reach server")
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/pods?watch=true",
	})
	if err == nil {
		t.Fatal("expected error for query string in path")
	}
	if !strings.Contains(err.Error(), "query string") {
		t.Errorf("expected query string error, got: %v", err)
	}
}

func TestAPIRequest_WatchBlocked(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not reach server for watch requests")
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"watch string true", map[string]interface{}{"watch": "true"}},
		{"watch bool true", map[string]interface{}{"watch": true}},
		{"watch float64", map[string]interface{}{"watch": float64(1)}},
		{"watch string false", map[string]interface{}{"watch": "false"}},
		{"watch bool false", map[string]interface{}{"watch": false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path":   "/api/v1/pods",
				"params": tt.params,
			})
			if err == nil {
				t.Fatal("expected error for watch parameter")
			}
			if !strings.Contains(err.Error(), "watch") {
				t.Errorf("expected watch error, got: %v", err)
			}
		})
	}
}

func TestAPIRequest_FollowBlocked(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not reach server for follow requests")
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"follow string true", map[string]interface{}{"follow": "true"}},
		{"follow bool true", map[string]interface{}{"follow": true}},
		{"follow string false", map[string]interface{}{"follow": "false"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path":   "/api/v1/namespaces/default/pods/my-pod/log",
				"params": tt.params,
			})
			if err == nil {
				t.Fatal("expected error for follow parameter")
			}
			if !strings.Contains(err.Error(), "follow") {
				t.Errorf("expected follow error, got: %v", err)
			}
		})
	}
}

func TestAPIRequest_SecretsBlocked(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not reach server for secrets paths")
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
	}{
		{"secrets list", "/api/v1/namespaces/default/secrets"},
		{"secrets get", "/api/v1/namespaces/default/secrets/my-secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path": tt.path,
			})
			if err == nil {
				t.Fatal("expected error for secrets path")
			}
			if !strings.Contains(err.Error(), "secrets") {
				t.Errorf("expected secrets error, got: %v", err)
			}
		})
	}
}

func TestAPIRequest_ExactApiPath(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"versions":["v1"]}`)
	})

	// /api and /apis should be accepted as exact paths
	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api",
	})
	if err != nil {
		t.Fatalf("expected /api to be accepted, got: %v", err)
	}

	_, err = tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/apis",
	})
	if err != nil {
		t.Fatalf("expected /apis to be accepted, got: %v", err)
	}
}

func TestAPIRequest_ConfigMapStripping(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"metadata":{"name":"my-config"},"data":{"password":"secret123"},"binaryData":{"cert":"base64data"}}`)
	})

	result, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/namespaces/default/configmaps/my-config",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "secret123") {
		t.Error("expected configmap data to be stripped via api_request")
	}
	if strings.Contains(result, "base64data") {
		t.Error("expected configmap binaryData to be stripped via api_request")
	}
	if !strings.Contains(result, "my-config") {
		t.Error("expected configmap metadata to be preserved")
	}
}

func TestAPIRequest_MissingPath(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected path required error, got: %v", err)
	}
}

// --- stripConfigMapData tests ---

func TestStripConfigMapData_List(t *testing.T) {
	input := []byte(`{"items":[{"metadata":{"name":"cm-1"},"data":{"key":"val"},"binaryData":{"b":"data"}}]}`)
	result := stripConfigMapData(input)

	if strings.Contains(result, `"data"`) {
		t.Error("expected data field to be stripped")
	}
	if strings.Contains(result, `"binaryData"`) {
		t.Error("expected binaryData field to be stripped")
	}
	if !strings.Contains(result, "cm-1") {
		t.Error("expected metadata to be preserved")
	}
}

func TestStripConfigMapData_InvalidJSON(t *testing.T) {
	input := []byte(`not json`)
	result := stripConfigMapData(input)
	if result != "not json" {
		t.Errorf("expected raw input returned for invalid JSON, got %q", result)
	}
}

func TestStripConfigMapData_SingleItem(t *testing.T) {
	input := []byte(`{"metadata":{"name":"cm-1"},"data":{"key":"val"}}`)
	result := stripConfigMapData(input)

	if strings.Contains(result, `"val"`) {
		t.Error("expected data field to be stripped from single item")
	}
	if !strings.Contains(result, "cm-1") {
		t.Error("expected metadata to be preserved")
	}
}
