package handlers

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/output"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/utils"
)

func (h *AlertHandler) processAlert(instance *database.AlertSourceInstance, normalized alerts.NormalizedAlert) {
	if normalized.Status == database.AlertStatusResolved {
		slog.Info("processing resolved alert", "alert_name", normalized.AlertName)
		// TODO: Handle resolved alerts (update incident_alerts status)
		return
	}

	slog.Info("processing firing alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

	// Evaluate aggregation
	aggregationResult, err := h.evaluateAlertAggregation(instance, normalized)
	if err != nil {
		slog.Warn("aggregation evaluation failed, creating new incident", "err", err)
		aggregationResult = &services.CorrelatorOutput{
			Decision: "new",
			Reason:   err.Error(),
		}
	}

	slog.Info("aggregation decision", "decision", aggregationResult.Decision, "confidence", aggregationResult.Confidence, "reason", aggregationResult.Reason)

	var incidentUUID string
	var workingDir string
	var isNewIncident bool

	// Try to attach to existing incident if aggregation says so
	attached := false
	if aggregationResult.Decision == "attach" && aggregationResult.IncidentUUID != "" {
		incidentUUID = aggregationResult.IncidentUUID
		existing, err := h.skillService.GetIncident(incidentUUID)
		if err == nil {
			workingDir = existing.WorkingDir

			// Attach alert to incident
			incidentAlert := &database.IncidentAlert{
				SourceType:            instance.AlertSourceType.Name,
				SourceFingerprint:     normalized.SourceFingerprint,
				AlertName:             normalized.AlertName,
				Severity:              string(normalized.Severity),
				TargetHost:            normalized.TargetHost,
				TargetService:         normalized.TargetService,
				Summary:               normalized.Summary,
				Description:           normalized.Description,
				TargetLabels:          database.JSONB(convertLabels(normalized.TargetLabels)),
				Status:                string(normalized.Status),
				AlertPayload:          database.JSONB(normalized.RawPayload),
				CorrelationConfidence: aggregationResult.Confidence,
				CorrelationReason:     aggregationResult.Reason,
				AttachedAt:            time.Now(),
			}

			if err := h.aggregationService.AttachAlertToIncident(existing.ID, incidentAlert); err == nil {
				// If incident was in observing state, move back to diagnosed
				if existing.Status == database.IncidentStatusObserving {
					if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusDiagnosed, "", ""); err != nil {
						slog.Error("failed to update incident status", "err", err)
					}
				}
				attached = true
				isNewIncident = false
				slog.Info("attached alert to existing incident", "incident_id", incidentUUID)
			} else {
				slog.Error("failed to attach alert to incident", "incident_id", incidentUUID, "err", err)
			}
		} else {
			slog.Error("failed to get incident, creating new", "incident_id", incidentUUID, "err", err)
		}
	}

	// CREATE NEW incident if not attached
	if !attached {
		// Convert target labels to JSONB
		targetLabels := database.JSONB{}
		for k, v := range normalized.TargetLabels {
			targetLabels[k] = v
		}

		// Convert raw payload to JSONB
		rawPayload := database.JSONB{}
		for k, v := range normalized.RawPayload {
			rawPayload[k] = v
		}

		// Create incident context from alert data
		incidentCtx := &services.IncidentContext{
			Source:   instance.AlertSourceType.Name,
			SourceID: normalized.SourceFingerprint,
			Context: database.JSONB{
				"alert_name":         normalized.AlertName,
				"severity":           string(normalized.Severity),
				"status":             string(normalized.Status),
				"summary":            normalized.Summary,
				"description":        normalized.Description,
				"target_host":        normalized.TargetHost,
				"target_service":     normalized.TargetService,
				"target_labels":      targetLabels,
				"metric_name":        normalized.MetricName,
				"metric_value":       normalized.MetricValue,
				"threshold_value":    normalized.ThresholdValue,
				"runbook_url":        normalized.RunbookURL,
				"source_alert_id":    normalized.SourceAlertID,
				"source_fingerprint": normalized.SourceFingerprint,
				"source_type":        instance.AlertSourceType.Name,
				"source_instance":    instance.Name,
				"raw_payload":        rawPayload,
			},
			Message: fmt.Sprintf("%s - %s: %s", normalized.AlertName, normalized.TargetHost, normalized.Summary),
		}

		// Spawn incident manager
		var err error
		incidentUUID, workingDir, err = h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			slog.Error("failed to spawn incident manager", "err", err)
			return
		}

		slog.Info("created incident for alert", "incident_id", incidentUUID, "working_dir", workingDir)

		// Create IncidentAlert record for the initial alert
		if h.aggregationService != nil {
			incident, err := h.skillService.GetIncident(incidentUUID)
			if err == nil {
				incidentAlert := &database.IncidentAlert{
					SourceType:            instance.AlertSourceType.Name,
					SourceFingerprint:     normalized.SourceFingerprint,
					AlertName:             normalized.AlertName,
					Severity:              string(normalized.Severity),
					TargetHost:            normalized.TargetHost,
					TargetService:         normalized.TargetService,
					Summary:               normalized.Summary,
					Description:           normalized.Description,
					TargetLabels:          database.JSONB(convertLabels(normalized.TargetLabels)),
					Status:                string(normalized.Status),
					AlertPayload:          rawPayload,
					CorrelationConfidence: 1.0,
					CorrelationReason:     "Initial alert - created incident",
					AttachedAt:            time.Now(),
				}
				if err := h.aggregationService.AttachAlertToIncident(incident.ID, incidentAlert); err != nil {
					slog.Warn("failed to create IncidentAlert record", "err", err)
				}
			} else {
				slog.Warn("failed to get incident for IncidentAlert record", "err", err)
			}
		}

		isNewIncident = true
	}

	// Post to Slack (only for new incidents)
	var threadTS string
	if h.isSlackEnabled() && isNewIncident {
		var err error
		threadTS, err = h.postAlertToSlack(normalized, instance)
		if err != nil {
			slog.Warn("failed to post alert to Slack", "err", err)
		}
	}

	// Update incident status and run investigation (only for new incidents)
	if isNewIncident {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			slog.Warn("failed to update incident status", "err", err)
		}
		go h.runInvestigation(incidentUUID, workingDir, normalized, instance, threadTS)
	}
}

// convertLabels converts map[string]string to map[string]interface{}
func convertLabels(labels map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range labels {
		result[k] = v
	}
	return result
}

// ProcessAlertFromSlackChannel processes an alert that originated from a Slack channel
// This is similar to processAlert but handles thread replies to the source message
func (h *AlertHandler) ProcessAlertFromSlackChannel(
	instance *database.AlertSourceInstance,
	normalized alerts.NormalizedAlert,
	slackChannelID string,
	slackMessageTS string,
) {
	if normalized.Status == database.AlertStatusResolved {
		slog.Info("processing resolved alert from Slack channel", "alert_name", normalized.AlertName)
		// For resolved alerts, we could potentially close related incidents
		// For now, just log it
		return
	}

	slog.Info("processing Slack channel alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

	// Evaluate aggregation
	aggregationResult, err := h.evaluateAlertAggregation(instance, normalized)
	if err != nil {
		slog.Warn("aggregation evaluation failed, creating new incident", "err", err)
		aggregationResult = &services.CorrelatorOutput{
			Decision: "new",
			Reason:   err.Error(),
		}
	}

	var incidentUUID string
	var workingDir string
	var isNewIncident bool

	// Try to attach to existing incident if aggregation says so
	attached := false
	if aggregationResult.Decision == "attach" && aggregationResult.IncidentUUID != "" {
		incidentUUID = aggregationResult.IncidentUUID
		existing, err := h.skillService.GetIncident(incidentUUID)
		if err == nil {
			workingDir = existing.WorkingDir

			// Attach alert to incident
			incidentAlert := &database.IncidentAlert{
				SourceType:            instance.AlertSourceType.Name,
				SourceFingerprint:     normalized.SourceFingerprint,
				AlertName:             normalized.AlertName,
				Severity:              string(normalized.Severity),
				TargetHost:            normalized.TargetHost,
				TargetService:         normalized.TargetService,
				Summary:               normalized.Summary,
				Description:           normalized.Description,
				TargetLabels:          database.JSONB(convertLabels(normalized.TargetLabels)),
				Status:                string(normalized.Status),
				AlertPayload:          database.JSONB(normalized.RawPayload),
				CorrelationConfidence: aggregationResult.Confidence,
				CorrelationReason:     aggregationResult.Reason,
				AttachedAt:            time.Now(),
			}

			if err := h.aggregationService.AttachAlertToIncident(existing.ID, incidentAlert); err == nil {
				if existing.Status == database.IncidentStatusObserving {
					if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusDiagnosed, "", ""); err != nil {
						slog.Error("failed to update incident status", "err", err)
					}
				}
				attached = true
				isNewIncident = false
				slog.Info("attached Slack channel alert to existing incident", "incident_id", incidentUUID)

				// Post a reply in the original Slack thread indicating attachment
				h.postSlackThreadReply(slackChannelID, slackMessageTS,
					fmt.Sprintf("🔗 Alert attached to existing incident. View at: %s/incidents/%s",
						h.getBaseURL(), incidentUUID))
			} else {
				slog.Error("failed to attach alert to incident", "incident_id", incidentUUID, "err", err)
			}
		}
	}

	// CREATE NEW incident if not attached
	if !attached {
		// Convert target labels to JSONB
		targetLabels := database.JSONB{}
		for k, v := range normalized.TargetLabels {
			targetLabels[k] = v
		}

		// Convert raw payload to JSONB
		rawPayload := database.JSONB{}
		for k, v := range normalized.RawPayload {
			rawPayload[k] = v
		}

		// Create incident context from alert data
		incidentCtx := &services.IncidentContext{
			Source:   instance.AlertSourceType.Name,
			SourceID: normalized.SourceFingerprint,
			Context: database.JSONB{
				"alert_name":         normalized.AlertName,
				"severity":           string(normalized.Severity),
				"status":             string(normalized.Status),
				"summary":            normalized.Summary,
				"description":        normalized.Description,
				"target_host":        normalized.TargetHost,
				"target_service":     normalized.TargetService,
				"target_labels":      targetLabels,
				"metric_name":        normalized.MetricName,
				"metric_value":       normalized.MetricValue,
				"threshold_value":    normalized.ThresholdValue,
				"runbook_url":        normalized.RunbookURL,
				"source_alert_id":    normalized.SourceAlertID,
				"source_fingerprint": normalized.SourceFingerprint,
				"source_type":        instance.AlertSourceType.Name,
				"source_instance":    instance.Name,
				"raw_payload":        rawPayload,
				"slack_channel_id":   slackChannelID,
				"slack_message_ts":   slackMessageTS,
			},
			Message: fmt.Sprintf("%s - %s: %s", normalized.AlertName, normalized.TargetHost, normalized.Summary),
		}

		// Spawn incident manager
		var err error
		incidentUUID, workingDir, err = h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			slog.Error("failed to spawn incident manager for Slack channel alert", "err", err)
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			h.postSlackThreadReply(slackChannelID, slackMessageTS,
				fmt.Sprintf("❌ Failed to create incident: %v", err))
			return
		}

		slog.Info("created incident for Slack channel alert", "incident_id", incidentUUID)

		// Update incident with Slack context for thread replies
		if err := h.updateIncidentSlackContext(incidentUUID, slackChannelID, slackMessageTS); err != nil {
			slog.Warn("failed to update incident Slack context", "err", err)
		}

		// Create IncidentAlert record
		if h.aggregationService != nil {
			incident, err := h.skillService.GetIncident(incidentUUID)
			if err == nil {
				incidentAlert := &database.IncidentAlert{
					SourceType:            instance.AlertSourceType.Name,
					SourceFingerprint:     normalized.SourceFingerprint,
					AlertName:             normalized.AlertName,
					Severity:              string(normalized.Severity),
					TargetHost:            normalized.TargetHost,
					TargetService:         normalized.TargetService,
					Summary:               normalized.Summary,
					Description:           normalized.Description,
					TargetLabels:          database.JSONB(convertLabels(normalized.TargetLabels)),
					Status:                string(normalized.Status),
					AlertPayload:          rawPayload,
					CorrelationConfidence: 1.0,
					CorrelationReason:     "Initial alert from Slack channel",
					AttachedAt:            time.Now(),
				}
				if err := h.aggregationService.AttachAlertToIncident(incident.ID, incidentAlert); err != nil {
					slog.Warn("failed to create IncidentAlert record", "err", err)
				}
			}
		}

		isNewIncident = true
	}

	// Run investigation for new incidents
	if isNewIncident {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			slog.Warn("failed to update incident status", "err", err)
		}

		// Run investigation with thread reply support
		go h.runSlackChannelInvestigation(incidentUUID, workingDir, normalized, instance, slackChannelID, slackMessageTS)
	}
}

func (h *AlertHandler) buildInvestigationPrompt(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) string {
	prompt := fmt.Sprintf(`Investigate this %s alert:

Alert: %s
Host: %s
Service: %s
Severity: %s
Summary: %s
Description: %s`,
		instance.AlertSourceType.DisplayName,
		alert.AlertName,
		alert.TargetHost,
		alert.TargetService,
		alert.Severity,
		alert.Summary,
		alert.Description,
	)

	if alert.MetricName != "" {
		prompt += fmt.Sprintf("\nMetric: %s = %s", alert.MetricName, alert.MetricValue)
	}

	if alert.RunbookURL != "" {
		prompt += fmt.Sprintf("\nRunbook: %s", alert.RunbookURL)
	}

	prompt += `

Please:
1. Check if this is a known issue or pattern
2. Analyze available metrics and logs
3. Identify potential root causes
4. Suggest remediation steps with priority
5. Assess urgency and impact

Be specific and actionable. Reference any relevant data sources or scripts you use.`

	return prompt
}

func (h *AlertHandler) runInvestigation(incidentUUID, workingDir string, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance, threadTS string) {
	slog.Info("starting investigation for alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Try WebSocket-based execution first (new architecture)
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
			slog.Info("using LLM provider", "provider", dbSettings.Provider, "model", dbSettings.Model)
		} else {
			slog.Warn("could not fetch LLM settings", "err", err)
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var lastStreamedLog string

		// Build task header for logging
		taskHeader := fmt.Sprintf("📋 Alert Investigation: %s\n🖥️ Host: %s\n⚠️ Severity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				// Update database with streamed log
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
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
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackWithResult(threadTS, "❌ "+errorMsg, true)
			return
		}

		// Wait for completion
		<-done

		// Build full log: task header + streamed log + final response
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Update incident with full results
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		// Format response for Slack (parse structured blocks and apply formatting)
		var formattedResp string
		if hasError {
			formattedResp = response
		} else if response != "" {
			// Extract metrics before formatting so they don't get lost in truncation
			contentOnly, footer := buildSlackFooter(response, incidentUUID)
			parsed := output.Parse(contentOnly)
			formatted := output.FormatForSlack(parsed)
			formattedResp = truncateWithFooter(formatted, footer, 3900)
		} else {
			formattedResp = "✅ Task completed (no output)"
		}

		// Update Slack if enabled - include reasoning context
		slackResponse := buildSlackResponse(lastStreamedLog, formattedResp)
		h.updateSlackWithResult(threadTS, slackResponse, hasError)

		slog.Info("investigation completed for alert via WebSocket", "alert_name", alert.AlertName)
		return
	}

	// No WebSocket worker available
	slog.Error("agent worker not connected", "incident_id", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", "❌ "+errorMsg); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackWithResult(threadTS, "❌ "+errorMsg, true)
}

// runSlackChannelInvestigation runs investigation and posts results to the Slack thread
func (h *AlertHandler) runSlackChannelInvestigation(
	incidentUUID, workingDir string,
	alert alerts.NormalizedAlert,
	instance *database.AlertSourceInstance,
	slackChannelID, slackMessageTS string,
) {
	slog.Info("starting investigation for Slack channel alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Try WebSocket-based execution first
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker for Slack channel incident", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
		}

		// Post initial progress message in the Slack thread
		progressMsgTS := h.postSlackThreadReplyGetTS(slackChannelID, slackMessageTS,
			"🔍 *Investigating...*\n```\nWaiting for output...\n```")
		lastSlackUpdate := time.Now()

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var lastStreamedLog string

		taskHeader := fmt.Sprintf("📋 Slack Channel Alert Investigation: %s\n🖥️ Host: %s\n⚠️ Severity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}

				// Throttled update of the Slack progress message
				if progressMsgTS != "" && time.Since(lastSlackUpdate) >= slackProgressInterval {
					lastSlackUpdate = time.Now()
					progressLines := utils.GetLastNLines(strings.TrimSpace(lastStreamedLog), 15)
					// Leave room for the wrapper text (~40 bytes for "🔍 *Investigating...*\n```\n...\n```")
					progressLines = truncateLogForSlack(progressLines, 3900)
					h.updateSlackThreadMessage(slackChannelID, progressMsgTS,
						fmt.Sprintf("🔍 *Investigating...*\n```\n%s\n```", progressLines))
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
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", "❌ "+errorMsg); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			h.postSlackThreadReply(slackChannelID, slackMessageTS, "❌ "+errorMsg)
			return
		}

		// Wait for completion
		<-done

		slog.Info("investigation done", "incident_id", incidentUUID, "has_error", hasError, "response_len", len(response), "session_id", sessionID)

		// Build full log
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Update incident
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		// Format response for Slack (parse structured blocks and apply formatting)
		var formattedResponse string
		if hasError {
			formattedResponse = response
		} else if response != "" {
			// Extract metrics before formatting so they don't get lost in truncation
			contentOnly, footer := buildSlackFooter(response, incidentUUID)
			parsed := output.Parse(contentOnly)
			formatted := output.FormatForSlack(parsed)
			formattedResponse = truncateWithFooter(formatted, footer, 3900)
		} else {
			formattedResponse = "✅ Task completed (no output)"
		}

		// Update Slack thread - replace progress message with final result
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, hasError)
		if progressMsgTS != "" {
			slog.Info("replacing Slack progress message with final response", "ts", progressMsgTS, "response_len", len(formattedResponse), "incident", incidentUUID)
			h.updateSlackThreadMessage(slackChannelID, progressMsgTS, formattedResponse)
		} else {
			// No live progress was shown, include reasoning context
			slackResponse := buildSlackResponse(lastStreamedLog, formattedResponse)
			h.postSlackThreadReply(slackChannelID, slackMessageTS, slackResponse)
		}

		slog.Info("investigation completed for Slack channel alert", "alert", alert.AlertName)
		return
	}

	// No WebSocket worker available
	slog.Error("agent worker not connected for Slack channel incident", "incident", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", "❌ "+errorMsg); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
	h.postSlackThreadReply(slackChannelID, slackMessageTS, "❌ "+errorMsg)
}
