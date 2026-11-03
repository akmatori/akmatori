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

// The one-time import ignores the generation gate: under the old per-run
// ingest, agent files were discarded while generation was off, so the files
// left at upgrade are hand edits, and the sync after the import would delete
// them.
func TestIngestFromDisk_GenerationOffStillImports(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	setMemoryGeneration(t, svc, false)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, dir, "hand-edit", "operator note", MemoryTypeFact, MemoryScopeGlobal, "", "body")
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := mustFindMemory(svc, MemoryScopeGlobal, "hand-edit"); err != nil {
		t.Errorf("hand edit not imported: %v", err)
	}
}

// An invalid file is skipped but kept in the backup; a database failure
// returns an error and leaves every file on disk for the retry.
func TestIngestFromDisk_BackupAndFailureKeepFiles(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, dir, "good", "fine", MemoryTypeFact, MemoryScopeGlobal, "", "body")
	writeMemoryFile(t, dir, "bad", "", MemoryTypeFact, MemoryScopeGlobal, "", "no description")

	// Break the database: the upsert fails, so no sync may run.
	if err := svc.db.Migrator().RenameTable("memories", "memories_away"); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestFromDisk(context.Background()); err == nil {
		t.Fatal("want an error when the upsert fails")
	}
	for _, f := range []string{"good.md", "bad.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s removed after a failed import: %v", f, err)
		}
	}
	backup := filepath.Join(filepath.Dir(svc.MemoryDir()), memoryImportBackupDir, MemoryScopeGlobal)
	for _, f := range []string{"good.md", "bad.md"} {
		if _, err := os.Stat(filepath.Join(backup, f)); err != nil {
			t.Errorf("%s not in the backup: %v", f, err)
		}
	}

	// Retry after the database is back: good imports, bad is skipped.
	if err := svc.db.Migrator().RenameTable("memories_away", "memories"); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := mustFindMemory(svc, MemoryScopeGlobal, "good"); err != nil {
		t.Errorf("good not imported on retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "bad.md")); err != nil {
		t.Errorf("the backup must keep the invalid file: %v", err)
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
	svc := &SkillService{db: mem.db, memoryDir: mem.MemoryDir()}

	on := svc.renderMemoryRecallSection(MemoryScopeGlobal)
	if !strings.Contains(on, "### Record durable findings") || !strings.Contains(on, `gateway_call("memory.write"`) {
		t.Fatalf("gate unset should keep the memory.write guidance:\n%s", on)
	}

	setMemoryGeneration(t, mem, false)
	off := svc.renderMemoryRecallSection(MemoryScopeGlobal)
	if strings.Contains(off, `gateway_call("memory.write"`) || strings.Contains(off, "### Record durable findings") {
		t.Errorf("gate off still tells the agent to write:\n%s", off)
	}
	if !strings.Contains(off, memoryGenerationOffInstruction) {
		t.Errorf("gate off is missing the off instruction:\n%s", off)
	}
	if !strings.Contains(off, memoryRecallInstruction) {
		t.Errorf("recall must stay available with the gate off:\n%s", off)
	}
}

func mustFindMemory(svc *MemoryService, scope, name string) error {
	var m database.Memory
	return svc.db.Where("scope = ? AND name = ?", scope, name).Take(&m).Error
}
