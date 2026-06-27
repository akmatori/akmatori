package handlers

import (
	"context"
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
			statuses := splitCSV(statusParam)
			if len(statuses) > 0 {
				query = query.Where("status IN ?", statuses)
			}
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
			statuses := splitCSV(statusParam)
			if len(statuses) > 0 {
				countQuery = countQuery.Where("status IN ?", statuses)
			}
		}
		countQuery.Count(&total)

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

// handleIncidentByID handles GET /api/incidents/:uuid
func (h *APIHandler) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	uuid := r.URL.Path[len("/api/incidents/"):]

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

// handleAlertUnlink handles POST /api/alerts/{uuid}/unlink. It detaches a
// correlated alert from its incident and spawns a fresh investigation for it.
// Returns 409 if the alert was not correlated, 404 if the alert does not exist.
func (h *APIHandler) handleAlertUnlink(w http.ResponseWriter, r *http.Request) {
	alertUUID := r.PathValue("uuid")

	// Load the alert to build the task text and verify existence before the
	// unlink operation modifies the row.
	db := database.GetDB()
	var alert database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&alert).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Alert not found")
		return
	}

	newIncidentUUID, err := h.skillService.UnlinkAlertFromIncident(r.Context(), alertUUID)
	if err != nil {
		if errors.Is(err, services.ErrAlertNotCorrelated) {
			api.RespondError(w, http.StatusConflict, "alert is not correlated")
			return
		}
		slog.Error("UnlinkAlertFromIncident failed", "alert", alertUUID, "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to unlink alert")
		return
	}

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
	go h.runAgentInvestigation(newIncidentUUID, taskHeader, task)

	api.RespondJSON(w, http.StatusOK, map[string]string{"incident_uuid": newIncidentUUID})
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
