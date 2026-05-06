package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
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

	// Show "is investigating..." in the alert thread for the duration of the
	// agent run when Slack is configured. The reaction lands on the bot's own
	// alert message (threadTS). Skipping when threadTS=="" because Slack
	// posting was disabled or failed earlier.
	if threadTS != "" {
		if slackClient := h.slackManager.GetClient(); slackClient != nil {
			if settings, err := database.GetSlackSettings(); err == nil && settings != nil && settings.AlertsChannel != "" {
				typing := slackutil.NewTypingController(slackutil.TypingControllerConfig{
					Client:      slackClient,
					ChannelID:   settings.AlertsChannel,
					ThreadTS:    threadTS,
					ReactionRef: slack.ItemRef{Channel: settings.AlertsChannel, Timestamp: threadTS},
				})
				typing.Start(context.Background())
				defer typing.Stop()
			}
		}
	}

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
		var superseded bool
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
			// If a newer run displaces us for the same incident_id, the
			// replacement run owns finalization. Unblock and exit silently
			// instead of writing a failure that races the replacement.
			OnSuperseded: func() {
				superseded = true
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

		// Replacement run owns finalization — exit before touching the DB or Slack.
		if superseded {
			slog.Info("alert investigation superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		// Apply the configured formatting prompt before persistence and
		// Slack posting. Passthrough on error/empty or when formatting
		// is disabled.
		formattedResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog)

		// Build full log using the raw response so full_log preserves the
		// original agent output for debugging.
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Update incident with full results — the formatted response is
		// what users see in the UI.
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, formattedResponse, finalTokensUsed, finalExecutionTimeMs); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		// Format response for Slack: parse structured blocks, summarize when
		// over budget, append the footer. Single message; no reasoning prefix.
		var formattedResp string
		if hasError {
			formattedResp = response
		} else if formattedResponse != "" {
			formattedResp = finalizeSlackMessageBody(context.Background(), h.slackSummarizer, formattedResponse, incidentUUID)
		} else {
			formattedResp = "Task completed (no output)"
		}

		h.updateSlackWithResult(threadTS, formattedResp, hasError)

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

	// Show "is investigating..." in the thread header and put a hourglass
	// reaction on the original Slack-channel alert message for the duration
	// of the agent run. defer Stop covers all exit paths since the function
	// blocks on <-done before returning.
	//
	// The progress streamer pipes the agent's latest 🤔 reasoning line into
	// the typing controller's loading_messages, replacing Slack's default
	// rotating phrases ("searching...", "evaluating...", etc.) with what
	// the agent is actually thinking. There is no "Thinking..." placeholder
	// message — the typing banner + reaction are the activity signal; the
	// final result is posted as a fresh thread reply when the agent finishes.
	var progressStreamer *SlackProgressStreamer
	if slackClient := h.slackManager.GetClient(); slackClient != nil {
		typing := slackutil.NewTypingController(slackutil.TypingControllerConfig{
			Client:      slackClient,
			ChannelID:   slackChannelID,
			ThreadTS:    slackMessageTS,
			ReactionRef: slack.ItemRef{Channel: slackChannelID, Timestamp: slackMessageTS},
		})
		typing.Start(context.Background())
		defer typing.Stop()
		progressStreamer = NewSlackProgressStreamer(typing.UpdateLoadingMessage, slackAppendInterval)
	}

	// Use WebSocket-based agent worker
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker for Slack channel incident", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var superseded bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		taskHeader := fmt.Sprintf("Slack Channel Alert Investigation: %s\nHost: %s\nSeverity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(outputLog string) {
				lastStreamedLog += outputLog
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}

				// Stream condensed progress to Slack (delta only, not full log).
				progressStreamer.AppendStatus(outputLog)
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
			// If a newer run displaces us for the same incident_id, the
			// replacement run owns finalization — DB update, Slack message,
			// channel reaction. Skip everything below to avoid racing it.
			OnSuperseded: func() {
				superseded = true
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
			h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
			return
		}

		// Wait for completion
		<-done

		// Flush any buffered progress lines so the last status is not lost.
		progressStreamer.Flush()

		// Replacement run owns finalization — exit silently before touching
		// the DB or channel reactions; the replacement posts its own final
		// thread reply when it finishes.
		if superseded {
			slog.Info("slack channel investigation superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		slog.Info("investigation done", "incident_id", incidentUUID, "has_error", hasError, "response_len", len(response), "session_id", sessionID)

		// Apply the configured formatting prompt before persistence and
		// Slack posting. Passthrough on error/empty or when formatting
		// is disabled.
		dbResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog)

		// Build full log using the raw response so full_log preserves the
		// original agent output for debugging.
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Update incident
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, dbResponse, finalTokensUsed, finalExecutionTimeMs); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		// Format response for Slack: parse structured blocks, summarize when
		// over budget, append the footer. Single message; no reasoning prefix.
		var formattedResponse string
		if hasError {
			formattedResponse = response
		} else if dbResponse != "" {
			formattedResponse = finalizeSlackMessageBody(context.Background(), h.slackSummarizer, dbResponse, incidentUUID)
		} else {
			formattedResponse = "Task completed (no output)"
		}

		// Post the full final body as a fresh thread reply. chat.postMessage
		// allows up to ~40,000 chars so long summaries always reach the user.
		// Completion is signaled by the success/error reaction added above
		// and the typing banner clearing as the deferred Stop fires.
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, hasError)
		slog.Info("posting Slack final summary as new thread reply", "response_len", len(formattedResponse), "incident", incidentUUID)
		h.postSlackThreadReply(slackChannelID, slackMessageTS, formattedResponse)

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
	h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
}
