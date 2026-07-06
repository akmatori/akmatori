package handlers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
)

// ---- helpers ----

// insertTrackingService embeds corrGateSkillService and overrides InsertFiringAlert
// to record each call so tests can assert the correct arguments were passed.
type insertTrackingService struct {
	corrGateSkillService
	insertMu         sync.Mutex
	insertAlertCalls []insertAlertRecord
}

type insertAlertRecord struct {
	incidentUUID string
	sourceUUID   string
	alertName    string
	targetHost   string
	decision     string
}

func (s *insertTrackingService) InsertFiringAlert(_ context.Context, incidentUUID, sourceUUID string, a alerts.NormalizedAlert, decision, _ string) error {
	s.insertMu.Lock()
	defer s.insertMu.Unlock()
	s.insertAlertCalls = append(s.insertAlertCalls, insertAlertRecord{
		incidentUUID: incidentUUID,
		sourceUUID:   sourceUUID,
		alertName:    a.AlertName,
		targetHost:   a.TargetHost,
		decision:     decision,
	})
	return nil
}

func (s *insertTrackingService) getInsertCount() int {
	s.insertMu.Lock()
	defer s.insertMu.Unlock()
	return len(s.insertAlertCalls)
}

// setupResolvedAlertTestDB opens a global SQLite DB with the tables needed by
// processResolvedAlert and pre-seeds a GeneralSettings row so that
// GetOrCreateGeneralSettings never needs to INSERT inside an active transaction
// (which deadlocks on SQLite shared-cache).
func setupResolvedAlertTestDB(t *testing.T) {
	t.Helper()
	testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{},
		&database.Alert{},
		&database.GeneralSettings{},
		&database.SlackSettings{},
	)
	// Pre-seed GeneralSettings so GetOrCreateGeneralSettings does a SELECT-only
	// path when called inside the transaction in processResolvedAlert.
	if err := database.GetDB().Create(&database.GeneralSettings{}).Error; err != nil {
		t.Fatalf("seed GeneralSettings: %v", err)
	}
}

// seedResolvedAlertTestData inserts a firing incident + alert pair and returns
// the incidentUUID and alertUUID for further assertions.
func seedResolvedAlertTestData(t *testing.T, incidentStatus database.IncidentStatus, monitorUntil *time.Time, sourceFingerprint string) (string, string) {
	t.Helper()
	db := database.GetDB()

	incUUID := uuid.New().String()
	if err := db.Create(&database.Incident{
		UUID:         incUUID,
		Source:       "test",
		SourceKind:   database.IncidentSourceKindAlert,
		SourceUUID:   "src-resolved-test",
		Title:        "test incident",
		Status:       incidentStatus,
		StartedAt:    time.Now().Add(-10 * time.Minute),
		MonitorUntil: monitorUntil,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	alertUUID := uuid.New().String()
	if err := db.Create(&database.Alert{
		UUID:              alertUUID,
		IncidentUUID:      incUUID,
		Status:            database.AlertStatusFiring,
		Fingerprint:       "fp-test-resolved",
		SourceUUID:        "src-resolved-test",
		SourceFingerprint: sourceFingerprint,
		AlertName:         "TestAlert",
		TargetHost:        "host-resolved",
		FiredAt:           time.Now().Add(-10 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	return incUUID, alertUUID
}

// ---- tests ----

// TestProcessAlert_SpawnsIncidentAndInsertsAlertRow verifies that processAlert
// with no correlator wired calls SpawnIncidentManager exactly once and then
// calls InsertFiringAlert with the returned incident UUID and the correct source
// UUID and alert name.
func TestProcessAlert_SpawnsIncidentAndInsertsAlertRow(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.SlackSettings{},
		&database.Incident{},
		&database.Alert{},
	)

	const wantIncidentUUID = "test-spawned-incident-uuid"
	const wantSourceUUID = "src-insert-test"

	svc := &insertTrackingService{
		corrGateSkillService: corrGateSkillService{
			spawnUUID: wantIncidentUUID,
		},
	}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	// No correlator wired — every alert spawns a new incident.

	instance := &database.AlertSourceInstance{
		UUID:    wantSourceUUID,
		Name:    "test-source",
		Enabled: true,
		AlertSourceType: database.AlertSourceType{
			Name:        "prometheus",
			DisplayName: "Prometheus",
		},
	}
	alert := alerts.NormalizedAlert{
		AlertName:         "CPUHighDesign",
		TargetHost:        "web-redesign",
		Summary:           "CPU above threshold",
		Status:            database.AlertStatusFiring,
		Severity:          database.AlertSeverityCritical,
		SourceFingerprint: "fp-design-test",
	}

	h.processAlert(instance, alert)

	if got := svc.getSpawnCount(); got != 1 {
		t.Errorf("SpawnIncidentManager call count = %d, want 1", got)
	}
	if got := svc.getInsertCount(); got != 1 {
		t.Errorf("InsertFiringAlert call count = %d, want 1", got)
	}
	if got := svc.insertAlertCalls[0].incidentUUID; got != wantIncidentUUID {
		t.Errorf("InsertFiringAlert incidentUUID = %q, want %q", got, wantIncidentUUID)
	}
	if got := svc.insertAlertCalls[0].sourceUUID; got != wantSourceUUID {
		t.Errorf("InsertFiringAlert sourceUUID = %q, want %q", got, wantSourceUUID)
	}
	if got := svc.insertAlertCalls[0].alertName; got != alert.AlertName {
		t.Errorf("InsertFiringAlert alertName = %q, want %q", got, alert.AlertName)
	}
	if got := svc.insertAlertCalls[0].targetHost; got != alert.TargetHost {
		t.Errorf("InsertFiringAlert targetHost = %q, want %q", got, alert.TargetHost)
	}
	// With no correlator wired, decision must be "not_evaluated".
	if got := svc.insertAlertCalls[0].decision; got != "not_evaluated" {
		t.Errorf("InsertFiringAlert decision = %q, want %q", got, "not_evaluated")
	}
}

// TestProcessResolvedAlert_MatchBySourceFingerprint_MarksResolved verifies that
// processResolvedAlert finds the matching firing alert by source_fingerprint and
// sets its status to resolved with a non-nil resolved_at timestamp.
func TestProcessResolvedAlert_MatchBySourceFingerprint_MarksResolved(t *testing.T) {
	setupResolvedAlertTestDB(t)

	const sourceFingerprint = "fp-external-123"
	_, alertUUID := seedResolvedAlertTestData(t, database.IncidentStatusRunning, nil, sourceFingerprint)

	svc := &corrGateSkillService{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	normalized := alerts.NormalizedAlert{
		AlertName:         "TestAlert",
		TargetHost:        "host-resolved",
		Status:            database.AlertStatusResolved,
		SourceFingerprint: sourceFingerprint,
	}
	h.processResolvedAlert("src-resolved-test", normalized)

	var a database.Alert
	if err := database.GetDB().Where("uuid = ?", alertUUID).First(&a).Error; err != nil {
		t.Fatalf("load alert: %v", err)
	}
	if a.Status != database.AlertStatusResolved {
		t.Errorf("alert status = %q, want resolved", a.Status)
	}
	if a.ResolvedAt == nil {
		t.Error("alert resolved_at should be set after processResolvedAlert")
	}
}

// TestProcessResolvedAlert_LastFiringResolved_PullsInMonitorUntil verifies that
// when the last firing alert for a monitor-status incident is resolved,
// monitor_until is shortened to min(current, resolved_at + window).
func TestProcessResolvedAlert_LastFiringResolved_PullsInMonitorUntil(t *testing.T) {
	setupResolvedAlertTestDB(t)

	// Set a far-future monitor_until that should be pulled in.
	farFuture := time.Now().Add(24 * time.Hour)
	incUUID, _ := seedResolvedAlertTestData(t, database.IncidentStatusMonitor, &farFuture, "fp-monitor-pull")

	svc := &corrGateSkillService{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	before := time.Now()
	normalized := alerts.NormalizedAlert{
		AlertName:         "TestAlert",
		TargetHost:        "host-resolved",
		Status:            database.AlertStatusResolved,
		SourceFingerprint: "fp-monitor-pull",
	}
	h.processResolvedAlert("src-resolved-test", normalized)

	var incident database.Incident
	if err := database.GetDB().Where("uuid = ?", incUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.MonitorUntil == nil {
		t.Fatal("MonitorUntil should still be set after resolution")
	}
	// The pulled-in value must be earlier than the far-future original value.
	if !incident.MonitorUntil.Before(farFuture) {
		t.Errorf("MonitorUntil %v should be before far-future %v after last alert resolved", incident.MonitorUntil, farFuture)
	}
	// The pulled-in value should be approximately resolved_at + window (60 min),
	// so it must be after our before-call timestamp.
	if incident.MonitorUntil.Before(before) {
		t.Errorf("MonitorUntil %v should be after pre-call time %v", incident.MonitorUntil, before)
	}
}

// TestProcessResolvedAlert_LastFiringResolved_CompletedIncident_PromotesToMonitor
// verifies that resolving the last firing alert on a "completed" incident (one
// that UpdateIncidentComplete held out of monitor mode because this alert was
// still firing) now promotes it to monitor with a fresh monitor_until.
func TestProcessResolvedAlert_LastFiringResolved_CompletedIncident_PromotesToMonitor(t *testing.T) {
	setupResolvedAlertTestDB(t)

	incUUID, _ := seedResolvedAlertTestData(t, database.IncidentStatusCompleted, nil, "fp-completed-promote")

	svc := &corrGateSkillService{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	before := time.Now()
	normalized := alerts.NormalizedAlert{
		AlertName:         "TestAlert",
		TargetHost:        "host-resolved",
		Status:            database.AlertStatusResolved,
		SourceFingerprint: "fp-completed-promote",
	}
	h.processResolvedAlert("src-resolved-test", normalized)

	var incident database.Incident
	if err := database.GetDB().Where("uuid = ?", incUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusMonitor {
		t.Errorf("Status = %q, want monitor once the last firing alert resolves on a completed incident", incident.Status)
	}
	if incident.MonitorUntil == nil || incident.MonitorUntil.Before(before) {
		t.Errorf("MonitorUntil should be freshly set in the future, got %v", incident.MonitorUntil)
	}
}

// TestProcessResolvedAlert_NoMatchingAlert_DropsSilently verifies that when no
// firing alert matches the incoming resolved notification, the function logs and
// silently drops the event without returning an error or panicking.
func TestProcessResolvedAlert_NoMatchingAlert_DropsSilently(t *testing.T) {
	setupResolvedAlertTestDB(t)
	// Seed an incident but no alert rows.
	if err := database.GetDB().Create(&database.Incident{
		UUID:       uuid.New().String(),
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-no-match",
		Title:      "no match incident",
		Status:     database.IncidentStatusRunning,
		StartedAt:  time.Now().Add(-5 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	svc := &corrGateSkillService{}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	// Should complete without panicking and without returning an error.
	// There is no firing alert with the given source_fingerprint or fingerprint.
	normalized := alerts.NormalizedAlert{
		AlertName:         "NonExistentAlert",
		TargetHost:        "host-no-match",
		Status:            database.AlertStatusResolved,
		SourceFingerprint: "fp-nonexistent",
	}
	// processResolvedAlert returns void; we just verify it does not panic.
	h.processResolvedAlert("src-no-match", normalized)

	// Verify no alert rows were created as a side effect.
	var count int64
	database.GetDB().Model(&database.Alert{}).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 alert rows after silent drop, got %d", count)
	}
}
