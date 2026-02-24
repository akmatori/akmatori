package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
)

func TestNewHTTPHandler(t *testing.T) {
	// Test with nil alertHandler
	h := NewHTTPHandler(nil)
	if h == nil {
		t.Fatal("NewHTTPHandler returned nil")
	}
	if h.alertHandler != nil {
		t.Error("alertHandler should be nil when passed nil")
	}
}

func TestHTTPHandler_handleHealth(t *testing.T) {
	h := NewHTTPHandler(nil)

	tests := []struct {
		name           string
		method         string
		expectedStatus int
		checkBody      bool
	}{
		{
			name:           "GET returns 200 OK",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			checkBody:      true,
		},
		{
			name:           "POST returns 405 Method Not Allowed",
			method:         http.MethodPost,
			expectedStatus: http.StatusMethodNotAllowed,
			checkBody:      false,
		},
		{
			name:           "PUT returns 405 Method Not Allowed",
			method:         http.MethodPut,
			expectedStatus: http.StatusMethodNotAllowed,
			checkBody:      false,
		},
		{
			name:           "DELETE returns 405 Method Not Allowed",
			method:         http.MethodDelete,
			expectedStatus: http.StatusMethodNotAllowed,
			checkBody:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/health", nil)
			w := httptest.NewRecorder()

			h.handleHealth(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("handleHealth() status = %d, want %d", w.Code, tt.expectedStatus)
			}

			if tt.checkBody {
				// Verify JSON response
				var response map[string]string
				if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
					t.Errorf("Failed to decode response: %v", err)
				}

				if response["status"] != "ok" {
					t.Errorf("response status = %q, want %q", response["status"], "ok")
				}

				if response["version"] == "" {
					t.Error("response version should not be empty")
				}

				// Check Content-Type header
				contentType := w.Header().Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
				}
			}
		})
	}
}

func TestHTTPHandler_SetupRoutes(t *testing.T) {
	t.Run("with nil alertHandler", func(t *testing.T) {
		h := NewHTTPHandler(nil)
		mux := http.NewServeMux()

		// Should not panic
		h.SetupRoutes(mux)

		// Health endpoint should be registered
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("health endpoint status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("with alertHandler routes registered", func(t *testing.T) {
		// Create minimal AlertHandler - just verify it doesn't panic on setup
		alertHandler := &AlertHandler{
			adapters: make(map[string]alerts.AlertAdapter),
		}
		h := NewHTTPHandler(alertHandler)
		mux := http.NewServeMux()

		// Should not panic during route setup
		h.SetupRoutes(mux)

		// Health should still work
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("health endpoint status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	// Suppress unused import warning
	_ = alerts.NormalizedAlert{}
}
