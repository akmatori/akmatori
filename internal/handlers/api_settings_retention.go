package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleRetentionSettings handles GET/PUT /api/settings/retention
func (h *APIHandler) handleRetentionSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := database.GetOrCreateRetentionSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get retention settings")
			return
		}
		api.RespondJSON(w, http.StatusOK, settings)

	case http.MethodPut:
		var req api.UpdateRetentionSettingsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		settings, err := database.GetOrCreateRetentionSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get retention settings")
			return
		}

		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}
		if req.RetentionDays != nil {
			if *req.RetentionDays < 1 || *req.RetentionDays > 3650 {
				api.RespondError(w, http.StatusBadRequest, "retention_days must be between 1 and 3650")
				return
			}
			settings.RetentionDays = *req.RetentionDays
		}
		if req.CleanupIntervalHours != nil {
			if *req.CleanupIntervalHours < 1 || *req.CleanupIntervalHours > 8760 {
				api.RespondError(w, http.StatusBadRequest, "cleanup_interval_hours must be between 1 and 8760")
				return
			}
			settings.CleanupIntervalHours = *req.CleanupIntervalHours
		}

		if err := database.UpdateRetentionSettings(settings); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update retention settings")
			return
		}

		api.RespondJSON(w, http.StatusOK, settings)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
