package output

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	// Markdown heading patterns: ## Heading -> *Heading*
	mdHeadingPattern = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	// Markdown bold: **text** -> *text*
	mdBoldPattern = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Markdown image: ![alt](url) -> <url|alt>
	mdImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	// Markdown link: [text](url) -> <url|text>
	mdLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// Markdown table separator row: |---|---|
	mdTableSepPattern = regexp.MustCompile(`(?m)^\|[\s:]*[-]+[\s:]*(\|[\s:]*[-]+[\s:]*)+\|\s*$`)
	// Markdown table row: | cell | cell |
	mdTableRowPattern = regexp.MustCompile(`(?m)^\|(.+)\|\s*$`)
	// Markdown horizontal rule: --- or *** or ___
	mdHRPattern = regexp.MustCompile(`(?m)^[\s]*([-*_]){3,}\s*$`)
)

// FormatForSlack converts parsed output to nicely formatted Slack message
func FormatForSlack(parsed *ParsedOutput) string {
	// If there's a final result, format it nicely
	if parsed.FinalResult != nil {
		return formatFinalResultForSlack(parsed.FinalResult, parsed.CleanOutput)
	}

	// If there's an escalation, format it with urgency
	if parsed.Escalation != nil {
		return formatEscalationForSlack(parsed.Escalation, parsed.CleanOutput)
	}

	// If there's progress, format it
	if parsed.Progress != nil {
		return formatProgressForSlack(parsed.Progress, parsed.CleanOutput)
	}

	// No structured output — convert markdown to Slack mrkdwn format
	if parsed.CleanOutput != "" {
		return MarkdownToSlack(parsed.CleanOutput)
	}

	return MarkdownToSlack(parsed.RawOutput)
}

// formatFinalResultForSlack formats a FinalResult for Slack
func formatFinalResultForSlack(result *FinalResult, additionalContext string) string {
	var sb strings.Builder

	// Status emoji and header
	statusEmoji := getStatusEmoji(result.Status)
	statusText := cases.Title(language.English).String(result.Status)
	sb.WriteString(fmt.Sprintf("%s *%s*\n\n", statusEmoji, statusText))

	// Summary
	if result.Summary != "" {
		sb.WriteString(fmt.Sprintf("*Summary*\n%s\n", result.Summary))
	}

	// Actions taken
	if len(result.ActionsTaken) > 0 {
		sb.WriteString("\n*Actions Taken*\n")
		for _, action := range result.ActionsTaken {
			sb.WriteString(fmt.Sprintf("• %s\n", action))
		}
	}

	// Recommendations
	if len(result.Recommendations) > 0 {
		sb.WriteString("\n*Recommendations*\n")
		for _, rec := range result.Recommendations {
			sb.WriteString(fmt.Sprintf("• %s\n", rec))
		}
	}

	// Add any additional context that was outside the structured block
	if additionalContext != "" {
		sb.WriteString(fmt.Sprintf("\n---\n%s", additionalContext))
	}

	return sb.String()
}

// formatEscalationForSlack formats an Escalation for Slack
func formatEscalationForSlack(esc *Escalation, additionalContext string) string {
	var sb strings.Builder

	// Urgency emoji and header
	urgencyEmoji := getUrgencyEmoji(esc.Urgency)
	sb.WriteString(fmt.Sprintf("%s *ESCALATION REQUIRED* (%s)\n\n", urgencyEmoji, strings.ToUpper(esc.Urgency)))

	// Reason
	if esc.Reason != "" {
		sb.WriteString(fmt.Sprintf("*Reason*\n%s\n", esc.Reason))
	}

	// Context
	if esc.Context != "" {
		sb.WriteString(fmt.Sprintf("\n*Context*\n%s\n", esc.Context))
	}

	// Suggested actions
	if len(esc.SuggestedActions) > 0 {
		sb.WriteString("\n*Suggested Actions*\n")
		for _, action := range esc.SuggestedActions {
			sb.WriteString(fmt.Sprintf("• %s\n", action))
		}
	}

	// Add any additional context
	if additionalContext != "" {
		sb.WriteString(fmt.Sprintf("\n---\n%s", additionalContext))
	}

	return sb.String()
}

// formatProgressForSlack formats a Progress update for Slack
func formatProgressForSlack(progress *Progress, additionalContext string) string {
	var sb strings.Builder

	sb.WriteString("🔄 *Progress Update*\n\n")

	if progress.Step != "" {
		sb.WriteString(fmt.Sprintf("*Current Step*: %s\n", progress.Step))
	}

	if progress.Completed != "" {
		sb.WriteString(fmt.Sprintf("*Progress*: %s\n", progress.Completed))
	}

	if progress.FindingsSoFar != "" {
		sb.WriteString(fmt.Sprintf("\n*Findings So Far*\n%s\n", progress.FindingsSoFar))
	}

	if additionalContext != "" {
		sb.WriteString(fmt.Sprintf("\n---\n%s", additionalContext))
	}

	return sb.String()
}

// getStatusEmoji returns an emoji for the given status
func getStatusEmoji(status string) string {
	switch strings.ToLower(status) {
	case "resolved":
		return "✅"
	case "unresolved":
		return "⚠️"
	case "escalate":
		return "🚨"
	default:
		return "📋"
	}
}

// getUrgencyEmoji returns an emoji for the given urgency level
func getUrgencyEmoji(urgency string) string {
	switch strings.ToLower(urgency) {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "low":
		return "🟢"
	default:
		return "⚠️"
	}
}

// MarkdownToSlack converts GitHub-flavored markdown to Slack mrkdwn format.
// Slack does not support markdown tables, ## headings, or **bold** syntax.
func MarkdownToSlack(text string) string {
	if text == "" {
		return text
	}

	// Convert markdown tables to readable plain text
	text = convertTables(text)

	// Convert headings: ## Title -> *Title*
	text = mdHeadingPattern.ReplaceAllString(text, "*$1*")

	// Convert bold: **text** -> *text* (must come after headings)
	text = mdBoldPattern.ReplaceAllString(text, "*$1*")

	// Convert images: ![alt](url) -> <url|alt> (must come before links)
	text = mdImagePattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := mdImagePattern.FindStringSubmatch(match)
		if parts[1] != "" {
			return fmt.Sprintf("<%s|%s>", parts[2], parts[1])
		}
		return parts[2]
	})

	// Convert links: [text](url) -> <url|text>
	text = mdLinkPattern.ReplaceAllString(text, "<$2|$1>")

	// Convert horizontal rules: --- -> ———
	text = mdHRPattern.ReplaceAllString(text, "———")

	return text
}

// convertTables converts markdown tables into a readable plain text format for Slack.
// Each row becomes "key: value" pairs when there's a header, or bullet points otherwise.
func convertTables(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	var headerCells []string
	inTable := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if !mdTableRowPattern.MatchString(line) {
			// Not a table row — flush state and pass through
			if inTable {
				inTable = false
				headerCells = nil
			}
			result = append(result, line)
			continue
		}

		// It's a table row
		cells := parseTableRow(line)

		if !inTable {
			// First row of a new table — this is the header
			inTable = true
			headerCells = cells
			continue
		}

		// Check if this is the separator row (|---|---|)
		if mdTableSepPattern.MatchString(line) {
			continue
		}

		// Data row — format as "Header: Value" pairs
		var parts []string
		for j, cell := range cells {
			cell = strings.TrimSpace(cell)
			if cell == "" {
				continue
			}
			if j < len(headerCells) && strings.TrimSpace(headerCells[j]) != "" {
				parts = append(parts, fmt.Sprintf("*%s:* %s", strings.TrimSpace(headerCells[j]), cell))
			} else {
				parts = append(parts, cell)
			}
		}
		if len(parts) > 0 {
			result = append(result, "• "+strings.Join(parts, " | "))
		}
	}

	return strings.Join(result, "\n")
}

// parseTableRow splits a markdown table row into cells.
func parseTableRow(row string) []string {
	// Remove leading/trailing pipes and split
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}
