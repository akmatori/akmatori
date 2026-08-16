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
// call when active candidates exist and returns a verdict. The seeded
// candidate deliberately carries a DIFFERENT fingerprint from the incoming
// alert (different host), because an exact fingerprint match is handled by the
// deterministic fast path and never reaches the LLM — see
// TestCorrelate_FingerprintFastPath.
func TestCorrelate_EndToEnd(t *testing.T) {
	db := setupCorrelatorDB(t)

	otherFP := ComputeAlertFingerprint("src-1", "CPUHigh", "web02")

	match := database.Incident{
		UUID:             "corr-fp-match",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "CPU high",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: otherFP,
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

// TestCorrelate_FingerprintFastPath verifies that an exact fingerprint match
// against a live incident is linked deterministically, with no LLM call.
func TestCorrelate_FingerprintFastPath(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")
	if err := db.Create(&database.Incident{
		UUID:             "fastpath-target",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "CPU high",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: fp,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		t.Error("fast path must not call the LLM")
		return "", nil
	}

	c := NewAlertCorrelator(caller, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "CPUHigh",
		TargetHost: "web01",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}

	if !verdict.Correlated || verdict.IncidentUUID != "fastpath-target" {
		t.Fatalf("verdict = %+v, want correlated match on fastpath-target", verdict)
	}
	if !verdict.IsConfident(c.Threshold()) {
		t.Errorf("fast path verdict must clear the link threshold, got confidence %v", verdict.Confidence)
	}
	if caller.lastUser != "" {
		t.Errorf("expected no LLM prompt, got %q", caller.lastUser)
	}
}

// TestCorrelate_FingerprintFastPath_NoLLMConfigured verifies the fast path
// still deduplicates when there is no LLM caller at all. Deterministic
// correlation must not depend on an AI dependency being available.
func TestCorrelate_FingerprintFastPath_NoLLMConfigured(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "DiskFull", "db01")
	if err := db.Create(&database.Incident{
		UUID:             "fastpath-nollm",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-2 * time.Minute),
		AlertFingerprint: fp,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	seedCorrelationSettings(t, db, true)

	c := NewAlertCorrelator(nil, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "DiskFull",
		TargetHost: "db01",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if verdict.IncidentUUID != "fastpath-nollm" {
		t.Errorf("IncidentUUID = %q, want fastpath-nollm", verdict.IncidentUUID)
	}
}

// TestCorrelate_FingerprintFastPath_Skips verifies the cases where an exact
// fingerprint match must NOT short-circuit: a disabled gate, an incident that
// is no longer a live target, and one whose activity predates the fast-path
// window.
func TestCorrelate_FingerprintFastPath_Skips(t *testing.T) {
	fp := ComputeAlertFingerprint("src-1", "CPUHigh", "web01")

	tests := []struct {
		name    string
		enabled bool
		seed    database.Incident
	}{
		{
			name:    "gate disabled",
			enabled: false,
			seed: database.Incident{
				Status:    database.IncidentStatusRunning,
				StartedAt: time.Now().Add(-5 * time.Minute),
			},
		},
		{
			name:    "closed incident is not a live target",
			enabled: true,
			seed: database.Incident{
				Status:    database.IncidentStatusClosed,
				StartedAt: time.Now().Add(-5 * time.Minute),
			},
		},
		{
			name:    "activity older than the fast-path window",
			enabled: true,
			seed: database.Incident{
				Status:    database.IncidentStatusRunning,
				StartedAt: time.Now().Add(-fingerprintFastPathWindow - time.Hour),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupCorrelatorDB(t)

			inc := tc.seed
			inc.UUID = "skip-target"
			inc.Source = "test"
			inc.SourceKind = database.IncidentSourceKindAlert
			inc.SourceUUID = "src-1"
			inc.AlertFingerprint = fp
			if err := db.Create(&inc).Error; err != nil {
				t.Fatalf("seed: %v", err)
			}

			seedCorrelationSettings(t, db, tc.enabled)

			c := NewAlertCorrelator(nil, db)
			verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
				AlertName:  "CPUHigh",
				TargetHost: "web01",
			})
			if err != nil {
				t.Fatalf("Correlate: %v", err)
			}
			if verdict.Correlated {
				t.Errorf("expected no fast-path match, got %+v", verdict)
			}
		})
	}
}

// TestCorrelate_FingerprintFastPath_MatchesLinkedAlert verifies the fast path
// follows a decision the LLM already made. Incident A was spawned by a
// different alert, then alert Y was linked to it; a later fire of Y must
// deterministically follow that link instead of being re-judged by the LLM,
// which might answer differently each time.
func TestCorrelate_FingerprintFastPath_MatchesLinkedAlert(t *testing.T) {
	db := setupCorrelatorDB(t)

	// The incident's own fingerprint belongs to a DIFFERENT alert.
	spawnFP := ComputeAlertFingerprint("src-1", "SpawningAlert", "web01")
	if err := db.Create(&database.Incident{
		UUID:             "linked-target",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-30 * time.Minute),
		AlertFingerprint: spawnFP,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	// A second alert the LLM previously linked to that incident.
	linkedFP := ComputeAlertFingerprint("src-1", "LinkedAlert", "web01")
	if err := db.Create(&database.Alert{
		UUID:              "linked-alert",
		IncidentUUID:      "linked-target",
		Status:            database.AlertStatusFiring,
		Fingerprint:       linkedFP,
		SourceUUID:        "src-1",
		SourceFingerprint: "sfp-linked-1",
		AlertName:         "LinkedAlert",
		TargetHost:        "web01",
		FiredAt:           time.Now().Add(-20 * time.Minute),
		Correlated:        true,
	}).Error; err != nil {
		t.Fatalf("seed linked alert: %v", err)
	}

	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		t.Error("a fingerprint already linked to this incident must not reach the LLM")
		return "", nil
	}

	c := NewAlertCorrelator(caller, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "LinkedAlert",
		TargetHost: "web01",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if !verdict.Correlated || verdict.IncidentUUID != "linked-target" {
		t.Fatalf("verdict = %+v, want a match on linked-target", verdict)
	}
}

// TestCorrelate_FingerprintFastPath_ResolvedLinkedAlert verifies a resolved
// alert still anchors the fingerprint. A monitor-status incident has resolved
// alerts by definition, and catching its recurrence is the entire point of
// monitor mode — restricting the match to firing alerts would break it.
func TestCorrelate_FingerprintFastPath_ResolvedLinkedAlert(t *testing.T) {
	db := setupCorrelatorDB(t)

	monitorUntil := time.Now().Add(30 * time.Minute)
	if err := db.Create(&database.Incident{
		UUID:         "monitor-target",
		Source:       "test",
		SourceKind:   database.IncidentSourceKindAlert,
		SourceUUID:   "src-1",
		Status:       database.IncidentStatusMonitor,
		StartedAt:    time.Now().Add(-30 * time.Minute),
		MonitorUntil: &monitorUntil,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	resolvedAt := time.Now().Add(-10 * time.Minute)
	if err := db.Create(&database.Alert{
		UUID:              "resolved-alert",
		IncidentUUID:      "monitor-target",
		Status:            database.AlertStatusResolved,
		Fingerprint:       ComputeAlertFingerprint("src-1", "FlappyAlert", "web01"),
		SourceUUID:        "src-1",
		SourceFingerprint: "sfp-resolved-1",
		AlertName:         "FlappyAlert",
		TargetHost:        "web01",
		FiredAt:           time.Now().Add(-25 * time.Minute),
		ResolvedAt:        &resolvedAt,
	}).Error; err != nil {
		t.Fatalf("seed resolved alert: %v", err)
	}

	seedCorrelationSettings(t, db, true)

	c := NewAlertCorrelator(nil, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "FlappyAlert",
		TargetHost: "web01",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if verdict.IncidentUUID != "monitor-target" {
		t.Errorf("IncidentUUID = %q, want monitor-target", verdict.IncidentUUID)
	}
}

// TestCorrelate_NoTargetHost_SkipsFastPath verifies a host-less alert is sent
// to the LLM instead. Its fingerprint is only hash(source, alertName, ""), so
// it would otherwise collapse fleet-wide onto whichever incident fired first.
func TestCorrelate_NoTargetHost_SkipsFastPath(t *testing.T) {
	db := setupCorrelatorDB(t)

	fp := ComputeAlertFingerprint("src-1", "Balancer has more than 25 POPs disabled", "")
	if err := db.Create(&database.Incident{
		UUID:             "hostless-target",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		SourceUUID:       "src-1",
		Title:            "balancer",
		Status:           database.IncidentStatusRunning,
		StartedAt:        time.Now().Add(-5 * time.Minute),
		AlertFingerprint: fp,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	seedCorrelationSettings(t, db, true)

	caller := &fakeOneShotLLMCaller{}
	caller.respond = func(_ context.Context) (string, error) {
		return `{"correlated":false,"incident_uuid":"","confidence":0.2,"reasoning":"different balancer"}`, nil
	}

	c := NewAlertCorrelator(caller, db)
	verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
		AlertName:  "Balancer has more than 25 POPs disabled",
		TargetHost: "",
	})
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if verdict.Correlated {
		t.Errorf("host-less alert must not fast-path, got %+v", verdict)
	}
	if caller.lastUser == "" {
		t.Error("expected the host-less alert to reach the LLM")
	}
}

// TestCorrelate_FingerprintFastPath_WindowBoundary pins the 48h window against
// the daily re-fire cadence that motivated it: a recurrence just under 48h
// still collapses, one past it starts a new incident.
func TestCorrelate_FingerprintFastPath_WindowBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lastFire    time.Duration
		wantMatched bool
	}{
		{name: "daily cadence, one day old", lastFire: 24*time.Hour + time.Minute, wantMatched: true},
		{name: "just inside the window", lastFire: 47 * time.Hour, wantMatched: true},
		{name: "just outside the window", lastFire: 49 * time.Hour, wantMatched: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupCorrelatorDB(t)

			firedAt := time.Now().Add(-tc.lastFire)
			if err := db.Create(&database.Incident{
				UUID:             "window-target",
				Source:           "test",
				SourceKind:       database.IncidentSourceKindAlert,
				SourceUUID:       "src-1",
				Status:           database.IncidentStatusCompleted,
				StartedAt:        firedAt,
				CompletedAt:      &firedAt,
				AlertFingerprint: ComputeAlertFingerprint("src-1", "DailyCheck", "web01"),
			}).Error; err != nil {
				t.Fatalf("seed incident: %v", err)
			}
			// A still-firing alert keeps the completed incident a live target.
			if err := db.Create(&database.Alert{
				UUID:              "window-alert",
				IncidentUUID:      "window-target",
				Status:            database.AlertStatusFiring,
				Fingerprint:       ComputeAlertFingerprint("src-1", "DailyCheck", "web01"),
				SourceUUID:        "src-1",
				SourceFingerprint: "sfp-window-1",
				AlertName:         "DailyCheck",
				TargetHost:        "web01",
				FiredAt:           firedAt,
			}).Error; err != nil {
				t.Fatalf("seed alert: %v", err)
			}

			seedCorrelationSettings(t, db, true)

			c := NewAlertCorrelator(nil, db)
			verdict, err := c.Correlate(context.Background(), "src-1", alerts.NormalizedAlert{
				AlertName:  "DailyCheck",
				TargetHost: "web01",
			})
			if err != nil {
				t.Fatalf("Correlate: %v", err)
			}
			if verdict.Correlated != tc.wantMatched {
				t.Errorf("Correlated = %v, want %v (last activity %v ago)",
					verdict.Correlated, tc.wantMatched, tc.lastFire)
			}
		})
	}
}
