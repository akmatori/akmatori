package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleGeneralSettings handles GET/PUT /api/settings/general
func (h *APIHandler) handleGeneralSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := database.GetOrCreateGeneralSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get general settings")
			return
		}
		api.RespondJSON(w, http.StatusOK, settings)

	case http.MethodPut:
		var req api.UpdateGeneralSettingsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		settings, err := database.GetOrCreateGeneralSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get general settings")
			return
		}

		if req.BaseURL != nil {
			if *req.BaseURL != "" && !isValidURL(*req.BaseURL) {
				api.RespondError(w, http.StatusBadRequest, "Invalid base_url: must be a valid HTTP or HTTPS URL")
				return
			}
			settings.BaseURL = *req.BaseURL
		}
		if req.AlertCorrelationEnabled != nil {
			settings.AlertCorrelationEnabled = req.AlertCorrelationEnabled
		}
		if req.AlertCorrelationWindowMinutes != nil {
			if *req.AlertCorrelationWindowMinutes < 1 || *req.AlertCorrelationWindowMinutes > 1440 {
				api.RespondError(w, http.StatusBadRequest, "alert_correlation_window_minutes must be between 1 and 1440")
				return
			}
			settings.AlertCorrelationWindowMinutes = req.AlertCorrelationWindowMinutes
		}
		if req.AlertCorrelationThreshold != nil {
			if *req.AlertCorrelationThreshold <= 0 || *req.AlertCorrelationThreshold > 1 {
				api.RespondError(w, http.StatusBadRequest, "alert_correlation_threshold must be greater than 0 and at most 1")
				return
			}
			settings.AlertCorrelationThreshold = req.AlertCorrelationThreshold
		}
		if req.AlertCorrelationMaxCandidates != nil {
			if *req.AlertCorrelationMaxCandidates < 1 || *req.AlertCorrelationMaxCandidates > 100 {
				api.RespondError(w, http.StatusBadRequest, "alert_correlation_max_candidates must be between 1 and 100")
				return
			}
			settings.AlertCorrelationMaxCandidates = req.AlertCorrelationMaxCandidates
		}

		if err := database.UpdateGeneralSettings(settings); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update general settings")
			return
		}

		api.RespondJSON(w, http.StatusOK, settings)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
