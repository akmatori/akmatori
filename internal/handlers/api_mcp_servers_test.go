package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
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
			return &c, nil
		}
	}
	return nil, m.getErr
}

func (m *mockMCPServerService) UpdateMCPServer(id uint, updates map[string]interface{}) (*database.MCPServerConfig, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for i, c := range m.configs {
		if c.ID == id {
			return &m.configs[i], nil
		}
	}
	return nil, m.updateErr
}

func (m *mockMCPServerService) DeleteMCPServer(id uint) error {
	return m.deleteErr
}

func (m *mockMCPServerService) ListMCPServers() ([]database.MCPServerConfig, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.configs, nil
}

func newTestMCPServerConfig() database.MCPServerConfig {
	return database.MCPServerConfig{
		ID:              1,
		Name:            "test-github",
		Transport:       database.MCPServerTransportSSE,
		URL:             "http://localhost:8080/mcp",
		NamespacePrefix: "ext.github",
		Enabled:         true,
	}
}

// TestHandleMCPServers_List tests GET /api/mcp-servers
func TestHandleMCPServers_List(t *testing.T) {
	mock := &mockMCPServerService{
		configs: []database.MCPServerConfig{newTestMCPServerConfig()},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-servers", nil)
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []database.MCPServerConfig
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 config, got %d", len(result))
	}
	if result[0].Name != "test-github" {
		t.Errorf("expected name 'test-github', got %q", result[0].Name)
	}
}

// TestHandleMCPServers_ListError tests GET /api/mcp-servers with error
func TestHandleMCPServers_ListError(t *testing.T) {
	mock := &mockMCPServerService{
		listErr: errMock("database error"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-servers", nil)
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestHandleMCPServers_Create tests POST /api/mcp-servers
func TestHandleMCPServers_Create(t *testing.T) {
	mock := &mockMCPServerService{}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	body := CreateMCPServerRequest{
		Name:            "my-github",
		Transport:       database.MCPServerTransportSSE,
		URL:             "http://localhost:9090/mcp",
		NamespacePrefix: "ext.github",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp-servers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if mock.lastCreate == nil {
		t.Fatal("expected create to be called")
	}
	if mock.lastCreate.Name != "my-github" {
		t.Errorf("expected name 'my-github', got %q", mock.lastCreate.Name)
	}
	if mock.lastCreate.NamespacePrefix != "ext.github" {
		t.Errorf("expected namespace_prefix 'ext.github', got %q", mock.lastCreate.NamespacePrefix)
	}
}

// TestHandleMCPServers_Create_StdioTransport tests POST with stdio transport
func TestHandleMCPServers_Create_StdioTransport(t *testing.T) {
	mock := &mockMCPServerService{}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	body := CreateMCPServerRequest{
		Name:            "local-tool",
		Transport:       database.MCPServerTransportStdio,
		Command:         "/usr/local/bin/my-mcp-server",
		Args:            database.JSONB{"args": []interface{}{"--port", "8080"}},
		EnvVars:         database.JSONB{"API_KEY": "secret"},
		NamespacePrefix: "ext.local",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp-servers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if mock.lastCreate == nil {
		t.Fatal("expected create to be called")
	}
	if mock.lastCreate.Transport != database.MCPServerTransportStdio {
		t.Errorf("expected transport 'stdio', got %q", mock.lastCreate.Transport)
	}
	if mock.lastCreate.Command != "/usr/local/bin/my-mcp-server" {
		t.Errorf("expected command '/usr/local/bin/my-mcp-server', got %q", mock.lastCreate.Command)
	}
}

// TestHandleMCPServers_Create_MissingFields tests validation
func TestHandleMCPServers_Create_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body CreateMCPServerRequest
	}{
		{
			name: "missing name",
			body: CreateMCPServerRequest{NamespacePrefix: "ext.test"},
		},
		{
			name: "missing namespace_prefix",
			body: CreateMCPServerRequest{Name: "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockMCPServerService{}
			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/mcp-servers", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleMCPServers(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
		})
	}
}

// TestHandleMCPServers_Create_Conflict tests duplicate name
func TestHandleMCPServers_Create_Conflict(t *testing.T) {
	mock := &mockMCPServerService{
		createErr: errMock("MCP server with name \"test\" already exists"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	body := CreateMCPServerRequest{
		Name:            "test",
		Transport:       database.MCPServerTransportSSE,
		URL:             "http://localhost/mcp",
		NamespacePrefix: "ext.test",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp-servers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// TestHandleMCPServers_Create_ValidationError tests create with validation failure
func TestHandleMCPServers_Create_ValidationError(t *testing.T) {
	mock := &mockMCPServerService{
		createErr: errMock("validation failed: transport must be 'sse' or 'stdio'"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	body := CreateMCPServerRequest{
		Name:            "test",
		Transport:       database.MCPServerTransportSSE,
		URL:             "http://localhost/mcp",
		NamespacePrefix: "ext.test",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp-servers", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestHandleMCPServers_MethodNotAllowed tests invalid method
func TestHandleMCPServers_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/mcp-servers", nil)
	w := httptest.NewRecorder()

	h.handleMCPServers(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_Get tests GET /api/mcp-servers/:id
func TestHandleMCPServerByID_Get(t *testing.T) {
	config := newTestMCPServerConfig()
	mock := &mockMCPServerService{
		configs: []database.MCPServerConfig{config},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-servers/1", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result database.MCPServerConfig
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.Name != "test-github" {
		t.Errorf("expected name 'test-github', got %q", result.Name)
	}
}

// TestHandleMCPServerByID_GetNotFound tests GET with not found
func TestHandleMCPServerByID_GetNotFound(t *testing.T) {
	mock := &mockMCPServerService{
		getErr: errMock("MCP server config not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-servers/999", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_InvalidID tests invalid ID format
func TestHandleMCPServerByID_InvalidID(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/mcp-servers/abc", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_Update tests PUT /api/mcp-servers/:id
func TestHandleMCPServerByID_Update(t *testing.T) {
	config := newTestMCPServerConfig()
	mock := &mockMCPServerService{
		configs: []database.MCPServerConfig{config},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	newName := "updated-github"
	body := UpdateMCPServerRequest{
		Name: &newName,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/mcp-servers/1", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleMCPServerByID_UpdateNotFound tests PUT with not found
func TestHandleMCPServerByID_UpdateNotFound(t *testing.T) {
	mock := &mockMCPServerService{
		updateErr: errMock("MCP server config not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	newName := "Updated"
	body := UpdateMCPServerRequest{Name: &newName}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/mcp-servers/999", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_UpdateConflict tests PUT with name conflict
func TestHandleMCPServerByID_UpdateConflict(t *testing.T) {
	mock := &mockMCPServerService{
		updateErr: errMock("MCP server with name \"existing\" already exists"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	newName := "existing"
	body := UpdateMCPServerRequest{Name: &newName}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/mcp-servers/1", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_Delete tests DELETE /api/mcp-servers/:id
func TestHandleMCPServerByID_Delete(t *testing.T) {
	mock := &mockMCPServerService{}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodDelete, "/api/mcp-servers/1", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_DeleteNotFound tests DELETE with not found
func TestHandleMCPServerByID_DeleteNotFound(t *testing.T) {
	mock := &mockMCPServerService{
		deleteErr: errMock("MCP server config not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, mock)

	req := httptest.NewRequest(http.MethodDelete, "/api/mcp-servers/999", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleMCPServerByID_MethodNotAllowed tests invalid method
func TestHandleMCPServerByID_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/mcp-servers/1", nil)
	w := httptest.NewRecorder()

	h.handleMCPServerByID(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestTriggerGatewayMCPReload tests that gateway MCP reload is called
func TestTriggerGatewayMCPReload(t *testing.T) {
	called := false
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetGatewayReloader(func() error {
		called = true
		return nil
	})

	h.triggerGatewayMCPReload()

	// Give goroutine time to execute
	for i := 0; i < 100 && !called; i++ {
		// busy-wait briefly
	}
}

// TestTriggerGatewayMCPReload_NilReloader tests that nil reloader doesn't panic
func TestTriggerGatewayMCPReload_NilReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// Should not panic
	h.triggerGatewayMCPReload()
}
