package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// replyRecordingProvider records top-level posts and thread replies.
type replyRecordingProvider struct {
	name    database.MessagingProvider
	posts   []string
	replies []struct{ parent, text string }
}

func (p *replyRecordingProvider) Name() database.MessagingProvider { return p.name }
func (p *replyRecordingProvider) PostMessage(_ context.Context, _ *database.Channel, text string) (*messaging.PostedMessage, error) {
	p.posts = append(p.posts, text)
	return &messaging.PostedMessage{MessageID: "42"}, nil
}
func (p *replyRecordingProvider) PostThreadReply(_ context.Context, _ *database.Channel, parent, text string) (*messaging.PostedMessage, error) {
	p.replies = append(p.replies, struct{ parent, text string }{parent, text})
	return &messaging.PostedMessage{MessageID: "43"}, nil
}
func (p *replyRecordingProvider) UpdateMessage(context.Context, *database.Channel, string, string) error {
	return nil
}

func seedTelegramChannel(t *testing.T, db *gorm.DB, canPost bool) *database.Channel {
	t.Helper()
	integ := &database.Integration{UUID: uuid.New().String(), Provider: database.MessagingProviderTelegram, Name: "tg", Enabled: true}
	if err := db.Create(integ).Error; err != nil {
		t.Fatal(err)
	}
	ch := &database.Channel{UUID: uuid.New().String(), IntegrationID: integ.ID, ExternalID: "-100123", DisplayName: "#ops", CanPost: canPost, Enabled: true}
	if err := db.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestResolveExternalChannel(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()
	_, slackDefault, _ := seedIntegrationWithChannels(t, db)
	tg := seedTelegramChannel(t, db, true)
	tgNoPost := seedTelegramChannel(t, db, false)

	registry := messaging.NewRegistry()
	registry.Register(&replyRecordingProvider{name: database.MessagingProviderTelegram})
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelService(services.NewChannelService())
	h.SetProviderRegistry(registry)

	cases := []struct {
		name string
		id   *uint
		want string
	}{
		{"no explicit channel keeps the Slack default flow", nil, ""},
		{"explicit Slack channel keeps the Slack flow", &slackDefault.ID, ""},
		{"explicit Telegram channel is external", &tg.ID, tg.UUID},
		{"Telegram channel that cannot post is ignored", &tgNoPost.ID, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asi := &database.AlertSourceInstance{NotificationChannelID: tc.id}
			got := h.resolveExternalChannel(asi)
			gotUUID := ""
			if got != nil {
				gotUUID = got.UUID
			}
			if gotUUID != tc.want {
				t.Fatalf("got %q, want %q", gotUUID, tc.want)
			}
		})
	}
}

func TestPostAlertAndResultExternal(t *testing.T) {
	db, cleanup := setupChannelRoutingDB(t)
	defer cleanup()
	tg := seedTelegramChannel(t, db, true)
	if err := db.Preload("Integration").First(tg, tg.ID).Error; err != nil {
		t.Fatal(err)
	}

	prov := &replyRecordingProvider{name: database.MessagingProviderTelegram}
	registry := messaging.NewRegistry()
	registry.Register(prov)
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetProviderRegistry(registry)

	instance := &database.AlertSourceInstance{Name: "prod", AlertSourceType: database.AlertSourceType{DisplayName: "Alertmanager"}}
	ext := h.postAlertExternal(tg, alerts.NormalizedAlert{AlertName: "DiskFull", TargetHost: "db1", Severity: "critical", Summary: "/var 95%"}, instance)
	if ext == nil || ext.parentID != "42" {
		t.Fatalf("expected an external thread rooted at 42, got %+v", ext)
	}
	if len(prov.posts) != 1 || !strings.Contains(prov.posts[0], "**Alert: DiskFull**") || !strings.Contains(prov.posts[0], "Alertmanager (prod)") {
		t.Fatalf("banner not posted as Markdown: %q", prov.posts)
	}

	h.postResultExternal(ext, "## Root cause\nDisk full.", false)
	h.postResultExternal(ext, "worker down", true)
	if len(prov.replies) != 2 || prov.replies[0].parent != "42" || prov.replies[0].text != "## Root cause\nDisk full." {
		t.Fatalf("unexpected replies: %+v", prov.replies)
	}
	if !strings.Contains(prov.replies[1].text, "Investigation failed") {
		t.Fatalf("error reply not marked: %q", prov.replies[1].text)
	}

	// nil thread is a no-op
	h.postResultExternal(nil, "x", false)
	if len(prov.replies) != 2 {
		t.Fatal("nil thread must not post")
	}
}
