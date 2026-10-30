package database

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// eventsBackfillBatch bounds one backfill step. Each step reads a batch,
// inserts its events and stamps the source rows in one transaction, so a
// crash mid-backfill resumes where it stopped on the next start.
const eventsBackfillBatch = 500

// eventsBackfillSummaryMax caps the summary text copied from an agent run's
// context into its backfilled request event.
const eventsBackfillSummaryMax = 1000

// migrateBackfillEvents gives every alert and every non-alert agent run that
// predates the events table one events row, so the Events feed keeps its
// history after the feed moves to the events table. Idempotent: alerts are
// selected by an empty event_uuid, runs by having no event that points at them.
//
// The caller logs a failure and continues: the backfill only feeds the
// history of the Events page, so it must never stop the API from starting.
func migrateBackfillEvents(db *gorm.DB) error {
	// See migrateBackfillAlerts: reset to a fresh-statement session on the
	// pinned migration connection. Silent: a slow batch INSERT would otherwise
	// print its SQL, all 500 rows included (1 MB of startup log on gcore).
	// Errors are returned.
	db = db.Session(&gorm.Session{NewDB: true, Logger: db.Logger.LogMode(logger.Silent)})
	if !db.Migrator().HasTable("events") || !db.Migrator().HasTable("alerts") {
		return nil
	}
	if err := backfillAlertEvents(db); err != nil {
		return err
	}
	return backfillRequestEvents(db)
}

func backfillAlertEvents(db *gorm.DB) error {
	// Alerts from listener channels carry the channel UUID as source_uuid;
	// everything else came from a webhook source instance.
	channelUUIDs := map[string]bool{}
	if db.Migrator().HasTable("channels") {
		var uuids []string
		if err := db.Raw("SELECT uuid FROM channels").Scan(&uuids).Error; err != nil {
			return fmt.Errorf("migrateBackfillEvents: list channels: %w", err)
		}
		for _, u := range uuids {
			channelUUIDs[u] = true
		}
	}

	// Keyset paging on uuid: every batch starts after the last one, so the
	// loop ends even if a stamp silently affects no row.
	total, after := 0, ""
	for {
		var alerts []Alert
		if err := db.Where("(event_uuid IS NULL OR event_uuid = '') AND uuid > ?", after).
			Order("uuid ASC").Limit(eventsBackfillBatch).Find(&alerts).Error; err != nil {
			return fmt.Errorf("migrateBackfillEvents: query alerts: %w", err)
		}
		if len(alerts) == 0 {
			break
		}
		after = alerts[len(alerts)-1].UUID
		err := db.Transaction(func(tx *gorm.DB) error {
			events := make([]Event, 0, len(alerts))
			for _, a := range alerts {
				sourceKind := EventSourceWebhook
				if channelUUIDs[a.SourceUUID] {
					sourceKind = EventSourceSlackListener
				}
				received := a.CreatedAt
				if received.IsZero() {
					received = a.FiredAt
				}
				events = append(events, Event{
					UUID:           uuid.New().String(),
					SourceKind:     sourceKind,
					SourceUUID:     a.SourceUUID,
					ExternalID:     a.SourceFingerprint,
					Fingerprint:    a.Fingerprint,
					ReceivedAt:     received,
					OccurredAt:     a.FiredAt,
					Kind:           EventKindAlert,
					Classification: EventClassificationBackfill,
					Title:          a.AlertName,
					Host:           a.TargetHost,
					Status:         string(a.Status),
					AlertUUID:      a.UUID,
					IncidentUUID:   a.IncidentUUID,
				})
			}
			if err := tx.Create(&events).Error; err != nil {
				return fmt.Errorf("insert events: %w", err)
			}
			for i, a := range alerts {
				if err := tx.Model(&Alert{}).Where("uuid = ?", a.UUID).
					Update("event_uuid", events[i].UUID).Error; err != nil {
					return fmt.Errorf("stamp alert %s: %w", a.UUID, err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migrateBackfillEvents: alerts batch: %w", err)
		}
		total += len(alerts)
		if len(alerts) < eventsBackfillBatch {
			break
		}
	}
	if total > 0 {
		slog.Info("migrateBackfillEvents: backfilled alert events", "count", total)
	}
	return nil
}

// requestEventSourceKind maps a non-alert incident's source_kind to the
// event source that started it. Legacy API rows have an empty source_kind.
func requestEventSourceKind(incidentSourceKind string) string {
	switch incidentSourceKind {
	case IncidentSourceKindCron:
		return EventSourceCron
	case IncidentSourceKindSlackMention:
		return EventSourceSlackMention
	case IncidentSourceKindProposal:
		return EventSourceProposal
	default:
		return EventSourceManual
	}
}

func backfillRequestEvents(db *gorm.DB) error {
	if !db.Migrator().HasTable("incidents") {
		return nil
	}
	type runRow struct {
		UUID       string
		Source     string
		SourceKind string
		SourceUUID string
		Title      string
		StartedAt  time.Time
		Context    JSONB `gorm:"type:jsonb"`
	}
	total, after := 0, ""
	for {
		var rows []runRow
		if err := db.Raw(`
			SELECT uuid, source, source_kind, source_uuid, title, started_at, context
			FROM incidents
			WHERE COALESCE(source_kind, '') <> ? AND uuid > ?
			  AND NOT EXISTS (SELECT 1 FROM events WHERE events.incident_uuid = incidents.uuid)
			ORDER BY uuid ASC
			LIMIT ?`, IncidentSourceKindAlert, after, eventsBackfillBatch).Scan(&rows).Error; err != nil {
			return fmt.Errorf("migrateBackfillEvents: query runs: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		after = rows[len(rows)-1].UUID
		events := make([]Event, 0, len(rows))
		for _, r := range rows {
			title := r.Title
			summary := backfillRunSummary(r.Context)
			if title == "" {
				title = truncateRunes(summary, 255)
			}
			events = append(events, Event{
				UUID:           uuid.New().String(),
				SourceKind:     requestEventSourceKind(r.SourceKind),
				SourceUUID:     r.SourceUUID,
				SourceType:     r.Source,
				ReceivedAt:     r.StartedAt,
				OccurredAt:     r.StartedAt,
				Kind:           EventKindRequest,
				Classification: EventClassificationBackfill,
				Title:          title,
				Summary:        summary,
				IncidentUUID:   r.UUID,
			})
		}
		if err := db.Create(&events).Error; err != nil {
			return fmt.Errorf("migrateBackfillEvents: insert request events: %w", err)
		}
		total += len(rows)
		if len(rows) < eventsBackfillBatch {
			break
		}
	}
	if total > 0 {
		slog.Info("migrateBackfillEvents: backfilled request events", "count", total)
	}
	return nil
}

// backfillRunSummary picks the request text out of an agent run's context:
// "task" (manual API), "text" (Slack mention) or "message".
func backfillRunSummary(ctx JSONB) string {
	for _, key := range []string{"task", "text", "message"} {
		if s, ok := ctx[key].(string); ok && s != "" {
			return truncateRunes(s, eventsBackfillSummaryMax)
		}
	}
	return ""
}

// truncateRunes cuts s to at most max runes so a multi-byte character is
// never split.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
