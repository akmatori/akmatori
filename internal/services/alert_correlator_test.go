package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupCorrelatorDB prepares an in-memory SQLite DB with the incidents and
// GeneralSettings tables and seeds LLM settings so the correlator can call
// GetLLMSettings. It also assigns database.DB so GetOrCreateGeneralSettings works.
func setupCorrelatorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Incident{},
		&database.Alert{},
		&database.LLMSettings{},
		&database.GeneralSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })

	// Seed active LLM settings so GetLLMSettings returns a valid row.
	if err := db.Create(&database.LLMSettings{
		Name:     "test",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "test-key",
		Model:    "claude-sonnet-4-6",
		Active:   true,
		Enabled:  true,
	}).Error; err != nil {
		t.Fatalf("seed llm settings: %v", err)
	}
	return db
}

// seedCorrelationSettings inserts a GeneralSettings row that enables the correlator.
func seedCorrelationSettings(t *testing.T, db *gorm.DB, enabled bool) {
	t.Helper()
	if err := db.Create(&database.GeneralSettings{
		AlertCorrelationEnabled: &enabled,
	}).Error; err != nil {
		t.Fatalf("seed general settings: %v", err)
	}
}

// seedIncident inserts a minimal incident into db and returns its UUID.
func seedIncident(t *testing.T, db *gorm.DB, uuid, title, status string, startedAt time.Time) {
	t.Helper()
	inc := database.Incident{
		UUID:       uuid,
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-1",
		Title:      title,
		Status:     database.IncidentStatus(status),
		StartedAt:  startedAt,
		Response:   "some response text",
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed incident %s: %v", uuid, err)
	}
}

// seedAlert inserts a minimal alert row linked to an incident.
func seedAlert(t *testing.T, db *gorm.DB, uuid, incidentUUID string, status database.AlertStatus, resolvedAt *time.Time) {
	t.Helper()
	alert := database.Alert{
		UUID:         uuid,
		IncidentUUID: incidentUUID,
		Status:       status,
		FiredAt:      time.Now().Add(-10 * time.Minute),
		ResolvedAt:   resolvedAt,
	}
	if err := db.Create(&alert).Error; err != nil {
		t.Fatalf("seed alert %s: %v", uuid, err)
	}
}

func newCorrelator(t *testing.T, caller OneShotLLMCaller, db *gorm.DB) *AlertCorrelator {
	t.Helper()
	return NewAlertCorrelator(caller, db)
}

// ---- tests ----

func TestAlertCorrelator_FlagOff_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-1", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	// No GeneralSettings row → enabled defaults to false.

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false when flag is off")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestAlertCorrelator_NilCaller_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-2", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))

	c := newCorrelator(t, nil, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false when caller is nil")
	}
}

func TestAlertCorrelator_ConfidentMatch_AboveThreshold(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-match", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-match","confidence":0.92,"reasoning":"same alert same host"}`, nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "web01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Correlated {
		t.Error("expected Correlated=true")
	}
	if verdict.IncidentUUID != "inc-match" {
		t.Errorf("expected IncidentUUID=inc-match, got %s", verdict.IncidentUUID)
	}
	if !verdict.IsConfident(0.7) {
		t.Error("expected IsConfident(0.7)=true at 0.92")
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
}

func TestAlertCorrelator_ConfidentMatch_BelowThreshold(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-low", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-low","confidence":0.55,"reasoning":"possibly related"}`, nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.IsConfident(0.7) {
		t.Error("expected IsConfident(0.7)=false at 0.55")
	}
	// Correlated is true but below threshold — the caller decides action.
	if !verdict.Correlated {
		t.Error("Correlated should reflect LLM response even below threshold")
	}
}

func TestAlertCorrelator_NotCorrelated(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-unrelated", "Disk full on db01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":false,"incident_uuid":"","confidence":0.1,"reasoning":"different alert"}`, nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false")
	}
	if verdict.IsConfident(0.7) {
		t.Error("expected IsConfident=false")
	}
}

func TestAlertCorrelator_HallucinatedUUID_ForcedFalse(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-real", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		// LLM returns a UUID that was NOT in the candidate set.
		return `{"correlated":true,"incident_uuid":"inc-invented-by-llm","confidence":0.95,"reasoning":"hallucinated"}`, nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("hallucinated UUID must be rejected → Correlated=false")
	}
}

func TestAlertCorrelator_FailedIncidentExcluded(t *testing.T) {
	db := setupCorrelatorDB(t)
	// Only a failed incident — should not be a candidate.
	seedIncident(t, db, "inc-failed", "CPU high on web01", "failed", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false (no viable candidates)")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls (no candidates), got %d", caller.callCount())
	}
}

func TestAlertCorrelator_WorkerNotConnected_ReturnedAsIs(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-3", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}
	c := newCorrelator(t, caller, db)

	_, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if !errors.Is(err, ErrWorkerNotConnected) {
		t.Errorf("expected ErrWorkerNotConnected, got %v", err)
	}
}

func TestAlertCorrelator_MalformedJSON_Silent(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-4", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "not valid json", nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("malformed JSON should return nil error (log-and-continue), got %v", err)
	}
	if verdict.Correlated {
		t.Error("malformed JSON should yield Correlated=false")
	}
}

// ---- unit tests for helpers ----

func TestParseCorrelationVerdict_StripsCodeFence(t *testing.T) {
	raw := "```json\n{\"correlated\":true,\"incident_uuid\":\"uuid-1\",\"confidence\":0.88,\"reasoning\":\"match\"}\n```"
	v, err := parseCorrelationVerdict(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !v.Correlated || v.IncidentUUID != "uuid-1" || v.Confidence != 0.88 {
		t.Errorf("unexpected verdict: %+v", v)
	}
}

func TestParseCorrelationVerdict_ClampsConfidence(t *testing.T) {
	v, _ := parseCorrelationVerdict(`{"correlated":true,"incident_uuid":"x","confidence":1.5,"reasoning":"over"}`)
	if v.Confidence != 1.0 {
		t.Errorf("expected confidence clamped to 1.0, got %f", v.Confidence)
	}
	v2, _ := parseCorrelationVerdict(`{"correlated":false,"incident_uuid":"","confidence":-0.2,"reasoning":"under"}`)
	if v2.Confidence != 0 {
		t.Errorf("expected confidence clamped to 0, got %f", v2.Confidence)
	}
}

func TestParseCorrelationVerdict_EmptyInput(t *testing.T) {
	_, err := parseCorrelationVerdict("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestCorrelationVerdict_IsConfident(t *testing.T) {
	cases := []struct {
		v         CorrelationVerdict
		threshold float64
		want      bool
	}{
		{CorrelationVerdict{Correlated: true, Confidence: 0.95}, 0.7, true},
		{CorrelationVerdict{Correlated: true, Confidence: 0.7}, 0.7, true},
		{CorrelationVerdict{Correlated: true, Confidence: 0.69}, 0.7, false},
		{CorrelationVerdict{Correlated: false, Confidence: 0.99}, 0.7, false},
	}
	for _, tc := range cases {
		got := tc.v.IsConfident(tc.threshold)
		if got != tc.want {
			t.Errorf("IsConfident(%+v, %.2f) = %v, want %v", tc.v, tc.threshold, got, tc.want)
		}
	}
}

// TestAlertCorrelator_DBStoredFlagOff_NoLLMCall verifies that a GeneralSettings
// row with enabled=false prevents any LLM call even when candidates exist.
func TestAlertCorrelator_DBStoredFlagOff_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-db-off", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	// Explicitly seed enabled=false.
	disabled := false
	if err := db.Create(&database.GeneralSettings{
		AlertCorrelationEnabled: &disabled,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false when DB-stored enabled=false")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls with DB-stored flag off, got %d", caller.callCount())
	}
}

// TestAlertCorrelator_Threshold_DefaultApplied verifies that Threshold() returns
// the hardcoded default (0.7) and that IsConfident correctly classifies verdicts
// relative to that threshold.
func TestAlertCorrelator_Threshold_DefaultApplied(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-thresh", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-thresh","confidence":0.85,"reasoning":"fairly sure"}`, nil
	}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "web01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Correlated {
		t.Error("expected Correlated=true from LLM response")
	}
	// Threshold is hardcoded at correlationThreshold (0.7); confidence 0.85 must be confident.
	thresh := c.Threshold()
	if thresh != correlationThreshold {
		t.Errorf("expected Threshold()=%v, got %f", correlationThreshold, thresh)
	}
	if !verdict.IsConfident(thresh) {
		t.Error("expected IsConfident(0.7)=true for confidence 0.85")
	}
}

func TestBuildCorrelationUserPrompt_ContainsAlertAndCandidates(t *testing.T) {
	alert := alerts.NormalizedAlert{
		AlertName:  "DiskFull",
		TargetHost: "db01",
		Summary:    "Disk usage above 90%",
	}
	candidates := []candidateRow{
		{
			UUID:      "uuid-a",
			Title:     "Disk full on db01",
			Status:    "running",
			Response:  "Investigating disk usage",
			StartedAt: time.Now().Add(-10 * time.Minute),
		},
	}
	prompt := buildCorrelationUserPrompt(alert, candidates)

	if !strings.Contains(prompt, "DiskFull") {
		t.Error("prompt should contain alert name")
	}
	if !strings.Contains(prompt, "db01") {
		t.Error("prompt should contain target host")
	}
	if !strings.Contains(prompt, "uuid-a") {
		t.Error("prompt should contain candidate UUID")
	}
	if !strings.Contains(prompt, "Disk full on db01") {
		t.Error("prompt should contain candidate title")
	}
}

// TestFetchCandidates_MonitorWithinWindow_IsCandidate verifies that a monitor-status
// incident with monitor_until in the future is included in candidates.
func TestFetchCandidates_MonitorWithinWindow_IsCandidate(t *testing.T) {
	db := setupCorrelatorDB(t)

	futureUntil := time.Now().Add(30 * time.Minute)
	inc := database.Incident{
		UUID:         "monitor-active",
		Source:       "test",
		SourceKind:   database.IncidentSourceKindAlert,
		SourceUUID:   "src-1",
		Title:        "CPU high - in monitor",
		Status:       database.IncidentStatusMonitor,
		StartedAt:    time.Now().Add(-2 * time.Hour),
		MonitorUntil: &futureUntil,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	found := false
	for _, row := range candidates {
		if row.UUID == "monitor-active" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected monitor incident with future monitor_until to be a candidate")
	}
}

// TestFetchCandidates_MonitorExpired_NotCandidate verifies that a monitor-status
// incident whose monitor_until is in the past is excluded from candidates.
func TestFetchCandidates_MonitorExpired_NotCandidate(t *testing.T) {
	db := setupCorrelatorDB(t)

	pastUntil := time.Now().Add(-5 * time.Minute)
	inc := database.Incident{
		UUID:         "monitor-expired",
		Source:       "test",
		SourceKind:   database.IncidentSourceKindAlert,
		SourceUUID:   "src-1",
		Title:        "CPU high - monitor expired",
		Status:       database.IncidentStatusMonitor,
		StartedAt:    time.Now().Add(-3 * time.Hour),
		MonitorUntil: &pastUntil,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	for _, row := range candidates {
		if row.UUID == "monitor-expired" {
			t.Error("expected monitor incident with expired monitor_until to be excluded from candidates")
		}
	}
}

// TestFetchCandidates_CompletedWithFiringAlert_IsCandidate verifies that a
// completed incident held out of monitor mode (because its alert never
// resolved) is still a correlation candidate.
func TestFetchCandidates_CompletedWithFiringAlert_IsCandidate(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "completed-still-firing", "edge-guard down on or0001", "completed", time.Now().Add(-20*time.Minute))
	seedAlert(t, db, "alert-1", "completed-still-firing", database.AlertStatusFiring, nil)

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	found := false
	for _, row := range candidates {
		if row.UUID == "completed-still-firing" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected completed incident with a still-firing alert to be a candidate")
	}
}

// TestFetchCandidates_CompletedFullyResolved_NotCandidate verifies that a
// completed incident whose alert already resolved is excluded — that
// incident should have already been promoted to monitor status by
// ResolveAlertTx, and once its monitor window lapses it must not resurface.
func TestFetchCandidates_CompletedFullyResolved_NotCandidate(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "completed-resolved", "edge-guard down on or0002", "completed", time.Now().Add(-3*time.Hour))
	resolvedAt := time.Now().Add(-2 * time.Hour)
	seedAlert(t, db, "alert-2", "completed-resolved", database.AlertStatusResolved, &resolvedAt)

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	for _, row := range candidates {
		if row.UUID == "completed-resolved" {
			t.Error("expected fully-resolved completed incident to be excluded from candidates")
		}
	}
}

// TestFetchCandidates_CompletedNoAlerts_NotCandidate verifies that a completed
// incident with no linked alert rows at all is excluded (no unresolved alert
// exists to justify eligibility).
func TestFetchCandidates_CompletedNoAlerts_NotCandidate(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "completed-no-alerts", "CPU high on web01", "completed", time.Now().Add(-20*time.Minute))

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	for _, row := range candidates {
		if row.UUID == "completed-no-alerts" {
			t.Error("expected completed incident with no alert rows to be excluded from candidates")
		}
	}
}
