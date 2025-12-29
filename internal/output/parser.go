package output

import (
	"regexp"
	"strings"
)

// FinalResult represents a parsed [FINAL_RESULT] block
type FinalResult struct {
	Status          string   // resolved, unresolved, escalate
	Summary         string
	ActionsTaken    []string
	Recommendations []string
}

// Escalation represents a parsed [ESCALATE] block
type Escalation struct {
	Reason           string
	Urgency          string // low, medium, high, critical
	Context          string
	SuggestedActions []string
}

// Progress represents a parsed [PROGRESS] block
type Progress struct {
	Step           string
	Completed      string
	FindingsSoFar  string
}

// ParsedOutput contains all parsed structured blocks from agent output
type ParsedOutput struct {
	// The original raw output
	RawOutput string

	// The output with structured blocks removed
	CleanOutput string

	// Parsed structured blocks (nil if not found)
	FinalResult *FinalResult
	Escalation  *Escalation
	Progress    *Progress
}

// Regex patterns for structured blocks
var (
	finalResultPattern = regexp.MustCompile(`(?s)\[FINAL_RESULT\]\s*(.+?)\s*\[/FINAL_RESULT\]`)
	escalatePattern    = regexp.MustCompile(`(?s)\[ESCALATE\]\s*(.+?)\s*\[/ESCALATE\]`)
	progressPattern    = regexp.MustCompile(`(?s)\[PROGRESS\]\s*(.+?)\s*\[/PROGRESS\]`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// Parse extracts structured blocks from agent output
func Parse(output string) *ParsedOutput {
	result := &ParsedOutput{
		RawOutput: output,
	}

	// Parse FINAL_RESULT
	if matches := finalResultPattern.FindStringSubmatch(output); len(matches) >= 2 {
		result.FinalResult = parseFinalResultContent(matches[1])
	}

	// Parse ESCALATE
	if matches := escalatePattern.FindStringSubmatch(output); len(matches) >= 2 {
		result.Escalation = parseEscalationContent(matches[1])
	}

	// Parse PROGRESS
	if matches := progressPattern.FindStringSubmatch(output); len(matches) >= 2 {
		result.Progress = parseProgressContent(matches[1])
	}

	// Create clean output by removing structured blocks
	clean := output
	clean = finalResultPattern.ReplaceAllString(clean, "")
	clean = escalatePattern.ReplaceAllString(clean, "")
	clean = progressPattern.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)
	clean = multiNewlinePattern.ReplaceAllString(clean, "\n\n")
	result.CleanOutput = clean

	return result
}

// parseFinalResultContent parses the content inside a [FINAL_RESULT] block
func parseFinalResultContent(content string) *FinalResult {
	result := &FinalResult{}

	lines := strings.Split(content, "\n")
	var currentSection string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for field prefixes
		if strings.HasPrefix(line, "status:") {
			result.Status = strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			currentSection = ""
		} else if strings.HasPrefix(line, "summary:") {
			result.Summary = strings.TrimSpace(strings.TrimPrefix(line, "summary:"))
			currentSection = ""
		} else if strings.HasPrefix(line, "actions_taken:") {
			currentSection = "actions"
		} else if strings.HasPrefix(line, "recommendations:") {
			currentSection = "recommendations"
		} else if strings.HasPrefix(line, "- ") {
			item := strings.TrimPrefix(line, "- ")
			switch currentSection {
			case "actions":
				result.ActionsTaken = append(result.ActionsTaken, item)
			case "recommendations":
				result.Recommendations = append(result.Recommendations, item)
			}
		}
	}

	return result
}

// parseEscalationContent parses the content inside an [ESCALATE] block
func parseEscalationContent(content string) *Escalation {
	result := &Escalation{}

	lines := strings.Split(content, "\n")
	var currentSection string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "reason:") {
			result.Reason = strings.TrimSpace(strings.TrimPrefix(line, "reason:"))
			currentSection = ""
		} else if strings.HasPrefix(line, "urgency:") {
			result.Urgency = strings.TrimSpace(strings.TrimPrefix(line, "urgency:"))
			currentSection = ""
		} else if strings.HasPrefix(line, "context:") {
			result.Context = strings.TrimSpace(strings.TrimPrefix(line, "context:"))
			currentSection = ""
		} else if strings.HasPrefix(line, "suggested_actions:") {
			currentSection = "suggested_actions"
		} else if strings.HasPrefix(line, "- ") {
			item := strings.TrimPrefix(line, "- ")
			if currentSection == "suggested_actions" {
				result.SuggestedActions = append(result.SuggestedActions, item)
			}
		}
	}

	return result
}

// parseProgressContent parses the content inside a [PROGRESS] block
func parseProgressContent(content string) *Progress {
	result := &Progress{}

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "step:") {
			result.Step = strings.TrimSpace(strings.TrimPrefix(line, "step:"))
		} else if strings.HasPrefix(line, "completed:") {
			result.Completed = strings.TrimSpace(strings.TrimPrefix(line, "completed:"))
		} else if strings.HasPrefix(line, "findings_so_far:") {
			result.FindingsSoFar = strings.TrimSpace(strings.TrimPrefix(line, "findings_so_far:"))
		}
	}

	return result
}

// HasStructuredOutput returns true if any structured blocks were found
func (p *ParsedOutput) HasStructuredOutput() bool {
	return p.FinalResult != nil || p.Escalation != nil || p.Progress != nil
}
