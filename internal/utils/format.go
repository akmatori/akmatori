package utils

import (
	"fmt"
	"strings"
	"time"
)

// FormatDuration formats a duration in a human-readable format
// Examples: "45ms", "1.5s", "2m 30s", "1h 15m"
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if minutes < 60 {
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	minutes = minutes % 60
	if minutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dh", hours)
}

// FormatNumber formats a number with comma separators
// Examples: 123 -> "123", 1234 -> "1,234", 1234567 -> "1,234,567"
func FormatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	str := fmt.Sprintf("%d", n)
	var result []rune
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, c)
	}
	return string(result)
}

// TruncateText truncates text to maxLen characters, adding "..." if truncated
// Also removes newlines for single-line display
func TruncateText(text string, maxLen int) string {
	// Remove newlines for single-line display
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)

	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return "..."
	}
	return text[:maxLen-3] + "..."
}

// GetLastNLines returns the last N lines from a multi-line string
func GetLastNLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// Max returns the maximum of two integers
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Min returns the minimum of two integers
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TruncateLogForSlack truncates a log for Slack display
// Keeps it under the specified max length and tries to start at a newline
func TruncateLogForSlack(log string, maxLen int) string {
	if len(log) <= maxLen {
		return log
	}

	truncated := log[len(log)-maxLen:]
	// Try to start at a newline to avoid cutting mid-line
	if idx := strings.Index(truncated, "\n"); idx > 0 && idx < 100 {
		truncated = truncated[idx+1:]
	}
	return "...(truncated)\n" + truncated
}

// AppendMetrics adds execution metrics to the end of a response
func AppendMetrics(response string, executionTime time.Duration, tokensUsed int) string {
	timeStr := FormatDuration(executionTime)

	var metricsLine string
	if tokensUsed > 0 {
		metricsLine = fmt.Sprintf("\n\n---\n⏱️ Time: %s | 🎯 Tokens: %s", timeStr, FormatNumber(tokensUsed))
	} else {
		metricsLine = fmt.Sprintf("\n\n---\n⏱️ Time: %s", timeStr)
	}

	return response + metricsLine
}
