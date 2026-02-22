package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
	"gorm.io/gorm"
)

// APIHandler handles API endpoints for the UI and skill communication
type APIHandler struct {
	skillService         *services.SkillService
	toolService          *services.ToolService
	contextService       *services.ContextService
	alertService         *services.AlertService
	codexExecutor        *executor.Executor
	agentWSHandler       *AgentWSHandler
	slackManager         *slackutil.Manager
	alertChannelReloader func() // called after alert source create/update/delete to reload Slack channel mappings
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(skillService *services.SkillService, toolService *services.ToolService, contextService *services.ContextService, alertService *services.AlertService, codexExecutor *executor.Executor, agentWSHandler *AgentWSHandler, slackManager *slackutil.Manager) *APIHandler {
	return &APIHandler{
		skillService:   skillService,
		toolService:    toolService,
		contextService: contextService,
		alertService:   alertService,
		codexExecutor:  codexExecutor,
		agentWSHandler: agentWSHandler,
		slackManager:   slackManager,
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

	// LLM settings
	mux.HandleFunc("/api/settings/llm", h.handleLLMSettings)

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
		json.NewEncoder(w).Encode(response)

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
		json.NewEncoder(w).Encode(skill)

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
		json.NewEncoder(w).Encode(response)

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
		json.NewEncoder(w).Encode(response)

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
		json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})

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
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

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
		json.NewEncoder(w).Encode(skill.Tools)

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
		json.NewEncoder(w).Encode(skill)

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
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Skills synced from filesystem"})
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
	json.NewEncoder(w).Encode(toolTypes)
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
		json.NewEncoder(w).Encode(instances)

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
		json.NewEncoder(w).Encode(instance)

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
		json.NewEncoder(w).Encode(instance)

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
		json.NewEncoder(w).Encode(instance)

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
		json.NewEncoder(w).Encode(keys)

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
		json.NewEncoder(w).Encode(key)

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
		json.NewEncoder(w).Encode(key)

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
		json.NewEncoder(w).Encode(incidents)

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
			h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting execution...")

			taskWithGuidance := executor.PrependGuidance(req.Task)

			// Try WebSocket-based execution first (new architecture)
			if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
				log.Printf("Using WebSocket-based agent worker for API incident %s", incidentUUID)

				// Fetch LLM settings from database
				var llmSettings *LLMSettingsForWorker
				if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
					llmSettings = BuildLLMSettingsForWorker(dbSettings)
					log.Printf("Using LLM provider: %s, model: %s", dbSettings.Provider, dbSettings.Model)
				}

				// Create channels for async result handling
				done := make(chan struct{})
				var closeOnce sync.Once
				var response string
				var sessionID string
				var hasError bool
				var lastStreamedLog string // Keep track of the accumulated log

				callback := IncidentCallback{
					OnOutput: func(output string) {
						lastStreamedLog += output // Append streamed output delta
						h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog)
					},
					OnCompleted: func(sid, output string) {
						sessionID = sid
						response = output
						closeOnce.Do(func() { close(done) })
					},
					OnError: func(errorMsg string) {
						response = fmt.Sprintf("❌ Error: %s", errorMsg)
						hasError = true
						closeOnce.Do(func() { close(done) })
					},
				}

				if err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, callback); err != nil {
					log.Printf("Failed to start incident via WebSocket: %v", err)
					errorMsg := fmt.Sprintf("Failed to start incident: %v", err)
					h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg)
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
					h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, sessionID, fullLog, response)
				} else {
					h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, sessionID, fullLog, response)
				}

				log.Printf("API incident %s completed (via WebSocket)", incidentUUID)
				return
			}

			// No WebSocket worker available
			log.Printf("ERROR: Agent worker not connected for API incident %s", incidentUUID)
			errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
			h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg)
		}()

		response := map[string]interface{}{
			"uuid":        incidentUUID,
			"status":      "pending",
			"working_dir": workingDir,
			"message":     "Incident created and processing started",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	json.NewEncoder(w).Encode(incident)
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"skill_name":  skillName,
			"scripts_dir": h.skillService.GetSkillScriptsDir(skillName),
			"scripts":     scripts,
		})

	case http.MethodDelete:
		if err := h.skillService.ClearSkillScripts(skillName); err != nil {
			http.Error(w, fmt.Sprintf("Failed to clear scripts: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":    "Scripts cleared successfully",
			"skill_name": skillName,
		})

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
		json.NewEncoder(w).Encode(scriptInfo)

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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"filename": filename,
		})

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
		json.NewEncoder(w).Encode(response)

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
		json.NewEncoder(w).Encode(response)

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

// handleLLMSettings handles GET /api/settings/llm and PUT /api/settings/llm.
//
// GET returns all provider settings so the frontend can show per-provider API keys.
// PUT updates a specific provider's settings (identified by the provider field) and
// marks it as the active provider.
func (h *APIHandler) handleLLMSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		allSettings, err := database.GetAllLLMSettings()
		if err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
			return
		}

		// Build per-provider map
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

		// Backward-compatible: also include top-level fields from the active provider
		active, _ := database.GetLLMSettings()
		response := map[string]interface{}{
			"active_provider": activeProvider,
			"providers":       providers,
			// Top-level fields for backward compat with existing consumers
			"id":             active.ID,
			"provider":       active.Provider,
			"api_key":        maskToken(active.APIKey),
			"model":          active.Model,
			"thinking_level": active.ThinkingLevel,
			"base_url":       active.BaseURL,
			"is_configured":  active.APIKey != "",
			"created_at":     active.CreatedAt,
			"updated_at":     active.UpdatedAt,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	case http.MethodPut:
		var req struct {
			Provider      *string `json:"provider"`
			APIKey        *string `json:"api_key"`
			Model         *string `json:"model"`
			ThinkingLevel *string `json:"thinking_level"`
			BaseURL       *string `json:"base_url"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		// Provider is required for per-provider updates
		if req.Provider == nil || *req.Provider == "" {
			http.Error(w, "provider is required", http.StatusBadRequest)
			return
		}

		if !database.IsValidLLMProvider(*req.Provider) {
			http.Error(w, fmt.Sprintf("Invalid provider: %s. Valid options: openai, anthropic, google, openrouter, custom", *req.Provider), http.StatusBadRequest)
			return
		}

		// Validate base URL if provided
		if req.BaseURL != nil && *req.BaseURL != "" && !isValidURL(*req.BaseURL) {
			http.Error(w, "Invalid base_url: must be a valid HTTP or HTTPS URL", http.StatusBadRequest)
			return
		}

		// Validate thinking level if provided
		if req.ThinkingLevel != nil && !database.IsValidThinkingLevel(*req.ThinkingLevel) {
			http.Error(w, fmt.Sprintf("Invalid thinking_level: %s. Valid options: off, minimal, low, medium, high, xhigh", *req.ThinkingLevel), http.StatusBadRequest)
			return
		}

		provider := database.LLMProvider(*req.Provider)

		// Get the provider's row
		settings, err := database.GetLLMSettingsByProvider(provider)
		if err != nil {
			http.Error(w, fmt.Sprintf("Provider settings not found: %s", *req.Provider), http.StatusNotFound)
			return
		}

		// Build updates for this provider's row
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
				http.Error(w, fmt.Sprintf("Failed to update settings: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// Mark this provider as active
		if err := database.SetActiveLLMProvider(provider); err != nil {
			http.Error(w, fmt.Sprintf("Failed to set active provider: %v", err), http.StatusInternalServerError)
			return
		}

		// Return the updated provider's settings
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
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	json.NewEncoder(w).Encode(response)
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

	// Notify agent worker of proxy config change
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		if err := h.agentWSHandler.BroadcastProxyConfig(settings); err != nil {
			log.Printf("Warning: failed to broadcast proxy config to agent worker: %v", err)
		}
	}

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
	json.NewEncoder(w).Encode(settings)
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
	json.NewEncoder(w).Encode(settings)
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
		json.NewEncoder(w).Encode(files)

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
		json.NewEncoder(w).Encode(contextFile)

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
		json.NewEncoder(w).Encode(file)

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
	json.NewEncoder(w).Encode(response)
}

// updateIncidentProgress streams progress updates to Slack thread (if enabled)
func (h *APIHandler) updateIncidentProgress(incidentUUID, progressLog string) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	incident, err := h.skillService.GetIncident(incidentUUID)
	if err != nil {
		log.Printf("Warning: Failed to get incident %s for progress update: %v", incidentUUID, err)
		return
	}

	if incident.Source != "slack" {
		return
	}

	channel, ok := incident.Context["channel"].(string)
	if !ok || channel == "" {
		log.Printf("Warning: No channel found in incident context for %s", incidentUUID)
		return
	}

	threadTS := incident.SourceID

	_, _, err = slackClient.PostMessage(
		channel,
		slack.MsgOptionText(fmt.Sprintf("🔄 *Progress:*\n```\n%s\n```", progressLog), false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("Warning: Failed to post progress to Slack: %v", err)
	}
}

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
	json.NewEncoder(w).Encode(sourceTypes)
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
		json.NewEncoder(w).Encode(instances)

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
		json.NewEncoder(w).Encode(instance)
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
		json.NewEncoder(w).Encode(instance)

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
		json.NewEncoder(w).Encode(instance)
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
	json.NewEncoder(w).Encode(alerts)
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
	json.NewEncoder(w).Encode(alert)
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "Incidents merged successfully",
		"target_incident": targetIncident,
		"alerts_moved":    sourceIncident.AlertCount,
	})
}
