package pagerduty

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

// newTestTool creates a PagerDutyTool with an httptest server's URL pre-populated in the config cache.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*PagerDutyTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewPagerDutyTool(testLogger(), nil)
	config := &PagerDutyConfig{
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

func TestNewPagerDutyTool(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)

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

func TestNewPagerDutyTool_WithRateLimiter(t *testing.T) {
	limiter := ratelimit.New(10, 20)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:pagerduty"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"statuses[]": []string{"triggered"}}
	params2 := url.Values{"statuses[]": []string{"resolved"}}

	key1 := responseCacheKey("/incidents", params1)
	key2 := responseCacheKey("/incidents", params2)
	key3 := responseCacheKey("/incidents", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- Timeout clamping tests ---

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero defaults to 30", 0, 30},
		{"negative defaults to 30", -5, 30},
		{"below min clamped to 5", 1, 5},
		{"above max clamped to 300", 999, 300},
		{"valid 30 kept", 30, 30},
		{"valid 5 kept", 5, 5},
		{"valid 300 kept", 300, 300},
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

// --- extractLogicalName tests ---

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-pd"}, "prod-pd"},
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

// --- trimTrailingSlash tests ---

func TestTrimTrailingSlash(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"https://api.pagerduty.com/", "https://api.pagerduty.com"},
		{"https://api.pagerduty.com", "https://api.pagerduty.com"},
		{"", ""},
	}
	for _, tt := range tests {
		got := trimTrailingSlash(tt.input)
		if got != tt.want {
			t.Errorf("trimTrailingSlash(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- getConfig tests ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	expected := &PagerDutyConfig{
		URL:       "https://api.pagerduty.com",
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
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	expected := &PagerDutyConfig{
		URL:      "https://api.pagerduty.com",
		APIToken: "prod-token",
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "pagerduty", "prod-pd"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", "prod-pd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.APIToken != expected.APIToken {
		t.Errorf("expected APIToken %q, got %q", expected.APIToken, config.APIToken)
	}
}

// --- doRequest tests ---

func TestDoRequest_TokenAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": "ok"}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:      server.URL,
		APIToken: "test-token-123",
		Timeout:  5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Token token=test-token-123"
	if receivedAuth != expected {
		t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
	}
}

func TestDoRequest_EmptyToken(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:      "http://localhost:9999",
		APIToken: "",
		Timeout:  5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty API token")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("expected error about missing token, got: %v", err)
	}
}

func TestDoRequest_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"Not Found"}}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "token", Timeout: 5}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents/BADID", nil, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to contain status code, got %q", err.Error())
	}
}

func TestDoRequest_ResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		data := make([]byte, 6*1024*1024)
		for i := range data {
			data[i] = 'x'
		}
		w.Write(data) //nolint:errcheck
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "token", Timeout: 10}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
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

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:      server.URL,
		APIToken: "token",
		Timeout:  5,
		UseProxy: false,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receivedReq {
		t.Error("expected request to reach server without proxy")
	}
}

func TestDoRequest_SSLVerificationDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "token",
		VerifySSL: false,
		Timeout:   5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
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

	limiter := ratelimit.New(100, 100)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "token", Timeout: 5}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestDoRequest_RateLimitCancelledContext(t *testing.T) {
	limiter := ratelimit.New(1, 1)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: "http://localhost:9999", APIToken: "token", Timeout: 5}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.doRequest(cancelledCtx, config, http.MethodGet, "/incidents", nil, nil)
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

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "token", Timeout: 5}
	params := url.Values{"statuses[]": []string{"triggered"}, "urgencies[]": []string{"high"}}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/incidents", params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedURL, "statuses") {
		t.Errorf("expected URL to contain 'statuses', got %q", receivedURL)
	}
	if !strings.Contains(receivedURL, "urgencies") {
		t.Errorf("expected URL to contain 'urgencies', got %q", receivedURL)
	}
}

// --- cachedGet tests ---

func TestCachedGet_CacheMiss(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incidents": []}`)
	})

	result, err := tool.cachedGet(context.Background(), "test-incident", "/incidents", nil, IncidentCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(result), "incidents") {
		t.Errorf("expected result to contain 'incidents', got %s", result)
	}
	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

func TestCachedGet_CacheHit(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incidents": []}`)
	})

	// First call - cache miss
	_, err := tool.cachedGet(context.Background(), "test-incident", "/incidents", nil, IncidentCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call - should be cache hit
	result, err := tool.cachedGet(context.Background(), "test-incident", "/incidents", nil, IncidentCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error on cached call: %v", err)
	}

	if !strings.Contains(string(result), "incidents") {
		t.Errorf("expected cached result to contain 'incidents', got %s", result)
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

	params1 := url.Values{"statuses[]": []string{"triggered"}}
	params2 := url.Values{"statuses[]": []string{"resolved"}}

	_, err := tool.cachedGet(context.Background(), "test-incident", "/incidents", params1, IncidentCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.cachedGet(context.Background(), "test-incident", "/incidents", params2, IncidentCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 requests for different params, got %d", counter.Load())
	}
}

func TestCachedGet_LogicalNameCacheKey(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data": "ok"}`)
	})

	// Also seed a logical-name config entry
	cachedConfig, _ := tool.configCache.Get(configCacheKey("test-incident"))
	baseConfig := cachedConfig.(*PagerDutyConfig)
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "pagerduty", "prod-pd"), &PagerDutyConfig{
		URL:      baseConfig.URL,
		APIToken: "test-token",
		Timeout:  5,
	})

	_, err := tool.cachedGet(context.Background(), "test-incident", "/incidents", nil, IncidentCacheTTL, "prod-pd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

// --- Read-only operation tests ---

func TestGetIncidents_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/incidents" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incidents":[{"id":"P123","title":"CPU High"}]}`)
	})

	result, err := tool.GetIncidents(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "P123") {
		t.Errorf("expected result to contain incident ID, got %s", result)
	}
}

func TestGetIncidents_WithFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("statuses[]") != "triggered" {
			t.Errorf("expected statuses[]=triggered, got %q", q.Get("statuses[]"))
		}
		if q.Get("urgencies[]") != "high" {
			t.Errorf("expected urgencies[]=high, got %q", q.Get("urgencies[]"))
		}
		if q.Get("since") != "2026-03-01T00:00:00Z" {
			t.Errorf("expected since param, got %q", q.Get("since"))
		}
		if q.Get("limit") != "10" {
			t.Errorf("expected limit=10, got %q", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incidents":[]}`)
	})

	args := map[string]interface{}{
		"statuses":  "triggered",
		"urgencies": "high",
		"since":     "2026-03-01T00:00:00Z",
		"limit":     float64(10),
	}
	_, err := tool.GetIncidents(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetIncident_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/incidents/P123ABC" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P123ABC","title":"DB Down"}}`)
	})

	result, err := tool.GetIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id": "P123ABC",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "P123ABC") {
		t.Errorf("expected result to contain incident ID, got %s", result)
	}
}

func TestGetIncident_MissingID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.GetIncident(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing incident_id")
	}
	if !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected error about missing incident_id, got: %v", err)
	}
}

func TestGetIncidentNotes_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/incidents/P123/notes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"notes":[{"id":"N1","content":"Investigating"}]}`)
	})

	result, err := tool.GetIncidentNotes(context.Background(), "test-incident", map[string]interface{}{
		"incident_id": "P123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Investigating") {
		t.Errorf("expected result to contain note content, got %s", result)
	}
}

func TestGetIncidentNotes_MissingID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.GetIncidentNotes(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing incident_id")
	}
	if !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected error about missing incident_id, got: %v", err)
	}
}

func TestGetIncidentAlerts_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/incidents/P456/alerts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"alerts":[{"id":"A1","status":"triggered"}]}`)
	})

	result, err := tool.GetIncidentAlerts(context.Background(), "test-incident", map[string]interface{}{
		"incident_id": "P456",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "A1") {
		t.Errorf("expected result to contain alert ID, got %s", result)
	}
}

func TestGetIncidentAlerts_MissingID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.GetIncidentAlerts(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing incident_id")
	}
	if !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected error about missing incident_id, got: %v", err)
	}
}

func TestGetServices_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"services":[{"id":"S1","name":"Web App"}]}`)
	})

	result, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Web App") {
		t.Errorf("expected result to contain service name, got %s", result)
	}
}

func TestGetServices_WithQuery(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "web" {
			t.Errorf("expected query=web, got %q", q.Get("query"))
		}
		if q.Get("limit") != "5" {
			t.Errorf("expected limit=5, got %q", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"services":[]}`)
	})

	_, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{
		"query": "web",
		"limit": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetOnCalls_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oncalls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"oncalls":[{"user":{"name":"Alice"}}]}`)
	})

	result, err := tool.GetOnCalls(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected result to contain user name, got %s", result)
	}
}

func TestGetOnCalls_WithFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("schedule_ids[]") != "SCHED1" {
			t.Errorf("expected schedule_ids[]=SCHED1, got %q", q.Get("schedule_ids[]"))
		}
		if q.Get("escalation_policy_ids[]") != "EP1" {
			t.Errorf("expected escalation_policy_ids[]=EP1, got %q", q.Get("escalation_policy_ids[]"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"oncalls":[]}`)
	})

	_, err := tool.GetOnCalls(context.Background(), "test-incident", map[string]interface{}{
		"schedule_ids":          "SCHED1",
		"escalation_policy_ids": "EP1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetEscalationPolicies_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/escalation_policies" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"escalation_policies":[{"id":"EP1","name":"Default"}]}`)
	})

	result, err := tool.GetEscalationPolicies(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Default") {
		t.Errorf("expected result to contain policy name, got %s", result)
	}
}

func TestGetEscalationPolicies_WithQuery(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "ops" {
			t.Errorf("expected query=ops, got %q", q.Get("query"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"escalation_policies":[]}`)
	})

	_, err := tool.GetEscalationPolicies(context.Background(), "test-incident", map[string]interface{}{
		"query": "ops",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListRecentChanges_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/change_events" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"change_events":[{"id":"C1","summary":"Deploy v2"}]}`)
	})

	result, err := tool.ListRecentChanges(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Deploy v2") {
		t.Errorf("expected result to contain change summary, got %s", result)
	}
}

func TestListRecentChanges_WithTimeRange(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("since") != "2026-03-01T00:00:00Z" {
			t.Errorf("expected since param, got %q", q.Get("since"))
		}
		if q.Get("until") != "2026-03-28T00:00:00Z" {
			t.Errorf("expected until param, got %q", q.Get("until"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"change_events":[]}`)
	})

	_, err := tool.ListRecentChanges(context.Background(), "test-incident", map[string]interface{}{
		"since": "2026-03-01T00:00:00Z",
		"until": "2026-03-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- API error propagation tests ---

func TestGetIncidents_APIError(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid token"}}`)
	})

	_, err := tool.GetIncidents(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got: %v", err)
	}
}

func TestGetServices_APIError(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	})

	_, err := tool.GetServices(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for server error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got: %v", err)
	}
}

// --- Response caching per function tests ---

func TestGetIncidents_ResponseCached(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incidents":[]}`)
	})

	args := map[string]interface{}{}

	_, err := tool.GetIncidents(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call should be cached
	_, err = tool.GetIncidents(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// newTestTool counter includes wrapping, use our own
	if callCount.Load() != 1 {
		t.Errorf("expected 1 API call (second should be cached), got %d", callCount.Load())
	}
}

// Verify unused import suppression
var _ = time.Second
