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

// TestPrependGuidance_DelegatesToMemorySearcherSubagent pins that the
// user-turn reminder also asks the agent to invoke the memory-searcher
// subagent right after the runbook search, with the same alert text. Kept
// in sync with the memory-search section of DefaultIncidentManagerPrompt.
func TestPrependGuidance_DelegatesToMemorySearcherSubagent(t *testing.T) {
	out := PrependGuidance("test task")
	for _, want := range []string{
		`"agent": "memory-searcher"`,
		`/akmatori/memory/`,
		"Cap total memory-searcher invocations at 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrependGuidance() missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestPrependGuidance_SingleMemorySearcherInvocation pins that the reminder
// shows exactly ONE subagent({"agent": "memory-searcher", ...}) example.
func TestPrependGuidance_SingleMemorySearcherInvocation(t *testing.T) {
	out := PrependGuidance("test task")
	if got := strings.Count(out, `"agent": "memory-searcher"`); got != 1 {
		t.Errorf("expected exactly 1 subagent({\"agent\": \"memory-searcher\"...}) example in guidance, got %d", got)
	}
}

// TestPrependGuidance_MemorySearchAfterRunbookSearch verifies that the
// runbook-search reminder appears before the memory-search reminder, and
// that both appear before the task body.
func TestPrependGuidance_MemorySearchAfterRunbookSearch(t *testing.T) {
	out := PrependGuidance("test task")
	runbookIdx := strings.Index(out, `"agent": "runbook-searcher"`)
	memoryIdx := strings.Index(out, `"agent": "memory-searcher"`)
	taskIdx := strings.Index(out, "test task")

	if runbookIdx == -1 || memoryIdx == -1 || taskIdx == -1 {
		t.Fatalf("missing required sections: runbook=%d memory=%d task=%d", runbookIdx, memoryIdx, taskIdx)
	}
	if runbookIdx >= memoryIdx {
		t.Errorf("runbook reminder must appear before memory reminder (runbook=%d memory=%d)", runbookIdx, memoryIdx)
	}
	if memoryIdx >= taskIdx {
		t.Errorf("memory reminder must appear before the task body (memory=%d task=%d)", memoryIdx, taskIdx)
	}
}
