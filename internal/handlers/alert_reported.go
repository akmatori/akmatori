package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// ReportedAlert is an alert that a person reported to the agent: a Slack
// mention or an API request that event classification found to describe a
// problem ("wa2 is down"). It goes through the same correlation gate as a
// monitoring alert, so it joins the open incident it belongs to or opens a
// new one — only alerts open incidents.
type ReportedAlert struct {
	// SourceUUID identifies the reporter channel: a Channel UUID, a
	// "slack:<channel id>" key for Slack channels with no Channel row, or
	// "manual" for the API.
	SourceUUID string
	// SourceLabel is the incident's Source column ("slack_mention", "api").
	SourceLabel string
	// SourceDisplay and SourceName render the prompt header and Source line.
	SourceDisplay string
	SourceName    string

	Alert alerts.NormalizedAlert

	// SlackChannelID and SlackThreadTS are the thread the report came from.
	// Empty for API reports: the result then lands in the UI only.
	SlackChannelID string
	SlackThreadTS  string
}

// ProcessReportedAlert runs a reported alert through the correlation gate.
// It returns the incident the alert joined (linked=true) or opened. Errors
// are returned only when no incident could be opened.
func (h *AlertHandler) ProcessReportedAlert(r ReportedAlert) (incidentUUID string, linked bool, err error) {
	normalized := r.Alert
	inThread := r.SlackChannelID != "" && r.SlackThreadTS != ""

	rawPayload := database.JSONB{}
	for k, v := range normalized.RawPayload {
		rawPayload[k] = v
	}
	alertFingerprint := services.ComputeAlertFingerprint(r.SourceUUID, normalized.AlertName, normalized.TargetHost)

	incidentCtx := &services.IncidentContext{
		Source:     r.SourceLabel,
		SourceID:   normalized.SourceFingerprint,
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: r.SourceUUID,
		Context: database.JSONB{
			"alert_name":         normalized.AlertName,
			"severity":           string(normalized.Severity),
			"status":             string(normalized.Status),
			"summary":            normalized.Summary,
			"description":        normalized.Description,
			"target_host":        normalized.TargetHost,
			"target_service":     normalized.TargetService,
			"source_alert_id":    normalized.SourceAlertID,
			"source_fingerprint": normalized.SourceFingerprint,
			"source_type":        r.SourceLabel,
			"source_instance":    r.SourceName,
			"raw_payload":        rawPayload,
			"slack_channel_id":   r.SlackChannelID,
			"slack_message_ts":   r.SlackThreadTS,
			"alert_fingerprint":  alertFingerprint,
			"reported":           true,
		},
		Message: fmt.Sprintf("%s - %s: %s", normalized.AlertName, normalized.TargetHost, normalized.Summary),
	}

	key := alertSpawnKey(r.SourceUUID, normalized.AlertName, normalized.TargetHost, normalized.SourceFingerprint)
	// ran is set only by the leader; a follower's event is linked below.
	var ran bool
	_, sfErr, _ := h.spawnGroup.Do(key, func() (interface{}, error) {
		ran = true
		// A repeat report of a problem that is still firing (a cron that
		// finds the same problem every run) joins that alert's incident. The
		// alert link then records the re-send instead of the insert hitting
		// uniq_firing_alert and failing a fresh incident.
		if openIncident := firingAlertIncident(r.SourceUUID, normalized.SourceFingerprint); openIncident != "" {
			err := h.skillService.LinkAlertToIncident(context.Background(), openIncident, r.SourceUUID, normalized, 1.0,
				"same report as an alert that is still firing")
			if err == nil {
				incidentUUID, linked = openIncident, true
				return nil, nil
			}
			slog.Warn("failed to link repeat report, using correlation", "incident_uuid", openIncident, "err", err)
		}

		verdict, corrErr := h.correlate(context.Background(), r.SourceUUID, normalized)
		if corrErr != nil && !errors.Is(corrErr, services.ErrWorkerNotConnected) {
			slog.Warn("alert correlator error on reported alert, opening new incident", "err", corrErr)
		}
		if verdict.IsConfident(h.correlationThreshold()) {
			if err := h.skillService.LinkAlertToIncident(context.Background(), verdict.IncidentUUID, r.SourceUUID, normalized, verdict.Confidence, verdict.Reasoning); err != nil {
				slog.Warn("failed to link reported alert, opening new incident", "incident_uuid", verdict.IncidentUUID, "err", err)
			} else {
				incidentUUID, linked = verdict.IncidentUUID, true
				if inThread {
					h.postSlackThreadReply(r.SlackChannelID, r.SlackThreadTS, h.buildAlertMergedMessage(verdict.IncidentUUID))
				}
				return nil, nil
			}
		}

		alertDecision, alertReasoning := "not_evaluated", ""
		if corrErr != nil {
			alertReasoning = "correlator error"
			if errors.Is(corrErr, services.ErrWorkerNotConnected) {
				alertReasoning = "worker not connected"
			}
		} else if h.alertCorrelator != nil {
			alertDecision, alertReasoning = "new_incident", verdict.Reasoning
		}

		newUUID, _, err := h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			return nil, fmt.Errorf("spawn incident for reported alert: %w", err)
		}
		if err := h.skillService.InsertFiringAlert(context.Background(), newUUID, r.SourceUUID, normalized, alertDecision, alertReasoning); err != nil {
			if uerr := h.skillService.UpdateIncidentStatus(newUUID, database.IncidentStatusFailed); uerr != nil {
				slog.Warn("failed to cancel reported-alert incident after alert insert failure", "incident_uuid", newUUID, "err", uerr)
			}
			return nil, fmt.Errorf("insert reported alert: %w", err)
		}
		if inThread {
			if err := h.updateIncidentSlackContext(newUUID, r.SlackChannelID, r.SlackThreadTS); err != nil {
				slog.Warn("failed to update incident Slack context", "err", err)
			}
		}
		if err := h.skillService.UpdateIncidentStatus(newUUID, database.IncidentStatusRunning); err != nil {
			slog.Warn("failed to update incident status", "err", err)
		}
		incidentUUID = newUUID

		// The listener-channel run loop covers both cases: in a Slack thread
		// it streams progress and replies there; without one (API) it writes
		// the result to the incident only.
		channel := &database.Channel{
			UUID:        r.SourceUUID,
			ExternalID:  r.SlackChannelID,
			DisplayName: r.SourceName,
			CanPost:     inThread,
			Integration: database.Integration{Provider: database.MessagingProviderSlack},
		}
		prompt := h.buildInvestigationPromptWithSource(normalized, r.SourceDisplay, r.SourceLabel, r.SourceName)
		go h.runListenerChannelInvestigation(newUUID, normalized, channel, r.SlackChannelID, r.SlackThreadTS, prompt)
		return nil, nil
	})
	if sfErr != nil {
		return "", false, sfErr
	}
	if !ran {
		// A follower of a concurrent identical report: the leader opened or
		// joined the incident. Link this report's event to that alert and
		// report the incident it joined.
		services.LinkFollowerEvent(database.GetDB(), normalized.EventUUID, r.SourceUUID, normalized)
		incidentUUID, linked = firingAlertIncident(r.SourceUUID, normalized.SourceFingerprint), true
	}
	return incidentUUID, linked, nil
}

// firingAlertIncident returns the incident of the firing alert with this
// source and source fingerprint, or "" when there is none.
func firingAlertIncident(sourceUUID, sourceFingerprint string) string {
	db := database.GetDB()
	if db == nil || sourceFingerprint == "" {
		return ""
	}
	var a database.Alert
	if err := db.Select("incident_uuid").
		Where("source_uuid = ? AND source_fingerprint = ? AND status = ? AND resolved_at IS NULL",
			sourceUUID, sourceFingerprint, string(database.AlertStatusFiring)).
		Limit(1).Find(&a).Error; err != nil {
		slog.Warn("firing alert lookup failed", "err", err)
		return ""
	}
	return a.IncidentUUID
}
