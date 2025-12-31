package handlers

import (
	"encoding/json"
	"log"
	"net/http"
)

// HTTPHandler handles HTTP endpoints
type HTTPHandler struct {
	alertHandler *AlertHandler
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(alertHandler *AlertHandler) *HTTPHandler {
	return &HTTPHandler{
		alertHandler: alertHandler,
	}
}

// SetupRoutes configures all HTTP routes
func (h *HTTPHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHealth)
	// Alert webhooks: /webhook/alert/{instance_uuid}
	if h.alertHandler != nil {
		mux.HandleFunc("/webhook/alert/", h.alertHandler.HandleWebhook)
	}
}

// handleHealth returns a simple health check response
func (h *HTTPHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]string{
		"status":  "ok",
		"version": "1.0.0",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding health response: %v", err)
	}
}
