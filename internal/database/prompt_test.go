package database

import (
	"strings"
	"testing"
)

func TestDefaultIncidentManagerPrompt_ContainsQMDSearch(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"qmd.query tool reference", `qmd.query`},
		{"qmd.get tool reference", `qmd.get`},
		{"gateway_call usage", `gateway_call`},
		{"fallback mention", `/akmatori/runbooks/`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

func TestDefaultIncidentManagerPrompt_HasFallbackInstruction(t *testing.T) {
	if !strings.Contains(DefaultIncidentManagerPrompt, "unavailable") {
		t.Error("prompt should mention fallback when QMD is unavailable")
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
		{"lex sub-query", `"type": "lex"`},
		{"vec sub-query", `"type": "vec"`},
		{"hyde sub-query", `"type": "hyde"`},
		// Regression: with the memories collection now enabled, the
		// runbook-search step MUST scope to the runbooks collection so it
		// doesn't surface memory documents during the "search runbooks
		// first" workflow.
		{"runbook collections scope", `"collections": ["runbooks"]`},
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
	// The QMD instructions should be inline in the workflow, not in a separate "## Runbooks" section
	if strings.Contains(DefaultIncidentManagerPrompt, "## Runbooks") {
		t.Error("QMD instructions should be inline in the workflow, not in a separate Runbooks section")
	}
}

// TestDefaultIncidentManagerPrompt_RunbookSearchSection asserts that the
// runbook-search step instructs the agent to issue a single qmd.query with a
// {lex, vec, hyde} triplet sub-query shape (all three carrying the same
// natural-language alert summary) with up-to-2 retries capped at 3 total calls.
// See plan: docs/plans/completed/2026-05-10-qmd-semantic-search-triplet.md
func TestDefaultIncidentManagerPrompt_RunbookSearchSection(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"lex sub-query", `"type": "lex"`},
		{"vec sub-query", `"type": "vec"`},
		{"hyde sub-query", `"type": "hyde"`},
		{"natural-language placeholder", "<one-sentence natural-language alert summary>"},
		{"limit 5", `"limit": 5`},
		{"runbooks collections scope", `"collections": ["runbooks"]`},
		{"max 3 calls cue", "Cap total qmd.query calls at 3"},
		{"retry guidance", "up to 2 retries"},
		{"retry angle source_system", "source_system"},
		{"retry angle target_service", "target_service"},
		{"score gate", "score > 0.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

// TestDefaultIncidentManagerPrompt_SingleQMDQueryWithOrderedTriplet pins the
// structural invariant that the runbook-search step issues exactly ONE
// gateway_call("qmd.query", ...) with the three sub-queries in lex→vec→hyde
// order inside a single searches[] array. The substring assertions in the
// other tests would still pass if a future edit split the call into three
// separate qmd.query invocations or reordered the modes — this test catches
// that drift.
func TestDefaultIncidentManagerPrompt_SingleQMDQueryWithOrderedTriplet(t *testing.T) {
	// Exactly one runbook-search qmd.query call (the test gateway_call("qmd.get", ...)
	// also exists in the prompt but uses a different tool name).
	if got := strings.Count(DefaultIncidentManagerPrompt, `gateway_call("qmd.query"`); got != 1 {
		t.Errorf("expected exactly 1 gateway_call(\"qmd.query\"...) in prompt, got %d", got)
	}

	lexIdx := strings.Index(DefaultIncidentManagerPrompt, `"type": "lex"`)
	vecIdx := strings.Index(DefaultIncidentManagerPrompt, `"type": "vec"`)
	hydeIdx := strings.Index(DefaultIncidentManagerPrompt, `"type": "hyde"`)
	if lexIdx < 0 || vecIdx < 0 || hydeIdx < 0 {
		t.Fatalf("missing one of the three sub-query type markers: lex=%d vec=%d hyde=%d", lexIdx, vecIdx, hydeIdx)
	}
	if !(lexIdx < vecIdx && vecIdx < hydeIdx) {
		t.Errorf("triplet must appear in lex→vec→hyde order, got lex=%d vec=%d hyde=%d", lexIdx, vecIdx, hydeIdx)
	}
}
