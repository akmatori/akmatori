package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleToolTypes handles GET /api/tool-types
func (h *APIHandler) handleToolTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	toolTypes, err := h.toolService.ListToolTypes()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get tool types")
		return
	}

	api.RespondJSON(w, http.StatusOK, toolTypes)
}

// handleTools handles GET /api/tools and POST /api/tools
func (h *APIHandler) handleTools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		instances, err := h.toolService.ListToolInstances()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get tools")
			return
		}
		api.RespondJSON(w, http.StatusOK, instances)

	case http.MethodPost:
		var req api.CreateToolInstanceRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		instance, err := h.toolService.CreateToolInstance(req.ToolTypeID, req.Name, req.Settings)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to create tool instance")
			return
		}

		api.RespondJSON(w, http.StatusCreated, instance)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleToolByID handles GET /api/tools/:id, PUT /api/tools/:id, DELETE /api/tools/:id
// Also handles /api/tools/:id/ssh-keys routes
func (h *APIHandler) handleToolByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/tools/"):]
	parts := strings.Split(path, "/")

	id, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid tool ID")
		return
	}

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
			api.RespondError(w, http.StatusNotFound, "Tool not found")
			return
		}

		h.maskSSHKeys(instance)
		api.RespondJSON(w, http.StatusOK, instance)

	case http.MethodPut:
		var req api.UpdateToolInstanceRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := h.toolService.UpdateToolInstance(uint(id), req.Name, req.Settings, req.Enabled); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update tool")
			return
		}

		instance, _ := h.toolService.GetToolInstance(uint(id))
		h.maskSSHKeys(instance)
		api.RespondJSON(w, http.StatusOK, instance)

	case http.MethodDelete:
		if err := h.toolService.DeleteToolInstance(uint(id)); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete tool")
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
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
			api.RespondError(w, http.StatusInternalServerError, "Failed to get SSH keys")
			return
		}
		api.RespondJSON(w, http.StatusOK, keys)

	case http.MethodPost:
		var req api.CreateSSHKeyRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Name == "" {
			api.RespondError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.PrivateKey == "" {
			api.RespondError(w, http.StatusBadRequest, "private_key is required")
			return
		}

		key, err := h.toolService.AddSSHKey(toolID, req.Name, req.PrivateKey, req.IsDefault)
		if err != nil {
			if containsString(err.Error(), "already exists") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to add SSH key")
			}
			return
		}

		api.RespondJSON(w, http.StatusCreated, key)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleSSHKeyByID handles PUT/DELETE /api/tools/:id/ssh-keys/:keyID
func (h *APIHandler) handleSSHKeyByID(w http.ResponseWriter, r *http.Request, toolID uint, keyID string) {
	switch r.Method {
	case http.MethodPut:
		var req api.UpdateSSHKeyRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		key, err := h.toolService.UpdateSSHKey(toolID, keyID, req.Name, req.IsDefault)
		if err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else if containsString(err.Error(), "already exists") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to update SSH key")
			}
			return
		}

		api.RespondJSON(w, http.StatusOK, key)

	case http.MethodDelete:
		if err := h.toolService.DeleteSSHKey(toolID, keyID); err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else if containsString(err.Error(), "cannot delete") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to delete SSH key")
			}
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
