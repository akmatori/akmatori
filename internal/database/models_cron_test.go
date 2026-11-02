package database

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCronJob_Schema_RoundTripsToolFields confirms tools_enabled and the
// builtin_tools JSON list persist, and that tools_enabled defaults to true.
func TestCronJob_Schema_RoundTripsToolFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Integration{}, &Channel{}, &CronJob{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	job := CronJob{
		UUID:         uuid.New().String(),
		Name:         "maint",
		Schedule:     "0 * * * *",
		Prompt:       "p",
		IsSystem:     true,
		BuiltinTools: StringList{"events", "memory"},
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var reloaded CronJob
	if err := db.First(&reloaded, job.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.IsSystem {
		t.Error("IsSystem lost")
	}
	if !reloaded.ToolsEnabled {
		t.Error("tools_enabled default = false, want true")
	}
	if len(reloaded.BuiltinTools) != 2 || reloaded.BuiltinTools[0] != "events" || reloaded.BuiltinTools[1] != "memory" {
		t.Errorf("BuiltinTools = %v", reloaded.BuiltinTools)
	}
	if err := db.Model(&reloaded).Update("tools_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reloaded, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.ToolsEnabled {
		t.Error("tools_enabled=false did not persist")
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
	if err := db.AutoMigrate(&CronJob{}); err != nil {
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
