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
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
)

// resolveOutboundSlackChannel picks the outbound destination for an alert.
//
// Consults ChannelService.ResolveForAlertSource and returns a Channel row
// whose Integration is preloaded so the caller can route through
// ProviderRegistry. Returns (nil, "") when no Channel destination can be
// resolved — callers then skip Slack posting.
func (h *AlertHandler) resolveOutboundSlackChannel(asi *database.AlertSourceInstance) (*database.Channel, string) {
	if h.channelService == nil {
		return nil, ""
	}
	ch, err := h.channelService.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil {
		if !errors.Is(err, services.ErrChannelNotFound) {
			slog.Warn("resolve channel for alert source failed", "err", err)
		}
		return nil, ""
	}
	if ch == nil {
		return nil, ""
	}
	// ResolveForAlertSource honours an explicit AlertSourceInstance.NotificationChannelID
	// without filtering by provider, so the resolved row could belong to a
	// non-slack integration (e.g. Telegram). Posting it through the Slack
	// client would silently misroute the alert. Fall through to the Slack
	// per-provider default so the alert still surfaces somewhere rather than
	// being silently dropped.
	if ch.Integration.Provider != database.MessagingProviderSlack {
		slog.Warn("alert source points at a non-slack channel; falling back to default Slack channel",
			"channel_uuid", ch.UUID,
			"provider", ch.Integration.Provider,
		)
		fallback, ferr := h.channelService.ResolveDefault(database.MessagingProviderSlack)
		if ferr != nil {
			if !errors.Is(ferr, services.ErrChannelNotFound) {
				slog.Warn("resolve default slack channel failed", "err", ferr)
			}
			return nil, ""
		}
		ch = fallback
	}
	return ch, h.resolveSlackExternalID(ch.ExternalID)
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

// postAlertToSlack posts the initial alert banner and returns the Slack
// channel ID, the message timestamp, and the resolved Channel row UUID (used
// for formatting-rule matching; "" when posting was skipped).
func (h *AlertHandler) postAlertToSlack(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) (string, string, string, error) {
	slackClient := h.slackManager.GetClient()
	if slackClient == nil {
		return "", "", "", nil
	}

	channel, channelID := h.resolveOutboundSlackChannel(instance)
	if channelID == "" {
		return "", "", "", nil
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
		return "", "", "", err
	}
	if ts == "" {
		_, t, err := slackClient.PostMessage(channelID, slack.MsgOptionText(message, false))
		if err != nil {
			return "", "", "", err
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

	return channelID, ts, channel.UUID, nil
}

// postAlertToChannel resolves the outbound destination for an alert and posts
// via the appropriate provider. Resolution order:
//  1. Explicit notification channel on the alert source (any provider)
//  2. Default Telegram channel
//  3. Default Slack channel
//
// Returns the resolved Channel, external ID, message ID, and provider for
// later result posting. Returns (nil, "", "", "", nil) when no provider
// is available — callers skip posting.
func (h *AlertHandler) postAlertToChannel(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) (*database.Channel, string, string, database.MessagingProvider, error) {
	if h.channelService == nil || h.providerRegistry == nil {
		return nil, "", "", "", nil
	}

	// Build provider-agnostic alert message.
	message := messaging.FormatAlertMessage(alert, instance.AlertSourceType.DisplayName, instance.Name)
	if alert.RunbookURL != "" {
		message += fmt.Sprintf("\n📖 *Runbook:* [Runbook](%s)", messaging.EscapeMarkdownV2(alert.RunbookURL))
	}

	// 1. Try explicit notification channel on the alert source (any provider).
	// ResolveForAlertSource returns the explicit channel when set, regardless
	// of the provider hint — we pass Telegram as hint only to control the
	// fallback when no explicit channel exists.
	if instance.NotificationChannelID != nil {
		ch, err := h.channelService.ResolveForAlertSource(instance, database.MessagingProviderTelegram)
		if err == nil && ch != nil {
			if posted, ok := h.postToChannel(context.Background(), ch, message); ok {
				return ch, posted.resolvedID, posted.messageID, posted.provider, nil
			}
		}
	}

	// 2. Try default channels — Telegram first, then Slack.
	for _, provider := range []database.MessagingProvider{
		database.MessagingProviderTelegram,
		database.MessagingProviderSlack,
	} {
		ch, err := h.channelService.ResolveDefault(provider)
		if err != nil || ch == nil {
			continue
		}

		if posted, ok := h.postToChannel(context.Background(), ch, message); ok {
			return ch, posted.resolvedID, posted.messageID, posted.provider, nil
		}
	}

	return nil, "", "", "", nil
}

// postResult holds the result of a successful post attempt.
type postResult struct {
	resolvedID string
	messageID  string
	provider   database.MessagingProvider
}

// postToChannel resolves the external ID (Slack name→ID if needed) and posts
// the message via the provider registry. Returns (result, true) on success.
func (h *AlertHandler) postToChannel(ctx context.Context, ch *database.Channel, message string) (postResult, bool) {
	if ch == nil || ch.ExternalID == "" {
		return postResult{}, false
	}

	provider := ch.Integration.Provider
	resolvedID := ch.ExternalID
	if provider == database.MessagingProviderSlack {
		resolvedID = h.resolveSlackExternalID(ch.ExternalID)
	}
	if resolvedID == "" {
		return postResult{}, false
	}

	postedID, err := h.postViaProvider(ctx, ch, resolvedID, message)
	if err != nil {
		slog.Warn("failed to post alert via provider", "provider", provider, "err", err)
		return postResult{}, false
	}
	if postedID == "" {
		return postResult{}, false
	}

	// Slack-specific: add reaction (best-effort)
	if provider == database.MessagingProviderSlack {
		if slackClient := h.slackManager.GetClient(); slackClient != nil {
			_ = slackClient.AddReaction("rotating_light", slack.ItemRef{
				Channel:   resolvedID,
				Timestamp: postedID,
			})
		}
	}

	return postResult{resolvedID: resolvedID, messageID: postedID, provider: provider}, true
}

// postInvestigationResult posts the investigation result to the appropriate
// provider. For Slack, posts as a thread reply with a result reaction.
// For Telegram, posts as a reply to the original message.
func (h *AlertHandler) postInvestigationResult(channel *database.Channel, externalID, messageID, text string, provider database.MessagingProvider, hasError bool) {
	if messageID == "" || externalID == "" || channel == nil {
		return
	}

	switch provider {
	case database.MessagingProviderSlack:
		h.updateSlackWithResult(externalID, messageID, text, hasError)

	case database.MessagingProviderTelegram:
		p, err := h.providerRegistry.Get(database.MessagingProviderTelegram)
		if err != nil {
			slog.Warn("telegram provider not registered", "err", err)
			return
		}
		telegramProvider, ok := p.(*messaging.TelegramProvider)
		if !ok {
			slog.Warn("provider is not TelegramProvider")
			return
		}

		// Build footer with UI link (plain text, no incident UUID needed here
		// since the handler doesn't pass it — the text is already formatted).
		formatted := messaging.FormatInvestigationResult(text)
		formatted = messaging.TruncateForTelegram(formatted, messaging.TelegramMaxMessageLength)

		_, err = telegramProvider.PostThreadReply(context.Background(), channel, messageID, formatted)
		if err != nil {
			slog.Warn("failed to post telegram investigation result", "err", err)
		}
	}
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
	// runListenerChannelInvestigation's deferred Stop.

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

	if !settings.IsActive() {
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
	fmt.Fprintf(&sb, "<%s/incidents/%s|View reasoning log>", baseURL, incidentUUID)
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
// Reserves space for a truncation notice and backtracks the cut to a UTF-8
// rune boundary so a multi-byte character (emoji, CJK, etc.) is never sliced
// in half — Slack rejects the message body silently when that happens.
func truncateForSlack(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	const suffix = "\n\n_...truncated. See full response in the UI._"
	cutoff := maxBytes - len(suffix)
	if cutoff < 100 {
		cutoff = 100
	}
	if cutoff > len(message) {
		cutoff = len(message)
	}
	// Walk cutoff back while the first EXCLUDED byte (message[cutoff]) is a
	// UTF-8 continuation byte (high bits 10xxxxxx). After the loop,
	// message[cutoff] is either a rune start byte or end-of-string, so
	// message[:cutoff] is guaranteed valid UTF-8.
	for cutoff > 0 && cutoff < len(message) && (message[cutoff]&0xC0) == 0x80 {
		cutoff--
	}
	truncated := message[:cutoff]
	// Prefer a clean newline break when one is reasonably close.
	if idx := strings.LastIndex(truncated, "\n"); idx > cutoff/2 {
		truncated = truncated[:idx]
	}
	return truncated + suffix
}

// incidentThreadPostable reports whether akmatori may write into the Slack
// thread recorded on an incident. Incidents spawned from a listener Channel
// carry that channel's UUID in source_uuid; when the row resolves to a
// channel with can_post=false the channel is a silent listener — alerts are
// extracted and investigated (results land in the UI) but akmatori never
// posts back into the thread. Source UUIDs that don't resolve to a channel
// (webhook alert sources, deleted channels) default to postable so
// webhook-notification threads keep working.
func (h *AlertHandler) incidentThreadPostable(incident *database.Incident) bool {
	if incident == nil || h.channelService == nil || incident.SourceUUID == "" {
		return true
	}
	ch, err := h.channelService.GetChannelByUUID(incident.SourceUUID)
	if err != nil || ch == nil {
		return true
	}
	return ch.CanPost
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

// buildAlertMergedMessage builds the Slack thread note posted when an incoming
// alert is correlated into an existing incident. It enriches the bare incident
// UUID with a clickable link, the incident subject, and the current count of
// alerts linked to that incident. All enrichment is best-effort: if the incident
// cannot be loaded, it degrades to a link with the UUID.
func (h *AlertHandler) buildAlertMergedMessage(incidentUUID string) string {
	baseURL := resolveBaseURL()
	link := fmt.Sprintf("<%s/incidents/%s|incident>", baseURL, incidentUUID)

	incident, err := h.skillService.GetIncident(incidentUUID)
	if err != nil || incident == nil {
		return fmt.Sprintf("Alert merged into existing %s (ID: %s)", link, incidentUUID)
	}

	subject := strings.TrimSpace(incident.Title)
	if subject == "" {
		subject = "untitled incident"
	}

	var alertCount int64
	database.GetDB().Model(&database.Alert{}).
		Where("incident_uuid = ?", incident.UUID).Count(&alertCount)

	plural := "alerts"
	if alertCount == 1 {
		plural = "alert"
	}
	return fmt.Sprintf("Alert merged into existing %s: *%s* — %d %s linked",
		link, subject, alertCount, plural)
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
