package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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
	client        *slack.Client
	codexExecutor *executor.Executor
	skillService  *services.SkillService
}

// Progress update interval for Slack messages (rate limiting)
const progressUpdateInterval = 2 * time.Second

// NewSlackHandler creates a new Slack handler
func NewSlackHandler(
	client *slack.Client,
	codexExecutor *executor.Executor,
	skillService *services.SkillService,
) *SlackHandler {
	return &SlackHandler{
		client:        client,
		codexExecutor: codexExecutor,
		skillService:  skillService,
	}
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

// handleMessage processes message events (DMs)
func (h *SlackHandler) handleMessage(event *slackevents.MessageEvent) {
	// Ignore bot messages and threaded replies in channels
	if event.BotID != "" || event.SubType != "" {
		return
	}

	// Only process DMs (ChannelType == "im")
	if event.ChannelType != "im" {
		return
	}

	h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
}

// processMessage is the core message processing logic
func (h *SlackHandler) processMessage(channel, threadTS, messageTS, text, user string) {
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

	// Execute Codex task - Codex handles agent/skill invocation natively
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	taskWithGuidance := executor.PrependGuidance(text)

	log.Printf("Executing incident manager in: %s", workingDir)
	result, err := h.codexExecutor.ExecuteInDirectory(ctx, taskWithGuidance, sessionID, workingDir, onStderrUpdate)

	// Remove processing reaction
	if removeErr := h.client.RemoveReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   channel,
		Timestamp: threadID,
	}); removeErr != nil {
		log.Printf("Error removing reaction: %v", removeErr)
	}

	// Add result reaction
	if err != nil {
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

	// Determine final status and session ID for incident update
	finalStatus := database.IncidentStatusCompleted
	if err != nil {
		finalStatus = database.IncidentStatusFailed
	}
	finalSessionID := ""
	if result != nil {
		finalSessionID = result.SessionID
	}
	if finalSessionID == "" {
		finalSessionID = sessionID
	}

	// Prepend the original user message to the full log for context
	fullLog := ""
	if result != nil {
		fullLog = result.FullLog
	}
	fullLogWithContext := fmt.Sprintf("📨 Original Message from User <%s>:\n%s\n\n--- Execution Log ---\n\n%s",
		user, text, fullLog)

	// Build final response for Slack
	var finalResponse string
	var rawOutput string
	if err != nil {
		finalResponse = fmt.Sprintf("❌ Error executing task: %v", err)
		rawOutput = finalResponse
	} else if result != nil && result.Output != "" {
		rawOutput = result.Output
		parsed := output.Parse(result.Output)
		finalResponse = output.FormatForSlack(parsed)

		finalResponse += fmt.Sprintf("\n\n---\n⏱️ Time: %s", formatSlackDuration(result.ExecutionTime))
		if result.TokensUsed > 0 {
			finalResponse += fmt.Sprintf(" | 🎯 Tokens: %s", formatSlackNumber(result.TokensUsed))
		}
	} else if result != nil && len(result.ErrorMessages) > 0 {
		finalResponse = "❌ " + strings.Join(result.ErrorMessages, "\n❌ ")
		rawOutput = finalResponse
	} else {
		finalResponse = "✅ Task completed (no output)"
		rawOutput = ""
	}

	// Update incident with status, log, and response
	if incidentUUID != "" {
		if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, finalSessionID, fullLogWithContext, rawOutput); updateErr != nil {
			log.Printf("Warning: Failed to update incident: %v", updateErr)
		} else {
			log.Printf("Updated incident %s to status: %s, session: %s, log length: %d, response length: %d",
				incidentUUID, finalStatus, finalSessionID, len(fullLogWithContext), len(rawOutput))
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

// formatSlackDuration formats a duration for Slack display
func formatSlackDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if minutes < 60 {
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	minutes = minutes % 60
	if minutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dh", hours)
}

// formatSlackNumber formats a number with comma separators
func formatSlackNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	str := fmt.Sprintf("%d", n)
	var result []rune
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, c)
	}
	return string(result)
}

// GetThreadID extracts the thread ID from a message
func GetThreadID(threadTS, messageTS string) string {
	if threadTS != "" {
		return threadTS
	}
	return messageTS
}

// IsNewThread checks if this is a new thread
func IsNewThread(threadTS string) bool {
	return threadTS == ""
}
