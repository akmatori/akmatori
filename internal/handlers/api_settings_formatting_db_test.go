//go:build cgo

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupFormattingHandlerTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	if err := db.AutoMigrate(&database.FormattingSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	db.Exec("DELETE FROM formatting_settings")

	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
}

func TestHandleFormattingSettings_GET_ReturnsDefaults(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/formatting", nil)
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var settings database.FormattingSettings
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if settings.Enabled {
		t.Error("expected default Enabled=false")
	}
	if settings.SystemPrompt != database.DefaultFormattingPrompt {
		t.Error("expected SystemPrompt to equal DefaultFormattingPrompt")
	}
	if settings.MaxTokens != 1500 {
		t.Errorf("expected default MaxTokens=1500, got %d", settings.MaxTokens)
	}
	if settings.Temperature != 0.2 {
		t.Errorf("expected default Temperature=0.2, got %f", settings.Temperature)
	}
}

func TestHandleFormattingSettings_PUT_ValidUpdate(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	body := `{"enabled": true, "system_prompt": "Respond as JSON with status and summary.", "max_tokens": 2048, "temperature": 0.5}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var settings database.FormattingSettings
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !settings.Enabled {
		t.Error("expected Enabled=true after update")
	}
	if settings.SystemPrompt != "Respond as JSON with status and summary." {
		t.Errorf("SystemPrompt did not round-trip, got %q", settings.SystemPrompt)
	}
	if settings.MaxTokens != 2048 {
		t.Errorf("expected MaxTokens=2048, got %d", settings.MaxTokens)
	}
	if settings.Temperature != 0.5 {
		t.Errorf("expected Temperature=0.5, got %f", settings.Temperature)
	}
}

func TestHandleFormattingSettings_PUT_PartialUpdate(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// Only update Enabled; other fields should remain at defaults.
	body := `{"enabled": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var settings database.FormattingSettings
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !settings.Enabled {
		t.Error("expected Enabled=true after partial update")
	}
	if settings.SystemPrompt != database.DefaultFormattingPrompt {
		t.Error("SystemPrompt should remain at default after partial update")
	}
	if settings.MaxTokens != 1500 {
		t.Errorf("MaxTokens should remain 1500, got %d", settings.MaxTokens)
	}
	if settings.Temperature != 0.2 {
		t.Errorf("Temperature should remain 0.2, got %f", settings.Temperature)
	}
}

func TestHandleFormattingSettings_PUT_ValidationBounds(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// system_prompt > 8KB.
	bigPrompt := strings.Repeat("x", 8*1024+1)
	bigPromptBody, err := json.Marshal(map[string]string{"system_prompt": bigPrompt})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{"max_tokens_zero", `{"max_tokens": 0}`},
		{"max_tokens_negative", `{"max_tokens": -1}`},
		{"max_tokens_too_high", `{"max_tokens": 8001}`},
		{"temperature_negative", `{"temperature": -0.1}`},
		{"temperature_too_high", `{"temperature": 2.1}`},
		{"system_prompt_too_long", string(bigPromptBody)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			h.handleFormattingSettings(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleFormattingSettings_PUT_AcceptsBoundaries(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// Boundary values that should be accepted.
	tests := []struct {
		name string
		body string
	}{
		{"max_tokens_min", `{"max_tokens": 1}`},
		{"max_tokens_max", `{"max_tokens": 8000}`},
		{"temperature_min", `{"temperature": 0}`},
		{"temperature_max", `{"temperature": 2}`},
		{"system_prompt_at_limit", `{"system_prompt": "` + strings.Repeat("x", 8*1024) + `"}`},
		{"system_prompt_empty", `{"system_prompt": ""}`},
		{"schema_example_empty", `{"output_schema_example": ""}`},
		{"schema_example_valid_object", `{"output_schema_example": "{\"status\":\"resolved\",\"summary\":\"ok\"}"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			h.handleFormattingSettings(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleFormattingSettings_PUT_OutputSchemaExample_RoundTrip(t *testing.T) {
	setupFormattingHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	example := `{"severity":"high","summary":"Pod crashed","affected_hosts":["host-1","host-2"]}`

	putBody, err := json.Marshal(map[string]string{"output_schema_example": example})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(string(putBody)))
	putW := httptest.NewRecorder()
	h.handleFormattingSettings(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %s", putW.Code, putW.Body.String())
	}

	// Verify PUT response contains the example.
	var putSettings database.FormattingSettings
	if err := json.NewDecoder(putW.Body).Decode(&putSettings); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if putSettings.OutputSchemaExample != example {
		t.Errorf("PUT response OutputSchemaExample = %q, want %q", putSettings.OutputSchemaExample, example)
	}

	// GET must return the persisted value.
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/formatting", nil)
	getW := httptest.NewRecorder()
	h.handleFormattingSettings(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var getSettings database.FormattingSettings
	if err := json.NewDecoder(getW.Body).Decode(&getSettings); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if getSettings.OutputSchemaExample != example {
		t.Errorf("GET OutputSchemaExample = %q, want %q", getSettings.OutputSchemaExample, example)
	}
}
