package database

import (
	"time"

	"gorm.io/gorm"
)

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

func (Incident) TableName() string {
	return "incidents"
}
