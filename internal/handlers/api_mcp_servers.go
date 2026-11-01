package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// CreateMCPServerRequest is the request body for POST /api/mcp-servers.
// Only remote servers are supported: command/args/env_vars are rejected.
// namespace_prefix is optional (derived from the name).
type CreateMCPServerRequest struct {
	Name            string                      `json:"name"`
	Transport       database.MCPServerTransport `json:"transport,omitempty"`
	URL             string                      `json:"url"`
	Command         string                      `json:"command,omitempty"`
	Args            database.JSONB              `json:"args,omitempty"`
	EnvVars         database.JSONB              `json:"env_vars,omitempty"`
	NamespacePrefix string                      `json:"namespace_prefix,omitempty"`
	AuthConfig      database.JSONB              `json:"auth_config,omitempty"`
	DisabledTools   []string                    `json:"disabled_tools,omitempty"`
	// ConnectorUUID joins an existing custom MCP connector; empty creates one.
	ConnectorUUID string `json:"connector_uuid,omitempty"`
}

// UpdateMCPServerRequest is the request body for PUT /api/mcp-servers/:id.
// auth_config: absent = unchanged; null = remove auth; object = replace,
// except that a missing, empty or masked secret keeps the stored secret
// when the auth type does not change.
type UpdateMCPServerRequest struct {
	Name            *string         `json:"name"`
	Transport       *string         `json:"transport"`
	URL             *string         `json:"url"`
	Command         *string         `json:"command"`
	Args            json.RawMessage `json:"args"`
	EnvVars         json.RawMessage `json:"env_vars"`
	NamespacePrefix *string         `json:"namespace_prefix"`
	AuthConfig      json.RawMessage `json:"auth_config"`
	DisabledTools   *[]string       `json:"disabled_tools"`
	Enabled         *bool           `json:"enabled"`
}

// mcpAuthSummary describes the auth of a server without its secrets.
type mcpAuthSummary struct {
	Type       string `json:"type"`
	HeaderName string `json:"header_name,omitempty"`
	HasSecret  bool   `json:"has_secret"`
	// OAuth only
	OAuthConnected bool       `json:"oauth_connected,omitempty"`
	OAuthExpiresAt *time.Time `json:"oauth_expires_at,omitempty"`
	OAuthScope     string     `json:"oauth_scope,omitempty"`
}

// mcpServerResponse is the redacted API view of an MCP server config.
type mcpServerResponse struct {
	ID              uint                        `json:"id"`
	Name            string                      `json:"name"`
	Transport       database.MCPServerTransport `json:"transport"`
	URL             string                      `json:"url,omitempty"`
	NamespacePrefix string                      `json:"namespace_prefix"`
	// AuthConfig keeps the stored shape with every secret replaced by
	// services.MCPMaskedSecret, so existing clients can round-trip it.
	AuthConfig    map[string]interface{} `json:"auth_config,omitempty"`
	Auth          mcpAuthSummary         `json:"auth"`
	DisabledTools []string               `json:"disabled_tools"`
	Enabled       bool                   `json:"enabled"`
	// Unsupported is set for legacy stdio rows the gateway no longer runs.
	Unsupported   string    `json:"unsupported,omitempty"`
	ConnectorID   *uint     `json:"connector_id,omitempty"`
	ConnectorUUID string    `json:"connector_uuid,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toMCPServerResponse(c *database.MCPServerConfig) mcpServerResponse {
	resp := mcpServerResponse{
		ID: c.ID, Name: c.Name, Transport: c.Transport, URL: c.URL, NamespacePrefix: c.NamespacePrefix,
		DisabledTools: []string(c.DisabledTools), Enabled: c.Enabled, ConnectorID: c.ConnectorID,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if resp.DisabledTools == nil {
		resp.DisabledTools = []string{}
	}
	if c.Transport == database.MCPServerTransportStdio {
		resp.Unsupported = "stdio MCP servers are no longer supported; replace it with a remote MCP server URL or delete it"
	}
	resp.Auth = summarizeMCPAuth(c.AuthConfig)
	if len(c.AuthConfig) > 0 {
		resp.AuthConfig = map[string]interface{}{}
		for k, v := range c.AuthConfig {
			if services.IsMCPSecretField(k) {
				if s, _ := v.(string); s != "" {
					resp.AuthConfig[k] = services.MCPMaskedSecret
				}
				continue
			}
			resp.AuthConfig[k] = v
		}
	}
	return resp
}

func summarizeMCPAuth(cfg database.JSONB) mcpAuthSummary {
	sum := mcpAuthSummary{Type: "none"}
	if len(cfg) == 0 {
		return sum
	}
	if t, _ := cfg["type"].(string); t != "" {
		sum.Type = t
	}
	sum.HeaderName, _ = cfg["header_name"].(string)
	for _, f := range []string{"token", "value", "access_token"} {
		if v, _ := cfg[f].(string); v != "" {
			sum.HasSecret = true
		}
	}
	if sum.Type == "oauth" {
		tok, _ := cfg["access_token"].(string)
		sum.OAuthConnected = tok != ""
		sum.OAuthScope, _ = cfg["scope"].(string)
		if exp, _ := cfg["expires_at"].(string); exp != "" {
			if t, err := time.Parse(time.RFC3339, exp); err == nil {
				sum.OAuthExpiresAt = &t
			}
		}
	}
	return sum
}

// rawPresent reports whether a JSON field was sent with a non-null value.
func rawPresent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && !bytes.Equal(t, []byte("null"))
}

// mcpErrStatus maps service errors to HTTP status codes.
func mcpErrStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrMCPServerNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrMCPAuthStale):
		return http.StatusConflict
	case errors.Is(err, services.ErrConnectorNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrConnectorPartIncompatible):
		return http.StatusBadRequest
	case errors.Is(err, services.ErrMCPServerConflict), containsString(err.Error(), "already exists"), containsString(err.Error(), "conflicts with"):
		return http.StatusConflict
	case containsString(err.Error(), "validation failed"):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func respondMCPError(w http.ResponseWriter, err error, fallback string) {
	status := mcpErrStatus(err)
	if status == http.StatusInternalServerError {
		slog.Error(fallback, "err", err)
		api.RespondError(w, status, fallback)
		return
	}
	api.RespondError(w, status, err.Error())
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
		out := make([]mcpServerResponse, len(configs))
		for i := range configs {
			out[i] = toMCPServerResponse(&configs[i])
		}
		h.addConnectorUUIDs(out)
		api.RespondJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var req CreateMCPServerRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			api.RespondError(w, http.StatusBadRequest, "name is required")
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
			DisabledTools:   database.StringList(req.DisabledTools),
		}
		if req.ConnectorUUID != "" && h.connectorService != nil {
			// Check the target before creating anything.
			target, err := h.connectorService.Get(r.Context(), req.ConnectorUUID)
			if err != nil {
				respondMCPError(w, err, "Failed to create MCP server")
				return
			}
			if target.Parts.MCPServer != nil {
				api.RespondError(w, http.StatusBadRequest, services.ErrConnectorHasMCPServer.Error())
				return
			}
		}
		result, err := h.mcpServerService.CreateMCPServer(config)
		if err != nil {
			respondMCPError(w, err, "Failed to create MCP server")
			return
		}
		h.linkNewPart(services.PartMCPServer, result.ID, req.ConnectorUUID)
		if fresh, err := h.mcpServerService.GetMCPServer(result.ID); err == nil && fresh != nil {
			result = fresh
		}
		h.triggerGatewayMCPReload()
		out := []mcpServerResponse{toMCPServerResponse(result)}
		h.addConnectorUUIDs(out)
		api.RespondJSON(w, http.StatusCreated, out[0])

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleMCPServerByID routes /api/mcp-servers/{id}[/...] and the
// non-numeric /api/mcp-servers/test, /status and /oauth/callback paths.
func (h *APIHandler) handleMCPServerByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(r.URL.Path[len("/api/mcp-servers/"):], "/")
	switch rest {
	case "test":
		h.handleMCPServerTest(w, r, 0)
		return
	case "status":
		h.handleMCPServerStatus(w, r)
		return
	case "oauth/callback":
		h.handleMCPOAuthCallback(w, r)
		return
	}
	segs := strings.Split(rest, "/")
	id64, err := strconv.ParseUint(segs[0], 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid server ID")
		return
	}
	id := uint(id64)
	if len(segs) > 1 {
		switch strings.Join(segs[1:], "/") {
		case "test":
			h.handleMCPServerTest(w, r, id)
		case "oauth/start":
			h.handleMCPOAuthStart(w, r, id)
		case "oauth/disconnect":
			h.handleMCPOAuthDisconnect(w, r, id)
		default:
			api.RespondError(w, http.StatusNotFound, "Not found")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		config, err := h.mcpServerService.GetMCPServer(id)
		if err != nil {
			respondMCPError(w, err, "Failed to load MCP server")
			return
		}
		out := []mcpServerResponse{toMCPServerResponse(config)}
		h.addConnectorUUIDs(out)
		api.RespondJSON(w, http.StatusOK, out[0])

	case http.MethodPut:
		var req UpdateMCPServerRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if (req.Command != nil && *req.Command != "") || rawPresent(req.Args) || rawPresent(req.EnvVars) {
			api.RespondError(w, http.StatusBadRequest, "validation failed: command, args and env_vars are not supported; use a remote MCP server URL")
			return
		}
		patch := services.MCPServerPatch{
			Name: req.Name, Transport: req.Transport, URL: req.URL, NamespacePrefix: req.NamespacePrefix,
			DisabledTools: req.DisabledTools, Enabled: req.Enabled,
		}
		if len(bytes.TrimSpace(req.AuthConfig)) > 0 {
			if bytes.Equal(bytes.TrimSpace(req.AuthConfig), []byte("null")) {
				patch.Auth = &services.MCPAuthPatch{Clear: true}
			} else {
				var v database.JSONB
				if err := json.Unmarshal(req.AuthConfig, &v); err != nil {
					api.RespondError(w, http.StatusBadRequest, "auth_config must be an object or null")
					return
				}
				patch.Auth = &services.MCPAuthPatch{Value: v}
			}
		}
		config, err := h.mcpServerService.UpdateMCPServer(id, patch)
		if err != nil {
			respondMCPError(w, err, "Failed to update MCP server")
			return
		}
		h.triggerGatewayMCPReload()
		h.regenerateSkillsForTools()
		out := []mcpServerResponse{toMCPServerResponse(config)}
		h.addConnectorUUIDs(out)
		api.RespondJSON(w, http.StatusOK, out[0])

	case http.MethodDelete:
		if err := h.mcpServerService.DeleteMCPServer(id); err != nil {
			respondMCPError(w, err, "Failed to delete MCP server")
			return
		}
		h.afterPartDeleted()
		h.triggerGatewayMCPReload()
		h.regenerateSkillsForTools()
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleMCPServerTest handles POST /api/mcp-servers/test (unsaved settings)
// and POST /api/mcp-servers/{id}/test (stored server, optional overrides).
func (h *APIHandler) handleMCPServerTest(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.mcpGateway == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "MCP gateway is not configured")
		return
	}
	var req struct {
		URL        string          `json:"url"`
		Transport  string          `json:"transport"`
		AuthConfig json.RawMessage `json:"auth_config"`
	}
	if r.ContentLength != 0 {
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
	}
	testReq := services.MCPTestRequest{ID: id, URL: strings.TrimSpace(req.URL), Transport: req.Transport}
	if testReq.Transport == "" {
		testReq.Transport = string(database.MCPServerTransportHTTP)
	}
	if rawPresent(req.AuthConfig) {
		var v database.JSONB
		if err := json.Unmarshal(req.AuthConfig, &v); err != nil {
			api.RespondError(w, http.StatusBadRequest, "auth_config must be an object")
			return
		}
		if id != 0 {
			// Masked or omitted secrets mean "use the stored one".
			stored, err := h.mcpServerService.GetMCPServer(id)
			if err != nil {
				respondMCPError(w, err, "Failed to load MCP server")
				return
			}
			v = services.MergeMCPAuthForTest(stored.AuthConfig, v)
		}
		if err := services.ValidateMCPAuth(v); err != nil {
			api.RespondError(w, http.StatusBadRequest, "validation failed: "+err.Error())
			return
		}
		testReq.AuthConfig = v
	}
	if id == 0 {
		probe := database.MCPServerConfig{Name: "test", NamespacePrefix: "ext.test", Transport: database.MCPServerTransport(testReq.Transport), URL: testReq.URL}
		if err := probe.Validate(); err != nil {
			api.RespondError(w, http.StatusBadRequest, "validation failed: "+err.Error())
			return
		}
	} else if _, err := h.mcpServerService.GetMCPServer(id); err != nil {
		respondMCPError(w, err, "Failed to load MCP server")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := h.mcpGateway.Test(ctx, testReq)
	if err != nil {
		api.RespondError(w, http.StatusBadGateway, err.Error())
		return
	}
	api.RespondJSON(w, http.StatusOK, res)
}

// handleMCPServerStatus handles GET /api/mcp-servers/status.
func (h *APIHandler) handleMCPServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.mcpGateway == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "MCP gateway is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	st, err := h.mcpGateway.Status(ctx)
	if err != nil {
		api.RespondError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]services.MCPServerRuntimeStatus, 0, len(st))
	for _, s := range st {
		out = append(out, s)
	}
	api.RespondJSON(w, http.StatusOK, map[string]interface{}{"servers": out})
}

// addConnectorUUIDs fills ConnectorUUID from ConnectorID.
func (h *APIHandler) addConnectorUUIDs(rows []mcpServerResponse) {
	ids := []uint{}
	for _, r := range rows {
		if r.ConnectorID != nil {
			ids = append(ids, *r.ConnectorID)
		}
	}
	if len(ids) == 0 || database.GetDB() == nil {
		return
	}
	var cs []database.Connector
	if err := database.GetDB().Where("id IN ?", ids).Find(&cs).Error; err != nil {
		return
	}
	byID := map[uint]string{}
	for _, c := range cs {
		byID[c.ID] = c.UUID
	}
	for i := range rows {
		if rows[i].ConnectorID != nil {
			rows[i].ConnectorUUID = byID[*rows[i].ConnectorID]
		}
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

// regenerateSkillsForTools rewrites every SKILL.md so tool names and types
// in "Assigned Tools" follow a renamed or deleted MCP server.
func (h *APIHandler) regenerateSkillsForTools() {
	regen, ok := h.skillService.(interface{ RegenerateAllSkillMds() error })
	if !ok {
		return
	}
	go func() {
		if err := regen.RegenerateAllSkillMds(); err != nil {
			slog.Warn("failed to regenerate SKILL.md files after MCP change", "err", err)
		}
	}()
}
