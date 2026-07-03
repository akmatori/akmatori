package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// mockProposalManager is a recording stub for services.ProposalManager so the
// handler tests exercise routing, status codes, and JSON shapes without
// sqlite or a real apply pipeline.
type mockProposalManager struct {
	proposals map[string]*database.Proposal
	messages  map[string][]database.ProposalChatMessage

	approveErr error
	rejectErr  error

	approvedUUID string
	rejectedUUID string
}

func newMockProposalManager() *mockProposalManager {
	return &mockProposalManager{
		proposals: map[string]*database.Proposal{},
		messages:  map[string][]database.ProposalChatMessage{},
	}
}

func (m *mockProposalManager) ListProposals(status, kind string, limit, offset int) ([]database.Proposal, int64, error) {
	var out []database.Proposal
	for _, p := range m.proposals {
		if status != "" && string(p.Status) != status {
			continue
		}
		if kind != "" && p.Kind != kind {
			continue
		}
		out = append(out, *p)
	}
	return out, int64(len(out)), nil
}

func (m *mockProposalManager) GetProposal(uuid string) (*database.Proposal, error) {
	if p, ok := m.proposals[uuid]; ok {
		out := *p
		return &out, nil
	}
	return nil, services.ErrProposalNotFound
}

func (m *mockProposalManager) CountPending() (int64, error) {
	var n int64
	for _, p := range m.proposals {
		if p.Status == database.ProposalStatusPending {
			n++
		}
	}
	return n, nil
}

func (m *mockProposalManager) Approve(_ context.Context, uuid string) (*database.Proposal, error) {
	p, ok := m.proposals[uuid]
	if !ok {
		return nil, services.ErrProposalNotFound
	}
	m.approvedUUID = uuid
	if m.approveErr != nil {
		out := *p
		return &out, m.approveErr
	}
	p.Status = database.ProposalStatusApproved
	out := *p
	return &out, nil
}

func (m *mockProposalManager) Reject(uuid string) (*database.Proposal, error) {
	p, ok := m.proposals[uuid]
	if !ok {
		return nil, services.ErrProposalNotFound
	}
	if m.rejectErr != nil {
		return nil, m.rejectErr
	}
	m.rejectedUUID = uuid
	p.Status = database.ProposalStatusRejected
	out := *p
	return &out, nil
}

func (m *mockProposalManager) SetChatIncident(uuid, incidentUUID string) error {
	if p, ok := m.proposals[uuid]; ok {
		p.ChatIncidentUUID = incidentUUID
		return nil
	}
	return services.ErrProposalNotFound
}

func (m *mockProposalManager) AppendChatMessage(proposalUUID, role, content string) error {
	m.messages[proposalUUID] = append(m.messages[proposalUUID], database.ProposalChatMessage{
		ProposalUUID: proposalUUID, Role: role, Content: content,
	})
	return nil
}

func (m *mockProposalManager) ListChatMessages(proposalUUID string) ([]database.ProposalChatMessage, error) {
	return m.messages[proposalUUID], nil
}

func (m *mockProposalManager) ChatToolAllowlist() []services.ToolAllowlistEntry {
	return []services.ToolAllowlistEntry{}
}

func newProposalAPIHandler(mgr services.ProposalManager) *APIHandler {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetProposalService(mgr)
	return h
}

func TestHandleProposals_ServiceUnavailable(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/proposals"},
		{http.MethodGet, "/api/proposals/count"},
		{http.MethodGet, "/api/proposals/u1"},
		{http.MethodPost, "/api/proposals/u1/approve"},
		{http.MethodPost, "/api/proposals/u1/reject"},
		{http.MethodGet, "/api/proposals/u1/chat"},
		{http.MethodPost, "/api/proposals/u1/chat"},
	} {
		w := doJSON(t, h, tc.method, tc.path, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: expected 503, got %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestHandleProposals_ListAndCount(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	mgr.proposals["p2"] = &database.Proposal{UUID: "p2", Kind: database.ProposalKindMemoryNew, Status: database.ProposalStatusRejected, Title: "Two"}
	h := newProposalAPIHandler(mgr)

	w := doJSON(t, h, http.MethodGet, "/api/proposals?status=pending", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data       []database.Proposal `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].UUID != "p1" || resp.Pagination.Total != 1 {
		t.Errorf("unexpected list payload: %+v", resp)
	}

	w = doJSON(t, h, http.MethodGet, "/api/proposals/count", nil)
	var count map[string]int64
	_ = json.Unmarshal(w.Body.Bytes(), &count)
	if count["pending"] != 1 {
		t.Errorf("expected pending=1, got %v", count)
	}
}

func TestHandleProposalByUUID(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	h := newProposalAPIHandler(mgr)

	w := doJSON(t, h, http.MethodGet, "/api/proposals/p1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	w = doJSON(t, h, http.MethodGet, "/api/proposals/missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleProposalApprove_Paths(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	h := newProposalAPIHandler(mgr)

	// Happy path.
	w := doJSON(t, h, http.MethodPost, "/api/proposals/p1/approve", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.approvedUUID != "p1" {
		t.Errorf("Approve not invoked")
	}

	// Missing row.
	w = doJSON(t, h, http.MethodPost, "/api/proposals/nope/approve", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	// Already decided → 409.
	mgr.approveErr = fmt.Errorf("%w: status is approved", services.ErrProposalNotApprovable)
	w = doJSON(t, h, http.MethodPost, "/api/proposals/p1/approve", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	// Stale target → 409 with the superseded proposal in the body.
	mgr.approveErr = fmt.Errorf("%w: runbook changed", services.ErrProposalStale)
	w = doJSON(t, h, http.MethodPost, "/api/proposals/p1/approve", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for stale, got %d", w.Code)
	}
	var staleResp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &staleResp)
	if staleResp["proposal"] == nil || staleResp["error"] == nil {
		t.Errorf("stale response must include proposal + error, got %v", staleResp)
	}

	// Apply failure → 422 with the apply_failed proposal.
	mgr.approveErr = fmt.Errorf("disk exploded")
	w = doJSON(t, h, http.MethodPost, "/api/proposals/p1/approve", nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for apply failure, got %d", w.Code)
	}
}

func TestHandleProposalReject(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	h := newProposalAPIHandler(mgr)

	w := doJSON(t, h, http.MethodPost, "/api/proposals/p1/reject", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if mgr.rejectedUUID != "p1" {
		t.Errorf("Reject not invoked")
	}
}

func TestHandleProposalChatGet_Transcript(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	_ = mgr.AppendChatMessage("p1", "operator", "hello")
	_ = mgr.AppendChatMessage("p1", "assistant", "hi")
	h := newProposalAPIHandler(mgr)

	w := doJSON(t, h, http.MethodGet, "/api/proposals/p1/chat", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp proposalChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Messages) != 2 || resp.Messages[0].Role != "operator" {
		t.Errorf("unexpected transcript: %+v", resp.Messages)
	}
	if resp.ChatStatus != "" {
		t.Errorf("no chat incident yet — status must be empty, got %q", resp.ChatStatus)
	}
}

func TestHandleProposalChatPost_Validation(t *testing.T) {
	mgr := newMockProposalManager()
	mgr.proposals["p1"] = &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusPending, Title: "One"}
	mgr.proposals["done"] = &database.Proposal{UUID: "done", Kind: database.ProposalKindRunbookNew, Status: database.ProposalStatusApproved, Title: "Done"}
	h := newProposalAPIHandler(mgr)

	// Empty message.
	w := doJSON(t, h, http.MethodPost, "/api/proposals/p1/chat", proposalChatRequest{Message: "  "})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty message, got %d", w.Code)
	}

	// Decided proposal → 409.
	w = doJSON(t, h, http.MethodPost, "/api/proposals/done/chat", proposalChatRequest{Message: "hi"})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for decided proposal, got %d", w.Code)
	}

	// Worker not connected (nil agentWSHandler) → 503, and the operator
	// message must NOT have been persisted yet.
	w = doJSON(t, h, http.MethodPost, "/api/proposals/p1/chat", proposalChatRequest{Message: "hi"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without worker, got %d", w.Code)
	}
	if len(mgr.messages["p1"]) != 0 {
		t.Errorf("message must not be persisted before the worker check")
	}

	// Missing proposal → 404.
	w = doJSON(t, h, http.MethodPost, "/api/proposals/missing/chat", proposalChatRequest{Message: "hi"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
