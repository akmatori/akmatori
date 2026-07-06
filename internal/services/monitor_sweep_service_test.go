package services

import (
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// seedSweepIncident inserts an incident row with the given status/monitor_until
// for sweep tests.
func seedSweepIncident(t *testing.T, db *gorm.DB, status database.IncidentStatus, monitorUntil *time.Time) string {
	t.Helper()
	incUUID := uuid.New().String()
	if err := db.Create(&database.Incident{
		UUID:         incUUID,
		Source:       "test",
		SourceKind:   database.IncidentSourceKindAlert,
		SourceUUID:   "src-sweep-test",
		Title:        "sweep test incident",
		Status:       status,
		StartedAt:    time.Now().Add(-2 * time.Hour),
		MonitorUntil: monitorUntil,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	return incUUID
}

func TestRunSweep_ExpiredMonitor_ClosesIncident(t *testing.T) {
	db := setupIncidentTestDB(t)
	expired := time.Now().Add(-time.Hour)
	incUUID := seedSweepIncident(t, db, database.IncidentStatusMonitor, &expired)

	svc := NewMonitorSweepService(db)
	result, err := svc.RunSweep()
	if err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}
	if result.IncidentsClosed != 1 {
		t.Errorf("IncidentsClosed = %d, want 1", result.IncidentsClosed)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", incident.Status)
	}
	if incident.MonitorUntil != nil {
		t.Errorf("MonitorUntil should be cleared, got %v", incident.MonitorUntil)
	}
	if incident.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}
}

func TestRunSweep_ActiveMonitorWindow_LeavesIncidentAlone(t *testing.T) {
	db := setupIncidentTestDB(t)
	future := time.Now().Add(time.Hour)
	incUUID := seedSweepIncident(t, db, database.IncidentStatusMonitor, &future)

	svc := NewMonitorSweepService(db)
	result, err := svc.RunSweep()
	if err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}
	if result.IncidentsClosed != 0 {
		t.Errorf("IncidentsClosed = %d, want 0", result.IncidentsClosed)
	}

	var incident database.Incident
	db.Where("uuid = ?", incUUID).First(&incident)
	if incident.Status != database.IncidentStatusMonitor {
		t.Errorf("Status = %q, want unchanged (monitor)", incident.Status)
	}
}

func TestRunSweep_NonMonitorIncidents_Untouched(t *testing.T) {
	db := setupIncidentTestDB(t)
	completedUUID := seedSweepIncident(t, db, database.IncidentStatusCompleted, nil)
	runningUUID := seedSweepIncident(t, db, database.IncidentStatusRunning, nil)

	svc := NewMonitorSweepService(db)
	if _, err := svc.RunSweep(); err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}

	var completed, running database.Incident
	db.Where("uuid = ?", completedUUID).First(&completed)
	db.Where("uuid = ?", runningUUID).First(&running)
	if completed.Status != database.IncidentStatusCompleted {
		t.Errorf("completed incident status = %q, want unchanged", completed.Status)
	}
	if running.Status != database.IncidentStatusRunning {
		t.Errorf("running incident status = %q, want unchanged", running.Status)
	}
}

func TestRunSweep_FiringAlertOnExpiredMonitor_ResolvedBeforeClose(t *testing.T) {
	db := setupIncidentTestDB(t)
	expired := time.Now().Add(-time.Hour)
	incUUID := seedSweepIncident(t, db, database.IncidentStatusMonitor, &expired)
	alertUUID := seedCorrelatedAlert(t, db, incUUID) // firing, unresolved

	svc := NewMonitorSweepService(db)
	result, err := svc.RunSweep()
	if err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}
	if result.IncidentsClosed != 1 {
		t.Errorf("IncidentsClosed = %d, want 1", result.IncidentsClosed)
	}

	var alert database.Alert
	db.Where("uuid = ?", alertUUID).First(&alert)
	if alert.Status != database.AlertStatusResolved || alert.ResolvedAt == nil {
		t.Errorf("alert should be resolved as a safety net, got status=%q resolved_at=%v", alert.Status, alert.ResolvedAt)
	}

	var incident database.Incident
	db.Where("uuid = ?", incUUID).First(&incident)
	if incident.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", incident.Status)
	}
}
