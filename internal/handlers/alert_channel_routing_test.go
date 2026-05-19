package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupChannelRoutingDB extends the alert-slack test DB with the Integration,
// Channel, and AlertSourceInstance tables that ChannelService needs to resolve
// outbound destinations. Returns a teardown that restores the global database
// handle for sibling tests.
func setupChannelRoutingDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	prevDB := database.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/routing.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(
		&database.SlackSettings{},
		&database.GeneralSettings{},
		&database.Integration{},
		&database.Channel{},
		&database.AlertSourceType{},
		&database.AlertSourceInstance{},
	); err != nil {
		t.Fatalf("migrate sqlite db: %v", err)
	}
	database.DB = db
	return db, func() { database.DB = prevDB }
}

func seedIntegrationWithChannels(t *testing.T, db *gorm.DB) (*database.Integration, *database.Channel, *database.Channel) {
	t.Helper()
	integ := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderSlack,
		Name:     "primary slack",
		Enabled:  true,
	}
	if err := db.Create(integ).Error; err != nil {
		t.Fatalf("create integration: %v", err)
	}

	defaultCh := &database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integ.ID,
		ExternalID:    "C_DEFAULT",
		DisplayName:   "#alerts",
		CanPost:       true,
		IsDefaultPost: true,
		Enabled:       true,
	}
	if err := db.Create(defaultCh).Error; err != nil {
		t.Fatalf("create default channel: %v", err)
	}

	stagingCh := &database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: integ.ID,
		ExternalID:    "C_STAGING",
		DisplayName:   "#staging-alerts",
		CanPost:       true,
		IsDefaultPost: false,
		Enabled:       true,
	}
	if err := db.Create(stagingCh).Error; err != nil {
		t.Fatalf("create staging channel: %v", err)
	}
	return integ, defaultCh, stagingCh
}

func TestAlertHandler_ResolveOutboundSlackChannel_ExplicitChannelWins(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	_, _, staging := seedIntegrationWithChannels(t, db)

	asi := &database.AlertSourceInstance{
		UUID:                  uuid.New().String(),
		Name:                  "explicit-asi",
		NotificationChannelID: &staging.ID,
	}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel == nil {
		t.Fatal("expected channel, got nil")
	}
	if channel.ID != staging.ID {
		t.Errorf("channel.ID = %d, want %d (staging)", channel.ID, staging.ID)
	}
	if channelID != "C_STAGING" {
		t.Errorf("channelID = %q, want %q", channelID, "C_STAGING")
	}
}

func TestAlertHandler_ResolveOutboundSlackChannel_FallsBackToDefault(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	_, defaultCh, _ := seedIntegrationWithChannels(t, db)

	asi := &database.AlertSourceInstance{
		UUID: uuid.New().String(),
		Name: "no-channel-asi",
		// NotificationChannelID intentionally nil
	}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel == nil {
		t.Fatal("expected default channel, got nil")
	}
	if channel.ID != defaultCh.ID {
		t.Errorf("channel.ID = %d, want %d (default)", channel.ID, defaultCh.ID)
	}
	if channelID != "C_DEFAULT" {
		t.Errorf("channelID = %q, want %q", channelID, "C_DEFAULT")
	}
}

// TestAlertHandler_ResolveOutboundSlackChannel_NoLegacyFallback asserts that
// the Task-3 legacy fallback (synthesising a Channel from
// SlackSettings.AlertsChannel) has been removed. Even with a configured
// AlertsChannel, resolution must return no destination when no Channel row
// exists. Without this guarantee, operators who never migrated would silently
// keep posting via the deprecated singleton — defeating the purpose of the
// Channels rewrite.
func TestAlertHandler_ResolveOutboundSlackChannel_NoLegacyFallback(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	settings := &database.SlackSettings{
		AlertsChannel: "C_LEGACY",
		Enabled:       true,
	}
	if err := db.Create(settings).Error; err != nil {
		t.Fatalf("create slack settings: %v", err)
	}

	asi := &database.AlertSourceInstance{
		UUID: uuid.New().String(),
		Name: "legacy-asi",
	}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel != nil || channelID != "" {
		t.Errorf("expected no destination once the legacy fallback is removed, got channel=%v channelID=%q",
			channel, channelID)
	}
}

// TestAlertHandler_ResolveOutboundSlackChannel_NonSlackFallsBackToDefault
// covers the cross-provider mismatch path: an AlertSourceInstance whose
// NotificationChannelID points at a non-slack channel (e.g. a Telegram
// channel) must not return the wrong-provider channel for slack posting.
// Instead, when a default Slack channel exists, fall back to it so the alert
// still surfaces somewhere rather than being silently dropped.
func TestAlertHandler_ResolveOutboundSlackChannel_NonSlackFallsBackToDefault(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	_, defaultCh, _ := seedIntegrationWithChannels(t, db)

	telegram := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderTelegram,
		Name:     "telegram bot",
		Enabled:  true,
	}
	if err := db.Create(telegram).Error; err != nil {
		t.Fatalf("create telegram integration: %v", err)
	}
	tgChannel := &database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: telegram.ID,
		ExternalID:    "tg-1234",
		DisplayName:   "team chat",
		CanPost:       true,
		IsDefaultPost: false,
		Enabled:       true,
	}
	if err := db.Create(tgChannel).Error; err != nil {
		t.Fatalf("create telegram channel: %v", err)
	}

	asi := &database.AlertSourceInstance{
		UUID:                  uuid.New().String(),
		Name:                  "tg-asi",
		NotificationChannelID: &tgChannel.ID,
	}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel == nil {
		t.Fatal("expected default slack fallback, got nil")
	}
	if channel.ID != defaultCh.ID {
		t.Errorf("channel.ID = %d, want %d (default slack)", channel.ID, defaultCh.ID)
	}
	if channelID != "C_DEFAULT" {
		t.Errorf("channelID = %q, want C_DEFAULT (default slack)", channelID)
	}
}

// TestAlertHandler_ResolveOutboundSlackChannel_NonSlackNoDefaultDropsPost
// covers the cross-provider mismatch path when no default Slack channel
// exists: the post is dropped rather than misrouted. There is nowhere safe
// to fall through to in this case.
func TestAlertHandler_ResolveOutboundSlackChannel_NonSlackNoDefaultDropsPost(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	telegram := &database.Integration{
		UUID:     uuid.New().String(),
		Provider: database.MessagingProviderTelegram,
		Name:     "telegram bot",
		Enabled:  true,
	}
	if err := db.Create(telegram).Error; err != nil {
		t.Fatalf("create telegram integration: %v", err)
	}
	tgChannel := &database.Channel{
		UUID:          uuid.New().String(),
		IntegrationID: telegram.ID,
		ExternalID:    "tg-1234",
		DisplayName:   "team chat",
		CanPost:       true,
		IsDefaultPost: false,
		Enabled:       true,
	}
	if err := db.Create(tgChannel).Error; err != nil {
		t.Fatalf("create telegram channel: %v", err)
	}

	asi := &database.AlertSourceInstance{
		UUID:                  uuid.New().String(),
		Name:                  "tg-asi",
		NotificationChannelID: &tgChannel.ID,
	}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel != nil || channelID != "" {
		t.Errorf("expected slack resolver to drop telegram-typed channel with no default, got channel=%v channelID=%q",
			channel, channelID)
	}
}

func TestAlertHandler_ResolveOutboundSlackChannel_NoDestination(t *testing.T) {
	_, cleanup := setupChannelRoutingDB(t)
	defer cleanup()

	asi := &database.AlertSourceInstance{UUID: uuid.New().String(), Name: "orphan"}

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())

	channel, channelID := h.resolveOutboundSlackChannel(asi)
	if channel != nil || channelID != "" {
		t.Errorf("expected nothing for empty DB, got channel=%v channelID=%q",
			channel, channelID)
	}
}

// fakeMessagingProvider records every PostMessage call so tests can assert
// the channel and text reaching the provider boundary without standing up
// a Slack client.
type fakeMessagingProvider struct {
	name  database.MessagingProvider
	calls []fakeMessagingCall
}

type fakeMessagingCall struct {
	ChannelExternalID string
	Text              string
}

func (f *fakeMessagingProvider) Name() database.MessagingProvider { return f.name }

func (f *fakeMessagingProvider) PostMessage(_ context.Context, channel *database.Channel, text string) (*messaging.PostedMessage, error) {
	f.calls = append(f.calls, fakeMessagingCall{ChannelExternalID: channel.ExternalID, Text: text})
	return &messaging.PostedMessage{MessageID: "fake-ts-1"}, nil
}

func (f *fakeMessagingProvider) PostThreadReply(_ context.Context, _ *database.Channel, _, _ string) (*messaging.PostedMessage, error) {
	return nil, messaging.ErrNotImplemented
}

func (f *fakeMessagingProvider) UpdateMessage(_ context.Context, _ *database.Channel, _, _ string) error {
	return messaging.ErrNotImplemented
}

func TestAlertHandler_PostViaProvider_DelegatesToRegisteredProvider(t *testing.T) {
	provider := &fakeMessagingProvider{name: database.MessagingProviderSlack}
	registry := messaging.NewRegistry()
	registry.Register(provider)

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetProviderRegistry(registry)

	channel := &database.Channel{
		ExternalID:  "#alerts",
		DisplayName: "#alerts",
		Integration: database.Integration{Provider: database.MessagingProviderSlack},
	}

	ts, err := h.postViaProvider(context.Background(), channel, "C_RESOLVED", "hello world")
	if err != nil {
		t.Fatalf("postViaProvider returned error: %v", err)
	}
	if ts != "fake-ts-1" {
		t.Errorf("postViaProvider ts = %q, want %q", ts, "fake-ts-1")
	}
	if len(provider.calls) != 1 {
		t.Fatalf("provider received %d calls, want 1", len(provider.calls))
	}
	call := provider.calls[0]
	if call.ChannelExternalID != "C_RESOLVED" {
		t.Errorf("provider got externalID = %q, want resolved %q", call.ChannelExternalID, "C_RESOLVED")
	}
	if call.Text != "hello world" {
		t.Errorf("provider got text = %q, want %q", call.Text, "hello world")
	}
}

func TestAlertHandler_PostViaProvider_NoRegistryFallsBack(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	channel := &database.Channel{
		ExternalID:  "C_RESOLVED",
		Integration: database.Integration{Provider: database.MessagingProviderSlack},
	}

	ts, err := h.postViaProvider(context.Background(), channel, "C_RESOLVED", "hello")
	if err != nil {
		t.Fatalf("postViaProvider error: %v", err)
	}
	if ts != "" {
		t.Errorf("ts = %q, want empty string so caller falls back to slack client", ts)
	}
}

func TestAlertHandler_PostViaProvider_UnknownProviderFallsBack(t *testing.T) {
	registry := messaging.NewRegistry()
	// Intentionally register nothing — Get should return ErrProviderNotRegistered.

	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetProviderRegistry(registry)

	channel := &database.Channel{
		ExternalID:  "C_RESOLVED",
		Integration: database.Integration{Provider: database.MessagingProviderTelegram},
	}

	ts, err := h.postViaProvider(context.Background(), channel, "C_RESOLVED", "hello")
	if err != nil {
		t.Fatalf("postViaProvider error: %v", err)
	}
	if ts != "" {
		t.Errorf("ts = %q, want empty string fallthrough on unknown provider", ts)
	}

	// Sanity check the registry actually rejects unknown providers.
	if _, gErr := registry.Get(database.MessagingProviderTelegram); !errors.Is(gErr, messaging.ErrProviderNotRegistered) {
		t.Fatalf("registry.Get(telegram) error = %v, want ErrProviderNotRegistered", gErr)
	}
}

func TestAlertHandler_UpdateSlackWithResult_NoOpWhenChannelEmpty(t *testing.T) {
	// Asserts the new channelID parameter gates the function so we never
	// attempt to post to "" — which previously could happen when the legacy
	// SlackSettings.AlertsChannel lookup returned empty.
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	// Intentionally pass nil slackManager via NewAlertHandler — calling
	// updateSlackWithResult with empty channelID must early-return BEFORE we
	// try to dereference slackManager.GetClient().
	h.updateSlackWithResult("", "ts-1", "ignored", false)
	h.updateSlackWithResult("C123", "", "ignored", false)
	// Reaching here without nil-deref means the early-returns held.
}
