package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// CodexAuthStatus carries only sign-in state and the temporary device code.
// OAuth credentials stay in the agent worker's SDK credential store.
type CodexAuthStatus struct {
	Connected bool   `json:"connected"`
	Pending   bool   `json:"pending"`
	UserCode  string `json:"user_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (h *APIHandler) handleCodexAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var action string
	switch r.Method {
	case http.MethodGet:
		action = "status"
	case http.MethodPost:
		action = "start"
	case http.MethodPatch:
		action = "cancel"
	case http.MethodDelete:
		action = "disconnect"
	default:
		w.Header().Set("Allow", "GET, POST, PATCH, DELETE")
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	status, err := h.requestCodexAuth(r.Context(), action)
	if err != nil {
		api.RespondError(w, http.StatusFailedDependency, "Codex sign-in is unavailable on the agent worker. Check that the worker is connected and up to date.")
		return
	}
	api.RespondJSON(w, http.StatusOK, status)
}

func (h *APIHandler) requestCodexAuth(ctx context.Context, action string) (*CodexAuthStatus, error) {
	if h.agentWSHandler == nil {
		return nil, ErrWorkerNotConnected
	}
	msg := AgentMessage{Type: AgentMessageTypeCodexAuthRequest, CodexAuthAction: action}
	if action == "start" {
		if settings, err := database.GetOrCreateProxySettings(); err == nil && settings != nil {
			msg.ProxyConfig = &ProxyConfig{
				URL:        settings.ProxyURL,
				NoProxy:    settings.NoProxy,
				LLMEnabled: settings.LLMEnabled,
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := h.agentWSHandler.requestWorker(ctx, msg, 3*time.Second)
	if err != nil {
		return nil, err
	}
	if err := workerResponseError(resp); err != nil {
		return nil, err
	}
	if resp.CodexAuth == nil {
		return nil, errors.New("worker returned no Codex sign-in state")
	}
	return resp.CodexAuth, nil
}

func (h *APIHandler) isCodexConnected(ctx context.Context) bool {
	status, err := h.requestCodexAuth(ctx, "status")
	return err == nil && status.Connected
}

func (h *APIHandler) requireCodexConnection(w http.ResponseWriter, r *http.Request, id uint) bool {
	settings, err := database.GetLLMSettingsByID(id)
	if err != nil || settings.Provider != database.LLMProviderOpenAICodex {
		return true // The settings operation handles missing rows under lock.
	}
	status, err := h.requestCodexAuth(r.Context(), "status")
	if err != nil {
		api.RespondError(w, http.StatusFailedDependency, "Could not check Codex sign-in on the agent worker")
		return false
	}
	if !status.Connected {
		api.RespondError(w, http.StatusBadRequest, "Connect ChatGPT before selecting a Codex subscription configuration")
		return false
	}
	return true
}
