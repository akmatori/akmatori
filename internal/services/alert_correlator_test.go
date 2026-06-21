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
		&database.LLMSettings{},
		&database.GeneralSettings{},
		&database.AlertSuppressionLog{},
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
func seedCorrelationSettings(t *testing.T, db *gorm.DB, enabled bool, _, _ int, _ float64) {
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

func TestAlertCorrelator_EmptyWindow_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	// Seed an incident that is outside the window (2 hours ago, window is 30m).
	seedIncident(t, db, "inc-old", "CPU high on web01", "running", time.Now().Add(-2*time.Hour))
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Correlated {
		t.Error("expected Correlated=false when no candidates in window")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls when candidates empty, got %d", caller.callCount())
	}
}

func TestAlertCorrelator_ConfidentMatch_AboveThreshold(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-match", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	// Only a failed incident in the window — should not be a candidate.
	seedIncident(t, db, "inc-failed", "CPU high on web01", "failed", time.Now().Add(-5*time.Minute))
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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

func TestCorrelationConfigWithDefaults_AppliesDefaults(t *testing.T) {
	cfg := CorrelationConfigWithDefaults(CorrelationConfig{})
	if cfg.Window != 30*time.Minute {
		t.Errorf("expected Window=30m, got %v", cfg.Window)
	}
	if cfg.MaxCandidates != 20 {
		t.Errorf("expected MaxCandidates=20, got %d", cfg.MaxCandidates)
	}
	if cfg.Threshold != 0.7 {
		t.Errorf("expected Threshold=0.7, got %f", cfg.Threshold)
	}
	// Enabled should remain false (zero value).
	if cfg.Enabled {
		t.Error("Enabled should default to false")
	}
}

func TestCorrelationConfigWithDefaults_PreservesNonZero(t *testing.T) {
	cfg := CorrelationConfigWithDefaults(CorrelationConfig{
		Enabled:       true,
		Window:        10 * time.Minute,
		MaxCandidates: 5,
		Threshold:     0.8,
	})
	if cfg.Window != 10*time.Minute || cfg.MaxCandidates != 5 || cfg.Threshold != 0.8 {
		t.Errorf("non-zero values should be preserved: %+v", cfg)
	}
	if !cfg.Enabled {
		t.Error("Enabled should be preserved")
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
	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

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
	// Threshold is hardcoded at 0.7; confidence 0.85 must be confident.
	thresh := c.Threshold()
	if thresh != 0.7 {
		t.Errorf("expected Threshold()=0.7, got %f", thresh)
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
	prompt := buildCorrelationUserPrompt(alert, candidates, map[string]struct{}{})

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

// seedIncidentWithFingerprint inserts an incident with the given fingerprint.
func seedIncidentWithFingerprint(t *testing.T, db *gorm.DB, uuid, title, status, fingerprint string, startedAt time.Time) {
	t.Helper()
	inc := database.Incident{
		UUID:             uuid,
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            title,
		Status:           database.IncidentStatus(status),
		StartedAt:        startedAt,
		Response:         "blocked — awaiting vendor fix",
		AlertFingerprint: fingerprint,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed fingerprint incident %s: %v", uuid, err)
	}
}

// TestFetchCandidates_LongWindowMatch verifies that a fingerprint-matching
// incident with status 'running' started 4 days ago is returned in the long-window
// set but NOT in the standard 30m window, and its UUID appears in longWindowUUIDs.
func TestFetchCandidates_LongWindowMatch(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := "abc123fingerprint"
	// Incident started 4 days ago — outside the 30m standard window.
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	seedIncidentWithFingerprint(t, db, "long-inc", "Service down for 4d", "running", fp, fourDaysAgo)

	c := NewAlertCorrelator(nil, db)
	candidates, longWindowUUIDs, err := c.fetchCandidates(context.Background(), fp, 30*time.Minute, 24*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	found := false
	for _, row := range candidates {
		if row.UUID == "long-inc" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected long-window incident to be in the candidates list")
	}
	if _, ok := longWindowUUIDs["long-inc"]; !ok {
		t.Error("expected long-inc to appear in longWindowUUIDs")
	}
}

// TestFetchCandidates_ResolvedExcludedFromLongWindow verifies that a completed
// (resolved) incident is not returned by the long-window query.
func TestFetchCandidates_ResolvedExcludedFromLongWindow(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := "resolvedfp"
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	// 'completed' incident — should be excluded from long-window (which only includes running/diagnosed).
	seedIncidentWithFingerprint(t, db, "resolved-inc", "Old resolved issue", "completed", fp, fourDaysAgo)

	c := NewAlertCorrelator(nil, db)
	_, longWindowUUIDs, err := c.fetchCandidates(context.Background(), fp, 30*time.Minute, 24*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}
	if _, ok := longWindowUUIDs["resolved-inc"]; ok {
		t.Error("completed incident must not appear in longWindowUUIDs")
	}
}

// TestFetchCandidates_EmptyFingerprintNoLongWindow verifies that the long-window
// query is skipped entirely when fingerprint is empty.
func TestFetchCandidates_EmptyFingerprintNoLongWindow(t *testing.T) {
	db := setupCorrelatorDB(t)

	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	seedIncidentWithFingerprint(t, db, "nofp-inc", "Blocked issue", "running", "somefp", fourDaysAgo)

	c := NewAlertCorrelator(nil, db)
	// Empty fingerprint — no long-window query should run.
	_, longWindowUUIDs, err := c.fetchCandidates(context.Background(), "", 30*time.Minute, 24*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}
	if len(longWindowUUIDs) != 0 {
		t.Errorf("expected empty longWindowUUIDs when fingerprint is empty, got %v", longWindowUUIDs)
	}
}

// TestCorrelate_LongWindowMatch_SetsIsLongWindowMatch verifies the end-to-end
// path: a fingerprint-matching running incident older than the standard window
// causes IsLongWindowMatch=true in the verdict.
func TestCorrelate_LongWindowMatch_SetsIsLongWindowMatch(t *testing.T) {
	db := setupCorrelatorDB(t)

	// Use the same fingerprint the correlator will compute.
	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")

	// Seed a running incident 4 days old — long-window only.
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour)
	seedIncidentWithFingerprint(t, db, "blocked-inc", "CPU blocked 4d", "running", fp, fourDaysAgo)

	enabled := true
	if err := db.Create(&database.GeneralSettings{
		AlertCorrelationEnabled: &enabled,
	}).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"blocked-inc","confidence":0.92,"reasoning":"same host blocked"}`, nil
	}
	c := NewAlertCorrelator(caller, db)

	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "web01"})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if !verdict.Correlated {
		t.Error("expected Correlated=true")
	}
	if verdict.IncidentUUID != "blocked-inc" {
		t.Errorf("expected matched UUID blocked-inc, got %q", verdict.IncidentUUID)
	}
	if !verdict.IsLongWindowMatch {
		t.Error("expected IsLongWindowMatch=true for 4-day-old incident matched via long-window")
	}
}

// TestBuildCorrelationUserPrompt_LongWindowLabel verifies that a long-window
// candidate is labeled with the [KNOWN OPEN ISSUE] marker in the prompt.
func TestBuildCorrelationUserPrompt_LongWindowLabel(t *testing.T) {
	alert := alerts.NormalizedAlert{AlertName: "DiskFull", TargetHost: "db01"}
	candidates := []candidateRow{
		{UUID: "short-inc", Title: "Short window inc", Status: "completed", StartedAt: time.Now().Add(-5 * time.Minute)},
		{UUID: "long-inc", Title: "Long window blocked inc", Status: "running", StartedAt: time.Now().Add(-96 * time.Hour)},
	}
	longWindowUUIDs := map[string]struct{}{"long-inc": {}}

	prompt := buildCorrelationUserPrompt(alert, candidates, longWindowUUIDs)

	if !strings.Contains(prompt, "KNOWN OPEN ISSUE") {
		t.Error("expected [KNOWN OPEN ISSUE] label for long-window candidate")
	}
	// Short-window candidate should NOT have the label.
	if strings.Contains(prompt, "short-inc") && strings.Contains(prompt, "KNOWN OPEN ISSUE") {
		// This is a coarse check; need to verify the label is only on the long candidate.
		// A more precise check: the label should NOT appear near short-inc.
		shortIdx := strings.Index(prompt, "short-inc")
		longIdx := strings.Index(prompt, "KNOWN OPEN ISSUE")
		if longIdx < shortIdx {
			t.Error("expected KNOWN OPEN ISSUE label to appear AFTER short-inc entry")
		}
	}
}

// TestCorrelationConfigWithDefaults_LongWindowDays verifies LongWindowDays defaults to 7.
func TestCorrelationConfigWithDefaults_LongWindowDays(t *testing.T) {
	cfg := CorrelationConfigWithDefaults(CorrelationConfig{})
	if cfg.LongWindowDays != 7 {
		t.Errorf("expected default LongWindowDays=7, got %d", cfg.LongWindowDays)
	}
}

// TestCorrelationConfigWithDefaults_FingerprintWindowDefault verifies FingerprintWindow
// defaults to 24h (1440 minutes) when not set.
func TestCorrelationConfigWithDefaults_FingerprintWindowDefault(t *testing.T) {
	cfg := CorrelationConfigWithDefaults(CorrelationConfig{})
	if cfg.FingerprintWindow != 24*time.Hour {
		t.Errorf("expected default FingerprintWindow=24h, got %v", cfg.FingerprintWindow)
	}
}

// TestFetchCandidates_FingerprintWindowMatchAt2h verifies that a fingerprint-matching
// incident started 2 hours ago (outside the standard 30m window) IS found when
// FingerprintWindow=1440m (24h).
func TestFetchCandidates_FingerprintWindowMatchAt2h(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := "fp-2hour-test"
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	// running status so it also qualifies for long-window, but 2h is within
	// the fingerprint window (24h) — should appear via query 2, NOT longWindowUUIDs.
	// Must NOT be completed: query 2 excludes completed incidents so that
	// AppendCorrelatedAlert is never called against a closed investigation.
	seedIncidentWithFingerprint(t, db, "fp-2h-inc", "Service down", "running", fp, twoHoursAgo)

	c := NewAlertCorrelator(nil, db)
	candidates, longWindowUUIDs, err := c.fetchCandidates(context.Background(), fp, 30*time.Minute, 24*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	found := false
	for _, row := range candidates {
		if row.UUID == "fp-2h-inc" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 2h-old fingerprint-matching incident to be found with FingerprintWindow=24h")
	}
	// Must NOT be in longWindowUUIDs (it was found via fingerprint query, not long-window).
	if _, ok := longWindowUUIDs["fp-2h-inc"]; ok {
		t.Error("2h incident found via fingerprint window must not be in longWindowUUIDs")
	}
}

// TestFetchCandidates_NonFingerprintAt40m_NotFound verifies that a legacy (empty
// fingerprint) incident started 40 minutes ago is NOT found when the standard
// window is 30 minutes — the fingerprint-gated wider window does not apply to
// empty-fingerprint rows.
func TestFetchCandidates_NonFingerprintAt40m_NotFound(t *testing.T) {
	db := setupCorrelatorDB(t)

	// Seed a 40m-old incident with no fingerprint (legacy row).
	fortyMinutesAgo := time.Now().Add(-40 * time.Minute)
	seedIncident(t, db, "legacy-40m", "Legacy alert", "running", fortyMinutesAgo)

	// Use a non-empty fingerprint for the incoming alert.
	incomingFP := "some-incoming-fingerprint"
	c := NewAlertCorrelator(nil, db)
	candidates, _, err := c.fetchCandidates(context.Background(), incomingFP, 30*time.Minute, 24*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	for _, row := range candidates {
		if row.UUID == "legacy-40m" {
			t.Error("legacy 40m incident must not be found: standard window is 30m and it has no matching fingerprint")
		}
	}
}

// TestFetchCandidates_FingerprintWindowShorterThanLongWindow verifies that an
// incident started 2 days ago (within 7d long-window but outside 24h fingerprint
// window default) is found only via the long-window query and appears in
// longWindowUUIDs.
func TestFetchCandidates_FingerprintWindowShorterThanLongWindow(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := "fp-2day"
	twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour)
	seedIncidentWithFingerprint(t, db, "2day-running", "Ongoing issue", "running", fp, twoDaysAgo)

	c := NewAlertCorrelator(nil, db)
	// FingerprintWindow=1h (shorter than 2 days), LongWindowDays=7.
	candidates, longWindowUUIDs, err := c.fetchCandidates(context.Background(), fp, 30*time.Minute, 1*time.Hour, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	found := false
	for _, row := range candidates {
		if row.UUID == "2day-running" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 2-day running incident to be found via long-window query")
	}
	if _, ok := longWindowUUIDs["2day-running"]; !ok {
		t.Error("2-day running incident should be in longWindowUUIDs (found only via long-window)")
	}
}
