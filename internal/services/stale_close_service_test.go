package services

import (
	"context"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupStaleCloseDB opens an isolated in-memory database per test.
//
// Deliberately ":memory:" rather than the shared-cache DSN used by
// setupIncidentTestDB: these tests each seed their own general_settings
// singleton, and a shared database would hand every test the first row any
// other test created.
func setupStaleCloseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Incident{},
		&database.Alert{},
		&database.GeneralSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
	return db
}

// staleSeed describes one incident (and optionally one linked alert) for the
// stale-close tests.
type staleSeed struct {
	status      database.IncidentStatus
	sourceKind  string
	startedAt   time.Time
	completedAt *time.Time
	mergedInto  string

	// withAlert seeds one linked alert row when true.
	withAlert  bool
	alertFired time.Time
	// alertLastSeen, when non-nil, is the alert's last re-send.
	alertLastSeen *time.Time
	// alertResolved marks the seeded alert resolved.
	alertResolved bool
}

func seedStaleIncident(t *testing.T, db *gorm.DB, s staleSeed) string {
	t.Helper()

	kind := s.sourceKind
	if kind == "" {
		kind = database.IncidentSourceKindAlert
	}
	incUUID := uuid.New().String()
	inc := database.Incident{
		UUID:           incUUID,
		Source:         "test",
		SourceKind:     kind,
		SourceUUID:     "src-stale-test",
		Title:          "stale test incident",
		Status:         s.status,
		StartedAt:      s.startedAt,
		CompletedAt:    s.completedAt,
		MergedIntoUUID: s.mergedInto,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	if s.withAlert {
		alert := database.Alert{
			UUID:              uuid.New().String(),
			IncidentUUID:      incUUID,
			Status:            database.AlertStatusFiring,
			SourceUUID:        "src-stale-test",
			SourceFingerprint: uuid.New().String(),
			AlertName:         "StaleTestAlert",
			TargetHost:        "host01",
			FiredAt:           s.alertFired,
			LastSeenAt:        s.alertLastSeen,
		}
		if s.alertResolved {
			resolved := s.alertFired.Add(time.Minute)
			alert.Status = database.AlertStatusResolved
			alert.ResolvedAt = &resolved
		}
		if err := db.Create(&alert).Error; err != nil {
			t.Fatalf("seed alert: %v", err)
		}
	}

	return incUUID
}

func seedAutoCloseSettings(t *testing.T, db *gorm.DB, enabled bool, minutes int) {
	t.Helper()
	if err := db.Create(&database.GeneralSettings{
		IncidentAutoCloseEnabled: &enabled,
		IncidentAutoCloseMinutes: &minutes,
	}).Error; err != nil {
		t.Fatalf("seed general settings: %v", err)
	}
}

func loadIncident(t *testing.T, db *gorm.DB, incUUID string) database.Incident {
	t.Helper()
	var inc database.Incident
	if err := db.Where("uuid = ?", incUUID).First(&inc).Error; err != nil {
		t.Fatalf("load incident %s: %v", incUUID, err)
	}
	return inc
}

// TestStaleClose_ClosesCompletedWithStuckFiringAlert covers the case this
// service exists for: an alert-sourced incident whose source never sent a
// matching resolve, so it is pinned at "completed" with a firing alert and the
// monitor sweep can never reach it.
func TestStaleClose_ClosesCompletedWithStuckFiringAlert(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, true, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	incUUID := seedStaleIncident(t, db, staleSeed{
		status:      database.IncidentStatusCompleted,
		startedAt:   old,
		completedAt: &old,
		withAlert:   true,
		alertFired:  old,
	})

	svc := NewStaleCloseService(db)
	result, err := svc.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IncidentsClosed != 1 {
		t.Fatalf("IncidentsClosed = %d, want 1", result.IncidentsClosed)
	}

	inc := loadIncident(t, db, incUUID)
	if inc.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", inc.Status)
	}
	if inc.ClosedReason != database.CloseReasonAutoStale {
		t.Errorf("ClosedReason = %q, want %q", inc.ClosedReason, database.CloseReasonAutoStale)
	}
	if inc.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}

	// The lingering firing alert must be resolved, not left dangling: it holds
	// a uniq_firing_alert slot that would otherwise block future alerts with
	// the same source fingerprint.
	var firing int64
	if err := db.Model(&database.Alert{}).
		Where("incident_uuid = ? AND status = ? AND resolved_at IS NULL",
			incUUID, string(database.AlertStatusFiring)).
		Count(&firing).Error; err != nil {
		t.Fatalf("count firing alerts: %v", err)
	}
	if firing != 0 {
		t.Errorf("firing alerts left = %d, want 0", firing)
	}
}

// TestStaleClose_KeepsActiveIncidents asserts the cases that must survive a
// sweep. Each is a distinct way an incident can still be alive.
func TestStaleClose_KeepsActiveIncidents(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	recent := time.Now().Add(-10 * time.Minute)

	tests := []struct {
		name string
		seed staleSeed
	}{
		{
			name: "recent alert activity",
			seed: staleSeed{
				status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
				withAlert: true, alertFired: recent,
			},
		},
		{
			name: "old alert re-sent recently",
			// The alert row is old, but the source keeps re-sending it. Without
			// last_seen_at this is indistinguishable from a dead alert, and the
			// sweep would close a live incident.
			seed: staleSeed{
				status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
				withAlert: true, alertFired: old, alertLastSeen: &recent,
			},
		},
		{
			name: "investigation turn completed recently",
			seed: staleSeed{
				status: database.IncidentStatusCompleted, startedAt: old, completedAt: &recent,
				withAlert: true, alertFired: old,
			},
		},
		{
			name: "still running",
			seed: staleSeed{status: database.IncidentStatusRunning, startedAt: old},
		},
		{
			name: "pending",
			seed: staleSeed{status: database.IncidentStatusPending, startedAt: old},
		},
		{
			name: "diagnosed",
			seed: staleSeed{status: database.IncidentStatusDiagnosed, startedAt: old},
		},
		{
			name: "failed stays terminal, not closed",
			seed: staleSeed{status: database.IncidentStatusFailed, startedAt: old, completedAt: &old},
		},
		{
			name: "merged is not open",
			seed: staleSeed{
				status: database.IncidentStatusMerged, startedAt: old, completedAt: &old,
				mergedInto: "some-survivor",
			},
		},
		{
			name: "cron incidents are out of scope",
			seed: staleSeed{
				status: database.IncidentStatusCompleted, sourceKind: database.IncidentSourceKindCron,
				startedAt: old, completedAt: &old,
			},
		},
		{
			name: "already closed",
			seed: staleSeed{status: database.IncidentStatusClosed, startedAt: old, completedAt: &old},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupStaleCloseDB(t)
			seedAutoCloseSettings(t, db, true, 24*60)
			incUUID := seedStaleIncident(t, db, tc.seed)

			svc := NewStaleCloseService(db)
			result, err := svc.Run(context.Background(), false)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.IncidentsClosed != 0 {
				t.Errorf("IncidentsClosed = %d, want 0", result.IncidentsClosed)
			}

			inc := loadIncident(t, db, incUUID)
			if inc.Status != tc.seed.status {
				t.Errorf("Status = %q, want %q (unchanged)", inc.Status, tc.seed.status)
			}
		})
	}
}

// TestStaleClose_ClosesExpiredMonitor confirms the sweep also covers monitor
// incidents, so it is a superset of the monitor sweep rather than a parallel
// path that could disagree with it.
func TestStaleClose_ClosesExpiredMonitor(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, true, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	incUUID := seedStaleIncident(t, db, staleSeed{
		status:      database.IncidentStatusMonitor,
		startedAt:   old,
		completedAt: &old,
		withAlert:   true,
		alertFired:  old,
		// Resolved, as a monitor incident's alerts always are.
		alertResolved: true,
	})

	svc := NewStaleCloseService(db)
	if _, err := svc.Run(context.Background(), false); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := loadIncident(t, db, incUUID).Status; got != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", got)
	}
}

// TestStaleClose_Disabled verifies the gate blocks the write path but still
// reports candidates, which is what makes the dry run useful before enabling.
func TestStaleClose_Disabled(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, false, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	incUUID := seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
		withAlert: true, alertFired: old,
	})

	svc := NewStaleCloseService(db)
	result, err := svc.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Enabled {
		t.Error("Enabled = true, want false")
	}
	if result.IncidentsClosed != 0 {
		t.Errorf("IncidentsClosed = %d, want 0", result.IncidentsClosed)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("Candidates = %d, want 1 (preview must work while disabled)", len(result.Candidates))
	}
	if got := loadIncident(t, db, incUUID).Status; got != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want completed", got)
	}
}

// TestStaleClose_DryRun verifies a preview changes nothing.
func TestStaleClose_DryRun(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, true, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	incUUID := seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
		withAlert: true, alertFired: old,
	})

	svc := NewStaleCloseService(db)
	result, err := svc.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IncidentsClosed != 0 {
		t.Errorf("IncidentsClosed = %d, want 0 on a dry run", result.IncidentsClosed)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("Candidates = %d, want 1", len(result.Candidates))
	}
	if result.Candidates[0].UUID != incUUID {
		t.Errorf("candidate UUID = %q, want %q", result.Candidates[0].UUID, incUUID)
	}
	if result.Candidates[0].FiringAlerts != 1 {
		t.Errorf("FiringAlerts = %d, want 1", result.Candidates[0].FiringAlerts)
	}
	if got := loadIncident(t, db, incUUID).Status; got != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want completed (dry run must not write)", got)
	}

	// A firing alert must survive a dry run too.
	var firing int64
	db.Model(&database.Alert{}).
		Where("incident_uuid = ? AND resolved_at IS NULL", incUUID).Count(&firing)
	if firing != 1 {
		t.Errorf("firing alerts = %d, want 1 (dry run must not resolve)", firing)
	}
}

// TestStaleClose_WindowBoundary checks the window is actually applied rather
// than assumed: the same incident is spared by a long window and closed by a
// short one.
func TestStaleClose_WindowBoundary(t *testing.T) {
	activity := time.Now().Add(-6 * time.Hour)

	for _, tc := range []struct {
		name       string
		minutes    int
		wantClosed int
	}{
		{name: "window longer than inactivity", minutes: 24 * 60, wantClosed: 0},
		{name: "window shorter than inactivity", minutes: 60, wantClosed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupStaleCloseDB(t)
			seedAutoCloseSettings(t, db, true, tc.minutes)
			seedStaleIncident(t, db, staleSeed{
				status: database.IncidentStatusCompleted, startedAt: activity, completedAt: &activity,
				withAlert: true, alertFired: activity,
			})

			svc := NewStaleCloseService(db)
			result, err := svc.Run(context.Background(), false)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.IncidentsClosed != tc.wantClosed {
				t.Errorf("IncidentsClosed = %d, want %d", result.IncidentsClosed, tc.wantClosed)
			}
		})
	}
}

// TestStaleClose_Idempotent verifies a second sweep finds nothing left.
func TestStaleClose_Idempotent(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, true, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
		withAlert: true, alertFired: old,
	})

	svc := NewStaleCloseService(db)
	if _, err := svc.Run(context.Background(), false); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := svc.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.IncidentsClosed != 0 {
		t.Errorf("second run closed %d, want 0", second.IncidentsClosed)
	}
}

// TestStaleClose_IncidentWithNoAlerts covers an alert-sourced incident whose
// alert row never landed: started_at is the only activity signal it has.
func TestStaleClose_IncidentWithNoAlerts(t *testing.T) {
	db := setupStaleCloseDB(t)
	seedAutoCloseSettings(t, db, true, 24*60)

	old := time.Now().Add(-72 * time.Hour)
	incUUID := seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: old, completedAt: &old,
	})

	svc := NewStaleCloseService(db)
	result, err := svc.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IncidentsClosed != 1 {
		t.Errorf("IncidentsClosed = %d, want 1", result.IncidentsClosed)
	}
	if got := loadIncident(t, db, incUUID).Status; got != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", got)
	}
}

// TestStaleClose_DefaultsWhenUnset verifies the shipped defaults: the gate is
// on and the window is three days when GeneralSettings has never been touched.
func TestStaleClose_DefaultsWhenUnset(t *testing.T) {
	db := setupStaleCloseDB(t)
	if err := db.Create(&database.GeneralSettings{}).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	// Inactive for two days: inside the 3-day default, so it must survive.
	twoDays := time.Now().Add(-48 * time.Hour)
	youngUUID := seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: twoDays, completedAt: &twoDays,
		withAlert: true, alertFired: twoDays,
	})

	// Inactive for four days: past the default.
	fourDays := time.Now().Add(-96 * time.Hour)
	oldUUID := seedStaleIncident(t, db, staleSeed{
		status: database.IncidentStatusCompleted, startedAt: fourDays, completedAt: &fourDays,
		withAlert: true, alertFired: fourDays,
	})

	svc := NewStaleCloseService(db)
	result, err := svc.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Enabled {
		t.Error("Enabled = false, want true by default")
	}
	if result.WindowMinutes != database.DefaultIncidentAutoCloseMinutes {
		t.Errorf("WindowMinutes = %d, want %d", result.WindowMinutes, database.DefaultIncidentAutoCloseMinutes)
	}
	if result.IncidentsClosed != 1 {
		t.Fatalf("IncidentsClosed = %d, want 1", result.IncidentsClosed)
	}
	if got := loadIncident(t, db, youngUUID).Status; got != database.IncidentStatusCompleted {
		t.Errorf("2-day-old incident Status = %q, want completed", got)
	}
	if got := loadIncident(t, db, oldUUID).Status; got != database.IncidentStatusClosed {
		t.Errorf("4-day-old incident Status = %q, want closed", got)
	}
}
