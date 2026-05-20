package database

import "time"

// CronJobRunStatus is the recorded status of the last cron tick.
const (
	CronJobRunStatusOK    = "ok"
	CronJobRunStatusError = "error"
)

// CronJob represents a scheduled task. Each job runs on its own cron schedule
// (parsed via robfig/cron/v3), executes a full agent investigation via the
// cron-agent system skill, and posts the resulting summary to a Channel.
//
// IsSystem rows are seeded by InitializeSchema (e.g. memory-curator). They
// cannot be deleted by operators; the runner enforces this in DeleteJob.
//
// Tools is a per-cron allowlist that overrides the global allowlist used by
// alert-driven incidents — each cron declares exactly which infrastructure
// tools its agent run may call.
type CronJob struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	UUID          string     `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Name          string     `gorm:"uniqueIndex;size:128;not null" json:"name"`
	Schedule      string     `gorm:"size:128;not null" json:"schedule"`
	Prompt        string     `gorm:"type:text;not null" json:"prompt"`
	IsSystem      bool       `gorm:"default:false" json:"is_system"`
	ChannelID     *uint      `gorm:"index" json:"channel_id"`
	Enabled       bool       `gorm:"default:true" json:"enabled"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus string     `gorm:"size:16" json:"last_run_status"`
	LastRunError  string     `gorm:"type:text" json:"last_run_error"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	Channel *Channel       `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
	Tools   []ToolInstance `gorm:"many2many:cron_job_tools;" json:"tools,omitempty"`
}

func (CronJob) TableName() string {
	return "cron_jobs"
}

// CronJobTool is the many-to-many join row between CronJob and ToolInstance.
// GORM auto-manages this table via the many2many:cron_job_tools tag; the
// struct is defined so callers can inspect and explicitly include it in
// AutoMigrate alongside the rest of the schema.
type CronJobTool struct {
	CronJobID      uint      `gorm:"primaryKey" json:"cron_job_id"`
	ToolInstanceID uint      `gorm:"primaryKey" json:"tool_instance_id"`
	CreatedAt      time.Time `json:"created_at"`
}

func (CronJobTool) TableName() string {
	return "cron_job_tools"
}
