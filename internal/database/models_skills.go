package database

import "time"

// Skill represents a skill definition (uses SKILL.md format internally for Codex compatibility)
// Skill prompt/instructions are stored in filesystem at /akmatori/skills/{name}/SKILL.md
type Skill struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:64;not null" json:"name"` // kebab-case name (e.g., "zabbix-analyst")
	Description string    `gorm:"size:1024" json:"description"`             // Short description for skill discovery
	Category    string    `gorm:"size:64" json:"category"`                  // Optional category (e.g., "monitoring", "database")
	IsSystem    bool      `gorm:"default:false" json:"is_system"`           // System skills cannot be deleted and don't connect to tools
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relationships - tools are symlinked to skills/{name}/scripts/ with imports embedded in SKILL.md
	Tools []ToolInstance `gorm:"many2many:skill_tools;" json:"tools,omitempty"`
}

// ToolType represents a predefined tool type (e.g., zabbix, grafana)
type ToolType struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"` // Snake_case tool name matching directory (e.g., "aws_cloudwatch")
	Description string    `gorm:"type:text" json:"description"`
	Schema      JSONB     `gorm:"type:jsonb" json:"schema"` // JSON schema for settings validation
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relationships
	Instances []ToolInstance `gorm:"foreignKey:ToolTypeID" json:"instances,omitempty"`
}

// ToolInstance represents an actual configured instance of a tool type
type ToolInstance struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ToolTypeID uint      `gorm:"not null;index" json:"tool_type_id"`
	Name       string    `gorm:"uniqueIndex;not null" json:"name"` // User-friendly name
	Settings   JSONB     `gorm:"type:jsonb" json:"settings"`       // Tool-specific settings (URLs, tokens, etc.)
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Relationships
	ToolType ToolType `gorm:"foreignKey:ToolTypeID" json:"tool_type,omitempty"`
	Skills   []Skill  `gorm:"many2many:skill_tools;" json:"skills,omitempty"`
}

// SkillTool represents the many-to-many relationship between skills and tools
// GORM auto-manages this table via the many2many:skill_tools tag
type SkillTool struct {
	SkillID        uint      `gorm:"primaryKey" json:"skill_id"`
	ToolInstanceID uint      `gorm:"primaryKey" json:"tool_instance_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// EventSourceType represents the type of event source
type EventSourceType string

const (
	EventSourceTypeSlack   EventSourceType = "slack"
	EventSourceTypeWebhook EventSourceType = "webhook"
)

// EventSource represents an event source configuration
type EventSource struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	Type      EventSourceType `gorm:"type:varchar(50);not null;index" json:"type"`
	Name      string          `gorm:"uniqueIndex;not null" json:"name"`
	Settings  JSONB           `gorm:"type:jsonb" json:"settings"` // Source-specific settings
	Enabled   bool            `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TableName overrides for explicit table naming
func (Skill) TableName() string {
	return "skills"
}

func (ToolType) TableName() string {
	return "tool_types"
}

func (ToolInstance) TableName() string {
	return "tool_instances"
}

func (SkillTool) TableName() string {
	return "skill_tools"
}

func (EventSource) TableName() string {
	return "event_sources"
}
