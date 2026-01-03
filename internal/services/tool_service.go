package services

import (
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// ToolService manages tool types and instances
type ToolService struct {
	db *gorm.DB
}

// NewToolService creates a new tool service
func NewToolService() *ToolService {
	return &ToolService{
		db: database.GetDB(),
	}
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

// EnsureToolTypes ensures the basic tool types exist in the database
func (s *ToolService) EnsureToolTypes() error {
	toolTypes := []database.ToolType{
		{Name: "ssh", Description: "SSH remote command execution tool"},
		{Name: "zabbix", Description: "Zabbix monitoring integration"},
	}

	for _, tt := range toolTypes {
		var existing database.ToolType
		result := s.db.Where("name = ?", tt.Name).First(&existing)
		if result.Error != nil {
			// Create if not exists
			if err := s.db.Create(&tt).Error; err != nil {
				return fmt.Errorf("failed to create tool type %s: %w", tt.Name, err)
			}
		}
	}

	return nil
}
