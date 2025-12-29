package services

import (
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
// Skills use SKILL.md format internally for Codex compatibility
type SkillService struct {
	db             *gorm.DB
	dataDir        string // /akmatori - base data directory
	incidentsDir   string // /akmatori/incidents - incident working directories
	skillsDir      string // /akmatori/skills - skill definitions with SKILL.md
	toolsDir       string // /akmatori/tools - tool implementations
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
		toolsDir:       toolService.GetToolsDir(), // Use actual tools directory from config
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

// GetSkillReferencesDir returns the path to the skill's references directory
func (s *SkillService) GetSkillReferencesDir(skillName string) string {
	return filepath.Join(s.skillsDir, skillName, "references")
}

// EnsureSkillDirectories creates the skill's directory structure
func (s *SkillService) EnsureSkillDirectories(skillName string) error {
	skillDir := s.GetSkillDir(skillName)
	scriptsDir := s.GetSkillScriptsDir(skillName)
	referencesDir := s.GetSkillReferencesDir(skillName)

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(referencesDir, 0755); err != nil {
		return err
	}
	return nil
}

// EnsureSkillScriptsDir creates the scripts directory if it doesn't exist
func (s *SkillService) EnsureSkillScriptsDir(skillName string) error {
	scriptsDir := s.GetSkillScriptsDir(skillName)
	return os.MkdirAll(scriptsDir, 0755)
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

	// Generate and write SKILL.md
	skillMd := s.generateSkillMd(name, description, prompt)
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
		skillMd := s.generateSkillMd(name, description, body)
		if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
			log.Printf("Warning: failed to update SKILL.md: %v", err)
		}
	}

	return &skill, nil
}

// DeleteSkill removes a skill from both filesystem and database
func (s *SkillService) DeleteSkill(name string) error {
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

// GetSkill returns a skill by name
func (s *SkillService) GetSkill(name string) (*database.Skill, error) {
	var skill database.Skill
	if err := s.db.Preload("Tools").Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, fmt.Errorf("skill not found: %w", err)
	}
	return &skill, nil
}

// GetSkillPrompt reads the body (instructions) from SKILL.md
func (s *SkillService) GetSkillPrompt(name string) (string, error) {
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	// Parse and extract body (after frontmatter)
	parts := strings.SplitN(string(content), "---", 3)
	if len(parts) >= 3 {
		return strings.TrimSpace(parts[2]), nil
	}
	return string(content), nil
}

// UpdateSkillPrompt updates the body (instructions) in SKILL.md
func (s *SkillService) UpdateSkillPrompt(name, prompt string) error {
	// Get current skill metadata
	skill, err := s.GetSkill(name)
	if err != nil {
		return err
	}

	// Generate new SKILL.md with updated body
	skillMd := s.generateSkillMd(name, skill.Description, prompt)
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")

	if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
		return fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	return nil
}

// generateSkillMd generates a SKILL.md file with YAML frontmatter
func (s *SkillService) generateSkillMd(name, description, body string) string {
	frontmatter := SkillFrontmatter{
		Name:        name,
		Description: description,
		Metadata: map[string]string{
			"short-description": truncateString(description, 50),
		},
	}

	yamlBytes, _ := yaml.Marshal(frontmatter)

	return fmt.Sprintf("---\n%s---\n\n%s\n", string(yamlBytes), body)
}

// AssignTools assigns tools to a skill - creates symlinks in scripts/ and generates tools.md
func (s *SkillService) AssignTools(skillName string, toolIDs []uint) error {
	// Verify skill exists
	skill, err := s.GetSkill(skillName)
	if err != nil {
		return err
	}

	scriptsDir := s.GetSkillScriptsDir(skillName)
	referencesDir := s.GetSkillReferencesDir(skillName)

	// Ensure directories exist
	if err := s.EnsureSkillDirectories(skillName); err != nil {
		return fmt.Errorf("failed to ensure directories: %w", err)
	}

	// 1. Remove old tool symlinks (keep skill's own scripts)
	s.cleanToolSymlinks(scriptsDir)

	// 2. Get tool instances
	var tools []database.ToolInstance
	if len(toolIDs) > 0 {
		if err := s.db.Preload("ToolType").Where("id IN ?", toolIDs).Find(&tools).Error; err != nil {
			return fmt.Errorf("failed to get tools: %w", err)
		}
	}

	// 3. Create symlinks for each assigned tool
	for _, tool := range tools {
		if !tool.Enabled {
			continue
		}
		toolPath := filepath.Join(s.toolsDir, tool.ToolType.Name)
		symlinkPath := filepath.Join(scriptsDir, tool.ToolType.Name)

		// Only create symlink if tool directory exists
		if _, err := os.Stat(toolPath); err == nil {
			if err := os.Symlink(toolPath, symlinkPath); err != nil && !os.IsExist(err) {
				log.Printf("Warning: failed to symlink tool %s: %v", tool.ToolType.Name, err)
			}
		}
	}

	// 4. Generate references/tools.md
	toolsMd := s.generateToolsDocumentation(tools)
	toolsMdPath := filepath.Join(referencesDir, "tools.md")
	if err := os.WriteFile(toolsMdPath, []byte(toolsMd), 0644); err != nil {
		return fmt.Errorf("failed to write tools.md: %w", err)
	}

	// 5. Generate .env file with tool settings
	skillDir := s.GetSkillDir(skillName)
	if err := s.generateSkillEnvFile(skillDir, tools); err != nil {
		log.Printf("Warning: failed to generate .env file: %v", err)
	}

	// 6. Update database association
	if err := s.db.Model(skill).Association("Tools").Replace(tools); err != nil {
		return fmt.Errorf("failed to update tool associations: %w", err)
	}

	return nil
}

// cleanToolSymlinks removes tool symlinks from scripts directory (keeps regular files)
func (s *SkillService) cleanToolSymlinks(scriptsDir string) {
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			os.Remove(filepath.Join(scriptsDir, e.Name()))
		}
	}
}

// generateToolsDocumentation generates markdown documentation for assigned tools
func (s *SkillService) generateToolsDocumentation(tools []database.ToolInstance) string {
	if len(tools) == 0 {
		return "# Available Tools\n\nNo tools assigned to this skill.\n"
	}

	var sb strings.Builder
	sb.WriteString("# Available Tools\n\n")
	sb.WriteString("This document describes the tools available to this skill.\n\n")

	for _, tool := range tools {
		if !tool.Enabled {
			continue
		}

		sb.WriteString(fmt.Sprintf("## %s\n\n", tool.ToolType.Name))
		sb.WriteString(fmt.Sprintf("%s\n\n", tool.ToolType.Description))

		// Get detailed tool description if available
		if s.toolService != nil {
			toolDesc, err := s.toolService.GenerateToolDescription(tool.ToolType.Name)
			if err == nil && toolDesc != "" {
				sb.WriteString("### Functions\n\n")
				sb.WriteString(toolDesc)
				sb.WriteString("\n\n")
			}
		}

		// Add usage example
		sb.WriteString("### Usage\n\n")
		sb.WriteString("```python\n")
		sb.WriteString("#!/usr/bin/env python3\n")
		sb.WriteString("import sys\n")
		sb.WriteString("from pathlib import Path\n\n")
		sb.WriteString("# Setup path to scripts directory\n")
		sb.WriteString("ROOT = Path(__file__).resolve().parent\n")
		sb.WriteString("sys.path.insert(0, str(ROOT))\n\n")
		sb.WriteString(fmt.Sprintf("# Import from the %s tool\n", tool.ToolType.Name))
		sb.WriteString(fmt.Sprintf("from scripts.%s import *  # Import available functions\n\n", tool.ToolType.Name))
		sb.WriteString("# Use the functions...\n")
		sb.WriteString("```\n\n")
		sb.WriteString("---\n\n")
	}

	return sb.String()
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

// truncateString truncates a string to max length
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// IncidentContext contains context for spawning an incident manager
type IncidentContext struct {
	Source   string         // e.g., "slack", "zabbix"
	SourceID string         // e.g., thread_ts, alert_id
	Context  database.JSONB // Event details
	Message  string         // Original message/alert text for title generation
}

// SpawnIncidentManager creates a new incident manager instance
// Creates AGENTS.md in .codex/ directory and .codex/skills symlink to skills directory
func (s *SkillService) SpawnIncidentManager(ctx *IncidentContext) (string, string, error) {
	// Generate UUID for this incident
	incidentUUID := uuid.New().String()

	// Create incident directory
	incidentDir := filepath.Join(s.incidentsDir, incidentUUID)
	if err := os.MkdirAll(incidentDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create incident directory: %w", err)
	}

	// Create .codex directory
	codexDir := filepath.Join(incidentDir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create .codex directory: %w", err)
	}

	// Create .codex/skills symlink to skills directory for Codex skill discovery
	codexSkillsDir := filepath.Join(codexDir, "skills")
	if err := os.Symlink(s.skillsDir, codexSkillsDir); err != nil {
		return "", "", fmt.Errorf("failed to symlink skills dir: %w", err)
	}

	// Generate AGENTS.md in .codex/ directory
	agentsMdPath := filepath.Join(codexDir, "AGENTS.md")
	if err := s.generateIncidentAgentsMd(agentsMdPath); err != nil {
		return "", "", fmt.Errorf("failed to generate AGENTS.md: %w", err)
	}

	// Generate separate .env.{tool_name} files for each tool
	if err := s.generateIncidentEnvFiles(incidentDir); err != nil {
		log.Printf("Warning: failed to generate incident env files: %v", err)
	}

	// Generate title using LLM
	var title string
	if ctx.Message != "" {
		titleGen := NewTitleGenerator()
		generatedTitle, err := titleGen.GenerateTitle(ctx.Message, ctx.Source)
		if err != nil {
			log.Printf("Warning: Failed to generate incident title: %v", err)
			title = titleGen.GenerateFallbackTitle(ctx.Message, ctx.Source)
		} else {
			title = generatedTitle
		}
	}

	// Create incident record in database
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

	return incidentUUID, incidentDir, nil
}

// generateIncidentAgentsMd generates the AGENTS.md file for incident manager
func (s *SkillService) generateIncidentAgentsMd(path string) error {
	// Get incident manager config from database
	var config database.IncidentManagerConfig
	if err := s.db.First(&config).Error; err != nil {
		return fmt.Errorf("failed to get incident manager config: %w", err)
	}

	// Build AGENTS.md content
	var sb strings.Builder

	sb.WriteString("# Incident Manager\n\n")
	sb.WriteString(config.Prompt)
	sb.WriteString("\n\n")

	// Add tool environment files section
	toolEnvFiles := s.getAvailableToolEnvFiles()
	if len(toolEnvFiles) > 0 {
		sb.WriteString("## Tool Configuration Files\n\n")
		sb.WriteString("The following environment files contain credentials and settings for available tools:\n\n")
		sb.WriteString("| File | Tool | Description |\n")
		sb.WriteString("|------|------|-------------|\n")
		for _, envFile := range toolEnvFiles {
			sb.WriteString(fmt.Sprintf("| `.env.%s` | %s | %s |\n", envFile.Name, envFile.Name, envFile.Description))
		}
		sb.WriteString("\n")
		sb.WriteString("### Usage\n\n")
		sb.WriteString("Load tool credentials in Python scripts:\n\n")
		sb.WriteString("```python\n")
		sb.WriteString("from dotenv import load_dotenv\n")
		sb.WriteString("import os\n\n")
		sb.WriteString("# Load specific tool configuration\n")
		sb.WriteString("load_dotenv('.env.zabbix')  # For Zabbix tool\n\n")
		sb.WriteString("# Access variables as defined in tool settings\n")
		sb.WriteString("zabbix_url = os.getenv('ZABBIX_URL')\n")
		sb.WriteString("zabbix_token = os.getenv('ZABBIX_TOKEN')\n")
		sb.WriteString("```\n\n")
	}

	// Add structured output protocol
	sb.WriteString("## Structured Output Protocol\n\n")
	sb.WriteString("Use these structured blocks to communicate clearly:\n\n")

	sb.WriteString("### Final Result\n")
	sb.WriteString("```\n")
	sb.WriteString("status: resolved|unresolved|escalate\n")
	sb.WriteString("summary: Brief description of what was found/done\n")
	sb.WriteString("actions_taken:\n")
	sb.WriteString("- Action 1\n")
	sb.WriteString("- Action 2\n")
	sb.WriteString("recommendations:\n")
	sb.WriteString("- Recommendation 1\n")
	sb.WriteString("```\n\n")

	sb.WriteString("### Escalation\n")
	sb.WriteString("```\n")
	sb.WriteString("reason: Why escalation is needed\n")
	sb.WriteString("urgency: low|medium|high|critical\n")
	sb.WriteString("context: Relevant information\n")
	sb.WriteString("```\n\n")

	// Write to file
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	return nil
}

// toolEnvInfo contains metadata about a tool's env file
type toolEnvInfo struct {
	Name        string
	Description string
}

// getAvailableToolEnvFiles returns list of tool env files that will be generated
func (s *SkillService) getAvailableToolEnvFiles() []toolEnvInfo {
	skills, err := s.ListEnabledSkills()
	if err != nil {
		return nil
	}

	seenTools := make(map[uint]bool)
	var envFiles []toolEnvInfo

	for _, skill := range skills {
		var skillTools []database.SkillTool
		if err := s.db.Where("skill_id = ?", skill.ID).Find(&skillTools).Error; err != nil {
			continue
		}

		for _, st := range skillTools {
			if seenTools[st.ToolInstanceID] {
				continue
			}
			seenTools[st.ToolInstanceID] = true

			var tool database.ToolInstance
			if err := s.db.Preload("ToolType").First(&tool, st.ToolInstanceID).Error; err != nil {
				continue
			}

			if !tool.Enabled || tool.ToolType.ID == 0 {
				continue
			}

			envFiles = append(envFiles, toolEnvInfo{
				Name:        tool.ToolType.Name,
				Description: tool.ToolType.Description,
			})
		}
	}

	return envFiles
}

// generateToolConfig generates a configuration file for a tool in its lib directory
func (s *SkillService) generateToolConfig(libDir, toolName string, settings database.JSONB) error {
	configPath := filepath.Join(libDir, toolName, "config.env")

	// Convert settings to environment variable format
	var lines []string
	for key, value := range settings {
		// Convert to uppercase and snake_case
		envKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		lines = append(lines, fmt.Sprintf("%s=%v", envKey, value))
	}

	content := strings.Join(lines, "\n")

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// generateSkillEnvFile generates a .env file at the skill root directory with all tool settings
func (s *SkillService) generateSkillEnvFile(skillDir string, tools []database.ToolInstance) error {
	envPath := filepath.Join(skillDir, ".env")

	var lines []string
	lines = append(lines, "# Auto-generated environment file for skill tools")
	lines = append(lines, "# This file contains settings for all connected tools")
	lines = append(lines, "")

	for _, tool := range tools {
		if !tool.Enabled {
			continue
		}

		// Get tool type for the name
		var toolType database.ToolType
		if err := s.db.First(&toolType, tool.ToolTypeID).Error; err != nil {
			continue
		}

		// Add section header for this tool
		lines = append(lines, fmt.Sprintf("# %s Configuration", strings.ToUpper(toolType.Name)))

		// Write settings with uppercase keys
		for key, value := range tool.Settings {
			lines = append(lines, fmt.Sprintf("%s=%v", strings.ToUpper(key), value))
		}
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")

	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write .env file: %w", err)
	}

	return nil
}

// generateIncidentEnvFiles generates separate .env.{tool_name} files in the incident directory
// for each tool used by enabled skills (e.g., .env.zabbix, .env.proxmox)
func (s *SkillService) generateIncidentEnvFiles(incidentDir string) error {
	// Get all enabled skills with their tools
	skills, err := s.ListEnabledSkills()
	if err != nil {
		return fmt.Errorf("failed to get skills: %w", err)
	}

	seenTools := make(map[uint]bool) // Avoid duplicate tool files

	for _, skill := range skills {
		// Get tools for this skill
		var skillTools []database.SkillTool
		if err := s.db.Where("skill_id = ?", skill.ID).Find(&skillTools).Error; err != nil {
			continue
		}

		for _, st := range skillTools {
			if seenTools[st.ToolInstanceID] {
				continue
			}
			seenTools[st.ToolInstanceID] = true

			// Get tool instance with type
			var tool database.ToolInstance
			if err := s.db.Preload("ToolType").First(&tool, st.ToolInstanceID).Error; err != nil {
				continue
			}

			if !tool.Enabled || tool.ToolType.ID == 0 {
				continue
			}

			// Generate .env.{tool_name} file
			toolName := tool.ToolType.Name
			envPath := filepath.Join(incidentDir, ".env."+toolName)

			var lines []string
			lines = append(lines, fmt.Sprintf("# %s Configuration", strings.ToUpper(toolName)))
			lines = append(lines, "# Auto-generated - do not edit")
			lines = append(lines, "")

			// Write settings with uppercase keys
			for key, value := range tool.Settings {
				lines = append(lines, fmt.Sprintf("%s=%v", strings.ToUpper(key), value))
			}

			content := strings.Join(lines, "\n")

			if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
				log.Printf("Warning: failed to write %s: %v", envPath, err)
				continue
			}
		}
	}

	return nil
}

// UpdateIncidentStatus updates the status of an incident
func (s *SkillService) UpdateIncidentStatus(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string) error {
	updates := map[string]interface{}{
		"status":     status,
		"session_id": sessionID,
		"full_log":   fullLog,
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
