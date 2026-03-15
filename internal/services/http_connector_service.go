package services

import (
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// HTTPConnectorService manages HTTP connector CRUD operations
type HTTPConnectorService struct {
	db *gorm.DB
}

// NewHTTPConnectorService creates a new HTTP connector service
func NewHTTPConnectorService() *HTTPConnectorService {
	return &HTTPConnectorService{
		db: database.GetDB(),
	}
}

// CreateHTTPConnector creates a new HTTP connector after validation
func (s *HTTPConnectorService) CreateHTTPConnector(connector *database.HTTPConnector) (*database.HTTPConnector, error) {
	if err := connector.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Check for duplicate tool_type_name
	var count int64
	s.db.Model(&database.HTTPConnector{}).Where("tool_type_name = ?", connector.ToolTypeName).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("connector with tool_type_name %q already exists", connector.ToolTypeName)
	}

	connector.Enabled = true
	if err := s.db.Create(connector).Error; err != nil {
		return nil, fmt.Errorf("failed to create HTTP connector: %w", err)
	}

	return connector, nil
}

// GetHTTPConnector retrieves an HTTP connector by ID
func (s *HTTPConnectorService) GetHTTPConnector(id uint) (*database.HTTPConnector, error) {
	var connector database.HTTPConnector
	if err := s.db.First(&connector, id).Error; err != nil {
		return nil, fmt.Errorf("HTTP connector not found: %w", err)
	}
	return &connector, nil
}

// UpdateHTTPConnector updates an HTTP connector by ID
func (s *HTTPConnectorService) UpdateHTTPConnector(id uint, updates map[string]interface{}) (*database.HTTPConnector, error) {
	var connector database.HTTPConnector
	if err := s.db.First(&connector, id).Error; err != nil {
		return nil, fmt.Errorf("HTTP connector not found: %w", err)
	}

	// Apply updates to a copy for validation
	if v, ok := updates["tool_type_name"]; ok {
		if name, ok := v.(string); ok {
			// Check uniqueness if name changed
			if name != connector.ToolTypeName {
				var count int64
				s.db.Model(&database.HTTPConnector{}).Where("tool_type_name = ? AND id != ?", name, id).Count(&count)
				if count > 0 {
					return nil, fmt.Errorf("connector with tool_type_name %q already exists", name)
				}
			}
			connector.ToolTypeName = name
		}
	}
	if v, ok := updates["description"]; ok {
		if desc, ok := v.(string); ok {
			connector.Description = desc
		}
	}
	if v, ok := updates["base_url_field"]; ok {
		if field, ok := v.(string); ok {
			connector.BaseURLField = field
		}
	}
	if v, ok := updates["auth_config"]; ok {
		if ac, ok := v.(database.JSONB); ok {
			connector.AuthConfig = ac
		}
	}
	if v, ok := updates["tools"]; ok {
		if tools, ok := v.(database.JSONB); ok {
			connector.Tools = tools
		}
	}
	if v, ok := updates["enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			connector.Enabled = enabled
		}
	}

	// Validate the updated connector
	if err := connector.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if err := s.db.Save(&connector).Error; err != nil {
		return nil, fmt.Errorf("failed to update HTTP connector: %w", err)
	}

	return &connector, nil
}

// DeleteHTTPConnector deletes an HTTP connector by ID
func (s *HTTPConnectorService) DeleteHTTPConnector(id uint) error {
	result := s.db.Delete(&database.HTTPConnector{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete HTTP connector: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("HTTP connector not found")
	}
	return nil
}

// ListHTTPConnectors lists all HTTP connectors
func (s *HTTPConnectorService) ListHTTPConnectors() ([]database.HTTPConnector, error) {
	var connectors []database.HTTPConnector
	if err := s.db.Find(&connectors).Error; err != nil {
		return nil, fmt.Errorf("failed to list HTTP connectors: %w", err)
	}
	return connectors, nil
}
