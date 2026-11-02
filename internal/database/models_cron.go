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
// ToolsEnabled grants the run every enabled connector tool (the same set an
// incident run gets); BuiltinTools picks the built-in namespaces (events,
// runbooks, memory, incidents, proposals). See the tool-authorization rule.
type CronJob struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	UUID      string `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Name      string `gorm:"uniqueIndex;size:128;not null" json:"name"`
	Schedule  string `gorm:"size:128;not null" json:"schedule"`
	Prompt    string `gorm:"type:text;not null" json:"prompt"`
	IsSystem  bool   `gorm:"default:false" json:"is_system"`
	ChannelID *uint  `gorm:"index" json:"channel_id"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	// PostResults controls whether the tick's final summary is posted to a
	// messaging channel. When false the run still executes and records its
	// result on the Incident row — it just stays out of Slack/Telegram.
	PostResults bool `gorm:"default:true" json:"post_results"`
	// ToolsEnabled: the run may call every enabled connector tool.
	ToolsEnabled bool `gorm:"not null;default:true" json:"tools_enabled"`
	// BuiltinTools: built-in namespaces the run may call.
	BuiltinTools  StringList `gorm:"type:jsonb" json:"builtin_tools"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus string     `gorm:"size:16" json:"last_run_status"`
	LastRunError  string     `gorm:"type:text" json:"last_run_error"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	Channel *Channel `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
}

func (CronJob) TableName() string {
	return "cron_jobs"
}
