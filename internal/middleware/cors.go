package middleware

import (
	"net/http"
)

// CORSMiddleware handles Cross-Origin Resource Sharing headers
type CORSMiddleware struct {
	allowedOrigins []string
	allowAll       bool
}

// NewCORSMiddleware creates a new CORS middleware
// If no origins are specified, all origins are allowed
func NewCORSMiddleware(allowedOrigins ...string) *CORSMiddleware {
	return &CORSMiddleware{
		allowedOrigins: allowedOrigins,
		allowAll:       len(allowedOrigins) == 0,
	}
}

// Wrap wraps an http.Handler with CORS headers
func (c *CORSMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Set CORS headers for all cross-origin requests
		if origin != "" && (c.allowAll || c.isAllowedOrigin(origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		// Handle preflight OPTIONS requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (c *CORSMiddleware) isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range c.allowedOrigins {
		if allowed == origin || allowed == "*" {
			return true
		}
	}
	return false
}
