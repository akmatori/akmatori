package handlers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// longWindowSkillService is a corrGateSkillService variant that supports
// GetIncident returning real incident data for runRecurrenceUpdate tests.
type longWindowSkillService struct {
	mu sync.Mutex

	spawnCount  int
	appendCount int
	spawnUUID   string
	spawnErr    error

	appendCalls  []corrAppendCall
	incidents    map[string]*database.Incident
	getIncErr    error
}

func newLongWindowSkillService(uuid string) *longWindowSkillService {
	return &longWindowSkillService{
		spawnUUID: uuid,
		incidents: make(map[string]*database.Incident),
	}
}

func (s *longWindowSkillService) addIncident(inc *database.Incident) {
	s.mu.Lock()
	s.incidents[inc.UUID] = inc
	s.mu.Unlock()
}

func (s *longWindowSkillService) SpawnIncidentManager(*services.IncidentContext) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spawnErr != nil {
		return "", "", s.spawnErr
	}
	s.spawnCount++
	return s.spawnUUID, "", nil
}
func (s *longWindowSkillService) AppendCorrelatedAlert(_ context.Context, _ string, incidentUUID string, alert alerts.NormalizedAlert, confidence float64, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendCount++
	s.appendCalls = append(s.appendCalls, corrAppendCall{
		incidentUUID: incidentUUID,
		alertName:    alert.AlertName,
		confidence:   confidence,
	})
	return nil
}
func (s *longWindowSkillService) GetIncident(uuid string) (*database.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getIncErr != nil {
		return nil, s.getIncErr
	}
	inc, ok := s.incidents[uuid]
	if !ok {
		return nil, errors.New("not found")
	}
	return inc, nil
}
func (s *longWindowSkillService) getSpawnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnCount
}
func (s *longWindowSkillService) getAppendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendCount
}

// Stub the full SkillIncidentManager interface.
func (s *longWindowSkillService) SpawnAgentInvocation(string, *services.IncidentContext) (string, string, error) {
	return "", "", nil
}
func (s *longWindowSkillService) UpdateIncidentStatus(string, database.IncidentStatus, string, string) error {
	return nil
}
func (s *longWindowSkillService) UpdateIncidentComplete(string, database.IncidentStatus, string, string, string, int, int64) error {
	return nil
}
func (s *longWindowSkillService) UpdateIncidentLog(string, string) error { return nil }
func (s *longWindowSkillService) AppendSubagentLog(string, string, string) error {
	return nil
}
func (s *longWindowSkillService) RecordSuppressedIncident(*services.IncidentContext, string, string, float64) (string, error) {
	return "", nil
}
func (s *longWindowSkillService) CreateSkill(string, string, string, string) (*database.Skill, error) {
	return nil, nil
}
func (s *longWindowSkillService) UpdateSkill(string, string, string, bool) (*database.Skill, error) {
	return nil, nil
}
func (s *longWindowSkillService) DeleteSkill(string) error              { return nil }
func (s *longWindowSkillService) ListSkills() ([]database.Skill, error) { return nil, nil }
func (s *longWindowSkillService) ListEnabledSkills() ([]database.Skill, error) {
	return nil, nil
}
func (s *longWindowSkillService) GetEnabledSkillNames() []string                  { return nil }
func (s *longWindowSkillService) GetToolAllowlist() []services.ToolAllowlistEntry { return nil }
func (s *longWindowSkillService) GetSkill(string) (*database.Skill, error)        { return nil, nil }
func (s *longWindowSkillService) AssignTools(string, []uint) error                { return nil }
func (s *longWindowSkillService) GetSkillDir(string) string                       { return "" }
func (s *longWindowSkillService) GetSkillScriptsDir(string) string                { return "" }
func (s *longWindowSkillService) GetSkillPrompt(string) (string, error)           { return "", nil }
func (s *longWindowSkillService) UpdateSkillPrompt(string, string) error          { return nil }
func (s *longWindowSkillService) RegenerateSkillMd(string) error                  { return nil }
func (s *longWindowSkillService) SyncSkillsFromFilesystem() error                 { return nil }
func (s *longWindowSkillService) ListSkillScripts(string) ([]string, error)       { return nil, nil }
func (s *longWindowSkillService) ClearSkillScripts(string) error                  { return nil }
func (s *longWindowSkillService) GetSkillScript(string, string) (*services.ScriptInfo, error) {
	return nil, nil
}
func (s *longWindowSkillService) UpdateSkillScript(string, string, string) error { return nil }
func (s *longWindowSkillService) DeleteSkillScript(string, string) error         { return nil }

// lwOneShotCaller is a configurable stub for runRecurrenceUpdate's LLM call.
type lwOneShotCaller struct {
	calls   int32
	respond func(ctx context.Context) (string, error)
}

func (c *lwOneShotCaller) OneShotLLM(ctx context.Context, _ *services.LLMSettingsForWorker, _, _ string, _ int, _ float64) (string, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.respond != nil {
		return c.respond(ctx)
	}
	return "Service outage persists. Engineers are still investigating the root cause.", nil
}
func (c *lwOneShotCaller) callCount() int { return int(atomic.LoadInt32(&c.calls)) }

// setupLWDB prepares a DB with LLMSettings for runRecurrenceUpdate.
func setupLWDB(t *testing.T) {
	t.Helper()
	testhelpers.NewGlobalSQLiteDB(t,
		&database.SlackSettings{},
		&database.AlertCorrelationLog{},
		&database.Incident{},
		&database.LLMSettings{},
		&database.GeneralSettings{},
	)
	database.DB.Create(&database.LLMSettings{
		Name:     "test",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4-6",
		Active:   true,
		Enabled:  true,
	})
}

// TestRunRecurrenceUpdate_SuccessPath verifies that when all conditions are met,
// runRecurrenceUpdate records the recurrence via AppendCorrelatedAlert and does
// not fall through to a full spawn.
func TestRunRecurrenceUpdate_SuccessPath(t *testing.T) {
	setupLWDB(t)

	inc := &database.Incident{
		UUID:           "blocked-4d",
		Title:          "Service down for 4 days",
		Status:         "running",
		CorrelatedCount: 2,
	}
	svc := newLongWindowSkillService("new-spawn-uuid")
	svc.addIncident(inc)

	caller := &lwOneShotCaller{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetOneShotCaller(caller)

	verdict := services.CorrelationVerdict{
		Correlated:      true,
		IncidentUUID:    "blocked-4d",
		Confidence:      0.92,
		Reasoning:       "same host blocked",
		IsLongWindowMatch: true,
	}
	alert := alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "web01"}

	err := h.runRecurrenceUpdate(context.Background(), "src-1", "blocked-4d", "fpABC", alert, verdict)
	if err != nil {
		t.Fatalf("runRecurrenceUpdate returned error: %v", err)
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
	if svc.getAppendCount() != 1 {
		t.Errorf("expected 1 AppendCorrelatedAlert call, got %d", svc.getAppendCount())
	}
	if svc.getSpawnCount() != 0 {
		t.Errorf("expected 0 spawns, got %d", svc.getSpawnCount())
	}
}

// TestRunRecurrenceUpdate_EmptyFingerprint_ReturnsError verifies that an empty
// fingerprint causes runRecurrenceUpdate to return an error (caller must spawn).
func TestRunRecurrenceUpdate_EmptyFingerprint_ReturnsError(t *testing.T) {
	setupLWDB(t)

	inc := &database.Incident{UUID: "inc-1", Title: "blocked", Status: "running"}
	svc := newLongWindowSkillService("spawn-uuid")
	svc.addIncident(inc)

	caller := &lwOneShotCaller{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetOneShotCaller(caller)

	verdict := services.CorrelationVerdict{
		Correlated: true, IncidentUUID: "inc-1", Confidence: 0.9, IsLongWindowMatch: true,
	}
	err := h.runRecurrenceUpdate(context.Background(), "src-1", "inc-1", "", alerts.NormalizedAlert{}, verdict)
	if err == nil {
		t.Error("expected error when fingerprint is empty")
	}
	if caller.callCount() != 0 {
		t.Error("expected no LLM call when fingerprint is empty")
	}
}

// TestRunRecurrenceUpdate_NoCaller_ReturnsError verifies that a nil one-shot
// caller causes runRecurrenceUpdate to return an error (caller must spawn).
func TestRunRecurrenceUpdate_NoCaller_ReturnsError(t *testing.T) {
	setupLWDB(t)

	inc := &database.Incident{UUID: "inc-2", Title: "blocked", Status: "running"}
	svc := newLongWindowSkillService("spawn-uuid")
	svc.addIncident(inc)

	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	// h.oneShotCaller is nil

	verdict := services.CorrelationVerdict{
		Correlated: true, IncidentUUID: "inc-2", Confidence: 0.9, IsLongWindowMatch: true,
	}
	err := h.runRecurrenceUpdate(context.Background(), "src-1", "inc-2", "fp123", alerts.NormalizedAlert{}, verdict)
	if err == nil {
		t.Error("expected error when one-shot caller is nil")
	}
}

// TestRunRecurrenceUpdate_LLMError_ReturnsError verifies that an LLM error
// causes runRecurrenceUpdate to return an error so the caller can fall through.
func TestRunRecurrenceUpdate_LLMError_ReturnsError(t *testing.T) {
	setupLWDB(t)

	inc := &database.Incident{UUID: "inc-3", Title: "blocked", Status: "running"}
	svc := newLongWindowSkillService("spawn-uuid")
	svc.addIncident(inc)

	caller := &lwOneShotCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "", errors.New("LLM unavailable")
	}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetOneShotCaller(caller)

	verdict := services.CorrelationVerdict{
		Correlated: true, IncidentUUID: "inc-3", Confidence: 0.9, IsLongWindowMatch: true,
	}
	err := h.runRecurrenceUpdate(context.Background(), "src-1", "inc-3", "fp123", alerts.NormalizedAlert{}, verdict)
	if err == nil {
		t.Error("expected error when LLM call fails")
	}
	if svc.getAppendCount() != 0 {
		t.Error("expected no AppendCorrelatedAlert call when LLM fails")
	}
}

// TestAlertHandler_LongWindowMatch_CallsRecurrenceUpdate verifies that the full
// processAlert flow calls runRecurrenceUpdate (not recordRecurrence) when the
// correlator returns IsLongWindowMatch=true, and falls through to spawn when
// runRecurrenceUpdate fails (no oneShotCaller configured).
func TestAlertHandler_LongWindowMatch_FallsThrough_WhenNoShotCaller(t *testing.T) {
	db := setupCorrelatorHandlerDB(t)

	// Seed an incident that is 4 days old with matching fingerprint.
	fp := services.ComputeAlertFingerprint("src-1", "CPUHigh", "web01")
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	if err := db.Create(&database.Incident{
		UUID:             "blocked-4d",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "CPU high on web01 (blocked 4d)",
		Status:           database.IncidentStatusRunning,
		StartedAt:        fourDaysAgo,
		AlertFingerprint: fp,
		Response:         "awaiting vendor fix",
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	longWindow := 7
	enabled := true
	win := 30
	mc := 20
	th := 0.7
	if err := db.Create(&database.GeneralSettings{
		AlertCorrelationEnabled:        &enabled,
		AlertCorrelationWindowMinutes:  &win,
		AlertCorrelationMaxCandidates:  &mc,
		AlertCorrelationThreshold:      &th,
		AlertCorrelationLongWindowDays: &longWindow,
	}).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	corrCaller := &corrOneShotLLMCaller{}
	corrCaller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"blocked-4d","confidence":0.93,"reasoning":"same host still blocked"}`, nil
	}
	correlator := services.NewAlertCorrelator(corrCaller, db)

	svc := &corrGateSkillService{spawnUUID: "fallthrough-spawn"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertCorrelator(correlator)
	// No SetOneShotCaller — runRecurrenceUpdate will fail, falling through to spawn.

	instance := &database.AlertSourceInstance{
		UUID:    "src-1",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	alert := alerts.NormalizedAlert{
		AlertName:  "CPUHigh",
		TargetHost: "web01",
		Summary:    "CPU above 90%",
		Status:     database.AlertStatusFiring,
	}
	h.processAlert(instance, alert)

	// runRecurrenceUpdate failed (no caller), so we fall through to spawn.
	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn (fall-through from long-window failure), got %d", svc.getSpawnCount())
	}
	if svc.getAppendCount() != 0 {
		t.Errorf("expected 0 append calls (runRecurrenceUpdate returns error before AppendCorrelatedAlert), got %d", svc.getAppendCount())
	}
}
