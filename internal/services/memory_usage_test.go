package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestParseMemoryUsed(t *testing.T) {
	cases := map[string]struct {
		text string
		want []string
	}{
		"inline":       {"Findings...\n\nMemory used: [[global/edge-nic]], [[linux-admin/disk-full]]", []string{"global/edge-nic", "linux-admin/disk-full"}},
		"list below":   {"**Memory used:**\n- [[edge-nic]]\n- [[global/disk-full]]\n\nNot this: [[other]]", []string{"edge-nic", "global/disk-full"}},
		"none":         {"Memory used: none", nil},
		"no block":     {"We used [[edge-nic]] above", nil},
		"dedupe":       {"Memory used: [[a]] [[a]]\n[[a]] [[b]]", []string{"a", "b"}},
		"stops at gap": {"Memory used:\n[[a]]\n\n[[b]]", []string{"a"}},
	}
	for name, c := range cases {
		got := ParseMemoryUsed(c.text)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func usageTestDB(t *testing.T) (*gorm.DB, *MemoryService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&database.Memory{}, &database.MemoryUse{}, &database.GeneralSettings{}); err != nil {
		t.Fatal(err)
	}
	orig := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = orig })
	return db, &MemoryService{db: db, memoryDir: t.TempDir()}
}

// TestRecordCitedMemories: cited refs become use rows and counters, unknown
// names are ignored, a bare name resolves to global first.
func TestRecordCitedMemories(t *testing.T) {
	db, mem := usageTestDB(t)
	db.Create(&database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "d", Body: "b"})
	db.Create(&database.Memory{Scope: "linux-admin", Name: "edge-nic", Type: "fact", Description: "d", Body: "b"})
	db.Create(&database.Memory{Scope: "redis", Name: "port", Type: "fact", Description: "d", Body: "b"})

	n := mem.RecordCitedMemories("run-1", "inc-1", "Report.\n\nMemory used: [[edge-nic]], [[redis/port]], [[nope]], [[bad name]]")
	if n != 2 {
		t.Fatalf("recorded %d, want 2", n)
	}
	var uses []database.MemoryUse
	db.Order("memory_id").Find(&uses)
	if len(uses) != 2 || uses[0].Kind != database.MemoryUseCited || uses[0].RunID != "run-1" || uses[0].IncidentUUID != "inc-1" {
		t.Fatalf("uses: %+v", uses)
	}
	var global, skill database.Memory
	db.Where("scope = ? AND name = ?", "global", "edge-nic").Take(&global)
	if global.UseCount != 1 || global.LastUsedAt == nil {
		t.Errorf("global/edge-nic: count %d last %v", global.UseCount, global.LastUsedAt)
	}
	db.Where("scope = ? AND name = ?", "linux-admin", "edge-nic").Take(&skill)
	if skill.UseCount != 0 {
		t.Errorf("bare name resolved to the skill scope: %+v", skill)
	}
	if mem.RecordCitedMemories("run-2", "", "Memory used: none") != 0 {
		t.Error("none recorded something")
	}

	// A search hit moves last_used_at only.
	var port database.Memory
	db.Where("scope = ? AND name = ?", "redis", "port").Take(&port)
	before := port.UseCount
	if err := RecordMemoryUses(db, []uint{port.ID}, "run-3", "", database.MemoryUseSearch, time.Now()); err != nil {
		t.Fatal(err)
	}
	var after database.Memory
	db.Where("id = ?", port.ID).Take(&after)
	if after.UseCount != before || after.LastUsedAt == nil {
		t.Errorf("search hit changed use_count: %+v", after)
	}

	// The memories an incident's runs used, newest use first.
	used, err := mem.ListMemories(MemoryFilter{UsedInIncident: "inc-1"})
	if err != nil || len(used) != 2 {
		t.Fatalf("used in inc-1: %d %v", len(used), err)
	}
	if used, _ := mem.ListMemories(MemoryFilter{UsedInIncident: "inc-1", Scope: "redis"}); len(used) != 1 || used[0].Name != "port" {
		t.Errorf("scope filter: %+v", used)
	}

	// Deleting the memory removes its uses.
	if err := mem.DeleteMemory(port.ID); err != nil {
		t.Fatal(err)
	}
	var left int64
	db.Model(&database.MemoryUse{}).Where("memory_id = ?", port.ID).Count(&left)
	if left != 0 {
		t.Errorf("uses left after delete: %d", left)
	}
}

// TestManifestRank_UsedMemoryStaysInCap: within the cap, a memory used
// recently outranks a newer unused one, and a brand-new memory is visible.
func TestManifestRank_UsedMemoryStaysInCap(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	used := now.Add(-time.Hour)
	entries := []database.Memory{
		{Name: "old-used", Type: "fact", UpdatedAt: old, LastUsedAt: &used},
		{Name: "mid-unused", Type: "fact", UpdatedAt: now.Add(-10 * 24 * time.Hour)},
		{Name: "brand-new", Type: "fact", UpdatedAt: now},
	}
	limited, truncated := limitManifestEntries(entries, 2)
	if truncated != 1 || len(limited) != 2 {
		t.Fatalf("limited %d truncated %d", len(limited), truncated)
	}
	if limited[0].Name != "brand-new" || limited[1].Name != "old-used" {
		t.Errorf("order: %s, %s", limited[0].Name, limited[1].Name)
	}
}

// TestFinishRun_RecordsCitedMemories: the final answer's block is recorded
// on the run's incident.
func TestFinishRun_RecordsCitedMemories(t *testing.T) {
	db := setupIncidentTestDB(t)
	if err := db.AutoMigrate(&database.Memory{}, &database.MemoryUse{}); err != nil {
		t.Fatal(err)
	}
	svc := newIncidentTestService(t, db)
	mem := &MemoryService{db: db, memoryDir: t.TempDir()}
	svc.SetMemoryCiter(mem)
	db.Create(&database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "d", Body: "b"})
	incidentUUID := spawnAlertIncident(t, svc)
	runID, err := svc.BeginRun(RunStart{IncidentUUID: incidentUUID, RootSkill: "incident-manager"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishRun(runID, RunOutcome{Response: "Root cause: NIC.\n\nMemory used: [[global/edge-nic]]"}); err != nil {
		t.Fatal(err)
	}
	var use database.MemoryUse
	if err := db.Where("run_id = ?", runID).Take(&use).Error; err != nil {
		t.Fatalf("no cited use: %v", err)
	}
	if use.IncidentUUID != incidentUUID || use.Kind != database.MemoryUseCited {
		t.Errorf("use: %+v", use)
	}
	_ = context.Background()
}
