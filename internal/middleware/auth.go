package middleware

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/akmatori/akmatori/internal/database"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	// APIKeys is a list of valid API keys (loaded from database or config)
	APIKeys []string

	// SkipPaths are paths that don't require authentication
	SkipPaths []string

	// Enabled determines if authentication is enforced
	Enabled bool
}

// AuthMiddleware provides API key authentication
type AuthMiddleware struct {
	config   *AuthConfig
	mu       sync.RWMutex
	skipMap  map[string]bool
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(config *AuthConfig) *AuthMiddleware {
	m := &AuthMiddleware{
		config:  config,
		skipMap: make(map[string]bool),
	}

	// Build skip paths map for O(1) lookup
	for _, path := range config.SkipPaths {
		m.skipMap[path] = true
	}

	return m
}

// LoadAPIKeysFromDB loads API keys from the database
// This allows hot-reloading of API keys without restart
func (m *AuthMiddleware) LoadAPIKeysFromDB() error {
	settings, err := database.GetAPIKeySettings()
	if err != nil {
		// If no settings exist, auth is disabled
		m.mu.Lock()
		m.config.Enabled = false
		m.mu.Unlock()
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = settings.Enabled
	m.config.APIKeys = settings.GetActiveKeys()

	if m.config.Enabled {
		log.Printf("AuthMiddleware: Loaded %d API keys, authentication ENABLED", len(m.config.APIKeys))
	} else {
		log.Printf("AuthMiddleware: Authentication DISABLED")
	}

	return nil
}

// Wrap wraps an http.Handler with authentication
func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if auth is enabled
		m.mu.RLock()
		enabled := m.config.Enabled
		apiKeys := m.config.APIKeys
		m.mu.RUnlock()

		if !enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Check if path should skip authentication
		if m.shouldSkipAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Extract API key from request
		apiKey := m.extractAPIKey(r)
		if apiKey == "" {
			m.unauthorized(w, "Missing API key")
			return
		}

		// Validate API key using constant-time comparison
		if !m.validateAPIKey(apiKey, apiKeys) {
			log.Printf("AuthMiddleware: Invalid API key attempt from %s", r.RemoteAddr)
			m.unauthorized(w, "Invalid API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// WrapFunc wraps an http.HandlerFunc with authentication
func (m *AuthMiddleware) WrapFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.Wrap(http.HandlerFunc(next)).ServeHTTP(w, r)
	}
}

// shouldSkipAuth checks if the path should skip authentication
func (m *AuthMiddleware) shouldSkipAuth(path string) bool {
	// Check exact match
	if m.skipMap[path] {
		return true
	}

	// Check prefix matches (for paths like /health, /webhook/*)
	for skipPath := range m.skipMap {
		if strings.HasSuffix(skipPath, "*") {
			prefix := strings.TrimSuffix(skipPath, "*")
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}

	return false
}

// extractAPIKey extracts the API key from the request
// Supports: Authorization header (Bearer/ApiKey), X-API-Key header, query param
func (m *AuthMiddleware) extractAPIKey(r *http.Request) string {
	// Try Authorization header first
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		// Support "Bearer <key>" format
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimPrefix(authHeader, "Bearer ")
		}
		// Support "ApiKey <key>" format
		if strings.HasPrefix(authHeader, "ApiKey ") {
			return strings.TrimPrefix(authHeader, "ApiKey ")
		}
	}

	// Try X-API-Key header
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return apiKey
	}

	// Try query parameter (less secure, but useful for some use cases)
	if apiKey := r.URL.Query().Get("api_key"); apiKey != "" {
		return apiKey
	}

	return ""
}

// validateAPIKey validates an API key against the list of valid keys
// Uses constant-time comparison to prevent timing attacks
func (m *AuthMiddleware) validateAPIKey(provided string, validKeys []string) bool {
	for _, valid := range validKeys {
		if subtle.ConstantTimeCompare([]byte(provided), []byte(valid)) == 1 {
			return true
		}
	}
	return false
}

// unauthorized sends an unauthorized response
func (m *AuthMiddleware) unauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer realm=\"API\"")
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write([]byte(`{"error":"` + message + `"}`)); err != nil {
		log.Printf("Failed to write error response: %v", err)
	}
}

// SetEnabled enables or disables authentication
func (m *AuthMiddleware) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Enabled = enabled
}

// AddAPIKey adds an API key to the valid keys list
func (m *AuthMiddleware) AddAPIKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.APIKeys = append(m.config.APIKeys, key)
}

// RemoveAPIKey removes an API key from the valid keys list
func (m *AuthMiddleware) RemoveAPIKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newKeys := make([]string, 0, len(m.config.APIKeys))
	for _, k := range m.config.APIKeys {
		if k != key {
			newKeys = append(newKeys, k)
		}
	}
	m.config.APIKeys = newKeys
}

// IsEnabled returns whether authentication is enabled
func (m *AuthMiddleware) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Enabled
}
