package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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
		countQuery.Count(&total)

		if err := query.Offset(params.Offset()).Limit(params.PerPage).Find(&incidents).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to get incidents")
			return
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
			Source:   "api",
			SourceID: fmt.Sprintf("api-%d", time.Now().UnixNano()),
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

		go func() {
			taskHeader := fmt.Sprintf("📝 API Incident Task:\n%s\n\n--- Execution Log ---\n\n", req.Task)
			if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting execution..."); err != nil {
				slog.Error("failed to update incident status", "err", err)
			}

			taskWithGuidance := executor.PrependGuidance(req.Task)

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
		}()

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

	api.RespondJSON(w, http.StatusOK, incident)
}
