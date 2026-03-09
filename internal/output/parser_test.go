package output

import (
	"testing"
)

func TestParse_EmptyInput(t *testing.T) {
	result := Parse("")
	if result == nil {
		t.Fatal("Parse returned nil")
	}
	if result.RawOutput != "" {
		t.Errorf("RawOutput = %q, want empty", result.RawOutput)
	}
	if result.CleanOutput != "" {
		t.Errorf("CleanOutput = %q, want empty", result.CleanOutput)
	}
	if result.FinalResult != nil {
		t.Error("FinalResult should be nil for empty input")
	}
}

func TestParse_NoStructuredBlocks(t *testing.T) {
	input := "This is just regular text output."
	result := Parse(input)

	if result.CleanOutput != input {
		t.Errorf("CleanOutput = %q, want %q", result.CleanOutput, input)
	}
	if result.FinalResult != nil || result.Escalation != nil || result.Progress != nil {
		t.Error("No structured blocks should be parsed")
	}
}

func TestParse_FinalResult(t *testing.T) {
	input := `Some preamble text.

[FINAL_RESULT]
status: resolved
summary: Fixed the memory leak by restarting the pod
actions_taken:
- Identified memory-leaking container
- Restarted pod app-server-xyz
- Verified memory usage normalized
recommendations:
- Increase memory limits
- Add memory alerts
[/FINAL_RESULT]

Some trailing text.`

	result := Parse(input)

	if result.FinalResult == nil {
		t.Fatal("FinalResult should not be nil")
	}

	fr := result.FinalResult
	if fr.Status != "resolved" {
		t.Errorf("Status = %q, want 'resolved'", fr.Status)
	}
	if fr.Summary != "Fixed the memory leak by restarting the pod" {
		t.Errorf("Summary = %q", fr.Summary)
	}
	if len(fr.ActionsTaken) != 3 {
		t.Errorf("ActionsTaken count = %d, want 3", len(fr.ActionsTaken))
	}
	if len(fr.Recommendations) != 2 {
		t.Errorf("Recommendations count = %d, want 2", len(fr.Recommendations))
	}

	// Clean output should not contain the block
	if contains(result.CleanOutput, "[FINAL_RESULT]") {
		t.Error("CleanOutput should not contain [FINAL_RESULT]")
	}
}

func TestParse_Escalation(t *testing.T) {
	input := `[ESCALATE]
reason: Database connection pool exhausted
urgency: high
context: Multiple services reporting DB connection timeouts
suggested_actions:
- Increase connection pool size
- Check for connection leaks
- Review recent deployments
[/ESCALATE]`

	result := Parse(input)

	if result.Escalation == nil {
		t.Fatal("Escalation should not be nil")
	}

	esc := result.Escalation
	if esc.Reason != "Database connection pool exhausted" {
		t.Errorf("Reason = %q", esc.Reason)
	}
	if esc.Urgency != "high" {
		t.Errorf("Urgency = %q, want 'high'", esc.Urgency)
	}
	if esc.Context != "Multiple services reporting DB connection timeouts" {
		t.Errorf("Context = %q", esc.Context)
	}
	if len(esc.SuggestedActions) != 3 {
		t.Errorf("SuggestedActions count = %d, want 3", len(esc.SuggestedActions))
	}
}

func TestParse_Progress(t *testing.T) {
	input := `Working on the issue...

[PROGRESS]
step: Analyzing logs
completed: 2/5
findings_so_far: Found elevated error rates starting at 14:32 UTC
[/PROGRESS]

Still investigating...`

	result := Parse(input)

	if result.Progress == nil {
		t.Fatal("Progress should not be nil")
	}

	prog := result.Progress
	if prog.Step != "Analyzing logs" {
		t.Errorf("Step = %q", prog.Step)
	}
	if prog.Completed != "2/5" {
		t.Errorf("Completed = %q, want '2/5'", prog.Completed)
	}
	if prog.FindingsSoFar != "Found elevated error rates starting at 14:32 UTC" {
		t.Errorf("FindingsSoFar = %q", prog.FindingsSoFar)
	}
}

func TestParse_MultipleBlocks(t *testing.T) {
	input := `Starting investigation.

[PROGRESS]
step: Initial triage
completed: 1/3
findings_so_far: CPU spike detected
[/PROGRESS]

More work...

[FINAL_RESULT]
status: resolved
summary: Killed runaway process
actions_taken:
- Identified process
- Killed it
recommendations:
- Add process limits
[/FINAL_RESULT]`

	result := Parse(input)

	if result.Progress == nil {
		t.Error("Progress should be parsed")
	}
	if result.FinalResult == nil {
		t.Error("FinalResult should be parsed")
	}
	if result.Escalation != nil {
		t.Error("Escalation should be nil")
	}
}

func TestParse_CleanOutputNormalizesNewlines(t *testing.T) {
	input := `Text before.



[FINAL_RESULT]
status: resolved
summary: Done
[/FINAL_RESULT]



Text after.`

	result := Parse(input)

	// Multiple newlines should be normalized to double newlines
	if contains(result.CleanOutput, "\n\n\n") {
		t.Error("CleanOutput should not have triple newlines")
	}
}

func TestHasStructuredOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"no blocks", "plain text", false},
		{"with final result", "[FINAL_RESULT]\nstatus: done\n[/FINAL_RESULT]", true},
		{"with escalation", "[ESCALATE]\nreason: urgent\n[/ESCALATE]", true},
		{"with progress", "[PROGRESS]\nstep: working\n[/PROGRESS]", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Parse(tt.input)
			if got := result.HasStructuredOutput(); got != tt.want {
				t.Errorf("HasStructuredOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseFinalResultContent_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, *FinalResult)
	}{
		{
			name:    "empty content",
			content: "",
			check: func(t *testing.T, fr *FinalResult) {
				if fr.Status != "" || fr.Summary != "" {
					t.Error("should have empty fields for empty content")
				}
			},
		},
		{
			name:    "only status",
			content: "status: unresolved",
			check: func(t *testing.T, fr *FinalResult) {
				if fr.Status != "unresolved" {
					t.Errorf("Status = %q, want 'unresolved'", fr.Status)
				}
			},
		},
		{
			name:    "status with extra whitespace",
			content: "status:    resolved   ",
			check: func(t *testing.T, fr *FinalResult) {
				if fr.Status != "resolved" {
					t.Errorf("Status = %q, want 'resolved'", fr.Status)
				}
			},
		},
		{
			name: "actions without section header",
			content: `status: resolved
- action one
- action two`,
			check: func(t *testing.T, fr *FinalResult) {
				// Items without section header should not be added
				if len(fr.ActionsTaken) != 0 {
					t.Errorf("ActionsTaken = %v, want empty", fr.ActionsTaken)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFinalResultContent(tt.content)
			tt.check(t, result)
		})
	}
}

func TestParseEscalationContent_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, *Escalation)
	}{
		{
			name:    "empty content",
			content: "",
			check: func(t *testing.T, esc *Escalation) {
				if esc.Reason != "" || esc.Urgency != "" {
					t.Error("should have empty fields for empty content")
				}
			},
		},
		{
			name:    "all urgency levels",
			content: "urgency: critical",
			check: func(t *testing.T, esc *Escalation) {
				if esc.Urgency != "critical" {
					t.Errorf("Urgency = %q, want 'critical'", esc.Urgency)
				}
			},
		},
		{
			name: "items without section",
			content: `reason: something bad
- action one`,
			check: func(t *testing.T, esc *Escalation) {
				if len(esc.SuggestedActions) != 0 {
					t.Errorf("SuggestedActions should be empty without section header")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseEscalationContent(tt.content)
			tt.check(t, result)
		})
	}
}

func TestParseProgressContent_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(*testing.T, *Progress)
	}{
		{
			name:    "empty content",
			content: "",
			check: func(t *testing.T, prog *Progress) {
				if prog.Step != "" || prog.Completed != "" {
					t.Error("should have empty fields for empty content")
				}
			},
		},
		{
			name:    "only step",
			content: "step: Investigating",
			check: func(t *testing.T, prog *Progress) {
				if prog.Step != "Investigating" {
					t.Errorf("Step = %q, want 'Investigating'", prog.Step)
				}
			},
		},
		{
			name: "all fields",
			content: `step: Running diagnostics
completed: 75%
findings_so_far: High CPU on node-3`,
			check: func(t *testing.T, prog *Progress) {
				if prog.Step != "Running diagnostics" {
					t.Errorf("Step = %q", prog.Step)
				}
				if prog.Completed != "75%" {
					t.Errorf("Completed = %q", prog.Completed)
				}
				if prog.FindingsSoFar != "High CPU on node-3" {
					t.Errorf("FindingsSoFar = %q", prog.FindingsSoFar)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseProgressContent(tt.content)
			tt.check(t, result)
		})
	}
}

// Note: contains helper is defined in slack_formatter_test.go
