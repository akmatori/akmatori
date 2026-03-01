package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestJWTMiddleware(setupMode bool) *JWTAuthMiddleware {
	hash, _ := HashPassword("test-password")
	return NewJWTAuthMiddleware(&JWTAuthConfig{
		Enabled:           true,
		SetupMode:         setupMode,
		AdminUsername:     "admin",
		AdminPasswordHash: hash,
		JWTSecret:         "test-secret-key-for-testing",
		JWTExpiryHours:    24,
		SkipPaths: []string{
			"/health",
			"/webhook/*",
			"/auth/login",
			"/auth/setup",
			"/auth/setup-status",
		},
	})
}

func TestJWTAuth_SetupMode_AllowsSetupPaths(t *testing.T) {
	m := newTestJWTMiddleware(true)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowedPaths := []string{"/auth/setup", "/auth/setup-status", "/health"}

	for _, path := range allowedPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected 200 for %s in setup mode, got %d", path, rec.Code)
			}
		})
	}
}

func TestJWTAuth_SetupMode_BlocksOtherPaths(t *testing.T) {
	m := newTestJWTMiddleware(true)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	blockedPaths := []string{"/api/incidents", "/auth/login", "/auth/verify", "/api/settings"}

	for _, path := range blockedPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("Expected 503 for %s in setup mode, got %d", path, rec.Code)
			}

			// Check the error code
			var resp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}
			if resp["code"] != "setup_required" {
				t.Errorf("Expected code 'setup_required', got '%v'", resp["code"])
			}
		})
	}
}

func TestJWTAuth_SetupMode_CompleteSetup(t *testing.T) {
	m := newTestJWTMiddleware(true)

	if !m.IsSetupMode() {
		t.Error("Should be in setup mode initially")
	}

	newHash, _ := HashPassword("new-password")
	m.CompleteSetup(newHash)

	if m.IsSetupMode() {
		t.Error("Should not be in setup mode after CompleteSetup")
	}

	// Verify new password works
	if !m.ValidateCredentials("admin", "new-password") {
		t.Error("New password should validate after CompleteSetup")
	}
}

func TestJWTAuth_NormalMode_SkipPaths(t *testing.T) {
	m := newTestJWTMiddleware(false)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	skipPaths := []string{"/health", "/webhook/zabbix", "/auth/login", "/auth/setup", "/auth/setup-status"}

	for _, path := range skipPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected 200 for skip path %s, got %d", path, rec.Code)
			}
		})
	}
}

func TestJWTAuth_NormalMode_RequiresToken(t *testing.T) {
	m := newTestJWTMiddleware(false)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/incidents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for missing token, got %d", rec.Code)
	}
}

func TestJWTAuth_NormalMode_ValidToken(t *testing.T) {
	m := newTestJWTMiddleware(false)

	// Generate a token
	token, err := m.GenerateToken("admin")
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user != "admin" {
			t.Errorf("Expected user 'admin' in context, got '%s'", user)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/incidents", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 for valid token, got %d", rec.Code)
	}
}

func TestJWTAuth_NormalMode_InvalidToken(t *testing.T) {
	m := newTestJWTMiddleware(false)

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/incidents", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-here")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for invalid token, got %d", rec.Code)
	}
}

func TestJWTAuth_ValidateCredentials(t *testing.T) {
	m := newTestJWTMiddleware(false)

	if !m.ValidateCredentials("admin", "test-password") {
		t.Error("Should validate correct credentials")
	}

	if m.ValidateCredentials("admin", "wrong-password") {
		t.Error("Should reject wrong password")
	}

	if m.ValidateCredentials("wrong-user", "test-password") {
		t.Error("Should reject wrong username")
	}
}

func TestJWTAuth_GetAdminUsername(t *testing.T) {
	m := newTestJWTMiddleware(false)

	if username := m.GetAdminUsername(); username != "admin" {
		t.Errorf("Expected 'admin', got '%s'", username)
	}
}

func TestJWTAuth_IsSetupMode(t *testing.T) {
	m := newTestJWTMiddleware(true)
	if !m.IsSetupMode() {
		t.Error("Should be in setup mode")
	}

	m2 := newTestJWTMiddleware(false)
	if m2.IsSetupMode() {
		t.Error("Should not be in setup mode")
	}
}

func TestJWTAuth_Disabled(t *testing.T) {
	m := NewJWTAuthMiddleware(&JWTAuthConfig{
		Enabled: false,
	})

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/incidents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 when auth disabled, got %d", rec.Code)
	}
}

func TestIsSetupPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/auth/setup", true},
		{"/auth/setup-status", true},
		{"/health", true},
		{"/auth/login", false},
		{"/api/incidents", false},
		{"/auth/verify", false},
		{"/auth/setup/extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if result := isSetupPath(tt.path); result != tt.expected {
				t.Errorf("isSetupPath(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("test-password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if hash == "" {
		t.Error("Expected non-empty hash")
	}

	if hash == "test-password" {
		t.Error("Hash should not be the plaintext password")
	}

	if !CheckPassword("test-password", hash) {
		t.Error("CheckPassword should validate the correct password")
	}

	if CheckPassword("wrong-password", hash) {
		t.Error("CheckPassword should reject the wrong password")
	}
}
