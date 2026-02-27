package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
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

		// Check for pagination params
		pageParam := r.URL.Query().Get("page")
		perPageParam := r.URL.Query().Get("per_page")

		if pageParam != "" || perPageParam != "" {
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
		} else {
			if err := query.Limit(500).Find(&incidents).Error; err != nil {
				api.RespondError(w, http.StatusInternalServerError, "Failed to get incidents")
				return
			}
			api.RespondJSON(w, http.StatusOK, incidents)
		}

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

		log.Printf("Created incident via API: %s", incidentUUID)

		go func() {
			taskHeader := fmt.Sprintf("📝 API Incident Task:\n%s\n\n--- Execution Log ---\n\n", req.Task)
			if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", taskHeader+"Starting execution..."); err != nil {
				log.Printf("Failed to update incident status: %v", err)
			}

			taskWithGuidance := executor.PrependGuidance(req.Task)

			if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
				log.Printf("Using WebSocket-based agent worker for API incident %s", incidentUUID)

				var llmSettings *LLMSettingsForWorker
				if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
					llmSettings = BuildLLMSettingsForWorker(dbSettings)
					log.Printf("Using LLM provider: %s, model: %s", dbSettings.Provider, dbSettings.Model)
				}

				done := make(chan struct{})
				var closeOnce sync.Once
				var response string
				var sessionID string
				var hasError bool
				var lastStreamedLog string

				callback := IncidentCallback{
					OnOutput: func(output string) {
						lastStreamedLog += output
						if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
							log.Printf("Failed to update incident log: %v", err)
						}
					},
					OnCompleted: func(sid, output string) {
						sessionID = sid
						response = output
						closeOnce.Do(func() { close(done) })
					},
					OnError: func(errorMsg string) {
						response = fmt.Sprintf("❌ Error: %s", errorMsg)
						hasError = true
						closeOnce.Do(func() { close(done) })
					},
				}

				if err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), callback); err != nil {
					log.Printf("Failed to start incident via WebSocket: %v", err)
					errorMsg := fmt.Sprintf("Failed to start incident: %v", err)
					h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg)
					return
				}

				<-done

				fullLog := taskHeader + lastStreamedLog
				if response != "" {
					fullLog += "\n\n--- Final Response ---\n\n" + response
				}

				if hasError {
					if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, sessionID, fullLog, response); err != nil {
						log.Printf("Failed to update incident complete: %v", err)
					}
				} else {
					if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, sessionID, fullLog, response); err != nil {
						log.Printf("Failed to update incident complete: %v", err)
					}
				}

				log.Printf("API incident %s completed (via WebSocket)", incidentUUID)
				return
			}

			log.Printf("ERROR: Agent worker not connected for API incident %s", incidentUUID)
			errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
			h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", taskHeader, "❌ "+errorMsg)
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

// runIncidentLocal runs incident using the local executor (legacy fallback)
func (h *APIHandler) runIncidentLocal(incidentUUID, workingDir, taskHeader, taskWithGuidance string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	progressCallback := func(progressLog string) {
		if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+progressLog); err != nil {
			log.Printf("Failed to update incident log: %v", err)
		}
	}

	result, err := h.codexExecutor.ExecuteInDirectory(ctx, taskWithGuidance, "", workingDir, progressCallback)

	fullLogWithContext := taskHeader + result.FullLog

	if err != nil {
		log.Printf("Incident %s failed: %v", incidentUUID, err)
		if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, result.SessionID, fullLogWithContext+"\n\nError: "+err.Error(), "Error: "+err.Error()); updateErr != nil {
			log.Printf("Failed to update incident complete: %v", updateErr)
		}
		return
	}

	log.Printf("Incident %s completed. Output: %d bytes, Tokens: %d, Session: %s",
		incidentUUID, len(result.Output), result.TokensUsed, result.SessionID)
	if err := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, result.SessionID, fullLogWithContext, result.Output); err != nil {
		log.Printf("Failed to update incident complete: %v", err)
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

// ========== Incident Alerts Management ==========

// handleGetIncidentAlerts handles GET /api/incidents/{uuid}/alerts
func (h *APIHandler) handleGetIncidentAlerts(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var alerts []database.IncidentAlert
	if err := db.Where("incident_id = ?", incident.ID).Order("attached_at DESC").Find(&alerts).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get alerts")
		return
	}

	api.RespondJSON(w, http.StatusOK, alerts)
}

// handleAttachAlert handles POST /api/incidents/{uuid}/alerts
func (h *APIHandler) handleAttachAlert(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var alert database.IncidentAlert
	if err := api.DecodeJSON(r, &alert); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	alert.IncidentID = incident.ID
	alert.AttachedAt = time.Now()

	if err := db.Create(&alert).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to attach alert")
		return
	}

	if err := db.Model(&incident).Update("alert_count", gorm.Expr("alert_count + 1")).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	api.RespondJSON(w, http.StatusCreated, alert)
}

// handleDetachAlert handles DELETE /api/incidents/{uuid}/alerts/{alertId}
func (h *APIHandler) handleDetachAlert(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")
	alertIdStr := r.PathValue("alertId")

	alertId, err := strconv.ParseUint(alertIdStr, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid alert ID")
		return
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var alert database.IncidentAlert
	if err := db.Where("id = ? AND incident_id = ?", alertId, incident.ID).First(&alert).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Alert not found in this incident")
		return
	}

	if err := db.Delete(&alert).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to detach alert")
		return
	}

	if err := db.Model(&incident).Update("alert_count", gorm.Expr("GREATEST(alert_count - 1, 0)")).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	api.RespondNoContent(w)
}

// handleMergeIncident handles POST /api/incidents/{uuid}/merge
func (h *APIHandler) handleMergeIncident(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	uuid := r.PathValue("uuid")

	var targetIncident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&targetIncident).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Target incident not found")
		return
	}

	var req api.MergeIncidentRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.SourceIncidentUUID == "" {
		api.RespondError(w, http.StatusBadRequest, "source_incident_uuid is required")
		return
	}

	var sourceIncident database.Incident
	if err := db.Where("uuid = ?", req.SourceIncidentUUID).First(&sourceIncident).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Source incident not found")
		return
	}

	if err := db.Model(&database.IncidentAlert{}).Where("incident_id = ?", sourceIncident.ID).Update("incident_id", targetIncident.ID).Error; err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to merge incidents")
		return
	}

	var newAlertCount int64
	db.Model(&database.IncidentAlert{}).Where("incident_id = ?", targetIncident.ID).Count(&newAlertCount)
	if err := db.Model(&targetIncident).Update("alert_count", newAlertCount).Error; err != nil {
		log.Printf("Warning: Failed to update alert count for incident %s: %v", uuid, err)
	}

	if err := db.Model(&sourceIncident).Updates(map[string]interface{}{
		"status":      database.IncidentStatusCompleted,
		"alert_count": 0,
		"response":    fmt.Sprintf("Merged into incident %s", uuid),
	}).Error; err != nil {
		log.Printf("Warning: Failed to update source incident %s after merge: %v", req.SourceIncidentUUID, err)
	}

	db.First(&targetIncident, targetIncident.ID)

	api.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"message":         "Incidents merged successfully",
		"target_incident": targetIncident,
		"alerts_moved":    sourceIncident.AlertCount,
	})
}
