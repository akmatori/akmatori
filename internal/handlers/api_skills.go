package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleSkills handles GET /api/skills and POST /api/skills
func (h *APIHandler) handleSkills(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var skills []database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Find(&skills).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get skills")
			return
		}

		var response []api.SkillResponse
		for _, skill := range skills {
			prompt, _ := h.skillService.GetSkillPrompt(skill.Name)
			response = append(response, api.SkillResponse{
				Skill:  skill,
				Prompt: prompt,
			})
		}

		api.RespondJSON(w, http.StatusOK, response)

	case http.MethodPost:
		var req api.CreateSkillRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		skill, err := h.skillService.CreateSkill(req.Name, req.Description, req.Category, req.Prompt)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to create skill")
			return
		}

		api.RespondJSON(w, http.StatusCreated, skill)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSkillByName handles GET /api/skills/:name, PUT /api/skills/:name, DELETE /api/skills/:name
// Also handles /api/skills/:name/prompt, /api/skills/:name/tools, /api/skills/:name/scripts
func (h *APIHandler) handleSkillByName(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	path := r.URL.Path[len("/api/skills/"):]

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

	skillName := path

	switch r.Method {
	case http.MethodGet:
		var skill database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill).Error; err != nil {
			api.RespondError(w, http.StatusNotFound, "Skill not found")
			return
		}

		prompt, _ := h.skillService.GetSkillPrompt(skill.Name)
		api.RespondJSON(w, http.StatusOK, api.SkillResponse{Skill: skill, Prompt: prompt})

	case http.MethodPut:
		var updates map[string]interface{}
		if err := api.DecodeJSON(r, &updates); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		var skill database.Skill
		if err := db.Where("name = ?", skillName).First(&skill).Error; err != nil {
			api.RespondError(w, http.StatusNotFound, "Skill not found")
			return
		}

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
				api.RespondError(w, http.StatusInternalServerError, "Failed to update skill")
				return
			}
		}

		if prompt, ok := updates["prompt"].(string); ok {
			if err := h.skillService.UpdateSkillPrompt(skillName, prompt); err != nil {
				api.RespondError(w, http.StatusInternalServerError, "Failed to update skill prompt")
				return
			}
		}

		db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill)
		promptText, _ := h.skillService.GetSkillPrompt(skill.Name)
		api.RespondJSON(w, http.StatusOK, api.SkillResponse{Skill: skill, Prompt: promptText})

	case http.MethodDelete:
		if err := h.skillService.DeleteSkill(skillName); err != nil {
			if containsString(err.Error(), "cannot delete system skill") {
				api.RespondError(w, http.StatusForbidden, err.Error())
				return
			}
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete skill")
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSkillPrompt handles GET/PUT /api/skills/:name/prompt
func (h *APIHandler) handleSkillPrompt(w http.ResponseWriter, r *http.Request, skillName string) {
	switch r.Method {
	case http.MethodGet:
		prompt, err := h.skillService.GetSkillPrompt(skillName)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get prompt")
			return
		}
		api.RespondJSON(w, http.StatusOK, map[string]string{"prompt": prompt})

	case http.MethodPut:
		var req api.UpdateSkillPromptRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := h.skillService.UpdateSkillPrompt(skillName, req.Prompt); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update prompt")
			return
		}

		api.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSkillTools handles GET/PUT /api/skills/:name/tools
func (h *APIHandler) handleSkillTools(w http.ResponseWriter, r *http.Request, skillName string) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var skill database.Skill
		if err := db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill).Error; err != nil {
			api.RespondError(w, http.StatusNotFound, "Skill not found")
			return
		}
		api.RespondJSON(w, http.StatusOK, skill.Tools)

	case http.MethodPut:
		var req api.UpdateSkillToolsRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := h.skillService.AssignTools(skillName, req.ToolInstanceIDs); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to assign tools")
			return
		}

		var skill database.Skill
		db.Preload("Tools").Preload("Tools.ToolType").Where("name = ?", skillName).First(&skill)
		api.RespondJSON(w, http.StatusOK, skill)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSkillsSync handles POST /api/skills/sync
func (h *APIHandler) handleSkillsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if err := h.skillService.SyncSkillsFromFilesystem(); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to sync skills")
		return
	}

	api.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "Skills synced from filesystem"})
}

// handleSkillScripts handles GET/DELETE /api/skills/:name/scripts
func (h *APIHandler) handleSkillScripts(w http.ResponseWriter, r *http.Request, skillName string) {
	switch r.Method {
	case http.MethodGet:
		scripts, err := h.skillService.ListSkillScripts(skillName)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list scripts")
			return
		}

		api.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"skill_name":  skillName,
			"scripts_dir": h.skillService.GetSkillScriptsDir(skillName),
			"scripts":     scripts,
		})

	case http.MethodDelete:
		if err := h.skillService.ClearSkillScripts(skillName); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to clear scripts")
			return
		}

		api.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"message":    "Scripts cleared successfully",
			"skill_name": skillName,
		})

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSkillScriptByFilename handles GET/PUT/DELETE /api/skills/:name/scripts/:filename
func (h *APIHandler) handleSkillScriptByFilename(w http.ResponseWriter, r *http.Request, skillName, filename string) {
	switch r.Method {
	case http.MethodGet:
		scriptInfo, err := h.skillService.GetSkillScript(skillName, filename)
		if err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else if containsString(err.Error(), "invalid filename") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to get script")
			}
			return
		}

		api.RespondJSON(w, http.StatusOK, scriptInfo)

	case http.MethodPut:
		var req api.UpdateScriptRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := h.skillService.UpdateSkillScript(skillName, filename, req.Content); err != nil {
			if containsString(err.Error(), "invalid filename") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to update script")
			}
			return
		}

		api.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"filename": filename,
		})

	case http.MethodDelete:
		if err := h.skillService.DeleteSkillScript(skillName, filename); err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else if containsString(err.Error(), "invalid filename") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to delete script")
			}
			return
		}

		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
