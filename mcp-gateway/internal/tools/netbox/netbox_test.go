package netbox

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

// newTestTool creates a NetBoxTool with an httptest server's URL pre-populated in the config cache.
// Returns the tool, the test server, and a request counter.
func newTestTool(t *testing.T, handler http.HandlerFunc) (*NetBoxTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewNetBoxTool(testLogger(), nil)
	config := &NetBoxConfig{
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

func TestNewNetBoxTool(t *testing.T) {
	logger := testLogger()
	tool := NewNetBoxTool(logger, nil)

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

func TestNewNetBoxTool_WithRateLimiter(t *testing.T) {
	logger := testLogger()
	limiter := ratelimit.New(10, 20)
	tool := NewNetBoxTool(logger, limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop(t *testing.T) {
	logger := testLogger()
	tool := NewNetBoxTool(logger, nil)
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:netbox"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"name": []string{"server1"}}
	params2 := url.Values{"name": []string{"server2"}}

	key1 := responseCacheKey("/api/dcim/devices/", params1)
	key2 := responseCacheKey("/api/dcim/devices/", params2)
	key3 := responseCacheKey("/api/dcim/devices/", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- getConfig tests ---

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewNetBoxTool(testLogger(), nil)
	defer tool.Stop()

	expected := &NetBoxConfig{
		URL:       "https://netbox.example.com",
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
	tool := NewNetBoxTool(testLogger(), nil)
	defer tool.Stop()

	expected := &NetBoxConfig{
		URL:      "https://netbox-prod.example.com",
		APIToken: "prod-token",
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "netbox", "prod-nb"), expected)

	config, err := tool.getConfig(context.Background(), "incident-1", "prod-nb")
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

func getTestConfig(tool *NetBoxTool) *NetBoxConfig {
	cached, ok := tool.configCache.Get(configCacheKey("test-incident"))
	if !ok {
		return nil
	}
	return cached.(*NetBoxConfig)
}

func TestDoRequest_TokenAuth(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Token test-token" {
			t.Errorf("expected 'Token test-token', got %q", auth)
		}
		accept := r.Header.Get("Accept")
		if accept != "application/json" {
			t.Errorf("expected Accept 'application/json', got %q", accept)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/dcim/devices/", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_EmptyToken(t *testing.T) {
	tool := NewNetBoxTool(testLogger(), nil)
	defer tool.Stop()

	config := &NetBoxConfig{
		URL:      "http://localhost",
		APIToken: "",
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/dcim/devices/", nil)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Errorf("expected token error, got: %v", err)
	}
}

func TestDoRequest_HTTPError(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"detail":"Not found."}`)
	})

	config := getTestConfig(tool)

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/dcim/devices/999/", nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "HTTP error 404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestDoRequest_QueryParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "web-server" {
			t.Errorf("expected name=web-server, got %q", r.URL.Query().Get("name"))
		}
		if r.URL.Query().Get("site") != "dc1" {
			t.Errorf("expected site=dc1, got %q", r.URL.Query().Get("site"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	config := getTestConfig(tool)
	params := url.Values{
		"name": []string{"web-server"},
		"site": []string{"dc1"},
	}

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/dcim/devices/", params)
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

	_, err := tool.doRequest(context.Background(), config, http.MethodGet, "/api/dcim/devices/", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counter.Load() != 1 {
		t.Errorf("expected 1 request, got %d", counter.Load())
	}
}

// --- cachedGet tests ---

func TestCachedGet_CachesResponse(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[{"id":1,"name":"server1"}]}`)
	})

	ctx := context.Background()

	// First call - should hit the server
	result1, err := tool.cachedGet(ctx, "test-incident", "/api/dcim/devices/", nil, DCIMCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call - should hit the cache
	result2, err := tool.cachedGet(ctx, "test-incident", "/api/dcim/devices/", nil, DCIMCacheTTL)
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
		fmt.Fprint(w, `{"results":[]}`)
	})

	// Pre-populate a second config for logical name lookup
	config2 := &NetBoxConfig{
		URL:       getTestConfig(tool).URL,
		APIToken:  "test-token-2",
		VerifySSL: true,
		Timeout:   5,
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "netbox", "prod-nb"), config2)

	ctx := context.Background()

	// Fetch with incident-based key
	_, err := tool.cachedGet(ctx, "test-incident", "/api/dcim/devices/", nil, DCIMCacheTTL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fetch with logical name key - should be separate cache entry
	_, err = tool.cachedGet(ctx, "test-incident", "/api/dcim/devices/", nil, DCIMCacheTTL, "prod-nb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Helper function tests ---

func TestAddPaginationParams(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		wantKeys map[string]string
	}{
		{
			"with limit and offset",
			map[string]interface{}{"limit": float64(50), "offset": float64(100)},
			map[string]string{"limit": "50", "offset": "100"},
		},
		{
			"limit clamped to 1000",
			map[string]interface{}{"limit": float64(2000)},
			map[string]string{"limit": "1000"},
		},
		{
			"zero limit not set",
			map[string]interface{}{"limit": float64(0)},
			map[string]string{},
		},
		{
			"no params",
			map[string]interface{}{},
			map[string]string{},
		},
		{
			"offset zero is set",
			map[string]interface{}{"offset": float64(0)},
			map[string]string{"offset": "0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{}
			addPaginationParams(params, tt.args)

			for k, v := range tt.wantKeys {
				if got := params.Get(k); got != v {
					t.Errorf("expected %s=%q, got %q", k, v, got)
				}
			}
		})
	}
}

func TestAddSearchParams(t *testing.T) {
	args := map[string]interface{}{
		"name":   "web-server",
		"site":   "dc1",
		"q":      "search-term",
		"empty":  "",
		"number": float64(42),
	}

	params := url.Values{}
	addSearchParams(params, args, "name", "site", "q", "empty", "number", "missing")

	if params.Get("name") != "web-server" {
		t.Errorf("expected name=web-server, got %q", params.Get("name"))
	}
	if params.Get("site") != "dc1" {
		t.Errorf("expected site=dc1, got %q", params.Get("site"))
	}
	if params.Get("q") != "search-term" {
		t.Errorf("expected q=search-term, got %q", params.Get("q"))
	}
	if params.Get("empty") != "" {
		t.Error("empty string should not be set")
	}
	if params.Get("number") != "" {
		t.Error("non-string value should not be set")
	}
	if params.Get("missing") != "" {
		t.Error("missing key should not be set")
	}
}

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-nb"}, "prod-nb"},
		{"absent", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 42}, ""},
		{"nil map", nil, ""},
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

// --- DCIM method tests ---

func TestGetDevices(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/devices/" {
			t.Errorf("expected path /api/dcim/devices/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "web-01" {
			t.Errorf("expected name=web-01, got %q", r.URL.Query().Get("name"))
		}
		if r.URL.Query().Get("site") != "dc1" {
			t.Errorf("expected site=dc1, got %q", r.URL.Query().Get("site"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":1,"results":[{"id":1,"name":"web-01"}]}`)
	})

	result, err := tool.GetDevices(context.Background(), "test-incident", map[string]interface{}{
		"name": "web-01",
		"site": "dc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "web-01") {
		t.Error("expected result to contain device name")
	}
}

func TestGetDevice(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/devices/42/" {
			t.Errorf("expected path /api/dcim/devices/42/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":42,"name":"core-switch"}`)
	})

	result, err := tool.GetDevice(context.Background(), "test-incident", map[string]interface{}{
		"id": float64(42),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "core-switch") {
		t.Error("expected result to contain device name")
	}
}

func TestGetDevice_StringID(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/devices/42/" {
			t.Errorf("expected path /api/dcim/devices/42/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":42,"name":"core-switch"}`)
	})

	result, err := tool.GetDevice(context.Background(), "test-incident", map[string]interface{}{
		"id": "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "core-switch") {
		t.Error("expected result to contain device name")
	}
}

func TestGetDevice_MissingID(t *testing.T) {
	tool := NewNetBoxTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetDevice(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("expected 'id is required' error, got: %v", err)
	}
}

func TestGetInterfaces(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/interfaces/" {
			t.Errorf("expected path /api/dcim/interfaces/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("device") != "web-01" {
			t.Errorf("expected device=web-01, got %q", r.URL.Query().Get("device"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetInterfaces(context.Background(), "test-incident", map[string]interface{}{
		"device": "web-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetSites(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/sites/" {
			t.Errorf("expected path /api/dcim/sites/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetSites(context.Background(), "test-incident", map[string]interface{}{
		"region": "us-east",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetRacks(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/racks/" {
			t.Errorf("expected path /api/dcim/racks/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetRacks(context.Background(), "test-incident", map[string]interface{}{
		"site": "dc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetCables(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/cables/" {
			t.Errorf("expected path /api/dcim/cables/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetCables(context.Background(), "test-incident", map[string]interface{}{
		"device": "core-switch",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetDeviceTypes(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/device-types/" {
			t.Errorf("expected path /api/dcim/device-types/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetDeviceTypes(context.Background(), "test-incident", map[string]interface{}{
		"manufacturer": "Cisco",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- IPAM method tests ---

func TestGetIPAddresses(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ipam/ip-addresses/" {
			t.Errorf("expected path /api/ipam/ip-addresses/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("address") != "10.0.0.1" {
			t.Errorf("expected address=10.0.0.1, got %q", r.URL.Query().Get("address"))
		}
		if r.URL.Query().Get("vrf") != "production" {
			t.Errorf("expected vrf=production, got %q", r.URL.Query().Get("vrf"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":1,"results":[{"id":1,"address":"10.0.0.1/24"}]}`)
	})

	result, err := tool.GetIPAddresses(context.Background(), "test-incident", map[string]interface{}{
		"address": "10.0.0.1",
		"vrf":     "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "10.0.0.1") {
		t.Error("expected result to contain IP address")
	}
}

func TestGetIPAddresses_AllFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, param := range []string{"address", "device", "interface", "vrf", "tenant", "status", "q"} {
			if q.Get(param) == "" {
				t.Errorf("expected param %s to be set", param)
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})

	_, err := tool.GetIPAddresses(context.Background(), "test-incident", map[string]interface{}{
		"address":   "10.0.0.1",
		"device":    "web-01",
		"interface": "eth0",
		"vrf":       "production",
		"tenant":    "acme",
		"status":    "active",
		"q":         "search",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPrefixes(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ipam/prefixes/" {
			t.Errorf("expected path /api/ipam/prefixes/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("prefix") != "10.0.0.0/24" {
			t.Errorf("expected prefix=10.0.0.0/24, got %q", r.URL.Query().Get("prefix"))
		}
		if r.URL.Query().Get("site") != "dc1" {
			t.Errorf("expected site=dc1, got %q", r.URL.Query().Get("site"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":1,"results":[{"id":1,"prefix":"10.0.0.0/24"}]}`)
	})

	result, err := tool.GetPrefixes(context.Background(), "test-incident", map[string]interface{}{
		"prefix": "10.0.0.0/24",
		"site":   "dc1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "10.0.0.0/24") {
		t.Error("expected result to contain prefix")
	}
}

func TestGetPrefixes_AllFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, param := range []string{"prefix", "site", "vrf", "vlan", "tenant", "status", "q"} {
			if q.Get(param) == "" {
				t.Errorf("expected param %s to be set", param)
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})

	_, err := tool.GetPrefixes(context.Background(), "test-incident", map[string]interface{}{
		"prefix": "10.0.0.0/24",
		"site":   "dc1",
		"vrf":    "production",
		"vlan":   "100",
		"tenant": "acme",
		"status": "active",
		"q":      "search",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetVLANs(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ipam/vlans/" {
			t.Errorf("expected path /api/ipam/vlans/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("vid") != "100" {
			t.Errorf("expected vid=100, got %q", r.URL.Query().Get("vid"))
		}
		if r.URL.Query().Get("name") != "mgmt" {
			t.Errorf("expected name=mgmt, got %q", r.URL.Query().Get("name"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":1,"results":[{"id":1,"vid":100,"name":"mgmt"}]}`)
	})

	result, err := tool.GetVLANs(context.Background(), "test-incident", map[string]interface{}{
		"vid":  "100",
		"name": "mgmt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "mgmt") {
		t.Error("expected result to contain VLAN name")
	}
}

func TestGetVLANs_AllFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, param := range []string{"vid", "name", "site", "group", "tenant", "q"} {
			if q.Get(param) == "" {
				t.Errorf("expected param %s to be set", param)
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})

	_, err := tool.GetVLANs(context.Background(), "test-incident", map[string]interface{}{
		"vid":    "100",
		"name":   "mgmt",
		"site":   "dc1",
		"group":  "core",
		"tenant": "acme",
		"q":      "search",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetVRFs(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ipam/vrfs/" {
			t.Errorf("expected path /api/ipam/vrfs/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "production" {
			t.Errorf("expected name=production, got %q", r.URL.Query().Get("name"))
		}
		if r.URL.Query().Get("tenant") != "acme" {
			t.Errorf("expected tenant=acme, got %q", r.URL.Query().Get("tenant"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":1,"results":[{"id":1,"name":"production","rd":"65000:100"}]}`)
	})

	result, err := tool.GetVRFs(context.Background(), "test-incident", map[string]interface{}{
		"name":   "production",
		"tenant": "acme",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "production") {
		t.Error("expected result to contain VRF name")
	}
}

func TestGetVRFs_AllFilters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, param := range []string{"name", "tenant", "q"} {
			if q.Get(param) == "" {
				t.Errorf("expected param %s to be set", param)
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})

	_, err := tool.GetVRFs(context.Background(), "test-incident", map[string]interface{}{
		"name":   "production",
		"tenant": "acme",
		"q":      "search",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetIPAddresses_WithPagination(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("limit") != "50" {
			t.Errorf("expected limit=50, got %q", q.Get("limit"))
		}
		if q.Get("offset") != "100" {
			t.Errorf("expected offset=100, got %q", q.Get("offset"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"count":200,"results":[]}`)
	})

	_, err := tool.GetIPAddresses(context.Background(), "test-incident", map[string]interface{}{
		"limit":  float64(50),
		"offset": float64(100),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Circuits method tests ---

func TestGetCircuits(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/circuits/circuits/" {
			t.Errorf("expected path /api/circuits/circuits/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetCircuits(context.Background(), "test-incident", map[string]interface{}{
		"provider": "Zayo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetProviders(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/circuits/providers/" {
			t.Errorf("expected path /api/circuits/providers/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetProviders(context.Background(), "test-incident", map[string]interface{}{
		"name": "Zayo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Virtualization method tests ---

func TestGetVirtualMachines(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/virtualization/virtual-machines/" {
			t.Errorf("expected path /api/virtualization/virtual-machines/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetVirtualMachines(context.Background(), "test-incident", map[string]interface{}{
		"cluster": "k8s-prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetClusters(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/virtualization/clusters/" {
			t.Errorf("expected path /api/virtualization/clusters/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetClusters(context.Background(), "test-incident", map[string]interface{}{
		"name": "k8s-prod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetVMInterfaces(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/virtualization/interfaces/" {
			t.Errorf("expected path /api/virtualization/interfaces/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetVMInterfaces(context.Background(), "test-incident", map[string]interface{}{
		"virtual_machine": "vm-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Tenancy method tests ---

func TestGetTenants(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tenancy/tenants/" {
			t.Errorf("expected path /api/tenancy/tenants/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetTenants(context.Background(), "test-incident", map[string]interface{}{
		"name": "acme",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetTenantGroups(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tenancy/tenant-groups/" {
			t.Errorf("expected path /api/tenancy/tenant-groups/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	_, err := tool.GetTenantGroups(context.Background(), "test-incident", map[string]interface{}{
		"name": "corporate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- APIRequest tests ---

func TestAPIRequest(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dcim/platforms/" {
			t.Errorf("expected path /api/dcim/platforms/, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"results":[]}`)
	})

	result, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/dcim/platforms/",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "results") {
		t.Error("expected results in response")
	}
}

func TestAPIRequest_PathNormalization(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{"already normalized", "/api/dcim/devices/", "/api/dcim/devices/"},
		{"missing api prefix", "dcim/devices", "/api/dcim/devices/"},
		{"missing trailing slash", "/api/dcim/devices", "/api/dcim/devices/"},
		{"with leading slash no api", "/dcim/devices", "/api/dcim/devices/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("expected path %s, got %s", tt.wantPath, r.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{}`)
			})

			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path": tt.path,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestAPIRequest_MissingPath(t *testing.T) {
	tool := NewNetBoxTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected 'path is required' error, got: %v", err)
	}
}

func TestAPIRequest_WithQueryParams(t *testing.T) {
	tool, _, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "active" {
			t.Errorf("expected status=active, got %q", r.URL.Query().Get("status"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/api/dcim/devices/",
		"query_params": map[string]interface{}{
			"status": "active",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Cache TTL verification ---

func TestCacheTTLConstants(t *testing.T) {
	// Verify CMDB-appropriate TTLs as specified in the plan
	if DCIMCacheTTL != 60*time.Second {
		t.Errorf("DCIMCacheTTL = %v, want 60s", DCIMCacheTTL)
	}
	if IPAMCacheTTL != 60*time.Second {
		t.Errorf("IPAMCacheTTL = %v, want 60s", IPAMCacheTTL)
	}
	if VMCacheTTL != 60*time.Second {
		t.Errorf("VMCacheTTL = %v, want 60s", VMCacheTTL)
	}
	if CircuitCacheTTL != 120*time.Second {
		t.Errorf("CircuitCacheTTL = %v, want 120s", CircuitCacheTTL)
	}
	if TenancyCacheTTL != 120*time.Second {
		t.Errorf("TenancyCacheTTL = %v, want 120s", TenancyCacheTTL)
	}
	if ConfigCacheTTL != 5*time.Minute {
		t.Errorf("ConfigCacheTTL = %v, want 5m", ConfigCacheTTL)
	}
}
