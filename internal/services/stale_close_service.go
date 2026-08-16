package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

const (
	// staleCloseInterval is how often the background sweep looks for stale
	// incidents. The inactivity window is measured in days, so checking four
	// times an hour is far more often than needed and costs one indexed query.
	staleCloseInterval = 15 * time.Minute

	// staleCloseBatchLimit caps how many incidents one sweep closes. The first
	// sweep after an upgrade can face a large backlog (an install that never
	// had this feature accumulates every incident whose alerting system did
	// not send a resolve); closing them in bounded batches keeps the
	// transaction short and the log readable. The backlog drains over
	// successive ticks.
	staleCloseBatchLimit = 500
)

// StaleCloseService closes alert-sourced incidents that have gone quiet.
//
// It exists because "completed" is not a terminal state for an alert-sourced
// incident. UpdateIncidentComplete only promotes one to "monitor" once every
// linked alert has resolved, and MonitorSweepService only ever closes
// "monitor" rows. An incident whose alerting system never sends a matching
// resolve therefore sits at "completed" with a permanently firing alert, and
// nothing in the lifecycle ever closes it.
type StaleCloseService struct {
	db *gorm.DB
}

// NewStaleCloseService creates a new stale-close service.
func NewStaleCloseService(db *gorm.DB) *StaleCloseService {
	return &StaleCloseService{db: db}
}

// StaleCandidate describes one incident the sweep would close, for dry-run
// previews and logging.
type StaleCandidate struct {
	UUID         string    `json:"uuid"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	LastActivity time.Time `json:"last_activity"`
	FiringAlerts int64     `json:"firing_alerts"`
}

// StaleCloseResult holds the outcome of a sweep run.
type StaleCloseResult struct {
	// Enabled reports the gate state at the time of the run. A dry run
	// computes candidates even when the gate is off, so the caller can
	// preview the effect of turning it on.
	Enabled bool `json:"enabled"`
	// WindowMinutes is the inactivity window that produced these candidates.
	WindowMinutes int `json:"window_minutes"`
	// Cutoff is the instant an incident must have been inactive since.
	Cutoff time.Time `json:"cutoff"`
	// Candidates lists the incidents matched, capped at staleCloseBatchLimit.
	Candidates []StaleCandidate `json:"candidates"`
	// IncidentsClosed is zero for a dry run.
	IncidentsClosed int `json:"incidents_closed"`
	// Truncated reports that the batch limit was hit and more stale
	// incidents remain for the next tick.
	Truncated bool `json:"truncated"`
}

// Run finds stale incidents and, unless dryRun is set, closes them.
//
// Scope is deliberately narrow: alert-sourced incidents in "completed" or
// "monitor" status. Statuses pending/running/diagnosed are excluded because a
// stuck run is an orphaned-agent bug and closing it would hide the bug rather
// than fix it; "failed" is excluded because it is already terminal and already
// filtered out of the open-incidents view, so closing it would only erase the
// failure signal. Merged rows are excluded — they are not open.
func (s *StaleCloseService) Run(ctx context.Context, dryRun bool) (*StaleCloseResult, error) {
	settings, err := database.GetOrCreateGeneralSettings()
	if err != nil {
		return nil, fmt.Errorf("stale close: load settings: %w", err)
	}
	window := settings.GetIncidentAutoCloseWindow()
	now := time.Now()
	cutoff := now.Add(-window)

	result := &StaleCloseResult{
		Enabled:       settings.GetIncidentAutoCloseEnabled(),
		WindowMinutes: int(window / time.Minute),
		Cutoff:        cutoff,
	}

	candidates, err := s.findCandidates(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("stale close: find candidates: %w", err)
	}
	result.Candidates = candidates
	result.Truncated = len(candidates) == staleCloseBatchLimit

	if dryRun || !result.Enabled || len(candidates) == 0 {
		return result, nil
	}

	uuids := make([]string, len(candidates))
	for i, c := range candidates {
		uuids[i] = c.UUID
	}

	closed, err := s.closeBatch(ctx, uuids, cutoff, now)
	if err != nil {
		return nil, fmt.Errorf("stale close: close batch: %w", err)
	}
	result.IncidentsClosed = closed
	return result, nil
}

// findCandidates returns the stale incidents eligible for closing, oldest
// first so a backlog drains in a predictable order.
//
// The selection is done entirely in the WHERE clause; the reported
// LastActivity and FiringAlerts are derived in Go from a second query over the
// matched incidents' alert rows. Computing them as SELECT expressions would be
// one fewer round trip, but SQLite (which the tests use) hands a computed
// datetime back as a string that the driver cannot scan into time.Time — the
// same limitation that already breaks the incident trend query. Selecting
// declared columns only keeps this working on both dialects.
func (s *StaleCloseService) findCandidates(ctx context.Context, cutoff time.Time) ([]StaleCandidate, error) {
	staleCond, staleArgs := staleIncidentCond(cutoff)

	args := []interface{}{
		database.IncidentSourceKindAlert,
		[]string{
			string(database.IncidentStatusCompleted),
			string(database.IncidentStatusMonitor),
		},
	}
	args = append(args, staleArgs...)

	type candidateIncident struct {
		UUID        string
		Title       string
		Status      string
		StartedAt   time.Time
		CompletedAt *time.Time
	}

	var incidents []candidateIncident
	err := s.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("incidents.uuid, incidents.title, incidents.status, incidents.started_at, incidents.completed_at").
		Where("incidents.source_kind = ? AND incidents.status IN ? AND COALESCE(incidents.merged_into_uuid, '') = '' AND "+staleCond, args...).
		Order("incidents.started_at ASC").
		Limit(staleCloseBatchLimit).
		Scan(&incidents).Error
	if err != nil {
		return nil, err
	}
	if len(incidents) == 0 {
		return nil, nil
	}

	uuids := make([]string, len(incidents))
	for i, inc := range incidents {
		uuids[i] = inc.UUID
	}

	type alertRow struct {
		IncidentUUID string
		Status       string
		FiredAt      time.Time
		LastSeenAt   *time.Time
		ResolvedAt   *time.Time
	}
	var alertRows []alertRow
	if err := s.db.WithContext(ctx).
		Model(&database.Alert{}).
		Select("incident_uuid, status, fired_at, last_seen_at, resolved_at").
		Where("incident_uuid IN ?", uuids).
		Scan(&alertRows).Error; err != nil {
		return nil, err
	}

	lastAlertActivity := make(map[string]time.Time, len(incidents))
	firingCounts := make(map[string]int64, len(incidents))
	for _, a := range alertRows {
		activity := a.FiredAt
		if a.LastSeenAt != nil && a.LastSeenAt.After(activity) {
			activity = *a.LastSeenAt
		}
		if activity.After(lastAlertActivity[a.IncidentUUID]) {
			lastAlertActivity[a.IncidentUUID] = activity
		}
		if a.Status == string(database.AlertStatusFiring) && a.ResolvedAt == nil {
			firingCounts[a.IncidentUUID]++
		}
	}

	out := make([]StaleCandidate, 0, len(incidents))
	for _, inc := range incidents {
		last := inc.StartedAt
		if inc.CompletedAt != nil && inc.CompletedAt.After(last) {
			last = *inc.CompletedAt
		}
		if a, ok := lastAlertActivity[inc.UUID]; ok && a.After(last) {
			last = a
		}
		out = append(out, StaleCandidate{
			UUID:         inc.UUID,
			Title:        inc.Title,
			Status:       inc.Status,
			StartedAt:    inc.StartedAt,
			LastActivity: last,
			FiringAlerts: firingCounts[inc.UUID],
		})
	}
	return out, nil
}

// closeBatch resolves any lingering firing alerts on the given incidents and
// closes them, in one transaction. The staleness condition is re-applied to
// the incident UPDATE so an alert that lands between the candidate query and
// this call leaves its incident open.
//
// Resolving the alerts first is safe for that re-check: it sets status and
// resolved_at but never fired_at or last_seen_at, which are the only columns
// the staleness condition reads.
func (s *StaleCloseService) closeBatch(ctx context.Context, uuids []string, cutoff, now time.Time) (int, error) {
	var closed int

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Alert{}).
			Where("incident_uuid IN ? AND status = ? AND resolved_at IS NULL",
				uuids, string(database.AlertStatusFiring)).
			Updates(map[string]interface{}{
				"status":      string(database.AlertStatusResolved),
				"resolved_at": now,
			}).Error; err != nil {
			return fmt.Errorf("resolve firing alerts: %w", err)
		}

		staleCond, staleArgs := staleIncidentCond(cutoff)
		args := []interface{}{
			uuids,
			[]string{
				string(database.IncidentStatusCompleted),
				string(database.IncidentStatusMonitor),
			},
		}
		args = append(args, staleArgs...)

		update := tx.Model(&database.Incident{}).
			Where("incidents.uuid IN ? AND incidents.status IN ? AND "+staleCond, args...).
			Updates(map[string]interface{}{
				"status":        database.IncidentStatusClosed,
				"resolved_at":   &now,
				"monitor_until": nil,
				"closed_reason": database.CloseReasonAutoStale,
			})
		if update.Error != nil {
			return fmt.Errorf("close incidents: %w", update.Error)
		}
		closed = int(update.RowsAffected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return closed, nil
}

// StartBackgroundSweep runs the sweep once at startup, then on a fixed ticker
// until ctx is cancelled. Failures are logged and the ticker continues — a
// transient DB error must not kill incident hygiene for the process lifetime.
func (s *StaleCloseService) StartBackgroundSweep(ctx context.Context) {
	slog.Info("starting stale incident close background service")

	s.runAndLog(ctx)

	ticker := time.NewTicker(staleCloseInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stale incident close background service stopped")
			return
		case <-ticker.C:
			s.runAndLog(ctx)
		}
	}
}

func (s *StaleCloseService) runAndLog(ctx context.Context) {
	result, err := s.Run(ctx, false)
	if err != nil {
		slog.Error("stale incident close sweep failed", "error", err)
		return
	}
	if result.IncidentsClosed > 0 {
		slog.Info("stale incident close sweep closed inactive incidents",
			"count", result.IncidentsClosed,
			"window_minutes", result.WindowMinutes,
			"truncated", result.Truncated)
	}
}
