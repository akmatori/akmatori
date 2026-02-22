package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"gorm.io/gorm"
)

// APIHandler handles API endpoints for the UI and skill communication
type APIHandler struct {
	skillService          *services.SkillService
	toolService           *services.ToolService
	contextService        *services.ContextService
	alertService          *services.AlertService
	codexExecutor         *executor.Executor
	codexWSHandler        *CodexWSHandler
	slackManager          *slackutil.Manager
	deviceAuthService     *services.DeviceAuthService
	alertChannelReloader  func() // called after alert source create/update/delete to reload Slack channel mappings
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(skillService *services.SkillService, toolService *services.ToolService, contextService *services.ContextService, alertService *services.AlertService, codexExecutor *executor.Executor, codexWSHandler *CodexWSHandler, slackManager *slackutil.Manager) *APIHandler {
	return &APIHandler{
		skillService:      skillService,
		toolService:       toolService,
		contextService:    contextService,
		alertService:      alertService,
		codexExecutor:     codexExecutor,
		codexWSHandler:    codexWSHandler,
		slackManager:      slackManager,
		deviceAuthService: services.NewDeviceAuthService(),
	}
}

// SetAlertChannelReloader sets the callback invoked after alert source create/update/delete
// to reload Slack channel mappings at runtime.
func (h *APIHandler) SetAlertChannelReloader(fn func()) {
	h.alertChannelReloader = fn
}

// reloadAlertChannels triggers the alert channel reload callback if set
func (h *APIHandler) reloadAlertChannels() {
	if h.alertChannelReloader != nil {
		go h.alertChannelReloader()
	}
}

// SetupRoutes sets up all API routes
func (h *APIHandler) SetupRoutes(mux *http.ServeMux) {
	// Skills management
	mux.HandleFunc("/api/skills", h.handleSkills)
	mux.HandleFunc("/api/skills/", h.handleSkillByName)
	mux.HandleFunc("/api/skills/sync", h.handleSkillsSync)

	// Tool types and instances
	mux.HandleFunc("/api/tool-types", h.handleToolTypes)
	mux.HandleFunc("/api/tools", h.handleTools)
	mux.HandleFunc("/api/tools/", h.handleToolByID)

	// Incidents
	mux.HandleFunc("/api/incidents", h.handleIncidents)
	mux.HandleFunc("/api/incidents/", h.handleIncidentByID)

	// Incident alerts management
	mux.HandleFunc("GET /api/incidents/{uuid}/alerts", h.handleGetIncidentAlerts)
	mux.HandleFunc("POST /api/incidents/{uuid}/alerts", h.handleAttachAlert)
	mux.HandleFunc("DELETE /api/incidents/{uuid}/alerts/{alertId}", h.handleDetachAlert)
	mux.HandleFunc("POST /api/incidents/{uuid}/merge", h.handleMergeIncident)

	// Slack settings
	mux.HandleFunc("/api/settings/slack", h.handleSlackSettings)

	// OpenAI settings
	mux.HandleFunc("/api/settings/openai", h.handleOpenAISettings)
	mux.HandleFunc("/api/settings/openai/device-auth/start", h.handleDeviceAuthStart)
	mux.HandleFunc("/api/settings/openai/device-auth/status", h.handleDeviceAuthStatus)
	mux.HandleFunc("/api/settings/openai/device-auth/cancel", h.handleDeviceAuthCancel)
	mux.HandleFunc("/api/settings/openai/chatgpt/disconnect", h.handleChatGPTDisconnect)

	// Proxy settings
	mux.HandleFunc("/api/settings/proxy", h.handleProxySettings)

	// Aggregation settings
	mux.HandleFunc("GET /api/settings/aggregation", h.handleGetAggregationSettings)
	mux.HandleFunc("PUT /api/settings/aggregation", h.handleUpdateAggregationSettings)

	// Context files
	mux.HandleFunc("/api/context", h.handleContext)
	mux.HandleFunc("/api/context/", h.handleContextByID)
	mux.HandleFunc("/api/context/validate", h.handleContextValidate)

	// Alert source types and instances
	mux.HandleFunc("/api/alert-source-types", h.handleAlertSourceTypes)
	mux.HandleFunc("/api/alert-sources", h.handleAlertSources)
	mux.HandleFunc("/api/alert-sources/", h.handleAlertSourceByUUID)
}

// handleSkills handles GET /api/skills and POST /api/skills
func (h *APIHandler) handleSkills(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var skills []database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Find(&skills).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to get skills: %v", err), http.StatusInternalServerError)
			return
		}

		// For each skill, get the prompt from filesystem
		type SkillResponse struct {
			database.Skill
			Prompt string `json:"prompt"`
		}
		var response []SkillResponse
		for _, skill := range skills {
			prompt, _ := h.skillService.GetSkillPrompt(skill.Name)
			response = append(response, SkillResponse{
				Skill:  skill,
				Prompt: prompt,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Failed to encode response: %v", err)
		}

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Category    string `json:"category"`
			Prompt      string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		skill, err := h.skillService.CreateSkill(req.Name, req.Description, req.Category, req.Prompt)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create skill: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(skill); err != nil {
			log.Printf("Failed to encode skill response: %v", err)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillByName handles GET /api/skills/:name, PUT /api/skills/:name, DELETE /api/skills/:name
// Also handles /api/skills/:name/prompt, /api/skills/:name/tools, /api/skills/:name/scripts
func (h *APIHandler) handleSkillByName(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	// Extract path after /api/skills/
	path := r.URL.Path[len("/api/skills/"):]

	// Check for sub-routes
	if len(path) > 0 {
		parts := splitPath(path)
		if len(parts) >= 2 {
			skillName := parts[0]
			subPath := parts[1]

			switch subPath {
			case "prompt":
				h.handleSkillPrompt(w, r, skillName)
				return
			case "tools":
				h.handleSkillTools(w, r, skillName)
				return
			case "scripts":
				if len(parts) == 2 {
					h.handleSkillScripts(w, r, skillName)
				} else if len(parts) == 3 {
					h.handleSkillScriptByFilename(w, r, skillName, parts[2])
				}
				return
			}
		}
	}

	// Extract skill name for regular operations
	skillName := path

	switch r.Method {
	case http.MethodGet:
		var skill database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill).Error; err != nil {
			http.Error(w, "Skill not found", http.StatusNotFound)
			return
		}

		// Get prompt from filesystem
		prompt, _ := h.skillService.GetSkillPrompt(skill.Name)

		response := struct {
			database.Skill
			Prompt string `json:"prompt"`
		}{
			Skill:  skill,
			Prompt: prompt,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Failed to encode response: %v", err)
		}

	case http.MethodPut:
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		// Get existing skill
		var skill database.Skill
		if err := db.Where("name = ?", skillName).First(&skill).Error; err != nil {
			http.Error(w, "Skill not found", http.StatusNotFound)
			return
		}

		// Only allow updating specific fields
		allowedFields := map[string]bool{
			"description": true,
			"category":    true,
			"enabled":     true,
		}
		filteredUpdates := make(map[string]interface{})
		for key, value := range updates {
			if allowedFields[key] {
				filteredUpdates[key] = value
			}
		}

		if len(filteredUpdates) > 0 {
			if err := db.Model(&database.Skill{}).Where("name = ?", skillName).Updates(filteredUpdates).Error; err != nil {
				http.Error(w, fmt.Sprintf("Failed to update skill: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// If prompt is included, update it separately
		if prompt, ok := updates["prompt"].(string); ok {
			if err := h.skillService.UpdateSkillPrompt(skillName, prompt); err != nil {
				http.Error(w, fmt.Sprintf("Failed to update skill prompt: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// Return updated skill
		db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill)
		promptText, _ := h.skillService.GetSkillPrompt(skill.Name)
		response := struct {
			database.Skill
			Prompt string `json:"prompt"`
		}{
			Skill:  skill,
			Prompt: promptText,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.skillService.DeleteSkill(skillName); err != nil {
			// Check if it's a system skill deletion attempt
			if strings.Contains(err.Error(), "cannot delete system skill") {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			http.Error(w, fmt.Sprintf("Failed to delete skill: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillPrompt handles GET/PUT /api/skills/:name/prompt
func (h *APIHandler) handleSkillPrompt(w http.ResponseWriter, r *http.Request, skillName string) {
	switch r.Method {
	case http.MethodGet:
		prompt, err := h.skillService.GetSkillPrompt(skillName)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get prompt: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"prompt": prompt}); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if err := h.skillService.UpdateSkillPrompt(skillName, req.Prompt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to update prompt: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillTools handles GET/PUT /api/skills/:name/tools
func (h *APIHandler) handleSkillTools(w http.ResponseWriter, r *http.Request, skillName string) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var skill database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill).Error; err != nil {
			http.Error(w, "Skill not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(skill.Tools); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			ToolInstanceIDs []uint `json:"tool_instance_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		// Assign tools (this creates symlinks and generates tools.md)
		if err := h.skillService.AssignTools(skillName, req.ToolInstanceIDs); err != nil {
			http.Error(w, fmt.Sprintf("Failed to assign tools: %v", err), http.StatusInternalServerError)
			return
		}

		// Return updated skill with tools
		var skill database.Skill
		db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(skill); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillsSync handles POST /api/skills/sync
func (h *APIHandler) handleSkillsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.skillService.SyncSkillsFromFilesystem(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to sync skills: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Skills synced from filesystem"}); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleToolTypes handles GET /api/tool-types
func (h *APIHandler) handleToolTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	toolTypes, err := h.toolService.ListToolTypes()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get tool types: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toolTypes); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleTools handles GET /api/tools and POST /api/tools
func (h *APIHandler) handleTools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		instances, err := h.toolService.ListToolInstances()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get tools: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instances); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		var req struct {
			ToolTypeID uint           `json:"tool_type_id"`
			Name       string         `json:"name"`
			Settings   database.JSONB `json:"settings"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		instance, err := h.toolService.CreateToolInstance(req.ToolTypeID, req.Name, req.Settings)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create tool instance: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleToolByID handles GET /api/tools/:id, PUT /api/tools/:id, DELETE /api/tools/:id
// Also handles /api/tools/:id/ssh-keys routes
func (h *APIHandler) handleToolByID(w http.ResponseWriter, r *http.Request) {
	// Extract path after /api/tools/
	path := r.URL.Path[len("/api/tools/"):]
	parts := strings.Split(path, "/")

	// Parse tool ID
	id, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		http.Error(w, "Invalid tool ID", http.StatusBadRequest)
		return
	}

	// Check for sub-routes like /api/tools/:id/ssh-keys
	if len(parts) >= 2 && parts[1] == "ssh-keys" {
		if len(parts) == 2 {
			h.handleSSHKeys(w, r, uint(id))
		} else if len(parts) == 3 {
			h.handleSSHKeyByID(w, r, uint(id), parts[2])
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		instance, err := h.toolService.GetToolInstance(uint(id))
		if err != nil {
			http.Error(w, "Tool not found", http.StatusNotFound)
			return
		}

		// Mask SSH keys (remove private_key from response)
		h.maskSSHKeys(instance)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			Name     string         `json:"name"`
			Settings database.JSONB `json:"settings"`
			Enabled  bool           `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if err := h.toolService.UpdateToolInstance(uint(id), req.Name, req.Settings, req.Enabled); err != nil {
			http.Error(w, fmt.Sprintf("Failed to update tool: %v", err), http.StatusInternalServerError)
			return
		}

		instance, _ := h.toolService.GetToolInstance(uint(id))
		h.maskSSHKeys(instance)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.toolService.DeleteToolInstance(uint(id)); err != nil {
			http.Error(w, fmt.Sprintf("Failed to delete tool: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// maskSSHKeys removes private_key from SSH keys in the response
func (h *APIHandler) maskSSHKeys(instance *database.ToolInstance) {
	if instance == nil || instance.Settings == nil {
		return
	}

	if keys, ok := instance.Settings["ssh_keys"].([]interface{}); ok {
		for _, keyData := range keys {
			if keyMap, ok := keyData.(map[string]interface{}); ok {
				delete(keyMap, "private_key")
			}
		}
	}
}

// handleSSHKeys handles GET/POST /api/tools/:id/ssh-keys
func (h *APIHandler) handleSSHKeys(w http.ResponseWriter, r *http.Request, toolID uint) {
	switch r.Method {
	case http.MethodGet:
		keys, err := h.toolService.GetSSHKeys(toolID)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get SSH keys: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(keys); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		var req struct {
			Name       string `json:"name"`
			PrivateKey string `json:"private_key"`
			IsDefault  bool   `json:"is_default"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if req.PrivateKey == "" {
			http.Error(w, "private_key is required", http.StatusBadRequest)
			return
		}

		key, err := h.toolService.AddSSHKey(toolID, req.Name, req.PrivateKey, req.IsDefault)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				http.Error(w, err.Error(), http.StatusConflict)
			} else {
				http.Error(w, fmt.Sprintf("Failed to add SSH key: %v", err), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(key); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSSHKeyByID handles PUT/DELETE /api/tools/:id/ssh-keys/:keyID
func (h *APIHandler) handleSSHKeyByID(w http.ResponseWriter, r *http.Request, toolID uint, keyID string) {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name      *string `json:"name"`
			IsDefault *bool   `json:"is_default"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		key, err := h.toolService.UpdateSSHKey(toolID, keyID, req.Name, req.IsDefault)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else if strings.Contains(err.Error(), "already exists") {
				http.Error(w, err.Error(), http.StatusConflict)
			} else {
				http.Error(w, fmt.Sprintf("Failed to update SSH key: %v", err), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(key); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.toolService.DeleteSSHKey(toolID, keyID); err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else if strings.Contains(err.Error(), "cannot delete") {
				http.Error(w, err.Error(), http.StatusConflict)
			} else {
				http.Error(w, fmt.Sprintf("Failed to delete SSH key: %v", err), http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// CreateIncidentRequest represents the request to create an incident via API
type CreateIncidentRequest struct {
	Task    string                 `json:"task"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// handleIncidents handles GET /api/incidents and POST /api/incidents
func (h *APIHandler) handleIncidents(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var incidents []database.Incident
		query := db.Order("created_at DESC")

		// Parse time range filters (unix timestamps in seconds)
		fromParam := r.URL.Query().Get("from")
		toParam := r.URL.Query().Get("to")

		if fromParam != "" {
			from, err := strconv.ParseInt(fromParam, 10, 64)
			if err == nil {
				query = query.Where("created_at >= ?", time.Unix(from, 0))
			}
		}
		if toParam != "" {
			to, err := strconv.ParseInt(toParam, 10, 64)
			if err == nil {
				query = query.Where("created_at <= ?", time.Unix(to, 0))
			}
		}

		if err := query.Limit(500).Find(&incidents).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to get incidents: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(incidents); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		var req CreateIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if req.Task == "" {
			http.Error(w, "Task is required", http.StatusBadRequest)
			return
		}

		// Create incident context
		incidentContext := &services.IncidentContext{
			Source:   "api",
			SourceID: fmt.Sprintf("api-%d", time.Now().UnixNano()),
			Context: database.JSONB{
				"task":       req.Task,
				"created_by": "api",
			},
			Message: req.Task,
		}

		// Merge any additional context provided
		if req.Context != nil {
			for k, v := range req.Context {
				incidentContext.Context[k] = v
			}
		}

		// Spawn incident manager
		incidentUUID, workingDir, err := h.skillService.SpawnIncidentManager(incidentContext)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create incident: %v", err), http.StatusInternalServerError)
			return
		}

		log.Printf("Created incident via API: %s", incidentUUID)

		// Execute in background (non-blocking)
		go func() {
			taskHeader := fmt.Sprintf("📝 API Incident Task:\n%s\n\n--- Execution Log ---\n\n", req.Task)
			if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting execution..."); err != nil {
				log.Printf("Failed to update incident status: %v", err)
			}

			taskWithGuidance := executor.PrependGuidance(req.Task)

			// Try WebSocket-based execution first (new architecture)
			if h.codexWSHandler != nil && h.codexWSHandler.IsWorkerConnected() {
				log.Printf("Using WebSocket-based Codex worker for API incident %s", incidentUUID)

				// Fetch OpenAI settings from database
				var openaiSettings *OpenAISettings
				if dbSettings, err := database.GetOpenAISettings(); err == nil && dbSettings != nil {
					openaiSettings = &OpenAISettings{
						APIKey:          dbSettings.APIKey,
						Model:           dbSettings.Model,
						ReasoningEffort: dbSettings.ModelReasoningEffort,
						BaseURL:         dbSettings.BaseURL,
						ProxyURL:        dbSettings.ProxyURL,
						NoProxy:         dbSettings.NoProxy,
						// ChatGPT subscription auth fields
						AuthMethod:          string(dbSettings.AuthMethod),
						ChatGPTAccessToken:  dbSettings.ChatGPTAccessToken,
						ChatGPTRefreshToken: dbSettings.ChatGPTRefreshToken,
						ChatGPTIDToken:      dbSettings.ChatGPTIDToken,
					}
					// Add expiry timestamp if set
					if dbSettings.ChatGPTExpiresAt != nil {
						openaiSettings.ChatGPTExpiresAt = dbSettings.ChatGPTExpiresAt.Format(time.RFC3339)
					}
					log.Printf("Using OpenAI model: %s, auth method: %s", dbSettings.Model, dbSettings.AuthMethod)
				}

				// Create channels for async result handling
				done := make(chan struct{})
				var response string
				var sessionID string
				var hasError bool
				var lastStreamedLog string // Keep track of the accumulated log

				callback := IncidentCallback{
					OnOutput: func(output string) {
						lastStreamedLog = output // Save the accumulated log
						if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+output); err != nil {
							log.Printf("Failed to update incident log: %v", err)
						}
					},
					OnCompleted: func(sid, output string) {
						sessionID = sid
						response = output
						close(done)
					},
					OnError: func(errorMsg string) {
						response = fmt.Sprintf("❌ Error: %s", errorMsg)
						hasError = true
						close(done)
					},
				}

				if err := h.codexWSHandler.StartIncident(incidentUUID, taskWithGuidance, openaiSettings, callback); err != nil {
					log.Printf("Failed to start incident via WebSocket: %v, falling back to local execution", err)
					h.runIncidentLocal(incidentUUID, workingDir, taskHeader, taskWithGuidance)
					return
				}

				// Wait for completion
				<-done

				// Build full log: task header + streamed log + final response
				fullLog := taskHeader + lastStreamedLog
				if response != "" {
					fullLog += "\n\n--- Final Response ---\n\n" + response
				}

				// Update incident status
				if hasError {
					if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, sessionID, fullLog, response); err != nil {
						log.Printf("Failed to update incident complete: %v", err)
					}
				} else {
					if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, sessionID, fullLog, response); err != nil {
						log.Printf("Failed to update incident complete: %v", err)
					}
				}

				log.Printf("API incident %s completed (via WebSocket)", incidentUUID)
				return
			}

			// Fall back to local execution (legacy)
			log.Printf("WebSocket worker not available, using local execution for API incident %s", incidentUUID)
			h.runIncidentLocal(incidentUUID, workingDir, taskHeader, taskWithGuidance)
		}()

		response := map[string]interface{}{
			"uuid":        incidentUUID,
			"status":      "pending",
			"working_dir": workingDir,
			"message":     "Incident created and processing started",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// runIncidentLocal runs incident using the local executor (legacy fallback)
func (h *APIHandler) runIncidentLocal(incidentUUID, workingDir, taskHeader, taskWithGuidance string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	progressCallback := func(progressLog string) {
		if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+progressLog); err != nil {
			log.Printf("Failed to update incident log: %v", err)
		}
	}

	result, err := h.codexExecutor.ExecuteInDirectory(ctx, taskWithGuidance, "", workingDir, progressCallback)

	fullLogWithContext := taskHeader + result.FullLog

	if err != nil {
		log.Printf("Incident %s failed: %v", incidentUUID, err)
		if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, result.SessionID, fullLogWithContext+"\n\nError: "+err.Error(), "Error: "+err.Error()); updateErr != nil {
			log.Printf("Failed to update incident complete: %v", updateErr)
		}
		return
	}

	log.Printf("Incident %s completed. Output: %d bytes, Tokens: %d, Session: %s",
		incidentUUID, len(result.Output), result.TokensUsed, result.SessionID)
	if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, result.SessionID, fullLogWithContext, result.Output); err != nil {
		log.Printf("Failed to update incident complete: %v", err)
	}
}

// handleIncidentByID handles GET /api/incidents/:uuid
func (h *APIHandler) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uuid := r.URL.Path[len("/api/incidents/"):]

	incident, err := h.skillService.GetIncident(uuid)
	if err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(incident); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// splitPath splits a URL path by slashes
func splitPath(path string) []string {
	result := []string{}
	current := ""
	for _, char := range path {
		if char == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// handleSkillScripts handles GET/DELETE /api/skills/:name/scripts
func (h *APIHandler) handleSkillScripts(w http.ResponseWriter, r *http.Request, skillName string) {
	switch r.Method {
	case http.MethodGet:
		scripts, err := h.skillService.ListSkillScripts(skillName)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list scripts: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"skill_name":  skillName,
			"scripts_dir": h.skillService.GetSkillScriptsDir(skillName),
			"scripts":     scripts,
		}); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.skillService.ClearSkillScripts(skillName); err != nil {
			http.Error(w, fmt.Sprintf("Failed to clear scripts: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"message":    "Scripts cleared successfully",
			"skill_name": skillName,
		}); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSkillScriptByFilename handles GET/PUT/DELETE /api/skills/:name/scripts/:filename
func (h *APIHandler) handleSkillScriptByFilename(w http.ResponseWriter, r *http.Request, skillName, filename string) {
	switch r.Method {
	case http.MethodGet:
		scriptInfo, err := h.skillService.GetSkillScript(skillName, filename)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else if strings.Contains(err.Error(), "invalid filename") {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, fmt.Sprintf("Failed to get script: %v", err), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(scriptInfo); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if err := h.skillService.UpdateSkillScript(skillName, filename, req.Content); err != nil {
			if strings.Contains(err.Error(), "invalid filename") {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, fmt.Sprintf("Failed to update script: %v", err), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"filename": filename,
		}); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.skillService.DeleteSkillScript(skillName, filename); err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else if strings.Contains(err.Error(), "invalid filename") {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, fmt.Sprintf("Failed to delete script: %v", err), http.StatusInternalServerError)
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSlackSettings handles GET /api/settings/slack and PUT /api/settings/slack
func (h *APIHandler) handleSlackSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var settings database.SlackSettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
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
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			BotToken      *string `json:"bot_token"`
			SigningSecret *string `json:"signing_secret"`
			AppToken      *string `json:"app_token"`
			AlertsChannel *string `json:"alerts_channel"`
			Enabled       *bool   `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		var settings database.SlackSettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
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
			http.Error(w, fmt.Sprintf("Failed to update settings: %v", err), http.StatusInternalServerError)
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
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
// e.g., "http://user:secret@proxy:8080" -> "http://user:****@proxy:8080"
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
		return true // Empty is valid (optional field)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// ModelConfig defines the available models and their valid reasoning effort options
var ModelConfigs = map[string][]string{
	"gpt-5.2":            {"low", "medium", "high", "extra_high"},
	"gpt-5.2-codex":      {"low", "medium", "high", "extra_high"},
	"gpt-5.1-codex-max":  {"low", "medium", "high", "extra_high"},
	"gpt-5.1-codex":      {"low", "medium", "high"},
	"gpt-5.1-codex-mini": {"medium", "high"},
	"gpt-5.1":            {"low", "medium", "high"},
}

// handleOpenAISettings handles GET /api/settings/openai and PUT /api/settings/openai
func (h *APIHandler) handleOpenAISettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var settings database.OpenAISettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
			return
		}

		// Determine auth method (default to api_key for backward compatibility)
		authMethod := string(settings.AuthMethod)
		if authMethod == "" {
			authMethod = string(database.AuthMethodAPIKey)
		}

		response := map[string]interface{}{
			"id":                      settings.ID,
			"api_key":                 maskToken(settings.APIKey),
			"model":                   settings.Model,
			"model_reasoning_effort":  settings.ModelReasoningEffort,
			"base_url":                settings.BaseURL,
			"proxy_url":               maskProxyURL(settings.ProxyURL),
			"no_proxy":                settings.NoProxy,
			"is_configured":           settings.IsConfigured(),
			"valid_reasoning_efforts": settings.GetValidReasoningEfforts(),
			"available_models":        ModelConfigs,
			"created_at":              settings.CreatedAt,
			"updated_at":              settings.UpdatedAt,
			// New auth method fields
			"auth_method":             authMethod,
			"chatgpt_email":           settings.ChatGPTUserEmail,
			"chatgpt_expires_at":      settings.ChatGPTExpiresAt,
			"chatgpt_expired":         settings.IsChatGPTTokenExpired(),
			"chatgpt_connected":       settings.ChatGPTAccessToken != "" && settings.ChatGPTRefreshToken != "",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			APIKey               *string `json:"api_key"`
			Model                *string `json:"model"`
			ModelReasoningEffort *string `json:"model_reasoning_effort"`
			BaseURL              *string `json:"base_url"`
			ProxyURL             *string `json:"proxy_url"`
			NoProxy              *string `json:"no_proxy"`
			AuthMethod           *string `json:"auth_method"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		var settings database.OpenAISettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
			return
		}

		// Validate URLs if provided
		if req.BaseURL != nil && !isValidURL(*req.BaseURL) {
			http.Error(w, "Invalid base_url: must be a valid HTTP or HTTPS URL", http.StatusBadRequest)
			return
		}
		if req.ProxyURL != nil && !isValidURL(*req.ProxyURL) {
			http.Error(w, "Invalid proxy_url: must be a valid HTTP or HTTPS URL", http.StatusBadRequest)
			return
		}

		if req.Model != nil {
			if _, ok := ModelConfigs[*req.Model]; !ok {
				http.Error(w, fmt.Sprintf("Invalid model: %s", *req.Model), http.StatusBadRequest)
				return
			}
		}

		if req.ModelReasoningEffort != nil {
			model := settings.Model
			if req.Model != nil {
				model = *req.Model
			}
			validEfforts := ModelConfigs[model]
			isValid := false
			for _, e := range validEfforts {
				if e == *req.ModelReasoningEffort {
					isValid = true
					break
				}
			}
			if !isValid {
				http.Error(w, fmt.Sprintf("Invalid reasoning effort '%s' for model '%s'. Valid options: %v",
					*req.ModelReasoningEffort, model, validEfforts), http.StatusBadRequest)
				return
			}
		}

		updates := make(map[string]interface{})
		if req.APIKey != nil {
			updates["api_key"] = *req.APIKey
		}
		if req.Model != nil {
			updates["model"] = *req.Model
			if req.ModelReasoningEffort == nil {
				validEfforts := ModelConfigs[*req.Model]
				currentEffortValid := false
				for _, e := range validEfforts {
					if e == settings.ModelReasoningEffort {
						currentEffortValid = true
						break
					}
				}
				if !currentEffortValid {
					defaultEffort := validEfforts[0]
					for _, e := range validEfforts {
						if e == "medium" {
							defaultEffort = "medium"
							break
						}
					}
					updates["model_reasoning_effort"] = defaultEffort
				}
			}
		}
		if req.ModelReasoningEffort != nil {
			updates["model_reasoning_effort"] = *req.ModelReasoningEffort
		}
		if req.BaseURL != nil {
			updates["base_url"] = *req.BaseURL
		}
		if req.ProxyURL != nil {
			updates["proxy_url"] = *req.ProxyURL
		}
		if req.NoProxy != nil {
			updates["no_proxy"] = *req.NoProxy
		}
		if req.AuthMethod != nil {
			// Validate auth method
			if *req.AuthMethod != string(database.AuthMethodAPIKey) && *req.AuthMethod != string(database.AuthMethodChatGPTSubscription) {
				http.Error(w, fmt.Sprintf("Invalid auth_method: %s. Valid options: api_key, chatgpt_subscription", *req.AuthMethod), http.StatusBadRequest)
				return
			}
			updates["auth_method"] = *req.AuthMethod
		}

		if err := db.Model(&settings).Updates(updates).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to update settings: %v", err), http.StatusInternalServerError)
			return
		}

		db.First(&settings)

		// Determine auth method for response
		respAuthMethod := string(settings.AuthMethod)
		if respAuthMethod == "" {
			respAuthMethod = string(database.AuthMethodAPIKey)
		}

		response := map[string]interface{}{
			"id":                      settings.ID,
			"api_key":                 maskToken(settings.APIKey),
			"model":                   settings.Model,
			"model_reasoning_effort":  settings.ModelReasoningEffort,
			"base_url":                settings.BaseURL,
			"proxy_url":               maskProxyURL(settings.ProxyURL),
			"no_proxy":                settings.NoProxy,
			"is_configured":           settings.IsConfigured(),
			"valid_reasoning_efforts": settings.GetValidReasoningEfforts(),
			"available_models":        ModelConfigs,
			"created_at":              settings.CreatedAt,
			"updated_at":              settings.UpdatedAt,
			// New auth method fields
			"auth_method":             respAuthMethod,
			"chatgpt_email":           settings.ChatGPTUserEmail,
			"chatgpt_expires_at":      settings.ChatGPTExpiresAt,
			"chatgpt_expired":         settings.IsChatGPTTokenExpired(),
			"chatgpt_connected":       settings.ChatGPTAccessToken != "" && settings.ChatGPTRefreshToken != "",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleDeviceAuthStart handles POST /api/settings/openai/device-auth/start
func (h *APIHandler) handleDeviceAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if worker is connected
	if h.codexWSHandler == nil || !h.codexWSHandler.IsWorkerConnected() {
		http.Error(w, "Codex worker not connected", http.StatusServiceUnavailable)
		return
	}

	// Fetch OpenAI settings from database for proxy configuration
	var openaiSettings *OpenAISettings
	if dbSettings, err := database.GetOpenAISettings(); err == nil && dbSettings != nil {
		openaiSettings = &OpenAISettings{
			BaseURL:  dbSettings.BaseURL,
			ProxyURL: dbSettings.ProxyURL,
			NoProxy:  dbSettings.NoProxy,
		}
	}

	// Clear any existing flow
	h.deviceAuthService.ClearFlow()

	// Start device auth via WebSocket to codex worker (with proxy settings)
	err := h.codexWSHandler.StartDeviceAuth(func(result *DeviceAuthResult) {
		// Forward result to device auth service
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
		http.Error(w, fmt.Sprintf("Failed to start device authentication: %v", err), http.StatusInternalServerError)
		return
	}

	// Wait for initial response (with device code and URL)
	response, err := h.deviceAuthService.WaitForInitialResponse(30 * time.Second)
	if err != nil {
		log.Printf("Failed to get device auth codes: %v", err)
		http.Error(w, fmt.Sprintf("Failed to start device authentication: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleDeviceAuthStatus handles GET /api/settings/openai/device-auth/status
func (h *APIHandler) handleDeviceAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	deviceCode := r.URL.Query().Get("device_code")
	if deviceCode == "" {
		http.Error(w, "device_code query parameter is required", http.StatusBadRequest)
		return
	}

	status, err := h.deviceAuthService.GetDeviceAuthStatus(deviceCode)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get device auth status: %v", err), http.StatusInternalServerError)
		return
	}

	// If authentication completed, save tokens to database
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleDeviceAuthCancel handles POST /api/settings/openai/device-auth/cancel
func (h *APIHandler) handleDeviceAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cancel via WebSocket if worker is connected
	if h.codexWSHandler != nil && h.codexWSHandler.IsWorkerConnected() {
		if err := h.codexWSHandler.CancelDeviceAuth(); err != nil {
			log.Printf("Failed to cancel device auth via WebSocket: %v", err)
		}
	}

	// Also clear local state
	h.deviceAuthService.CancelDeviceAuth()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Device authentication cancelled",
	}); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleChatGPTDisconnect handles POST /api/settings/openai/chatgpt/disconnect
func (h *APIHandler) handleChatGPTDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	settings, err := database.GetOpenAISettings()
	if err != nil {
		http.Error(w, "Settings not found", http.StatusNotFound)
		return
	}

	// Clear ChatGPT tokens
	if err := database.ClearOpenAIChatGPTTokens(settings.ID); err != nil {
		http.Error(w, fmt.Sprintf("Failed to disconnect: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("ChatGPT subscription disconnected")

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "ChatGPT subscription disconnected",
	}); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleProxySettings handles GET /api/settings/proxy and PUT /api/settings/proxy
func (h *APIHandler) handleProxySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetProxySettings(w, r)
	case http.MethodPut:
		h.UpdateProxySettings(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// GetProxySettings returns the current proxy configuration
func (h *APIHandler) GetProxySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		http.Error(w, "Failed to get proxy settings", http.StatusInternalServerError)
		return
	}

	// Mask proxy URL password if present
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// UpdateProxySettings updates proxy configuration
func (h *APIHandler) UpdateProxySettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProxyURL string `json:"proxy_url"`
		NoProxy  string `json:"no_proxy"`
		Services struct {
			OpenAI struct {
				Enabled bool `json:"enabled"`
			} `json:"openai"`
			Slack struct {
				Enabled bool `json:"enabled"`
			} `json:"slack"`
			Zabbix struct {
				Enabled bool `json:"enabled"`
			} `json:"zabbix"`
		} `json:"services"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate proxy URL if provided
	if input.ProxyURL != "" && !isValidURL(input.ProxyURL) {
		http.Error(w, "Invalid proxy URL format", http.StatusBadRequest)
		return
	}

	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		http.Error(w, "Failed to get proxy settings", http.StatusInternalServerError)
		return
	}

	// Update fields
	settings.ProxyURL = input.ProxyURL
	settings.NoProxy = input.NoProxy
	settings.OpenAIEnabled = input.Services.OpenAI.Enabled
	settings.SlackEnabled = input.Services.Slack.Enabled
	settings.ZabbixEnabled = input.Services.Zabbix.Enabled

	if err := database.UpdateProxySettings(settings); err != nil {
		http.Error(w, "Failed to update proxy settings", http.StatusInternalServerError)
		return
	}

	// Notify MCP gateway of config change (skip BroadcastProxyConfig for now - will be added later)
	// if h.codexWSHandler != nil {
	// 	h.codexWSHandler.BroadcastProxyConfig(settings)
	// }

	// Return updated settings
	h.GetProxySettings(w, r)
}

// handleGetAggregationSettings handles GET /api/settings/aggregation
func (h *APIHandler) handleGetAggregationSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	settings, err := database.GetOrCreateAggregationSettings(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(settings); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleUpdateAggregationSettings handles PUT /api/settings/aggregation
func (h *APIHandler) handleUpdateAggregationSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	var settings database.AggregationSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Ensure we update existing record
	existing, err := database.GetOrCreateAggregationSettings(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	settings.ID = existing.ID

	if err := database.UpdateAggregationSettings(db, &settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(settings); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleContext handles GET /api/context and POST /api/context
func (h *APIHandler) handleContext(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		files, err := h.contextService.ListFiles()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list files: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(files); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		if err := r.ParseMultipartForm(services.MaxFileSize); err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get file: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		filename := r.FormValue("filename")
		if filename == "" {
			http.Error(w, "Filename is required", http.StatusBadRequest)
			return
		}

		description := r.FormValue("description")

		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "text/plain"
		}

		contextFile, err := h.contextService.SaveFile(filename, header.Filename, mimeType, description, header.Size, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(contextFile); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleContextByID handles GET /api/context/:id, GET /api/context/:id/download, DELETE /api/context/:id
func (h *APIHandler) handleContextByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/context/"):]

	if strings.HasSuffix(path, "/download") {
		idStr := strings.TrimSuffix(path, "/download")
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			http.Error(w, "Invalid file ID", http.StatusBadRequest)
			return
		}
		h.handleContextDownload(w, r, uint(id))
		return
	}

	id, err := strconv.ParseUint(path, 10, 32)
	if err != nil {
		http.Error(w, "Invalid file ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		file, err := h.contextService.GetFile(uint(id))
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(file); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodDelete:
		if err := h.contextService.DeleteFile(uint(id)); err != nil {
			http.Error(w, fmt.Sprintf("Failed to delete file: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleContextDownload handles GET /api/context/:id/download
func (h *APIHandler) handleContextDownload(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, err := h.contextService.GetFile(id)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	filePath := h.contextService.GetFilePath(file.Filename)

	w.Header().Set("Content-Type", file.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.Filename))

	http.ServeFile(w, r, filePath)
}

// handleContextValidate handles POST /api/context/validate
func (h *APIHandler) handleContextValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	references := h.contextService.ParseReferences(req.Text)
	valid, missing, found := h.contextService.ValidateReferences(req.Text)

	response := map[string]interface{}{
		"valid":      valid,
		"references": references,
		"found":      found,
		"missing":    missing,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// NOTE: updateIncidentProgress was removed as unused. Restore if progress
// streaming to Slack threads is needed. The function posted formatted progress
// updates to the incident's Slack thread using incident.Context["channel"] and
// incident.SourceID as the thread timestamp.

// ========== Alert Source Management ==========

// handleAlertSourceTypes handles GET /api/alert-source-types
func (h *APIHandler) handleAlertSourceTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sourceTypes, err := h.alertService.ListSourceTypes()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list source types: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sourceTypes); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleAlertSources handles GET /api/alert-sources and POST /api/alert-sources
func (h *APIHandler) handleAlertSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		instances, err := h.alertService.ListInstances()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list alert sources: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instances); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		var req struct {
			SourceTypeName string         `json:"source_type_name"`
			Name           string         `json:"name"`
			Description    string         `json:"description"`
			WebhookSecret  string         `json:"webhook_secret"`
			FieldMappings  database.JSONB `json:"field_mappings"`
			Settings       database.JSONB `json:"settings"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		if req.SourceTypeName == "" || req.Name == "" {
			http.Error(w, "source_type_name and name are required", http.StatusBadRequest)
			return
		}

		// Validate slack_channel sources have a channel ID configured
		if req.SourceTypeName == "slack_channel" {
			channelID, _ := req.Settings["slack_channel_id"].(string)
			if strings.TrimSpace(channelID) == "" {
				http.Error(w, "slack_channel_id is required in settings for slack_channel source type", http.StatusBadRequest)
				return
			}
		}

		instance, err := h.alertService.CreateInstance(req.SourceTypeName, req.Name, req.Description, req.WebhookSecret, req.FieldMappings, req.Settings)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create alert source: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
		h.reloadAlertChannels()

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAlertSourceByUUID handles GET/PUT/DELETE /api/alert-sources/{uuid}
func (h *APIHandler) handleAlertSourceByUUID(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Path[len("/api/alert-sources/"):]
	if uuid == "" {
		http.Error(w, "UUID is required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		instance, err := h.alertService.GetInstanceByUUID(uuid)
		if err != nil {
			http.Error(w, "Alert source not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

	case http.MethodPut:
		var req struct {
			Name          *string         `json:"name"`
			Description   *string         `json:"description"`
			WebhookSecret *string         `json:"webhook_secret"`
			FieldMappings *database.JSONB `json:"field_mappings"`
			Settings      *database.JSONB `json:"settings"`
			Enabled       *bool           `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		updates := make(map[string]interface{})
		if req.Name != nil {
			updates["name"] = *req.Name
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.WebhookSecret != nil {
			updates["webhook_secret"] = *req.WebhookSecret
		}
		if req.FieldMappings != nil {
			updates["field_mappings"] = *req.FieldMappings
		}
		if req.Settings != nil {
			updates["settings"] = *req.Settings
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		// If settings are being updated on a slack_channel source, ensure channel ID is not cleared
		if req.Settings != nil {
			existing, err := h.alertService.GetInstanceByUUID(uuid)
			if err == nil && existing.AlertSourceType.Name == "slack_channel" {
				channelID, _ := (*req.Settings)["slack_channel_id"].(string)
				if strings.TrimSpace(channelID) == "" {
					http.Error(w, "slack_channel_id is required in settings for slack_channel source type", http.StatusBadRequest)
					return
				}
			}
		}

		if err := h.alertService.UpdateInstance(uuid, updates); err != nil {
			http.Error(w, fmt.Sprintf("Failed to update alert source: %v", err), http.StatusInternalServerError)
			return
		}

		instance, _ := h.alertService.GetInstanceByUUID(uuid)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instance); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
		h.reloadAlertChannels()

	case http.MethodDelete:
		if err := h.alertService.DeleteInstance(uuid); err != nil {
			http.Error(w, fmt.Sprintf("Failed to delete alert source: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		h.reloadAlertChannels()

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ========== Incident Alerts Management ==========

// handleGetIncidentAlerts handles GET /api/incidents/{uuid}/alerts
func (h *APIHandler) handleGetIncidentAlerts(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	// Check if incident exists
	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Get all alerts for this incident
	var alerts []database.IncidentAlert
	if err := db.Where("incident_id = ?", incident.ID).Order("attached_at DESC").Find(&alerts).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(alerts); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleAttachAlert handles POST /api/incidents/{uuid}/alerts
func (h *APIHandler) handleAttachAlert(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	// Check if incident exists
	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Parse request body
	var alert database.IncidentAlert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Set incident ID and timestamp
	alert.IncidentID = incident.ID
	alert.AttachedAt = time.Now()

	// Create the alert
	if err := db.Create(&alert).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update incident alert count
	if err := db.Model(&incident).Update("alert_count", gorm.Expr("alert_count + 1")).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(alert); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleDetachAlert handles DELETE /api/incidents/{uuid}/alerts/{alertId}
func (h *APIHandler) handleDetachAlert(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")
	alertIdStr := r.PathValue("alertId")

	alertId, err := strconv.ParseUint(alertIdStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid alert ID", http.StatusBadRequest)
		return
	}

	// Check if incident exists
	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	// Check if alert exists and belongs to this incident
	var alert database.IncidentAlert
	if err := db.Where("id = ? AND incident_id = ?", alertId, incident.ID).First(&alert).Error; err != nil {
		http.Error(w, "Alert not found in this incident", http.StatusNotFound)
		return
	}

	// Delete the alert
	if err := db.Delete(&alert).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update incident alert count
	if err := db.Model(&incident).Update("alert_count", gorm.Expr("GREATEST(alert_count - 1, 0)")).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleMergeIncident handles POST /api/incidents/{uuid}/merge
func (h *APIHandler) handleMergeIncident(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	// Check if target incident exists
	var targetIncident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&targetIncident).Error; err != nil {
		http.Error(w, "Target incident not found", http.StatusNotFound)
		return
	}

	// Parse request body
	var req struct {
		SourceIncidentUUID string `json:"source_incident_uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SourceIncidentUUID == "" {
		http.Error(w, "source_incident_uuid is required", http.StatusBadRequest)
		return
	}

	// Check if source incident exists
	var sourceIncident database.Incident
	if err := db.Where("uuid = ?", req.SourceIncidentUUID).First(&sourceIncident).Error; err != nil {
		http.Error(w, "Source incident not found", http.StatusNotFound)
		return
	}

	// Move all alerts from source to target
	if err := db.Model(&database.IncidentAlert{}).Where("incident_id = ?", sourceIncident.ID).Update("incident_id", targetIncident.ID).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update target incident alert count
	var newAlertCount int64
	db.Model(&database.IncidentAlert{}).Where("incident_id = ?", targetIncident.ID).Count(&newAlertCount)
	if err := db.Model(&targetIncident).Update("alert_count", newAlertCount).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	// Mark source incident as merged (using completed status with a note in the response)
	if err := db.Model(&sourceIncident).Updates(map[string]interface{}{
		"status":      database.IncidentStatusCompleted,
		"alert_count": 0,
		"response":    fmt.Sprintf("Merged into incident %s", uuid),
	}).Error; err != nil {
		log.Printf("Warning: Failed to update source incident %s after merge: %v", req.SourceIncidentUUID, err)
	}

	// Return updated target incident
	db.First(&targetIncident, targetIncident.ID)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "Incidents merged successfully",
		"target_incident": targetIncident,
		"alerts_moved":    sourceIncident.AlertCount,
	}); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}
