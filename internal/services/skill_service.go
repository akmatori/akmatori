package services

import (
	"encoding/base64"
	"fmt"
	"io"
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
		body = stripResourceInstructions(body)
		return body, nil
	}
	return string(content), nil
}

// stripResourceInstructions removes the auto-generated "Quick Start" section
// from the skill body to get only the user-defined prompt
func stripResourceInstructions(body string) string {
	const marker = "## Quick Start"
	const endMarker = "---\n"

	if strings.HasPrefix(body, marker) {
		// Find where the auto-generated section ends
		idx := strings.Index(body, endMarker)
		if idx == -1 {
			return body
		}
		// Return everything after the end marker, trimmed
		body = strings.TrimSpace(body[idx+len(endMarker):])
		// Check again in case there are multiple sections (from bug)
		return stripResourceInstructions(body)
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

	// Generate new SKILL.md with updated body and current tools
	tools := s.getSkillTools(name)
	skillMd := s.generateSkillMd(name, skill.Description, prompt, tools)
	skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")

	if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
		return fmt.Errorf("failed to write SKILL.md: %w", err)
	}

	return nil
}

// generateSkillMd generates a SKILL.md file with YAML frontmatter
// Tools parameter embeds import paths directly in Quick Start section
func (s *SkillService) generateSkillMd(name, description, body string, tools []database.ToolInstance) string {
	frontmatter := SkillFrontmatter{
		Name:        name,
		Description: description,
		Metadata: map[string]string{
			"short-description": truncateString(description, 50),
		},
	}

	yamlBytes, _ := yaml.Marshal(frontmatter)

	// Generate Quick Start with embedded imports (no tools.md reference)
	var quickStart strings.Builder
	quickStart.WriteString("## Quick Start\n\n")

	// Filter enabled tools
	var enabledTools []database.ToolInstance
	for _, tool := range tools {
		if tool.Enabled && tool.ToolType.ID != 0 {
			enabledTools = append(enabledTools, tool)
		}
	}

	if len(enabledTools) > 0 {
		quickStart.WriteString("```python\n")
		quickStart.WriteString(fmt.Sprintf("import sys; sys.path.insert(0, './.codex/skills/%s')\n", name))

		for _, tool := range enabledTools {
			toolName := tool.ToolType.Name
			switch toolName {
			case "ssh":
				quickStart.WriteString("from scripts.ssh import execute_command, test_connectivity\n")
			case "zabbix":
				quickStart.WriteString("from scripts.zabbix import get_hosts, get_problems, get_history\n")
			default:
				quickStart.WriteString(fmt.Sprintf("from scripts.%s import *\n", toolName))
			}
		}

		quickStart.WriteString("\n# Full API docs: help(execute_command)\n")
		quickStart.WriteString("```\n")
	} else {
		quickStart.WriteString("No tools assigned.\n")
	}
	quickStart.WriteString("\n---\n\n")

	return fmt.Sprintf("---\n%s---\n\n%s%s\n", string(yamlBytes), quickStart.String(), body)
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

// AssignTools assigns tools to a skill, creates symlinks, and regenerates SKILL.md
// NOTE: Tools are executed via MCP Gateway, not as local scripts
func (s *SkillService) AssignTools(skillName string, toolIDs []uint) error {
	// Verify skill exists
	skill, err := s.GetSkill(skillName)
	if err != nil {
		return err
	}

	// Ensure directories exist
	if err := s.EnsureSkillDirectories(skillName); err != nil {
		return fmt.Errorf("failed to ensure directories: %w", err)
	}

	// Get tool instances
	var tools []database.ToolInstance
	if len(toolIDs) > 0 {
		if err := s.db.Preload("ToolType").Where("id IN ?", toolIDs).Find(&tools).Error; err != nil {
			return fmt.Errorf("failed to get tools: %w", err)
		}
	}

	// Update database association first
	// NOTE: Tool credentials are NOT written to skill directories
	// They are fetched by MCP Gateway at execution time for security
	if err := s.db.Model(skill).Association("Tools").Replace(tools); err != nil {
		return fmt.Errorf("failed to update tool associations: %w", err)
	}

	// Create symlinks for tools in the scripts directory
	scriptsDir := filepath.Join(s.GetSkillDir(skillName), "scripts")

	// Clear existing symlinks
	entries, err := os.ReadDir(scriptsDir)
	if err == nil {
		for _, entry := range entries {
			entryPath := filepath.Join(scriptsDir, entry.Name())
			if info, err := os.Lstat(entryPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				os.Remove(entryPath)
			}
		}
	}

	// Create new symlinks for assigned tools
	for _, tool := range tools {
		if tool.ToolType.Name == "" {
			continue
		}
		toolName := tool.ToolType.Name
		linkPath := filepath.Join(scriptsDir, toolName)
		targetPath := filepath.Join("/tools", toolName)

		// Create symlink
		if err := os.Symlink(targetPath, linkPath); err != nil {
			log.Printf("Warning: failed to create symlink for tool %s: %v", toolName, err)
		}
	}

	// Regenerate SKILL.md with embedded tool imports
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

// RegenerateAllSkillMds regenerates SKILL.md files for all skills
// This updates existing skills with the latest template (e.g., resource instructions)
func (s *SkillService) RegenerateAllSkillMds() error {
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

		// Skip incident-manager (system skill)
		if name == "incident-manager" {
			continue
		}

		// Get skill from database to get description
		var skill database.Skill
		if err := s.db.Where("name = ?", name).First(&skill).Error; err != nil {
			continue // Skip skills not in database
		}

		// Get current prompt (strips auto-generated sections)
		prompt, err := s.GetSkillPrompt(name)
		if err != nil {
			log.Printf("Warning: failed to get prompt for skill %s: %v", name, err)
			continue
		}

		// Get tools for this skill
		tools := s.getSkillTools(name)

		// Regenerate SKILL.md with embedded tool imports
		skillMd := s.generateSkillMd(name, skill.Description, prompt, tools)
		skillPath := filepath.Join(s.GetSkillDir(name), "SKILL.md")

		if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
			log.Printf("Warning: failed to regenerate SKILL.md for %s: %v", name, err)
			continue
		}

		log.Printf("Regenerated SKILL.md for skill: %s", name)

		// Ensure scripts directory exists and create tool symlinks
		scriptsDir := filepath.Join(s.GetSkillDir(name), "scripts")
		os.MkdirAll(scriptsDir, 0755)

		// Clear existing symlinks
		dirEntries, _ := os.ReadDir(scriptsDir)
		for _, de := range dirEntries {
			entryPath := filepath.Join(scriptsDir, de.Name())
			if info, err := os.Lstat(entryPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				os.Remove(entryPath)
			}
		}

		// Create symlinks for assigned tools
		for _, tool := range tools {
			if tool.ToolType.Name == "" {
				continue
			}
			toolName := tool.ToolType.Name
			linkPath := filepath.Join(scriptsDir, toolName)
			targetPath := filepath.Join("/tools", toolName)
			if err := os.Symlink(targetPath, linkPath); err != nil {
				log.Printf("Warning: failed to create symlink for tool %s in skill %s: %v", toolName, name, err)
			}
		}

		// Clean up legacy tools.md files (no longer needed)
		toolsPath := filepath.Join(s.GetSkillReferencesDir(name), "tools.md")
		if err := os.Remove(toolsPath); err == nil {
			log.Printf("Removed legacy tools.md for skill: %s", name)
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

// copyDirPreserveSymlinks copies a directory recursively, preserving symlinks
// (symlinks are recreated as symlinks, not dereferenced)
func copyDirPreserveSymlinks(src, dst string) error {
	// Get source directory info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}

	// Create destination directory
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	// Read source directory entries
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Check if entry is a symlink (must use Lstat, not Stat)
		fileInfo, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("failed to lstat %s: %w", srcPath, err)
		}

		if fileInfo.Mode()&os.ModeSymlink != 0 {
			// It's a symlink - recreate it
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %w", srcPath, err)
			}
			if err := os.Symlink(linkTarget, dstPath); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", dstPath, err)
			}
		} else if fileInfo.IsDir() {
			// It's a directory - recurse
			if err := copyDirPreserveSymlinks(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// It's a regular file - copy it
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file: %w", err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	return nil
}

// IncidentContext contains context for spawning an incident manager
type IncidentContext struct {
	Source   string         // e.g., "slack", "zabbix"
	SourceID string         // e.g., thread_ts, alert_id
	Context  database.JSONB // Event details
	Message  string         // Original message/alert text for title generation
}

// SpawnIncidentManager creates a new incident manager instance
// Creates AGENTS.md in .codex/ directory and copies skills into .codex/skills/
func (s *SkillService) SpawnIncidentManager(ctx *IncidentContext) (string, string, error) {
	// Generate UUID for this incident
	incidentUUID := uuid.New().String()

	// Create incident directory with 0777 permissions so codex (UID 1001) can create files
	incidentDir := filepath.Join(s.incidentsDir, incidentUUID)
	if err := os.MkdirAll(incidentDir, 0777); err != nil {
		return "", "", fmt.Errorf("failed to create incident directory: %w", err)
	}
	// Ensure directory has correct permissions even if parent existed
	os.Chmod(incidentDir, 0777)

	// Create .codex directory with 0755 (codex can read but not modify)
	codexDir := filepath.Join(incidentDir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create .codex directory: %w", err)
	}

	// Copy skills directory into .codex/skills/ (full copy, preserving symlinks)
	// This ensures Codex sees local paths only, not resolved symlink targets
	codexSkillsDir := filepath.Join(codexDir, "skills")
	if err := copyDirPreserveSymlinks(s.skillsDir, codexSkillsDir); err != nil {
		return "", "", fmt.Errorf("failed to copy skills dir: %w", err)
	}

	// Generate AGENTS.md in .codex/ directory
	agentsMdPath := filepath.Join(codexDir, "AGENTS.md")
	if err := s.generateIncidentAgentsMd(agentsMdPath); err != nil {
		return "", "", fmt.Errorf("failed to generate AGENTS.md: %w", err)
	}

	// NOTE: Tool credentials are NOT written to incident directory
	// They are fetched by MCP Gateway at execution time for security

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
	// Get incident manager prompt from the system skill
	prompt, err := s.GetSkillPrompt("incident-manager")
	if err != nil {
		// Fallback to default if skill file doesn't exist yet
		prompt = database.DefaultIncidentManagerPrompt
	}

	// Build AGENTS.md content
	var sb strings.Builder

	sb.WriteString("# Incident Manager\n\n")
	sb.WriteString(prompt)
	sb.WriteString("\n\n")

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

// formatEnvValue formats a value for .env file output.
// - Arrays are converted to comma-separated strings
// - Multi-line values (containing newlines) are base64-encoded with a "base64:" prefix
func formatEnvValue(value interface{}) string {
	var str string

	// Handle arrays/slices - convert to comma-separated string
	switch v := value.(type) {
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprintf("%v", item)
		}
		str = strings.Join(parts, ",")
	case []string:
		str = strings.Join(v, ",")
	default:
		str = fmt.Sprintf("%v", value)
	}

	// Handle PEM/SSH keys that might have spaces instead of newlines
	// (happens when pasted through HTML textarea or JSON parsing issues)
	if strings.Contains(str, "-----BEGIN") && strings.Contains(str, "-----END") {
		str = fixPEMKey(str)
	}

	// Base64 encode if contains newlines
	if strings.Contains(str, "\n") {
		return "base64:" + base64.StdEncoding.EncodeToString([]byte(str))
	}
	return str
}

// fixPEMKey reconstructs a PEM key that may have spaces instead of newlines
func fixPEMKey(key string) string {
	// If already has newlines, return as-is
	if strings.Contains(key, "\n") {
		return key
	}

	// Check for valid PEM markers
	if !strings.Contains(key, "-----BEGIN") || !strings.Contains(key, "-----END") {
		return key
	}

	// Parse by splitting on whitespace
	// Format: "-----BEGIN TYPE-----" content "-----END TYPE-----"
	parts := strings.Fields(key)

	if len(parts) < 4 {
		return key
	}

	// Reconstruct: find BEGIN...END markers and body
	var header, footer string
	var bodyParts []string

	// Use index-based loop so we can skip parts already processed
	for i := 0; i < len(parts); i++ {
		part := parts[i]

		if strings.HasPrefix(part, "-----BEGIN") {
			// Header spans from here to next "-----"
			headerParts := []string{part}
			for j := i + 1; j < len(parts); j++ {
				headerParts = append(headerParts, parts[j])
				if strings.HasSuffix(parts[j], "-----") {
					header = strings.Join(headerParts, " ")
					i = j // Skip to after header
					break
				}
			}
		} else if strings.HasPrefix(part, "-----END") {
			// Footer spans from here to end marker
			footerParts := []string{part}
			for j := i + 1; j < len(parts); j++ {
				footerParts = append(footerParts, parts[j])
				if strings.HasSuffix(parts[j], "-----") {
					break
				}
			}
			footer = strings.Join(footerParts, " ")
			break // Done processing
		} else if header != "" && !strings.HasSuffix(part, "-----") {
			// We're in the body (after header, before footer)
			bodyParts = append(bodyParts, part)
		}
	}

	if header == "" || footer == "" {
		return key
	}

	// Join body parts (base64 content) - PEM keys have no spaces in the body
	body := strings.Join(bodyParts, "")

	return header + "\n" + body + "\n" + footer + "\n"
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
