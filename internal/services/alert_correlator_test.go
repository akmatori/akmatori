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

// setupCorrelatorDB prepares an in-memory SQLite DB with the incidents table
// and seeds LLM settings so the correlator can call GetLLMSettings.
func setupCorrelatorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Incident{},
		&database.LLMSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	database.DB = db

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

func newCorrelator(t *testing.T, caller OneShotLLMCaller, db *gorm.DB, cfg CorrelationConfig) *AlertCorrelator {
	t.Helper()
	return NewAlertCorrelator(caller, db, cfg)
}

func enabledCfg() CorrelationConfig {
	return CorrelationConfig{
		Enabled:       true,
		Window:        30 * time.Minute,
		MaxCandidates: 20,
		Threshold:     0.7,
	}
}

// ---- tests ----

func TestAlertCorrelator_FlagOff_NoLLMCall(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-1", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	cfg := enabledCfg()
	cfg.Enabled = false
	c := newCorrelator(t, caller, db, cfg)

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

	c := newCorrelator(t, nil, db, enabledCfg())
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

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-match","confidence":0.92,"reasoning":"same alert same host"}`, nil
	}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-low","confidence":0.55,"reasoning":"possibly related"}`, nil
	}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":false,"incident_uuid":"","confidence":0.1,"reasoning":"different alert"}`, nil
	}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		// LLM returns a UUID that was NOT in the candidate set.
		return `{"correlated":true,"incident_uuid":"inc-invented-by-llm","confidence":0.95,"reasoning":"hallucinated"}`, nil
	}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	c := newCorrelator(t, caller, db, enabledCfg())

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

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}
	c := newCorrelator(t, caller, db, enabledCfg())

	_, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if !errors.Is(err, ErrWorkerNotConnected) {
		t.Errorf("expected ErrWorkerNotConnected, got %v", err)
	}
}

func TestAlertCorrelator_MalformedJSON_Silent(t *testing.T) {
	db := setupCorrelatorDB(t)
	seedIncident(t, db, "inc-4", "CPU high on web01", "running", time.Now().Add(-5*time.Minute))

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "not valid json", nil
	}
	c := newCorrelator(t, caller, db, enabledCfg())

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
