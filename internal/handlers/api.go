package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
)

// APIHandler handles API endpoints for the UI and skill communication
type APIHandler struct {
	skillService   *services.SkillService
	toolService    *services.ToolService
	contextService *services.ContextService
	codexExecutor  *executor.Executor
	slackManager   *slackutil.Manager
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(skillService *services.SkillService, toolService *services.ToolService, contextService *services.ContextService, codexExecutor *executor.Executor, slackManager *slackutil.Manager) *APIHandler {
	return &APIHandler{
		skillService:   skillService,
		toolService:    toolService,
		contextService: contextService,
		codexExecutor:  codexExecutor,
		slackManager:   slackManager,
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

	// Slack settings
	mux.HandleFunc("/api/settings/slack", h.handleSlackSettings)

	// Zabbix settings
	mux.HandleFunc("/api/settings/zabbix", h.handleZabbixSettings)

	// OpenAI settings
	mux.HandleFunc("/api/settings/openai", h.handleOpenAISettings)

	// Context files
	mux.HandleFunc("/api/context", h.handleContext)
	mux.HandleFunc("/api/context/", h.handleContextByID)
	mux.HandleFunc("/api/context/validate", h.handleContextValidate)
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
func (h *APIHandler) handleToolByID(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	idStr := r.URL.Path[len("/api/tools/"):]
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid tool ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		instance, err := h.toolService.GetToolInstance(uint(id))
		if err != nil {
			http.Error(w, "Tool not found", http.StatusNotFound)
			return
		}
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
		if err := db.Order("created_at DESC").Limit(100).Find(&incidents).Error; err != nil {
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

			// Use background context since r.Context() may be cancelled
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
			defer cancel()

			progressCallback := func(progressLog string) {
				h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+progressLog)
			}

			taskWithGuidance := executor.PrependGuidance(req.Task)

			// Execute Codex - it handles skill invocation natively
			result, err := h.codexExecutor.ExecuteInDirectory(ctx, taskWithGuidance, "", workingDir, progressCallback)

			fullLogWithContext := taskHeader + result.FullLog

			if err != nil {
				log.Printf("Incident %s failed: %v", incidentUUID, err)
				h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, result.SessionID, fullLogWithContext+"\n\nError: "+err.Error(), "Error: "+err.Error())
				return
			}

			log.Printf("Incident %s completed. Output: %d bytes, Tokens: %d, Session: %s",
				incidentUUID, len(result.Output), result.TokensUsed, result.SessionID)
			h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, result.SessionID, fullLogWithContext, result.Output)
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

// handleZabbixSettings handles GET /api/settings/zabbix and PUT /api/settings/zabbix
func (h *APIHandler) handleZabbixSettings(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var settings database.ZabbixSettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
			return
		}
		response := map[string]interface{}{
			"id":             settings.ID,
			"webhook_secret": maskToken(settings.WebhookSecret),
			"enabled":        settings.Enabled,
			"is_configured":  settings.IsConfigured(),
			"created_at":     settings.CreatedAt,
			"updated_at":     settings.UpdatedAt,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	case http.MethodPut:
		var req struct {
			WebhookSecret *string `json:"webhook_secret"`
			Enabled       *bool   `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		var settings database.ZabbixSettings
		if err := db.First(&settings).Error; err != nil {
			http.Error(w, "Settings not found", http.StatusNotFound)
			return
		}

		updates := make(map[string]interface{})
		if req.WebhookSecret != nil {
			updates["webhook_secret"] = *req.WebhookSecret
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		if err := db.Model(&settings).Updates(updates).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to update settings: %v", err), http.StatusInternalServerError)
			return
		}

		db.First(&settings)
		response := map[string]interface{}{
			"id":             settings.ID,
			"webhook_secret": maskToken(settings.WebhookSecret),
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
		response := map[string]interface{}{
			"id":                      settings.ID,
			"api_key":                 maskToken(settings.APIKey),
			"model":                   settings.Model,
			"model_reasoning_effort":  settings.ModelReasoningEffort,
			"is_configured":           settings.IsConfigured(),
			"valid_reasoning_efforts": settings.GetValidReasoningEfforts(),
			"available_models":        ModelConfigs,
			"created_at":              settings.CreatedAt,
			"updated_at":              settings.UpdatedAt,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	case http.MethodPut:
		var req struct {
			APIKey               *string `json:"api_key"`
			Model                *string `json:"model"`
			ModelReasoningEffort *string `json:"model_reasoning_effort"`
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

		if err := db.Model(&settings).Updates(updates).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to update settings: %v", err), http.StatusInternalServerError)
			return
		}

		db.First(&settings)
		response := map[string]interface{}{
			"id":                      settings.ID,
			"api_key":                 maskToken(settings.APIKey),
			"model":                   settings.Model,
			"model_reasoning_effort":  settings.ModelReasoningEffort,
			"is_configured":           settings.IsConfigured(),
			"valid_reasoning_efforts": settings.GetValidReasoningEfforts(),
			"available_models":        ModelConfigs,
			"created_at":              settings.CreatedAt,
			"updated_at":              settings.UpdatedAt,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
