package database

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupFormattingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&FormattingSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	// Point global DB to test DB so package-level helpers operate on it.
	origDB := DB
	t.Cleanup(func() { DB = origDB })
	DB = db
	return db
}

func TestFormattingSettings_TableName(t *testing.T) {
	if got := (FormattingSettings{}).TableName(); got != "formatting_settings" {
		t.Errorf("TableName() = %q, want %q", got, "formatting_settings")
	}
}

func TestFormattingSettings_ZeroValues(t *testing.T) {
	// Verify a zero-value struct has the expected Go zero values.
	// (GORM defaults apply at the DB level, not the struct level.)
	s := FormattingSettings{}
	if s.Enabled {
		t.Error("zero-value Enabled should be false")
	}
	if s.SystemPrompt != "" {
		t.Errorf("zero-value SystemPrompt = %q, want empty", s.SystemPrompt)
	}
	if s.MaxTokens != 0 {
		t.Errorf("zero-value MaxTokens = %d, want 0", s.MaxTokens)
	}
	if s.Temperature != 0 {
		t.Errorf("zero-value Temperature = %f, want 0", s.Temperature)
	}
}

func TestDefaultFormattingSettings(t *testing.T) {
	defaults := DefaultFormattingSettings()
	if defaults == nil {
		t.Fatal("DefaultFormattingSettings() returned nil")
	}
	if defaults.SingletonKey != "default" {
		t.Errorf("SingletonKey = %q, want %q", defaults.SingletonKey, "default")
	}
	if defaults.Enabled {
		t.Error("Enabled should be false by default (opt-in feature)")
	}
	if defaults.SystemPrompt == "" {
		t.Error("SystemPrompt should be populated with a default prompt")
	}
	if defaults.SystemPrompt != DefaultFormattingPrompt {
		t.Errorf("SystemPrompt should match DefaultFormattingPrompt constant")
	}
	if defaults.MaxTokens != 1500 {
		t.Errorf("MaxTokens = %d, want 1500", defaults.MaxTokens)
	}
	if defaults.Temperature != 0.2 {
		t.Errorf("Temperature = %f, want 0.2", defaults.Temperature)
	}

	// The default prompt should be substantive — instruct the LLM to keep
	// status, actions, and recommendations so the formatted output stays
	// useful even before an operator authors a custom prompt.
	for _, keyword := range []string{"Status", "Summary", "Actions", "Recommend"} {
		if !strings.Contains(defaults.SystemPrompt, keyword) {
			t.Errorf("default prompt missing expected keyword %q:\n%s", keyword, defaults.SystemPrompt)
		}
	}
}

func TestGetOrCreateFormattingSettings_NilDB(t *testing.T) {
	origDB := DB
	DB = nil
	defer func() { DB = origDB }()

	_, err := GetOrCreateFormattingSettings()
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
	if err.Error() != "database not initialized" {
		t.Errorf("error = %q, want %q", err.Error(), "database not initialized")
	}
}

func TestGetOrCreateFormattingSettings_CreatesDefault(t *testing.T) {
	setupFormattingTestDB(t)

	settings, err := GetOrCreateFormattingSettings()
	if err != nil {
		t.Fatalf("GetOrCreateFormattingSettings failed: %v", err)
	}
	if settings == nil {
		t.Fatal("expected non-nil settings")
	}
	if settings.ID == 0 {
		t.Error("expected ID to be set after create")
	}
	if settings.SingletonKey != "default" {
		t.Errorf("SingletonKey = %q, want %q", settings.SingletonKey, "default")
	}
	if settings.Enabled {
		t.Error("Enabled should default to false")
	}
	if settings.SystemPrompt != DefaultFormattingPrompt {
		t.Error("SystemPrompt should equal DefaultFormattingPrompt on first create")
	}
	if settings.MaxTokens != 1500 {
		t.Errorf("MaxTokens = %d, want 1500", settings.MaxTokens)
	}
	if settings.Temperature != 0.2 {
		t.Errorf("Temperature = %f, want 0.2", settings.Temperature)
	}
}

func TestGetOrCreateFormattingSettings_Idempotent(t *testing.T) {
	setupFormattingTestDB(t)

	first, err := GetOrCreateFormattingSettings()
	if err != nil {
		t.Fatalf("first GetOrCreate failed: %v", err)
	}
	second, err := GetOrCreateFormattingSettings()
	if err != nil {
		t.Fatalf("second GetOrCreate failed: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected idempotent IDs; got first=%d second=%d", first.ID, second.ID)
	}

	// Verify only one row exists.
	var count int64
	if err := DB.Model(&FormattingSettings{}).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 formatting_settings row, got %d", count)
	}
}

func TestUpdateFormattingSettings_RoundTrip(t *testing.T) {
	setupFormattingTestDB(t)

	settings, err := GetOrCreateFormattingSettings()
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	settings.Enabled = true
	settings.SystemPrompt = "Respond as JSON with status and summary keys."
	settings.MaxTokens = 2048
	settings.Temperature = 0.5

	if err := UpdateFormattingSettings(settings); err != nil {
		t.Fatalf("UpdateFormattingSettings failed: %v", err)
	}

	reloaded, err := GetOrCreateFormattingSettings()
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if reloaded.ID != settings.ID {
		t.Errorf("ID changed across update: before=%d after=%d", settings.ID, reloaded.ID)
	}
	if !reloaded.Enabled {
		t.Error("Enabled should persist as true after update")
	}
	if reloaded.SystemPrompt != "Respond as JSON with status and summary keys." {
		t.Errorf("SystemPrompt did not round-trip: got %q", reloaded.SystemPrompt)
	}
	if reloaded.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", reloaded.MaxTokens)
	}
	if reloaded.Temperature != 0.5 {
		t.Errorf("Temperature = %f, want 0.5", reloaded.Temperature)
	}
}
