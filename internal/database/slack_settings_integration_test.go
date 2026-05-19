package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupSlackRuntimeDB returns an in-memory SQLite DB with the legacy
// slack_settings table plus the new Integration table migrated, and points the
// package-global DB at it so GetSlackSettings hits the test DB.
func setupSlackRuntimeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SlackSettings{}, &Integration{}, &Channel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	origDB := DB
	t.Cleanup(func() { DB = origDB })
	DB = db
	return db
}

// TestGetSlackSettings_PrefersEnabledIntegration asserts that an enabled Slack
// Integration row with full credentials wins over an empty legacy
// slack_settings row. Without this preference the Slack manager would never
// connect on a fresh install where the operator only used /api/integrations.
func TestGetSlackSettings_PrefersEnabledIntegration(t *testing.T) {
	db := setupSlackRuntimeDB(t)

	// Empty legacy row, as produced by InitializeDefaults on a fresh install.
	if err := db.Create(&SlackSettings{Enabled: false}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	// Integration row containing the real tokens.
	integration := &Integration{
		UUID:     "uuid-1",
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Credentials: JSONB{
			"bot_token":      "xoxb-from-integration",
			"signing_secret": "sig-from-integration",
			"app_token":      "xapp-from-integration",
		},
		Enabled: true,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	got, err := GetSlackSettings()
	if err != nil {
		t.Fatalf("GetSlackSettings: %v", err)
	}
	if got.BotToken != "xoxb-from-integration" {
		t.Errorf("BotToken = %q, want xoxb-from-integration (from Integration)", got.BotToken)
	}
	if got.SigningSecret != "sig-from-integration" {
		t.Errorf("SigningSecret = %q, want sig-from-integration", got.SigningSecret)
	}
	if got.AppToken != "xapp-from-integration" {
		t.Errorf("AppToken = %q, want xapp-from-integration", got.AppToken)
	}
	if !got.IsActive() {
		t.Errorf("expected derived settings to be active, got inactive")
	}
}

// TestGetSlackSettings_FallsBackToLegacyWhenNoIntegration asserts that when no
// Slack Integration is configured, the function falls back to slack_settings
// so upgrade-in-place deployments keep their pre-Integration credentials.
func TestGetSlackSettings_FallsBackToLegacyWhenNoIntegration(t *testing.T) {
	db := setupSlackRuntimeDB(t)

	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-legacy",
		SigningSecret: "sig-legacy",
		AppToken:      "xapp-legacy",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	got, err := GetSlackSettings()
	if err != nil {
		t.Fatalf("GetSlackSettings: %v", err)
	}
	if got.BotToken != "xoxb-legacy" {
		t.Errorf("BotToken = %q, want xoxb-legacy (legacy fallback)", got.BotToken)
	}
}

// TestGetSlackSettings_DisabledIntegrationDoesNotFallBackToLegacy asserts
// that once an operator has a Slack Integration row, disabling it via
// /api/integrations turns Slack off — even if the migrated-in-place legacy
// slack_settings row is still enabled with valid tokens. Falling back to the
// legacy row in that case would mean operators could not actually pause
// Slack from the Integrations UI on upgraded installs.
func TestGetSlackSettings_DisabledIntegrationDoesNotFallBackToLegacy(t *testing.T) {
	db := setupSlackRuntimeDB(t)

	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-legacy",
		SigningSecret: "sig-legacy",
		AppToken:      "xapp-legacy",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	integration := &Integration{
		UUID:     "uuid-1",
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Credentials: JSONB{
			"bot_token":      "xoxb-disabled",
			"signing_secret": "sig-disabled",
			"app_token":      "xapp-disabled",
		},
		Enabled: false,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}
	if err := db.Model(integration).Update("enabled", false).Error; err != nil {
		t.Fatalf("force integration disabled: %v", err)
	}

	got, err := GetSlackSettings()
	if err != nil {
		t.Fatalf("GetSlackSettings: %v", err)
	}
	if got.IsActive() {
		t.Errorf("IsActive() = true, want false (disabled Integration must not fall back to legacy slack_settings)")
	}
	if got.BotToken == "xoxb-legacy" {
		t.Errorf("BotToken = %q, want Integration projection (got legacy fallback)", got.BotToken)
	}
}

// TestGetSlackSettings_DeletedIntegrationDoesNotRestoreFromLegacy asserts
// that after the migration ran (so the legacy slack_settings row has been
// neutralized), deleting the Slack Integration leaves the system with Slack
// off. Prior to the migration-time clearLegacySlackSettingsCredentials hook,
// the legacy row still carried full credentials and the fall-back path in
// GetSlackSettings silently revived Slack with the migrated tokens —
// defeating the operator's DELETE.
func TestGetSlackSettings_DeletedIntegrationDoesNotRestoreFromLegacy(t *testing.T) {
	db := setupSlackRuntimeDB(t)

	// Seed legacy state as it would exist on an upgrade-in-place install.
	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-legacy",
		SigningSecret: "sig-legacy",
		AppToken:      "xapp-legacy",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	// Run the migration. This is the production path: legacy → Integration
	// → legacy row neutralized.
	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Simulate DELETE /api/integrations/{uuid}: remove the Slack
	// Integration row. The cascading channel cleanup is irrelevant for the
	// runtime fall-back question we're testing here.
	if err := db.Where("provider = ?", MessagingProviderSlack).
		Delete(&Integration{}).Error; err != nil {
		t.Fatalf("delete slack integration: %v", err)
	}

	got, err := GetSlackSettings()
	if err != nil {
		t.Fatalf("GetSlackSettings: %v", err)
	}
	if got.IsActive() {
		t.Errorf("IsActive() = true after deleting Slack Integration; expected Slack off (legacy fall-back must not revive migrated credentials)")
	}
	if got.BotToken != "" || got.SigningSecret != "" || got.AppToken != "" {
		t.Errorf("expected empty credentials after delete, got bot=%q sig=%q app=%q",
			got.BotToken, got.SigningSecret, got.AppToken)
	}
}

// TestGetSlackSettings_IncompleteIntegrationDoesNotFallBackToLegacy asserts
// that an Integration row with incomplete credentials marks the operator as
// "moved to the Integrations model" — the function must NOT silently revive
// the legacy slack_settings credentials behind the operator's back. A
// half-configured Integration means Slack is effectively unconfigured.
func TestGetSlackSettings_IncompleteIntegrationDoesNotFallBackToLegacy(t *testing.T) {
	db := setupSlackRuntimeDB(t)

	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-legacy",
		SigningSecret: "sig-legacy",
		AppToken:      "xapp-legacy",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	integration := &Integration{
		UUID:     "uuid-1",
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Credentials: JSONB{
			"bot_token": "xoxb-only",
		},
		Enabled: true,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	got, err := GetSlackSettings()
	if err != nil {
		t.Fatalf("GetSlackSettings: %v", err)
	}
	if got.IsActive() {
		t.Errorf("IsActive() = true, want false (incomplete Integration must not fall back to legacy slack_settings)")
	}
	if got.BotToken == "xoxb-legacy" {
		t.Errorf("BotToken = %q, want Integration projection (got legacy fallback)", got.BotToken)
	}
}
