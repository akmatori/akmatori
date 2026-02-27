package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	var capturedID string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should have X-Request-ID in response
	responseID := w.Header().Get(RequestIDHeader)
	if responseID == "" {
		t.Fatal("expected X-Request-ID header in response")
	}

	// Should be a valid UUID format
	if !isUUIDFormat(responseID) {
		t.Errorf("expected UUID format, got %q", responseID)
	}

	// Context should contain the same ID
	if capturedID != responseID {
		t.Errorf("context ID = %q, response ID = %q", capturedID, responseID)
	}
}

func TestRequestIDMiddleware_ReusesClientID(t *testing.T) {
	clientID := "my-custom-request-id-123"
	var capturedID string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = GetRequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(RequestIDHeader, clientID)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should reuse the client-provided ID
	responseID := w.Header().Get(RequestIDHeader)
	if responseID != clientID {
		t.Errorf("response ID = %q, want %q", responseID, clientID)
	}
	if capturedID != clientID {
		t.Errorf("context ID = %q, want %q", capturedID, clientID)
	}
}

func TestRequestIDMiddleware_UniqueIDs(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		id := w.Header().Get(RequestIDHeader)
		if ids[id] {
			t.Fatalf("duplicate request ID generated: %q", id)
		}
		ids[id] = true
	}
}

func TestGetRequestID_EmptyContext(t *testing.T) {
	id := GetRequestID(context.Background())
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestGenerateUUID(t *testing.T) {
	id := generateUUID()
	if !isUUIDFormat(id) {
		t.Errorf("expected UUID format, got %q", id)
	}

	// Check version 4 indicator
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d", len(parts))
	}
	if parts[2][0] != '4' {
		t.Errorf("expected version 4, got %c", parts[2][0])
	}
}

func isUUIDFormat(s string) bool {
	// Basic UUID format check: 8-4-4-4-12 hex characters
	parts := strings.Split(s, "-")
	if len(parts) != 5 {
		return false
	}
	expectedLens := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != expectedLens[i] {
			return false
		}
		for _, c := range part {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
	}
	return true
}
