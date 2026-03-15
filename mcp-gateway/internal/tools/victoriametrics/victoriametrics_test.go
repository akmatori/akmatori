package victoriametrics

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

func uintPtr(v uint) *uint {
	return &v
}

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// newTestTool creates a VictoriaMetricsTool with an httptest server's URL pre-populated in the config cache.
// Returns the tool, the test server, and a request counter.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*VictoriaMetricsTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "none",
		VerifySSL:  true,
		Timeout:    5,
	}
	// Pre-populate config cache so getConfig doesn't hit the database
	tool.configCache.Set(configCacheKey("test-incident"), config)

	t.Cleanup(func() {
		tool.Stop()
		server.Close()
	})

	return tool, server, counter
}

// successResponse builds a valid Prometheus-format JSON response body.
func successResponse(data interface{}) string {
	dataJSON, _ := json.Marshal(data)
	return fmt.Sprintf(`{"status":"success","data":%s}`, string(dataJSON))
}

// --- Unit tests for parsePrometheusResponse ---

func TestParsePrometheusResponse_Success(t *testing.T) {
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1234,"1"]}]}}`)

	data, err := parsePrometheusResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}

	if parsed["resultType"] != "vector" {
		t.Errorf("expected resultType 'vector', got %v", parsed["resultType"])
	}
}

func TestParsePrometheusResponse_Error(t *testing.T) {
	body := []byte(`{"status":"error","errorType":"bad_data","error":"invalid query"}`)

	_, err := parsePrometheusResponse(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expected := "VictoriaMetrics API error (bad_data): invalid query"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParsePrometheusResponse_MalformedJSON(t *testing.T) {
	body := []byte(`not json at all`)

	_, err := parsePrometheusResponse(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParsePrometheusResponse_EmptyData(t *testing.T) {
	body := []byte(`{"status":"success","data":null}`)

	data, err := parsePrometheusResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(data) != "null" {
		t.Errorf("expected null data, got %s", string(data))
	}
}

func TestParsePrometheusResponse_ErrorWithoutType(t *testing.T) {
	body := []byte(`{"status":"error","error":"something went wrong"}`)

	_, err := parsePrometheusResponse(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("expected error to contain message, got %q", err.Error())
	}
}

// --- Unit tests for extractInstanceID ---

func TestExtractInstanceID(t *testing.T) {
	tests := []struct {
		name   string
		args   map[string]interface{}
		wantID *uint
	}{
		{
			name:   "valid instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(5)},
			wantID: uintPtr(5),
		},
		{
			name:   "zero instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(0)},
			wantID: nil,
		},
		{
			name:   "negative instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(-1)},
			wantID: nil,
		},
		{
			name:   "missing instance ID",
			args:   map[string]interface{}{},
			wantID: nil,
		},
		{
			name:   "wrong type",
			args:   map[string]interface{}{"tool_instance_id": "5"},
			wantID: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInstanceID(tt.args)
			if tt.wantID == nil {
				if got != nil {
					t.Errorf("expected nil, got %d", *got)
				}
			} else {
				if got == nil {
					t.Errorf("expected %d, got nil", *tt.wantID)
				} else if *got != *tt.wantID {
					t.Errorf("expected %d, got %d", *tt.wantID, *got)
				}
			}
		})
	}
}

// --- Unit tests for cache keys ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:victoria_metrics"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"query": []string{"up"}}
	params2 := url.Values{"query": []string{"down"}}

	key1 := responseCacheKey("/api/v1/query", params1)
	key2 := responseCacheKey("/api/v1/query", params2)
	key3 := responseCacheKey("/api/v1/query", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- Constructor and lifecycle tests ---

func TestNewVictoriaMetricsTool(t *testing.T) {
	logger := testLogger()
	tool := NewVictoriaMetricsTool(logger, nil)

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

func TestNewVictoriaMetricsTool_WithRateLimiter(t *testing.T) {
	logger := testLogger()
	limiter := ratelimit.New(10, 20)
	tool := NewVictoriaMetricsTool(logger, limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	logger := testLogger()
	tool := NewVictoriaMetricsTool(logger, nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Unit tests for getConfig ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	expected := &VMConfig{
		URL:        "https://vm.example.com",
		AuthMethod: "bearer_token",
		BearerToken: "my-token",
		VerifySSL:  true,
		Timeout:    30,
	}
	tool.configCache.Set(configCacheKey("incident-1"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
	if config.BearerToken != expected.BearerToken {
		t.Errorf("expected BearerToken %q, got %q", expected.BearerToken, config.BearerToken)
	}
}

func TestGetConfig_CacheHitByInstanceID(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	expected := &VMConfig{
		URL:        "https://vm-instance.example.com",
		AuthMethod: "basic_auth",
		Username:   "admin",
		Password:   "secret",
	}
	instanceID := uintPtr(42)
	tool.configCache.Set(fmt.Sprintf("creds:instance:%d", *instanceID), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", instanceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, config.URL)
	}
	if config.Username != expected.Username {
		t.Errorf("expected Username %q, got %q", expected.Username, config.Username)
	}
}


// --- Timeout clamping tests ---

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    int
	}{
		{"zero is clamped to default", 0, 30},
		{"negative is clamped to default", -5, 30},
		{"excessive is clamped to max", 999, 300},
		{"301 is clamped to max", 301, 300},
		{"valid timeout 1 is kept", 1, 1},
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

// --- Auth header injection tests ---

func TestDoRequest_BearerTokenAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "bearer_token",
		BearerToken: "test-token-123",
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Bearer test-token-123"
	if receivedAuth != expected {
		t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
	}
}

func TestDoRequest_BasicAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "basic_auth",
		Username:   "admin",
		Password:   "secret",
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth == "" {
		t.Error("expected Authorization header to be set for basic auth")
	}
	if !strings.HasPrefix(receivedAuth, "Basic ") {
		t.Errorf("expected Basic auth prefix, got %q", receivedAuth)
	}
}

func TestDoRequest_NoAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "none",
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "" {
		t.Errorf("expected no Authorization header for 'none' auth, got %q", receivedAuth)
	}
}

func TestDoRequest_BearerTokenEmptyString(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        "http://localhost:9999",
		AuthMethod: "bearer_token",
		BearerToken: "", // Empty token
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error for empty bearer token")
	}
	if !strings.Contains(err.Error(), "no token configured") {
		t.Errorf("expected error about missing token, got: %v", err)
	}
}

func TestDoRequest_BasicAuthEmptyUsername(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        "http://localhost:9999",
		AuthMethod: "basic_auth",
		Username:   "",
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error for empty username")
	}
	if !strings.Contains(err.Error(), "no username configured") {
		t.Errorf("expected error about missing username, got: %v", err)
	}
}

func TestDoRequest_UnknownAuthMethod(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        "http://localhost:9999",
		AuthMethod: "kerberos",
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error for unknown auth method")
	}
	if !strings.Contains(err.Error(), "unknown auth_method") {
		t.Errorf("expected error about unknown auth method, got: %v", err)
	}
}

// --- doRequest HTTP error handling ---

func TestDoRequest_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{URL: server.URL, AuthMethod: "none", Timeout: 5}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status code, got %q", err.Error())
	}
}

func TestDoRequest_POSTSendsFormBody(t *testing.T) {
	var receivedContentType string
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{URL: server.URL, AuthMethod: "none", Timeout: 5}
	params := url.Values{"query": []string{"up"}}

	_, err := tool.doRequest(context.Background(), config, http.MethodPost, "/api/v1/query", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedContentType != "application/x-www-form-urlencoded" {
		t.Errorf("expected form content type, got %q", receivedContentType)
	}
	if !strings.Contains(receivedBody, "query=up") {
		t.Errorf("expected body to contain 'query=up', got %q", receivedBody)
	}
}

func TestDoRequest_GETAppendsQueryParams(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{URL: server.URL, AuthMethod: "none", Timeout: 5}
	params := url.Values{"query": []string{"up"}, "time": []string{"1234"}}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/v1/query", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedURL, "query=up") {
		t.Errorf("expected URL to contain 'query=up', got %q", receivedURL)
	}
	if !strings.Contains(receivedURL, "time=1234") {
		t.Errorf("expected URL to contain 'time=1234', got %q", receivedURL)
	}
}

// --- InstantQuery tests ---

func TestInstantQuery_Success(t *testing.T) {
	responseData := map[string]interface{}{
		"resultType": "vector",
		"result": []map[string]interface{}{
			{"metric": map[string]string{"__name__": "up"}, "value": []interface{}{1234, "1"}},
		},
	}

	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("expected path /api/v1/query, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, successResponse(responseData))
	})

	result, err := tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "up",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "vector") {
		t.Errorf("expected result to contain 'vector', got %s", result)
	}
}

func TestInstantQuery_MissingQuery(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.InstantQuery(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected 'query is required' error, got %q", err.Error())
	}
}

func TestInstantQuery_WithOptionalParams(t *testing.T) {
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})

	_, err := tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{
		"query":   "up",
		"time":    "2024-01-01T00:00:00Z",
		"step":    "15s",
		"timeout": "10s",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedBody, "time=") {
		t.Error("expected 'time' param in request body")
	}
	if !strings.Contains(receivedBody, "step=") {
		t.Error("expected 'step' param in request body")
	}
	if !strings.Contains(receivedBody, "timeout=") {
		t.Error("expected 'timeout' param in request body")
	}
}

// --- RangeQuery tests ---

func TestRangeQuery_Success(t *testing.T) {
	responseData := map[string]interface{}{
		"resultType": "matrix",
		"result": []map[string]interface{}{
			{
				"metric": map[string]string{"__name__": "http_requests_total"},
				"values": [][]interface{}{{1234, "100"}, {1235, "150"}},
			},
		},
	}

	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("expected path /api/v1/query_range, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, successResponse(responseData))
	})

	result, err := tool.RangeQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "rate(http_requests_total[5m])",
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-01T01:00:00Z",
		"step":  "1m",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "matrix") {
		t.Errorf("expected result to contain 'matrix', got %s", result)
	}
}

func TestRangeQuery_MissingRequiredParams(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{
			name: "missing query",
			args: map[string]interface{}{"start": "1h", "end": "now", "step": "1m"},
			want: "query is required",
		},
		{
			name: "missing start",
			args: map[string]interface{}{"query": "up", "end": "now", "step": "1m"},
			want: "start is required",
		},
		{
			name: "missing end",
			args: map[string]interface{}{"query": "up", "start": "1h", "step": "1m"},
			want: "end is required",
		},
		{
			name: "missing step",
			args: map[string]interface{}{"query": "up", "start": "1h", "end": "now"},
			want: "step is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.RangeQuery(context.Background(), "test", tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestRangeQuery_VerifyRequestParams(t *testing.T) {
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	})

	_, err := tool.RangeQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "up",
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-01T01:00:00Z",
		"step":  "1m",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, param := range []string{"query=up", "step=1m"} {
		if !strings.Contains(receivedBody, param) {
			t.Errorf("expected body to contain %q, got %q", param, receivedBody)
		}
	}
}

// --- LabelValues tests ---

func TestLabelValues_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/label/__name__/values") {
			t.Errorf("expected path containing /api/v1/label/__name__/values, got %s", r.URL.Path)
		}
		// LabelValues uses GET
		if r.Method != http.MethodGet {
			t.Errorf("expected GET method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":["up","node_cpu_seconds_total","node_memory_MemTotal_bytes"]}`)
	})

	result, err := tool.LabelValues(context.Background(), "test-incident", map[string]interface{}{
		"label_name": "__name__",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "up") {
		t.Errorf("expected result to contain 'up', got %s", result)
	}
}

func TestLabelValues_URLPathConstruction(t *testing.T) {
	var receivedPath string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	})

	_, err := tool.LabelValues(context.Background(), "test-incident", map[string]interface{}{
		"label_name": "instance",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "/api/v1/label/instance/values"
	if receivedPath != expected {
		t.Errorf("expected path %q, got %q", expected, receivedPath)
	}
}

func TestLabelValues_SpecialCharacterInLabelName(t *testing.T) {
	var receivedPath string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	})

	_, err := tool.LabelValues(context.Background(), "test-incident", map[string]interface{}{
		"label_name": "__name__",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "/api/v1/label/__name__/values"
	if receivedPath != expected {
		t.Errorf("expected path %q, got %q", expected, receivedPath)
	}
}

func TestLabelValues_MissingLabelName(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.LabelValues(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing label_name")
	}
	if !strings.Contains(err.Error(), "label_name is required") {
		t.Errorf("expected 'label_name is required' error, got %q", err.Error())
	}
}

func TestLabelValues_WithOptionalParams(t *testing.T) {
	var receivedURL string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	})

	_, err := tool.LabelValues(context.Background(), "test-incident", map[string]interface{}{
		"label_name": "job",
		"match":      "up",
		"start":      "2024-01-01T00:00:00Z",
		"end":        "2024-01-01T01:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// GET request should have query params in URL
	if !strings.Contains(receivedURL, "match") {
		t.Error("expected 'match' param in URL")
	}
	if !strings.Contains(receivedURL, "start=") {
		t.Error("expected 'start' param in URL")
	}
	if !strings.Contains(receivedURL, "end=") {
		t.Error("expected 'end' param in URL")
	}
}

// --- Series tests ---

func TestSeries_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/series" {
			t.Errorf("expected path /api/v1/series, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[{"__name__":"up","job":"prometheus","instance":"localhost:9090"}]}`)
	})

	result, err := tool.Series(context.Background(), "test-incident", map[string]interface{}{
		"match": "up",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "prometheus") {
		t.Errorf("expected result to contain 'prometheus', got %s", result)
	}
}

func TestSeries_MissingMatch(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.Series(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing match")
	}
	if !strings.Contains(err.Error(), "match is required") {
		t.Errorf("expected 'match is required' error, got %q", err.Error())
	}
}

func TestSeries_VerifyMatchParam(t *testing.T) {
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	})

	_, err := tool.Series(context.Background(), "test-incident", map[string]interface{}{
		"match": "{job=\"prometheus\"}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// match[] should be in the form body (POST request)
	if !strings.Contains(receivedBody, "match") {
		t.Errorf("expected body to contain 'match', got %q", receivedBody)
	}
}

// --- APIRequest tests ---

func TestAPIRequest_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status/tsdb" {
			t.Errorf("expected path /api/v1/status/tsdb, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"totalSeries":1000}}`)
	})

	result, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/status/tsdb",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "totalSeries") {
		t.Errorf("expected result to contain 'totalSeries', got %s", result)
	}
}

func TestAPIRequest_CustomMethod(t *testing.T) {
	var receivedMethod string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path":   "/api/v1/admin/snapshot",
		"method": "post",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST method, got %s", receivedMethod)
	}
}

func TestAPIRequest_WithParams(t *testing.T) {
	var receivedURL string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path":   "/api/v1/export",
		"method": "GET",
		"params": map[string]interface{}{
			"match": "{job=\"test\"}",
			"start": "1h",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedURL, "match=") {
		t.Error("expected 'match' param in URL")
	}
}

func TestAPIRequest_MissingPath(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.APIRequest(context.Background(), "test", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected 'path is required' error, got %q", err.Error())
	}
}

func TestAPIRequest_NonPrometheusResponse(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"version":"1.0","uptime":"24h"}`)
	})

	result, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/v1/status/buildinfo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should fall back to raw body when response isn't Prometheus format
	if !strings.Contains(result, "version") {
		t.Errorf("expected raw body to be returned, got %s", result)
	}
}

func TestAPIRequest_DefaultsToGET(t *testing.T) {
	var receivedMethod string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != http.MethodGet {
		t.Errorf("expected default method GET, got %s", receivedMethod)
	}
}

// --- Response caching tests ---

func TestResponseCaching_CacheHit(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})

	args := map[string]interface{}{"query": "up"}

	// First call - should hit the server
	result1, err := tool.InstantQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Second call with same args - should use cache
	result2, err := tool.InstantQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if result1 != result2 {
		t.Error("expected same result from cache")
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request (second should be cached), got %d", counter.Load())
	}
}

func TestResponseCaching_DifferentQueryNotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})

	// Different queries should each hit the server
	_, err := tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{"query": "up"})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	_, err = tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{"query": "down"})
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests for different queries, got %d", counter.Load())
	}
}

func TestAPIRequest_NotCached(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	args := map[string]interface{}{"path": "/api/v1/status/tsdb"}

	// Call twice - both should hit the server since APIRequest is not cached
	_, err := tool.APIRequest(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	_, err = tool.APIRequest(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if counter.Load() != 2 {
		t.Errorf("expected 2 HTTP requests (APIRequest should not cache), got %d", counter.Load())
	}
}

func TestResponseCaching_RangeQuery(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	})

	args := map[string]interface{}{
		"query": "up",
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-01T01:00:00Z",
		"step":  "1m",
	}

	_, err := tool.RangeQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	_, err = tool.RangeQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 HTTP request for cached range query, got %d", counter.Load())
	}
}

func TestResponseCaching_DifferentIncidentsNotShared(t *testing.T) {
	callCount := &atomic.Int32{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "none",
		VerifySSL:  true,
		Timeout:    5,
	}
	// Set config for two different incidents
	tool.configCache.Set(configCacheKey("incident-A"), config)
	tool.configCache.Set(configCacheKey("incident-B"), config)

	args := map[string]interface{}{"query": "up"}

	_, err := tool.InstantQuery(context.Background(), "incident-A", args)
	if err != nil {
		t.Fatalf("incident-A call error: %v", err)
	}

	_, err = tool.InstantQuery(context.Background(), "incident-B", args)
	if err != nil {
		t.Fatalf("incident-B call error: %v", err)
	}

	// Different incidents should NOT share cache even with same query
	if callCount.Load() != 2 {
		t.Errorf("expected 2 HTTP requests for different incidents, got %d", callCount.Load())
	}
}

// --- Rate limiter integration tests ---

func TestRateLimiter_Integration(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	// Attach a rate limiter
	limiter := ratelimit.New(10, 20)
	tool.rateLimiter = limiter

	config := &VMConfig{
		URL:        "unused", // won't actually connect
		AuthMethod: "none",
		Timeout:    5,
	}

	// Verify the limiter allows requests through
	initialTokens := limiter.Tokens()
	if initialTokens <= 0 {
		t.Fatal("expected positive initial tokens")
	}

	// Use doRequest with the server to verify rate limiter is invoked
	// Get the config from cache first
	cachedConfig, ok := tool.configCache.Get(configCacheKey("test-incident"))
	if !ok {
		t.Fatal("expected config in cache")
	}
	config = cachedConfig.(*VMConfig)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tokens should have decreased
	afterTokens := limiter.Tokens()
	if afterTokens >= initialTokens {
		t.Error("expected token count to decrease after request")
	}
}

func TestRateLimiter_ContextCancellation(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	// Create a rate limiter with 0 burst so it immediately blocks
	limiter := ratelimit.New(0.001, 0) // Very slow refill, 0 burst
	tool.rateLimiter = limiter

	// Drain all tokens
	for limiter.Allow() {
		// drain
	}

	config := &VMConfig{
		URL:        "http://localhost:9999",
		AuthMethod: "none",
		Timeout:    5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := tool.doRequest(ctx, config, http.MethodGet, "/test", nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "rate limit wait cancelled") {
		t.Errorf("expected rate limit cancellation error, got %q", err.Error())
	}
}

// --- Config empty URL test ---

func TestCachedRequest_EmptyURL(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	// Pre-populate cache with config that has empty URL
	config := &VMConfig{
		URL:        "",
		AuthMethod: "none",
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	_, err := tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "up",
	})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "URL not configured") {
		t.Errorf("expected URL not configured error, got %q", err.Error())
	}
}

// --- Series with optional params ---

func TestSeries_WithStartAndEnd(t *testing.T) {
	var receivedBody string
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	})

	_, err := tool.Series(context.Background(), "test-incident", map[string]interface{}{
		"match": "up",
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-01T01:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedBody, "start=") {
		t.Error("expected 'start' param in body")
	}
	if !strings.Contains(receivedBody, "end=") {
		t.Error("expected 'end' param in body")
	}
}

// --- doRequest proxy and SSL tests ---

func TestDoRequest_SSLVerifyDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	}))
	defer server.Close()

	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        server.URL,
		AuthMethod: "none",
		VerifySSL:  false,
		Timeout:    5,
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- cachedRequest with instanceID ---

func TestCachedRequest_WithInstanceID(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})

	// Also set config for instance ID
	config := &VMConfig{
		URL:        "",
		AuthMethod: "none",
		Timeout:    5,
	}
	// Get the URL from the cached config
	cached, _ := tool.configCache.Get(configCacheKey("test-incident"))
	config = cached.(*VMConfig)
	instanceID := uintPtr(42)
	tool.configCache.Set(fmt.Sprintf("creds:instance:%d", *instanceID), config)

	args := map[string]interface{}{
		"query":            "up",
		"tool_instance_id": float64(42),
	}

	// First call
	_, err := tool.InstantQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Second call should cache
	_, err = tool.InstantQuery(context.Background(), "test-incident", args)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 HTTP request with instance ID caching, got %d", callCount.Load())
	}
}

// --- APIRequest empty URL test ---

func TestAPIRequest_EmptyURL(t *testing.T) {
	tool := NewVictoriaMetricsTool(testLogger(), nil)
	defer tool.Stop()

	config := &VMConfig{
		URL:        "",
		AuthMethod: "none",
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/test",
	})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "URL not configured") {
		t.Errorf("expected URL not configured error, got %q", err.Error())
	}
}

// --- APIRequest security validation tests ---

func TestAPIRequest_RejectsUnsupportedHTTPMethods(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	for _, method := range []string{"DELETE", "PUT", "PATCH"} {
		_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
			"path":   "/api/v1/query",
			"method": method,
		})
		if err == nil {
			t.Errorf("expected error for HTTP method %s", method)
		}
		if err != nil && !strings.Contains(err.Error(), "unsupported HTTP method") {
			t.Errorf("expected unsupported method error for %s, got %q", method, err.Error())
		}
	}
}

func TestAPIRequest_AllowsGETAndPOST(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	for _, method := range []string{"GET", "POST", "get", "post"} {
		_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
			"path":   "/api/v1/query",
			"method": method,
		})
		if err != nil {
			t.Errorf("expected no error for HTTP method %s, got %v", method, err)
		}
	}
}

func TestAPIRequest_RejectsPathTraversal(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"success","data":"ok"}`)
	})

	badPaths := []string{
		"../etc/passwd",
		"/api/v1/../../../secret",
		"relative/path",
		"/api/v1/%2e%2e/%2e%2e/etc/passwd",
		"/api/v1/..%2F..%2Fetc",
		"/api/v1/query?query=injected",
		"/api/v1/query#fragment",
	}
	for _, path := range badPaths {
		_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
			"path": path,
		})
		if err == nil {
			t.Errorf("expected error for path %q", path)
		}
	}
}

// --- Error propagation from VictoriaMetrics API ---

func TestInstantQuery_VMAPIError(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"parse error at char 5"}`)
	})

	_, err := tool.InstantQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "invalid{{{",
	})
	if err == nil {
		t.Fatal("expected error from VM API")
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Errorf("expected parse error, got %q", err.Error())
	}
}
