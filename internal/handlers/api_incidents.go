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

			// Batch 1: count + first/last seen per incident.
			type alertAggRow struct {
				IncidentUUID string
				Count        int64
				FirstSeen    *time.Time
				LastSeen     *time.Time
			}
			var aggRows []alertAggRow
			if err := db.Model(&database.Alert{}).
				Select("incident_uuid, COUNT(*) as count, MIN(fired_at) as first_seen, MAX(fired_at) as last_seen").
				Where("incident_uuid IN ?", uuids).
				Group("incident_uuid").
				Scan(&aggRows).Error; err != nil {
				slog.Warn("failed to fetch alert aggregates", "err", err)
			}
			aggMap := make(map[string]alertAggRow, len(aggRows))
			for _, row := range aggRows {
				aggMap[row.IncidentUUID] = row
			}

			// Batch 2: timestamps within the trend window for sparkline.
			windowEnd := time.Now()
			windowStart := windowEnd.Add(-trendWindow)
			type alertTsRow struct {
				IncidentUUID string
				FiredAt      time.Time
			}
			var tsRows []alertTsRow
			if err := db.Model(&database.Alert{}).
				Select("incident_uuid, fired_at").
				Where("incident_uuid IN ? AND fired_at >= ?", uuids, windowStart).
				Scan(&tsRows).Error; err != nil {
				slog.Warn("failed to fetch alert timestamps", "err", err)
			}
			tsMap := make(map[string][]time.Time, len(incidents))
			for _, row := range tsRows {
				tsMap[row.IncidentUUID] = append(tsMap[row.IncidentUUID], row.FiredAt)
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

		incidentContext := &services.IncidentContext{
			Source:     "api",
			SourceKind: database.IncidentSourceKindManual,
			SourceID:   fmt.Sprintf("api-%d", time.Now().UnixNano()),
			Context: database.JSONB{
				"task":       req.Task,
				"created_by": "api",
			},
			Message: req.Task,
		}

		if req.Context != nil {
			for k, v := range req.Context {
				incidentContext.Context[k] = v
			}
		}

		incidentUUID, workingDir, err := h.skillService.SpawnIncidentManager(incidentContext)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to create incident")
			return
		}

		slog.Info("created incident via API", "incident_id", incidentUUID)

		taskHeader := fmt.Sprintf("📝 API Incident Task:\n%s\n\n--- Execution Log ---\n\n", req.Task)
		go h.runAgentInvestigation(incidentUUID, taskHeader, req.Task)

		api.RespondJSON(w, http.StatusCreated, api.CreateIncidentResponse{
			UUID:       incidentUUID,
			Status:     "pending",
			WorkingDir: workingDir,
			Message:    "Incident created and processing started",
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

	api.RespondJSON(w, http.StatusOK, incident)
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

// runAgentInvestigation runs a full agent investigation for the given incident.
// It must be launched as a goroutine by the caller. taskHeader is prepended to
// all log updates; task is the raw user-facing task text (guidance is added
// internally via executor.PrependGuidance).
func (h *APIHandler) runAgentInvestigation(incidentUUID, taskHeader, task string) {
	if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting execution..."); err != nil {
		slog.Error("failed to update incident status", "err", err)
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
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
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

		runID, err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback)
		if err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start incident: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg, 0, 0); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			return
		}

		<-done

		// Replacement run owns DB finalization — exit silently.
		if superseded.Load() {
			slog.Info("API incident superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		// Apply the configured formatting prompt before persistence.
		// Passthrough on error/empty or when formatting is disabled.
		formattedResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog)

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

		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, formattedWithMetrics, finalTokensUsed, finalExecutionTimeMs); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		slog.Info("API incident completed via WebSocket", "incident_id", incidentUUID)
		return
	}

	slog.Error("agent worker not connected for API incident", "incident_id", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg, 0, 0); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
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
		go h.runAgentInvestigation(resultIncidentUUID, taskHeader, task)
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
