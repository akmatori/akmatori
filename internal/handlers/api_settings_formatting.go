package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

const (
	formattingMaxTokensMin     = 1
	formattingMaxTokensMax     = 8000
	formattingTemperatureMin   = 0.0
	formattingTemperatureMax   = 2.0
	formattingSystemPromptMax  = 8 * 1024
)

// handleFormattingSettings handles GET/PUT /api/settings/formatting
func (h *APIHandler) handleFormattingSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := database.GetOrCreateFormattingSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get formatting settings")
			return
		}
		api.RespondJSON(w, http.StatusOK, settings)

	case http.MethodPut:
		var req api.UpdateFormattingSettingsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		settings, err := database.GetOrCreateFormattingSettings()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get formatting settings")
			return
		}

		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}
		if req.SystemPrompt != nil {
			if len(*req.SystemPrompt) > formattingSystemPromptMax {
				api.RespondError(w, http.StatusBadRequest, "system_prompt must be 8192 bytes or fewer")
				return
			}
			settings.SystemPrompt = *req.SystemPrompt
		}
		if req.MaxTokens != nil {
			if *req.MaxTokens < formattingMaxTokensMin || *req.MaxTokens > formattingMaxTokensMax {
				api.RespondError(w, http.StatusBadRequest, "max_tokens must be between 1 and 8000")
				return
			}
			settings.MaxTokens = *req.MaxTokens
		}
		if req.Temperature != nil {
			if *req.Temperature < formattingTemperatureMin || *req.Temperature > formattingTemperatureMax {
				api.RespondError(w, http.StatusBadRequest, "temperature must be between 0 and 2")
				return
			}
			settings.Temperature = *req.Temperature
		}

		if err := database.UpdateFormattingSettings(settings); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update formatting settings")
			return
		}

		api.RespondJSON(w, http.StatusOK, settings)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
