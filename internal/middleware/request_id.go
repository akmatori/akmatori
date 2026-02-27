package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
)

const (
	// RequestIDHeader is the HTTP header used for request IDs.
	RequestIDHeader = "X-Request-ID"
)

// requestIDContextKey is the context key for the request ID.
type requestIDContextKey struct{}

// RequestIDMiddleware adds an X-Request-ID header to every response.
// If the client provides one, it is reused; otherwise a new UUID is generated.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = generateUUID()
		}

		w.Header().Set(RequestIDHeader, id)

		ctx := context.WithValue(r.Context(), requestIDContextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID returns the request ID from the context, or an empty string.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDContextKey{}).(string); ok {
		return id
	}
	return ""
}

// generateUUID generates a random UUID v4.
func generateUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fallback: return a zero UUID on error (extremely unlikely).
		return "00000000-0000-0000-0000-000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
