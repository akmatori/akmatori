package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

// ---- test doubles ----

// corrGateSkillService is a minimal SkillIncidentManager stub that records
// SpawnIncidentManager and AppendCorrelatedAlert calls.
type corrGateSkillService struct {
	mu sync.Mutex

	spawnCount  int
	appendCount int
	spawnErr    error
	spawnUUID   string
	spawnHook   func() // called at entry of SpawnIncidentManager (before the mutex)

	appendCalls []corrAppendCall
}

type corrAppendCall struct {
	incidentUUID string
	alertName    string
	confidence   float64
}

func (s *corrGateSkillService) SpawnIncidentManager(*services.IncidentContext) (string, string, error) {
	if s.spawnHook != nil {
		s.spawnHook()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spawnErr != nil {
		return "", "", s.spawnErr
	}
	s.spawnCount++
	uuid := s.spawnUUID
	if uuid == "" {
		uuid = "spawned-incident-uuid"
	}
	return uuid, "", nil
}

func (s *corrGateSkillService) AppendCorrelatedAlert(_ context.Context, incidentUUID string, alert alerts.NormalizedAlert, confidence float64, _ string, _ time.Time) error {
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

func (s *corrGateSkillService) getSpawnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnCount
}

func (s *corrGateSkillService) getAppendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendCount
}

// SkillManager stubs (not used in these tests).
func (s *corrGateSkillService) SpawnAgentInvocation(string, *services.IncidentContext) (string, string, error) {
	return "", "", nil
}
func (s *corrGateSkillService) UpdateIncidentStatus(string, database.IncidentStatus, string, string) error {
	return nil
}
func (s *corrGateSkillService) UpdateIncidentComplete(string, database.IncidentStatus, string, string, string, int, int64) error {
	return nil
}
func (s *corrGateSkillService) UpdateIncidentLog(string, string) error        { return nil }
func (s *corrGateSkillService) GetIncident(string) (*database.Incident, error) { return nil, nil }
func (s *corrGateSkillService) AppendSubagentLog(string, string, string) error { return nil }
func (s *corrGateSkillService) CreateSkill(string, string, string, string) (*database.Skill, error) {
	return nil, nil
}
func (s *corrGateSkillService) UpdateSkill(string, string, string, bool) (*database.Skill, error) {
	return nil, nil
}
func (s *corrGateSkillService) DeleteSkill(string) error              { return nil }
func (s *corrGateSkillService) ListSkills() ([]database.Skill, error) { return nil, nil }
func (s *corrGateSkillService) ListEnabledSkills() ([]database.Skill, error) {
	return nil, nil
}
func (s *corrGateSkillService) GetEnabledSkillNames() []string                     { return nil }
func (s *corrGateSkillService) GetToolAllowlist() []services.ToolAllowlistEntry    { return nil }
func (s *corrGateSkillService) GetSkill(string) (*database.Skill, error)           { return nil, nil }
func (s *corrGateSkillService) AssignTools(string, []uint) error                   { return nil }
func (s *corrGateSkillService) GetSkillDir(string) string                          { return "" }
func (s *corrGateSkillService) GetSkillScriptsDir(string) string                   { return "" }
func (s *corrGateSkillService) GetSkillPrompt(string) (string, error)              { return "", nil }
func (s *corrGateSkillService) UpdateSkillPrompt(string, string) error             { return nil }
func (s *corrGateSkillService) RegenerateSkillMd(string) error                     { return nil }
func (s *corrGateSkillService) SyncSkillsFromFilesystem() error                    { return nil }
func (s *corrGateSkillService) ListSkillScripts(string) ([]string, error)          { return nil, nil }
func (s *corrGateSkillService) ClearSkillScripts(string) error                     { return nil }
func (s *corrGateSkillService) GetSkillScript(string, string) (*services.ScriptInfo, error) {
	return nil, nil
}
func (s *corrGateSkillService) UpdateSkillScript(string, string, string) error { return nil }
func (s *corrGateSkillService) DeleteSkillScript(string, string) error         { return nil }

// corrOneShotLLMCaller is a configurable stub for services.OneShotLLMCaller.
type corrOneShotLLMCaller struct {
	mu      sync.Mutex
	calls   int32
	respond func(ctx context.Context) (string, error)
}

func (c *corrOneShotLLMCaller) OneShotLLM(ctx context.Context, _ *services.LLMSettingsForWorker, _, _ string, _ int, _ float64) (string, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.respond != nil {
		return c.respond(ctx)
	}
	return `{"correlated":false,"incident_uuid":"","confidence":0.1,"reasoning":"no match"}`, nil
}

func (c *corrOneShotLLMCaller) callCount() int {
	return int(atomic.LoadInt32(&c.calls))
}

// setupCorrelatorHandlerDB opens an isolated in-memory DB with the tables
// needed by AlertCorrelator and seeds LLM settings so GetLLMSettings works.
func setupCorrelatorHandlerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{},
		&database.LLMSettings{},
		&database.AlertCorrelationLog{},
		&database.SlackSettings{},
	)
	if err := db.Create(&database.LLMSettings{
		Name:     "test",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4-6",
		Active:   true,
		Enabled:  true,
	}).Error; err != nil {
		t.Fatalf("seed LLMSettings: %v", err)
	}
	return db
}

// seedHandlerIncident inserts a candidate incident for correlation tests.
func seedHandlerIncident(t *testing.T, db *gorm.DB, uuid, title, status string, age time.Duration) {
	t.Helper()
	if err := db.Create(&database.Incident{
		UUID:       uuid,
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-1",
		Title:      title,
		Status:     database.IncidentStatus(status),
		StartedAt:  time.Now().Add(-age),
		Response:   "some response text",
	}).Error; err != nil {
		t.Fatalf("seed incident %s: %v", uuid, err)
	}
}

// newCorrTestAlert returns a normalized alert for correlation tests.
func newCorrTestAlert() alerts.NormalizedAlert {
	return alerts.NormalizedAlert{
		AlertName:  "CPUHigh",
		TargetHost: "web01",
		Summary:    "CPU above 90%",
		Status:     database.AlertStatusFiring,
		Severity:   database.AlertSeverityCritical,
	}
}

// ---- tests ----

// TestAlertHandler_Singleflight_15ConcurrentAlerts verifies that concurrent
// alerts with the same key result in exactly 1 spawn and N-1 recurrences.
//
// Approach: the leader's SpawnIncidentManager blocks until all followers have
// queued inside singleflight.Do, then releases. This guarantees deterministic
// overlap rather than relying on goroutine scheduling timing.
func TestAlertHandler_Singleflight_15ConcurrentAlerts(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.SlackSettings{},
		&database.AlertCorrelationLog{},
		&database.Incident{},
	)

	const n = 15

	// spawnEntered is closed when SpawnIncidentManager first starts.
	// spawnRelease is closed to let SpawnIncidentManager complete.
	spawnEntered := make(chan struct{})
	spawnRelease := make(chan struct{})
	var enterOnce sync.Once

	svc := &corrGateSkillService{
		spawnUUID: "shared-incident",
		spawnHook: func() {
			enterOnce.Do(func() { close(spawnEntered) })
			<-spawnRelease
		},
	}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	instance := &database.AlertSourceInstance{
		UUID:    "src-uuid",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}
	alert := newCorrTestAlert()

	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	for range n {
		go func() {
			defer wg.Done()
			<-start
			h.processAlert(instance, alert)
		}()
	}
	close(start)

	// Wait until the leader goroutine is inside SpawnIncidentManager (blocking).
	// All other goroutines that call Do with the same key while the leader is
	// blocked will queue as followers rather than becoming new leaders.
	<-spawnEntered

	// Give the remaining goroutines time to reach singleflight.Do and queue up.
	// The leader is blocking so they will wait rather than spawn independently.
	time.Sleep(50 * time.Millisecond)

	// Release the leader; queued followers receive the same incident UUID.
	close(spawnRelease)
	wg.Wait()

	if spawns := svc.getSpawnCount(); spawns != 1 {
		t.Errorf("expected 1 spawn, got %d", spawns)
	}
	if appends := svc.getAppendCount(); appends != n-1 {
		t.Errorf("expected %d recurrences, got %d", n-1, appends)
	}
}

// TestAlertHandler_ConfidentVerdict_NoSpawn verifies that a confident
// correlation verdict suppresses the spawn and records a recurrence instead.
func TestAlertHandler_ConfidentVerdict_NoSpawn(t *testing.T) {
	db := setupCorrelatorHandlerDB(t)
	seedHandlerIncident(t, db, "existing-inc", "CPU high on web01", "running", 5*time.Minute)

	caller := &corrOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"existing-inc","confidence":0.92,"reasoning":"same host and alert"}`, nil
	}

	cfg := services.CorrelationConfig{
		Enabled:       true,
		Window:        30 * time.Minute,
		MaxCandidates: 20,
		Threshold:     0.7,
	}
	correlator := services.NewAlertCorrelator(caller, db, cfg)

	svc := &corrGateSkillService{spawnUUID: "would-be-new-incident"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertCorrelator(correlator)

	instance := &database.AlertSourceInstance{
		UUID:    "src-1",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newCorrTestAlert())

	if svc.getSpawnCount() != 0 {
		t.Errorf("expected 0 spawns with confident correlation, got %d", svc.getSpawnCount())
	}
	if svc.getAppendCount() != 1 {
		t.Errorf("expected 1 AppendCorrelatedAlert call, got %d", svc.getAppendCount())
	}
	if svc.appendCalls[0].incidentUUID != "existing-inc" {
		t.Errorf("expected recurrence attached to 'existing-inc', got %q", svc.appendCalls[0].incidentUUID)
	}
}

// TestAlertHandler_BelowThresholdVerdict_Spawns verifies that a below-threshold
// correlation verdict falls through to normal incident spawning.
func TestAlertHandler_BelowThresholdVerdict_Spawns(t *testing.T) {
	db := setupCorrelatorHandlerDB(t)
	seedHandlerIncident(t, db, "maybe-related", "CPU high on web01", "running", 5*time.Minute)

	caller := &corrOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"maybe-related","confidence":0.55,"reasoning":"possibly related"}`, nil
	}

	cfg := services.CorrelationConfig{
		Enabled:       true,
		Window:        30 * time.Minute,
		MaxCandidates: 20,
		Threshold:     0.7,
	}
	correlator := services.NewAlertCorrelator(caller, db, cfg)

	svc := &corrGateSkillService{spawnUUID: "new-incident-uuid"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertCorrelator(correlator)

	instance := &database.AlertSourceInstance{
		UUID:    "src-1",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newCorrTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn for below-threshold verdict, got %d", svc.getSpawnCount())
	}
	if svc.getAppendCount() != 0 {
		t.Errorf("expected 0 recurrences for below-threshold verdict, got %d", svc.getAppendCount())
	}
}

// TestAlertHandler_WorkerNotConnected_Spawns verifies that ErrWorkerNotConnected
// from the correlator is treated as "no correlation" — the alert still spawns
// a new incident (fail-open behavior).
func TestAlertHandler_WorkerNotConnected_Spawns(t *testing.T) {
	db := setupCorrelatorHandlerDB(t)
	seedHandlerIncident(t, db, "active-inc", "CPU high on web01", "running", 5*time.Minute)

	caller := &corrOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "", services.ErrWorkerNotConnected
	}

	cfg := services.CorrelationConfig{
		Enabled:       true,
		Window:        30 * time.Minute,
		MaxCandidates: 20,
		Threshold:     0.7,
	}
	correlator := services.NewAlertCorrelator(caller, db, cfg)

	svc := &corrGateSkillService{spawnUUID: "fail-open-incident"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertCorrelator(correlator)

	instance := &database.AlertSourceInstance{
		UUID:    "src-1",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newCorrTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn when worker not connected (fail-open), got %d", svc.getSpawnCount())
	}
	if svc.getAppendCount() != 0 {
		t.Errorf("expected 0 recurrences in fail-open path, got %d", svc.getAppendCount())
	}
}

// TestAlertHandler_NilCorrelator_AlwaysSpawns verifies that the handler works
// correctly when SetAlertCorrelator is never called (nil correlator).
func TestAlertHandler_NilCorrelator_AlwaysSpawns(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.SlackSettings{},
		&database.AlertCorrelationLog{},
		&database.Incident{},
	)

	svc := &corrGateSkillService{spawnUUID: "no-correlator-incident"}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	// No SetAlertCorrelator call — h.alertCorrelator is nil.

	instance := &database.AlertSourceInstance{
		UUID:    "src-uuid",
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}

	h.processAlert(instance, newCorrTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Errorf("expected 1 spawn with nil correlator, got %d", svc.getSpawnCount())
	}
	if svc.getAppendCount() != 0 {
		t.Errorf("expected 0 recurrences with nil correlator, got %d", svc.getAppendCount())
	}
}

// TestAlertHandler_SetAlertCorrelator_NilSafe verifies that SetAlertCorrelator
// accepts nil without panicking.
func TestAlertHandler_SetAlertCorrelator_NilSafe(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetAlertCorrelator(nil)
	if h.alertCorrelator != nil {
		t.Error("expected nil alertCorrelator after SetAlertCorrelator(nil)")
	}
}

// TestAlertSpawnKey verifies the key function produces stable output for
// known inputs and distinct keys for distinct inputs.
func TestAlertSpawnKey(t *testing.T) {
	k1 := alertSpawnKey("src-1", "CPUHigh", "web01")
	k2 := alertSpawnKey("src-1", "CPUHigh", "web01")
	if k1 != k2 {
		t.Error("alertSpawnKey must be deterministic")
	}

	k3 := alertSpawnKey("src-2", "CPUHigh", "web01")
	if k1 == k3 {
		t.Error("different sourceUUID must produce different key")
	}

	k4 := alertSpawnKey("src-1", "DiskFull", "web01")
	if k1 == k4 {
		t.Error("different alertName must produce different key")
	}

	k5 := alertSpawnKey("src-1", "CPUHigh", "web02")
	if k1 == k5 {
		t.Error("different targetHost must produce different key")
	}
}
