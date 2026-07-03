package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// proposalChatMessageMaxBytes caps a single operator chat message so a paste
// bomb cannot blow up the agent task.
const proposalChatMessageMaxBytes = 16_000

// handleProposals handles GET /api/proposals (?status=&kind=&page=&per_page=).
func (h *APIHandler) handleProposals(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	params := api.ParsePagination(r)

	rows, total, err := h.proposalService.ListProposals(status, kind, params.PerPage, params.Offset())
	if err != nil {
		slog.Error("failed to list proposals", "err", err)
		api.RespondError(w, http.StatusInternalServerError, "failed to list proposals")
		return
	}

	api.RespondJSON(w, http.StatusOK, api.PaginatedResponse{
		Data: rows,
		Pagination: api.PaginationMeta{
			Page:       params.Page,
			PerPage:    params.PerPage,
			Total:      total,
			TotalPages: params.TotalPages(total),
		},
	})
}

// handleProposalsCount handles GET /api/proposals/count — the pending-count
// badge in the UI nav.
func (h *APIHandler) handleProposalsCount(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	n, err := h.proposalService.CountPending()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "failed to count proposals")
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]int64{"pending": n})
}

// handleProposalByUUID handles GET /api/proposals/{uuid}.
func (h *APIHandler) handleProposalByUUID(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	p, err := h.proposalService.GetProposal(r.PathValue("uuid"))
	if err != nil {
		if errors.Is(err, services.ErrProposalNotFound) {
			api.RespondError(w, http.StatusNotFound, "proposal not found")
			return
		}
		api.RespondError(w, http.StatusInternalServerError, "failed to load proposal")
		return
	}
	api.RespondJSON(w, http.StatusOK, p)
}

// handleProposalApprove handles POST /api/proposals/{uuid}/approve.
// Approval applies the proposal immediately through the existing services;
// a stale target yields 409 with the superseded row so the UI can explain.
func (h *APIHandler) handleProposalApprove(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	p, err := h.proposalService.Approve(r.Context(), r.PathValue("uuid"))
	switch {
	case err == nil:
		api.RespondJSON(w, http.StatusOK, p)
	case errors.Is(err, services.ErrProposalNotFound):
		api.RespondError(w, http.StatusNotFound, "proposal not found")
	case errors.Is(err, services.ErrProposalNotApprovable):
		api.RespondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, services.ErrProposalStale):
		// The row was already marked superseded; return it with the reason so
		// the UI can render the "target changed" banner.
		api.RespondJSON(w, http.StatusConflict, map[string]interface{}{
			"error":    err.Error(),
			"proposal": p,
		})
	default:
		// Apply failure: the row carries status=apply_failed + apply_error.
		slog.Error("proposal apply failed", "uuid", r.PathValue("uuid"), "err", err)
		api.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":    err.Error(),
			"proposal": p,
		})
	}
}

// handleProposalReject handles POST /api/proposals/{uuid}/reject.
func (h *APIHandler) handleProposalReject(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	p, err := h.proposalService.Reject(r.PathValue("uuid"))
	switch {
	case err == nil:
		api.RespondJSON(w, http.StatusOK, p)
	case errors.Is(err, services.ErrProposalNotFound):
		api.RespondError(w, http.StatusNotFound, "proposal not found")
	case errors.Is(err, services.ErrProposalNotApprovable):
		api.RespondError(w, http.StatusConflict, err.Error())
	default:
		api.RespondError(w, http.StatusInternalServerError, "failed to reject proposal")
	}
}

// proposalChatResponse is the envelope for GET /api/proposals/{uuid}/chat.
type proposalChatResponse struct {
	Messages         []database.ProposalChatMessage `json:"messages"`
	ChatIncidentUUID string                         `json:"chat_incident_uuid"`
	ChatStatus       string                         `json:"chat_status"`
}

// handleProposalChatGet handles GET /api/proposals/{uuid}/chat — the
// transcript plus the backing incident's status so the UI knows whether the
// assistant is still responding (poll while chat_status == "running").
func (h *APIHandler) handleProposalChatGet(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}
	p, err := h.proposalService.GetProposal(r.PathValue("uuid"))
	if err != nil {
		if errors.Is(err, services.ErrProposalNotFound) {
			api.RespondError(w, http.StatusNotFound, "proposal not found")
			return
		}
		api.RespondError(w, http.StatusInternalServerError, "failed to load proposal")
		return
	}

	messages, err := h.proposalService.ListChatMessages(p.UUID)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "failed to load chat messages")
		return
	}

	chatStatus := ""
	if p.ChatIncidentUUID != "" {
		if inc, err := h.skillService.GetIncident(p.ChatIncidentUUID); err == nil {
			chatStatus = string(inc.Status)
		}
	}

	api.RespondJSON(w, http.StatusOK, proposalChatResponse{
		Messages:         messages,
		ChatIncidentUUID: p.ChatIncidentUUID,
		ChatStatus:       chatStatus,
	})
}

// proposalChatRequest is the body for POST /api/proposals/{uuid}/chat.
type proposalChatRequest struct {
	Message string `json:"message"`
}

// handleProposalChatPost handles POST /api/proposals/{uuid}/chat. Each turn
// runs a FRESH agent session under the proposal-editor root skill on the same
// chat incident (the Slack-proven pattern — session resume is unreliable once
// the original agent process exits). Continuity comes from the task body: the
// live proposal row plus the full transcript are rebuilt into every turn.
// Returns 202 immediately; the UI polls GET .../chat for the reply.
func (h *APIHandler) handleProposalChatPost(w http.ResponseWriter, r *http.Request) {
	if h.proposalService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "proposal service not available")
		return
	}

	var req proposalChatRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		api.RespondError(w, http.StatusBadRequest, "message cannot be empty")
		return
	}
	if len(message) > proposalChatMessageMaxBytes {
		api.RespondError(w, http.StatusBadRequest, "message too long")
		return
	}

	p, err := h.proposalService.GetProposal(r.PathValue("uuid"))
	if err != nil {
		if errors.Is(err, services.ErrProposalNotFound) {
			api.RespondError(w, http.StatusNotFound, "proposal not found")
			return
		}
		api.RespondError(w, http.StatusInternalServerError, "failed to load proposal")
		return
	}
	if p.Status != database.ProposalStatusPending && p.Status != database.ProposalStatusApplyFailed {
		api.RespondError(w, http.StatusConflict, fmt.Sprintf("proposal is %s; only pending proposals can be refined", p.Status))
		return
	}

	if h.agentWSHandler == nil || !h.agentWSHandler.IsWorkerConnected() {
		api.RespondError(w, http.StatusServiceUnavailable, "agent worker not connected")
		return
	}

	// Reject a new turn while the previous one is still running so a rapid
	// second message cannot displace an in-flight reply mid-generation.
	if p.ChatIncidentUUID != "" {
		if inc, err := h.skillService.GetIncident(p.ChatIncidentUUID); err == nil &&
			(inc.Status == database.IncidentStatusRunning || inc.Status == database.IncidentStatusPending) {
			api.RespondError(w, http.StatusConflict, "assistant is still responding; wait for the current turn to finish")
			return
		}
	}

	// Persist the operator message FIRST so the transcript survives any agent
	// failure downstream.
	if err := h.proposalService.AppendChatMessage(p.UUID, "operator", message); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "failed to store chat message")
		return
	}

	// First turn: spawn the backing incident for this proposal's chat.
	chatIncidentUUID := p.ChatIncidentUUID
	if chatIncidentUUID == "" {
		incidentCtx := &services.IncidentContext{
			Source:     "proposal",
			SourceID:   p.UUID,
			SourceKind: database.IncidentSourceKindProposal,
			SourceUUID: p.UUID,
			Context: database.JSONB{
				"proposal_uuid":  p.UUID,
				"proposal_kind":  p.Kind,
				"proposal_title": p.Title,
			},
			Message: fmt.Sprintf("Proposal chat: %s", p.Title),
		}
		var err error
		chatIncidentUUID, _, err = h.skillService.SpawnAgentInvocation("proposal-editor", incidentCtx)
		if err != nil {
			slog.Error("failed to spawn proposal chat incident", "proposal", p.UUID, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "failed to start proposal chat")
			return
		}
		if err := h.proposalService.SetChatIncident(p.UUID, chatIncidentUUID); err != nil {
			slog.Error("failed to link chat incident to proposal", "proposal", p.UUID, "err", err)
		}
	}

	messages, err := h.proposalService.ListChatMessages(p.UUID)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "failed to load chat transcript")
		return
	}

	task := buildProposalChatTask(p, messages)
	taskHeader := fmt.Sprintf("💡 Proposal Chat: %s\n\n--- Execution Log ---\n\n", p.Title)

	go h.runProposalChatTurn(p.UUID, chatIncidentUUID, taskHeader, task)

	api.RespondJSON(w, http.StatusAccepted, map[string]string{
		"chat_incident_uuid": chatIncidentUUID,
		"status":             "running",
	})
}

// buildProposalChatTask renders the per-turn task for the proposal-editor
// agent: the live proposal row, the full conversation so far, and an explicit
// pointer to the newest operator message. Rebuilt fresh every turn because
// each turn is a fresh agent session.
func buildProposalChatTask(p *database.Proposal, messages []database.ProposalChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are refining the following self-improvement proposal.\n\n")
	sb.WriteString("## Proposal\n\n")
	fmt.Fprintf(&sb, "- UUID: %s\n", p.UUID)
	fmt.Fprintf(&sb, "- Kind: %s\n", p.Kind)
	fmt.Fprintf(&sb, "- Status: %s\n", p.Status)
	fmt.Fprintf(&sb, "- Title: %s\n", p.Title)
	if p.TargetRef != "" {
		fmt.Fprintf(&sb, "- Target: %s\n", p.TargetRef)
	}
	if p.SourceIncidentUUIDs != nil {
		if uuids, ok := p.SourceIncidentUUIDs["uuids"].([]interface{}); ok && len(uuids) > 0 {
			strs := make([]string, 0, len(uuids))
			for _, u := range uuids {
				if s, ok := u.(string); ok {
					strs = append(strs, s)
				}
			}
			fmt.Fprintf(&sb, "- Evidence incidents: %s\n", strings.Join(strs, ", "))
		}
	}
	if p.Reasoning != "" {
		sb.WriteString("\n### Reasoning\n\n")
		sb.WriteString(p.Reasoning)
		sb.WriteString("\n")
	}
	if p.CurrentSnapshot != "" {
		sb.WriteString("\n### Current content (snapshot of the live target)\n\n```json\n")
		sb.WriteString(p.CurrentSnapshot)
		sb.WriteString("\n```\n")
	}
	sb.WriteString("\n### Proposed content (the draft you can revise via proposals.update_draft)\n\n```json\n")
	sb.WriteString(p.ProposedContent)
	sb.WriteString("\n```\n")

	sb.WriteString("\n## Conversation so far\n\n")
	for _, m := range messages {
		role := "Operator"
		if m.Role == "assistant" {
			role = "You"
		}
		fmt.Fprintf(&sb, "%s: %s\n\n", role, m.Content)
	}
	sb.WriteString("Reply to the operator's latest message above.")
	return sb.String()
}

// runProposalChatTurn executes one chat turn as a fresh agent session on the
// proposal's chat incident. Mirrors runAgentInvestigation, with three
// differences: the skill set is pinned to proposal-editor, the tool allowlist
// is the incidents+proposals pair, and the final response is appended to the
// proposal's chat transcript instead of being reformatted.
func (h *APIHandler) runProposalChatTurn(proposalUUID, chatIncidentUUID, taskHeader, task string) {
	if err := h.skillService.UpdateIncidentStatus(chatIncidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting turn..."); err != nil {
		slog.Error("proposal chat: failed to update incident status", "err", err)
	}

	var llmSettings *LLMSettingsForWorker
	if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
		llmSettings = BuildLLMSettingsForWorker(dbSettings)
	}

	done := make(chan struct{})
	var closeOnce sync.Once
	var response string
	var sessionID string
	var hasError bool
	var superseded atomic.Bool
	var lastStreamedLog string
	var finalTokensUsed int
	var finalExecutionTimeMs int64

	callback := IncidentCallback{
		OnOutput: func(output string) {
			lastStreamedLog += output
			if err := h.skillService.UpdateIncidentLog(chatIncidentUUID, taskHeader+lastStreamedLog); err != nil {
				slog.Error("proposal chat: failed to update incident log", "err", err)
			}
		},
		OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
			sessionID = sid
			response = output
			finalTokensUsed = tokensUsed
			finalExecutionTimeMs = executionTimeMs
			closeOnce.Do(func() { close(done) })
		},
		OnError: func(errorMsg string) {
			response = fmt.Sprintf("Error: %s", errorMsg)
			hasError = true
			closeOnce.Do(func() { close(done) })
		},
		OnSuperseded: func() {
			superseded.Store(true)
			closeOnce.Do(func() { close(done) })
		},
	}

	// Deliberately NOT executor.PrependGuidance — that helper is
	// incident-triage framed and would conflict with the proposal-editor
	// prompt (same reasoning as the cron path).
	// The tool allowlist is always non-nil (empty slice on lookup failure =
	// reject all), never nil (= allow all).
	runID, err := h.agentWSHandler.StartIncident(chatIncidentUUID, task, llmSettings,
		[]string{"proposal-editor"}, h.proposalService.ChatToolAllowlist(), callback)
	if err != nil {
		slog.Error("proposal chat: failed to start agent turn", "err", err)
		errMsg := fmt.Sprintf("Error: agent worker failed to start: %v", err)
		if updateErr := h.skillService.UpdateIncidentComplete(chatIncidentUUID, database.IncidentStatusFailed, "", taskHeader, errMsg, 0, 0); updateErr != nil {
			slog.Error("proposal chat: failed to finalize incident", "err", updateErr)
		}
		if appendErr := h.proposalService.AppendChatMessage(proposalUUID, "assistant", errMsg); appendErr != nil {
			slog.Error("proposal chat: failed to append error message", "err", appendErr)
		}
		return
	}

	<-done

	// A newer turn displaced this one — the replacement owns finalization.
	if superseded.Load() {
		slog.Info("proposal chat turn superseded", "proposal", proposalUUID)
		return
	}

	if !h.agentWSHandler.ReleaseRun(chatIncidentUUID, runID) {
		slog.Info("proposal chat turn displaced during finalization", "proposal", proposalUUID)
		return
	}

	fullLog := taskHeader + lastStreamedLog
	if response != "" {
		fullLog += "\n\n--- Final Response ---\n\n" + response
	}

	finalStatus := database.IncidentStatusCompleted
	if hasError {
		finalStatus = database.IncidentStatusFailed
	}
	if err := h.skillService.UpdateIncidentComplete(chatIncidentUUID, finalStatus, sessionID, fullLog, response, finalTokensUsed, finalExecutionTimeMs); err != nil {
		slog.Error("proposal chat: failed to finalize incident", "err", err)
	}

	if response == "" {
		response = "(the assistant returned an empty reply)"
	}
	if err := h.proposalService.AppendChatMessage(proposalUUID, "assistant", response); err != nil {
		slog.Error("proposal chat: failed to append assistant message", "err", err)
	}
}
