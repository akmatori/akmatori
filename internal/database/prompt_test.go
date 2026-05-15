package database

import (
	"strings"
	"testing"
)

func TestDefaultIncidentManagerPrompt_ContainsRunbookSearcherSubagent(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"subagent call shape", `subagent(`},
		{"runbook-searcher agent name", `"agent": "runbook-searcher"`},
		{"runbook directory fallback", `/akmatori/runbooks/`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

func TestDefaultIncidentManagerPrompt_NoLegacyQMDOrMemoryToolReferences(t *testing.T) {
	// Regression: after the QMD subagent migration, the incident-manager
	// prompt must not reference the retired gateway tools.
	for _, banned := range []string{
		"qmd.query",
		"qmd.get",
		"memory.search",
		"memory.get",
	} {
		if strings.Contains(DefaultIncidentManagerPrompt, banned) {
			t.Errorf("DefaultIncidentManagerPrompt must not contain legacy tool reference %q", banned)
		}
	}
}

func TestDefaultIncidentManagerPrompt_HasFallbackInstruction(t *testing.T) {
	// The subagent-errored / unavailable fallback path must be explicit.
	if !strings.Contains(DefaultIncidentManagerPrompt, "unavailable") {
		t.Error("prompt should mention fallback when the subagent is unavailable")
	}
}

func TestDefaultIncidentManagerPrompt_MandatoryRunbookSearch(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"mandatory keyword", "MANDATORY"},
		{"search first instruction", "MANDATORY - Search runbooks FIRST before using any infrastructure tools"},
		{"must search before other steps", "You MUST search for relevant runbooks before performing any other investigation steps"},
		{"runbook-searcher delegated", "runbook-searcher"},
		{"empty not a skip reason", "Empty results are NOT a reason to skip"},
		{"primary guide", "PRIMARY investigation guide"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

func TestDefaultIncidentManagerPrompt_RunbookSearchBeforeInfraTools(t *testing.T) {
	// Verify that runbook search (step 2) comes before "Load relevant skills" (step 3)
	mandatoryIdx := strings.Index(DefaultIncidentManagerPrompt, "MANDATORY - Search runbooks FIRST")
	skillsIdx := strings.Index(DefaultIncidentManagerPrompt, "Load relevant skills")

	if mandatoryIdx == -1 {
		t.Fatal("prompt must contain mandatory runbook search step")
	}
	if skillsIdx == -1 {
		t.Fatal("prompt must contain load relevant skills step")
	}
	if mandatoryIdx >= skillsIdx {
		t.Error("mandatory runbook search must appear before load relevant skills step")
	}
}

func TestDefaultIncidentManagerPrompt_NoSeparateRunbooksSection(t *testing.T) {
	// The subagent instructions should be inline in the workflow, not in a separate "## Runbooks" section
	if strings.Contains(DefaultIncidentManagerPrompt, "## Runbooks") {
		t.Error("runbook-search instructions should be inline in the workflow, not in a separate Runbooks section")
	}
}

// TestDefaultIncidentManagerPrompt_RunbookSearcherRetryBudget asserts the
// runbook-search step caps total subagent invocations at 3 (initial plus up to
// 2 retries) and names target_service / host as a possible retry angle.
func TestDefaultIncidentManagerPrompt_RunbookSearcherRetryBudget(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"natural-language placeholder", "<one-sentence natural-language alert summary"},
		{"max 3 calls cue", "Cap total runbook-searcher invocations at 3"},
		{"retry guidance", "up to 2 retries"},
		{"target_service mentioned as retry angle", "target_service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

// TestDefaultIncidentManagerPrompt_SingleRunbookSearcherInvocation pins the
// structural invariant that the runbook-search step shows exactly ONE
// subagent({"agent": "runbook-searcher", ...}) example. Without this, a
// future edit could split the example into multiple per-retry invocations or
// drift the agent name.
func TestDefaultIncidentManagerPrompt_SingleRunbookSearcherInvocation(t *testing.T) {
	if got := strings.Count(DefaultIncidentManagerPrompt, `"agent": "runbook-searcher"`); got != 1 {
		t.Errorf("expected exactly 1 subagent({\"agent\": \"runbook-searcher\"...}) example in prompt, got %d", got)
	}
}

// TestDefaultIncidentManagerPrompt_RequiresSourcePhraseOnRetry pins the
// conditional MUST that retry #1 quote a verbatim sender/source/channel phrase
// from the Original alert text: block. Mirrors
// TestPrependGuidance_RequiresSourcePhraseOnRetry in internal/executor — both
// prompts are kept in sync per the "keep them in sync" invariant noted in
// executor.go's PrependGuidance comment.
//
// Asserted as a single normalized clause (not three independent substrings)
// so the rule can't be silently weakened by scattering the tokens — e.g.,
// dropping MUST to "may", removing the gate, or moving "verbatim" to an
// unrelated sentence.
func TestDefaultIncidentManagerPrompt_RequiresSourcePhraseOnRetry(t *testing.T) {
	normalized := strings.Join(strings.Fields(DefaultIncidentManagerPrompt), " ")
	want := `When the prompt contains an "Original alert text:" block, include a distinctive sender / source / channel / title phrase verbatim`
	if !strings.Contains(normalized, want) {
		t.Errorf("DefaultIncidentManagerPrompt missing conditional verbatim-quote clause\nwant: %s", want)
	}
}
