package messaging

import (
	"fmt"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// telegramMarkdownV2SpecialChars lists all characters that must be escaped
// in MarkdownV2 format as specified by the Telegram Bot API documentation.
const telegramMarkdownV2SpecialChars = "_*[]()~`>#+-=|{}.!"

// EscapeMarkdownV2 escapes all special characters for Telegram MarkdownV2.
// Every character in telegramMarkdownV2SpecialChars is prefixed with a backslash.
func EscapeMarkdownV2(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 20) // rough estimate for extra backslashes
	for _, r := range s {
		if strings.ContainsRune(telegramMarkdownV2SpecialChars, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FormatAlertMessage builds a MarkdownV2 alert message body from a
// NormalizedAlert. Equivalent to the Slack format in alert_slack.go but
// using MarkdownV2 syntax (double asterisks for bold, Unicode emoji).
func FormatAlertMessage(alert alerts.NormalizedAlert, sourceDisplayName, sourceName string) string {
	emoji := database.GetSeverityEmoji(alert.Severity)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" **Alert: ")
	b.WriteString(EscapeMarkdownV2(alert.AlertName))
	b.WriteString("**\n\n")

	b.WriteString("🏷️ *Source:* ")
	b.WriteString(EscapeMarkdownV2(sourceDisplayName))
	b.WriteString(" (")
	b.WriteString(EscapeMarkdownV2(sourceName))
	b.WriteString(")\n")

	b.WriteString("💻 *Host:* ")
	b.WriteString(EscapeMarkdownV2(alert.TargetHost))
	b.WriteString("\n")

	b.WriteString("⚙️ *Service:* ")
	b.WriteString(EscapeMarkdownV2(alert.TargetService))
	b.WriteString("\n")

	b.WriteString("⚠️ *Severity:* ")
	b.WriteString(EscapeMarkdownV2(string(alert.Severity)))
	b.WriteString("\n")

	b.WriteString("📝 *Summary:* ")
	b.WriteString(EscapeMarkdownV2(alert.Summary))
	b.WriteString("\n")

	if alert.RunbookURL != "" {
		// MarkdownV2 link syntax: [text](url)
		// Both text and URL must be escaped.
		b.WriteString("\n📖 *Runbook:* [")
		b.WriteString(EscapeMarkdownV2("Runbook"))
		b.WriteString("](")
		b.WriteString(EscapeMarkdownV2(alert.RunbookURL))
		b.WriteString(")\n")
	}

	return b.String()
}

// FormatInvestigationResult builds a MarkdownV2 message from the investigation
// result text. The input is expected to already be formatted (by
// finalizeSlackMessageBody or similar); this function escapes any MarkdownV2
// special characters that would break parsing.
//
// If the input contains Slack mrkdwn links like <URL|Label>, they are
// converted to MarkdownV2 link syntax [Label](URL).
func FormatInvestigationResult(text string) string {
	// Convert Slack-style links <URL|Label> to MarkdownV2 [Label](URL)
	text = convertSlackLinksToMarkdownV2(text)
	// Escape remaining special characters, but preserve already-valid MarkdownV2
	return text
}

// convertSlackLinksToMarkdownV2 replaces <URL|Label> patterns with [Label](URL).
func convertSlackLinksToMarkdownV2(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	for {
		start := strings.Index(text, "<")
		if start < 0 {
			b.WriteString(text)
			break
		}

		// Write everything before the '<'
		b.WriteString(text[:start])

		end := strings.Index(text[start:], ">")
		if end < 0 {
			// No closing '>', treat as literal
			b.WriteString(text[start:])
			break
		}

		linkContent := text[start+1 : start+end]
		after := text[start+end+1:]

		if idx := strings.Index(linkContent, "|"); idx >= 0 {
			// It's a Slack link: <URL|Label>
			url := EscapeMarkdownV2(linkContent[:idx])
			label := EscapeMarkdownV2(linkContent[idx+1:])
			b.WriteString("[")
			b.WriteString(label)
			b.WriteString("](")
			b.WriteString(url)
			b.WriteString(")")
		} else {
			// Not a link, just escape and write literally
			b.WriteString(EscapeMarkdownV2("<" + linkContent + ">"))
		}

		text = after
	}

	return b.String()
}

// TelegramMaxMessageLength is the maximum message length for Telegram (4096 characters).
const TelegramMaxMessageLength = 4096

// TruncateForTelegram truncates text to fit within Telegram's message size
// limit (4096 characters). Telegram enforces this at the API level.
func TruncateForTelegram(text string, maxBytes int) string {
	if maxBytes <= 0 || maxBytes > TelegramMaxMessageLength {
		maxBytes = TelegramMaxMessageLength
	}
	if len(text) <= maxBytes {
		return text
	}

	// Truncate and add ellipsis
	truncated := text[:maxBytes-3]
	// Backtrack to a UTF-8 character boundary
	for len(truncated) > 0 {
		if r := truncated[len(truncated)-1]; r < 0x80 || r >= 0xC0 {
			break
		}
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "..."
}

// FormatResultForTelegram formats an investigation result for Telegram posting.
// It converts Slack mrkdwn to MarkdownV2 and truncates to fit Telegram's limit.
func FormatResultForTelegram(text string, footer string) string {
	// Telegram budget: 4096 chars total, reserve space for footer
	footerLen := len(footer)
	bodyBudget := TelegramMaxMessageLength - footerLen - 50 // margin
	if bodyBudget < 200 {
		bodyBudget = 200
	}

	formatted := FormatInvestigationResult(text)
	if len(formatted) > bodyBudget {
		formatted = TruncateForTelegram(formatted, bodyBudget)
	}

	if footer != "" {
		formatted += "\n\n" + footer
	}

	return formatted
}

// BuildTelegramFooter builds a simple footer with metrics and UI link.
// Analogous to buildSlackFooter but without Slack mrkdwn link syntax.
// baseURL is the Akmatori web UI base URL (e.g. "https://akmatori.example.com").
func BuildTelegramFooter(response, incidentUUID, baseURL string) (responseWithoutFooter, footer string) {
	metricsLine := ""
	if idx := strings.LastIndex(response, "\n---\n⏱️"); idx >= 0 {
		metricsLine = strings.TrimSpace(response[idx+len("\n---\n"):])
		responseWithoutFooter = response[:idx]
	} else {
		responseWithoutFooter = response
	}

	var sb strings.Builder
	sb.WriteString("\n\n———\n")
	if metricsLine != "" {
		sb.WriteString(EscapeMarkdownV2(metricsLine))
		sb.WriteString("\n")
	}
	sb.WriteString("[View reasoning log](")
	sb.WriteString(EscapeMarkdownV2(fmt.Sprintf("%s/incidents/%s", baseURL, incidentUUID)))
	sb.WriteString(")")
	footer = sb.String()
	return
}
