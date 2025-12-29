package output

import (
	"fmt"
	"strings"
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

	// No structured output, return clean output (with any blocks stripped)
	if parsed.CleanOutput != "" {
		return parsed.CleanOutput
	}

	return parsed.RawOutput
}

// formatFinalResultForSlack formats a FinalResult for Slack
func formatFinalResultForSlack(result *FinalResult, additionalContext string) string {
	var sb strings.Builder

	// Status emoji and header
	statusEmoji := getStatusEmoji(result.Status)
	statusText := strings.Title(result.Status)
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
