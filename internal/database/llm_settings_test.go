package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLLMTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&LLMSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	// Point global DB to test DB
	DB = db
	return db
}

func TestCreateLLMSettings(t *testing.T) {
	setupLLMTestDB(t)

	settings := &LLMSettings{
		Name:          "My OpenAI",
		Provider:      LLMProviderOpenAI,
		APIKey:        "sk-test",
		Model:         "gpt-4",
		ThinkingLevel: ThinkingLevelMedium,
	}
	err := CreateLLMSettings(settings)
	if err != nil {
		t.Fatalf("CreateLLMSettings failed: %v", err)
	}
	if settings.ID == 0 {
		t.Error("expected ID to be set after create")
	}
}

func TestCreateLLMSettings_NameUniqueness(t *testing.T) {
	setupLLMTestDB(t)

	s1 := &LLMSettings{Name: "Production", Provider: LLMProviderOpenAI, Model: "gpt-4"}
	if err := CreateLLMSettings(s1); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	// Same name, different provider should fail
	s2 := &LLMSettings{Name: "Production", Provider: LLMProviderAnthropic, Model: "claude-sonnet-4-6"}
	err := CreateLLMSettings(s2)
	if err == nil {
		t.Error("expected error for duplicate name, got nil")
	}
}

func TestCreateLLMSettings_SameProviderDifferentNames(t *testing.T) {
	setupLLMTestDB(t)

	s1 := &LLMSettings{Name: "OpenAI Prod", Provider: LLMProviderOpenAI, Model: "gpt-4"}
	if err := CreateLLMSettings(s1); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	s2 := &LLMSettings{Name: "OpenAI Dev", Provider: LLMProviderOpenAI, Model: "gpt-3.5-turbo"}
	if err := CreateLLMSettings(s2); err != nil {
		t.Fatalf("second create with same provider but different name should succeed: %v", err)
	}
}

func TestGetLLMSettingsByID(t *testing.T) {
	setupLLMTestDB(t)

	created := &LLMSettings{Name: "Test Config", Provider: LLMProviderAnthropic, Model: "claude-sonnet-4-6"}
	if err := CreateLLMSettings(created); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	found, err := GetLLMSettingsByID(created.ID)
	if err != nil {
		t.Fatalf("GetLLMSettingsByID failed: %v", err)
	}
	if found.Name != "Test Config" {
		t.Errorf("expected name 'Test Config', got %q", found.Name)
	}
	if found.Provider != LLMProviderAnthropic {
		t.Errorf("expected provider anthropic, got %s", found.Provider)
	}
}

func TestGetLLMSettingsByID_NotFound(t *testing.T) {
	setupLLMTestDB(t)

	_, err := GetLLMSettingsByID(999)
	if err == nil {
		t.Error("expected error for non-existent ID, got nil")
	}
}

func TestSetActiveLLMConfig(t *testing.T) {
	setupLLMTestDB(t)

	s1 := &LLMSettings{Name: "Config A", Provider: LLMProviderOpenAI, Active: true}
	s2 := &LLMSettings{Name: "Config B", Provider: LLMProviderAnthropic, Active: false}
	CreateLLMSettings(s1)
	CreateLLMSettings(s2)

	// Switch active to Config B
	if err := SetActiveLLMConfig(s2.ID); err != nil {
		t.Fatalf("SetActiveLLMConfig failed: %v", err)
	}

	// Verify Config A is no longer active
	a, _ := GetLLMSettingsByID(s1.ID)
	if a.Active {
		t.Error("expected Config A to be inactive")
	}

	// Verify Config B is active
	b, _ := GetLLMSettingsByID(s2.ID)
	if !b.Active {
		t.Error("expected Config B to be active")
	}

	// Verify GetLLMSettings returns the active one
	active, err := GetLLMSettings()
	if err != nil {
		t.Fatalf("GetLLMSettings failed: %v", err)
	}
	if active.ID != s2.ID {
		t.Errorf("expected active config ID %d, got %d", s2.ID, active.ID)
	}
}

func TestSetActiveLLMConfig_NotFound(t *testing.T) {
	setupLLMTestDB(t)

	err := SetActiveLLMConfig(999)
	if err == nil {
		t.Error("expected error for non-existent ID, got nil")
	}
}

func TestDeleteLLMSettings(t *testing.T) {
	setupLLMTestDB(t)

	s1 := &LLMSettings{Name: "Active", Provider: LLMProviderOpenAI, Active: true}
	s2 := &LLMSettings{Name: "Inactive", Provider: LLMProviderAnthropic, Active: false}
	CreateLLMSettings(s1)
	CreateLLMSettings(s2)

	// Deleting inactive config should succeed
	if err := DeleteLLMSettings(s2.ID); err != nil {
		t.Fatalf("DeleteLLMSettings failed: %v", err)
	}

	// Verify it's gone
	_, err := GetLLMSettingsByID(s2.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestDeleteLLMSettings_ActiveConfigRejected(t *testing.T) {
	setupLLMTestDB(t)

	s := &LLMSettings{Name: "Active One", Provider: LLMProviderOpenAI, Active: true}
	CreateLLMSettings(s)

	err := DeleteLLMSettings(s.ID)
	if err == nil {
		t.Error("expected error when deleting active config, got nil")
	}
}

func TestDeleteLLMSettings_NotFound(t *testing.T) {
	setupLLMTestDB(t)

	err := DeleteLLMSettings(999)
	if err == nil {
		t.Error("expected error for non-existent ID, got nil")
	}
}

func TestGetAllLLMSettings_OrderByProviderThenName(t *testing.T) {
	setupLLMTestDB(t)

	configs := []*LLMSettings{
		{Name: "OpenAI Beta", Provider: LLMProviderOpenAI},
		{Name: "Anthropic", Provider: LLMProviderAnthropic},
		{Name: "OpenAI Alpha", Provider: LLMProviderOpenAI},
	}
	for _, c := range configs {
		if err := CreateLLMSettings(c); err != nil {
			t.Fatalf("create failed: %v", err)
		}
	}

	all, err := GetAllLLMSettings()
	if err != nil {
		t.Fatalf("GetAllLLMSettings failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(all))
	}

	// anthropic comes before openai alphabetically
	if all[0].Provider != LLMProviderAnthropic {
		t.Errorf("expected first config to be anthropic, got %s", all[0].Provider)
	}
	// OpenAI Alpha before OpenAI Beta
	if all[1].Name != "OpenAI Alpha" {
		t.Errorf("expected second config name 'OpenAI Alpha', got %q", all[1].Name)
	}
	if all[2].Name != "OpenAI Beta" {
		t.Errorf("expected third config name 'OpenAI Beta', got %q", all[2].Name)
	}
}

func TestMigrateLLMSettingsName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&LLMSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Insert rows with empty names (simulating pre-migration state)
	db.Exec("INSERT INTO llm_settings (name, provider, model, thinking_level, active) VALUES ('', 'openai', 'gpt-4', 'medium', 1)")
	db.Exec("INSERT INTO llm_settings (name, provider, model, thinking_level, active) VALUES ('', 'anthropic', 'claude-sonnet-4-6', 'medium', 0)")
	// One row that already has a name - should not be touched
	db.Exec("INSERT INTO llm_settings (name, provider, model, thinking_level, active) VALUES ('My Custom', 'custom', '', 'medium', 0)")

	if err := migrateLLMSettingsName(db); err != nil {
		t.Fatalf("migrateLLMSettingsName failed: %v", err)
	}

	var rows []LLMSettings
	db.Order("id asc").Find(&rows)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Name != "OpenAI" {
		t.Errorf("expected first row name 'OpenAI', got %q", rows[0].Name)
	}
	if rows[1].Name != "Anthropic" {
		t.Errorf("expected second row name 'Anthropic', got %q", rows[1].Name)
	}
	if rows[2].Name != "My Custom" {
		t.Errorf("expected third row name unchanged 'My Custom', got %q", rows[2].Name)
	}
}

func TestMigrateLLMSettingsName_NoRowsToMigrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&LLMSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// No rows with empty names - should be a no-op
	db.Exec("INSERT INTO llm_settings (name, provider, model, thinking_level, active) VALUES ('Existing', 'openai', 'gpt-4', 'medium', 1)")

	if err := migrateLLMSettingsName(db); err != nil {
		t.Fatalf("migrateLLMSettingsName failed: %v", err)
	}

	var row LLMSettings
	db.First(&row)
	if row.Name != "Existing" {
		t.Errorf("expected name 'Existing', got %q", row.Name)
	}
}

func TestSeedLLMProviders_SetsNames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&LLMSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	DB = db

	if err := seedLLMProviders(); err != nil {
		t.Fatalf("seedLLMProviders failed: %v", err)
	}

	var rows []LLMSettings
	db.Order("provider asc").Find(&rows)

	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}

	// Verify each row has a non-empty name matching its provider display name
	for _, row := range rows {
		expected := ProviderDisplayName(row.Provider)
		if row.Name != expected {
			t.Errorf("provider %s: expected name %q, got %q", row.Provider, expected, row.Name)
		}
	}
}

func TestProviderDisplayName(t *testing.T) {
	tests := []struct {
		provider LLMProvider
		expected string
	}{
		{LLMProviderOpenAI, "OpenAI"},
		{LLMProviderAnthropic, "Anthropic"},
		{LLMProviderGoogle, "Google"},
		{LLMProviderOpenRouter, "OpenRouter"},
		{LLMProviderCustom, "Custom"},
		{LLMProvider("unknown"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			if got := ProviderDisplayName(tt.provider); got != tt.expected {
				t.Errorf("ProviderDisplayName(%q) = %q, want %q", tt.provider, got, tt.expected)
			}
		})
	}
}
