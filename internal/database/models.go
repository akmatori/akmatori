package database

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

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

// SlackSettings stores Slack integration configuration
type SlackSettings struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	BotToken       string    `gorm:"type:text" json:"bot_token"`
	SigningSecret  string    `gorm:"type:text" json:"signing_secret"`
	AppToken       string    `gorm:"type:text" json:"app_token"`
	AlertsChannel  string    `gorm:"type:varchar(255)" json:"alerts_channel"`
	Enabled        bool      `gorm:"default:false" json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// IsConfigured returns true if all required Slack tokens are set
func (s *SlackSettings) IsConfigured() bool {
	return s.BotToken != "" && s.SigningSecret != "" && s.AppToken != ""
}

// IsActive returns true if Slack is enabled and configured
func (s *SlackSettings) IsActive() bool {
	return s.Enabled && s.IsConfigured()
}

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
	ID       uint            `gorm:"primaryKey" json:"id"`
	Type     EventSourceType `gorm:"type:varchar(50);not null;index" json:"type"`
	Name     string          `gorm:"uniqueIndex;not null" json:"name"`
	Settings JSONB           `gorm:"type:jsonb" json:"settings"` // Source-specific settings
	Enabled  bool            `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// IncidentStatus represents the status of an incident
type IncidentStatus string

const (
	IncidentStatusPending   IncidentStatus = "pending"
	IncidentStatusRunning   IncidentStatus = "running"
	IncidentStatusDiagnosed IncidentStatus = "diagnosed"
	IncidentStatusObserving IncidentStatus = "observing"
	IncidentStatusCompleted IncidentStatus = "completed"
	IncidentStatusFailed    IncidentStatus = "failed"
)

// Incident represents a spawned incident manager session
type Incident struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	UUID            string         `gorm:"uniqueIndex;not null" json:"uuid"` // Unique UUID for this incident
	Source          string         `gorm:"not null;index" json:"source"`     // e.g., "slack", "zabbix"
	SourceID        string         `gorm:"index" json:"source_id"`           // e.g., thread_ts, alert_id
	Title           string         `gorm:"type:varchar(255)" json:"title"`   // LLM-generated title summarizing the incident
	Status          IncidentStatus `gorm:"type:varchar(50);not null;default:'pending'" json:"status"`
	Context         JSONB          `gorm:"type:jsonb" json:"context"`       // Event context (message, alert details, etc.)
	SessionID       string         `gorm:"index" json:"session_id"`         // Codex session ID
	WorkingDir      string         `json:"working_dir"`                     // Path to incident working directory
	FullLog         string         `gorm:"type:text" json:"full_log"`       // Complete Codex output log (reasoning, commands, etc.)
	Response        string         `gorm:"type:text" json:"response"`       // Final response/output to user
	TokensUsed      int            `json:"tokens_used"`                     // Total tokens used (input + output)
	ExecutionTimeMs int64          `json:"execution_time_ms"`               // Execution time in milliseconds
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	// Aggregation fields
	AlertCount               int        `gorm:"default:1" json:"alert_count"`                // Number of alerts aggregated into this incident
	LastAlertAt              *time.Time `json:"last_alert_at"`                               // Timestamp of when the last alert was attached
	ObservingStartedAt       *time.Time `json:"observing_started_at"`                        // When the incident entered the "observing" state
	ObservingDurationMinutes int        `gorm:"default:30" json:"observing_duration_minutes"` // How long to wait in observing state before closing

	// Slack context fields (for thread replies to source messages)
	SlackChannelID string `gorm:"column:slack_channel_id" json:"slack_channel_id"` // Slack channel ID where alert originated
	SlackMessageTS string `gorm:"column:slack_message_ts" json:"slack_message_ts"` // Slack message timestamp for thread replies
}

// BeforeCreate hook to set StartedAt
func (i *Incident) BeforeCreate(tx *gorm.DB) error {
	if i.StartedAt.IsZero() {
		i.StartedAt = time.Now()
	}
	return nil
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

func (Incident) TableName() string {
	return "incidents"
}

func (SlackSettings) TableName() string {
	return "slack_settings"
}

// LLMProvider represents the LLM provider
type LLMProvider string

const (
	LLMProviderOpenAI     LLMProvider = "openai"
	LLMProviderAnthropic  LLMProvider = "anthropic"
	LLMProviderGoogle     LLMProvider = "google"
	LLMProviderOpenRouter LLMProvider = "openrouter"
	LLMProviderCustom     LLMProvider = "custom"
)

// ValidLLMProviders returns all valid LLM provider values
func ValidLLMProviders() []LLMProvider {
	return []LLMProvider{
		LLMProviderOpenAI,
		LLMProviderAnthropic,
		LLMProviderGoogle,
		LLMProviderOpenRouter,
		LLMProviderCustom,
	}
}

// IsValidLLMProvider checks if a provider string is valid
func IsValidLLMProvider(provider string) bool {
	for _, p := range ValidLLMProviders() {
		if string(p) == provider {
			return true
		}
	}
	return false
}

// ThinkingLevel represents the thinking/reasoning level for the LLM
type ThinkingLevel string

const (
	ThinkingLevelOff     ThinkingLevel = "off"
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

// ValidThinkingLevels returns all valid thinking level values
func ValidThinkingLevels() []ThinkingLevel {
	return []ThinkingLevel{
		ThinkingLevelOff,
		ThinkingLevelMinimal,
		ThinkingLevelLow,
		ThinkingLevelMedium,
		ThinkingLevelHigh,
		ThinkingLevelXHigh,
	}
}

// IsValidThinkingLevel checks if a thinking level string is valid
func IsValidThinkingLevel(level string) bool {
	for _, l := range ValidThinkingLevels() {
		if string(l) == level {
			return true
		}
	}
	return false
}

// LLMSettings stores per-provider LLM configuration.
// Each provider (openai, anthropic, etc.) has its own row with separate API key, model, and settings.
// The Active field indicates which provider is currently selected for use.
type LLMSettings struct {
	ID            uint          `gorm:"primaryKey" json:"id"`
	Provider      LLMProvider   `gorm:"type:varchar(50);uniqueIndex;not null" json:"provider"`
	APIKey        string        `gorm:"type:text" json:"api_key"`
	Model         string        `gorm:"type:varchar(100)" json:"model"`
	ThinkingLevel ThinkingLevel `gorm:"type:varchar(50);default:'medium'" json:"thinking_level"`
	BaseURL       string        `gorm:"type:text" json:"base_url"`
	Enabled       bool          `gorm:"default:false" json:"enabled"`
	Active        bool          `gorm:"default:false" json:"active"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// IsConfigured returns true if the LLM provider has an API key set
func (l *LLMSettings) IsConfigured() bool {
	return l.APIKey != ""
}

// IsActive returns true if the LLM settings are enabled and configured
func (l *LLMSettings) IsActive() bool {
	return l.Enabled && l.IsConfigured()
}

func (LLMSettings) TableName() string {
	return "llm_settings"
}

// ProxySettings stores HTTP proxy configuration with per-service toggles
type ProxySettings struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ProxyURL      string    `gorm:"type:text" json:"proxy_url"`        // HTTP/HTTPS proxy URL
	NoProxy       string    `gorm:"type:text" json:"no_proxy"`         // Comma-separated hosts to bypass proxy
	OpenAIEnabled bool      `gorm:"default:true" json:"openai_enabled"`  // Use proxy for OpenAI API
	SlackEnabled  bool      `gorm:"default:true" json:"slack_enabled"`   // Use proxy for Slack
	ZabbixEnabled bool      `gorm:"default:false" json:"zabbix_enabled"` // Use proxy for Zabbix API
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName overrides the table name
func (ProxySettings) TableName() string {
	return "proxy_settings"
}

// IsConfigured returns true if a proxy URL is set
func (p *ProxySettings) IsConfigured() bool {
	return p.ProxyURL != ""
}

// ContextFile stores metadata for uploaded context files
// Files are stored in filesystem, only metadata in database
type ContextFile struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Filename     string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"filename"`
	OriginalName string    `gorm:"type:varchar(255)" json:"original_name"`
	MimeType     string    `gorm:"type:varchar(100)" json:"mime_type"`
	Size         int64     `json:"size"`
	Description  string    `gorm:"type:text" json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (ContextFile) TableName() string {
	return "context_files"
}

// APIKeySettings stores API authentication configuration
type APIKeySettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Enabled   bool      `gorm:"default:false" json:"enabled"`
	Keys      JSONB     `gorm:"type:jsonb" json:"keys"` // Array of {key, name, enabled, created_at}
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// APIKeyEntry represents a single API key entry
type APIKeyEntry struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// GetActiveKeys returns all enabled API keys
func (a *APIKeySettings) GetActiveKeys() []string {
	if a.Keys == nil {
		return []string{}
	}

	keysData, ok := a.Keys["keys"].([]interface{})
	if !ok {
		return []string{}
	}

	var activeKeys []string
	for _, k := range keysData {
		keyMap, ok := k.(map[string]interface{})
		if !ok {
			continue
		}
		enabled, _ := keyMap["enabled"].(bool)
		key, _ := keyMap["key"].(string)
		if enabled && key != "" {
			activeKeys = append(activeKeys, key)
		}
	}
	return activeKeys
}

// IsActive returns true if API key authentication is enabled
func (a *APIKeySettings) IsActive() bool {
	return a.Enabled && len(a.GetActiveKeys()) > 0
}

func (APIKeySettings) TableName() string {
	return "api_key_settings"
}

// ========== Alert Source Models ==========

// AlertSourceType represents a type of alert source (e.g., Alertmanager, PagerDuty)
type AlertSourceType struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	Name                 string    `gorm:"uniqueIndex;size:64;not null" json:"name"`         // snake_case: "alertmanager", "pagerduty"
	DisplayName          string    `gorm:"size:128;not null" json:"display_name"`            // Human-friendly: "Prometheus Alertmanager"
	Description          string    `gorm:"type:text" json:"description"`
	DefaultFieldMappings JSONB     `gorm:"type:jsonb" json:"default_field_mappings"`         // Default field mappings for this source
	WebhookSecretHeader  string    `gorm:"size:128" json:"webhook_secret_header"`            // e.g., "X-Alertmanager-Secret"
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	// Relationships
	Instances []AlertSourceInstance `gorm:"foreignKey:AlertSourceTypeID" json:"instances,omitempty"`
}

func (AlertSourceType) TableName() string {
	return "alert_source_types"
}

// AlertSourceInstance represents a configured instance of an alert source
type AlertSourceInstance struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	UUID              string    `gorm:"uniqueIndex;size:36;not null" json:"uuid"`           // UUID for webhook URL
	AlertSourceTypeID uint      `gorm:"not null;index" json:"alert_source_type_id"`
	Name              string    `gorm:"uniqueIndex;size:128;not null" json:"name"`          // User-friendly name
	Description       string    `gorm:"type:text" json:"description"`
	WebhookSecret     string    `gorm:"type:text" json:"webhook_secret"`                    // Instance-specific secret
	FieldMappings     JSONB     `gorm:"type:jsonb" json:"field_mappings"`                   // Override default mappings
	Settings          JSONB     `gorm:"type:jsonb" json:"settings"`                         // Additional instance settings
	Enabled           bool      `gorm:"default:true" json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	// Relationships
	AlertSourceType AlertSourceType `gorm:"foreignKey:AlertSourceTypeID" json:"alert_source_type,omitempty"`
}

func (AlertSourceInstance) TableName() string {
	return "alert_source_instances"
}

// GetWebhookURL returns the webhook URL for this instance
func (a *AlertSourceInstance) GetWebhookURL(baseURL string) string {
	return baseURL + "/webhook/alert/" + a.UUID
}

// AlertSeverity represents normalized severity levels (used in incident context)
type AlertSeverity string

const (
	AlertSeverityCritical AlertSeverity = "critical"
	AlertSeverityHigh     AlertSeverity = "high"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityInfo     AlertSeverity = "info"
)

// AlertStatus represents normalized alert status
type AlertStatus string

const (
	AlertStatusFiring   AlertStatus = "firing"
	AlertStatusResolved AlertStatus = "resolved"
)

// GetSeverityEmoji returns an emoji for the alert severity
func GetSeverityEmoji(severity AlertSeverity) string {
	switch severity {
	case AlertSeverityCritical:
		return ":red_circle:"
	case AlertSeverityHigh:
		return ":large_orange_circle:"
	case AlertSeverityWarning:
		return ":large_yellow_circle:"
	case AlertSeverityInfo:
		return ":large_blue_circle:"
	default:
		return ":white_circle:"
	}
}
