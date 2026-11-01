package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// externalThread is an alert notification posted to a non-Slack messaging
// provider (Telegram, Discord). The investigation result is posted as a
// reply to the alert message. These providers have no typing banner,
// reactions or thread context for follow-up chat: the alert and its result
// are the whole conversation; follow-ups happen in the UI.
type externalThread struct {
	channel  *database.Channel
	parentID string
}

// resolveExternalChannel returns the alert source's explicit notification
// channel when it belongs to a non-Slack provider and can post. Slack
// destinations (and the per-provider default) keep the Slack flow.
func (h *AlertHandler) resolveExternalChannel(asi *database.AlertSourceInstance) *database.Channel {
	if asi == nil || asi.NotificationChannelID == nil || h.channelService == nil || h.providerRegistry == nil {
		return nil
	}
	ch, err := h.channelService.ResolveForAlertSource(asi, database.MessagingProviderSlack)
	if err != nil || ch == nil {
		return nil
	}
	if ch.Integration.Provider == database.MessagingProviderSlack || !ch.CanPost || !ch.Enabled || !ch.Integration.Enabled {
		return nil
	}
	if _, err := h.providerRegistry.Get(ch.Integration.Provider); err != nil {
		return nil
	}
	return ch
}

// alertBannerMarkdown is the provider-neutral alert banner.
func alertBannerMarkdown(alert alerts.NormalizedAlert, source string) string {
	msg := fmt.Sprintf("%s **Alert: %s**\n\n:label: **Source:** %s  \n:computer: **Host:** %s  \n:gear: **Service:** %s  \n:warning: **Severity:** %s  \n:memo: **Summary:** %s",
		database.GetSeverityEmoji(alert.Severity), alert.AlertName, source,
		alert.TargetHost, alert.TargetService, alert.Severity, alert.Summary)
	if alert.RunbookURL != "" {
		msg += fmt.Sprintf("  \n:book: **Runbook:** %s", alert.RunbookURL)
	}
	return msg
}

// postAlertExternal posts the alert banner to a non-Slack channel. Returns
// nil when posting failed (logged); the investigation still runs.
func (h *AlertHandler) postAlertExternal(ch *database.Channel, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) *externalThread {
	provider, err := h.providerRegistry.Get(ch.Integration.Provider)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := fmt.Sprintf("%s (%s)", instance.AlertSourceType.DisplayName, instance.Name)
	posted, err := provider.PostMessage(ctx, ch, alertBannerMarkdown(alert, source))
	if err != nil || posted == nil {
		slog.Warn("failed to post alert to channel", "provider", ch.Integration.Provider, "channel_uuid", ch.UUID, "err", err)
		return nil
	}
	return &externalThread{channel: ch, parentID: posted.MessageID}
}

// postResultExternal posts the investigation result as a reply to the
// alert message. text is provider-neutral Markdown; the provider renders
// and splits it.
func (h *AlertHandler) postResultExternal(ext *externalThread, text string, hasError bool) {
	if ext == nil || h.providerRegistry == nil {
		return
	}
	provider, err := h.providerRegistry.Get(ext.channel.Integration.Provider)
	if err != nil {
		return
	}
	if hasError {
		text = ":x: **Investigation failed**\n\n" + text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := provider.PostThreadReply(ctx, ext.channel, ext.parentID, text); err != nil {
		slog.Warn("failed to post investigation result to channel", "provider", ext.channel.Integration.Provider, "channel_uuid", ext.channel.UUID, "err", err)
	}
}
