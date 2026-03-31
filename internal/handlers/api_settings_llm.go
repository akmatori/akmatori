package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleLLMSettings handles GET /api/settings/llm and POST /api/settings/llm.
func (h *APIHandler) handleLLMSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listLLMConfigs(w, r)
	case http.MethodPost:
		h.createLLMConfig(w, r)
	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleLLMSettingsByID handles GET/PUT/DELETE /api/settings/llm/{id} and PUT /api/settings/llm/{id}/activate.
func (h *APIHandler) handleLLMSettingsByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/settings/llm/"):]
	parts := strings.Split(path, "/")

	id, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid config ID")
		return
	}

	// Handle /api/settings/llm/{id}/activate
	if len(parts) >= 2 && parts[1] == "activate" {
		if r.Method != http.MethodPut {
			api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		h.activateLLMConfig(w, r, uint(id))
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getLLMConfig(w, r, uint(id))
	case http.MethodPut:
		h.updateLLMConfig(w, r, uint(id))
	case http.MethodDelete:
		h.deleteLLMConfig(w, r, uint(id))
	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listLLMConfigs returns all LLM configurations with the active config ID.
func (h *APIHandler) listLLMConfigs(w http.ResponseWriter, _ *http.Request) {
	allSettings, err := database.GetAllLLMSettings()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to retrieve LLM settings")
		return
	}

	configs := make([]map[string]interface{}, 0, len(allSettings))
	for _, s := range allSettings {
		configs = append(configs, llmConfigResponse(&s))
	}
	// Derive active_id from the same snapshot used for configs to avoid a
	// race where a concurrent create+activate between two queries could
	// return an active_id not present in configs.  The fallback priority
	// mirrors GetLLMSettings(): active → enabled → first row (lowest PK).
	var activeID uint
	if len(allSettings) > 0 {
		// Mirror GetLLMSettings() resolution: active → enabled → lowest PK.
		// GetAllLLMSettings() orders by (provider, name), but GetLLMSettings()
		// uses GORM First() which orders by primary key.  We must pick the
		// lowest-PK row in each category to match incident dispatch.
		var firstActive, firstEnabled *database.LLMSettings
		lowestPK := &allSettings[0]
		for i := range allSettings {
			s := &allSettings[i]
			if s.ID < lowestPK.ID {
				lowestPK = s
			}
			if s.Active && (firstActive == nil || s.ID < firstActive.ID) {
				firstActive = s
			}
			if s.Enabled && (firstEnabled == nil || s.ID < firstEnabled.ID) {
				firstEnabled = s
			}
		}
		pick := lowestPK
		if firstEnabled != nil {
			pick = firstEnabled
		}
		if firstActive != nil {
			pick = firstActive
		}
		activeID = pick.ID
	}

	response := map[string]interface{}{
		"configs":   configs,
		"active_id": activeID,
	}
	api.RespondJSON(w, http.StatusOK, response)
}

// createLLMConfig creates a new LLM configuration.
func (h *APIHandler) createLLMConfig(w http.ResponseWriter, r *http.Request) {
	var req api.CreateLLMSettingsRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Provider == "" {
		api.RespondError(w, http.StatusBadRequest, "provider is required")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		api.RespondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if utf8.RuneCountInString(req.Name) > 100 {
		api.RespondError(w, http.StatusBadRequest, "name must be 100 characters or less")
		return
	}
	if !database.IsValidLLMProvider(req.Provider) {
		api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid provider: %s. Valid options: openai, anthropic, google, openrouter, custom", req.Provider))
		return
	}
	if req.BaseURL != "" && !isValidURL(req.BaseURL) {
		api.RespondError(w, http.StatusBadRequest, "Invalid base_url: must be a valid HTTP or HTTPS URL")
		return
	}
	if req.ThinkingLevel != "" && !database.IsValidThinkingLevel(req.ThinkingLevel) {
		api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid thinking_level: %s. Valid options: off, minimal, low, medium, high, xhigh", req.ThinkingLevel))
		return
	}

	thinkingLevel := database.ThinkingLevelMedium
	if req.ThinkingLevel != "" {
		thinkingLevel = database.ThinkingLevel(req.ThinkingLevel)
	}

	settings := &database.LLMSettings{
		Name:          req.Name,
		Provider:      database.LLMProvider(req.Provider),
		APIKey:        req.APIKey,
		Model:         req.Model,
		ThinkingLevel: thinkingLevel,
		BaseURL:       req.BaseURL,
		Enabled:       req.APIKey != "",
	}

	if err := database.CreateLLMSettings(settings); err != nil {
		if containsString(err.Error(), "UNIQUE") || containsString(err.Error(), "duplicate key") || containsString(err.Error(), "unique") {
			api.RespondError(w, http.StatusConflict, fmt.Sprintf("A configuration with name %q already exists", req.Name))
			return
		}
		api.RespondError(w, http.StatusInternalServerError, "Failed to create LLM configuration")
		return
	}

	api.RespondJSON(w, http.StatusCreated, llmConfigResponse(settings))
}

// getLLMConfig returns a single LLM configuration by ID.
func (h *APIHandler) getLLMConfig(w http.ResponseWriter, _ *http.Request, id uint) {
	settings, err := database.GetLLMSettingsByID(id)
	if err != nil {
		api.RespondError(w, http.StatusNotFound, "LLM configuration not found")
		return
	}
	api.RespondJSON(w, http.StatusOK, llmConfigResponse(settings))
}

// updateLLMConfig updates an existing LLM configuration by ID.
func (h *APIHandler) updateLLMConfig(w http.ResponseWriter, r *http.Request, id uint) {
	// Quick existence check (non-authoritative, just for early 404)
	if _, err := database.GetLLMSettingsByID(id); err != nil {
		api.RespondError(w, http.StatusNotFound, "LLM configuration not found")
		return
	}

	var req api.UpdateLLMSettingsRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if *req.Name == "" {
			api.RespondError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		if utf8.RuneCountInString(*req.Name) > 100 {
			api.RespondError(w, http.StatusBadRequest, "name must be 100 characters or less")
			return
		}
	}
	if req.BaseURL != nil && *req.BaseURL != "" && !isValidURL(*req.BaseURL) {
		api.RespondError(w, http.StatusBadRequest, "Invalid base_url: must be a valid HTTP or HTTPS URL")
		return
	}
	if req.ThinkingLevel != nil && !database.IsValidThinkingLevel(*req.ThinkingLevel) {
		api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid thinking_level: %s. Valid options: off, minimal, low, medium, high, xhigh", *req.ThinkingLevel))
		return
	}

	updates := make(map[string]interface{})
	if req.Name != nil {
		updates["name"] = *req.Name
	}
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

	if len(updates) == 0 {
		settings, err := database.GetLLMSettingsByID(id)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to retrieve configuration")
			return
		}
		api.RespondJSON(w, http.StatusOK, llmConfigResponse(settings))
		return
	}

	// Atomic update with row lock — validates active+key invariant under lock
	settings, err := database.UpdateLLMSettings(id, updates)
	if err != nil {
		errMsg := err.Error()
		if containsString(errMsg, "not found") || containsString(errMsg, "record not found") {
			api.RespondError(w, http.StatusNotFound, "LLM configuration not found")
		} else if containsString(errMsg, "cannot clear") || containsString(errMsg, "active") {
			api.RespondError(w, http.StatusBadRequest, "Cannot clear the API key on the active configuration")
		} else if containsString(errMsg, "UNIQUE") || containsString(errMsg, "duplicate key") || containsString(errMsg, "unique") {
			msg := "A configuration with that name already exists"
			if req.Name != nil {
				msg = fmt.Sprintf("A configuration with name %q already exists", *req.Name)
			}
			api.RespondError(w, http.StatusConflict, msg)
		} else {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update LLM configuration")
		}
		return
	}
	api.RespondJSON(w, http.StatusOK, llmConfigResponse(settings))
}

// deleteLLMConfig deletes an LLM configuration by ID.
func (h *APIHandler) deleteLLMConfig(w http.ResponseWriter, _ *http.Request, id uint) {
	if err := database.DeleteLLMSettings(id); err != nil {
		if containsString(err.Error(), "not found") {
			api.RespondError(w, http.StatusNotFound, "LLM configuration not found")
		} else if containsString(err.Error(), "active") || containsString(err.Error(), "last") {
			api.RespondError(w, http.StatusBadRequest, err.Error())
		} else {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete LLM configuration")
		}
		return
	}

	api.RespondJSON(w, http.StatusOK, map[string]string{"message": "Configuration deleted"})
}

// activateLLMConfig sets an LLM configuration as the globally active one.
func (h *APIHandler) activateLLMConfig(w http.ResponseWriter, _ *http.Request, id uint) {
	// All validation (existence, API key) happens inside the locked transaction
	if err := database.SetActiveLLMConfig(id); err != nil {
		errMsg := err.Error()
		if containsString(errMsg, "not found") {
			api.RespondError(w, http.StatusNotFound, "LLM configuration not found")
		} else if containsString(errMsg, "cannot activate") || containsString(errMsg, "API key") {
			api.RespondError(w, http.StatusBadRequest, "Cannot activate a configuration without an API key")
		} else {
			api.RespondError(w, http.StatusInternalServerError, "Failed to activate configuration")
		}
		return
	}

	// Re-read to get updated active status
	settings, err := database.GetLLMSettingsByID(id)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to retrieve activated configuration")
		return
	}
	api.RespondJSON(w, http.StatusOK, llmConfigResponse(settings))
}

// llmConfigResponse builds a standard response map for an LLM config, masking the API key.
func llmConfigResponse(s *database.LLMSettings) map[string]interface{} {
	return map[string]interface{}{
		"id":             s.ID,
		"name":           s.Name,
		"provider":       s.Provider,
		"model":          s.Model,
		"thinking_level": s.ThinkingLevel,
		"base_url":       s.BaseURL,
		"api_key":        maskToken(s.APIKey),
		"is_configured":  s.APIKey != "",
		"enabled":        s.Enabled,
		"active":         s.Active,
		"created_at":     s.CreatedAt,
		"updated_at":     s.UpdatedAt,
	}
}
