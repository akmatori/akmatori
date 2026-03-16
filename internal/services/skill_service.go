package services

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/akmatori/akmatori/internal/database"
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
		slog.Warn("failed to sync skill assets", "err", err)
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
			slog.Warn("failed to update SKILL.md", "err", err)
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

// ToolAllowlistEntry represents one authorized tool instance for an incident.
type ToolAllowlistEntry struct {
	InstanceID  uint   `json:"instance_id"`
	LogicalName string `json:"logical_name"`
	ToolType    string `json:"tool_type"`
}

// GetToolAllowlist builds an allowlist of tool instances from all enabled, non-system skills.
// The allowlist is deduplicated by instance ID (a tool instance assigned to multiple skills
// only appears once). Returns an empty slice (not nil) if no tools are assigned, so the
// gateway receives an explicit empty allowlist and rejects all tool calls.
func (s *SkillService) GetToolAllowlist() []ToolAllowlistEntry {
	var skills []database.Skill
	err := s.db.Preload("Tools.ToolType").Where("enabled = ?", true).Find(&skills).Error
	if err != nil {
		slog.Error("failed to list enabled skills for allowlist", "error", err)
		return []ToolAllowlistEntry{}
	}

	seen := make(map[uint]bool)
	entries := make([]ToolAllowlistEntry, 0)
	for _, sk := range skills {
		if sk.IsSystem {
			continue
		}
		for _, tool := range sk.Tools {
			if !tool.Enabled || seen[tool.ID] {
				continue
			}
			seen[tool.ID] = true

			// Resolve tool type name — it's already loaded via Preload in ListEnabledSkills,
			// but the nested ToolType may not be preloaded. Query if needed.
			toolTypeName := tool.ToolType.Name
			if toolTypeName == "" {
				var tt database.ToolType
				if err := s.db.First(&tt, tool.ToolTypeID).Error; err == nil {
					toolTypeName = tt.Name
				}
			}

			entries = append(entries, ToolAllowlistEntry{
				InstanceID:  tool.ID,
				LogicalName: tool.LogicalName,
				ToolType:    toolTypeName,
			})
		}
	}
	return entries
}

// GetSkill returns a skill by name
func (s *SkillService) GetSkill(name string) (*database.Skill, error) {
	var skill database.Skill
	if err := s.db.Preload("Tools").Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, fmt.Errorf("skill not found: %w", err)
	}
	return &skill, nil
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
// Tools are registered as pi-mono ToolDefinition objects at session creation time.
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
