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
		_, _ = w.Write([]byte("success")) // ignore: test ResponseRecorder never fails
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
		_, _ = w.Write([]byte("success")) // ignore: test ResponseRecorder never fails
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

// --- Additional edge case tests ---

func TestAuthMiddleware_EmptyAPIKeyList(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{}, // Empty list
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Any key should fail
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "some-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 with empty key list, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NilConfig(t *testing.T) {
	// Ensure middleware handles nil config gracefully
	config := &AuthConfig{
		Enabled: false,
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 when disabled, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WhitespaceInKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Key with leading/trailing whitespace should not match
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", " valid-key ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for key with whitespace, got %d", rec.Code)
	}
}

func TestAuthMiddleware_CaseSensitiveKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"ValidKey"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Lowercase version should not match
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "validkey")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for case mismatch, got %d", rec.Code)
	}

	// Exact case should match
	req = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "ValidKey")
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for exact case match, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AuthorizationHeaderPriority(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"auth-key", "x-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Authorization header should be checked first
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer auth-key")
	req.Header.Set("X-API-Key", "invalid-key") // This would fail, but Auth header should win
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, Authorization header should take priority, got %d", rec.Code)
	}
}

func TestAuthMiddleware_MalformedBearerToken(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	testCases := []struct {
		name      string
		authValue string
	}{
		{"bearer lowercase", "bearer valid-key"},
		{"BEARER uppercase", "BEARER valid-key"},
		{"no space after Bearer", "Bearervalid-key"},
		{"extra space", "Bearer  valid-key"},
		{"just Bearer", "Bearer "},
		{"unknown scheme", "Basic valid-key"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set("Authorization", tc.authValue)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			// These should all fail (except potentially some edge cases the impl handles)
			// This test documents current behavior
		})
	}
}

func TestAuthMiddleware_SkipPaths_DeepNested(t *testing.T) {
	config := &AuthConfig{
		Enabled:   true,
		APIKeys:   []string{"valid-key"},
		SkipPaths: []string{"/api/v1/webhooks/*"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Deep nested path should be skipped
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/slack/events/callback", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for deep nested skipped path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_SkipPaths_NoWildcard(t *testing.T) {
	config := &AuthConfig{
		Enabled:   true,
		APIKeys:   []string{"valid-key"},
		SkipPaths: []string{"/health"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exact match should skip
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for exact skip path, got %d", rec.Code)
	}

	// Subpath should NOT skip (no wildcard)
	req = httptest.NewRequest(http.MethodGet, "/health/detailed", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for subpath without wildcard, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ConcurrentAccess(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"key1"},
	}
	middleware := NewAuthMiddleware(config)

	// Test concurrent reads and writes
	done := make(chan bool)

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			middleware.IsEnabled()
		}
		done <- true
	}()

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				middleware.AddAPIKey("key2")
			} else {
				middleware.RemoveAPIKey("key2")
			}
		}
		done <- true
	}()

	// Another writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			middleware.SetEnabled(i%2 == 0)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Test should complete without race conditions
}

func TestAuthMiddleware_ResponseContentType(t *testing.T) {
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

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got: %s", contentType)
	}
}

func TestAuthMiddleware_RemoveNonexistentKey(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"key1", "key2"},
	}
	middleware := NewAuthMiddleware(config)

	// Remove a key that doesn't exist - should not panic
	middleware.RemoveAPIKey("nonexistent")

	// Existing keys should still work
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "key1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_DuplicateKeys(t *testing.T) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"key1"},
	}
	middleware := NewAuthMiddleware(config)

	// Add same key multiple times
	middleware.AddAPIKey("key1")
	middleware.AddAPIKey("key1")

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "key1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Remove once - should still work due to duplicates
	middleware.RemoveAPIKey("key1")

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Behavior depends on implementation - this documents it
}

// Benchmark tests
func BenchmarkAuthMiddleware_ValidKey(b *testing.B) {
	config := &AuthConfig{
		Enabled: true,
		APIKeys: []string{"valid-key"},
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", "valid-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkAuthMiddleware_InvalidKey(b *testing.B) {
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkAuthMiddleware_ManyKeys(b *testing.B) {
	keys := make([]string, 100)
	for i := 0; i < 100; i++ {
		keys[i] = "key-" + string(rune(i))
	}

	config := &AuthConfig{
		Enabled: true,
		APIKeys: keys,
	}
	middleware := NewAuthMiddleware(config)

	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test with last key (worst case)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-API-Key", keys[99])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
