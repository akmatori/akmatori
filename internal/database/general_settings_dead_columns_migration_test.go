package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The correlation redesign reduced the correlation gate to a single Enabled
// flag plus package-level constants, and removed alert suppression entirely.
// GORM AutoMigrate never drops columns, so upgrade installs still carry the
// old tuning columns with stale values that look configurable but affect
// nothing.

// legacyGeneralSettingsDDL is the general_settings table as an upgrade install
// carries it: current columns plus the dead ones.
const legacyGeneralSettingsDDL = `CREATE TABLE general_settings (
	id integer PRIMARY KEY AUTOINCREMENT,
	base_url text,
	created_at datetime,
	updated_at datetime,
	alert_correlation_enabled numeric,
	alert_correlation_window_minutes integer,
	alert_correlation_threshold real,
	alert_correlation_max_candidates integer,
	alert_suppression_enabled numeric,
	alert_suppression_threshold real,
	alert_correlation_long_window_days integer,
	alert_correlation_fingerprint_window_minutes integer,
	alert_monitor_window_minutes integer,
	incident_merge_enabled numeric
)`

func TestPreMigrateGeneralSettingsDropDeadColumns_DropsAll(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.Exec(legacyGeneralSettingsDDL).Error; err != nil {
		t.Fatalf("create legacy general_settings table: %v", err)
	}
	if err := db.Exec(`INSERT INTO general_settings
		(base_url, alert_correlation_enabled, alert_correlation_threshold, alert_monitor_window_minutes)
		VALUES ('https://akmatori.example', 1, 0.85, 120)`).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if err := preMigrateGeneralSettingsDropDeadColumns(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	for _, col := range deadGeneralSettingsColumns {
		if db.Migrator().HasColumn(&GeneralSettings{}, col) {
			t.Errorf("column %s still present after migration", col)
		}
	}

	// Live configuration must survive the drop untouched.
	var row GeneralSettings
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load row after migration: %v", err)
	}
	if row.BaseURL != "https://akmatori.example" {
		t.Errorf("BaseURL = %q, want https://akmatori.example", row.BaseURL)
	}
	if row.AlertCorrelationEnabled == nil || !*row.AlertCorrelationEnabled {
		t.Error("AlertCorrelationEnabled should still be true")
	}
	if row.AlertMonitorWindowMinutes == nil || *row.AlertMonitorWindowMinutes != 120 {
		t.Errorf("AlertMonitorWindowMinutes = %v, want 120", row.AlertMonitorWindowMinutes)
	}
}

func TestPreMigrateGeneralSettingsDropDeadColumns_Idempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.Exec(legacyGeneralSettingsDDL).Error; err != nil {
		t.Fatalf("create legacy general_settings table: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := preMigrateGeneralSettingsDropDeadColumns(db); err != nil {
			t.Fatalf("migration run %d failed: %v", i+1, err)
		}
	}
}

// A fresh install has no general_settings table when migrations start.
func TestPreMigrateGeneralSettingsDropDeadColumns_NoTable_IsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := preMigrateGeneralSettingsDropDeadColumns(db); err != nil {
		t.Fatalf("migration failed on a table-less DB: %v", err)
	}
}

// A fresh install that has already run AutoMigrate has the current model's
// columns and none of the dead ones.
func TestPreMigrateGeneralSettingsDropDeadColumns_FreshSchema_IsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&GeneralSettings{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := preMigrateGeneralSettingsDropDeadColumns(db); err != nil {
		t.Fatalf("migration failed on a fresh schema: %v", err)
	}
	if !db.Migrator().HasColumn(&GeneralSettings{}, "alert_monitor_window_minutes") {
		t.Error("migration removed a live column")
	}
}
