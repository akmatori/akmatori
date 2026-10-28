package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	"gorm.io/gorm"
)

// handleIncidents handles GET /api/incidents and POST /api/incidents
func (h *APIHandler) handleIncidents(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()

	switch r.Method {
	case http.MethodGet:
		var incidents []database.Incident
		query := db.Order("created_at DESC")

		fromParam := r.URL.Query().Get("from")
		toParam := r.URL.Query().Get("to")
		statusParam := r.URL.Query().Get("status")

		if fromParam != "" {
			from, err := strconv.ParseInt(fromParam, 10, 64)
			if err == nil {
				query = query.Where("created_at >= ?", time.Unix(from, 0))
			}
		}
		if toParam != "" {
			to, err := strconv.ParseInt(toParam, 10, 64)
			if err == nil {
				query = query.Where("created_at <= ?", time.Unix(to, 0))
			}
		}
		if statusParam != "" {
			query = applyIncidentStatusFilter(query, statusParam)
		}

		// Always use pagination (defaults: page=1, per_page=50)
		params := api.ParsePagination(r)

		var total int64
		countQuery := db.Model(&database.Incident{})
		if fromParam != "" {
			if from, err := strconv.ParseInt(fromParam, 10, 64); err == nil {
				countQuery = countQuery.Where("created_at >= ?", time.Unix(from, 0))
			}
		}
		if toParam != "" {
			if to, err := strconv.ParseInt(toParam, 10, 64); err == nil {
				countQuery = countQuery.Where("created_at <= ?", time.Unix(to, 0))
			}
		}
		if statusParam != "" {
			countQuery = applyIncidentStatusFilter(countQuery, statusParam)
		}
		if err := countQuery.Count(&total).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to count incidents")
			return
		}

		if err := query.Offset(params.Offset()).Limit(params.PerPage).Find(&incidents).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get incidents")
			return
		}

		// Enrich incidents with alert aggregation fields.
		if len(incidents) > 0 {
			uuids := make([]string, len(incidents))
			for i, inc := range incidents {
				uuids[i] = inc.UUID
			}

			// Parse trend_window (default "1h", also accept "3h").
			trendWindowParam := r.URL.Query().Get("trend_window")
			var trendWindow time.Duration
			switch trendWindowParam {
			case "3h":
				trendWindow = 3 * time.Hour
			default:
				trendWindow = time.Hour
			}

			// One pass over the page's alert rows supplies both the
			// count/first/last aggregate and the in-window sparkline buckets.
			//
			// This deliberately selects fired_at per row and folds in Go rather
			// than using MIN()/MAX() in SQL: a computed datetime comes back
			// from SQLite as a string the driver cannot scan into time.Time, so
			// the aggregate form silently dropped every enrichment under tests
			// (it works on PostgreSQL, which types the result). Folding in Go
			// works on both and costs one query instead of two.
			windowEnd := time.Now()
			windowStart := windowEnd.Add(-trendWindow)

			type alertTsRow struct {
				IncidentUUID string
				FiredAt      time.Time
			}
			var tsRows []alertTsRow
			if err := db.Model(&database.Alert{}).
				Select("incident_uuid, fired_at").
				Where("incident_uuid IN ?", uuids).
				Scan(&tsRows).Error; err != nil {
				slog.Warn("failed to fetch alert timestamps", "err", err)
			}

			type alertAggRow struct {
				Count     int64
				FirstSeen *time.Time
				LastSeen  *time.Time
			}
			aggMap := make(map[string]alertAggRow, len(incidents))
			tsMap := make(map[string][]time.Time, len(incidents))
			for _, row := range tsRows {
				agg := aggMap[row.IncidentUUID]
				agg.Count++
				if agg.FirstSeen == nil || row.FiredAt.Before(*agg.FirstSeen) {
					firedAt := row.FiredAt
					agg.FirstSeen = &firedAt
				}
				if agg.LastSeen == nil || row.FiredAt.After(*agg.LastSeen) {
					firedAt := row.FiredAt
					agg.LastSeen = &firedAt
				}
				aggMap[row.IncidentUUID] = agg

				if !row.FiredAt.Before(windowStart) {
					tsMap[row.IncidentUUID] = append(tsMap[row.IncidentUUID], row.FiredAt)
				}
			}

			const trendBuckets = 12

			for i := range incidents {
				uuid := incidents[i].UUID
				if agg, ok := aggMap[uuid]; ok {
					incidents[i].AlertCount = agg.Count
					incidents[i].FirstSeen = agg.FirstSeen
					incidents[i].LastSeen = agg.LastSeen
				}
				if ts, ok := tsMap[uuid]; ok {
					incidents[i].Trend = bucketTimestamps(ts, windowStart, windowEnd, trendBuckets)
				} else {
					incidents[i].Trend = make([]int, trendBuckets)
				}
			}
		}

		api.RespondJSON(w, http.StatusOK, api.PaginatedResponse{
			Data: incidents,
			Pagination: api.PaginationMeta{
				Page:       params.Page,
				PerPage:    params.PerPage,
				Total:      total,
				TotalPages: params.TotalPages(total),
			},
		})

	case http.MethodPost:
		var req api.CreateIncidentRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Task == "" {
			api.RespondError(w, http.StatusBadRequest, "Task is required")
			return
		}

		// Classify the task first: a report of a problem goes to the
		// correlation gate as a reported alert (only alerts open incidents);
		// anything else is a request and starts an agent run as before.
		eventUUID, reported, handled := h.classifyManualTask(r.Context(), req.Task)
		if handled && reported.storedKind != "" {
			api.RespondJSON(w, http.StatusCreated, api.CreateIncidentResponse{
				Status:    "recorded",
				Message:   fmt.Sprintf("Task stored as a %s event; no agent run started", reported.storedKind),
				EventUUID: eventUUID,
				EventKind: reported.storedKind,
			})
			return
		}
		if handled {
			if reported.err != nil {
				api.RespondError(w, http.StatusInternalServerError, "Failed to open incident for reported alert")
				return
			}
			status, msg := "pending", "Reported alert opened a new incident"
			if reported.linked {
				status, msg = "linked", "Reported alert linked to an open incident"
			}
			api.RespondJSON(w, http.StatusCreated, api.CreateIncidentResponse{
				UUID:      reported.incidentUUID,
				Status:    status,
				Message:   msg,
				EventUUID: eventUUID,
				EventKind: database.EventKindAlert,
			})
			return
		}

		// A request is agent work, not an incident: only alerts open
		// incidents. The returned uuid is the request scope (GET
		// /api/requests/{uuid}).
		scopeUUID, workingDir, err := h.skillService.PrepareRequestScope("incident-manager", eventUUID)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to start request")
			return
		}

		slog.Info("started request via API", "scope", scopeUUID)

		taskHeader := fmt.Sprintf("📝 API Task:\n%s\n\n--- Execution Log ---\n\n", req.Task)
		go h.runAgentInvestigation(services.RunStart{
			ScopeUUID:  scopeUUID,
			EventUUID:  eventUUID,
			SourceKind: database.IncidentSourceKindManual,
			SourceUUID: manualEventSource,
			RootSkill:  "incident-manager",
		}, taskHeader, req.Task)

		api.RespondJSON(w, http.StatusCreated, api.CreateIncidentResponse{
			UUID:       scopeUUID,
			Status:     "running",
			WorkingDir: workingDir,
			Message:    "Request started",
			EventUUID:  eventUUID,
			EventKind:  database.EventKindRequest,
		})

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleIncidentAlerts handles GET /api/incidents/{uuid}/alerts — returns the
// alert rows attached to an incident, ordered by fired_at ASC.
func (h *APIHandler) handleIncidentAlerts(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	db := database.GetDB()

	// Verify incident exists first.
	var count int64
	if err := db.Model(&database.Incident{}).Where("uuid = ?", uuid).Count(&count).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to verify incident")
		return
	}
	if count == 0 {
		api.RespondError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var alerts []database.Alert
	if err := db.Where("incident_uuid = ?", uuid).Order("fired_at ASC").Find(&alerts).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get alerts")
		return
	}

	api.RespondJSON(w, http.StatusOK, alerts)
}

// handleIncidentByID handles GET /api/incidents/{uuid}
func (h *APIHandler) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	incident, err := h.skillService.GetIncident(uuid)
	if err != nil {
		api.RespondError(w, http.StatusNotFound, "Incident not found")
		return
	}

	db := database.GetDB()
	var cnt int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", incident.UUID).Count(&cnt)
	incident.AlertCount = cnt

	runs, err := loadRuns(db, "incident_uuid = ?", incident.UUID)
	if err != nil {
		slog.Error("incident detail: failed to load runs", "uuid", incident.UUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to load incident runs")
		return
	}
	work := summarizeRuns(runs)
	incident.Response, incident.FullLog = work.Response, work.FullLog
	incident.TokensUsed, incident.ExecutionTimeMs = work.TokensUsed, work.ExecutionTimeMs
	incident.Runs = work.Runs

	api.RespondJSON(w, http.StatusOK, incident)
}

// handleIncidentResponse handles GET /api/incidents/{uuid}/response — a
// lightweight projection (the newest run answer, no log) used by the Events
// page's inline response expansion so it doesn't pull megabytes of agent log
// per click.
func (h *APIHandler) handleIncidentResponse(w http.ResponseWriter, r *http.Request) {
	incidentUUID := r.PathValue("uuid")
	db := database.GetDB()

	var row struct {
		UUID     string `json:"uuid"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Response string `json:"response"`
	}
	err := db.Model(&database.Incident{}).
		Select("uuid, title, status").
		Where("uuid = ?", incidentUUID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			api.RespondError(w, http.StatusNotFound, "Incident not found")
		} else {
			slog.Error("incident response: failed to load", "uuid", incidentUUID, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to load incident")
		}
		return
	}
	responses, err := database.LatestRunResponses(db, []string{incidentUUID})
	if err != nil {
		slog.Error("incident response: failed to load runs", "uuid", incidentUUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to load incident")
		return
	}
	row.Response = responses[incidentUUID]
	api.RespondJSON(w, http.StatusOK, row)
}

// incidentCloseRequest is the body for POST /api/incidents/{uuid}/close.
type incidentCloseRequest struct {
	// Confirm must be true to close an incident that still has firing alerts
	// linked; those alerts are resolved as part of the close. Without it, a
	// close attempt against a firing incident returns 409 with the firing
	// count so the caller can prompt the operator first.
	Confirm bool `json:"confirm"`
}

// handleIncidentClose handles POST /api/incidents/{uuid}/close. It manually
// closes an incident. Returns 404 if missing, 409 if already closed, and 409
// with a requires_confirmation body (firing_alert_count, in_progress) if the
// incident has firing alerts and/or is still pending/running and confirm was
// not set — confirming force-closes it regardless, resolving any linked
// firing alerts.
func (h *APIHandler) handleIncidentClose(w http.ResponseWriter, r *http.Request) {
	incidentUUID := r.PathValue("uuid")

	var req incidentCloseRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	err := h.skillService.CloseIncident(r.Context(), incidentUUID, req.Confirm)
	if err == nil {
		api.RespondJSON(w, http.StatusOK, map[string]string{"status": "closed"})
		return
	}

	var confirmErr *services.ErrConfirmationRequired
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		api.RespondError(w, http.StatusNotFound, "Incident not found")
	case errors.Is(err, services.ErrIncidentAlreadyClosed):
		api.RespondError(w, http.StatusConflict, "incident is already closed")
	case errors.As(err, &confirmErr):
		api.RespondJSON(w, http.StatusConflict, map[string]interface{}{
			"error":                 confirmErr.Error(),
			"requires_confirmation": true,
			"firing_alert_count":    confirmErr.FiringAlertCount,
			"in_progress":           confirmErr.InProgress,
		})
	default:
		slog.Error("CloseIncident failed", "incident", incidentUUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to close incident")
	}
}

// handleIncidentSweepStale handles POST /api/incidents/sweep-stale. It runs
// the stale-incident close sweep on demand.
//
// With ?dry_run=true it reports what would close without changing anything,
// and does so even when the gate is disabled — that is the point: an operator
// upgrading an install with a large backlog can see the blast radius, and tune
// incident_auto_close_minutes, before the first real sweep runs.
func (h *APIHandler) handleIncidentSweepStale(w http.ResponseWriter, r *http.Request) {
	if h.staleIncidentCloser == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "stale incident close service not available")
		return
	}

	dryRun := r.URL.Query().Get("dry_run") == "true"

	result, err := h.staleIncidentCloser.Run(r.Context(), dryRun)
	if err != nil {
		slog.Error("stale incident sweep failed", "dry_run", dryRun, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to run stale incident sweep")
		return
	}

	api.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"dry_run":          dryRun,
		"enabled":          result.Enabled,
		"window_minutes":   result.WindowMinutes,
		"cutoff":           result.Cutoff,
		"candidate_count":  len(result.Candidates),
		"candidates":       result.Candidates,
		"incidents_closed": result.IncidentsClosed,
		"truncated":        result.Truncated,
	})
}

// runAgentInvestigation runs one agent run: an API request (request scope)
// or the investigation of an unlinked alert (incident). It must be launched
// as a goroutine by the caller. taskHeader is prepended to all log updates;
// task is the raw user-facing task text (guidance is added internally via
// executor.PrependGuidance).
func (h *APIHandler) runAgentInvestigation(start services.RunStart, taskHeader, task string) {
	incidentUUID := start.ScopeUUID // the scope the worker keys the run by
	if incidentUUID == "" {
		incidentUUID = start.IncidentUUID
	}
	runID, err := h.skillService.BeginRun(start)
	if err != nil {
		slog.Error("failed to begin run", "scope", incidentUUID, "err", err)
		if start.IncidentUUID != "" {
			if uerr := h.skillService.UpdateIncidentStatus(start.IncidentUUID, database.IncidentStatusFailed); uerr != nil {
				slog.Warn("failed to mark incident failed", "incident_id", start.IncidentUUID, "err", uerr)
			}
		}
		return
	}
	if err := h.skillService.UpdateRunLog(runID, taskHeader+"Starting execution..."); err != nil {
		slog.Warn("failed to write run log", "err", err)
	}

	taskWithGuidance := executor.PrependGuidance(task)

	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker for API incident", "incident_id", incidentUUID)

		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
			slog.Info("using LLM provider", "provider", dbSettings.Provider, "model", dbSettings.Model)
		}

		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var superseded atomic.Bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				if err := h.skillService.UpdateRunLog(runID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update run log", "err", err)
				}
			},
			OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
				sessionID = sid
				response = output
				finalTokensUsed = tokensUsed
				finalExecutionTimeMs = executionTimeMs
				closeOnce.Do(func() { close(done) })
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("❌ Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
			// If a newer API call displaces us for the same incident_id,
			// the replacement run owns finalization. Unblock and exit
			// silently rather than overwrite its result with a failure.
			OnSuperseded: func() {
				superseded.Store(true)
				closeOnce.Do(func() { close(done) })
			},
		}

		_, err := h.agentWSHandler.StartIncident(incidentUUID, runID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback)
		if err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start run: %v", err)
			if updateErr := h.skillService.FinishRun(runID, services.RunOutcome{Failed: true, FullLog: taskHeader, Response: "❌ " + errorMsg}); updateErr != nil {
				slog.Error("failed to record run failure", "err", updateErr)
			}
			return
		}

		<-done

		// Replacement run owns DB finalization — exit silently.
		if superseded.Load() {
			slog.Info("API incident superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		// Apply the first matching formatting rule before persistence.
		// Passthrough on error/empty or when no rule matches the
		// incident's flow. Manual runs have no destination channel.
		formattedResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog,
			services.BuildFormatFlow(runID, ""))

		// Re-attach the metrics footer AFTER formatting so the LLM
		// never sees it (and therefore cannot strip or rewrite ⏱️
		// Time / 🎯 Tokens). The deterministic footer is derived
		// from finalTokensUsed/finalExecutionTimeMs and lands at
		// the end of `incident.response`, so the web UI's metrics
		// line stays correct even when the formatter rewrote the
		// body.
		formattedWithMetrics := appendFinalizeMetrics(formattedResponse, finalExecutionTimeMs, finalTokensUsed, hasError)
		rawWithMetrics := appendFinalizeMetrics(response, finalExecutionTimeMs, finalTokensUsed, hasError)

		// Claim ownership of finalization atomically. A second API
		// call for the same incident_id displaces this run; without
		// the ReleaseRun guard we'd race the replacement's DB write.
		if !h.agentWSHandler.ReleaseRun(incidentUUID, runID) {
			slog.Info("API incident displaced during finalization; leaving DB write to the new run", "incident_id", incidentUUID)
			return
		}

		// Build full log using the raw response (with metrics) so
		// full_log preserves the original agent output for debugging.
		fullLog := taskHeader + lastStreamedLog
		if rawWithMetrics != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + rawWithMetrics
		}

		if err := h.skillService.FinishRun(runID, services.RunOutcome{Failed: hasError, SessionID: sessionID, FullLog: fullLog, Response: formattedWithMetrics}); err != nil {
			slog.Error("failed to store run result", "err", err)
		}

		slog.Info("API incident completed via WebSocket", "incident_id", incidentUUID)
		return
	}

	slog.Error("agent worker not connected for API incident", "incident_id", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.FinishRun(runID, services.RunOutcome{Failed: true, FullLog: taskHeader, Response: "❌ " + errorMsg}); updateErr != nil {
		slog.Error("failed to record run failure", "err", updateErr)
	}
}

// handleAlertUnlink handles POST /api/alerts/{uuid}/unlink. It detaches an
// alert from its incident and spawns a fresh investigation for it.
// Returns 404 if the alert does not exist, 409 on a concurrent move.
func (h *APIHandler) handleAlertUnlink(w http.ResponseWriter, r *http.Request) {
	h.moveAlert(w, r, r.PathValue("uuid"), "")
}

// alertMoveRequest is the body for POST /api/alerts/{uuid}/move.
type alertMoveRequest struct {
	// TargetIncidentUUID is the incident to attach the alert to. Empty means
	// "spawn a fresh investigation" (equivalent to unlink).
	TargetIncidentUUID string `json:"target_incident_uuid"`
}

// handleAlertMove handles POST /api/alerts/{uuid}/move. It reassigns an alert to
// a different incident: an empty target spawns a fresh investigation (unlink),
// a non-empty target links the alert to that existing incident.
func (h *APIHandler) handleAlertMove(w http.ResponseWriter, r *http.Request) {
	var req alertMoveRequest
	if r.Body != nil {
		// A missing/empty body is valid and means "new incident".
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	h.moveAlert(w, r, r.PathValue("uuid"), strings.TrimSpace(req.TargetIncidentUUID))
}

// handleAlertResolve handles POST /api/alerts/{uuid}/resolve. It manually
// marks a firing alert resolved (e.g. an operator confirming the underlying
// issue cleared). Returns 404 if the alert does not exist, 409 if it is
// already resolved.
func (h *APIHandler) handleAlertResolve(w http.ResponseWriter, r *http.Request) {
	alertUUID := r.PathValue("uuid")
	if err := h.skillService.ResolveAlert(r.Context(), alertUUID); err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			api.RespondError(w, http.StatusNotFound, "Alert not found")
		case errors.Is(err, services.ErrAlertAlreadyResolved):
			api.RespondError(w, http.StatusConflict, "alert is already resolved")
		default:
			slog.Error("ResolveAlert failed", "alert", alertUUID, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to resolve alert")
		}
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// moveAlert is the shared implementation behind unlink and move. When target is
// empty it spawns and runs a fresh investigation for the alert; otherwise it
// links the alert to the existing target incident without spawning anything.
func (h *APIHandler) moveAlert(w http.ResponseWriter, r *http.Request, alertUUID, target string) {
	// Load the alert to build the task text and verify existence before the
	// move operation modifies the row.
	db := database.GetDB()
	var alert database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&alert).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			api.RespondError(w, http.StatusNotFound, "Alert not found")
		} else {
			slog.Error("moveAlert: db error loading alert", "alert", alertUUID, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to load alert")
		}
		return
	}

	resultIncidentUUID, err := h.skillService.MoveAlertToIncident(r.Context(), alertUUID, target)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidMoveTarget):
			api.RespondError(w, http.StatusBadRequest, "target incident does not exist or is the alert's current incident")
		case errors.Is(err, services.ErrAlertAlreadyMoved):
			api.RespondError(w, http.StatusConflict, "alert was moved by a concurrent request")
		default:
			slog.Error("MoveAlertToIncident failed", "alert", alertUUID, "target", target, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to move alert")
		}
		return
	}

	// Linking to an existing incident does not start a new investigation.
	if target == "" {
		task := alert.AlertName
		if alert.TargetHost != "" {
			task += " on " + alert.TargetHost
		}
		if task == "" {
			task = "Alert investigation"
		}
		// Include the original raw alert text when available so the agent has the
		// same rich context it would receive from the normal alert-processing path.
		if original := extractOriginalMessage(alert.RawPayload, originalAlertTextMaxBytes); original != "" {
			task += "\n\nOriginal alert text:\n" + original
		}
		taskHeader := fmt.Sprintf("🔗 Unlinked Alert Investigation:\n%s\n\n--- Execution Log ---\n\n", task)
		go h.runAgentInvestigation(services.RunStart{
			IncidentUUID: resultIncidentUUID,
			EventUUID:    alert.EventUUID,
			SourceKind:   database.IncidentSourceKindAlert,
			SourceUUID:   alert.SourceUUID,
			RootSkill:    "incident-manager",
		}, taskHeader, task)
	}

	api.RespondJSON(w, http.StatusOK, map[string]string{"incident_uuid": resultIncidentUUID})
}

// applyIncidentStatusFilter applies a comma-separated ?status= filter to an
// incidents query. Besides the real IncidentStatus values, it recognizes the
// pseudo-token "alert_active" — an alert-sourced incident only stays
// "completed" while at least one linked alert is still firing (see
// UpdateIncidentComplete / ResolveAlertTx), so it represents unresolved,
// still-open work rather than history. The plain "completed" token is
// narrowed to exclude that case so the two buckets partition cleanly (used by
// the Incidents page's Open/History toggle).
func applyIncidentStatusFilter(query *gorm.DB, statusParam string) *gorm.DB {
	statuses := splitCSV(statusParam)
	if len(statuses) == 0 {
		return query
	}
	conds := make([]string, 0, len(statuses))
	args := make([]interface{}, 0, len(statuses))
	for _, s := range statuses {
		switch s {
		case "alert_active":
			conds = append(conds, "(status = ? AND source_kind = ?)")
			args = append(args, string(database.IncidentStatusCompleted), database.IncidentSourceKindAlert)
		case "completed":
			conds = append(conds, "(status = ? AND source_kind != ?)")
			args = append(args, string(database.IncidentStatusCompleted), database.IncidentSourceKindAlert)
		default:
			conds = append(conds, "status = ?")
			args = append(args, s)
		}
	}
	return query.Where(strings.Join(conds, " OR "), args...)
}

// splitCSV splits a comma-separated string into a trimmed, non-empty slice.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// reportedResult is the outcome of a manual task that did not become a
// request: storedKind is set for change/info (stored only); otherwise it was
// an alert and the other fields hold the correlation outcome.
type reportedResult struct {
	storedKind   string
	incidentUUID string
	linked       bool
	err          error
}

// classifyManualTask stores an API task as an event and classifies it.
// handled is true when the task did not become a request: it reported a
// problem (correlation gate) or was a change/info event (stored only). With
// no event intake wired it returns ("", _, false).
func (h *APIHandler) classifyManualTask(ctx context.Context, task string) (eventUUID string, res reportedResult, handled bool) {
	if h.eventService == nil || h.alertExtractor == nil {
		return "", res, false
	}
	// Gate off: every API task is a request, with no extraction call.
	if !services.LoadEventClassificationConfig().Enabled {
		ev := h.eventService.Record(ctx, services.EventInput{
			SourceKind: database.EventSourceManual,
			SourceUUID: manualEventSource,
			SourceType: "api",
			Title:      firstLineTruncated(task, mentionEventTitleMax),
			Summary:    task,
		}, services.ClassificationOffVerdict(database.EventKindRequest))
		return ev.UUID, res, false
	}
	allowed := []string{database.EventKindRequest, database.EventKindChange, database.EventKindInfo}
	if h.reportedAlerts != nil {
		allowed = append(allowed, database.EventKindAlert)
	}
	normalized, verdict := h.alertExtractor.ExtractAndClassify(ctx, task, "", allowed, database.EventKindRequest)
	ev := h.eventService.Record(ctx, services.EventInput{
		SourceKind:  database.EventSourceManual,
		SourceUUID:  manualEventSource,
		SourceType:  "api",
		Title:       normalized.AlertName,
		Summary:     task,
		Description: normalized.Description,
		Host:        normalized.TargetHost,
		Service:     normalized.TargetService,
		Severity:    string(normalized.Severity),
		Labels:      normalized.TargetLabels,
	}, verdict)
	switch ev.Kind {
	case database.EventKindRequest:
		return ev.UUID, res, false
	case database.EventKindChange, database.EventKindInfo:
		res.storedKind = ev.Kind
		return ev.UUID, res, true
	}

	normalized.SourceFingerprint = "api:" + ev.UUID
	normalized.EventUUID = ev.UUID
	res.incidentUUID, res.linked, res.err = h.reportedAlerts.ProcessReportedAlert(ReportedAlert{
		SourceUUID:    manualEventSource,
		SourceLabel:   "api",
		SourceDisplay: "API-reported",
		SourceName:    "API",
		Alert:         *normalized,
	})
	if res.err != nil {
		slog.Error("reported alert from API task failed", "event_uuid", ev.UUID, "err", res.err)
	}
	return ev.UUID, res, true
}

// manualEventSource is the source key of events and alerts from the API.
const manualEventSource = "manual"

// eventKindIfRecorded returns kind when an event row was recorded.
func eventKindIfRecorded(eventUUID, kind string) string {
	if eventUUID == "" {
		return ""
	}
	return kind
}
