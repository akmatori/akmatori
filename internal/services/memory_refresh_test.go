package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

type countingRefresher struct{ syncs int }

func (c *countingRefresher) SyncMemoryFiles() error { c.syncs++; return nil }

func TestRefreshAfterMemoryChange(t *testing.T) {
	db := setupSkillTestDB(t)
	if err := db.AutoMigrate(&database.Memory{}); err != nil {
		t.Fatal(err)
	}
	svc := newTestSkillService(t, db)
	ref := &countingRefresher{}
	svc.SetMemoryRefresher(ref)
	db.Create(&database.Skill{Name: "linux-admin", Description: "Linux", Enabled: true})

	svc.RefreshAfterMemoryChange("r0")
	if ref.syncs != 1 {
		t.Fatalf("first refresh: syncs = %d", ref.syncs)
	}
	svc.RefreshAfterMemoryChange("r1")
	if ref.syncs != 1 {
		t.Errorf("refresh without a memory change synced again")
	}

	// A gateway write lands in the database; the next run end picks it up.
	m := database.Memory{Scope: "linux-admin", Name: "journald-cap", Type: "fact", Description: "journald is capped at 2G", Body: "b", CreatedBy: "agent"}
	db.Create(&m)
	svc.RefreshAfterMemoryChange("r2")
	if ref.syncs != 2 {
		t.Errorf("write not detected: syncs = %d", ref.syncs)
	}
	md, err := os.ReadFile(filepath.Join(svc.GetSkillDir("linux-admin"), "SKILL.md"))
	if err != nil || !strings.Contains(string(md), "journald is capped at 2G") {
		t.Errorf("SKILL.md manifest not refreshed: %v\n%s", err, md)
	}

	// A delete is a change too.
	db.Delete(&m)
	svc.RefreshAfterMemoryChange("r3")
	if ref.syncs != 3 {
		t.Errorf("delete not detected: syncs = %d", ref.syncs)
	}
}

type fakeDiskIngester struct {
	calls int
	err   error
}

func (f *fakeDiskIngester) IngestFromDisk(context.Context) error { f.calls++; return f.err }

func TestIngestDiskMemoriesOnce(t *testing.T) {
	db := setupSkillTestDB(t)
	if err := db.AutoMigrate(&database.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })

	failing := &fakeDiskIngester{err: errors.New("disk")}
	IngestDiskMemoriesOnce(failing)
	if database.HasSystemSetting(SystemSettingMemoryDiskIngestedAt) {
		t.Fatal("a failed import must not be marked done")
	}
	ok := &fakeDiskIngester{}
	IngestDiskMemoriesOnce(ok)
	IngestDiskMemoriesOnce(ok)
	if ok.calls != 1 {
		t.Errorf("import ran %d times, want once", ok.calls)
	}
}
