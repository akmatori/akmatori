package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	config             *config.Config
	slackManager       *slackutil.Manager
	codexExecutor      *executor.Executor
	codexWSHandler     *CodexWSHandler
	skillService       *services.SkillService
	alertService       *services.AlertService
	channelResolver    *slackutil.ChannelResolver
	aggregationService *services.AggregationService

	// Registered adapters by source type
	adapters map[string]alerts.AlertAdapter
}

// NewAlertHandler creates a new alert handler
func NewAlertHandler(
	cfg *config.Config,
	slackManager *slackutil.Manager,
	codexExecutor *executor.Executor,
	codexWSHandler *CodexWSHandler,
	skillService *services.SkillService,
	alertService *services.AlertService,
	channelResolver *slackutil.ChannelResolver,
	aggregationService *services.AggregationService,
) *AlertHandler {
	h := &AlertHandler{
		config:             cfg,
		slackManager:       slackManager,
		codexExecutor:      codexExecutor,
		codexWSHandler:     codexWSHandler,
		skillService:       skillService,
		alertService:       alertService,
		channelResolver:    channelResolver,
		aggregationService: aggregationService,
		adapters:           make(map[string]alerts.AlertAdapter),
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

// evaluateAlertAggregation uses Codex to decide if alert should attach to existing incident
func (h *AlertHandler) evaluateAlertAggregation(
	instance *database.AlertSourceInstance,
	normalized alerts.NormalizedAlert,
) (*services.CorrelatorOutput, error) {
	// Check if aggregation service is available
	if h.aggregationService == nil {
		return &services.CorrelatorOutput{Decision: "new", Reason: "Aggregation service not configured"}, nil
	}

	// Check if aggregation is enabled
	settings, err := h.aggregationService.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get aggregation settings: %w", err)
	}
	if !settings.Enabled {
		return &services.CorrelatorOutput{Decision: "new", Reason: "Aggregation disabled"}, nil
	}

	// Build alert context
	alertContext := services.AlertContext{
		AlertName:         normalized.AlertName,
		Severity:          string(normalized.Severity),
		TargetHost:        normalized.TargetHost,
		TargetService:     normalized.TargetService,
		Summary:           normalized.Summary,
		Description:       normalized.Description,
		SourceType:        instance.AlertSourceType.Name,
		SourceFingerprint: normalized.SourceFingerprint,
		TargetLabels:      normalized.TargetLabels,
		ReceivedAt:        time.Now(),
	}

	// Build correlator input
	input, err := h.aggregationService.BuildCorrelatorInput(alertContext)
	if err != nil {
		return nil, fmt.Errorf("failed to build correlator input: %w", err)
	}

	// If no open incidents, create new
	if len(input.OpenIncidents) == 0 {
		return &services.CorrelatorOutput{Decision: "new", Reason: "No open incidents"}, nil
	}

	// TODO: Call Codex correlator (will be implemented in a future task)
	// For now, return "new" as default
	return &services.CorrelatorOutput{Decision: "new", Reason: "Correlator not yet implemented"}, nil
}

func (h *AlertHandler) processAlert(instance *database.AlertSourceInstance, normalized alerts.NormalizedAlert) {
	if normalized.Status == database.AlertStatusResolved {
		log.Printf("Processing resolved alert: %s", normalized.AlertName)
		// TODO: Handle resolved alerts (update incident_alerts status)
		return
	}

	log.Printf("Processing firing alert: %s (severity: %s)", normalized.AlertName, normalized.Severity)

	// Evaluate aggregation
	aggregationResult, err := h.evaluateAlertAggregation(instance, normalized)
	if err != nil {
		log.Printf("Warning: Aggregation evaluation failed, creating new incident: %v", err)
		aggregationResult = &services.CorrelatorOutput{
			Decision: "new",
			Reason:   err.Error(),
		}
	}

	log.Printf("Aggregation decision: %s (confidence: %.2f, reason: %s)",
		aggregationResult.Decision, aggregationResult.Confidence, aggregationResult.Reason)

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
					h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusDiagnosed, "", "")
				}
				attached = true
				isNewIncident = false
				log.Printf("Attached alert to existing incident: %s", incidentUUID)
			} else {
				log.Printf("Failed to attach alert to incident %s: %v", incidentUUID, err)
			}
		} else {
			log.Printf("Failed to get incident %s, creating new: %v", incidentUUID, err)
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
			log.Printf("Error spawning incident manager: %v", err)
			return
		}

		log.Printf("Created incident for alert: UUID=%s, WorkingDir=%s", incidentUUID, workingDir)

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
					log.Printf("Warning: Failed to create IncidentAlert record: %v", err)
				}
			} else {
				log.Printf("Warning: Failed to get incident for IncidentAlert record: %v", err)
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
			log.Printf("Warning: Failed to post alert to Slack: %v", err)
		}
	}

	// Update incident status and run investigation (only for new incidents)
	if isNewIncident {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			log.Printf("Warning: Failed to update incident status: %v", err)
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

func (h *AlertHandler) postAlertToSlack(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) (string, error) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return "", nil
	}

	// Get alerts channel from database settings
	settings, err := database.GetSlackSettings()
	if err != nil || settings == nil || settings.AlertsChannel == "" {
		return "", nil
	}
	alertsChannel := settings.AlertsChannel

	// Resolve channel ID
	var channelID string
	if h.channelResolver != nil {
		var err error
		channelID, err = h.channelResolver.ResolveChannel(alertsChannel)
		if err != nil {
			return "", fmt.Errorf("failed to resolve channel: %w", err)
		}
	} else {
		channelID = alertsChannel
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
	_, ts, err := slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		return "", err
	}

	// Add reaction
	slackClient.AddReaction("rotating_light", slack.ItemRef{
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
	if threadTS == "" {
		return
	}

	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	// Get alerts channel from database settings
	settings, err := database.GetSlackSettings()
	if err != nil || settings == nil || settings.AlertsChannel == "" {
		return
	}
	alertsChannel := settings.AlertsChannel

	channelID := alertsChannel
	if h.channelResolver != nil {
		resolved, _ := h.channelResolver.ResolveChannel(alertsChannel)
		if resolved != "" {
			channelID = resolved
		}
	}

	// Add result reaction
	reactionName := "white_check_mark"
	if hasError {
		reactionName = "x"
	}
	slackClient.AddReaction(reactionName, slack.ItemRef{
		Channel:   channelID,
		Timestamp: threadTS,
	})

	// Post result summary
	slackClient.PostMessage(
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
	// Check database setting - user may have disabled Slack in UI
	settings, err := database.GetSlackSettings()
	if err != nil {
		return false
	}

	if !settings.IsActive() || settings.AlertsChannel == "" {
		return false
	}

	// Check that we have a valid client
	return h.slackManager.GetClient() != nil
}

// formatAggregationFooter generates a footer for Slack messages showing
// how many alerts are aggregated into an incident with a link to view it
func (h *AlertHandler) formatAggregationFooter(incidentUUID string, alertCount int) string {
	baseURL := os.Getenv("AKMATORI_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	return fmt.Sprintf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n:link: %d alert%s aggregated • <%s/incidents/%s|View incident>",
		alertCount,
		pluralize(alertCount),
		baseURL,
		incidentUUID,
	)
}

// pluralize returns "s" for counts other than 1
func pluralize(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
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
		log.Printf("Processing resolved alert from Slack channel: %s", normalized.AlertName)
		// For resolved alerts, we could potentially close related incidents
		// For now, just log it
		return
	}

	log.Printf("Processing Slack channel alert: %s (severity: %s)", normalized.AlertName, normalized.Severity)

	// Evaluate aggregation
	aggregationResult, err := h.evaluateAlertAggregation(instance, normalized)
	if err != nil {
		log.Printf("Warning: Aggregation evaluation failed, creating new incident: %v", err)
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
					h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusDiagnosed, "", "")
				}
				attached = true
				isNewIncident = false
				log.Printf("Attached Slack channel alert to existing incident: %s", incidentUUID)

				// Post a reply in the original Slack thread indicating attachment
				h.postSlackThreadReply(slackChannelID, slackMessageTS,
					fmt.Sprintf("🔗 Alert attached to existing incident. View at: %s/incidents/%s",
						h.getBaseURL(), incidentUUID))
			} else {
				log.Printf("Failed to attach alert to incident %s: %v", incidentUUID, err)
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
			log.Printf("Error spawning incident manager for Slack channel alert: %v", err)
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			h.postSlackThreadReply(slackChannelID, slackMessageTS,
				fmt.Sprintf("❌ Failed to create incident: %v", err))
			return
		}

		log.Printf("Created incident for Slack channel alert: UUID=%s", incidentUUID)

		// Update incident with Slack context for thread replies
		if err := h.updateIncidentSlackContext(incidentUUID, slackChannelID, slackMessageTS); err != nil {
			log.Printf("Warning: Failed to update incident Slack context: %v", err)
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
					log.Printf("Warning: Failed to create IncidentAlert record: %v", err)
				}
			}
		}

		isNewIncident = true
	}

	// Run investigation for new incidents
	if isNewIncident {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			log.Printf("Warning: Failed to update incident status: %v", err)
		}

		// Run investigation with thread reply support
		go h.runSlackChannelInvestigation(incidentUUID, workingDir, normalized, instance, slackChannelID, slackMessageTS)
	}
}

// runSlackChannelInvestigation runs investigation and posts results to the Slack thread
func (h *AlertHandler) runSlackChannelInvestigation(
	incidentUUID, workingDir string,
	alert alerts.NormalizedAlert,
	instance *database.AlertSourceInstance,
	slackChannelID, slackMessageTS string,
) {
	log.Printf("Starting investigation for Slack channel alert: %s (incident: %s)", alert.AlertName, incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Try WebSocket-based execution first
	if h.codexWSHandler != nil && h.codexWSHandler.IsWorkerConnected() {
		log.Printf("Using WebSocket-based Codex worker for Slack channel incident %s", incidentUUID)

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
				AuthMethod:      string(dbSettings.AuthMethod),
				ChatGPTAccessToken:  dbSettings.ChatGPTAccessToken,
				ChatGPTRefreshToken: dbSettings.ChatGPTRefreshToken,
			}
			if dbSettings.ChatGPTExpiresAt != nil {
				openaiSettings.ChatGPTExpiresAt = dbSettings.ChatGPTExpiresAt.Format(time.RFC3339)
			}
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var response string
		var sessionID string
		var hasError bool
		var lastStreamedLog string

		taskHeader := fmt.Sprintf("📋 Slack Channel Alert Investigation: %s\n🖥️ Host: %s\n⚠️ Severity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog = output
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
			h.runSlackChannelInvestigationLocal(incidentUUID, workingDir, alert, instance, slackChannelID, slackMessageTS, taskWithGuidance)
			return
		}

		// Wait for completion
		<-done

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
		h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response)

		// Update Slack thread
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, hasError)
		h.postSlackThreadReply(slackChannelID, slackMessageTS, response)

		log.Printf("Investigation completed for Slack channel alert: %s", alert.AlertName)
		return
	}

	// Fallback to local execution
	h.runSlackChannelInvestigationLocal(incidentUUID, workingDir, alert, instance, slackChannelID, slackMessageTS, taskWithGuidance)
}

// runSlackChannelInvestigationLocal runs investigation using local executor
func (h *AlertHandler) runSlackChannelInvestigationLocal(
	incidentUUID, workingDir string,
	alert alerts.NormalizedAlert,
	instance *database.AlertSourceInstance,
	slackChannelID, slackMessageTS string,
	taskWithGuidance string,
) {
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

	// Update incident
	finalStatus := database.IncidentStatusCompleted
	if result.Error != nil {
		finalStatus = database.IncidentStatusFailed
	}

	alertHeader := fmt.Sprintf(`Slack Channel Alert Investigation

Alert: %s
Source: %s (%s)
Host: %s
Service: %s
Severity: %s
Summary: %s

--- Investigation ---

`, alert.AlertName, instance.AlertSourceType.DisplayName, instance.Name,
		alert.TargetHost, alert.TargetService, alert.Severity, alert.Summary)

	fullLog := alertHeader + result.FullLog
	h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, result.SessionID, fullLog, result.Response)

	// Update Slack thread
	h.updateSlackChannelReactions(slackChannelID, slackMessageTS, result.Error != nil)
	h.postSlackThreadReply(slackChannelID, slackMessageTS, result.Response)

	log.Printf("Investigation completed for Slack channel alert: %s (local)", alert.AlertName)
}

// updateSlackChannelReactions updates reactions on the original Slack message
func (h *AlertHandler) updateSlackChannelReactions(channelID, messageTS string, hasError bool) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	// Remove hourglass reaction
	slackClient.RemoveReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   channelID,
		Timestamp: messageTS,
	})

	// Add result reaction
	reactionName := "white_check_mark"
	if hasError {
		reactionName = "x"
	}
	slackClient.AddReaction(reactionName, slack.ItemRef{
		Channel:   channelID,
		Timestamp: messageTS,
	})
}

// postSlackThreadReply posts a message as a thread reply
func (h *AlertHandler) postSlackThreadReply(channelID, threadTS, message string) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	_, _, err := slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Printf("Error posting thread reply: %v", err)
	}
}

// updateIncidentSlackContext updates the incident with Slack channel context
func (h *AlertHandler) updateIncidentSlackContext(incidentUUID, channelID, messageTS string) error {
	return database.GetDB().Model(&database.Incident{}).
		Where("uuid = ?", incidentUUID).
		Updates(map[string]interface{}{
			"slack_channel_id": channelID,
			"slack_message_ts": messageTS,
		}).Error
}

// getBaseURL returns the base URL for incident links
func (h *AlertHandler) getBaseURL() string {
	baseURL := os.Getenv("AKMATORI_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	return baseURL
}
