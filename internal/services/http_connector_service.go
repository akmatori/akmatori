package services

import (
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// reservedToolNamespaces contains built-in tool namespaces that cannot be used
// by user-defined HTTP connectors or MCP servers.
var reservedToolNamespaces = []string{
	"ssh", "zabbix", "victoria_metrics", "catchpoint",
	"postgresql", "grafana", "pagerduty", "clickhouse",
	"netbox", "kubernetes", "qmd",
}

// isReservedToolNamespace checks if a name conflicts with a built-in tool namespace.
func isReservedToolNamespace(name string) bool {
	for _, ns := range reservedToolNamespaces {
		if name == ns {
			return true
		}
	}
	return false
}

// namespaceConflictsWithMCPServer checks if a namespace is already used by an MCP server.
func namespaceConflictsWithMCPServer(db *gorm.DB, namespace string) bool {
	var count int64
	db.Model(&database.MCPServerConfig{}).Where("namespace_prefix = ?", namespace).Count(&count)
	return count > 0
}

// namespaceConflictsWithHTTPConnector checks if a namespace is already used by an HTTP connector.
func namespaceConflictsWithHTTPConnector(db *gorm.DB, namespace string) bool {
	var count int64
	db.Model(&database.HTTPConnector{}).Where("tool_type_name = ?", namespace).Count(&count)
	return count > 0
}

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

	// Check for conflicts with built-in tool namespaces
	if isReservedToolNamespace(connector.ToolTypeName) {
		return nil, fmt.Errorf("tool_type_name %q conflicts with a built-in tool namespace", connector.ToolTypeName)
	}

	// Check for duplicate tool_type_name
	var count int64
	s.db.Model(&database.HTTPConnector{}).Where("tool_type_name = ?", connector.ToolTypeName).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("connector with tool_type_name %q already exists", connector.ToolTypeName)
	}

	// Check for cross-type namespace collision with MCP servers
	if namespaceConflictsWithMCPServer(s.db, connector.ToolTypeName) {
		return nil, fmt.Errorf("tool_type_name %q conflicts with an existing MCP server namespace", connector.ToolTypeName)
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
			// Check for conflicts with built-in tool namespaces
			if isReservedToolNamespace(name) {
				return nil, fmt.Errorf("tool_type_name %q conflicts with a built-in tool namespace", name)
			}
			// Check uniqueness if name changed
			if name != connector.ToolTypeName {
				var count int64
				s.db.Model(&database.HTTPConnector{}).Where("tool_type_name = ? AND id != ?", name, id).Count(&count)
				if count > 0 {
					return nil, fmt.Errorf("connector with tool_type_name %q already exists", name)
				}
				// Check cross-type namespace collision with MCP servers
				if namespaceConflictsWithMCPServer(s.db, name) {
					return nil, fmt.Errorf("tool_type_name %q conflicts with an existing MCP server namespace", name)
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
