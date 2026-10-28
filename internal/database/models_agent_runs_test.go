package database

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAgentRunsDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Incident{}, &AgentRun{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
}

func mustGetRun(t *testing.T, runID string) AgentRun {
	t.Helper()
	var run AgentRun
	if err := DB.Where("run_id = ?", runID).First(&run).Error; err != nil {
		t.Fatalf("fetch run %s: %v", runID, err)
	}
	return run
}

func TestAgentRunLifecycle_Completed(t *testing.T) {
	setupAgentRunsDB(t)

	incidentUUID := uuid.NewString()
	if err := DB.Create(&Incident{
		UUID:       incidentUUID,
		Source:     "zabbix",
		SourceKind: IncidentSourceKindAlert,
	}).Error; err != nil {
		t.Fatalf("create incident: %v", err)
	}

	runID := uuid.NewString()
	if err := RecordAgentRunDispatch(runID, incidentUUID, "anthropic", "claude-sonnet-5", "high"); err != nil {
		t.Fatalf("RecordAgentRunDispatch: %v", err)
	}

	run := mustGetRun(t, runID)
	if run.Status != AgentRunStatusRunning {
		t.Fatalf("status = %s, want running", run.Status)
	}
	if run.SourceKind != IncidentSourceKindAlert {
		t.Fatalf("source_kind = %q, want %q (denormalized from incident)", run.SourceKind, IncidentSourceKindAlert)
	}

	metrics := AgentRunMetrics{
		TokensUsed:       1200,
		InputTokens:      900,
		OutputTokens:     300,
		CacheReadTokens:  500,
		CacheWriteTokens: 100,
		CostUSD:          0.042,
		ExecutionTimeMs:  61_000,
	}
	if err := CompleteAgentRun(runID, metrics, AgentRunOutcomeResolved); err != nil {
		t.Fatalf("CompleteAgentRun: %v", err)
	}

	run = mustGetRun(t, runID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("status = %s, want completed", run.Status)
	}
	if run.Outcome != AgentRunOutcomeResolved {
		t.Fatalf("outcome = %q, want resolved", run.Outcome)
	}
	if run.TokensUsed != 1200 || run.InputTokens != 900 || run.OutputTokens != 300 ||
		run.CacheReadTokens != 500 || run.CacheWriteTokens != 100 {
		t.Fatalf("token fields not persisted: %+v", run)
	}
	if run.CostUSD != 0.042 {
		t.Fatalf("cost_usd = %v, want 0.042", run.CostUSD)
	}
	if run.CompletedAt == nil {
		t.Fatal("completed_at not set")
	}
}

func TestAgentRunLifecycle_Failed(t *testing.T) {
	setupAgentRunsDB(t)

	runID := uuid.NewString()
	if err := RecordAgentRunDispatch(runID, uuid.NewString(), "openai", "gpt-5.6", "medium"); err != nil {
		t.Fatalf("RecordAgentRunDispatch: %v", err)
	}
	if err := FailAgentRun(runID, "quota exceeded"); err != nil {
		t.Fatalf("FailAgentRun: %v", err)
	}

	run := mustGetRun(t, runID)
	if run.Status != AgentRunStatusFailed {
		t.Fatalf("status = %s, want failed", run.Status)
	}
	if run.ErrorMessage != "quota exceeded" {
		t.Fatalf("error_message = %q", run.ErrorMessage)
	}
}

// A superseded run keeps its label when its late completion frame arrives,
// but still records the tokens it spent.
func TestAgentRunLifecycle_SupersededKeepsLabelRecordsMetrics(t *testing.T) {
	setupAgentRunsDB(t)

	runID := uuid.NewString()
	if err := RecordAgentRunDispatch(runID, uuid.NewString(), "google", "gemini-4", "off"); err != nil {
		t.Fatalf("RecordAgentRunDispatch: %v", err)
	}
	if err := SupersedeAgentRun(runID); err != nil {
		t.Fatalf("SupersedeAgentRun: %v", err)
	}
	if err := CompleteAgentRun(runID, AgentRunMetrics{TokensUsed: 700, CostUSD: 0.01}, ""); err != nil {
		t.Fatalf("CompleteAgentRun: %v", err)
	}

	run := mustGetRun(t, runID)
	if run.Status != AgentRunStatusSuperseded {
		t.Fatalf("status = %s, want superseded (late completion must not relabel)", run.Status)
	}
	if run.TokensUsed != 700 || run.CostUSD != 0.01 {
		t.Fatalf("late metrics not recorded: %+v", run)
	}
}

// SupersedeAgentRun must not relabel a run that already reached a terminal
// state — only a still-running run can be displaced.
func TestSupersedeAgentRun_TerminalRunUntouched(t *testing.T) {
	setupAgentRunsDB(t)

	runID := uuid.NewString()
	if err := RecordAgentRunDispatch(runID, uuid.NewString(), "anthropic", "claude-haiku-4-5", "low"); err != nil {
		t.Fatalf("RecordAgentRunDispatch: %v", err)
	}
	if err := CompleteAgentRun(runID, AgentRunMetrics{TokensUsed: 10}, AgentRunOutcomeUnresolved); err != nil {
		t.Fatalf("CompleteAgentRun: %v", err)
	}
	if err := SupersedeAgentRun(runID); err != nil {
		t.Fatalf("SupersedeAgentRun: %v", err)
	}

	if run := mustGetRun(t, runID); run.Status != AgentRunStatusCompleted {
		t.Fatalf("status = %s, want completed", run.Status)
	}
}

func TestAgentRunHelpers_NilDB(t *testing.T) {
	prev := DB
	DB = nil
	t.Cleanup(func() { DB = prev })

	if err := RecordAgentRunDispatch("r", "i", "p", "m", "t"); err == nil {
		t.Fatal("RecordAgentRunDispatch with nil DB: want error")
	}
	if err := CompleteAgentRun("r", AgentRunMetrics{}, ""); err == nil {
		t.Fatal("CompleteAgentRun with nil DB: want error")
	}
	if err := FailAgentRun("r", "boom"); err == nil {
		t.Fatal("FailAgentRun with nil DB: want error")
	}
	if err := SupersedeAgentRun("r"); err == nil {
		t.Fatal("SupersedeAgentRun with nil DB: want error")
	}
}

func TestFailInterruptedAgentRuns(t *testing.T) {
	setupAgentRunsDB(t)

	running, completed, superseded := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{running, completed, superseded} {
		if err := RecordAgentRunDispatch(id, uuid.NewString(), "anthropic", "claude-sonnet-5", ""); err != nil {
			t.Fatalf("RecordAgentRunDispatch: %v", err)
		}
	}
	if err := CompleteAgentRun(completed, AgentRunMetrics{TokensUsed: 10}, AgentRunOutcomeResolved); err != nil {
		t.Fatalf("CompleteAgentRun: %v", err)
	}
	if err := SupersedeAgentRun(superseded); err != nil {
		t.Fatalf("SupersedeAgentRun: %v", err)
	}

	n, err := FailInterruptedAgentRuns("interrupted")
	if err != nil {
		t.Fatalf("FailInterruptedAgentRuns: %v", err)
	}
	if n != 1 {
		t.Errorf("rows changed = %d, want 1", n)
	}

	run := mustGetRun(t, running)
	if run.Status != AgentRunStatusFailed || run.ErrorMessage != "interrupted" || run.CompletedAt == nil {
		t.Errorf("running run = {status %s, error %q, completed_at %v}, want failed/interrupted/set", run.Status, run.ErrorMessage, run.CompletedAt)
	}
	if got := mustGetRun(t, completed).Status; got != AgentRunStatusCompleted {
		t.Errorf("completed run status = %s, want unchanged", got)
	}
	if got := mustGetRun(t, superseded).Status; got != AgentRunStatusSuperseded {
		t.Errorf("superseded run status = %s, want unchanged", got)
	}

	// A second sweep finds nothing.
	if n, _ := FailInterruptedAgentRuns("interrupted"); n != 0 {
		t.Errorf("second sweep changed %d rows, want 0", n)
	}
}

// A run created before dispatch keeps its scope fields; the dispatch only
// stamps the model, and FinishAgentRun stores the work.
func TestBeginAgentRun_DispatchAndFinish(t *testing.T) {
	setupAgentRunsDB(t)

	run := &AgentRun{ScopeUUID: "scope-1", EventUUID: "ev-1", SourceKind: IncidentSourceKindCron, SourceUUID: "job-1"}
	if err := BeginAgentRun(DB, run); err != nil {
		t.Fatalf("BeginAgentRun: %v", err)
	}
	if run.RunID == "" || run.Status != AgentRunStatusRunning {
		t.Fatalf("run = %+v, want generated run_id and running status", run)
	}
	if err := RecordAgentRunDispatch(run.RunID, "scope-1", "anthropic", "claude-sonnet-5", "high"); err != nil {
		t.Fatalf("RecordAgentRunDispatch: %v", err)
	}
	if err := UpdateAgentRunLog(run.RunID, "step 1"); err != nil {
		t.Fatalf("UpdateAgentRunLog: %v", err)
	}
	got := mustGetRun(t, run.RunID)
	if got.Model != "claude-sonnet-5" || got.SourceUUID != "job-1" || got.FullLog != "step 1" {
		t.Fatalf("after dispatch = %+v", got)
	}
	var n int64
	DB.Model(&AgentRun{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (dispatch must update, not insert)", n)
	}

	if err := FinishAgentRun(DB, run.RunID, AgentRunResult{SessionID: "s", FullLog: "full", Response: "answer"}); err != nil {
		t.Fatalf("FinishAgentRun: %v", err)
	}
	got = mustGetRun(t, run.RunID)
	if got.Status != AgentRunStatusCompleted || got.Response != "answer" || got.FullLog != "full" || got.CompletedAt == nil {
		t.Fatalf("after finish = %+v", got)
	}

	latest, err := LatestScopeRun(DB, "scope-1")
	if err != nil || latest == nil || latest.RunID != run.RunID {
		t.Fatalf("LatestScopeRun = %+v, %v", latest, err)
	}
	if none, _ := LatestScopeRun(DB, "other"); none != nil {
		t.Fatalf("LatestScopeRun(other) = %+v, want nil", none)
	}
}

// FinishAgentRun must not relabel a run the worker frames already ended.
func TestFinishAgentRun_KeepsTerminalStatus(t *testing.T) {
	setupAgentRunsDB(t)
	run := &AgentRun{ScopeUUID: "s"}
	if err := BeginAgentRun(DB, run); err != nil {
		t.Fatal(err)
	}
	if err := FailAgentRun(run.RunID, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := FinishAgentRun(DB, run.RunID, AgentRunResult{Response: "late"}); err != nil {
		t.Fatal(err)
	}
	if got := mustGetRun(t, run.RunID); got.Status != AgentRunStatusFailed || got.Response != "late" {
		t.Fatalf("got %+v, want failed status kept and response stored", got)
	}
}

func TestLatestRunResponses(t *testing.T) {
	setupAgentRunsDB(t)
	for _, r := range []AgentRun{
		{IncidentUUID: "a", Response: "first"},
		{IncidentUUID: "a", Response: "second"},
		{IncidentUUID: "a"}, // newest run has no answer yet
		{IncidentUUID: "b"},
	} {
		r := r
		if err := BeginAgentRun(DB, &r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LatestRunResponses(DB, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != "second" || len(got) != 1 {
		t.Fatalf("got %v, want only a=second", got)
	}
}
