package output

import (
	"strings"
	"unicode/utf8"
)

// truncationNotice is appended when content is byte-truncated so readers know
// the original message was longer than the budget allowed.
const truncationNotice = "\n\n_...truncated. See full response in the UI._"

// WithinSlackBudget reports whether text fits within maxBytes (byte length, not
// rune count, because Slack enforces byte-based limits).
func WithinSlackBudget(text string, maxBytes int) bool {
	return len(text) <= maxBytes
}

// ShortenForSlackBudget produces a deterministic shortened body that fits within
// maxBytes. When parsed contains a [FINAL_RESULT] block it collapses to the
// status verdict, summary, the first action, and the first recommendation. For
// free-form input it falls back to safe byte-truncation. The returned string is
// guaranteed to satisfy len(result) <= maxBytes provided maxBytes >= 1.
func ShortenForSlackBudget(parsed *ParsedOutput, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}

	if parsed != nil && parsed.FinalResult != nil {
		if condensed := condenseFinalResult(parsed.FinalResult); condensed != "" {
			if len(condensed) <= maxBytes {
				return condensed
			}
			return truncateBytes(condensed, maxBytes)
		}
	}

	if parsed != nil && parsed.Escalation != nil {
		if condensed := condenseEscalation(parsed.Escalation); condensed != "" {
			if len(condensed) <= maxBytes {
				return condensed
			}
			return truncateBytes(condensed, maxBytes)
		}
	}

	source := ""
	if parsed != nil {
		if parsed.CleanOutput != "" {
			source = parsed.CleanOutput
		} else {
			source = parsed.RawOutput
		}
	}
	return truncateBytes(source, maxBytes)
}

// condenseFinalResult builds a compact Slack-formatted summary from a parsed
// [FINAL_RESULT] block. Mirrors the heading/section layout used by the full
// formatter so the shortened view feels consistent.
func condenseFinalResult(fr *FinalResult) string {
	if fr == nil {
		return ""
	}

	var sb strings.Builder
	emoji := getStatusEmoji(fr.Status)
	status := strings.TrimSpace(fr.Status)
	if status == "" {
		status = "Result"
	} else {
		status = strings.ToUpper(status[:1]) + strings.ToLower(status[1:])
	}
	sb.WriteString(emoji)
	sb.WriteString(" *")
	sb.WriteString(status)
	sb.WriteString("*\n")

	if fr.Summary != "" {
		sb.WriteString("\n*Summary*\n")
		sb.WriteString(fr.Summary)
		sb.WriteString("\n")
	}

	if len(fr.ActionsTaken) > 0 {
		sb.WriteString("\n*Action*\n• ")
		sb.WriteString(fr.ActionsTaken[0])
		sb.WriteString("\n")
	}

	if len(fr.Recommendations) > 0 {
		sb.WriteString("\n*Recommendation*\n• ")
		sb.WriteString(fr.Recommendations[0])
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// condenseEscalation builds a compact Slack-formatted summary from a parsed
// [ESCALATE] block. Used as a fallback when the worker is unavailable for
// escalation-style outputs.
func condenseEscalation(esc *Escalation) string {
	if esc == nil {
		return ""
	}

	var sb strings.Builder
	emoji := getUrgencyEmoji(esc.Urgency)
	urgency := strings.ToUpper(strings.TrimSpace(esc.Urgency))
	if urgency == "" {
		urgency = "UNKNOWN"
	}
	sb.WriteString(emoji)
	sb.WriteString(" *ESCALATION REQUIRED* (")
	sb.WriteString(urgency)
	sb.WriteString(")\n")

	if esc.Reason != "" {
		sb.WriteString("\n*Reason*\n")
		sb.WriteString(esc.Reason)
		sb.WriteString("\n")
	}

	if len(esc.SuggestedActions) > 0 {
		sb.WriteString("\n*Suggested Action*\n• ")
		sb.WriteString(esc.SuggestedActions[0])
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// truncateBytes returns text truncated to at most maxBytes, appending a short
// "...truncated" notice when truncation actually happens. The result is
// guaranteed to be valid UTF-8 (we never split a multi-byte rune).
func truncateBytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}

	if maxBytes <= len(truncationNotice) {
		return safeUTF8Slice(text, maxBytes)
	}

	cutoff := maxBytes - len(truncationNotice)
	body := safeUTF8Slice(text, cutoff)
	if idx := strings.LastIndex(body, "\n"); idx > cutoff/2 {
		body = body[:idx]
	}
	return body + truncationNotice
}

// safeUTF8Slice slices s to at most n bytes without splitting a multi-byte rune.
func safeUTF8Slice(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
