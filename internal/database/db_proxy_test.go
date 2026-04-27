package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProxySettingsDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&ProxySettings{}); err != nil {
		t.Fatalf("migrate proxy settings: %v", err)
	}

	originalDB := DB
	DB = db
	t.Cleanup(func() {
		DB = originalDB
	})

	return db
}

func TestGetOrCreateProxySettings_Defaults(t *testing.T) {
	setupProxySettingsDB(t)

	settings, err := GetOrCreateProxySettings()
	if err != nil {
		t.Fatalf("GetOrCreateProxySettings returned error: %v", err)
	}

	if !settings.OpenAIEnabled {
		t.Error("OpenAIEnabled = false, want true")
	}
	if !settings.SlackEnabled {
		t.Error("SlackEnabled = false, want true")
	}
	if settings.ZabbixEnabled {
		t.Error("ZabbixEnabled = true, want false")
	}
}

func TestUpdateProxySettings_PersistsZeroValues(t *testing.T) {
	setupProxySettingsDB(t)

	settings, err := GetOrCreateProxySettings()
	if err != nil {
		t.Fatalf("GetOrCreateProxySettings returned error: %v", err)
	}

	settings.ProxyURL = "https://user:pass@proxy.example.com:8443"
	settings.NoProxy = "localhost,.svc"
	settings.OpenAIEnabled = false
	settings.SlackEnabled = false
	settings.ZabbixEnabled = true

	if err := UpdateProxySettings(settings); err != nil {
		t.Fatalf("UpdateProxySettings returned error: %v", err)
	}

	reloaded, err := GetOrCreateProxySettings()
	if err != nil {
		t.Fatalf("reload proxy settings: %v", err)
	}

	if reloaded.ProxyURL != settings.ProxyURL {
		t.Errorf("ProxyURL = %q, want %q", reloaded.ProxyURL, settings.ProxyURL)
	}
	if reloaded.NoProxy != settings.NoProxy {
		t.Errorf("NoProxy = %q, want %q", reloaded.NoProxy, settings.NoProxy)
	}
	if reloaded.OpenAIEnabled {
		t.Error("OpenAIEnabled = true, want false")
	}
	if reloaded.SlackEnabled {
		t.Error("SlackEnabled = true, want false")
	}
	if !reloaded.ZabbixEnabled {
		t.Error("ZabbixEnabled = false, want true")
	}
}
