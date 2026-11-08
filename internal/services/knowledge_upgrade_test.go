package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestDisableReservedNamespaceCollisions(t *testing.T) {
	db := setupSkillTestDB(t)
	if err := db.AutoMigrate(&database.HTTPConnector{}, &database.MCPServerConfig{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.HTTPConnector{ToolTypeName: "memory", Enabled: true, Tools: database.JSONB{}})
	db.Create(&database.HTTPConnector{ToolTypeName: "billing", Enabled: true, Tools: database.JSONB{}})
	db.Create(&database.MCPServerConfig{Name: "Runbook wiki", NamespacePrefix: "runbooks", Enabled: true, Transport: "http", URL: "https://x"})
	got := DisableReservedNamespaceCollisions(db)
	if len(got) != 2 {
		t.Fatalf("disabled %v, want the memory connector and the runbooks MCP server", got)
	}
	var billing database.HTTPConnector
	db.Where("tool_type_name = ?", "billing").First(&billing)
	if !billing.Enabled {
		t.Error("unrelated connector disabled")
	}
	if again := DisableReservedNamespaceCollisions(db); len(again) != 0 {
		t.Errorf("second start disabled %v", again)
	}
}

func TestWarnRetiredKnowledgeReferences(t *testing.T) {
	db := setupSkillTestDB(t)
	if err := db.AutoMigrate(&database.Runbook{}, &database.Memory{}, &database.CronJob{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db.Create(&database.Skill{Name: "old-skill", Enabled: true})
	db.Create(&database.Skill{Name: "new-skill", Enabled: true})
	_ = os.MkdirAll(filepath.Join(dir, "old-skill"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "new-skill"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "old-skill", "SKILL.md"), []byte(`ask subagent({"agent": "memory-searcher"})`), 0644)
	_ = os.WriteFile(filepath.Join(dir, "new-skill", "SKILL.md"), []byte(`gateway_call("memory.search")`), 0644)
	db.Create(&database.Runbook{Title: "Old SOP", Content: "grep /akmatori/runbooks/ first"})
	db.Create(&database.Memory{Scope: "global", Name: "m1", Type: "fact", Description: "d", Body: "the memory-writer saves"})
	db.Create(&database.CronJob{UUID: "c", Name: "custom-dreaming", Schedule: "* * * * *", Prompt: "use runbook-searcher"})

	hits := WarnRetiredKnowledgeReferences(db, dir)
	joined := strings.Join(hits, "|")
	for _, want := range []string{"skill old-skill", "runbook Old SOP", "memory global/m1", "cron custom-dreaming"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, hits)
		}
	}
	if strings.Contains(joined, "new-skill") {
		t.Errorf("clean skill flagged: %v", hits)
	}
}
