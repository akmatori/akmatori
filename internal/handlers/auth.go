package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/akmatori/akmatori/internal/middleware"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	jwtAuth *middleware.JWTAuthMiddleware
}

// NewAuthHandler creates a new authentication handler
func NewAuthHandler(jwtAuth *middleware.JWTAuthMiddleware) *AuthHandler {
	return &AuthHandler{
		jwtAuth: jwtAuth,
	}
}

// LoginRequest represents the login request body
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents the login response
type LoginResponse struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	ExpiresIn int    `json:"expires_in"` // seconds
}

// SetupRoutes sets up authentication routes
func (h *AuthHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.handleLogin)
	mux.HandleFunc("/auth/verify", h.handleVerify)
}

// handleLogin handles POST /auth/login
func (h *AuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"}) // ignore: error response
		return
	}

	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Username and password are required"}) // ignore: error response
		return
	}

	// Validate credentials
	if !h.jwtAuth.ValidateCredentials(req.Username, req.Password) {
		log.Printf("AuthHandler: Failed login attempt for user '%s' from %s", req.Username, r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid username or password"}) // ignore: error response
		return
	}

	// Generate JWT token
	token, err := h.jwtAuth.GenerateToken(req.Username)
	if err != nil {
		log.Printf("AuthHandler: Failed to generate token for user '%s': %v", req.Username, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to generate token"}) // ignore: error response
		return
	}

	log.Printf("AuthHandler: User '%s' logged in successfully from %s", req.Username, r.RemoteAddr)

	response := LoginResponse{
		Token:     token,
		Username:  req.Username,
		ExpiresIn: 24 * 60 * 60, // 24 hours in seconds
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response) // ignore: final response, nothing more to do
}

// handleVerify handles GET /auth/verify - verifies if the current token is valid
func (h *AuthHandler) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user from context (set by JWT middleware)
	user := middleware.GetUserFromContext(r.Context())
	if user == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Not authenticated"}) // ignore: error response
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":    true,
		"username": user,
	}) // ignore: final response, nothing more to do
}
