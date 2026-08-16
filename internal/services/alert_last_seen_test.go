package services

import (
	"context"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupLastSeenDB opens an isolated in-memory database that carries the
// uniq_firing_alert partial index. AutoMigrate alone does not create it — it
// is created by ensureAlertsIndexes at real startup — and without it a
// duplicate insert simply succeeds, so the conflict branch these tests exist
// to cover would never run.
func setupLastSeenDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}, &database.Alert{}, &database.GeneralSettings{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS uniq_firing_alert ON alerts (source_uuid, source_fingerprint) " +
			"WHERE status = 'firing' AND source_fingerprint <> ''").Error; err != nil {
		t.Fatalf("create uniq_firing_alert: %v", err)
	}
	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
	return db
}

func seedLastSeenIncident(t *testing.T, db *gorm.DB, status database.IncidentStatus) string {
	t.Helper()
	incUUID := uuid.New().String()
	if err := db.Create(&database.Incident{
		UUID:       incUUID,
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-lastseen",
		Status:     status,
		StartedAt:  time.Now().Add(-time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	return incUUID
}

func loadAlertBySourceFingerprint(t *testing.T, db *gorm.DB, sfp string) database.Alert {
	t.Helper()
	var row database.Alert
	if err := db.Where("source_fingerprint = ?", sfp).First(&row).Error; err != nil {
		t.Fatalf("load alert %s: %v", sfp, err)
	}
	return row
}

// TestInsertFiringAlert_DuplicateBumpsLastSeen covers a source that re-sends
// an alert with a stable fingerprint (Alertmanager and friends). The insert is
// dropped by uniq_firing_alert, so last_seen_at is the only record that the
// alert is still live.
func TestInsertFiringAlert_DuplicateBumpsLastSeen(t *testing.T) {
	db := setupLastSeenDB(t)
	svc := newIncidentTestService(t, db)
	incUUID := seedLastSeenIncident(t, db, database.IncidentStatusRunning)

	firstFire := time.Now().Add(-2 * time.Hour)
	a := alerts.NormalizedAlert{
		AlertName:         "HighCPU",
		TargetHost:        "host-01",
		SourceFingerprint: "fp-stable",
		StartedAt:         &firstFire,
	}
	if err := svc.InsertFiringAlert(context.Background(), incUUID, "src-lastseen", a, "new_incident", ""); err != nil {
		t.Fatalf("first InsertFiringAlert: %v", err)
	}

	row := loadAlertBySourceFingerprint(t, db, "fp-stable")
	if row.LastSeenAt != nil {
		t.Errorf("LastSeenAt = %v on first fire, want nil", row.LastSeenAt)
	}

	// The source re-sends the same alert. StartedAt deliberately stays at the
	// original fire time — that is what Alertmanager does, and recording it
	// instead of receipt time would advance nothing.
	err := svc.InsertFiringAlert(context.Background(), incUUID, "src-lastseen", a, "new_incident", "")
	if err != ErrAlertAlreadyClaimed {
		t.Fatalf("second InsertFiringAlert error = %v, want ErrAlertAlreadyClaimed", err)
	}

	var count int64
	db.Model(&database.Alert{}).Where("source_fingerprint = ?", "fp-stable").Count(&count)
	if count != 1 {
		t.Fatalf("alert rows = %d, want 1 (the duplicate must not insert)", count)
	}

	row = loadAlertBySourceFingerprint(t, db, "fp-stable")
	if row.LastSeenAt == nil {
		t.Fatal("LastSeenAt is nil after a re-send, want the re-send time")
	}
	if !row.LastSeenAt.After(row.FiredAt) {
		t.Errorf("LastSeenAt = %v, want later than FiredAt = %v", row.LastSeenAt, row.FiredAt)
	}
}

// TestLinkAlertToIncident_DuplicateBumpsLastSeen covers the same re-send on
// the correlation path. A recurrence routed to an existing incident hits the
// same unique index, so it needs the same bump.
func TestLinkAlertToIncident_DuplicateBumpsLastSeen(t *testing.T) {
	db := setupLastSeenDB(t)
	svc := newIncidentTestService(t, db)
	incUUID := seedLastSeenIncident(t, db, database.IncidentStatusRunning)

	firstFire := time.Now().Add(-2 * time.Hour)
	a := alerts.NormalizedAlert{
		AlertName:         "DiskFull",
		TargetHost:        "db-01",
		SourceFingerprint: "fp-linked",
		StartedAt:         &firstFire,
	}
	if err := svc.LinkAlertToIncident(context.Background(), incUUID, "src-lastseen", a, 0.9, "recurrence"); err != nil {
		t.Fatalf("first LinkAlertToIncident: %v", err)
	}

	// StartedAt stays at the original fire time, as a real re-send does.
	if err := svc.LinkAlertToIncident(context.Background(), incUUID, "src-lastseen", a, 0.9, "recurrence"); err != nil {
		t.Fatalf("second LinkAlertToIncident: %v", err)
	}

	var count int64
	db.Model(&database.Alert{}).Where("source_fingerprint = ?", "fp-linked").Count(&count)
	if count != 1 {
		t.Fatalf("alert rows = %d, want 1", count)
	}

	row := loadAlertBySourceFingerprint(t, db, "fp-linked")
	if row.LastSeenAt == nil {
		t.Fatal("LastSeenAt is nil after a re-send, want the re-send time")
	}
	if !row.LastSeenAt.After(row.FiredAt) {
		t.Errorf("LastSeenAt = %v, want later than FiredAt = %v", row.LastSeenAt, row.FiredAt)
	}
}

// TestStaleClose_ResentAlertKeepsIncidentOpen is the end-to-end statement of
// why last_seen_at exists: an incident whose alert first fired long ago but is
// still being re-sent must survive the sweep. Without the bump the sweep would
// close an incident whose alert is firing right now.
func TestStaleClose_ResentAlertKeepsIncidentOpen(t *testing.T) {
	db := setupLastSeenDB(t)
	svc := newIncidentTestService(t, db)

	enabled := true
	minutes := 60
	if err := db.Create(&database.GeneralSettings{
		IncidentAutoCloseEnabled: &enabled,
		IncidentAutoCloseMinutes: &minutes,
	}).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	long := time.Now().Add(-48 * time.Hour)
	incUUID := seedLastSeenIncident(t, db, database.IncidentStatusCompleted)
	if err := db.Model(&database.Incident{}).Where("uuid = ?", incUUID).
		Updates(map[string]interface{}{"started_at": long, "completed_at": long}).Error; err != nil {
		t.Fatalf("age incident: %v", err)
	}

	a := alerts.NormalizedAlert{
		AlertName:         "HighCPU",
		TargetHost:        "host-01",
		SourceFingerprint: "fp-live",
		StartedAt:         &long,
	}
	if err := svc.InsertFiringAlert(context.Background(), incUUID, "src-lastseen", a, "new_incident", ""); err != nil {
		t.Fatalf("InsertFiringAlert: %v", err)
	}

	closer := NewStaleCloseService(db)

	// Before any re-send the incident is genuinely inactive and closes.
	result, err := closer.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("candidates before re-send = %d, want 1", len(result.Candidates))
	}

	// The source re-sends it, still reporting the original StartedAt. The
	// incident is live and must be spared.
	if err := svc.InsertFiringAlert(context.Background(), incUUID, "src-lastseen", a, "new_incident", ""); err != ErrAlertAlreadyClaimed {
		t.Fatalf("re-send error = %v, want ErrAlertAlreadyClaimed", err)
	}

	result, err = closer.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.IncidentsClosed != 0 {
		t.Errorf("IncidentsClosed = %d, want 0 — the alert is still being re-sent", result.IncidentsClosed)
	}
	if got := loadIncident(t, db, incUUID).Status; got != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want completed", got)
	}
}
