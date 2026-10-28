package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

const (
	// reportedDispatchInterval is how often the dispatcher looks for alert
	// events that the MCP gateway queued (events.report_alert).
	reportedDispatchInterval = 5 * time.Second
	reportedDispatchBatch    = 20
)

// ReportedEventDispatcher runs the alert events that cron runs report through
// the gateway (events.report_alert) through the alert correlation gate. The
// gateway cannot reach the API, so it writes the event with
// pending_dispatch=true and this loop picks it up.
type ReportedEventDispatcher struct {
	db        *gorm.DB
	processor reportedAlertProcessor
	interval  time.Duration
}

// NewReportedEventDispatcher constructs the dispatcher.
func NewReportedEventDispatcher(db *gorm.DB, processor reportedAlertProcessor) *ReportedEventDispatcher {
	return &ReportedEventDispatcher{db: db, processor: processor, interval: reportedDispatchInterval}
}

// Start runs the loop until ctx is done.
func (d *ReportedEventDispatcher) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		for {
			d.DispatchPending(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// DispatchPending claims and processes queued alert events. Each event is
// claimed with a conditional update, so two API processes never handle the
// same event. Returns the number of events processed.
func (d *ReportedEventDispatcher) DispatchPending(ctx context.Context) int {
	var pending []database.Event
	if err := d.db.WithContext(ctx).
		Where("pending_dispatch = ? AND kind = ?", true, database.EventKindAlert).
		Order("received_at ASC").Limit(reportedDispatchBatch).Find(&pending).Error; err != nil {
		slog.Warn("reported events: query failed", "err", err)
		return 0
	}

	done := 0
	for i := range pending {
		ev := &pending[i]
		claim := d.db.WithContext(ctx).Model(&database.Event{}).
			Where("uuid = ? AND pending_dispatch = ?", ev.UUID, true).
			Update("pending_dispatch", false)
		if claim.Error != nil {
			slog.Warn("reported events: claim failed", "event_uuid", ev.UUID, "err", claim.Error)
			continue
		}
		if claim.RowsAffected != 1 {
			continue // another API process claimed it
		}
		d.dispatch(ctx, ev)
		done++
	}
	return done
}

func (d *ReportedEventDispatcher) dispatch(ctx context.Context, ev *database.Event) {
	raw := map[string]interface{}{}
	for k, v := range ev.RawPayload {
		raw[k] = v
	}
	jobName, _ := raw["cron_job_name"].(string)
	if jobName == "" {
		jobName = "cron"
	}
	firedAt := ev.OccurredAt

	incidentUUID, linked, err := d.processor.ProcessReportedAlert(ReportedAlert{
		SourceUUID:    ev.SourceUUID,
		SourceLabel:   database.IncidentSourceKindCron,
		SourceDisplay: "cron-reported",
		SourceName:    jobName,
		Alert: alerts.NormalizedAlert{
			AlertName:         ev.Title,
			Severity:          database.AlertSeverity(ev.Severity),
			Status:            database.AlertStatusFiring,
			Summary:           ev.Summary,
			Description:       ev.Summary,
			TargetHost:        ev.Host,
			TargetService:     ev.Service,
			StartedAt:         &firedAt,
			SourceAlertID:     ev.UUID,
			SourceFingerprint: ev.ExternalID,
			RawPayload:        raw,
			EventUUID:         ev.UUID,
		},
	})
	if err != nil {
		slog.Error("reported events: correlation failed", "event_uuid", ev.UUID, "err", err)
		if uerr := d.db.WithContext(ctx).Model(&database.Event{}).Where("uuid = ?", ev.UUID).
			Update("classification_reasoning", ev.ClassificationReasoning+"; dispatch failed: "+err.Error()).Error; uerr != nil {
			slog.Warn("reported events: record failure", "event_uuid", ev.UUID, "err", uerr)
		}
		return
	}
	slog.Info("reported alert dispatched", "event_uuid", ev.UUID, "incident_uuid", incidentUUID, "linked", linked)
}
