package handlers

import (
	"errors"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// connectorPartRequest names a part to move into a connector.
type connectorPartRequest struct {
	Kind string `json:"kind"`
	ID   uint   `json:"id"`
}

func connectorErrStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrConnectorNotFound), errors.Is(err, services.ErrConnectorPartNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrConnectorPartIncompatible):
		return http.StatusBadRequest
	case containsString(err.Error(), "validation failed"):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func respondConnectorError(w http.ResponseWriter, err error, fallback string) {
	status := connectorErrStatus(err)
	if status == http.StatusInternalServerError {
		slog.Error(fallback, "err", err)
		api.RespondError(w, status, fallback)
		return
	}
	api.RespondError(w, status, err.Error())
}

// handleConnectors handles GET /api/connectors.
func (h *APIHandler) handleConnectors(w http.ResponseWriter, r *http.Request) {
	if h.connectorService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Connectors are not configured")
		return
	}
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	list, err := h.connectorService.List(r.Context())
	if err != nil {
		respondConnectorError(w, err, "Failed to list connectors")
		return
	}
	api.RespondJSON(w, http.StatusOK, list)
}

// handleConnectorByUUID handles:
//
//	GET    /api/connectors/catalog
//	GET    /api/connectors/{uuid}
//	PATCH  /api/connectors/{uuid}                 {"name": "..."}
//	DELETE /api/connectors/{uuid}                 (deletes every part)
//	POST   /api/connectors/{uuid}/parts           {"kind": "...", "id": N}  move a part in
//	DELETE /api/connectors/{uuid}/parts/{kind}/{id}                          move a part out
func (h *APIHandler) handleConnectorByUUID(w http.ResponseWriter, r *http.Request) {
	if h.connectorService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Connectors are not configured")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/connectors/"), "/")
	if rest == "lookup" {
		// GET /api/connectors/lookup?kind=tool_instance&id=5 -> {"connector_uuid": "..."}
		id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 32)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid part ID")
			return
		}
		u, err := h.connectorService.LookupByPart(r.URL.Query().Get("kind"), uint(id))
		if err != nil {
			respondConnectorError(w, err, "Failed to look up connector")
			return
		}
		api.RespondJSON(w, http.StatusOK, map[string]string{"connector_uuid": u})
		return
	}
	if rest == "catalog" {
		if r.Method != http.MethodGet {
			api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		cat, err := h.connectorService.Catalog()
		if err != nil {
			respondConnectorError(w, err, "Failed to load connector catalog")
			return
		}
		api.RespondJSON(w, http.StatusOK, cat)
		return
	}
	segs := strings.Split(rest, "/")
	connectorUUID := segs[0]
	if connectorUUID == "" {
		api.RespondError(w, http.StatusNotFound, "Not found")
		return
	}

	if len(segs) >= 2 && segs[1] == "parts" {
		switch {
		case len(segs) == 2 && r.Method == http.MethodPost:
			var req connectorPartRequest
			if err := api.DecodeJSON(r, &req); err != nil {
				api.RespondError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			if err := h.connectorService.Attach(connectorUUID, req.Kind, req.ID); err != nil {
				respondConnectorError(w, err, "Failed to move part")
				return
			}
			h.respondConnector(w, r, connectorUUID, http.StatusOK)
		case len(segs) == 4 && r.Method == http.MethodDelete:
			id, err := strconv.ParseUint(segs[3], 10, 32)
			if err != nil {
				api.RespondError(w, http.StatusBadRequest, "Invalid part ID")
				return
			}
			nc, err := h.connectorService.Detach(connectorUUID, segs[2], uint(id))
			if err != nil {
				respondConnectorError(w, err, "Failed to move part")
				return
			}
			api.RespondJSON(w, http.StatusOK, map[string]string{"connector_uuid": nc.UUID})
		default:
			api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}
	if len(segs) != 1 {
		api.RespondError(w, http.StatusNotFound, "Not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.respondConnector(w, r, connectorUUID, http.StatusOK)
	case http.MethodPatch, http.MethodPut:
		var req struct {
			Name string `json:"name"`
		}
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if _, err := h.connectorService.Rename(connectorUUID, req.Name); err != nil {
			respondConnectorError(w, err, "Failed to rename connector")
			return
		}
		h.respondConnector(w, r, connectorUUID, http.StatusOK)
	case http.MethodDelete:
		d, err := h.connectorService.Get(r.Context(), connectorUUID)
		if err != nil {
			respondConnectorError(w, err, "Failed to delete connector")
			return
		}
		if err := h.connectorService.Delete(r.Context(), connectorUUID); err != nil {
			respondConnectorError(w, err, "Failed to delete connector")
			return
		}
		// Same follow-ups as deleting each part through its own endpoint.
		for _, in := range d.Parts.Integrations {
			h.afterIntegrationMutation(database.MessagingProvider(in.Provider))
		}
		if len(d.Parts.AlertSources) > 0 {
			h.reloadAlertChannels()
		}
		if d.Parts.MCPServer != nil {
			h.regenerateSkillsForTools()
		}
		api.RespondNoContent(w)
	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *APIHandler) respondConnector(w http.ResponseWriter, r *http.Request, connectorUUID string, status int) {
	d, err := h.connectorService.Get(r.Context(), connectorUUID)
	if err != nil {
		respondConnectorError(w, err, "Failed to load connector")
		return
	}
	api.RespondJSON(w, status, d)
}

// connectorUUIDFromQuery reads ?connector_uuid= (used by clients that can
// not change a create body, e.g. multipart uploads).
func connectorUUIDFromQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("connector_uuid"))
}

// checkConnectorTarget validates an optional target connector before a
// part is created, so a bad UUID fails the request instead of leaving an
// unlinked part. Writes the error response and returns false on failure.
func (h *APIHandler) checkConnectorTarget(w http.ResponseWriter, r *http.Request, connectorUUID string) bool {
	if connectorUUID == "" || h.connectorService == nil {
		return true
	}
	if _, err := h.connectorService.Get(r.Context(), connectorUUID); err != nil {
		respondConnectorError(w, err, "Failed to load connector")
		return false
	}
	return true
}

// linkNewPart attaches a just-created part to its connector. Best-effort:
// on failure the part stays unlinked and the next start reconciles it.
func (h *APIHandler) linkNewPart(kind string, id uint, connectorUUID string) {
	if h.connectorService == nil {
		return
	}
	if _, err := h.connectorService.LinkNew(kind, id, connectorUUID); err != nil {
		slog.Warn("failed to link new part to a connector", "kind", kind, "id", id, "connector", connectorUUID, "err", err)
	}
}

// afterPartDeleted removes connectors left with no parts.
func (h *APIHandler) afterPartDeleted() {
	if h.connectorService != nil {
		h.connectorService.PruneEmpty()
	}
}

// ---------- MCP OAuth ----------

// handleMCPOAuthStart handles POST /api/mcp-servers/{id}/oauth/start.
// Body (optional): {"www_authenticate": "<header from a failed test>"}.
// Returns {"authorize_url": "..."}; the UI opens it in the browser.
func (h *APIHandler) handleMCPOAuthStart(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.mcpOAuth == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "MCP OAuth is not configured")
		return
	}
	var req struct {
		WWWAuthenticate string `json:"www_authenticate"`
	}
	if r.ContentLength != 0 {
		_ = api.DecodeJSON(r, &req)
	}
	authURL, err := h.mcpOAuth.Start(r.Context(), id, req.WWWAuthenticate)
	if err != nil {
		status := mcpErrStatus(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest // discovery/registration failures are the server's answer
		}
		api.RespondError(w, status, err.Error())
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]string{"authorize_url": authURL})
}

// handleMCPOAuthDisconnect handles POST /api/mcp-servers/{id}/oauth/disconnect.
func (h *APIHandler) handleMCPOAuthDisconnect(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.mcpOAuth == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "MCP OAuth is not configured")
		return
	}
	if err := h.mcpOAuth.Disconnect(id); err != nil {
		respondMCPError(w, err, "Failed to disconnect")
		return
	}
	api.RespondNoContent(w)
}

// handleMCPOAuthCallback handles GET /api/mcp-servers/oauth/callback. It is
// a public route (the authorization server redirects the browser here);
// the one-time state ties it to a flow an operator started. It redirects to
// the connector page with ?oauth=connected or ?oauth_error=...
func (h *APIHandler) handleMCPOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.mcpOAuth == nil {
		http.Error(w, "MCP OAuth is not configured", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	if errCode := q.Get("error"); errCode != "" {
		msg := errCode
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		// Burn the flow so the state cannot be replayed.
		id := h.mcpOAuth.Cancel(state)
		h.redirectAfterOAuth(w, r, id, msg)
		return
	}
	if state == "" || q.Get("code") == "" {
		h.redirectAfterOAuth(w, r, 0, "missing code or state")
		return
	}
	id, err := h.mcpOAuth.Callback(r.Context(), state, q.Get("code"))
	if err != nil {
		h.redirectAfterOAuth(w, r, id, err.Error())
		return
	}
	h.redirectAfterOAuth(w, r, id, "")
}

func (h *APIHandler) redirectAfterOAuth(w http.ResponseWriter, r *http.Request, mcpID uint, errMsg string) {
	target := "/connectors"
	if mcpID != 0 {
		if cfg, err := h.mcpServerService.GetMCPServer(mcpID); err == nil && cfg.ConnectorID != nil && database.GetDB() != nil {
			var c database.Connector
			if err := database.GetDB().First(&c, *cfg.ConnectorID).Error; err == nil {
				target = "/connectors/" + url.PathEscape(c.UUID)
			}
		}
	}
	v := url.Values{}
	if errMsg != "" {
		v.Set("oauth_error", errMsg)
	} else {
		v.Set("oauth", "connected")
	}
	target += "?" + v.Encode()
	// A tiny page instead of a bare 302 keeps the target relative to the UI
	// origin even when the API sits behind a different path prefix.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	esc := html.EscapeString(target)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` + esc +
		`"><title>Akmatori</title><p>Returning to Akmatori… <a href="` + esc + `">Continue</a></p>`))
}
