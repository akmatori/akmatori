package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAuthHandler(t *testing.T) {
	h := NewAuthHandler(nil)
	if h == nil {
		t.Fatal("NewAuthHandler returned nil")
	}
}

func TestAuthHandler_SetupRoutes(t *testing.T) {
	h := NewAuthHandler(nil)
	mux := http.NewServeMux()

	// Should not panic
	h.SetupRoutes(mux)
}

func TestAuthHandler_handleLogin_MethodNotAllowed(t *testing.T) {
	h := NewAuthHandler(nil)

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/auth/login", nil)
			w := httptest.NewRecorder()

			h.handleLogin(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("handleLogin(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestAuthHandler_handleLogin_InvalidJSON(t *testing.T) {
	h := NewAuthHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()

	h.handleLogin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("handleLogin with invalid JSON = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var response map[string]string
	json.NewDecoder(w.Body).Decode(&response)
	if response["error"] != "Invalid request body" {
		t.Errorf("expected 'Invalid request body' error, got %q", response["error"])
	}
}

func TestAuthHandler_handleLogin_MissingCredentials(t *testing.T) {
	h := NewAuthHandler(nil)

	tests := []struct {
		name     string
		body     map[string]string
		wantErr  string
	}{
		{
			name:    "empty username",
			body:    map[string]string{"username": "", "password": "test"},
			wantErr: "Username and password are required",
		},
		{
			name:    "empty password",
			body:    map[string]string{"username": "test", "password": ""},
			wantErr: "Username and password are required",
		},
		{
			name:    "both empty",
			body:    map[string]string{"username": "", "password": ""},
			wantErr: "Username and password are required",
		},
		{
			name:    "missing fields",
			body:    map[string]string{},
			wantErr: "Username and password are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer(body))
			w := httptest.NewRecorder()

			h.handleLogin(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("handleLogin = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var response map[string]string
			json.NewDecoder(w.Body).Decode(&response)
			if response["error"] != tt.wantErr {
				t.Errorf("expected %q error, got %q", tt.wantErr, response["error"])
			}
		})
	}
}

func TestAuthHandler_handleVerify_MethodNotAllowed(t *testing.T) {
	h := NewAuthHandler(nil)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/auth/verify", nil)
			w := httptest.NewRecorder()

			h.handleVerify(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("handleVerify(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestAuthHandler_handleVerify_NoUser(t *testing.T) {
	h := NewAuthHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
	w := httptest.NewRecorder()

	h.handleVerify(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleVerify without user = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var response map[string]string
	json.NewDecoder(w.Body).Decode(&response)
	if response["error"] != "Not authenticated" {
		t.Errorf("expected 'Not authenticated' error, got %q", response["error"])
	}
}

func TestLoginRequest_Fields(t *testing.T) {
	// Test JSON unmarshaling
	jsonData := `{"username":"testuser","password":"testpass"}`
	var req LoginRequest
	if err := json.Unmarshal([]byte(jsonData), &req); err != nil {
		t.Fatalf("Failed to unmarshal LoginRequest: %v", err)
	}
	if req.Username != "testuser" {
		t.Errorf("Username = %q, want %q", req.Username, "testuser")
	}
	if req.Password != "testpass" {
		t.Errorf("Password = %q, want %q", req.Password, "testpass")
	}
}

func TestLoginResponse_Fields(t *testing.T) {
	resp := LoginResponse{
		Token:     "test-token",
		Username:  "testuser",
		ExpiresIn: 86400,
	}

	// Test JSON marshaling
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal LoginResponse: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["token"] != "test-token" {
		t.Errorf("token = %v, want %q", decoded["token"], "test-token")
	}
	if decoded["username"] != "testuser" {
		t.Errorf("username = %v, want %q", decoded["username"], "testuser")
	}
	if decoded["expires_in"] != float64(86400) {
		t.Errorf("expires_in = %v, want %v", decoded["expires_in"], 86400)
	}
}
