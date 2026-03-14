package handlers

import (
	"log/slog"
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleSlackSettings handles GET /api/settings/slack and PUT /api/settings/slack
func (h *APIHandler) handleSlackSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var settings database.SlackSettings
		if err := db.First(&settings).Error; err != nil {
			api.RespondError(w, http.StatusNotFound, "Settings not found")
			return
		}
		response := map[string]interface{}{
			"id":             settings.ID,
			"bot_token":      maskToken(settings.BotToken),
			"signing_secret": maskToken(settings.SigningSecret),
			"app_token":      maskToken(settings.AppToken),
			"alerts_channel": settings.AlertsChannel,
			"enabled":        settings.Enabled,
			"is_configured":  settings.IsConfigured(),
			"created_at":     settings.CreatedAt,
			"updated_at":     settings.UpdatedAt,
		}
		api.RespondJSON(w, http.StatusOK, response)

	case http.MethodPut:
		var req api.UpdateSlackSettingsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		var settings database.SlackSettings
		if err := db.First(&settings).Error; err != nil {
			api.RespondError(w, http.StatusNotFound, "Settings not found")
			return
		}

		updates := make(map[string]interface{})
		if req.BotToken != nil {
			updates["bot_token"] = *req.BotToken
		}
		if req.SigningSecret != nil {
			updates["signing_secret"] = *req.SigningSecret
		}
		if req.AppToken != nil {
			updates["app_token"] = *req.AppToken
		}
		if req.AlertsChannel != nil {
			updates["alerts_channel"] = *req.AlertsChannel
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		if err := db.Model(&settings).Updates(updates).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update settings")
			return
		}

		if h.slackManager != nil {
			h.slackManager.TriggerReload()
			slog.Info("Slack settings updated, triggering hot-reload")
		}

		db.First(&settings)
		response := map[string]interface{}{
			"id":             settings.ID,
			"bot_token":      maskToken(settings.BotToken),
			"signing_secret": maskToken(settings.SigningSecret),
			"app_token":      maskToken(settings.AppToken),
			"alerts_channel": settings.AlertsChannel,
			"enabled":        settings.Enabled,
			"is_configured":  settings.IsConfigured(),
			"created_at":     settings.CreatedAt,
			"updated_at":     settings.UpdatedAt,
		}
		api.RespondJSON(w, http.StatusOK, response)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// maskToken masks a token for display, showing only last 4 characters
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}
