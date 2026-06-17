package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// setupRecurrenceStatsDB opens an isolated in-memory DB with all tables
// needed by handleRecurrenceStats.
func setupRecurrenceStatsDB(t *testing.T) {
	t.Helper()
	testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{},
		&database.AlertCorrelationLog{},
		&database.AlertSuppressionLog{},
		&database.Memory{},
	)
}

// TestHandleRecurrenceStats_Empty returns zeros and empty slices when the DB
// is empty.
func TestHandleRecurrenceStats_Empty(t *testing.T) {
	setupRecurrenceStatsDB(t)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RecurrenceStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.FingerprintGroups) != 0 {
		t.Errorf("expected empty fingerprint groups, got %d", len(resp.FingerprintGroups))
	}
	if resp.GateHitRates.Correlation24h.Hits != 0 {
		t.Errorf("expected 0 correlation hits, got %d", resp.GateHitRates.Correlation24h.Hits)
	}
	if resp.RedundancyRate24h != 0 {
		t.Errorf("expected 0 redundancy rate, got %f", resp.RedundancyRate24h)
	}
}

// TestHandleRecurrenceStats_MethodNotAllowed rejects non-GET requests.
func TestHandleRecurrenceStats_MethodNotAllowed(t *testing.T) {
	setupRecurrenceStatsDB(t)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// TestHandleRecurrenceStats_GateHitRates verifies correlation and suppression
// log counts are aggregated correctly for 24h and 7d windows.
func TestHandleRecurrenceStats_GateHitRates(t *testing.T) {
	setupRecurrenceStatsDB(t)

	now := time.Now()

	// Seed one incident as the correlation target.
	inc := database.Incident{
		UUID:       "inc-corr-1",
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		Status:     database.IncidentStatusCompleted,
		AlertFingerprint: "abc123",
		StartedAt:  now.Add(-2 * time.Hour),
	}
	if err := database.DB.Create(&inc).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	// Two correlation log rows (one recent, one older).
	corr1 := database.AlertCorrelationLog{
		SourceUUID:          "src-1",
		AlertName:           "CPUHigh",
		TargetHost:          "web01",
		MatchedIncidentUUID: "inc-corr-1",
		Confidence:          0.9,
		CreatedAt:           now.Add(-1 * time.Hour), // within 24h
	}
	corr2 := database.AlertCorrelationLog{
		SourceUUID:          "src-1",
		AlertName:           "CPUHigh",
		TargetHost:          "web01",
		MatchedIncidentUUID: "inc-corr-1",
		Confidence:          0.85,
		CreatedAt:           now.Add(-5 * 24 * time.Hour), // within 7d but not 24h
	}
	if err := database.DB.Create(&corr1).Error; err != nil {
		t.Fatalf("seed corr1: %v", err)
	}
	if err := database.DB.Create(&corr2).Error; err != nil {
		t.Fatalf("seed corr2: %v", err)
	}

	// One suppression log row within 24h.
	supp1 := database.AlertSuppressionLog{
		SourceUUID:    "src-2",
		AlertName:     "DiskFull",
		TargetHost:    "db01",
		IncidentUUID:  "inc-supp-1",
		SignatureName: "disk-full-nightly",
		Confidence:    0.95,
		CreatedAt:     now.Add(-30 * time.Minute),
	}
	if err := database.DB.Create(&supp1).Error; err != nil {
		t.Fatalf("seed supp1: %v", err)
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RecurrenceStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.GateHitRates.Correlation24h.Hits != 1 {
		t.Errorf("correlation 24h hits: want 1, got %d", resp.GateHitRates.Correlation24h.Hits)
	}
	if resp.GateHitRates.Correlation7d.Hits != 2 {
		t.Errorf("correlation 7d hits: want 2, got %d", resp.GateHitRates.Correlation7d.Hits)
	}
	if resp.GateHitRates.Suppression24h.Hits != 1 {
		t.Errorf("suppression 24h hits: want 1, got %d", resp.GateHitRates.Suppression24h.Hits)
	}
	if resp.GateHitRates.Suppression7d.Hits != 1 {
		t.Errorf("suppression 7d hits: want 1, got %d", resp.GateHitRates.Suppression7d.Hits)
	}
}

// TestHandleRecurrenceStats_FingerprintGroups verifies the top-N fingerprint
// aggregation from correlation logs joined with incidents.
func TestHandleRecurrenceStats_FingerprintGroups(t *testing.T) {
	setupRecurrenceStatsDB(t)

	now := time.Now()

	// Two incidents with distinct fingerprints.
	inc1 := database.Incident{
		UUID:             "inc-fp-1",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		Status:           database.IncidentStatusRunning,
		AlertFingerprint: "fp111",
		StartedAt:        now.Add(-48 * time.Hour),
	}
	inc2 := database.Incident{
		UUID:             "inc-fp-2",
		Source:           "test",
		SourceKind:       database.IncidentSourceKindAlert,
		Status:           database.IncidentStatusRunning,
		AlertFingerprint: "fp222",
		StartedAt:        now.Add(-48 * time.Hour),
	}
	if err := database.DB.Create(&inc1).Error; err != nil {
		t.Fatalf("seed inc1: %v", err)
	}
	if err := database.DB.Create(&inc2).Error; err != nil {
		t.Fatalf("seed inc2: %v", err)
	}

	// Three correlation events for fp111, one for fp222 (all within 7d).
	for i := 0; i < 3; i++ {
		cl := database.AlertCorrelationLog{
			SourceUUID:          "src-a",
			AlertName:           "MemHigh",
			TargetHost:          "app01",
			MatchedIncidentUUID: "inc-fp-1",
			Confidence:          0.9,
			CreatedAt:           now.Add(-time.Duration(i+1) * time.Hour),
		}
		if err := database.DB.Create(&cl).Error; err != nil {
			t.Fatalf("seed corr log %d: %v", i, err)
		}
	}
	cl2 := database.AlertCorrelationLog{
		SourceUUID:          "src-b",
		AlertName:           "DiskLow",
		TargetHost:          "db01",
		MatchedIncidentUUID: "inc-fp-2",
		Confidence:          0.8,
		CreatedAt:           now.Add(-2 * time.Hour),
	}
	if err := database.DB.Create(&cl2).Error; err != nil {
		t.Fatalf("seed corr log 2: %v", err)
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RecurrenceStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.FingerprintGroups) != 2 {
		t.Fatalf("expected 2 fingerprint groups, got %d", len(resp.FingerprintGroups))
	}
	// Groups sorted descending by recurrence_count.
	top := resp.FingerprintGroups[0]
	if top.RecurrenceCount != 3 {
		t.Errorf("top group recurrence_count: want 3, got %d", top.RecurrenceCount)
	}
	if top.Fingerprint != "fp111" {
		t.Errorf("top group fingerprint: want fp111, got %q", top.Fingerprint)
	}
	const tokensPerRun = 412000
	if top.EstTokensSaved != 3*tokensPerRun {
		t.Errorf("est_tokens_saved: want %d, got %d", 3*tokensPerRun, top.EstTokensSaved)
	}
	second := resp.FingerprintGroups[1]
	if second.RecurrenceCount != 1 {
		t.Errorf("second group recurrence_count: want 1, got %d", second.RecurrenceCount)
	}
}

// TestHandleRecurrenceStats_CandidateSignatures verifies that the candidate
// list includes non-suppressed incident_pattern/feedback memories from the last
// 7d and excludes suppress=true memories.
func TestHandleRecurrenceStats_CandidateSignatures(t *testing.T) {
	setupRecurrenceStatsDB(t)

	now := time.Now()

	suppTrue := true

	// suppress=true memory — must NOT appear in candidates.
	m1 := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        "disk-fp",
		Description: "disk false positive",
		Body:        "suppress: true",
		Suppress:    true,
		CreatedAt:   now.Add(-1 * time.Hour),
	}
	// suppress=false incident_pattern — must appear.
	m2 := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        "cpu-candidate",
		Description: "cpu pattern",
		Body:        "some body",
		Suppress:    false,
		CreatedAt:   now.Add(-2 * time.Hour),
	}
	// feedback type without suppress — must appear.
	m3 := database.Memory{
		Scope:       "global",
		Type:        "feedback",
		Name:        "false-positive-feedback",
		Description: "fp feedback",
		Body:        "this alert is a noop",
		Suppress:    false,
		CreatedAt:   now.Add(-3 * time.Hour),
	}
	// older than 7d — must NOT appear.
	m4 := database.Memory{
		Scope:       "global",
		Type:        "incident_pattern",
		Name:        "old-pattern",
		Description: "old",
		Body:        "old",
		Suppress:    false,
		CreatedAt:   now.Add(-8 * 24 * time.Hour),
	}
	// host type — must NOT appear (wrong type).
	m5 := database.Memory{
		Scope:       "global",
		Type:        "host",
		Name:        "host-pattern",
		Description: "host",
		Body:        "host body",
		Suppress:    false,
		CreatedAt:   now.Add(-1 * time.Hour),
	}
	_ = suppTrue

	for i, m := range []*database.Memory{&m1, &m2, &m3, &m4, &m5} {
		if err := database.DB.Create(m).Error; err != nil {
			t.Fatalf("seed memory %d: %v", i, err)
		}
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RecurrenceStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Expect m2 (incident_pattern, no suppress) and m3 (feedback, no suppress).
	if len(resp.CandidateSignatures) != 2 {
		t.Errorf("expected 2 candidate signatures, got %d", len(resp.CandidateSignatures))
		for _, c := range resp.CandidateSignatures {
			t.Logf("  candidate: name=%q type=%q suppress=%v", c.Name, c.Type, c.Suppress)
		}
	}
	for _, c := range resp.CandidateSignatures {
		if c.Suppress {
			t.Errorf("candidate %q has suppress=true, should be excluded", c.Name)
		}
		if c.Type != "incident_pattern" && c.Type != "feedback" {
			t.Errorf("candidate %q has unexpected type %q", c.Name, c.Type)
		}
	}
}

// TestHandleRecurrenceStats_RedundancyRate verifies the redundancy rate
// calculation: sum(correlated_count) / count(alert incidents) in the last 24h.
func TestHandleRecurrenceStats_RedundancyRate(t *testing.T) {
	setupRecurrenceStatsDB(t)

	now := time.Now()

	// 5 alert incidents in last 24h; 3 of them have correlated_count > 0.
	incidents := []database.Incident{
		{UUID: "r1", Source: "t", SourceKind: database.IncidentSourceKindAlert, Status: "completed", CorrelatedCount: 4, StartedAt: now.Add(-1 * time.Hour)},
		{UUID: "r2", Source: "t", SourceKind: database.IncidentSourceKindAlert, Status: "completed", CorrelatedCount: 2, StartedAt: now.Add(-2 * time.Hour)},
		{UUID: "r3", Source: "t", SourceKind: database.IncidentSourceKindAlert, Status: "completed", CorrelatedCount: 0, StartedAt: now.Add(-3 * time.Hour)},
		{UUID: "r4", Source: "t", SourceKind: database.IncidentSourceKindAlert, Status: "completed", CorrelatedCount: 0, StartedAt: now.Add(-4 * time.Hour)},
		{UUID: "r5", Source: "t", SourceKind: database.IncidentSourceKindAlert, Status: "completed", CorrelatedCount: 0, StartedAt: now.Add(-5 * time.Hour)},
	}
	for _, inc := range incidents {
		if err := database.DB.Create(&inc).Error; err != nil {
			t.Fatalf("seed incident %s: %v", inc.UUID, err)
		}
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stats/recurrence", nil)
	rec := httptest.NewRecorder()
	h.handleRecurrenceStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RecurrenceStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// sum(correlated_count)=6, count=5 → rate=1.2 (can exceed 1 since correlated_count
	// tracks events collapsed into an incident, not a per-incident flag)
	want := 6.0 / 5.0
	if resp.RedundancyRate24h != want {
		t.Errorf("redundancy_rate_24h: want %f, got %f", want, resp.RedundancyRate24h)
	}

	// Warning condition: rate > 0.2 means badge should show.
	if resp.RedundancyRate24h <= 0.2 {
		t.Errorf("redundancy_rate_24h %f should be > 0.2 to trigger warning badge", resp.RedundancyRate24h)
	}
}
