package executor

import (
	"strings"
	"testing"
)

// TestPrependGuidance_KnowledgeTools pins the user-turn reminder: runbooks
// first, then memory with the same query, through the gateway tools, with
// the same retry budget as DefaultIncidentManagerPrompt.
func TestPrependGuidance_KnowledgeTools(t *testing.T) {
	out := PrependGuidance("test task")
	for _, want := range []string{
		`gateway_call("runbooks.search"`,
		`gateway_call("runbooks.get"`,
		`gateway_call("memory.search"`,
		`gateway_call("memory.get"`,
		"Original alert text",
		"retry up to 2 times",
		"PRIMARY",
		"test task",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrependGuidance() missing %q\nfull output:\n%s", want, out)
		}
	}
	if strings.Index(out, `"runbooks.search"`) > strings.Index(out, `"memory.search"`) {
		t.Error("runbook search must come before memory search")
	}
	if strings.Count(out, `gateway_call("runbooks.search"`) != 1 || strings.Count(out, `gateway_call("memory.search"`) != 1 {
		t.Error("expected exactly one example call per search")
	}
	if !strings.HasPrefix(out, "Current time: 20") {
		t.Errorf("time header broken: %q", out[:40])
	}
}

// TestPrependGuidance_NoRetiredReferences pins the absence of the retired
// subagents, agent-side paths and QMD tools.
func TestPrependGuidance_NoRetiredReferences(t *testing.T) {
	out := PrependGuidance("test task")
	for _, banned := range []string{"subagent(", "runbook-searcher", "memory-searcher", "/akmatori/runbooks", "/akmatori/memory", "qmd."} {
		if strings.Contains(out, banned) {
			t.Errorf("PrependGuidance() must not contain %q", banned)
		}
	}
}
