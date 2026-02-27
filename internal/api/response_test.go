package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRespondJSON(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		data       interface{}
		wantStatus int
		wantBody   string
	}{
		{
			name:       "200 with data",
			status:     http.StatusOK,
			data:       map[string]string{"key": "value"},
			wantStatus: http.StatusOK,
			wantBody:   `{"key":"value"}`,
		},
		{
			name:       "201 created",
			status:     http.StatusCreated,
			data:       map[string]int{"id": 42},
			wantStatus: http.StatusCreated,
			wantBody:   `{"id":42}`,
		},
		{
			name:       "nil data",
			status:     http.StatusOK,
			data:       nil,
			wantStatus: http.StatusOK,
			wantBody:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			RespondJSON(w, tt.status, tt.data)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.data != nil {
				ct := w.Header().Get("Content-Type")
				if ct != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", ct)
				}
			}
			if tt.wantBody != "" {
				// json.Encoder appends a newline
				got := w.Body.String()
				if got != tt.wantBody+"\n" {
					t.Errorf("body = %q, want %q", got, tt.wantBody+"\n")
				}
			}
		})
	}
}

func TestRespondError(t *testing.T) {
	w := httptest.NewRecorder()
	RespondError(w, http.StatusBadRequest, "invalid input")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "invalid input" {
		t.Errorf("error = %q, want %q", resp.Error, "invalid input")
	}
	if resp.Code != "" {
		t.Errorf("code = %q, want empty", resp.Code)
	}
}

func TestRespondErrorWithCode(t *testing.T) {
	w := httptest.NewRecorder()
	RespondErrorWithCode(w, http.StatusConflict, "duplicate_name", "name already exists")

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "name already exists" {
		t.Errorf("error = %q, want %q", resp.Error, "name already exists")
	}
	if resp.Code != "duplicate_name" {
		t.Errorf("code = %q, want %q", resp.Code, "duplicate_name")
	}
}

func TestRespondValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	RespondValidationError(w, map[string]string{
		"name":  "is required",
		"email": "is invalid",
	})

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnprocessableEntity)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "Validation failed" {
		t.Errorf("error = %q, want %q", resp.Error, "Validation failed")
	}
	if resp.Code != "validation_error" {
		t.Errorf("code = %q, want %q", resp.Code, "validation_error")
	}
	if resp.Details["name"] != "is required" {
		t.Errorf("details[name] = %q, want %q", resp.Details["name"], "is required")
	}
	if resp.Details["email"] != "is invalid" {
		t.Errorf("details[email] = %q, want %q", resp.Details["email"], "is invalid")
	}
}

func TestRespondNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	RespondNoContent(w)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0", w.Body.Len())
	}
}

func TestErrorResponse_OmitsEmptyFields(t *testing.T) {
	resp := ErrorResponse{Error: "not found"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := m["code"]; ok {
		t.Error("empty code should be omitted from JSON")
	}
	if _, ok := m["details"]; ok {
		t.Error("nil details should be omitted from JSON")
	}
}
