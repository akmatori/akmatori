package pagerduty

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

func TestClampLimit(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero returns zero", 0, 0},
		{"negative returns zero", -5, 0},
		{"within range kept", 50, 50},
		{"at max kept", 100, 100},
		{"above max clamped to 100", 500, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampLimit(tt.input)
			if got != tt.want {
				t.Errorf("clampLimit(%d) = %d, want %d", tt.input, got, tt.want)
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

// --- buildURL tests ---

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		path     string
		params   url.Values
		expected string
	}{
		{"basic", "https://api.pagerduty.com", "/incidents", nil, "https://api.pagerduty.com/incidents"},
		{"trailing slash on base", "https://api.pagerduty.com/", "/incidents", nil, "https://api.pagerduty.com/incidents"},
		{"with query params", "https://api.pagerduty.com", "/incidents", url.Values{"limit": {"10"}}, "https://api.pagerduty.com/incidents?limit=10"},
		{"empty base", "", "/incidents", nil, "/incidents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildURL(tt.base, tt.path, tt.params)
			if got != tt.expected {
				t.Errorf("buildURL(%q, %q, %v) = %q, want %q", tt.base, tt.path, tt.params, got, tt.expected)
			}
		})
	}
}

// --- addArrayParam tests ---

func TestAddArrayParam(t *testing.T) {
	params := url.Values{}
	addArrayParam(params, "statuses[]", "triggered,acknowledged")
	values := params["statuses[]"]
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(values), values)
	}
	if values[0] != "triggered" || values[1] != "acknowledged" {
		t.Errorf("expected [triggered, acknowledged], got %v", values)
	}

	// Single value
	params2 := url.Values{}
	addArrayParam(params2, "statuses[]", "triggered")
	if len(params2["statuses[]"]) != 1 || params2["statuses[]"][0] != "triggered" {
		t.Errorf("single value failed: %v", params2["statuses[]"])
	}

	// Empty value parts are skipped
	params3 := url.Values{}
	addArrayParam(params3, "statuses[]", "triggered,,acknowledged")
	if len(params3["statuses[]"]) != 2 {
		t.Errorf("expected 2 values (empty skipped), got %d: %v", len(params3["statuses[]"]), params3["statuses[]"])
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

// --- Action operation tests ---

// newTestToolWithHeaders creates a test tool that also captures request headers and body.
func newTestToolWithHeaders(t *testing.T, handler http.HandlerFunc) (*PagerDutyTool, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)

	tool := NewPagerDutyTool(testLogger(), nil)
	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	t.Cleanup(func() {
		tool.Stop()
		server.Close()
	})

	return tool, server
}

// overrideEventsURL sets EventsAPIURL to the given URL for the duration of the test.
func overrideEventsURL(t *testing.T, url string) {
	t.Helper()
	original := EventsAPIURL
	EventsAPIURL = url
	t.Cleanup(func() { EventsAPIURL = original })
}

func TestAcknowledgeIncident_Success(t *testing.T) {
	var receivedMethod, receivedPath, receivedFrom string
	var receivedBody map[string]interface{}

	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedFrom = r.Header.Get("From")
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P123","status":"acknowledged"}}`)
	})

	result, err := tool.AcknowledgeIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", receivedMethod)
	}
	if receivedPath != "/incidents/P123" {
		t.Errorf("expected /incidents/P123, got %s", receivedPath)
	}
	if receivedFrom != "user@example.com" {
		t.Errorf("expected From header user@example.com, got %s", receivedFrom)
	}
	if !strings.Contains(result, "acknowledged") {
		t.Errorf("expected result to contain acknowledged, got %s", result)
	}

	incident, ok := receivedBody["incident"].(map[string]interface{})
	if !ok {
		t.Fatal("expected incident in body")
	}
	if incident["status"] != "acknowledged" {
		t.Errorf("expected status acknowledged, got %v", incident["status"])
	}
}

func TestAcknowledgeIncident_MissingIncidentID(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.AcknowledgeIncident(context.Background(), "test-incident", map[string]interface{}{
		"requester_email": "user@example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing incident_id")
	}
	if !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected incident_id error, got: %v", err)
	}
}

func TestAcknowledgeIncident_MissingEmail(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.AcknowledgeIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id": "P123",
	})
	if err == nil {
		t.Fatal("expected error for missing requester_email")
	}
	if !strings.Contains(err.Error(), "requester_email is required") {
		t.Errorf("expected requester_email error, got: %v", err)
	}
}

func TestResolveIncident_Success(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P456","status":"resolved"}}`)
	})

	result, err := tool.ResolveIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P456",
		"requester_email": "user@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "resolved") {
		t.Errorf("expected result to contain resolved, got %s", result)
	}

	incident, ok := receivedBody["incident"].(map[string]interface{})
	if !ok {
		t.Fatal("expected incident in body")
	}
	if incident["status"] != "resolved" {
		t.Errorf("expected status resolved, got %v", incident["status"])
	}
}

func TestResolveIncident_APIError(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"message":"Incident already resolved"}}`)
	})

	_, err := tool.ResolveIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P456",
		"requester_email": "user@example.com",
	})
	if err == nil {
		t.Fatal("expected error for conflict")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("expected 409 error, got: %v", err)
	}
}

func TestReassignIncident_Success(t *testing.T) {
	var receivedBody map[string]interface{}
	var receivedFrom string

	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		receivedFrom = r.Header.Get("From")
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P789","assignments":[{"assignee":{"id":"PUSER1"}}]}}`)
	})

	result, err := tool.ReassignIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P789",
		"requester_email": "admin@example.com",
		"assignee_ids":    "PUSER1,PUSER2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedFrom != "admin@example.com" {
		t.Errorf("expected From admin@example.com, got %s", receivedFrom)
	}
	if !strings.Contains(result, "PUSER1") {
		t.Errorf("expected result to contain assignee, got %s", result)
	}

	incident, ok := receivedBody["incident"].(map[string]interface{})
	if !ok {
		t.Fatal("expected incident in body")
	}
	assignments, ok := incident["assignments"].([]interface{})
	if !ok {
		t.Fatal("expected assignments array in body")
	}
	if len(assignments) != 2 {
		t.Errorf("expected 2 assignments, got %d", len(assignments))
	}
}

func TestReassignIncident_WithEscalationPolicy(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P789"}}`)
	})

	_, err := tool.ReassignIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":          "P789",
		"requester_email":      "admin@example.com",
		"assignee_ids":         "PUSER1",
		"escalation_policy_id": "PEPOL1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	incident, ok := receivedBody["incident"].(map[string]interface{})
	if !ok {
		t.Fatal("expected incident in body")
	}
	ep, ok := incident["escalation_policy"].(map[string]interface{})
	if !ok {
		t.Fatal("expected escalation_policy in body")
	}
	if ep["id"] != "PEPOL1" {
		t.Errorf("expected escalation policy PEPOL1, got %v", ep["id"])
	}
}

func TestReassignIncident_MissingAssignees(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.ReassignIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P789",
		"requester_email": "admin@example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing assignee_ids")
	}
	if !strings.Contains(err.Error(), "assignee_ids is required") {
		t.Errorf("expected assignee_ids error, got: %v", err)
	}
}

func TestReassignIncident_EmptyAssignees(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.ReassignIncident(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P789",
		"requester_email": "admin@example.com",
		"assignee_ids":    "",
	})
	if err == nil {
		t.Fatal("expected error for empty assignee_ids")
	}
}

func TestAddIncidentNote_Success(t *testing.T) {
	var receivedMethod, receivedPath, receivedFrom string
	var receivedBody map[string]interface{}

	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedFrom = r.Header.Get("From")
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"note":{"id":"N1","content":"Investigating root cause"}}`)
	})

	result, err := tool.AddIncidentNote(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
		"content":         "Investigating root cause",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/incidents/P123/notes" {
		t.Errorf("expected /incidents/P123/notes, got %s", receivedPath)
	}
	if receivedFrom != "user@example.com" {
		t.Errorf("expected From user@example.com, got %s", receivedFrom)
	}
	if !strings.Contains(result, "Investigating root cause") {
		t.Errorf("expected result to contain note content, got %s", result)
	}

	note, ok := receivedBody["note"].(map[string]interface{})
	if !ok {
		t.Fatal("expected note in body")
	}
	if note["content"] != "Investigating root cause" {
		t.Errorf("expected content match, got %v", note["content"])
	}
}

func TestAddIncidentNote_MissingContent(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.AddIncidentNote(context.Background(), "test-incident", map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("expected content error, got: %v", err)
	}
}

func TestAddIncidentNote_MissingEmail(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.AddIncidentNote(context.Background(), "test-incident", map[string]interface{}{
		"incident_id": "P123",
		"content":     "A note",
	})
	if err == nil {
		t.Fatal("expected error for missing requester_email")
	}
	if !strings.Contains(err.Error(), "requester_email is required") {
		t.Errorf("expected requester_email error, got: %v", err)
	}
}

// --- SendEvent tests ---

func TestSendEvent_Trigger_Success(t *testing.T) {
	var receivedPath string
	var receivedBody map[string]interface{}
	var receivedAuth string

	tool, server := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"abc123"}`)
	})
	overrideEventsURL(t, server.URL)

	result, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "trigger",
		"summary":      "CPU usage over 90%",
		"severity":     "critical",
		"source":       "prod-web-01",
		"component":    "web-server",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedPath != "/v2/enqueue" {
		t.Errorf("expected /v2/enqueue, got %s", receivedPath)
	}
	// Events API should NOT have Authorization header
	if receivedAuth != "" {
		t.Errorf("expected no Authorization header for Events API, got %s", receivedAuth)
	}
	if !strings.Contains(result, "success") {
		t.Errorf("expected result to contain success, got %s", result)
	}

	if receivedBody["routing_key"] != "R0123456789" {
		t.Errorf("expected routing_key R0123456789, got %v", receivedBody["routing_key"])
	}
	if receivedBody["event_action"] != "trigger" {
		t.Errorf("expected event_action trigger, got %v", receivedBody["event_action"])
	}

	payload, ok := receivedBody["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("expected payload in body")
	}
	if payload["summary"] != "CPU usage over 90%" {
		t.Errorf("expected summary match, got %v", payload["summary"])
	}
	if payload["severity"] != "critical" {
		t.Errorf("expected severity critical, got %v", payload["severity"])
	}
	if payload["source"] != "prod-web-01" {
		t.Errorf("expected source prod-web-01, got %v", payload["source"])
	}
	if payload["component"] != "web-server" {
		t.Errorf("expected component web-server, got %v", payload["component"])
	}
}

func TestSendEvent_Acknowledge_Success(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, server := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"abc123"}`)
	})
	overrideEventsURL(t, server.URL)

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "acknowledge",
		"dedup_key":    "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedBody["event_action"] != "acknowledge" {
		t.Errorf("expected event_action acknowledge, got %v", receivedBody["event_action"])
	}
	if receivedBody["dedup_key"] != "abc123" {
		t.Errorf("expected dedup_key abc123, got %v", receivedBody["dedup_key"])
	}
}

func TestSendEvent_Resolve_Success(t *testing.T) {
	tool, server := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"abc123"}`)
	})
	overrideEventsURL(t, server.URL)

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "resolve",
		"dedup_key":    "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendEvent_MissingRoutingKey(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"event_action": "trigger",
		"summary":      "test",
	})
	if err == nil {
		t.Fatal("expected error for missing routing_key")
	}
	if !strings.Contains(err.Error(), "routing_key is required") {
		t.Errorf("expected routing_key error, got: %v", err)
	}
}

func TestSendEvent_MissingEventAction(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key": "R0123456789",
	})
	if err == nil {
		t.Fatal("expected error for missing event_action")
	}
	if !strings.Contains(err.Error(), "event_action is required") {
		t.Errorf("expected event_action error, got: %v", err)
	}
}

func TestSendEvent_InvalidEventAction(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid event_action")
	}
	if !strings.Contains(err.Error(), "invalid event_action") {
		t.Errorf("expected invalid event_action error, got: %v", err)
	}
}

func TestSendEvent_AcknowledgeMissingDedupKey(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "acknowledge",
	})
	if err == nil {
		t.Fatal("expected error for missing dedup_key on acknowledge")
	}
	if !strings.Contains(err.Error(), "dedup_key is required") {
		t.Errorf("expected dedup_key error, got: %v", err)
	}
}

func TestSendEvent_ResolveMissingDedupKey(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "resolve",
	})
	if err == nil {
		t.Fatal("expected error for missing dedup_key on resolve")
	}
	if !strings.Contains(err.Error(), "dedup_key is required") {
		t.Errorf("expected dedup_key error, got: %v", err)
	}
}

func TestSendEvent_TriggerMissingSummary(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "trigger",
	})
	if err == nil {
		t.Fatal("expected error for missing summary on trigger")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("expected summary error, got: %v", err)
	}
}

func TestSendEvent_TriggerInvalidSeverity(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "trigger",
		"summary":      "test",
		"severity":     "extreme",
	})
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
	if !strings.Contains(err.Error(), "invalid severity") {
		t.Errorf("expected severity error, got: %v", err)
	}
}

func TestSendEvent_TriggerDefaults(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, server := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"def456"}`)
	})

	overrideEventsURL(t, server.URL)

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "trigger",
		"summary":      "Test alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload, ok := receivedBody["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("expected payload in body")
	}
	// Default severity should be "error"
	if payload["severity"] != "error" {
		t.Errorf("expected default severity error, got %v", payload["severity"])
	}
	// Default source should be "akmatori"
	if payload["source"] != "akmatori" {
		t.Errorf("expected default source akmatori, got %v", payload["source"])
	}
}

func TestSendEvent_APIError(t *testing.T) {
	tool, server := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"status":"invalid event","message":"Event object is invalid"}`)
	})

	overrideEventsURL(t, server.URL)

	_, err := tool.SendEvent(context.Background(), "test-incident", map[string]interface{}{
		"routing_key":  "R0123456789",
		"event_action": "trigger",
		"summary":      "Test",
	})
	if err == nil {
		t.Fatal("expected error for API error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 error, got: %v", err)
	}
}

// --- Action operations are NOT cached tests ---

func TestAcknowledgeIncident_NotCached(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"incident":{"id":"P123","status":"acknowledged"}}`)
	})

	args := map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
	}

	_, err := tool.AcknowledgeIncident(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.AcknowledgeIncident(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 API calls (actions should NOT be cached), got %d", callCount.Load())
	}
}

func TestAddIncidentNote_NotCached(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"note":{"id":"N1","content":"test"}}`)
	})

	args := map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
		"content":         "test note",
	}

	_, err := tool.AddIncidentNote(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.AddIncidentNote(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 API calls (notes should NOT be cached), got %d", callCount.Load())
	}
}

// --- doRequestWithHeaders tests ---

func TestDoRequestWithHeaders_EmptyToken(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: "http://localhost", APIToken: ""}
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("expected token error, got %v", err)
	}
}

func TestDoRequestWithHeaders_QueryParams(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}

	params := url.Values{"foo": {"bar"}, "baz": {"qux"}}
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodGet, "/test", params, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedQuery, "foo=bar") {
		t.Errorf("expected query params, got %q", receivedQuery)
	}
}

func TestDoRequestWithHeaders_ExtraHeaders(t *testing.T) {
	var receivedFrom, receivedCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedFrom = r.Header.Get("From")
		receivedCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "test-token", VerifySSL: true, Timeout: 5}
	headers := map[string]string{
		"From":     "test@example.com",
		"X-Custom": "value",
	}

	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedFrom != "test@example.com" {
		t.Errorf("expected From header, got %q", receivedFrom)
	}
	if receivedCustom != "value" {
		t.Errorf("expected X-Custom header, got %q", receivedCustom)
	}
}

func TestDoRequestWithHeaders_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"Forbidden"}}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "test-token", VerifySSL: true, Timeout: 5}
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP error 403") {
		t.Errorf("expected HTTP 403 error, got %v", err)
	}
}

func TestDoRequestWithHeaders_SSLDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "test-token", VerifySSL: false, Timeout: 5}
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequestWithHeaders_ProxyEnabled(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       "http://localhost:1",
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   1,
		UseProxy:  true,
		ProxyURL:  "http://proxy.example.com:8080",
	}

	// The request will fail because the proxy doesn't exist, but we cover the proxy code path
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err == nil {
		t.Error("expected error with non-existent proxy")
	}
}

func TestDoRequestWithHeaders_InvalidProxyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
		UseProxy:  true,
		ProxyURL:  "://invalid-url",
	}

	// Invalid proxy URL should proceed without proxy and succeed
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err != nil {
		t.Fatalf("expected success with invalid proxy URL (fallback to no proxy), got %v", err)
	}
}

func TestDoRequestWithHeaders_RateLimiting(t *testing.T) {
	limiter := ratelimit.New(100, 100)
	tool := NewPagerDutyTool(testLogger(), limiter)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)
	defer tool.Stop()

	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequestWithHeaders_CancelledContext(t *testing.T) {
	limiter := ratelimit.New(1, 1)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       "http://localhost",
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.doRequestWithHeaders(ctx, config, http.MethodPut, "/test", nil, nil, nil)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestDoRequestWithHeaders_ResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		data := strings.Repeat("x", 6*1024*1024)
		fmt.Fprint(w, data)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "test-token", VerifySSL: true, Timeout: 10}
	_, err := tool.doRequestWithHeaders(context.Background(), config, http.MethodPut, "/test", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size limit error, got %v", err)
	}
}

// --- doRequestNoAuth tests ---

func TestDoRequestNoAuth_Success(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"abc"}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "should-not-appear",
		VerifySSL: true,
		Timeout:   5,
	}

	resp, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/v2/enqueue", nil, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedAuth != "" {
		t.Errorf("expected no Authorization header, got %q", receivedAuth)
	}
	if !strings.Contains(string(resp), "success") {
		t.Errorf("expected success response, got %s", resp)
	}
}

func TestDoRequestNoAuth_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"status":"invalid_event"}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "tok", VerifySSL: true, Timeout: 5}
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/v2/enqueue", nil, strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "HTTP error 400") {
		t.Errorf("expected HTTP 400 error, got %v", err)
	}
}

func TestDoRequestNoAuth_QueryParams(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "tok", VerifySSL: true, Timeout: 5}
	params := url.Values{"key": {"val"}}
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", params, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedQuery, "key=val") {
		t.Errorf("expected query params, got %q", receivedQuery)
	}
}

func TestDoRequestNoAuth_SSLDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "tok", VerifySSL: false, Timeout: 5}
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequestNoAuth_ProxyEnabled(t *testing.T) {
	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       "http://localhost:1",
		APIToken:  "tok",
		VerifySSL: true,
		Timeout:   1,
		UseProxy:  true,
		ProxyURL:  "http://proxy.example.com:8080",
	}

	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err == nil {
		t.Error("expected error with non-existent proxy")
	}
}

func TestDoRequestNoAuth_InvalidProxyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "tok",
		VerifySSL: true,
		Timeout:   5,
		UseProxy:  true,
		ProxyURL:  "://invalid",
	}

	// Invalid proxy falls back to no proxy
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("expected success with invalid proxy fallback, got %v", err)
	}
}

func TestDoRequestNoAuth_RateLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	limiter := ratelimit.New(100, 100)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "tok", VerifySSL: true, Timeout: 5}
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequestNoAuth_CancelledContext(t *testing.T) {
	limiter := ratelimit.New(1, 1)
	tool := NewPagerDutyTool(testLogger(), limiter)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: "http://localhost", APIToken: "tok", VerifySSL: true, Timeout: 5}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.doRequestNoAuth(ctx, config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestDoRequestNoAuth_ResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		data := strings.Repeat("x", 6*1024*1024)
		fmt.Fprint(w, data)
	}))
	defer server.Close()

	tool := NewPagerDutyTool(testLogger(), nil)
	defer tool.Stop()

	config := &PagerDutyConfig{URL: server.URL, APIToken: "tok", VerifySSL: true, Timeout: 5}
	_, err := tool.doRequestNoAuth(context.Background(), config, http.MethodPost, "/test", nil, strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size limit error, got %v", err)
	}
}

// --- doRequest additional tests ---

func TestDoRequest_ProxyEnabled(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	config := &PagerDutyConfig{
		URL:       "http://localhost:1",
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   1,
		UseProxy:  true,
		ProxyURL:  "http://proxy.example.com:8080",
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Error("expected error with non-existent proxy")
	}
}

func TestDoRequest_InvalidProxyURL(t *testing.T) {
	tool, server, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	})

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
		UseProxy:  true,
		ProxyURL:  "://invalid",
	}

	// Invalid proxy falls back to no proxy
	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatalf("expected success with invalid proxy fallback, got %v", err)
	}
}

func TestDoRequest_LongErrorTruncation(t *testing.T) {
	tool, server, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, strings.Repeat("error text ", 100))
	})

	config := &PagerDutyConfig{
		URL:       server.URL,
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected truncated error message, got %v", err)
	}
}

// --- Additional edge case tests ---

func TestReassignIncident_MissingIncidentID(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	args := map[string]interface{}{
		"requester_email": "user@example.com",
		"assignee_ids":    "PUSER1",
	}

	_, err := tool.ReassignIncident(context.Background(), "test-incident", args)
	if err == nil || !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected incident_id required error, got %v", err)
	}
}

func TestReassignIncident_MissingEmail(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	args := map[string]interface{}{
		"incident_id":  "P123",
		"assignee_ids": "PUSER1",
	}

	_, err := tool.ReassignIncident(context.Background(), "test-incident", args)
	if err == nil || !strings.Contains(err.Error(), "requester_email is required") {
		t.Errorf("expected requester_email required error, got %v", err)
	}
}

func TestReassignIncident_APIError(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"error":{"message":"Invalid assignment"}}`)
	})

	args := map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
		"assignee_ids":    "PUSER1",
	}

	_, err := tool.ReassignIncident(context.Background(), "test-incident", args)
	if err == nil || !strings.Contains(err.Error(), "HTTP error 422") {
		t.Errorf("expected HTTP 422 error, got %v", err)
	}
}

func TestAddIncidentNote_MissingIncidentID(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	args := map[string]interface{}{
		"requester_email": "user@example.com",
		"content":         "test note",
	}

	_, err := tool.AddIncidentNote(context.Background(), "test-incident", args)
	if err == nil || !strings.Contains(err.Error(), "incident_id is required") {
		t.Errorf("expected incident_id required error, got %v", err)
	}
}

func TestAddIncidentNote_APIError(t *testing.T) {
	tool, _ := newTestToolWithHeaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"Forbidden"}}`)
	})

	args := map[string]interface{}{
		"incident_id":     "P123",
		"requester_email": "user@example.com",
		"content":         "test note",
	}

	_, err := tool.AddIncidentNote(context.Background(), "test-incident", args)
	if err == nil || !strings.Contains(err.Error(), "HTTP error 403") {
		t.Errorf("expected HTTP 403 error, got %v", err)
	}
}

func TestSendEvent_UsesFixedEventsAPIURL(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"success","dedup_key":"abc123"}`)
	}))
	defer server.Close()

	overrideEventsURL(t, server.URL)

	tool := NewPagerDutyTool(testLogger(), nil)
	config := &PagerDutyConfig{
		URL:       "http://should-not-be-used-for-events",
		APIToken:  "test-token",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)
	defer tool.Stop()

	args := map[string]interface{}{
		"routing_key":  "test-key",
		"event_action": "trigger",
		"summary":      "Test alert",
		"source":       "test",
		"severity":     "critical",
	}

	result, err := tool.SendEvent(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedPath != "/v2/enqueue" {
		t.Errorf("expected /v2/enqueue path, got %s", receivedPath)
	}
	if !strings.Contains(result, "success") {
		t.Errorf("expected success in result, got %s", result)
	}
}

