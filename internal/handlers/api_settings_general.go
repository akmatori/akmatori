package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// applyGeneralSettingsDefaults fills nil alert config pointers with effective
// code defaults so the GET response never contains null. It modifies the struct
// in-place; callers must not persist the result back to the DB.
func applyGeneralSettingsDefaults(s *database.GeneralSettings) {
	if s.AlertCorrelationEnabled == nil {
		v := false
		s.AlertCorrelationEnabled = &v
	}
	if s.AlertCorrelationWindowMinutes == nil {
		v := 30
		s.AlertCorrelationWindowMinutes = &v
	}
	if s.AlertCorrelationThreshold == nil {
		v := 0.7
		s.AlertCorrelationThreshold = &v
	}
	if s.AlertCorrelationMaxCandidates == nil {
		v := 20
		s.AlertCorrelationMaxCandidates = &v
	}
	if s.AlertCorrelationLongWindowDays == nil {
		v := 7
		s.AlertCorrelationLongWindowDays = &v
	}
	if s.AlertCorrelationFingerprintWindowMinutes == nil {
		v := 1440
		s.AlertCorrelationFingerprintWindowMinutes = &v
	}
	if s.AlertSuppressionEnabled == nil {
		v := false
		s.AlertSuppressionEnabled = &v
	}
	if s.AlertSuppressionThreshold == nil {
		v := 0.7
		s.AlertSuppressionThreshold = &v
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
		if req.AlertCorrelationLongWindowDays != nil {
			if *req.AlertCorrelationLongWindowDays < 1 || *req.AlertCorrelationLongWindowDays > 90 {
				api.RespondError(w, http.StatusBadRequest, "alert_correlation_long_window_days must be between 1 and 90")
				return
			}
			settings.AlertCorrelationLongWindowDays = req.AlertCorrelationLongWindowDays
		}
		if req.AlertCorrelationFingerprintWindowMinutes != nil {
			if *req.AlertCorrelationFingerprintWindowMinutes < 1 || *req.AlertCorrelationFingerprintWindowMinutes > 10080 {
				api.RespondError(w, http.StatusBadRequest, "alert_correlation_fingerprint_window_minutes must be between 1 and 10080")
				return
			}
			settings.AlertCorrelationFingerprintWindowMinutes = req.AlertCorrelationFingerprintWindowMinutes
		}
		if req.AlertSuppressionEnabled != nil {
			settings.AlertSuppressionEnabled = req.AlertSuppressionEnabled
		}
		if req.AlertSuppressionThreshold != nil {
			if *req.AlertSuppressionThreshold <= 0 || *req.AlertSuppressionThreshold > 1 {
				api.RespondError(w, http.StatusBadRequest, "alert_suppression_threshold must be greater than 0 and at most 1")
				return
			}
			settings.AlertSuppressionThreshold = req.AlertSuppressionThreshold
		}

		// Cross-field: fingerprint window must be shorter than the long window, otherwise
		// the dedup logic in fetchCandidates absorbs all long-window candidates into query 2
		// and IsLongWindowMatch is never set, silently killing the cheap recurrence path.
		{
			fpMins := 1440
			if settings.AlertCorrelationFingerprintWindowMinutes != nil {
				fpMins = *settings.AlertCorrelationFingerprintWindowMinutes
			}
			lwDays := 7
			if settings.AlertCorrelationLongWindowDays != nil {
				lwDays = *settings.AlertCorrelationLongWindowDays
			}
			if fpMins >= lwDays*1440 {
				api.RespondError(w, http.StatusBadRequest,
					"alert_correlation_fingerprint_window_minutes must be strictly less than alert_correlation_long_window_days × 1440")
				return
			}
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
