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
	_ = os.MkdirAll(svc.incidentsDir, 0755)
	_ = os.MkdirAll(svc.skillsDir, 0755)

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
	_ = os.MkdirAll(testSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(testSkillDir, "SKILL.md"), []byte("test skill"), 0644)

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

func TestGenerateIncidentAgentsMd_NoSkillsEmbedded(t *testing.T) {
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
	_ = os.MkdirAll(skillDir, 0755)
	skillMd := "---\nname: zabbix-analyst\ndescription: Analyzes Zabbix alerts\n---\n\nYou are a Zabbix specialist."
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

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

	// Skills are discovered by pi-mono's DefaultResourceLoader via additionalSkillPaths,
	// so AGENTS.md should NOT embed skill details inline
	if strings.Contains(contentStr, "### zabbix-analyst") {
		t.Error("AGENTS.md should NOT embed skills inline - pi-mono discovers them via additionalSkillPaths")
	}

	// Should still contain the incident manager content
	if !strings.Contains(contentStr, "# Incident Manager") {
		t.Error("AGENTS.md should contain Incident Manager header")
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

	toolType := &database.ToolType{Name: "ssh", Description: "SSH tool"}
	db.Create(toolType)

	toolInstance := &database.ToolInstance{
		ToolTypeID: toolType.ID,
		Name:       "ssh-prod",
		Enabled:    true,
		ToolType:   *toolType,
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1"},
			},
		},
	}

	tools := []database.ToolInstance{*toolInstance}

	result := svc.generateSkillMd("test-skill", "Test skill description", "Investigate the server", tools)

	// Should NOT contain old-style Python import statements (sys.path.insert, from scripts.*)
	if strings.Contains(result, "import sys") {
		t.Error("SKILL.md should NOT contain 'import sys' statements")
	}
	if strings.Contains(result, "from scripts.") {
		t.Error("SKILL.md should NOT contain 'from scripts.' imports")
	}
	// But SHOULD contain new-style gateway_call usage examples
	if !strings.Contains(result, "gateway_call") {
		t.Error("SKILL.md SHOULD contain gateway_call usage examples")
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
	// New format: ### instance-name (ID: X, type: tool-type)
	if !strings.Contains(result, "ssh-prod") || !strings.Contains(result, "type: ssh") {
		t.Error("SKILL.md should list ssh tool instance with type")
	}
	if !strings.Contains(result, "zabbix-main") || !strings.Contains(result, "type: zabbix") {
		t.Error("SKILL.md should list zabbix tool instance with type")
	}
	// Should include gateway_call routing instructions
	if !strings.Contains(result, "gateway_call") {
		t.Error("SKILL.md should include gateway_call routing instructions")
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

func TestStripAutoGeneratedSections_NewRichAssignedTools(t *testing.T) {
	body := "You are a specialist.\n\n## Assigned Tools\n\n### Production hosts (ID: 3, type: ssh)\nConfigured hosts: web-01, web-02\nWhen using ssh tools, pass `tool_instance_id: 3` to target this instance.\n"

	result := stripAutoGeneratedSections(body)
	if !strings.Contains(result, "You are a specialist.") {
		t.Error("should preserve user prompt")
	}
	if strings.Contains(result, "## Assigned Tools") {
		t.Error("should strip rich Assigned Tools section")
	}
	if strings.Contains(result, "tool_instance_id") {
		t.Error("should strip tool_instance_id instructions")
	}
}

func TestExtractToolDetails_SSH(t *testing.T) {
	tool := database.ToolInstance{
		Name:     "ssh-prod",
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1"},
				map[string]interface{}{"hostname": "db-01", "address": "10.0.0.2"},
			},
		},
	}

	details := extractToolDetails(tool)
	if !strings.Contains(details, "web-01") {
		t.Error("SSH details should list hostname web-01")
	}
	if !strings.Contains(details, "db-01") {
		t.Error("SSH details should list hostname db-01")
	}
}

func TestExtractToolDetails_Zabbix(t *testing.T) {
	// Zabbix URL is an internal MCP Gateway detail — should NOT appear in SKILL.md
	tool := database.ToolInstance{
		Name:     "zabbix-prod",
		ToolType: database.ToolType{Name: "zabbix"},
		Settings: database.JSONB{
			"zabbix_url": "https://zabbix.example.com",
		},
	}

	details := extractToolDetails(tool)
	if details != "" {
		t.Errorf("expected empty details for zabbix tool (URL is internal to MCP Gateway), got '%s'", details)
	}
}

func TestExtractToolDetails_NoSettings(t *testing.T) {
	tool := database.ToolInstance{
		Name:     "empty-tool",
		ToolType: database.ToolType{Name: "ssh"},
	}

	details := extractToolDetails(tool)
	if details != "" {
		t.Errorf("expected empty details for tool without settings, got '%s'", details)
	}
}

func TestGetEnabledSkillNames_FiltersSystemAndDisabled(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create a mix of skills: enabled non-system, enabled system, disabled
	db.Create(&database.Skill{Name: "linux-agent", Description: "Linux", Enabled: true, IsSystem: false})
	db.Create(&database.Skill{Name: "incident-manager", Description: "Manager", Enabled: true, IsSystem: true})
	disabledSkill := &database.Skill{Name: "disabled-skill", Description: "Disabled", Enabled: true, IsSystem: false}
	db.Create(disabledSkill)
	db.Model(disabledSkill).Update("enabled", false) // Must update after create to bypass GORM default:true
	db.Create(&database.Skill{Name: "zabbix-analyst", Description: "Zabbix", Enabled: true, IsSystem: false})

	names := svc.GetEnabledSkillNames()

	// Should include only enabled non-system skills
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	if !nameSet["linux-agent"] {
		t.Error("expected linux-agent in enabled skill names")
	}
	if !nameSet["zabbix-analyst"] {
		t.Error("expected zabbix-analyst in enabled skill names")
	}
	if nameSet["incident-manager"] {
		t.Error("system skill incident-manager should not be in enabled skill names")
	}
	if nameSet["disabled-skill"] {
		t.Error("disabled skill should not be in enabled skill names")
	}
}

func TestStripAutoGeneratedSections_NoSections(t *testing.T) {
	body := "You are a specialist with deep knowledge."

	result := stripAutoGeneratedSections(body)
	if result != body {
		t.Errorf("expected unchanged body, got '%s'", result)
	}
}

func TestGenerateSkillMd_ContainsGatewayCallExamples(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	sshType := &database.ToolType{Name: "ssh", Description: "SSH access"}
	zabbixType := &database.ToolType{Name: "zabbix", Description: "Zabbix monitoring"}
	db.Create(sshType)
	db.Create(zabbixType)

	tools := []database.ToolInstance{
		{
			ToolTypeID: sshType.ID, Name: "Production hosts", LogicalName: "prod-ssh", Enabled: true, ToolType: *sshType,
			Settings: database.JSONB{"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1"},
			}},
		},
		{ToolTypeID: zabbixType.ID, Name: "Zabbix Main", LogicalName: "zabbix-main", Enabled: true, ToolType: *zabbixType},
	}
	tools[0].ID = 3
	tools[1].ID = 2

	result := svc.generateSkillMd("test-skill", "desc", "prompt body", tools)

	// Should contain gateway_call examples
	if !strings.Contains(result, "gateway_call") {
		t.Error("SKILL.md should contain gateway_call usage examples")
	}
	// SSH: should show gateway_call with logical name
	if !strings.Contains(result, `"ssh.execute_command"`) {
		t.Error("SKILL.md should contain ssh.execute_command tool reference")
	}
	if !strings.Contains(result, `"prod-ssh"`) {
		t.Error("SKILL.md should contain logical name prod-ssh for SSH instance")
	}
	// Zabbix: should show gateway_call with logical name
	if !strings.Contains(result, `"zabbix.get_hosts"`) {
		t.Error("SKILL.md should contain zabbix.get_hosts tool reference")
	}
	if !strings.Contains(result, `"zabbix-main"`) {
		t.Error("SKILL.md should contain logical name zabbix-main for Zabbix instance")
	}
	// Should contain discovery and scripting hints
	if !strings.Contains(result, "search_tools") {
		t.Error("SKILL.md should mention search_tools for discovery")
	}
	if !strings.Contains(result, "execute_script") {
		t.Error("SKILL.md should mention execute_script for batch operations")
	}
	// Should show logical_name in header instead of ID
	if !strings.Contains(result, `logical_name: "prod-ssh"`) {
		t.Error("SKILL.md tool header should show logical_name")
	}
	// Should NOT contain old Python patterns
	if strings.Contains(result, "from ssh import") {
		t.Error("SKILL.md should NOT contain Python import patterns")
	}
	if strings.Contains(result, "tool_instance_id=") {
		t.Error("SKILL.md should NOT contain tool_instance_id= patterns")
	}
}

func TestSshAllHostsAllowWrite_AllWriteEnabled(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1", "allow_write_commands": true},
				map[string]interface{}{"hostname": "web-02", "address": "10.0.0.2", "allow_write_commands": true},
			},
		},
	}
	if !sshAllHostsAllowWrite(tool) {
		t.Error("expected true when all hosts have allow_write_commands=true")
	}
}

func TestSshAllHostsAllowWrite_SomeReadOnly(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1", "allow_write_commands": true},
				map[string]interface{}{"hostname": "web-02", "address": "10.0.0.2", "allow_write_commands": false},
			},
		},
	}
	if sshAllHostsAllowWrite(tool) {
		t.Error("expected false when some hosts are read-only")
	}
}

func TestSshAllHostsAllowWrite_NoWriteField(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01"},
			},
		},
	}
	if sshAllHostsAllowWrite(tool) {
		t.Error("expected false when allow_write_commands field is missing")
	}
}

func TestSshAllHostsAllowWrite_NoSettings(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
	}
	if sshAllHostsAllowWrite(tool) {
		t.Error("expected false when settings is nil")
	}
}

func TestSshAllHostsAllowWrite_EmptyHosts(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{},
		},
	}
	if sshAllHostsAllowWrite(tool) {
		t.Error("expected false when hosts list is empty")
	}
}

func TestSshAllHostsAllowWrite_NoHostsKey(t *testing.T) {
	tool := database.ToolInstance{
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"other_setting": "value",
		},
	}
	if sshAllHostsAllowWrite(tool) {
		t.Error("expected false when ssh_hosts key is missing")
	}
}

func TestGenerateToolUsageExample_SSHReadOnly(t *testing.T) {
	tool := database.ToolInstance{
		Name:        "readonly-ssh",
		LogicalName: "readonly-ssh",
		ToolType:    database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1", "allow_write_commands": false},
			},
		},
	}
	tool.ID = 5

	result := generateToolUsageExample(tool)

	if !strings.Contains(result, "Read-only mode is enabled") {
		t.Error("SSH read-only tool should include read-only mode note")
	}
	if !strings.Contains(result, "nproc") {
		t.Error("SSH read-only note should mention nproc")
	}
	if !strings.Contains(result, "lscpu") {
		t.Error("SSH read-only note should mention lscpu")
	}
	if !strings.Contains(result, `"servers"`) {
		t.Error("SSH usage example should show servers parameter")
	}
}

func TestGenerateToolUsageExample_SSHWriteEnabled(t *testing.T) {
	tool := database.ToolInstance{
		Name:        "write-ssh",
		LogicalName: "write-ssh",
		ToolType:    database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1", "allow_write_commands": true},
			},
		},
	}
	tool.ID = 5

	result := generateToolUsageExample(tool)

	if strings.Contains(result, "Read-only mode is enabled") {
		t.Error("SSH write-enabled tool should NOT include read-only mode note")
	}
}

func TestAssignTools_UpdatesDatabaseAssociation(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create skill in database and filesystem
	skill := &database.Skill{Name: "test-skill", Description: "Test", Enabled: true}
	db.Create(skill)
	skillDir := filepath.Join(svc.skillsDir, "test-skill")
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\ntest prompt"), 0644)

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
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\ntest prompt"), 0644)

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
	_ = os.MkdirAll(skillDir, 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Test\n---\n\noriginal prompt"), 0644)

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

	// Should contain the tool list with instance details
	if !strings.Contains(contentStr, "## Assigned Tools") {
		t.Error("regenerated SKILL.md should contain Assigned Tools section")
	}
	if !strings.Contains(contentStr, "zabbix-main") || !strings.Contains(contentStr, "type: zabbix") {
		t.Error("regenerated SKILL.md should list zabbix tool instance with type")
	}
	if !strings.Contains(contentStr, "gateway_call") {
		t.Error("regenerated SKILL.md should include gateway_call routing instructions")
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

func TestGetToolAllowlist_SkillWithTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create tool types
	sshType := database.ToolType{Name: "ssh", Description: "SSH connections"}
	db.Create(&sshType)
	zabbixType := database.ToolType{Name: "zabbix", Description: "Zabbix monitoring"}
	db.Create(&zabbixType)

	// Create tool instances
	sshInstance := database.ToolInstance{
		Name:        "Production SSH",
		LogicalName: "prod-ssh",
		ToolTypeID:  sshType.ID,
		Enabled:     true,
	}
	db.Create(&sshInstance)

	zabbixInstance := database.ToolInstance{
		Name:        "Production Zabbix",
		LogicalName: "prod-zabbix",
		ToolTypeID:  zabbixType.ID,
		Enabled:     true,
	}
	db.Create(&zabbixInstance)

	// Create skill with tools assigned
	skill := database.Skill{Name: "linux-admin", Description: "Linux admin", Enabled: true, IsSystem: false}
	db.Create(&skill)
	db.Model(&skill).Association("Tools").Append(&sshInstance, &zabbixInstance)

	allowlist := svc.GetToolAllowlist()

	if len(allowlist) != 2 {
		t.Fatalf("expected 2 entries in allowlist, got %d", len(allowlist))
	}

	// Verify entries contain correct data
	entryMap := make(map[string]ToolAllowlistEntry)
	for _, e := range allowlist {
		entryMap[e.LogicalName] = e
	}

	sshEntry, ok := entryMap["prod-ssh"]
	if !ok {
		t.Fatal("expected prod-ssh in allowlist")
	}
	if sshEntry.InstanceID != sshInstance.ID {
		t.Errorf("expected instance ID %d, got %d", sshInstance.ID, sshEntry.InstanceID)
	}
	if sshEntry.ToolType != "ssh" {
		t.Errorf("expected tool type 'ssh', got '%s'", sshEntry.ToolType)
	}

	zbxEntry, ok := entryMap["prod-zabbix"]
	if !ok {
		t.Fatal("expected prod-zabbix in allowlist")
	}
	if zbxEntry.ToolType != "zabbix" {
		t.Errorf("expected tool type 'zabbix', got '%s'", zbxEntry.ToolType)
	}
}

func TestGetToolAllowlist_NoTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create skill without any tools
	db.Create(&database.Skill{Name: "basic-skill", Description: "No tools", Enabled: true, IsSystem: false})

	allowlist := svc.GetToolAllowlist()

	if len(allowlist) != 0 {
		t.Errorf("expected empty allowlist for skill with no tools, got %v", allowlist)
	}
}

func TestGetToolAllowlist_MultipleSkillsDeduplication(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create tool type and instance
	sshType := database.ToolType{Name: "ssh", Description: "SSH"}
	db.Create(&sshType)
	sshInstance := database.ToolInstance{
		Name:        "Shared SSH",
		LogicalName: "shared-ssh",
		ToolTypeID:  sshType.ID,
		Enabled:     true,
	}
	db.Create(&sshInstance)

	// Create two skills that share the same tool instance
	skill1 := database.Skill{Name: "skill-one", Description: "First", Enabled: true, IsSystem: false}
	db.Create(&skill1)
	db.Model(&skill1).Association("Tools").Append(&sshInstance)

	skill2 := database.Skill{Name: "skill-two", Description: "Second", Enabled: true, IsSystem: false}
	db.Create(&skill2)
	db.Model(&skill2).Association("Tools").Append(&sshInstance)

	allowlist := svc.GetToolAllowlist()

	// Should deduplicate — only one entry for shared-ssh
	if len(allowlist) != 1 {
		t.Fatalf("expected 1 entry (deduplicated), got %d", len(allowlist))
	}
	if allowlist[0].LogicalName != "shared-ssh" {
		t.Errorf("expected logical name 'shared-ssh', got '%s'", allowlist[0].LogicalName)
	}
}

func TestGetToolAllowlist_ExcludesSystemSkills(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create tool type and instance
	sshType := database.ToolType{Name: "ssh", Description: "SSH"}
	db.Create(&sshType)
	sshInstance := database.ToolInstance{
		Name:        "System SSH",
		LogicalName: "system-ssh",
		ToolTypeID:  sshType.ID,
		Enabled:     true,
	}
	db.Create(&sshInstance)

	// Create system skill with tool — should be excluded
	systemSkill := database.Skill{Name: "incident-manager", Description: "System", Enabled: true, IsSystem: true}
	db.Create(&systemSkill)
	db.Model(&systemSkill).Association("Tools").Append(&sshInstance)

	allowlist := svc.GetToolAllowlist()

	if len(allowlist) != 0 {
		t.Errorf("expected empty allowlist (system skills excluded), got %v", allowlist)
	}
}

func TestGetToolAllowlist_ExcludesDisabledToolInstances(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// Create tool type and disabled instance
	sshType := database.ToolType{Name: "ssh", Description: "SSH"}
	db.Create(&sshType)
	disabledInstance := database.ToolInstance{
		Name:        "Disabled SSH",
		LogicalName: "disabled-ssh",
		ToolTypeID:  sshType.ID,
		Enabled:     true,
	}
	db.Create(&disabledInstance)
	db.Model(&disabledInstance).Update("enabled", false)

	// Create skill with disabled tool
	skill := database.Skill{Name: "test-skill", Description: "Test", Enabled: true, IsSystem: false}
	db.Create(&skill)
	db.Model(&skill).Association("Tools").Append(&disabledInstance)

	allowlist := svc.GetToolAllowlist()

	if len(allowlist) != 0 {
		t.Errorf("expected empty allowlist (disabled tools excluded), got %v", allowlist)
	}
}
