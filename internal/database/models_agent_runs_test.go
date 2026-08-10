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
	if err := CreateAgentRun(runID, incidentUUID, "anthropic", "claude-sonnet-5", "high"); err != nil {
		t.Fatalf("CreateAgentRun: %v", err)
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
	if err := CreateAgentRun(runID, uuid.NewString(), "openai", "gpt-5.6", "medium"); err != nil {
		t.Fatalf("CreateAgentRun: %v", err)
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
	if err := CreateAgentRun(runID, uuid.NewString(), "google", "gemini-4", "off"); err != nil {
		t.Fatalf("CreateAgentRun: %v", err)
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
	if err := CreateAgentRun(runID, uuid.NewString(), "anthropic", "claude-haiku-4-5", "low"); err != nil {
		t.Fatalf("CreateAgentRun: %v", err)
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

	if err := CreateAgentRun("r", "i", "p", "m", "t"); err == nil {
		t.Fatal("CreateAgentRun with nil DB: want error")
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
