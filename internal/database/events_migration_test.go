package database

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEventsBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&Incident{}, &Alert{}, &Event{}, &Integration{}, &Channel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

func TestMigrateBackfillEvents(t *testing.T) {
	db := setupEventsBackfillDB(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if err := db.Create(&Integration{UUID: "int-1", Provider: MessagingProviderSlack, Name: "slack"}).Error; err != nil {
		t.Fatal(err)
	}
	var integ Integration
	db.First(&integ)
	if err := db.Create(&Channel{UUID: "chan-1", IntegrationID: integ.ID, ExternalID: "C1"}).Error; err != nil {
		t.Fatal(err)
	}
	incidents := []Incident{
		{UUID: "inc-alert", Source: "alertmanager", SourceKind: IncidentSourceKindAlert, StartedAt: start},
		{UUID: "inc-cron", Source: "cron", SourceKind: IncidentSourceKindCron, SourceUUID: "job-1", Title: "Cron: nightly", StartedAt: start},
		{UUID: "inc-api", Source: "api", SourceKind: "", StartedAt: start, Context: JSONB{"task": "check disk on db1"}},
		{UUID: "inc-mention", Source: "slack", SourceKind: IncidentSourceKindSlackMention, StartedAt: start, Context: JSONB{"text": "what changed?"}},
	}
	for i := range incidents {
		if err := db.Create(&incidents[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	alerts := []Alert{
		{UUID: "al-webhook", IncidentUUID: "inc-alert", Status: AlertStatusResolved, SourceUUID: "src-1", AlertName: "HostDown", TargetHost: "h1", FiredAt: start},
		{UUID: "al-listener", IncidentUUID: "inc-alert", Status: AlertStatusFiring, SourceUUID: "chan-1", AlertName: "Slack Alert", FiredAt: start.Add(time.Minute)},
	}
	for i := range alerts {
		if err := db.Create(&alerts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := migrateBackfillEvents(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	// Idempotent: a second start adds nothing.
	if err := migrateBackfillEvents(db); err != nil {
		t.Fatalf("second backfill: %v", err)
	}

	var events []Event
	db.Order("source_kind, title").Find(&events)
	if len(events) != 5 {
		t.Fatalf("events = %d, want 5 (2 alerts + 3 non-alert runs): %+v", len(events), events)
	}
	byIncident := map[string][]Event{}
	for _, e := range events {
		if e.Classification != EventClassificationBackfill {
			t.Errorf("event %s classification = %s, want backfill", e.Title, e.Classification)
		}
		byIncident[e.IncidentUUID] = append(byIncident[e.IncidentUUID], e)
	}

	for _, a := range alerts {
		var got Alert
		db.First(&got, "uuid = ?", a.UUID)
		if got.EventUUID == "" {
			t.Fatalf("alert %s not stamped with event_uuid", a.UUID)
		}
		var ev Event
		db.First(&ev, "uuid = ?", got.EventUUID)
		if ev.Kind != EventKindAlert || ev.AlertUUID != a.UUID || ev.Status != string(a.Status) {
			t.Errorf("alert %s event = %+v", a.UUID, ev)
		}
		wantSource := EventSourceWebhook
		if a.SourceUUID == "chan-1" {
			wantSource = EventSourceSlackListener
		}
		if ev.SourceKind != wantSource {
			t.Errorf("alert %s source_kind = %s, want %s", a.UUID, ev.SourceKind, wantSource)
		}
	}

	for incident, want := range map[string]struct{ source, title string }{
		"inc-cron":    {EventSourceCron, "Cron: nightly"},
		"inc-api":     {EventSourceManual, "check disk on db1"},
		"inc-mention": {EventSourceSlackMention, "what changed?"},
	} {
		got := byIncident[incident]
		if len(got) != 1 || got[0].Kind != EventKindRequest || got[0].SourceKind != want.source || got[0].Title != want.title {
			t.Errorf("%s events = %+v, want one %s request titled %q", incident, got, want.source, want.title)
		}
	}
}

func TestMigrateBackfillEvents_NoEventsTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Alert{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateBackfillEvents(db); err != nil {
		t.Fatalf("want no-op without events table, got %v", err)
	}
}

func TestEventIndexes(t *testing.T) {
	db := setupEventsBackfillDB(t)
	for _, name := range []string{"idx_events_source_fp", "idx_events_kind_occurred", "idx_events_received_at", "idx_events_incident_uuid", "idx_alerts_event_uuid"} {
		model := interface{}(&Event{})
		if name == "idx_alerts_event_uuid" {
			model = &Alert{}
		}
		if !db.Migrator().HasIndex(model, name) {
			t.Errorf("missing index %s", name)
		}
	}
}
