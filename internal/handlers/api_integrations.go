package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// integrationCredentialSecretSubstrings lists credential key substrings that
// indicate the value carries a secret and must be masked before being returned
// over the API. Operator inputs (tokens, secrets) are accepted on write but
// never echoed back in plaintext — the same posture the retired
// /api/settings/slack endpoint maintained.
//
// Substring matching (case-insensitive) lets any future provider whose secret
// key contains "token"/"secret"/"password"/"key" be masked automatically
// without having to enumerate every provider-specific name here. An explicit
// allow-list (below) skips known-non-secret fields that happen to match a
// substring (e.g. "key_id" labels, not the key itself).
var integrationCredentialSecretSubstrings = []string{
	"token",
	"secret",
	"password",
	"passwd",
	"apikey",
	"api_key",
	"webhook",
	"private",
	"credential",
}

// integrationCredentialPlaintextKeys names credential keys that look secret-ish
// (matching a substring above) but are intentionally returned in plaintext —
// either because they are identifiers, not secrets, or because the UI relies
// on rendering them.
var integrationCredentialPlaintextKeys = map[string]struct{}{
	"is_configured": {},
}

// integrationResponse is the API-facing shape of an Integration row. It
// mirrors the GORM model fields but replaces Credentials with masked values
// so secrets do not leak to authenticated callers via GET responses.
type integrationResponse struct {
	ID          uint                       `json:"id"`
	UUID        string                     `json:"uuid"`
	Provider    database.MessagingProvider `json:"provider"`
	Name        string                     `json:"name"`
	Credentials map[string]interface{}     `json:"credentials"`
	Enabled     bool                       `json:"enabled"`
	CreatedAt   interface{}                `json:"created_at"`
	UpdatedAt   interface{}                `json:"updated_at"`
}

// toIntegrationResponse returns a redacted view of the supplied integration
// suitable for API responses. Credential values whose key matches
// shouldMaskCredentialField are replaced with maskToken so that the UI can
// render "configured: yes" without ever exposing the plaintext secret.
func toIntegrationResponse(row *database.Integration) integrationResponse {
	resp := integrationResponse{
		ID:          row.ID,
		UUID:        row.UUID,
		Provider:    row.Provider,
		Name:        row.Name,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Credentials: map[string]interface{}{},
	}
	for k, v := range row.Credentials {
		if shouldMaskCredentialField(k) {
			str, _ := v.(string)
			resp.Credentials[k] = maskToken(str)
			continue
		}
		resp.Credentials[k] = v
	}
	return resp
}

// shouldMaskCredentialField reports whether the given credential key carries a
// secret that must be masked before going on the wire. Match is
// case-insensitive substring against integrationCredentialSecretSubstrings, with
// an explicit allow-list of look-alike-but-not-secret keys.
func shouldMaskCredentialField(key string) bool {
	if _, ok := integrationCredentialPlaintextKeys[key]; ok {
		return false
	}
	lower := strings.ToLower(key)
	for _, fragment := range integrationCredentialSecretSubstrings {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// toIntegrationResponses applies toIntegrationResponse across a slice.
func toIntegrationResponses(rows []database.Integration) []integrationResponse {
	out := make([]integrationResponse, len(rows))
	for i := range rows {
		out[i] = toIntegrationResponse(&rows[i])
	}
	return out
}

// CreateIntegrationRequest is the request body for POST /api/integrations.
// Credentials are stored verbatim as JSONB; their shape is provider-specific.
type CreateIntegrationRequest struct {
	Provider    string         `json:"provider"`
	Name        string         `json:"name"`
	Credentials database.JSONB `json:"credentials,omitempty"`
	Enabled     *bool          `json:"enabled,omitempty"`
}

// UpdateIntegrationRequest is the request body for PUT /api/integrations/{uuid}.
// Provider is intentionally immutable on update — operators must delete and
// re-create when switching backends so credential shape stays consistent.
type UpdateIntegrationRequest struct {
	Name        *string         `json:"name,omitempty"`
	Credentials *database.JSONB `json:"credentials,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
}

// handleIntegrations dispatches GET /api/integrations and POST /api/integrations.
func (h *APIHandler) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	if h.channelService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Channel service is not configured")
		return
	}

	switch r.Method {
	case http.MethodGet:
		rows, err := h.channelService.ListIntegrations()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list integrations")
			return
		}
		api.RespondJSON(w, http.StatusOK, toIntegrationResponses(rows))

	case http.MethodPost:
		var req CreateIntegrationRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		provider := strings.TrimSpace(req.Provider)
		if provider == "" {
			api.RespondError(w, http.StatusBadRequest, "provider is required")
			return
		}
		if !h.isProviderKnown(database.MessagingProvider(provider)) {
			api.RespondError(w, http.StatusBadRequest, "provider is not a known messaging provider")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			api.RespondError(w, http.StatusBadRequest, "name is required")
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		row, err := h.channelService.CreateIntegration(database.MessagingProvider(provider), req.Name, req.Credentials, enabled)
		if err != nil {
			api.RespondError(w, integrationErrStatus(err), err.Error())
			return
		}
		// Credentials and the enabled flag on an Integration drive whether the
		// Slack manager connects and which listener channels are active. Fire
		// both reload paths so a freshly-added Slack integration takes effect
		// without restarting the API.
		h.afterIntegrationMutation(row.Provider)
		api.RespondJSON(w, http.StatusCreated, toIntegrationResponse(row))

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleIntegrationByUUID dispatches GET/PUT/DELETE /api/integrations/{uuid}.
func (h *APIHandler) handleIntegrationByUUID(w http.ResponseWriter, r *http.Request) {
	if h.channelService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Channel service is not configured")
		return
	}

	uuid := strings.TrimPrefix(r.URL.Path, "/api/integrations/")
	if uuid == "" || strings.Contains(uuid, "/") {
		api.RespondError(w, http.StatusBadRequest, "Invalid integration UUID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		row, err := h.channelService.GetIntegrationByUUID(uuid)
		if err != nil {
			api.RespondError(w, integrationErrStatus(err), err.Error())
			return
		}
		api.RespondJSON(w, http.StatusOK, toIntegrationResponse(row))

	case http.MethodPut:
		var req UpdateIntegrationRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		var creds database.JSONB
		if req.Credentials != nil {
			creds = database.JSONB(*req.Credentials)
		}
		row, err := h.channelService.UpdateIntegration(uuid, req.Name, creds, req.Enabled)
		if err != nil {
			api.RespondError(w, integrationErrStatus(err), err.Error())
			return
		}
		// Credential rotations and enabled toggles affect the live Slack
		// connection and which listener channels are loaded; trigger both
		// reload paths so the change is observable without restart.
		h.afterIntegrationMutation(row.Provider)
		api.RespondJSON(w, http.StatusOK, toIntegrationResponse(row))

	case http.MethodDelete:
		// Capture the provider before delete so we can decide whether the
		// Slack manager needs a reload after the row is gone.
		row, lookupErr := h.channelService.GetIntegrationByUUID(uuid)
		if err := h.channelService.DeleteIntegration(uuid); err != nil {
			api.RespondError(w, integrationErrStatus(err), err.Error())
			return
		}
		var provider database.MessagingProvider
		if lookupErr == nil && row != nil {
			provider = row.Provider
		}
		h.afterIntegrationMutation(provider)
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// afterIntegrationMutation fans the post-CRUD signals out so the runtime
// picks up credential, enabled, and existence changes without an API restart.
// Listener channels filter by Integration.Enabled in LoadListenerChannels so
// any provider's CRUD potentially affects the listener map; the slack manager
// reload only matters when the touched integration is slack (or might have
// been — provider can be empty when the row is gone before lookup).
func (h *APIHandler) afterIntegrationMutation(provider database.MessagingProvider) {
	h.reloadAlertChannels()
	if h.slackManager != nil && (provider == "" || provider == database.MessagingProviderSlack) {
		h.slackManager.TriggerReload()
	}
}

// isProviderKnown reports whether the supplied provider identifier is one the
// API will accept on create. When a ProviderRegistry is wired we ask the
// registry (so a registered telegram stub would be acceptable while an
// unregistered provider is rejected); otherwise we fall back to the model
// whitelist so unit tests without a registry still validate.
func (h *APIHandler) isProviderKnown(p database.MessagingProvider) bool {
	if h.providerRegistry != nil {
		if _, err := h.providerRegistry.Get(p); err == nil {
			return true
		}
		// Even when the registry lacks the provider, accept any value the
		// model layer recognises so operators can pre-configure a Telegram
		// integration before the runtime provider lands.
	}
	return database.IsValidMessagingProvider(string(p))
}

// integrationErrStatus translates ChannelService errors into HTTP status codes.
func integrationErrStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrIntegrationNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrChannelNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrDuplicateDefaultPost):
		return http.StatusConflict
	default:
		// Validation errors from the service layer carry plain-text messages
		// (e.g. "integration name cannot be empty"); surface them as 400 so
		// the UI can render the message directly.
		if isClientError(err) {
			return http.StatusBadRequest
		}
		return http.StatusInternalServerError
	}
}

// isClientError reports whether the error message looks like a user-facing
// validation failure rather than an unexpected backend error. The service
// layer wraps DB errors with "create integration: ..." / "update channel: ..."
// prefixes; anything that lacks such a prefix is treated as a 400.
func isClientError(err error) bool {
	msg := err.Error()
	prefixes := []string{
		"create integration: ",
		"update integration: ",
		"delete integration",
		"create channel: ",
		"update channel: ",
		"delete channel",
		"list channels: ",
		"list integrations: ",
		"get integration ",
		"get channel ",
		"resolve ",
		"load integration ",
		"count existing ",
		"reload integration after update",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return false
		}
	}
	return true
}
