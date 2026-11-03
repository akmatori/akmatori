package database

import (
	"strings"
	"testing"
)

// retiredKnowledgeRefs are the subagent names and agent-side paths that the
// gateway knowledge tools replaced. No default prompt may name them.
var retiredKnowledgeRefs = []string{
	"subagent(", "runbook-searcher", "memory-searcher", "memory-writer",
	"/akmatori/runbooks", "/akmatori/memory", "Action: upsert", "Action: delete",
}

func TestDefaultPrompts_NoRetiredKnowledgeRefs(t *testing.T) {
	prompts := map[string]string{
		"incident-manager":      DefaultIncidentManagerPrompt,
		"cron-agent":            DefaultCronAgentPrompt,
		"proposal-editor":       DefaultProposalEditorPrompt,
		"Dreaming":              dreamingCronPrompt,
		"improvement-evaluator": improvementEvaluatorCronPrompt,
	}
	for name, p := range prompts {
		for _, bad := range retiredKnowledgeRefs {
			if strings.Contains(p, bad) {
				t.Errorf("%s prompt still contains %q", name, bad)
			}
		}
	}
}

// TestDefaultIncidentManagerPrompt_KnowledgeSteps pins the mandatory order:
// runbooks.search, then memory.search, both before any infrastructure tool,
// then the change check and the skills.
func TestDefaultIncidentManagerPrompt_KnowledgeSteps(t *testing.T) {
	p := DefaultIncidentManagerPrompt
	runbookIdx := strings.Index(p, `gateway_call("runbooks.search"`)
	memoryIdx := strings.Index(p, `gateway_call("memory.search"`)
	changeIdx := strings.Index(p, "**Check recent changes**")
	if runbookIdx == -1 || memoryIdx == -1 || !(runbookIdx < memoryIdx && memoryIdx < changeIdx) {
		t.Fatalf("order runbooks.search < memory.search < change check broken (%d %d %d)", runbookIdx, memoryIdx, changeIdx)
	}
	for _, want := range []string{
		"MANDATORY - Search runbooks FIRST",
		"Original alert text",
		"retry up to 2 times",
		`gateway_call("runbooks.get"`,
		`gateway_call("memory.get"`,
		"PRIMARY investigation guide",
		`"scopes": [`,
		`gateway_call("memory.write"`,
		"Memory contradicted:",
		"Operator asked to remember:",
		"post-incident pass",
		"unverified",
		"Memory used:",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("incident-manager prompt missing %q", want)
		}
	}
}

func TestDefaultCronAgentPrompt_KnowledgeTools(t *testing.T) {
	for _, want := range []string{`gateway_call("runbooks.search"`, `gateway_call("memory.search"`, `gateway_call("memory.list"`, `gateway_call("memory.write"`, `gateway_call("memory.delete"`} {
		if !strings.Contains(DefaultCronAgentPrompt, want) {
			t.Errorf("cron prompt missing %q", want)
		}
	}
}

func TestDreamingAndEvaluatorPrompts_UseMemoryList(t *testing.T) {
	if !strings.Contains(dreamingCronPrompt, `gateway_call("memory.list", {"scopes": ["global"], "since"`) || !strings.Contains(dreamingCronPrompt, "next_cursor") {
		t.Error("Dreaming must enumerate with memory.list since + next_cursor")
	}
	if !strings.Contains(improvementEvaluatorCronPrompt, `gateway_call("memory.list", {"type": "feedback"`) {
		t.Error("evaluator must list recent feedback with memory.list")
	}
}

// TestDreamingPrompt_EvidenceRules pins the consolidation rules of the
// evidence grades: lowest grade wins, confirmed is never written, a
// contradiction needs a specific newer observation.
func TestDreamingPrompt_EvidenceRules(t *testing.T) {
	for _, want := range []string{"LOWEST evidence", `Never write "evidence": "confirmed"`, "Memory contradicted:", `"evidence_note"`, "stays unverified", `"unused_since"`, "90 days", "Never retire a confirmed"} {
		if !strings.Contains(dreamingCronPrompt, want) {
			t.Errorf("Dreaming prompt missing %q", want)
		}
	}
}

func TestLegacySystemCronPrompts_ExcludeCurrent(t *testing.T) {
	current := map[string]string{dreamingCronName: dreamingCronPrompt, improvementEvaluatorCronName: improvementEvaluatorCronPrompt}
	for name, olds := range legacySystemCronPrompts {
		if len(olds) == 0 {
			t.Errorf("%s: no legacy prompts", name)
		}
		seen := map[string]bool{}
		for _, old := range olds {
			if old == current[name] {
				t.Errorf("%s: the current default is listed as legacy", name)
			}
			if seen[old] {
				t.Errorf("%s: a legacy prompt is listed twice", name)
			}
			seen[old] = true
		}
	}
}

// TestDefaultIncidentManagerPrompt_ChangeCheckAfterMemory pins the recent-
// changes step: after the memory search, before loading skills, through the
// built-in events tool.
func TestDefaultIncidentManagerPrompt_ChangeCheckAfterMemory(t *testing.T) {
	memoryIdx := strings.Index(DefaultIncidentManagerPrompt, "MANDATORY - Search cross-incident memory next")
	changeIdx := strings.Index(DefaultIncidentManagerPrompt, "**Check recent changes**")
	skillsIdx := strings.Index(DefaultIncidentManagerPrompt, "Load relevant skills")
	if changeIdx == -1 || !(memoryIdx < changeIdx && changeIdx < skillsIdx) {
		t.Fatalf("change check must sit between memory search and skills (memory=%d change=%d skills=%d)", memoryIdx, changeIdx, skillsIdx)
	}
	if !strings.Contains(DefaultIncidentManagerPrompt, `gateway_call("events.search", {"kind": "change"`) {
		t.Error("change check must show the events.search call")
	}
}

func TestDefaultCronAgentPrompt_ReportsProblemsAsAlerts(t *testing.T) {
	if !strings.Contains(DefaultCronAgentPrompt, `gateway_call("events.report_alert"`) {
		t.Error("cron prompt must show the events.report_alert call")
	}
	reportIdx := strings.Index(DefaultCronAgentPrompt, "**Report problems as alerts**")
	summaryIdx := strings.Index(DefaultCronAgentPrompt, "**Produce the final summary**")
	if reportIdx == -1 || reportIdx > summaryIdx {
		t.Error("alert reporting must come before the final summary")
	}
}
