package handlers

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupBuildLLMTest(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate llm_settings: %v", err)
	}
	database.DB = db
}

func TestBuildLLMSettingsForWorker_NilInput(t *testing.T) {
	result := BuildLLMSettingsForWorker(nil)
	if result != nil {
		t.Errorf("nil input should return nil, got %+v", result)
	}
}

func TestBuildLLMSettingsForWorker_NotEnabled(t *testing.T) {
	settings := &database.LLMSettings{
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-ant-test",
		Enabled:  false,
	}
	result := BuildLLMSettingsForWorker(settings)
	if result != nil {
		t.Errorf("disabled settings should return nil, got %+v", result)
	}
}

func TestBuildLLMSettingsForWorker_NotConfigured(t *testing.T) {
	settings := &database.LLMSettings{
		Provider: database.LLMProviderAnthropic,
		APIKey:   "",
		Enabled:  true,
	}
	result := BuildLLMSettingsForWorker(settings)
	if result != nil {
		t.Errorf("unconfigured (no API key) settings should return nil, got %+v", result)
	}
}

func TestBuildLLMSettingsForWorker_ActiveConfig(t *testing.T) {
	settings := &database.LLMSettings{
		Name:          "My Anthropic",
		Provider:      database.LLMProviderAnthropic,
		APIKey:        "sk-ant-test-123",
		Model:         "claude-sonnet-4-20250514",
		ThinkingLevel: database.ThinkingLevelHigh,
		BaseURL:       "https://custom.api.example.com",
		Enabled:       true,
		Active:        true,
	}
	result := BuildLLMSettingsForWorker(settings)
	testhelpers.AssertNotNil(t, result, "active config should return non-nil")
	testhelpers.AssertEqual(t, "anthropic", result.Provider, "provider")
	testhelpers.AssertEqual(t, "sk-ant-test-123", result.APIKey, "api key")
	testhelpers.AssertEqual(t, "claude-sonnet-4-20250514", result.Model, "model")
	testhelpers.AssertEqual(t, "high", result.ThinkingLevel, "thinking level")
	testhelpers.AssertEqual(t, "https://custom.api.example.com", result.BaseURL, "base url")
}

func TestBuildLLMSettingsForWorker_AllProviders(t *testing.T) {
	providers := []struct {
		provider database.LLMProvider
		name     string
	}{
		{database.LLMProviderAnthropic, "Anthropic Config"},
		{database.LLMProviderOpenAI, "OpenAI Config"},
		{database.LLMProviderGoogle, "Google Config"},
		{database.LLMProviderOpenRouter, "OpenRouter Config"},
		{database.LLMProviderCustom, "Custom Config"},
	}

	for _, tc := range providers {
		t.Run(string(tc.provider), func(t *testing.T) {
			settings := &database.LLMSettings{
				Name:     tc.name,
				Provider: tc.provider,
				APIKey:   "sk-test-key",
				Model:    "test-model",
				Enabled:  true,
			}
			result := BuildLLMSettingsForWorker(settings)
			testhelpers.AssertNotNil(t, result, "should return non-nil for any valid provider")
			testhelpers.AssertEqual(t, string(tc.provider), result.Provider, "provider should match")
		})
	}
}

func TestGetLLMSettings_ReturnsActiveConfig_MultiConfig(t *testing.T) {
	setupBuildLLMTest(t)

	// Create multiple configs for the same provider
	config1 := &database.LLMSettings{
		Name:     "OpenAI Production",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "sk-prod-key",
		Model:    "gpt-4o",
		Enabled:  true,
		Active:   false,
	}
	config2 := &database.LLMSettings{
		Name:     "OpenAI Development",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "sk-dev-key",
		Model:    "gpt-4o-mini",
		Enabled:  true,
		Active:   true, // This one is active
	}
	config3 := &database.LLMSettings{
		Name:     "Anthropic Main",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-ant-key",
		Model:    "claude-sonnet-4-20250514",
		Enabled:  true,
		Active:   false,
	}

	testhelpers.AssertNil(t, database.CreateLLMSettings(config1), "create config1")
	testhelpers.AssertNil(t, database.CreateLLMSettings(config2), "create config2")
	testhelpers.AssertNil(t, database.CreateLLMSettings(config3), "create config3")

	// GetLLMSettings should return the active config (OpenAI Development)
	active, err := database.GetLLMSettings()
	testhelpers.AssertNil(t, err, "GetLLMSettings should not error")
	testhelpers.AssertNotNil(t, active, "should return active config")
	testhelpers.AssertEqual(t, "OpenAI Development", active.Name, "should return the active config by name")
	testhelpers.AssertEqual(t, database.LLMProviderOpenAI, active.Provider, "active provider")
	testhelpers.AssertEqual(t, "gpt-4o-mini", active.Model, "active model")

	// BuildLLMSettingsForWorker should work with the active config
	worker := BuildLLMSettingsForWorker(active)
	testhelpers.AssertNotNil(t, worker, "worker settings should not be nil")
	testhelpers.AssertEqual(t, "openai", worker.Provider, "worker provider")
	testhelpers.AssertEqual(t, "sk-dev-key", worker.APIKey, "worker api key")
	testhelpers.AssertEqual(t, "gpt-4o-mini", worker.Model, "worker model")
}

func TestGetLLMSettings_SwitchActiveConfig(t *testing.T) {
	setupBuildLLMTest(t)

	config1 := &database.LLMSettings{
		Name:     "Config A",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-ant-a",
		Model:    "claude-sonnet-4-20250514",
		Enabled:  true,
		Active:   true,
	}
	config2 := &database.LLMSettings{
		Name:     "Config B",
		Provider: database.LLMProviderOpenAI,
		APIKey:   "sk-openai-b",
		Model:    "gpt-4o",
		Enabled:  true,
		Active:   false,
	}

	testhelpers.AssertNil(t, database.CreateLLMSettings(config1), "create config1")
	testhelpers.AssertNil(t, database.CreateLLMSettings(config2), "create config2")

	// Initially Config A is active
	active, err := database.GetLLMSettings()
	testhelpers.AssertNil(t, err, "get active")
	testhelpers.AssertEqual(t, "Config A", active.Name, "initial active")

	// Switch to Config B
	testhelpers.AssertNil(t, database.SetActiveLLMConfig(config2.ID), "activate config B")

	// Now Config B should be active
	active, err = database.GetLLMSettings()
	testhelpers.AssertNil(t, err, "get active after switch")
	testhelpers.AssertEqual(t, "Config B", active.Name, "switched active")

	// BuildLLMSettingsForWorker should reflect the new active config
	worker := BuildLLMSettingsForWorker(active)
	testhelpers.AssertNotNil(t, worker, "worker settings after switch")
	testhelpers.AssertEqual(t, "openai", worker.Provider, "switched provider")
	testhelpers.AssertEqual(t, "sk-openai-b", worker.APIKey, "switched api key")
	testhelpers.AssertEqual(t, "gpt-4o", worker.Model, "switched model")
}

func TestGetLLMSettings_CustomNamedConfig(t *testing.T) {
	setupBuildLLMTest(t)

	// Create a config with a custom name (the new multi-config feature)
	config := &database.LLMSettings{
		Name:          "EU Production OpenAI",
		Provider:      database.LLMProviderOpenAI,
		APIKey:        "sk-eu-prod",
		Model:         "gpt-4o",
		ThinkingLevel: database.ThinkingLevelMedium,
		BaseURL:       "https://eu.api.openai.com/v1",
		Enabled:       true,
		Active:        true,
	}
	testhelpers.AssertNil(t, database.CreateLLMSettings(config), "create custom named config")

	active, err := database.GetLLMSettings()
	testhelpers.AssertNil(t, err, "get active")
	testhelpers.AssertEqual(t, "EU Production OpenAI", active.Name, "custom name preserved")

	worker := BuildLLMSettingsForWorker(active)
	testhelpers.AssertNotNil(t, worker, "worker from custom named config")
	testhelpers.AssertEqual(t, "openai", worker.Provider, "provider")
	testhelpers.AssertEqual(t, "sk-eu-prod", worker.APIKey, "api key")
	testhelpers.AssertEqual(t, "gpt-4o", worker.Model, "model")
	testhelpers.AssertEqual(t, "https://eu.api.openai.com/v1", worker.BaseURL, "base url")
}
