package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// CreateHTTPConnectorRequest is the request body for POST /api/http-connectors
type CreateHTTPConnectorRequest struct {
	ToolTypeName string         `json:"tool_type_name"`
	Description  string         `json:"description"`
	BaseURLField string         `json:"base_url_field"`
	AuthConfig   database.JSONB `json:"auth_config"`
	Tools        database.JSONB `json:"tools"`
}

// UpdateHTTPConnectorRequest is the request body for PUT /api/http-connectors/:id
type UpdateHTTPConnectorRequest struct {
	ToolTypeName *string         `json:"tool_type_name"`
	Description  *string         `json:"description"`
	BaseURLField *string         `json:"base_url_field"`
	AuthConfig   *database.JSONB `json:"auth_config"`
	Tools        *database.JSONB `json:"tools"`
	Enabled      *bool           `json:"enabled"`
}

// handleHTTPConnectors handles GET /api/http-connectors and POST /api/http-connectors
func (h *APIHandler) handleHTTPConnectors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		connectors, err := h.httpConnectorService.ListHTTPConnectors()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list HTTP connectors")
			return
		}
		api.RespondJSON(w, http.StatusOK, connectors)

	case http.MethodPost:
		var req CreateHTTPConnectorRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.ToolTypeName == "" {
			api.RespondError(w, http.StatusBadRequest, "tool_type_name is required")
			return
		}
		if req.BaseURLField == "" {
			api.RespondError(w, http.StatusBadRequest, "base_url_field is required")
			return
		}

		connector := &database.HTTPConnector{
			ToolTypeName: req.ToolTypeName,
			Description:  req.Description,
			BaseURLField: req.BaseURLField,
			AuthConfig:   req.AuthConfig,
			Tools:        req.Tools,
		}

		result, err := h.httpConnectorService.CreateHTTPConnector(connector)
		if err != nil {
			if containsString(err.Error(), "already exists") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else if containsString(err.Error(), "validation failed") {
				api.RespondError(w, http.StatusBadRequest, err.Error())
			} else if containsString(err.Error(), "conflicts with") {
				api.RespondError(w, http.StatusConflict, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to create HTTP connector")
			}
			return
		}

		h.triggerGatewayReload()
		api.RespondJSON(w, http.StatusCreated, result)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleHTTPConnectorByID handles GET/PUT/DELETE /api/http-connectors/:id
func (h *APIHandler) handleHTTPConnectorByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/api/http-connectors/"):]
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid connector ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		connector, err := h.httpConnectorService.GetHTTPConnector(uint(id))
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "HTTP connector not found")
			return
		}
		api.RespondJSON(w, http.StatusOK, connector)

	case http.MethodPut:
		var req UpdateHTTPConnectorRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		updates := make(map[string]interface{})
		if req.ToolTypeName != nil {
			updates["tool_type_name"] = *req.ToolTypeName
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.BaseURLField != nil {
			updates["base_url_field"] = *req.BaseURLField
		}
		if req.AuthConfig != nil {
			updates["auth_config"] = database.JSONB(*req.AuthConfig)
		}
		if req.Tools != nil {
			updates["tools"] = database.JSONB(*req.Tools)
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		connector, err := h.httpConnectorService.UpdateHTTPConnector(uint(id), updates)
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
				api.RespondError(w, http.StatusInternalServerError, "Failed to update HTTP connector")
			}
			return
		}

		h.triggerGatewayReload()
		api.RespondJSON(w, http.StatusOK, connector)

	case http.MethodDelete:
		if err := h.httpConnectorService.DeleteHTTPConnector(uint(id)); err != nil {
			if containsString(err.Error(), "not found") {
				api.RespondError(w, http.StatusNotFound, err.Error())
			} else {
				api.RespondError(w, http.StatusInternalServerError, "Failed to delete HTTP connector")
			}
			return
		}

		h.triggerGatewayReload()
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// triggerGatewayReload calls the MCP Gateway reload endpoint asynchronously
func (h *APIHandler) triggerGatewayReload() {
	if h.gatewayReloader != nil {
		go func() {
			if err := h.gatewayReloader(); err != nil {
				slog.Error("failed to trigger gateway reload", "error", err)
			}
		}()
	}
}

// GatewayReloadFunc creates a function that triggers the MCP Gateway HTTP connector reload
func GatewayReloadFunc(gatewayURL string) func() error {
	return func() error {
		resp, err := http.Post(gatewayURL+"/reload/http-connectors", "application/json", nil)
		if err != nil {
			return fmt.Errorf("gateway reload request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("gateway reload returned status %d", resp.StatusCode)
		}
		return nil
	}
}

// GatewayMCPReloadFunc creates a function that triggers the MCP Gateway MCP server proxy reload
func GatewayMCPReloadFunc(gatewayURL string) func() error {
	return func() error {
		resp, err := http.Post(gatewayURL+"/reload/mcp-servers", "application/json", nil)
		if err != nil {
			return fmt.Errorf("gateway MCP reload request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("gateway MCP reload returned status %d", resp.StatusCode)
		}
		return nil
	}
}
