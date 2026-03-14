package handlers

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// extractSlackMessageText extracts all readable text from a Slack message,
// including the main text, attachments, and blocks. Monitoring tools like
// Zabbix typically send alert content in attachments rather than plain text.
func extractSlackMessageText(msg slack.Message) string {
	var parts []string

	// 1. Main message text
	if msg.Text != "" {
		parts = append(parts, msg.Text)
	}

	// 2. Attachments (common for Zabbix, PagerDuty, Datadog, etc.)
	for _, att := range msg.Attachments {
		var attParts []string
		if att.Pretext != "" {
			attParts = append(attParts, att.Pretext)
		}
		if att.Title != "" {
			attParts = append(attParts, att.Title)
		}
		if att.Text != "" {
			attParts = append(attParts, att.Text)
		}
		for _, f := range att.Fields {
			if f.Title != "" && f.Value != "" {
				attParts = append(attParts, fmt.Sprintf("%s: %s", f.Title, f.Value))
			} else if f.Value != "" {
				attParts = append(attParts, f.Value)
			}
		}
		if att.Footer != "" {
			attParts = append(attParts, att.Footer)
		}
		// If nothing was extracted, use Fallback as last resort
		if len(attParts) == 0 && att.Fallback != "" {
			attParts = append(attParts, att.Fallback)
		}
		if len(attParts) > 0 {
			parts = append(parts, strings.Join(attParts, "\n"))
		}
	}

	// 3. Block Kit blocks (section text, header text)
	for _, block := range msg.Blocks.BlockSet {
		switch b := block.(type) {
		case *slack.SectionBlock:
			if b.Text != nil && b.Text.Text != "" {
				parts = append(parts, b.Text.Text)
			}
			for _, f := range b.Fields {
				if f != nil && f.Text != "" {
					parts = append(parts, f.Text)
				}
			}
		case *slack.HeaderBlock:
			if b.Text != nil && b.Text.Text != "" {
				parts = append(parts, b.Text.Text)
			}
		}
	}

	return strings.Join(parts, "\n")
}
