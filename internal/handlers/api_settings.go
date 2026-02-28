package handlers

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
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
			log.Printf("Slack settings updated, triggering hot-reload")
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

// maskProxyURL masks the password in a proxy URL if present
func maskProxyURL(proxyURL string) string {
	if proxyURL == "" {
		return ""
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "****")
		}
	}
	return parsed.String()
}

// isValidURL validates that a string is a valid HTTP or HTTPS URL
func isValidURL(rawURL string) bool {
	if rawURL == "" {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// ModelConfigs defines the available models and their valid reasoning effort options (legacy, kept for tests)
var ModelConfigs = map[string][]string{
	"gpt-5.2":            {"low", "medium", "high", "extra_high"},
	"gpt-5.2-codex":      {"low", "medium", "high", "extra_high"},
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

// handleDeviceAuthStart handles POST /api/settings/openai/device-auth/start.
// Part of the device authentication flow (not yet wired to routes).
//
//nolint:unused // Device auth feature - routes not yet registered
func (h *APIHandler) handleDeviceAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if h.codexWSHandler == nil || !h.codexWSHandler.IsWorkerConnected() {
		api.RespondError(w, http.StatusServiceUnavailable, "Codex worker not connected")
		return
	}

	var openaiSettings *OpenAISettings
	if dbSettings, err := database.GetOpenAISettings(); err == nil && dbSettings != nil {
		openaiSettings = &OpenAISettings{
			BaseURL:  dbSettings.BaseURL,
			ProxyURL: dbSettings.ProxyURL,
			NoProxy:  dbSettings.NoProxy,
		}
	}

	h.deviceAuthService.ClearFlow()

	err := h.codexWSHandler.StartDeviceAuth(func(result *DeviceAuthResult) {
		h.deviceAuthService.HandleDeviceAuthResult(&services.DeviceAuthResult{
			DeviceCode:      result.DeviceCode,
			UserCode:        result.UserCode,
			VerificationURL: result.VerificationURL,
			ExpiresIn:       result.ExpiresIn,
			Status:          result.Status,
			Email:           result.Email,
			AccessToken:     result.AccessToken,
			RefreshToken:    result.RefreshToken,
			IDToken:         result.IDToken,
			ExpiresAt:       result.ExpiresAt,
			Error:           result.Error,
		})
	}, openaiSettings)
	if err != nil {
		log.Printf("Failed to start device auth: %v", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to start device authentication")
		return
	}

	response, err := h.deviceAuthService.WaitForInitialResponse(30 * time.Second)
	if err != nil {
		log.Printf("Failed to get device auth codes: %v", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to start device authentication")
		return
	}

	api.RespondJSON(w, http.StatusOK, response)
}

// handleDeviceAuthStatus handles GET /api/settings/openai/device-auth/status.
// Part of the device authentication flow (not yet wired to routes).
//
//nolint:unused // Device auth feature - routes not yet registered
func (h *APIHandler) handleDeviceAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	deviceCode := r.URL.Query().Get("device_code")
	if deviceCode == "" {
		api.RespondError(w, http.StatusBadRequest, "device_code query parameter is required")
		return
	}

	status, err := h.deviceAuthService.GetDeviceAuthStatus(deviceCode)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get device auth status")
		return
	}

	if status.Status == services.DeviceAuthStatusComplete {
		tokens, err := h.deviceAuthService.GetAuthTokens()
		if err == nil && tokens != nil {
			if err := h.deviceAuthService.SaveTokensToDatabase(tokens); err != nil {
				log.Printf("Failed to save tokens to database: %v", err)
				status.Error = "Authentication succeeded but failed to save tokens"
				status.Status = services.DeviceAuthStatusFailed
			} else {
				status.Email = tokens.Email
				log.Printf("ChatGPT authentication completed for: %s", tokens.Email)
			}
		}
	}

	api.RespondJSON(w, http.StatusOK, status)
}

// handleDeviceAuthCancel handles POST /api/settings/openai/device-auth/cancel.
// Part of the device authentication flow (not yet wired to routes).
//
//nolint:unused // Device auth feature - routes not yet registered
func (h *APIHandler) handleDeviceAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if h.codexWSHandler != nil && h.codexWSHandler.IsWorkerConnected() {
		if err := h.codexWSHandler.CancelDeviceAuth(); err != nil {
			log.Printf("Failed to cancel device auth via WebSocket: %v", err)
		}
	}

	h.deviceAuthService.CancelDeviceAuth()

	api.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Device authentication cancelled",
	})
}

// handleChatGPTDisconnect handles POST /api/settings/openai/chatgpt/disconnect.
// Part of the ChatGPT integration (not yet wired to routes).
//
//nolint:unused // ChatGPT disconnect feature - routes not yet registered
func (h *APIHandler) handleChatGPTDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	settings, err := database.GetOpenAISettings()
	if err != nil {
		api.RespondError(w, http.StatusNotFound, "Settings not found")
		return
	}

	if err := database.ClearOpenAIChatGPTTokens(settings.ID); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to disconnect")
		return
	}

	log.Printf("ChatGPT subscription disconnected")

	api.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "ChatGPT subscription disconnected",
	})
}

// handleProxySettings handles GET /api/settings/proxy and PUT /api/settings/proxy
func (h *APIHandler) handleProxySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetProxySettings(w, r)
	case http.MethodPut:
		h.UpdateProxySettings(w, r)
	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// GetProxySettings returns the current proxy configuration
func (h *APIHandler) GetProxySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get proxy settings")
		return
	}

	maskedURL := maskProxyURL(settings.ProxyURL)

	response := map[string]interface{}{
		"proxy_url": maskedURL,
		"no_proxy":  settings.NoProxy,
		"services": map[string]interface{}{
			"openai": map[string]interface{}{
				"enabled":   settings.OpenAIEnabled,
				"supported": true,
			},
			"slack": map[string]interface{}{
				"enabled":   settings.SlackEnabled,
				"supported": true,
			},
			"zabbix": map[string]interface{}{
				"enabled":   settings.ZabbixEnabled,
				"supported": true,
			},
			"ssh": map[string]interface{}{
				"enabled":   false,
				"supported": false,
			},
		},
	}

	api.RespondJSON(w, http.StatusOK, response)
}

// UpdateProxySettings updates proxy configuration
func (h *APIHandler) UpdateProxySettings(w http.ResponseWriter, r *http.Request) {
	var input api.UpdateProxySettingsRequest
	if err := api.DecodeJSON(r, &input); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if input.ProxyURL != "" && !isValidURL(input.ProxyURL) {
		api.RespondError(w, http.StatusBadRequest, "Invalid proxy URL format")
		return
	}

	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get proxy settings")
		return
	}

	settings.ProxyURL = input.ProxyURL
	settings.NoProxy = input.NoProxy
	settings.OpenAIEnabled = input.Services.OpenAI.Enabled
	settings.SlackEnabled = input.Services.Slack.Enabled
	settings.ZabbixEnabled = input.Services.Zabbix.Enabled

	if err := database.UpdateProxySettings(settings); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to update proxy settings")
		return
	}

	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		if err := h.agentWSHandler.BroadcastProxyConfig(settings); err != nil {
			log.Printf("Warning: failed to broadcast proxy config to agent worker: %v", err)
		}
	}

	h.GetProxySettings(w, r)
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
