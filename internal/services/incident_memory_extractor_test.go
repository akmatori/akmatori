package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// extractorTestDB opens an in-memory database with every table the
// extractor reads and writes, assigns it to database.DB (the gate and the
// LLM settings read the global), and seeds active LLM settings.
func extractorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&database.Incident{}, &database.AgentRun{}, &database.Alert{}, &database.Memory{},
		&database.Skill{}, &database.GeneralSettings{}, &database.LLMSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.LLMSettings{Name: "t", Provider: database.LLMProviderAnthropic, APIKey: "k", Model: "m", Active: true, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	orig := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = orig })
	return db
}

// newTestExtractor returns an extractor over db with the fake caller and a
// MemoryService on the same database (no disk mirror).
func newTestExtractor(t *testing.T, db *gorm.DB, caller OneShotLLMCaller) (*IncidentMemoryExtractor, *MemoryService) {
	t.Helper()
	mem := &MemoryService{db: db, memoryDir: t.TempDir()}
	e := NewIncidentMemoryExtractor(db, caller, mem, nil)
	return e, mem
}

// seedClosedIncident inserts a closed alert incident queued for extraction,
// with one resolved alert and one completed run whose answer is response.
func seedClosedIncident(t *testing.T, db *gorm.DB, uuid, response string, resolvedBy string) {
	t.Helper()
	started := time.Now().Add(-2 * time.Hour)
	resolved := time.Now().Add(-time.Hour)
	if err := db.Create(&database.Incident{
		UUID: uuid, Source: "zabbix", SourceKind: database.IncidentSourceKindAlert, SourceUUID: "src",
		Title: "ingest gap on edge-01", Status: database.IncidentStatusClosed, StartedAt: started, ResolvedAt: &resolved,
		ClosedReason: database.CloseReasonMonitorExpired, MemoryExtraction: database.MemoryExtractionPending,
		Context: database.JSONB{"alert_name": "IngestGap", "target_host": "edge-01", "summary": "no samples for 5m"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	fired := started.Add(2 * time.Minute)
	if err := db.Create(&database.Alert{UUID: "al-" + uuid, IncidentUUID: uuid, Status: database.AlertStatusResolved,
		AlertName: "IngestGap", TargetHost: "edge-01", FiredAt: fired, ResolvedAt: &resolved, ResolvedBy: resolvedBy,
		RawPayload: database.JSONB{"original_message": "PROBLEM: IngestGap on edge-01"}}).Error; err != nil {
		t.Fatal(err)
	}
	if response != "" {
		completed := started.Add(10 * time.Minute)
		if err := db.Create(&database.AgentRun{RunID: "run-" + uuid, ScopeUUID: uuid, IncidentUUID: uuid, SourceKind: "alert",
			Status: database.AgentRunStatusCompleted, Outcome: database.AgentRunOutcomeResolved, StartedAt: started.Add(time.Minute),
			CompletedAt: &completed, Response: response, FullLog: "tool: prometheus query ... load 95% at 10:05\n" + response,
			LastSkillUsed: "linux-admin"}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func loadInc(t *testing.T, db *gorm.DB, uuid string) database.Incident {
	t.Helper()
	var inc database.Incident
	if err := db.Where("uuid = ?", uuid).Take(&inc).Error; err != nil {
		t.Fatalf("load %s: %v", uuid, err)
	}
	return inc
}

// reportedCaseJSON is the model's answer for the reported case: the agent
// wrote "high load caused the data gap", the load rose after the gap.
const reportedCaseJSON = `{"timeline": [{"at": "2026-11-02T10:02:00Z", "what": "ingest gap starts", "source": "alert"}, {"at": "2026-11-02T10:05:00Z", "what": "load rises to 95%", "source": "tool: prometheus query"}],
 "memories": [
  {"scope": "linux-admin", "name": "edge-01-load-causes-ingest-gap", "type": "fact", "claim": "causal", "description": "High load on edge-01 caused the ingest gap", "body": "The agent concluded that load caused the gap on 2026-11-02.", "evidence_note": "load seen at 10:05, gap from 10:02", "cause_at": "2026-11-02T10:05:00Z", "effect_at": "2026-11-02T10:02:00Z"},
  {"scope": "nope", "name": "Edge 01 Timeline!", "type": "fact", "claim": "factual", "description": "IngestGap on edge-01 resolved by itself on 2026-11-02", "body": "Gap 10:02-10:12 UTC; nobody acted; the sweep closed the incident.", "evidence_note": "alert resolved_at 10:12 by sweep"}
 ]}`

// TestExtractor_ReportedCase: a self-resolved incident whose agent claimed a
// cause. The causal memory is stored unverified with the order check in its
// note; the factual timeline memory is observed; no observed causal row.
func TestExtractor_ReportedCase(t *testing.T) {
	db := extractorTestDB(t)
	db.Create(&database.Skill{Name: "linux-admin", Enabled: true})
	seedClosedIncident(t, db, "inc-1", "Root cause: high load caused the data gap. Status resolved.", database.AlertResolvedBySweep)
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return reportedCaseJSON, nil }}
	e, _ := newTestExtractor(t, db, caller)

	did, err := e.RunOnce(context.Background())
	if err != nil || !did {
		t.Fatalf("RunOnce: did=%v err=%v", did, err)
	}
	inc := loadInc(t, db, "inc-1")
	if inc.MemoryExtraction != database.MemoryExtractionSucceeded || inc.MemoryExtractionError != "" {
		t.Fatalf("status %q error %q", inc.MemoryExtraction, inc.MemoryExtractionError)
	}
	if !strings.Contains(inc.MemoryTimeline, "ingest gap starts") || inc.MemoryExtractionAttempts != 1 {
		t.Errorf("timeline/attempts: %+v", inc)
	}
	// The prompt carries the facts the pass needs.
	for _, want := range []string{"close reason: monitor_expired", "resolved", "by a sweep", "agent self-report: resolved", "ORIGINAL ALERT TEXT", "PROBLEM: IngestGap on edge-01", "prometheus query"} {
		if !strings.Contains(caller.lastUser, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	var mems []database.Memory
	db.Order("name").Find(&mems)
	if len(mems) != 2 {
		t.Fatalf("memories: %+v", mems)
	}
	causal, factual := mems[0], mems[1]
	if causal.Name != "edge-01-load-causes-ingest-gap" || causal.Scope != "linux-admin" {
		t.Errorf("causal identity: %+v", causal)
	}
	if causal.Claim != MemoryClaimCausal || causal.Evidence != MemoryEvidenceUnverified {
		t.Errorf("causal grade: claim %q evidence %q", causal.Claim, causal.Evidence)
	}
	if !strings.Contains(causal.EvidenceNote, "order check: cause 2026-11-02T10:05:00Z is NOT before effect 2026-11-02T10:02:00Z") {
		t.Errorf("order check note: %q", causal.EvidenceNote)
	}
	if causal.CreatedBy != MemoryCreatedByAgent || causal.IncidentUUID != "inc-1" {
		t.Errorf("provenance: %+v", causal)
	}
	// Unknown scope → global; a bad name is slugified.
	if factual.Scope != MemoryScopeGlobal || factual.Name != "edge-01-timeline" {
		t.Errorf("factual identity: %+v", factual)
	}
	if factual.Claim != MemoryClaimFactual || factual.Evidence != MemoryEvidenceObserved {
		t.Errorf("factual grade: claim %q evidence %q", factual.Claim, factual.Evidence)
	}
	for _, m := range mems {
		if m.Claim == MemoryClaimCausal && m.Evidence == MemoryEvidenceObserved {
			t.Errorf("observed causal row: %+v", m)
		}
	}
	// Nothing left to claim.
	if did, _ := e.RunOnce(context.Background()); did {
		t.Error("second RunOnce claimed a row")
	}
}

// TestExtractor_RerunUpdatesInPlace: a rerun (crash recovery or the
// endpoint) updates the same names, no suffixed duplicates; a name owned by
// an operator gets a suffix.
func TestExtractor_RerunUpdatesInPlace(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	db.Create(&database.Memory{Scope: "global", Name: "edge-01-nic", Type: "fact", Description: "operator's", Body: "keep", CreatedBy: MemoryCreatedByOperator, Evidence: MemoryEvidenceObserved})
	out := `{"timeline": [], "memories": [{"scope": "global", "name": "edge-01-nic", "type": "fact", "claim": "factual", "description": "v1", "body": "b1", "evidence_note": "n"}]}`
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return out, nil }}
	e, _ := newTestExtractor(t, db, caller)
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var m database.Memory
	db.Where("name = ?", "edge-01-nic-2").Take(&m)
	if m.Description != "v1" || m.IncidentUUID != "inc-1" {
		t.Fatalf("first pass: %+v", m)
	}

	// Crash mid-job: the row is running; startup re-queues it; the rerun
	// updates edge-01-nic-2 in place.
	db.Model(&database.Incident{}).Where("uuid = ?", "inc-1").Update("memory_extraction", database.MemoryExtractionRunning)
	if err := e.ResetInterrupted(); err != nil {
		t.Fatal(err)
	}
	if inc := loadInc(t, db, "inc-1"); inc.MemoryExtraction != database.MemoryExtractionPending {
		t.Fatalf("after reset: %q", inc.MemoryExtraction)
	}
	out = strings.Replace(out, `"description": "v1"`, `"description": "v2"`, 1)
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names []string
	db.Model(&database.Memory{}).Order("name").Pluck("name", &names)
	if strings.Join(names, ",") != "edge-01-nic,edge-01-nic-2" {
		t.Fatalf("names after rerun: %v", names)
	}
	db.Where("name = ?", "edge-01-nic-2").Take(&m)
	if m.Description != "v2" {
		t.Errorf("not updated in place: %+v", m)
	}
	var op database.Memory
	db.Where("name = ?", "edge-01-nic").Take(&op)
	if op.Body != "keep" || op.CreatedBy != MemoryCreatedByOperator {
		t.Errorf("operator row touched: %+v", op)
	}

	// The endpoint: closed + succeeded → pending again; pending → busy.
	if err := e.Rerun(context.Background(), "inc-1"); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if inc := loadInc(t, db, "inc-1"); inc.MemoryExtraction != database.MemoryExtractionPending || inc.MemoryExtractionAttempts != 0 {
		t.Errorf("after Rerun: %+v", inc)
	}
	if err := e.Rerun(context.Background(), "inc-1"); !errors.Is(err, ErrExtractionBusy) {
		t.Errorf("rerun of pending: %v", err)
	}
}

func TestExtractor_RerunMatrix(t *testing.T) {
	db := extractorTestDB(t)
	e, _ := newTestExtractor(t, db, nil)
	db.Create(&database.Incident{UUID: "open", SourceKind: "alert", Status: database.IncidentStatusMonitor})
	db.Create(&database.Incident{UUID: "merged", SourceKind: "alert", Status: database.IncidentStatusMerged, MemoryExtraction: database.MemoryExtractionSkipped})
	db.Create(&database.Incident{UUID: "running", SourceKind: "alert", Status: database.IncidentStatusClosed, MemoryExtraction: database.MemoryExtractionRunning})
	db.Create(&database.Incident{UUID: "old", SourceKind: "alert", Status: database.IncidentStatusClosed})
	for uuid, want := range map[string]error{"open": ErrExtractionNotClosed, "merged": ErrExtractionNotClosed, "running": ErrExtractionBusy, "missing": gorm.ErrRecordNotFound} {
		if err := e.Rerun(context.Background(), uuid); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", uuid, err, want)
		}
	}
	// A closed incident never queued (closed before the upgrade) is queued.
	if err := e.Rerun(context.Background(), "old"); err != nil {
		t.Errorf("old: %v", err)
	}
	if inc := loadInc(t, db, "old"); inc.MemoryExtraction != database.MemoryExtractionPending {
		t.Errorf("old not queued: %q", inc.MemoryExtraction)
	}
}

// TestExtractor_NoLLM_RetriesThenFails: no worker is a transient failure,
// retried with the backoff, then failed; the incident stays closed.
func TestExtractor_NoLLM_RetriesThenFails(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return "", ErrWorkerNotConnected }}
	e, _ := newTestExtractor(t, db, caller)
	now := time.Now()
	e.now = func() time.Time { return now }

	for attempt := 1; attempt <= extractionMaxAttempts; attempt++ {
		if did, _ := e.RunOnce(context.Background()); !did {
			t.Fatalf("attempt %d not claimed", attempt)
		}
		inc := loadInc(t, db, "inc-1")
		if inc.Status != database.IncidentStatusClosed || inc.MemoryExtractionAttempts != attempt {
			t.Fatalf("attempt %d: %+v", attempt, inc)
		}
		if attempt < extractionMaxAttempts {
			if inc.MemoryExtraction != database.MemoryExtractionPending || inc.MemoryExtractionRetryAt == nil {
				t.Fatalf("attempt %d not re-queued: %+v", attempt, inc)
			}
			wantRetry := now.Add(extractionBackoff[attempt-1])
			if d := inc.MemoryExtractionRetryAt.Sub(wantRetry); d < -time.Second || d > time.Second {
				t.Errorf("attempt %d retry_at = %v, want %v", attempt, inc.MemoryExtractionRetryAt, wantRetry)
			}
			// Not claimable before the retry time.
			if did, _ := e.RunOnce(context.Background()); did {
				t.Fatalf("attempt %d: claimed before retry_at", attempt)
			}
			now = now.Add(extractionBackoff[attempt-1] + time.Second)
		} else if inc.MemoryExtraction != database.MemoryExtractionFailed || !strings.Contains(inc.MemoryExtractionError, "not connected") {
			t.Fatalf("after the last attempt: %+v", inc)
		}
	}
	var n int64
	db.Model(&database.Memory{}).Count(&n)
	if n != 0 {
		t.Errorf("memories written without an LLM: %d", n)
	}
}

// TestExtractor_LeaseReclaim: a row running past the lease is claimed again;
// the stale job's late apply is a no-op.
func TestExtractor_LeaseReclaim(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	out := `{"timeline": [], "memories": [{"scope": "global", "name": "fresh", "type": "fact", "claim": "factual", "description": "d", "body": "b"}]}`
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return out, nil }}
	e, mem := newTestExtractor(t, db, caller)

	staleClaim := time.Now().Add(-extractionLease - time.Minute).Truncate(time.Microsecond)
	db.Model(&database.Incident{}).Where("uuid = ?", "inc-1").Updates(map[string]interface{}{
		"memory_extraction": database.MemoryExtractionRunning, "memory_extraction_at": staleClaim, "memory_extraction_attempts": 1,
	})
	if did, _ := e.RunOnce(context.Background()); !did {
		t.Fatal("stale running row not re-claimed")
	}
	if inc := loadInc(t, db, "inc-1"); inc.MemoryExtraction != database.MemoryExtractionSucceeded || inc.MemoryExtractionAttempts != 2 {
		t.Fatalf("after re-claim: %+v", inc)
	}
	// The stale job applies late: nothing happens.
	_, err := mem.ApplyExtraction(context.Background(), "inc-1", staleClaim, ExtractionResult{Memories: []database.Memory{
		{Scope: "global", Name: "stale", Type: "fact", Description: "d", Body: "b", Evidence: MemoryEvidenceObserved},
	}})
	if !errors.Is(err, ErrExtractionStale) {
		t.Fatalf("late apply: %v", err)
	}
	var n int64
	db.Model(&database.Memory{}).Where("name = ?", "stale").Count(&n)
	if n != 0 {
		t.Error("stale job wrote a memory")
	}

	// A running row inside the lease is not claimable.
	db.Model(&database.Incident{}).Where("uuid = ?", "inc-1").Updates(map[string]interface{}{
		"memory_extraction": database.MemoryExtractionRunning, "memory_extraction_at": time.Now(),
	})
	if did, _ := e.RunOnce(context.Background()); did {
		t.Error("live running row claimed")
	}
}

// TestExtractor_ReopenedAbortsApply: the incident reopens while the LLM
// runs; the apply sees the claim broken and writes nothing.
func TestExtractor_ReopenedAbortsApply(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) {
		db.Model(&database.Incident{}).Where("uuid = ?", "inc-1").Update("status", database.IncidentStatusRunning)
		return `{"timeline": [], "memories": [{"scope": "global", "name": "x", "type": "fact", "claim": "factual", "description": "d", "body": "b"}]}`, nil
	}}
	e, _ := newTestExtractor(t, db, caller)
	if did, _ := e.RunOnce(context.Background()); !did {
		t.Fatal("not claimed")
	}
	inc := loadInc(t, db, "inc-1")
	if inc.Status != database.IncidentStatusRunning || inc.MemoryExtraction != database.MemoryExtractionRunning {
		t.Errorf("row changed by the stale job: %+v", inc)
	}
	var n int64
	db.Model(&database.Memory{}).Count(&n)
	if n != 0 {
		t.Errorf("memories written for a reopened incident: %d", n)
	}
}

// TestExtractor_Outcomes: no answer → no_output; gate off → skipped; an
// empty list → no_output; a running run holds the claim.
func TestExtractor_Outcomes(t *testing.T) {
	db := extractorTestDB(t)
	e, _ := newTestExtractor(t, db, &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"timeline": [{"at": "2026-11-02T10:00:00Z", "what": "nothing durable", "source": "agent"}], "memories": []}`, nil
	}})

	seedClosedIncident(t, db, "inc-noanswer", "", database.AlertResolvedBySource)
	e.RunOnce(context.Background())
	if inc := loadInc(t, db, "inc-noanswer"); inc.MemoryExtraction != database.MemoryExtractionNoOutput {
		t.Errorf("no answer: %+v", inc)
	}

	seedClosedIncident(t, db, "inc-empty", "answer", database.AlertResolvedBySource)
	e.RunOnce(context.Background())
	if inc := loadInc(t, db, "inc-empty"); inc.MemoryExtraction != database.MemoryExtractionNoOutput || !strings.Contains(inc.MemoryTimeline, "nothing durable") {
		t.Errorf("empty list: %+v", inc)
	}

	seedClosedIncident(t, db, "inc-held", "answer", database.AlertResolvedBySource)
	db.Create(&database.AgentRun{RunID: "run-live", ScopeUUID: "inc-held", IncidentUUID: "inc-held", Status: database.AgentRunStatusRunning})
	if did, _ := e.RunOnce(context.Background()); did {
		t.Error("incident with a running run was claimed")
	}
	db.Model(&database.AgentRun{}).Where("run_id = ?", "run-live").Update("status", database.AgentRunStatusCompleted)

	// The gate row exists (an earlier read created it); turn it off.
	if err := db.Model(&database.GeneralSettings{}).Where("1 = 1").Update("memory_generation_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	e.RunOnce(context.Background())
	if inc := loadInc(t, db, "inc-held"); inc.MemoryExtraction != database.MemoryExtractionSkipped {
		t.Errorf("gate off: %+v", inc)
	}
}

// TestExtractor_MergeFamilyAndRedaction: the survivor's pass reads the
// merged child's answer; a token in the transcript never reaches the prompt,
// and a token in the output never reaches a memory.
func TestExtractor_MergeFamilyAndRedaction(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-surv", "Survivor answer with Bearer SECRETTOKEN12345 inside.", database.AlertResolvedBySource)
	db.Create(&database.Incident{UUID: "inc-child", SourceKind: "alert", Status: database.IncidentStatusMerged, MergedIntoUUID: "inc-surv", MemoryExtraction: database.MemoryExtractionSkipped})
	db.Create(&database.AgentRun{RunID: "run-child", ScopeUUID: "inc-child", IncidentUUID: "inc-child", Status: database.AgentRunStatusCompleted, Response: "CHILD-ANSWER: same bad deploy", FullLog: "password=childsecret99 in env"})
	out := `{"timeline": [], "memories": [{"scope": "global", "name": "deploy-check", "type": "fact", "claim": "factual", "description": "Deploy check needs token=abcdefgh1234", "body": "Use Authorization: Bearer SECRETTOKEN12345 against the API.", "evidence_note": "n"}]}`
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return out, nil }}
	e, _ := newTestExtractor(t, db, caller)
	if did, _ := e.RunOnce(context.Background()); !did {
		t.Fatal("not claimed")
	}
	if !strings.Contains(caller.lastUser, "CHILD-ANSWER") || !strings.Contains(caller.lastUser, "merged family: inc-child") {
		t.Errorf("child's work missing from the prompt")
	}
	for _, secret := range []string{"SECRETTOKEN12345", "childsecret99"} {
		if strings.Contains(caller.lastUser, secret) {
			t.Errorf("secret %q reached the prompt", secret)
		}
	}
	var m database.Memory
	db.Where("name = ?", "deploy-check").Take(&m)
	if strings.Contains(m.Body, "SECRETTOKEN12345") || strings.Contains(m.Description, "abcdefgh1234") {
		t.Errorf("secret stored: %+v", m)
	}
	if !strings.Contains(m.Body, RedactedSecret) {
		t.Errorf("no marker in body: %q", m.Body)
	}
	inc := loadInc(t, db, "inc-surv")
	if !strings.HasPrefix(inc.MemoryExtractionNote, "redacted ") {
		t.Errorf("note: %q", inc.MemoryExtractionNote)
	}
	// The child was skipped by the merge, not extracted on its own.
	if c := loadInc(t, db, "inc-child"); c.MemoryExtraction != database.MemoryExtractionSkipped {
		t.Errorf("child: %+v", c)
	}
}

// TestExtractor_InvalidJSONRetries: a bad answer is transient.
func TestExtractor_InvalidJSONRetries(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return "I cannot do that", nil }}
	e, _ := newTestExtractor(t, db, caller)
	e.RunOnce(context.Background())
	inc := loadInc(t, db, "inc-1")
	if inc.MemoryExtraction != database.MemoryExtractionPending || !strings.Contains(inc.MemoryExtractionError, "invalid json") {
		t.Errorf("%+v", inc)
	}
}

// TestApplyExtraction_PartialFailureRollsBack: a memory that cannot be
// written rolls the whole apply back, status included.
func TestApplyExtraction_PartialFailureRollsBack(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	claim := time.Now().Truncate(time.Microsecond)
	db.Model(&database.Incident{}).Where("uuid = ?", "inc-1").Updates(map[string]interface{}{
		"memory_extraction": database.MemoryExtractionRunning, "memory_extraction_at": claim,
	})
	mem := &MemoryService{db: db, memoryDir: t.TempDir()}
	long := strings.Repeat("x", MemoryNameMaxLen)
	for i := 2; i <= memoryUniqueNameTries; i++ {
		db.Create(&database.Memory{Scope: "global", Name: fmt.Sprintf("%s-%d", strings.TrimRight(long[:MemoryNameMaxLen-len(fmt.Sprintf("-%d", i))], "-"), i), Type: "fact", Description: "d", Body: "b", CreatedBy: MemoryCreatedByOperator})
	}
	db.Create(&database.Memory{Scope: "global", Name: long, Type: "fact", Description: "d", Body: "b", CreatedBy: MemoryCreatedByOperator})
	_, err := mem.ApplyExtraction(context.Background(), "inc-1", claim, ExtractionResult{Memories: []database.Memory{
		{Scope: "global", Name: "good-one", Type: "fact", Description: "d", Body: "b", Evidence: MemoryEvidenceObserved},
		{Scope: "global", Name: long, Type: "fact", Description: "d", Body: "b", Evidence: MemoryEvidenceObserved},
	}})
	if err == nil || !strings.Contains(err.Error(), "no free memory name") {
		t.Fatalf("err = %v", err)
	}
	var n int64
	db.Model(&database.Memory{}).Where("name = ?", "good-one").Count(&n)
	if n != 0 {
		t.Error("partial apply left the first memory")
	}
	if inc := loadInc(t, db, "inc-1"); inc.MemoryExtraction != database.MemoryExtractionRunning {
		t.Errorf("status changed by a rolled-back apply: %q", inc.MemoryExtraction)
	}
}

func TestBuildExtractedMemories_CapsAndOrderCheck(t *testing.T) {
	var out extractionOutput
	for i := 0; i < extractionMaxMemories+2; i++ {
		out.Memories = append(out.Memories, extractedMemory{Scope: "global", Name: fmt.Sprintf("m-%d", i), Type: "fact", Claim: "factual", Description: "d", Body: "b"})
	}
	out.Memories[0].Claim = "causal"
	out.Memories[0].CauseAt, out.Memories[0].EffectAt = "2026-11-02T10:00:00Z", "2026-11-02T10:05:00Z"
	out.Memories[1].Name = "m-0" // duplicate
	build := buildExtractedMemories(&out, "inc", "", nil)
	if len(build.Memories) != extractionMaxMemories-1 || build.Dropped != 3 {
		t.Fatalf("kept %d dropped %d", len(build.Memories), build.Dropped)
	}
	first := build.Memories[0]
	if first.Evidence != MemoryEvidenceUnverified || !strings.Contains(first.EvidenceNote, "cause 2026-11-02T10:00:00Z is before effect") {
		t.Errorf("causal in order: %+v", first)
	}
	if _, err := parseExtractionOutput("```json\n{\"timeline\": [], \"memories\": []}\n```"); err != nil {
		t.Errorf("fenced json: %v", err)
	}
	if _, err := parseExtractionOutput("Sure! {\"timeline\": [], \"memories\": []} done"); err != nil {
		t.Errorf("wrapped json: %v", err)
	}
}

func TestCutHeadTail(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	got := cutHeadTail(s, 50, 10, 20)
	if !strings.HasPrefix(got, strings.Repeat("a", 10)) || !strings.HasSuffix(got, strings.Repeat("b", 20)) || !strings.Contains(got, "170 bytes cut") {
		t.Errorf("%q", got)
	}
	if cutHeadTail("short", 50, 10, 20) != "short" {
		t.Error("short text changed")
	}
}

// TestExtractor_SpilledToolOutputs: the heads of the spilled tool outputs of
// the family reach the prompt, newest first, within the caps; a symlink in
// the directory is not followed.
func TestExtractor_SpilledToolOutputs(t *testing.T) {
	db := extractorTestDB(t)
	seedClosedIncident(t, db, "inc-1", "answer", database.AlertResolvedBySource)
	db.Create(&database.Incident{UUID: "inc-child", SourceKind: "alert", Status: database.IncidentStatusMerged, MergedIntoUUID: "inc-1", MemoryExtraction: database.MemoryExtractionSkipped})
	dir := t.TempDir()
	spill := filepath.Join(dir, "inc-1", "tool_outputs")
	if err := os.MkdirAll(spill, 0o755); err != nil {
		t.Fatal(err)
	}
	childSpill := filepath.Join(dir, "inc-child", "tool_outputs")
	if err := os.MkdirAll(childSpill, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", extractionSpillFileMax+100)
	for name, content := range map[string]string{
		filepath.Join(spill, "prometheus_1.txt"):    "PROM-OLD: load 95% at 10:05\n",
		filepath.Join(spill, "ssh_2.txt"):           "SSH-NEW: dmesg NETDEV WATCHDOG eth1\n",
		filepath.Join(spill, "huge_3.txt"):          big,
		filepath.Join(childSpill, "child_4.txt"):    "CHILD-SPILL: same deploy\n",
		filepath.Join(dir, "inc-1", "session.json"): "NOT-SPILL",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("OUTSIDE-LINK"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(spill, "link.txt")); err != nil {
		t.Fatal(err)
	}
	in, err := collectExtractionInput(db, &database.Incident{UUID: "inc-1", Status: database.IncidentStatusClosed, SourceKind: "alert"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PROM-OLD", "SSH-NEW", "CHILD-SPILL", "TOOL OUTPUT ssh_2.txt (incident inc-1"} {
		if !strings.Contains(in.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, bad := range []string{"OUTSIDE-LINK", "NOT-SPILL"} {
		if strings.Contains(in.Prompt, bad) {
			t.Errorf("prompt carries %q", bad)
		}
	}
	for _, sp := range in.Spill {
		if len(sp.Head) > extractionSpillFileMax {
			t.Errorf("%s head %d bytes over the cap", sp.Name, len(sp.Head))
		}
	}
	total := 0
	for _, sp := range in.Spill {
		total += len(sp.Head)
	}
	if total > extractionSpillTotal {
		t.Errorf("spill total %d over the cap", total)
	}
	// No directory: no spill, no error.
	in, err = collectExtractionInput(db, &database.Incident{UUID: "inc-1", Status: database.IncidentStatusClosed, SourceKind: "alert"}, filepath.Join(dir, "missing"))
	if err != nil || len(in.Spill) != 0 {
		t.Errorf("missing dir: spill=%d err=%v", len(in.Spill), err)
	}
}

// TestExtractor_Backfill queues only the closed alert incidents that were
// never extracted and resolved in the window.
func TestExtractor_Backfill(t *testing.T) {
	db := extractorTestDB(t)
	e, _ := newTestExtractor(t, db, nil)
	old := time.Now().AddDate(0, 0, -40)
	recent := time.Now().AddDate(0, 0, -3)
	db.Create(&database.Incident{UUID: "never-recent", SourceKind: "alert", Status: database.IncidentStatusClosed, ResolvedAt: &recent})
	db.Create(&database.Incident{UUID: "never-old", SourceKind: "alert", Status: database.IncidentStatusClosed, ResolvedAt: &old})
	db.Create(&database.Incident{UUID: "done", SourceKind: "alert", Status: database.IncidentStatusClosed, ResolvedAt: &recent, MemoryExtraction: database.MemoryExtractionSucceeded})
	db.Create(&database.Incident{UUID: "open", SourceKind: "alert", Status: database.IncidentStatusMonitor, ResolvedAt: &recent})
	n, err := e.Backfill(context.Background(), time.Now().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Fatalf("queued %d err %v", n, err)
	}
	if inc := loadInc(t, db, "never-recent"); inc.MemoryExtraction != database.MemoryExtractionPending {
		t.Errorf("never-recent: %q", inc.MemoryExtraction)
	}
	for _, uuid := range []string{"never-old", "open"} {
		if inc := loadInc(t, db, uuid); inc.MemoryExtraction != "" {
			t.Errorf("%s queued: %q", uuid, inc.MemoryExtraction)
		}
	}
	if inc := loadInc(t, db, "done"); inc.MemoryExtraction != database.MemoryExtractionSucceeded {
		t.Errorf("done re-queued: %q", inc.MemoryExtraction)
	}
	select {
	case <-e.kick:
	default:
		t.Error("backfill did not kick the loop")
	}
}
