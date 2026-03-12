package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
)

// CreateRunbookRequest is the request body for creating a runbook
type CreateRunbookRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// UpdateRunbookRequest is the request body for updating a runbook
type UpdateRunbookRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// handleRunbooks handles GET /api/runbooks and POST /api/runbooks
func (h *APIHandler) handleRunbooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runbooks, err := h.runbookService.ListRunbooks()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list runbooks")
			return
		}
		api.RespondJSON(w, http.StatusOK, runbooks)

	case http.MethodPost:
		var req CreateRunbookRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		runbook, err := h.runbookService.CreateRunbook(req.Title, req.Content)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "file sync failed") {
				status = http.StatusInternalServerError
			}
			api.RespondError(w, status, err.Error())
			return
		}

		api.RespondJSON(w, http.StatusCreated, runbook)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleRunbookByID handles GET/PUT/DELETE /api/runbooks/{id}
func (h *APIHandler) handleRunbookByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/api/runbooks/"):]
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid runbook ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		runbook, err := h.runbookService.GetRunbook(uint(id))
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "Runbook not found")
			return
		}
		api.RespondJSON(w, http.StatusOK, runbook)

	case http.MethodPut:
		var req UpdateRunbookRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		runbook, err := h.runbookService.UpdateRunbook(uint(id), req.Title, req.Content)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "file sync failed") {
				status = http.StatusInternalServerError
			}
			api.RespondError(w, status, err.Error())
			return
		}

		api.RespondJSON(w, http.StatusOK, runbook)

	case http.MethodDelete:
		if err := h.runbookService.DeleteRunbook(uint(id)); err != nil {
			status := http.StatusNotFound
			if strings.Contains(err.Error(), "file sync failed") {
				status = http.StatusInternalServerError
			}
			api.RespondError(w, status, err.Error())
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
