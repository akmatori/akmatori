package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// setMemoryGeneration adds the general_settings table to the test DB and
// stores the gate. The table is dropped on cleanup: database.DB is global and
// outlives the test, and later tests expect the gate's fail-open default.
func setMemoryGeneration(t *testing.T, svc *MemoryService, enabled bool) {
	t.Helper()
	if err := svc.db.AutoMigrate(&database.GeneralSettings{}); err != nil {
		t.Fatalf("migrate general_settings: %v", err)
	}
	t.Cleanup(func() { _ = svc.db.Migrator().DropTable(&database.GeneralSettings{}) })
	if err := svc.db.Create(&database.GeneralSettings{MemoryGenerationEnabled: &enabled}).Error; err != nil {
		t.Fatalf("seed general_settings: %v", err)
	}
}

func TestIngestFromDisk_GenerationOffDiscardsAgentWrites(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	kept, err := svc.CreateMemory(validMemory("kept-fact"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doomed, err := svc.CreateMemory(validMemory("not-deleted"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	setMemoryGeneration(t, svc, false)

	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	// The agent creates a memory, rewrites one, and tombstones another.
	writeMemoryFile(t, dir, "agent-new", "new fact", MemoryTypeFact, MemoryScopeGlobal, "inc-1", "new body")
	writeMemoryFile(t, dir, "kept-fact", "agent rewrite", MemoryTypeFact, MemoryScopeGlobal, "inc-1", "agent body")
	writeTombstoneFile(t, dir, "not-deleted")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 2 {
		t.Fatalf("expected the 2 operator rows only, got %d: %+v", len(mems), mems)
	}
	for _, m := range mems {
		if m.ID == kept.ID && m.Description != kept.Description {
			t.Errorf("agent rewrite reached the DB: %+v", m)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-new.md")); !os.IsNotExist(err) {
		t.Errorf("agent-created file should be discarded; stat err=%v", err)
	}
	if file := readFile(t, filepath.Join(dir, "kept-fact.md")); strings.Contains(file, "agent rewrite") {
		t.Errorf("agent rewrite should be reverted on disk:\n%s", file)
	}
	if file := readFile(t, filepath.Join(dir, doomed.Name+".md")); strings.Contains(file, "deleted: true") {
		t.Errorf("tombstone should be reverted on disk:\n%s", file)
	}
}

func TestIngestFromDisk_GenerationOnIngests(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	setMemoryGeneration(t, svc, true)
	writeMemoryFile(t, filepath.Join(svc.MemoryDir(), MemoryScopeGlobal), "agent-new", "new fact", MemoryTypeFact, MemoryScopeGlobal, "inc-1", "new body")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal); len(mems) != 1 {
		t.Fatalf("expected the agent memory ingested, got %+v", mems)
	}
}

func TestRenderMemoryRecallSection_GenerationOff(t *testing.T) {
	mem := setupMemoryServiceTest(t)
	svc := &SkillService{memoryDir: mem.MemoryDir()}

	on := svc.renderMemoryRecallSection(MemoryScopeGlobal, "inc-1")
	if !strings.Contains(on, "### Record durable findings") || !strings.Contains(on, `"agent": "memory-writer"`) {
		t.Fatalf("gate unset should keep the memory-writer guidance:\n%s", on)
	}

	setMemoryGeneration(t, mem, false)
	off := svc.renderMemoryRecallSection(MemoryScopeGlobal, "inc-1")
	if strings.Contains(off, `"agent": "memory-writer"`) || strings.Contains(off, "### Record durable findings") {
		t.Errorf("gate off still tells the agent to write:\n%s", off)
	}
	if !strings.Contains(off, memoryGenerationOffInstruction) {
		t.Errorf("gate off is missing the off instruction:\n%s", off)
	}
	if !strings.Contains(off, memoryRecallInstruction) {
		t.Errorf("recall must stay available with the gate off:\n%s", off)
	}
}
