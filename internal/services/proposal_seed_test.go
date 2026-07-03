package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newProposalSeedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Skill{},
		&database.CronJob{},
		&database.CronJobTool{},
		&database.ToolType{},
		&database.ToolInstance{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })
	return db
}

func seedEvaluatorToolInstances(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, name := range []string{"incidents", "proposals"} {
		tt := database.ToolType{Name: name}
		if err := db.Create(&tt).Error; err != nil {
			t.Fatalf("seed tool type %s: %v", name, err)
		}
		if err := db.Create(&database.ToolInstance{
			ToolTypeID: tt.ID, Name: name + "-instance", LogicalName: name, Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed tool instance %s: %v", name, err)
		}
	}
}

// TestDefaultProposalEditorPrompt_PinsRequiredDirectives locks the
// proposal-editor root prompt against silent regressions: it must direct the
// agent to verify via incidents/proposals gateway calls, persist revisions
// via update_draft, and never apply or write memory itself.
func TestDefaultProposalEditorPrompt_PinsRequiredDirectives(t *testing.T) {
	prompt := database.DefaultProposalEditorPrompt
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("DefaultProposalEditorPrompt must be non-empty")
	}
	for _, want := range []string{
		`proposals.update_draft`,
		`incidents.get`,
		`proposals.list_cron_jobs`,
		"runbook-searcher",
		"memory-searcher",
		"never apply proposals",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("DefaultProposalEditorPrompt is missing required directive %q", want)
		}
	}
	// The editor must not invoke memory-writer — proposal application is the
	// only mutation path and it belongs to the operator's approve action.
	if !strings.Contains(prompt, "never call the memory-writer") {
		t.Errorf("DefaultProposalEditorPrompt must forbid memory-writer usage")
	}
}

// TestInitializeProposalEditorSkill_CreatesAndIsIdempotent mirrors the
// cron-agent seed contract for the proposal-editor system skill.
func TestInitializeProposalEditorSkill_CreatesAndIsIdempotent(t *testing.T) {
	db := newProposalSeedTestDB(t)

	if err := database.InitializeProposalEditorSkill(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	var rows []database.Skill
	if err := db.Where("name = ?", "proposal-editor").Find(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || !rows[0].IsSystem || !rows[0].Enabled {
		t.Fatalf("expected one enabled system row, got %+v", rows)
	}

	if err := database.InitializeProposalEditorSkill(); err != nil {
		t.Fatalf("second init: %v", err)
	}
	var count int64
	db.Model(&database.Skill{}).Where("name = ?", "proposal-editor").Count(&count)
	if count != 1 {
		t.Errorf("expected idempotent seed, got %d rows", count)
	}
}

// TestSeedImprovementEvaluatorCron_SeedsDisabledWithTools verifies the
// evaluator cron is created disabled with the incidents+proposals allowlist
// attached, is idempotent, and preserves operator edits on re-seed.
func TestSeedImprovementEvaluatorCron_SeedsDisabledWithTools(t *testing.T) {
	db := newProposalSeedTestDB(t)
	seedEvaluatorToolInstances(t, db)

	if err := database.SeedImprovementEvaluatorCron(); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	var job database.CronJob
	if err := db.Preload("Tools").Where("name = ?", "improvement-evaluator").First(&job).Error; err != nil {
		t.Fatalf("job not seeded: %v", err)
	}
	if !job.IsSystem {
		t.Error("evaluator cron must be IsSystem=true")
	}
	if job.Enabled {
		t.Error("evaluator cron must seed DISABLED (operator opts in)")
	}
	if len(job.Tools) != 2 {
		t.Fatalf("expected incidents+proposals tools attached, got %d", len(job.Tools))
	}
	names := map[string]bool{}
	for _, ti := range job.Tools {
		names[ti.LogicalName] = true
	}
	if !names["incidents"] || !names["proposals"] {
		t.Errorf("expected incidents+proposals, got %v", names)
	}
	if !strings.Contains(job.Prompt, "proposals.create") || !strings.Contains(job.Prompt, "incidents.list") {
		t.Errorf("evaluator prompt must reference the gateway ops it depends on")
	}
	if !strings.Contains(job.Prompt, `"status": "pending"`) {
		t.Errorf("evaluator prompt must instruct dedup against pending proposals")
	}

	// Operator edits survive a re-seed.
	if err := db.Model(&job).Updates(map[string]interface{}{
		"enabled":  true,
		"schedule": "0 6 * * *",
		"prompt":   "operator-tuned prompt",
	}).Error; err != nil {
		t.Fatalf("simulate operator edit: %v", err)
	}
	if err := database.SeedImprovementEvaluatorCron(); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	var after database.CronJob
	db.Where("name = ?", "improvement-evaluator").First(&after)
	if !after.Enabled || after.Schedule != "0 6 * * *" || after.Prompt != "operator-tuned prompt" {
		t.Errorf("re-seed must preserve operator edits, got %+v", after)
	}
	var count int64
	db.Model(&database.CronJob{}).Where("name = ?", "improvement-evaluator").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly one row, got %d", count)
	}
}

// TestSeedImprovementEvaluatorCron_RequiresToolInstances verifies the seed
// fails loudly when called before EnsureToolTypes (boot-order guard).
func TestSeedImprovementEvaluatorCron_RequiresToolInstances(t *testing.T) {
	newProposalSeedTestDB(t)
	err := database.SeedImprovementEvaluatorCron()
	if err == nil || !strings.Contains(err.Error(), "EnsureToolTypes") {
		t.Fatalf("expected boot-order error, got %v", err)
	}
}

// TestSeedImprovementEvaluatorCron_ShadowRowRefusesSeed verifies a non-system
// row with the same name blocks the seed instead of being hijacked.
func TestSeedImprovementEvaluatorCron_ShadowRowRefusesSeed(t *testing.T) {
	db := newProposalSeedTestDB(t)
	seedEvaluatorToolInstances(t, db)
	if err := db.Create(&database.CronJob{
		UUID: "operator-row", Name: "improvement-evaluator",
		Schedule: "0 1 * * *", Prompt: "mine", IsSystem: false, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed shadow row: %v", err)
	}

	if err := database.SeedImprovementEvaluatorCron(); err != nil {
		t.Fatalf("seed with shadow should be a warning no-op, got %v", err)
	}
	var rows []database.CronJob
	db.Where("name = ?", "improvement-evaluator").Find(&rows)
	if len(rows) != 1 || rows[0].IsSystem || rows[0].UUID != "operator-row" {
		t.Errorf("shadow row must be left untouched, got %+v", rows)
	}
}

// TestProposalEditorSystemSkillPromptPlumbing pins the root-skill checklist:
// GetSkillPrompt returns the hardcoded prompt and UpdateSkillPrompt is a
// no-op for proposal-editor.
func TestProposalEditorSystemSkillPromptPlumbing(t *testing.T) {
	svc := &SkillService{}
	got, err := svc.GetSkillPrompt("proposal-editor")
	if err != nil {
		t.Fatalf("GetSkillPrompt: %v", err)
	}
	if got != database.DefaultProposalEditorPrompt {
		t.Errorf("GetSkillPrompt must return the hardcoded proposal-editor prompt")
	}
	if err := svc.UpdateSkillPrompt("proposal-editor", "attempted overwrite"); err != nil {
		t.Errorf("UpdateSkillPrompt must be a silent no-op for proposal-editor, got %v", err)
	}
	if err := svc.RegenerateSkillMd("proposal-editor"); err != nil {
		t.Errorf("RegenerateSkillMd must be a silent no-op for proposal-editor, got %v", err)
	}
}
