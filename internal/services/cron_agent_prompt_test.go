package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestDefaultCronAgentPrompt_PinsRequiredDirectives locks the cron-agent
// root prompt against silent regressions. The redesigned cron path runs as
// `cron-agent` instead of `incident-manager`, so the prompt MUST guide the
// agent through the same orient → optional runbook → memory recall → tool
// execution → optional memory write flow without the incident-triage framing.
func TestDefaultCronAgentPrompt_PinsRequiredDirectives(t *testing.T) {
	prompt := database.DefaultCronAgentPrompt
	if strings.TrimSpace(prompt) == "" {
		t.Fatal("DefaultCronAgentPrompt must be non-empty")
	}

	// The cron-agent must delegate memory recall to memory-searcher and offer
	// memory mutations (upsert/delete) via memory-writer. Without these the
	// memory-curator system cron has no way to dedupe or remove entries.
	for _, want := range []string{
		`"agent": "memory-searcher"`,
		`"agent": "memory-writer"`,
		"Action: upsert",
		"Action: delete",
		"gateway_call",
		"Scope:",
		"Incident UUID:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("DefaultCronAgentPrompt is missing required directive %q", want)
		}
	}

	// Runbook recall is optional for cron runs (a consolidation pass does not
	// need a runbook), but the prompt should still tell the agent how to
	// delegate the lookup when the task references a procedure.
	if !strings.Contains(prompt, `"agent": "runbook-searcher"`) {
		t.Errorf("DefaultCronAgentPrompt should mention runbook-searcher for optional procedure lookup")
	}

	// Affirmative incident-triage framing must not appear — a cron run is not
	// an incident and the operator-facing summary should not be framed as one.
	for _, banned := range []string{
		"Triage:",
		"Senior Incident Manager",
		"infrastructure incidents",
	} {
		if strings.Contains(prompt, banned) {
			t.Errorf("DefaultCronAgentPrompt must not contain incident-triage framing %q", banned)
		}
	}
}

// TestInitializeCronAgentSkill_CreatesAndIsIdempotent confirms the system
// skill row is created on first call and not duplicated on a second call.
// Mirrors InitializeSystemSkill's contract.
func TestInitializeCronAgentSkill_CreatesAndIsIdempotent(t *testing.T) {
	db := newCronAgentTestDB(t)

	if err := database.InitializeCronAgentSkill(); err != nil {
		t.Fatalf("first init: %v", err)
	}

	var rows []database.Skill
	if err := db.Where("name = ?", "cron-agent").Find(&rows).Error; err != nil {
		t.Fatalf("query cron-agent skill: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one cron-agent row, got %d", len(rows))
	}
	if !rows[0].IsSystem {
		t.Error("cron-agent skill must be marked IsSystem=true")
	}
	if !rows[0].Enabled {
		t.Error("cron-agent skill must be enabled on first seed")
	}

	// Second call must not duplicate or error.
	if err := database.InitializeCronAgentSkill(); err != nil {
		t.Fatalf("second init: %v", err)
	}
	var count int64
	db.Model(&database.Skill{}).Where("name = ?", "cron-agent").Count(&count)
	if count != 1 {
		t.Errorf("expected idempotent seed, got %d rows after second call", count)
	}
}

// TestInitializeCronAgentSkill_UpgradesNonSystemRow makes sure a pre-existing
// row created before the system flag landed (e.g. via an operator import) is
// upgraded to IsSystem=true on the next boot, mirroring InitializeSystemSkill.
func TestInitializeCronAgentSkill_UpgradesNonSystemRow(t *testing.T) {
	db := newCronAgentTestDB(t)

	if err := db.Create(&database.Skill{
		Name:     "cron-agent",
		IsSystem: false,
		Enabled:  true,
	}).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := database.InitializeCronAgentSkill(); err != nil {
		t.Fatalf("init: %v", err)
	}

	var got database.Skill
	if err := db.Where("name = ?", "cron-agent").First(&got).Error; err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !got.IsSystem {
		t.Errorf("expected IsSystem=true after init upgrade, got false")
	}
}

// TestSeedSystemCronJobs_IdempotentAndPreservesOperatorState verifies the
// memory-curator system cron is created disabled on first seed, survives a
// second InitializeDefaults-equivalent call, and preserves ALL operator
// edits (Enabled, ChannelID, Schedule, Prompt) across simulated restarts.
// Re-asserting schedule/prompt on boot would silently revert operator edits,
// contradicting the documented "can be disabled but not deleted; all other
// fields remain editable" contract.
func TestSeedSystemCronJobs_IdempotentAndPreservesOperatorState(t *testing.T) {
	db := newCronAgentTestDB(t)

	// First seed — row must be created disabled.
	if err := seedSystemCronJobsViaPackage(); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	var rows []database.CronJob
	if err := db.Where("name = ?", "memory-curator").Find(&rows).Error; err != nil {
		t.Fatalf("query memory-curator: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one memory-curator row, got %d", len(rows))
	}
	row := rows[0]
	if !row.IsSystem {
		t.Error("memory-curator must be IsSystem=true")
	}
	if row.Enabled {
		t.Error("memory-curator must default to Enabled=false (operator opts in)")
	}
	if row.Schedule != "0 2 * * *" {
		t.Errorf("memory-curator schedule wrong, got %q", row.Schedule)
	}
	if !strings.Contains(row.Prompt, "memory consolidation") {
		t.Errorf("memory-curator prompt is missing the consolidation directive: %q", row.Prompt)
	}
	originalUUID := row.UUID

	// Operator enables the cron. A subsequent seed must NOT flip it back to
	// disabled — the seed is "ensure the row exists with the canonical
	// schedule + prompt", not "reset operator preferences on every boot".
	if err := db.Model(&database.CronJob{}).Where("id = ?", row.ID).Update("enabled", true).Error; err != nil {
		t.Fatalf("operator-enable: %v", err)
	}

	// Operator-only customization that must survive: pick a channel, retune
	// the schedule, rewrite the prompt body.
	channelID := uint(99)
	if err := db.Model(&database.CronJob{}).Where("id = ?", row.ID).Update("channel_id", channelID).Error; err != nil {
		t.Fatalf("operator-set channel: %v", err)
	}
	const operatorSchedule = "*/30 * * * *"
	const operatorPrompt = "operator-edited memory consolidation prompt"
	if err := db.Model(&database.CronJob{}).Where("id = ?", row.ID).
		Updates(map[string]interface{}{"schedule": operatorSchedule, "prompt": operatorPrompt}).Error; err != nil {
		t.Fatalf("operator-edit schedule/prompt: %v", err)
	}

	if err := seedSystemCronJobsViaPackage(); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var after database.CronJob
	if err := db.Where("name = ?", "memory-curator").First(&after).Error; err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !after.Enabled {
		t.Error("operator's Enabled=true flip must survive re-seed across restarts")
	}
	if after.ChannelID == nil || *after.ChannelID != channelID {
		t.Errorf("operator's ChannelID must survive re-seed: got %v", after.ChannelID)
	}
	if after.UUID != originalUUID {
		t.Errorf("UUID must not change on re-seed: %s -> %s", originalUUID, after.UUID)
	}
	if after.Schedule != operatorSchedule {
		t.Errorf("operator-edited schedule must survive re-seed: got %q want %q", after.Schedule, operatorSchedule)
	}
	if after.Prompt != operatorPrompt {
		t.Errorf("operator-edited prompt must survive re-seed: got %q", after.Prompt)
	}

	// Final sanity: still exactly one row after a third seed.
	if err := seedSystemCronJobsViaPackage(); err != nil {
		t.Fatalf("third seed: %v", err)
	}
	var count int64
	db.Model(&database.CronJob{}).Where("name = ?", "memory-curator").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly one memory-curator row after 3 seeds, got %d", count)
	}
}

// newCronAgentTestDB returns an in-memory SQLite DB with the schema the
// cron-agent skill / system-cron seed code touches. Stashes the global DB on
// the testing.T's cleanup so other tests in this package keep their handle.
func newCronAgentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Skill{},
		&database.CronJob{},
		&database.Integration{},
		&database.Channel{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })
	return db
}

// seedSystemCronJobsViaPackage delegates to the exported database seed.
// Kept as a tiny indirection so future test plumbing changes only touch
// this services package.
func seedSystemCronJobsViaPackage() error {
	return database.SeedSystemCronJobs()
}
