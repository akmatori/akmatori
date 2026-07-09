package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

// --- Helper functions ---

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// newTestTool creates a JiraTool with an httptest server's URL pre-populated in the config cache.
func newTestTool(t *testing.T, authType string, handler http.HandlerFunc) (*JiraTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		handler(w, r)
	})
	server := httptest.NewServer(wrappedHandler)

	tool := NewJiraTool(testLogger(), nil)
	config := &JiraConfig{
		URL:        server.URL,
		AuthType:   authType,
		APIVersion: "3",
		Username:   "user@example.com",
		APIToken:   "test-token",
		VerifySSL:  true,
		Timeout:    5,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	t.Cleanup(func() {
		tool.Stop()
		server.Close()
	})

	return tool, server, counter
}

func getTestConfig(tool *JiraTool) *JiraConfig {
	cached, ok := tool.configCache.Get(configCacheKey("test-incident"))
	if !ok {
		return nil
	}
	return cached.(*JiraConfig)
}

// --- Constructor and lifecycle tests ---

func TestNewJiraTool(t *testing.T) {
	tool := NewJiraTool(testLogger(), nil)
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

func TestNewJiraTool_WithRateLimiter(t *testing.T) {
	limiter := ratelimit.New(10, 20)
	tool := NewJiraTool(testLogger(), limiter)
	defer tool.Stop()

	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
}

func TestStop_DoubleStop(t *testing.T) {
	tool := NewJiraTool(testLogger(), nil)
	tool.Stop()
	tool.Stop() // should not panic
}

// --- Cache key tests ---

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:jira"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey_Stability(t *testing.T) {
	params1 := url.Values{"jql": []string{"project=FOO"}}
	params2 := url.Values{"jql": []string{"project=BAR"}}

	key1 := responseCacheKey("/rest/api/3/search", params1)
	key2 := responseCacheKey("/rest/api/3/search", params2)
	key3 := responseCacheKey("/rest/api/3/search", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

// --- Helper function tests ---

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero is clamped to default", 0, 30},
		{"negative is clamped to default", -5, 30},
		{"too low is clamped to min", 1, 5},
		{"valid 30 kept", 30, 30},
		{"max kept", 300, 300},
		{"over max clamped", 999, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampTimeout(tt.input); got != tt.want {
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
		{"zero stays zero", 0, 0},
		{"negative stays zero", -1, 0},
		{"50 kept", 50, 50},
		{"100 kept", 100, 100},
		{"over max clamped to 100", 250, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampLimit(tt.input); got != tt.want {
				t.Errorf("clampLimit(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-jira"}, "prod-jira"},
		{"absent", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 42}, ""},
		{"nil map", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLogicalName(tt.args); got != tt.want {
				t.Errorf("extractLogicalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApiPath(t *testing.T) {
	tests := []struct {
		name    string
		version string
		suffix  string
		want    string
	}{
		{"v3 search", "3", "/search", "/rest/api/3/search"},
		{"v2 search", "2", "/search", "/rest/api/2/search"},
		{"default to v3", "", "/issue/FOO-1", "/rest/api/3/issue/FOO-1"},
		{"missing slash prepended", "3", "search", "/rest/api/3/search"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apiPath(tt.version, tt.suffix); got != tt.want {
				t.Errorf("apiPath(%q, %q) = %q, want %q", tt.version, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestRequireWrites(t *testing.T) {
	if err := requireWrites(nil); err == nil {
		t.Error("expected error for nil config")
	}

	cfg := &JiraConfig{AllowWrites: false}
	err := requireWrites(cfg)
	if err == nil {
		t.Fatal("expected error when AllowWrites=false")
	}
	if !strings.Contains(err.Error(), "jira_allow_writes") {
		t.Errorf("error should mention jira_allow_writes setting, got: %v", err)
	}
	if !strings.Contains(err.Error(), "writes disabled") {
		t.Errorf("error should mention writes disabled, got: %v", err)
	}

	cfg.AllowWrites = true
	if err := requireWrites(cfg); err != nil {
		t.Errorf("expected no error when AllowWrites=true, got %v", err)
	}
}

func TestVerifyWriteGate_NilDBRejectsCachedFalse(t *testing.T) {
	// When database.DB is nil (tests / dev), verifyWriteGate falls back to the cached
	// config: a cached AllowWrites=false must reject with the writes-disabled error.
	// (In production with a live DB, the gate is always re-read from the DB regardless
	// of the cached value so toggle changes propagate immediately in both directions.)
	tool := NewJiraTool(testLogger(), nil)
	t.Cleanup(tool.Stop)

	_, err := tool.verifyWriteGate(context.Background(), "test-incident", "", &JiraConfig{AllowWrites: false})
	if err == nil {
		t.Fatal("expected error when DB is nil and cached AllowWrites=false")
	}
	if !strings.Contains(err.Error(), "writes disabled") {
		t.Errorf("expected 'writes disabled', got: %v", err)
	}
}

func TestVerifyWriteGate_NilDBAllowsCachedTrue(t *testing.T) {
	// Unit tests run without database.DB; verifyWriteGate must fall back to the cached
	// value so existing write-method tests (which pre-populate the cache) still work.
	tool := NewJiraTool(testLogger(), nil)
	t.Cleanup(tool.Stop)

	if _, err := tool.verifyWriteGate(context.Background(), "test-incident", "", &JiraConfig{AllowWrites: true}); err != nil {
		t.Errorf("expected nil DB to fall back to cached AllowWrites=true, got: %v", err)
	}
}

// --- authHeader tests ---

func TestAuthHeader_CloudBasic(t *testing.T) {
	cfg := &JiraConfig{
		AuthType: AuthTypeCloudBasic,
		Username: "user@example.com",
		APIToken: "secret-token",
	}
	got, err := authHeader(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedCreds := base64.StdEncoding.EncodeToString([]byte("user@example.com:secret-token"))
	want := "Basic " + expectedCreds
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestAuthHeader_Basic(t *testing.T) {
	cfg := &JiraConfig{
		AuthType: AuthTypeBasic,
		Username: "admin",
		APIToken: "password",
	}
	got, err := authHeader(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedCreds := base64.StdEncoding.EncodeToString([]byte("admin:password"))
	want := "Basic " + expectedCreds
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestAuthHeader_ServerBearer(t *testing.T) {
	cfg := &JiraConfig{
		AuthType: AuthTypeServerBearer,
		APIToken: "PAT-token",
	}
	got, err := authHeader(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Bearer PAT-token"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestAuthHeader_MissingUsername(t *testing.T) {
	cfg := &JiraConfig{
		AuthType: AuthTypeCloudBasic,
		APIToken: "token",
	}
	if _, err := authHeader(cfg); err == nil {
		t.Fatal("expected error for missing username on cloud_basic")
	}
}

func TestAuthHeader_MissingToken(t *testing.T) {
	cases := []*JiraConfig{
		{AuthType: AuthTypeCloudBasic, Username: "u"},
		{AuthType: AuthTypeServerBearer},
		{AuthType: AuthTypeBasic, Username: "u"},
	}
	for _, cfg := range cases {
		if _, err := authHeader(cfg); err == nil {
			t.Errorf("expected error for missing token (auth_type=%s)", cfg.AuthType)
		}
	}
}

func TestAuthHeader_UnsupportedType(t *testing.T) {
	cfg := &JiraConfig{
		AuthType: "oauth2",
		APIToken: "t",
	}
	_, err := authHeader(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported auth type")
	}
	if !strings.Contains(err.Error(), "unsupported jira_auth_type") {
		t.Errorf("error should mention unsupported, got: %v", err)
	}
}

// --- doRequest tests ---

func TestDoRequest_CloudBasicAuthHeader(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		expectedCreds := base64.StdEncoding.EncodeToString([]byte("user@example.com:test-token"))
		if auth != "Basic "+expectedCreds {
			t.Errorf("expected Basic %s, got %q", expectedCreds, auth)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept: application/json, got %q", r.Header.Get("Accept"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/3/search", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_ServerBearerAuthHeader(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeServerBearer, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/2/search", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_BasicAuthHeader(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeBasic, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		expectedCreds := base64.StdEncoding.EncodeToString([]byte("user@example.com:test-token"))
		if auth != "Basic "+expectedCreds {
			t.Errorf("expected Basic %s, got %q", expectedCreds, auth)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/2/search", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_QueryParams(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("jql") != "project=FOO" {
			t.Errorf("expected jql=project=FOO, got %q", r.URL.Query().Get("jql"))
		}
		if r.URL.Query().Get("maxResults") != "10" {
			t.Errorf("expected maxResults=10, got %q", r.URL.Query().Get("maxResults"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	params := url.Values{
		"jql":        []string{"project=FOO"},
		"maxResults": []string{"10"},
	}

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/3/search", params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_HTTPError(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errorMessages":["Issue does not exist"]}`)
	})

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/3/issue/MISSING-1", nil, nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "HTTP error 404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestDoRequest_NoURL(t *testing.T) {
	tool := NewJiraTool(testLogger(), nil)
	defer tool.Stop()

	cfg := &JiraConfig{
		AuthType: AuthTypeCloudBasic,
		Username: "u",
		APIToken: "t",
	}
	_, err := tool.doRequest(context.Background(), cfg, http.MethodGet, "/rest/api/3/search", nil, nil)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
	if !strings.Contains(err.Error(), "URL not configured") {
		t.Errorf("expected URL error, got: %v", err)
	}
}

func TestDoRequest_AuthError(t *testing.T) {
	tool := NewJiraTool(testLogger(), nil)
	defer tool.Stop()

	cfg := &JiraConfig{
		URL:      "http://localhost",
		AuthType: AuthTypeCloudBasic,
		APIToken: "t",
		// Missing username — should fail at auth header step
	}
	_, err := tool.doRequest(context.Background(), cfg, http.MethodGet, "/rest/api/3/search", nil, nil)
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "jira_username") {
		t.Errorf("expected username error, got: %v", err)
	}
}

func TestDoRequest_WithRateLimiter(t *testing.T) {
	limiter := ratelimit.New(100, 100)
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})
	tool.rateLimiter = limiter

	_, err := tool.doRequest(context.Background(), getTestConfig(tool), http.MethodGet, "/rest/api/3/search", nil, nil)
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
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"issues":[]}`)
	})

	ctx := context.Background()
	if _, err := tool.cachedGet(ctx, "test-incident", "/rest/api/3/search", nil, SearchCacheTTL); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if _, err := tool.cachedGet(ctx, "test-incident", "/rest/api/3/search", nil, SearchCacheTTL); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 server call (second cached), got %d", callCount.Load())
	}
}

func TestCachedGet_LogicalNameIsolation(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	cfg2 := &JiraConfig{
		URL:        getTestConfig(tool).URL,
		AuthType:   AuthTypeCloudBasic,
		APIVersion: "3",
		Username:   "other@example.com",
		APIToken:   "other-token",
		VerifySSL:  true,
		Timeout:    5,
	}
	tool.configCache.Set(fmt.Sprintf("creds:logical:%s:%s", "jira", "prod-jira"), cfg2)

	ctx := context.Background()
	if _, err := tool.cachedGet(ctx, "test-incident", "/rest/api/3/search", nil, SearchCacheTTL); err != nil {
		t.Fatalf("incident-keyed call failed: %v", err)
	}
	if _, err := tool.cachedGet(ctx, "test-incident", "/rest/api/3/search", nil, SearchCacheTTL, "prod-jira"); err != nil {
		t.Fatalf("logical-name-keyed call failed: %v", err)
	}
}

// --- Helper tests for read methods ---

func TestFieldsParam(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"absent", map[string]interface{}{}, ""},
		{"string", map[string]interface{}{"fields": "summary,status"}, "summary,status"},
		{"string trims", map[string]interface{}{"fields": "  summary,status  "}, "summary,status"},
		{"array joined", map[string]interface{}{"fields": []interface{}{"summary", "status"}}, "summary,status"},
		{"array trims and skips empty", map[string]interface{}{"fields": []interface{}{"summary ", "", " status"}}, "summary,status"},
		{"wrong type returns empty", map[string]interface{}{"fields": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldsParam(tt.args, "fields"); got != tt.want {
				t.Errorf("fieldsParam = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAddPagingParams(t *testing.T) {
	params := url.Values{}
	addPagingParams(params, map[string]interface{}{
		"start_at":    float64(50),
		"max_results": float64(250),
	})
	if got := params.Get("startAt"); got != "50" {
		t.Errorf("startAt = %q, want 50", got)
	}
	if got := params.Get("maxResults"); got != "100" {
		t.Errorf("maxResults = %q, want 100 (clamped)", got)
	}

	// Zero / negative are omitted.
	params2 := url.Values{}
	addPagingParams(params2, map[string]interface{}{
		"start_at":    float64(-1),
		"max_results": float64(0),
	})
	if params2.Get("startAt") != "" {
		t.Errorf("expected no startAt for negative input")
	}
	if params2.Get("maxResults") != "" {
		t.Errorf("expected no maxResults for zero input")
	}
}

// --- SearchIssues tests ---

func TestSearchIssues_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/search" {
			t.Errorf("path = %q, want /rest/api/3/search", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("jql") != "project = FOO" {
			t.Errorf("jql = %q, want project = FOO", q.Get("jql"))
		}
		if q.Get("fields") != "summary,status" {
			t.Errorf("fields = %q, want summary,status", q.Get("fields"))
		}
		if q.Get("expand") != "names" {
			t.Errorf("expand = %q, want names", q.Get("expand"))
		}
		if q.Get("startAt") != "10" {
			t.Errorf("startAt = %q, want 10", q.Get("startAt"))
		}
		if q.Get("maxResults") != "100" {
			t.Errorf("maxResults = %q, want 100 (clamped)", q.Get("maxResults"))
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"issues":[{"key":"FOO-1"}]}`)
	})

	out, err := tool.SearchIssues(context.Background(), "test-incident", map[string]interface{}{
		"jql":         "project = FOO",
		"fields":      []interface{}{"summary", "status"},
		"expand":      "names",
		"start_at":    float64(10),
		"max_results": float64(500), // should clamp
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"FOO-1"`) {
		t.Errorf("expected response to contain FOO-1, got %q", out)
	}
}

func TestSearchIssues_MissingJQL(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, err := tool.SearchIssues(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing jql")
	}
	if !strings.Contains(err.Error(), "jql is required") {
		t.Errorf("expected jql required error, got: %v", err)
	}
	if counter.Load() != 0 {
		t.Errorf("expected no server calls for invalid args, got %d", counter.Load())
	}
}

func TestSearchIssues_HTTPError(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errorMessages":["bad jql"]}`)
	})

	_, err := tool.SearchIssues(context.Background(), "test-incident", map[string]interface{}{
		"jql": "bogus !!",
	})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP error 400") {
		t.Errorf("expected 400, got: %v", err)
	}
}

func TestSearchIssues_APIVersion2(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeServerBearer, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/search" {
			t.Errorf("path = %q, want /rest/api/2/search", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})
	// Override cached config to v2.
	cfg := getTestConfig(tool)
	cfg.APIVersion = "2"

	_, err := tool.SearchIssues(context.Background(), "test-incident", map[string]interface{}{
		"jql": "project = X",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetIssue tests ---

func TestGetIssue_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/FOO-1" {
			t.Errorf("path = %q, want /rest/api/3/issue/FOO-1", r.URL.Path)
		}
		if r.URL.Query().Get("expand") != "changelog" {
			t.Errorf("expand = %q", r.URL.Query().Get("expand"))
		}
		if r.URL.Query().Get("fields") != "summary" {
			t.Errorf("fields = %q", r.URL.Query().Get("fields"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"key":"FOO-1"}`)
	})

	out, err := tool.GetIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":    "FOO-1",
		"expand": "changelog",
		"fields": "summary",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "FOO-1") {
		t.Errorf("expected FOO-1 in response, got %q", out)
	}
}

func TestGetIssue_MissingKey(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.GetIssue(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !strings.Contains(err.Error(), "key is required") {
		t.Errorf("expected key required error, got: %v", err)
	}
}

func TestGetIssue_KeyEscaped(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		// Path should contain the percent-encoded form.
		if !strings.HasPrefix(r.URL.EscapedPath(), "/rest/api/3/issue/") {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})
	if _, err := tool.GetIssue(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO BAR/1",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- GetIssueComments tests ---

func TestGetIssueComments_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/FOO-1/comment" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("startAt") != "5" {
			t.Errorf("startAt = %q", r.URL.Query().Get("startAt"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"comments":[]}`)
	})

	out, err := tool.GetIssueComments(context.Background(), "test-incident", map[string]interface{}{
		"key":      "FOO-1",
		"start_at": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "comments") {
		t.Errorf("expected comments in response, got %q", out)
	}
}

func TestGetIssueComments_MissingKey(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.GetIssueComments(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// --- GetIssueTransitions tests ---

func TestGetIssueTransitions_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/FOO-1/transitions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"transitions":[{"id":"31","name":"Done"}]}`)
	})

	out, err := tool.GetIssueTransitions(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"name":"Done"`) {
		t.Errorf("expected transitions JSON, got %q", out)
	}
}

func TestGetIssueTransitions_MissingKey(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.GetIssueTransitions(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// --- GetIssueChangelog tests ---

func TestGetIssueChangelog_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/FOO-1/changelog" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("maxResults") != "50" {
			t.Errorf("maxResults = %q", r.URL.Query().Get("maxResults"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"values":[]}`)
	})

	_, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key":         "FOO-1",
		"max_results": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetIssueChangelog_APIVersion2_UsesDedicatedEndpoint(t *testing.T) {
	// Modern Jira Server/DC (8.x+) exposes /rest/api/2/issue/{key}/changelog which
	// natively supports paging — we hit that first without falling back.
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue/FOO-1/changelog" {
			t.Errorf("path = %q, want v2 dedicated changelog path", r.URL.Path)
		}
		if r.URL.Query().Get("startAt") != "10" {
			t.Errorf("expected startAt=10, got %q", r.URL.Query().Get("startAt"))
		}
		if r.URL.Query().Get("maxResults") != "25" {
			t.Errorf("expected maxResults=25, got %q", r.URL.Query().Get("maxResults"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"values":[],"startAt":10,"maxResults":25,"total":0}`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key":         "FOO-1",
		"start_at":    float64(10),
		"max_results": float64(25),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"values"`) {
		t.Errorf("expected response to contain values field, got %q", out)
	}
}

func TestGetIssueChangelog_APIVersion2_FallsBackOn404(t *testing.T) {
	// Older Jira Server (pre-8.x) returns 404 on /issue/{key}/changelog; we then
	// fall back to /issue/{key}?expand=changelog, extract changelog.histories, and
	// normalize to a v3-style {values, startAt, maxResults, total, isLast} envelope.
	var firstCall, secondCall string
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			firstCall = r.URL.Path
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["not found"]}`)
		case 2:
			secondCall = r.URL.Path + "?" + r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"key":"FOO-1","changelog":{"histories":[{"id":"1"},{"id":"2"}]}}`)
		default:
			t.Errorf("unexpected extra call: %s", r.URL.Path)
		}
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if firstCall != "/rest/api/2/issue/FOO-1/changelog" {
		t.Errorf("first call = %q, want dedicated endpoint", firstCall)
	}
	if !strings.HasPrefix(secondCall, "/rest/api/2/issue/FOO-1?") || !strings.Contains(secondCall, "expand=changelog") {
		t.Errorf("second call = %q, want expand=changelog fallback", secondCall)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	values, ok := resp["values"].([]interface{})
	if !ok {
		t.Fatalf("expected values array in normalized envelope, got %v", resp)
	}
	if len(values) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(values))
	}
	if total, _ := resp["total"].(float64); total != 2 {
		t.Errorf("expected total=2, got %v", resp["total"])
	}
	if isLast, _ := resp["isLast"].(bool); !isLast {
		t.Errorf("expected isLast=true, got %v", resp["isLast"])
	}
	if strings.Contains(out, `"key":"FOO-1"`) {
		t.Errorf("normalized envelope should not leak full issue document, got %q", out)
	}
}

func TestGetIssueChangelog_APIVersion2_FallsBackOn404_AppliesPaging(t *testing.T) {
	// On the v2 fallback path, paging args must be applied client-side over the
	// full histories array so callers don't get identical cached responses for
	// different page requests.
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["not found"]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"changelog":{"histories":[{"id":"1"},{"id":"2"},{"id":"3"},{"id":"4"},{"id":"5"}]}}`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key":         "FOO-1",
		"start_at":    float64(1),
		"max_results": float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	values, _ := resp["values"].([]interface{})
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d (%v)", len(values), values)
	}
	first, _ := values[0].(map[string]interface{})
	if first["id"] != "2" {
		t.Errorf("expected first entry id=2 after start_at=1, got %v", first["id"])
	}
	second, _ := values[1].(map[string]interface{})
	if second["id"] != "3" {
		t.Errorf("expected second entry id=3, got %v", second["id"])
	}
	if total, _ := resp["total"].(float64); total != 5 {
		t.Errorf("expected total=5, got %v", resp["total"])
	}
	if isLast, _ := resp["isLast"].(bool); isLast {
		t.Errorf("expected isLast=false at start_at=1, got %v", resp["isLast"])
	}
	if start, _ := resp["startAt"].(float64); start != 1 {
		t.Errorf("expected startAt=1, got %v", resp["startAt"])
	}
	if max, _ := resp["maxResults"].(float64); max != 2 {
		t.Errorf("expected maxResults=2, got %v", resp["maxResults"])
	}
}

func TestGetIssueChangelog_APIVersion2_FallsBackOn404_PreservesEmbeddedTotal(t *testing.T) {
	// On older Jira Server, the embedded changelog (via ?expand=changelog) reports
	// its own total even when the histories array is capped. Surface the embedded
	// total so the agent can see entries exist upstream, but isLast must reflect
	// LOCAL exhaustion — the fallback can't paginate beyond what's embedded, so
	// once the caller has read all local entries, the contract has to say so.
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["not found"]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		// Embedded changelog reports total=42 but only ships 2 histories.
		fmt.Fprint(w, `{"changelog":{"startAt":0,"maxResults":100,"total":42,"histories":[{"id":"1"},{"id":"2"}]}}`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if total, _ := resp["total"].(float64); total != 42 {
		t.Errorf("expected total=42 (from embedded changelog), got %v", resp["total"])
	}
	if isLast, _ := resp["isLast"].(bool); !isLast {
		t.Errorf("expected isLast=true once the local slice (2 entries) is exhausted at startAt=0, got %v", resp["isLast"])
	}
}

func TestGetIssueChangelog_APIVersion2_FallsBackOn404_PagingBeyondLocalIsLast(t *testing.T) {
	// When the caller paginates past the embedded slice (e.g. start_at=100 against
	// a 2-entry local histories), the fallback must return empty values WITH
	// isLast=true. Returning isLast=false alongside an empty values array would
	// trick the agent into looping forever, since the dedicated /changelog
	// endpoint (the only way to fetch entries beyond the embed) 404s here.
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["not found"]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"changelog":{"startAt":0,"maxResults":100,"total":42,"histories":[{"id":"1"},{"id":"2"}]}}`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key":         "FOO-1",
		"start_at":    float64(100),
		"max_results": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	values, _ := resp["values"].([]interface{})
	if len(values) != 0 {
		t.Errorf("expected empty values when start_at exceeds local histories, got %d", len(values))
	}
	if isLast, _ := resp["isLast"].(bool); !isLast {
		t.Errorf("expected isLast=true to stop pagination loops when no more local entries exist, got %v", resp["isLast"])
	}
	if total, _ := resp["total"].(float64); total != 42 {
		t.Errorf("expected total=42 (still surfaced from embed), got %v", resp["total"])
	}
}

func TestGetIssueChangelog_APIVersion2_NonNotFoundError(t *testing.T) {
	// A non-404 error on the dedicated endpoint should propagate (no fallback).
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"errorMessages":["boom"]}`)
	})
	getTestConfig(tool).APIVersion = "2"

	_, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err == nil {
		t.Fatal("expected error to propagate without fallback")
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call on non-404, got %d", calls)
	}
}

func TestGetIssueChangelog_APIVersion3_DoesNotFallBackOn404(t *testing.T) {
	// On v3 (Cloud), there is no v2 fallback — a 404 should propagate.
	calls := 0
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errorMessages":["not found"]}`)
	})

	_, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err == nil {
		t.Fatal("expected error to propagate without fallback on v3")
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call on v3, got %d", calls)
	}
}

func TestGetIssueChangelog_MissingKey(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// --- GetProjects tests ---

func TestGetProjects_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/project/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "platform" {
			t.Errorf("query = %q", r.URL.Query().Get("query"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"values":[]}`)
	})

	_, err := tool.GetProjects(context.Background(), "test-incident", map[string]interface{}{
		"query": "platform",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetProjects_APIVersion2_ClientSideFilterAndPaging(t *testing.T) {
	// Server/DC v2 uses /project (no native filter/paging). The tool fetches the
	// full list and applies `query` + start_at/max_results client-side, normalizing
	// to the v3-style {values, startAt, maxResults, total, isLast} shape.
	var receivedQuery url.Values
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/project" {
			t.Errorf("path = %q, want /rest/api/2/project", r.URL.Path)
		}
		receivedQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[
			{"key":"FOO","name":"Foo Platform"},
			{"key":"BAR","name":"Bar Service"},
			{"key":"FOOBAR","name":"FooBar Combined"},
			{"key":"QUX","name":"Qux Project"}
		]`)
	})
	getTestConfig(tool).APIVersion = "2"

	// query=foo should match Foo Platform, FooBar Combined (case-insensitive on name/key).
	out, err := tool.GetProjects(context.Background(), "test-incident", map[string]interface{}{
		"query":       "foo",
		"start_at":    float64(0),
		"max_results": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// /project must not receive any query/paging — filtering happens client-side.
	if receivedQuery.Get("query") != "" || receivedQuery.Get("startAt") != "" || receivedQuery.Get("maxResults") != "" {
		t.Errorf("v2 /project should be called without params, got %v", receivedQuery)
	}

	var parsed struct {
		StartAt    int                      `json:"startAt"`
		MaxResults int                      `json:"maxResults"`
		Total      int                      `json:"total"`
		IsLast     bool                     `json:"isLast"`
		Values     []map[string]interface{} `json:"values"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to parse normalized response: %v\nresponse: %s", err, out)
	}
	if parsed.Total != 2 {
		t.Errorf("total = %d, want 2 (matches: FOO + FOOBAR)", parsed.Total)
	}
	if parsed.MaxResults != 1 {
		t.Errorf("maxResults = %d, want 1", parsed.MaxResults)
	}
	if len(parsed.Values) != 1 {
		t.Fatalf("values length = %d, want 1 (paging window)", len(parsed.Values))
	}
	if parsed.Values[0]["key"] != "FOO" {
		t.Errorf("first value key = %v, want FOO", parsed.Values[0]["key"])
	}
	if parsed.IsLast {
		t.Errorf("isLast = true, want false (still 1 match remaining)")
	}
}

func TestGetProjects_APIVersion2_PagingSecondPage(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"key":"A","name":"A"},{"key":"B","name":"B"},{"key":"C","name":"C"}]`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetProjects(context.Background(), "test-incident", map[string]interface{}{
		"start_at":    float64(2),
		"max_results": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed struct {
		StartAt int                      `json:"startAt"`
		Total   int                      `json:"total"`
		IsLast  bool                     `json:"isLast"`
		Values  []map[string]interface{} `json:"values"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to parse normalized response: %v", err)
	}
	if parsed.Total != 3 {
		t.Errorf("total = %d, want 3", parsed.Total)
	}
	if len(parsed.Values) != 1 || parsed.Values[0]["key"] != "C" {
		t.Errorf("values = %v, want [C]", parsed.Values)
	}
	if !parsed.IsLast {
		t.Errorf("isLast = false, want true (window covers tail)")
	}
}

// --- GetProject tests ---

func TestGetProject_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/project/FOO" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"key":"FOO"}`)
	})

	out, err := tool.GetProject(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"FOO"`) {
		t.Errorf("expected FOO in response, got %q", out)
	}
}

func TestGetProject_MissingKey(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.GetProject(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// --- SearchUsers tests ---

func TestSearchUsers_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/user/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "alice" {
			t.Errorf("query = %q", r.URL.Query().Get("query"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})

	_, err := tool.SearchUsers(context.Background(), "test-incident", map[string]interface{}{
		"query": "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchUsers_APIVersion2_UsesUsernameParam(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/user/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// v2 Jira Server/DC ignores ?query=...; the correct parameter is ?username=...
		if got := r.URL.Query().Get("username"); got != "alice" {
			t.Errorf("expected username=alice, got %q (query=%q)", got, r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("query") != "" {
			t.Errorf("v2 should not send ?query=, got %q", r.URL.Query().Get("query"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[]`)
	})
	getTestConfig(tool).APIVersion = "2"

	_, err := tool.SearchUsers(context.Background(), "test-incident", map[string]interface{}{
		"query": "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchUsers_MissingQuery(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.SearchUsers(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// --- APIRequest tests ---

func TestAPIRequest_Success(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/myself" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("expand") != "groups" {
			t.Errorf("expand = %q", r.URL.Query().Get("expand"))
		}
		if r.URL.Query().Get("limit") != "25" {
			t.Errorf("limit = %q", r.URL.Query().Get("limit"))
		}
		if r.URL.Query().Get("active") != "true" {
			t.Errorf("active = %q", r.URL.Query().Get("active"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"accountId":"abc"}`)
	})

	out, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/rest/api/3/myself",
		"params": map[string]interface{}{
			"expand": "groups",
			"limit":  float64(25),
			"active": true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "accountId") {
		t.Errorf("expected accountId in response, got %q", out)
	}
}

func TestAPIRequest_MissingPath(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected path required error, got: %v", err)
	}
}

func TestAPIRequest_RejectsPathTraversal(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []string{
		"/rest/../etc/passwd",
		"/rest/api/3/%2e%2e/admin",
		"/rest/api/3/%252e%252e/admin", // double-encoded
		"/rest/api/3/issue?jql=foo",
		"/rest/api/3/issue#fragment",
		"/api/3/myself",                       // wrong prefix
		"/rest/api/3/issue/%0D%0AHeader:%20x", // CRLF injection
		"/rest/api/3/issue/foo bar",           // whitespace
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path": p,
			})
			if err == nil {
				t.Errorf("expected error for path %q", p)
			}
		})
	}
	if counter.Load() != 0 {
		t.Errorf("expected no server calls for rejected paths, got %d", counter.Load())
	}
}

func TestAPIRequest_ArrayParam(t *testing.T) {
	var capturedExpand []string
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		capturedExpand = r.URL.Query()["expand"]
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/rest/api/3/myself",
		"params": map[string]interface{}{
			"expand": []interface{}{"groups", "applicationRoles"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedExpand) != 2 || capturedExpand[0] != "groups" || capturedExpand[1] != "applicationRoles" {
		t.Errorf("expected expand=[groups, applicationRoles], got %v", capturedExpand)
	}
}

func TestAPIRequest_UnsupportedParamType(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {})
	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path": "/rest/api/3/myself",
		"params": map[string]interface{}{
			"bad": map[string]interface{}{"nested": "value"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported param type")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected unsupported type error, got: %v", err)
	}
}

// --- Read-method smoke coverage ---

// TestReadMethods_ExecuteWithoutError is a smoke test: each read method runs to completion
// against a generic 200/`{}` server with default args. It does NOT verify per-method cache
// TTLs (the in-memory cache type exposes no TTL accessor); cache wiring is asserted by the
// per-method success tests above through observed parameter and path behaviour.
func TestReadMethods_ExecuteWithoutError(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})
	ctx := context.Background()

	if _, err := tool.SearchIssues(ctx, "test-incident", map[string]interface{}{"jql": "x"}); err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if _, err := tool.GetIssue(ctx, "test-incident", map[string]interface{}{"key": "FOO-1"}); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if _, err := tool.GetIssueChangelog(ctx, "test-incident", map[string]interface{}{"key": "FOO-1"}); err != nil {
		t.Fatalf("GetIssueChangelog: %v", err)
	}
	if _, err := tool.GetProjects(ctx, "test-incident", map[string]interface{}{}); err != nil {
		t.Fatalf("GetProjects: %v", err)
	}
	if _, err := tool.SearchUsers(ctx, "test-incident", map[string]interface{}{"query": "x"}); err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
}

// --- Write-method helper tests ---

func TestAssigneeRef(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		version string
		want    interface{}
		wantErr bool
	}{
		{"v3 string", "alice-id", "3", map[string]interface{}{"accountId": "alice-id"}, false},
		{"v2 string", "alice", "2", map[string]interface{}{"name": "alice"}, false},
		{"empty string", "  ", "3", nil, false},
		{"map passthrough", map[string]interface{}{"emailAddress": "a@b"}, "3", map[string]interface{}{"emailAddress": "a@b"}, false},
		{"nil", nil, "3", nil, false},
		{"wrong type", 42, "3", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := assigneeRef(tt.input, tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("assigneeRef err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("assigneeRef = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPriorityRef(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    interface{}
		wantErr bool
	}{
		{"string", "High", map[string]interface{}{"name": "High"}, false},
		{"empty string", "   ", nil, false},
		{"map passthrough", map[string]interface{}{"id": "1"}, map[string]interface{}{"id": "1"}, false},
		{"nil", nil, nil, false},
		{"wrong type", 10, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := priorityRef(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("priorityRef err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("priorityRef = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStringSlice(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    []string
		wantErr bool
	}{
		{"strings", []interface{}{"a", "b", "c"}, []string{"a", "b", "c"}, false},
		{"trim and drop empty", []interface{}{" a ", "", "  "}, []string{"a"}, false},
		{"non-string element errors", []interface{}{"a", 42, true}, nil, true},
		{"wrong type returns error", "a,b,c", nil, true},
		{"nil returns nil", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stringSlice(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("stringSlice err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stringSlice = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClampStartAt(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want int
	}{
		{"zero", 0, 0},
		{"negative", -5, 0},
		{"normal", 100, 100},
		{"overflow 1e20", 1e20, math.MaxInt32},
		{"overflow MaxFloat64", math.MaxFloat64, math.MaxInt32},
		{"just under int32 max", float64(math.MaxInt32 - 1), math.MaxInt32 - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampStartAt(tt.in)
			if got != tt.want {
				t.Errorf("clampStartAt(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestGetIssueChangelog_StartAtOverflow guards against the
// historical panic where a crafted float start_at > int64 max wrapped to
// MinInt64, then was used as a slice index in the v2 changelog fallback.
func TestGetIssueChangelog_StartAtOverflow(t *testing.T) {
	calls := 0
	tool, server, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "/changelog") {
			http.Error(w, `{"errorMessages":["not found"]}`, http.StatusNotFound)
			return
		}
		if _, err := w.Write([]byte(`{"changelog":{"total":1,"histories":[{"id":"1"}]}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	defer server.Close()
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key":      "FOO-1",
		"start_at": 1e20,
	})
	if err != nil {
		t.Fatalf("GetIssueChangelog unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty response")
	}
	if calls < 2 {
		t.Errorf("expected fallback to /issue, got %d server calls", calls)
	}
}

func TestAPIRequest_BadParamsType(t *testing.T) {
	tool, server, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when params is malformed; got %s %s", r.Method, r.URL.String())
	})
	defer server.Close()

	_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
		"path":   "/rest/api/3/myself",
		"params": "expand=names",
	})
	if err == nil || !strings.Contains(err.Error(), "params must be an object") {
		t.Errorf("expected params type error, got: %v", err)
	}
}

func TestCreateIssue_RejectsMalformedFields(t *testing.T) {
	tool, server, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when args are malformed; got %s %s", r.Method, r.URL.String())
	})
	defer server.Close()

	base := map[string]interface{}{
		"project_key": "FOO",
		"issue_type":  "Task",
		"summary":     "x",
	}
	for _, tc := range []struct {
		name  string
		patch map[string]interface{}
		want  string
	}{
		{"assignee wrong type", map[string]interface{}{"assignee": 42}, "assignee must be a string or object"},
		{"priority wrong type", map[string]interface{}{"priority": 10}, "priority must be a string or object"},
		{"labels wrong type", map[string]interface{}{"labels": "a,b,c"}, "labels must be an array of strings"},
		{"labels non-string elem", map[string]interface{}{"labels": []interface{}{"a", 42}}, "labels[1] must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := make(map[string]interface{}, len(base)+len(tc.patch))
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.patch {
				args[k] = v
			}
			_, err := tool.CreateIssue(context.Background(), "test-incident", args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

// newWriteTestTool creates a JiraTool with AllowWrites=true for write-method success-path testing.
func newWriteTestTool(t *testing.T, handler http.HandlerFunc) (*JiraTool, *httptest.Server, *atomic.Int32) {
	t.Helper()
	tool, server, counter := newTestTool(t, AuthTypeCloudBasic, handler)
	getTestConfig(tool).AllowWrites = true
	return tool, server, counter
}

// --- AddComment tests ---

// extractADFText pulls the leaf text out of a minimal ADF doc produced by adfTextDoc.
// Returns "" if the structure does not match the expected single-paragraph shape.
func extractADFText(t *testing.T, doc interface{}) string {
	t.Helper()
	m, ok := doc.(map[string]interface{})
	if !ok {
		return ""
	}
	if m["type"] != "doc" {
		return ""
	}
	content, ok := m["content"].([]interface{})
	if !ok || len(content) == 0 {
		return ""
	}
	para, ok := content[0].(map[string]interface{})
	if !ok {
		return ""
	}
	inner, ok := para["content"].([]interface{})
	if !ok || len(inner) == 0 {
		return ""
	}
	leaf, ok := inner[0].(map[string]interface{})
	if !ok {
		return ""
	}
	s, _ := leaf["text"].(string)
	return s
}

func TestAddComment_Success_V3WrapsStringAsADF(t *testing.T) {
	var receivedMethod, receivedPath, receivedAuth string
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(bodyBytes, &receivedBody); err != nil {
			t.Fatalf("request body was not valid JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10001"}`)
	})

	out, err := tool.AddComment(context.Background(), "test-incident", map[string]interface{}{
		"key":  "FOO-1",
		"body": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/rest/api/3/issue/FOO-1/comment" {
		t.Errorf("path = %q", receivedPath)
	}
	if receivedAuth == "" {
		t.Error("missing Authorization header")
	}
	if got := extractADFText(t, receivedBody["body"]); got != "hello" {
		t.Errorf("expected body auto-wrapped as ADF text 'hello', got %v", receivedBody["body"])
	}
	if !strings.Contains(out, `"id":"10001"`) {
		t.Errorf("expected response to contain id, got %q", out)
	}
}

func TestAddComment_Success_V2PassesStringThrough(t *testing.T) {
	var receivedPath string
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(bodyBytes, &receivedBody); err != nil {
			t.Fatalf("request body was not valid JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10001"}`)
	})
	getTestConfig(tool).APIVersion = "2"

	_, err := tool.AddComment(context.Background(), "test-incident", map[string]interface{}{
		"key":  "FOO-1",
		"body": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedPath != "/rest/api/2/issue/FOO-1/comment" {
		t.Errorf("path = %q, want v2 path", receivedPath)
	}
	if receivedBody["body"] != "hello" {
		t.Errorf("v2 body should pass through verbatim as string, got %T %v", receivedBody["body"], receivedBody["body"])
	}
}

func TestAddComment_ADFObjectPassthrough(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	})

	adf := map[string]interface{}{
		"version": float64(1),
		"type":    "doc",
		"content": []interface{}{},
	}
	_, err := tool.AddComment(context.Background(), "test-incident", map[string]interface{}{
		"key":  "FOO-1",
		"body": adf,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotBody, ok := receivedBody["body"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected body to be ADF object, got %T", receivedBody["body"])
	}
	if gotBody["type"] != "doc" {
		t.Errorf("expected ADF doc, got %v", gotBody["type"])
	}
}

func TestAddComment_WriteGateBlocks(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server when writes are disabled")
	})
	// Default: AllowWrites=false.

	_, err := tool.AddComment(context.Background(), "test-incident", map[string]interface{}{
		"key":  "FOO-1",
		"body": "hello",
	})
	if err == nil {
		t.Fatal("expected write-gate error")
	}
	if !strings.Contains(err.Error(), "jira_allow_writes") {
		t.Errorf("expected jira_allow_writes mention, got: %v", err)
	}
	if !strings.Contains(err.Error(), "writes disabled") {
		t.Errorf("expected 'writes disabled', got: %v", err)
	}
	if counter.Load() != 0 {
		t.Errorf("expected 0 server calls, got %d", counter.Load())
	}
}

func TestAddComment_MissingKey(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})
	_, err := tool.AddComment(context.Background(), "test-incident", map[string]interface{}{
		"body": "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Errorf("expected key required error, got %v", err)
	}
}

func TestAddComment_MissingBody(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})
	cases := []map[string]interface{}{
		{"key": "FOO-1"},                                   // missing body
		{"key": "FOO-1", "body": ""},                       // empty string
		{"key": "FOO-1", "body": "   "},                    // whitespace
		{"key": "FOO-1", "body": map[string]interface{}{}}, // empty object
		{"key": "FOO-1", "body": 42},                       // wrong type
	}
	for i, args := range cases {
		_, err := tool.AddComment(context.Background(), "test-incident", args)
		if err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

// --- TransitionIssue tests ---

func TestTransitionIssue_Success(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusNoContent)
	})

	_, err := tool.TransitionIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":           "FOO-1",
		"transition_id": "31",
		"comment":       "deploying fix",
		"fields": map[string]interface{}{
			"resolution": map[string]interface{}{"name": "Done"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/rest/api/3/issue/FOO-1/transitions" {
		t.Errorf("path = %q", receivedPath)
	}

	tr, ok := receivedBody["transition"].(map[string]interface{})
	if !ok {
		t.Fatal("expected transition object in body")
	}
	if tr["id"] != "31" {
		t.Errorf("transition.id = %v, want 31", tr["id"])
	}

	update, ok := receivedBody["update"].(map[string]interface{})
	if !ok {
		t.Fatal("expected update.comment in body")
	}
	comments, ok := update["comment"].([]interface{})
	if !ok || len(comments) != 1 {
		t.Fatalf("expected 1 update.comment entry, got %v", update["comment"])
	}
	add, ok := comments[0].(map[string]interface{})["add"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected comments[0].add object, got %v", comments[0])
	}
	// v3 (default test config) auto-wraps the comment string as ADF.
	if got := extractADFText(t, add["body"]); got != "deploying fix" {
		t.Errorf("expected ADF-wrapped comment body 'deploying fix', got %v", add["body"])
	}

	fields, ok := receivedBody["fields"].(map[string]interface{})
	if !ok {
		t.Fatal("expected fields in body")
	}
	if _, ok := fields["resolution"]; !ok {
		t.Error("expected resolution in fields")
	}
}

func TestTransitionIssue_NoComment(t *testing.T) {
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusNoContent)
	})

	_, err := tool.TransitionIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":           "FOO-1",
		"transition_id": "31",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := receivedBody["update"]; exists {
		t.Error("expected update to be omitted when no comment provided")
	}
	if _, exists := receivedBody["fields"]; exists {
		t.Error("expected fields to be omitted when not provided")
	}
}

func TestTransitionIssue_WriteGateBlocks(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.TransitionIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":           "FOO-1",
		"transition_id": "31",
	})
	if err == nil || !strings.Contains(err.Error(), "jira_allow_writes") {
		t.Fatalf("expected write-gate error, got %v", err)
	}
	if counter.Load() != 0 {
		t.Errorf("expected 0 server calls, got %d", counter.Load())
	}
}

func TestTransitionIssue_MissingArgs(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"no key", map[string]interface{}{"transition_id": "31"}, "key is required"},
		{"no transition_id", map[string]interface{}{"key": "FOO-1"}, "transition_id is required"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.TransitionIssue(context.Background(), "test-incident", tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

// --- CreateIssue tests ---

func TestCreateIssue_Success(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10002","key":"FOO-2"}`)
	})

	out, err := tool.CreateIssue(context.Background(), "test-incident", map[string]interface{}{
		"project_key": "FOO",
		"issue_type":  "Bug",
		"summary":     "Boom",
		"description": "Details",
		"assignee":    "acct-123",
		"priority":    "High",
		"labels":      []interface{}{"prod", "urgent"},
		"fields": map[string]interface{}{
			"customfield_10001": "extra",
			"summary":           "Overridden", // raw fields override convenience
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/rest/api/3/issue" {
		t.Errorf("path = %q", receivedPath)
	}

	fields, ok := receivedBody["fields"].(map[string]interface{})
	if !ok {
		t.Fatal("expected fields in body")
	}
	project, ok := fields["project"].(map[string]interface{})
	if !ok || project["key"] != "FOO" {
		t.Errorf("project = %v", fields["project"])
	}
	issuetype, ok := fields["issuetype"].(map[string]interface{})
	if !ok || issuetype["name"] != "Bug" {
		t.Errorf("issuetype = %v", fields["issuetype"])
	}
	// raw fields.summary overrode convenience summary
	if fields["summary"] != "Overridden" {
		t.Errorf("expected raw fields override, got summary = %v", fields["summary"])
	}
	// v3 auto-wraps string descriptions as ADF.
	if got := extractADFText(t, fields["description"]); got != "Details" {
		t.Errorf("expected ADF-wrapped description 'Details', got %v", fields["description"])
	}
	assignee, ok := fields["assignee"].(map[string]interface{})
	if !ok || assignee["accountId"] != "acct-123" {
		t.Errorf("expected v3 assignee accountId, got %v", fields["assignee"])
	}
	priority, ok := fields["priority"].(map[string]interface{})
	if !ok || priority["name"] != "High" {
		t.Errorf("priority = %v", fields["priority"])
	}
	labels, ok := fields["labels"].([]interface{})
	if !ok || len(labels) != 2 || labels[0] != "prod" || labels[1] != "urgent" {
		t.Errorf("labels = %v", fields["labels"])
	}
	if fields["customfield_10001"] != "extra" {
		t.Errorf("expected customfield_10001 to be set from raw fields, got %v", fields["customfield_10001"])
	}
	if !strings.Contains(out, "FOO-2") {
		t.Errorf("expected FOO-2 in response, got %q", out)
	}
}

func TestCreateIssue_APIVersion2_UsesAssigneeName(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue" {
			t.Errorf("path = %q", r.URL.Path)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	})
	getTestConfig(tool).APIVersion = "2"

	_, err := tool.CreateIssue(context.Background(), "test-incident", map[string]interface{}{
		"project_key": "FOO",
		"issue_type":  "Bug",
		"summary":     "Boom",
		"description": "Plain text on v2",
		"assignee":    "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := receivedBody["fields"].(map[string]interface{})
	assignee, ok := fields["assignee"].(map[string]interface{})
	if !ok || assignee["name"] != "alice" {
		t.Errorf("expected v2 assignee name=alice, got %v", fields["assignee"])
	}
	if fields["description"] != "Plain text on v2" {
		t.Errorf("v2 description should pass through as string, got %T %v", fields["description"], fields["description"])
	}
}

func TestCreateIssue_DescriptionADFPassthrough(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	})

	adf := map[string]interface{}{
		"type":    "doc",
		"version": float64(1),
		"content": []interface{}{},
	}
	_, err := tool.CreateIssue(context.Background(), "test-incident", map[string]interface{}{
		"project_key": "FOO",
		"issue_type":  "Bug",
		"summary":     "Boom",
		"description": adf,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := receivedBody["fields"].(map[string]interface{})
	desc, ok := fields["description"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected description to be ADF object passthrough, got %T", fields["description"])
	}
	if desc["type"] != "doc" {
		t.Errorf("expected ADF doc, got %v", desc["type"])
	}
}

func TestTransitionIssue_V2_PlainStringComment(t *testing.T) {
	var receivedBody map[string]interface{}
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusNoContent)
	})
	getTestConfig(tool).APIVersion = "2"

	_, err := tool.TransitionIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":           "FOO-1",
		"transition_id": "31",
		"comment":       "fixed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	update := receivedBody["update"].(map[string]interface{})
	comments := update["comment"].([]interface{})
	add := comments[0].(map[string]interface{})["add"].(map[string]interface{})
	if add["body"] != "fixed" {
		t.Errorf("v2 comment body should be plain string, got %T %v", add["body"], add["body"])
	}
}

func TestCreateIssue_WriteGateBlocks(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.CreateIssue(context.Background(), "test-incident", map[string]interface{}{
		"project_key": "FOO",
		"issue_type":  "Bug",
		"summary":     "Boom",
	})
	if err == nil || !strings.Contains(err.Error(), "jira_allow_writes") {
		t.Fatalf("expected write-gate error, got %v", err)
	}
	if counter.Load() != 0 {
		t.Errorf("expected 0 server calls, got %d", counter.Load())
	}
}

func TestCreateIssue_MissingArgs(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"no project_key", map[string]interface{}{"issue_type": "Bug", "summary": "x"}, "project_key is required"},
		{"no issue_type", map[string]interface{}{"project_key": "FOO", "summary": "x"}, "issue_type is required"},
		{"no summary", map[string]interface{}{"project_key": "FOO", "issue_type": "Bug"}, "summary is required"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.CreateIssue(context.Background(), "test-incident", tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCreateIssue_InvalidDescriptionType(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	cases := []struct {
		name string
		desc interface{}
	}{
		{"number", float64(42)},
		{"bool", true},
		{"array", []interface{}{"line1", "line2"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.CreateIssue(context.Background(), "test-incident", map[string]interface{}{
				"project_key": "FOO",
				"issue_type":  "Bug",
				"summary":     "x",
				"description": tt.desc,
			})
			if err == nil || !strings.Contains(err.Error(), "description must be a string or object") {
				t.Errorf("expected description type error, got %v", err)
			}
		})
	}
}

// --- UpdateIssue tests ---

func TestUpdateIssue_Success(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedBody map[string]interface{}

	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusNoContent)
	})

	_, err := tool.UpdateIssue(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
		"fields": map[string]interface{}{
			"summary": "New title",
			"labels":  []interface{}{"updated"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", receivedMethod)
	}
	if receivedPath != "/rest/api/3/issue/FOO-1" {
		t.Errorf("path = %q", receivedPath)
	}
	fields, ok := receivedBody["fields"].(map[string]interface{})
	if !ok {
		t.Fatal("expected fields in body")
	}
	if fields["summary"] != "New title" {
		t.Errorf("summary = %v", fields["summary"])
	}
}

func TestUpdateIssue_WriteGateBlocks(t *testing.T) {
	tool, _, counter := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	_, err := tool.UpdateIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":    "FOO-1",
		"fields": map[string]interface{}{"summary": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "jira_allow_writes") {
		t.Fatalf("expected write-gate error, got %v", err)
	}
	if counter.Load() != 0 {
		t.Errorf("expected 0 server calls, got %d", counter.Load())
	}
}

func TestUpdateIssue_MissingArgs(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server")
	})

	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"no key", map[string]interface{}{"fields": map[string]interface{}{"summary": "x"}}, "key is required"},
		{"no fields", map[string]interface{}{"key": "FOO-1"}, "fields is required"},
		{"empty fields", map[string]interface{}{"key": "FOO-1", "fields": map[string]interface{}{}}, "at least one field"},
		{"wrong fields type", map[string]interface{}{"key": "FOO-1", "fields": "summary=foo"}, "fields is required"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.UpdateIssue(context.Background(), "test-incident", tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestUpdateIssue_HTTPError(t *testing.T) {
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errors":{"summary":"too long"}}`)
	})

	_, err := tool.UpdateIssue(context.Background(), "test-incident", map[string]interface{}{
		"key":    "FOO-1",
		"fields": map[string]interface{}{"summary": "x"},
	})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP error 400") {
		t.Errorf("expected 400 error, got: %v", err)
	}
}

// --- Write methods are not cached ---

func TestWriteMethods_NotCached(t *testing.T) {
	callCount := &atomic.Int32{}
	tool, _, _ := newWriteTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	})

	args := map[string]interface{}{
		"key":  "FOO-1",
		"body": "comment",
	}

	for i := 0; i < 3; i++ {
		if _, err := tool.AddComment(context.Background(), "test-incident", args); err != nil {
			t.Fatalf("AddComment %d: %v", i, err)
		}
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 server calls (writes never cached), got %d", callCount.Load())
	}
}
