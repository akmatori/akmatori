package services

import (
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupChannelServiceTest builds an in-memory sqlite DB with the channels
// schema applied and returns a ChannelService bound to it. The DB also has the
// partial-unique index installed so the per-integration default-post
// invariant is exercised end-to-end.
func setupChannelServiceTest(t *testing.T) (*ChannelService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.AlertSourceType{},
		&database.AlertSourceInstance{},
		&database.Integration{},
		&database.Channel{},
		&database.CronJob{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Mirror the production partial-unique index so default-post conflicts
	// surface here just like they would in postgres.
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_default_post_per_integration ON channels (integration_id) WHERE is_default_post = true").Error; err != nil {
		t.Fatalf("partial index: %v", err)
	}
	return newChannelServiceWithDB(db), db
}

func seedSlackIntegration(t *testing.T, db *gorm.DB) *database.Integration {
	t.Helper()
	row := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderSlack,
		Name:     "Slack",
		Enabled:  true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed integration: %v", err)
	}
	return row
}

func TestChannelService_CreateIntegration_RejectsUnknownProvider(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)

	_, err := svc.CreateIntegration(database.MessagingProvider("discord"), "Discord", nil, true)
	if err == nil {
		t.Fatal("CreateIntegration with unknown provider error = nil, want error")
	}
}

func TestChannelService_CreateIntegration_AssignsUUID(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)

	got, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack Prod", database.JSONB{"bot_token": "x"}, true)
	if err != nil {
		t.Fatalf("CreateIntegration error = %v", err)
	}
	if got.UUID == "" {
		t.Errorf("CreateIntegration UUID is empty, want auto-generated UUID")
	}
	if got.Provider != database.MessagingProviderSlack {
		t.Errorf("CreateIntegration provider = %q, want slack", got.Provider)
	}
}

func TestChannelService_CreateChannel_DefaultsDisplayNameToExternalID(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)

	got, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-incidents",
		CanPost:       true,
	})
	if err != nil {
		t.Fatalf("CreateChannel error = %v", err)
	}
	if got.DisplayName != "C-incidents" {
		t.Errorf("CreateChannel DisplayName = %q, want external_id fallback", got.DisplayName)
	}
}

// TestChannelService_CreateChannel_HonorsEnabledFalse guards against the GORM
// v2 zero-value-bool INSERT omission. Without the explicit post-create
// Update, the column-level `default:true` would silently flip a
// caller-requested Enabled=false back to true on create.
func TestChannelService_CreateChannel_HonorsEnabledFalse(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)

	got, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-disabled",
		CanPost:       true,
		Enabled:       false,
	})
	if err != nil {
		t.Fatalf("CreateChannel error = %v", err)
	}
	if got.Enabled {
		t.Errorf("returned Channel.Enabled = true, want false")
	}
	// Verify the row actually persisted as disabled, not just the in-memory
	// struct.
	var reloaded database.Channel
	if err := db.First(&reloaded, got.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Enabled {
		t.Errorf("persisted Channel.Enabled = true, want false")
	}
}

// TestChannelService_CreateIntegration_HonorsEnabledFalse mirrors the channel
// test for the integration row — same GORM zero-value-bool gotcha.
func TestChannelService_CreateIntegration_HonorsEnabledFalse(t *testing.T) {
	svc, db := setupChannelServiceTest(t)

	got, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack Dev", database.JSONB{"bot_token": "x"}, false)
	if err != nil {
		t.Fatalf("CreateIntegration error = %v", err)
	}
	if got.Enabled {
		t.Errorf("returned Integration.Enabled = true, want false")
	}
	var reloaded database.Integration
	if err := db.First(&reloaded, got.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Enabled {
		t.Errorf("persisted Integration.Enabled = true, want false")
	}
}

func TestChannelService_CreateChannel_RejectsEmptyExternalID(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)

	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "   ",
		CanPost:       true,
	}); err == nil {
		t.Errorf("CreateChannel with blank external_id error = nil, want error")
	}
}

func TestChannelService_CreateChannel_RejectsSecondDefaultPerProvider(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed default channel: %v", err)
	}

	// A second integration of the same provider must not be allowed to also
	// host a default channel — the cross-integration invariant.
	second := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderSlack,
		Name:     "Slack Backup",
		Enabled:  true,
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("seed second integration: %v", err)
	}

	_, err := svc.CreateChannel(&database.Channel{
		IntegrationID: second.ID,
		ExternalID:    "C-second-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if !errors.Is(err, ErrDuplicateDefaultPost) {
		t.Errorf("CreateChannel second default error = %v, want ErrDuplicateDefaultPost", err)
	}
}

func TestChannelService_CreateChannel_RejectsSecondDefaultSameIntegration(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed default channel: %v", err)
	}

	// Adding a second default on the same integration is blocked by the
	// service guard before it ever reaches the DB partial-unique index.
	_, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-another-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if !errors.Is(err, ErrDuplicateDefaultPost) {
		t.Errorf("CreateChannel duplicate same-integration default error = %v, want ErrDuplicateDefaultPost", err)
	}
}

// TestChannelService_UpdateChannel_RejectsCanPostFalseOnExistingDefault asserts
// the invariant guard fires when the patch flips can_post=false without
// touching is_default_post. Without this guard, the row ends in the forbidden
// state is_default_post=true && can_post=false, where ResolveDefault's
// can_post=true filter silently drops it and creating a fresh default
// elsewhere appears to collide with a row the operator believes is no longer
// the default.
func TestChannelService_UpdateChannel_RejectsCanPostFalseOnExistingDefault(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default channel: %v", err)
	}

	no := false
	if _, err := svc.UpdateChannel(channel.UUID, ChannelUpdate{CanPost: &no}); err == nil {
		t.Fatal("UpdateChannel with can_post=false on existing default-post row succeeded; want validation error")
	}

	// Verify the row was not actually mutated.
	reloaded, err := svc.GetChannelByUUID(channel.UUID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.CanPost || !reloaded.IsDefaultPost {
		t.Fatalf("UpdateChannel mutated row despite returning error: %+v", reloaded)
	}
}

func TestChannelService_UpdateChannel_AllowsSelfReSaveAsDefault(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default channel: %v", err)
	}

	yes := true
	updated, err := svc.UpdateChannel(channel.UUID, ChannelUpdate{IsDefaultPost: &yes})
	if err != nil {
		t.Fatalf("UpdateChannel re-save as default error = %v, want nil", err)
	}
	if !updated.IsDefaultPost {
		t.Errorf("UpdateChannel re-save as default IsDefaultPost = false, want true")
	}
}

func TestChannelService_ResolveDefault_ReturnsConfiguredChannel(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	created, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default channel: %v", err)
	}

	got, err := svc.ResolveDefault(database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("ResolveDefault error = %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ResolveDefault returned channel id %d, want %d", got.ID, created.ID)
	}
	if got.Integration.Provider != database.MessagingProviderSlack {
		t.Errorf("ResolveDefault preloaded integration provider = %q, want slack", got.Integration.Provider)
	}
}

func TestChannelService_ResolveDefault_NoDefault_ReturnsErrChannelNotFound(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-other",
		CanPost:       true,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	_, err := svc.ResolveDefault(database.MessagingProviderSlack)
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("ResolveDefault error = %v, want ErrChannelNotFound", err)
	}
}

func TestChannelService_ResolveForAlertSource_PrefersExplicitChannel(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	defaultChan, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	explicit, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-explicit",
		CanPost:       true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed explicit: %v", err)
	}

	asi := &database.AlertSourceInstance{NotificationChannelID: &explicit.ID}
	got, err := svc.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("ResolveForAlertSource(explicit) error = %v", err)
	}
	if got.ID != explicit.ID {
		t.Errorf("ResolveForAlertSource returned id %d, want explicit id %d (default was %d)", got.ID, explicit.ID, defaultChan.ID)
	}
}

func TestChannelService_ResolveForAlertSource_FallsBackToDefault(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	defaultChan, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}

	asi := &database.AlertSourceInstance{}
	got, err := svc.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("ResolveForAlertSource(no explicit) error = %v", err)
	}
	if got.ID != defaultChan.ID {
		t.Errorf("ResolveForAlertSource fallback returned id %d, want default id %d", got.ID, defaultChan.ID)
	}
}

func TestChannelService_ResolveForAlertSource_StaleFKFallsBackToDefault(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	defaultChan, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}

	staleID := defaultChan.ID + 9999
	asi := &database.AlertSourceInstance{NotificationChannelID: &staleID}

	got, err := svc.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("ResolveForAlertSource(stale fk) error = %v", err)
	}
	if got.ID != defaultChan.ID {
		t.Errorf("ResolveForAlertSource stale fk returned id %d, want default id %d", got.ID, defaultChan.ID)
	}
}

func TestChannelService_ListChannels_FilterByCanListen(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-listener",
		CanListen:     true,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed listener: %v", err)
	}
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-poster",
		CanPost:       true,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("seed poster: %v", err)
	}

	yes := true
	rows, err := svc.ListChannels(ListChannelsFilter{CanListen: &yes})
	if err != nil {
		t.Fatalf("ListChannels error = %v", err)
	}
	if len(rows) != 1 || rows[0].ExternalID != "C-listener" {
		t.Errorf("ListChannels CanListen=true returned %+v, want exactly the listener row", rows)
	}
}

func TestChannelService_DeleteIntegration_CascadesChannels(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-x",
		CanPost:       true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	if err := svc.DeleteIntegration(integration.UUID); err != nil {
		t.Fatalf("DeleteIntegration error = %v", err)
	}
	var remaining int64
	db.Model(&database.Channel{}).Where("integration_id = ?", integration.ID).Count(&remaining)
	if remaining != 0 {
		t.Errorf("DeleteIntegration left %d channels behind, want 0", remaining)
	}
}

// TestChannelService_DeleteIntegration_ClearsTriggerFKs asserts the cleanup
// transaction nulls AlertSourceInstance.NotificationChannelID and
// CronJob.ChannelID for any row pointing at a doomed channel, so triggers fall
// back to the per-provider default rather than carrying a dangling reference.
func TestChannelService_DeleteIntegration_ClearsTriggerFKs(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-x",
		CanPost:       true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	srcType := &database.AlertSourceType{Name: "test", DisplayName: "Test"}
	if err := db.Create(srcType).Error; err != nil {
		t.Fatalf("seed source type: %v", err)
	}
	asi := &database.AlertSourceInstance{
		UUID:                  uuid.New().String(),
		AlertSourceTypeID:     srcType.ID,
		Name:                  "alert-instance",
		NotificationChannelID: &channel.ID,
	}
	if err := db.Create(asi).Error; err != nil {
		t.Fatalf("seed alert instance: %v", err)
	}
	cron := &database.CronJob{
		UUID:      uuid.New().String(),
		Name:      "cron-job",
		Schedule:  "* * * * *",
		Prompt:    "do thing",
		Mode:      database.CronJobModeOneshot,
		ChannelID: &channel.ID,
	}
	if err := db.Create(cron).Error; err != nil {
		t.Fatalf("seed cron: %v", err)
	}

	if err := svc.DeleteIntegration(integration.UUID); err != nil {
		t.Fatalf("DeleteIntegration error = %v", err)
	}

	var reloadedASI database.AlertSourceInstance
	if err := db.First(&reloadedASI, asi.ID).Error; err != nil {
		t.Fatalf("reload alert instance: %v", err)
	}
	if reloadedASI.NotificationChannelID != nil {
		t.Errorf("AlertSourceInstance.NotificationChannelID = %v, want nil", *reloadedASI.NotificationChannelID)
	}
	var reloadedCron database.CronJob
	if err := db.First(&reloadedCron, cron.ID).Error; err != nil {
		t.Fatalf("reload cron: %v", err)
	}
	if reloadedCron.ChannelID != nil {
		t.Errorf("CronJob.ChannelID = %v, want nil", *reloadedCron.ChannelID)
	}
}

// TestChannelService_CreateChannel_RejectsDefaultWithoutCanPost asserts the
// invariant that a default-post channel must also be permitted to post —
// otherwise the provider would refuse the message at runtime and the operator
// would never see why.
func TestChannelService_CreateChannel_RejectsDefaultWithoutCanPost(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	_, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-listen-only",
		IsDefaultPost: true,
		CanPost:       false,
		CanListen:     true,
		Enabled:       true,
	})
	if err == nil {
		t.Fatal("CreateChannel default-without-can-post should error")
	}
}

// TestChannelService_ResolveForAlertSource_DisabledExplicitFallsBack asserts
// that an explicit notification channel that is no longer usable (disabled
// channel, disabled integration, or can_post flipped to false) falls back to
// the per-provider default rather than silently leaking an unusable row.
func TestChannelService_ResolveForAlertSource_DisabledExplicitFallsBack(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	defaultChan, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-default",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	// Build the explicit channel via CreateChannel (enabled) then disable it
	// via UPDATE — GORM v2 omits zero-value bools from INSERT, so the DB
	// default for `enabled` would override the struct field on create.
	explicit, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-explicit",
		CanPost:       true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed explicit: %v", err)
	}
	if err := db.Model(&database.Channel{}).Where("id = ?", explicit.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable explicit: %v", err)
	}

	asi := &database.AlertSourceInstance{NotificationChannelID: &explicit.ID}
	got, err := svc.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("ResolveForAlertSource(disabled explicit) error = %v", err)
	}
	if got.ID != defaultChan.ID {
		t.Errorf("disabled explicit fell to id %d, want default id %d", got.ID, defaultChan.ID)
	}
}

// TestChannelService_ListIntegrations_Sorted asserts the surface returns rows
// in (provider, name) order so the UI listing is deterministic.
func TestChannelService_ListIntegrations_Sorted(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	if _, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack B", nil, true); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	if _, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack A", nil, true); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	rows, err := svc.ListIntegrations()
	if err != nil {
		t.Fatalf("ListIntegrations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Name != "Slack A" || rows[1].Name != "Slack B" {
		t.Errorf("ListIntegrations order = [%s, %s], want [Slack A, Slack B]", rows[0].Name, rows[1].Name)
	}
	_ = db
}

// TestChannelService_GetIntegrationByUUID_NotFound surfaces ErrIntegrationNotFound
// rather than wrapping the GORM error.
func TestChannelService_GetIntegrationByUUID_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	_, err := svc.GetIntegrationByUUID("does-not-exist")
	if !errors.Is(err, ErrIntegrationNotFound) {
		t.Fatalf("error = %v, want ErrIntegrationNotFound", err)
	}
}

// TestChannelService_UpdateIntegration_PatchesFields exercises the partial
// update path: changing name, credentials, and enabled in one PUT.
func TestChannelService_UpdateIntegration_PatchesFields(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	integration, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack", database.JSONB{"bot_token": "old"}, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newName := "Slack Renamed"
	newCreds := database.JSONB{"bot_token": "new"}
	off := false
	got, err := svc.UpdateIntegration(integration.UUID, &newName, newCreds, &off)
	if err != nil {
		t.Fatalf("UpdateIntegration: %v", err)
	}
	if got.Name != "Slack Renamed" {
		t.Errorf("Name = %q, want Slack Renamed", got.Name)
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if got.Credentials["bot_token"] != "new" {
		t.Errorf("Credentials.bot_token = %v, want new", got.Credentials["bot_token"])
	}
}

// TestChannelService_UpdateIntegration_MergesCredentials asserts that updating
// a subset of credential keys leaves the other keys untouched. The UI strips
// blank fields from edit submissions, so the service must treat a partial
// credentials payload as a merge rather than a full replace; otherwise
// rotating one secret (e.g. bot_token) would erase signing_secret and
// app_token from the row.
func TestChannelService_UpdateIntegration_MergesCredentials(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	original := database.JSONB{
		"bot_token":      "xoxb-old",
		"signing_secret": "sig",
		"app_token":      "xapp-old",
	}
	integration, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack", original, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// UI sends only the changed key.
	partial := database.JSONB{"bot_token": "xoxb-new"}
	got, err := svc.UpdateIntegration(integration.UUID, nil, partial, nil)
	if err != nil {
		t.Fatalf("UpdateIntegration: %v", err)
	}
	if got.Credentials["bot_token"] != "xoxb-new" {
		t.Errorf("bot_token = %v, want xoxb-new", got.Credentials["bot_token"])
	}
	if got.Credentials["signing_secret"] != "sig" {
		t.Errorf("signing_secret = %v, want preserved 'sig' (merge must not drop other keys)", got.Credentials["signing_secret"])
	}
	if got.Credentials["app_token"] != "xapp-old" {
		t.Errorf("app_token = %v, want preserved 'xapp-old' (merge must not drop other keys)", got.Credentials["app_token"])
	}
}

// TestChannelService_UpdateIntegration_EmptyCredentialIgnored confirms that
// an explicitly empty string in the patch is treated as "no change" rather
// than as a clear. The UI strips empty fields, but a request that still
// carries them must not zero out the stored secret.
func TestChannelService_UpdateIntegration_EmptyCredentialIgnored(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	original := database.JSONB{
		"bot_token":      "xoxb-keep",
		"signing_secret": "sig-keep",
	}
	integration, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack", original, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	patch := database.JSONB{"bot_token": ""}
	got, err := svc.UpdateIntegration(integration.UUID, nil, patch, nil)
	if err != nil {
		t.Fatalf("UpdateIntegration: %v", err)
	}
	if got.Credentials["bot_token"] != "xoxb-keep" {
		t.Errorf("bot_token = %v, want preserved 'xoxb-keep' (empty value must be ignored)", got.Credentials["bot_token"])
	}
	if got.Credentials["signing_secret"] != "sig-keep" {
		t.Errorf("signing_secret = %v, want preserved 'sig-keep'", got.Credentials["signing_secret"])
	}
}

// TestChannelService_UpdateIntegration_RejectsBlankName rejects whitespace-only
// names with a plain validation error.
func TestChannelService_UpdateIntegration_RejectsBlankName(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	integration, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack", nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	blank := "   "
	if _, err := svc.UpdateIntegration(integration.UUID, &blank, nil, nil); err == nil {
		t.Error("UpdateIntegration blank name error = nil, want validation error")
	}
}

// TestChannelService_UpdateIntegration_NoOpReturnsRow covers the "no fields
// changed" branch where the row is returned without a write.
func TestChannelService_UpdateIntegration_NoOpReturnsRow(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	integration, err := svc.CreateIntegration(database.MessagingProviderSlack, "Slack", nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.UpdateIntegration(integration.UUID, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateIntegration: %v", err)
	}
	if got.UUID != integration.UUID {
		t.Errorf("UUID = %q, want %q", got.UUID, integration.UUID)
	}
}

// TestChannelService_UpdateIntegration_NotFound surfaces ErrIntegrationNotFound
// when the target UUID does not exist.
func TestChannelService_UpdateIntegration_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	name := "X"
	_, err := svc.UpdateIntegration("ghost", &name, nil, nil)
	if !errors.Is(err, ErrIntegrationNotFound) {
		t.Errorf("error = %v, want ErrIntegrationNotFound", err)
	}
}

// TestChannelService_GetChannelByUUID_NotFound surfaces ErrChannelNotFound
// rather than wrapping the GORM error.
func TestChannelService_GetChannelByUUID_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	_, err := svc.GetChannelByUUID("does-not-exist")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("error = %v, want ErrChannelNotFound", err)
	}
}

// TestChannelService_ListChannels_FilterByIntegrationUUID narrows by the
// parent integration. Two integrations seeded so the filter is meaningful.
func TestChannelService_ListChannels_FilterByIntegrationUUID(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	intA := seedSlackIntegration(t, db)
	intB := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderSlack,
		Name:     "Slack B",
		Enabled:  true,
	}
	if err := db.Create(intB).Error; err != nil {
		t.Fatalf("seed intB: %v", err)
	}
	if _, err := svc.CreateChannel(&database.Channel{IntegrationID: intA.ID, ExternalID: "C-a", CanPost: true}); err != nil {
		t.Fatalf("seed A channel: %v", err)
	}
	if _, err := svc.CreateChannel(&database.Channel{IntegrationID: intB.ID, ExternalID: "C-b", CanPost: true}); err != nil {
		t.Fatalf("seed B channel: %v", err)
	}

	rows, err := svc.ListChannels(ListChannelsFilter{IntegrationUUID: intB.UUID})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(rows) != 1 || rows[0].ExternalID != "C-b" {
		t.Errorf("rows = %+v, want only C-b", rows)
	}
}

// TestChannelService_ListChannels_FilterByIntegrationUUID_NotFound surfaces
// ErrIntegrationNotFound when the requested parent does not exist.
func TestChannelService_ListChannels_FilterByIntegrationUUID_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	_, err := svc.ListChannels(ListChannelsFilter{IntegrationUUID: "ghost"})
	if !errors.Is(err, ErrIntegrationNotFound) {
		t.Errorf("err = %v, want ErrIntegrationNotFound", err)
	}
}

// TestChannelService_ListChannels_FilterByCanPost confirms the boolean filter
// flows through to the underlying query.
func TestChannelService_ListChannels_FilterByCanPost(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	if _, err := svc.CreateChannel(&database.Channel{IntegrationID: integration.ID, ExternalID: "C-poster", CanPost: true}); err != nil {
		t.Fatalf("seed poster: %v", err)
	}
	if _, err := svc.CreateChannel(&database.Channel{IntegrationID: integration.ID, ExternalID: "C-listener", CanListen: true}); err != nil {
		t.Fatalf("seed listener: %v", err)
	}

	yes := true
	rows, err := svc.ListChannels(ListChannelsFilter{CanPost: &yes})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(rows) != 1 || rows[0].ExternalID != "C-poster" {
		t.Errorf("rows = %+v, want only poster", rows)
	}
}

// TestChannelService_UpdateChannel_PatchesNonDefaultFields exercises the
// non-IsDefaultPost update branches: display name, can_listen, extraction
// prompt, process_human_messages, enabled.
func TestChannelService_UpdateChannel_PatchesNonDefaultFields(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-listen",
		CanListen:     true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	newExternal := "C-listen-renamed"
	newDisplay := "Listener"
	canPost := true
	canListen := false
	prompt := "Extract X from message"
	process := true
	enabled := false
	got, err := svc.UpdateChannel(channel.UUID, ChannelUpdate{
		ExternalID:           &newExternal,
		DisplayName:          &newDisplay,
		CanPost:              &canPost,
		CanListen:            &canListen,
		ExtractionPrompt:     &prompt,
		ProcessHumanMessages: &process,
		Enabled:              &enabled,
	})
	if err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	if got.ExternalID != newExternal {
		t.Errorf("ExternalID = %q, want %q", got.ExternalID, newExternal)
	}
	if got.DisplayName != newDisplay {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, newDisplay)
	}
	if !got.CanPost {
		t.Errorf("CanPost = false, want true")
	}
	if got.CanListen {
		t.Errorf("CanListen = true, want false")
	}
	if got.ExtractionPrompt != prompt {
		t.Errorf("ExtractionPrompt = %q, want %q", got.ExtractionPrompt, prompt)
	}
	if !got.ProcessHumanMessages {
		t.Errorf("ProcessHumanMessages = false, want true")
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false")
	}
}

// TestChannelService_UpdateChannel_RejectsBlankExternalID guards the validation
// branch.
func TestChannelService_UpdateChannel_RejectsBlankExternalID(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-listen",
		CanListen:     true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	blank := "   "
	if _, err := svc.UpdateChannel(channel.UUID, ChannelUpdate{ExternalID: &blank}); err == nil {
		t.Errorf("UpdateChannel blank external_id error = nil, want validation error")
	}
}

// TestChannelService_UpdateChannel_NoOpReturnsRow covers the "no patch supplied"
// branch.
func TestChannelService_UpdateChannel_NoOpReturnsRow(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-listen",
		CanListen:     true,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := svc.UpdateChannel(channel.UUID, ChannelUpdate{})
	if err != nil {
		t.Fatalf("UpdateChannel no-op: %v", err)
	}
	if got.UUID != channel.UUID {
		t.Errorf("UUID = %q, want %q", got.UUID, channel.UUID)
	}
}

// TestChannelService_UpdateChannel_NotFound returns ErrChannelNotFound for
// unknown UUID.
func TestChannelService_UpdateChannel_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	if _, err := svc.UpdateChannel("ghost", ChannelUpdate{}); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v, want ErrChannelNotFound", err)
	}
}

// TestChannelService_DeleteChannel_RemovesRow exercises the happy path.
func TestChannelService_DeleteChannel_RemovesRow(t *testing.T) {
	svc, db := setupChannelServiceTest(t)
	integration := seedSlackIntegration(t, db)
	channel, err := svc.CreateChannel(&database.Channel{
		IntegrationID: integration.ID,
		ExternalID:    "C-incidents",
		CanPost:       true,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.DeleteChannel(channel.UUID); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, err := svc.GetChannelByUUID(channel.UUID); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("after delete, get err = %v, want ErrChannelNotFound", err)
	}
}

// TestChannelService_DeleteChannel_NotFound surfaces ErrChannelNotFound when
// the target UUID is absent.
func TestChannelService_DeleteChannel_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	if err := svc.DeleteChannel("ghost"); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v, want ErrChannelNotFound", err)
	}
}

// TestChannelService_DeleteIntegration_NotFound covers the not-found path on
// the delete surface.
func TestChannelService_DeleteIntegration_NotFound(t *testing.T) {
	svc, _ := setupChannelServiceTest(t)
	if err := svc.DeleteIntegration("ghost"); !errors.Is(err, ErrIntegrationNotFound) {
		t.Errorf("err = %v, want ErrIntegrationNotFound", err)
	}
}
