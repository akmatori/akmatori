package database

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB holds the database connection
var DB *gorm.DB

// JSONB is a custom type for PostgreSQL JSONB columns
type JSONB map[string]interface{}

// Scan implements the sql.Scanner interface
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = make(map[string]interface{})
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, j)
}

// Value implements the driver.Valuer interface
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

// ToolType represents a tool type definition
type ToolType struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	Schema      JSONB     `gorm:"type:jsonb" json:"schema"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ToolType) TableName() string {
	return "tool_types"
}

// ToolInstance represents a configured tool instance
type ToolInstance struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ToolTypeID  uint      `gorm:"not null;index" json:"tool_type_id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	LogicalName string    `gorm:"uniqueIndex;size:128" json:"logical_name"`
	Settings    JSONB     `gorm:"type:jsonb" json:"settings"`
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	ToolType ToolType `gorm:"foreignKey:ToolTypeID" json:"tool_type,omitempty"`
}

func (ToolInstance) TableName() string {
	return "tool_instances"
}

// Skill represents a skill definition
type Skill struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Description string         `gorm:"size:1024" json:"description"`
	Category    string         `gorm:"size:64" json:"category"`
	IsSystem    bool           `gorm:"default:false" json:"is_system"`
	Enabled     bool           `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Tools       []ToolInstance `gorm:"many2many:skill_tools;" json:"tools,omitempty"`
}

func (Skill) TableName() string {
	return "skills"
}

// Incident represents an incident record
type Incident struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UUID       string    `gorm:"uniqueIndex;not null" json:"uuid"`
	Source     string    `gorm:"not null;index" json:"source"`
	SourceID   string    `gorm:"index" json:"source_id"`
	Title      string    `gorm:"type:varchar(255)" json:"title"`
	Status     string    `gorm:"type:varchar(50);not null;default:'pending'" json:"status"`
	Context    JSONB     `gorm:"type:jsonb" json:"context"`
	SessionID  string    `gorm:"index" json:"session_id"`
	WorkingDir string    `json:"working_dir"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Incident) TableName() string {
	return "incidents"
}

// Connect establishes a database connection
func Connect(dsn string, logLevel logger.LogLevel) error {
	config := &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	}

	db, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		return err
	}

	DB = db
	slog.Info("database connected successfully")
	return nil
}

// GetDB returns the database connection
func GetDB() *gorm.DB {
	return DB
}

// ToolCredentials holds credentials for a tool
type ToolCredentials struct {
	ToolType    string                 `json:"tool_type"`
	ToolName    string                 `json:"tool_name"`
	Settings    map[string]interface{} `json:"settings"`
	InstanceID  uint                   `json:"instance_id"`
	LogicalName string                 `json:"logical_name,omitempty"`
}

// GetToolCredentialsForIncident fetches tool credentials for an incident
// It looks up which skills/tools are associated with the incident
func GetToolCredentialsForIncident(ctx context.Context, incidentID string, toolType string) (*ToolCredentials, error) {
	// For now, we get the first enabled tool instance of the given type
	// In future, we can associate specific tool instances with incidents

	var toolInstance ToolInstance
	err := DB.WithContext(ctx).
		Preload("ToolType").
		Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").
		Where("tool_types.name = ? AND tool_instances.enabled = ?", toolType, true).
		First(&toolInstance).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no enabled tool instance found for type: " + toolType)
		}
		return nil, err
	}

	return &ToolCredentials{
		ToolType:    toolInstance.ToolType.Name,
		ToolName:    toolInstance.Name,
		Settings:    toolInstance.Settings,
		InstanceID:  toolInstance.ID,
		LogicalName: toolInstance.LogicalName,
	}, nil
}

// GetAllEnabledToolInstances returns all enabled tool instances
func GetAllEnabledToolInstances(ctx context.Context) ([]ToolInstance, error) {
	var instances []ToolInstance
	err := DB.WithContext(ctx).
		Preload("ToolType").
		Where("enabled = ?", true).
		Find(&instances).Error
	return instances, err
}

// GetToolCredentialsByInstanceID fetches tool credentials by the tool instance primary key.
// This is used when the agent explicitly specifies which tool instance to use.
// The expectedToolType parameter ensures the instance belongs to the requested tool type,
// preventing misrouted calls (e.g., an SSH call with a Zabbix instance ID).
func GetToolCredentialsByInstanceID(ctx context.Context, instanceID uint, expectedToolType string) (*ToolCredentials, error) {
	var toolInstance ToolInstance
	err := DB.WithContext(ctx).
		Preload("ToolType").
		Where("id = ? AND enabled = ?", instanceID, true).
		First(&toolInstance).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no enabled tool instance found with ID: %d", instanceID)
		}
		return nil, err
	}

	if toolInstance.ToolType.Name != expectedToolType {
		return nil, fmt.Errorf("tool instance %d is type %q, but %q was requested", instanceID, toolInstance.ToolType.Name, expectedToolType)
	}

	return &ToolCredentials{
		ToolType:    toolInstance.ToolType.Name,
		ToolName:    toolInstance.Name,
		Settings:    toolInstance.Settings,
		InstanceID:  toolInstance.ID,
		LogicalName: toolInstance.LogicalName,
	}, nil
}

// GetToolCredentialsByLogicalName fetches tool credentials by logical name.
// The expectedToolType parameter ensures the instance belongs to the requested tool type.
func GetToolCredentialsByLogicalName(ctx context.Context, logicalName string, expectedToolType string) (*ToolCredentials, error) {
	var toolInstance ToolInstance
	err := DB.WithContext(ctx).
		Preload("ToolType").
		Where("logical_name = ? AND enabled = ?", logicalName, true).
		First(&toolInstance).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no enabled tool instance found with logical name: %s", logicalName)
		}
		return nil, err
	}

	if toolInstance.ToolType.Name != expectedToolType {
		return nil, fmt.Errorf("tool instance %q is type %q, but %q was requested", logicalName, toolInstance.ToolType.Name, expectedToolType)
	}

	return &ToolCredentials{
		ToolType:    toolInstance.ToolType.Name,
		ToolName:    toolInstance.Name,
		Settings:    toolInstance.Settings,
		InstanceID:  toolInstance.ID,
		LogicalName: toolInstance.LogicalName,
	}, nil
}

// ResolveToolCredentials resolves tool credentials with priority:
// 1. Explicit instance ID (if provided and > 0)
// 2. Logical name (if provided and non-empty)
// 3. First enabled instance of the given tool type
func ResolveToolCredentials(ctx context.Context, incidentID string, toolType string, instanceID *uint, logicalName string) (*ToolCredentials, error) {
	if instanceID != nil && *instanceID > 0 {
		return GetToolCredentialsByInstanceID(ctx, *instanceID, toolType)
	}
	if logicalName != "" {
		return GetToolCredentialsByLogicalName(ctx, logicalName, toolType)
	}
	return GetToolCredentialsForIncident(ctx, incidentID, toolType)
}

// GetToolInstanceByType returns a specific tool instance by type name
func GetToolInstanceByType(ctx context.Context, typeName string) (*ToolInstance, error) {
	var instance ToolInstance
	err := DB.WithContext(ctx).
		Preload("ToolType").
		Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").
		Where("tool_types.name = ? AND tool_instances.enabled = ?", typeName, true).
		First(&instance).Error

	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// HTTPConnector represents a declarative HTTP connector definition (mirrors main app model)
type HTTPConnector struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	ToolTypeName string    `gorm:"uniqueIndex;size:128;not null" json:"tool_type_name"`
	Description  string    `gorm:"size:1024" json:"description"`
	BaseURLField string    `gorm:"size:128;not null" json:"base_url_field"`
	AuthConfig   JSONB     `gorm:"type:jsonb" json:"auth_config"`
	Tools        JSONB     `gorm:"type:jsonb;not null" json:"tools"`
	Enabled      bool      `gorm:"default:true" json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (HTTPConnector) TableName() string {
	return "http_connectors"
}

// GetAllEnabledHTTPConnectors returns all enabled HTTP connector definitions
func GetAllEnabledHTTPConnectors(ctx context.Context) ([]HTTPConnector, error) {
	var connectors []HTTPConnector
	err := DB.WithContext(ctx).
		Where("enabled = ?", true).
		Find(&connectors).Error
	return connectors, err
}

// ProxySettings stores HTTP proxy configuration with per-service toggles
type ProxySettings struct {
	ID                      uint      `gorm:"primaryKey" json:"id"`
	ProxyURL                string    `gorm:"type:text" json:"proxy_url"`
	NoProxy                 string    `gorm:"type:text" json:"no_proxy"`
	OpenAIEnabled           bool      `gorm:"default:true" json:"openai_enabled"`
	SlackEnabled            bool      `gorm:"default:true" json:"slack_enabled"`
	ZabbixEnabled           bool      `gorm:"default:false" json:"zabbix_enabled"`
	VictoriaMetricsEnabled  bool      `gorm:"default:false" json:"victoria_metrics_enabled"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (ProxySettings) TableName() string {
	return "proxy_settings"
}

// GetProxySettings retrieves proxy settings from the database
func GetProxySettings(ctx context.Context) (*ProxySettings, error) {
	var settings ProxySettings
	err := DB.WithContext(ctx).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// MCPServerConfig represents a registered external MCP server for proxying.
type MCPServerConfig struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Name            string    `gorm:"uniqueIndex;size:128;not null" json:"name"`
	Transport       string    `gorm:"type:varchar(16);not null" json:"transport"`
	URL             string    `gorm:"size:512" json:"url,omitempty"`
	Command         string    `gorm:"size:512" json:"command,omitempty"`
	Args            JSONB     `gorm:"type:jsonb" json:"args,omitempty"`
	EnvVars         JSONB     `gorm:"type:jsonb" json:"env_vars,omitempty"`
	NamespacePrefix string    `gorm:"size:128;not null" json:"namespace_prefix"`
	AuthConfig      JSONB     `gorm:"type:jsonb" json:"auth_config,omitempty"`
	Enabled         bool      `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (MCPServerConfig) TableName() string {
	return "mcp_server_configs"
}

// GetAllEnabledMCPServerConfigs returns all enabled MCP server configurations.
func GetAllEnabledMCPServerConfigs(ctx context.Context) ([]MCPServerConfig, error) {
	var configs []MCPServerConfig
	err := DB.WithContext(ctx).
		Where("enabled = ?", true).
		Find(&configs).Error
	return configs, err
}

// GetMCPServerConfigByID returns an MCP server config by ID.
func GetMCPServerConfigByID(ctx context.Context, id uint) (*MCPServerConfig, error) {
	var config MCPServerConfig
	err := DB.WithContext(ctx).First(&config, id).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}
