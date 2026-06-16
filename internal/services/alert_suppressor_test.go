package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupSuppressorDB prepares an in-memory SQLite DB with the memories and
// GeneralSettings tables and seeds LLM settings so the suppressor can call
// GetLLMSettings and GetOrCreateGeneralSettings.
func setupSuppressorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Memory{},
		&database.LLMSettings{},
		&database.GeneralSettings{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })

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

// seedSuppressionSettings inserts a GeneralSettings row that enables the
// suppressor with the given parameters.
func seedSuppressionSettings(t *testing.T, db *gorm.DB, enabled bool, threshold float64) {
	t.Helper()
	if err := db.Create(&database.GeneralSettings{
		AlertSuppressionEnabled:   &enabled,
		AlertSuppressionThreshold: &threshold,
	}).Error; err != nil {
		t.Fatalf("seed suppression settings: %v", err)
	}
}

// seedSignature inserts a suppression-signature memory into db.
func seedSignature(t *testing.T, db *gorm.DB, name, body string) {
	t.Helper()
	m := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        name,
		Description: "test signature",
		Body:        body,
		Suppress:    true,
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed signature %s: %v", name, err)
	}
}

// ---- tests ----

func TestAlertSuppressor_FlagOff_NoLLMCall(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "disk-false-positive", "DiskSpaceLow on cron-*.prod fires nightly")
	// No GeneralSettings row → suppression enabled defaults to false.

	caller := &fakeOneShotLLMCaller{}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("expected Suppressed=false when flag is off")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestAlertSuppressor_NilCaller_NoLLMCall(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "disk-false-positive", "DiskSpaceLow fires nightly")

	s := NewAlertSuppressor(nil, db)
	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("expected Suppressed=false when caller is nil")
	}
}

func TestAlertSuppressor_ZeroSignatures_NoLLMCall(t *testing.T) {
	db := setupSuppressorDB(t)
	// No signatures in DB (no suppress=true rows).
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("expected Suppressed=false with no signatures")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls when no signatures, got %d", caller.callCount())
	}
}

func TestAlertSuppressor_NonSuppressMemoriesIgnored(t *testing.T) {
	db := setupSuppressorDB(t)
	// Insert a memory that is NOT flagged as a signature (suppress=false).
	m := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        "regular-memory",
		Description: "not a signature",
		Body:        "DiskSpaceLow on cron-*.prod fires nightly",
		Suppress:    false,
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("expected Suppressed=false: non-suppress memories should not be candidates")
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls (no signatures), got %d", caller.callCount())
	}
}

func TestAlertSuppressor_ConfidentSuppress_AboveThreshold(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "cron-disk-false-positive", "DiskSpaceLow on cron-01 nightly rotation")
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"suppressed":true,"signature_name":"cron-disk-false-positive","confidence":0.95,"reasoning":"identical rule on same host"}`, nil
	}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{
		AlertName:  "DiskSpaceLow",
		TargetHost: "cron-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Suppressed {
		t.Error("expected Suppressed=true")
	}
	if verdict.SignatureName != "cron-disk-false-positive" {
		t.Errorf("expected SignatureName=cron-disk-false-positive, got %s", verdict.SignatureName)
	}
	if !verdict.IsConfident(0.7) {
		t.Error("expected IsConfident(0.7)=true at 0.95")
	}
	if caller.callCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", caller.callCount())
	}
}

func TestAlertSuppressor_LowConfidence_NotSuppressed(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "maybe-similar", "DiskSpaceLow pattern")
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"suppressed":true,"signature_name":"maybe-similar","confidence":0.55,"reasoning":"possibly related but uncertain"}`, nil
	}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.IsConfident(0.7) {
		t.Error("expected IsConfident(0.7)=false at 0.55")
	}
	// Suppressed is true from LLM but below threshold — caller decides action.
	if !verdict.Suppressed {
		t.Error("Suppressed should reflect LLM response even below threshold")
	}
}

func TestAlertSuppressor_WorkerNotConnected_PropagatedFailOpen(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "some-sig", "Some pattern")
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return "", ErrWorkerNotConnected
	}
	s := NewAlertSuppressor(caller, db)

	_, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if !errors.Is(err, ErrWorkerNotConnected) {
		t.Errorf("expected ErrWorkerNotConnected, got %v", err)
	}
}

func TestAlertSuppressor_HallucinatedName_ForcedFalse(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "real-sig", "Real pattern")
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		// LLM returns a signature name NOT in the set.
		return `{"suppressed":true,"signature_name":"invented-sig-llm","confidence":0.95,"reasoning":"hallucinated"}`, nil
	}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "DiskSpaceLow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("hallucinated signature name must be rejected → Suppressed=false")
	}
}

func TestAlertSuppressor_NotSuppressed_LLMSaysNo(t *testing.T) {
	db := setupSuppressorDB(t)
	seedSignature(t, db, "unrelated-pattern", "Memory leak on cache servers")
	seedSuppressionSettings(t, db, true, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"suppressed":false,"signature_name":"","confidence":0.1,"reasoning":"different alert"}`, nil
	}
	s := NewAlertSuppressor(caller, db)

	verdict, err := s.Evaluate(context.Background(), alerts.NormalizedAlert{AlertName: "CPUHigh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Suppressed {
		t.Error("expected Suppressed=false when LLM says no")
	}
}

// ---- unit tests for helpers ----

func TestParseSuppressionVerdict_StripsCodeFence(t *testing.T) {
	raw := "```json\n{\"suppressed\":true,\"signature_name\":\"sig-1\",\"confidence\":0.92,\"reasoning\":\"match\"}\n```"
	v, err := parseSuppressionVerdict(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !v.Suppressed || v.SignatureName != "sig-1" || v.Confidence != 0.92 {
		t.Errorf("unexpected verdict: %+v", v)
	}
}

func TestParseSuppressionVerdict_ClampsConfidence(t *testing.T) {
	v, _ := parseSuppressionVerdict(`{"suppressed":true,"signature_name":"x","confidence":1.5,"reasoning":"over"}`)
	if v.Confidence != 1.0 {
		t.Errorf("expected confidence clamped to 1.0, got %f", v.Confidence)
	}
	v2, _ := parseSuppressionVerdict(`{"suppressed":false,"signature_name":"","confidence":-0.2,"reasoning":"under"}`)
	if v2.Confidence != 0 {
		t.Errorf("expected confidence clamped to 0, got %f", v2.Confidence)
	}
}

func TestParseSuppressionVerdict_EmptyInput(t *testing.T) {
	_, err := parseSuppressionVerdict("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestSuppressionVerdict_IsConfident(t *testing.T) {
	cases := []struct {
		v         SuppressionVerdict
		threshold float64
		want      bool
	}{
		{SuppressionVerdict{Suppressed: true, Confidence: 0.95}, 0.7, true},
		{SuppressionVerdict{Suppressed: true, Confidence: 0.7}, 0.7, true},
		{SuppressionVerdict{Suppressed: true, Confidence: 0.69}, 0.7, false},
		{SuppressionVerdict{Suppressed: false, Confidence: 0.99}, 0.7, false},
	}
	for _, tc := range cases {
		got := tc.v.IsConfident(tc.threshold)
		if got != tc.want {
			t.Errorf("IsConfident(%+v, %.2f) = %v, want %v", tc.v, tc.threshold, got, tc.want)
		}
	}
}

func TestSuppressionConfigWithDefaults_AppliesDefaults(t *testing.T) {
	cfg := SuppressionConfigWithDefaults(SuppressionConfig{})
	if cfg.MaxSignatures != 50 {
		t.Errorf("expected MaxSignatures=50, got %d", cfg.MaxSignatures)
	}
	if cfg.Threshold != 0.7 {
		t.Errorf("expected Threshold=0.7, got %f", cfg.Threshold)
	}
	if cfg.Enabled {
		t.Error("Enabled should default to false")
	}
}

func TestSuppressionConfigWithDefaults_PreservesNonZero(t *testing.T) {
	cfg := SuppressionConfigWithDefaults(SuppressionConfig{
		Enabled:       true,
		MaxSignatures: 10,
		Threshold:     0.8,
	})
	if cfg.MaxSignatures != 10 || cfg.Threshold != 0.8 {
		t.Errorf("non-zero values should be preserved: %+v", cfg)
	}
	if !cfg.Enabled {
		t.Error("Enabled should be preserved")
	}
}

func TestBuildSuppressionUserPrompt_ContainsAlertAndSignatures(t *testing.T) {
	alert := alerts.NormalizedAlert{
		AlertName:  "DiskSpaceLow",
		TargetHost: "cron-01",
		Summary:    "Disk usage above 90%",
	}
	sigs := []signatureRow{
		{Name: "cron-disk-sig", Body: "Nightly rotation pattern on cron servers"},
	}
	prompt := buildSuppressionUserPrompt(alert, sigs)

	if !strings.Contains(prompt, "DiskSpaceLow") {
		t.Error("prompt should contain alert name")
	}
	if !strings.Contains(prompt, "cron-01") {
		t.Error("prompt should contain target host")
	}
	if !strings.Contains(prompt, "cron-disk-sig") {
		t.Error("prompt should contain signature name")
	}
	if !strings.Contains(prompt, "Nightly rotation") {
		t.Error("prompt should contain signature body excerpt")
	}
}
