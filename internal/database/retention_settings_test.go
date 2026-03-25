package database

import (
	"testing"
)

func TestRetentionSettings_TableName(t *testing.T) {
	s := RetentionSettings{}
	if got := s.TableName(); got != "retention_settings" {
		t.Errorf("TableName() = %q, want %q", got, "retention_settings")
	}
}

func TestRetentionSettings_Defaults(t *testing.T) {
	// Verify that a zero-value struct has the expected Go zero values
	// (GORM defaults apply at DB level, not struct level)
	s := RetentionSettings{}
	if s.Enabled {
		t.Error("zero-value Enabled should be false (GORM default applies at DB level)")
	}
	if s.RetentionDays != 0 {
		t.Errorf("zero-value RetentionDays = %d, want 0", s.RetentionDays)
	}
	if s.CleanupIntervalHours != 0 {
		t.Errorf("zero-value CleanupIntervalHours = %d, want 0", s.CleanupIntervalHours)
	}
}

func TestDefaultRetentionSettings_SingletonKey(t *testing.T) {
	defaults := DefaultRetentionSettings()
	if defaults.SingletonKey != "default" {
		t.Errorf("SingletonKey = %q, want %q", defaults.SingletonKey, "default")
	}
}

func TestGetOrCreateRetentionSettings_NilDB(t *testing.T) {
	// Save and restore global DB
	origDB := DB
	DB = nil
	defer func() { DB = origDB }()

	_, err := GetOrCreateRetentionSettings()
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
	if err.Error() != "database not initialized" {
		t.Errorf("error = %q, want %q", err.Error(), "database not initialized")
	}
}
