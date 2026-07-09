package services

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// seedMergeSettings inserts a GeneralSettings row controlling the merge gate.
func seedMergeSettings(t *testing.T, db *gorm.DB, enabled bool) {
	t.Helper()
	if err := db.Create(&database.GeneralSettings{
		IncidentMergeEnabled: &enabled,
	}).Error; err != nil {
		t.Fatalf("seed general settings: %v", err)
	}
}

// seedCompletedIncident inserts an alert-sourced incident in the given status
// with a diagnosis response and completion timestamps.
func seedCompletedIncident(t *testing.T, db *gorm.DB, uuid, title, response string, status database.IncidentStatus, startedAt time.Time) {
	t.Helper()
	completedAt := startedAt.Add(5 * time.Minute)
	inc := database.Incident{
		UUID:        uuid,
		Source:      "test",
		SourceKind:  database.IncidentSourceKindAlert,
		SourceUUID:  "src-1",
		Title:       title,
		Status:      status,
		StartedAt:   startedAt,
		CompletedAt: &completedAt,
		Response:    response,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed incident %s: %v", uuid, err)
	}
}

func mergeVerdictJSON(uuid string, confidence float64) string {
	return fmt.Sprintf(`{"merge":true,"incident_uuid":"%s","confidence":%.2f,"reasoning":"same bad deploy"}`, uuid, confidence)
}

func TestIncidentMerger_FlagOff_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, false)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls with flag off, got %d", caller.callCount())
	}
}

func TestIncidentMerger_ConfidentMatch_MergesIncidents(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	until := time.Now().Add(5 * time.Minute)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	if err := db.Model(&database.Incident{}).Where("uuid = ?", "inc-old").Update("monitor_until", &until).Error; err != nil {
		t.Fatalf("set monitor_until: %v", err)
	}
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))
	seedAlert(t, db, "alert-new", "inc-new", database.AlertStatusFiring, nil)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return mergeVerdictJSON("inc-old", 0.92), nil
	}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var merged database.Incident
	if err := db.Where("uuid = ?", "inc-new").First(&merged).Error; err != nil {
		t.Fatalf("load merged: %v", err)
	}
	if merged.Status != database.IncidentStatusMerged {
		t.Errorf("expected status=merged, got %s", merged.Status)
	}
	if merged.MergedIntoUUID != "inc-old" {
		t.Errorf("expected merged_into_uuid=inc-old, got %q", merged.MergedIntoUUID)
	}
	if merged.MonitorUntil != nil {
		t.Error("expected merged incident monitor_until cleared")
	}

	var alert database.Alert
	if err := db.Where("uuid = ?", "alert-new").First(&alert).Error; err != nil {
		t.Fatalf("load alert: %v", err)
	}
	if alert.IncidentUUID != "inc-old" {
		t.Errorf("expected alert re-pointed to inc-old, got %s", alert.IncidentUUID)
	}

	var survivor database.Incident
	if err := db.Where("uuid = ?", "inc-old").First(&survivor).Error; err != nil {
		t.Fatalf("load survivor: %v", err)
	}
	if survivor.Status != database.IncidentStatusMonitor {
		t.Errorf("survivor status changed unexpectedly: %s", survivor.Status)
	}
	if survivor.MonitorUntil == nil || !survivor.MonitorUntil.After(until) {
		t.Error("expected survivor monitor_until extended")
	}
}

func TestIncidentMerger_BelowThreshold_NoMerge(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: disk failure", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return mergeVerdictJSON("inc-old", 0.6), nil
	}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inc database.Incident
	db.Where("uuid = ?", "inc-new").First(&inc)
	if inc.Status == database.IncidentStatusMerged {
		t.Error("expected no merge below threshold")
	}
}

func TestIncidentMerger_HallucinatedUUID_NoMerge(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return mergeVerdictJSON("inc-invented", 0.95), nil
	}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inc database.Incident
	db.Where("uuid = ?", "inc-new").First(&inc)
	if inc.Status == database.IncidentStatusMerged {
		t.Error("expected no merge for hallucinated UUID")
	}
}

func TestIncidentMerger_NoResponse_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls for incident without diagnosis, got %d", caller.callCount())
	}
}

func TestIncidentMerger_NonAlertIncident_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	completedAt := time.Now()
	inc := database.Incident{
		UUID: "inc-cron", Source: "test", SourceKind: database.IncidentSourceKindCron,
		Status: database.IncidentStatusCompleted, StartedAt: time.Now().Add(-10 * time.Minute),
		CompletedAt: &completedAt, Response: "cron output",
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	caller := &fakeOneShotLLMCaller{}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-cron"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls for non-alert incident, got %d", caller.callCount())
	}
}

func TestIncidentMerger_CandidatesOnlyEarlierIncidents(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	// A LATER incident must not be offered as a survivor for an earlier one.
	seedCompletedIncident(t, db, "inc-later", "edge-guard down or0003", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-5*time.Minute))
	// An incident already merged away must not be a candidate either.
	seedCompletedIncident(t, db, "inc-merged", "edge-guard down or0004", "root cause: bad deploy v1.2", database.IncidentStatusMerged, time.Now().Add(-2*time.Hour))
	// Outside the lookback window.
	old := time.Now().Add(-48 * time.Hour)
	seedCompletedIncident(t, db, "inc-ancient", "edge-guard down or0005", "root cause: bad deploy v0.9", database.IncidentStatusCompleted, old)

	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: bad deploy v1.2", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	var inc database.Incident
	if err := db.Where("uuid = ?", "inc-new").First(&inc).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	m := NewIncidentMerger(&fakeOneShotLLMCaller{}, db, nil)
	candidates, err := m.fetchMergeCandidates(context.Background(), &inc)
	if err != nil {
		t.Fatalf("fetchMergeCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.UUID == "inc-later" || c.UUID == "inc-merged" || c.UUID == "inc-ancient" {
			t.Errorf("unexpected candidate %s", c.UUID)
		}
	}
	if len(candidates) != 0 {
		t.Errorf("expected empty candidate set, got %d", len(candidates))
	}
}

func TestIncidentMerger_PromptContainsDiagnoses(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedMergeSettings(t, db, true)
	seedCompletedIncident(t, db, "inc-old", "edge-guard down or0001", "root cause: bad deploy v1.2 of edge-guard", database.IncidentStatusMonitor, time.Now().Add(-1*time.Hour))
	seedCompletedIncident(t, db, "inc-new", "edge-guard down or0002", "root cause: same deploy v1.2 broke or0002", database.IncidentStatusMonitor, time.Now().Add(-10*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"merge":false,"incident_uuid":"","confidence":0.1,"reasoning":"unrelated"}`, nil
	}
	m := NewIncidentMerger(caller, db, nil)
	if err := m.EvaluateAndMerge(context.Background(), "inc-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", caller.callCount())
	}
	if !strings.Contains(caller.lastUser, "bad deploy v1.2 of edge-guard") {
		t.Error("prompt should contain candidate diagnosis")
	}
	if !strings.Contains(caller.lastUser, "same deploy v1.2 broke or0002") {
		t.Error("prompt should contain the new incident's diagnosis")
	}
	if !strings.Contains(caller.lastUser, "inc-old") {
		t.Error("prompt should contain candidate UUID")
	}
}
