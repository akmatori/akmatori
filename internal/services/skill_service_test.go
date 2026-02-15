package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupSkillTestDB creates an in-memory SQLite database with skill-related tables
func setupSkillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.Skill{},
		&database.ToolType{},
		&database.ToolInstance{},
		&database.SkillTool{},
		&database.Incident{},
		&database.LLMSettings{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	// Set global DB for functions that use it directly (e.g., TitleGenerator)
	database.DB = db

	return db
}

// newTestSkillService creates a SkillService with temp directories for testing
func newTestSkillService(t *testing.T, db *gorm.DB) *SkillService {
	t.Helper()
	dataDir := t.TempDir()

	// Create a minimal ContextService
	contextService, err := NewContextService(dataDir)
	if err != nil {
		t.Fatalf("failed to create context service: %v", err)
	}

	svc := NewSkillService(dataDir, nil, contextService)
	svc.db = db

	// Ensure directories exist
	os.MkdirAll(svc.incidentsDir, 0755)
	os.MkdirAll(svc.skillsDir, 0755)

	return svc
}

func TestSpawnIncidentManager_CreatesAgentsMdAtWorkspaceRoot(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "test-123",
		Message:  "Test alert",
	}

	incidentUUID, incidentDir, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	if incidentUUID == "" {
		t.Fatal("expected non-empty incident UUID")
	}
	if incidentDir == "" {
		t.Fatal("expected non-empty incident directory")
	}

	// AGENTS.md should be at workspace root, NOT in .codex/
	agentsMdPath := filepath.Join(incidentDir, "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); os.IsNotExist(err) {
		t.Error("AGENTS.md should exist at workspace root")
	}

	// .codex/ directory should NOT exist (pi-mono doesn't use it)
	codexDir := filepath.Join(incidentDir, ".codex")
	if _, err := os.Stat(codexDir); !os.IsNotExist(err) {
		t.Error(".codex directory should NOT exist - pi-mono uses workspace root")
	}
}

func TestSpawnIncidentManager_NoSkillsCopied(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create a skill directory with a SKILL.md to verify it is NOT copied
	testSkillDir := filepath.Join(svc.skillsDir, "test-skill")
	os.MkdirAll(testSkillDir, 0755)
	os.WriteFile(filepath.Join(testSkillDir, "SKILL.md"), []byte("test skill"), 0644)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "test-456",
		Message:  "Test alert message for test",
	}

	_, incidentDir, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	// Skills should NOT be copied into .codex/skills/ (pi-mono uses native tools)
	codexSkillsDir := filepath.Join(incidentDir, ".codex", "skills")
	if _, err := os.Stat(codexSkillsDir); !os.IsNotExist(err) {
		t.Error(".codex/skills directory should NOT exist - tools are registered as pi-mono ToolDefinitions")
	}
}

func TestSpawnIncidentManager_CreatesIncidentRecord(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	ctx := &IncidentContext{
		Source:   "zabbix",
		SourceID: "alert-789",
		Message:  "High CPU on server-01",
	}

	incidentUUID, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	// Verify incident record exists in database
	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident in database: %v", err)
	}

	if incident.Source != "zabbix" {
		t.Errorf("expected source 'zabbix', got '%s'", incident.Source)
	}
	if incident.Status != database.IncidentStatusPending {
		t.Errorf("expected status pending, got '%s'", incident.Status)
	}
}

func TestGenerateIncidentAgentsMd_ContainsPrompt(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	tmpFile := filepath.Join(t.TempDir(), "AGENTS.md")
	err := svc.generateIncidentAgentsMd(tmpFile)
	if err != nil {
		t.Fatalf("generateIncidentAgentsMd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)

	// Should contain the incident manager header
	if !strings.Contains(contentStr, "# Incident Manager") {
		t.Error("AGENTS.md should contain '# Incident Manager' header")
	}

	// Should contain the default prompt content
	if !strings.Contains(contentStr, "Senior Incident Manager") {
		t.Error("AGENTS.md should contain the incident manager prompt")
	}
}

func TestGenerateIncidentAgentsMd_NoStructuredOutputProtocol(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	tmpFile := filepath.Join(t.TempDir(), "AGENTS.md")
	err := svc.generateIncidentAgentsMd(tmpFile)
	if err != nil {
		t.Fatalf("generateIncidentAgentsMd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)

	// Should NOT contain old Codex-specific structured output protocol
	if strings.Contains(contentStr, "Structured Output Protocol") {
		t.Error("AGENTS.md should NOT contain 'Structured Output Protocol' - pi-mono handles output natively")
	}
}

func TestGenerateIncidentAgentsMd_EmbedsEnabledSkills(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create a skill in the database and on filesystem
	skill := &database.Skill{
		Name:        "zabbix-analyst",
		Description: "Analyzes Zabbix alerts",
		Enabled:     true,
	}
	db.Create(skill)

	// Create SKILL.md on filesystem
	skillDir := filepath.Join(svc.skillsDir, "zabbix-analyst")
	os.MkdirAll(skillDir, 0755)
	skillMd := "---\nname: zabbix-analyst\ndescription: Analyzes Zabbix alerts\n---\n\nYou are a Zabbix specialist."
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

	tmpFile := filepath.Join(t.TempDir(), "AGENTS.md")
	err := svc.generateIncidentAgentsMd(tmpFile)
	if err != nil {
		t.Fatalf("generateIncidentAgentsMd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)

	// Should contain the skill header
	if !strings.Contains(contentStr, "### zabbix-analyst") {
		t.Error("AGENTS.md should embed enabled skill headers")
	}

	// Should contain the skill description
	if !strings.Contains(contentStr, "Analyzes Zabbix alerts") {
		t.Error("AGENTS.md should embed skill description")
	}

	// Should contain the skill prompt body
	if !strings.Contains(contentStr, "You are a Zabbix specialist") {
		t.Error("AGENTS.md should embed skill prompt inline")
	}
}

func TestGenerateIncidentAgentsMd_ExcludesIncidentManager(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create incident-manager system skill in database
	db.Create(&database.Skill{
		Name:     "incident-manager",
		Enabled:  true,
		IsSystem: true,
	})

	tmpFile := filepath.Join(t.TempDir(), "AGENTS.md")
	err := svc.generateIncidentAgentsMd(tmpFile)
	if err != nil {
		t.Fatalf("generateIncidentAgentsMd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)

	// Should NOT embed incident-manager as a sub-skill (it's the root agent)
	if strings.Contains(contentStr, "### incident-manager") {
		t.Error("AGENTS.md should NOT embed incident-manager as a sub-skill")
	}
}

func TestGenerateSkillMd_NoPythonImports(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create tool types and instances
	toolType := &database.ToolType{Name: "ssh", Description: "SSH tool"}
	db.Create(toolType)

	toolInstance := &database.ToolInstance{
		ToolTypeID: toolType.ID,
		Name:       "ssh-prod",
		Enabled:    true,
		ToolType:   *toolType,
	}

	tools := []database.ToolInstance{*toolInstance}

	result := svc.generateSkillMd("test-skill", "Test skill description", "Investigate the server", tools)

	// Should NOT contain Python import statements
	if strings.Contains(result, "import sys") {
		t.Error("SKILL.md should NOT contain Python import statements")
	}
	if strings.Contains(result, "from scripts.") {
		t.Error("SKILL.md should NOT contain 'from scripts.' imports")
	}
	if strings.Contains(result, "```python") {
		t.Error("SKILL.md should NOT contain Python code blocks")
	}
}

func TestGenerateSkillMd_ContainsFrontmatter(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	result := svc.generateSkillMd("test-skill", "Test skill description", "Investigate the server", nil)

	if !strings.HasPrefix(result, "---\n") {
		t.Error("SKILL.md should start with YAML frontmatter delimiter")
	}
	if !strings.Contains(result, "name: test-skill") {
		t.Error("SKILL.md should contain skill name in frontmatter")
	}
	if !strings.Contains(result, "description: Test skill description") {
		t.Error("SKILL.md should contain description in frontmatter")
	}
}

func TestGenerateSkillMd_ContainsUserPrompt(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	result := svc.generateSkillMd("test-skill", "desc", "You are a specialist in database analysis.", nil)

	if !strings.Contains(result, "You are a specialist in database analysis.") {
		t.Error("SKILL.md should contain user prompt body")
	}
}

func TestGenerateSkillMd_ListsAssignedTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	sshType := &database.ToolType{Name: "ssh", Description: "SSH access"}
	zabbixType := &database.ToolType{Name: "zabbix", Description: "Zabbix monitoring"}
	db.Create(sshType)
	db.Create(zabbixType)

	tools := []database.ToolInstance{
		{ToolTypeID: sshType.ID, Name: "ssh-prod", Enabled: true, ToolType: *sshType},
		{ToolTypeID: zabbixType.ID, Name: "zabbix-main", Enabled: true, ToolType: *zabbixType},
	}

	result := svc.generateSkillMd("test-skill", "desc", "prompt body", tools)

	if !strings.Contains(result, "## Assigned Tools") {
		t.Error("SKILL.md should contain 'Assigned Tools' section")
	}
	if !strings.Contains(result, "- ssh") {
		t.Error("SKILL.md should list ssh tool")
	}
	if !strings.Contains(result, "- zabbix") {
		t.Error("SKILL.md should list zabbix tool")
	}
}

func TestGenerateSkillMd_NoToolsSection_WhenNoTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	result := svc.generateSkillMd("test-skill", "desc", "prompt body", nil)

	if strings.Contains(result, "## Assigned Tools") {
		t.Error("SKILL.md should NOT contain 'Assigned Tools' section when no tools assigned")
	}
}

func TestStripAutoGeneratedSections_OldQuickStart(t *testing.T) {
	body := `## Quick Start

` + "```python" + `
import sys; sys.path.insert(0, './.codex/skills/test-skill')
from scripts.ssh import execute_command
` + "```" + `

---

You are a specialist.`

	result := stripAutoGeneratedSections(body)
	if !strings.Contains(result, "You are a specialist.") {
		t.Error("should preserve user prompt after stripping Quick Start")
	}
	if strings.Contains(result, "Quick Start") {
		t.Error("should strip old Quick Start section")
	}
	if strings.Contains(result, "from scripts.") {
		t.Error("should strip Python imports")
	}
}

func TestStripAutoGeneratedSections_NewAssignedTools(t *testing.T) {
	body := "You are a specialist.\n\n## Assigned Tools\n\n- ssh\n- zabbix\n"

	result := stripAutoGeneratedSections(body)
	if !strings.Contains(result, "You are a specialist.") {
		t.Error("should preserve user prompt")
	}
	if strings.Contains(result, "## Assigned Tools") {
		t.Error("should strip Assigned Tools section")
	}
}

func TestStripAutoGeneratedSections_NoSections(t *testing.T) {
	body := "You are a specialist with deep knowledge."

	result := stripAutoGeneratedSections(body)
	if result != body {
		t.Errorf("expected unchanged body, got '%s'", result)
	}
}

func TestAssignTools_UpdatesDatabaseAssociation(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create skill in database and filesystem
	skill := &database.Skill{Name: "test-skill", Description: "Test", Enabled: true}
	db.Create(skill)
	skillDir := filepath.Join(svc.skillsDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\ntest prompt"), 0644)

	// Create tool type and instance
	toolType := &database.ToolType{Name: "ssh", Description: "SSH"}
	db.Create(toolType)
	toolInstance := &database.ToolInstance{ToolTypeID: toolType.ID, Name: "ssh-prod", Enabled: true}
	db.Create(toolInstance)

	// Assign tool
	err := svc.AssignTools("test-skill", []uint{toolInstance.ID})
	if err != nil {
		t.Fatalf("AssignTools failed: %v", err)
	}

	// Verify database association
	var skillTools []database.SkillTool
	db.Where("skill_id = ?", skill.ID).Find(&skillTools)
	if len(skillTools) != 1 {
		t.Errorf("expected 1 tool association, got %d", len(skillTools))
	}
}

func TestAssignTools_NoSymlinks(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create skill in database and filesystem
	skill := &database.Skill{Name: "test-skill", Description: "Test", Enabled: true}
	db.Create(skill)
	skillDir := filepath.Join(svc.skillsDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\ntest prompt"), 0644)

	// Create tool
	toolType := &database.ToolType{Name: "ssh", Description: "SSH"}
	db.Create(toolType)
	toolInstance := &database.ToolInstance{ToolTypeID: toolType.ID, Name: "ssh-prod", Enabled: true}
	db.Create(toolInstance)

	// Assign tool
	err := svc.AssignTools("test-skill", []uint{toolInstance.ID})
	if err != nil {
		t.Fatalf("AssignTools failed: %v", err)
	}

	// Scripts directory should NOT have symlinks (pi-mono uses native tools)
	scriptsDir := filepath.Join(skillDir, "scripts")
	if _, err := os.Stat(scriptsDir); !os.IsNotExist(err) {
		// If scripts dir exists, it should be empty of symlinks
		entries, _ := os.ReadDir(scriptsDir)
		for _, e := range entries {
			entryPath := filepath.Join(scriptsDir, e.Name())
			if info, err := os.Lstat(entryPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				t.Errorf("found unexpected symlink: %s - pi-mono uses native ToolDefinition objects", e.Name())
			}
		}
	}

	// mcp_client.py symlink should NOT exist
	mcpClientPath := filepath.Join(skillDir, "scripts", "mcp_client.py")
	if _, err := os.Stat(mcpClientPath); !os.IsNotExist(err) {
		t.Error("mcp_client.py symlink should NOT exist - pi-mono uses native tools")
	}
}

func TestAssignTools_RegeneratesSkillMd(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create skill
	skill := &database.Skill{Name: "test-skill", Description: "Test", Enabled: true}
	db.Create(skill)
	skillDir := filepath.Join(svc.skillsDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\noriginal prompt"), 0644)

	// Create tool
	toolType := &database.ToolType{Name: "zabbix", Description: "Zabbix"}
	db.Create(toolType)
	toolInstance := &database.ToolInstance{ToolTypeID: toolType.ID, Name: "zabbix-main", Enabled: true}
	db.Create(toolInstance)

	// Assign tool
	err := svc.AssignTools("test-skill", []uint{toolInstance.ID})
	if err != nil {
		t.Fatalf("AssignTools failed: %v", err)
	}

	// Read regenerated SKILL.md
	content, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read regenerated SKILL.md: %v", err)
	}

	contentStr := string(content)

	// Should contain the tool list
	if !strings.Contains(contentStr, "## Assigned Tools") {
		t.Error("regenerated SKILL.md should contain Assigned Tools section")
	}
	if !strings.Contains(contentStr, "- zabbix") {
		t.Error("regenerated SKILL.md should list zabbix tool")
	}

	// Should preserve user prompt
	if !strings.Contains(contentStr, "original prompt") {
		t.Error("regenerated SKILL.md should preserve original prompt")
	}

	// Should NOT contain Python imports
	if strings.Contains(contentStr, "from scripts.") {
		t.Error("regenerated SKILL.md should NOT contain Python imports")
	}
}
