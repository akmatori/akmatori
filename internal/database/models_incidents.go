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
	IncidentStatusCompleted IncidentStatus = "completed"
	IncidentStatusFailed    IncidentStatus = "failed"
	IncidentStatusMonitor   IncidentStatus = "monitor"
	IncidentStatusClosed    IncidentStatus = "closed"
	// IncidentStatusMerged marks an incident whose investigation concluded it
	// shares a root cause with an earlier incident; its alerts were re-pointed
	// to the survivor referenced by MergedIntoUUID. Merged incidents are
	// excluded from all correlation candidate pools.
	IncidentStatusMerged IncidentStatus = "merged"
)

// Trigger kinds. Incidents are always "alert" (only alerts open incidents);
// the other kinds are the source_kind of request runs on agent_runs.
const (
	IncidentSourceKindAlert        = "alert"
	IncidentSourceKindCron         = "cron"
	IncidentSourceKindSlackMention = "slack_mention"
	IncidentSourceKindManual       = "manual"
	IncidentSourceKindProposal     = "proposal"
)

// IncidentCloseReason records why an incident reached "closed" status. An
// incident that closes with no visible cause reads as data loss to an
// operator, so every close path stamps its reason and the UI surfaces it.
const (
	// CloseReasonManual — an operator closed it via POST /api/incidents/{uuid}/close.
	CloseReasonManual = "manual"
	// CloseReasonMonitorExpired — the monitor window elapsed with no recurrence.
	CloseReasonMonitorExpired = "monitor_expired"
	// CloseReasonAutoStale — the stale-close sweep found no alert or
	// investigation activity within the configured inactivity window.
	CloseReasonAutoStale = "auto_stale"
)

// Incident is a production problem opened from an alert through the
// correlation gate. Only alerts open incidents (see the events rule); the
// agent work done on an incident lives on its agent_runs rows.
type Incident struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	UUID         string         `gorm:"uniqueIndex;not null" json:"uuid"` // Unique UUID for this incident
	Source       string         `gorm:"not null;index" json:"source"`     // e.g., "slack", "zabbix"
	SourceID     string         `gorm:"index" json:"source_id"`           // e.g., thread_ts, alert_id
	SourceKind   string         `gorm:"size:32;index" json:"source_kind"` // Trigger kind: "alert" | "cron" | "slack_mention"
	SourceUUID   string         `gorm:"size:36;index" json:"source_uuid"` // UUID of the triggering entity (alert source instance, cron job, ...)
	Title        string         `gorm:"type:varchar(255)" json:"title"`   // LLM-generated title summarizing the incident
	Status       IncidentStatus `gorm:"type:varchar(50);not null;default:'pending'" json:"status"`
	Context      JSONB          `gorm:"type:jsonb" json:"context"` // Event context (message, alert details, etc.)
	StartedAt    time.Time      `json:"started_at"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	ResolvedAt   *time.Time     `json:"resolved_at,omitempty"`
	MonitorUntil *time.Time     `json:"monitor_until,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`

	// Slack context fields (for thread replies to source messages)
	SlackChannelID string `gorm:"column:slack_channel_id" json:"slack_channel_id"` // Slack channel ID where alert originated
	SlackMessageTS string `gorm:"column:slack_message_ts" json:"slack_message_ts"` // Slack message timestamp for thread replies

	// AlertFingerprint is a stable 32-char hex digest derived from
	// sha256(json([sourceUUID, lower(alertName), lower(targetHost)])). It is
	// used to pre-filter the LLM correlation candidate set to same-identity
	// incidents, reducing the number of rows the LLM must evaluate.
	// Distinct from SourceFingerprint (adapter-supplied external ID) and
	// alertSpawnKey (includes SourceFingerprint for exact-burst dedup).
	AlertFingerprint string `gorm:"size:32;index" json:"alert_fingerprint"`

	// ClosedReason records which close path set Status to "closed": one of
	// the CloseReason* constants. Empty for incidents that never closed and
	// for rows closed before this field existed.
	ClosedReason string `gorm:"size:24" json:"closed_reason,omitempty"`

	// MergedIntoUUID points at the surviving incident when Status is
	// "merged" (post-investigation root-cause merge). Empty otherwise.
	MergedIntoUUID string `gorm:"size:36;index" json:"merged_into_uuid,omitempty"`

	// ResultStatus is the agent's self-reported [FINAL_RESULT] status from
	// the most recent completed run: "resolved" | "unresolved" | "escalate".
	// Empty when the run produced no FINAL_RESULT block. Persisted from the
	// raw agent output before the response formatter can rewrite it. A
	// self-report, not ground truth — resolution quality signals additionally
	// come from the monitor window and alert resolved_at.
	ResultStatus string `gorm:"size:16;index" json:"result_status,omitempty"`

	// AlertCount is not stored; populated by API handlers via COUNT query.
	AlertCount int64 `gorm:"-" json:"alert_count"`

	// The agent work lives on agent_runs. The detail endpoint fills these
	// transient fields from the incident's runs: Response is the newest
	// answer, FullLog is every run's log (oldest first, one header per run),
	// TokensUsed and ExecutionTimeMs are the sums, Runs lists the runs
	// without their payload.
	Response        string     `gorm:"-" json:"response,omitempty"`
	FullLog         string     `gorm:"-" json:"full_log,omitempty"`
	TokensUsed      int64      `gorm:"-" json:"tokens_used"`
	ExecutionTimeMs int64      `gorm:"-" json:"execution_time_ms"`
	Runs            []AgentRun `gorm:"-" json:"runs,omitempty"`

	// FirstSeen, LastSeen, and Trend are transient; populated by the list endpoint.
	FirstSeen *time.Time `gorm:"-" json:"first_seen,omitempty"`
	LastSeen  *time.Time `gorm:"-" json:"last_seen,omitempty"`
	Trend     []int      `gorm:"-" json:"trend,omitempty"`
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
