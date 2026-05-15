package executor

import (
	"strings"
	"testing"
)

// TestPrependGuidance_DelegatesToRunbookSearcherSubagent guards against the
// runbook-search guidance regressing to a direct tool call. After the QMD
// subagent migration, the user-turn reminder must delegate the runbook search
// to the runbook-searcher subagent and stay in sync with
// DefaultIncidentManagerPrompt's runbook-search section.
func TestPrependGuidance_DelegatesToRunbookSearcherSubagent(t *testing.T) {
	out := PrependGuidance("test task")
	for _, want := range []string{
		`subagent(`,
		`"agent": "runbook-searcher"`,
		`/akmatori/runbooks/`,
		"Cap total runbook-searcher invocations at 3",
		"up to 2 retries",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrependGuidance() missing %q\nfull output:\n%s", want, out)
		}
	}

	if !strings.Contains(out, "test task") {
		t.Errorf("PrependGuidance() should append the user task, got:\n%s", out)
	}
}

// TestPrependGuidance_NoLegacyQMDOrMemoryToolReferences pins the absence of
// the retired gateway tool names after the subagent migration.
func TestPrependGuidance_NoLegacyQMDOrMemoryToolReferences(t *testing.T) {
	out := PrependGuidance("test task")
	for _, banned := range []string{
		"qmd.query",
		"qmd.get",
		"memory.search",
		"memory.get",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("PrependGuidance() must not contain legacy tool reference %q\nfull output:\n%s", banned, out)
		}
	}
}

// TestPrependGuidance_SingleRunbookSearcherInvocation pins the structural
// invariant that the user-turn reminder shows exactly ONE
// subagent({"agent": "runbook-searcher", ...}) example. The substring
// assertions above would still pass if a future edit split the example into
// multiple per-retry invocations.
func TestPrependGuidance_SingleRunbookSearcherInvocation(t *testing.T) {
	out := PrependGuidance("test task")

	if got := strings.Count(out, `"agent": "runbook-searcher"`); got != 1 {
		t.Errorf("expected exactly 1 subagent({\"agent\": \"runbook-searcher\"...}) example in guidance, got %d", got)
	}
}

// TestPrependGuidance_PassesFullAlertTextToSubagent pins the conditional
// clause that asks the agent to pass the full "Original alert text:" block
// verbatim as the runbook-searcher subagent task. The subagent extracts
// distinctive keywords on its own, so the user-turn reminder does not embed
// example phrases. Stays in sync with the equivalent assertion in
// internal/database/prompt_test.go so the user-turn reminder and the system
// prompt give the same instruction.
//
// Asserted as a single normalized clause (not independent substrings) so the
// rule can't be silently weakened by scattering the tokens.
func TestPrependGuidance_PassesFullAlertTextToSubagent(t *testing.T) {
	normalized := strings.Join(strings.Fields(PrependGuidance("test task")), " ")
	want := `When the prompt contains an "Original alert text:" block, pass that block verbatim as the "task"`
	if !strings.Contains(normalized, want) {
		t.Errorf("PrependGuidance() missing full-alert-text pass-through clause\nwant: %s\ngot (normalized):\n%s", want, normalized)
	}
}
