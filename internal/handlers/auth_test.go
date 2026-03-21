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
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response["error"] != "Invalid request body" {
		t.Errorf("expected 'Invalid request body' error, got %q", response["error"])
	}
}

func TestAuthHandler_handleLogin_MissingCredentials(t *testing.T) {
	h := NewAuthHandler(nil)

	tests := []struct {
		name    string
		body    map[string]string
		wantErr string
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
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}
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
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
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
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}

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

// Tests for handleSetupStatus endpoint
func TestAuthHandler_handleSetupStatus_MethodNotAllowed(t *testing.T) {
	h := NewAuthHandler(nil)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/auth/setup-status", nil)
			w := httptest.NewRecorder()

			h.handleSetupStatus(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("handleSetupStatus(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// Tests for handleSetup endpoint
func TestAuthHandler_handleSetup_MethodNotAllowed(t *testing.T) {
	h := NewAuthHandler(nil)

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/auth/setup", nil)
			w := httptest.NewRecorder()

			h.handleSetup(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("handleSetup(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// Note: handleSetup requires jwtAuth to be non-nil for full testing
// Full integration tests are in integration_test.go

func TestSetupRequest_Fields(t *testing.T) {
	// Test JSON unmarshaling
	jsonData := `{"password":"testpassword123","confirm_password":"testpassword123"}`
	var req SetupRequest
	if err := json.Unmarshal([]byte(jsonData), &req); err != nil {
		t.Fatalf("Failed to unmarshal SetupRequest: %v", err)
	}
	if req.Password != "testpassword123" {
		t.Errorf("Password = %q, want %q", req.Password, "testpassword123")
	}
	if req.ConfirmPassword != "testpassword123" {
		t.Errorf("ConfirmPassword = %q, want %q", req.ConfirmPassword, "testpassword123")
	}
}

func TestSetupStatusResponse_Fields(t *testing.T) {
	resp := SetupStatusResponse{
		SetupRequired:  true,
		SetupCompleted: false,
	}

	// Test JSON marshaling
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal SetupStatusResponse: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal data: %v", err)
	}

	if decoded["setup_required"] != true {
		t.Errorf("setup_required = %v, want %v", decoded["setup_required"], true)
	}
	if decoded["setup_completed"] != false {
		t.Errorf("setup_completed = %v, want %v", decoded["setup_completed"], false)
	}
}

func TestLoginRequest_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		username string
		password string
	}{
		{
			name:     "empty values",
			json:     `{"username":"","password":""}`,
			username: "",
			password: "",
		},
		{
			name:     "whitespace values",
			json:     `{"username":"  ","password":"  "}`,
			username: "  ",
			password: "  ",
		},
		{
			name:     "special characters",
			json:     `{"username":"user@domain.com","password":"p@ss!word#123"}`,
			username: "user@domain.com",
			password: "p@ss!word#123",
		},
		{
			name:     "unicode characters",
			json:     `{"username":"пользователь","password":"пароль123"}`,
			username: "пользователь",
			password: "пароль123",
		},
		{
			name:     "extra fields ignored",
			json:     `{"username":"test","password":"pass","extra":"ignored"}`,
			username: "test",
			password: "pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req LoginRequest
			if err := json.Unmarshal([]byte(tt.json), &req); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if req.Username != tt.username {
				t.Errorf("Username = %q, want %q", req.Username, tt.username)
			}
			if req.Password != tt.password {
				t.Errorf("Password = %q, want %q", req.Password, tt.password)
			}
		})
	}
}

func TestAuthHandler_handleLogin_EmptyBody(t *testing.T) {
	h := NewAuthHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	w := httptest.NewRecorder()

	h.handleLogin(w, req)

	// Empty body should be treated as invalid JSON
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleLogin with empty body = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Note: Full handleLogin tests with credential validation require jwtAuth to be non-nil
// See integration_test.go for end-to-end tests

func TestAuthHandler_handleVerify_AllMethods(t *testing.T) {
	h := NewAuthHandler(nil)

	// Test that only GET is allowed
	allowedMethods := map[string]bool{
		http.MethodGet: true,
	}

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/auth/verify", nil)
			w := httptest.NewRecorder()

			h.handleVerify(w, req)

			if allowedMethods[method] {
				// GET should return 401 (not authenticated) since no user in context
				if w.Code != http.StatusUnauthorized {
					t.Errorf("handleVerify(%s) = %d, want %d", method, w.Code, http.StatusUnauthorized)
				}
			} else {
				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("handleVerify(%s) = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
				}
			}
		})
	}
}
