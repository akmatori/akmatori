package database

import (
	"time"

	"gorm.io/gorm"
)

// AgentRunStatus tracks the lifecycle of a single agent invocation.
type AgentRunStatus string

const (
	AgentRunStatusRunning    AgentRunStatus = "running"
	AgentRunStatusCompleted  AgentRunStatus = "completed"
	AgentRunStatusFailed     AgentRunStatus = "failed"
	AgentRunStatusSuperseded AgentRunStatus = "superseded"
)

// AgentRunOutcome values mirror the [FINAL_RESULT] status vocabulary parsed
// by internal/output. Empty when the run produced no FINAL_RESULT block
// (cron runs, proposal chat, failed runs).
const (
	AgentRunOutcomeResolved   = "resolved"
	AgentRunOutcomeUnresolved = "unresolved"
	AgentRunOutcomeEscalate   = "escalate"
)

// AgentRun records one StartIncident/ContinueIncident invocation: which model
// ran, what it cost, how long it took, and how it ended. One incident can have
// many runs (each Slack turn spawns a fresh run) and each run keeps its own
// numbers, unlike Incident.TokensUsed which accumulates across runs.
//
// Rows are deliberately NOT deleted when incident retention removes the parent
// incident: they are the evidence base for per-model performance statistics
// and carry no sensitive payload (no prompt, no response text).
//
// CostUSD is a price snapshot computed by the agent worker at run time from
// the SDK's per-model pricing table. Token counts are the durable ground
// truth; the dollar figure can be recomputed later against a different price
// table. Custom/on-prem models report 0 until operator-supplied pricing
// exists.
type AgentRun struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	RunID        string `gorm:"size:36;uniqueIndex;not null" json:"run_id"`
	IncidentUUID string `gorm:"size:36;index;not null" json:"incident_uuid"`

	// SourceKind is denormalized from the incident row so per-kind stats
	// survive incident retention. Empty when the incident row was not
	// readable at run start.
	SourceKind string `gorm:"size:32;index" json:"source_kind"`

	Provider      string `gorm:"size:32;index" json:"provider"`
	Model         string `gorm:"size:128;index" json:"model"`
	ThinkingLevel string `gorm:"size:16" json:"thinking_level"`

	Status  AgentRunStatus `gorm:"size:16;index;not null;default:'running'" json:"status"`
	Outcome string         `gorm:"size:16" json:"outcome,omitempty"`

	TokensUsed       int64 `json:"tokens_used"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`

	CostUSD float64 `gorm:"column:cost_usd" json:"cost_usd"`

	ExecutionTimeMs int64  `json:"execution_time_ms"`
	ErrorMessage    string `gorm:"type:text" json:"error_message,omitempty"`

	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (AgentRun) TableName() string {
	return "agent_runs"
}

// AgentRunMetrics carries the per-run usage numbers reported by the worker on
// an agent_completed frame.
type AgentRunMetrics struct {
	TokensUsed       int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	ExecutionTimeMs  int64
}

// CreateAgentRun inserts the run row at dispatch time. Best-effort telemetry:
// callers log and continue on error — recording must never block an
// investigation.
func CreateAgentRun(runID, incidentUUID, provider, model, thinkingLevel string) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	var sourceKind string
	// Best-effort denormalization; the incident row exists by the time a run
	// is dispatched, but a missing row must not prevent run recording.
	DB.Model(&Incident{}).Where("uuid = ?", incidentUUID).
		Pluck("source_kind", &sourceKind)
	run := &AgentRun{
		RunID:         runID,
		IncidentUUID:  incidentUUID,
		SourceKind:    sourceKind,
		Provider:      provider,
		Model:         model,
		ThinkingLevel: thinkingLevel,
		Status:        AgentRunStatusRunning,
		StartedAt:     time.Now(),
	}
	return DB.Create(run).Error
}

// CompleteAgentRun finalizes the run row with usage metrics and the parsed
// outcome. A run displaced by a newer one keeps its "superseded" status but
// still records its numbers — the tokens were spent either way.
func CompleteAgentRun(runID string, metrics AgentRunMetrics, outcome string) error {
	return finalizeAgentRun(runID, AgentRunStatusCompleted, map[string]any{
		"tokens_used":        metrics.TokensUsed,
		"input_tokens":       metrics.InputTokens,
		"output_tokens":      metrics.OutputTokens,
		"cache_read_tokens":  metrics.CacheReadTokens,
		"cache_write_tokens": metrics.CacheWriteTokens,
		"cost_usd":           metrics.CostUSD,
		"execution_time_ms":  metrics.ExecutionTimeMs,
		"outcome":            outcome,
	})
}

// FailAgentRun finalizes the run row after an agent_error frame.
func FailAgentRun(runID, errorMessage string) error {
	return finalizeAgentRun(runID, AgentRunStatusFailed, map[string]any{
		"error_message": errorMessage,
	})
}

// SupersedeAgentRun marks a still-running run as displaced by a newer run for
// the same incident. Late completion/error frames from it still record their
// metrics but no longer change the status.
func SupersedeAgentRun(runID string) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	return DB.Model(&AgentRun{}).
		Where("run_id = ? AND status = ?", runID, AgentRunStatusRunning).
		Update("status", AgentRunStatusSuperseded).Error
}

func finalizeAgentRun(runID string, terminal AgentRunStatus, updates map[string]any) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	now := time.Now()
	updates["completed_at"] = &now
	// Only a live run transitions to the terminal status; a superseded run
	// keeps its label so stats can exclude displaced work.
	updates["status"] = gorm.Expr(
		"CASE WHEN status = ? THEN ? ELSE status END",
		string(AgentRunStatusRunning), string(terminal),
	)
	return DB.Model(&AgentRun{}).Where("run_id = ?", runID).Updates(updates).Error
}
