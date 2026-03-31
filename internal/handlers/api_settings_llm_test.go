package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLLMHandlerTest(t *testing.T) *APIHandler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate llm_settings: %v", err)
	}
	database.DB = db
	return NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func seedLLMConfig(t *testing.T, name string, provider database.LLMProvider, active bool) *database.LLMSettings {
	t.Helper()
	s := &database.LLMSettings{
		Name:          name,
		Provider:      provider,
		APIKey:        "sk-test-" + name,
		Model:         "gpt-4",
		ThinkingLevel: database.ThinkingLevelMedium,
		Enabled:       true,
		Active:        active,
	}
	if err := database.CreateLLMSettings(s); err != nil {
		t.Fatalf("seedLLMConfig: %v", err)
	}
	return s
}

func TestHandleLLMSettings_ListEmpty(t *testing.T) {
	h := setupLLMHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/llm", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	configs := resp["configs"].([]interface{})
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
	if resp["active_id"].(float64) != 0 {
		t.Errorf("expected active_id 0, got %v", resp["active_id"])
	}
}

func TestHandleLLMSettings_ListWithConfigs(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c1 := seedLLMConfig(t, "Production OpenAI", database.LLMProviderOpenAI, true)
	seedLLMConfig(t, "Dev Anthropic", database.LLMProviderAnthropic, false)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/llm", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	configs := resp["configs"].([]interface{})
	if len(configs) != 2 {
		t.Errorf("expected 2 configs, got %d", len(configs))
	}
	if uint(resp["active_id"].(float64)) != c1.ID {
		t.Errorf("expected active_id %d, got %v", c1.ID, resp["active_id"])
	}

	// Verify API key is masked
	first := configs[0].(map[string]interface{})
	apiKey := first["api_key"].(string)
	if apiKey == "sk-test-Production OpenAI" {
		t.Error("API key should be masked")
	}
}

func TestHandleLLMSettings_Create(t *testing.T) {
	h := setupLLMHandlerTest(t)

	body := `{"provider":"openai","name":"My Config","api_key":"sk-123","model":"gpt-4","thinking_level":"high"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != "My Config" {
		t.Errorf("expected name 'My Config', got %v", resp["name"])
	}
	if resp["provider"] != "openai" {
		t.Errorf("expected provider 'openai', got %v", resp["provider"])
	}
	if resp["thinking_level"] != "high" {
		t.Errorf("expected thinking_level 'high', got %v", resp["thinking_level"])
	}
	if resp["is_configured"] != true {
		t.Errorf("expected is_configured true")
	}
}

func TestHandleLLMSettings_Create_DefaultThinkingLevel(t *testing.T) {
	h := setupLLMHandlerTest(t)

	body := `{"provider":"openai","name":"Default TL","api_key":"sk-123","model":"gpt-4"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["thinking_level"] != "medium" {
		t.Errorf("expected default thinking_level 'medium', got %v", resp["thinking_level"])
	}
}

func TestHandleLLMSettings_Create_ValidationErrors(t *testing.T) {
	h := setupLLMHandlerTest(t)

	tests := []struct {
		name string
		body string
		want int
		msg  string
	}{
		{"missing provider", `{"name":"test"}`, http.StatusBadRequest, "provider is required"},
		{"missing name", `{"provider":"openai"}`, http.StatusBadRequest, "name is required"},
		{"invalid provider", `{"provider":"invalid","name":"test"}`, http.StatusBadRequest, "Invalid provider"},
		{"invalid thinking_level", `{"provider":"openai","name":"test","thinking_level":"ultra"}`, http.StatusBadRequest, "Invalid thinking_level"},
		{"invalid base_url", `{"provider":"openai","name":"test","base_url":"ftp://bad"}`, http.StatusBadRequest, "Invalid base_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			h.handleLLMSettings(w, req)

			if w.Code != tt.want {
				t.Errorf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleLLMSettings_Create_NameConflict(t *testing.T) {
	h := setupLLMHandlerTest(t)
	seedLLMConfig(t, "Existing", database.LLMProviderOpenAI, false)

	body := `{"provider":"anthropic","name":"Existing","api_key":"sk-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettingsByID_Get(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c := seedLLMConfig(t, "Test Config", database.LLMProviderOpenAI, false)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/settings/llm/%d", c.ID), nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != "Test Config" {
		t.Errorf("expected name 'Test Config', got %v", resp["name"])
	}
}

func TestHandleLLMSettingsByID_Get_NotFound(t *testing.T) {
	h := setupLLMHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/llm/999", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleLLMSettingsByID_Update(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c := seedLLMConfig(t, "Original", database.LLMProviderOpenAI, false)

	body := `{"name":"Updated Name","model":"gpt-5"}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/settings/llm/%d", c.ID), bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["name"] != "Updated Name" {
		t.Errorf("expected name 'Updated Name', got %v", resp["name"])
	}
	if resp["model"] != "gpt-5" {
		t.Errorf("expected model 'gpt-5', got %v", resp["model"])
	}
}

func TestHandleLLMSettingsByID_Update_NameConflict(t *testing.T) {
	h := setupLLMHandlerTest(t)
	seedLLMConfig(t, "Config A", database.LLMProviderOpenAI, false)
	b := seedLLMConfig(t, "Config B", database.LLMProviderAnthropic, false)

	body := `{"name":"Config A"}`
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/settings/llm/%d", b.ID), bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettingsByID_Update_ValidationErrors(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c := seedLLMConfig(t, "Config", database.LLMProviderOpenAI, false)

	tests := []struct {
		name string
		body string
	}{
		{"empty name", `{"name":""}`},
		{"invalid thinking_level", `{"thinking_level":"ultra"}`},
		{"invalid base_url", `{"base_url":"ftp://bad"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/settings/llm/%d", c.ID), bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			h.handleLLMSettingsByID(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleLLMSettingsByID_Delete(t *testing.T) {
	h := setupLLMHandlerTest(t)
	active := seedLLMConfig(t, "Active", database.LLMProviderOpenAI, true)
	inactive := seedLLMConfig(t, "Inactive", database.LLMProviderAnthropic, false)

	// Delete inactive config should succeed
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/settings/llm/%d", inactive.ID), nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for deleting inactive, got %d: %s", w.Code, w.Body.String())
	}

	// Delete active config should fail
	// Need another config first so it's not the last one
	seedLLMConfig(t, "Another", database.LLMProviderGoogle, false)
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/settings/llm/%d", active.ID), nil)
	w = httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for deleting active, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettingsByID_Delete_LastConfig(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c := seedLLMConfig(t, "Only", database.LLMProviderOpenAI, false)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/settings/llm/%d", c.ID), nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for deleting last config, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettingsByID_Delete_NotFound(t *testing.T) {
	h := setupLLMHandlerTest(t)
	// Need at least 2 configs so the "last config" check doesn't block us
	seedLLMConfig(t, "A", database.LLMProviderOpenAI, false)
	seedLLMConfig(t, "B", database.LLMProviderAnthropic, false)

	req := httptest.NewRequest(http.MethodDelete, "/api/settings/llm/999", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettingsByID_Activate(t *testing.T) {
	h := setupLLMHandlerTest(t)
	seedLLMConfig(t, "First", database.LLMProviderOpenAI, true)
	second := seedLLMConfig(t, "Second", database.LLMProviderAnthropic, false)

	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/settings/llm/%d/activate", second.ID), nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["active"] != true {
		t.Error("expected activated config to have active=true")
	}

	// Verify listing shows updated active_id
	req = httptest.NewRequest(http.MethodGet, "/api/settings/llm", nil)
	w = httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	var listResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if uint(listResp["active_id"].(float64)) != second.ID {
		t.Errorf("expected active_id %d, got %v", second.ID, listResp["active_id"])
	}
}

func TestHandleLLMSettingsByID_Activate_NotFound(t *testing.T) {
	h := setupLLMHandlerTest(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/llm/999/activate", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleLLMSettings_MethodNotAllowed(t *testing.T) {
	h := setupLLMHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/settings/llm", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettings(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleLLMSettingsByID_InvalidID(t *testing.T) {
	h := setupLLMHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/llm/abc", nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleLLMSettingsByID_Activate_MethodNotAllowed(t *testing.T) {
	h := setupLLMHandlerTest(t)
	c := seedLLMConfig(t, "Test", database.LLMProviderOpenAI, false)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/settings/llm/%d/activate", c.ID), nil)
	w := httptest.NewRecorder()
	h.handleLLMSettingsByID(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
