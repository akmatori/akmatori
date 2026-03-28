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
		return
	}

	slog.Info("processing firing alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

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
	incidentUUID, _, err := h.skillService.SpawnIncidentManager(incidentCtx)
	if err != nil {
		slog.Error("failed to spawn incident manager", "err", err)
		return
	}

	slog.Info("created incident for alert", "incident_id", incidentUUID)

	// Post to Slack
	var threadTS string
	if h.isSlackEnabled() {
		var err error
		threadTS, err = h.postAlertToSlack(normalized, instance)
		if err != nil {
			slog.Warn("failed to post alert to Slack", "err", err)
		}
	}

	// Update incident status and run investigation
	if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
		slog.Warn("failed to update incident status", "err", err)
	}
	go h.runInvestigation(incidentUUID, normalized, instance, threadTS)
}

// ProcessAlertFromSlackChannel processes an alert that originated from a Slack channel
func (h *AlertHandler) ProcessAlertFromSlackChannel(
	instance *database.AlertSourceInstance,
	normalized alerts.NormalizedAlert,
	slackChannelID string,
	slackMessageTS string,
) {
	if normalized.Status == database.AlertStatusResolved {
		slog.Info("processing resolved alert from Slack channel", "alert_name", normalized.AlertName)
		return
	}

	slog.Info("processing Slack channel alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

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
	incidentUUID, _, err := h.skillService.SpawnIncidentManager(incidentCtx)
	if err != nil {
		slog.Error("failed to spawn incident manager for Slack channel alert", "err", err)
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
		h.postSlackThreadReply(slackChannelID, slackMessageTS,
			fmt.Sprintf("Failed to create incident: %v", err))
		return
	}

	slog.Info("created incident for Slack channel alert", "incident_id", incidentUUID)

	// Update incident with Slack context for thread replies
	if err := h.updateIncidentSlackContext(incidentUUID, slackChannelID, slackMessageTS); err != nil {
		slog.Warn("failed to update incident Slack context", "err", err)
	}

	// Update incident status and run investigation
	if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
		slog.Warn("failed to update incident status", "err", err)
	}

	go h.runSlackChannelInvestigation(incidentUUID, normalized, instance, slackChannelID, slackMessageTS)
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

func (h *AlertHandler) runInvestigation(incidentUUID string, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance, threadTS string) {
	slog.Info("starting investigation for alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Use WebSocket-based agent worker
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
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		// Build task header for logging
		taskHeader := fmt.Sprintf("Alert Investigation: %s\nHost: %s\nSeverity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				// Update database with streamed log
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
				response = fmt.Sprintf("Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
		}

		if err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback); err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackWithResult(threadTS, errorMsg, true)
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
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response, finalTokensUsed, finalExecutionTimeMs); err != nil {
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
			formattedResp = truncateWithFooter(formatted, footer, slackMaxTextBytes)
		} else {
			formattedResp = "Task completed (no output)"
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
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackWithResult(threadTS, errorMsg, true)
}

// runSlackChannelInvestigation runs investigation and posts results to the Slack thread
func (h *AlertHandler) runSlackChannelInvestigation(
	incidentUUID string,
	alert alerts.NormalizedAlert,
	instance *database.AlertSourceInstance,
	slackChannelID, slackMessageTS string,
) {
	slog.Info("starting investigation for Slack channel alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Extract Slack user ID for the Streaming API (may be empty for bot-originated alerts)
	var slackUserID string
	if uid, ok := alert.RawPayload["slack_user"].(string); ok {
		slackUserID = uid
	}

	// Start a streaming message in the Slack thread — bot name blinks while open.
	// Falls back to a regular thread reply if streaming is unavailable.
	progressMsgTS, isStreaming := h.startSlackThreadStream(slackChannelID, slackMessageTS, "Thinking...", slackUserID)

	// Use WebSocket-based agent worker
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker for Slack channel incident", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
		}
		lastSlackUpdate := time.Now()

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		taskHeader := fmt.Sprintf("Slack Channel Alert Investigation: %s\nHost: %s\nSeverity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}

				// Throttled update of the Slack progress message.
				// Skip when streaming is active (chat.update conflicts with streaming state).
				if !isStreaming && progressMsgTS != "" && time.Since(lastSlackUpdate) >= slackProgressInterval {
					lastSlackUpdate = time.Now()
					progressLines := utils.GetLastNLines(strings.TrimSpace(lastStreamedLog), 15)
					progressLines = truncateLogForSlack(progressLines, slackMaxTextBytes-50)
					h.updateSlackThreadMessage(slackChannelID, progressMsgTS,
						fmt.Sprintf("*Investigating...*\n```\n%s\n```", progressLines))
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
				response = fmt.Sprintf("Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
		}

		if err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback); err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			if progressMsgTS != "" {
				h.stopSlackThreadStream(slackChannelID, progressMsgTS, isStreaming)
				h.updateSlackThreadMessage(slackChannelID, progressMsgTS, errorMsg)
			} else {
				h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
			}
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
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response, finalTokensUsed, finalExecutionTimeMs); err != nil {
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
			formattedResponse = truncateWithFooter(formatted, footer, slackMaxTextBytes)
		} else {
			formattedResponse = "Task completed (no output)"
		}

		// Stop the streaming indicator, then replace with final result
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, hasError)
		if progressMsgTS != "" {
			h.stopSlackThreadStream(slackChannelID, progressMsgTS, isStreaming)
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
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
	if progressMsgTS != "" {
		h.stopSlackThreadStream(slackChannelID, progressMsgTS, isStreaming)
		h.updateSlackThreadMessage(slackChannelID, progressMsgTS, errorMsg)
	} else {
		h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
	}
}
