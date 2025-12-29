package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_Disabled(t *testing.T) {
	config := &AuthConfig{
		Enabled: false,
		APIKeys: []string{"test-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_NoKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"test-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_InvalidKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "invalid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_ValidKey_XAPIKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_ValidKey_Bearer(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer valid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_ValidKey_ApiKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "ApiKey valid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Enabled_ValidKey_QueryParam(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test?api_key=valid-key", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_SkipPaths_Exact(t *testing.T) {
	config := &AuthConfig{
		Enabled:   true,
		APIKeys:   []string{"valid-key"},
		SkipPaths: []string{"/health", "/metrics"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should skip auth for /health
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for skipped path, got %d", rec.Code)
	}

	// Should require auth for /api/test
	req = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for protected path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_SkipPaths_Prefix(t *testing.T) {
	config := &AuthConfig{
		Enabled:   true,
		APIKeys:   []string{"valid-key"},
		SkipPaths: []string{"/webhook/*"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should skip auth for /webhook/zabbix
	req := httptest.NewRequest(http.MethodPost, "/webhook/zabbix", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for skipped prefix path, got %d", rec.Code)
	}

	// Should skip auth for /webhook/slack
	req = httptest.NewRequest(http.MethodPost, "/webhook/slack", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for skipped prefix path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_MultipleKeys(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"key1", "key2", "key3"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test each key works
	for _, key := range config.APIKeys {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Key %s should be valid, got status %d", key, rec.Code)
		}
	}
}

func TestAuthMiddleware_SetEnabled(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	if !middleware.IsEnabled() {
		t.Error("Middleware should be enabled initially")
	}

	middleware.SetEnabled(false)

	if middleware.IsEnabled() {
		t.Error("Middleware should be disabled after SetEnabled(false)")
	}
}

func TestAuthMiddleware_AddRemoveKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"key1"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// key2 should not work initially
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "key2")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Error("key2 should not be valid initially")
	}

	// Add key2
	middleware.AddAPIKey("key2")

	// key2 should work now
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Error("key2 should be valid after adding")
	}

	// Remove key2
	middleware.RemoveAPIKey("key2")

	// key2 should not work anymore
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Error("key2 should not be valid after removal")
	}
}

func TestAuthMiddleware_WrapFunc(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handlerFunc := middleware.WrapFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "valid-key")
	rec := httptest.NewRecorder()

	handlerFunc(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WWWAuthenticateHeader(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	authHeader := rec.Header().Get("WWW-Authenticate")
	if authHeader != `Bearer realm="API"` {
		t.Errorf("Expected WWW-Authenticate header, got: %s", authHeader)
	}
}
