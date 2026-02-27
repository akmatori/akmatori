package middleware

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// UserClaims represents the JWT claims for a user
type UserClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// JWTAuthConfig holds JWT authentication configuration
type JWTAuthConfig struct {
	// Enabled determines if JWT authentication is enforced
	Enabled bool

	// AdminUsername is the admin username from env
	AdminUsername string

	// AdminPasswordHash is the bcrypt hash of the admin password
	AdminPasswordHash string

	// JWTSecret is the secret key for signing JWT tokens
	JWTSecret string

	// JWTExpiryHours is the token expiry in hours
	JWTExpiryHours int

	// SkipPaths are paths that don't require authentication
	SkipPaths []string
}

// JWTAuthMiddleware provides JWT-based authentication
type JWTAuthMiddleware struct {
	config  *JWTAuthConfig
	mu      sync.RWMutex
	skipMap map[string]bool
}

// ContextKey is a type for context keys
type ContextKey string

const (
	// UserContextKey is the context key for the authenticated user
	UserContextKey ContextKey = "user"
)

// NewJWTAuthMiddleware creates a new JWT authentication middleware
func NewJWTAuthMiddleware(config *JWTAuthConfig) *JWTAuthMiddleware {
	m := &JWTAuthMiddleware{
		config:  config,
		skipMap: make(map[string]bool),
	}

	// Build skip paths map for O(1) lookup
	for _, path := range config.SkipPaths {
		m.skipMap[path] = true
	}

	return m
}

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword checks if the provided password matches the hash
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateToken generates a JWT token for a user
func (m *JWTAuthMiddleware) GenerateToken(username string) (string, error) {
	m.mu.RLock()
	secret := m.config.JWTSecret
	expiryHours := m.config.JWTExpiryHours
	m.mu.RUnlock()

	claims := UserClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expiryHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "akmatori",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateToken validates a JWT token and returns the claims
func (m *JWTAuthMiddleware) ValidateToken(tokenString string) (*UserClaims, error) {
	m.mu.RLock()
	secret := m.config.JWTSecret
	m.mu.RUnlock()

	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*UserClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
}

// ValidateCredentials validates username and password
func (m *JWTAuthMiddleware) ValidateCredentials(username, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Use constant-time comparison for username
	if subtle.ConstantTimeCompare([]byte(username), []byte(m.config.AdminUsername)) != 1 {
		return false
	}

	return CheckPassword(password, m.config.AdminPasswordHash)
}

// Wrap wraps an http.Handler with JWT authentication
func (m *JWTAuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if auth is enabled
		m.mu.RLock()
		enabled := m.config.Enabled
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

		// Extract token from request
		tokenString := m.extractToken(r)
		if tokenString == "" {
			m.unauthorized(w, "Missing authentication token")
			return
		}

		// Validate token
		claims, err := m.ValidateToken(tokenString)
		if err != nil {
			log.Printf("JWTAuthMiddleware: Invalid token from %s: %v", r.RemoteAddr, err)
			m.unauthorized(w, "Invalid or expired token")
			return
		}

		// Add user to context
		ctx := context.WithValue(r.Context(), UserContextKey, claims.Username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WrapFunc wraps an http.HandlerFunc with JWT authentication
func (m *JWTAuthMiddleware) WrapFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.Wrap(http.HandlerFunc(next)).ServeHTTP(w, r)
	}
}

// shouldSkipAuth checks if the path should skip authentication
func (m *JWTAuthMiddleware) shouldSkipAuth(path string) bool {
	// Check exact match
	if m.skipMap[path] {
		return true
	}

	// Check prefix matches (for paths like /health, /webhook/*, /auth/*)
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

// extractToken extracts the JWT token from the request
func (m *JWTAuthMiddleware) extractToken(r *http.Request) string {
	// Try Authorization header (Bearer token)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	return ""
}

// unauthorized sends an unauthorized response
func (m *JWTAuthMiddleware) unauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer realm=\"API\"")
	api.RespondError(w, http.StatusUnauthorized, message)
}

// SetEnabled enables or disables authentication
func (m *JWTAuthMiddleware) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Enabled = enabled
}

// IsEnabled returns whether authentication is enabled
func (m *JWTAuthMiddleware) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Enabled
}

// GetUserFromContext returns the username from the request context
func GetUserFromContext(ctx context.Context) string {
	if user, ok := ctx.Value(UserContextKey).(string); ok {
		return user
	}
	return ""
}
