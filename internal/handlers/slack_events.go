package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
)

// mentionEventTitleMax caps the title of a mention event built from raw text.
const mentionEventTitleMax = 120

// mentionSourceUUID returns the event/alert source key of a Slack channel: the
// Channel row UUID when the channel is configured, else "slack:<channel id>"
// (DMs and channels Akmatori was only invited to).
func (h *SlackHandler) mentionSourceUUID(channelID string) string {
	if u := h.channelUUIDForExternalID(channelID); u != "" {
		return u
	}
	return "slack:" + channelID
}

// recordMentionFollowUp stores a mention that continues an existing thread.
// Its class comes from the source: it is a request on that thread's run.
func (h *SlackHandler) recordMentionFollowUp(channelID, messageTS, text, incidentUUID string) {
	if h.eventService == nil {
		return
	}
	h.eventService.Record(context.Background(), services.EventInput{
		SourceKind:   database.EventSourceSlackMention,
		SourceUUID:   h.mentionSourceUUID(channelID),
		SourceType:   string(database.MessagingProviderSlack),
		ExternalID:   messageTS,
		Title:        firstLineTruncated(text, mentionEventTitleMax),
		Summary:      text,
		IncidentUUID: incidentUUID,
	}, services.SourceEventVerdict(database.EventKindRequest))
}

// classifyNewMention stores a mention that starts a new thread and classifies
// it in one LLM call (extraction + class). handled is true when this function
// finished the mention:
//   - alert: a report of a problem goes to the correlation gate
//   - change / info: stored, with a short reply in the thread
//
// A request returns handled=false: the caller starts the agent run as before
// and links eventUUID. Every failure is a request, the behaviour from before
// events.
func (h *SlackHandler) classifyNewMention(channelID, threadID, messageTS, text string) (eventUUID string, handled bool) {
	if h.eventService == nil {
		return "", false
	}
	ctx := context.Background()
	sourceUUID := h.mentionSourceUUID(channelID)

	// Gate off: every mention is a request, with no extraction call.
	if !services.LoadEventClassificationConfig().Enabled {
		ev := h.eventService.Record(ctx, services.EventInput{
			SourceKind: database.EventSourceSlackMention,
			SourceUUID: sourceUUID,
			SourceType: string(database.MessagingProviderSlack),
			ExternalID: messageTS,
			Title:      firstLineTruncated(text, mentionEventTitleMax),
			Summary:    text,
		}, services.ClassificationOffVerdict(database.EventKindRequest))
		return ev.UUID, false
	}

	allowed := []string{database.EventKindRequest, database.EventKindChange, database.EventKindInfo}
	if h.alertHandler != nil {
		allowed = append(allowed, database.EventKindAlert)
	}
	normalized, verdict := h.alertExtractor.ExtractAndClassify(ctx, text, "", allowed, database.EventKindRequest)

	ev := h.eventService.Record(ctx, services.EventInput{
		SourceKind:  database.EventSourceSlackMention,
		SourceUUID:  sourceUUID,
		SourceType:  string(database.MessagingProviderSlack),
		ExternalID:  messageTS,
		Title:       normalized.AlertName,
		Summary:     text,
		Description: normalized.Description,
		Host:        normalized.TargetHost,
		Service:     normalized.TargetService,
		Severity:    string(normalized.Severity),
		Labels:      normalized.TargetLabels,
	}, verdict)
	switch ev.Kind {
	case database.EventKindRequest:
		return ev.UUID, false
	case database.EventKindChange, database.EventKindInfo:
		h.postMentionAck(channelID, threadID, ev.Kind)
		return ev.UUID, true
	}

	normalized.SourceFingerprint = fmt.Sprintf("slack:%s:%s", channelID, messageTS)
	normalized.SourceAlertID = messageTS
	normalized.EventUUID = ev.UUID
	if normalized.RawPayload == nil {
		normalized.RawPayload = map[string]interface{}{}
	}
	normalized.RawPayload["slack_channel_id"] = channelID
	normalized.RawPayload["slack_message_ts"] = threadID

	incidentUUID, linked, err := h.alertHandler.ProcessReportedAlert(ReportedAlert{
		SourceUUID:     sourceUUID,
		SourceLabel:    database.IncidentSourceKindSlackMention,
		SourceDisplay:  "Slack-reported",
		SourceName:     h.channelResolver.ResolveLabel(channelID),
		Alert:          *normalized,
		SlackChannelID: channelID,
		SlackThreadTS:  threadID,
	})
	if err != nil {
		slog.Error("reported alert from Slack mention failed", "channel", channelID, "err", err)
		if _, _, postErr := h.client.PostMessage(channelID,
			slack.MsgOptionText(fmt.Sprintf("❌ Failed to open an incident for this report: %v", err), false),
			slack.MsgOptionTS(threadID)); postErr != nil {
			slog.Error("failed to post error message to Slack", "err", postErr)
		}
		return ev.UUID, true
	}
	slog.Info("Slack mention reported an alert", "event_uuid", ev.UUID, "incident_uuid", incidentUUID, "linked", linked)
	return ev.UUID, true
}

// mentionAckText is the thread reply for a mention that is stored only. It
// says what happened and how to get an answer, so the person is not left
// waiting for a run that will not come.
func mentionAckText(kind string) string {
	if kind == database.EventKindChange {
		return "📝 Recorded as a change event. I did not start an investigation. Ask me a question if you want me to look at something."
	}
	return "📝 Noted as an info event. I did not start an investigation. Ask me a question if you want me to look at something."
}

// postMentionAck posts mentionAckText in the mention's thread. Best-effort.
func (h *SlackHandler) postMentionAck(channelID, threadID, kind string) {
	if h.client == nil {
		return
	}
	if _, _, err := h.client.PostMessage(channelID,
		slack.MsgOptionText(mentionAckText(kind), false),
		slack.MsgOptionTS(threadID)); err != nil {
		slog.Warn("failed to post mention ack", "channel", channelID, "err", err)
	}
}

// firstLineTruncated returns the first non-empty line of s, cut to max runes.
func firstLineTruncated(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			r := []rune(line)
			if len(r) > max {
				return string(r[:max-3]) + "..."
			}
			return line
		}
	}
	return ""
}
