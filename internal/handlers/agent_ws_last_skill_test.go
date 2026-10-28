package handlers

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupLastSkillDB(t *testing.T, runIDs ...string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}, &database.AgentRun{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	for _, id := range runIDs {
		if err := db.Create(&database.AgentRun{RunID: id, ScopeUUID: "incident-skill"}).Error; err != nil {
			t.Fatalf("seed run: %v", err)
		}
	}
	return db
}

func lastSkillOf(t *testing.T, db *gorm.DB, runID string) string {
	t.Helper()
	var run database.AgentRun
	if err := db.Where("run_id = ?", runID).First(&run).Error; err != nil {
		t.Fatalf("read run %s: %v", runID, err)
	}
	return run.LastSkillUsed
}

// TestHandleAgentCompleted_PersistsLastSkillBeforeCallback verifies that the
// last_skill from an agent_completed frame lands on the run row before
// OnCompleted fires, so the finalizer's BuildFormatFlow read observes it.
func TestHandleAgentCompleted_PersistsLastSkillBeforeCallback(t *testing.T) {
	db := setupLastSkillDB(t, "run-1")

	handler := NewAgentWSHandler()
	var skillAtCallbackTime string
	handler.callbackMu.Lock()
	handler.callbacks["incident-skill"] = incidentCallbackEntry{
		runID: "run-1",
		callback: IncidentCallback{
			OnCompleted: func(sessionID, response string, tokensUsed int, executionTimeMs int64) {
				skillAtCallbackTime = lastSkillOf(t, db, "run-1")
			},
		},
	}
	handler.callbackMu.Unlock()

	handler.handleAgentCompleted(AgentMessage{
		Type:       AgentMessageTypeAgentCompleted,
		IncidentID: "incident-skill",
		Output:     "final response",
		SessionID:  "session-1",
		RunID:      "run-1",
		LastSkill:  "victoria-metrics",
	})

	if skillAtCallbackTime != "victoria-metrics" {
		t.Errorf("last_skill_used at callback time = %q, want victoria-metrics", skillAtCallbackTime)
	}
}

// TestHandleAgentCompleted_SupersededRunWritesOnlyItsOwnRow verifies that a
// late completion frame from a superseded run records its last skill on its
// own run row and leaves the current run's row alone.
func TestHandleAgentCompleted_SupersededRunWritesOnlyItsOwnRow(t *testing.T) {
	db := setupLastSkillDB(t, "run-1", "run-2")
	if err := db.Model(&database.AgentRun{}).Where("run_id = ?", "run-2").
		Update("last_skill_used", "netbox").Error; err != nil {
		t.Fatalf("seed last_skill_used: %v", err)
	}

	handler := NewAgentWSHandler()
	handler.callbackMu.Lock()
	handler.callbacks["incident-skill"] = incidentCallbackEntry{runID: "run-2"}
	handler.callbackMu.Unlock()

	handler.handleAgentCompleted(AgentMessage{
		Type:       AgentMessageTypeAgentCompleted,
		IncidentID: "incident-skill",
		Output:     "stale response",
		RunID:      "run-1",
		LastSkill:  "grafana-watcher",
	})

	if got := lastSkillOf(t, db, "run-2"); got != "netbox" {
		t.Errorf("current run's last_skill_used = %q, want netbox", got)
	}
	if got := lastSkillOf(t, db, "run-1"); got != "grafana-watcher" {
		t.Errorf("superseded run's last_skill_used = %q, want grafana-watcher", got)
	}
}
