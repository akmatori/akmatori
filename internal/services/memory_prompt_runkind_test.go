package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// TestRenderMemoryRecallSection_RunKinds: an alert investigation's
// AGENTS.md tells the agent not to write memory and what to put in the
// final answer; request work keeps the memory.write guidance; a shared
// SKILL.md carries the neutral note plus the guidance.
func TestRenderMemoryRecallSection_RunKinds(t *testing.T) {
	svc := newSkillServiceForPromptTest(t)

	incident := svc.renderMemoryRecallSectionFor(MemoryScopeGlobal, scopeKindIncident)
	for _, want := range []string{"### Record durable findings", "do NOT call memory.write", "post-incident pass", "Operator asked to remember:", "Memory contradicted:", `gateway_call("memory.search"`} {
		if !strings.Contains(incident, want) {
			t.Errorf("incident section missing %q:\n%s", want, incident)
		}
	}
	if strings.Contains(incident, `gateway_call("memory.write"`) {
		t.Errorf("incident section carries the write guidance:\n%s", incident)
	}

	request := svc.renderMemoryRecallSectionFor(MemoryScopeGlobal, scopeKindRequest)
	if !strings.Contains(request, `gateway_call("memory.write", {"scope": "global"`) || strings.Contains(request, "do NOT call memory.write") || strings.Contains(request, memorySharedWriteNote) {
		t.Errorf("request section:\n%s", request)
	}

	shared := svc.renderMemoryRecallSection("redis-skill")
	if !strings.Contains(shared, memorySharedWriteNote) || !strings.Contains(shared, `gateway_call("memory.write", {"scope": "redis-skill"`) {
		t.Errorf("shared section:\n%s", shared)
	}
}

// TestPrepareScopeDir_RunKinds: SpawnIncidentManager renders the incident
// AGENTS.md, PrepareRequestScope the request one, and EnsureScopeDir picks
// the kind from the incidents table.
func TestPrepareScopeDir_RunKinds(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	incidentUUID := spawnAlertIncident(t, svc)
	agents := readAgentsMd(t, svc, incidentUUID)
	// The root prompt still names memory.write for request work; the
	// incident AGENTS.md must not carry the write guidance template.
	if !strings.Contains(agents, "do NOT call memory.write") || strings.Contains(agents, writeTemplateMarker) {
		t.Errorf("incident AGENTS.md:\n%s", agents)
	}

	scope, _, err := svc.PrepareRequestScope("incident-manager", "")
	if err != nil {
		t.Fatal(err)
	}
	agents = readAgentsMd(t, svc, scope)
	if strings.Contains(agents, "do NOT call memory.write") || !strings.Contains(agents, writeTemplateMarker) {
		t.Errorf("request AGENTS.md:\n%s", agents)
	}

	// A follow-up turn on the incident (Slack reply) after retention removed
	// the directory renders the incident kind again.
	if err := os.RemoveAll(filepath.Join(svc.incidentsDir, incidentUUID)); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureScopeDir("incident-manager", incidentUUID); err != nil {
		t.Fatal(err)
	}
	if agents = readAgentsMd(t, svc, incidentUUID); !strings.Contains(agents, "do NOT call memory.write") {
		t.Errorf("EnsureScopeDir on an incident lost the incident kind:\n%s", agents)
	}
	if err := os.RemoveAll(filepath.Join(svc.incidentsDir, scope)); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureScopeDir("incident-manager", scope); err != nil {
		t.Fatal(err)
	}
	if agents = readAgentsMd(t, svc, scope); strings.Contains(agents, "do NOT call memory.write") {
		t.Errorf("EnsureScopeDir on request work rendered the incident kind:\n%s", agents)
	}
	var n int64
	db.Model(&database.Incident{}).Where("uuid = ?", scope).Count(&n)
	if n != 0 {
		t.Error("request scope has an incidents row")
	}
}

// writeTemplateMarker is the start of the memory.write example of the
// write guidance (memoryWriteInstructionTemplate) in the global scope.
const writeTemplateMarker = `gateway_call("memory.write", {"scope": "global"`

func readAgentsMd(t *testing.T, svc *SkillService, scope string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(svc.incidentsDir, scope, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
