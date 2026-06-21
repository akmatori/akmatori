package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

// ---- test doubles ----

// suppGateSkillService records SpawnIncidentManager and RecordSuppressedIncident
// calls for suppression gate tests.
type suppGateSkillService struct {
	mu sync.Mutex

	spawnCount    int
	suppressCount int
	spawnErr      error
	spawnUUID     string
	suppressUUID  string
	suppressErr   error

	suppressCalls []suppCall
}

type suppCall struct {
	signatureName string
	confidence    float64
}

func (s *suppGateSkillService) SpawnIncidentManager(*services.IncidentContext) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spawnErr != nil {
		return "", "", s.spawnErr
	}
	s.spawnCount++
	u := s.spawnUUID
	if u == "" {
		u = "spawned-uuid"
	}
	return u, "", nil
}

func (s *suppGateSkillService) RecordSuppressedIncident(_ *services.IncidentContext, sigName, _ string, confidence float64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppressErr != nil {
		return "", s.suppressErr
	}
	s.suppressCount++
	s.suppressCalls = append(s.suppressCalls, suppCall{signatureName: sigName, confidence: confidence})
	u := s.suppressUUID
	if u == "" {
		u = "suppressed-uuid"
	}
	return u, nil
}

func (s *suppGateSkillService) InsertFiringAlert(_ context.Context, _ string, _ string, _ alerts.NormalizedAlert) error {
	return nil
}

func (s *suppGateSkillService) LinkAlertToIncident(_ context.Context, _ string, _ string, _ alerts.NormalizedAlert) error {
	return nil
}

func (s *suppGateSkillService) getSpawnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnCount
}

func (s *suppGateSkillService) getSuppressCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suppressCount
}

// Stubs for SkillManager (not used in these tests).
func (s *suppGateSkillService) SpawnAgentInvocation(string, *services.IncidentContext) (string, string, error) {
	return "", "", nil
}
func (s *suppGateSkillService) UpdateIncidentStatus(string, database.IncidentStatus, string, string) error {
	return nil
}
func (s *suppGateSkillService) UpdateIncidentComplete(string, database.IncidentStatus, string, string, string, int, int64) error {
	return nil
}
func (s *suppGateSkillService) UpdateIncidentLog(string, string) error        { return nil }
func (s *suppGateSkillService) GetIncident(string) (*database.Incident, error) { return nil, nil }
func (s *suppGateSkillService) AppendSubagentLog(string, string, string) error { return nil }
func (s *suppGateSkillService) CreateSkill(string, string, string, string) (*database.Skill, error) {
	return nil, nil
}
func (s *suppGateSkillService) UpdateSkill(string, string, string, bool) (*database.Skill, error) {
	return nil, nil
}
func (s *suppGateSkillService) DeleteSkill(string) error              { return nil }
func (s *suppGateSkillService) ListSkills() ([]database.Skill, error) { return nil, nil }
func (s *suppGateSkillService) ListEnabledSkills() ([]database.Skill, error) {
	return nil, nil
}
func (s *suppGateSkillService) GetEnabledSkillNames() []string                     { return nil }
func (s *suppGateSkillService) GetToolAllowlist() []services.ToolAllowlistEntry    { return nil }
func (s *suppGateSkillService) GetSkill(string) (*database.Skill, error)           { return nil, nil }
func (s *suppGateSkillService) AssignTools(string, []uint) error                   { return nil }
func (s *suppGateSkillService) GetSkillDir(string) string                          { return "" }
func (s *suppGateSkillService) GetSkillScriptsDir(string) string                   { return "" }
func (s *suppGateSkillService) GetSkillPrompt(string) (string, error)              { return "", nil }
func (s *suppGateSkillService) UpdateSkillPrompt(string, string) error             { return nil }
func (s *suppGateSkillService) RegenerateSkillMd(string) error                     { return nil }
func (s *suppGateSkillService) SyncSkillsFromFilesystem() error                    { return nil }
func (s *suppGateSkillService) ListSkillScripts(string) ([]string, error)          { return nil, nil }
func (s *suppGateSkillService) ClearSkillScripts(string) error                     { return nil }
func (s *suppGateSkillService) GetSkillScript(string, string) (*services.ScriptInfo, error) {
	return nil, nil
}
func (s *suppGateSkillService) UpdateSkillScript(string, string, string) error { return nil }
func (s *suppGateSkillService) DeleteSkillScript(string, string) error         { return nil }

// suppOneShotLLMCaller is a configurable stub for the suppressor LLM call.
type suppOneShotLLMCaller struct {
	calls   int32
	respond func(ctx context.Context) (string, error)
}

func (c *suppOneShotLLMCaller) OneShotLLM(ctx context.Context, _ *services.LLMSettingsForWorker, _, _ string, _ int, _ float64) (string, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.respond != nil {
		return c.respond(ctx)
	}
	return `{"suppressed":false,"signature_name":"","confidence":0.1,"reasoning":"no match"}`, nil
}

func (c *suppOneShotLLMCaller) callCount() int {
	return int(atomic.LoadInt32(&c.calls))
}

// setupSuppressorHandlerDB opens an isolated in-memory DB with the tables
// needed by AlertSuppressor and seeds LLM settings.
func setupSuppressorHandlerDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testhelpers.NewGlobalSQLiteDB(t,
		&database.Memory{},
		&database.LLMSettings{},
		&database.AlertSuppressionLog{},
		&database.Incident{},
		&database.SlackSettings{},
		&database.GeneralSettings{},
	)
}

// seedSuppHandlerSettings seeds a GeneralSettings row for suppressor tests.
func seedSuppHandlerSettings(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&database.GeneralSettings{}).Error; err != nil {
		t.Fatalf("seed GeneralSettings: %v", err)
	}
}

// seedHandlerSignature inserts a suppression signature memory for handler tests.
func seedHandlerSignature(t *testing.T, db *gorm.DB, name, body string) {
	t.Helper()
	m := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        name,
		Description: "test signature",
		Body:        body,
		Suppress:    true,
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed signature %s: %v", name, err)
	}
	if err := db.Create(&database.LLMSettings{
		Name:     "test",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4-6",
		Active:   true,
		Enabled:  true,
	}).Error; err != nil {
		// Already seeded by another call — ignore unique constraint error.
		_ = err
	}
}

// newSuppTestAlert returns a normalized alert for suppression gate tests.
func newSuppTestAlert() alerts.NormalizedAlert {
	return alerts.NormalizedAlert{
		AlertName:  "DiskSpaceLow",
		TargetHost: "cron-01",
		Summary:    "Disk usage above 90%",
		Status:     database.AlertStatusFiring,
		Severity:   database.AlertSeverityWarning,
	}
}

// ---- tests ----

// TestAlertHandler_BelowThresholdSuppression_Spawns verifies that a below-threshold
// suppression verdict falls through to normal incident spawning.
func TestAlertHandler_BelowThresholdSuppression_Spawns(t *testing.T) {
	db := setupSuppressorHandlerDB(t)
	seedHandlerSignature(t, db, "maybe-pattern", "Possibly related pattern")
	seedSuppHandlerSettings(t, db)

	caller := &suppOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"suppressed":true,"signature_name":"maybe-pattern","confidence":0.55,"reasoning":"not certain"}`, nil
	}

	suppressor := services.NewAlertSuppressor(caller, db)

	svc := &suppGateSkillService{spawnUUID: "new-incident-uuid"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertSuppressor(suppressor)

	instance := &database.AlertSourceInstance{
		UUID:    "src-1",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newSuppTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn for below-threshold suppression, got %d", svc.getSpawnCount())
	}
	if svc.getSuppressCount() != 0 {
		t.Errorf("expected 0 RecordSuppressedIncident calls, got %d", svc.getSuppressCount())
	}
}

// TestAlertHandler_NilSuppressor_AlwaysSpawns verifies the handler works
// when SetAlertSuppressor is never called (nil suppressor).
func TestAlertHandler_NilSuppressor_AlwaysSpawns(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.SlackSettings{},
		&database.AlertCorrelationLog{},
		&database.AlertSuppressionLog{},
		&database.Incident{},
	)

	svc := &suppGateSkillService{spawnUUID: "no-suppressor-incident"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	// No SetAlertSuppressor call — h.alertSuppressor is nil.

	instance := &database.AlertSourceInstance{
		UUID:    "src-uuid",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newSuppTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn with nil suppressor, got %d", svc.getSpawnCount())
	}
	if svc.getSuppressCount() != 0 {
		t.Errorf("expected 0 suppression calls with nil suppressor, got %d", svc.getSuppressCount())
	}
}

// TestAlertHandler_SetAlertSuppressor_NilSafe verifies SetAlertSuppressor
// accepts nil without panicking.
func TestAlertHandler_SetAlertSuppressor_NilSafe(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetAlertSuppressor(nil)
	if h.alertSuppressor != nil {
		t.Error("expected nil alertSuppressor after SetAlertSuppressor(nil)")
	}
}
