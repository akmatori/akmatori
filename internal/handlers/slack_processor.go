package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/output"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// finalizeSlackMessageBody compresses the agent's final response into a
// single Slack-sized message: the summarizer parses any structured blocks,
// formats them for Slack, runs the SummarizeForSlack flow when over budget,
// and the footer (metrics + UI link) is appended. When summarizer is nil
// (early startup), it falls back to the deterministic byte-truncation path.
func finalizeSlackMessageBody(ctx context.Context, summarizer *services.SlackSummarizer, response, incidentUUID string) string {
	contentOnly, footer := buildSlackFooter(response, incidentUUID)

	bodyBudget := slackMaxTextBytes - len(footer) - slackSummaryMargin
	if bodyBudget < 200 {
		bodyBudget = 200
	}

	if summarizer != nil {
		summary, err := summarizer.SummarizeForSlack(ctx, contentOnly, bodyBudget)
		if err == nil && summary != "" {
			return summary + footer
		}
	}
	formatted := output.FormatForSlack(output.Parse(contentOnly))
	return truncateWithFooter(formatted, footer, slackMaxTextBytes)
}

// processMessage is the core message processing logic
func (h *SlackHandler) processMessage(channel, threadTS, messageTS, text, user string) {
	// Check if Slack is still enabled before processing
	// This catches messages queued before Slack was disabled
	settings, err := database.GetSlackSettings()
	if err != nil || !settings.IsActive() {
		slog.Info("Slack is disabled, ignoring message", "channel", channel)
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

	// Check if this is an existing incident (continuation) by looking up in database.
	// First try by source="slack" (DM-originated incidents), then fall back to
	// slack_message_ts (alert channel incidents where users reply with @mention).
	var incident database.Incident
	if err := database.GetDB().Where("source = ? AND source_id = ?", "slack", threadID).First(&incident).Error; err == nil {
		// Existing DM incident found - resume session
		sessionID = incident.SessionID
		incidentUUID = incident.UUID
		// WorkingDir is stored in DB but session already knows its path from creation
		_ = incident.WorkingDir
		slog.Info("resuming session for thread", "session_id", sessionID, "thread_id", threadID, "incident_id", incidentUUID)
	} else if threadTS != "" {
		// Try to find an alert channel incident by slack_message_ts
		// (when user replies to an alert thread with @Akmatori)
		if err := database.GetDB().Where("slack_message_ts = ?", threadID).First(&incident).Error; err == nil {
			sessionID = incident.SessionID
			incidentUUID = incident.UUID
			// WorkingDir is stored in DB but session already knows its path from creation
			_ = incident.WorkingDir
			slog.Info("resuming alert channel session for thread", "session_id", sessionID, "thread_id", threadID, "incident_id", incidentUUID)
		}
	}

	if incidentUUID == "" {
		// New thread - spawn incident manager
		slog.Info("starting new session for thread", "thread_id", threadID)

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
			slog.Error("failed to spawn incident manager", "err", err)
			_, _, postErr := h.client.PostMessage(
				channel,
				slack.MsgOptionText(fmt.Sprintf("❌ Failed to spawn incident manager: %v", err), false),
				slack.MsgOptionTS(threadID),
			)
			if postErr != nil {
				slog.Error("failed to post error message to Slack", "err", postErr)
			}
			return
		}

		slog.Info("spawned incident manager", "incident_id", incidentUUID, "working_dir", workingDir)
	}

	// Update incident status to "running" before execution
	if incidentUUID != "" {
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			slog.Warn("failed to update incident status to running", "err", err)
		}
	}

	// Show "is investigating..." in the thread header and add a hourglass
	// reaction on the thread root for the duration of the agent run. The
	// controller owns both signals' lifecycle; defer Stop covers all exit
	// paths (success, error, supersede) since the handler blocks on <-done
	// before returning.
	typing := slackutil.NewTypingController(slackutil.TypingControllerConfig{
		Client:      h.client,
		ChannelID:   channel,
		ThreadTS:    threadID,
		ReactionRef: slack.ItemRef{Channel: channel, Timestamp: threadID},
	})
	typing.Start(context.Background())
	defer typing.Stop()

	// SlackProgressStreamer extracts the agent's latest reasoning (🤔)
	// line from output deltas and forwards it to the TypingController as
	// the assistant.threads.setStatus loading_messages content. Tool
	// start/end markers are filtered out so the rotating banner shows a
	// clean single-line current-thought indicator. There is no Slack
	// "Thinking..." placeholder message — the typing banner + reaction
	// are the activity signal; the final result is posted as a fresh
	// thread reply when the agent finishes.
	progressStreamer := NewSlackProgressStreamer(typing.UpdateLoadingMessage, slackAppendInterval)

	taskWithGuidance := executor.PrependGuidance(text)

	// Execute via WebSocket-based agent worker
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
		var finalSessionID string
		var hasError bool
		var superseded bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		// Build task header for logging
		taskHeader := fmt.Sprintf("📨 Slack Message from User <%s>:\n%s\n\n--- Execution Log ---\n\n", user, text)

		callback := IncidentCallback{
			OnOutput: func(outputLog string) {
				lastStreamedLog += outputLog
				// Update database with streamed log
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}

				// Stream condensed progress to Slack (delta only, not full log).
				progressStreamer.AppendStatus(outputLog)
			},
			OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
				finalSessionID = sid
				response = output
				finalTokensUsed = tokensUsed
				finalExecutionTimeMs = executionTimeMs
				closeOnce.Do(func() { close(done) })
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("❌ Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
			// A second Slack message in the same thread starts a new agent run
			// for the same incident_id. The replacement run owns finalization
			// (DB + Slack) — this displaced goroutine must unblock and exit
			// silently rather than overwrite the replacement's success with
			// a "superseded" failure.
			OnSuperseded: func() {
				superseded = true
				closeOnce.Do(func() { close(done) })
			},
		}

		// Always start a fresh agent session — resuming stale sessions causes
		// "timeout waiting for child process to exit" errors when the original
		// agent process is no longer running.
		slog.Info("starting new agent session for incident", "incident_id", incidentUUID)
		if wsErr := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback); wsErr != nil {
			slog.Error("failed to start/continue incident via WebSocket", "err", wsErr)
			startErr := fmt.Sprintf("❌ Agent worker error: %v", wsErr)
			h.finishSlackMessage(channel, threadID, incidentUUID, user, text,
				startErr, startErr, "", true, "", 0, 0)
			return
		}

		// Wait for completion
		<-done

		// Flush any buffered progress lines so the last status is not lost.
		progressStreamer.Flush()

		// If a newer message displaced this run, the replacement run will
		// finalize the incident in the DB and post its own final message.
		// Exit silently so we do not race the replacement with a failure
		// update.
		if superseded {
			slog.Info("slack run superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		// Use original sessionID if finalSessionID is empty (for resume cases)
		if finalSessionID == "" {
			finalSessionID = sessionID
		}

		// Build full log
		fullLog := taskHeader + lastStreamedLog
		if response != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + response
		}

		// Format response for Slack: parse structured blocks, summarize when
		// over budget, append the footer. Single message; no thread replies.
		// Note: finalResponse is the Slack-sized message; response is the raw
		// agent output that gets stored in the DB so the web UI shows the full
		// `[FINAL_RESULT]` content rather than the truncated Slack version.
		var finalResponse string
		if hasError {
			finalResponse = response
		} else if response != "" {
			finalResponse = finalizeSlackMessageBody(context.Background(), h.slackSummarizer, response, incidentUUID)
		} else {
			finalResponse = "✅ Task completed (no output)"
		}

		h.finishSlackMessage(channel, threadID, incidentUUID, user, text,
			finalResponse, response, fullLog, hasError, finalSessionID, finalTokensUsed, finalExecutionTimeMs)
		return
	}

	// No WebSocket worker available
	slog.Error("agent worker not connected", "incident_id", incidentUUID)
	errMsg := "❌ Agent worker not connected. Please check that the agent-worker container is running."
	h.finishSlackMessage(channel, threadID, incidentUUID, user, text,
		errMsg, errMsg, "", true, "", 0, 0)
}

// finishSlackMessage handles the final steps of Slack message processing.
// finalResponse is what gets posted to Slack; rawResponse is the full agent
// output stored in `incident.response` so the web UI shows the complete
// result, not the Slack-sized summarization.
func (h *SlackHandler) finishSlackMessage(channel, threadID, incidentUUID, user, text, finalResponse, rawResponse, fullLog string, hasError bool, sessionID string, tokensUsed int, executionTimeMs int64) {
	// The hourglass reaction is now removed by the TypingController in
	// processMessage's deferred Stop; here we only add the terminal
	// success/error reaction.

	// Add result reaction
	if hasError {
		if addErr := h.client.AddReaction("x", slack.ItemRef{
			Channel:   channel,
			Timestamp: threadID,
		}); addErr != nil {
			slog.Warn("failed to add error reaction", "err", addErr)
		}
	} else {
		if addErr := h.client.AddReaction("white_check_mark", slack.ItemRef{
			Channel:   channel,
			Timestamp: threadID,
		}); addErr != nil {
			slog.Warn("failed to add success reaction", "err", addErr)
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

		if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLogWithContext, rawResponse, tokensUsed, executionTimeMs); updateErr != nil {
			slog.Warn("failed to update incident", "err", updateErr)
		} else {
			slog.Info("updated incident", "incident_id", incidentUUID, "status", finalStatus, "session_id", sessionID)
		}
	}

	// Post the full final body as a fresh thread reply. chat.postMessage
	// allows up to ~40,000 chars so long summaries always reach the user.
	// Completion is signaled by the success/error reaction added above and
	// the typing banner clearing as the run's deferred Stop fires.
	if _, _, postErr := h.client.PostMessage(
		channel,
		slack.MsgOptionText(finalResponse, false),
		slack.MsgOptionTS(threadID),
	); postErr != nil {
		slog.Error("failed to post final summary thread reply", "err", postErr)
	}
}

// handleAlertChannelMessage processes a message from a configured alert channel
func (h *SlackHandler) handleAlertChannelMessage(event *slackevents.MessageEvent, instance *database.AlertSourceInstance) {
	slog.Info("processing alert channel message", "user", event.User, "channel", event.Channel)

	// Extract message text (including text from blocks and attachments)
	messageText := h.extractFullMessageText(event)
	messageText = utils.StripSlackMrkdwn(messageText)
	if messageText == "" {
		slog.Info("empty message text, skipping")
		return
	}

	// Determine the thread TS for incident storage and thread replies.
	// For thread replies, use the parent/root TS so that later @mentions in the
	// same thread can find and resume the incident. For top-level messages, the
	// message itself becomes the thread root.
	threadTS := event.TimeStamp
	if event.ThreadTimeStamp != "" {
		threadTS = event.ThreadTimeStamp
	}

	// The hourglass reaction is now placed by the TypingController in the
	// downstream Slack-channel-alert investigation flow (alert_processor.go's
	// runSlackChannelInvestigation). The brief alert-extraction phase below
	// has no dedicated indicator; the bot's "Thinking..." progress message
	// lands shortly after as activity feedback.

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
		slog.Warn("alert extraction failed, using fallback", "err", err)
		// Fallback alert is created by the extractor
	}

	// Set fingerprint and source fields (use message TS for uniqueness)
	normalized.SourceFingerprint = fmt.Sprintf("slack:%s:%s", event.Channel, event.TimeStamp)
	normalized.SourceAlertID = event.TimeStamp

	// Store Slack context for thread replies.
	// Use threadTS (root TS) so processMessage can find and resume this incident
	// when a human @mentions the bot in the same thread.
	if normalized.RawPayload == nil {
		normalized.RawPayload = make(map[string]interface{})
	}
	normalized.RawPayload["slack_channel_id"] = event.Channel
	normalized.RawPayload["slack_message_ts"] = threadTS
	normalized.RawPayload["slack_user"] = event.User

	// Process through AlertHandler if available
	if h.alertHandler != nil {
		h.alertHandler.ProcessAlertFromSlackChannel(instance, *normalized, event.Channel, threadTS)
	} else {
		slog.Error("AlertHandler not configured, cannot process Slack channel alert")
		// No hourglass to remove on this error path — the typing controller
		// in runSlackChannelInvestigation never started because we never got
		// there. Just surface the misconfiguration as a warning reaction.
		if err := h.client.AddReaction("warning", slack.ItemRef{
			Channel:   event.Channel,
			Timestamp: event.TimeStamp,
		}); err != nil {
			slog.Warn("failed to add warning reaction", "err", err)
		}
	}
}

// extractFullMessageText extracts the full text content from a Slack message event.
// The slackevents.MessageEvent struct doesn't expose Attachments or Blocks, but
// monitoring tools (Zabbix, Datadog, etc.) typically send content in attachments.
// When event.Text is empty, we fetch the full message via the Slack API to extract
// text from attachments and blocks.
func (h *SlackHandler) extractFullMessageText(event *slackevents.MessageEvent) string {
	// Always try to fetch the full message from the Slack API. The Events API
	// MessageEvent only contains event.Text (a plain-text summary) and does NOT
	// include Blocks or Attachments. Bots and integrations (Lark, Zabbix, Datadog,
	// etc.) often put the real alert content in blocks/attachments while event.Text
	// is just a short preview like "New notification from …".
	fullText := h.fetchFullMessageText(event)
	if fullText != "" {
		return fullText
	}

	// Fallback to event.Text when the API fetch fails or returns nothing
	if event.Text != "" {
		slog.Info("using event.Text fallback", "channel", event.Channel, "ts", event.TimeStamp)
		return event.Text
	}

	return ""
}

// fetchFullMessageText retrieves the full message (with blocks and attachments)
// from the Slack API and extracts all readable text.
func (h *SlackHandler) fetchFullMessageText(event *slackevents.MessageEvent) string {
	if event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.TimeStamp {
		// Thread reply: use GetConversationReplies
		msgs, _, _, err := h.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: event.Channel,
			Timestamp: event.ThreadTimeStamp,
			Latest:    event.TimeStamp,
			Oldest:    event.TimeStamp,
			Limit:     1,
			Inclusive: true,
		})
		if err != nil {
			slog.Error("failed to fetch full message via replies API", "channel", event.Channel, "ts", event.TimeStamp, "err", err)
			return ""
		}
		// Find the specific message by timestamp
		for _, msg := range msgs {
			if msg.Timestamp == event.TimeStamp {
				return extractSlackMessageText(msg)
			}
		}
		if len(msgs) > 0 {
			return extractSlackMessageText(msgs[len(msgs)-1])
		}
		return ""
	}

	// Top-level message: use GetConversationHistory
	params := &slack.GetConversationHistoryParameters{
		ChannelID: event.Channel,
		Latest:    event.TimeStamp,
		Oldest:    event.TimeStamp,
		Limit:     1,
		Inclusive: true,
	}
	history, err := h.client.GetConversationHistory(params)
	if err != nil {
		slog.Error("failed to fetch full message via history API", "channel", event.Channel, "ts", event.TimeStamp, "err", err)
		return ""
	}
	if len(history.Messages) == 0 {
		return ""
	}
	return extractSlackMessageText(history.Messages[0])
}
