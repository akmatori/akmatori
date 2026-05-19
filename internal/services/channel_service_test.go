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
