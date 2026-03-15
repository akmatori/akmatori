package httpconnector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// helper to create a bool pointer
func boolPtr(b bool) *bool { return &b }

// newTestConnector creates a ConnectorDef with a test server URL
func newTestConnector(baseURL string, tools []ToolDef, auth *AuthConfig) ConnectorDef {
	return ConnectorDef{
		ToolTypeName: "test-connector",
		BaseURL:      baseURL,
		AuthConfig:   auth,
		Tools:        tools,
	}
}

func TestExecute_GETWithPathParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/users/42" {
			t.Errorf("expected /api/users/42, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"name": "Alice"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "get_user",
			HTTPMethod: "GET",
			Path:       "/api/users/{{user_id}}",
			Params: []ToolParam{
				{Name: "user_id", Type: "string", Required: true, In: "path"},
			},
		},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "get_user", map[string]interface{}{
		"user_id": "42",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}

	var body map[string]string
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	if body["name"] != "Alice" {
		t.Errorf("expected name Alice, got %s", body["name"])
	}
}

func TestExecute_GETWithQueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "active" {
			t.Errorf("expected query param status=active, got %s", r.URL.Query().Get("status"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected query param limit=10, got %s", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{"item1", "item2"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "list_items",
			HTTPMethod: "GET",
			Path:       "/api/items",
			Params: []ToolParam{
				{Name: "status", Type: "string", Required: true, In: "query"},
				{Name: "limit", Type: "integer", Required: false, In: "query", Default: "10"},
			},
		},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "list_items", map[string]interface{}{
		"status": "active",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
}

func TestExecute_POSTWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatalf("failed to unmarshal body: %v", err)
		}
		if data["title"] != "Test Issue" {
			t.Errorf("expected title 'Test Issue', got %v", data["title"])
		}
		if data["priority"] != "high" {
			t.Errorf("expected priority 'high', got %v", data["priority"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "title": "Test Issue"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "create_issue",
			HTTPMethod: "POST",
			Path:       "/api/issues",
			ReadOnly:   boolPtr(false),
			Params: []ToolParam{
				{Name: "title", Type: "string", Required: true, In: "body"},
				{Name: "priority", Type: "string", Required: false, In: "body", Default: "medium"},
			},
		},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "create_issue", map[string]interface{}{
		"title":    "Test Issue",
		"priority": "high",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", result.StatusCode)
	}
}

func TestExecute_AuthBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-secret-token" {
			t.Errorf("expected 'Bearer my-secret-token', got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "check_status", HTTPMethod: "GET", Path: "/api/status"},
	}, &AuthConfig{
		Method:     AuthBearer,
		TokenField: "api_token",
	})

	result, err := executor.Execute(context.Background(), connector, "check_status", nil, Credentials{
		"api_token": "my-secret-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
}

func TestExecute_AuthBasic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("expected basic auth to be present")
		}
		if user != "admin" || pass != "secret123" {
			t.Errorf("expected admin:secret123, got %s:%s", user, pass)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "check_status", HTTPMethod: "GET", Path: "/api/status"},
	}, &AuthConfig{
		Method: AuthBasic,
	})

	result, err := executor.Execute(context.Background(), connector, "check_status", nil, Credentials{
		"username": "admin",
		"password": "secret123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
}

func TestExecute_AuthAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-Custom-Key")
		if apiKey != "key-12345" {
			t.Errorf("expected 'key-12345', got %q", apiKey)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "check_status", HTTPMethod: "GET", Path: "/api/status"},
	}, &AuthConfig{
		Method:     AuthAPIKey,
		TokenField: "api_key",
		HeaderName: "X-Custom-Key",
	})

	result, err := executor.Execute(context.Background(), connector, "check_status", nil, Credentials{
		"api_key": "key-12345",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
}

func TestExecute_AuthAPIKeyDefaultHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "key-12345" {
			t.Errorf("expected 'key-12345' in X-API-Key header, got %q", apiKey)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "check_status", HTTPMethod: "GET", Path: "/api/status"},
	}, &AuthConfig{
		Method:     AuthAPIKey,
		TokenField: "api_key",
	})

	_, err := executor.Execute(context.Background(), connector, "check_status", nil, Credentials{
		"api_key": "key-12345",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_ReadOnlyEnforcement(t *testing.T) {
	executor := New()
	defer executor.Stop()

	// A tool that is read-only (default) but uses POST should fail
	connector := newTestConnector("http://localhost", []ToolDef{
		{
			Name:       "create_something",
			HTTPMethod: "POST",
			Path:       "/api/things",
			// ReadOnly is nil, defaults to true
		},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "create_something", nil, nil)
	if err == nil {
		t.Fatal("expected error for read-only POST tool")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("expected read-only error, got: %v", err)
	}
}

func TestExecute_ReadOnlyExplicitFalseAllows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "delete_item",
			HTTPMethod: "DELETE",
			Path:       "/api/items/{{id}}",
			ReadOnly:   boolPtr(false),
			Params: []ToolParam{
				{Name: "id", Type: "string", Required: true, In: "path"},
			},
		},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "delete_item", map[string]interface{}{
		"id": "99",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 204 {
		t.Errorf("expected status 204, got %d", result.StatusCode)
	}
}

func TestExecute_ToolNotFound(t *testing.T) {
	executor := New()
	defer executor.Stop()

	connector := newTestConnector("http://localhost", []ToolDef{
		{Name: "get_user", HTTPMethod: "GET", Path: "/api/users"},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "nonexistent", nil, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestExecute_RequiredParamMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not have been made")
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "get_user",
			HTTPMethod: "GET",
			Path:       "/api/users",
			Params: []ToolParam{
				{Name: "user_id", Type: "string", Required: true, In: "query"},
			},
		},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "get_user", map[string]interface{}{}, nil)
	if err == nil {
		t.Fatal("expected error for missing required param")
	}
	if !strings.Contains(err.Error(), "required parameter") {
		t.Errorf("expected 'required parameter' in error, got: %v", err)
	}
}

func TestExecute_HeaderParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "req-123" {
			t.Errorf("expected X-Request-ID=req-123, got %q", r.Header.Get("X-Request-ID"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "get_status",
			HTTPMethod: "GET",
			Path:       "/api/status",
			Params: []ToolParam{
				{Name: "X-Request-ID", Type: "string", Required: false, In: "header"},
			},
		},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "get_status", map[string]interface{}{
		"X-Request-ID": "req-123",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_ResponseCaching(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int32{"call": callCount.Load()})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "get_data", HTTPMethod: "GET", Path: "/api/data"},
	}, nil)

	// First call should hit the server
	result1, err := executor.Execute(context.Background(), connector, "get_data", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call should be cached
	result2, err := executor.Execute(context.Background(), connector, "get_data", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected server to be called once (caching), got %d calls", callCount.Load())
	}

	// Both results should be the same
	if string(result1.Body) != string(result2.Body) {
		t.Errorf("expected cached result to match, got %s vs %s", result1.Body, result2.Body)
	}
}

func TestExecute_POSTNotCached(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int32{"call": callCount.Load()})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "create_item",
			HTTPMethod: "POST",
			Path:       "/api/items",
			ReadOnly:   boolPtr(false),
		},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "create_item", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = executor.Execute(context.Background(), connector, "create_item", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls (POST not cached), got %d", callCount.Load())
	}
}

func TestExecute_NonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("plain text response"))
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "get_text", HTTPMethod: "GET", Path: "/api/text"},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "get_text", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Non-JSON body should be wrapped as a JSON string
	var body string
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatalf("failed to unmarshal wrapped body: %v", err)
	}
	if body != "plain text response" {
		t.Errorf("expected 'plain text response', got %q", body)
	}
}

func TestExecute_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(200)
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{Name: "slow_call", HTTPMethod: "GET", Path: "/api/slow"},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := executor.Execute(ctx, connector, "slow_call", nil, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestExecute_AuthBearerMissingToken(t *testing.T) {
	executor := New()
	defer executor.Stop()

	connector := newTestConnector("http://localhost:1", []ToolDef{
		{Name: "get_data", HTTPMethod: "GET", Path: "/api/data"},
	}, &AuthConfig{
		Method:     AuthBearer,
		TokenField: "api_token",
	})

	_, err := executor.Execute(context.Background(), connector, "get_data", nil, Credentials{})
	if err == nil {
		t.Fatal("expected error for missing bearer token")
	}
	if !strings.Contains(err.Error(), "bearer token not found") {
		t.Errorf("expected bearer token error, got: %v", err)
	}
}

func TestExecute_AuthBasicMissingUsername(t *testing.T) {
	executor := New()
	defer executor.Stop()

	connector := newTestConnector("http://localhost:1", []ToolDef{
		{Name: "get_data", HTTPMethod: "GET", Path: "/api/data"},
	}, &AuthConfig{
		Method: AuthBasic,
	})

	_, err := executor.Execute(context.Background(), connector, "get_data", nil, Credentials{})
	if err == nil {
		t.Fatal("expected error for missing username")
	}
	if !strings.Contains(err.Error(), "username not found") {
		t.Errorf("expected username error, got: %v", err)
	}
}

func TestExecute_DefaultParamValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			t.Errorf("expected default page=1, got %s", r.URL.Query().Get("page"))
		}
		if r.URL.Query().Get("limit") != "25" {
			t.Errorf("expected default limit=25, got %s", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "list_items",
			HTTPMethod: "GET",
			Path:       "/api/items",
			Params: []ToolParam{
				{Name: "page", Type: "integer", Required: false, In: "query", Default: "1"},
				{Name: "limit", Type: "integer", Required: false, In: "query", Default: "25"},
			},
		},
	}, nil)

	// Call with no args - should use defaults
	_, err := executor.Execute(context.Background(), connector, "list_items", map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_PathParamEscaping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// URL path should have the space encoded
		if !strings.Contains(r.URL.RawPath, "hello%20world") && !strings.Contains(r.URL.Path, "hello world") {
			t.Errorf("expected path with 'hello world' encoded, got path=%s rawpath=%s", r.URL.Path, r.URL.RawPath)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "get_item",
			HTTPMethod: "GET",
			Path:       "/api/items/{{name}}",
			Params: []ToolParam{
				{Name: "name", Type: "string", Required: true, In: "path"},
			},
		},
	}, nil)

	_, err := executor.Execute(context.Background(), connector, "get_item", map[string]interface{}{
		"name": "hello world",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_PUTWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		if data["name"] != "Updated" {
			t.Errorf("expected name 'Updated', got %v", data["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}))
	defer server.Close()

	executor := NewWithClient(server.Client())
	defer executor.Stop()

	connector := newTestConnector(server.URL, []ToolDef{
		{
			Name:       "update_item",
			HTTPMethod: "PUT",
			Path:       "/api/items/{{id}}",
			ReadOnly:   boolPtr(false),
			Params: []ToolParam{
				{Name: "id", Type: "string", Required: true, In: "path"},
				{Name: "name", Type: "string", Required: true, In: "body"},
			},
		},
	}, nil)

	result, err := executor.Execute(context.Background(), connector, "update_item", map[string]interface{}{
		"id":   "1",
		"name": "Updated",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name     string
		template string
		params   []ToolParam
		args     map[string]interface{}
		want     string
	}{
		{
			name:     "single param",
			template: "/api/users/{{id}}",
			params:   []ToolParam{{Name: "id", In: "path"}},
			args:     map[string]interface{}{"id": "42"},
			want:     "/api/users/42",
		},
		{
			name:     "multiple params",
			template: "/api/orgs/{{org}}/repos/{{repo}}",
			params:   []ToolParam{{Name: "org", In: "path"}, {Name: "repo", In: "path"}},
			args:     map[string]interface{}{"org": "acme", "repo": "widgets"},
			want:     "/api/orgs/acme/repos/widgets",
		},
		{
			name:     "param with special chars is escaped",
			template: "/api/items/{{name}}",
			params:   []ToolParam{{Name: "name", In: "path"}},
			args:     map[string]interface{}{"name": "a/b"},
			want:     "/api/items/a%2Fb",
		},
		{
			name:     "no params",
			template: "/api/health",
			params:   nil,
			args:     nil,
			want:     "/api/health",
		},
		{
			name:     "non-path params ignored",
			template: "/api/items",
			params:   []ToolParam{{Name: "status", In: "query"}},
			args:     map[string]interface{}{"status": "active"},
			want:     "/api/items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePath(tt.template, tt.params, tt.args)
			if got != tt.want {
				t.Errorf("resolvePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInjectAuth(t *testing.T) {
	tests := []struct {
		name       string
		authConfig *AuthConfig
		creds      Credentials
		checkFn    func(t *testing.T, req *http.Request)
		wantErr    string
	}{
		{
			name:       "bearer token",
			authConfig: &AuthConfig{Method: AuthBearer, TokenField: "token"},
			creds:      Credentials{"token": "abc123"},
			checkFn: func(t *testing.T, req *http.Request) {
				if req.Header.Get("Authorization") != "Bearer abc123" {
					t.Errorf("expected 'Bearer abc123', got %q", req.Header.Get("Authorization"))
				}
			},
		},
		{
			name:       "api key with custom header",
			authConfig: &AuthConfig{Method: AuthAPIKey, TokenField: "key", HeaderName: "X-My-Key"},
			creds:      Credentials{"key": "secret"},
			checkFn: func(t *testing.T, req *http.Request) {
				if req.Header.Get("X-My-Key") != "secret" {
					t.Errorf("expected 'secret' in X-My-Key, got %q", req.Header.Get("X-My-Key"))
				}
			},
		},
		{
			name:       "empty method is no-op",
			authConfig: &AuthConfig{},
			creds:      Credentials{},
			checkFn: func(t *testing.T, req *http.Request) {
				if req.Header.Get("Authorization") != "" {
					t.Errorf("expected no Authorization header, got %q", req.Header.Get("Authorization"))
				}
			},
		},
		{
			name:       "unsupported method",
			authConfig: &AuthConfig{Method: "oauth2"},
			creds:      Credentials{},
			wantErr:    "unsupported auth method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://example.com", nil)
			err := injectAuth(req, tt.authConfig, tt.creds)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkFn != nil {
				tt.checkFn(t, req)
			}
		})
	}
}

func TestNew(t *testing.T) {
	executor := New()
	defer executor.Stop()

	if executor == nil {
		t.Fatal("expected executor to not be nil")
	}
	if executor.client == nil {
		t.Error("expected HTTP client to be initialized")
	}
	if executor.responseCache == nil {
		t.Error("expected response cache to be initialized")
	}
	if executor.rateLimiters == nil {
		t.Error("expected rate limiters map to be initialized")
	}
}
