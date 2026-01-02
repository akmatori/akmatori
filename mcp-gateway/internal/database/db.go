package database

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
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
	ID         uint      `gorm:"primaryKey" json:"id"`
	ToolTypeID uint      `gorm:"not null;index" json:"tool_type_id"`
	Name       string    `gorm:"uniqueIndex;not null" json:"name"`
	Settings   JSONB     `gorm:"type:jsonb" json:"settings"`
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

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
	log.Println("Database connected successfully")
	return nil
}

// GetDB returns the database connection
func GetDB() *gorm.DB {
	return DB
}

// ToolCredentials holds credentials for a tool
type ToolCredentials struct {
	ToolType   string                 `json:"tool_type"`
	ToolName   string                 `json:"tool_name"`
	Settings   map[string]interface{} `json:"settings"`
	InstanceID uint                   `json:"instance_id"`
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
		ToolType:   toolInstance.ToolType.Name,
		ToolName:   toolInstance.Name,
		Settings:   toolInstance.Settings,
		InstanceID: toolInstance.ID,
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
