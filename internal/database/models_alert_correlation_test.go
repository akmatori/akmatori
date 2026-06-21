package database

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestAlertCorrelationLog_AutoMigrate confirms that AutoMigrate succeeds with
// the new AlertCorrelationLog model and that basic CRUD works.
func TestAlertCorrelationLog_AutoMigrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AlertCorrelationLog{}); err != nil {
		t.Fatalf("AutoMigrate AlertCorrelationLog: %v", err)
	}

	row := AlertCorrelationLog{
		SourceUUID:          "src-uuid-1",
		AlertName:           "HighCPU",
		TargetHost:          "prod-01",
		MatchedIncidentUUID: "inc-uuid-1",
		Confidence:          0.92,
		Reasoning:           "Same host and alert name within window",
		CreatedAt:           time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create row: %v", err)
	}
	if row.ID == 0 {
		t.Error("expected non-zero ID after create")
	}

	var reloaded AlertCorrelationLog
	if err := db.First(&reloaded, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SourceUUID != "src-uuid-1" {
		t.Errorf("SourceUUID = %q, want %q", reloaded.SourceUUID, "src-uuid-1")
	}
	if reloaded.MatchedIncidentUUID != "inc-uuid-1" {
		t.Errorf("MatchedIncidentUUID = %q, want %q", reloaded.MatchedIncidentUUID, "inc-uuid-1")
	}
	if reloaded.Confidence != 0.92 {
		t.Errorf("Confidence = %v, want 0.92", reloaded.Confidence)
	}
}

// TestAlertCorrelationLog_TableName confirms the table name override.
func TestAlertCorrelationLog_TableName(t *testing.T) {
	if got := (AlertCorrelationLog{}).TableName(); got != "alert_correlation_logs" {
		t.Errorf("TableName = %q, want %q", got, "alert_correlation_logs")
	}
}

// TestGeneralSettings_AlertCorrelationColumns confirms AutoMigrate succeeds with
// the correlation columns on GeneralSettings and that nil (pointer) defaults
// are preserved on a freshly-created row.
func TestGeneralSettings_AlertCorrelationColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&GeneralSettings{}); err != nil {
		t.Fatalf("AutoMigrate GeneralSettings: %v", err)
	}

	row := GeneralSettings{BaseURL: "https://example.com"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create row: %v", err)
	}

	var reloaded GeneralSettings
	if err := db.First(&reloaded, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AlertCorrelationEnabled != nil {
		t.Errorf("AlertCorrelationEnabled = %v, want nil", reloaded.AlertCorrelationEnabled)
	}
	if reloaded.AlertMonitorWindowMinutes != nil {
		t.Errorf("AlertMonitorWindowMinutes = %v, want nil", reloaded.AlertMonitorWindowMinutes)
	}

	// Verify that explicit values round-trip correctly.
	enabled := true
	if err := db.Model(&reloaded).Updates(map[string]interface{}{
		"alert_correlation_enabled": enabled,
	}).Error; err != nil {
		t.Fatalf("update correlation columns: %v", err)
	}

	var updated GeneralSettings
	if err := db.First(&updated, row.ID).Error; err != nil {
		t.Fatalf("reload after update: %v", err)
	}
	if updated.AlertCorrelationEnabled == nil || *updated.AlertCorrelationEnabled != true {
		t.Errorf("AlertCorrelationEnabled = %v, want *true", updated.AlertCorrelationEnabled)
	}
}
