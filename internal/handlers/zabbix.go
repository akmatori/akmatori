package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/models"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
)

// ZabbixHandler handles Zabbix webhook requests
type ZabbixHandler struct {
	config          *config.Config
	slackClient     *slack.Client // Can be nil if Slack is disabled
	codexExecutor   *executor.Executor
	skillService    *services.SkillService
	channelResolver *slackutil.ChannelResolver // Can be nil if Slack is disabled
	alertsChannel   string                     // Channel ID or name for alerts
}

// NewZabbixHandler creates a new Zabbix handler
func NewZabbixHandler(
	cfg *config.Config,
	slackClient *slack.Client,
	codexExecutor *executor.Executor,
	skillService *services.SkillService,
	channelResolver *slackutil.ChannelResolver,
	alertsChannel string,
) *ZabbixHandler {
	return &ZabbixHandler{
		config:          cfg,
		slackClient:     slackClient,
		codexExecutor:   codexExecutor,
		skillService:    skillService,
		channelResolver: channelResolver,
		alertsChannel:   alertsChannel,
	}
}

// isSlackEnabled returns true if Slack client is configured
func (h *ZabbixHandler) isSlackEnabled() bool {
	return h.slackClient != nil && h.channelResolver != nil
}

// HandleWebhook processes incoming Zabbix webhook requests
func (h *ZabbixHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify webhook secret from database settings
	zabbixSettings, err := database.GetZabbixSettings()
	if err != nil {
		log.Printf("Warning: Could not load Zabbix settings: %v", err)
		// Allow requests if settings can't be loaded (first-time setup)
	} else if zabbixSettings.WebhookSecret != "" {
		secret := r.Header.Get("X-Zabbix-Secret")
		if secret != zabbixSettings.WebhookSecret {
			log.Printf("Invalid Zabbix webhook secret")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading webhook body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse Zabbix alert
	var alert models.ZabbixAlert
	if err := json.Unmarshal(body, &alert); err != nil {
		log.Printf("Error parsing Zabbix alert: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	log.Printf("Received Zabbix alert: event_id=%s, severity=%s, hardware=%s",
		alert.EventID, alert.Severity, alert.Hardware)

	// Post alert to Slack if enabled
	var threadTS string
	if h.isSlackEnabled() {
		threadTS, err = h.postAlertToSlack(&alert)
		if err != nil {
			log.Printf("Warning: Error posting alert to Slack: %v", err)
			// Continue processing even if Slack fails
		}
	} else {
		log.Printf("Slack is disabled, skipping Slack notification")
	}

	// Trigger automatic investigation
	go h.triggerInvestigation(&alert, threadTS)

	// Return success
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Alert received and processed")
}

// postAlertToSlack posts the alert to the configured Slack channel
func (h *ZabbixHandler) postAlertToSlack(alert *models.ZabbixAlert) (string, error) {
	// Get channel name or ID from settings
	channelNameOrID := h.alertsChannel
	if channelNameOrID == "" {
		log.Printf("Warning: Alerts channel not configured, alert will not be posted to Slack")
		return "", nil
	}

	// Resolve channel name to ID if needed
	channelID, err := h.channelResolver.ResolveChannel(channelNameOrID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve channel '%s': %w", channelNameOrID, err)
	}

	// Format alert message
	message := h.formatAlertMessage(alert)

	// Post message to Slack
	_, timestamp, _, err := h.slackClient.SendMessage(
		channelID,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	// Add alert reaction
	if err := h.slackClient.AddReaction("🚨", slack.ItemRef{
		Channel:   channelID,
		Timestamp: timestamp,
	}); err != nil {
		log.Printf("Error adding reaction: %v", err)
	}

	log.Printf("Posted alert to Slack: channel=%s, thread_ts=%s", channelID, timestamp)
	return timestamp, nil
}

// formatAlertMessage formats a Zabbix alert for Slack
func (h *ZabbixHandler) formatAlertMessage(alert *models.ZabbixAlert) string {
	emoji := alert.GetSeverityEmoji()
	severityLabel := alert.GetSeverityLabel()

	return fmt.Sprintf(`%s *Alert: %s*

🏷️ *Hardware:* %s
📊 *Metric:* %s = %s
⚠️ *Severity:* %s (%s)
⏱️ *Duration:* %s
🔍 *Trigger:* %s
🆔 *Event ID:* %s
🕐 *Time:* %s`,
		emoji,
		alert.AlertName,
		alert.Hardware,
		alert.MetricName,
		alert.MetricValue,
		alert.Severity,
		severityLabel,
		alert.PendingDuration,
		alert.TriggerExpression,
		alert.EventID,
		alert.EventTime,
	)
}

// triggerInvestigation automatically starts investigating the alert
func (h *ZabbixHandler) triggerInvestigation(alert *models.ZabbixAlert, threadTS string) {
	log.Printf("Starting automatic investigation for alert: event_id=%s", alert.EventID)

	// Resolve channel if Slack is enabled
	var channelID string
	if h.isSlackEnabled() && h.alertsChannel != "" {
		var err error
		channelID, err = h.channelResolver.ResolveChannel(h.alertsChannel)
		if err != nil {
			log.Printf("Warning: Error resolving channel for investigation: %v", err)
			// Continue without Slack notifications
		}
	}

	// Build alert message for title generation
	alertMessage := fmt.Sprintf("%s - %s: %s", alert.AlertName, alert.Hardware, alert.MetricName)
	if alert.MetricValue != "" {
		alertMessage += " = " + alert.MetricValue
	}

	// Spawn incident manager for Zabbix alert
	incidentCtx := &services.IncidentContext{
		Source:   "zabbix",
		SourceID: alert.EventID,
		Context: database.JSONB{
			"alert_name":         alert.AlertName,
			"hardware":          alert.Hardware,
			"metric_name":       alert.MetricName,
			"metric_value":      alert.MetricValue,
			"severity":          alert.Severity,
			"pending_duration":  alert.PendingDuration,
			"trigger_expression": alert.TriggerExpression,
			"event_time":        alert.EventTime,
		},
		Message: alertMessage, // Pass alert info for title generation
	}

	incidentUUID, workingDir, err := h.skillService.SpawnIncidentManager(incidentCtx)
	if err != nil {
		log.Printf("Error spawning incident manager for Zabbix alert: %v", err)
		if h.isSlackEnabled() && channelID != "" {
			h.slackClient.PostMessage(
				channelID,
				slack.MsgOptionText(fmt.Sprintf("❌ Failed to spawn incident manager: %v", err), false),
				slack.MsgOptionTS(threadTS),
			)
		}
		return
	}

	log.Printf("Spawned incident manager for Zabbix alert: UUID=%s, WorkingDir=%s", incidentUUID, workingDir)

	// Update incident status to "running" before execution
	if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
		log.Printf("Warning: Failed to update incident status to running: %v", err)
	}

	// Post "investigating" message if Slack is enabled
	var progressMsgTS string
	if h.isSlackEnabled() && channelID != "" {
		_, progressMsgTS, _, err = h.slackClient.SendMessage(
			channelID,
			slack.MsgOptionText("🔄 *Starting automatic investigation...*\n```\nInitializing...\n```", false),
			slack.MsgOptionTS(threadTS),
		)
		if err != nil {
			log.Printf("Warning: Error posting investigation message: %v", err)
			// Continue without Slack notifications
		}
	}

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert)

	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Progress update callback
	onStderrUpdate := func(progressLog string) {
		// Update progress message if Slack is enabled
		if h.isSlackEnabled() && channelID != "" && progressMsgTS != "" {
			_, _, _, err := h.slackClient.UpdateMessage(
				channelID,
				progressMsgTS,
				slack.MsgOptionText(fmt.Sprintf("🔄 *Investigating...*\n```\n%s\n```", progressLog), false),
			)
			if err != nil {
				log.Printf("Warning: Error updating investigation progress: %v", err)
			}
		}
	}

	// Execute investigation in the incident-specific working directory
	ctx := context.Background()
	log.Printf("Executing Zabbix investigation in working directory: %s", workingDir)
	result := h.codexExecutor.ExecuteForSlackInDirectory(ctx, taskWithGuidance, "", workingDir, onStderrUpdate)

	// Update incident status based on execution result
	finalStatus := database.IncidentStatusCompleted
	if result.Error != nil {
		finalStatus = database.IncidentStatusFailed
	}

	// Prepend the original Zabbix alert details to the full log for context
	alertHeader := fmt.Sprintf(`🚨 Original Zabbix Alert:

Alert: %s
Hardware: %s
Metric: %s = %s
Severity: %s (%s)
Duration: %s
Trigger: %s
Event ID: %s
Time: %s

--- Investigation Log ---

`, alert.AlertName, alert.Hardware, alert.MetricName, alert.MetricValue,
		alert.Severity, alert.GetSeverityLabel(), alert.PendingDuration,
		alert.TriggerExpression, alert.EventID, alert.EventTime)

	fullLogWithContext := alertHeader + result.FullLog

	if err := h.skillService.UpdateIncidentStatus(incidentUUID, finalStatus, result.SessionID, fullLogWithContext); err != nil {
		log.Printf("Warning: Failed to update incident status to %s: %v", finalStatus, err)
	} else {
		log.Printf("Updated incident %s to status: %s, session: %s, log length: %d", incidentUUID, finalStatus, result.SessionID, len(fullLogWithContext))
	}

	// Slack notifications (if enabled)
	if h.isSlackEnabled() && channelID != "" && threadTS != "" {
		// Add result reaction
		if result.Error != nil {
			if err := h.slackClient.AddReaction("x", slack.ItemRef{
				Channel:   channelID,
				Timestamp: threadTS,
			}); err != nil {
				log.Printf("Warning: Error adding error reaction: %v", err)
			}
		} else {
			if err := h.slackClient.AddReaction("white_check_mark", slack.ItemRef{
				Channel:   channelID,
				Timestamp: threadTS,
			}); err != nil {
				log.Printf("Warning: Error adding success reaction: %v", err)
			}
		}

		// Update with final result
		if progressMsgTS != "" {
			_, _, _, err = h.slackClient.UpdateMessage(
				channelID,
				progressMsgTS,
				slack.MsgOptionText(result.Response, false),
			)
			if err != nil {
				log.Printf("Warning: Error updating final investigation result: %v", err)
			}
		}
	}

	log.Printf("Investigation complete for alert: event_id=%s", alert.EventID)
}

// buildInvestigationPrompt creates an investigation prompt for the alert
func (h *ZabbixHandler) buildInvestigationPrompt(alert *models.ZabbixAlert) string {
	return fmt.Sprintf(`Investigate this Zabbix alert:

Alert: %s
Hardware: %s
Metric: %s = %s
Severity: %s (%s)
Duration: %s
Trigger: %s
Event ID: %s

Please:
1. Check if this is a known issue or pattern
2. Analyze the metric and check for trends
3. Check related hosts/VMs if this is infrastructure-related
4. Identify potential root causes
5. Suggest remediation steps with priority
6. Assess urgency and impact

Be specific and actionable. Reference any relevant data sources or scripts you use.`,
		alert.AlertName,
		alert.Hardware,
		alert.MetricName,
		alert.MetricValue,
		alert.Severity,
		alert.GetSeverityLabel(),
		alert.PendingDuration,
		alert.TriggerExpression,
		alert.EventID,
	)
}
