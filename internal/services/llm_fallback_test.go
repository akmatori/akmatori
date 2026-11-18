package services

import (
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func withFallbackRow(t *testing.T, row *database.LLMSettings, err error) {
	t.Helper()
	prev := loadFallbackLLMSettings
	loadFallbackLLMSettings = func() (*database.LLMSettings, error) { return row, err }
	t.Cleanup(func() { loadFallbackLLMSettings = prev })
}

func TestResolveFallbackLLM(t *testing.T) {
	primary := &LLMSettingsForWorker{Provider: "custom", Model: "deepseek", BaseURL: "https://llm.internal/v1", APIKey: "k"}
	backup := &database.LLMSettings{Name: "Backup", Provider: database.LLMProviderOpenRouter, Model: "openai/gpt-5.5", APIKey: "k2", Enabled: true}

	withFallbackRow(t, backup, nil)
	fb := ResolveFallbackLLM(primary)
	if fb == nil || fb.Name != "Backup" || fb.Model != "openai/gpt-5.5" {
		t.Fatalf("ResolveFallbackLLM = %+v", fb)
	}
	if ResolveFallbackLLM(nil) != nil {
		t.Error("no primary → no fallback")
	}

	same := &database.LLMSettings{Name: "Same", Provider: database.LLMProviderCustom, Model: "deepseek",
		BaseURL: "https://llm.internal/v1", APIKey: "other-key", Enabled: true}
	withFallbackRow(t, same, nil)
	if ResolveFallbackLLM(primary) != nil {
		t.Error("a fallback on the same endpoint and model must be ignored")
	}

	withFallbackRow(t, nil, nil)
	if ResolveFallbackLLM(primary) != nil {
		t.Error("no row → nil")
	}
	withFallbackRow(t, nil, errors.New("db down"))
	if ResolveFallbackLLM(primary) != nil {
		t.Error("db error → nil")
	}
	withFallbackRow(t, &database.LLMSettings{Provider: database.LLMProviderOpenAI, Model: "m", Enabled: true}, nil)
	if ResolveFallbackLLM(primary) != nil {
		t.Error("row without key → nil")
	}
}
