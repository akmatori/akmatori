package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// runsWork is the agent work of a set of runs, as the incident and request
// detail pages show it.
type runsWork struct {
	// Response is the newest answer.
	Response string
	// FullLog is every run's log, oldest first, with a header per run when
	// there is more than one.
	FullLog         string
	TokensUsed      int64
	ExecutionTimeMs int64
	// Runs are the runs without their payload columns.
	Runs []database.AgentRun
}

// loadRuns returns the runs matching the condition, oldest first.
func loadRuns(db *gorm.DB, cond string, args ...interface{}) ([]database.AgentRun, error) {
	var runs []database.AgentRun
	err := db.Where(cond, args...).Order("id ASC").Find(&runs).Error
	return runs, err
}

// summarizeRuns folds runs (oldest first) into the work a detail page shows.
func summarizeRuns(runs []database.AgentRun) runsWork {
	var w runsWork
	var log strings.Builder
	for i := range runs {
		r := &runs[i]
		w.TokensUsed += r.TokensUsed
		w.ExecutionTimeMs += r.ExecutionTimeMs
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
		r.FullLog, r.Response, r.SessionID = "", "", ""
	}
	w.FullLog = strings.TrimRight(log.String(), "\n")
	w.Runs = runs
	return w
}

// requestDetail is request work (agent work that is not an incident: a Slack
// or API request, a cron tick, a proposal chat) as the request page shows it.
type requestDetail struct {
	UUID        string     `json:"uuid"`
	Title       string     `json:"title"`
	SourceKind  string     `json:"source_kind"`
	SourceUUID  string     `json:"source_uuid,omitempty"`
	Status      string     `json:"status"`
	EventUUID   string     `json:"event_uuid,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	Response        string              `json:"response,omitempty"`
	FullLog         string              `json:"full_log,omitempty"`
	TokensUsed      int64               `json:"tokens_used"`
	ExecutionTimeMs int64               `json:"execution_time_ms"`
	Runs            []database.AgentRun `json:"runs"`
}

// requestStatus maps the newest run's status to the request's status.
func requestStatus(newest database.AgentRun) string {
	switch newest.Status {
	case database.AgentRunStatusRunning:
		return "running"
	case database.AgentRunStatusFailed:
		return "failed"
	default:
		return "completed"
	}
}

// loadRequest builds the request detail of a scope. found is false when the
// scope has no run.
func loadRequest(db *gorm.DB, scopeUUID string) (req requestDetail, found bool, err error) {
	runs, err := loadRuns(db, "scope_uuid = ? AND (incident_uuid IS NULL OR incident_uuid = '')", scopeUUID)
	if err != nil || len(runs) == 0 {
		return req, false, err
	}
	first, newest := runs[0], runs[len(runs)-1]
	req = requestDetail{
		UUID:        scopeUUID,
		SourceKind:  first.SourceKind,
		SourceUUID:  first.SourceUUID,
		Status:      requestStatus(newest),
		EventUUID:   first.EventUUID,
		StartedAt:   first.StartedAt,
		CompletedAt: newest.CompletedAt,
	}
	if req.Status == "running" {
		req.CompletedAt = nil
	}
	// The title is the title of the event that started the request.
	var ev database.Event
	for _, id := range []string{scopeUUID, first.EventUUID} {
		if id == "" {
			continue
		}
		if err := db.Select("title").Where("uuid = ?", id).Limit(1).Find(&ev).Error; err == nil && ev.Title != "" {
			req.Title = ev.Title
			break
		}
	}
	if req.Title == "" {
		req.Title = "Request " + scopeUUID[:min(8, len(scopeUUID))]
	}
	work := summarizeRuns(runs)
	req.Response, req.FullLog = work.Response, work.FullLog
	req.TokensUsed, req.ExecutionTimeMs, req.Runs = work.TokensUsed, work.ExecutionTimeMs, work.Runs
	return req, true, nil
}

// handleRequestByID handles GET /api/requests/{uuid}: request work with its
// runs, newest answer and log.
func (h *APIHandler) handleRequestByID(w http.ResponseWriter, r *http.Request) {
	scopeUUID := r.PathValue("uuid")
	req, found, err := loadRequest(database.GetDB(), scopeUUID)
	if err != nil {
		slog.Error("request detail: failed to load", "uuid", scopeUUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to load request")
		return
	}
	if !found {
		api.RespondError(w, http.StatusNotFound, "Request not found")
		return
	}
	api.RespondJSON(w, http.StatusOK, req)
}

// handleRequestResponse handles GET /api/requests/{uuid}/response — the same
// lightweight projection as /api/incidents/{uuid}/response, for the Events
// page's inline answer of a request event.
func (h *APIHandler) handleRequestResponse(w http.ResponseWriter, r *http.Request) {
	scopeUUID := r.PathValue("uuid")
	req, found, err := loadRequest(database.GetDB(), scopeUUID)
	if err != nil {
		slog.Error("request response: failed to load", "uuid", scopeUUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to load request")
		return
	}
	if !found {
		api.RespondError(w, http.StatusNotFound, "Request not found")
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]string{
		"uuid":     req.UUID,
		"title":    req.Title,
		"status":   req.Status,
		"response": req.Response,
	})
}
