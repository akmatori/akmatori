package handlers

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/alerts/extraction"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SlackHandler handles Slack events and commands
type SlackHandler struct {
	client         *slack.Client
	agentExecutor  *executor.Executor
	agentWSHandler *AgentWSHandler
	skillService   services.SkillIncidentManager

	// Alert channel support
	alertChannels   map[string]*database.AlertSourceInstance // channel_id -> instance
	alertChannelsMu sync.RWMutex
	alertExtractor  *extraction.AlertExtractor
	alertHandler    *AlertHandler
	alertService    services.AlertManager
	botUserID       string // Bot's user ID for self-message filtering

	// Dedup: prevent double processing when both app_mention and message events fire
	processedMsgs sync.Map // key: "channel:messageTS" -> struct{}

	// Track alert channel threads that already have a bot message being processed,
	// so subsequent bot thread replies (status updates) are skipped.
	alertThreads sync.Map // key: threadTS -> struct{}
}

// Progress update interval for Slack messages (rate limiting)
const progressUpdateInterval = 2 * time.Second

// NewSlackHandler creates a new Slack handler
func NewSlackHandler(
	client *slack.Client,
	agentExecutor *executor.Executor,
	agentWSHandler *AgentWSHandler,
	skillService services.SkillIncidentManager,
) *SlackHandler {
	return &SlackHandler{
		client:         client,
		agentExecutor:  agentExecutor,
		agentWSHandler: agentWSHandler,
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
func (h *SlackHandler) SetAlertService(alertService services.AlertManager) {
	h.alertService = alertService
}

// SetBotUserID sets the bot's user ID for self-message filtering
func (h *SlackHandler) SetBotUserID(botUserID string) {
	h.botUserID = botUserID
}

// LoadAlertChannels loads alert channel configurations from the database
func (h *SlackHandler) LoadAlertChannels() error {
	if h.alertService == nil {
		slog.Info("alert service not configured, skipping alert channel loading")
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
			slog.Warn("Slack channel instance has no channel ID configured", "instance", instance.Name)
			continue
		}

		h.alertChannels[channelID] = instance
		slog.Info("loaded alert channel", "channel", channelID, "instance", instance.Name)
	}

	slog.Info("loaded alert channels", "count", len(h.alertChannels))
	return nil
}

// ReloadAlertChannels reloads alert channel configurations (called when settings change)
func (h *SlackHandler) ReloadAlertChannels() {
	if err := h.LoadAlertChannels(); err != nil {
		slog.Warn("failed to reload alert channels", "err", err)
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
					slog.Warn("ignored non-EventsAPI data", "event", evt)
					continue
				}

				slog.Info("received Events API event", "outer_type", eventsAPIEvent.Type, "inner_type", eventsAPIEvent.InnerEvent.Type)

				// Ack immediately to avoid Slack retries
				socketClient.Ack(*evt.Request)

				// Process event in a goroutine to handle multiple messages concurrently
				go h.handleEventsAPI(eventsAPIEvent)

			case socketmode.EventTypeInteractive:
				socketClient.Ack(*evt.Request)

			case socketmode.EventTypeSlashCommand:
				socketClient.Ack(*evt.Request)

			case socketmode.EventTypeConnecting,
				socketmode.EventTypeConnected,
				socketmode.EventTypeHello:
				// Socket Mode lifecycle events - expected, no action needed
				slog.Info("Socket Mode lifecycle event", "type", evt.Type)

			default:
				slog.Warn("unexpected event type received", "type", evt.Type)
			}
		}
		slog.Info("Socket Mode event loop ended (Events channel closed)")
	}()
}

// handleEventsAPI processes Events API events
func (h *SlackHandler) handleEventsAPI(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			slog.Info("processing app_mention event", "user", ev.User, "channel", ev.Channel)
			h.handleAppMention(ev)
		case *slackevents.MessageEvent:
			slog.Info("processing message event", "channel", ev.Channel, "channel_type", ev.ChannelType, "user", ev.User, "subtype", ev.SubType, "bot_id", ev.BotID)
			h.handleMessage(ev)
		default:
			slog.Info("unhandled inner event type", "type", innerEvent.Type)
		}
	}
}

// handleAppMention processes app mention events
func (h *SlackHandler) handleAppMention(event *slackevents.AppMentionEvent) {
	// Dedup: skip if already processed via handleMessage (both events can fire)
	dedupeKey := event.Channel + ":" + event.TimeStamp
	if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
		slog.Info("skipping duplicate app_mention processing", "dedupe_key", dedupeKey)
		return
	}
	go func() {
		time.Sleep(60 * time.Second)
		h.processedMsgs.Delete(dedupeKey)
	}()

	// Remove bot mention from text.
	// Use botUserID (the bot's User ID that appears in <@U...> mentions).
	// Fall back to event.BotID for bot-triggered mentions.
	text := event.Text
	if h.botUserID != "" {
		text = strings.Replace(text, fmt.Sprintf("<@%s>", h.botUserID), "", 1)
	}
	if event.BotID != "" {
		text = strings.Replace(text, fmt.Sprintf("<@%s>", event.BotID), "", 1)
	}
	text = strings.TrimSpace(text)

	// If this is a thread reply, fetch the parent message for context
	// so the AI knows what "this alert" or "this message" refers to.
	if event.ThreadTimeStamp != "" {
		parentText := h.fetchThreadParentText(event.Channel, event.ThreadTimeStamp)
		if parentText != "" {
			text = fmt.Sprintf("Context — original message in this thread:\n---\n%s\n---\n\nUser request: %s", parentText, text)
		}
	}

	h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, text, event.User)
}

// fetchThreadParentText fetches the parent (first) message of a Slack thread.
// Extracts text from the message body, attachments, and blocks since monitoring
// tools (Zabbix, Datadog, etc.) often send content in attachments/blocks rather
// than the plain Text field.
func (h *SlackHandler) fetchThreadParentText(channelID, threadTS string) string {
	msgs, _, _, err := h.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     1,
		Inclusive: true,
	})
	if err != nil {
		slog.Error("failed to fetch thread parent message", "channel", channelID, "thread_ts", threadTS, "err", err)
		return ""
	}
	if len(msgs) == 0 {
		return ""
	}
	return extractSlackMessageText(msgs[0])
}

// handleBotMentionInThread processes a human @mention of the bot in an alert channel thread.
// It strips the mention, fetches the parent message for context, and processes via processMessage.
// Uses dedup to avoid double-processing when both app_mention and message events fire.
func (h *SlackHandler) handleBotMentionInThread(channel, threadTS, messageTS, rawText, user string) {
	// Dedup: skip if this message was already processed (e.g. via app_mention event)
	dedupeKey := channel + ":" + messageTS
	if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
		slog.Info("skipping duplicate bot mention processing", "dedupe_key", dedupeKey)
		return
	}
	// Clean up after 60 seconds
	go func() {
		time.Sleep(60 * time.Second)
		h.processedMsgs.Delete(dedupeKey)
	}()

	// Strip bot mention
	text := strings.TrimSpace(strings.Replace(rawText, fmt.Sprintf("<@%s>", h.botUserID), "", 1))

	// Fetch the parent message (the alert) for context
	if parentText := h.fetchThreadParentText(channel, threadTS); parentText != "" {
		text = fmt.Sprintf("Context — original message in this thread:\n---\n%s\n---\n\nUser request: %s", parentText, text)
	}

	h.processMessage(channel, threadTS, messageTS, text, user)
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
		slog.Debug("alert channel message received",
			"channel", event.Channel,
			"user", event.User,
			"bot_id", event.BotID,
			"sub_type", event.SubType,
			"ts", event.TimeStamp,
			"thread_ts", event.ThreadTimeStamp,
			"text_preview", truncateForLog(event.Text, 100),
		)
		// Detect bot/integration messages (Zabbix, Alertmanager, etc.)
		// Some integrations set BotID without bot_message subtype,
		// others use the bot_message subtype. Accept both.
		isBotMessage := event.SubType == "bot_message" || event.BotID != ""

		if event.ThreadTimeStamp != "" {
			// Thread reply in alert channel.
			if isBotMessage {
				// Allow the first bot message per thread (the actual alert) but skip
				// subsequent bot thread replies which are status updates (e.g. PagerDuty
				// "Status changed to Acknowledged", "Status changed to Triggered").
				if _, alreadyTracked := h.alertThreads.LoadOrStore(event.ThreadTimeStamp, struct{}{}); alreadyTracked {
					slog.Info("skipping bot thread reply in alert channel (status update)",
						"thread_ts", event.ThreadTimeStamp,
						"channel", event.Channel,
						"bot_id", event.BotID,
					)
				} else {
					// Clean up after 1 hour to prevent unbounded growth.
					threadTS := event.ThreadTimeStamp
					go func() {
						time.Sleep(1 * time.Hour)
						h.alertThreads.Delete(threadTS)
					}()
					// First bot message in this thread — process as alert.
					go h.handleAlertChannelMessage(event, instance)
				}
			} else if h.botUserID != "" && event.SubType == "" && event.User != "" &&
				strings.Contains(event.Text, fmt.Sprintf("<@%s>", h.botUserID)) {
				// Human user @mentioning the bot in a thread reply.
				h.handleBotMentionInThread(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
			}
			// Ignore other thread replies (regular human chat).
			return
		}

		// Top-level message: check for human @mention of the bot.
		if !isBotMessage {
			// Check if this instance processes human messages as alerts
			if shouldProcessHuman, _ := instance.Settings["process_human_messages"].(bool); shouldProcessHuman {
				go h.handleAlertChannelMessage(event, instance)
				return
			}
			if h.botUserID != "" && event.SubType == "" && event.User != "" &&
				strings.Contains(event.Text, fmt.Sprintf("<@%s>", h.botUserID)) {
				// Human @mentioning the bot at top level in alert channel.
				// Dedup with app_mention handler.
				dedupeKey := event.Channel + ":" + event.TimeStamp
				if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
					return
				}
				go func() {
					time.Sleep(60 * time.Second)
					h.processedMsgs.Delete(dedupeKey)
				}()

				text := strings.Replace(event.Text, fmt.Sprintf("<@%s>", h.botUserID), "", 1)
				text = strings.TrimSpace(text)
				h.processMessage(event.Channel, "", event.TimeStamp, text, event.User)
			}
			return
		}

		// Top-level bot message — process as alert and track the thread
		// so that subsequent bot replies (status updates) in this thread are skipped.
		h.alertThreads.LoadOrStore(event.TimeStamp, struct{}{})
		ts := event.TimeStamp
		go func() {
			time.Sleep(1 * time.Hour)
			h.alertThreads.Delete(ts)
		}()
		go h.handleAlertChannelMessage(event, instance)
		return
	}

	// For non-alert-channel messages, ignore bot messages and subtypes (edits, deletes, etc.)
	if event.BotID != "" || event.SubType != "" {
		return
	}

	// Check for @mention of the bot in a regular channel.
	// This handles cases where Slack sends a message event but no app_mention event.
	if event.ChannelType != "im" && h.botUserID != "" &&
		strings.Contains(event.Text, fmt.Sprintf("<@%s>", h.botUserID)) {
		// Dedup with app_mention handler
		dedupeKey := event.Channel + ":" + event.TimeStamp
		if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
			slog.Info("skipping duplicate message mention processing", "dedupe_key", dedupeKey)
			return
		}
		go func() {
			time.Sleep(60 * time.Second)
			h.processedMsgs.Delete(dedupeKey)
		}()

		text := strings.Replace(event.Text, fmt.Sprintf("<@%s>", h.botUserID), "", 1)
		text = strings.TrimSpace(text)

		// If this is a thread reply, fetch parent for context
		if event.ThreadTimeStamp != "" {
			if parentText := h.fetchThreadParentText(event.Channel, event.ThreadTimeStamp); parentText != "" {
				text = fmt.Sprintf("Context — original message in this thread:\n---\n%s\n---\n\nUser request: %s", parentText, text)
			}
		}

		h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, text, event.User)
		return
	}

	// Only process DMs (ChannelType == "im") for conversational AI
	if event.ChannelType != "im" {
		return
	}

	h.processMessage(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
}

// truncateForLog truncates a string to maxLen runes for log output.
func truncateForLog(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
