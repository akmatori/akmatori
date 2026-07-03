package database

import "time"

// Mirror structs for tables owned by the main API. The gateway never runs
// migrations — the main API's AutoMigrate owns the DDL. When a table is
// missing (e.g. gateway upgraded ahead of the API), queries return errors
// that surface as tool errors, which is the intended graceful behavior.

// Proposal mirrors the main API's Proposal model (internal/database/models_proposals.go).
type Proposal struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	UUID                string     `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Kind                string     `gorm:"size:32;not null" json:"kind"`
	Status              string     `gorm:"size:16;not null;default:'pending'" json:"status"`
	Title               string     `gorm:"size:255;not null" json:"title"`
	Reasoning           string     `gorm:"type:text" json:"reasoning"`
	TargetRef           string     `gorm:"size:512" json:"target_ref"`
	CurrentSnapshot     string     `gorm:"type:text" json:"current_snapshot"`
	ProposedContent     string     `gorm:"type:text;not null" json:"proposed_content"`
	SourceIncidentUUIDs JSONB      `gorm:"type:jsonb" json:"source_incident_uuids"`
	EvaluationRunUUID   string     `gorm:"size:36" json:"evaluation_run_uuid"`
	ChatIncidentUUID    string     `gorm:"size:36" json:"chat_incident_uuid"`
	CreatedBy           string     `gorm:"size:32" json:"created_by"`
	ApplyError          string     `gorm:"type:text" json:"apply_error"`
	AppliedAt           *time.Time `json:"applied_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (Proposal) TableName() string {
	return "proposals"
}

// Runbook mirrors the main API's Runbook model.
type Runbook struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `json:"title"`
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Runbook) TableName() string {
	return "runbooks"
}

// Memory mirrors the main API's Memory model.
type Memory struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Scope       string    `json:"scope"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `gorm:"type:text" json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Memory) TableName() string {
	return "memories"
}

// CronJob mirrors the main API's CronJob model (read-only in the gateway).
type CronJob struct {
	ID       uint           `gorm:"primaryKey" json:"id"`
	UUID     string         `json:"uuid"`
	Name     string         `json:"name"`
	Schedule string         `json:"schedule"`
	Prompt   string         `gorm:"type:text" json:"prompt"`
	IsSystem bool           `json:"is_system"`
	Enabled  bool           `json:"enabled"`
	Tools    []ToolInstance `gorm:"many2many:cron_job_tools;joinForeignKey:CronJobID;joinReferences:ToolInstanceID" json:"tools,omitempty"`
}

func (CronJob) TableName() string {
	return "cron_jobs"
}

// CronJobTool mirrors the cron_job_tools join table.
type CronJobTool struct {
	CronJobID      uint `gorm:"primaryKey" json:"cron_job_id"`
	ToolInstanceID uint `gorm:"primaryKey" json:"tool_instance_id"`
}

func (CronJobTool) TableName() string {
	return "cron_job_tools"
}
