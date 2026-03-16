package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// CreateMCPServerRequest is the request body for POST /api/mcp-servers
type CreateMCPServerRequest struct {
	Name            string                     `json:"name"`
	Transport       database.MCPServerTransport `json:"transport"`
	URL             string                     `json:"url,omitempty"`
	Command         string                     `json:"command,omitempty"`
	Args            database.JSONB             `json:"args,omitempty"`
	EnvVars         database.JSONB             `json:"env_vars,omitempty"`
	NamespacePrefix string                     `json:"namespace_prefix"`
	AuthConfig      database.JSONB             `json:"auth_config,omitempty"`
}

// UpdateMCPServerRequest is the request body for PUT /api/mcp-servers/:id
type UpdateMCPServerRequest struct {
	Name            *string         `json:"name"`
	Transport       *string         `json:"transport"`
	URL             *string         `json:"url"`
	Command         *string         `json:"command"`
	Args            *database.JSONB `json:"args"`
	EnvVars         *database.JSONB `json:"env_vars"`
	NamespacePrefix *string         `json:"namespace_prefix"`
	AuthConfig      *database.JSONB `json:"auth_config"`
	Enabled         *bool           `json:"enabled"`
}

// handleMCPServers handles GET /api/mcp-servers and POST /api/mcp-servers
func (h *APIHandler) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		configs, err := h.mcpServerService.ListMCPServers()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list MCP servers")
			return
		}
		api.RespondJSON(w, http.StatusOK, configs)

	case http.MethodPost:
		var req CreateMCPServerRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Name == "" {
			api.RespondError(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.NamespacePrefix == "" {
			api.RespondError(w, http.StatusBadRequest, "namespace_prefix is required")
			return
		}

		config := &database.MCPServerConfig{
			Name:            req.Name,
			Transport:       req.Transport,
			URL:             req.URL,
			Command:         req.Command,
			Args:            req.Args,
			EnvVars:         req.EnvVars,
			NamespacePrefix: req.NamespacePrefix,
			AuthConfig:      req.AuthConfig,
		}

		result, err := h.mcpServerService.CreateMCPServer(config)
		if err != nil {
			if containsString(err.Error(), "already exists") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else if containsString(err.Error(), "validation failed") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else if containsString(err.Error(), "conflicts with") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to create MCP server")
			}
			return
		}

		h.triggerGatewayMCPReload()
		api.RespondJSON(w, http.StatusCreated, result)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleMCPServerByID handles GET/PUT/DELETE /api/mcp-servers/:id
func (h *APIHandler) handleMCPServerByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/api/mcp-servers/"):]
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid server ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		config, err := h.mcpServerService.GetMCPServer(uint(id))
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "MCP server not found")
			return
		}
		api.RespondJSON(w, http.StatusOK, config)

	case http.MethodPut:
		var req UpdateMCPServerRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		updates := make(map[string]interface{})
		if req.Name != nil {
			updates["name"] = *req.Name
		}
		if req.Transport != nil {
			updates["transport"] = *req.Transport
		}
		if req.URL != nil {
			updates["url"] = *req.URL
		}
		if req.Command != nil {
			updates["command"] = *req.Command
		}
		if req.Args != nil {
			updates["args"] = database.JSONB(*req.Args)
		}
		if req.EnvVars != nil {
			updates["env_vars"] = database.JSONB(*req.EnvVars)
		}
		if req.NamespacePrefix != nil {
			updates["namespace_prefix"] = *req.NamespacePrefix
		}
		if req.AuthConfig != nil {
			updates["auth_config"] = database.JSONB(*req.AuthConfig)
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		config, err := h.mcpServerService.UpdateMCPServer(uint(id), updates)
		if err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else if containsString(err.Error(), "already exists") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else if containsString(err.Error(), "validation failed") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else if containsString(err.Error(), "conflicts with") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to update MCP server")
			}
			return
		}

		h.triggerGatewayMCPReload()
		api.RespondJSON(w, http.StatusOK, config)

	case http.MethodDelete:
		if err := h.mcpServerService.DeleteMCPServer(uint(id)); err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to delete MCP server")
			}
			return
		}

		h.triggerGatewayMCPReload()
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// triggerGatewayMCPReload calls the MCP Gateway reload endpoint for MCP proxy configs
func (h *APIHandler) triggerGatewayMCPReload() {
	if h.mcpServerReloader != nil {
		go func() {
			if err := h.mcpServerReloader(); err != nil {
				slog.Error("failed to trigger gateway MCP reload", "error", err)
			}
		}()
	}
}
