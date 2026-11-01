package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// mockMCPServerService implements services.MCPServerManager for testing
type mockMCPServerService struct {
	configs    []database.MCPServerConfig
	lastCreate *database.MCPServerConfig
	createErr  error
	getErr     error
	updateErr  error
	deleteErr  error
	listErr    error
	lastPatch  *services.MCPServerPatch
	deletedID  uint
}

func (m *mockMCPServerService) CreateMCPServer(config *database.MCPServerConfig) (*database.MCPServerConfig, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	config.ID = 1
	m.lastCreate = config
	return config, nil
}

func (m *mockMCPServerService) GetMCPServer(id uint) (*database.MCPServerConfig, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, c := range m.configs {
		if c.ID == id {
			cc := c
			return &cc, nil
		}
	}
	return nil, services.ErrMCPServerNotFound
}

func (m *mockMCPServerService) UpdateMCPServer(id uint, patch services.MCPServerPatch) (*database.MCPServerConfig, error) {
	m.lastPatch = &patch
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for i, c := range m.configs {
		if c.ID == id {
			return &m.configs[i], nil
		}
	}
	return nil, services.ErrMCPServerNotFound
}

func (m *mockMCPServerService) DeleteMCPServer(id uint) error {
	m.deletedID = id
	return m.deleteErr
}

func (m *mockMCPServerService) ListMCPServers() ([]database.MCPServerConfig, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.configs, nil
}

// fakeMCPGateway implements services.MCPGateway.
type fakeMCPGateway struct {
	mu        sync.Mutex
	lastTest  *services.MCPTestRequest
	testRes   *services.MCPTestResult
	testErr   error
	status    map[uint]services.MCPServerRuntimeStatus
	statusErr error
}

func (g *fakeMCPGateway) Test(_ context.Context, req services.MCPTestRequest) (*services.MCPTestResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastTest = &req
	return g.testRes, g.testErr
}

func (g *fakeMCPGateway) Status(context.Context) (map[uint]services.MCPServerRuntimeStatus, error) {
	return g.status, g.statusErr
}

func newTestMCPServerConfig() database.MCPServerConfig {
	return database.MCPServerConfig{
		ID:              1,
		Name:            "test-github",
		Transport:       database.MCPServerTransportHTTP,
		URL:             "https://mcp.example.com/mcp",
		NamespacePrefix: "ext.github",
		AuthConfig:      database.JSONB{"type": "header", "header_name": "X-Api-Key", "value": "s3cret"},
		Enabled:         true,
	}
}

func newMCPHandler(mock *mockMCPServerService) *APIHandler {
	return NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)
}

func doMCP(t *testing.T, h *APIHandler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	if path == "/api/mcp-servers" {
		h.handleMCPServers(w, req)
	} else {
		h.handleMCPServerByID(w, req)
	}
	return w
}

func TestHandleMCPServers_ListIsRedacted(t *testing.T) {
	mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
	w := doMCP(t, newMCPHandler(mock), http.MethodGet, "/api/mcp-servers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("secret leaked: %s", w.Body.String())
	}
	var result []mcpServerResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Name != "test-github" {
		t.Fatalf("result = %+v", result)
	}
	r := result[0]
	if r.AuthConfig["value"] != services.MCPMaskedSecret || r.AuthConfig["header_name"] != "X-Api-Key" {
		t.Fatalf("auth_config = %v", r.AuthConfig)
	}
	if r.Auth.Type != "header" || !r.Auth.HasSecret || r.Auth.HeaderName != "X-Api-Key" {
		t.Fatalf("auth summary = %+v", r.Auth)
	}
	if r.DisabledTools == nil || r.Unsupported != "" {
		t.Fatalf("disabled_tools/unsupported = %v %q", r.DisabledTools, r.Unsupported)
	}
}

func TestToMCPServerResponse_OAuthAndStdio(t *testing.T) {
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := database.MCPServerConfig{
		ID: 2, Name: "linear", Transport: database.MCPServerTransportStdio,
		AuthConfig: database.JSONB{"type": "oauth", "access_token": "at", "refresh_token": "rt", "client_secret": "cs", "client_id": "cid", "scope": "read", "expires_at": exp.Format(time.RFC3339)},
	}
	r := toMCPServerResponse(&c)
	if r.Unsupported == "" {
		t.Error("stdio row not marked unsupported")
	}
	for _, k := range []string{"access_token", "refresh_token", "client_secret"} {
		if r.AuthConfig[k] != services.MCPMaskedSecret {
			t.Errorf("%s = %v, want masked", k, r.AuthConfig[k])
		}
	}
	if r.AuthConfig["client_id"] != "cid" {
		t.Errorf("client_id = %v", r.AuthConfig["client_id"])
	}
	if !r.Auth.OAuthConnected || r.Auth.OAuthScope != "read" || r.Auth.OAuthExpiresAt == nil || !r.Auth.OAuthExpiresAt.Equal(exp) {
		t.Errorf("oauth summary = %+v", r.Auth)
	}
	if s := summarizeMCPAuth(nil); s.Type != "none" || s.HasSecret {
		t.Errorf("empty summary = %+v", s)
	}
}

func TestHandleMCPServers_ListError(t *testing.T) {
	w := doMCP(t, newMCPHandler(&mockMCPServerService{listErr: errMock("database error")}), http.MethodGet, "/api/mcp-servers", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleMCPServers_Create(t *testing.T) {
	mock := &mockMCPServerService{}
	h := newMCPHandler(mock)
	reloaded := make(chan struct{}, 1)
	h.SetMCPServerReloader(func() error { reloaded <- struct{}{}; return nil })

	w := doMCP(t, h, http.MethodPost, "/api/mcp-servers", `{"name":"github","url":"https://mcp.example.com/mcp","auth_config":{"type":"bearer","token":"tok"},"disabled_tools":["delete_repo"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"tok"`) {
		t.Fatalf("token leaked in create response: %s", w.Body.String())
	}
	if mock.lastCreate == nil || mock.lastCreate.URL != "https://mcp.example.com/mcp" || len(mock.lastCreate.DisabledTools) != 1 || mock.lastCreate.AuthConfig["token"] != "tok" {
		t.Fatalf("create input = %+v", mock.lastCreate)
	}
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway reload not triggered")
	}
}

func TestHandleMCPServers_CreateErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		err  error
		want int
	}{
		{"missing name", `{"url":"https://x"}`, nil, http.StatusBadRequest},
		{"bad json", `{`, nil, http.StatusBadRequest},
		{"conflict", `{"name":"a","url":"https://x"}`, fmt.Errorf("MCP server with name %q already exists: %w", "a", services.ErrMCPServerConflict), http.StatusConflict},
		{"validation", `{"name":"a","url":"x","transport":"stdio","command":"/bin/sh"}`, errors.New("validation failed: stdio MCP servers are not supported"), http.StatusBadRequest},
		{"internal", `{"name":"a","url":"https://x"}`, errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doMCP(t, newMCPHandler(&mockMCPServerService{createErr: tt.err}), http.MethodPost, "/api/mcp-servers", tt.body)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestHandleMCPServers_CreateUnknownConnector(t *testing.T) {
	mock := &mockMCPServerService{}
	h := newMCPHandler(mock)
	h.SetConnectorService(&fakeConnectorManager{getErr: services.ErrConnectorNotFound})
	w := doMCP(t, h, http.MethodPost, "/api/mcp-servers", `{"name":"a","url":"https://x.example.com","connector_uuid":"nope"}`)
	if w.Code != http.StatusNotFound || mock.lastCreate != nil {
		t.Fatalf("status = %d, created = %v", w.Code, mock.lastCreate)
	}
}

func TestHandleMCPServers_CreateLinksConnector(t *testing.T) {
	mock := &mockMCPServerService{}
	h := newMCPHandler(mock)
	cm := &fakeConnectorManager{}
	h.SetConnectorService(cm)
	w := doMCP(t, h, http.MethodPost, "/api/mcp-servers", `{"name":"a","url":"https://x.example.com"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if len(cm.linked) != 1 || cm.linked[0] != "mcp_server:1:" {
		t.Fatalf("linked = %v", cm.linked)
	}
}

func TestHandleMCPServers_MethodNotAllowed(t *testing.T) {
	w := doMCP(t, newMCPHandler(&mockMCPServerService{}), http.MethodPatch, "/api/mcp-servers", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleMCPServerByID_Get(t *testing.T) {
	mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
	h := newMCPHandler(mock)
	w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/1", "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/999", ""); w.Code != http.StatusNotFound {
		t.Errorf("missing: expected 404, got %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/1/unknown", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown subpath: expected 404, got %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/1", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: expected 405, got %d", w.Code)
	}
}

func TestHandleMCPServerByID_UpdateAuthSemantics(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantNil   bool
		wantClear bool
		wantValue map[string]interface{}
	}{
		{"absent", `{"name":"renamed"}`, true, false, nil},
		{"null clears", `{"auth_config":null}`, false, true, nil},
		{"object replaces", `{"auth_config":{"type":"bearer","token":"new"}}`, false, false, map[string]interface{}{"type": "bearer", "token": "new"}},
		{"masked secret passes through", `{"auth_config":{"type":"header","header_name":"X-Key","value":"********"}}`, false, false, map[string]interface{}{"type": "header", "header_name": "X-Key", "value": "********"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
			w := doMCP(t, newMCPHandler(mock), http.MethodPut, "/api/mcp-servers/1", tt.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			p := mock.lastPatch
			if tt.wantNil {
				if p.Auth != nil {
					t.Fatalf("auth patch = %+v, want nil", p.Auth)
				}
				if p.Name == nil || *p.Name != "renamed" {
					t.Fatalf("name patch = %v", p.Name)
				}
				return
			}
			if p.Auth == nil || p.Auth.Clear != tt.wantClear {
				t.Fatalf("auth patch = %+v", p.Auth)
			}
			for k, v := range tt.wantValue {
				if p.Auth.Value[k] != v {
					t.Fatalf("auth value %s = %v, want %v", k, p.Auth.Value[k], v)
				}
			}
		})
	}
}

func TestHandleMCPServerByID_UpdateRejectsCommandFields(t *testing.T) {
	for _, body := range []string{`{"command":"/bin/sh"}`, `{"args":{"args":["x"]}}`, `{"env_vars":{"A":"b"}}`, `{"auth_config":"string"}`, `{`} {
		mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
		w := doMCP(t, newMCPHandler(mock), http.MethodPut, "/api/mcp-servers/1", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
	// Explicit nulls / empty command are fine.
	mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
	w := doMCP(t, newMCPHandler(mock), http.MethodPut, "/api/mcp-servers/1", `{"command":"","args":null,"env_vars":null,"enabled":false}`)
	if w.Code != http.StatusOK || mock.lastPatch.Enabled == nil || *mock.lastPatch.Enabled {
		t.Fatalf("status = %d patch = %+v", w.Code, mock.lastPatch)
	}
}

func TestHandleMCPServerByID_UpdateErrors(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{services.ErrMCPServerNotFound, http.StatusNotFound},
		{fmt.Errorf("x: %w", services.ErrMCPServerConflict), http.StatusConflict},
		{errors.New("namespace_prefix \"grafana\" conflicts with a built-in tool namespace"), http.StatusConflict},
		{errors.New("validation failed: bad url"), http.StatusBadRequest},
		{errors.New("disk on fire"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}, updateErr: c.err}
		w := doMCP(t, newMCPHandler(mock), http.MethodPut, "/api/mcp-servers/1", `{"name":"x"}`)
		if w.Code != c.want {
			t.Errorf("%v: status = %d, want %d", c.err, w.Code, c.want)
		}
		if c.want == http.StatusInternalServerError && strings.Contains(w.Body.String(), "fire") {
			t.Errorf("internal error text leaked: %s", w.Body.String())
		}
	}
}

func TestHandleMCPServerByID_Delete(t *testing.T) {
	mock := &mockMCPServerService{}
	h := newMCPHandler(mock)
	cm := &fakeConnectorManager{}
	h.SetConnectorService(cm)
	w := doMCP(t, h, http.MethodDelete, "/api/mcp-servers/7", "")
	if w.Code != http.StatusNoContent || mock.deletedID != 7 || cm.pruned != 1 {
		t.Fatalf("status = %d deleted = %d pruned = %d", w.Code, mock.deletedID, cm.pruned)
	}
	mock.deleteErr = services.ErrMCPServerNotFound
	if w := doMCP(t, h, http.MethodDelete, "/api/mcp-servers/7", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing delete: %d", w.Code)
	}
}

func TestHandleMCPServerTestAndStatus_NoGateway(t *testing.T) {
	h := newMCPHandler(&mockMCPServerService{})
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/test", `{"url":"https://x.example.com"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("test without gateway: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/status", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status without gateway: %d", w.Code)
	}
}

func TestHandleMCPServerTest_Unsaved(t *testing.T) {
	gw := &fakeMCPGateway{testRes: &services.MCPTestResult{OK: true, Tools: []services.MCPTestTool{{Name: "list_issues"}}}}
	h := newMCPHandler(&mockMCPServerService{})
	h.SetMCPGateway(gw)

	w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/test", `{"url":" https://x.example.com/mcp ","auth_config":{"type":"bearer","token":"t"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var res services.MCPTestResult
	_ = json.NewDecoder(w.Body).Decode(&res)
	if !res.OK || len(res.Tools) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if gw.lastTest.URL != "https://x.example.com/mcp" || gw.lastTest.Transport != "http" || gw.lastTest.ID != 0 || gw.lastTest.AuthConfig["token"] != "t" {
		t.Fatalf("gateway request = %+v", gw.lastTest)
	}

	for name, body := range map[string]string{
		"bad url":    `{"url":"ftp://x"}`,
		"no url":     `{}`,
		"bad auth":   `{"url":"https://x.example.com","auth_config":{"type":"bearer"}}`,
		"auth shape": `{"url":"https://x.example.com","auth_config":[1]}`,
	} {
		if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/test", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, w.Code)
		}
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/test", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET test: %d", w.Code)
	}

	gw.testErr = errors.New("gateway unreachable")
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/test", `{"url":"https://x.example.com"}`); w.Code != http.StatusBadGateway {
		t.Errorf("gateway error: %d", w.Code)
	}
}

func TestHandleMCPServerTest_StoredMergesMaskedSecrets(t *testing.T) {
	gw := &fakeMCPGateway{testRes: &services.MCPTestResult{OK: false, AuthRequired: true, WWWAuthenticate: "Bearer"}}
	mock := &mockMCPServerService{configs: []database.MCPServerConfig{newTestMCPServerConfig()}}
	h := newMCPHandler(mock)
	h.SetMCPGateway(gw)

	// No body: the gateway uses the stored URL and auth.
	w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/1/test", "")
	if w.Code != http.StatusOK || gw.lastTest.ID != 1 || gw.lastTest.AuthConfig != nil {
		t.Fatalf("status = %d req = %+v", w.Code, gw.lastTest)
	}
	// Masked secret with a new header name: stored secret filled in.
	w = doMCP(t, h, http.MethodPost, "/api/mcp-servers/1/test", `{"auth_config":{"type":"header","header_name":"X-Other","value":"********"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if gw.lastTest.AuthConfig["value"] != "s3cret" || gw.lastTest.AuthConfig["header_name"] != "X-Other" {
		t.Fatalf("merged auth = %v", gw.lastTest.AuthConfig)
	}
	// Unknown stored server.
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/99/test", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown id: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/99/test", `{"auth_config":{"type":"bearer","token":"x"}}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown id with auth: %d", w.Code)
	}
}

func TestHandleMCPServerStatus(t *testing.T) {
	gw := &fakeMCPGateway{status: map[uint]services.MCPServerRuntimeStatus{1: {ID: 1, Connected: true, ToolCount: 3}}}
	h := newMCPHandler(&mockMCPServerService{})
	h.SetMCPGateway(gw)
	w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Servers []services.MCPServerRuntimeStatus `json:"servers"`
	}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if len(body.Servers) != 1 || body.Servers[0].ToolCount != 3 {
		t.Fatalf("body = %+v", body)
	}
	gw.statusErr = errors.New("down")
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/status", ""); w.Code != http.StatusBadGateway {
		t.Errorf("gateway error: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/status", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status: %d", w.Code)
	}
}

func TestHandleMCPOAuthRoutes_NotConfigured(t *testing.T) {
	h := newMCPHandler(&mockMCPServerService{})
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/1/oauth/start", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("start: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/1/oauth/disconnect", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("disconnect: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/oauth/callback?state=x&code=y", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("callback: %d", w.Code)
	}
	if w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/1/oauth/start", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET start: %d", w.Code)
	}
}

func TestRawPresent(t *testing.T) {
	cases := map[string]bool{"": false, "null": false, "  null ": false, "{}": true, `"x"`: true, "[]": true}
	for in, want := range cases {
		if got := rawPresent(json.RawMessage(in)); got != want {
			t.Errorf("rawPresent(%q) = %v", in, got)
		}
	}
}

// TestTriggerGatewayMCPReload tests that gateway MCP reload is called
func TestTriggerGatewayMCPReload(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetMCPServerReloader(func() error {
		called <- struct{}{}
		return nil
	})
	h.triggerGatewayMCPReload()
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("reloader not called")
	}
}

// TestTriggerGatewayMCPReload_NilReloader tests that nil reloader doesn't panic
func TestTriggerGatewayMCPReload_NilReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.triggerGatewayMCPReload()
}
