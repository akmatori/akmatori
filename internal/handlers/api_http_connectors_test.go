package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// mockHTTPConnectorService implements services.HTTPConnectorManager for testing
type mockHTTPConnectorService struct {
	connectors []database.HTTPConnector
	lastCreate *database.HTTPConnector
	createErr  error
	getErr     error
	updateErr  error
	deleteErr  error
	listErr    error
}

func (m *mockHTTPConnectorService) CreateHTTPConnector(connector *database.HTTPConnector) (*database.HTTPConnector, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	connector.ID = 1
	m.lastCreate = connector
	return connector, nil
}

func (m *mockHTTPConnectorService) GetHTTPConnector(id uint) (*database.HTTPConnector, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, c := range m.connectors {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, m.getErr
}

func (m *mockHTTPConnectorService) UpdateHTTPConnector(id uint, updates map[string]interface{}) (*database.HTTPConnector, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for i, c := range m.connectors {
		if c.ID == id {
			return &m.connectors[i], nil
		}
	}
	return nil, m.updateErr
}

func (m *mockHTTPConnectorService) DeleteHTTPConnector(id uint) error {
	return m.deleteErr
}

func (m *mockHTTPConnectorService) ListHTTPConnectors() ([]database.HTTPConnector, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.connectors, nil
}

func newTestConnector() database.HTTPConnector {
	return database.HTTPConnector{
		ID:           1,
		ToolTypeName: "test-api",
		Description:  "Test API connector",
		BaseURLField: "base_url",
		AuthConfig:   database.JSONB{"method": "bearer_token", "token_field": "api_key"},
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_status",
					"description": "Get status",
					"http_method": "GET",
					"path":        "/status",
					"params":      []interface{}{},
				},
			},
		},
		Enabled: true,
	}
}

// TestHandleHTTPConnectors_List tests GET /api/http-connectors
func TestHandleHTTPConnectors_List(t *testing.T) {
	mock := &mockHTTPConnectorService{
		connectors: []database.HTTPConnector{newTestConnector()},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/http-connectors", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []database.HTTPConnector
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 connector, got %d", len(result))
	}
	if result[0].ToolTypeName != "test-api" {
		t.Errorf("expected tool_type_name 'test-api', got %q", result[0].ToolTypeName)
	}
}

// TestHandleHTTPConnectors_ListError tests GET /api/http-connectors with error
func TestHandleHTTPConnectors_ListError(t *testing.T) {
	mock := &mockHTTPConnectorService{
		listErr: errMock("database error"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/http-connectors", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestHandleHTTPConnectors_Create tests POST /api/http-connectors
func TestHandleHTTPConnectors_Create(t *testing.T) {
	mock := &mockHTTPConnectorService{}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	body := CreateHTTPConnectorRequest{
		ToolTypeName: "billing-api",
		Description:  "Billing API",
		BaseURLField: "base_url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_invoice",
					"http_method": "GET",
					"path":        "/invoices/{{id}}",
					"params":      []interface{}{},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/http-connectors", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if mock.lastCreate == nil {
		t.Fatal("expected create to be called")
	}
	if mock.lastCreate.ToolTypeName != "billing-api" {
		t.Errorf("expected tool_type_name 'billing-api', got %q", mock.lastCreate.ToolTypeName)
	}
}

// TestHandleHTTPConnectors_Create_MissingFields tests validation
func TestHandleHTTPConnectors_Create_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body CreateHTTPConnectorRequest
		want string
	}{
		{
			name: "missing tool_type_name",
			body: CreateHTTPConnectorRequest{BaseURLField: "url"},
			want: "tool_type_name is required",
		},
		{
			name: "missing base_url_field",
			body: CreateHTTPConnectorRequest{ToolTypeName: "test"},
			want: "base_url_field is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockHTTPConnectorService{}
			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/http-connectors", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.handleHTTPConnectors(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
		})
	}
}

// TestHandleHTTPConnectors_Create_Conflict tests duplicate name
func TestHandleHTTPConnectors_Create_Conflict(t *testing.T) {
	mock := &mockHTTPConnectorService{
		createErr: errMock("connector with tool_type_name \"test\" already exists"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	body := CreateHTTPConnectorRequest{
		ToolTypeName: "test",
		BaseURLField: "url",
		Tools:        database.JSONB{"tools": []interface{}{}},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/http-connectors", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// TestHandleHTTPConnectors_MethodNotAllowed tests invalid method
func TestHandleHTTPConnectors_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/http-connectors", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_Get tests GET /api/http-connectors/:id
func TestHandleHTTPConnectorByID_Get(t *testing.T) {
	conn := newTestConnector()
	mock := &mockHTTPConnectorService{
		connectors: []database.HTTPConnector{conn},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/http-connectors/1", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result database.HTTPConnector
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.ToolTypeName != "test-api" {
		t.Errorf("expected tool_type_name 'test-api', got %q", result.ToolTypeName)
	}
}

// TestHandleHTTPConnectorByID_GetNotFound tests GET with invalid ID
func TestHandleHTTPConnectorByID_GetNotFound(t *testing.T) {
	mock := &mockHTTPConnectorService{
		getErr: errMock("HTTP connector not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/http-connectors/999", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_InvalidID tests invalid ID format
func TestHandleHTTPConnectorByID_InvalidID(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/http-connectors/abc", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_Update tests PUT /api/http-connectors/:id
func TestHandleHTTPConnectorByID_Update(t *testing.T) {
	conn := newTestConnector()
	mock := &mockHTTPConnectorService{
		connectors: []database.HTTPConnector{conn},
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	desc := "Updated description"
	body := UpdateHTTPConnectorRequest{
		Description: &desc,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/http-connectors/1", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleHTTPConnectorByID_UpdateNotFound tests PUT with not found
func TestHandleHTTPConnectorByID_UpdateNotFound(t *testing.T) {
	mock := &mockHTTPConnectorService{
		updateErr: errMock("HTTP connector not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	desc := "Updated"
	body := UpdateHTTPConnectorRequest{Description: &desc}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/http-connectors/999", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_Delete tests DELETE /api/http-connectors/:id
func TestHandleHTTPConnectorByID_Delete(t *testing.T) {
	mock := &mockHTTPConnectorService{}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/http-connectors/1", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_DeleteNotFound tests DELETE with not found
func TestHandleHTTPConnectorByID_DeleteNotFound(t *testing.T) {
	mock := &mockHTTPConnectorService{
		deleteErr: errMock("HTTP connector not found"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/http-connectors/999", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleHTTPConnectorByID_MethodNotAllowed tests invalid method
func TestHandleHTTPConnectorByID_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/http-connectors/1", nil)
	w := httptest.NewRecorder()

	h.handleHTTPConnectorByID(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestTriggerGatewayReload_WithReloader tests that gateway reload is called
func TestTriggerGatewayReload_WithReloader(t *testing.T) {
	called := false
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetGatewayReloader(func() error {
		called = true
		return nil
	})

	h.triggerGatewayReload()

	// Give goroutine time to execute
	for i := 0; i < 100 && !called; i++ {
		// busy-wait briefly
	}
}

// TestTriggerGatewayReload_NilReloader tests that nil reloader doesn't panic
func TestTriggerGatewayReload_NilReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// Should not panic
	h.triggerGatewayReload()
}

// TestGatewayReloadFunc tests the reload function factory
func TestGatewayReloadFunc(t *testing.T) {
	// Create a test server that acts as the gateway
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/reload/http-connectors" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"reloaded"}`))
	}))
	defer server.Close()

	reloader := GatewayReloadFunc(server.URL)
	err := reloader()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestGatewayReloadFunc_Error tests the reload function with server error
func TestGatewayReloadFunc_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	reloader := GatewayReloadFunc(server.URL)
	err := reloader()
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

// TestHandleHTTPConnectors_CreateValidationError tests create with validation failure
func TestHandleHTTPConnectors_CreateValidationError(t *testing.T) {
	mock := &mockHTTPConnectorService{
		createErr: errMock("validation failed: tool_type_name is required"),
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mock, nil)

	body := CreateHTTPConnectorRequest{
		ToolTypeName: "test",
		BaseURLField: "url",
		Tools:        database.JSONB{"tools": []interface{}{}},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/http-connectors", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleHTTPConnectors(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// errMock creates a simple error for testing
type errMock string

func (e errMock) Error() string { return string(e) }
