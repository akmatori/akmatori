package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AgentRun mirrors the main API's AgentRun model
// (internal/database/models_agent_runs.go): one agent invocation with its log
// and answer. ScopeUUID is the X-Incident-ID the worker sends: the incident
// UUID for incident work, else the request scope. Only the columns the gateway
// reads are mirrored.
type AgentRun struct {
	ID              uint       `gorm:"primaryKey"`
	RunID           string     `gorm:"size:36;uniqueIndex;not null"`
	ScopeUUID       string     `gorm:"size:36;index"`
	IncidentUUID    string     `gorm:"size:36;index"`
	EventUUID       string     `gorm:"size:36"`
	SourceKind      string     `gorm:"size:32"`
	SourceUUID      string     `gorm:"size:64"`
	Status          string     `gorm:"size:16"`
	TokensUsed      int64      ``
	ExecutionTimeMs int64      ``
	FullLog         string     `gorm:"type:text"`
	Response        string     `gorm:"type:text"`
	StartedAt       time.Time  ``
	CompletedAt     *time.Time ``
}

func (AgentRun) TableName() string {
	return "agent_runs"
}

// RunsWork is the agent work of a set of runs: the newest answer, every
// log (oldest first, one header per run when there is more than one) and
// the token and time sums.
type RunsWork struct {
	Runs            int    `json:"runs"`
	Status          string `json:"status,omitempty"`
	Response        string `json:"response"`
	FullLog         string `json:"full_log"`
	TokensUsed      int64  `json:"tokens_used"`
	ExecutionTimeMs int64  `json:"execution_time_ms"`
}

// LoadRunsWork folds the runs where column = value into RunsWork. The log
// keeps its newest maxLog bytes (the end of an investigation is where the
// answer is); maxLog <= 0 keeps it all.
func LoadRunsWork(ctx context.Context, db *gorm.DB, column, value string, maxLog int) (RunsWork, error) {
	var runs []AgentRun
	if err := db.WithContext(ctx).Where(column+" = ?", value).Order("id ASC").Find(&runs).Error; err != nil {
		return RunsWork{}, err
	}
	w := RunsWork{Runs: len(runs)}
	var log strings.Builder
	for i, r := range runs {
		w.TokensUsed += r.TokensUsed
		w.ExecutionTimeMs += r.ExecutionTimeMs
		w.Status = r.Status
		if r.Response != "" {
			w.Response = r.Response
		}
		if r.FullLog != "" {
			if len(runs) > 1 {
				fmt.Fprintf(&log, "=== Run %d of %d, started %s ===\n\n", i+1, len(runs), r.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
			}
			log.WriteString(r.FullLog)
			log.WriteString("\n\n")
		}
	}
	w.FullLog = strings.TrimRight(log.String(), "\n")
	if maxLog > 0 && len(w.FullLog) > maxLog {
		// Drop any incomplete multi-byte sequence at the boundary.
		w.FullLog = strings.ToValidUTF8(w.FullLog[len(w.FullLog)-maxLog:], "")
	}
	return w, nil
}
