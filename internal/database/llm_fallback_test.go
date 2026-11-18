package database

import (
	"errors"
	"testing"
)

func seedFallbackRows(t *testing.T) (active, other, nokey *LLMSettings) {
	t.Helper()
	setupLLMTestDB(t)
	active = &LLMSettings{Name: "Main", Provider: LLMProviderCustom, APIKey: "k1", Model: "deepseek", Enabled: true, Active: true}
	other = &LLMSettings{Name: "Backup", Provider: LLMProviderOpenRouter, APIKey: "k2", Model: "openai/gpt-5.5"}
	nokey = &LLMSettings{Name: "Empty", Provider: LLMProviderOpenAI, Model: "gpt-5.5"}
	for _, s := range []*LLMSettings{active, other, nokey} {
		if err := CreateLLMSettings(s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}
	return active, other, nokey
}

func TestFallbackLLM_SetGetClear(t *testing.T) {
	active, other, nokey := seedFallbackRows(t)

	if fb, err := GetFallbackLLMSettings(); err != nil || fb != nil {
		t.Fatalf("no fallback yet: got %v, %v", fb, err)
	}
	if err := SetFallbackLLMConfig(active.ID); !errors.Is(err, ErrFallbackIsActive) {
		t.Errorf("active as fallback: err = %v", err)
	}
	if err := SetFallbackLLMConfig(nokey.ID); !errors.Is(err, ErrFallbackNoAPIKey) {
		t.Errorf("no-key fallback: err = %v", err)
	}
	if err := SetFallbackLLMConfig(9999); err == nil {
		t.Error("missing id must fail")
	}
	if err := SetFallbackLLMConfig(other.ID); err != nil {
		t.Fatalf("set fallback: %v", err)
	}
	fb, err := GetFallbackLLMSettings()
	if err != nil || fb == nil || fb.ID != other.ID || !fb.Enabled {
		t.Fatalf("GetFallbackLLMSettings = %+v, %v", fb, err)
	}

	// Only one fallback at a time.
	if err := updateLLMKeyForTest(nokey.ID, "k3"); err != nil {
		t.Fatal(err)
	}
	if err := SetFallbackLLMConfig(nokey.ID); err != nil {
		t.Fatalf("move fallback: %v", err)
	}
	var count int64
	DB.Model(&LLMSettings{}).Where("fallback = ?", true).Count(&count)
	if count != 1 {
		t.Errorf("fallback rows = %d, want 1", count)
	}

	if err := ClearFallbackLLMConfig(nokey.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if fb, _ := GetFallbackLLMSettings(); fb != nil {
		t.Errorf("fallback after clear = %+v", fb)
	}
	if err := ClearFallbackLLMConfig(9999); err == nil {
		t.Error("clearing a missing id must fail")
	}
}

func updateLLMKeyForTest(id uint, key string) error {
	_, err := UpdateLLMSettings(id, map[string]interface{}{"api_key": key, "enabled": true})
	return err
}

func TestFallbackLLM_ActivatingFallbackClearsIt(t *testing.T) {
	_, other, _ := seedFallbackRows(t)
	if err := SetFallbackLLMConfig(other.ID); err != nil {
		t.Fatal(err)
	}
	if err := SetActiveLLMConfig(other.ID); err != nil {
		t.Fatal(err)
	}
	row, _ := GetLLMSettingsByID(other.ID)
	if !row.Active || row.Fallback {
		t.Errorf("activated row: active=%v fallback=%v, want true/false", row.Active, row.Fallback)
	}
	if fb, _ := GetFallbackLLMSettings(); fb != nil {
		t.Errorf("active row must not be returned as fallback: %+v", fb)
	}
}

func TestFallbackLLM_CannotClearKey(t *testing.T) {
	_, other, _ := seedFallbackRows(t)
	if err := SetFallbackLLMConfig(other.ID); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateLLMSettings(other.ID, map[string]interface{}{"api_key": ""})
	if !errors.Is(err, ErrFallbackKeyClear) {
		t.Errorf("clearing fallback key: err = %v", err)
	}
}
