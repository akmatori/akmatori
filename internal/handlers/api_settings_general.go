package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

const defaultAlertMonitorWindowMinutes = 60

// Bounds for incident_auto_close_minutes. The floor is one hour: anything
// shorter would race normal investigation turnaround and close incidents under
// an operator. The ceiling is 90 days, past which the setting is effectively
// "never" and the operator should turn the gate off instead.
const (
	minIncidentAutoCloseMinutes = 60
	maxIncidentAutoCloseMinutes = 90 * 24 * 60
)

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
	// Note the inverted default: the stale-close gate is on unless explicitly
	// disabled. See GeneralSettings.IncidentAutoCloseEnabled.
	if s.IncidentAutoCloseEnabled == nil {
		v := true
		s.IncidentAutoCloseEnabled = &v
	}
	if s.IncidentAutoCloseMinutes == nil {
		v := database.DefaultIncidentAutoCloseMinutes
		s.IncidentAutoCloseMinutes = &v
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
		if req.IncidentAutoCloseEnabled != nil {
			settings.IncidentAutoCloseEnabled = req.IncidentAutoCloseEnabled
		}
		if req.IncidentAutoCloseMinutes != nil {
			if *req.IncidentAutoCloseMinutes < minIncidentAutoCloseMinutes ||
				*req.IncidentAutoCloseMinutes > maxIncidentAutoCloseMinutes {
				api.RespondError(w, http.StatusBadRequest,
					"incident_auto_close_minutes must be between 60 and 129600")
				return
			}
			settings.IncidentAutoCloseMinutes = req.IncidentAutoCloseMinutes
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
