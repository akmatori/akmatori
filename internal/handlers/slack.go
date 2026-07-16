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
	client            *slack.Client
	agentExecutor     *executor.Executor
	agentWSHandler    *AgentWSHandler
	skillService      services.SkillIncidentManager
	slackSummarizer   *services.SlackSummarizer
	responseFormatter *services.ResponseFormatter

	// Cross-incident memory + LLM-classified Slack feedback (Task 7).
	// Both are optional — when either is unset, thread-reply feedback
	// classification is silently skipped.
	memoryManager      services.MemoryManager
	feedbackClassifier *services.FeedbackClassifier

	// Listener channel support. Keyed by the provider-side channel ID
	// (Slack channel ID today). Populated from the channels table where
	// can_listen=true; the legacy slack_channel AlertSourceInstance path is
	// deprecated and the in-memory map is sourced exclusively from Channel
	// rows after Task 6 of the unified-channels plan.
	alertChannels   map[string]*database.Channel
	alertChannelsMu sync.RWMutex
	alertExtractor  *extraction.AlertExtractor
	alertHandler    *AlertHandler
	alertService    services.AlertManager
	channelService  services.ChannelManager
	botUserID       string // Bot's user ID for self-message filtering
	teamID          string // Workspace team ID (required for Streaming API)

	// Dedup: prevent double processing when both app_mention and message events fire
	processedMsgs sync.Map // key: "channel:messageTS" -> struct{}

	// runMentionContinuation is the agent-side fall-through for the classify-
	// first router on @mention thread replies. Defaults to
	// handleBotMentionInThread; tests override it to assert routing without
	// spinning up the real agent path.
	runMentionContinuation func(channel, threadTS, messageTS, text, user string)

	// feedbackAcker performs the Slack-side acknowledgment (reaction + text)
	// after a confident feedback verdict. Wired to a default adapter only when
	// client != nil (mirrors graceful degradation); tests override it to assert
	// ack call counts without a live client.
	feedbackAcker feedbackAcker
}

// NewSlackHandler creates a new Slack handler. The supplied caller is forwarded
// to the alert extractor so it can route extraction calls through the agent
// worker's provider-agnostic one-shot LLM path.
func NewSlackHandler(
	client *slack.Client,
	agentExecutor *executor.Executor,
	agentWSHandler *AgentWSHandler,
	skillService services.SkillIncidentManager,
	oneShotCaller services.OneShotLLMCaller,
) *SlackHandler {
	h := &SlackHandler{
		client:         client,
		agentExecutor:  agentExecutor,
		agentWSHandler: agentWSHandler,
		skillService:   skillService,
		alertChannels:  make(map[string]*database.Channel),
		alertExtractor: extraction.NewAlertExtractor(oneShotCaller),
	}
	h.runMentionContinuation = h.handleBotMentionInThread
	// Wire the default ack adapter only when a real client is present, so a
	// client-less handler degrades to persist-only (no panic on nil client).
	if client != nil {
		h.feedbackAcker = slackFeedbackAcker{client: client}
	}
	return h
}

// SetAlertHandler sets the alert handler for processing Slack channel alerts
func (h *SlackHandler) SetAlertHandler(alertHandler *AlertHandler) {
	h.alertHandler = alertHandler
}

// SetSlackSummarizer wires the SlackSummarizer used for compressing final
// Slack messages. Optional — when unset, the handler falls back to the
// existing byte-truncation path.
func (h *SlackHandler) SetSlackSummarizer(s *services.SlackSummarizer) {
	h.slackSummarizer = s
}

// SetResponseFormatter wires the ResponseFormatter used to apply the
// configured global formatting prompt to the agent's final response.
// Optional — when unset (or when formatting is disabled in settings), the
// raw agent response flows through unchanged.
func (h *SlackHandler) SetResponseFormatter(f *services.ResponseFormatter) {
	h.responseFormatter = f
}

// SetAlertService sets the alert service. Retained for backward compatibility
// with code that constructs the handler with an alert-service dependency, but
// no longer consulted by the listener loader after Task 6 of the
// unified-channels plan (Channels table is the source of truth).
func (h *SlackHandler) SetAlertService(alertService services.AlertManager) {
	h.alertService = alertService
}

// SetChannelService wires the ChannelManager used by LoadListenerChannels to
// source listener channels from the channels table. Optional — when unset,
// loading is a no-op and the handler degrades to a Slack handler that only
// processes DMs (matching the prior alert-service-not-configured behavior).
func (h *SlackHandler) SetChannelService(c services.ChannelManager) {
	h.channelService = c
}

// SetMemoryManager wires the cross-incident memory manager. When set together
// with a FeedbackClassifier, the handler routes non-mention thread replies on
// incident threads through the classifier and persists confident feedback as
// a global-scope memory.
func (h *SlackHandler) SetMemoryManager(m services.MemoryManager) {
	h.memoryManager = m
}

// SetFeedbackClassifier wires the LLM-backed thread-reply classifier.
// Optional — when unset, the existing "ignore non-mention thread replies"
// behavior is preserved exactly.
func (h *SlackHandler) SetFeedbackClassifier(c *services.FeedbackClassifier) {
	h.feedbackClassifier = c
}

// SetBotUserID sets the bot's user ID for self-message filtering
func (h *SlackHandler) SetBotUserID(botUserID string) {
	h.botUserID = botUserID
}

// SetTeamID sets the workspace team ID (used by the Streaming API).
func (h *SlackHandler) SetTeamID(teamID string) {
	h.teamID = teamID
}

// LoadListenerChannels loads listener channel configurations from the channels
// table. A channel is considered a listener when can_listen=true and both the
// channel and its parent integration are enabled. The map is keyed by the
// provider-side external channel id (Slack channel id today).
//
// When the ChannelManager is not wired the loader silently returns, matching
// the pre-Task-6 behavior where missing services skipped listener setup.
func (h *SlackHandler) LoadListenerChannels() error {
	if h.channelService == nil {
		slog.Info("channel service not configured, skipping listener channel loading")
		return nil
	}

	canListen := true
	channels, err := h.channelService.ListChannels(services.ListChannelsFilter{CanListen: &canListen})
	if err != nil {
		return fmt.Errorf("failed to list listener channels: %w", err)
	}

	h.alertChannelsMu.Lock()
	defer h.alertChannelsMu.Unlock()

	// Replace the map rather than mutate so a torn read sees the prior
	// snapshot, not a half-rebuilt one.
	next := make(map[string]*database.Channel, len(channels))
	for i := range channels {
		ch := &channels[i]
		if !ch.Enabled || !ch.Integration.Enabled {
			continue
		}
		if ch.ExternalID == "" {
			slog.Warn("listener channel missing external_id, skipping", "uuid", ch.UUID, "display_name", ch.DisplayName)
			continue
		}
		next[ch.ExternalID] = ch
		slog.Info("loaded listener channel", "channel", ch.ExternalID, "display_name", ch.DisplayName, "provider", ch.Integration.Provider)
	}
	h.alertChannels = next

	slog.Info("loaded listener channels", "count", len(h.alertChannels))
	return nil
}

// ReloadListenerChannels reloads listener channel configurations after a
// Channels CRUD change (called from the API handler's reload hook).
func (h *SlackHandler) ReloadListenerChannels() {
	if err := h.LoadListenerChannels(); err != nil {
		slog.Warn("failed to reload listener channels", "err", err)
	}
}

// isAlertChannel reports whether the given Slack channel id is configured as
// a listener channel and returns the resolved Channel row when so. The legacy
// name is preserved because it reads naturally at call sites
// ("if isAlertChannel..."); the meaning is "the channel is a listener".
func (h *SlackHandler) isAlertChannel(channelID string) (*database.Channel, bool) {
	h.alertChannelsMu.RLock()
	defer h.alertChannelsMu.RUnlock()

	ch, ok := h.alertChannels[channelID]
	return ch, ok
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
	// Alert-channel thread @mention from a human: delegate to the classify-
	// first router so the FeedbackClassifier runs regardless of which Slack
	// event (app_mention vs message) reaches the dispatcher first. Both
	// events fire and race on the same dedup key; without this branch the
	// classifier is silently bypassed whenever app_mention wins.
	if _, isAlert := h.isAlertChannel(event.Channel); isAlert &&
		event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.TimeStamp &&
		event.User != "" && event.User != h.botUserID {
		h.routeBotMentionThreadReply(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
		return
	}

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

// routeBotMentionThreadReply is the classify-first entry point for an @mention
// thread reply in an incident thread. Confident operator feedback short-circuits
// to persist + 👍 + ack; every other branch (low confidence, classifier error,
// worker offline, no incident match, empty text, classifier/memory manager
// unavailable) falls through to the existing agent continuation path via
// runMentionContinuation.
//
// Dedup uses the same key shape as handleAppMention / handleBotMentionInThread
// so duplicate MessageEvent / AppMentionEvent pairs are deduped at the entry.
// The classifier round-trip runs in a goroutine because Slack's Socket Mode
// handler thread must not block on an LLM call.
func (h *SlackHandler) routeBotMentionThreadReply(channel, threadTS, messageTS, text, user string) {
	dedupeKey := channel + ":" + messageTS
	if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
		slog.Info("skipping duplicate routed mention processing", "dedupe_key", dedupeKey)
		return
	}
	time.AfterFunc(60*time.Second, func() {
		h.processedMsgs.Delete(dedupeKey)
	})

	go func() {
		verdict, incident, err := h.classifyThreadReplyForFeedback(threadTS, text)
		if err == nil && incident != nil && verdict.IsConfidentFeedback() {
			// Mention path keeps today's behaviour: persist + emoji + short text
			// ack (Akmatori posts text in a thread only when @mentioned).
			if mem := h.persistFeedback(threadTS, text, verdict, incident); mem != nil {
				h.reactFeedback(channel, messageTS)
				h.postFeedbackTextAck(channel, threadTS, mem.Name)
			}
			return
		}
		if h.runMentionContinuation != nil {
			h.runMentionContinuation(channel, threadTS, messageTS, text, user)
		}
	}()
}

// handleBotMentionInThread processes a human @mention of the bot in an alert channel thread.
// It strips the mention, fetches the parent message for context, and processes via processMessage.
// Dedup is owned by the sole caller (routeBotMentionThreadReply) — adding it
// here would short-circuit the router's fall-through, since the router claims
// the dedup key BEFORE invoking this function.
func (h *SlackHandler) handleBotMentionInThread(channel, threadTS, messageTS, rawText, user string) {
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
	if listenerChannel, ok := h.isAlertChannel(event.Channel); ok {
		slog.Info("alert channel message received",
			"channel", event.Channel,
			"user", event.User,
			"bot_id", event.BotID,
			"sub_type", event.SubType,
			"ts", event.TimeStamp,
			"thread_ts", event.ThreadTimeStamp,
			"text_preview", truncateForLog(event.Text, 100),
		)
		// Skip message_changed events — they carry the edit notification TS
		// (not the original message TS) so Slack API lookups for the full
		// message text fail. PagerDuty triggers these when updating message
		// formatting; the actual alert content arrives via regular message events.
		if event.SubType == "message_changed" {
			slog.Debug("skipping message_changed in alert channel",
				"channel", event.Channel,
				"ts", event.TimeStamp,
			)
			return
		}

		// Detect bot/integration messages (Zabbix, Alertmanager, etc.)
		// Some integrations set BotID without bot_message subtype,
		// others use the bot_message subtype. Accept both.
		isBotMessage := event.SubType == "bot_message" || event.BotID != ""

		isThreadReply := event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.TimeStamp

		if isThreadReply {
			// Thread reply in alert channel (thread_ts != ts).
			// When thread_ts == ts the message is a thread root, not a reply
			// (PagerDuty sets thread_ts on the initial message itself).
			// Only respond when the bot is explicitly @mentioned — ignore all
			// other thread replies (bot status updates, escalations, human chat).
			if h.botUserID != "" && event.SubType == "" && event.User != "" &&
				strings.Contains(event.Text, fmt.Sprintf("<@%s>", h.botUserID)) {
				h.routeBotMentionThreadReply(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
				return
			}
			// Non-mention thread reply on an incident thread: route to the
			// LLM-backed feedback classifier when configured. Skips bot/subtype
			// noise (those wouldn't survive the alert-channel filter above) and
			// silently no-ops when the classifier or memory manager is offline.
			if event.SubType == "" && event.User != "" && event.User != h.botUserID {
				go h.maybeCaptureSlackFeedback(event.Channel, event.ThreadTimeStamp, event.TimeStamp, event.Text, event.User)
			}
			slog.Info("ignoring thread reply in alert channel (no bot mention)",
				"channel", event.Channel,
				"thread_ts", event.ThreadTimeStamp,
				"bot_id", event.BotID,
				"text_preview", truncateForLog(event.Text, 100),
			)
			return
		}

		// Top-level message: check for human @mention of the bot.
		if !isBotMessage {
			// Check if this channel processes human messages as alerts
			if listenerChannel.ProcessHumanMessages {
				go h.handleAlertChannelMessage(event, listenerChannel)
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

		// Top-level bot message — process as alert unless the channel opted
		// out of bot messages (humans-only listeners).
		if listenerChannel.ProcessBotMessages {
			go h.handleAlertChannelMessage(event, listenerChannel)
		} else {
			slog.Info("ignoring bot message in listener channel (process_bot_messages=false)",
				"channel", event.Channel,
				"bot_id", event.BotID,
				"text_preview", truncateForLog(event.Text, 100),
			)
		}
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
