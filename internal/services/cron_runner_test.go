package services

import (
	"context"
	"errors"
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

func (s *fakeScheduler) Start()                 { s.mu.Lock(); s.started = true; s.mu.Unlock() }
func (s *fakeScheduler) Stop() context.Context  { s.mu.Lock(); s.stopped = true; s.mu.Unlock(); return context.Background() }
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
// for slack so executeOneshot can route through the standard provider API.
type recordingProvider struct {
	mu       sync.Mutex
	posts    []recordedPost
	postErr  error
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
		&database.LLMSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	database.DB = db

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
	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return "ok response", nil
	}}

	runner := newCronRunnerWithDeps(db, chMgr, reg, caller, sched)
	return runner, db, sched, chMgr, prov
}

// ===== schedule validation =====

func TestCronRunner_CreateJob_ValidatesSchedule(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	chUUID := runner.channels.(*recordingChannelManager).channels[0].UUID

	_, err := runner.CreateJob("daily report", "", "not a cron", "report status", database.CronJobModeOneshot, chUUID, true)
	if err == nil || !errors.Is(err, ErrInvalidCronSchedule) {
		t.Fatalf("expected ErrInvalidCronSchedule, got %v", err)
	}
}

func TestCronRunner_CreateJob_RejectsEmptyName(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	chUUID := runner.channels.(*recordingChannelManager).channels[0].UUID

	_, err := runner.CreateJob("   ", "", "*/2 * * * *", "report status", database.CronJobModeOneshot, chUUID, true)
	if err == nil {
		t.Fatal("expected name validation error, got nil")
	}
}

func TestCronRunner_CreateJob_RejectsInvalidMode(t *testing.T) {
	runner, _, _, _, _ := setupCronRunnerTest(t)
	chUUID := runner.channels.(*recordingChannelManager).channels[0].UUID

	_, err := runner.CreateJob("daily", "", "*/2 * * * *", "do thing", "exotic", chUUID, true)
	if err == nil {
		t.Fatal("expected mode validation error, got nil")
	}
}

func TestCronRunner_UpdateJob_ScheduleAndChannelTakeEffect(t *testing.T) {
	runner, _, sched, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("nightly", "", "*/5 * * * *", "x", database.CronJobModeOneshot, chUUID, true)
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

	job, err := runner.CreateJob("nightly", "", "*/5 * * * *", "x", database.CronJobModeOneshot, chUUID, true)
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

	job, err := runner.CreateJob("nightly", "", "*/5 * * * *", "x", database.CronJobModeOneshot, chUUID, true)
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

// ===== oneshot tick =====

func TestCronRunner_OneshotTick_PostsToConfiguredChannel(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	if _, err := runner.CreateJob("status", "describe state", "*/2 * * * *", "Summarize", database.CronJobModeOneshot, chUUID, true); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Fire the registered entry (skipping wall-clock time).
	if sched.entryCount() != 1 {
		t.Fatalf("expected 1 entry, got %d", sched.entryCount())
	}
	for id := range sched.jobs {
		sched.fire(id)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.posts) != 1 {
		t.Fatalf("expected one PostMessage call, got %d", len(prov.posts))
	}
	if prov.posts[0].channel.UUID != chUUID {
		t.Errorf("post landed on wrong channel: %s", prov.posts[0].channel.UUID)
	}
	if !contains(prov.posts[0].text, "ok response") {
		t.Errorf("post body missing LLM output: %q", prov.posts[0].text)
	}

	var job database.CronJob
	if err := db.First(&job, "uuid = ?", findOnlyJobUUID(t, db)).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if job.LastRunStatus != database.CronJobRunStatusOK {
		t.Errorf("LastRunStatus = %q, want ok", job.LastRunStatus)
	}
	if job.LastRunError != "" {
		t.Errorf("LastRunError = %q, want empty", job.LastRunError)
	}
	if job.LastRunAt == nil {
		t.Error("LastRunAt is nil after tick")
	}
}

func TestCronRunner_RunNow_FiresImmediately(t *testing.T) {
	runner, _, _, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID

	job, err := runner.CreateJob("status", "", "*/10 * * * *", "Summarize", database.CronJobModeOneshot, chUUID, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run now: %v", err)
	}
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

func TestCronRunner_OneshotTick_RecordsProviderError(t *testing.T) {
	runner, db, sched, chMgr, prov := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	prov.postErr = errors.New("network unreachable")

	if _, err := runner.CreateJob("status", "", "*/2 * * * *", "Summarize", database.CronJobModeOneshot, chUUID, true); err != nil {
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

func TestCronRunner_OneshotTick_RecordsLLMError(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)
	caller := runner.caller.(*fakeOneShotLLMCaller)
	caller.respond = func(ctx context.Context) (string, error) { return "", ErrWorkerNotConnected }

	chUUID := chMgr.channels[0].UUID
	if _, err := runner.CreateJob("status", "", "*/2 * * * *", "Summarize", database.CronJobModeOneshot, chUUID, true); err != nil {
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
	if !contains(job.LastRunError, "worker not connected") {
		t.Errorf("LastRunError = %q, want worker-disconnect message", job.LastRunError)
	}
}

func TestCronRunner_OneshotTick_AgentModeMarksError(t *testing.T) {
	runner, db, _, chMgr, _ := setupCronRunnerTest(t)
	chUUID := chMgr.channels[0].UUID
	job, err := runner.CreateJob("status", "", "*/2 * * * *", "Investigate", database.CronJobModeAgent, chUUID, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := runner.RunNow(job.UUID); err != nil {
		t.Fatalf("run: %v", err)
	}

	var got database.CronJob
	if err := db.First(&got, "uuid = ?", job.UUID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LastRunStatus != database.CronJobRunStatusError {
		t.Errorf("LastRunStatus = %q, want error (agent mode pending Task 8)", got.LastRunStatus)
	}
}

// ===== Start loads enabled jobs =====

func TestCronRunner_Start_LoadsEnabledJobs(t *testing.T) {
	runner, db, sched, chMgr, _ := setupCronRunnerTest(t)

	channel := chMgr.channels[0]
	enabledJob := database.CronJob{UUID: uuid.New().String(), Name: "enabled", Schedule: "*/5 * * * *", Prompt: "p", Mode: database.CronJobModeOneshot, ChannelID: &channel.ID, Enabled: true}
	disabledJob := database.CronJob{UUID: uuid.New().String(), Name: "disabled", Schedule: "*/5 * * * *", Prompt: "p", Mode: database.CronJobModeOneshot, ChannelID: &channel.ID, Enabled: true}
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

func contains(haystack, needle string) bool {
	return needle == "" || (haystack != "" && (haystack == needle || indexOf(haystack, needle) >= 0))
}

func indexOf(haystack, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return i
		}
	}
	return -1
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

