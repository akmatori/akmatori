package services

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gopkg.in/yaml.v3"
)

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
			slog.Warn("referenced file does not exist, skipping", "filename", filename)
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
			slog.Warn("no SKILL.md for skill", "skill", name, "err", err)
			continue
		}

		// Parse frontmatter
		parts := strings.SplitN(string(content), "---", 3)
		if len(parts) < 3 {
			slog.Warn("invalid SKILL.md format for skill", "skill", name)
			continue
		}

		var frontmatter SkillFrontmatter
		if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
			slog.Warn("failed to parse frontmatter for skill", "skill", name, "err", err)
			continue
		}

		// Create database record
		skill := &database.Skill{
			Name:        name,
			Description: frontmatter.Description,
			Enabled:     true,
		}
		if err := s.db.Create(skill).Error; err != nil {
			slog.Warn("failed to sync skill from filesystem", "skill", name, "err", err)
		} else {
			slog.Info("synced skill from filesystem", "skill", name)
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
			slog.Warn("failed to create directories for skill", "skill", skill.Name, "err", err)
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
			slog.Warn("failed to sync assets for skill", "skill", skill.Name, "err", err)
		}

		// Get tools for this skill
		tools := s.getSkillTools(skill.Name)

		// Write SKILL.md with frontmatter + prompt body
		skillMd := s.generateSkillMd(skill.Name, skill.Description, prompt, tools)
		skillPath := filepath.Join(s.GetSkillDir(skill.Name), "SKILL.md")

		if err := os.WriteFile(skillPath, []byte(skillMd), 0644); err != nil {
			slog.Warn("failed to regenerate SKILL.md for skill", "skill", skill.Name, "err", err)
			continue
		}

		slog.Info("regenerated SKILL.md for skill", "skill", skill.Name)
	}

	return nil
}
