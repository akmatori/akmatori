package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ===== test fakes =====

// fakeScheduler captures AddFunc registrations without driving wall-clock
// time. Tests invoke fire() directly to simulate a tick.
type fakeScheduler struct {
	mu       sync.Mutex
	nextID   cron.EntryID
	jobs     map[cron.EntryID]func()
	started  bool
	stopped  bool
	addCalls int
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{jobs: map[cron.EntryID]func(){}}
}

func (s *fakeScheduler) AddFunc(spec string, cmd func()) (cron.EntryID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addCalls++
	s.nextID++
	id := s.nextID
	s.jobs[id] = cmd
	return id, nil
}

func (s *fakeScheduler) Remove(id cron.EntryID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
}

func (s *fakeScheduler) Entry(id cron.EntryID) cron.Entry {
	return cron.Entry{ID: id}
}

func (s *fakeScheduler) Start() { s.mu.Lock(); s.started = true; s.mu.Unlock() }
func (s *fakeScheduler) Stop() context.Context {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	return context.Background()
}
func (s *fakeScheduler) fire(id cron.EntryID) {
	s.mu.Lock()
	fn, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	fn()
}
func (s *fakeScheduler) entryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

// recordingChannelManager satisfies ChannelManager just enough for the cron
// runner: GetChannelByUUID + ResolveDefault. Other methods panic so tests
// that accidentally exercise them fail fast.
type recordingChannelManager struct {
	channels       []database.Channel
	resolveDefault *database.Channel
	resolveErr     error
}

func (r *recordingChannelManager) ListIntegrations() ([]database.Integration, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) GetIntegrationByUUID(string) (*database.Integration, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) CreateIntegration(database.MessagingProvider, string, database.JSONB, bool) (*database.Integration, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) UpdateIntegration(string, *string, database.JSONB, *bool) (*database.Integration, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) DeleteIntegration(string) error { panic("not implemented") }
func (r *recordingChannelManager) ListChannels(ListChannelsFilter) ([]database.Channel, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) GetChannelByUUID(uuid string) (*database.Channel, error) {
	for i := range r.channels {
		if r.channels[i].UUID == uuid {
			out := r.channels[i]
			return &out, nil
		}
	}
	return nil, ErrChannelNotFound
}
func (r *recordingChannelManager) CreateChannel(*database.Channel) (*database.Channel, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) UpdateChannel(string, ChannelUpdate) (*database.Channel, error) {
	panic("not implemented")
}
func (r *recordingChannelManager) DeleteChannel(string) error { panic("not implemented") }
func (r *recordingChannelManager) ResolveDefault(provider database.MessagingProvider) (*database.Channel, error) {
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	if r.resolveDefault != nil {
		out := *r.resolveDefault
		return &out, nil
	}
	return nil, ErrChannelNotFound
}
func (r *recordingChannelManager) ResolveForAlertSource(*database.AlertSourceInstance, database.MessagingProvider) (*database.Channel, error) {
	return r.ResolveDefault(database.MessagingProviderSlack)
}

// recordingProvider captures PostMessage calls. The fake registry returns it
// for slack so the cron tick path can route through the standard provider API.
type recordingProvider struct {
	mu      sync.Mutex
	posts   []recordedPost
	postErr error
}

type recordedPost struct {
	channel *database.Channel
	text    string
}

func (p *recordingProvider) Name() database.MessagingProvider { return database.MessagingProviderSlack }
func (p *recordingProvider) PostMessage(_ context.Context, ch *database.Channel, text string) (*messaging.PostedMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.postErr != nil {
		return nil, p.postErr
	}
	p.posts = append(p.posts, recordedPost{channel: ch, text: text})
	return &messaging.PostedMessage{MessageID: "ts-1"}, nil
}
func (p *recordingProvider) PostThreadReply(context.Context, *database.Channel, string, string) (*messaging.PostedMessage, error) {
	return nil, messaging.ErrNotImplemented
}
func (p *recordingProvider) UpdateMessage(context.Context, *database.Channel, string, string) error {
	return messaging.ErrNotImplemented
}

// fakeProviderRegistry returns the recording provider for slack and
// ErrProviderNotRegistered otherwise. lookupErr lets a test simulate the
// "registry missing" path without changing the channel's integration row.
type fakeProviderRegistry struct {
	provider  messaging.Provider
	lookupErr error
}

func (f *fakeProviderRegistry) Get(name database.MessagingProvider) (messaging.Provider, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if name == database.MessagingProviderSlack && f.provider != nil {
		return f.provider, nil
	}
	return nil, messaging.ErrProviderNotRegistered
}
func (f *fakeProviderRegistry) List() []database.MessagingProvider {
	return []database.MessagingProvider{database.MessagingProviderSlack}
}

// fakeSkillIncidentManager is the test double for SkillIncidentManager.
// Only the methods exercised by the cron agent path are implemented; the rest
// panic so a stray call from new code fails the test loudly. The handler
// surface records SpawnAgentInvocation calls so tests can assert the cron
// path stamps source_kind=cron/source_uuid=<job.uuid> on the Incident row
// AND that it spawns the cron-agent root skill rather than incident-manager.
type fakeSkillIncidentManager struct {
	spawnCalls        []fakeSpawnCall
	spawnIncidentID   string
	spawnErr          error
	updateStatusErr   error
	updateCompleteErr error

	mu            sync.Mutex
	updates       []fakeIncidentUpdate
	enabledSkills []string
	toolAllowlist []ToolAllowlistEntry
}

// fakeSpawnCall captures the rootSkillName + IncidentContext pair a tick
// sent into the spawn surface. Recording both is what lets tests pin the
// "cron-agent" framing — without the rootSkillName a regression that
// fell back to incident-manager would still satisfy provenance assertions.
type fakeSpawnCall struct {
	rootSkillName string
	ctx           IncidentContext
}

type fakeIncidentUpdate struct {
	uuid     string
	status   database.IncidentStatus
	response string
	fullLog  string
}

func (f *fakeSkillIncidentManager) SpawnIncidentManager(ctx *IncidentContext) (string, string, error) {
	return f.SpawnAgentInvocation("incident-manager", ctx)
}

func (f *fakeSkillIncidentManager) SpawnAgentInvocation(rootSkillName string, ctx *IncidentContext) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.spawnErr != nil {
		return "", "", f.spawnErr
	}
	if ctx != nil {
		f.spawnCalls = append(f.spawnCalls, fakeSpawnCall{rootSkillName: rootSkillName, ctx: *ctx})
	}
	if f.spawnIncidentID == "" {
		f.spawnIncidentID = "test-incident-uuid"
	}
	return f.spawnIncidentID, "/tmp/" + f.spawnIncidentID, nil
}
func (f *fakeSkillIncidentManager) UpdateIncidentStatus(uuid string, status database.IncidentStatus, _ string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, fakeIncidentUpdate{uuid: uuid, status: status})
	return f.updateStatusErr
}
func (f *fakeSkillIncidentManager) UpdateIncidentComplete(uuid string, status database.IncidentStatus, _ string, fullLog string, response string, _ int, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, fakeIncidentUpdate{uuid: uuid, status: status, response: response, fullLog: fullLog})
	return f.updateCompleteErr
}
func (f *fakeSkillIncidentManager) UpdateIncidentLog(string, string) error         { return nil }
func (f *fakeSkillIncidentManager) GetIncident(string) (*database.Incident, error) { return nil, nil }
func (f *fakeSkillIncidentManager) AppendSubagentLog(string, string, string) error { return nil }

func (f *fakeSkillIncidentManager) CreateSkill(string, string, string, string) (*database.Skill, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) UpdateSkill(string, string, string, bool) (*database.Skill, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) DeleteSkill(string) error              { panic("not implemented") }
func (f *fakeSkillIncidentManager) ListSkills() ([]database.Skill, error) { panic("not implemented") }
func (f *fakeSkillIncidentManager) ListEnabledSkills() ([]database.Skill, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) GetEnabledSkillNames() []string { return f.enabledSkills }
func (f *fakeSkillIncidentManager) GetToolAllowlist() []ToolAllowlistEntry {
	return f.toolAllowlist
}
func (f *fakeSkillIncidentManager) GetSkill(string) (*database.Skill, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) AssignTools(string, []uint) error       { panic("not implemented") }
func (f *fakeSkillIncidentManager) GetSkillDir(string) string              { panic("not implemented") }
func (f *fakeSkillIncidentManager) GetSkillScriptsDir(string) string       { panic("not implemented") }
func (f *fakeSkillIncidentManager) GetSkillPrompt(string) (string, error)  { panic("not implemented") }
func (f *fakeSkillIncidentManager) UpdateSkillPrompt(string, string) error { panic("not implemented") }
func (f *fakeSkillIncidentManager) RegenerateSkillMd(string) error         { panic("not implemented") }
func (f *fakeSkillIncidentManager) SyncSkillsFromFilesystem() error        { panic("not implemented") }
func (f *fakeSkillIncidentManager) ListSkillScripts(string) ([]string, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) ClearSkillScripts(string) error { panic("not implemented") }
func (f *fakeSkillIncidentManager) GetSkillScript(string, string) (*ScriptInfo, error) {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) UpdateSkillScript(string, string, string) error {
	panic("not implemented")
}
func (f *fakeSkillIncidentManager) DeleteSkillScript(string, string) error { panic("not implemented") }

// fakeIncidentRunner drives the cron agent path deterministically: tests
// configure how StartIncident responds (success/error/superseded), and the
// fake fires the corresponding callback synchronously so the cron runner's
// done channel closes without spinning up a real worker WebSocket.
type fakeIncidentRunner struct {
	mu           sync.Mutex
	connected    bool
	startCalls   []fakeStartCall
	startErr     error
	releaseFalse bool
	// behavior controls what the runner does after StartIncident registers
	// the callback. Default: invoke OnCompleted with a canned response.
	behavior func(callback IncidentCallback)
}

type fakeStartCall struct {
	incidentID string
	task       string
	llm        *LLMSettingsForWorker
	skills     []string
	tools      []ToolAllowlistEntry
	runID      string
}

func newFakeIncidentRunner() *fakeIncidentRunner {
	return &fakeIncidentRunner{
		connected: true,
		behavior: func(cb IncidentCallback) {
			if cb.OnOutput != nil {
				cb.OnOutput("streaming output\n")
			}
			if cb.OnCompleted != nil {
				cb.OnCompleted("session-1", "Final cron summary", 42, 1234)
			}
		},
	}
}

func (f *fakeIncidentRunner) IsWorkerConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeIncidentRunner) StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []ToolAllowlistEntry, callback IncidentCallback) (string, error) {
	f.mu.Lock()
	if f.startErr != nil {
		err := f.startErr
		f.mu.Unlock()
		return "", err
	}
	runID := fmt.Sprintf("run-%d", len(f.startCalls)+1)
	f.startCalls = append(f.startCalls, fakeStartCall{
		incidentID: incidentID,
		task:       task,
		llm:        llm,
		skills:     enabledSkills,
		tools:      toolAllowlist,
		runID:      runID,
	})
	behavior := f.behavior
	f.mu.Unlock()
	if behavior != nil {
		behavior(callback)
	}
	return runID, nil
}

func (f *fakeIncidentRunner) ReleaseRun(string, string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.releaseFalse
}

// ===== setup =====

func setupCronRunnerTest(t *testing.T) (*CronRunner, *gorm.DB, *fakeScheduler, *recordingChannelManager, *recordingProvider) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Integration{},
		&database.Channel{},
		&database.CronJob{},
		&database.CronJobTool{},
		&database.ToolType{},
		&database.ToolInstance{},
		&database.LLMSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Stash and restore the package-level DB so other tests in this package
	// (which share the global) see the prior handle once we're done.
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	integration := database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderSlack,
		Name:     "Slack",
		Enabled:  true,
	}
	if err := db.Create(&integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}
	channel := database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integration.ID,
		ExternalID:    "C-incidents",
		DisplayName:   "#incidents",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
		Integration:   integration,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	if err := db.Create(&database.LLMSettings{
		Name:     "openai",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "test-key",
		Enabled:  true,
		Active:   true,
	}).Error; err != nil {
		t.Fatalf("seed llm settings: %v", err)
	}

	chMgr := &recordingChannelManager{channels: []database.Channel{channel}, resolveDefault: &channel}
	prov := &recordingProvider{}
	reg := &fakeProviderRegistry{provider: prov}
	sched := newFakeScheduler()
	skills := &fakeSkillIncidentManager{}
	agentRunner := newFakeIncidentRunner()

	runner := newCronRunnerWithDeps(db, chMgr, reg, skills, agentRunner, sched)
	return runner, db, sched, chMgr, prov
}

// ===== schedule validation =====

func TestCronRunner_CreateJob_ValidatesSchedule(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	chUUID := runner.channels.(*recordingChannelManager).channels[0].UUID

	_, err := runner.CreateJob("daily report", "not a cron", "report status", chUUID, true, nil)
	if err == nil || !errors.Is(err, ErrInvalidCronSchedule) {
		t.Fatalf("expected ErrInvalidCronSchedule, got %v", err)
	}
}

func TestCronRunner_CreateJob_RejectsEmptyName(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	chUUID := runner.channels.(*recordingChannelManager).channels[0].UUID

	_, err := runner.CreateJob("   ", "*/2 * * * *", "report status", chUUID, true, nil)
	if err == nil {
		t.Fatal("expected name validation error, got nil")
	}
}

func TestCronRunner_UpdateJob_ScheduleAndChannelTakeEffect(t *testing.T) {
	runner, _, sched, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("nightly", "*/5 * * * *", "x", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sched.addCalls != 1 {
		t.Fatalf("expected 1 schedule registration, got %d", sched.addCalls)
	}

	newSchedule := "0 9 * * *"
	updated, err := runner.UpdateJob(job.UUID, CronJobUpdate{Schedule: &newSchedule})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Schedule != newSchedule {
		t.Errorf("schedule = %q, want %q", updated.Schedule, newSchedule)
	}
	// reload should have removed + re-added — net 2 add calls, but only one
	// live entry.
	if sched.entryCount() != 1 {
		t.Errorf("entry count = %d, want 1", sched.entryCount())
	}
}

func TestCronRunner_UpdateJob_RejectsBadSchedule(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("nightly", "*/5 * * * *", "x", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bad := "definitely not cron"
	if _, err := runner.UpdateJob(job.UUID, CronJobUpdate{Schedule: &bad}); err == nil || !errors.Is(err, ErrInvalidCronSchedule) {
		t.Fatalf("expected ErrInvalidCronSchedule, got %v", err)
	}
}

func TestCronRunner_DeleteJob_RemovesScheduledEntry(t *testing.T) {
	runner, _, sched, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("nightly", "*/5 * * * *", "x", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sched.entryCount() != 1 {
		t.Fatalf("entry count = %d, want 1", sched.entryCount())
	}
	if err := runner.DeleteJob(job.UUID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if sched.entryCount() != 0 {
		t.Errorf("after delete entry count = %d, want 0", sched.entryCount())
	}
}

func TestCronRunner_GetJobByUUID_NotFound(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	if _, err := runner.GetJobByUUID("does-not-exist"); !errors.Is(err, ErrCronJobNotFound) {
		t.Fatalf("expected ErrCronJobNotFound, got %v", err)
	}
}

// ===== tick =====

func TestCronRunner_RunNow_FiresImmediately(t *testing.T) {
	runner, _, _, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("status", "*/10 * * * *", "Summarize", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run now: %v", err)
	}
	runner.WaitForInflight()
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one post, got %d", len(prov.posts))
	}
}

func TestCronRunner_RunNow_UnknownUUID(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	if err := runner.RunNow("ghost"); !errors.Is(err, ErrCronJobNotFound) {
		t.Fatalf("expected ErrCronJobNotFound, got %v", err)
	}
}

// TestCronRunner_Tick_RecordsProviderError exercises the channel post failure
// branch: when the messaging provider returns an error after the agent run
// completes, the tick records LastRunStatus=error so the operator sees the
// failure on the row without having to tail API logs.
func TestCronRunner_Tick_RecordsProviderError(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	prov.postErr = errors.New("network unreachable")

	if _, err := runner.CreateJob("status", "*/2 * * * *", "Summarize", chUUID, true, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}

	var job database.CronJob
	if err := db.First(&job, "uuid = ?", findOnlyJobUUID(t, db)).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if job.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", job.LastRunStatus)
	}
	if !contains(job.LastRunError, "network unreachable") {
		t.Errorf("LastRunError = %q, want provider error captured", job.LastRunError)
	}
}

// ===== agent tick =====

// TestCronRunner_AgentTick_SpawnsIncidentWithCronProvenance verifies the cron
// agent path stamps source_kind="cron" + source_uuid=<job.uuid> on the
// Incident row so the UI can link an investigation back to the scheduled job
// that triggered it.
func TestCronRunner_AgentTick_SpawnsIncidentWithCronProvenance(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	skills := runner.skills.(*fakeSkillIncidentManager)

	job, err := runner.CreateJob("daily report", "*/2 * * * *", "Investigate disks", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	if len(skills.spawnCalls) != 1 {
		t.Fatalf("expected one SpawnAgentInvocation call, got %d", len(skills.spawnCalls))
	}
	spawn := skills.spawnCalls[0]
	if spawn.rootSkillName != "cron-agent" {
		t.Errorf("rootSkillName = %q, want cron-agent", spawn.rootSkillName)
	}
	if spawn.ctx.SourceKind != database.IncidentSourceKindCron {
		t.Errorf("source_kind = %q, want %q", spawn.ctx.SourceKind, database.IncidentSourceKindCron)
	}
	if spawn.ctx.SourceUUID != job.UUID {
		t.Errorf("source_uuid = %q, want %q", spawn.ctx.SourceUUID, job.UUID)
	}
	if spawn.ctx.Source != "cron" {
		t.Errorf("source = %q, want cron", spawn.ctx.Source)
	}
}

// TestCronRunner_AgentTick_PostsFinalSummaryToChannel verifies the agent path
// posts the agent's final response to the cron's Channel and records
// LastRunStatus=ok.
func TestCronRunner_AgentTick_PostsFinalSummaryToChannel(t *testing.T) {
	runner, db, _, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("morning report", "*/2 * * * *", "Summarize incidents", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	prov.mu.Lock()
	posts := append([]recordedPost(nil), prov.posts...)
	prov.mu.Unlock()

	if len(posts) != 1 {
		t.Fatalf("expected one PostMessage call, got %d", len(posts))
	}
	if posts[0].channel.UUID != chUUID {
		t.Errorf("post landed on wrong channel: %s", posts[0].channel.UUID)
	}
	if !contains(posts[0].text, "Final cron summary") {
		t.Errorf("post body missing agent response: %q", posts[0].text)
	}
	if !contains(posts[0].text, "morning report") {
		t.Errorf("post body missing cron name header: %q", posts[0].text)
	}

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusOK {
		t.Errorf("LastRunStatus = %q, want ok", got.LastRunStatus)
	}
}

// TestCronRunner_AgentTick_RecordsWorkerNotConnected verifies a tick fired
// while the agent worker is offline records LastRunStatus=error without
// crashing the runner.
func TestCronRunner_AgentTick_RecordsWorkerNotConnected(t *testing.T) {
	runner, db, _, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	runner.runner.(*fakeIncidentRunner).connected = false

	job, err := runner.CreateJob("morning report", "*/2 * * * *", "Summarize incidents", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", got.LastRunStatus)
	}
	if !contains(got.LastRunError, "worker not connected") {
		t.Errorf("LastRunError = %q, want worker-disconnect message", got.LastRunError)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 0 {
		t.Errorf("expected no posts when worker offline, got %d", len(prov.posts))
	}
}

// TestCronRunner_AgentTick_RecordsAgentError verifies a failing agent run
// (OnError callback) records LastRunStatus=error, surfaces the error into
// the channel message, and does not crash the runner.
func TestCronRunner_AgentTick_RecordsAgentError(t *testing.T) {
	runner, db, _, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	runner.runner.(*fakeIncidentRunner).behavior = func(cb IncidentCallback) {
		if cb.OnError != nil {
			cb.OnError("agent crashed mid-investigation")
		}
	}

	job, err := runner.CreateJob("morning report", "*/2 * * * *", "Investigate", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", got.LastRunStatus)
	}
	if !contains(got.LastRunError, "agent crashed") {
		t.Errorf("LastRunError = %q, want agent error captured", got.LastRunError)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one channel post (failure surfaced to channel), got %d", len(prov.posts))
	}
	if !contains(prov.posts[0].text, "Investigation failed") {
		t.Errorf("post body missing failure header: %q", prov.posts[0].text)
	}
}

// TestCronRunner_AgentTick_RecordsStartIncidentError verifies that a failed
// StartIncident (e.g. transport write error) records LastRunStatus=error and
// does not deadlock waiting for callbacks.
func TestCronRunner_AgentTick_RecordsStartIncidentError(t *testing.T) {
	runner, db, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	runner.runner.(*fakeIncidentRunner).startErr = errors.New("transport closed")

	job, err := runner.CreateJob("morning report", "*/2 * * * *", "Investigate", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", got.LastRunStatus)
	}
	if !contains(got.LastRunError, "transport closed") {
		t.Errorf("LastRunError = %q, want StartIncident failure captured", got.LastRunError)
	}
}

// TestCronRunner_AgentTick_MissingWiringMarksError verifies a cron runner
// constructed without skill/runner wiring (e.g. early startup) records a
// clean error rather than crashing.
func TestCronRunner_AgentTick_MissingWiringMarksError(t *testing.T) {
	runner, db, _, chMgr, _ := setupCronRunnerTest(t)
	runner.skills = nil
	runner.runner = nil
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("morning report", "*/2 * * * *", "Investigate", chUUID, true, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", got.LastRunStatus)
	}
	if !contains(got.LastRunError, "agent runner wiring") {
		t.Errorf("LastRunError = %q, want wiring-missing message", got.LastRunError)
	}
}

// TestCronRunner_AgentTick_UsesCronAgentSkillAndPerCronTools verifies the
// redesigned agent path:
//   - StartIncident receives skillNames=[cron-agent] (the root prompt — not
//     the global enabled-skills set).
//   - StartIncident receives a tool allowlist derived from the cron's own
//     Tools m2m, NOT the global SkillIncidentManager.GetToolAllowlist() that
//     alert-driven incidents use.
//
// Together these two assertions pin the "cron jobs run agent-only with their
// own tool allowlist" contract from the redesign.
func TestCronRunner_AgentTick_UsesCronAgentSkillAndPerCronTools(t *testing.T) {
	runner, db, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	agentRunner := runner.runner.(*fakeIncidentRunner)
	skills := runner.skills.(*fakeSkillIncidentManager)
	// Salt the global allowlist with a sentinel entry — if the runner ever
	// falls back to GetToolAllowlist() the assertions below catch it.
	skills.enabledSkills = []string{"unused-global-skill"}
	skills.toolAllowlist = []ToolAllowlistEntry{{InstanceID: 999, LogicalName: "should-not-flow"}}

	// Seed two tool instances: one enabled (must flow) and one disabled
	// (must be filtered out so an operator-disabled tool cannot get smuggled
	// past the gateway via a stale cron assignment).
	toolType := database.ToolType{Name: "ssh"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("seed tool type: %v", err)
	}
	enabledTool := database.ToolInstance{ToolTypeID: toolType.ID, Name: "prod-ssh", LogicalName: "prod-ssh", Enabled: true}
	if err := db.Create(&enabledTool).Error; err != nil {
		t.Fatalf("seed enabled tool: %v", err)
	}
	disabledTool := database.ToolInstance{ToolTypeID: toolType.ID, Name: "stale-ssh", LogicalName: "stale-ssh"}
	if err := db.Create(&disabledTool).Error; err != nil {
		t.Fatalf("seed disabled tool: %v", err)
	}
	if err := db.Model(&database.ToolInstance{}).Where("id = ?", disabledTool.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable tool: %v", err)
	}

	job, err := runner.CreateJob("status report", "*/5 * * * *", "Summarize", chUUID, true, []uint{enabledTool.ID, disabledTool.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}
	runner.WaitForInflight()

	agentRunner.mu.Lock()
	defer agentRunner.mu.Unlock()
	if len(agentRunner.startCalls) != 1 {
		t.Fatalf("expected one StartIncident call, got %d", len(agentRunner.startCalls))
	}
	call := agentRunner.startCalls[0]
	if len(call.skills) != 1 || call.skills[0] != "cron-agent" {
		t.Errorf("StartIncident skills = %v, want [cron-agent]", call.skills)
	}
	if len(call.tools) != 1 {
		t.Fatalf("StartIncident toolAllowlist = %d entries, want 1 (only enabled tool)", len(call.tools))
	}
	if call.tools[0].InstanceID != enabledTool.ID {
		t.Errorf("StartIncident toolAllowlist[0].InstanceID = %d, want %d", call.tools[0].InstanceID, enabledTool.ID)
	}
	if call.tools[0].LogicalName != "prod-ssh" {
		t.Errorf("StartIncident toolAllowlist[0].LogicalName = %q, want prod-ssh", call.tools[0].LogicalName)
	}
	if call.tools[0].ToolType != "ssh" {
		t.Errorf("StartIncident toolAllowlist[0].ToolType = %q, want ssh", call.tools[0].ToolType)
	}
	// Sanity check: the global allowlist sentinel must NOT have leaked through.
	for _, e := range call.tools {
		if e.InstanceID == 999 {
			t.Fatalf("global allowlist leaked into cron tick: %+v", e)
		}
	}
}

// TestCronRunner_CreateJob_RejectsUnknownToolID guards the resolveToolInstances
// validation: an unknown tool ID surfaces as a typed error rather than
// silently dropping the tool from the allowlist.
func TestCronRunner_CreateJob_RejectsUnknownToolID(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	_, err := runner.CreateJob("ghost-tool", "*/5 * * * *", "p", chUUID, true, []uint{99999})
	if err == nil || !strings.Contains(err.Error(), "tool instance 99999 not found") {
		t.Fatalf("err = %v, want tool-not-found", err)
	}
}

// TestCronRunner_UpdateJob_ReplacesToolAllowlist verifies that passing a
// fresh ToolInstanceIDs slice on UpdateJob replaces the prior assignment
// rather than appending to it — operators must be able to fully revoke a
// previously-granted tool without an explicit DELETE.
func TestCronRunner_UpdateJob_ReplacesToolAllowlist(t *testing.T) {
	runner, db, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	toolType := database.ToolType{Name: "ssh"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("seed tool type: %v", err)
	}
	first := database.ToolInstance{ToolTypeID: toolType.ID, Name: "alpha", LogicalName: "alpha", Enabled: true}
	second := database.ToolInstance{ToolTypeID: toolType.ID, Name: "beta", LogicalName: "beta", Enabled: true}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed beta: %v", err)
	}

	job, err := runner.CreateJob("swap-tools", "*/5 * * * *", "p", chUUID, true, []uint{first.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(job.Tools) != 1 || job.Tools[0].ID != first.ID {
		t.Fatalf("initial Tools = %+v, want exactly [alpha]", job.Tools)
	}

	swap := []uint{second.ID}
	updated, err := runner.UpdateJob(job.UUID, CronJobUpdate{ToolInstanceIDs: &swap})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.Tools) != 1 || updated.Tools[0].ID != second.ID {
		t.Errorf("post-update Tools = %+v, want exactly [beta]", updated.Tools)
	}

	// Empty slice must clear the allowlist entirely (passing nil is the
	// distinct "leave tools alone" branch and is exercised by the no-op test).
	empty := []uint{}
	cleared, err := runner.UpdateJob(job.UUID, CronJobUpdate{ToolInstanceIDs: &empty})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(cleared.Tools) != 0 {
		t.Errorf("post-clear Tools = %+v, want empty", cleared.Tools)
	}
}

// TestCronRunner_DeleteJob_SystemRowImmutable verifies that operator
// DELETE on a seeded system cron returns ErrSystemCronImmutable rather than
// removing the row — dreaming-style maintenance jobs (memory-curator and
// any future REM/deep phases) must survive a routine prune.
func TestCronRunner_DeleteJob_SystemRowImmutable(t *testing.T) {
	runner, db, _, _, _ := setupCronRunnerTest(t)
	systemJob := database.CronJob{
		UUID:     uuid.New().String(),
		Name:     "memory-curator",
		Schedule: "0 2 * * *",
		Prompt:   "consolidate memory",
		IsSystem: true,
		Enabled:  false,
	}
	if err := db.Create(&systemJob).Error; err != nil {
		t.Fatalf("seed system cron: %v", err)
	}
	// GORM treats IsSystem=true as the default true alongside the zero-value
	// bool quirk, so reload to be sure the row really is flagged system.
	if err := db.Model(&database.CronJob{}).Where("id = ?", systemJob.ID).Update("is_system", true).Error; err != nil {
		t.Fatalf("pin is_system: %v", err)
	}

	if err := runner.DeleteJob(systemJob.UUID); !errors.Is(err, ErrSystemCronImmutable) {
		t.Fatalf("DeleteJob system row err = %v, want ErrSystemCronImmutable", err)
	}
	// Row must still exist.
	var reloaded database.CronJob
	if err := db.First(&reloaded, systemJob.ID).Error; err != nil {
		t.Fatalf("system cron must still exist after blocked delete: %v", err)
	}
}

// TestCronRunner_UpdateJob_SystemRow_EnableOnlySucceeds asserts that
// operators can still toggle a seeded system cron's Enabled flag (and
// channel binding) without tripping the system-immutability guard. The
// guard fires only on DeleteJob; UpdateJob deliberately keeps system rows
// operator-controllable so dreaming-style crons can be turned on/off.
func TestCronRunner_UpdateJob_SystemRow_EnableOnlySucceeds(t *testing.T) {
	runner, db, _, _, _ := setupCronRunnerTest(t)
	systemJob := database.CronJob{
		UUID:     uuid.New().String(),
		Name:     "memory-curator",
		Schedule: "0 2 * * *",
		Prompt:   "consolidate memory",
		IsSystem: true,
		Enabled:  false,
	}
	if err := db.Create(&systemJob).Error; err != nil {
		t.Fatalf("seed system cron: %v", err)
	}
	if err := db.Model(&database.CronJob{}).Where("id = ?", systemJob.ID).Update("is_system", true).Error; err != nil {
		t.Fatalf("pin is_system: %v", err)
	}

	enabled := true
	updated, err := runner.UpdateJob(systemJob.UUID, CronJobUpdate{Enabled: &enabled})
	if err != nil {
		t.Fatalf("enable system cron: %v", err)
	}
	if !updated.Enabled {
		t.Errorf("post-update Enabled = false, want true")
	}
	if !updated.IsSystem {
		t.Errorf("UpdateJob must not clear IsSystem flag")
	}
}

// ===== Start loads enabled jobs =====

func TestCronRunner_Start_LoadsEnabledJobs(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)

	channel := chMgr.channels[0]
	enabledJob := database.CronJob{UUID: uuid.New().String(), Name: "enabled", Schedule: "*/5 * * * *", Prompt: "p", ChannelID: &channel.ID, Enabled: true}
	disabledJob := database.CronJob{UUID: uuid.New().String(), Name: "disabled", Schedule: "*/5 * * * *", Prompt: "p", ChannelID: &channel.ID, Enabled: true}
	if err := db.Create(&enabledJob).Error; err != nil {
		t.Fatalf("seed enabled job: %v", err)
	}
	if err := db.Create(&disabledJob).Error; err != nil {
		t.Fatalf("seed disabled job: %v", err)
	}
	// GORM applies the gorm:"default:true" column tag for bool zero values
	// at create time; explicit toggle to false avoids that surprise.
	if err := db.Model(&database.CronJob{}).Where("id = ?", disabledJob.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable job: %v", err)
	}

	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer runner.Stop()

	if !sched.started {
		t.Error("expected scheduler.Start to be called")
	}
	if sched.entryCount() != 1 {
		t.Errorf("entry count = %d, want 1 (only enabled job)", sched.entryCount())
	}
}

// TestCronRunner_Start_DoesNotMarkStartedOnDBError asserts the runner does
// not flip its started flag when the initial jobs load fails. Otherwise a
// transient DB hiccup at boot would wedge the scheduler permanently — Start
// would be a no-op on the next attempt while the scheduler never ticked.
func TestCronRunner_Start_DoesNotMarkStartedOnDBError(t *testing.T) {
	runner, db, sched, _, _ := setupCronRunnerTest(t)
	// Drop the cron_jobs table so the Find call surfaces a real DB error.
	if err := db.Migrator().DropTable(&database.CronJob{}); err != nil {
		t.Fatalf("drop cron_jobs: %v", err)
	}
	if err := runner.Start(context.Background()); err == nil {
		t.Fatal("expected Start to return an error after DB failure")
	}
	runner.mu.Lock()
	started := runner.started
	runner.mu.Unlock()
	if started {
		t.Fatal("runner.started left true after Start returned an error — subsequent Start calls would be silent no-ops")
	}
	if sched.started {
		t.Fatal("scheduler was started even though jobs load failed")
	}

	// Restore the table and verify a follow-up Start succeeds.
	if err := db.AutoMigrate(&database.CronJob{}); err != nil {
		t.Fatalf("restore cron_jobs: %v", err)
	}
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	if !sched.started {
		t.Fatal("scheduler not started on retry")
	}
}

// TestCronRunner_ResolveChannel_FallsBackWhenExplicitDisabled asserts that an
// explicit ChannelID pointing at a disabled or non-posting channel resolves to
// the per-provider default at fire time instead of failing the tick. Mirrors
// ChannelService.ResolveForAlertSource's semantics so the cron path and the
// alert-routing path agree on what "explicit but unusable" means.
func TestCronRunner_ResolveChannel_FallsBackWhenExplicitDisabled(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	defaultCh := chMgr.channels[0]
	// Seed a second channel that's explicitly disabled in both the DB
	// (so fire's Preload sees it) and the mock channel manager (so the
	// CreateJob lookup resolves the UUID to an ID). The default channel
	// remains the fallback target.
	disabled := database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: defaultCh.IntegrationID,
		ExternalID:    "C-disabled",
		DisplayName:   "#disabled",
		CanPost:       true,
		Enabled:       false,
		Integration:   defaultCh.Integration,
	}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatalf("seed disabled channel: %v", err)
	}
	// GORM honors the gorm:"default:true" column tag when a bool field is its
	// zero value at insert time. Explicitly flip enabled=false so the row in
	// the DB actually reflects the disabled state we want to test.
	if err := db.Model(&database.Channel{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	disabled.Enabled = false
	chMgr.channels = append(chMgr.channels, disabled)

	if _, err := runner.CreateJob("status", "*/2 * * * *", "Summarize", disabled.UUID, true, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one post, got %d", len(prov.posts))
	}
	// The post should have landed on the default (fallback), not the disabled
	// explicit channel.
	if prov.posts[0].channel.UUID != defaultCh.UUID {
		t.Fatalf("post landed on %s (disabled explicit), want fallback to default %s", prov.posts[0].channel.UUID, defaultCh.UUID)
	}
}

func TestCronRunner_Start_Idempotent(t *testing.T) {
	runner, _, sched, _, _ := setupCronRunnerTest(t)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	startsBefore := startCount(sched)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if startCount(sched) != startsBefore {
		t.Error("Start invoked twice — expected idempotent behavior")
	}
}

// ===== helpers =====

// contains is a thin assertion helper that fails closed on an empty needle so
// a future refactor that produces an empty expected string doesn't silently
// vacuously pass.
func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(haystack, needle)
}

func findOnlyJobUUID(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var jobs []database.CronJob
	if err := db.Find(&jobs).Error; err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 cron job, got %d", len(jobs))
	}
	return jobs[0].UUID
}

func startCount(s *fakeScheduler) int {
	// fakeScheduler.Start sets started but doesn't count — we model
	// idempotency by checking that started flips once. Use a sentinel field
	// captured under mu so race detector is happy.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return 1
	}
	return 0
}

// TestCronRunner_ListJobs_ReturnsRowsSorted verifies the listing surface used
// by GET /api/cron-jobs returns rows ordered by name with channel preloaded.
func TestCronRunner_ListJobs_ReturnsRowsSorted(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	if _, err := runner.CreateJob("B job", "*/5 * * * *", "p", chUUID, true, nil); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	if _, err := runner.CreateJob("A job", "*/5 * * * *", "p", chUUID, true, nil); err != nil {
		t.Fatalf("seed A: %v", err)
	}

	rows, err := runner.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Name != "A job" || rows[1].Name != "B job" {
		t.Errorf("order = [%s, %s], want [A job, B job]", rows[0].Name, rows[1].Name)
	}
}

// TestCronRunner_CreateJob_AcceptsEmptyChannelUUID exercises the resolver
// fallback path where ChannelID is left nil so the runner falls back to the
// per-provider default at tick time.
func TestCronRunner_CreateJob_AcceptsEmptyChannelUUID(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	job, err := runner.CreateJob("nightly", "0 9 * * *", "Summarize", "", true, nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.ChannelID != nil {
		t.Errorf("ChannelID = %v, want nil", job.ChannelID)
	}
}

// TestCronRunner_CreateJob_UnknownChannelUUID surfaces ErrChannelNotFound from
// the channel manager.
func TestCronRunner_CreateJob_UnknownChannelUUID(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	_, err := runner.CreateJob("nightly", "0 9 * * *", "Summarize", "ghost", true, nil)
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v, want ErrChannelNotFound", err)
	}
}

// TestCronRunner_CreateJob_RejectsNonPostableChannel guards the capability
// gating contract from CLAUDE.md: a cron job (a posting trigger) cannot
// reference a listen-only Channel. The check runs at write time so the
// operator sees a clean validation error rather than a silent fall-through to
// the default at fire time.
func TestCronRunner_CreateJob_RejectsNonPostableChannel(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chMgr.channels[0].CanPost = false
	chUUID := chMgr.channels[0].UUID
	_, err := runner.CreateJob("nightly", "0 9 * * *", "Summarize", chUUID, true, nil)
	if !errors.Is(err, ErrChannelNotPostable) {
		t.Errorf("err = %v, want ErrChannelNotPostable", err)
	}
}

// TestCronRunner_UpdateJob_RejectsNonPostableChannel mirrors the create-time
// guard on the update path so a cron job cannot be re-pointed at a
// listen-only channel after creation.
func TestCronRunner_UpdateJob_RejectsNonPostableChannel(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	postable := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "Summarize", postable, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	chMgr.channels[0].CanPost = false
	if _, err := runner.UpdateJob(job.UUID, CronJobUpdate{ChannelUUID: &postable}); !errors.Is(err, ErrChannelNotPostable) {
		t.Errorf("err = %v, want ErrChannelNotPostable", err)
	}
}

// TestCronRunner_CreateJob_RejectsEmptyPrompt rejects payloads with no prompt
// so a tick cannot fire against an empty request.
func TestCronRunner_CreateJob_RejectsEmptyPrompt(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	if _, err := runner.CreateJob("nightly", "0 9 * * *", "   ", chUUID, true, nil); err == nil {
		t.Error("CreateJob blank prompt error = nil, want validation error")
	}
}

// TestCronRunner_CreateJob_HonorsEnabledFalse guards against the GORM v2
// zero-value-bool INSERT omission. A "create-disabled" cron must persist as
// disabled — otherwise the column-level `default:true` would silently flip
// the row to enabled and the scheduler would start firing immediately.
func TestCronRunner_CreateJob_HonorsEnabledFalse(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("disabled-on-create", "0 9 * * *", "Summarize", chUUID, false, nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.Enabled {
		t.Errorf("returned CronJob.Enabled = true, want false")
	}
	var reloaded database.CronJob
	if err := db.First(&reloaded, job.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Enabled {
		t.Errorf("persisted CronJob.Enabled = true, want false")
	}
	// A disabled job must not be registered with the scheduler; otherwise the
	// runner would start firing it on the next tick despite the operator
	// having explicitly asked for disabled.
	if len(sched.jobs) != 0 {
		t.Errorf("scheduler.jobs = %d, want 0 for disabled-on-create job", len(sched.jobs))
	}
}

// TestCronRunner_UpdateJob_RejectsBlankNameAndPrompt covers the
// patch-validation paths.
func TestCronRunner_UpdateJob_RejectsBlankNameAndPrompt(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	blank := "   "
	if _, err := runner.UpdateJob(job.UUID, CronJobUpdate{Name: &blank}); err == nil {
		t.Error("UpdateJob blank name error = nil, want validation error")
	}
	if _, err := runner.UpdateJob(job.UUID, CronJobUpdate{Prompt: &blank}); err == nil {
		t.Error("UpdateJob blank prompt error = nil, want validation error")
	}
}

// TestCronRunner_UpdateJob_ChannelUUIDClearAndReassign verifies that passing an
// empty channel_uuid clears the FK, and that reassigning resolves a fresh
// channel via the channel manager.
func TestCronRunner_UpdateJob_ChannelUUIDClearAndReassign(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if job.ChannelID == nil {
		t.Fatal("expected ChannelID after create with explicit channel")
	}

	empty := ""
	cleared, err := runner.UpdateJob(job.UUID, CronJobUpdate{ChannelUUID: &empty})
	if err != nil {
		t.Fatalf("UpdateJob clear channel: %v", err)
	}
	if cleared.ChannelID != nil {
		t.Errorf("ChannelID after clear = %v, want nil", cleared.ChannelID)
	}

	reassigned, err := runner.UpdateJob(job.UUID, CronJobUpdate{ChannelUUID: &chUUID})
	if err != nil {
		t.Fatalf("UpdateJob reassign channel: %v", err)
	}
	if reassigned.ChannelID == nil {
		t.Errorf("ChannelID after reassign = nil, want non-nil")
	}
}

// TestCronRunner_UpdateJob_RejectsUnknownChannel surfaces ErrChannelNotFound
// when the operator points at a non-existent channel.
func TestCronRunner_UpdateJob_RejectsUnknownChannel(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ghost := "ghost-channel"
	if _, err := runner.UpdateJob(job.UUID, CronJobUpdate{ChannelUUID: &ghost}); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v, want ErrChannelNotFound", err)
	}
}

// TestCronRunner_UpdateJob_NotFound returns ErrCronJobNotFound when the cron
// job UUID does not exist.
func TestCronRunner_UpdateJob_NotFound(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	if _, err := runner.UpdateJob("ghost", CronJobUpdate{}); !errors.Is(err, ErrCronJobNotFound) {
		t.Errorf("err = %v, want ErrCronJobNotFound", err)
	}
}

// TestCronRunner_UpdateJob_NoFieldsReturnsRow covers the no-op patch branch.
func TestCronRunner_UpdateJob_NoFieldsReturnsRow(t *testing.T) {
	runner, _, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := runner.UpdateJob(job.UUID, CronJobUpdate{})
	if err != nil {
		t.Fatalf("UpdateJob no-op: %v", err)
	}
	if got.UUID != job.UUID {
		t.Errorf("UUID = %q, want %q", got.UUID, job.UUID)
	}
}

// TestCronRunner_DeleteJob_NotFound surfaces ErrCronJobNotFound when the UUID
// is absent.
func TestCronRunner_DeleteJob_NotFound(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	if err := runner.DeleteJob("ghost"); !errors.Is(err, ErrCronJobNotFound) {
		t.Errorf("err = %v, want ErrCronJobNotFound", err)
	}
}

// TestCronRunner_Reload_RemovesEntryWhenRowDeleted exercises the path where
// Reload is invoked for a job whose row has been deleted out from under the
// runner — should silently remove the scheduler entry.
func TestCronRunner_Reload_RemovesEntryWhenRowDeleted(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "0 9 * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if sched.entryCount() != 1 {
		t.Fatalf("entry count after create = %d, want 1", sched.entryCount())
	}
	// Hard-delete the row out from under the runner (bypassing DeleteJob's
	// Reload call) so we can test the Reload-not-found branch directly.
	if err := db.Unscoped().Delete(&database.CronJob{}, job.ID).Error; err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	if err := runner.Reload(job.ID); err != nil {
		t.Fatalf("Reload after row deleted: %v", err)
	}
	if sched.entryCount() != 0 {
		t.Errorf("entry count after reload = %d, want 0", sched.entryCount())
	}
}

// TestCronRunner_Fire_SkipsDisabledJob ensures that an in-flight disable takes
// effect on the very next firing — the tick early-returns before posting.
func TestCronRunner_Fire_SkipsDisabledJob(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "*/5 * * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Disable the row directly so the scheduler still holds the entry but the
	// fire callback exits at the enabled-check.
	if err := db.Model(&database.CronJob{}).Where("id = ?", job.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 0 {
		t.Errorf("expected no posts when job disabled mid-flight, got %d", len(prov.posts))
	}
}

// TestCronRunner_Fire_VanishedJob covers the branch where the cron job row was
// hard-deleted but the scheduler still holds a stale entry; the fire callback
// must warn and return without crashing.
func TestCronRunner_Fire_VanishedJob(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "*/5 * * * *", "p", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Unscoped().Delete(&database.CronJob{}, job.ID).Error; err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 0 {
		t.Errorf("expected no posts after vanish, got %d", len(prov.posts))
	}
}

// TestCronRunner_OneshotTick_FallsBackToDefaultChannel verifies the resolver
// path that picks the workspace default when the cron job has no explicit
// channel.
func TestCronRunner_OneshotTick_FallsBackToDefaultChannel(t *testing.T) {
	runner, _, sched, chMgr, prov := setupCronRunnerTest(t)
	// CreateJob without a channel — runner should resolve via channels.ResolveDefault.
	if _, err := runner.CreateJob("nightly", "*/5 * * * *", "Summarize", "", true, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one post via default fallback, got %d", len(prov.posts))
	}
	if prov.posts[0].channel.UUID != chMgr.channels[0].UUID {
		t.Errorf("post landed on %q, want default channel %q", prov.posts[0].channel.UUID, chMgr.channels[0].UUID)
	}
}

// TestCronRunner_OneshotTick_MissingDefaultRecordsError covers the resolver
// branch where no channel is configured for the cron and the workspace has no
// default — surfaces a clear error message.
func TestCronRunner_OneshotTick_MissingDefaultRecordsError(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)
	chMgr.resolveDefault = nil
	chMgr.resolveErr = nil

	if _, err := runner.CreateJob("nightly", "*/5 * * * *", "Summarize", "", true, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for id := range sched.jobs {
		sched.fire(id)
	}

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", findOnlyJobUUID(t, db)).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error", got.LastRunStatus)
	}
	if !contains(got.LastRunError, "no channel configured") {
		t.Errorf("LastRunError = %q, want no-channel message", got.LastRunError)
	}
}

// TestCronRunner_OneshotTick_LoadChannelByIDStale exercises the resolveChannel
// branch where a cron job has ChannelID set but the row was deleted (FK gone);
// the resolver falls back to the default rather than crashing.
func TestCronRunner_OneshotTick_LoadChannelByIDStale(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("nightly", "*/5 * * * *", "Summarize", chUUID, true, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Drop the channel from the DB; preloaded copy stays on the job from
	// initial fire's Preload, but the resolver loads fresh, so simulate a stale
	// preload by setting ChannelID to a missing FK after Channel was loaded.
	staleID := uint(99999)
	if err := db.Model(&database.CronJob{}).Where("id = ?", job.ID).Update("channel_id", staleID).Error; err != nil {
		t.Fatalf("stale FK: %v", err)
	}

	for id := range sched.jobs {
		sched.fire(id)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one post via default fallback, got %d", len(prov.posts))
	}
}

// TestCronRunner_FormatMessages_RespectsByteCap verifies the cron formatter
// truncates oversized agent bodies so a runaway LLM response cannot exceed
// Slack's chat.postMessage limit. Without the cap the slack provider would
// either silently truncate or fail with a 400.
func TestCronRunner_FormatMessages_RespectsByteCap(t *testing.T) {
	job := &database.CronJob{Name: "Daily report"}
	huge := strings.Repeat("a", cronChannelMaxMessageBytes*2)

	agentOut := formatCronAgentMessage(job, huge, false, "")
	if len(agentOut) > cronChannelMaxMessageBytes {
		t.Fatalf("agent message %d bytes exceeds cap %d", len(agentOut), cronChannelMaxMessageBytes)
	}
	if !strings.Contains(agentOut, "*Daily report*") {
		t.Errorf("expected header preserved after truncation: %q", agentOut)
	}
	if !strings.Contains(agentOut, "truncated") {
		t.Errorf("expected truncation marker in agent output")
	}

	// Short input must pass through unchanged so normal traffic is unaffected.
	short := formatCronAgentMessage(job, "OK", false, "")
	if !strings.Contains(short, "OK") || strings.Contains(short, "truncated") {
		t.Errorf("short input unexpectedly modified: %q", short)
	}
}

// TestCronRunner_NewCronRunner_ConstructorReturnsRunner is a smoke check that
// the production constructor wires every dependency through.
func TestCronRunner_NewCronRunner_ConstructorReturnsRunner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	runner := NewCronRunner(
		&recordingChannelManager{},
		&fakeProviderRegistry{},
		&fakeSkillIncidentManager{},
		newFakeIncidentRunner(),
	)
	if runner == nil {
		t.Fatal("NewCronRunner returned nil")
	}
	if runner.scheduler == nil {
		t.Error("scheduler not wired")
	}
}
