package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

const defaultAlertMonitorWindowMinutes = 60

// applyGeneralSettingsDefaults fills nil alert config pointers with effective
// code defaults so the GET response never contains null. It modifies the struct
// in-place; callers must not persist the result back to the DB.
func applyGeneralSettingsDefaults(s *database.GeneralSettings) {
	if s.AlertCorrelationEnabled == nil {
		v := false
		s.AlertCorrelationEnabled = &v
	}
	if s.AlertMonitorWindowMinutes == nil {
		v := defaultAlertMonitorWindowMinutes
		s.AlertMonitorWindowMinutes = &v
	}
	if s.IncidentMergeEnabled == nil {
		v := false
		s.IncidentMergeEnabled = &v
	}
}

// handleGeneralSettings handles GET/PUT /api/settings/general
func (h *APIHandler) handleGeneralSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := database.GetOrCreateGeneralSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get general settings")
			return
		}
		// Hydrate nil alert config fields with effective defaults so the
		// frontend always receives non-null values and can display them
		// without null guards. The defaults are NOT persisted to the DB.
		applyGeneralSettingsDefaults(settings)
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
		if req.AlertMonitorWindowMinutes != nil {
			if *req.AlertMonitorWindowMinutes < 1 || *req.AlertMonitorWindowMinutes > 10080 {
				api.RespondError(w, http.StatusBadRequest, "alert_monitor_window_minutes must be between 1 and 10080")
				return
			}
			settings.AlertMonitorWindowMinutes = req.AlertMonitorWindowMinutes
		}
		if req.IncidentMergeEnabled != nil {
			settings.IncidentMergeEnabled = req.IncidentMergeEnabled
		}

		if err := database.UpdateGeneralSettings(settings); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update general settings")
			return
		}

		applyGeneralSettingsDefaults(settings)
		api.RespondJSON(w, http.StatusOK, settings)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
