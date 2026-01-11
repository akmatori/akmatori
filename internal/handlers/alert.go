package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
)

// AlertHandler handles webhook requests from multiple alert sources
type AlertHandler struct {
	config          *config.Config
	slackClient     *slack.Client
	codexExecutor   *executor.Executor
	codexWSHandler  *CodexWSHandler
	skillService    *services.SkillService
	alertService    *services.AlertService
	channelResolver *slackutil.ChannelResolver
	alertsChannel   string

	// Registered adapters by source type
	adapters map[string]alerts.AlertAdapter
}

// NewAlertHandler creates a new alert handler
func NewAlertHandler(
	cfg *config.Config,
	slackClient *slack.Client,
	codexExecutor *executor.Executor,
	codexWSHandler *CodexWSHandler,
	skillService *services.SkillService,
	alertService *services.AlertService,
	channelResolver *slackutil.ChannelResolver,
	alertsChannel string,
) *AlertHandler {
	h := &AlertHandler{
		config:          cfg,
		slackClient:     slackClient,
		codexExecutor:   codexExecutor,
		codexWSHandler:  codexWSHandler,
		skillService:    skillService,
		alertService:    alertService,
		channelResolver: channelResolver,
		alertsChannel:   alertsChannel,
		adapters:        make(map[string]alerts.AlertAdapter),
	}

	return h
}

// RegisterAdapter registers an alert adapter for a source type
func (h *AlertHandler) RegisterAdapter(adapter alerts.AlertAdapter) {
	h.adapters[adapter.GetSourceType()] = adapter
	log.Printf("Registered alert adapter: %s", adapter.GetSourceType())
}

// HandleWebhook processes incoming webhook requests
// Route: /webhook/alert/{instance_uuid}
func (h *AlertHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract instance UUID from path
	path := strings.TrimPrefix(r.URL.Path, "/webhook/alert/")
	instanceUUID := strings.TrimSuffix(path, "/")

	if instanceUUID == "" {
		http.Error(w, "Missing instance UUID", http.StatusBadRequest)
		return
	}

	// Look up instance
	instance, err := h.alertService.GetInstanceByUUID(instanceUUID)
	if err != nil {
		log.Printf("Alert instance not found: %s - %v", instanceUUID, err)
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	if !instance.Enabled {
		log.Printf("Alert instance disabled: %s", instanceUUID)
		http.Error(w, "Instance disabled", http.StatusForbidden)
		return
	}

	// Get adapter for source type
	adapter, ok := h.adapters[instance.AlertSourceType.Name]
	if !ok {
		log.Printf("No adapter for source type: %s", instance.AlertSourceType.Name)
		http.Error(w, "Unsupported source type", http.StatusBadRequest)
		return
	}

	// Validate webhook secret
	if err := adapter.ValidateWebhookSecret(r, instance); err != nil {
		log.Printf("Webhook secret validation failed for %s: %v", instanceUUID, err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading webhook body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse payload into normalized alerts
	normalizedAlerts, err := adapter.ParsePayload(body, instance)
	if err != nil {
		log.Printf("Error parsing alert payload: %v", err)
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	log.Printf("Received %d alerts from %s (instance: %s)",
		len(normalizedAlerts), instance.AlertSourceType.Name, instance.Name)

	// Process each alert
	for _, normalizedAlert := range normalizedAlerts {
		go h.processAlert(instance, normalizedAlert)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Received %d alerts", len(normalizedAlerts))
}

func (h *AlertHandler) processAlert(instance *database.AlertSourceInstance, normalized alerts.NormalizedAlert) {
	// Skip resolved alerts - log only
	if normalized.Status == database.AlertStatusResolved {
		log.Printf("Skipping resolved alert (log only): %s", normalized.AlertName)
		return
	}

	log.Printf("Processing firing alert: %s (severity: %s)", normalized.AlertName, normalized.Severity)

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
	incidentUUID, workingDir, err := h.skillService.SpawnIncidentManager(incidentCtx)
	if err != nil {
		log.Printf("Error spawning incident manager: %v", err)
		return
	}

	log.Printf("Created incident for alert: UUID=%s, WorkingDir=%s", incidentUUID, workingDir)

	// Post to Slack if enabled
	var threadTS string
	if h.slackClient != nil && h.alertsChannel != "" {
		var err error
		threadTS, err = h.postAlertToSlack(normalized, instance)
		if err != nil {
			log.Printf("Warning: Failed to post alert to Slack: %v", err)
		}
	}

	// Update incident status
	if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
		log.Printf("Warning: Failed to update incident status: %v", err)
	}

	// Trigger investigation asynchronously
	go h.runInvestigation(incidentUUID, workingDir, normalized, instance, threadTS)
}

func (h *AlertHandler) postAlertToSlack(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) (string, error) {
	if h.slackClient == nil || h.alertsChannel == "" {
		return "", nil
	}

	// Resolve channel ID
	var channelID string
	if h.channelResolver != nil {
		var err error
		channelID, err = h.channelResolver.ResolveChannel(h.alertsChannel)
		if err != nil {
			return "", fmt.Errorf("failed to resolve channel: %w", err)
		}
	} else {
		channelID = h.alertsChannel
	}

	// Format alert message
	emoji := database.GetSeverityEmoji(alert.Severity)
	message := fmt.Sprintf(`%s *Alert: %s*

:label: *Source:* %s (%s)
:computer: *Host:* %s
:gear: *Service:* %s
:warning: *Severity:* %s
:memo: *Summary:* %s`,
		emoji,
		alert.AlertName,
		instance.AlertSourceType.DisplayName,
		instance.Name,
		alert.TargetHost,
		alert.TargetService,
		alert.Severity,
		alert.Summary,
	)

	if alert.RunbookURL != "" {
		message += fmt.Sprintf("\n:book: *Runbook:* %s", alert.RunbookURL)
	}

	// Post message
	_, ts, err := h.slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		return "", err
	}

	// Add reaction
	h.slackClient.AddReaction("rotating_light", slack.ItemRef{
		Channel:   channelID,
		Timestamp: ts,
	})

	return ts, nil
}

func (h *AlertHandler) runInvestigation(incidentUUID, workingDir string, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance, threadTS string) {
	log.Printf("Starting investigation for alert: %s (incident: %s)", alert.AlertName, incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Try WebSocket-based execution first (new architecture)
	if h.codexWSHandler != nil && h.codexWSHandler.IsWorkerConnected() {
		log.Printf("Using WebSocket-based Codex worker for incident %s", incidentUUID)

		// Fetch OpenAI settings from database
		var openaiSettings *OpenAISettings
		if dbSettings, err := database.GetOpenAISettings(); err == nil && dbSettings != nil {
			openaiSettings = &OpenAISettings{
				APIKey:          dbSettings.APIKey,
				Model:           dbSettings.Model,
				ReasoningEffort: dbSettings.ModelReasoningEffort,
				BaseURL:         dbSettings.BaseURL,
				ProxyURL:        dbSettings.ProxyURL,
				NoProxy:         dbSettings.NoProxy,
				// ChatGPT subscription auth fields
				AuthMethod:          string(dbSettings.AuthMethod),
				ChatGPTAccessToken:  dbSettings.ChatGPTAccessToken,
				ChatGPTRefreshToken: dbSettings.ChatGPTRefreshToken,
			}
			// Add expiry timestamp if set
			if dbSettings.ChatGPTExpiresAt != nil {
				openaiSettings.ChatGPTExpiresAt = dbSettings.ChatGPTExpiresAt.Format(time.RFC3339)
			}
			log.Printf("Using OpenAI model: %s, auth method: %s", dbSettings.Model, dbSettings.AuthMethod)
		} else {
			log.Printf("Warning: Could not fetch OpenAI settings: %v", err)
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var response string
		var sessionID string
		var hasError bool
		var lastStreamedLog string

		// Build task header for logging
		taskHeader := fmt.Sprintf("📋 Alert Investigation: %s\n🖥️ Host: %s\n⚠️ Severity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog = output
				// Update database with streamed log
				h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+output)
			},
			OnCompleted: func(sid, output string) {
				sessionID = sid
				response = output
				close(done)
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("❌ Error: %s", errorMsg)
				hasError = true
				close(done)
			},
		}

		if err := h.codexWSHandler.StartIncident(incidentUUID, taskWithGuidance, openaiSettings, callback); err != nil {
			log.Printf("Failed to start incident via WebSocket: %v, falling back to local execution", err)
			h.runInvestigationLocal(incidentUUID, workingDir, alert, instance, threadTS, taskWithGuidance)
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
		h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response)

		// Update Slack if enabled
		h.updateSlackWithResult(threadTS, response, hasError)

		log.Printf("Investigation completed for alert: %s (via WebSocket)", alert.AlertName)
		return
	}

	// Fall back to local execution (legacy)
	log.Printf("WebSocket worker not available, using local execution for incident %s", incidentUUID)
	h.runInvestigationLocal(incidentUUID, workingDir, alert, instance, threadTS, taskWithGuidance)
}

// runInvestigationLocal runs investigation using the local executor (legacy fallback)
func (h *AlertHandler) runInvestigationLocal(incidentUUID, workingDir string, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance, threadTS string, taskWithGuidance string) {
	ctx := context.Background()

	result := h.codexExecutor.ExecuteForSlackInDirectory(
		ctx,
		taskWithGuidance,
		"",
		workingDir,
		func(progress string) {
			log.Printf("Investigation progress for %s: %s", alert.AlertName, progress)
		},
	)

	// Update incident with results
	finalStatus := database.IncidentStatusCompleted
	if result.Error != nil {
		finalStatus = database.IncidentStatusFailed
	}

	alertHeader := fmt.Sprintf(`Alert Investigation Log

Alert: %s
Source: %s (%s)
Host: %s
Service: %s
Severity: %s
Summary: %s

--- Investigation ---

`, alert.AlertName, instance.AlertSourceType.DisplayName, instance.Name,
		alert.TargetHost, alert.TargetService, alert.Severity, alert.Summary)

	fullLogWithContext := alertHeader + result.FullLog

	if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, result.SessionID, fullLogWithContext, result.Response); err != nil {
		log.Printf("Warning: Failed to update incident: %v", err)
	}

	// Update Slack
	h.updateSlackWithResult(threadTS, result.Response, result.Error != nil)

	log.Printf("Investigation completed for alert: %s (status: %s, local)", alert.AlertName, finalStatus)
}

// updateSlackWithResult posts results to Slack thread
func (h *AlertHandler) updateSlackWithResult(threadTS, response string, hasError bool) {
	if h.slackClient == nil || threadTS == "" || h.alertsChannel == "" {
		return
	}

	channelID := h.alertsChannel
	if h.channelResolver != nil {
		resolved, _ := h.channelResolver.ResolveChannel(h.alertsChannel)
		if resolved != "" {
			channelID = resolved
		}
	}

	// Add result reaction
	reactionName := "white_check_mark"
	if hasError {
		reactionName = "x"
	}
	h.slackClient.AddReaction(reactionName, slack.ItemRef{
		Channel:   channelID,
		Timestamp: threadTS,
	})

	// Post result summary
	h.slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(response, false),
		slack.MsgOptionTS(threadTS),
	)
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

// isSlackEnabled checks if Slack integration is active
func (h *AlertHandler) isSlackEnabled() bool {
	return h.slackClient != nil && h.alertsChannel != ""
}
