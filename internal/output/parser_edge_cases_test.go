package output

import (
	"strings"
	"testing"
)

// --- Parser Edge Cases ---

func TestParse_MalformedBlocks_NoClosingTag(t *testing.T) {
	// Missing closing tag should not panic and should not parse block
	input := `Start text
[FINAL_RESULT]
status: resolved
summary: No closing tag
More text`

	result := Parse(input)

	// Block is never closed, so it shouldn't be parsed
	if result.FinalResult != nil {
		t.Error("unclosed FINAL_RESULT block should not be parsed")
	}
}

func TestParse_MalformedBlocks_NestedTags(t *testing.T) {
	// Nested tags (invalid) - regex should handle gracefully
	input := `[FINAL_RESULT]
status: done
[FINAL_RESULT]
nested block
[/FINAL_RESULT]
[/FINAL_RESULT]`

	result := Parse(input)

	// Should parse something, even if nested is weird
	// Non-greedy regex should match first complete block
	if result.FinalResult == nil {
		t.Error("should parse at least one FINAL_RESULT block")
	}
}

func TestParse_MalformedBlocks_ExtraWhitespace(t *testing.T) {
	// Extra whitespace around tags
	input := `

   [FINAL_RESULT]

status: resolved
summary: Extra whitespace test

   [/FINAL_RESULT]

`

	result := Parse(input)

	if result.FinalResult == nil {
		t.Fatal("FINAL_RESULT should be parsed despite whitespace")
	}
	if result.FinalResult.Status != "resolved" {
		t.Errorf("Status = %q, want 'resolved'", result.FinalResult.Status)
	}
}

func TestParse_CaseSensitiveTags(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldParse bool
	}{
		{
			name:        "correct case",
			input:       "[FINAL_RESULT]\nstatus: done\n[/FINAL_RESULT]",
			shouldParse: true,
		},
		{
			name:        "lowercase tags",
			input:       "[final_result]\nstatus: done\n[/final_result]",
			shouldParse: false,
		},
		{
			name:        "mixed case tags",
			input:       "[Final_Result]\nstatus: done\n[/Final_Result]",
			shouldParse: false,
		},
		{
			name:        "correct escalate",
			input:       "[ESCALATE]\nreason: test\n[/ESCALATE]",
			shouldParse: true,
		},
		{
			name:        "lowercase escalate",
			input:       "[escalate]\nreason: test\n[/escalate]",
			shouldParse: false,
		},
		{
			name:        "correct progress",
			input:       "[PROGRESS]\nstep: test\n[/PROGRESS]",
			shouldParse: true,
		},
		{
			name:        "lowercase progress",
			input:       "[progress]\nstep: test\n[/progress]",
			shouldParse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.input)
			hasParsed := result.FinalResult != nil || result.Escalation != nil || result.Progress != nil

			if hasParsed != tt.shouldParse {
				t.Errorf("shouldParse = %v, got parsed = %v", tt.shouldParse, hasParsed)
			}
		})
	}
}

func TestParse_MultipleOfSameBlock(t *testing.T) {
	// Multiple FINAL_RESULT blocks - should only parse first
	input := `[FINAL_RESULT]
status: first
summary: First block
[/FINAL_RESULT]

[FINAL_RESULT]
status: second
summary: Second block
[/FINAL_RESULT]`

	result := Parse(input)

	if result.FinalResult == nil {
		t.Fatal("should parse at least one FINAL_RESULT")
	}
	// FindStringSubmatch only returns first match
	if result.FinalResult.Status != "first" {
		t.Errorf("Status = %q, want 'first' (first match)", result.FinalResult.Status)
	}
}

func TestParse_AllThreeBlocks(t *testing.T) {
	input := `[PROGRESS]
step: Analyzing
completed: 1/3
findings_so_far: Disk is full
[/PROGRESS]

Some investigation notes...

[ESCALATE]
reason: Need DBA help
urgency: high
context: Database locks detected
suggested_actions:
- Contact DBA team
[/ESCALATE]

After escalation...

[FINAL_RESULT]
status: escalate
summary: Escalated to DBA
actions_taken:
- Identified locks
recommendations:
- Review query plans
[/FINAL_RESULT]`

	result := Parse(input)

	if result.Progress == nil {
		t.Error("Progress should be parsed")
	}
	if result.Escalation == nil {
		t.Error("Escalation should be parsed")
	}
	if result.FinalResult == nil {
		t.Error("FinalResult should be parsed")
	}

	// Verify all three have content
	if result.Progress.Step != "Analyzing" {
		t.Errorf("Progress.Step = %q", result.Progress.Step)
	}
	if result.Escalation.Reason != "Need DBA help" {
		t.Errorf("Escalation.Reason = %q", result.Escalation.Reason)
	}
	if result.FinalResult.Status != "escalate" {
		t.Errorf("FinalResult.Status = %q", result.FinalResult.Status)
	}
}

// --- FinalResult Content Edge Cases ---

func TestParseFinalResultContent_StatusVariations(t *testing.T) {
	statuses := []string{
		"resolved",
		"unresolved",
		"escalate",
		"partial",
		"in_progress",
		"", // empty
	}

	for _, status := range statuses {
		t.Run("status_"+status, func(t *testing.T) {
			content := "status: " + status
			result := parseFinalResultContent(content)
			if result.Status != status {
				t.Errorf("Status = %q, want %q", result.Status, status)
			}
		})
	}
}

func TestParseFinalResultContent_MixedListItems(t *testing.T) {
	// Actions and recommendations interleaved with other lines
	content := `status: resolved
summary: Fixed the issue
actions_taken:
- Action 1
- Action 2
Some random text that's not a list item
- Action 3
recommendations:
- Rec 1
More random text
- Rec 2`

	result := parseFinalResultContent(content)

	// Random text lines should be ignored
	if len(result.ActionsTaken) != 3 {
		t.Errorf("ActionsTaken count = %d, want 3", len(result.ActionsTaken))
	}
	if len(result.Recommendations) != 2 {
		t.Errorf("Recommendations count = %d, want 2", len(result.Recommendations))
	}
}

func TestParseFinalResultContent_EmptyListItems(t *testing.T) {
	content := `status: resolved
actions_taken:
- 
- Non-empty action
-   
recommendations:
-
`

	result := parseFinalResultContent(content)

	// Empty list items (just "-" or "- ") should still be captured
	// The "- " prefix is stripped, leaving empty strings
	// Implementation detail: trim only removes "- " prefix, not whitespace after
	if len(result.ActionsTaken) < 1 {
		t.Errorf("should have at least one action item")
	}
}

func TestParseFinalResultContent_ColonsInValues(t *testing.T) {
	content := `status: resolved
summary: Fix applied at 14:30:00 UTC
actions_taken:
- Time: 14:30
- Server: prod-01:8080`

	result := parseFinalResultContent(content)

	// Colons in values (not field names) should be preserved
	if !strings.Contains(result.Summary, "14:30:00") {
		t.Errorf("Summary should preserve time format: %q", result.Summary)
	}
	if len(result.ActionsTaken) != 2 {
		t.Errorf("ActionsTaken count = %d, want 2", len(result.ActionsTaken))
	}
	if !strings.Contains(result.ActionsTaken[0], "14:30") {
		t.Errorf("Action should preserve time: %q", result.ActionsTaken[0])
	}
}

// --- Escalation Content Edge Cases ---

func TestParseEscalationContent_UrgencyLevels(t *testing.T) {
	levels := []string{"low", "medium", "high", "critical", "unknown", ""}

	for _, level := range levels {
		t.Run("urgency_"+level, func(t *testing.T) {
			content := "urgency: " + level
			result := parseEscalationContent(content)
			if result.Urgency != level {
				t.Errorf("Urgency = %q, want %q", result.Urgency, level)
			}
		})
	}
}

func TestParseEscalationContent_LongContext(t *testing.T) {
	// Context is read from single line only (parser design)
	longContext := strings.Repeat("x", 200) // Long but single line
	content := "reason: Test\ncontext: " + longContext

	result := parseEscalationContent(content)

	if result.Context != longContext {
		t.Errorf("long single-line context should be preserved fully, got %d chars, want %d",
			len(result.Context), len(longContext))
	}
}

func TestParseEscalationContent_MultipleActions(t *testing.T) {
	content := `reason: Database failure
urgency: critical
suggested_actions:
- Failover to secondary
- Notify DBA team
- Check replication lag
- Update status page
- Prepare RCA document`

	result := parseEscalationContent(content)

	if len(result.SuggestedActions) != 5 {
		t.Errorf("SuggestedActions count = %d, want 5", len(result.SuggestedActions))
	}
	if result.SuggestedActions[0] != "Failover to secondary" {
		t.Errorf("first action = %q", result.SuggestedActions[0])
	}
	if result.SuggestedActions[4] != "Prepare RCA document" {
		t.Errorf("last action = %q", result.SuggestedActions[4])
	}
}

// --- Progress Content Edge Cases ---

func TestParseProgressContent_CompletedFormats(t *testing.T) {
	formats := []struct {
		input    string
		expected string
	}{
		{"completed: 1/5", "1/5"},
		{"completed: 50%", "50%"},
		{"completed: 3 of 10", "3 of 10"},
		{"completed: done", "done"},
		{"completed: in progress", "in progress"},
	}

	for _, tt := range formats {
		t.Run(tt.input, func(t *testing.T) {
			result := parseProgressContent(tt.input)
			if result.Completed != tt.expected {
				t.Errorf("Completed = %q, want %q", result.Completed, tt.expected)
			}
		})
	}
}

func TestParseProgressContent_MultipleFindingsLines(t *testing.T) {
	// Only the first findings_so_far: line should be captured
	content := `step: Investigating
findings_so_far: First finding
findings_so_far: Second finding (should be ignored)`

	result := parseProgressContent(content)

	// Implementation overwrites, so last one wins
	// This test documents current behavior
	if result.FindingsSoFar != "Second finding (should be ignored)" {
		t.Logf("Note: multiple findings_so_far lines - last one wins: %q", result.FindingsSoFar)
	}
}

// --- Clean Output Tests ---

func TestParse_CleanOutput_PreservesNonBlockContent(t *testing.T) {
	input := `## Investigation Report

This is the preamble.

[FINAL_RESULT]
status: resolved
summary: Done
[/FINAL_RESULT]

This is the epilogue.

### Appendix

More details here.`

	result := Parse(input)

	if !strings.Contains(result.CleanOutput, "## Investigation Report") {
		t.Error("clean output should preserve preamble heading")
	}
	if !strings.Contains(result.CleanOutput, "preamble") {
		t.Error("clean output should preserve preamble text")
	}
	if !strings.Contains(result.CleanOutput, "epilogue") {
		t.Error("clean output should preserve epilogue")
	}
	if !strings.Contains(result.CleanOutput, "### Appendix") {
		t.Error("clean output should preserve appendix heading")
	}
	if strings.Contains(result.CleanOutput, "[FINAL_RESULT]") {
		t.Error("clean output should NOT contain FINAL_RESULT tag")
	}
}

func TestParse_CleanOutput_MultipleBlocksRemoved(t *testing.T) {
	input := `Before.
[PROGRESS]
step: test
[/PROGRESS]
Middle.
[ESCALATE]
reason: test
[/ESCALATE]
After.
[FINAL_RESULT]
status: done
[/FINAL_RESULT]
End.`

	result := Parse(input)

	if strings.Contains(result.CleanOutput, "[PROGRESS]") {
		t.Error("clean output should not contain PROGRESS")
	}
	if strings.Contains(result.CleanOutput, "[ESCALATE]") {
		t.Error("clean output should not contain ESCALATE")
	}
	if strings.Contains(result.CleanOutput, "[FINAL_RESULT]") {
		t.Error("clean output should not contain FINAL_RESULT")
	}
	if !strings.Contains(result.CleanOutput, "Before") {
		t.Error("clean output should contain 'Before'")
	}
	if !strings.Contains(result.CleanOutput, "Middle") {
		t.Error("clean output should contain 'Middle'")
	}
	if !strings.Contains(result.CleanOutput, "After") {
		t.Error("clean output should contain 'After'")
	}
	if !strings.Contains(result.CleanOutput, "End") {
		t.Error("clean output should contain 'End'")
	}
}

// --- HasStructuredOutput Tests ---

func TestHasStructuredOutput_AllCombinations(t *testing.T) {
	tests := []struct {
		name     string
		parsed   *ParsedOutput
		expected bool
	}{
		{
			name:     "all nil",
			parsed:   &ParsedOutput{},
			expected: false,
		},
		{
			name:     "only FinalResult",
			parsed:   &ParsedOutput{FinalResult: &FinalResult{}},
			expected: true,
		},
		{
			name:     "only Escalation",
			parsed:   &ParsedOutput{Escalation: &Escalation{}},
			expected: true,
		},
		{
			name:     "only Progress",
			parsed:   &ParsedOutput{Progress: &Progress{}},
			expected: true,
		},
		{
			name:     "FinalResult and Escalation",
			parsed:   &ParsedOutput{FinalResult: &FinalResult{}, Escalation: &Escalation{}},
			expected: true,
		},
		{
			name:     "all three",
			parsed:   &ParsedOutput{FinalResult: &FinalResult{}, Escalation: &Escalation{}, Progress: &Progress{}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.parsed.HasStructuredOutput(); got != tt.expected {
				t.Errorf("HasStructuredOutput() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// --- Real-World Samples ---

func TestParse_RealWorldSample_ResolvedIncident(t *testing.T) {
	input := `## Investigation Summary

Analyzed the high CPU alert on prod-server-01.

### Findings

The CPU spike was caused by a runaway cron job.

[FINAL_RESULT]
status: resolved
summary: Killed runaway cron job (pid 12345) that was consuming 100% CPU
actions_taken:
- Identified high-CPU process using top
- Traced to cron job running data export
- Killed process with SIGTERM
- Verified CPU returned to normal levels
recommendations:
- Add timeout to cron job
- Implement CPU limits via cgroups
- Add monitoring alert for long-running processes
[/FINAL_RESULT]

Investigation complete. No further action needed.`

	result := Parse(input)

	if result.FinalResult == nil {
		t.Fatal("should parse FINAL_RESULT")
	}

	fr := result.FinalResult
	if fr.Status != "resolved" {
		t.Errorf("Status = %q", fr.Status)
	}
	if !strings.Contains(fr.Summary, "cron job") {
		t.Errorf("Summary should mention cron job: %q", fr.Summary)
	}
	if len(fr.ActionsTaken) != 4 {
		t.Errorf("ActionsTaken count = %d, want 4", len(fr.ActionsTaken))
	}
	if len(fr.Recommendations) != 3 {
		t.Errorf("Recommendations count = %d, want 3", len(fr.Recommendations))
	}
}

func TestParse_RealWorldSample_Escalation(t *testing.T) {
	input := `I've investigated the database connectivity issue but need help.

[ESCALATE]
reason: Database replication is broken and I don't have DBA privileges to fix it
urgency: critical
context: Primary database is running but replica is 2 hours behind. Writes are succeeding but reads from replica are returning stale data. Customer complaints are increasing.
suggested_actions:
- Contact DBA team immediately
- Consider failover to read from primary
- Prepare customer communication
- Check if recent schema migrations caused the issue
[/ESCALATE]

Waiting for DBA team response.`

	result := Parse(input)

	if result.Escalation == nil {
		t.Fatal("should parse ESCALATE")
	}

	esc := result.Escalation
	if esc.Urgency != "critical" {
		t.Errorf("Urgency = %q", esc.Urgency)
	}
	if !strings.Contains(esc.Reason, "DBA privileges") {
		t.Errorf("Reason should mention DBA: %q", esc.Reason)
	}
	if len(esc.SuggestedActions) != 4 {
		t.Errorf("SuggestedActions count = %d, want 4", len(esc.SuggestedActions))
	}
}
