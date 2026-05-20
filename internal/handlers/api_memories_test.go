package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// mockMemoryService implements services.MemoryManager for handler tests.
// Each field can override behavior; calls without overrides just operate on
// the in-memory slice. Counters help assertions for sync-after-write.
//
// `mu` guards every field — Slack router tests drive writes from a worker
// goroutine and read state from the main goroutine via AssertEventually. Use
// the lowercase accessor methods (lastUpsertedSnap, syncCallCount, etc.) when
// reading from racing tests; synchronous tests can read fields directly.
type mockMemoryService struct {
	mu       sync.Mutex
	memories []database.Memory
	nextID   uint

	createErr error
	updateErr error
	upsertErr error
	deleteErr error
	getErr    error
	listErr   error

	syncCalls int

	lastCreated  *database.Memory
	lastUpdated  *database.Memory
	lastUpserted *database.Memory
}

// lastUpsertedSnap returns a snapshot of the last-upserted memory under the
// mutex. Used by router tests where writes happen on a worker goroutine.
func (m *mockMemoryService) lastUpsertedSnap() *database.Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastUpserted == nil {
		return nil
	}
	cp := *m.lastUpserted
	return &cp
}

func newMockMemoryService() *mockMemoryService {
	return &mockMemoryService{nextID: 1}
}

func (m *mockMemoryService) CreateMemory(mem *database.Memory) (*database.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	mem.ID = m.nextID
	m.nextID++
	m.memories = append(m.memories, *mem)
	m.lastCreated = mem
	m.syncCalls++
	return mem, nil
}

func (m *mockMemoryService) UpdateMemory(id uint, mem *database.Memory) (*database.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for i := range m.memories {
		if m.memories[i].ID == id {
			if mem.Scope != "" {
				m.memories[i].Scope = mem.Scope
			}
			if mem.Type != "" {
				m.memories[i].Type = mem.Type
			}
			if mem.Description != "" {
				m.memories[i].Description = mem.Description
			}
			if mem.Body != "" {
				m.memories[i].Body = mem.Body
			}
			cp := m.memories[i]
			m.lastUpdated = &cp
			m.syncCalls++
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("memory not found: %w", errFakeNotFound)
}

func (m *mockMemoryService) UpsertByName(mem *database.Memory) (*database.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upsertErr != nil {
		return nil, m.upsertErr
	}
	for i := range m.memories {
		if m.memories[i].Scope == mem.Scope && m.memories[i].Name == mem.Name {
			m.memories[i].Description = mem.Description
			m.memories[i].Body = mem.Body
			m.memories[i].IncidentUUID = mem.IncidentUUID
			m.memories[i].CreatedBy = mem.CreatedBy
			cp := m.memories[i]
			m.lastUpserted = &cp
			m.syncCalls++
			return &cp, nil
		}
	}
	mem.ID = m.nextID
	m.nextID++
	m.memories = append(m.memories, *mem)
	m.lastUpserted = mem
	m.syncCalls++
	return mem, nil
}

func (m *mockMemoryService) DeleteMemory(id uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for i := range m.memories {
		if m.memories[i].ID == id {
			m.memories = append(m.memories[:i], m.memories[i+1:]...)
			m.syncCalls++
			return nil
		}
	}
	return fmt.Errorf("memory not found: %w", errFakeNotFound)
}

func (m *mockMemoryService) GetMemory(id uint) (*database.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.memories {
		if m.memories[i].ID == id {
			cp := m.memories[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("memory not found: %w", errFakeNotFound)
}

func (m *mockMemoryService) ListMemories(scope, memType string) ([]database.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []database.Memory
	for _, mem := range m.memories {
		if scope != "" && mem.Scope != scope {
			continue
		}
		if memType != "" && mem.Type != memType {
			continue
		}
		out = append(out, mem)
	}
	return out, nil
}

func (m *mockMemoryService) ListMemoriesByScope(scope string) ([]database.Memory, error) {
	return m.ListMemories(scope, "")
}

func (m *mockMemoryService) ListAllScopes() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, mem := range m.memories {
		if !seen[mem.Scope] {
			seen[mem.Scope] = true
			out = append(out, mem.Scope)
		}
	}
	return out, nil
}

func (m *mockMemoryService) CountByIncidentUUID(uuid string, createdBy string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, mem := range m.memories {
		if mem.IncidentUUID != uuid {
			continue
		}
		if createdBy != "" && mem.CreatedBy != createdBy {
			continue
		}
		n++
	}
	return n, nil
}

func (m *mockMemoryService) SyncMemoryFiles() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncCalls++
	return nil
}

// errFakeNotFound is a sentinel that wraps services' own not-found semantics
// so respondMemoryWriteError routes mock errors to 404 in tests where we
// purposely set updateErr/deleteErr.
var errFakeNotFound = errors.New("fake not found")

// newMemoryAPIHandler is a small helper that wires only the memory service
// onto a bare APIHandler. All other deps stay nil — they're not exercised
// by the memory routes.
func newMemoryAPIHandler(mem services.MemoryManager) *APIHandler {
	return NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, mem, nil, nil)
}

func validMemoryRequest(name string) MemoryRequest {
	return MemoryRequest{
		Scope:       services.MemoryScopeGlobal,
		Type:        services.MemoryTypeHost,
		Name:        name,
		Description: "host description",
		Body:        "body content",
		CreatedBy:   services.MemoryCreatedByOperator,
	}
}

func doJSON(t *testing.T, h *APIHandler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.SetupRoutes(mux)
	mux.ServeHTTP(w, req)
	return w
}

func TestHandleMemories_List_NoFilter(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	if _, err := mock.CreateMemory(&database.Memory{Scope: "global", Type: services.MemoryTypeHost, Name: "a", Description: "d", Body: "b"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := doJSON(t, h, http.MethodGet, "/api/memories", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []database.Memory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got = %+v", got)
	}
}

func TestHandleMemories_List_WithFilters(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	for _, m := range []*database.Memory{
		{Scope: "global", Type: services.MemoryTypeHost, Name: "h1", Description: "d", Body: "b"},
		{Scope: "global", Type: services.MemoryTypeFeedback, Name: "f1", Description: "d", Body: "b"},
		{Scope: "redis", Type: services.MemoryTypeHost, Name: "h2", Description: "d", Body: "b"},
	} {
		if _, err := mock.CreateMemory(m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	w := doJSON(t, h, http.MethodGet, "/api/memories?type=host", nil)
	var got []database.Memory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("type filter expected 2, got %d: %+v", len(got), got)
	}

	w = doJSON(t, h, http.MethodGet, "/api/memories?scope=redis", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "h2" {
		t.Fatalf("scope filter = %+v", got)
	}

	w = doJSON(t, h, http.MethodGet, "/api/memories?type=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid type filter expected 400, got %d", w.Code)
	}
}

func TestHandleMemories_Create_Valid(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodPost, "/api/memories", validMemoryRequest("brand-new"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if mock.lastCreated == nil || mock.lastCreated.Name != "brand-new" {
		t.Fatalf("expected last created memory to match, got %+v", mock.lastCreated)
	}
	if mock.syncCalls != 1 {
		t.Fatalf("sync calls = %d, want 1", mock.syncCalls)
	}
}

func TestHandleMemories_Create_PropagatesValidationError(t *testing.T) {
	mock := newMockMemoryService()
	mock.createErr = errors.New("invalid memory type \"bogus\"")
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodPost, "/api/memories", validMemoryRequest("x"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid memory type") {
		t.Fatalf("body missing service error: %s", w.Body.String())
	}
}

func TestHandleMemories_Create_FileSyncErrorIs500(t *testing.T) {
	mock := newMockMemoryService()
	mock.createErr = errors.New("memory created but file sync failed: oops")
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodPost, "/api/memories", validMemoryRequest("x"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestHandleMemoryByID_GetUpdateDelete(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	created, _ := mock.CreateMemory(&database.Memory{Scope: "global", Type: services.MemoryTypeHost, Name: "to-mutate", Description: "d", Body: "b"})

	// GET
	w := doJSON(t, h, http.MethodGet, fmt.Sprintf("/api/memories/%d", created.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}

	// PUT
	updateReq := validMemoryRequest("to-mutate")
	updateReq.Description = "updated"
	w = doJSON(t, h, http.MethodPut, fmt.Sprintf("/api/memories/%d", created.ID), updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", w.Code, w.Body.String())
	}

	// DELETE
	w = doJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/memories/%d", created.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", w.Code)
	}

	// GET after delete -> 404
	w = doJSON(t, h, http.MethodGet, fmt.Sprintf("/api/memories/%d", created.ID), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("post-delete get status = %d", w.Code)
	}
}

func TestHandleMemoryByID_InvalidIDIs400(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodGet, "/api/memories/not-a-number", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleMemoryScopes(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	for _, m := range []*database.Memory{
		{Scope: "global", Type: services.MemoryTypeHost, Name: "g", Description: "d", Body: "b"},
		{Scope: "redis", Type: services.MemoryTypeHost, Name: "r", Description: "d", Body: "b"},
	} {
		if _, err := mock.CreateMemory(m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	w := doJSON(t, h, http.MethodGet, "/api/memories/scopes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var scopes []string
	if err := json.Unmarshal(w.Body.Bytes(), &scopes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %v", scopes)
	}
}

func TestHandleIncidentFeedback_StoresAsGlobalFeedbackMemory(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodPost, "/api/incidents/abc-123/feedback", IncidentFeedbackRequest{
		Text: "the postgres data dir is /mnt/data not /var/lib/postgresql",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if mock.lastUpserted == nil {
		t.Fatalf("expected feedback persisted")
	}
	got := mock.lastUpserted
	if got.Scope != services.MemoryScopeGlobal {
		t.Errorf("scope = %q, want global", got.Scope)
	}
	if got.Type != services.MemoryTypeFeedback {
		t.Errorf("type = %q, want feedback", got.Type)
	}
	if got.IncidentUUID != "abc-123" {
		t.Errorf("incident UUID = %q", got.IncidentUUID)
	}
	if got.CreatedBy != services.MemoryCreatedByOperator {
		t.Errorf("created_by = %q, want operator", got.CreatedBy)
	}
	if !strings.Contains(got.Body, "postgres data dir") {
		t.Errorf("body did not preserve original message: %q", got.Body)
	}
	if !strings.Contains(got.Name, "abc123") {
		t.Errorf("name should embed incident UUID prefix to avoid collisions: %q", got.Name)
	}
}

func TestHandleIncidentFeedback_EmptyTextIs400(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	w := doJSON(t, h, http.MethodPost, "/api/incidents/abc/feedback", IncidentFeedbackRequest{Text: "  "})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleIncidentFeedback_LongTextTruncatedToDescriptionCap(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	long := strings.Repeat("x", services.MemoryDescriptionMaxLen+200)
	w := doJSON(t, h, http.MethodPost, "/api/incidents/abc/feedback", IncidentFeedbackRequest{Text: long})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := mock.lastUpserted; got != nil {
		if len(got.Description) > services.MemoryDescriptionMaxLen {
			t.Errorf("description len = %d, want ≤ %d", len(got.Description), services.MemoryDescriptionMaxLen)
		}
	}
}

// recordingSkillService is a minimal SkillIncidentManager test double that
// records RegenerateSkillMd calls. All other interface methods are no-ops or
// zero-value returns — only the regeneration call is exercised in these
// tests, but the type still has to satisfy the full interface so it can be
// passed to NewAPIHandler.
type recordingSkillService struct {
	regenerated []string
	regenErr    error
}

func (r *recordingSkillService) RegenerateSkillMd(name string) error {
	r.regenerated = append(r.regenerated, name)
	return r.regenErr
}

// --- SkillManager no-ops ---
func (r *recordingSkillService) CreateSkill(string, string, string, string) (*database.Skill, error) {
	return nil, nil
}
func (r *recordingSkillService) UpdateSkill(string, string, string, bool) (*database.Skill, error) {
	return nil, nil
}
func (r *recordingSkillService) DeleteSkill(string) error              { return nil }
func (r *recordingSkillService) ListSkills() ([]database.Skill, error) { return nil, nil }
func (r *recordingSkillService) ListEnabledSkills() ([]database.Skill, error) {
	return nil, nil
}
func (r *recordingSkillService) GetEnabledSkillNames() []string { return nil }
func (r *recordingSkillService) GetToolAllowlist() []services.ToolAllowlistEntry {
	return nil
}
func (r *recordingSkillService) GetSkill(string) (*database.Skill, error)  { return nil, nil }
func (r *recordingSkillService) AssignTools(string, []uint) error          { return nil }
func (r *recordingSkillService) GetSkillDir(string) string                 { return "" }
func (r *recordingSkillService) GetSkillScriptsDir(string) string          { return "" }
func (r *recordingSkillService) GetSkillPrompt(string) (string, error)     { return "", nil }
func (r *recordingSkillService) UpdateSkillPrompt(string, string) error    { return nil }
func (r *recordingSkillService) SyncSkillsFromFilesystem() error           { return nil }
func (r *recordingSkillService) ListSkillScripts(string) ([]string, error) { return nil, nil }
func (r *recordingSkillService) ClearSkillScripts(string) error            { return nil }
func (r *recordingSkillService) GetSkillScript(string, string) (*services.ScriptInfo, error) {
	return nil, nil
}
func (r *recordingSkillService) UpdateSkillScript(string, string, string) error { return nil }
func (r *recordingSkillService) DeleteSkillScript(string, string) error         { return nil }

// --- IncidentManager no-ops ---
func (r *recordingSkillService) SpawnIncidentManager(*services.IncidentContext) (string, string, error) {
	return "", "", nil
}
func (r *recordingSkillService) SpawnAgentInvocation(string, *services.IncidentContext) (string, string, error) {
	return "", "", nil
}
func (r *recordingSkillService) UpdateIncidentStatus(string, database.IncidentStatus, string, string) error {
	return nil
}
func (r *recordingSkillService) UpdateIncidentComplete(string, database.IncidentStatus, string, string, string, int, int64) error {
	return nil
}
func (r *recordingSkillService) UpdateIncidentLog(string, string) error         { return nil }
func (r *recordingSkillService) GetIncident(string) (*database.Incident, error) { return nil, nil }
func (r *recordingSkillService) AppendSubagentLog(string, string, string) error { return nil }

// newMemoryAPIHandlerWithSkill wires both a memory mock and a skill
// regeneration recorder. Used by tests that need to verify skill-scoped
// memory writes trigger SKILL.md regeneration.
func newMemoryAPIHandlerWithSkill(mem services.MemoryManager, skill services.SkillIncidentManager) *APIHandler {
	return NewAPIHandler(skill, nil, nil, nil, nil, nil, nil, nil, mem, nil, nil)
}

func TestHandleMemories_Create_SkillScopeRegeneratesSkillMd(t *testing.T) {
	// Regression: skill-scoped memory writes only updated MEMORY.md but
	// not the embedded copy in SKILL.md, so agents kept seeing the stale
	// manifest until the next restart.
	mem := newMockMemoryService()
	skill := &recordingSkillService{}
	h := newMemoryAPIHandlerWithSkill(mem, skill)

	req := validMemoryRequest("test-mem")
	req.Scope = "redis-skill"
	w := doJSON(t, h, http.MethodPost, "/api/memories", req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(skill.regenerated) != 1 || skill.regenerated[0] != "redis-skill" {
		t.Errorf("expected RegenerateSkillMd(\"redis-skill\"), got %v", skill.regenerated)
	}
}

func TestHandleMemories_Create_GlobalScopeDoesNotRegenerate(t *testing.T) {
	// AGENTS.md is rebuilt at incident-spawn time and reads the global
	// manifest fresh — no SKILL.md regen needed for global writes.
	mem := newMockMemoryService()
	skill := &recordingSkillService{}
	h := newMemoryAPIHandlerWithSkill(mem, skill)

	req := validMemoryRequest("test-mem")
	req.Scope = services.MemoryScopeGlobal
	w := doJSON(t, h, http.MethodPost, "/api/memories", req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if len(skill.regenerated) != 0 {
		t.Errorf("expected no regen for global scope, got %v", skill.regenerated)
	}
}

func TestHandleMemoryByID_Update_CrossScopeMoveRegeneratesBoth(t *testing.T) {
	mem := newMockMemoryService()
	skill := &recordingSkillService{}
	h := newMemoryAPIHandlerWithSkill(mem, skill)

	created, _ := mem.CreateMemory(&database.Memory{
		Scope: "redis-skill", Type: services.MemoryTypeHost, Name: "movable",
		Description: "d", Body: "b",
	})

	updateReq := validMemoryRequest("movable")
	updateReq.Scope = "postgres-skill"
	w := doJSON(t, h, http.MethodPut, fmt.Sprintf("/api/memories/%d", created.ID), updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Should regenerate both the prior scope (redis-skill) and the new
	// scope (postgres-skill) so both SKILL.mds reflect the move.
	want := map[string]int{"redis-skill": 1, "postgres-skill": 1}
	got := map[string]int{}
	for _, n := range skill.regenerated {
		got[n]++
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("regen[%q] = %d, want %d (full log: %v)", k, got[k], v, skill.regenerated)
		}
	}
}

func TestHandleMemoryByID_Delete_RegeneratesPriorSkillScope(t *testing.T) {
	mem := newMockMemoryService()
	skill := &recordingSkillService{}
	h := newMemoryAPIHandlerWithSkill(mem, skill)

	created, _ := mem.CreateMemory(&database.Memory{
		Scope: "redis-skill", Type: services.MemoryTypeHost, Name: "doomed",
		Description: "d", Body: "b",
	})
	// Reset recorder so we don't see the create's regen.
	skill.regenerated = nil

	w := doJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/memories/%d", created.ID), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if len(skill.regenerated) != 1 || skill.regenerated[0] != "redis-skill" {
		t.Errorf("expected delete to regen redis-skill, got %v", skill.regenerated)
	}
}

func TestHandleIncidentFeedback_LongMultibyteBodyStaysValidUTF8(t *testing.T) {
	// Regression: previously the body was sliced by raw byte count, which
	// could split a multi-byte rune mid-character — Postgres then rejected
	// the INSERT with "invalid byte sequence". Use a string of 3-byte runes
	// long enough that the cap lands in the middle of a rune.
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)

	long := strings.Repeat("日", (services.MemoryBodyMaxBytes/3)+10)
	w := doJSON(t, h, http.MethodPost, "/api/incidents/abc/feedback", IncidentFeedbackRequest{Text: long})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := mock.lastUpserted
	if got == nil {
		t.Fatal("expected memory persisted")
	}
	if len(got.Body) > services.MemoryBodyMaxBytes {
		t.Errorf("body len = %d, want ≤ %d", len(got.Body), services.MemoryBodyMaxBytes)
	}
	// Every char in the input is a 3-byte UTF-8 sequence; if we sliced
	// mid-rune the result wouldn't be a multiple of 3.
	if len(got.Body)%3 != 0 {
		t.Errorf("body len %d is not on a 3-byte UTF-8 boundary — body was sliced mid-rune", len(got.Body))
	}
}
