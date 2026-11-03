package database

import (
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// legacyToolMappingSchema builds the pre-migration shape: cron_jobs without
// the new columns, plus the skill_tools and cron_job_tools join tables.
func legacyToolMappingSchema(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE tool_types (id INTEGER PRIMARY KEY, name TEXT, description TEXT, kind TEXT NOT NULL DEFAULT '', connector_id INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE tool_instances (id INTEGER PRIMARY KEY, tool_type_id INTEGER, name TEXT, logical_name TEXT, settings TEXT, enabled BOOLEAN, connector_id INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE cron_jobs (id INTEGER PRIMARY KEY, uuid TEXT, name TEXT, schedule TEXT, prompt TEXT, is_system BOOLEAN, channel_id INTEGER, enabled BOOLEAN, post_results BOOLEAN, last_run_at DATETIME, last_run_status TEXT, last_run_error TEXT, next_run_at DATETIME, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE cron_job_tools (cron_job_id INTEGER, tool_instance_id INTEGER, created_at DATETIME, PRIMARY KEY (cron_job_id, tool_instance_id))`,
		`CREATE TABLE skill_tools (skill_id INTEGER, tool_instance_id INTEGER, created_at DATETIME, PRIMARY KEY (skill_id, tool_instance_id))`,
		`INSERT INTO tool_types (id, name) VALUES (1, 'ssh'), (2, 'incidents'), (3, 'proposals'), (4, 'events')`,
		`INSERT INTO tool_instances (id, tool_type_id, name, logical_name, enabled) VALUES (10, 1, 'prod-ssh', 'prod-ssh', 1), (11, 2, 'Incidents', 'incidents', 1), (12, 3, 'Proposals', 'proposals', 1), (13, 1, 'old-ssh', 'old-ssh', 0)`,
		`INSERT INTO cron_jobs (id, uuid, name, schedule, prompt, is_system, enabled) VALUES
			(1, 'u1', 'disk-check', '* * * * *', 'p', 0, 1),
			(2, 'u2', 'Dreaming', '* * * * *', 'p', 1, 1),
			(3, 'u3', 'improvement-evaluator', '* * * * *', 'p', 1, 0),
			(4, 'u4', 'disabled-tool-only', '* * * * *', 'p', 0, 1)`,
		`INSERT INTO cron_job_tools (cron_job_id, tool_instance_id) VALUES (1, 10), (3, 11), (3, 12), (4, 13)`,
		`INSERT INTO skill_tools (skill_id, tool_instance_id) VALUES (1, 10)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	return db
}

type migratedCron struct {
	ToolsEnabled bool
	BuiltinTools string
}

func readMigratedCron(t *testing.T, db *gorm.DB, id int) (bool, []string) {
	t.Helper()
	var row migratedCron
	if err := db.Raw("SELECT tools_enabled, builtin_tools FROM cron_jobs WHERE id = ?", id).Scan(&row).Error; err != nil {
		t.Fatalf("read cron %d: %v", id, err)
	}
	var list []string
	if err := json.Unmarshal([]byte(row.BuiltinTools), &list); err != nil {
		t.Fatalf("cron %d builtin_tools %q: %v", id, row.BuiltinTools, err)
	}
	return row.ToolsEnabled, list
}

func equalList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPreMigrateToolMappings_Backfill(t *testing.T) {
	db := legacyToolMappingSchema(t)
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cases := []struct {
		id       int
		enabled  bool
		builtins []string
	}{
		{1, true, []string{"events", "runbooks", "memory"}},                             // connector tool
		{2, false, []string{"events", "runbooks", "memory"}},                            // no tools (Dreaming)
		{3, false, []string{"events", "runbooks", "memory", "incidents", "proposals"}}, // evaluator keeps its built-ins
		{4, true, []string{"events", "runbooks", "memory"}},                             // a disabled connector still shows intent
	}
	for _, c := range cases {
		enabled, builtins := readMigratedCron(t, db, c.id)
		if enabled != c.enabled || !equalList(builtins, c.builtins) {
			t.Errorf("cron %d: tools_enabled=%v builtin_tools=%v, want %v %v", c.id, enabled, builtins, c.enabled, c.builtins)
		}
	}
	for _, table := range []string{"cron_job_tools", "skill_tools"} {
		if db.Migrator().HasTable(table) {
			t.Errorf("%s still exists", table)
		}
	}
	// Second start: a no-op.
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if enabled, _ := readMigratedCron(t, db, 1); !enabled {
		t.Error("second run changed cron 1")
	}
}

func TestPreMigrateToolMappings_UpgradeWithoutCronTools(t *testing.T) {
	db := legacyToolMappingSchema(t)
	if err := db.Exec("DELETE FROM cron_job_tools").Error; err != nil {
		t.Fatal(err)
	}
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, id := range []int{1, 2, 3, 4} {
		enabled, builtins := readMigratedCron(t, db, id)
		if enabled || !equalList(builtins, []string{"events", "runbooks", "memory"}) {
			t.Errorf("cron %d: %v %v, want false + defaults", id, enabled, builtins)
		}
	}
}

func TestPreMigrateToolMappings_FreshInstallIsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if err := db.AutoMigrate(&CronJob{}); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&CronJob{}, "tools_enabled") || !db.Migrator().HasColumn(&CronJob{}, "builtin_tools") {
		t.Error("AutoMigrate did not add the new columns")
	}
}

func TestPreMigrateToolMappings_OnlySkillToolsLeft(t *testing.T) {
	db := legacyToolMappingSchema(t)
	if err := db.Exec("DROP TABLE cron_job_tools").Error; err != nil {
		t.Fatal(err)
	}
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Migrator().HasTable("skill_tools") {
		t.Error("skill_tools still exists")
	}
}

// A cron_jobs table from before cron_job_tools existed has neither the join
// table nor the new columns. Its jobs must still get the fail-closed backfill,
// not AutoMigrate's tools_enabled=true with no built-ins.
func TestPreMigrateToolMappings_ScheduleBeforeCronToolsIsBackfilled(t *testing.T) {
	db := legacyToolMappingSchema(t)
	if err := db.Exec("DROP TABLE cron_job_tools").Error; err != nil {
		t.Fatal(err)
	}
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&CronJob{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Second start: columns exist, no join table → no-op that keeps the rows.
	if err := db.Exec("UPDATE cron_jobs SET builtin_tools = ? WHERE id = 1", `["events"]`).Error; err != nil {
		t.Fatal(err)
	}
	if err := preMigrateToolMappings(db); err != nil {
		t.Fatalf("second start: %v", err)
	}
	for _, id := range []int{2, 3, 4} {
		enabled, builtins := readMigratedCron(t, db, id)
		if enabled || !equalList(builtins, []string{"events", "runbooks", "memory"}) {
			t.Errorf("cron %d: %v %v, want false + defaults", id, enabled, builtins)
		}
	}
	if _, builtins := readMigratedCron(t, db, 1); !equalList(builtins, []string{"events"}) {
		t.Errorf("second start rewrote cron 1: %v", builtins)
	}
}

func TestLegacyCronToolFields(t *testing.T) {
	enabled, builtins := LegacyCronToolFields([]string{"proposals", "jira", "incidents"})
	if !enabled || !equalList(builtins, []string{"events", "runbooks", "memory", "incidents", "proposals"}) {
		t.Errorf("got %v %v", enabled, builtins)
	}
	enabled, builtins = LegacyCronToolFields(nil)
	if enabled || !equalList(builtins, []string{"events", "runbooks", "memory"}) {
		t.Errorf("empty: got %v %v", enabled, builtins)
	}
}
