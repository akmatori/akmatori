package database

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupChannelsMigrationDB returns a fresh in-memory SQLite DB with the new
// schema fully applied (Integration, Channel, CronJob plus the updated
// alert/incident columns). Tests can then seed legacy rows and call the
// backfill functions to assert behaviour.
func setupChannelsMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&SlackSettings{},
		&AlertSourceType{},
		&AlertSourceInstance{},
		&Incident{},
		&Integration{},
		&Channel{},
		&CronJob{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := ensureChannelsDefaultPartialIndex(db); err != nil {
		t.Fatalf("ensure partial index: %v", err)
	}
	return db
}

func TestChannelsMigration_EmptyDB_IsNoop(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrateSlackSettingsToIntegrations: %v", err)
	}
	if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
		t.Fatalf("migrateSlackChannelAlertSourcesToChannels: %v", err)
	}
	if err := deprecateSlackChannelAlertSourceType(db); err != nil {
		t.Fatalf("deprecateSlackChannelAlertSourceType: %v", err)
	}

	var integrations int64
	db.Model(&Integration{}).Count(&integrations)
	if integrations != 0 {
		t.Errorf("expected 0 integrations on empty DB, got %d", integrations)
	}
	var channels int64
	db.Model(&Channel{}).Count(&channels)
	if channels != 0 {
		t.Errorf("expected 0 channels on empty DB, got %d", channels)
	}
}

func TestChannelsMigration_BackfillsSlackSettings(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	legacy := &SlackSettings{
		BotToken:      "xoxb-test",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test",
		AlertsChannel: "C12345",
		Enabled:       true,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed slack_settings: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var integration Integration
	if err := db.Where("provider = ?", MessagingProviderSlack).First(&integration).Error; err != nil {
		t.Fatalf("expected slack integration, got error: %v", err)
	}
	if !integration.Enabled {
		t.Errorf("expected integration to be enabled, got disabled")
	}
	if got, _ := integration.Credentials["bot_token"].(string); got != "xoxb-test" {
		t.Errorf("expected bot_token=xoxb-test in credentials, got %q", got)
	}
	if got, _ := integration.Credentials["signing_secret"].(string); got != "signing-secret" {
		t.Errorf("expected signing_secret=signing-secret in credentials, got %q", got)
	}
	if got, _ := integration.Credentials["app_token"].(string); got != "xapp-test" {
		t.Errorf("expected app_token=xapp-test in credentials, got %q", got)
	}

	var channel Channel
	if err := db.Where("integration_id = ?", integration.ID).First(&channel).Error; err != nil {
		t.Fatalf("expected default channel, got error: %v", err)
	}
	if channel.ExternalID != "C12345" {
		t.Errorf("expected ExternalID=C12345, got %q", channel.ExternalID)
	}
	if !channel.IsDefaultPost {
		t.Errorf("expected IsDefaultPost=true")
	}
	if !channel.CanPost {
		t.Errorf("expected CanPost=true")
	}
	if channel.CanListen {
		t.Errorf("expected CanListen=false for migrated default channel")
	}
}

func TestChannelsMigration_BackfillSkipsUnconfiguredSlackSettings(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	// Seed an empty placeholder slack_settings row (InitializeDefaults creates
	// one with Enabled=false and no tokens). The backfill must not produce a
	// half-filled Integration.
	if err := db.Create(&SlackSettings{Enabled: false}).Error; err != nil {
		t.Fatalf("seed empty slack_settings: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var integrations int64
	db.Model(&Integration{}).Count(&integrations)
	if integrations != 0 {
		t.Errorf("expected 0 integrations for empty legacy row, got %d", integrations)
	}
}

// TestChannelsMigration_PlaceholderIntegrationStaysDisabled asserts that when
// the listener migration has to fabricate a placeholder Integration (because
// the operator never configured slack_settings but still has slack_channel
// AlertSourceInstance rows from a prior dev run), the placeholder lands as
// Enabled=false. Without the explicit post-create Update, the column-level
// `default:true` would persist the placeholder as Enabled=true despite the
// struct field saying false — GORM v2 omits zero-value bools from INSERT.
func TestChannelsMigration_PlaceholderIntegrationStaysDisabled(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	sourceType := &AlertSourceType{
		Name:        "slack_channel",
		DisplayName: "Slack Alert Channel",
	}
	if err := db.Create(sourceType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	inst := &AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceType.ID,
		Name:              "edge-alerts",
		Settings: JSONB{
			"slack_channel_id": "C99999",
		},
		Enabled: true,
	}
	if err := db.Create(inst).Error; err != nil {
		t.Fatalf("seed alert source instance: %v", err)
	}

	if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var integration Integration
	if err := db.Where("provider = ?", MessagingProviderSlack).First(&integration).Error; err != nil {
		t.Fatalf("expected placeholder integration, got error: %v", err)
	}
	if integration.Enabled {
		t.Errorf("expected placeholder integration to be disabled, got enabled — credentials are empty so an enabled placeholder would falsely pass the listener-enabled gate")
	}
}

// TestChannelsMigration_DisabledSlackSettingsStaysDisabled asserts that a
// legacy SlackSettings row with Enabled=false migrates into a disabled
// Integration. Without an explicit post-create Update, GORM v2 omits the
// zero-value bool from INSERT and the column-level `default:true` flips the
// migrated row to enabled — a regression that would silently re-enable
// previously disabled Slack integrations on upgrade.
func TestChannelsMigration_DisabledSlackSettingsStaysDisabled(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	legacy := &SlackSettings{
		BotToken:      "xoxb-test",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test",
		AlertsChannel: "C12345",
		Enabled:       false,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed slack_settings: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var integration Integration
	if err := db.Where("provider = ?", MessagingProviderSlack).First(&integration).Error; err != nil {
		t.Fatalf("expected slack integration, got error: %v", err)
	}
	if integration.Enabled {
		t.Errorf("expected migrated integration to honor SlackSettings.Enabled=false, got enabled")
	}
}

// TestChannelsMigration_DisabledListenerInstanceStaysDisabled asserts that
// a slack_channel AlertSourceInstance with Enabled=false migrates into a
// disabled Channel row. The column-level `default:true` on Channel.Enabled
// would otherwise re-enable a disabled listener on upgrade.
func TestChannelsMigration_DisabledListenerInstanceStaysDisabled(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	integration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Enabled:  true,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	sourceType := &AlertSourceType{
		Name:        "slack_channel",
		DisplayName: "Slack Alert Channel",
	}
	if err := db.Create(sourceType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	inst := &AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceType.ID,
		Name:              "edge-alerts",
		Settings: JSONB{
			"slack_channel_id": "C77777",
		},
		Enabled: false,
	}
	if err := db.Create(inst).Error; err != nil {
		t.Fatalf("seed alert source instance: %v", err)
	}
	// AlertSourceInstance.Enabled also carries `gorm:"default:true"`, so the
	// previous Create silently flipped the seeded row to enabled=true. Force
	// the column to match the disabled state the test set up.
	if err := db.Model(inst).Update("enabled", false).Error; err != nil {
		t.Fatalf("force seed disabled: %v", err)
	}

	if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var channel Channel
	if err := db.Where("external_id = ?", "C77777").First(&channel).Error; err != nil {
		t.Fatalf("expected migrated listener channel, got error: %v", err)
	}
	if channel.Enabled {
		t.Errorf("expected migrated listener channel to inherit Enabled=false from source instance, got enabled")
	}
}

func TestChannelsMigration_BackfillsSlackChannelAlertSources(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	// Seed slack integration so the listener migration has somewhere to attach.
	integration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Enabled:  true,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	// Seed the slack_channel alert source type + an instance.
	sourceType := &AlertSourceType{
		Name:        "slack_channel",
		DisplayName: "Slack Alert Channel",
	}
	if err := db.Create(sourceType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	inst := &AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceType.ID,
		Name:              "edge-alerts",
		Settings: JSONB{
			"slack_channel_id":       "C99999",
			"extraction_prompt":      "Extract incident details.",
			"process_human_messages": true,
		},
		Enabled: true,
	}
	if err := db.Create(inst).Error; err != nil {
		t.Fatalf("seed alert source instance: %v", err)
	}

	if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var channel Channel
	if err := db.Where("integration_id = ? AND external_id = ?", integration.ID, "C99999").First(&channel).Error; err != nil {
		t.Fatalf("expected migrated listener channel, got error: %v", err)
	}
	if !channel.CanListen {
		t.Errorf("expected CanListen=true on migrated channel")
	}
	if channel.CanPost {
		t.Errorf("expected CanPost=false on migrated listener channel")
	}
	if channel.IsDefaultPost {
		t.Errorf("expected IsDefaultPost=false on migrated listener channel")
	}
	if channel.ExtractionPrompt != "Extract incident details." {
		t.Errorf("expected ExtractionPrompt to be copied, got %q", channel.ExtractionPrompt)
	}
	if !channel.ProcessHumanMessages {
		t.Errorf("expected ProcessHumanMessages=true on migrated channel")
	}

	// The originating alert source instance must be deleted.
	var remaining int64
	db.Model(&AlertSourceInstance{}).Where("id = ?", inst.ID).Count(&remaining)
	if remaining != 0 {
		t.Errorf("expected migrated AlertSourceInstance to be deleted, found %d rows", remaining)
	}
}

func TestChannelsMigration_DeprecatesSlackChannelType(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	sourceType := &AlertSourceType{
		Name:        "slack_channel",
		DisplayName: "Slack Alert Channel",
	}
	if err := db.Create(sourceType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}

	if err := deprecateSlackChannelAlertSourceType(db); err != nil {
		t.Fatalf("deprecate: %v", err)
	}

	var reloaded AlertSourceType
	if err := db.First(&reloaded, sourceType.ID).Error; err != nil {
		t.Fatalf("reload source type: %v", err)
	}
	if !reloaded.Deprecated {
		t.Errorf("expected slack_channel source type to be marked deprecated")
	}
}

// TestChannelsMigration_ClearsLegacyCredentialsAfterBackfill asserts that
// after migrateSlackSettingsToIntegrations creates the new Integration, the
// legacy slack_settings row no longer carries credentials. Without this, a
// later DELETE /api/integrations/{uuid} would fall back to the still-populated
// legacy row via GetSlackSettings and silently revive Slack with the migrated
// tokens — directly contradicting the operator's intent.
func TestChannelsMigration_ClearsLegacyCredentialsAfterBackfill(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	legacy := &SlackSettings{
		BotToken:      "xoxb-test",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test",
		AlertsChannel: "C12345",
		Enabled:       true,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed slack_settings: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var reloaded SlackSettings
	if err := db.First(&reloaded).Error; err != nil {
		t.Fatalf("reload legacy slack_settings: %v", err)
	}
	if reloaded.BotToken != "" || reloaded.SigningSecret != "" || reloaded.AppToken != "" {
		t.Errorf("expected legacy credentials to be cleared after migration, got bot=%q sig=%q app=%q",
			reloaded.BotToken, reloaded.SigningSecret, reloaded.AppToken)
	}
	if reloaded.Enabled {
		t.Errorf("expected legacy Enabled=false after migration, got true")
	}
	if reloaded.IsActive() {
		t.Errorf("expected legacy row to be inactive after migration, got active")
	}
}

// TestChannelsMigration_ClearsLegacyCredentialsOnAlreadyMigratedRerun asserts
// the idempotent re-run branch also neutralizes the legacy row. This covers
// installs that ran the original (pre-fix) migration: their slack_settings
// row still has credentials, and only the re-run cleanup ensures DELETE of
// the Integration cannot leak them back into the runtime.
func TestChannelsMigration_ClearsLegacyCredentialsOnAlreadyMigratedRerun(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	// Pre-existing Integration (simulates a prior startup where the
	// pre-fix migration ran). Legacy row still has credentials.
	integration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Credentials: JSONB{
			"bot_token":      "xoxb-current",
			"signing_secret": "sig-current",
			"app_token":      "xapp-current",
		},
		Enabled: true,
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}
	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-legacy",
		SigningSecret: "sig-legacy",
		AppToken:      "xapp-legacy",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate rerun: %v", err)
	}

	var reloaded SlackSettings
	if err := db.First(&reloaded).Error; err != nil {
		t.Fatalf("reload legacy slack_settings: %v", err)
	}
	if reloaded.BotToken != "" || reloaded.SigningSecret != "" || reloaded.AppToken != "" {
		t.Errorf("expected legacy credentials to be cleared on already-migrated rerun, got bot=%q sig=%q app=%q",
			reloaded.BotToken, reloaded.SigningSecret, reloaded.AppToken)
	}
	if reloaded.Enabled {
		t.Errorf("expected legacy Enabled=false on already-migrated rerun, got true")
	}

	// Re-running once more must be a clean no-op (no error from the WHERE
	// clause matching zero rows).
	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("migrate second rerun: %v", err)
	}
}

// TestChannelsMigration_RepairsEnabledOnPreviouslyMigratedDisabledSlack asserts
// that the rerun path repairs the Integration.Enabled state for installs that
// ran the original buggy migration (which used `Enabled: legacy.Enabled` on
// the gorm Create without a follow-up Update — GORM v2 dropped the zero-value
// false and the column-level default:true silently re-enabled Slack).
//
// The repair must:
//   - flip the migrated Integration to enabled=false based on bot_token match
//     against the still-credentialed legacy slack_settings row
//   - leave operator-created Integrations on a different workspace alone
//   - subsequently neutralize the legacy row so a later DELETE cannot revive
//     it via the GetSlackSettings fallback
//   - be a no-op once the legacy row is cleared
func TestChannelsMigration_RepairsEnabledOnPreviouslyMigratedDisabledSlack(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	// Simulate the post-buggy-migration state: legacy row still has tokens
	// (the original migration did not clear them) and shows enabled=false,
	// matching the operator's prior intent. The Integration carries the
	// migrated tokens but landed at enabled=true due to GORM's default.
	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-original",
		SigningSecret: "sig-original",
		AppToken:      "xapp-original",
		Enabled:       false,
	}).Error; err != nil {
		t.Fatalf("seed legacy slack_settings: %v", err)
	}
	buggyIntegration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack",
		Credentials: JSONB{
			"bot_token":      "xoxb-original",
			"signing_secret": "sig-original",
			"app_token":      "xapp-original",
		},
		Enabled: true,
	}
	if err := db.Create(buggyIntegration).Error; err != nil {
		t.Fatalf("seed buggy slack integration: %v", err)
	}

	// Operator separately created a second, unrelated Slack Integration via
	// the new API for a different workspace. The repair must not touch it.
	unrelatedIntegration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack (other workspace)",
		Credentials: JSONB{
			"bot_token":      "xoxb-other",
			"signing_secret": "sig-other",
			"app_token":      "xapp-other",
		},
		Enabled: true,
	}
	if err := db.Create(unrelatedIntegration).Error; err != nil {
		t.Fatalf("seed unrelated slack integration: %v", err)
	}

	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("rerun migrate: %v", err)
	}

	var repaired Integration
	if err := db.First(&repaired, buggyIntegration.ID).Error; err != nil {
		t.Fatalf("reload buggy integration: %v", err)
	}
	if repaired.Enabled {
		t.Errorf("expected migrated slack integration to be repaired to enabled=false, got enabled")
	}

	var untouched Integration
	if err := db.First(&untouched, unrelatedIntegration.ID).Error; err != nil {
		t.Fatalf("reload unrelated integration: %v", err)
	}
	if !untouched.Enabled {
		t.Errorf("expected unrelated slack integration to remain enabled, got disabled")
	}

	var reloadedLegacy SlackSettings
	if err := db.First(&reloadedLegacy).Error; err != nil {
		t.Fatalf("reload legacy slack_settings: %v", err)
	}
	if reloadedLegacy.BotToken != "" || reloadedLegacy.SigningSecret != "" || reloadedLegacy.AppToken != "" {
		t.Errorf("expected legacy credentials cleared after repair+rerun, got bot=%q sig=%q app=%q",
			reloadedLegacy.BotToken, reloadedLegacy.SigningSecret, reloadedLegacy.AppToken)
	}

	// A second rerun must be a clean no-op: legacy credentials are gone so
	// the repair branch short-circuits and nothing else changes.
	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		t.Fatalf("second rerun migrate: %v", err)
	}
	var stillRepaired Integration
	if err := db.First(&stillRepaired, buggyIntegration.ID).Error; err != nil {
		t.Fatalf("reload buggy integration after second rerun: %v", err)
	}
	if stillRepaired.Enabled {
		t.Errorf("expected buggy integration to remain enabled=false on second rerun, got enabled")
	}
	var stillUntouched Integration
	if err := db.First(&stillUntouched, unrelatedIntegration.ID).Error; err != nil {
		t.Fatalf("reload unrelated integration after second rerun: %v", err)
	}
	if !stillUntouched.Enabled {
		t.Errorf("expected unrelated integration to remain enabled on second rerun, got disabled")
	}
}

func TestChannelsMigration_IsIdempotentOnRerun(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	// Seed full legacy state: slack_settings, slack_channel type + instance.
	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-test",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test",
		AlertsChannel: "C12345",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed slack_settings: %v", err)
	}
	sourceType := &AlertSourceType{Name: "slack_channel", DisplayName: "Slack Alert Channel"}
	if err := db.Create(sourceType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	inst := &AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceType.ID,
		Name:              "edge-alerts",
		Settings: JSONB{
			"slack_channel_id":       "C99999",
			"extraction_prompt":      "Extract incident details.",
			"process_human_messages": true,
		},
		Enabled: true,
	}
	if err := db.Create(inst).Error; err != nil {
		t.Fatalf("seed alert source instance: %v", err)
	}

	runAll := func() {
		t.Helper()
		if err := migrateSlackSettingsToIntegrations(db); err != nil {
			t.Fatalf("migrateSlackSettingsToIntegrations: %v", err)
		}
		if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
			t.Fatalf("migrateSlackChannelAlertSourcesToChannels: %v", err)
		}
		if err := deprecateSlackChannelAlertSourceType(db); err != nil {
			t.Fatalf("deprecateSlackChannelAlertSourceType: %v", err)
		}
	}

	runAll()

	var integrationsAfterFirst, channelsAfterFirst, instancesAfterFirst int64
	db.Model(&Integration{}).Count(&integrationsAfterFirst)
	db.Model(&Channel{}).Count(&channelsAfterFirst)
	db.Model(&AlertSourceInstance{}).Count(&instancesAfterFirst)
	if integrationsAfterFirst != 1 {
		t.Fatalf("expected 1 integration after first migrate, got %d", integrationsAfterFirst)
	}
	if channelsAfterFirst != 2 {
		t.Fatalf("expected 2 channels (1 default-post + 1 listener) after first migrate, got %d", channelsAfterFirst)
	}
	if instancesAfterFirst != 0 {
		t.Fatalf("expected 0 alert source instances after first migrate, got %d", instancesAfterFirst)
	}

	// Re-run: counts must not change.
	runAll()

	var integrationsAfterSecond, channelsAfterSecond, instancesAfterSecond int64
	db.Model(&Integration{}).Count(&integrationsAfterSecond)
	db.Model(&Channel{}).Count(&channelsAfterSecond)
	db.Model(&AlertSourceInstance{}).Count(&instancesAfterSecond)
	if integrationsAfterSecond != integrationsAfterFirst {
		t.Errorf("expected idempotent integrations count, got %d != %d", integrationsAfterSecond, integrationsAfterFirst)
	}
	if channelsAfterSecond != channelsAfterFirst {
		t.Errorf("expected idempotent channels count, got %d != %d", channelsAfterSecond, channelsAfterFirst)
	}
	if instancesAfterSecond != instancesAfterFirst {
		t.Errorf("expected idempotent alert source instances count, got %d != %d", instancesAfterSecond, instancesAfterFirst)
	}
}

func TestRunMigrations_AppliesChannelsBackfillEndToEnd(t *testing.T) {
	// Exercise the top-level runMigrations entry point on a sqlite DB so the
	// AutoMigrate registration + partial index + backfill chain are all wired
	// together correctly.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Seed legacy state BEFORE migrations run: a configured slack_settings row
	// and a slack_channel alert source instance. The migration must convert
	// both into Integration + Channel rows.
	if err := db.AutoMigrate(&SlackSettings{}, &AlertSourceType{}, &AlertSourceInstance{}); err != nil {
		t.Fatalf("seed automigrate: %v", err)
	}
	if err := db.Create(&SlackSettings{
		BotToken:      "xoxb-test",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test",
		AlertsChannel: "C-default",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed slack_settings: %v", err)
	}
	st := &AlertSourceType{Name: "slack_channel", DisplayName: "Slack Alert Channel"}
	if err := db.Create(st).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	if err := db.Create(&AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: st.ID,
		Name:              "edge-alerts",
		Settings: JSONB{
			"slack_channel_id":       "C-listener",
			"extraction_prompt":      "Extract details.",
			"process_human_messages": true,
		},
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed alert source instance: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	var integrationCount, channelCount, instanceCount int64
	db.Model(&Integration{}).Count(&integrationCount)
	db.Model(&Channel{}).Count(&channelCount)
	db.Model(&AlertSourceInstance{}).Count(&instanceCount)
	if integrationCount != 1 {
		t.Errorf("expected 1 integration after runMigrations, got %d", integrationCount)
	}
	if channelCount != 2 {
		t.Errorf("expected 2 channels after runMigrations (1 default-post + 1 listener), got %d", channelCount)
	}
	if instanceCount != 0 {
		t.Errorf("expected 0 alert source instances after runMigrations, got %d", instanceCount)
	}

	// The slack_channel alert source type row must remain (deprecated) so that
	// historical references stay resolvable, but with Deprecated=true.
	var st2 AlertSourceType
	if err := db.Where("name = ?", "slack_channel").First(&st2).Error; err != nil {
		t.Fatalf("expected slack_channel type to still exist (just deprecated): %v", err)
	}
	if !st2.Deprecated {
		t.Errorf("expected slack_channel alert source type to be marked deprecated after runMigrations")
	}

	// Second invocation must be a clean no-op (idempotent).
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations rerun: %v", err)
	}
	var integrationCount2, channelCount2 int64
	db.Model(&Integration{}).Count(&integrationCount2)
	db.Model(&Channel{}).Count(&channelCount2)
	if integrationCount2 != integrationCount || channelCount2 != channelCount {
		t.Errorf("runMigrations rerun was not a no-op: integrations %d→%d, channels %d→%d",
			integrationCount, integrationCount2, channelCount, channelCount2)
	}
}

func TestChannelsPartialUniqueIndex_RejectsTwoDefaults(t *testing.T) {
	db := setupChannelsMigrationDB(t)

	integration := &Integration{
		UUID:     uuid.New().String(),
		Provider: MessagingProviderSlack,
		Name:     "Slack",
	}
	if err := db.Create(integration).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	first := &Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integration.ID,
		ExternalID:    "C-first",
		CanPost:       true,
		IsDefaultPost: true,
	}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first default channel: %v", err)
	}

	second := &Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integration.ID,
		ExternalID:    "C-second",
		CanPost:       true,
		IsDefaultPost: true,
	}
	if err := db.Create(second).Error; err == nil {
		t.Errorf("expected partial-unique index to reject second default-post channel on the same integration, got nil error")
	}

	// Non-default channels on the same integration must still be insertable.
	third := &Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integration.ID,
		ExternalID:    "C-third",
		CanPost:       true,
		IsDefaultPost: false,
	}
	if err := db.Create(third).Error; err != nil {
		t.Errorf("expected non-default channel to be allowed, got error: %v", err)
	}
}
