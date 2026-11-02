package services

import (
	"context"
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
	db               *gorm.DB
	dataDir          string // /akmatori - base data directory
	incidentsDir     string // /akmatori/incidents - incident working directories
	skillsDir        string // /akmatori/skills - skill definitions with SKILL.md
	memoryDir        string // /akmatori/memory - cross-incident memory mirror
	toolService      *ToolService
	contextService   *ContextService
	oneShotLLMCaller OneShotLLMCaller       // optional; nil = title generation falls back deterministically
	memoryIngester   MemoryIngester         // optional; nil = post-investigation file ingest is a no-op
	incidentMerger   IncidentMergeEvaluator // optional; nil = post-investigation merge pass is a no-op
	gatewayTools     GatewayToolLister      // optional; nil = connected-tools renders discovery hints for MCP/HTTP tools
}

// SetMemoryIngester wires the post-investigation memory file ingester that
// runs in a detached goroutine when an incident transitions to a terminal
// status. Optional — when unset, ingest is skipped silently.
func (s *SkillService) SetMemoryIngester(m MemoryIngester) {
	s.memoryIngester = m
}

// NewSkillService creates a new skill service. The oneShotLLMCaller is optional:
// pass nil to skip LLM-backed title generation (tests, early startup).
func NewSkillService(dataDir string, toolService *ToolService, contextService *ContextService, oneShotLLMCaller OneShotLLMCaller) *SkillService {
	return &SkillService{
		db:               database.GetDB(),
		dataDir:          dataDir,
		incidentsDir:     filepath.Join(dataDir, "incidents"),
		skillsDir:        filepath.Join(dataDir, "skills"),
		memoryDir:        filepath.Join(dataDir, "memory"),
		toolService:      toolService,
		contextService:   contextService,
		oneShotLLMCaller: oneShotLLMCaller,
	}
}

// MemoryIngester represents the post-incident file-to-DB ingest call.
// Kept as a narrow interface so SkillService can be tested without pulling
// in the full MemoryService.
type MemoryIngester interface {
	IngestFromDisk(ctx context.Context) error
}

// SetIncidentMerger wires the post-investigation root-cause merge pass that
// runs in a detached goroutine when an alert-sourced incident completes.
// Optional — when unset, the merge pass is skipped silently.
func (s *SkillService) SetIncidentMerger(m IncidentMergeEvaluator) {
	s.incidentMerger = m
}

// IncidentMergeEvaluator represents the post-investigation merge check.
// Narrow interface so SkillService can be tested without the full
// IncidentMerger (and its LLM dependency).
type IncidentMergeEvaluator interface {
	EvaluateAndMerge(ctx context.Context, incidentUUID string) error
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
		if removeErr := os.RemoveAll(skillDir); removeErr != nil {
			slog.Warn("failed to clean skill directory after database error", "dir", skillDir, "err", removeErr)
		}
		return nil, fmt.Errorf("failed to create skill record: %w", err)
	}

	return skill, nil
}

// UpdateSkill updates a skill's metadata and optionally the SKILL.md
func (s *SkillService) UpdateSkill(name string, description, category string, enabled bool) (*database.Skill, error) {
	if IsGeneratedSkill(name) {
		return nil, ErrGeneratedSkill
	}
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
	if err := s.db.Find(&skills).Error; err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	return skills, nil
}

// ListEnabledSkills returns all enabled skills
func (s *SkillService) ListEnabledSkills() ([]database.Skill, error) {
	var skills []database.Skill
	if err := s.db.Where("enabled = ?", true).Find(&skills).Error; err != nil {
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
		if !sk.IsSystem || IsGeneratedSkill(sk.Name) {
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

// GetToolAllowlist is the allowlist of every incident run: all enabled
// connector tools plus the incident built-ins (see IncidentRunAllowlist).
// Lookup errors return an empty slice, so the gateway rejects every call.
func (s *SkillService) GetToolAllowlist() []ToolAllowlistEntry {
	return IncidentRunAllowlist(s.db)
}

// GetSkill returns a skill by name
func (s *SkillService) GetSkill(name string) (*database.Skill, error) {
	var skill database.Skill
	if err := s.db.Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, fmt.Errorf("skill not found: %w", err)
	}
	return &skill, nil
}
