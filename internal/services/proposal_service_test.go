package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newProposalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Proposal{},
		&database.ProposalChatMessage{},
		&database.Runbook{},
		&database.Memory{},
		&database.CronJob{},
		&database.ToolType{},
		&database.ToolInstance{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// Fakes embed the interface so only the methods the service touches need
// implementations; calling anything else panics loudly.

type fakeRunbooks struct {
	RunbookManager
	created  []database.Runbook
	updated  []database.Runbook
	live     map[uint]*database.Runbook
	createFn func(title, content string) (*database.Runbook, error)
}

func (f *fakeRunbooks) CreateRunbook(title, content string) (*database.Runbook, error) {
	if f.createFn != nil {
		return f.createFn(title, content)
	}
	rb := database.Runbook{ID: uint(len(f.created) + 1), Title: title, Content: content}
	f.created = append(f.created, rb)
	return &rb, nil
}

func (f *fakeRunbooks) UpdateRunbook(id uint, title, content string) (*database.Runbook, error) {
	rb := database.Runbook{ID: id, Title: title, Content: content}
	f.updated = append(f.updated, rb)
	return &rb, nil
}

func (f *fakeRunbooks) GetRunbook(id uint) (*database.Runbook, error) {
	if rb, ok := f.live[id]; ok {
		return rb, nil
	}
	return nil, gorm.ErrRecordNotFound
}

type fakeMemories struct {
	MemoryManager
	upserted []database.Memory
}

func (f *fakeMemories) UpsertByName(m *database.Memory) (*database.Memory, error) {
	f.upserted = append(f.upserted, *m)
	return m, nil
}

type fakeCrons struct {
	CronJobManager
	createdName     string
	createdSchedule string
	createdEnabled  bool
	createdTools    CronJobTools
	updatedUUID     string
	updatedPatch    CronJobUpdate
	live            map[string]*database.CronJob
}

func (f *fakeCrons) CreateJob(name, schedule, prompt string, channelUUID string, enabled, postResults bool, tools CronJobTools) (*database.CronJob, error) {
	f.createdName = name
	f.createdSchedule = schedule
	f.createdEnabled = enabled
	f.createdTools = tools
	return &database.CronJob{Name: name, Schedule: schedule, Prompt: prompt, Enabled: enabled}, nil
}

func (f *fakeCrons) UpdateJob(uuid string, patch CronJobUpdate) (*database.CronJob, error) {
	f.updatedUUID = uuid
	f.updatedPatch = patch
	return &database.CronJob{UUID: uuid}, nil
}

func (f *fakeCrons) GetJobByUUID(uuid string) (*database.CronJob, error) {
	if j, ok := f.live[uuid]; ok {
		return j, nil
	}
	return nil, gorm.ErrRecordNotFound
}

type fakeSkills struct {
	SkillManager
	skills         map[string]*database.Skill
	prompts        map[string]string
	updatedName    string
	updatedPrompt  string
	promptReadErrs map[string]error
}

func (f *fakeSkills) GetSkill(name string) (*database.Skill, error) {
	if s, ok := f.skills[name]; ok {
		return s, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeSkills) GetSkillPrompt(name string) (string, error) {
	if err, ok := f.promptReadErrs[name]; ok {
		return "", err
	}
	if p, ok := f.prompts[name]; ok {
		return p, nil
	}
	return "", errors.New("no prompt")
}

func (f *fakeSkills) UpdateSkillPrompt(name, prompt string) error {
	f.updatedName = name
	f.updatedPrompt = prompt
	return nil
}

func seedProposal(t *testing.T, db *gorm.DB, p *database.Proposal) *database.Proposal {
	t.Helper()
	if p.Status == "" {
		p.Status = database.ProposalStatusPending
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	return p
}

func TestApprove_RunbookNew_Applies(t *testing.T) {
	db := newProposalTestDB(t)
	runbooks := &fakeRunbooks{}
	svc := NewProposalService(db, runbooks, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindRunbookNew, Title: "Add runbook",
		ProposedContent: `{"title":"DNS issues","content":"steps"}`,
	})

	p, err := svc.Approve(context.Background(), "p1")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if p.Status != database.ProposalStatusApproved || p.AppliedAt == nil {
		t.Errorf("expected approved+applied_at, got %s %v", p.Status, p.AppliedAt)
	}
	if len(runbooks.created) != 1 || runbooks.created[0].Title != "DNS issues" {
		t.Errorf("CreateRunbook not called correctly: %+v", runbooks.created)
	}
}

func TestApprove_RunbookUpdate_StaleMarksSuperseded(t *testing.T) {
	db := newProposalTestDB(t)
	runbooks := &fakeRunbooks{live: map[uint]*database.Runbook{
		7: {ID: 7, Title: "Live title", Content: "content DRIFTED"},
	}}
	svc := NewProposalService(db, runbooks, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindRunbookUpdate, Title: "Refresh", TargetRef: "7",
		CurrentSnapshot: `{"title":"Live title","content":"original content"}`,
		ProposedContent: `{"title":"New","content":"new"}`,
	})

	p, err := svc.Approve(context.Background(), "p1")
	if !errors.Is(err, ErrProposalStale) {
		t.Fatalf("expected ErrProposalStale, got %v", err)
	}
	if p.Status != database.ProposalStatusSuperseded {
		t.Errorf("expected superseded, got %s", p.Status)
	}
	if len(runbooks.updated) != 0 {
		t.Errorf("stale proposal must not be applied")
	}
}

func TestApprove_ApplyFailureThenRetry(t *testing.T) {
	db := newProposalTestDB(t)
	fail := true
	runbooks := &fakeRunbooks{createFn: func(title, content string) (*database.Runbook, error) {
		if fail {
			return nil, errors.New("disk exploded")
		}
		return &database.Runbook{Title: title}, nil
	}}
	svc := NewProposalService(db, runbooks, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindRunbookNew, Title: "Add",
		ProposedContent: `{"title":"T","content":"C"}`,
	})

	p, err := svc.Approve(context.Background(), "p1")
	if err == nil || !strings.Contains(err.Error(), "disk exploded") {
		t.Fatalf("expected apply error, got %v", err)
	}
	if p.Status != database.ProposalStatusApplyFailed || p.ApplyError == "" {
		t.Errorf("expected apply_failed+error, got %s %q", p.Status, p.ApplyError)
	}

	// Re-approve retries the apply and clears the error.
	fail = false
	p, err = svc.Approve(context.Background(), "p1")
	if err != nil {
		t.Fatalf("retry approve: %v", err)
	}
	if p.Status != database.ProposalStatusApproved || p.ApplyError != "" {
		t.Errorf("expected approved with cleared error, got %s %q", p.Status, p.ApplyError)
	}
}

func TestApprove_AlreadyDecidedIs409(t *testing.T) {
	db := newProposalTestDB(t)
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindRunbookNew, Title: "Add",
		Status: database.ProposalStatusRejected, ProposedContent: `{}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); !errors.Is(err, ErrProposalNotApprovable) {
		t.Fatalf("expected ErrProposalNotApprovable, got %v", err)
	}
	if _, err := svc.Reject("p1"); !errors.Is(err, ErrProposalNotApprovable) {
		t.Fatalf("expected reject to refuse decided proposal, got %v", err)
	}
}

func TestApprove_MemoryUpdate_IdentityFromTargetRef(t *testing.T) {
	db := newProposalTestDB(t)
	memories := &fakeMemories{}
	svc := NewProposalService(db, &fakeRunbooks{}, memories, &fakeCrons{}, &fakeSkills{})

	// Live memory row for the staleness check.
	db.Create(&database.Memory{Scope: "global", Type: "host", Name: "web-1-quirks", Description: "d", Body: "orig body"})

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryUpdate, Title: "Refresh memory",
		TargetRef:       "global/web-1-quirks",
		CurrentSnapshot: `{"scope":"global","type":"host","name":"web-1-quirks","description":"d","body":"orig body"}`,
		// Content deliberately carries a drifted scope/name — target_ref wins.
		ProposedContent: `{"scope":"WRONG","type":"host","name":"WRONG","description":"new d","body":"new body"}`,
	})

	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(memories.upserted) != 1 {
		t.Fatalf("expected one upsert, got %d", len(memories.upserted))
	}
	up := memories.upserted[0]
	if up.Scope != "global" || up.Name != "web-1-quirks" {
		t.Errorf("identity must come from target_ref, got %s/%s", up.Scope, up.Name)
	}
	if up.Body != "new body" || up.CreatedBy != database.ProposalCreatedByOperator {
		t.Errorf("unexpected upsert payload: %+v", up)
	}
}

func TestApprove_CronNew_AppliesDisabledWithToolFields(t *testing.T) {
	db := newProposalTestDB(t)
	crons := &fakeCrons{}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, crons, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindCronNew, Title: "Add weekly review",
		ProposedContent: `{"name":"weekly-review","schedule":"0 9 * * 1","prompt":"review things","tools_enabled":true,"builtin_tools":["events","incidents"]}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if crons.createdName != "weekly-review" || crons.createdEnabled {
		t.Errorf("cron must be created DISABLED, got name=%s enabled=%v", crons.createdName, crons.createdEnabled)
	}
	if !crons.createdTools.ToolsEnabled || !equalStringSets(crons.createdTools.BuiltinTools, []string{"events", "incidents"}) {
		t.Errorf("tool fields not applied: %+v", crons.createdTools)
	}
}

// TestApprove_CronNew_LegacyToolLogicalNames: a pending proposal made
// before the tool mapping was removed is translated with the migration rule.
func TestApprove_CronNew_LegacyToolLogicalNames(t *testing.T) {
	db := newProposalTestDB(t)
	crons := &fakeCrons{}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, crons, &fakeSkills{})

	inc := database.ToolType{Name: "incidents"}
	db.Create(&inc)
	db.Create(&database.ToolInstance{ToolTypeID: inc.ID, Name: "Incidents", LogicalName: "incidents", Enabled: true})

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindCronNew, Title: "Add weekly review",
		ProposedContent: `{"name":"weekly-review","schedule":"0 9 * * 1","prompt":"review things","tool_logical_names":["incidents"]}`,
	})
	got, err := svc.GetProposal("p1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.ProposedContent, "tool_logical_names") || !strings.Contains(got.ProposedContent, `"tools_enabled":false`) {
		t.Errorf("read not translated: %s", got.ProposedContent)
	}
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if crons.createdTools.ToolsEnabled {
		t.Error("only a built-in was named; tools_enabled must be false")
	}
	if !equalStringSets(crons.createdTools.BuiltinTools, []string{"events", "runbooks", "memory", "incidents"}) {
		t.Errorf("builtin_tools = %v", crons.createdTools.BuiltinTools)
	}

	// A name that matches no instance counts as a connector tool.
	seedProposal(t, db, &database.Proposal{
		UUID: "p2", Kind: database.ProposalKindCronNew, Title: "Add disk check",
		ProposedContent: `{"name":"j","schedule":"0 9 * * 1","prompt":"p","tool_logical_names":["prod-ssh"]}`,
	})
	if _, err := svc.Approve(context.Background(), "p2"); err != nil {
		t.Fatalf("approve p2: %v", err)
	}
	if !crons.createdTools.ToolsEnabled {
		t.Error("an unknown logical name must enable connector tools")
	}
}

// TestApprove_CronUpdate_LegacySnapshotNotStale: the snapshot of a legacy
// cron_update proposal is translated the same way, so the upgrade alone
// does not mark it superseded.
func TestApprove_CronUpdate_LegacySnapshotNotStale(t *testing.T) {
	db := newProposalTestDB(t)
	crons := &fakeCrons{live: map[string]*database.CronJob{
		"c1": {UUID: "c1", Name: "j", Schedule: "0 9 * * 1", Prompt: "p", ToolsEnabled: false, BuiltinTools: database.StringList{"events", "runbooks", "memory"}},
	}}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, crons, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindCronUpdate, Title: "Tweak", TargetRef: "c1",
		CurrentSnapshot: `{"name":"j","schedule":"0 9 * * 1","prompt":"p","tool_logical_names":[]}`,
		ProposedContent: `{"name":"j","schedule":"0 10 * * 1","prompt":"p","tool_logical_names":[]}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if crons.updatedPatch.ToolsEnabled == nil || *crons.updatedPatch.ToolsEnabled {
		t.Errorf("patch tools_enabled = %v, want false", crons.updatedPatch.ToolsEnabled)
	}

	// A live change of the tool access marks a new-shape proposal stale.
	crons.live["c1"].ToolsEnabled = true
	seedProposal(t, db, &database.Proposal{
		UUID: "p2", Kind: database.ProposalKindCronUpdate, Title: "Tweak 2", TargetRef: "c1",
		CurrentSnapshot: `{"name":"j","schedule":"0 9 * * 1","prompt":"p","tools_enabled":false,"builtin_tools":["events","runbooks","memory"]}`,
		ProposedContent: `{"name":"j","schedule":"0 11 * * 1","prompt":"p","tools_enabled":false,"builtin_tools":["events"]}`,
	})
	if _, err := svc.Approve(context.Background(), "p2"); !errors.Is(err, ErrProposalStale) {
		t.Fatalf("err = %v, want ErrProposalStale", err)
	}
}

func TestApprove_SkillPrompt_SystemSkillRefused(t *testing.T) {
	db := newProposalTestDB(t)
	skills := &fakeSkills{
		skills:  map[string]*database.Skill{"cron-agent": {Name: "cron-agent", IsSystem: true}},
		prompts: map[string]string{"cron-agent": "hardcoded"},
	}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, skills)
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindSkillPromptUpdate, Title: "Tweak", TargetRef: "cron-agent",
		CurrentSnapshot: `{"skill_name":"cron-agent","prompt":"hardcoded"}`,
		ProposedContent: `{"skill_name":"cron-agent","prompt":"new"}`,
	})
	p, err := svc.Approve(context.Background(), "p1")
	if err == nil || !strings.Contains(err.Error(), "system skill") {
		t.Fatalf("expected system-skill refusal, got %v", err)
	}
	if p.Status != database.ProposalStatusApplyFailed {
		t.Errorf("expected apply_failed, got %s", p.Status)
	}
	if skills.updatedName != "" {
		t.Errorf("UpdateSkillPrompt must not be called for system skills")
	}
}

func TestApprove_SkillPrompt_AppliesAndDetectsDrift(t *testing.T) {
	db := newProposalTestDB(t)
	skills := &fakeSkills{
		skills:  map[string]*database.Skill{"linux-engineer": {Name: "linux-engineer", IsSystem: false}},
		prompts: map[string]string{"linux-engineer": "current prompt"},
	}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, skills)

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindSkillPromptUpdate, Title: "Tweak", TargetRef: "linux-engineer",
		CurrentSnapshot: `{"skill_name":"linux-engineer","prompt":"current prompt"}`,
		ProposedContent: `{"skill_name":"linux-engineer","prompt":"improved prompt"}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if skills.updatedName != "linux-engineer" || skills.updatedPrompt != "improved prompt" {
		t.Errorf("UpdateSkillPrompt not called correctly: %s %q", skills.updatedName, skills.updatedPrompt)
	}

	// Drifted live prompt → superseded.
	skills.prompts["linux-engineer"] = "someone edited this"
	seedProposal(t, db, &database.Proposal{
		UUID: "p2", Kind: database.ProposalKindSkillPromptUpdate, Title: "Tweak again", TargetRef: "linux-engineer",
		CurrentSnapshot: `{"skill_name":"linux-engineer","prompt":"current prompt"}`,
		ProposedContent: `{"skill_name":"linux-engineer","prompt":"v2"}`,
	})
	p, err := svc.Approve(context.Background(), "p2")
	if !errors.Is(err, ErrProposalStale) {
		t.Fatalf("expected stale, got %v", err)
	}
	if p.Status != database.ProposalStatusSuperseded {
		t.Errorf("expected superseded, got %s", p.Status)
	}
}

func TestGetProposal_BackfillsSkillSnapshotLazily(t *testing.T) {
	db := newProposalTestDB(t)
	skills := &fakeSkills{
		skills:  map[string]*database.Skill{"linux-engineer": {Name: "linux-engineer"}},
		prompts: map[string]string{"linux-engineer": "live prompt"},
	}
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, skills)

	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindSkillPromptUpdate, Title: "Tweak",
		TargetRef: "linux-engineer", CurrentSnapshot: "",
		ProposedContent: `{"skill_name":"linux-engineer","prompt":"new"}`,
	})

	p, err := svc.GetProposal("p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var snap proposalSkillPromptContent
	if err := json.Unmarshal([]byte(p.CurrentSnapshot), &snap); err != nil {
		t.Fatalf("snapshot should be backfilled JSON, got %q", p.CurrentSnapshot)
	}
	if snap.Prompt != "live prompt" {
		t.Errorf("expected live prompt in snapshot, got %q", snap.Prompt)
	}

	// Persisted, not just in-memory.
	var row database.Proposal
	db.Where("uuid = ?", "p1").First(&row)
	if row.CurrentSnapshot == "" {
		t.Errorf("backfill must persist to the DB")
	}
}

func TestRejectAndCounts(t *testing.T) {
	db := newProposalTestDB(t)
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Title: "A", ProposedContent: `{}`})
	seedProposal(t, db, &database.Proposal{UUID: "p2", Kind: database.ProposalKindRunbookNew, Title: "B", ProposedContent: `{}`})

	if n, _ := svc.CountPending(); n != 2 {
		t.Errorf("expected 2 pending, got %d", n)
	}
	p, err := svc.Reject("p1")
	if err != nil || p.Status != database.ProposalStatusRejected {
		t.Fatalf("reject failed: %v %s", err, p.Status)
	}
	if n, _ := svc.CountPending(); n != 1 {
		t.Errorf("expected 1 pending after reject, got %d", n)
	}
	if _, err := svc.GetProposal("missing"); !errors.Is(err, ErrProposalNotFound) {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestChatTranscriptAndAllowlist(t *testing.T) {
	db := newProposalTestDB(t)
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{UUID: "p1", Kind: database.ProposalKindRunbookNew, Title: "A", ProposedContent: `{}`})

	if err := svc.SetChatIncident("p1", "chat-inc-1"); err != nil {
		t.Fatalf("set chat incident: %v", err)
	}
	if err := svc.SetChatIncident("missing", "x"); !errors.Is(err, ErrProposalNotFound) {
		t.Errorf("expected not-found for missing proposal, got %v", err)
	}

	_ = svc.AppendChatMessage("p1", "operator", "make it shorter")
	_ = svc.AppendChatMessage("p1", "assistant", "done")
	msgs, err := svc.ListChatMessages("p1")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d (%v)", len(msgs), err)
	}
	if msgs[0].Role != "operator" || msgs[1].Role != "assistant" {
		t.Errorf("messages out of order: %+v", msgs)
	}

	// Allowlist: type-only built-ins, no connector tools, never nil.
	entries := svc.ChatToolAllowlist()
	if entries == nil {
		t.Fatal("allowlist must be non-nil ([] = reject all)")
	}
	types := map[string]bool{}
	for _, e := range entries {
		types[e.ToolType] = true
		if e.InstanceID != 0 || e.LogicalName != "" {
			t.Errorf("chat entries must be type-only: %+v", e)
		}
	}
	for _, want := range []string{"incidents", "proposals", "runbooks", "memory"} {
		if !types[want] {
			t.Errorf("chat allowlist missing %s: %v", want, entries)
		}
	}
	if types["events"] || len(entries) != 4 {
		t.Errorf("unexpected chat allowlist: %v", entries)
	}
}
