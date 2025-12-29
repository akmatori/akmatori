package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// isValidToolName validates that a tool name is in snake_case format
func isValidToolName(name string) bool {
	// Must be lowercase letters, numbers, and underscores only
	// Must start with a letter
	// Must not end with an underscore
	matched, _ := regexp.MatchString(`^[a-z][a-z0-9_]*[a-z0-9]$`, name)
	return matched
}

// ToolMetadata represents the metadata for a tool type
type ToolMetadata struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Version         string              `json:"version"`
	SettingsSchema  database.JSONB      `json:"settings_schema"`
	Functions       []ToolFunction      `json:"functions"`
}

// ToolFunction represents a function provided by a tool
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"`
	Returns     string `json:"returns"`
}

// ToolService manages tool types and instances
type ToolService struct {
	toolsDir string
	db       *gorm.DB
}

// NewToolService creates a new tool service
func NewToolService(toolsDir string) *ToolService {
	return &ToolService{
		toolsDir: toolsDir,
		db:       database.GetDB(),
	}
}

// GetToolsDir returns the tools directory path
func (s *ToolService) GetToolsDir() string {
	return s.toolsDir
}

// LoadToolTypes loads all tool types from the tools directory
func (s *ToolService) LoadToolTypes() error {
	// Get all subdirectories in tools directory
	entries, err := os.ReadDir(s.toolsDir)
	if err != nil {
		return fmt.Errorf("failed to read tools directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Tool name is the directory name (must be snake_case)
		toolName := entry.Name()

		// Validate tool name format
		if !isValidToolName(toolName) {
			return fmt.Errorf("invalid tool name '%s': must be snake_case (lowercase letters, numbers, underscores; start with letter, not end with underscore)", toolName)
		}

		metadataPath := filepath.Join(s.toolsDir, toolName, "tool_metadata.json")

		// Check if metadata file exists
		if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
			continue
		}

		// Read metadata file
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			return fmt.Errorf("failed to read metadata for tool %s: %w", toolName, err)
		}

		var metadata ToolMetadata
		if err := json.Unmarshal(data, &metadata); err != nil {
			return fmt.Errorf("failed to parse metadata for tool %s: %w", toolName, err)
		}

		// Check if tool type already exists in database (lookup by name)
		var existing database.ToolType
		result := s.db.Where("name = ?", toolName).First(&existing)

		if result.Error == nil {
			// Update existing (name stays the same, update other fields)
			existing.Description = metadata.Description
			existing.Schema = metadata.SettingsSchema
			if err := s.db.Save(&existing).Error; err != nil {
				return fmt.Errorf("failed to update tool type %s: %w", toolName, err)
			}
		} else {
			// Create new (use directory name, not metadata.Name)
			toolType := database.ToolType{
				Name:        toolName, // Use directory name (snake_case)
				Description: metadata.Description,
				Schema:      metadata.SettingsSchema,
			}
			if err := s.db.Create(&toolType).Error; err != nil {
				return fmt.Errorf("failed to create tool type %s: %w", toolName, err)
			}
		}
	}

	return nil
}

// GetToolMetadata reads and returns the metadata for a specific tool type
func (s *ToolService) GetToolMetadata(toolName string) (*ToolMetadata, error) {
	metadataPath := filepath.Join(s.toolsDir, toolName, "tool_metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata ToolMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

// CopyToolToSkillLib copies a tool's files to a skill's lib directory
func (s *ToolService) CopyToolToSkillLib(toolName, skillLibDir string) error {
	srcDir := filepath.Join(s.toolsDir, toolName)
	dstDir := filepath.Join(skillLibDir, toolName)

	// Create destination directory
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Copy all files except tool_metadata.json
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == "tool_metadata.json" || entry.Name() == "__pycache__" {
			continue
		}

		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			// Skip directories for now (can be enhanced if needed)
			continue
		}

		// Copy file
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", entry.Name(), err)
		}

		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// GenerateToolDescription generates a description of tool functions for AGENTS.md
func (s *ToolService) GenerateToolDescription(toolName string) (string, error) {
	metadata, err := s.GetToolMetadata(toolName)
	if err != nil {
		return "", err
	}

	description := fmt.Sprintf("### %s\n\n%s\n\n**Available Functions:**\n\n", metadata.Name, metadata.Description)

	for _, fn := range metadata.Functions {
		description += fmt.Sprintf("- **%s**: %s\n", fn.Name, fn.Description)
		if fn.Parameters != "" {
			description += fmt.Sprintf("  - Parameters: %s\n", fn.Parameters)
		}
		if fn.Returns != "" {
			description += fmt.Sprintf("  - Returns: %s\n", fn.Returns)
		}
		description += "\n"
	}

	return description, nil
}

// CreateToolInstance creates a new tool instance
func (s *ToolService) CreateToolInstance(toolTypeID uint, name string, settings database.JSONB) (*database.ToolInstance, error) {
	instance := &database.ToolInstance{
		ToolTypeID: toolTypeID,
		Name:       name,
		Settings:   settings,
		Enabled:    true,
	}

	if err := s.db.Create(instance).Error; err != nil {
		return nil, fmt.Errorf("failed to create tool instance: %w", err)
	}

	return instance, nil
}

// GetToolInstance retrieves a tool instance by ID
func (s *ToolService) GetToolInstance(id uint) (*database.ToolInstance, error) {
	var instance database.ToolInstance
	if err := s.db.Preload("ToolType").First(&instance, id).Error; err != nil {
		return nil, fmt.Errorf("failed to get tool instance: %w", err)
	}
	return &instance, nil
}

// UpdateToolInstance updates a tool instance
func (s *ToolService) UpdateToolInstance(id uint, name string, settings database.JSONB, enabled bool) error {
	updates := map[string]interface{}{
		"name":     name,
		"settings": settings,
		"enabled":  enabled,
	}

	if err := s.db.Model(&database.ToolInstance{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update tool instance: %w", err)
	}

	return nil
}

// DeleteToolInstance deletes a tool instance
func (s *ToolService) DeleteToolInstance(id uint) error {
	if err := s.db.Delete(&database.ToolInstance{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete tool instance: %w", err)
	}
	return nil
}

// ListToolTypes lists all tool types
func (s *ToolService) ListToolTypes() ([]database.ToolType, error) {
	var toolTypes []database.ToolType
	if err := s.db.Find(&toolTypes).Error; err != nil {
		return nil, fmt.Errorf("failed to list tool types: %w", err)
	}
	return toolTypes, nil
}

// ListToolInstances lists all tool instances
func (s *ToolService) ListToolInstances() ([]database.ToolInstance, error) {
	var instances []database.ToolInstance
	if err := s.db.Preload("ToolType").Find(&instances).Error; err != nil {
		return nil, fmt.Errorf("failed to list tool instances: %w", err)
	}
	return instances, nil
}
