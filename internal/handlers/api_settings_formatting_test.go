package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleFormattingSettings_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	methods := []string{http.MethodPost, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/settings/formatting", nil)
			w := httptest.NewRecorder()

			h.handleFormattingSettings(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405, got %d", w.Code)
			}
		})
	}
}

func TestHandleFormattingSettings_PUT_InvalidJSON(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleFormattingSettings_PUT_OutputSchemaExample_InvalidJSON(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// output_schema_example value is a string that is not valid JSON.
	body := `{"output_schema_example": "not valid json"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFormattingSettings_PUT_OutputSchemaExample_NonObject(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	cases := []struct {
		name  string
		value string // JSON value to encode as the output_schema_example string
	}{
		{"array", `[1, 2, 3]`},
		{"scalar_number", `42`},
		{"scalar_string", `"hello"`},
		{"scalar_bool", `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			body := `{"output_schema_example": ` + string(encoded) + `}`
			req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(body))
			w := httptest.NewRecorder()
			h.handleFormattingSettings(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: expected 400, got %d: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleFormattingSettings_PUT_OutputSchemaExample_Oversize(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// The value itself (including quotes) must exceed 8192 bytes.
	oversize := strings.Repeat("x", 8*1024+1)
	encoded, _ := json.Marshal(oversize)
	body := `{"output_schema_example": ` + string(encoded) + `}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/formatting", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleFormattingSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
