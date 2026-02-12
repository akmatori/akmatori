package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/alerts/extraction"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/output"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SlackHandler handles Slack events and commands
type SlackHandler struct {
	client         *slack.Client
	codexExecutor  *executor.Executor
	codexWSHandler *CodexWSHandler
	skillService   *services.SkillService

	// Alert channel support
	alertChannels    map[string]*database.AlertSourceInstance // channel_id -> instance
	alertChannelsMu  sync.RWMutex
	alertExtractor   *extraction.AlertExtractor
	alertHandler     *AlertHandler
	alertService     *services.AlertService
	botUserID        string // Bot's user ID for self-message filtering
}

// Progress update interval for Slack messages (rate limiting)
const progressUpdateInterval = 2 * time.Second

// NewSlackHandler creates a new Slack handler
func NewSlackHandler(
	client *slack.Client,
	codexExecutor *executor.Executor,
	codexWSHandler *CodexWSHandler,
	skillService *services.SkillService,
) *SlackHandler {
	return &SlackHandler{
		client:         client,
		codexExecutor:  codexExecutor,
		codexWSHandler: codexWSHandler,
		skillService:   skillService,
		alertChannels:  make(map[string]*database.AlertSourceInstance),
		alertExtractor: extraction.NewAlertExtractor(),
	}
}

// SetAlertHandler sets the alert handler for processing Slack channel alerts
func (h *SlackHandler) SetAlertHandler(alertHandler *AlertHandler) {
	h.alertHandler = alertHandler
}

// SetAlertService sets the alert service for loading alert channel configs
func (h *SlackHandler) SetAlertService(alertService *services.AlertService) {
	h.alertService = alertService
}

// SetBotUserID sets the bot's user ID for self-message filtering
func (h *SlackHandler) SetBotUserID(botUserID string) {
	h.botUserID = botUserID
}

// LoadAlertChannels loads alert channel configurations from the database
func (h *SlackHandler) LoadAlertChannels() error {
	if h.alertService == nil {
		log.Printf("Alert service not configured, skipping alert channel loading")
		return nil
	}

	instances, err := h.alertService.ListInstances()
	if err != nil {
		return fmt.Errorf("failed to list alert source instances: %w", err)
	}

	h.alertChannelsMu.Lock()
	defer h.alertChannelsMu.Unlock()

	// Clear existing channels
	h.alertChannels = make(map[string]*database.AlertSourceInstance)

	// Load slack_channel instances
	for i := range instances {
		instance := &instances[i]
		if instance.AlertSourceType.Name != "slack_channel" || !instance.Enabled {
			continue
		}

		// Extract channel ID from settings
		channelID, ok := instance.Settings["slack_channel_id"].(string)
		if !ok || channelID == "" {
			log.Printf("Slack channel instance %s has no channel ID configured", instance.Name)
			continue
		}

		h.alertChannels[channelID] = instance
		log.Printf("Loaded alert channel: %s -> %s", channelID, instance.Name)
	}

	log.Printf("Loaded %d alert channel(s)", len(h.alertChannels))
	return nil
}

// ReloadAlertChannels reloads alert channel configurations (called when settings change)
func (h *SlackHandler) ReloadAlertChannels() {
	if err := h.LoadAlertChannels(); err != nil {
		log.Printf("Warning: Failed to reload alert channels: %v", err)
	}
}

// isAlertChannel checks if a channel is configured as an alert channel
func (h *SlackHandler) isAlertChannel(channelID string) (*database.AlertSourceInstance, bool) {
	h.alertChannelsMu.RLock()
	defer h.alertChannelsMu.RUnlock()

	instance, ok := h.alertChannels[channelID]
	return instance, ok
}

// HandleSocketMode starts the Socket Mode handler
func (h *SlackHandler) HandleSocketMode(socketClient *socketmode.Client) {
	go func() {
		for evt := range socketClient.Events {
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					log.Printf("Ignored %+v\n", evt)
					continue
				}

				// Ack immediately to avoid Slack retries
				socketClient.Ack(*evt.Request)

				// Process event in a goroutine to handle multiple messages concurrently
				go h.handleEventsAPI(eventsAPIEvent)

			case socketmode.EventTypeInteractive:
				socketClient.Ack(*evt.Request)

			case socketmode.EventTypeSlashCommand:
				socketClient.Ack(*evt.Request)

			default:
				log.Printf("Unexpected event type received: %s\n", evt.Type)
			}
		}
	}()
}

// handleEventsAPI processes Events API events
func (h *SlackHandler) handleEventsAPI(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			h.handleAppMention(ev)
		case *slackevents.MessageEvent:
			h.handleMessage(ev)
		}
	}
}

// handleAppMention processes app mention events
func (h *SlackHandler) handleAppMention(event *slackevents.AppMentionEvent) {
	// Remove bot mention from text
	text := strings.TrimSpace(strings.Replace(event.Text, fmt.Sprintf("<@%s>", event.BotID), "", 1))

	h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, text, event.User)
}

// handleMessage processes message events (DMs and alert channels)
func (h *SlackHandler) handleMessage(event *slackevents.MessageEvent) {
	// Always skip our own messages to prevent loops
	if h.botUserID != "" && event.User == h.botUserID {
		return
	}

	// Check if this is a configured alert channel BEFORE filtering bots,
	// because monitoring integrations post as bots (bot_message subtype)
	if instance, ok := h.isAlertChannel(event.Channel); ok {
		// Skip thread replies - only process top-level messages
		if event.ThreadTimeStamp != "" {
			return
		}

		// Only process bot/integration messages in alert channels.
		// Some integrations (e.g. Zabbix) set BotID without bot_message subtype,
		// others use the bot_message subtype. Accept both, skip human users.
		isBotMessage := event.SubType == "bot_message" || event.BotID != ""
		if !isBotMessage {
			return
		}

		// Process as alert channel message
		go h.handleAlertChannelMessage(event, instance)
		return
	}

	// For non-alert-channel messages, ignore bot messages and subtypes (edits, deletes, etc.)
	if event.BotID != "" || event.SubType != "" {
		return
	}

	// Only process DMs (ChannelType == "im") for conversational AI
	if event.ChannelType != "im" {
		return
	}

	h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
}

// processMessage is the core message processing logic
func (h *SlackHandler) processMessage(channel, threadTS, messageTS, text, user string) {
	// Check if Slack is still enabled before processing
	// This catches messages queued before Slack was disabled
	settings, err := database.GetSlackSettings()
	if err != nil || !settings.IsActive() {
		log.Printf("Slack is disabled, ignoring message from channel %s", channel)
		return
	}

	// Determine thread ID
	threadID := messageTS
	if threadTS != "" {
		threadID = threadTS
	}

	var sessionID string
	var incidentUUID string
	var workingDir string

	// Check if this is an existing incident (continuation) by looking up in database
	var incident database.Incident
	if err := database.GetDB().Where("source = ? AND source_id = ?", "slack", threadID).First(&incident).Error; err == nil {
		// Existing incident found - resume session
		sessionID = incident.SessionID
		incidentUUID = incident.UUID
		workingDir = incident.WorkingDir
		log.Printf("Resuming session %s for thread %s (incident: %s)", sessionID, threadID, incidentUUID)
	} else {
		// New thread - spawn incident manager
		log.Printf("Starting new session for thread %s", threadID)

		// Spawn incident manager for this event
		incidentCtx := &services.IncidentContext{
			Source:   "slack",
			SourceID: threadID,
			Context: database.JSONB{
				"channel": channel,
				"user":    user,
				"text":    text,
			},
			Message: text, // Pass message for title generation
		}

		var err error
		incidentUUID, workingDir, err = h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			log.Printf("Error spawning incident manager: %v", err)
			h.client.PostMessage(
				channel,
				slack.MsgOptionText(fmt.Sprintf("❌ Failed to spawn incident manager: %v", err), false),
				slack.MsgOptionTS(threadID),
			)
			return
		}

		log.Printf("Spawned incident manager: UUID=%s, WorkingDir=%s", incidentUUID, workingDir)
	}

	// Update incident status to "running" before execution
	if incidentUUID != "" {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			log.Printf("Warning: Failed to update incident status to running: %v", err)
		}
	}

	// Add processing reaction
	if err := h.client.AddReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   channel,
		Timestamp: threadID,
	}); err != nil {
		log.Printf("Error adding reaction: %v", err)
	}

	// Post initial progress message
	_, progressMsgTS, _, err := h.client.SendMessage(
		channel,
		slack.MsgOptionText("🔄 *Executing task...*\n```\nWaiting for output...\n```", false),
		slack.MsgOptionTS(threadID),
	)
	if err != nil {
		log.Printf("Error posting progress message: %v", err)
		return
	}

	// Track last update time to implement rate limiting
	lastUpdate := time.Now()
	var lastProgressLog string

	// Progress update callback
	onStderrUpdate := func(progressLog string) {
		if progressLog == "" {
			return
		}

		if time.Since(lastUpdate) < progressUpdateInterval {
			return
		}

		if progressLog == lastProgressLog {
			return
		}

		lastUpdate = time.Now()
		lastProgressLog = progressLog

		// Truncate if too long (Slack has ~4000 char limit, keep under 3000 to be safe)
		maxProgressLen := 3000
		truncatedLog := progressLog
		if len(progressLog) > maxProgressLen {
			truncatedLog = progressLog[len(progressLog)-maxProgressLen:]
			if idx := strings.Index(truncatedLog, "\n"); idx > 0 && idx < 100 {
				truncatedLog = truncatedLog[idx+1:]
			}
			truncatedLog = "...(truncated)\n" + truncatedLog
		}

		_, _, _, err := h.client.UpdateMessage(
			channel,
			progressMsgTS,
			slack.MsgOptionText(fmt.Sprintf("🔄 *Progress Log:*\n```\n%s\n```", truncatedLog), false),
		)
		if err != nil {
			log.Printf("Error updating progress message (ts=%s): %v", progressMsgTS, err)
		}
	}

	taskWithGuidance := executor.PrependGuidance(text)

	// Execute via WebSocket-based Codex worker
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
		var finalSessionID string
		var hasError bool
		var lastStreamedLog string

		// Build task header for logging
		taskHeader := fmt.Sprintf("📨 Slack Message from User <%s>:\n%s\n\n--- Execution Log ---\n\n", user, text)

		callback := IncidentCallback{
			OnOutput: func(outputLog string) {
				lastStreamedLog = outputLog
				// Update database with streamed log
				h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+outputLog)

				// Also update Slack progress message
				onStderrUpdate(outputLog)
			},
			OnCompleted: func(sid, output string) {
				finalSessionID = sid
				response = output
				close(done)
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("❌ Error: %s", errorMsg)
				hasError = true
				close(done)
			},
		}

		// Start or continue incident based on whether we have a session
		var wsErr error
		if sessionID != "" {
			log.Printf("Continuing session %s for incident %s", sessionID, incidentUUID)
			wsErr = h.codexWSHandler.ContinueIncident(incidentUUID, sessionID, taskWithGuidance, callback)
		} else {
			log.Printf("Starting new Codex session for incident %s", incidentUUID)
			wsErr = h.codexWSHandler.StartIncident(incidentUUID, taskWithGuidance, openaiSettings, callback)
		}

		if wsErr != nil {
			log.Printf("Failed to start/continue incident via WebSocket: %v", wsErr)
			h.finishSlackMessage(channel, threadID, progressMsgTS, incidentUUID, user, text,
				fmt.Sprintf("❌ Codex worker error: %v", wsErr), "", true, "")
			return
		}

		// Wait for completion
		<-done

		// Use original sessionID if finalSessionID is empty (for resume cases)
		if finalSessionID == "" {
			finalSessionID = sessionID
		}

		// Build full log
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Format response for Slack
		var finalResponse string
		if hasError {
			finalResponse = response
		} else if response != "" {
			parsed := output.Parse(response)
			finalResponse = output.FormatForSlack(parsed)
		} else {
			finalResponse = "✅ Task completed (no output)"
		}

		h.finishSlackMessage(channel, threadID, progressMsgTS, incidentUUID, user, text,
			finalResponse, fullLog, hasError, finalSessionID)
		return
	}

	// No WebSocket worker available
	log.Printf("ERROR: Codex worker not connected for incident %s", incidentUUID)
	h.finishSlackMessage(channel, threadID, progressMsgTS, incidentUUID, user, text,
		"❌ Codex worker not connected. Please check that the akmatori-codex container is running.", "", true, "")
}

// finishSlackMessage handles the final steps of Slack message processing
func (h *SlackHandler) finishSlackMessage(channel, threadID, progressMsgTS, incidentUUID, user, text, finalResponse, fullLog string, hasError bool, sessionID string) {
	// Remove processing reaction
	if removeErr := h.client.RemoveReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   channel,
		Timestamp: threadID,
	}); removeErr != nil {
		log.Printf("Error removing reaction: %v", removeErr)
	}

	// Add result reaction
	if hasError {
		if addErr := h.client.AddReaction("x", slack.ItemRef{
			Channel:   channel,
			Timestamp: threadID,
		}); addErr != nil {
			log.Printf("Error adding error reaction: %v", addErr)
		}
	} else {
		if addErr := h.client.AddReaction("white_check_mark", slack.ItemRef{
			Channel:   channel,
			Timestamp: threadID,
		}); addErr != nil {
			log.Printf("Error adding success reaction: %v", addErr)
		}
	}

	// Update incident with status, log, and response
	if incidentUUID != "" {
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}

		// Build full log with context if not already built
		fullLogWithContext := fullLog
		if fullLogWithContext == "" {
			fullLogWithContext = fmt.Sprintf("📨 Original Message from User <%s>:\n%s\n\n--- Execution Log ---\n\n%s",
				user, text, "")
		}

		if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLogWithContext, finalResponse); updateErr != nil {
			log.Printf("Warning: Failed to update incident: %v", updateErr)
		} else {
			log.Printf("Updated incident %s to status: %s, session: %s", incidentUUID, finalStatus, sessionID)
		}
	}

	// Update the progress message with the final result
	_, _, _, updateErr := h.client.UpdateMessage(
		channel,
		progressMsgTS,
		slack.MsgOptionText(finalResponse, false),
	)
	if updateErr != nil {
		log.Printf("Error updating final result: %v", updateErr)
	}
}

// handleAlertChannelMessage processes a message from a configured alert channel
func (h *SlackHandler) handleAlertChannelMessage(event *slackevents.MessageEvent, instance *database.AlertSourceInstance) {
	log.Printf("Processing alert channel message from %s in channel %s", event.User, event.Channel)

	// Extract message text (including text from blocks and attachments)
	messageText := h.extractFullMessageText(event)
	if messageText == "" {
		log.Printf("Empty message text, skipping")
		return
	}

	// Get custom extraction prompt if configured
	var customPrompt string
	if instance.Settings != nil {
		if prompt, ok := instance.Settings["extraction_prompt"].(string); ok {
			customPrompt = prompt
		}
	}

	// Extract alert fields via AI
	ctx := context.Background()
	var normalized *alerts.NormalizedAlert
	var err error

	if customPrompt != "" {
		normalized, err = h.alertExtractor.ExtractWithPrompt(ctx, messageText, customPrompt)
	} else {
		normalized, err = h.alertExtractor.Extract(ctx, messageText)
	}

	if err != nil {
		log.Printf("Alert extraction failed: %v, using fallback", err)
		// Fallback alert is created by the extractor
	}

	// Set fingerprint and source fields
	normalized.SourceFingerprint = fmt.Sprintf("slack:%s:%s", event.Channel, event.TimeStamp)
	normalized.SourceAlertID = event.TimeStamp

	// Store Slack context for thread replies
	if normalized.RawPayload == nil {
		normalized.RawPayload = make(map[string]interface{})
	}
	normalized.RawPayload["slack_channel_id"] = event.Channel
	normalized.RawPayload["slack_message_ts"] = event.TimeStamp
	normalized.RawPayload["slack_user"] = event.User

	// Add hourglass reaction to indicate processing
	if err := h.client.AddReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   event.Channel,
		Timestamp: event.TimeStamp,
	}); err != nil {
		log.Printf("Error adding reaction: %v", err)
	}

	// Process through AlertHandler if available
	if h.alertHandler != nil {
		h.alertHandler.ProcessAlertFromSlackChannel(instance, *normalized, event.Channel, event.TimeStamp)
	} else {
		log.Printf("AlertHandler not configured, cannot process Slack channel alert")
		// Remove hourglass and add warning reaction
		h.client.RemoveReaction("hourglass_flowing_sand", slack.ItemRef{
			Channel:   event.Channel,
			Timestamp: event.TimeStamp,
		})
		h.client.AddReaction("warning", slack.ItemRef{
			Channel:   event.Channel,
			Timestamp: event.TimeStamp,
		})
	}
}

// extractFullMessageText extracts the full text content from a Slack message event
func (h *SlackHandler) extractFullMessageText(event *slackevents.MessageEvent) string {
	// The Text field contains the rendered text from the message,
	// including text from blocks. This is sufficient for most alert messages.
	// Note: slackevents.MessageEvent doesn't expose Attachments directly,
	// but the Text field should contain the main message content.
	return event.Text
}
