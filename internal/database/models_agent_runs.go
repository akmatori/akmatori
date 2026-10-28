package database

import (
	"time"

	"github.com/google/uuid"
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

// AgentRun records one StartIncident invocation: which model ran, what it
// cost, how long it took, how it ended, and the agent's work (log, response,
// session). One incident or request can have many runs (each Slack turn and
// each proposal chat turn is a fresh run) and each run keeps its own numbers
// and its own log.
//
// ScopeUUID is the work scope the worker, the MCP gateway and the work
// directory key the run by (the "incident_id" on the worker wire): the
// incident UUID for incident work, else the UUID of the request event that
// started the request (a Slack thread, an API task, a cron tick, a proposal
// chat). Follow-up turns reuse the scope.
//
// Retention never deletes run rows: they are the evidence base for per-model
// performance statistics. It clears the payload columns (FullLog, Response)
// of runs whose incident or request expired.
//
// CostUSD is a price snapshot computed by the agent worker at run time from
// the SDK's per-model pricing table. Token counts are the durable ground
// truth; the dollar figure can be recomputed later against a different price
// table. Custom/on-prem models report 0 until operator-supplied pricing
// exists.
type AgentRun struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	RunID     string `gorm:"size:36;uniqueIndex;not null" json:"run_id"`
	ScopeUUID string `gorm:"size:36;index" json:"scope_uuid"`
	// IncidentUUID is the incident the run works on; empty for request runs.
	IncidentUUID string `gorm:"size:36;index" json:"incident_uuid,omitempty"`
	// EventUUID is the event that triggered the run (the alert, the Slack
	// message, the API task, the cron tick, the chat message).
	EventUUID string `gorm:"size:36;index" json:"event_uuid,omitempty"`

	// SourceKind and SourceUUID describe the trigger ("alert" + alert source,
	// "cron" + cron job, "slack_mention", "manual", "proposal" + proposal).
	SourceKind string `gorm:"size:32;index" json:"source_kind"`
	SourceUUID string `gorm:"size:64;index" json:"source_uuid,omitempty"`
	// RootSkill is the system skill the run started under
	// ("incident-manager", "cron-agent", "proposal-editor").
	RootSkill string `gorm:"size:32" json:"root_skill,omitempty"`

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

	// Agent work. FullLog is the streamed log plus the raw final answer;
	// Response is the stored answer (formatted when a formatting rule ran).
	SessionID  string `gorm:"size:255" json:"session_id,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	FullLog    string `gorm:"type:text" json:"full_log,omitempty"`
	Response   string `gorm:"type:text" json:"response,omitempty"`
	// LastSkillUsed is the last skill whose SKILL.md the agent read, reported
	// on the agent_completed frame. A formatting-rule match dimension.
	LastSkillUsed string `gorm:"size:64" json:"last_skill_used,omitempty"`

	// SlackChannelID and SlackThreadTS are the Slack thread a request run
	// answers in. A mention in that thread continues the same scope.
	SlackChannelID string `gorm:"size:64" json:"slack_channel_id,omitempty"`
	SlackThreadTS  string `gorm:"size:64;index" json:"slack_thread_ts,omitempty"`

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

// BeginAgentRun inserts the run row before the run is dispatched, so every
// log and response write of the run targets one known row. RunID, StartedAt
// and Status are filled when empty.
func BeginAgentRun(db *gorm.DB, run *AgentRun) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if run.RunID == "" {
		run.RunID = uuid.NewString()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if run.Status == "" {
		run.Status = AgentRunStatusRunning
	}
	return db.Create(run).Error
}

// RecordAgentRunDispatch stamps the model that runs the run when it is sent
// to the worker. A run with no BeginAgentRun row (a caller that did not
// create one) gets a row here, so metrics are still recorded. Best-effort
// telemetry: callers log and continue on error.
func RecordAgentRunDispatch(runID, scopeUUID, provider, model, thinkingLevel string) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	res := DB.Model(&AgentRun{}).Where("run_id = ?", runID).Updates(map[string]any{
		"provider":       provider,
		"model":          model,
		"thinking_level": thinkingLevel,
	})
	if res.Error != nil || res.RowsAffected > 0 {
		return res.Error
	}
	run := &AgentRun{
		RunID:         runID,
		ScopeUUID:     scopeUUID,
		Provider:      provider,
		Model:         model,
		ThinkingLevel: thinkingLevel,
	}
	// Best-effort: a scope that is an incident gets the incident link and
	// its source kind, so per-kind stats keep working.
	var inc Incident
	if err := DB.Select("uuid, source_kind, source_uuid").Where("uuid = ?", scopeUUID).
		Limit(1).Find(&inc).Error; err == nil && inc.UUID != "" {
		run.IncidentUUID, run.SourceKind, run.SourceUUID = inc.UUID, inc.SourceKind, inc.SourceUUID
	}
	return BeginAgentRun(DB, run)
}

// UpdateAgentRunLog replaces the run's streamed log.
func UpdateAgentRunLog(runID, fullLog string) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	return DB.Model(&AgentRun{}).Where("run_id = ?", runID).Update("full_log", fullLog).Error
}

// AgentRunResult is the agent work a finished run stores.
type AgentRunResult struct {
	Failed    bool
	SessionID string
	FullLog   string
	Response  string
}

// FinishAgentRun stores the run's log, answer and session and ends the run:
// a run still in the running state becomes completed or failed. Called by the
// run's waiter after the worker frames (which only record metrics), so the
// terminal status appears together with the answer. A superseded run keeps
// its status.
func FinishAgentRun(db *gorm.DB, runID string, result AgentRunResult) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	terminal := AgentRunStatusCompleted
	if result.Failed {
		terminal = AgentRunStatusFailed
	}
	now := time.Now()
	return db.Model(&AgentRun{}).Where("run_id = ?", runID).Updates(map[string]any{
		"session_id": result.SessionID,
		"full_log":   result.FullLog,
		"response":   result.Response,
		"status": gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END",
			string(AgentRunStatusRunning), string(terminal)),
		"completed_at": gorm.Expr("COALESCE(completed_at, ?)", now),
	}).Error
}

// GetAgentRun loads one run by run_id.
func GetAgentRun(runID string) (*AgentRun, error) {
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	var run AgentRun
	if err := DB.Where("run_id = ?", runID).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

// LatestScopeRun returns the newest run of a scope, or nil when the scope
// has no run.
func LatestScopeRun(db *gorm.DB, scopeUUID string) (*AgentRun, error) {
	var runs []AgentRun
	if err := db.Where("scope_uuid = ?", scopeUUID).Order("id DESC").Limit(1).Find(&runs).Error; err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// LatestRunResponses returns, per incident, the response of its newest run
// that has one. Incidents with no answered run are absent from the map.
func LatestRunResponses(db *gorm.DB, incidentUUIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(incidentUUIDs))
	if len(incidentUUIDs) == 0 {
		return out, nil
	}
	var rows []AgentRun
	if err := db.Select("incident_uuid, response, id").
		Where("incident_uuid IN ? AND response <> ''", incidentUUIDs).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IncidentUUID] = r.Response // ascending: the newest wins
	}
	return out, nil
}

// CompleteAgentRun records the usage metrics and the parsed outcome of an
// agent_completed frame. It does NOT end the run: the run stays "running"
// until its waiter has stored the answer (FinishAgentRun), so a reader never
// sees a finished run with no answer yet (the response formatter can take
// seconds). A superseded run keeps its status and still records its numbers —
// the tokens were spent either way.
func CompleteAgentRun(runID string, metrics AgentRunMetrics, outcome string) error {
	return recordAgentRunEnd(runID, map[string]any{
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

// FailAgentRun records the error of an agent_error frame (or of a worker
// disconnect). Like CompleteAgentRun it leaves the status to the waiter,
// which stores the error answer with FinishAgentRun(Failed: true).
func FailAgentRun(runID, errorMessage string) error {
	return recordAgentRunEnd(runID, map[string]any{
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

// FailInterruptedAgentRuns marks every run still in the running state as
// failed. Call it once at startup, before the agent worker can connect: the
// callbacks of runs that were in flight lived in the previous process, so no
// frame can finalize those rows any more. Returns the number of rows changed.
func FailInterruptedAgentRuns(reason string) (int64, error) {
	if DB == nil {
		return 0, gorm.ErrInvalidDB
	}
	res := DB.Model(&AgentRun{}).
		Where("status = ?", AgentRunStatusRunning).
		Updates(map[string]any{
			"status":        AgentRunStatusFailed,
			"error_message": reason,
			"completed_at":  time.Now(),
		})
	return res.RowsAffected, res.Error
}

func recordAgentRunEnd(runID string, updates map[string]any) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	now := time.Now()
	updates["completed_at"] = &now
	return DB.Model(&AgentRun{}).Where("run_id = ?", runID).Updates(updates).Error
}
