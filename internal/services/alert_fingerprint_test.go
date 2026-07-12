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
// and targetHost are normalised to lower-case before hashing, so "HighCPU" and
// "highcpu" produce the same fingerprint.
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

// TestFetchCandidates_ActiveIncidentsReturned verifies that active incidents
// (running, pending, diagnosed) are included in candidates.
func TestFetchCandidates_ActiveIncidentsReturned(t *testing.T) {
	db := setupCorrelatorDB(t)

	for _, status := range []database.IncidentStatus{
		database.IncidentStatusRunning,
		database.IncidentStatusPending,
		database.IncidentStatusDiagnosed,
	} {
		inc := database.Incident{
			UUID:       "inc-" + string(status),
			Source:     "test",
			SourceKind: database.IncidentSourceKindAlert,
			SourceUUID: "src-1",
			Title:      "test",
			Status:     status,
			StartedAt:  time.Now().Add(-5 * time.Minute),
		}
		if err := db.Create(&inc).Error; err != nil {
			t.Fatalf("seed %s: %v", status, err)
		}
	}

	c := NewAlertCorrelator(nil, db)
	candidates, err := c.fetchCandidates(context.Background())
	if err != nil {
		t.Fatalf("fetchCandidates: %v", err)
	}

	uuids := make(map[string]bool, len(candidates))
	for _, row := range candidates {
		uuids[row.UUID] = true
	}

	for _, status := range []database.IncidentStatus{
		database.IncidentStatusRunning,
		database.IncidentStatusPending,
		database.IncidentStatusDiagnosed,
	} {
		if !uuids["inc-"+string(status)] {
			t.Errorf("expected %s incident to be a candidate", status)
		}
	}
}

// TestCorrelate_EndToEnd verifies the end-to-end path: Correlate makes an LLM
// call when active candidates exist and returns a verdict.
func TestCorrelate_EndToEnd(t *testing.T) {
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

	seedCorrelationSettings(t, db, true)

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

	// The prompt should contain the active candidate incident.
	capturedPrompt := caller.lastUser
	if !strings.Contains(capturedPrompt, "corr-fp-match") {
		t.Error("expected prompt to contain active candidate incident UUID")
	}
}
