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
		&database.Incident{},
		&database.LLMSettings{},
		&database.Integration{},
		&database.Channel{},
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

	svc := NewSkillService(dataDir, nil, contextService, nil)
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
	err := svc.generateAgentsMd(tmpFile, "incident-manager", "test-incident-uuid", scopeKindIncident)
	if err != nil {
		t.Fatalf("generateAgentsMd failed: %v", err)
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
	err := svc.generateAgentsMd(tmpFile, "incident-manager", "test-incident-uuid", scopeKindIncident)
	if err != nil {
		t.Fatalf("generateAgentsMd failed: %v", err)
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
	err := svc.generateAgentsMd(tmpFile, "incident-manager", "test-incident-uuid", scopeKindIncident)
	if err != nil {
		t.Fatalf("generateAgentsMd failed: %v", err)
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
	err := svc.generateAgentsMd(tmpFile, "incident-manager", "test-incident-uuid", scopeKindIncident)
	if err != nil {
		t.Fatalf("generateAgentsMd failed: %v", err)
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

func TestGenerateIncidentAgentsMd_CronAgentHeader(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	tmpFile := filepath.Join(t.TempDir(), "AGENTS.md")
	err := svc.generateAgentsMd(tmpFile, "cron-agent", "test-incident-uuid", scopeKindRequest)
	if err != nil {
		t.Fatalf("generateAgentsMd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "# Cron Agent") {
		t.Errorf("AGENTS.md should contain '# Cron Agent' header, got:\n%s", contentStr)
	}
	if strings.Contains(contentStr, "# Incident Manager") {
		t.Errorf("AGENTS.md must not contain '# Incident Manager' for cron-agent, got:\n%s", contentStr)
	}
	if strings.Contains(contentStr, "Senior Incident Manager") {
		t.Errorf("AGENTS.md must not contain incident-manager framing for cron-agent, got:\n%s", contentStr)
	}
	if !strings.Contains(contentStr, database.DefaultCronAgentPrompt[:50]) {
		t.Errorf("AGENTS.md should embed DefaultCronAgentPrompt, got:\n%s", contentStr)
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

	result := svc.generateSkillMd("test-skill", "Test skill description", "Investigate the server") +
		generateConnectedToolsSkillMd(tools, nil, false)

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

	result := svc.generateSkillMd("test-skill", "Test skill description", "Investigate the server")

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

	result := svc.generateSkillMd("test-skill", "desc", "You are a specialist in database analysis.")

	if !strings.Contains(result, "You are a specialist in database analysis.") {
		t.Error("SKILL.md should contain user prompt body")
	}
}

func TestGenerateSkillMd_PointsAtConnectedTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	result := svc.generateSkillMd("test-skill", "desc", "prompt body")
	if strings.Contains(result, "## Assigned Tools") {
		t.Error("SKILL.md must not carry a per-skill tools section")
	}
	if !strings.Contains(result, "connected-tools/SKILL.md") {
		t.Error("SKILL.md should point at connected-tools/SKILL.md")
	}
	// The pointer is generated and must not survive a read-back of the prompt.
	if got := stripAutoGeneratedSections(strings.SplitN(result, "---\n\n", 2)[1]); strings.Contains(got, "connected-tools") {
		t.Errorf("pointer survived stripAutoGeneratedSections: %q", got)
	}
}

func TestGenerateSkillMd_NoToolsSection_WhenNoTools(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	result := svc.generateSkillMd("test-skill", "desc", "prompt body")

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

func TestExtractToolDetails_SSHCommandPolicy(t *testing.T) {
	tool := database.ToolInstance{
		Name:     "ssh-prod",
		ToolType: database.ToolType{Name: "ssh"},
		Settings: database.JSONB{
			"ssh_hosts":            []interface{}{map[string]interface{}{"hostname": "web-01", "address": "10.0.0.1"}},
			"ssh_allowed_commands": "systemctl restart nginx\r\n# comment\n\n journalctl -u nginx * \n",
			"ssh_denied_commands":  []interface{}{"reboot", " "},
		},
	}
	details := extractToolDetails(tool)
	for _, want := range []string{"`systemctl restart nginx`, `journalctl -u nginx *`", "Always denied, on every host: `reboot`"} {
		if !strings.Contains(details, want) {
			t.Errorf("details must contain %q:\n%s", want, details)
		}
	}
	if strings.Contains(details, "comment") {
		t.Error("comments must not be listed")
	}

	tool.Settings = database.JSONB{"ssh_hosts": tool.Settings["ssh_hosts"]}
	if d := extractToolDetails(tool); strings.Contains(d, "allowed on read-only") || strings.Contains(d, "denied") {
		t.Errorf("no policy: no policy lines, got:\n%s", d)
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
	_ = svc

	result := generateConnectedToolsSkillMd(tools, nil, false)

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
	if !strings.Contains(result, "list_tools_for_tool_type") {
		t.Error("SKILL.md should mention list_tools_for_tool_type for discovery")
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

func TestSkillScriptFileLifecycle(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	if err := svc.UpdateSkillScript("test-skill", "diagnose.sh", "echo ok\n"); err != nil {
		t.Fatalf("UpdateSkillScript() error = %v", err)
	}

	info, err := svc.GetSkillScript("test-skill", "diagnose.sh")
	if err != nil {
		t.Fatalf("GetSkillScript() error = %v", err)
	}
	if info.Filename != "diagnose.sh" {
		t.Errorf("Filename = %q, want diagnose.sh", info.Filename)
	}
	if info.Content != "echo ok\n" {
		t.Errorf("Content = %q, want script body", info.Content)
	}
	if info.Size != int64(len("echo ok\n")) {
		t.Errorf("Size = %d, want %d", info.Size, len("echo ok\n"))
	}
	if info.ModifiedAt.IsZero() {
		t.Error("ModifiedAt should be populated")
	}

	scripts, err := svc.ListSkillScripts("test-skill")
	if err != nil {
		t.Fatalf("ListSkillScripts() error = %v", err)
	}
	if len(scripts) != 1 || scripts[0] != "diagnose.sh" {
		t.Fatalf("ListSkillScripts() = %#v, want [diagnose.sh]", scripts)
	}

	if err := svc.DeleteSkillScript("test-skill", "diagnose.sh"); err != nil {
		t.Fatalf("DeleteSkillScript() error = %v", err)
	}
	if _, err := svc.GetSkillScript("test-skill", "diagnose.sh"); err == nil {
		t.Fatal("GetSkillScript() after delete error = nil, want not found")
	}
}

func TestSkillScriptListFiltersCacheAndHiddenEntries(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	scriptsDir := svc.GetSkillScriptsDir("test-skill")
	if err := os.MkdirAll(filepath.Join(scriptsDir, "__pycache__"), 0755); err != nil {
		t.Fatalf("create __pycache__: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, ".env"), []byte("secret"), 0644); err != nil {
		t.Fatalf("write hidden entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	scripts, err := svc.ListSkillScripts("test-skill")
	if err != nil {
		t.Fatalf("ListSkillScripts() error = %v", err)
	}
	if len(scripts) != 1 || scripts[0] != "run.py" {
		t.Fatalf("ListSkillScripts() = %#v, want [run.py]", scripts)
	}
}

func TestClearSkillScriptsRemovesRegularFilesOnly(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	scriptsDir := svc.GetSkillScriptsDir("test-skill")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("create scripts dir: %v", err)
	}
	regularPath := filepath.Join(scriptsDir, "cleanup.sh")
	if err := os.WriteFile(regularPath, []byte("echo cleanup\n"), 0644); err != nil {
		t.Fatalf("write regular script: %v", err)
	}
	targetPath := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(targetPath, []byte("tool"), 0644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	symlinkPath := filepath.Join(scriptsDir, "tool-link")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := svc.ClearSkillScripts("test-skill"); err != nil {
		t.Fatalf("ClearSkillScripts() error = %v", err)
	}
	if _, err := os.Stat(regularPath); !os.IsNotExist(err) {
		t.Fatalf("regular script still exists or stat failed: %v", err)
	}
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Fatalf("tool symlink should be preserved: %v", err)
	}
}

func TestSkillScriptRejectsUnsafeFilename(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	if err := svc.UpdateSkillScript("test-skill", "../escape.sh", "echo bad\n"); err == nil {
		t.Fatal("UpdateSkillScript() error = nil, want path traversal rejection")
	}
	if _, err := svc.GetSkillScript("test-skill", "nested/escape.sh"); err == nil {
		t.Fatal("GetSkillScript() error = nil, want path traversal rejection")
	}
	if err := svc.DeleteSkillScript("test-skill", "no-extension"); err == nil {
		t.Fatal("DeleteSkillScript() error = nil, want extension validation error")
	}
}

// withoutEventsTool checks that the built-in events tool is in an agent run's
// allowlist (every run gets it) and returns the other entries.
func withoutEventsTool(t *testing.T, entries []ToolAllowlistEntry) []ToolAllowlistEntry {
	t.Helper()
	out := make([]ToolAllowlistEntry, 0, len(entries))
	found := false
	for _, e := range entries {
		if e.InstanceID == 0 && e.LogicalName == "" && IsBuiltinToolNamespace(e.ToolType) {
			if e.ToolType == EventsToolType {
				found = true
			}
			continue
		}
		out = append(out, e)
	}
	if !found {
		t.Fatalf("allowlist %v lacks the built-in events tool", entries)
	}
	return out
}
