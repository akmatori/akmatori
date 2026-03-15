package services

import (
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// MCPServerService manages MCP server configuration CRUD operations
type MCPServerService struct {
	db *gorm.DB
}

// NewMCPServerService creates a new MCP server service
func NewMCPServerService() *MCPServerService {
	return &MCPServerService{
		db: database.GetDB(),
	}
}

// CreateMCPServer creates a new MCP server configuration after validation
func (s *MCPServerService) CreateMCPServer(config *database.MCPServerConfig) (*database.MCPServerConfig, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Check for duplicate name
	var count int64
	s.db.Model(&database.MCPServerConfig{}).Where("name = ?", config.Name).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("MCP server with name %q already exists", config.Name)
	}

	// Check for duplicate namespace_prefix
	s.db.Model(&database.MCPServerConfig{}).Where("namespace_prefix = ?", config.NamespacePrefix).Count(&count)
	if count > 0 {
		return nil, fmt.Errorf("MCP server with namespace_prefix %q already exists", config.NamespacePrefix)
	}

	config.Enabled = true
	if err := s.db.Create(config).Error; err != nil {
		return nil, fmt.Errorf("failed to create MCP server config: %w", err)
	}

	return config, nil
}

// GetMCPServer retrieves an MCP server configuration by ID
func (s *MCPServerService) GetMCPServer(id uint) (*database.MCPServerConfig, error) {
	var config database.MCPServerConfig
	if err := s.db.First(&config, id).Error; err != nil {
		return nil, fmt.Errorf("MCP server config not found: %w", err)
	}
	return &config, nil
}

// UpdateMCPServer updates an MCP server configuration by ID
func (s *MCPServerService) UpdateMCPServer(id uint, updates map[string]interface{}) (*database.MCPServerConfig, error) {
	var config database.MCPServerConfig
	if err := s.db.First(&config, id).Error; err != nil {
		return nil, fmt.Errorf("MCP server config not found: %w", err)
	}

	if v, ok := updates["name"]; ok {
		if name, ok := v.(string); ok {
			if name != config.Name {
				var count int64
				s.db.Model(&database.MCPServerConfig{}).Where("name = ? AND id != ?", name, id).Count(&count)
				if count > 0 {
					return nil, fmt.Errorf("MCP server with name %q already exists", name)
				}
			}
			config.Name = name
		}
	}
	if v, ok := updates["transport"]; ok {
		if t, ok := v.(string); ok {
			config.Transport = database.MCPServerTransport(t)
		}
	}
	if v, ok := updates["url"]; ok {
		if u, ok := v.(string); ok {
			config.URL = u
		}
	}
	if v, ok := updates["command"]; ok {
		if c, ok := v.(string); ok {
			config.Command = c
		}
	}
	if v, ok := updates["args"]; ok {
		if a, ok := v.(database.JSONB); ok {
			config.Args = a
		}
	}
	if v, ok := updates["env_vars"]; ok {
		if e, ok := v.(database.JSONB); ok {
			config.EnvVars = e
		}
	}
	if v, ok := updates["namespace_prefix"]; ok {
		if ns, ok := v.(string); ok {
			if ns != config.NamespacePrefix {
				var count int64
				s.db.Model(&database.MCPServerConfig{}).Where("namespace_prefix = ? AND id != ?", ns, id).Count(&count)
				if count > 0 {
					return nil, fmt.Errorf("MCP server with namespace_prefix %q already exists", ns)
				}
			}
			config.NamespacePrefix = ns
		}
	}
	if v, ok := updates["auth_config"]; ok {
		if ac, ok := v.(database.JSONB); ok {
			config.AuthConfig = ac
		}
	}
	if v, ok := updates["enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			config.Enabled = enabled
		}
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if err := s.db.Save(&config).Error; err != nil {
		return nil, fmt.Errorf("failed to update MCP server config: %w", err)
	}

	return &config, nil
}

// DeleteMCPServer deletes an MCP server configuration by ID
func (s *MCPServerService) DeleteMCPServer(id uint) error {
	result := s.db.Delete(&database.MCPServerConfig{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete MCP server config: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("MCP server config not found")
	}
	return nil
}

// ListMCPServers lists all MCP server configurations
func (s *MCPServerService) ListMCPServers() ([]database.MCPServerConfig, error) {
	var configs []database.MCPServerConfig
	if err := s.db.Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to list MCP server configs: %w", err)
	}
	return configs, nil
}
