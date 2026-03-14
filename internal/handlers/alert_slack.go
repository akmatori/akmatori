package handlers

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/slack-go/slack"
)

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
	if err := slackClient.AddReaction("rotating_light", slack.ItemRef{
		Channel:   channelID,
		Timestamp: ts,
	}); err != nil {
		slog.Warn("failed to add reaction", "err", err)
	}

	return ts, nil
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
		slog.Warn("error posting thread reply", "err", err)
	}
}

// postSlackThreadReplyGetTS posts a thread reply and returns the message timestamp
func (h *AlertHandler) postSlackThreadReplyGetTS(channelID, threadTS, message string) string {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return ""
	}

	_, ts, err := slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(message, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		slog.Warn("error posting thread reply", "err", err)
		return ""
	}
	return ts
}

// updateSlackThreadMessage updates an existing Slack message in a channel
func (h *AlertHandler) updateSlackThreadMessage(channelID, messageTS, message string) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	_, _, _, err := slackClient.UpdateMessage(
		channelID,
		messageTS,
		slack.MsgOptionText(message, false),
	)
	if err != nil {
		slog.Warn("error updating Slack message", "ts", messageTS, "err", err)
	}
}

// updateSlackChannelReactions updates reactions on the original Slack message
func (h *AlertHandler) updateSlackChannelReactions(channelID, messageTS string, hasError bool) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	// Remove hourglass reaction
	if err := slackClient.RemoveReaction("hourglass_flowing_sand", slack.ItemRef{
		Channel:   channelID,
		Timestamp: messageTS,
	}); err != nil {
		slog.Warn("failed to remove hourglass reaction", "err", err)
	}

	// Add result reaction
	reactionName := "white_check_mark"
	if hasError {
		reactionName = "x"
	}
	if err := slackClient.AddReaction(reactionName, slack.ItemRef{
		Channel:   channelID,
		Timestamp: messageTS,
	}); err != nil {
		slog.Warn("failed to add result reaction", "err", err)
	}
}

// buildSlackResponse prepends the last N lines of the reasoning/execution log
// to the final response so Slack readers can see investigation context.
func buildSlackResponse(reasoningLog, response string) string {
	if reasoningLog == "" {
		return response
	}
	context := utils.GetLastNLines(strings.TrimSpace(reasoningLog), 15)
	if context == "" {
		return response
	}
	return context + "\n\n---\n\n" + response
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
	if err := slackClient.AddReaction(reactionName, slack.ItemRef{
		Channel:   channelID,
		Timestamp: threadTS,
	}); err != nil {
		slog.Warn("failed to add reaction", "err", err)
	}

	// Post result summary
	if _, _, err := slackClient.PostMessage(
		channelID,
		slack.MsgOptionText(response, false),
		slack.MsgOptionTS(threadTS),
	); err != nil {
		slog.Error("failed to post message", "err", err)
	}
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
	baseURL := h.getBaseURL()
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

// truncateLogForSlack truncates a log string to fit within Slack's message limits.
// It keeps the last maxLen bytes and trims to a clean line boundary.
// Uses byte length (not rune count) because Slack enforces byte-based limits.
func truncateLogForSlack(logText string, maxLen int) string {
	if len(logText) <= maxLen {
		return logText
	}
	truncated := logText[len(logText)-maxLen:]
	// Find first newline to avoid partial lines
	if idx := strings.Index(truncated, "\n"); idx > 0 && idx < 100 {
		truncated = truncated[idx+1:]
	}
	return "...(truncated)\n" + truncated
}

// buildSlackFooter extracts the metrics line from a response and builds a footer
// with metrics + a UI link. Returns the response without metrics and the footer string.
func buildSlackFooter(response, incidentUUID string) (responseWithoutMetrics, footer string) {
	metricsLine := ""
	if idx := strings.LastIndex(response, "\n---\n⏱️"); idx >= 0 {
		metricsLine = strings.TrimSpace(response[idx+len("\n---\n"):])
		responseWithoutMetrics = response[:idx]
	} else {
		responseWithoutMetrics = response
	}

	baseURL := resolveBaseURL()

	var sb strings.Builder
	sb.WriteString("\n\n———\n")
	if metricsLine != "" {
		sb.WriteString(metricsLine)
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("<%s/incidents/%s|View reasoning log>", baseURL, incidentUUID))
	footer = sb.String()
	return
}

// truncateWithFooter truncates content to fit within maxBytes including a guaranteed footer.
func truncateWithFooter(content, footer string, maxBytes int) string {
	if len(content)+len(footer) <= maxBytes {
		return content + footer
	}
	contentLimit := maxBytes - len(footer)
	if contentLimit < 100 {
		contentLimit = 100
	}
	content = truncateForSlack(content, contentLimit)
	return content + footer
}

// truncateForSlack truncates a message to fit within Slack's 4000-byte text limit.
// Reserves space for a truncation notice.
func truncateForSlack(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	const suffix = "\n\n_...truncated. See full response in the UI._"
	cutoff := maxBytes - len(suffix)
	if cutoff < 100 {
		cutoff = 100
	}
	// Avoid cutting in the middle of a UTF-8 character
	truncated := message[:cutoff]
	// Find last newline for a cleaner break
	if idx := strings.LastIndex(truncated, "\n"); idx > cutoff/2 {
		truncated = truncated[:idx]
	}
	return truncated + suffix
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

// resolveBaseURL returns the base URL for incident links (package-level helper).
// Priority: DB GeneralSettings > AKMATORI_BASE_URL env var > fallback.
func resolveBaseURL() string {
	if settings, err := database.GetOrCreateGeneralSettings(); err == nil && settings.BaseURL != "" {
		return strings.TrimRight(settings.BaseURL, "/")
	}
	if envURL := os.Getenv("AKMATORI_BASE_URL"); envURL != "" {
		return envURL
	}
	return "http://localhost:3000"
}

// getBaseURL returns the base URL for incident links.
func (h *AlertHandler) getBaseURL() string {
	return resolveBaseURL()
}
