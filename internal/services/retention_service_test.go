package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRetentionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.Incident{},
		&database.RetentionSettings{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	database.DB = db
	return db
}

func createExpiredIncident(t *testing.T, db *gorm.DB, uuid string, dataDir string, daysOld int) {
	t.Helper()
	completedAt := time.Now().AddDate(0, 0, -daysOld)
	workDir := filepath.Join(dataDir, uuid)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create incident dir: %v", err)
	}
	// Write a file so we can verify cleanup
	if err := os.WriteFile(filepath.Join(workDir, "log.txt"), []byte("test log data"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	incident := &database.Incident{
		UUID:        uuid,
		Source:      "test",
		Status:      database.IncidentStatusCompleted,
		WorkingDir:  workDir,
		CompletedAt: &completedAt,
	}
	if err := db.Create(incident).Error; err != nil {
		t.Fatalf("failed to create incident: %v", err)
	}
}

func TestRunCleanup_ExpiredIncidents(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	// Create retention settings with 30-day retention
	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired incident (60 days old) and a recent one (10 days old)
	createExpiredIncident(t, db, "expired-uuid-1", dataDir, 60)
	createExpiredIncident(t, db, "recent-uuid-1", dataDir, 10)

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired incident deleted, got %d", result.ExpiredIncidentsDeleted)
	}
	if result.ExpiredDirsDeleted != 1 {
		t.Errorf("expected 1 expired dir deleted, got %d", result.ExpiredDirsDeleted)
	}
	if result.ExpiredBytesFreed <= 0 {
		t.Errorf("expected bytes freed > 0, got %d", result.ExpiredBytesFreed)
	}

	// Verify expired directory is gone
	if _, err := os.Stat(filepath.Join(dataDir, "expired-uuid-1")); !os.IsNotExist(err) {
		t.Error("expected expired directory to be deleted")
	}

	// Verify recent directory still exists
	if _, err := os.Stat(filepath.Join(dataDir, "recent-uuid-1")); err != nil {
		t.Error("expected recent directory to still exist")
	}

	// Verify DB record is gone for expired, still exists for recent
	var count int64
	db.Model(&database.Incident{}).Where("uuid = ?", "expired-uuid-1").Count(&count)
	if count != 0 {
		t.Error("expected expired incident DB record to be deleted")
	}
	db.Model(&database.Incident{}).Where("uuid = ?", "recent-uuid-1").Count(&count)
	if count != 1 {
		t.Error("expected recent incident DB record to still exist")
	}
}

func TestRunCleanup_FailedIncidentsAlsoCleanedUp(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired failed incident
	completedAt := time.Now().AddDate(0, 0, -60)
	workDir := filepath.Join(dataDir, "failed-uuid-1")
	os.MkdirAll(workDir, 0755)
	db.Create(&database.Incident{
		UUID:        "failed-uuid-1",
		Source:      "test",
		Status:      database.IncidentStatusFailed,
		WorkingDir:  workDir,
		CompletedAt: &completedAt,
	})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired incident deleted, got %d", result.ExpiredIncidentsDeleted)
	}
}

func TestRunCleanup_RunningIncidentsNotDeleted(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an old running incident - should NOT be deleted
	workDir := filepath.Join(dataDir, "running-uuid-1")
	os.MkdirAll(workDir, 0755)
	db.Create(&database.Incident{
		UUID:       "running-uuid-1",
		Source:     "test",
		Status:     database.IncidentStatusRunning,
		WorkingDir: workDir,
	})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.ExpiredIncidentsDeleted != 0 {
		t.Errorf("expected 0 expired incidents deleted, got %d", result.ExpiredIncidentsDeleted)
	}

	// Verify directory still exists
	if _, err := os.Stat(workDir); err != nil {
		t.Error("expected running incident directory to still exist")
	}
}

func TestRunCleanup_OrphanedDirectories(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an orphaned directory (no matching DB record)
	orphanDir := filepath.Join(dataDir, "orphan-uuid-1")
	os.MkdirAll(orphanDir, 0755)
	os.WriteFile(filepath.Join(orphanDir, "data.txt"), []byte("orphaned data"), 0644)

	// Create a directory with a matching DB record (should not be deleted)
	matchedDir := filepath.Join(dataDir, "matched-uuid-1")
	os.MkdirAll(matchedDir, 0755)
	db.Create(&database.Incident{
		UUID:       "matched-uuid-1",
		Source:     "test",
		Status:     database.IncidentStatusRunning,
		WorkingDir: matchedDir,
	})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.OrphanedDirsDeleted != 1 {
		t.Errorf("expected 1 orphaned dir deleted, got %d", result.OrphanedDirsDeleted)
	}
	if result.OrphanedBytesFreed <= 0 {
		t.Errorf("expected orphaned bytes freed > 0, got %d", result.OrphanedBytesFreed)
	}

	// Verify orphan is gone
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Error("expected orphaned directory to be deleted")
	}

	// Verify matched directory still exists
	if _, err := os.Stat(matchedDir); err != nil {
		t.Error("expected matched directory to still exist")
	}
}

func TestRunCleanup_DisabledRetention(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: false, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired incident
	createExpiredIncident(t, db, "expired-uuid-1", dataDir, 60)

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	// Nothing should be deleted when retention is disabled
	if result.ExpiredIncidentsDeleted != 0 {
		t.Errorf("expected 0 deleted when disabled, got %d", result.ExpiredIncidentsDeleted)
	}

	// Directory should still exist
	if _, err := os.Stat(filepath.Join(dataDir, "expired-uuid-1")); err != nil {
		t.Error("expected directory to still exist when retention is disabled")
	}
}

func TestRunCleanup_MissingWorkingDir(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired incident with a non-existent working dir
	completedAt := time.Now().AddDate(0, 0, -60)
	db.Create(&database.Incident{
		UUID:        "no-dir-uuid",
		Source:      "test",
		Status:      database.IncidentStatusCompleted,
		WorkingDir:  filepath.Join(dataDir, "nonexistent"),
		CompletedAt: &completedAt,
	})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	// DB record should still be deleted even if dir doesn't exist
	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired incident deleted, got %d", result.ExpiredIncidentsDeleted)
	}
}

func TestRunCleanup_EmptyDataDir(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.ExpiredIncidentsDeleted != 0 || result.OrphanedDirsDeleted != 0 {
		t.Error("expected no deletions on empty data dir")
	}
}

func TestRunCleanup_NonexistentDataDir(t *testing.T) {
	db := setupRetentionTestDB(t)

	svc := NewRetentionService("/nonexistent/path", db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	// Should handle gracefully
	if result.ExpiredIncidentsDeleted != 0 || result.OrphanedDirsDeleted != 0 {
		t.Error("expected no deletions for nonexistent data dir")
	}
}

func TestRunCleanup_NoRetentionSettings(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	// Don't create any retention settings - should use defaults
	createExpiredIncident(t, db, "expired-uuid-1", dataDir, 100)

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	// Default is enabled with 90 days, so 100-day-old incident should be deleted
	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired incident deleted with defaults, got %d", result.ExpiredIncidentsDeleted)
	}
}

func TestStartBackgroundCleanup_ContextCancellation(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 1})

	svc := NewRetentionService(dataDir, db)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		svc.StartBackgroundCleanup(ctx)
		close(done)
	}()

	// Cancel and verify it stops
	cancel()

	select {
	case <-done:
		// Success - goroutine exited
	case <-time.After(5 * time.Second):
		t.Fatal("StartBackgroundCleanup did not stop after context cancellation")
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()

	// Write some files
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world!"), 0644)

	size, err := dirSize(dir)
	if err != nil {
		t.Fatalf("dirSize failed: %v", err)
	}

	// 5 + 6 = 11 bytes
	if size != 11 {
		t.Errorf("expected size 11, got %d", size)
	}
}

func TestDirSize_NonexistentDir(t *testing.T) {
	_, err := dirSize("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestNewRetentionService(t *testing.T) {
	svc := NewRetentionService("/test/dir", nil)
	if svc.dataDir != "/test/dir" {
		t.Errorf("expected dataDir /test/dir, got %s", svc.dataDir)
	}
}

func TestRunCleanup_BothPhasesCombined(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired incident
	createExpiredIncident(t, db, "expired-uuid-1", dataDir, 60)

	// Create an orphaned directory
	orphanDir := filepath.Join(dataDir, "orphan-uuid-1")
	os.MkdirAll(orphanDir, 0755)
	os.WriteFile(filepath.Join(orphanDir, "data.txt"), []byte("orphan"), 0644)

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired deleted, got %d", result.ExpiredIncidentsDeleted)
	}
	if result.OrphanedDirsDeleted != 1 {
		t.Errorf("expected 1 orphan deleted, got %d", result.OrphanedDirsDeleted)
	}
}

func TestRunCleanup_EmptyWorkingDir(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create an expired incident with empty working dir
	completedAt := time.Now().AddDate(0, 0, -60)
	db.Create(&database.Incident{
		UUID:        "empty-dir-uuid",
		Source:      "test",
		Status:      database.IncidentStatusCompleted,
		WorkingDir:  "",
		CompletedAt: &completedAt,
	})

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	// DB record should still be deleted
	if result.ExpiredIncidentsDeleted != 1 {
		t.Errorf("expected 1 expired incident deleted, got %d", result.ExpiredIncidentsDeleted)
	}
}

func TestRunCleanup_FilesInDataDirIgnored(t *testing.T) {
	db := setupRetentionTestDB(t)
	dataDir := t.TempDir()

	db.Create(&database.RetentionSettings{Enabled: true, RetentionDays: 30, CleanupIntervalHours: 6})

	// Create a regular file (not a directory) in dataDir - should be ignored by orphan cleanup
	os.WriteFile(filepath.Join(dataDir, "some-file.txt"), []byte("test"), 0644)

	svc := NewRetentionService(dataDir, db)
	result, err := svc.RunCleanup()
	if err != nil {
		t.Fatalf("RunCleanup failed: %v", err)
	}

	if result.OrphanedDirsDeleted != 0 {
		t.Errorf("expected 0 orphaned dirs deleted, got %d", result.OrphanedDirsDeleted)
	}

	// File should still exist
	if _, err := os.Stat(filepath.Join(dataDir, "some-file.txt")); err != nil {
		t.Error("expected file to still exist")
	}
}
