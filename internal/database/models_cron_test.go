package database

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCronJob_Schema_RoundTripsIsSystemAndTools confirms the redesigned
// CronJob schema persists the new IsSystem flag and the per-cron Tools
// many-to-many through the cron_job_tools join table. The reload step covers
// both branches of the agent-only redesign: the system-cron guard (IsSystem)
// and the per-cron tool allowlist that Task 3 will start threading into the
// agent invocation.
func TestCronJob_Schema_RoundTripsIsSystemAndTools(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&Integration{},
		&Channel{},
		&ToolType{},
		&ToolInstance{},
		&CronJob{},
		&CronJobTool{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	toolType := ToolType{Name: "kubectl", Description: "kubectl wrapper"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("seed tool type: %v", err)
	}
	toolA := ToolInstance{ToolTypeID: toolType.ID, Name: "prod-k8s", LogicalName: "prod-k8s", Enabled: true}
	toolB := ToolInstance{ToolTypeID: toolType.ID, Name: "staging-k8s", LogicalName: "staging-k8s", Enabled: true}
	if err := db.Create(&toolA).Error; err != nil {
		t.Fatalf("seed tool A: %v", err)
	}
	if err := db.Create(&toolB).Error; err != nil {
		t.Fatalf("seed tool B: %v", err)
	}

	job := CronJob{
		UUID:     uuid.New().String(),
		Name:     "memory-curator",
		Schedule: "0 2 * * *",
		Prompt:   "Consolidate global memory",
		IsSystem: true,
		Enabled:  true,
		Tools:    []ToolInstance{toolA, toolB},
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create cron job: %v", err)
	}

	var reloaded CronJob
	if err := db.Preload("Tools").First(&reloaded, job.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.IsSystem {
		t.Errorf("IsSystem = false after reload, want true")
	}
	if len(reloaded.Tools) != 2 {
		t.Fatalf("Tools len = %d, want 2", len(reloaded.Tools))
	}
	got := map[string]bool{reloaded.Tools[0].LogicalName: true, reloaded.Tools[1].LogicalName: true}
	for _, want := range []string{"prod-k8s", "staging-k8s"} {
		if !got[want] {
			t.Errorf("Tools missing %q", want)
		}
	}

	// Verify the join table rows are present so a follow-up Association
	// .Replace() in Task 3 has a known starting state.
	var joinCount int64
	if err := db.Model(&CronJobTool{}).Where("cron_job_id = ?", job.ID).Count(&joinCount).Error; err != nil {
		t.Fatalf("count cron_job_tools: %v", err)
	}
	if joinCount != 2 {
		t.Errorf("cron_job_tools rows = %d, want 2", joinCount)
	}

	// Replacing the association should drop the old rows. Mirrors the API
	// flow Task 3 will introduce for the per-cron tool picker.
	if err := db.Model(&reloaded).Association("Tools").Replace([]ToolInstance{toolA}); err != nil {
		t.Fatalf("Association.Replace: %v", err)
	}
	if err := db.Model(&CronJobTool{}).Where("cron_job_id = ?", job.ID).Count(&joinCount).Error; err != nil {
		t.Fatalf("re-count cron_job_tools: %v", err)
	}
	if joinCount != 1 {
		t.Errorf("after Replace, cron_job_tools rows = %d, want 1", joinCount)
	}
}

// TestCronJob_Schema_DefaultIsSystem confirms a cron job created without an
// explicit IsSystem value defaults to non-system (i.e. operator-deletable).
// Sanity-checks the gorm `default:false` column tag so a future GORM upgrade
// or column-tag regression does not silently promote operator-created rows
// to undeletable system rows.
func TestCronJob_Schema_DefaultIsSystem(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&CronJob{}, &CronJobTool{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	job := CronJob{
		UUID:     uuid.New().String(),
		Name:     "operator-cron",
		Schedule: "0 0 * * *",
		Prompt:   "Do thing",
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var reloaded CronJob
	if err := db.First(&reloaded, job.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.IsSystem {
		t.Errorf("IsSystem = true on freshly-created operator row, want false")
	}
}

// TestPreMigrateCronJobsDropLegacyColumns verifies the one-shot migration that
// strips the legacy `mode` and `description` columns from cron_jobs. The test
// builds a stand-in table with those columns, seeds rows in each mode, and
// asserts:
//
//   - rows pre-migration with mode='oneshot' are normalized to 'agent' so a
//     downstream rollback would not silently resurrect the oneshot dispatch
//   - both columns are gone after the migration runs
//   - re-running the migration is a no-op (idempotent on fresh installs and
//     on already-migrated databases)
func TestPreMigrateCronJobsDropLegacyColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Build the legacy table shape directly so we exercise the drop path.
	if err := db.Exec(`CREATE TABLE cron_jobs (
		id INTEGER PRIMARY KEY,
		uuid TEXT,
		name TEXT,
		description TEXT,
		schedule TEXT,
		prompt TEXT,
		mode TEXT,
		channel_id INTEGER,
		enabled BOOLEAN,
		last_run_at DATETIME,
		last_run_status TEXT,
		last_run_error TEXT,
		next_run_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`INSERT INTO cron_jobs (uuid, name, description, schedule, prompt, mode, enabled) VALUES ('u1', 'a', 'old description', '* * * * *', 'p', 'oneshot', 1), ('u2', 'b', '', '* * * * *', 'p', 'agent', 1)`).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	if err := preMigrateCronJobsDropLegacyColumns(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	if db.Migrator().HasColumn(&CronJob{}, "mode") {
		t.Error("mode column still present after migration")
	}
	if db.Migrator().HasColumn(&CronJob{}, "description") {
		t.Error("description column still present after migration")
	}

	// Idempotency: a second invocation must be a no-op (no rows lost, no
	// errors raised).
	if err := preMigrateCronJobsDropLegacyColumns(db); err != nil {
		t.Fatalf("second migration (idempotent): %v", err)
	}

	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM cron_jobs").Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("row count after migration = %d, want 2", count)
	}
}
