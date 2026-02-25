package services

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// SkillService manages skill spawning and lifecycle
// Skills use SKILL.md format with YAML frontmatter and user prompt body
type SkillService struct {
	db             *gorm.DB
	dataDir        string // /akmatori - base data directory
	incidentsDir   string // /akmatori/incidents - incident working directories
	skillsDir      string // /akmatori/skills - skill definitions with SKILL.md
	toolService    *ToolService
	contextService *ContextService
}

// NewSkillService creates a new skill service
func NewSkillService(dataDir string, toolService *ToolService, contextService *ContextService) *SkillService {
	return &SkillService{
		db:             database.GetDB(),
		dataDir:        dataDir,
		incidentsDir:   filepath.Join(dataDir, "incidents"),
		skillsDir:      filepath.Join(dataDir, "skills"),
		toolService:    toolService,
		contextService: contextService,
	}
}

// ValidateSkillName validates that skill name follows kebab-case format
func ValidateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("skill name must be 64 characters or less")
	}
	// Kebab-case: lowercase alphanumeric with hyphens, no consecutive hyphens, no leading/trailing hyphens
	matched, _ := regexp.MatchString(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`, name)
	if !matched {
		return fmt.Errorf("skill name must be kebab-case (e.g., 'zabbix-analyst', 'db-admin')")
	}
	return nil
}

// GetSkillDir returns the path to the skill's directory
func (s *SkillService) GetSkillDir(skillName string) string {
	return filepath.Join(s.skillsDir, skillName)
}

// GetSkillScriptsDir returns the path to the skill's scripts directory
func (s *SkillService) GetSkillScriptsDir(skillName string) string {
	return filepath.Join(s.skillsDir, skillName, "scripts")
}

// GetSkillAssetsDir returns the path to the skill's assets directory
func (s *SkillService) GetSkillAssetsDir(skillName string) string {
	return filepath.Join(s.skillsDir, skillName, "assets")
}

// EnsureSkillDirectories creates the skill's directory structure
func (s *SkillService) EnsureSkillDirectories(skillName string) error {
	skillDir := s.GetSkillDir(skillName)
	scriptsDir := s.GetSkillScriptsDir(skillName)
	assetsDir := s.GetSkillAssetsDir(skillName)

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return err
	}
	return nil
}

// EnsureSkillScriptsDir creates the scripts directory if it doesn't exist
func (s *SkillService) EnsureSkillScriptsDir(skillName string) error {
	scriptsDir := s.GetSkillScriptsDir(skillName)
	return os.MkdirAll(scriptsDir, 0755)
}

// SyncSkillAssets creates symlinks in the skill's assets directory for [[filename]] references
// Symlinks point to /akmatori/context/{filename} which is shared between API and agent containers
// It removes stale symlinks and adds new ones based on the current prompt
func (s *SkillService) SyncSkillAssets(skillName string, prompt string) error {
	assetsDir := s.GetSkillAssetsDir(skillName)

	// Ensure assets directory exists
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return fmt.Errorf("failed to create assets directory: %w", err)
	}

	// Get current references from prompt
	currentRefs := s.contextService.ParseReferences(prompt)
	currentRefSet := make(map[string]bool)
	for _, ref := range currentRefs {
		currentRefSet[ref] = true
	}

	// Clean up stale entries (files or symlinks no longer referenced)
	entries, err := os.ReadDir(assetsDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read assets directory: %w", err)
	}

	for _, entry := range entries {
		if !currentRefSet[entry.Name()] {
			entryPath := filepath.Join(assetsDir, entry.Name())
			os.Remove(entryPath)
		}
	}

	// Create symlinks for current references
	for _, filename := range currentRefs {
		srcPath := s.contextService.GetFilePath(filename)
		dstPath := filepath.Join(assetsDir, filename)

		// Check if source file exists
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			log.Printf("Warning: referenced file %s does not exist, skipping", filename)
			continue
		}

		// Remove existing entry (file or symlink) to ensure fresh symlink
		if _, err := os.Lstat(dstPath); err == nil {
			os.Remove(dstPath)
		}

		// Create symlink pointing to /akmatori/context/{filename}
		// This path is shared between API and codex containers
		symlinkTarget := filepath.Join("/akmatori/context", filename)
		if err := os.Symlink(symlinkTarget, dstPath); err != nil {
			return fmt.Errorf("failed to create symlink for %s: %w", filename, err)
		}
	}

	return nil
}

// ClearSkillScripts removes all scripts from the skill's scripts directory (keeps tool symlinks)
func (s *SkillService) ClearSkillScripts(skillName string) error {
	scriptsDir := s.GetSkillScriptsDir(skillName)
	entries, err := os.ReadDir(scriptsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Only remove regular files, keep symlinks (tools)
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := os.Remove(filepath.Join(scriptsDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListSkillScripts returns a list of files in the skill's persistent scripts directory
// It filters out Python cache directories like __pycache__
func (s *SkillService) ListSkillScripts(skillName string) ([]string, error) {
	scriptsDir := s.GetSkillScriptsDir(skillName)
	entries, err := os.ReadDir(scriptsDir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var scripts []string
	for _, e := range entries {
		// Skip Python cache directories and other hidden/cache entries
		name := e.Name()
		if name == "__pycache__" || strings.HasPrefix(name, ".") {
			continue
		}
		scripts = append(scripts, name)
	}
	return scripts, nil
}

// ValidateScriptFilename validates a script filename to prevent path traversal attacks
func ValidateScriptFilename(filename string) error {
	// Check for empty filename
	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}

	// Check for path traversal attempts
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return fmt.Errorf("invalid filename: path traversal not allowed")
	}

	// Only allow alphanumeric, underscore, dash, and dot characters
	for _, c := range filename {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return fmt.Errorf("invalid filename: only alphanumeric, underscore, dash, and dot characters allowed")
		}
	}

	// Must have an extension
	if !strings.Contains(filename, ".") {
		return fmt.Errorf("invalid filename: must have a file extension")
	}

	return nil
}

// ScriptInfo contains metadata about a script file
type ScriptInfo struct {
	Filename   string    `json:"filename"`
	Content    string    `json:"content"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// GetSkillScript reads a script file content
func (s *SkillService) GetSkillScript(skillName, filename string) (*ScriptInfo, error) {
	// Validate filename
	if err := ValidateScriptFilename(filename); err != nil {
		return nil, err
	}

	scriptPath := filepath.Join(s.GetSkillScriptsDir(skillName), filename)

	// Get file info
	info, err := os.Stat(scriptPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("script not found: %s", filename)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get script info: %w", err)
	}

	// Read file content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script: %w", err)
	}

	return &ScriptInfo{
		Filename:   filename,
		Content:    string(content),
		Size:       info.Size(),
		ModifiedAt: info.ModTime(),
	}, nil
}

// UpdateSkillScript writes content to a script file
func (s *SkillService) UpdateSkillScript(skillName, filename, content string) error {
	// Validate filename
	if err := ValidateScriptFilename(filename); err != nil {
		return err
	}

	// Ensure scripts directory exists
	if err := s.EnsureSkillScriptsDir(skillName); err != nil {
		return fmt.Errorf("failed to create scripts directory: %w", err)
	}

	scriptPath := filepath.Join(s.GetSkillScriptsDir(skillName), filename)

	// Write file content
	if err := os.WriteFile(scriptPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write script: %w", err)
	}

	return nil
}

// DeleteSkillScript removes a specific script
func (s *SkillService) DeleteSkillScript(skillName, filename string) error {
	// Validate filename
	if err := ValidateScriptFilename(filename); err != nil {
		return err
	}

	scriptPath := filepath.Join(s.GetSkillScriptsDir(skillName), filename)

	// Check if file exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("script not found: %s", filename)
	}

	// Remove the file
	if err := os.Remove(scriptPath); err != nil {
		return fmt.Errorf("failed to delete script: %w", err)
	}

	return nil
}

// SkillFrontmatter represents the YAML frontmatter of a SKILL.md file
type SkillFrontmatter struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
}

// CreateSkill creates a new skill with SKILL.md on filesystem and record in database
func (s *SkillService) CreateSkill(name, description, category, prompt string) (*database.Skill, error) {
	// Validate name
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}

	// Check if skill already exists in filesystem
	skillDir := s.GetSkillDir(name)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, fmt.Errorf("skill directory already exists: %s", name)
	}

	// Create directory structure
	if err := s.EnsureSkillDirectories(name); err != nil {
		return nil, fmt.Errorf("failed to create skill directories: %w", err)
	}

	// Sync asset symlinks for [[filename]] references in prompt
	if err := s.SyncSkillAssets(name, prompt); err != nil {
		log.Printf("Warning: failed to sync skill assets: %v", err)
		// Continue even if asset sync fails - it's not critical
	}

	// Generate and write SKILL.md (no tools yet for new skill)
	skillMd := s.generateSkillMd(name, description, prompt, nil)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
		return nil, fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	// Create database record
	skill := &database.Skill{
		Name:        name,
		Description: description,
		Category:    category,
		Enabled:     true,
	}

	if err := s.db.Create(skill).Error; err != nil {
		// Clean up filesystem on DB error
		os.RemoveAll(skillDir)
		return nil, fmt.Errorf("failed to create skill record: %w", err)
	}

	return skill, nil
}

// UpdateSkill updates a skill's metadata and optionally the SKILL.md
func (s *SkillService) UpdateSkill(name string, description, category string, enabled bool) (*database.Skill, error) {
	var skill database.Skill
	if err := s.db.Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, fmt.Errorf("skill not found: %w", err)
	}

	// Update database record
	skill.Description = description
	skill.Category = category
	skill.Enabled = enabled

	if err := s.db.Save(&skill).Error; err != nil {
		return nil, fmt.Errorf("failed to update skill: %w", err)
	}

	// Update SKILL.md frontmatter (read existing, update frontmatter, preserve body)
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		body, _ := s.GetSkillPrompt(name)
		tools := s.getSkillTools(name)
		skillMd := s.generateSkillMd(name, description, body, tools)
		if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
			log.Printf("Warning: failed to update SKILL.md: %v", err)
		}
	}

	return &skill, nil
}

// DeleteSkill removes a skill from both filesystem and database
// System skills cannot be deleted
func (s *SkillService) DeleteSkill(name string) error {
	// Check if skill is a system skill
	var skill database.Skill
	if err := s.db.Where("name = ?", name).First(&skill).Error; err != nil {
		return fmt.Errorf("skill not found: %w", err)
	}

	if skill.IsSystem {
		return fmt.Errorf("cannot delete system skill: %s", name)
	}

	// Delete from database
	if err := s.db.Where("name = ?", name).Delete(&database.Skill{}).Error; err != nil {
		return fmt.Errorf("failed to delete skill from database: %w", err)
	}

	// Delete from filesystem
	skillDir := s.GetSkillDir(name)
	if err := os.RemoveAll(skillDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete skill directory: %w", err)
	}

	return nil
}



// ListSkills returns all skills from the database
func (s *SkillService) ListSkills() ([]database.Skill, error) {
	var skills []database.Skill
	if err := s.db.Preload("Tools").Find(&skills).Error; err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	return skills, nil
}

// ListEnabledSkills returns all enabled skills
func (s *SkillService) ListEnabledSkills() ([]database.Skill, error) {
	var skills []database.Skill
	if err := s.db.Preload("Tools").Where("enabled = ?", true).Find(&skills).Error; err != nil {
		return nil, fmt.Errorf("failed to list enabled skills: %w", err)
	}
	return skills, nil
}

// GetEnabledSkillNames returns the names of all enabled, non-system skills.
// Used to tell the agent worker which skills it should load from the shared skills directory.
func (s *SkillService) GetEnabledSkillNames() []string {
	skills, err := s.ListEnabledSkills()
	if err != nil {
		return nil
	}
	var names []string
	for _, sk := range skills {
		if !sk.IsSystem {
			names = append(names, sk.Name)
		}
	}
	return names
}

// GetSkill returns a skill by name
func (s *SkillService) GetSkill(name string) (*database.Skill, error) {
	var skill database.Skill
	if err := s.db.Preload("Tools").Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, fmt.Errorf("skill not found: %w", err)
	}
	return &skill, nil
}

// GetSkillPrompt reads the prompt for a skill
// For incident-manager system skill, returns the hardcoded default
// For regular skills, reads from SKILL.md file
func (s *SkillService) GetSkillPrompt(name string) (string, error) {
	// Incident-manager uses hardcoded prompt (not editable)
	if name == "incident-manager" {
		return database.DefaultIncidentManagerPrompt, nil
	}

	// Regular skill - read from SKILL.md
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	// Parse and extract body (after frontmatter)
	parts := strings.SplitN(string(content), "---", 3)
	if len(parts) >= 3 {
		body := strings.TrimSpace(parts[2])
		// Strip auto-generated resource instructions section if present
		body = stripAutoGeneratedSections(body)
		return body, nil
	}
	return string(content), nil
}

// stripAutoGeneratedSections removes auto-generated sections from the skill body
// to get only the user-defined prompt. Strips both old "Quick Start" (Python imports)
// and new "Assigned Tools" sections.
func stripAutoGeneratedSections(body string) string {
	// Strip old-format "Quick Start" section (Python imports from pre-pi-mono era)
	const quickStartMarker = "## Quick Start"
	const endMarker = "---\n"

	if strings.HasPrefix(body, quickStartMarker) {
		idx := strings.Index(body, endMarker)
		if idx == -1 {
			return body
		}
		body = strings.TrimSpace(body[idx+len(endMarker):])
		return stripAutoGeneratedSections(body)
	}

	// Strip new-format "Assigned Tools" section (auto-generated tool list)
	const toolsMarker = "\n\n## Assigned Tools\n"
	if idx := strings.Index(body, toolsMarker); idx != -1 {
		body = strings.TrimSpace(body[:idx])
	}

	return body
}

// UpdateSkillPrompt updates the prompt for a skill
// For incident-manager system skill, this is a no-op (prompt is hardcoded)
// For regular skills, writes to SKILL.md file
func (s *SkillService) UpdateSkillPrompt(name, prompt string) error {
	// Incident-manager prompt is hardcoded, can't be updated
	if name == "incident-manager" {
		return nil
	}

	// Regular skill - write to SKILL.md
	skill, err := s.GetSkill(name)
	if err != nil {
		return err
	}

	// Sync asset symlinks for [[filename]] references in prompt
	if err := s.SyncSkillAssets(name, prompt); err != nil {
		log.Printf("Warning: failed to sync skill assets: %v", err)
		// Continue even if asset sync fails - it's not critical
	}

	// Generate new SKILL.md with updated body and current tools
	tools := s.getSkillTools(name)
	skillMd := s.generateSkillMd(name, skill.Description, prompt, tools)
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")

	if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
		return fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	return nil
}

// generateSkillMd generates a SKILL.md file with YAML frontmatter and user prompt body
// Tools are called via Python wrappers through the bash tool, with usage examples per tool type
func (s *SkillService) generateSkillMd(name, description, body string, tools []database.ToolInstance) string {
	frontmatter := SkillFrontmatter{
		Name:        name,
		Description: description,
		Metadata: map[string]string{
			"short-description": truncateString(description, 50),
		},
	}

	yamlBytes, err := yaml.Marshal(frontmatter)
	if err != nil {
		log.Printf("Failed to marshal SKILL.md frontmatter for %s: %v", name, err)
		yamlBytes = []byte(fmt.Sprintf("name: %s\n", name))
	}

	// Transform [[filename]] references to markdown links [filename](assets/filename)
	resolvedBody := s.contextService.ResolveReferencesToMarkdownLinks(body)

	// List assigned tools with Python usage examples for per-skill routing
	var toolsSection strings.Builder
	var enabledTools []database.ToolInstance
	for _, tool := range tools {
		if tool.Enabled && tool.ToolType.ID != 0 {
			enabledTools = append(enabledTools, tool)
		}
	}
	if len(enabledTools) > 0 {
		toolsSection.WriteString("\n\n## Assigned Tools\n")
		for _, tool := range enabledTools {
			toolsSection.WriteString(fmt.Sprintf("\n### %s (ID: %d, type: %s)\n", tool.Name, tool.ID, tool.ToolType.Name))
			if details := extractToolDetails(tool); details != "" {
				toolsSection.WriteString(details)
			}
			toolsSection.WriteString(generateToolUsageExample(tool))
		}
	}

	return fmt.Sprintf("---\n%s---\n\n%s%s\n", string(yamlBytes), resolvedBody, toolsSection.String())
}

// generateToolUsageExample creates Python code block showing how to call the tool
func generateToolUsageExample(tool database.ToolInstance) string {
	typeName := tool.ToolType.Name
	id := tool.ID

	switch typeName {
	case "ssh":
		return fmt.Sprintf(`
Usage (via bash tool):
`+"```python"+`
from ssh import execute_command, test_connectivity, get_server_info

result = execute_command("uptime", tool_instance_id=%d)
result = test_connectivity(tool_instance_id=%d)
result = get_server_info(tool_instance_id=%d)
`+"```"+`
`, id, id, id)
	case "zabbix":
		return fmt.Sprintf(`
Usage (via bash tool):
`+"```python"+`
from zabbix import get_hosts, get_problems, get_history, get_items, get_items_batch, get_triggers, api_request

result = get_hosts(tool_instance_id=%d)
result = get_problems(severity_min=3, tool_instance_id=%d)
result = get_items_batch(searches=["cpu", "memory"], tool_instance_id=%d)
result = get_triggers(hostids=["12345"], only_true=True, tool_instance_id=%d)
`+"```"+`
`, id, id, id, id)
	default:
		return fmt.Sprintf("When using %s tools, pass `tool_instance_id: %d` to target this instance.\n", typeName, id)
	}
}

// extractToolDetails extracts non-secret, agent-relevant details from a tool instance's settings.
// For SSH: lists configured hostnames so the agent knows which servers it can target.
// For other tool types (zabbix, etc.): no extra details needed — the agent interacts via MCP Gateway
// tools and doesn't need internal connection details like URLs or endpoints.
func extractToolDetails(tool database.ToolInstance) string {
	if tool.Settings == nil {
		return ""
	}

	typeName := tool.ToolType.Name

	switch typeName {
	case "ssh":
		// Extract hostnames from ssh_hosts array — agent needs these for server targeting
		hostsData, ok := tool.Settings["ssh_hosts"]
		if !ok {
			return ""
		}
		hostsJSON, err := json.Marshal(hostsData)
		if err != nil {
			return ""
		}
		var hosts []map[string]interface{}
		if err := json.Unmarshal(hostsJSON, &hosts); err != nil {
			return ""
		}
		var hostnames []string
		for _, h := range hosts {
			if hostname, ok := h["hostname"].(string); ok && hostname != "" {
				hostnames = append(hostnames, hostname)
			}
		}
		if len(hostnames) > 0 {
			return fmt.Sprintf("Configured hosts: %s\n", strings.Join(hostnames, ", "))
		}
	}

	return ""
}

// getSkillTools fetches tool instances for a skill from the database
func (s *SkillService) getSkillTools(skillName string) []database.ToolInstance {
	skill, err := s.GetSkill(skillName)
	if err != nil {
		return nil
	}

	var skillTools []database.SkillTool
	if err := s.db.Where("skill_id = ?", skill.ID).Find(&skillTools).Error; err != nil {
		return nil
	}

	var tools []database.ToolInstance
	for _, st := range skillTools {
		var tool database.ToolInstance
		if err := s.db.Preload("ToolType").First(&tool, st.ToolInstanceID).Error; err != nil {
			continue
		}
		if tool.Enabled && tool.ToolType.ID != 0 {
			tools = append(tools, tool)
		}
	}
	return tools
}

// AssignTools assigns tools to a skill and regenerates SKILL.md
// Tools are registered as pi-mono ToolDefinition objects at session creation time,
// so no symlink creation is needed (Python wrappers are removed)
func (s *SkillService) AssignTools(skillName string, toolIDs []uint) error {
	// Verify skill exists
	skill, err := s.GetSkill(skillName)
	if err != nil {
		return err
	}

	// Get tool instances
	var tools []database.ToolInstance
	if len(toolIDs) > 0 {
		if err := s.db.Preload("ToolType").Where("id IN ?", toolIDs).Find(&tools).Error; err != nil {
			return fmt.Errorf("failed to get tools: %w", err)
		}
	}

	// Update database association
	// Tool credentials are fetched by MCP Gateway at execution time for security
	if err := s.db.Model(skill).Association("Tools").Replace(tools); err != nil {
		return fmt.Errorf("failed to update tool associations: %w", err)
	}

	// Regenerate SKILL.md with updated tool list
	prompt, _ := s.GetSkillPrompt(skillName)
	skillMd := s.generateSkillMd(skillName, skill.Description, prompt, tools)
	skillPath := filepath.Join(s.GetSkillDir(skillName), "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
		return fmt.Errorf("failed to regenerate SKILL.md: %w", err)
	}

	return nil
}

// SyncSkillsFromFilesystem scans the skills directory and syncs to database
func (s *SkillService) SyncSkillsFromFilesystem() error {
	entries, err := os.ReadDir(s.skillsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read skills directory: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

		// Skip if skill already exists in database
		var count int64
		s.db.Model(&database.Skill{}).Where("name = ?", name).Count(&count)
		if count > 0 {
			continue
		}

		// Read SKILL.md to get metadata
		skillPath := filepath.Join(s.skillsDir, name, "SKILL.md")
		content, err := os.ReadFile(skillPath)
		if err != nil {
			log.Printf("Warning: no SKILL.md for skill %s: %v", name, err)
			continue
		}

		// Parse frontmatter
		parts := strings.SplitN(string(content), "---", 3)
		if len(parts) < 3 {
			log.Printf("Warning: invalid SKILL.md format for skill %s", name)
			continue
		}

		var frontmatter SkillFrontmatter
		if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
			log.Printf("Warning: failed to parse frontmatter for skill %s: %v", name, err)
			continue
		}

		// Create database record
		skill := &database.Skill{
			Name:        name,
			Description: frontmatter.Description,
			Enabled:     true,
		}
		if err := s.db.Create(skill).Error; err != nil {
			log.Printf("Warning: failed to sync skill %s: %v", name, err)
		} else {
			log.Printf("Synced skill from filesystem: %s", name)
		}
	}

	return nil
}

// RegenerateAllSkillMds regenerates SKILL.md files for all enabled skills.
// For skills that exist on disk, it updates the SKILL.md with the latest template.
// For skills that only exist in the database (no directory on disk), it materializes
// the directory and writes a SKILL.md so pi-mono's DefaultResourceLoader can discover them.
func (s *SkillService) RegenerateAllSkillMds() error {
	// Ensure the skills directory exists
	if err := os.MkdirAll(s.skillsDir, 0755); err != nil {
		return fmt.Errorf("failed to create skills directory: %w", err)
	}

	// Get all enabled skills from the database (source of truth)
	var skills []database.Skill
	if err := s.db.Where("enabled = ?", true).Find(&skills).Error; err != nil {
		return fmt.Errorf("failed to list skills: %w", err)
	}

	for _, skill := range skills {
		// Skip incident-manager (system skill, handled by AGENTS.md)
		if skill.Name == "incident-manager" {
			continue
		}

		// Ensure skill directory exists on disk
		if err := s.EnsureSkillDirectories(skill.Name); err != nil {
			log.Printf("Warning: failed to create directories for skill %s: %v", skill.Name, err)
			continue
		}

		// Get current prompt from SKILL.md if it exists, otherwise use description
		prompt, err := s.GetSkillPrompt(skill.Name)
		if err != nil {
			// No SKILL.md yet — use description as initial prompt body
			prompt = skill.Description
		}

		// Sync asset symlinks for [[filename]] references in prompt
		if err := s.SyncSkillAssets(skill.Name, prompt); err != nil {
			log.Printf("Warning: failed to sync assets for skill %s: %v", skill.Name, err)
		}

		// Get tools for this skill
		tools := s.getSkillTools(skill.Name)

		// Write SKILL.md with frontmatter + prompt body
		skillMd := s.generateSkillMd(skill.Name, skill.Description, prompt, tools)
		skillPath := filepath.Join(s.GetSkillDir(skill.Name), "SKILL.md")

		if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
			log.Printf("Warning: failed to regenerate SKILL.md for %s: %v", skill.Name, err)
			continue
		}

		log.Printf("Regenerated SKILL.md for skill: %s", skill.Name)
	}

	return nil
}

// truncateString truncates a string to max rune length, safe for multi-byte UTF-8
func truncateString(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

// IncidentContext contains context for spawning an incident manager
type IncidentContext struct {
	Source   string         // e.g., "slack", "zabbix"
	SourceID string         // e.g., thread_ts, alert_id
	Context  database.JSONB // Event details
	Message  string         // Original message/alert text for title generation
}

// SpawnIncidentManager creates a new incident manager instance
// Creates AGENTS.md at workspace root (pi-mono reads it from cwd upward)
func (s *SkillService) SpawnIncidentManager(ctx *IncidentContext) (string, string, error) {
	// Generate UUID for this incident
	incidentUUID := uuid.New().String()

	// Create incident directory with 0777 permissions so agent worker (UID 1001) can create files
	incidentDir := filepath.Join(s.incidentsDir, incidentUUID)
	if err := os.MkdirAll(incidentDir, 0777); err != nil {
		return "", "", fmt.Errorf("failed to create incident directory: %w", err)
	}
	// Ensure directory has correct permissions even if parent existed
	if err := os.Chmod(incidentDir, 0777); err != nil {
		log.Printf("Failed to chmod incident directory %s: %v", incidentDir, err)
	}

	// Generate AGENTS.md at workspace root (pi-mono reads agentDir from cwd)
	agentsMdPath := filepath.Join(incidentDir, "AGENTS.md")
	if err := s.generateIncidentAgentsMd(agentsMdPath); err != nil {
		return "", "", fmt.Errorf("failed to generate AGENTS.md: %w", err)
	}

	// NOTE: Tool credentials are NOT written to incident directory
	// They are fetched by MCP Gateway at execution time for security

	// Use fast fallback title immediately to avoid blocking on LLM call.
	// The LLM-generated title is updated asynchronously in the background.
	titleGen := NewTitleGenerator()
	title := titleGen.GenerateFallbackTitle(ctx.Message, ctx.Source)

	// Create incident record in database with fallback title
	incident := &database.Incident{
		UUID:       incidentUUID,
		Source:     ctx.Source,
		SourceID:   ctx.SourceID,
		Title:      title,
		Status:     database.IncidentStatusPending,
		Context:    ctx.Context,
		WorkingDir: incidentDir, // Working dir is incident root
	}

	if err := s.db.Create(incident).Error; err != nil {
		return "", "", fmt.Errorf("failed to create incident record: %w", err)
	}

	// Generate LLM title in background and update DB when ready
	if ctx.Message != "" && len(ctx.Message) >= 10 {
		go func() {
			generatedTitle, err := titleGen.GenerateTitle(ctx.Message, ctx.Source)
			if err != nil {
				log.Printf("Warning: Background title generation failed for %s: %v", incidentUUID, err)
				return
			}
			if generatedTitle != "" && generatedTitle != title {
				if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
					Update("title", generatedTitle).Error; err != nil {
					log.Printf("Warning: Failed to update incident title for %s: %v", incidentUUID, err)
				} else {
					log.Printf("Updated incident %s title: %s", incidentUUID, generatedTitle)
				}
			}
		}()
	}

	return incidentUUID, incidentDir, nil
}

// generateIncidentAgentsMd generates the AGENTS.md file for incident manager
// pi-mono reads this file from the workspace root (agentDir parameter)
// Skills are discovered by pi-mono's DefaultResourceLoader via additionalSkillPaths,
// so only the incident manager prompt is written here.
func (s *SkillService) generateIncidentAgentsMd(path string) error {
	// Get incident manager prompt from the system skill
	prompt, err := s.GetSkillPrompt("incident-manager")
	if err != nil {
		// Fallback to default if skill file doesn't exist yet
		prompt = database.DefaultIncidentManagerPrompt
	}

	var sb strings.Builder
	sb.WriteString("# Incident Manager\n\n")
	sb.WriteString(prompt)
	sb.WriteString("\n")

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	return nil
}

// NOTE: formatEnvValue and fixPEMKey were removed as unused. They handled
// .env file value formatting with base64 encoding for multiline values and
// PEM key reconstruction. See mcp-gateway/internal/tools/ssh/ssh.go for
// a working fixPEMKey implementation if needed.

// UpdateIncidentStatus updates the status of an incident.
// Only sets session_id and full_log when non-empty to avoid overwriting existing values.
func (s *SkillService) UpdateIncidentStatus(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if sessionID != "" {
		updates["session_id"] = sessionID
	}
	if fullLog != "" {
		updates["full_log"] = fullLog
	}

	// Set completed_at timestamp when incident is completed or failed
	if status == database.IncidentStatusCompleted || status == database.IncidentStatusFailed {
		now := time.Now()
		updates["completed_at"] = &now
	}

	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update incident status: %w", err)
	}

	return nil
}

// UpdateIncidentComplete updates the incident with final status, log, and response
func (s *SkillService) UpdateIncidentComplete(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string, response string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"status":       status,
		"session_id":   sessionID,
		"full_log":     fullLog,
		"response":     response,
		"completed_at": &now,
	}

	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update incident: %w", err)
	}

	return nil
}

// UpdateIncidentLog updates only the full_log field of an incident (for progress tracking)
func (s *SkillService) UpdateIncidentLog(incidentUUID string, fullLog string) error {
	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Update("full_log", fullLog).Error; err != nil {
		return fmt.Errorf("failed to update incident log: %w", err)
	}
	return nil
}

// GetIncident retrieves an incident by UUID
func (s *SkillService) GetIncident(incidentUUID string) (*database.Incident, error) {
	var incident database.Incident
	if err := s.db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		return nil, fmt.Errorf("incident not found: %w", err)
	}
	return &incident, nil
}

// SubagentSummaryInput contains the outcome of a subagent execution for context management
type SubagentSummaryInput struct {
	SkillName     string
	Success       bool
	Output        string   // Final output from the subagent
	FullLog       string   // Complete reasoning log (for database storage)
	ErrorMessages []string // Error messages if failed
	TokensUsed    int
}

// SummarizeSubagentForContext creates a concise summary for the incident manager's context
// This implements failure isolation - failed attempts don't pollute the main context
func SummarizeSubagentForContext(result *SubagentSummaryInput) string {
	if result.Success {
		// For successful runs, include just the final output (not full reasoning)
		return fmt.Sprintf(`
=== Subagent [%s] Result ===
Status: SUCCESS
Output:
%s
=== End [%s] ===
`, result.SkillName, result.Output, result.SkillName)
	}

	// For failed runs, provide minimal context to avoid polluting the LLM's context
	// The incident manager should try a different approach, not retry the same thing
	errorSummary := "Unknown error"
	if len(result.ErrorMessages) > 0 {
		// Take just the first error message, truncated
		errorSummary = result.ErrorMessages[0]
		if len(errorSummary) > 200 {
			errorSummary = errorSummary[:200] + "..."
		}
	}

	return fmt.Sprintf(`
=== Subagent [%s] Result ===
Status: FAILED
Error: %s
Note: The full reasoning log is stored but not shown here to keep context clean.
      Consider trying a different approach or skill.
=== End [%s] ===
`, result.SkillName, errorSummary, result.SkillName)
}

// AppendSubagentLog appends a subagent's reasoning log to the incident's full_log
// This stores the FULL log in the database for debugging/review purposes
func (s *SkillService) AppendSubagentLog(incidentUUID string, skillName string, subagentLog string) error {
	// Get current incident
	incident, err := s.GetIncident(incidentUUID)
	if err != nil {
		return err
	}

	// Format subagent log with markers
	formattedLog := fmt.Sprintf("\n\n--- Subagent [%s] Reasoning Log ---\n%s\n--- End Subagent [%s] Reasoning Log ---\n",
		skillName, subagentLog, skillName)

	// Append to existing log
	newLog := incident.FullLog + formattedLog

	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Update("full_log", newLog).Error; err != nil {
		return fmt.Errorf("failed to append subagent log: %w", err)
	}

	return nil
}
