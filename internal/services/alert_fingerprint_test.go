package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// TestComputeAlertFingerprint_StableAcrossCaseVariants verifies that alertName
// and targetHost are normalised to lower-case before hashing, so "TikTok" and
// "tiktok" produce the same fingerprint.
func TestComputeAlertFingerprint_StableAcrossCaseVariants(t *testing.T) {
	fp1 := ComputeAlertFingerprint("src-1", "HighCPU", "web01")
	fp2 := ComputeAlertFingerprint("src-1", "highcpu", "WEB01")
	fp3 := ComputeAlertFingerprint("src-1", "HIGHCPU", "Web01")

	if fp1 != fp2 || fp1 != fp3 {
		t.Errorf("fingerprints differ across case variants: %q %q %q", fp1, fp2, fp3)
	}
}

// TestComputeAlertFingerprint_DifferentSourceFingerprint verifies that two
// alerts with different SourceFingerprints (label set) but the same
// sourceUUID+alertName+targetHost produce identical alert fingerprints.
func TestComputeAlertFingerprint_DifferentSourceFingerprint(t *testing.T) {
	// Two Alertmanager alerts with different label sets (fingerprints) but
	// same rule + host → same alert identity fingerprint.
	fp1 := ComputeAlertFingerprint("src-uuid-1", "DiskFull", "db01")
	fp2 := ComputeAlertFingerprint("src-uuid-1", "DiskFull", "db01")
	if fp1 != fp2 {
		t.Errorf("expected identical fingerprints, got %q and %q", fp1, fp2)
	}
}

// TestComputeAlertFingerprint_DifferentHost verifies distinct hosts produce
// different fingerprints.
func TestComputeAlertFingerprint_DifferentHost(t *testing.T) {
	fp1 := ComputeAlertFingerprint("src-1", "DiskFull", "db01")
	fp2 := ComputeAlertFingerprint("src-1", "DiskFull", "db02")
	if fp1 == fp2 {
		t.Error("expected different fingerprints for different hosts")
	}
}

// TestComputeAlertFingerprint_DifferentSource verifies different sources
// produce different fingerprints.
func TestComputeAlertFingerprint_DifferentSource(t *testing.T) {
	fp1 := ComputeAlertFingerprint("src-1", "DiskFull", "db01")
	fp2 := ComputeAlertFingerprint("src-2", "DiskFull", "db01")
	if fp1 == fp2 {
		t.Error("expected different fingerprints for different sources")
	}
}

// TestComputeAlertFingerprint_Length verifies the fingerprint is exactly 32 chars.
func TestComputeAlertFingerprint_Length(t *testing.T) {
	fp := ComputeAlertFingerprint("src-uuid", "AlertName", "host01")
	if len(fp) != 32 {
		t.Errorf("expected 32 chars, got %d: %q", len(fp), fp)
	}
	for _, c := range fp {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("fingerprint contains non-hex char %q", c)
		}
	}
}

// TestFetchCandidates_FingerprintFilter verifies that fetchCandidates returns
// only incidents whose alert_fingerprint matches the incoming fingerprint OR
// whose fingerprint is empty (legacy rows).
func TestFetchCandidates_FingerprintFilter(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")

	// Seed an incident with the matching fingerprint.
	match := database.Incident{
		UUID:             "fp-match",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "CPU high",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: fp,
	}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("seed match: %v", err)
	}

	// Seed an incident with a different fingerprint.
	noMatch := database.Incident{
		UUID:             "fp-nomatch",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "Disk full",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: ComputeAlertFingerprint("src-1", "DiskFull", "db01"),
	}
	if err := db.Create(&noMatch).Error; err != nil {
		t.Fatalf("seed nomatch: %v", err)
	}

	// Seed a legacy incident with no fingerprint (empty string).
	legacy := database.Incident{
		UUID:             "fp-legacy",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "Legacy",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: "",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	c := NewAlertCorrelator(nil, db)

	candidates, _, err := c.fetchCandidates(context.Background(), fp, 30*time.Minute, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	uuids := make(map[string]bool, len(candidates))
	for _, row := range candidates {
		uuids[row.UUID] = true
	}

	if !uuids["fp-match"] {
		t.Error("expected matching fingerprint incident to be returned")
	}
	if !uuids["fp-legacy"] {
		t.Error("expected legacy (empty fingerprint) incident to be returned")
	}
	if uuids["fp-nomatch"] {
		t.Error("expected non-matching fingerprint incident to be excluded")
	}
}

// TestFetchCandidates_EmptyFingerprintPassthrough verifies that when the
// incoming fingerprint is empty (edge case), all candidates are returned
// (no filter applied).
func TestFetchCandidates_EmptyFingerprintPassthrough(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")

	inc := database.Incident{
		UUID:             "fp-any",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "Any",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: fp,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	c := NewAlertCorrelator(nil, db)

	// Pass empty fingerprint — all qualifying candidates should be returned.
	candidates, _, err := c.fetchCandidates(context.Background(), "", 30*time.Minute, 7, 20)
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Error("expected at least one candidate when fingerprint is empty")
	}
}

// TestCorrelate_UsesFingerprint verifies the end-to-end path: Correlate
// computes the fingerprint internally and uses it to filter candidates, so the
// LLM is only given same-identity incidents.
func TestCorrelate_UsesFingerprint(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")

	match := database.Incident{
		UUID:             "corr-fp-match",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "CPU high",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: fp,
		Response:         "investigating",
	}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("seed match: %v", err)
	}

	irrelevant := database.Incident{
		UUID:             "corr-fp-other",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "Disk full",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: ComputeAlertFingerprint("src-1", "DiskFull", "db01"),
		Response:         "disk investigation",
	}
	if err := db.Create(&irrelevant).Error; err != nil {
		t.Fatalf("seed irrelevant: %v", err)
	}

	seedCorrelationSettings(t, db, true, 30, 20, 0.7)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":false,"incident_uuid":"","confidence":0.1,"reasoning":"captured"}`, nil
	}

	c := NewAlertCorrelator(caller, db)

	_, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "CPUHigh",
		TargetHost: "web01",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}

	// The user prompt sent to the LLM should include the matching incident but
	// NOT the disk-full one.
	capturedPrompt := caller.lastUser
	if !strings.Contains(capturedPrompt, "corr-fp-match") {
		t.Error("expected prompt to contain matching fingerprint incident UUID")
	}
	if strings.Contains(capturedPrompt, "corr-fp-other") {
		t.Error("expected prompt to exclude non-matching fingerprint incident UUID")
	}
}
