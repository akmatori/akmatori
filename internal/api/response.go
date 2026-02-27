package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// ErrorResponse is the standard error envelope returned by all endpoints.
type ErrorResponse struct {
	Error   string            `json:"error"`
	Code    string            `json:"code,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// RespondJSON writes data as a JSON response with the given status code.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Printf("Failed to encode JSON response: %v", err)
		}
	}
}

// RespondError writes a standard error response.
func RespondError(w http.ResponseWriter, status int, message string) {
	RespondJSON(w, status, ErrorResponse{Error: message})
}

// RespondErrorWithCode writes an error response with a machine-readable code.
func RespondErrorWithCode(w http.ResponseWriter, status int, code, message string) {
	RespondJSON(w, status, ErrorResponse{Error: message, Code: code})
}

// RespondValidationError writes field-level validation errors as a 422 response.
func RespondValidationError(w http.ResponseWriter, fieldErrors map[string]string) {
	RespondJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
		Error:   "Validation failed",
		Code:    "validation_error",
		Details: fieldErrors,
	})
}

// RespondNoContent writes a 204 No Content response with no body.
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
