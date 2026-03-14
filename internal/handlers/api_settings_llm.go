package handlers

import (
	"fmt"
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// ModelConfigs defines the available models and their valid reasoning effort options (legacy, kept for tests)
var ModelConfigs = map[string][]string{
	"gpt-5.4":            {"low", "medium", "high", "extra_high"},
	"gpt-5.2":            {"low", "medium", "high", "extra_high"},
	"gpt-5.2-codex":      {"low", "medium", "high", "extra_high"},
	"gpt-5.3-codex":      {"low", "medium", "high"},
	"gpt-5.1-codex-max":  {"low", "medium", "high", "extra_high"},
	"gpt-5.1-codex":      {"low", "medium", "high"},
	"gpt-5.1-codex-mini": {"medium", "high"},
	"gpt-5.1":            {"low", "medium", "high"},
}

// handleLLMSettings handles GET /api/settings/llm and PUT /api/settings/llm.
func (h *APIHandler) handleLLMSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		allSettings, err := database.GetAllLLMSettings()
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "Settings not found")
			return
		}

		providers := make(map[string]interface{})
		activeProvider := ""
		for _, s := range allSettings {
			providers[string(s.Provider)] = map[string]interface{}{
				"api_key":        maskToken(s.APIKey),
				"model":          s.Model,
				"thinking_level": s.ThinkingLevel,
				"base_url":       s.BaseURL,
				"is_configured":  s.APIKey != "",
			}
			if s.Active {
				activeProvider = string(s.Provider)
			}
		}

		active, _ := database.GetLLMSettings()
		response := map[string]interface{}{
			"active_provider": activeProvider,
			"providers":       providers,
			"id":              active.ID,
			"provider":        active.Provider,
			"api_key":         maskToken(active.APIKey),
			"model":           active.Model,
			"thinking_level":  active.ThinkingLevel,
			"base_url":        active.BaseURL,
			"is_configured":   active.APIKey != "",
			"created_at":      active.CreatedAt,
			"updated_at":      active.UpdatedAt,
		}
		api.RespondJSON(w, http.StatusOK, response)

	case http.MethodPut:
		var req api.UpdateLLMSettingsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Provider == nil || *req.Provider == "" {
			api.RespondError(w, http.StatusBadRequest, "provider is required")
			return
		}

		if !database.IsValidLLMProvider(*req.Provider) {
			api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid provider: %s. Valid options: openai, anthropic, google, openrouter, custom", *req.Provider))
			return
		}

		if req.BaseURL != nil && *req.BaseURL != "" && !isValidURL(*req.BaseURL) {
			api.RespondError(w, http.StatusBadRequest, "Invalid base_url: must be a valid HTTP or HTTPS URL")
			return
		}

		if req.ThinkingLevel != nil && !database.IsValidThinkingLevel(*req.ThinkingLevel) {
			api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid thinking_level: %s. Valid options: off, minimal, low, medium, high, xhigh", *req.ThinkingLevel))
			return
		}

		provider := database.LLMProvider(*req.Provider)

		settings, err := database.GetLLMSettingsByProvider(provider)
		if err != nil {
			api.RespondError(w, http.StatusNotFound, fmt.Sprintf("Provider settings not found: %s", *req.Provider))
			return
		}

		updates := make(map[string]interface{})
		if req.APIKey != nil {
			updates["api_key"] = *req.APIKey
			updates["enabled"] = *req.APIKey != ""
		}
		if req.Model != nil {
			updates["model"] = *req.Model
		}
		if req.ThinkingLevel != nil {
			updates["thinking_level"] = *req.ThinkingLevel
		}
		if req.BaseURL != nil {
			updates["base_url"] = *req.BaseURL
		}

		if len(updates) > 0 {
			if err := database.GetDB().Model(settings).Updates(updates).Error; err != nil {
				api.RespondError(w, http.StatusInternalServerError, "Failed to update settings")
				return
			}
		}

		if err := database.SetActiveLLMProvider(provider); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to set active provider")
			return
		}

		settings, _ = database.GetLLMSettingsByProvider(provider)
		response := map[string]interface{}{
			"id":             settings.ID,
			"provider":       settings.Provider,
			"api_key":        maskToken(settings.APIKey),
			"model":          settings.Model,
			"thinking_level": settings.ThinkingLevel,
			"base_url":       settings.BaseURL,
			"is_configured":  settings.APIKey != "",
			"created_at":     settings.CreatedAt,
			"updated_at":     settings.UpdatedAt,
		}
		api.RespondJSON(w, http.StatusOK, response)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
