package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/akmatori/akmatori/internal/database"
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
	err := s.db.Where("status IN ? AND completed_at < ?",
		[]database.IncidentStatus{database.IncidentStatusCompleted, database.IncidentStatusFailed},
		cutoff,
	).Find(&incidents).Error
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("query expired incidents: %w", err))
		return
	}

	for _, incident := range incidents {
		// Delete working directory from disk
		if incident.WorkingDir != "" {
			bytesFreed, err := dirSize(incident.WorkingDir)
			if err == nil {
				if err := os.RemoveAll(incident.WorkingDir); err != nil {
					slog.Error("failed to remove incident directory", "uuid", incident.UUID, "dir", incident.WorkingDir, "error", err)
					result.Errors = append(result.Errors, fmt.Errorf("remove dir %s: %w", incident.UUID, err))
				} else {
					result.ExpiredDirsDeleted++
					result.ExpiredBytesFreed += bytesFreed
				}
			} else if !os.IsNotExist(err) {
				result.Errors = append(result.Errors, fmt.Errorf("stat dir %s: %w", incident.UUID, err))
			}
		}

		// Delete the incident record from the database
		if err := s.db.Delete(&incident).Error; err != nil {
			slog.Error("failed to delete incident record", "uuid", incident.UUID, "error", err)
			result.Errors = append(result.Errors, fmt.Errorf("delete record %s: %w", incident.UUID, err))
		} else {
			result.ExpiredIncidentsDeleted++
		}
	}
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

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		dirPath := filepath.Join(s.dataDir, dirName)

		// Check if there's a matching incident record
		var count int64
		if err := s.db.Model(&database.Incident{}).Where("uuid = ?", dirName).Count(&count).Error; err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("check orphan %s: %w", dirName, err))
			continue
		}

		if count > 0 {
			continue
		}

		// Orphaned directory - remove it
		bytesFreed, err := dirSize(dirPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("stat orphan %s: %w", dirName, err))
			continue
		}

		if err := os.RemoveAll(dirPath); err != nil {
			slog.Error("failed to remove orphaned directory", "dir", dirPath, "error", err)
			result.Errors = append(result.Errors, fmt.Errorf("remove orphan %s: %w", dirName, err))
		} else {
			result.OrphanedDirsDeleted++
			result.OrphanedBytesFreed += bytesFreed
			slog.Info("removed orphaned incident directory", "dir", dirName, "bytes_freed", bytesFreed)
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
		return &database.RetentionSettings{
			Enabled:              true,
			RetentionDays:        90,
			CleanupIntervalHours: 6,
		}, nil
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
