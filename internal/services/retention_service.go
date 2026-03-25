package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RetentionService handles automatic cleanup of old incident data.
type RetentionService struct {
	dataDir string
	db      *gorm.DB
}

// NewRetentionService creates a new retention service.
// dataDir is the incidents directory (e.g., /akmatori/incidents).
func NewRetentionService(dataDir string, db *gorm.DB) *RetentionService {
	return &RetentionService{
		dataDir: dataDir,
		db:      db,
	}
}

// CleanupResult holds statistics from a cleanup run.
type CleanupResult struct {
	ExpiredIncidentsDeleted int
	ExpiredDirsDeleted      int
	ExpiredBytesFreed       int64
	OrphanedDirsDeleted     int
	OrphanedBytesFreed      int64
	Errors                  []error
}

// RunCleanup executes both cleanup phases: expired incidents and orphaned directories.
func (s *RetentionService) RunCleanup() (*CleanupResult, error) {
	settings, err := s.getRetentionSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get retention settings: %w", err)
	}

	if !settings.Enabled {
		slog.Info("retention cleanup skipped: disabled")
		return &CleanupResult{}, nil
	}

	result := &CleanupResult{}

	// Phase 1: Delete expired incidents
	s.cleanupExpiredIncidents(settings.RetentionDays, result)

	// Phase 2: Delete orphaned directories
	s.cleanupOrphanedDirectories(result)

	slog.Info("retention cleanup completed",
		"expired_incidents_deleted", result.ExpiredIncidentsDeleted,
		"expired_dirs_deleted", result.ExpiredDirsDeleted,
		"expired_bytes_freed", result.ExpiredBytesFreed,
		"orphaned_dirs_deleted", result.OrphanedDirsDeleted,
		"orphaned_bytes_freed", result.OrphanedBytesFreed,
		"errors", len(result.Errors),
	)

	return result, nil
}

// cleanupExpiredIncidents finds and removes incidents older than retentionDays.
func (s *RetentionService) cleanupExpiredIncidents(retentionDays int, result *CleanupResult) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	var incidents []database.Incident
	err := s.db.Select("id, uuid, working_dir, status, completed_at").
		Where("status IN ? AND completed_at < ?",
			[]database.IncidentStatus{database.IncidentStatusCompleted, database.IncidentStatusFailed, database.IncidentStatusDiagnosed},
			cutoff,
		).Find(&incidents).Error
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("query expired incidents: %w", err))
		return
	}

	// Resolve dataDir once (with symlinks resolved) for path traversal checks
	absDataDir, err := filepath.EvalSymlinks(s.dataDir)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("resolve data dir: %w", err))
		return
	}

	for _, incident := range incidents {
		dirRemoved := s.removeIncidentDir(incident, absDataDir, result)

		// Only delete the DB record if the directory was successfully removed (or didn't exist)
		if !dirRemoved {
			continue
		}
		if err := s.db.Delete(&incident).Error; err != nil {
			slog.Error("failed to delete incident record", "uuid", incident.UUID, "error", err)
			result.Errors = append(result.Errors, fmt.Errorf("delete record %s: %w", incident.UUID, err))
		} else {
			result.ExpiredIncidentsDeleted++
		}
	}
}

// removeIncidentDir removes an incident's working directory from disk.
// Returns true if the directory was successfully removed or didn't exist.
func (s *RetentionService) removeIncidentDir(incident database.Incident, absDataDir string, result *CleanupResult) bool {
	if incident.WorkingDir == "" {
		return true
	}

	// Resolve symlinks to prevent path traversal via symlinked WorkingDir
	absWorkDir, err := filepath.EvalSymlinks(incident.WorkingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return true // Directory already gone
		}
		result.Errors = append(result.Errors, fmt.Errorf("resolve dir %s: %w", incident.UUID, err))
		return false
	}
	if !strings.HasPrefix(absWorkDir, absDataDir+string(os.PathSeparator)) {
		result.Errors = append(result.Errors, fmt.Errorf("working dir %q for %s is outside data dir, skipping", incident.WorkingDir, incident.UUID))
		return false
	}

	bytesFreed, err := dirSize(absWorkDir)
	if err != nil {
		if os.IsNotExist(err) {
			return true // Directory already gone
		}
		result.Errors = append(result.Errors, fmt.Errorf("stat dir %s: %w", incident.UUID, err))
		return false
	}

	if err := os.RemoveAll(absWorkDir); err != nil {
		slog.Error("failed to remove incident directory", "uuid", incident.UUID, "dir", absWorkDir, "error", err)
		result.Errors = append(result.Errors, fmt.Errorf("remove dir %s: %w", incident.UUID, err))
		return false
	}

	result.ExpiredDirsDeleted++
	result.ExpiredBytesFreed += bytesFreed
	return true
}

// cleanupOrphanedDirectories removes directories in dataDir with no matching incident record.
func (s *RetentionService) cleanupOrphanedDirectories(result *CleanupResult) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		result.Errors = append(result.Errors, fmt.Errorf("read data dir: %w", err))
		return
	}

	// Collect candidate directories (must be valid UUIDs and not recently modified)
	type candidate struct {
		name string
		path string
	}
	var candidates []candidate
	var candidateUUIDs []string
	gracePeriod := time.Now().Add(-1 * time.Hour)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()

		// Only consider directories with valid UUID names
		if _, err := uuid.Parse(dirName); err != nil {
			continue
		}

		// Skip recently modified directories to avoid racing with incident creation
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(gracePeriod) {
			continue
		}

		dirPath := filepath.Join(s.dataDir, dirName)
		candidates = append(candidates, candidate{name: dirName, path: dirPath})
		candidateUUIDs = append(candidateUUIDs, dirName)
	}

	if len(candidates) == 0 {
		return
	}

	// Batch lookup: find which UUIDs exist in the database
	existingUUIDs := make(map[string]bool)
	const batchSize = 500
	for i := 0; i < len(candidateUUIDs); i += batchSize {
		end := i + batchSize
		if end > len(candidateUUIDs) {
			end = len(candidateUUIDs)
		}
		batch := candidateUUIDs[i:end]

		var found []struct{ UUID string }
		if err := s.db.Model(&database.Incident{}).Select("uuid").Where("uuid IN ?", batch).Find(&found).Error; err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("batch check orphans: %w", err))
			return
		}
		for _, f := range found {
			existingUUIDs[f.UUID] = true
		}
	}

	// Remove orphaned directories
	for _, c := range candidates {
		if existingUUIDs[c.name] {
			continue
		}

		bytesFreed, err := dirSize(c.path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("stat orphan %s: %w", c.name, err))
			continue
		}

		if err := os.RemoveAll(c.path); err != nil {
			slog.Error("failed to remove orphaned directory", "dir", c.path, "error", err)
			result.Errors = append(result.Errors, fmt.Errorf("remove orphan %s: %w", c.name, err))
		} else {
			result.OrphanedDirsDeleted++
			result.OrphanedBytesFreed += bytesFreed
			slog.Info("removed orphaned incident directory", "dir", c.name, "bytes_freed", bytesFreed)
		}
	}
}

// StartBackgroundCleanup runs RunCleanup on a ticker based on CleanupIntervalHours.
func (s *RetentionService) StartBackgroundCleanup(ctx context.Context) {
	slog.Info("starting retention background cleanup")

	// Run once at startup
	if _, err := s.RunCleanup(); err != nil {
		slog.Error("initial retention cleanup failed", "error", err)
	}

	for {
		settings, err := s.getRetentionSettings()
		if err != nil {
			slog.Error("failed to get retention settings for interval", "error", err)
			// Default to 6 hours on error
			settings = &database.RetentionSettings{CleanupIntervalHours: 6}
		}

		interval := time.Duration(settings.CleanupIntervalHours) * time.Hour
		if interval < time.Hour {
			interval = time.Hour
		}

		select {
		case <-ctx.Done():
			slog.Info("retention background cleanup stopped")
			return
		case <-time.After(interval):
			if _, err := s.RunCleanup(); err != nil {
				slog.Error("retention cleanup failed", "error", err)
			}
		}
	}
}

// getRetentionSettings retrieves settings using the service's db instance.
func (s *RetentionService) getRetentionSettings() (*database.RetentionSettings, error) {
	var settings database.RetentionSettings
	err := s.db.First(&settings).Error
	if err == gorm.ErrRecordNotFound {
		return database.DefaultRetentionSettings(), nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// dirSize calculates the total size of a directory and its contents.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
