package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

func TestGetIssueChangelog_APIVersion2_UsesExpandFallback(t *testing.T) {
	tool, _, _ := newTestTool(t, AuthTypeCloudBasic, func(w http.ResponseWriter, r *http.Request) {
		// v2 Server/DC lacks /issue/{key}/changelog; fall back to /issue/{key}?expand=changelog
		if r.URL.Path != "/rest/api/2/issue/FOO-1" {
			t.Errorf("path = %q, want v2 issue path", r.URL.Path)
		}
		if r.URL.Query().Get("expand") != "changelog" {
			t.Errorf("expected expand=changelog, got %q", r.URL.Query().Get("expand"))
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"key":"FOO-1","changelog":{"histories":[]}}`)
	})
	getTestConfig(tool).APIVersion = "2"

	out, err := tool.GetIssueChangelog(context.Background(), "test-incident", map[string]interface{}{
		"key": "FOO-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "changelog") {
		t.Errorf("expected response to contain changelog field, got %q", out)
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
	}{
		{"v3 string", "alice-id", "3", map[string]interface{}{"accountId": "alice-id"}},
		{"v2 string", "alice", "2", map[string]interface{}{"name": "alice"}},
		{"empty string", "  ", "3", nil},
		{"map passthrough", map[string]interface{}{"emailAddress": "a@b"}, "3", map[string]interface{}{"emailAddress": "a@b"}},
		{"nil", nil, "3", nil},
		{"wrong type", 42, "3", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assigneeRef(tt.input, tt.version)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("assigneeRef = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPriorityRef(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  interface{}
	}{
		{"string", "High", map[string]interface{}{"name": "High"}},
		{"empty string", "   ", nil},
		{"map passthrough", map[string]interface{}{"id": "1"}, map[string]interface{}{"id": "1"}},
		{"nil", nil, nil},
		{"wrong type", 10, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := priorityRef(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("priorityRef = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStringSlice(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  []string
	}{
		{"strings", []interface{}{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"trim and drop empty", []interface{}{" a ", "", "  "}, []string{"a"}},
		{"non-string elements dropped", []interface{}{"a", 42, true}, []string{"a"}},
		{"wrong type returns nil", "a,b,c", nil},
		{"nil returns nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSlice(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stringSlice = %v, want %v", got, tt.want)
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
		{"key": "FOO-1"},                                       // missing body
		{"key": "FOO-1", "body": ""},                           // empty string
		{"key": "FOO-1", "body": "   "},                        // whitespace
		{"key": "FOO-1", "body": map[string]interface{}{}},     // empty object
		{"key": "FOO-1", "body": 42},                           // wrong type
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
