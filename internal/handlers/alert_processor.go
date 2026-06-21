package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// originalAlertTextMaxBytes caps how much of the verbatim alert text
// (raw_payload.original_message, populated by the Slack alert extractor) is
// rendered into the investigation prompt. Long Slack messages are truncated
// with a UTF-8-safe ellipsis. The agent feeds this excerpt to the
// runbook-searcher subagent, so a generous cap leaves room for distinctive
// phrasing on retries without re-fetching the source message.
const originalAlertTextMaxBytes = 1500

// alertSpawnKey returns a stable singleflight key for deduplicating concurrent
// alerts with the same origin tuple. The tuple is JSON-encoded before hashing
// to prevent delimiter collisions when fields contain "|". SourceFingerprint
// distinguishes alert instances that share a name and host but differ in their
// label set (e.g. two Alertmanager rules on the same host with different jobs).
func alertSpawnKey(sourceUUID, alertName, targetHost, fingerprint string) string {
	tuple, _ := json.Marshal([]string{sourceUUID, alertName, targetHost, fingerprint})
	h := sha256.Sum256(tuple)
	return hex.EncodeToString(h[:])
}


func (h *AlertHandler) processAlert(instance *database.AlertSourceInstance, normalized alerts.NormalizedAlert) {
	if normalized.Status == database.AlertStatusResolved {
		slog.Info("processing resolved alert", "alert_name", normalized.AlertName)
		go h.processResolvedAlert(instance.UUID, normalized)
		return
	}

	slog.Info("processing firing alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

	// Convert target labels to JSONB
	targetLabels := database.JSONB{}
	for k, v := range normalized.TargetLabels {
		targetLabels[k] = v
	}

	// Convert raw payload to JSONB
	rawPayload := database.JSONB{}
	for k, v := range normalized.RawPayload {
		rawPayload[k] = v
	}

	// Compute stable alert fingerprint for correlation candidate pre-filtering.
	alertFingerprint := services.ComputeAlertFingerprint(instance.UUID, normalized.AlertName, normalized.TargetHost)

	// Create incident context from alert data
	incidentCtx := &services.IncidentContext{
		Source:     instance.AlertSourceType.Name,
		SourceID:   normalized.SourceFingerprint,
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: instance.UUID,
		Context: database.JSONB{
			"alert_name":         normalized.AlertName,
			"severity":           string(normalized.Severity),
			"status":             string(normalized.Status),
			"summary":            normalized.Summary,
			"description":        normalized.Description,
			"target_host":        normalized.TargetHost,
			"target_service":     normalized.TargetService,
			"target_labels":      targetLabels,
			"metric_name":        normalized.MetricName,
			"metric_value":       normalized.MetricValue,
			"threshold_value":    normalized.ThresholdValue,
			"runbook_url":        normalized.RunbookURL,
			"source_alert_id":    normalized.SourceAlertID,
			"source_fingerprint": normalized.SourceFingerprint,
			"source_type":        instance.AlertSourceType.Name,
			"source_instance":    instance.Name,
			"raw_payload":        rawPayload,
			"alert_fingerprint":  alertFingerprint,
		},
		Message: fmt.Sprintf("%s - %s: %s", normalized.AlertName, normalized.TargetHost, normalized.Summary),
	}

	key := alertSpawnKey(instance.UUID, normalized.AlertName, normalized.TargetHost, normalized.SourceFingerprint)

	_, sfErr, _ := h.spawnGroup.Do(key, func() (interface{}, error) {
		// Correlation gate: attach to a recent open or monitor incident when confident.
		verdict, corrErr := h.correlate(context.Background(), instance.UUID, normalized)
		if corrErr != nil {
			if errors.Is(corrErr, services.ErrWorkerNotConnected) {
				slog.Debug("alert correlator worker not connected, spawning new incident")
			} else {
				slog.Warn("alert correlator error, spawning new incident", "err", corrErr)
			}
		}
		if verdict.IsConfident(h.correlationThreshold()) {
			slog.Info("alert correlated to existing incident", "incident_uuid", verdict.IncidentUUID, "confidence", verdict.Confidence)
			if err := h.skillService.LinkAlertToIncident(context.Background(), verdict.IncidentUUID, instance.UUID, normalized); err != nil {
				slog.Warn("failed to link alert to incident", "incident_uuid", verdict.IncidentUUID, "err", err)
			}
			// Best-effort Slack thread note on the matched incident's thread.
			if incident, err := h.skillService.GetIncident(verdict.IncidentUUID); err == nil && incident != nil &&
				incident.SlackChannelID != "" && incident.SlackMessageTS != "" {
				h.postSlackThreadReply(incident.SlackChannelID, incident.SlackMessageTS,
					fmt.Sprintf("Recurring alert: %s", normalized.AlertName))
			}
			return nil, nil
		}

		// Spawn incident manager
		incidentUUID, _, err := h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			slog.Error("failed to spawn incident manager", "err", err)
			return nil, err
		}

		// Insert the initial firing alert row for this new incident.
		if err := h.skillService.InsertFiringAlert(context.Background(), incidentUUID, instance.UUID, normalized); err != nil {
			slog.Warn("failed to insert firing alert", "incident_uuid", incidentUUID, "err", err)
		}

		slog.Info("created incident for alert", "incident_id", incidentUUID)

		// Post to Slack
		var channelID, threadTS string
		if h.isSlackEnabled() {
			var err error
			channelID, threadTS, err = h.postAlertToSlack(normalized, instance)
			if err != nil {
				slog.Warn("failed to post alert to Slack", "err", err)
			}
		}

		if channelID != "" && threadTS != "" {
			if err := h.updateIncidentSlackContext(incidentUUID, channelID, threadTS); err != nil {
				slog.Warn("failed to update incident Slack context", "err", err)
			}
		}

		// Update incident status and run investigation
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			slog.Warn("failed to update incident status", "err", err)
		}
		go h.runInvestigation(incidentUUID, normalized, instance, channelID, threadTS)

		return nil, nil
	})

	if sfErr != nil {
		slog.Error("failed to process alert", "err", sfErr)
		return
	}
	// Followers (isLeader==false): singleflight collapsed the burst; the leader
	// owned all work. The partial-unique index on alerts prevents duplicate rows
	// if the same alert arrives again before the leader's insert commits.
}

// ProcessAlertFromListenerChannel processes an alert that originated from a
// listener channel (Slack today). Replaces the pre-Task-6
// ProcessAlertFromSlackChannel which threaded a synthetic slack_channel
// AlertSourceInstance through the same pipeline; the channel row is now the
// authoritative source for routing and metadata.
func (h *AlertHandler) ProcessAlertFromListenerChannel(
	channel *database.Channel,
	normalized alerts.NormalizedAlert,
	slackChannelID string,
	slackMessageTS string,
) {
	if normalized.Status == database.AlertStatusResolved {
		slog.Info("processing resolved alert from listener channel", "alert_name", normalized.AlertName)
		go h.processResolvedAlert(channel.UUID, normalized)
		return
	}

	slog.Info("processing listener channel alert", "alert_name", normalized.AlertName, "severity", normalized.Severity)

	// Convert target labels to JSONB
	targetLabels := database.JSONB{}
	for k, v := range normalized.TargetLabels {
		targetLabels[k] = v
	}

	// Convert raw payload to JSONB
	rawPayload := database.JSONB{}
	for k, v := range normalized.RawPayload {
		rawPayload[k] = v
	}

	provider := string(channel.Integration.Provider)
	if provider == "" {
		provider = string(database.MessagingProviderSlack)
	}
	sourceLabel := provider + "_channel"
	sourceInstance := channel.DisplayName
	if sourceInstance == "" {
		sourceInstance = channel.ExternalID
	}

	// Compute stable alert fingerprint for correlation candidate pre-filtering.
	alertFingerprint := services.ComputeAlertFingerprint(channel.UUID, normalized.AlertName, normalized.TargetHost)

	// Create incident context from alert data
	incidentCtx := &services.IncidentContext{
		Source:     sourceLabel,
		SourceID:   normalized.SourceFingerprint,
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: channel.UUID,
		Context: database.JSONB{
			"alert_name":         normalized.AlertName,
			"severity":           string(normalized.Severity),
			"status":             string(normalized.Status),
			"summary":            normalized.Summary,
			"description":        normalized.Description,
			"target_host":        normalized.TargetHost,
			"target_service":     normalized.TargetService,
			"target_labels":      targetLabels,
			"metric_name":        normalized.MetricName,
			"metric_value":       normalized.MetricValue,
			"threshold_value":    normalized.ThresholdValue,
			"runbook_url":        normalized.RunbookURL,
			"source_alert_id":    normalized.SourceAlertID,
			"source_fingerprint": normalized.SourceFingerprint,
			"source_type":        sourceLabel,
			"source_instance":    sourceInstance,
			"channel_uuid":       channel.UUID,
			"raw_payload":        rawPayload,
			"slack_channel_id":   slackChannelID,
			"slack_message_ts":   slackMessageTS,
			"alert_fingerprint":  alertFingerprint,
		},
		Message: fmt.Sprintf("%s - %s: %s", normalized.AlertName, normalized.TargetHost, normalized.Summary),
	}

	key := alertSpawnKey(channel.UUID, normalized.AlertName, normalized.TargetHost, normalized.SourceFingerprint)

	_, sfErr, _ := h.spawnGroup.Do(key, func() (interface{}, error) {
		// Correlation gate: attach to a recent open or monitor incident when confident.
		verdict, corrErr := h.correlate(context.Background(), channel.UUID, normalized)
		if corrErr != nil {
			if errors.Is(corrErr, services.ErrWorkerNotConnected) {
				slog.Debug("alert correlator worker not connected, spawning new incident")
			} else {
				slog.Warn("alert correlator error, spawning new incident", "err", corrErr)
			}
		}
		if verdict.IsConfident(h.correlationThreshold()) {
			slog.Info("listener channel alert correlated to existing incident", "incident_uuid", verdict.IncidentUUID, "confidence", verdict.Confidence)
			if err := h.skillService.LinkAlertToIncident(context.Background(), verdict.IncidentUUID, channel.UUID, normalized); err != nil {
				slog.Warn("failed to link alert to incident", "incident_uuid", verdict.IncidentUUID, "err", err)
			}
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, false)
			h.postSlackThreadReply(slackChannelID, slackMessageTS,
				fmt.Sprintf("Alert merged into existing incident (ID: %s)", verdict.IncidentUUID))
			return nil, nil
		}

		// Spawn incident manager
		incidentUUID, _, err := h.skillService.SpawnIncidentManager(incidentCtx)
		if err != nil {
			slog.Error("failed to spawn incident manager for listener channel alert", "err", err)
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			h.postSlackThreadReply(slackChannelID, slackMessageTS,
				fmt.Sprintf("Failed to create incident: %v", err))
			return nil, err
		}

		// Insert the initial firing alert row for this new incident.
		if err := h.skillService.InsertFiringAlert(context.Background(), incidentUUID, channel.UUID, normalized); err != nil {
			slog.Warn("failed to insert firing alert", "incident_uuid", incidentUUID, "err", err)
		}

		slog.Info("created incident for listener channel alert", "incident_id", incidentUUID)

		// Update incident with Slack context for thread replies
		if err := h.updateIncidentSlackContext(incidentUUID, slackChannelID, slackMessageTS); err != nil {
			slog.Warn("failed to update incident Slack context", "err", err)
		}

		// Update incident status and run investigation
		if err := h.skillService.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
			slog.Warn("failed to update incident status", "err", err)
		}

		go h.runListenerChannelInvestigation(incidentUUID, normalized, channel, slackChannelID, slackMessageTS)

		return nil, nil
	})

	if sfErr != nil {
		slog.Error("failed to process listener channel alert", "err", sfErr)
		return
	}
	// Followers (isLeader==false): singleflight collapsed the burst; the leader owned all work.
}

// processResolvedAlert finds the matching firing alert row in the alerts table
// and marks it resolved. When no firing alerts remain for the incident and the
// incident is in completed or monitor status, the monitor window is shortened to
// min(monitor_until, resolved_at + window) so the watch period ends promptly.
// A no-match is logged and silently dropped — it is not an error (the alert
// may have fired before Akmatori was deployed, or the source sent a duplicate
// resolved notification). A best-effort Slack thread reply is posted to the
// incident's source thread after the transaction commits.
func (h *AlertHandler) processResolvedAlert(sourceUUID string, normalized alerts.NormalizedAlert) {
	db := database.GetDB()
	if db == nil {
		slog.Warn("processResolvedAlert: database not available")
		return
	}

	fingerprint := services.ComputeAlertFingerprint(sourceUUID, normalized.AlertName, normalized.TargetHost)
	now := time.Now()
	var linkedIncidentUUID string

	if err := db.Transaction(func(tx *gorm.DB) error {
		// Prefer source_fingerprint (external adapter ID); fall back to the
		// computed fingerprint derived from alertName + targetHost.
		var a database.Alert
		found := false
		if normalized.SourceFingerprint != "" {
			if err := tx.Where(
				"source_uuid = ? AND source_fingerprint = ? AND status = ? AND resolved_at IS NULL",
				sourceUUID, normalized.SourceFingerprint, string(database.AlertStatusFiring),
			).Order("fired_at DESC").Limit(1).First(&a).Error; err == nil {
				found = true
			}
		}
		if !found {
			if err := tx.Where(
				"source_uuid = ? AND fingerprint = ? AND status = ? AND resolved_at IS NULL",
				sourceUUID, fingerprint, string(database.AlertStatusFiring),
			).Order("fired_at DESC").Limit(1).First(&a).Error; err != nil {
				slog.Info("processResolvedAlert: no matching firing alert, dropping",
					"alert_name", normalized.AlertName, "source_uuid", sourceUUID)
				return nil
			}
		}

		linkedIncidentUUID = a.IncidentUUID

		// Mark the alert resolved.
		resolvedAt := now
		if err := tx.Model(&a).Updates(map[string]interface{}{
			"status":      string(database.AlertStatusResolved),
			"resolved_at": resolvedAt,
		}).Error; err != nil {
			return fmt.Errorf("mark alert resolved: %w", err)
		}

		// Check whether any firing alerts still remain for this incident.
		var firingCount int64
		if err := tx.Model(&database.Alert{}).
			Where("incident_uuid = ? AND status = ? AND resolved_at IS NULL",
				a.IncidentUUID, string(database.AlertStatusFiring)).
			Count(&firingCount).Error; err != nil {
			return fmt.Errorf("count firing alerts: %w", err)
		}
		if firingCount > 0 {
			return nil // active alerts remain; leave monitor_until unchanged
		}

		// All alerts resolved. Lock the incident row and potentially pull in the
		// monitor window so the watch period ends promptly.
		var incident database.Incident
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("uuid = ?", a.IncidentUUID).First(&incident).Error; err != nil {
			return fmt.Errorf("load incident: %w", err)
		}

		if incident.Status != database.IncidentStatusCompleted &&
			incident.Status != database.IncidentStatusMonitor {
			return nil
		}

		settings, err := database.GetOrCreateGeneralSettings()
		if err != nil {
			slog.Warn("processResolvedAlert: could not load settings, skipping monitor_until update", "err", err)
			return nil
		}
		window := settings.GetAlertMonitorWindow()
		newUntil := resolvedAt.Add(window)
		if incident.MonitorUntil == nil || newUntil.Before(*incident.MonitorUntil) {
			if err := tx.Model(&incident).Update("monitor_until", newUntil).Error; err != nil {
				return fmt.Errorf("update monitor_until: %w", err)
			}
		}
		return nil
	}); err != nil {
		slog.Warn("processResolvedAlert: error", "err", err,
			"alert_name", normalized.AlertName, "source_uuid", sourceUUID)
		return
	}

	if linkedIncidentUUID == "" {
		return // no matching alert found; already logged above
	}

	slog.Info("processResolvedAlert: alert resolved",
		"alert_name", normalized.AlertName, "incident_uuid", linkedIncidentUUID)

	// Best-effort Slack thread reply on the incident's source thread.
	if h.skillService != nil {
		if incident, err := h.skillService.GetIncident(linkedIncidentUUID); err == nil && incident != nil &&
			incident.SlackChannelID != "" && incident.SlackMessageTS != "" {
			h.postSlackThreadReply(incident.SlackChannelID, incident.SlackMessageTS,
				fmt.Sprintf("Alert resolved: %s", normalized.AlertName))
		}
	}
}

// extractOriginalMessage returns the verbatim original alert message stored in
// payload["original_message"] (set by the alert extractor in
// internal/alerts/extraction/extractor.go), trimmed of surrounding whitespace
// and truncated with a "..." suffix when it exceeds maxBytes. Truncation is
// rune-aware so multi-byte characters at the cap (common in Slack-channel
// alerts) are not split, matching the UTF-8-safe pattern used elsewhere in
// this codebase (see internal/slack/typing.go). Returns "" when the field is
// missing, empty, or not a string.
func extractOriginalMessage(payload map[string]interface{}, maxBytes int) string {
	raw, ok := payload["original_message"]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	const ellipsis = "..."
	if maxBytes <= len(ellipsis) {
		return s[:maxBytes]
	}
	cut := maxBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

func (h *AlertHandler) buildInvestigationPrompt(alert alerts.NormalizedAlert, instance *database.AlertSourceInstance) string {
	return h.buildInvestigationPromptWithSource(alert,
		instance.AlertSourceType.DisplayName,
		instance.AlertSourceType.Name,
		instance.Name,
	)
}

// buildInvestigationPromptForChannel mirrors buildInvestigationPrompt for
// alerts that originated from a listener channel rather than a webhook-driven
// AlertSourceInstance. The header reads "Investigate this Slack channel alert"
// (sourceDisplay = "Slack channel") and Source identifies the channel by name
// so the agent can still scope runbook searches.
func (h *AlertHandler) buildInvestigationPromptForChannel(alert alerts.NormalizedAlert, channel *database.Channel) string {
	provider := string(channel.Integration.Provider)
	if provider == "" {
		provider = string(database.MessagingProviderSlack)
	}
	sourceDisplay := titleProvider(provider) + " channel"
	sourceTypeID := provider + "_channel"
	sourceInstance := channel.DisplayName
	if sourceInstance == "" {
		sourceInstance = channel.ExternalID
	}
	return h.buildInvestigationPromptWithSource(alert, sourceDisplay, sourceTypeID, sourceInstance)
}

// titleProvider capitalizes the first ASCII letter of a provider identifier
// so "slack" renders as "Slack" in user-visible prompts. strings.Title is
// deprecated and the input is already lowercase ASCII, so a hand-rolled
// title cast keeps this readable without pulling in unicode/cases.
func titleProvider(p string) string {
	if p == "" {
		return ""
	}
	first := p[0]
	if first >= 'a' && first <= 'z' {
		first = first - 'a' + 'A'
	}
	return string(first) + p[1:]
}

// buildInvestigationPromptWithSource is the common prompt-building core. The
// three source* parameters drive the header (sourceDisplay) and the "Source:"
// breadcrumb (sourceTypeID / sourceInstance), so the two call sites
// (AlertSourceInstance + Channel) stay in sync as the prompt evolves.
func (h *AlertHandler) buildInvestigationPromptWithSource(alert alerts.NormalizedAlert, sourceDisplay, sourceTypeID, sourceInstanceName string) string {
	prompt := fmt.Sprintf(`Investigate this %s alert:

Alert: %s
Host: %s
Service: %s
Severity: %s
Summary: %s
Description: %s`,
		sourceDisplay,
		alert.AlertName,
		alert.TargetHost,
		alert.TargetService,
		alert.Severity,
		alert.Summary,
		alert.Description,
	)

	// Source identifies the upstream alerting system + instance so the agent
	// can disambiguate which integration a runbook should target. The type
	// and instance Name columns are NOT NULL but the API has historically
	// allowed whitespace-only names through, so trim and render whichever
	// non-empty components remain to avoid emitting "Source: type /    "
	// stubs while still surfacing whichever cue is available.
	sourceType := strings.TrimSpace(sourceTypeID)
	sourceInstance := strings.TrimSpace(sourceInstanceName)
	switch {
	case sourceType != "" && sourceInstance != "":
		prompt += fmt.Sprintf("\nSource: %s / %s", sourceType, sourceInstance)
	case sourceType != "":
		prompt += fmt.Sprintf("\nSource: %s", sourceType)
	case sourceInstance != "":
		prompt += fmt.Sprintf("\nSource: %s", sourceInstance)
	}

	if alert.MetricName != "" {
		prompt += fmt.Sprintf("\nMetric: %s = %s", alert.MetricName, alert.MetricValue)
	}

	if alert.RunbookURL != "" {
		prompt += fmt.Sprintf("\nRunbook: %s", alert.RunbookURL)
	}

	// Always render the labeled "Original alert text:" block when the
	// extractor populated raw_payload.original_message. The agent feeds this
	// raw excerpt to the runbook-searcher subagent, so preserving it (even
	// when Description carries the same string) gives the agent the full
	// source text instead of the 100-char truncated summary that the
	// Slack-channel fallback path otherwise leaves in Description. Duplicating
	// the text under both Description and Original alert text is harmless (a
	// few hundred extra prompt bytes) and keeps the labeled anchor stable.
	if original := extractOriginalMessage(alert.RawPayload, originalAlertTextMaxBytes); original != "" {
		prompt += "\n\nOriginal alert text:\n" + original
	}

	prompt += `

Please:
1. Check if this is a known issue or pattern
2. Analyze available metrics and logs
3. Identify potential root causes
4. Suggest remediation steps with priority
5. Assess urgency and impact

Be specific and actionable. Reference any relevant data sources or scripts you use.`

	return prompt
}

func (h *AlertHandler) runInvestigation(incidentUUID string, alert alerts.NormalizedAlert, instance *database.AlertSourceInstance, channelID, threadTS string) {
	slog.Info("starting investigation for alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPrompt(alert, instance)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Show "is investigating..." in the alert thread for the duration of the
	// agent run when Slack is configured. The reaction lands on the bot's own
	// alert message (threadTS). Skipping when threadTS=="" because Slack
	// posting was disabled or failed earlier.
	//
	// Hoisted to function scope so the OnSuperseded callback can call
	// typing.Discard() — without that, the deferred Stop on a displaced run
	// fires setStatus("") + RemoveReaction against the shared thread and
	// erases the replacement run's banner + hourglass.
	var typing slackutil.TypingController
	if threadTS != "" && channelID != "" {
		if slackClient := h.slackManager.GetClient(); slackClient != nil {
			typing = slackutil.NewTypingController(slackutil.TypingControllerConfig{
				Client:      slackClient,
				ChannelID:   channelID,
				ThreadTS:    threadTS,
				ReactionRef: slack.ItemRef{Channel: channelID, Timestamp: threadTS},
			})
			typing.Start(context.Background())
			defer typing.Stop()
		}
	}

	// Use WebSocket-based agent worker
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
			slog.Info("using LLM provider", "provider", dbSettings.Provider, "model", dbSettings.Model)
		} else {
			slog.Warn("could not fetch LLM settings", "err", err)
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var superseded atomic.Bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		// Build task header for logging
		taskHeader := fmt.Sprintf("Alert Investigation: %s\nHost: %s\nSeverity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(output string) {
				lastStreamedLog += output
				// Update database with streamed log
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}
			},
			OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
				sessionID = sid
				response = output
				finalTokensUsed = tokensUsed
				finalExecutionTimeMs = executionTimeMs
				closeOnce.Do(func() { close(done) })
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
			// If a newer run displaces us for the same incident_id, the
			// replacement run owns finalization. Unblock and exit silently
			// instead of writing a failure that races the replacement.
			//
			// Discard the typing controller so the deferred Stop does not
			// clear the shared thread status / hourglass that the
			// replacement run has already established.
			OnSuperseded: func() {
				superseded.Store(true)
				if typing != nil {
					typing.Discard()
				}
				closeOnce.Do(func() { close(done) })
			},
		}

		runID, err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback)
		if err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackWithResult(channelID, threadTS, errorMsg, true)
			return
		}

		// Wait for completion
		<-done

		// Replacement run owns finalization — exit before touching the DB or Slack.
		if superseded.Load() {
			slog.Info("alert investigation superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		// Apply the configured formatting prompt before persistence and
		// Slack posting. Passthrough on error/empty or when formatting
		// is disabled.
		formattedResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog)

		// Re-attach the metrics footer AFTER formatting so the LLM never
		// sees it (and therefore cannot strip or rewrite ⏱️ Time / 🎯
		// Tokens). The deterministic footer lands at the end of the
		// stored DB response and the Slack final-message body, where
		// buildSlackFooter extracts it for the trailing metrics line.
		formattedWithMetrics := appendFinalizeMetrics(formattedResponse, finalExecutionTimeMs, finalTokensUsed, hasError)
		rawWithMetrics := appendFinalizeMetrics(response, finalExecutionTimeMs, finalTokensUsed, hasError)

		// Build full log using the raw response (with metrics) so full_log
		// preserves the original agent output for debugging.
		fullLog := taskHeader + lastStreamedLog
		if rawWithMetrics != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + rawWithMetrics
		}

		// Format response for Slack: parse structured blocks, summarize when
		// over budget, append the footer. Run the summarizer BEFORE
		// ReleaseRun so the slack-summarizer LLM call (which can take
		// several seconds) is also covered by the entry-still-in-map
		// invariant — a newer Start/Continue arriving during this call
		// can still fire OnSuperseded on the displaced waiter, and the
		// subsequent ReleaseRun returns false so the stale finalize is
		// abandoned.
		var formattedResp string
		if hasError {
			formattedResp = response
		} else if formattedWithMetrics != "" {
			formattedResp = finalizeSlackMessageBody(context.Background(), h.slackSummarizer, formattedWithMetrics, incidentUUID)
		} else {
			formattedResp = "Task completed (no output)"
		}

		// Claim ownership of finalization atomically. A newer alert / Slack
		// reply that displaced us during the formatter or slack summarizer
		// invalidates this run's finalization — exit silently and let the
		// replacement own the DB + Slack post.
		//
		// Discard the typing controller in the displaced path too: the
		// replacement's sendIncidentMessage may not have fired our
		// OnSuperseded yet (it fires after the map swap, and we win the
		// race to ReleaseRun), so without an explicit Discard here the
		// deferred typing.Stop() would issue setStatus("") + RemoveReaction
		// against the shared thread and erase the replacement run's banner
		// + hourglass.
		if !h.agentWSHandler.ReleaseRun(incidentUUID, runID) {
			if typing != nil {
				typing.Discard()
			}
			slog.Info("alert investigation displaced during finalization; leaving DB + Slack post to the new run", "incident_id", incidentUUID)
			return
		}

		// Update incident with full results — the formatted response is
		// what users see in the UI.
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, formattedWithMetrics, finalTokensUsed, finalExecutionTimeMs); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		h.updateSlackWithResult(channelID, threadTS, formattedResp, hasError)

		slog.Info("investigation completed for alert via WebSocket", "alert_name", alert.AlertName)
		return
	}

	// No WebSocket worker available
	slog.Error("agent worker not connected", "incident_id", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackWithResult(channelID, threadTS, errorMsg, true)
}

// runListenerChannelInvestigation runs investigation and posts results to the
// Slack thread that originated the alert. Replaces the pre-Task-6
// runSlackChannelInvestigation that took an AlertSourceInstance; the channel
// row is the authoritative source for the prompt now.
func (h *AlertHandler) runListenerChannelInvestigation(
	incidentUUID string,
	alert alerts.NormalizedAlert,
	channel *database.Channel,
	slackChannelID, slackMessageTS string,
) {
	slog.Info("starting investigation for listener channel alert", "alert_name", alert.AlertName, "incident_id", incidentUUID)

	// Build investigation prompt
	investigationPrompt := h.buildInvestigationPromptForChannel(alert, channel)
	taskWithGuidance := executor.PrependGuidance(investigationPrompt)

	// Show "is investigating..." in the thread header and put a hourglass
	// reaction on the original Slack-channel alert message for the duration
	// of the agent run. defer Stop covers all exit paths since the function
	// blocks on <-done before returning.
	//
	// The progress streamer pipes the agent's latest 🤔 reasoning line into
	// the typing controller's loading_messages, replacing Slack's default
	// rotating phrases ("searching...", "evaluating...", etc.) with what
	// the agent is actually thinking. There is no "Thinking..." placeholder
	// message — the typing banner + reaction are the activity signal; the
	// final result is posted as a fresh thread reply when the agent finishes.
	//
	// typing is hoisted to function scope so OnSuperseded can call
	// typing.Discard() — without that, the deferred Stop on a displaced run
	// erases the replacement run's banner + hourglass on the shared thread.
	var progressStreamer *SlackProgressStreamer
	var typing slackutil.TypingController
	if slackClient := h.slackManager.GetClient(); slackClient != nil {
		typing = slackutil.NewTypingController(slackutil.TypingControllerConfig{
			Client:      slackClient,
			ChannelID:   slackChannelID,
			ThreadTS:    slackMessageTS,
			ReactionRef: slack.ItemRef{Channel: slackChannelID, Timestamp: slackMessageTS},
		})
		typing.Start(context.Background())
		defer typing.Stop()
		progressStreamer = NewSlackProgressStreamer(typing.UpdateLoadingMessage, slackAppendInterval)
	}

	// Use WebSocket-based agent worker
	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		slog.Info("using WebSocket-based agent worker for Slack channel incident", "incident_id", incidentUUID)

		// Fetch LLM settings from database
		var llmSettings *LLMSettingsForWorker
		if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
			llmSettings = BuildLLMSettingsForWorker(dbSettings)
		}

		// Create channels for async result handling
		done := make(chan struct{})
		var closeOnce sync.Once
		var response string
		var sessionID string
		var hasError bool
		var superseded atomic.Bool
		var lastStreamedLog string
		var finalTokensUsed int
		var finalExecutionTimeMs int64

		taskHeader := fmt.Sprintf("Slack Channel Alert Investigation: %s\nHost: %s\nSeverity: %s\n\n--- Execution Log ---\n\n",
			alert.AlertName, alert.TargetHost, alert.Severity)

		callback := IncidentCallback{
			OnOutput: func(outputLog string) {
				lastStreamedLog += outputLog
				if err := h.skillService.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
					slog.Error("failed to update incident log", "err", err)
				}

				// Stream condensed progress to Slack (delta only, not full log).
				progressStreamer.AppendStatus(outputLog)
			},
			OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
				sessionID = sid
				response = output
				finalTokensUsed = tokensUsed
				finalExecutionTimeMs = executionTimeMs
				closeOnce.Do(func() { close(done) })
			},
			OnError: func(errorMsg string) {
				response = fmt.Sprintf("Error: %s", errorMsg)
				hasError = true
				closeOnce.Do(func() { close(done) })
			},
			// If a newer run displaces us for the same incident_id, the
			// replacement run owns finalization — DB update, Slack message,
			// channel reaction. Skip everything below to avoid racing it.
			//
			// Discard the typing controller so the deferred Stop does not
			// clear the shared thread status / hourglass that the
			// replacement run has already established.
			OnSuperseded: func() {
				superseded.Store(true)
				if typing != nil {
					typing.Discard()
				}
				closeOnce.Do(func() { close(done) })
			},
		}

		runID, err := h.agentWSHandler.StartIncident(incidentUUID, taskWithGuidance, llmSettings, h.skillService.GetEnabledSkillNames(), h.skillService.GetToolAllowlist(), callback)
		if err != nil {
			slog.Error("failed to start incident via WebSocket", "err", err)
			errorMsg := fmt.Sprintf("Failed to start investigation: %v", err)
			if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
				slog.Error("failed to update incident status", "err", updateErr)
			}
			h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
			h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
			return
		}

		// Wait for completion
		<-done

		// Flush any buffered progress lines so the last status is not lost.
		progressStreamer.Flush()

		// Replacement run owns finalization — exit silently before touching
		// the DB or channel reactions; the replacement posts its own final
		// thread reply when it finishes.
		if superseded.Load() {
			slog.Info("slack channel investigation superseded; leaving finalization to the new run", "incident_id", incidentUUID)
			return
		}

		slog.Info("investigation done", "incident_id", incidentUUID, "has_error", hasError, "response_len", len(response), "session_id", sessionID)

		// Apply the configured formatting prompt before persistence and
		// Slack posting. Passthrough on error/empty or when formatting
		// is disabled.
		dbResponse := applyResponseFormatter(context.Background(), h.responseFormatter, hasError, response, taskHeader+lastStreamedLog)

		// Re-attach the metrics footer AFTER formatting so the LLM never
		// sees it (and therefore cannot strip or rewrite ⏱️ Time / 🎯
		// Tokens). The deterministic footer lands at the end of the
		// stored DB response and the Slack final-message body, where
		// buildSlackFooter extracts it for the trailing metrics line.
		dbResponseWithMetrics := appendFinalizeMetrics(dbResponse, finalExecutionTimeMs, finalTokensUsed, hasError)
		rawWithMetrics := appendFinalizeMetrics(response, finalExecutionTimeMs, finalTokensUsed, hasError)

		// Build full log using the raw response (with metrics) so full_log
		// preserves the original agent output for debugging.
		fullLog := taskHeader + lastStreamedLog
		if rawWithMetrics != "" {
			fullLog += "\n\n--- Final Response ---\n\n" + rawWithMetrics
		}

		// Format response for Slack: parse structured blocks, summarize when
		// over budget, append the footer. Run the summarizer BEFORE
		// ReleaseRun so the slack-summarizer LLM call (which can take
		// several seconds) is also covered by the entry-still-in-map
		// invariant — a newer Start/Continue arriving during this call
		// can still fire OnSuperseded on the displaced waiter, and the
		// subsequent ReleaseRun returns false so the stale finalize is
		// abandoned.
		var formattedResponse string
		if hasError {
			formattedResponse = response
		} else if dbResponseWithMetrics != "" {
			formattedResponse = finalizeSlackMessageBody(context.Background(), h.slackSummarizer, dbResponseWithMetrics, incidentUUID)
		} else {
			formattedResponse = "Task completed (no output)"
		}

		// Claim ownership of finalization atomically. A newer alert / Slack
		// reply that displaced us during the formatter or slack summarizer
		// invalidates this run's finalization — exit silently and let the
		// replacement own the DB update and channel reaction / thread reply.
		//
		// Discard the typing controller in the displaced path too: the
		// replacement's sendIncidentMessage may not have fired our
		// OnSuperseded yet (it fires after the map swap, and we win the
		// race to ReleaseRun), so without an explicit Discard here the
		// deferred typing.Stop() would issue setStatus("") + RemoveReaction
		// against the shared thread and erase the replacement run's banner
		// + hourglass.
		if !h.agentWSHandler.ReleaseRun(incidentUUID, runID) {
			if typing != nil {
				typing.Discard()
			}
			slog.Info("slack channel investigation displaced during finalization; leaving DB + Slack post to the new run", "incident_id", incidentUUID)
			return
		}

		// Update incident
		finalStatus := database.IncidentStatusCompleted
		if hasError {
			finalStatus = database.IncidentStatusFailed
		}
		if err := h.skillService.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, dbResponseWithMetrics, finalTokensUsed, finalExecutionTimeMs); err != nil {
			slog.Error("failed to update incident complete", "err", err)
		}

		// Post the full final body as a fresh thread reply. chat.postMessage
		// allows up to ~40,000 chars so long summaries always reach the user.
		// Completion is signaled by the success/error reaction added above
		// and the typing banner clearing as the deferred Stop fires.
		h.updateSlackChannelReactions(slackChannelID, slackMessageTS, hasError)
		slog.Info("posting Slack final summary as new thread reply", "response_len", len(formattedResponse), "incident", incidentUUID)
		h.postSlackThreadReply(slackChannelID, slackMessageTS, formattedResponse)

		slog.Info("investigation completed for Slack channel alert", "alert", alert.AlertName)
		return
	}

	// No WebSocket worker available
	slog.Error("agent worker not connected for Slack channel incident", "incident", incidentUUID)
	errorMsg := "Agent worker not connected. Please check that the agent-worker container is running."
	if updateErr := h.skillService.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errorMsg, 0, 0); updateErr != nil {
		slog.Error("failed to update incident status", "err", updateErr)
	}
	h.updateSlackChannelReactions(slackChannelID, slackMessageTS, true)
	h.postSlackThreadReply(slackChannelID, slackMessageTS, errorMsg)
}
