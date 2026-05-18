package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
)

// resolveOutboundSlackChannel picks the outbound destination for an alert.
//
// The new path consults ChannelService.ResolveForAlertSource first, returning
// a Channel row whose Integration is preloaded so the caller can route
// through ProviderRegistry. When no Channel row exists (no Channel rows
// configured, or the per-provider default is missing), it falls back to the
// legacy SlackSettings.AlertsChannel and synthesizes a transient Channel so
// the rest of the post path can stay uniform.
//
// Returns (channel, channelID, isLegacy). channelID is the resolved Slack
// channel ID (post-name→ID resolution); both return values are empty when
// no destination can be determined. isLegacy is true when the synthesized
// channel came from SlackSettings.AlertsChannel — callers log a one-time
// deprecation warning the first time this happens.
func (h *AlertHandler) resolveOutboundSlackChannel(asi *database.AlertSourceInstance) (*database.Channel, string, bool) {
	// New path: Channel/Integration table.
	if h.channelService != nil {
		ch, err := h.channelService.ResolveForAlertSource(asi, database.MessagingProviderSlack)
		if err == nil && ch != nil {
			return ch, h.resolveSlackExternalID(ch.ExternalID), false
		}
		if err != nil && !errors.Is(err, services.ErrChannelNotFound) {
			slog.Warn("resolve channel for alert source failed", "err", err)
		}
	}

	// Legacy fallback: SlackSettings.AlertsChannel.
	settings, sErr := database.GetSlackSettings()
	if sErr != nil || settings == nil || settings.AlertsChannel == "" {
		return nil, "", false
	}
	h.legacyFallbackWarnOnce.Do(func() {
		slog.Warn("no Channel rows configured; using legacy SlackSettings.AlertsChannel — migrate to /api/channels (SlackSettings.AlertsChannel will be removed in a future release)")
	})
	synth := &database.Channel{
		ExternalID:  settings.AlertsChannel,
		DisplayName: settings.AlertsChannel,
		Integration: database.Integration{Provider: database.MessagingProviderSlack},
	}
	return synth, h.resolveSlackExternalID(settings.AlertsChannel), true
}

// resolveSlackExternalID converts a Channel.ExternalID (which may be a Slack
// channel ID like C012345 or a human name like #alerts) into a concrete
// channel ID using the cached resolver. Falls back to the input value when
// the resolver is missing or errors out so the post still has a target to
// try; downstream Slack errors will be logged on failure.
func (h *AlertHandler) resolveSlackExternalID(externalID string) string {
	if externalID == "" {
		return ""
	}
	if h.channelResolver == nil {
		return externalID
	}
	resolved, err := h.channelResolver.ResolveChannel(externalID)
	if err != nil {
		slog.Warn("failed to resolve slack channel", "external_id", externalID, "err", err)
		return externalID
	}
	return resolved
}

func (h *AlertHandler) postAlertToSlack(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) (string, string, error) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return "", "", nil
	}

	channel, channelID, _ := h.resolveOutboundSlackChannel(instance)
	if channelID == "" {
		return "", "", nil
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

	// Post message via the messaging provider when available; fall back to
	// the slack client directly when no provider is registered for this
	// channel's provider name (keeps tests + legacy boot paths working).
	ts, err := h.postViaProvider(context.Background(), channel, channelID, message)
	if err != nil {
		return "", "", err
	}
	if ts == "" {
		_, t, err := slackClient.PostMessage(channelID, slack.MsgOptionText(message, false))
		if err != nil {
			return "", "", err
		}
		ts = t
	}

	// Add reaction
	if err := slackClient.AddReaction("rotating_light", slack.ItemRef{
		Channel:   channelID,
		Timestamp: ts,
	}); err != nil {
		slog.Warn("failed to add reaction", "err", err)
	}

	return channelID, ts, nil
}

// postViaProvider posts text to the destination using the registered messaging
// provider when one is available. Returns "" without error when no provider is
// registered for the channel's provider — callers then fall back to direct
// slack client posting (the legacy code path) so we degrade gracefully when
// the registry has not been wired yet.
func (h *AlertHandler) postViaProvider(ctx context.Context, channel *database.Channel, resolvedChannelID, text string) (string, error) {
	if h.providerRegistry == nil || channel == nil {
		return "", nil
	}
	provider, err := h.providerRegistry.Get(channel.Integration.Provider)
	if err != nil {
		// Unknown provider → silently fall back so legacy/test paths keep working.
		return "", nil
	}
	// The provider expects channel.ExternalID to address the destination
	// directly; substitute the resolved Slack channel ID before delegating
	// so name→ID resolution stays in one place.
	out := *channel
	out.ExternalID = resolvedChannelID
	posted, err := provider.PostMessage(ctx, &out, text)
	if err != nil {
		return "", err
	}
	if posted == nil {
		return "", nil
	}
	return posted.MessageID, nil
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

// updateSlackChannelReactions updates reactions on the original Slack message
func (h *AlertHandler) updateSlackChannelReactions(channelID, messageTS string, hasError bool) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
	}

	// The hourglass reaction is now removed by the TypingController in
	// runSlackChannelInvestigation's deferred Stop.

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

// updateSlackWithResult posts results to Slack thread. channelID is the
// resolved Slack channel for the alert's destination — typically the same
// channel that postAlertToSlack posted to. Empty channelID is treated as a
// no-op so we don't surface a stray reaction on the wrong thread.
func (h *AlertHandler) updateSlackWithResult(channelID, threadTS, response string, hasError bool) {
	if threadTS == "" || channelID == "" {
		return
	}

	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return
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

// truncateForSlack truncates a message to fit within Slack's text limit.
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
