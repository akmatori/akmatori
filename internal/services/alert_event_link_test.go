package services

import (
	"context"
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

func seedEvent(t *testing.T, db *gorm.DB, uuid string) {
	t.Helper()
	if err := db.Create(&database.Event{UUID: uuid, SourceKind: database.EventSourceWebhook, Kind: database.EventKindAlert, Classification: database.EventClassificationLLM}).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func loadEvent(t *testing.T, db *gorm.DB, uuid string) database.Event {
	t.Helper()
	var ev database.Event
	if err := db.First(&ev, "uuid = ?", uuid).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	return ev
}

func TestInsertFiringAlert_LinksEvent(t *testing.T) {
	db := setupLastSeenDB(t)
	if err := db.AutoMigrate(&database.Event{}); err != nil {
		t.Fatal(err)
	}
	svc := newIncidentTestService(t, db)
	incUUID := seedLastSeenIncident(t, db, database.IncidentStatusRunning)
	seedEvent(t, db, "ev-1")
	seedEvent(t, db, "ev-2")

	a := alerts.NormalizedAlert{AlertName: "HighCPU", TargetHost: "h1", SourceFingerprint: "fp-1", EventUUID: "ev-1"}
	if err := svc.InsertFiringAlert(context.Background(), incUUID, "src-lastseen", a, "new_incident", ""); err != nil {
		t.Fatal(err)
	}
	row := loadAlertBySourceFingerprint(t, db, "fp-1")
	ev := loadEvent(t, db, "ev-1")
	if row.EventUUID != "ev-1" || ev.AlertUUID != row.UUID || ev.IncidentUUID != incUUID {
		t.Fatalf("alert.event_uuid=%q event=(%q,%q), want two-way link", row.EventUUID, ev.AlertUUID, ev.IncidentUUID)
	}

	// A re-send hits uniq_firing_alert: its event links to the row that
	// already holds the slot.
	a.EventUUID = "ev-2"
	if err := svc.InsertFiringAlert(context.Background(), "other-incident", "src-lastseen", a, "new_incident", ""); !errors.Is(err, ErrAlertAlreadyClaimed) {
		t.Fatalf("want ErrAlertAlreadyClaimed, got %v", err)
	}
	ev2 := loadEvent(t, db, "ev-2")
	if ev2.AlertUUID != row.UUID || ev2.IncidentUUID != incUUID {
		t.Fatalf("re-send event = (%q,%q), want (%q,%q)", ev2.AlertUUID, ev2.IncidentUUID, row.UUID, incUUID)
	}
}

func TestLinkAlertToIncident_LinksEventToSurvivor(t *testing.T) {
	db := setupLastSeenDB(t)
	if err := db.AutoMigrate(&database.Event{}); err != nil {
		t.Fatal(err)
	}
	svc := newIncidentTestService(t, db)
	survivor := seedLastSeenIncident(t, db, database.IncidentStatusRunning)
	merged := seedLastSeenIncident(t, db, database.IncidentStatusMerged)
	db.Model(&database.Incident{}).Where("uuid = ?", merged).Update("merged_into_uuid", survivor)
	seedEvent(t, db, "ev-1")

	a := alerts.NormalizedAlert{AlertName: "HighCPU", TargetHost: "h1", SourceFingerprint: "fp-1", EventUUID: "ev-1"}
	if err := svc.LinkAlertToIncident(context.Background(), merged, "src-lastseen", a, 0.9, "sibling"); err != nil {
		t.Fatal(err)
	}
	ev := loadEvent(t, db, "ev-1")
	row := loadAlertBySourceFingerprint(t, db, "fp-1")
	if ev.IncidentUUID != survivor || ev.AlertUUID != row.UUID || row.EventUUID != "ev-1" {
		t.Fatalf("event=(%q,%q) alert.event=%q, want linked to survivor %s", ev.AlertUUID, ev.IncidentUUID, row.EventUUID, survivor)
	}
}

func TestLinkAlertToIncident_NoEventsTableStillLinks(t *testing.T) {
	db := setupLastSeenDB(t)
	svc := newIncidentTestService(t, db)
	incUUID := seedLastSeenIncident(t, db, database.IncidentStatusRunning)

	a := alerts.NormalizedAlert{AlertName: "HighCPU", TargetHost: "h1", SourceFingerprint: "fp-1", EventUUID: "ev-missing"}
	if err := svc.LinkAlertToIncident(context.Background(), incUUID, "src-lastseen", a, 0.9, "x"); err != nil {
		t.Fatalf("event link failure must not fail the alert link: %v", err)
	}
	if row := loadAlertBySourceFingerprint(t, db, "fp-1"); row.IncidentUUID != incUUID {
		t.Fatalf("alert not linked")
	}
}
