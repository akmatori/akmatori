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

		if err := database.UpdateGeneralSettings(settings); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update general settings")
			return
		}

		api.RespondJSON(w, http.StatusOK, settings)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleGetAggregationSettings handles GET /api/settings/aggregation
func (h *APIHandler) handleGetAggregationSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	settings, err := database.GetOrCreateAggregationSettings(db)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get aggregation settings")
		return
	}

	api.RespondJSON(w, http.StatusOK, settings)
}

// handleUpdateAggregationSettings handles PUT /api/settings/aggregation
func (h *APIHandler) handleUpdateAggregationSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	var settings database.AggregationSettings
	if err := api.DecodeJSON(r, &settings); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := database.GetOrCreateAggregationSettings(db)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get aggregation settings")
		return
	}
	settings.ID = existing.ID

	if err := database.UpdateAggregationSettings(db, &settings); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to update aggregation settings")
		return
	}

	api.RespondJSON(w, http.StatusOK, settings)
}
