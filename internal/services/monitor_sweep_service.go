package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// monitorSweepInterval is how often the background sweep checks for expired
// monitor-status incidents. Monitor windows are on the order of tens of
// minutes to hours (GeneralSettings.AlertMonitorWindowMinutes, 1-10080), so
// this cadence keeps closure prompt without meaningfully increasing DB load.
const monitorSweepInterval = 15 * time.Minute

// MonitorSweepService automatically closes incidents whose monitor window has
// expired. Entering "monitor" already implies every linked alert was
// resolved (see UpdateIncidentComplete / ResolveAlertTx); once monitor_until
// passes with no recurrence, the incident is done being watched and should
// move to "closed" rather than sit in "monitor" indefinitely.
type MonitorSweepService struct {
	db *gorm.DB
}

// NewMonitorSweepService creates a new monitor sweep service.
func NewMonitorSweepService(db *gorm.DB) *MonitorSweepService {
	return &MonitorSweepService{db: db}
}

// SweepResult holds statistics from a sweep run.
type SweepResult struct {
	IncidentsClosed int
}

// RunSweep closes every incident whose monitor window has expired. As a
// safety net (entering monitor should already guarantee zero firing alerts,
// and LinkAlertToIncident extends monitor_until rather than leaving it stale
// when a recurrence lands), any alert still firing on a swept incident is
// resolved first so the close is never left inconsistent.
func (s *MonitorSweepService) RunSweep() (*SweepResult, error) {
	result := &SweepResult{}
	now := time.Now()

	err := s.db.Transaction(func(tx *gorm.DB) error {
		expiredMonitorIncidents := tx.Model(&database.Incident{}).
			Select("uuid").
			Where("status = ? AND monitor_until < ?", database.IncidentStatusMonitor, now)

		if err := tx.Model(&database.Alert{}).
			Where("status = ? AND resolved_at IS NULL AND incident_uuid IN (?)",
				string(database.AlertStatusFiring), expiredMonitorIncidents).
			Updates(map[string]interface{}{
				"status":      string(database.AlertStatusResolved),
				"resolved_at": now,
			}).Error; err != nil {
			return fmt.Errorf("resolve firing alerts on expired-monitor incidents: %w", err)
		}

		update := tx.Model(&database.Incident{}).
			Where("status = ? AND monitor_until < ?", database.IncidentStatusMonitor, now).
			Updates(map[string]interface{}{
				"status":        database.IncidentStatusClosed,
				"resolved_at":   &now,
				"monitor_until": nil,
			})
		if update.Error != nil {
			return fmt.Errorf("close expired-monitor incidents: %w", update.Error)
		}
		result.IncidentsClosed = int(update.RowsAffected)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if result.IncidentsClosed > 0 {
		slog.Info("monitor sweep closed expired incidents", "count", result.IncidentsClosed)
	}
	return result, nil
}

// StartBackgroundSweep runs RunSweep once at startup, then on a fixed ticker
// until ctx is cancelled.
func (s *MonitorSweepService) StartBackgroundSweep(ctx context.Context) {
	slog.Info("starting monitor sweep background service")

	if _, err := s.RunSweep(); err != nil {
		slog.Error("initial monitor sweep failed", "error", err)
	}

	ticker := time.NewTicker(monitorSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("monitor sweep background service stopped")
			return
		case <-ticker.C:
			if _, err := s.RunSweep(); err != nil {
				slog.Error("monitor sweep failed", "error", err)
			}
		}
	}
}
