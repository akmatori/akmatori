package handlers

import (
	"log"
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
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
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req LoginRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Username == "" || req.Password == "" {
		api.RespondError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	if !h.jwtAuth.ValidateCredentials(req.Username, req.Password) {
		log.Printf("AuthHandler: Failed login attempt for user '%s' from %s", req.Username, r.RemoteAddr)
		api.RespondError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	token, err := h.jwtAuth.GenerateToken(req.Username)
	if err != nil {
		log.Printf("AuthHandler: Failed to generate token for user '%s': %v", req.Username, err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	log.Printf("AuthHandler: User '%s' logged in successfully from %s", req.Username, r.RemoteAddr)

	api.RespondJSON(w, http.StatusOK, LoginResponse{
		Token:     token,
		Username:  req.Username,
		ExpiresIn: 24 * 60 * 60,
	})
}

// handleVerify handles GET /auth/verify - verifies if the current token is valid
func (h *AuthHandler) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := middleware.GetUserFromContext(r.Context())
	if user == "" {
		api.RespondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	api.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"valid":    true,
		"username": user,
	})
}
