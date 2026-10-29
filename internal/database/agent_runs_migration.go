package database

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// splitRunsBatch bounds one step of migrateSplitAgentRuns. Each batch runs in
// one transaction, so a crash resumes with the incidents that are left.
const splitRunsBatch = 200

// legacyIncidentWorkColumns are the agent-work columns that moved from
// incidents to agent_runs. migrateSplitAgentRuns drops them at the end.
var legacyIncidentWorkColumns = []string{
	"session_id", "working_dir", "full_log", "response",
	"tokens_used", "execution_time_ms", "last_skill_used",
}

// RootSkillForSourceKind returns the system skill a run of this trigger kind
// starts under.
func RootSkillForSourceKind(sourceKind string) string {
	switch sourceKind {
	case IncidentSourceKindCron:
		return "cron-agent"
	case IncidentSourceKindProposal:
		return "proposal-editor"
	default:
		return "incident-manager"
	}
}

// legacyIncidentRow is an incidents row as it was before the runs split.
type legacyIncidentRow struct {
	UUID            string
	Source          string
	SourceID        string
	SourceKind      string
	SourceUUID      string
	Status          string
	Context         JSONB `gorm:"type:jsonb"`
	SessionID       string
	WorkingDir      string
	FullLog         string
	Response        string
	TokensUsed      int64
	ExecutionTimeMs int64
	LastSkillUsed   string
	SlackChannelID  string
	SlackMessageTS  string
	StartedAt       time.Time
	CompletedAt     *time.Time
}

// migrateSplitAgentRuns moves the agent work out of incidents (phase 5 of the
// events redesign): the agent-work columns move onto agent_runs, and every
// non-alert incident (cron, Slack mention, API task, proposal chat) becomes
// request work: its runs keep the incident UUID as their scope, its events
// point at that scope, and the incidents row is deleted. Then the columns
// are dropped.
//
// Idempotent: it runs only while incidents.full_log exists, request
// incidents are gone once moved, and work is copied only onto a run that has
// none. The caller logs a failure
// and continues; the columns are dropped only after every incident moved, so
// a failed run loses nothing and resumes on the next start.
func migrateSplitAgentRuns(db *gorm.DB) error {
	// Silent: a slow UPDATE would otherwise print its SQL, full log included
	// (27 MB of startup log on a copy of gcore). Errors are returned.
	db = db.Session(&gorm.Session{NewDB: true, Logger: db.Logger.LogMode(logger.Silent)})
	m := db.Migrator()
	if !m.HasTable("incidents") || !m.HasTable("agent_runs") || !m.HasColumn("incidents", "full_log") {
		return nil
	}

	cols := []string{"uuid", "source", "source_id", "source_kind", "source_uuid", "status", "context",
		"started_at", "completed_at", "slack_channel_id", "slack_message_ts"}
	for _, c := range legacyIncidentWorkColumns {
		if m.HasColumn("incidents", c) {
			cols = append(cols, c)
		}
	}
	selectList := strings.Join(cols, ", ")

	moved, requests, after := 0, 0, ""
	for {
		var rows []legacyIncidentRow
		if err := db.Raw(`SELECT `+selectList+` FROM incidents WHERE uuid > ? ORDER BY uuid ASC LIMIT ?`,
			after, splitRunsBatch).Scan(&rows).Error; err != nil {
			return fmt.Errorf("migrateSplitAgentRuns: query incidents: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		after = rows[len(rows)-1].UUID
		err := db.Transaction(func(tx *gorm.DB) error {
			for i := range rows {
				isRequest, err := splitIncidentRuns(tx, &rows[i])
				if err != nil {
					return fmt.Errorf("incident %s: %w", rows[i].UUID, err)
				}
				if isRequest {
					requests++
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migrateSplitAgentRuns: %w", err)
		}
		moved += len(rows)
		if len(rows) < splitRunsBatch {
			break
		}
	}

	// Plain SQL: the SQLite migrator cannot drop a column by table name.
	// SQLite also refuses to drop an indexed column, so the session_id index
	// goes first (PostgreSQL drops it with the column anyway).
	if err := db.Exec("DROP INDEX IF EXISTS idx_incidents_session_id").Error; err != nil {
		return fmt.Errorf("migrateSplitAgentRuns: drop session_id index: %w", err)
	}
	for _, c := range legacyIncidentWorkColumns {
		if m.HasColumn("incidents", c) {
			if err := db.Exec("ALTER TABLE incidents DROP COLUMN " + c).Error; err != nil {
				return fmt.Errorf("migrateSplitAgentRuns: drop incidents.%s: %w", c, err)
			}
		}
	}
	slog.Info("migrateSplitAgentRuns: moved agent work to agent_runs",
		"incidents", moved, "request_work", requests)
	return nil
}

// splitIncidentRuns moves one incident's agent work onto its runs. It
// reports whether the incident was request work (and is now deleted).
func splitIncidentRuns(tx *gorm.DB, inc *legacyIncidentRow) (bool, error) {
	isAlert := inc.SourceKind == IncidentSourceKindAlert
	if !isAlert {
		// Rows from before source_kind existed: an incident with alerts is
		// alert work.
		var n int64
		if err := tx.Model(&Alert{}).Where("incident_uuid = ?", inc.UUID).Count(&n).Error; err != nil {
			return false, err
		}
		isAlert = n > 0
	}

	var runs []AgentRun
	if err := tx.Where("incident_uuid = ?", inc.UUID).Order("id ASC").Find(&runs).Error; err != nil {
		return false, err
	}
	if len(runs) == 0 {
		// Incidents from before agent_runs existed get one run that holds
		// their work. It has no model, so per-model stats skip it.
		run := AgentRun{
			RunID:           uuid.New().String(),
			IncidentUUID:    inc.UUID,
			SourceKind:      inc.SourceKind,
			Status:          legacyRunStatus(inc.Status),
			TokensUsed:      inc.TokensUsed,
			ExecutionTimeMs: inc.ExecutionTimeMs,
			StartedAt:       inc.StartedAt,
			CompletedAt:     inc.CompletedAt,
			CreatedAt:       inc.StartedAt,
		}
		if err := tx.Create(&run).Error; err != nil {
			return false, err
		}
		runs = append(runs, run)
	}

	// The request event that started request work; its runs point at it.
	var firstEvent string
	if !isAlert {
		var ev Event
		if err := tx.Select("uuid").Where("incident_uuid = ?", inc.UUID).
			Order("received_at ASC").Limit(1).Find(&ev).Error; err != nil {
			return false, err
		}
		firstEvent = ev.UUID
	}

	for i, r := range runs {
		updates := map[string]any{
			"scope_uuid": inc.UUID,
			"root_skill": RootSkillForSourceKind(inc.SourceKind),
		}
		if r.SourceUUID == "" && inc.SourceUUID != "" {
			updates["source_uuid"] = inc.SourceUUID
		}
		if r.SourceKind == "" && inc.SourceKind != "" {
			updates["source_kind"] = inc.SourceKind
		}
		if !isAlert {
			updates["incident_uuid"] = ""
			if r.EventUUID == "" && firstEvent != "" {
				updates["event_uuid"] = firstEvent
			}
			if inc.Source == "slack" && inc.SourceID != "" {
				updates["slack_thread_ts"] = inc.SourceID
				if ch, _ := inc.Context["channel"].(string); ch != "" {
					updates["slack_channel_id"] = ch
				}
			}
		}
		// The incident row held the work of its newest run only. A run that
		// already has work (a re-run after a failed migration) keeps it.
		if i == len(runs)-1 && r.FullLog == "" && r.Response == "" {
			updates["session_id"] = inc.SessionID
			updates["working_dir"] = inc.WorkingDir
			updates["full_log"] = inc.FullLog
			updates["response"] = inc.Response
			updates["last_skill_used"] = inc.LastSkillUsed
		}
		if err := tx.Model(&AgentRun{}).Where("id = ?", r.ID).Updates(updates).Error; err != nil {
			return false, err
		}
	}

	if isAlert {
		return false, nil
	}
	if err := tx.Model(&Event{}).Where("incident_uuid = ?", inc.UUID).
		Updates(map[string]any{"scope_uuid": inc.UUID, "incident_uuid": ""}).Error; err != nil {
		return false, err
	}
	if err := tx.Exec("DELETE FROM incidents WHERE uuid = ?", inc.UUID).Error; err != nil {
		return false, err
	}
	return true, nil
}

// legacyRunStatus maps an incident status to the status of the run that
// did its work.
func legacyRunStatus(incidentStatus string) AgentRunStatus {
	switch IncidentStatus(incidentStatus) {
	case IncidentStatusFailed, IncidentStatusPending, IncidentStatusRunning:
		// A pending or running row at migration time was cut off by the
		// restart that runs this migration.
		return AgentRunStatusFailed
	default:
		return AgentRunStatusCompleted
	}
}
