package services

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// TestMemoryValidationFixture runs the validation cases shared with the
// gateway's memory.write tool (tests/fixtures/memory_validation_cases.json).
// "api" is the expected outcome of MemoryService.validate.
func TestMemoryValidationFixture(t *testing.T) {
	b, err := os.ReadFile("../../tests/fixtures/memory_validation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name       string            `json:"name"`
			Input      map[string]string `json:"input"`
			API        string            `json:"api"`
			ExpectType string            `json:"expect_type"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 10 {
		t.Fatalf("fixture has %d cases", len(f.Cases))
	}
	svc := &MemoryService{}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			m := &database.Memory{Scope: c.Input["scope"], Name: c.Input["name"], Type: c.Input["type"], Description: c.Input["description"], Body: c.Input["body"]}
			err := svc.validate(m)
			if (err == nil) != (c.API == "ok") {
				t.Fatalf("validate err=%v, want %s", err, c.API)
			}
			if err == nil && c.ExpectType != "" && m.Type != c.ExpectType {
				t.Errorf("type = %q, want %q", m.Type, c.ExpectType)
			}
		})
	}
}

func TestUpsertByName_OperatorTakesOwnership(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	agent := &database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "d", Body: "agent text", CreatedBy: MemoryCreatedByAgent}
	if _, err := svc.UpsertByName(agent); err != nil {
		t.Fatal(err)
	}
	// Another agent-style upsert keeps the authorship.
	again := &database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "d", Body: "agent text 2", CreatedBy: MemoryCreatedByAgent}
	saved, err := svc.UpsertByName(again)
	if err != nil || saved.CreatedBy != MemoryCreatedByAgent {
		t.Fatalf("agent upsert: %+v %v", saved, err)
	}
	// An operator upsert (proposal approval) takes ownership, even with the same content.
	op := &database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "d", Body: "agent text 2", CreatedBy: MemoryCreatedByOperator}
	saved, err = svc.UpsertByName(op)
	if err != nil || saved.CreatedBy != MemoryCreatedByOperator {
		t.Fatalf("operator upsert: %+v %v", saved, err)
	}
}
