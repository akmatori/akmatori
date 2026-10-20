package handlers

import (
	"net/http"
	"time"

	"github.com/akmatori/akmatori/internal/api"
)

// workerStatusResponse is the worker part of GET /api/system/status.
// LastMessageAgeSeconds is computed on the server so the UI does not depend
// on the browser clock agreeing with the API host.
type workerStatusResponse struct {
	Connected             bool       `json:"connected"`
	ConnectedAt           *time.Time `json:"connected_at,omitempty"`
	LastMessageAt         *time.Time `json:"last_message_at,omitempty"`
	LastMessageAgeSeconds *int64     `json:"last_message_age_seconds,omitempty"`
}

type systemStatusResponse struct {
	Worker workerStatusResponse `json:"worker"`
}

// handleSystemStatus handles GET /api/system/status: the runtime state the
// UI status bar shows. /health stays a bare liveness probe.
func (h *APIHandler) handleSystemStatus(w http.ResponseWriter, _ *http.Request) {
	var resp systemStatusResponse
	if h.agentWSHandler != nil {
		if st := h.agentWSHandler.WorkerStatus(); st.Connected {
			connectedAt, lastMessageAt := st.ConnectedAt, st.LastMessageAt
			age := int64(time.Since(lastMessageAt).Seconds())
			resp.Worker = workerStatusResponse{
				Connected:             true,
				ConnectedAt:           &connectedAt,
				LastMessageAt:         &lastMessageAt,
				LastMessageAgeSeconds: &age,
			}
		}
	}
	api.RespondJSON(w, http.StatusOK, resp)
}
