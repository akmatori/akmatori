package handlers

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// MemoryRequest is the shared payload for create / update / feedback. Empty
// fields on update are treated as "leave unchanged" by the service layer
// (which merges before validation).
type MemoryRequest struct {
	Scope        string `json:"scope"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Body         string `json:"body"`
	IncidentUUID string `json:"incident_uuid,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
}

// memoryUpdateRequest is the PUT body. Body shadows MemoryRequest.Body so
// the handler can tell an absent "body" key (leave unchanged) from "body": ""
// (clear the body).
type memoryUpdateRequest struct {
	MemoryRequest
	Body *string `json:"body"`
}

// MemoryFromTextRequest is the body for POST /api/memories/from-text: free
// operator text that the drafter turns into a memory.
type MemoryFromTextRequest struct {
	Text  string `json:"text"`
	Scope string `json:"scope,omitempty"`
}

// MemoryDetailResponse is the GET /api/memories/{id} body: the memory plus
// its resolved [[...]] references, so a view can link them.
type MemoryDetailResponse struct {
	database.Memory
	References []services.MemoryRef `json:"references"`
}

// IncidentFeedbackRequest is the body for POST /api/incidents/{uuid}/feedback.
// Operators send free-form feedback text; the handler persists it as a
// scope=global memory of type=feedback tagged with the incident UUID.
type IncidentFeedbackRequest struct {
	Text string `json:"text"`
}

// handleMemories handles GET (list, with ?scope=, ?type= and ?incident_uuid=
// filters) and POST.
func (h *APIHandler) handleMemories(w http.ResponseWriter, r *http.Request) {
	if h.memoryService == nil {
		api.RespondError(w, http.StatusInternalServerError, "memory service not available")
		return
	}
	switch r.Method {
	case http.MethodGet:
		scope := r.URL.Query().Get("scope")
		memType := services.NormalizeMemoryType(r.URL.Query().Get("type"))
		if memType != "" && !services.ValidMemoryType(memType) {
			api.RespondError(w, http.StatusBadRequest, "invalid type filter")
			return
		}
		memories, err := h.memoryService.ListMemories(services.MemoryFilter{
			Scope:        scope,
			Type:         memType,
			IncidentUUID: r.URL.Query().Get("incident_uuid"),
		})
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "failed to list memories")
			return
		}
		api.RespondJSON(w, http.StatusOK, memories)

	case http.MethodPost:
		var req MemoryRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		m := &database.Memory{
			Scope:        req.Scope,
			Type:         req.Type,
			Name:         req.Name,
			Description:  req.Description,
			Body:         req.Body,
			IncidentUUID: req.IncidentUUID,
			CreatedBy:    req.CreatedBy,
		}
		created, err := h.memoryService.CreateMemory(m)
		if err != nil {
			respondMemoryWriteError(w, err)
			return
		}
		// Skill-scoped writes must trigger SKILL.md regeneration; the memory
		// manifest is embedded into SKILL.md at write time, so the existing
		// file would otherwise still show the pre-create state until restart.
		h.regenerateSkillForMemoryScope(created.Scope)
		api.RespondJSON(w, http.StatusCreated, created)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleMemoryByID dispatches GET/PUT/DELETE on /api/memories/{id} and the
// nested /api/memories/scopes endpoint (handled inline so the route table
// doesn't fight Go's ServeMux precedence rules).
func (h *APIHandler) handleMemoryByID(w http.ResponseWriter, r *http.Request) {
	if h.memoryService == nil {
		api.RespondError(w, http.StatusInternalServerError, "memory service not available")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/api/memories/")
	switch tail {
	case "scopes":
		h.handleMemoryScopes(w, r)
		return
	case "export":
		h.handleMemoryExport(w, r)
		return
	case "from-text":
		h.handleMemoryFromText(w, r)
		return
	}

	id, err := strconv.ParseUint(tail, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid memory ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		m, err := h.memoryService.GetMemory(uint(id))
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "memory not found")
			return
		}
		refs := h.memoryService.ResolveRefs(m)
		if refs == nil {
			refs = []services.MemoryRef{}
		}
		api.RespondJSON(w, http.StatusOK, MemoryDetailResponse{Memory: *m, References: refs})

	case http.MethodPut:
		var req memoryUpdateRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// Capture the prior scope BEFORE update so a cross-scope move
		// (e.g. "redis" → "postgres") can refresh both affected SKILL.mds.
		var priorScope string
		if existing, err := h.memoryService.GetMemory(uint(id)); err == nil {
			priorScope = existing.Scope
		}
		m := &database.Memory{
			Scope:        req.Scope,
			Type:         req.Type,
			Name:         req.Name,
			Description:  req.Description,
			IncidentUUID: req.IncidentUUID,
			CreatedBy:    req.CreatedBy,
		}
		if req.Body != nil {
			m.Body = *req.Body
		}
		updated, err := h.memoryService.UpdateMemory(uint(id), m, req.Body != nil)
		if err != nil {
			respondMemoryWriteError(w, err)
			return
		}
		h.regenerateSkillForMemoryScope(priorScope)
		if updated.Scope != priorScope {
			h.regenerateSkillForMemoryScope(updated.Scope)
		}
		api.RespondJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		// Read scope before delete so we can regenerate the right SKILL.md
		// after the row is gone.
		var priorScope string
		if existing, err := h.memoryService.GetMemory(uint(id)); err == nil {
			priorScope = existing.Scope
		}
		if err := h.memoryService.DeleteMemory(uint(id)); err != nil {
			respondMemoryWriteError(w, err)
			return
		}
		h.regenerateSkillForMemoryScope(priorScope)
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleMemoryExport streams every memory as a zip of <scope>/<name>.md files.
// It is a normal authenticated GET; the UI fetches it with its auth header
// and saves the blob, so no token goes into a URL.
func (h *APIHandler) handleMemoryExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var buf bytes.Buffer
	if err := h.memoryService.ExportZip(&buf); err != nil {
		slog.Error("memory export failed", "err", err)
		api.RespondError(w, http.StatusInternalServerError, "failed to export memories")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="akmatori-memory-`+time.Now().UTC().Format("2006-01-02")+`.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// handleMemoryFromText turns operator free text into an operator-authored
// memory. The drafter proposes name/description/body/type with a one-shot LLM
// call and falls back to a deterministic draft, so the text is never lost.
// A taken name gets a numeric suffix instead of overwriting another memory.
func (h *APIHandler) handleMemoryFromText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req MemoryFromTextRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		api.RespondError(w, http.StatusBadRequest, "text cannot be empty")
		return
	}
	if len(text) > services.MemoryTextMaxBytes {
		api.RespondError(w, http.StatusBadRequest, fmt.Sprintf("text exceeds %d bytes", services.MemoryTextMaxBytes))
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = services.MemoryScopeGlobal
	}

	draft := h.memoryDrafter.Draft(r.Context(), text)
	created, err := h.memoryService.CreateWithUniqueName(&database.Memory{
		Scope:       scope,
		Type:        draft.Type,
		Name:        draft.Name,
		Description: draft.Description,
		Body:        draft.Body,
		CreatedBy:   services.MemoryCreatedByOperator,
	})
	if err != nil {
		respondMemoryWriteError(w, err)
		return
	}
	h.regenerateSkillForMemoryScope(created.Scope)
	api.RespondJSON(w, http.StatusCreated, created)
}

// handleMemoryScopes returns the distinct scope strings present in the table.
func (h *APIHandler) handleMemoryScopes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.memoryService == nil {
		api.RespondError(w, http.StatusInternalServerError, "memory service not available")
		return
	}
	scopes, err := h.memoryService.ListAllScopes()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "failed to list scopes")
		return
	}
	api.RespondJSON(w, http.StatusOK, scopes)
}

// handleIncidentFeedback persists operator-supplied feedback against an
// incident as a scope=global memory of type=feedback. Used by the UI's
// "leave feedback" affordance on the incident detail page; mirrors the
// LLM-classified Slack thread-reply path that lands in Task 7.
func (h *APIHandler) handleIncidentFeedback(w http.ResponseWriter, r *http.Request) {
	if h.memoryService == nil {
		api.RespondError(w, http.StatusInternalServerError, "memory service not available")
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		api.RespondError(w, http.StatusBadRequest, "missing incident UUID")
		return
	}

	var req IncidentFeedbackRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		api.RespondError(w, http.StatusBadRequest, "feedback text cannot be empty")
		return
	}

	description := truncateForFeedbackDescription(text, services.MemoryDescriptionMaxLen)
	// Body must stay valid UTF-8 — Postgres rejects mid-rune slicing with
	// "invalid byte sequence", so we use the shared UTF-8-safe truncator
	// instead of slicing by raw byte count.
	body := services.TruncateMemoryBody(text)

	name := services.SlugifyMemoryName(description)
	// Ensure uniqueness per (scope, name) by appending the incident UUID's prefix.
	// Operator-driven feedback often carries a similar gist across incidents; without
	// this we'd collapse them all into one memory.
	if uuidPrefix := safeUUIDPrefix(uuid); uuidPrefix != "" {
		name = name + "-" + uuidPrefix
		// Cap at the validation length so the UpsertByName doesn't reject.
		if len(name) > services.MemoryNameMaxLen {
			name = name[:services.MemoryNameMaxLen]
		}
	}

	m := &database.Memory{
		Scope:        services.MemoryScopeGlobal,
		Type:         services.MemoryTypeFeedback,
		Name:         name,
		Description:  description,
		Body:         body,
		IncidentUUID: uuid,
		CreatedBy:    services.MemoryCreatedByOperator,
	}
	created, err := h.memoryService.UpsertByName(m)
	if err != nil {
		respondMemoryWriteError(w, err)
		return
	}
	api.RespondJSON(w, http.StatusCreated, created)
}

// truncateForFeedbackDescription trims to at most maxBytes bytes (the validation
// cap is byte-based) without slicing mid-character. Reserves 3 bytes for the
// trailing "…" so the result still fits.
func truncateForFeedbackDescription(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	const ellipsis = "…" // 3 bytes
	budget := maxBytes - len(ellipsis)
	if budget < 0 {
		return s[:maxBytes]
	}
	// Step back to a UTF-8 boundary. RuneStart gates a leading byte.
	cut := budget
	for cut > 0 && !isUTF8RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ") + ellipsis
}

func isUTF8RuneStart(b byte) bool {
	// 0xxxxxxx (ASCII) or 11xxxxxx (multibyte leading byte).
	return b&0xC0 != 0x80
}

// safeUUIDPrefix returns up to 8 slug-safe characters from the front of a UUID.
// Returns empty string if the input is empty or yields no slug-safe content.
func safeUUIDPrefix(uuid string) string {
	out := strings.ToLower(strings.TrimSpace(uuid))
	keep := make([]byte, 0, 8)
	for i := 0; i < len(out) && len(keep) < 8; i++ {
		c := out[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			keep = append(keep, c)
		}
	}
	return string(keep)
}

// regenerateSkillForMemoryScope refreshes a skill's SKILL.md after a memory
// write that may have changed the per-scope manifest. No-op for the global
// scope (AGENTS.md is regenerated per incident at spawn time so the global
// manifest is always read fresh) and for empty input. Best-effort: failures
// are logged but never bubble up — the memory write itself succeeded and
// the manifest will catch up at the next regeneration window.
func (h *APIHandler) regenerateSkillForMemoryScope(scope string) {
	if scope == "" || scope == services.MemoryScopeGlobal {
		return
	}
	if h.skillService == nil {
		return
	}
	if err := h.skillService.RegenerateSkillMd(scope); err != nil {
		// Skill may not exist (operator created memory under an arbitrary
		// scope name), or the regen may have hit a transient FS error.
		// Logged at debug because both states are recoverable on the next
		// startup or skill edit.
		slog.Debug("memory write: skill regen skipped", "scope", scope, "err", err)
	}
}

// respondMemoryWriteError maps service-layer errors onto HTTP statuses.
// Validation problems → 400, missing rows → 404, sync issues → 500.
func respondMemoryWriteError(w http.ResponseWriter, err error) {
	if services.IsMemoryNotFoundErr(err) {
		api.RespondError(w, http.StatusNotFound, err.Error())
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "file sync failed") {
		api.RespondError(w, http.StatusInternalServerError, msg)
		return
	}
	api.RespondError(w, http.StatusBadRequest, msg)
}
