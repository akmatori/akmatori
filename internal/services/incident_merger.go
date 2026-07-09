package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	mergeTimeout       = 20 * time.Second
	mergeMaxCandidates = 25
	// mergeThreshold is higher than correlationThreshold because a merge is
	// harder to undo than a skipped investigation.
	mergeThreshold = 0.8
	// mergeLookback bounds how far back completed incidents remain merge
	// candidates.
	mergeLookback = 24 * time.Hour
)

// MergeVerdict is the structured output of the post-investigation merge gate.
type MergeVerdict struct {
	Merge        bool    `json:"merge"`
	IncidentUUID string  `json:"incident_uuid"`
	Confidence   float64 `json:"confidence"`
	Reasoning    string  `json:"reasoning"`
}

// IncidentMerger runs after an alert-sourced investigation completes: a
// one-shot LLM call compares the just-diagnosed root cause against recent
// investigated incidents and, on a confident match, merges the newer incident
// into the earlier survivor. All failures are fail-open (no merge).
type IncidentMerger struct {
	caller   OneShotLLMCaller
	db       *gorm.DB
	registry ProviderRegistry // optional; nil = no Slack note on merge
}

// NewIncidentMerger constructs an IncidentMerger. caller may be nil (merger
// becomes a no-op); registry may be nil (merge happens without a Slack note).
func NewIncidentMerger(caller OneShotLLMCaller, db *gorm.DB, registry ProviderRegistry) *IncidentMerger {
	return &IncidentMerger{caller: caller, db: db, registry: registry}
}

// EvaluateAndMerge checks whether the freshly completed incident shares a root
// cause with an earlier investigated incident and merges it in when the LLM is
// confident. Gated on GeneralSettings.IncidentMergeEnabled (read live).
// Designed to run in a detached goroutine: every error path is fail-open and
// only logged by the caller.
func (m *IncidentMerger) EvaluateAndMerge(ctx context.Context, incidentUUID string) error {
	if m.caller == nil {
		return nil
	}
	gs, err := database.GetOrCreateGeneralSettings()
	if err != nil {
		return fmt.Errorf("merge: load general settings: %w", err)
	}
	if !gs.GetIncidentMergeEnabled() {
		return nil
	}

	var incident database.Incident
	if err := m.db.WithContext(ctx).Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		return fmt.Errorf("merge: load incident: %w", err)
	}
	if incident.SourceKind != database.IncidentSourceKindAlert {
		return nil
	}
	if incident.Status != database.IncidentStatusCompleted && incident.Status != database.IncidentStatusMonitor {
		return nil
	}
	if strings.TrimSpace(incident.Response) == "" {
		return nil // no diagnosed root cause to compare
	}

	candidates, err := m.fetchMergeCandidates(ctx, &incident)
	if err != nil {
		return fmt.Errorf("merge: fetch candidates: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	settings, err := database.GetLLMSettings()
	if err != nil {
		return fmt.Errorf("merge: load llm settings: %w", err)
	}
	if settings == nil || settings.APIKey == "" {
		return fmt.Errorf("merge: LLM settings not configured")
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return fmt.Errorf("merge: could not build LLM worker settings")
	}

	userPrompt := buildMergeUserPrompt(&incident, candidates)

	callCtx, cancel := context.WithTimeout(ctx, mergeTimeout)
	defer cancel()

	raw, err := m.caller.OneShotLLM(callCtx, worker, mergeSystemPrompt, userPrompt, 250, 0.0)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			return nil // fail-open
		}
		return fmt.Errorf("merge: llm call: %w", err)
	}

	verdict, err := parseMergeVerdict(raw)
	if err != nil {
		slog.Debug("incident merger: invalid response", "err", err, "raw", raw)
		return nil
	}
	if !verdict.Merge || verdict.Confidence < mergeThreshold {
		return nil
	}

	// Hallucination guard: the survivor must be one of the candidates we sent.
	found := false
	for _, cand := range candidates {
		if cand.UUID == verdict.IncidentUUID {
			found = true
			break
		}
	}
	if !found {
		slog.Debug("incident merger: hallucinated UUID rejected", "uuid", verdict.IncidentUUID)
		return nil
	}

	if err := m.mergeIncidents(ctx, incidentUUID, verdict.IncidentUUID); err != nil {
		return fmt.Errorf("merge: apply: %w", err)
	}
	slog.Info("incident merged into earlier incident",
		"merged", incidentUUID, "survivor", verdict.IncidentUUID,
		"confidence", verdict.Confidence, "reasoning", verdict.Reasoning)

	// Best-effort Slack note in the merged incident's thread; a failure here
	// never rolls back the merge.
	m.notifyMerged(ctx, &incident, verdict)
	return nil
}

// fetchMergeCandidates returns earlier investigated alert-sourced incidents
// whose investigations finished within the lookback window. Only incidents
// that started before the just-completed one qualify, so merges always flow
// newer -> older and cannot form cycles.
func (m *IncidentMerger) fetchMergeCandidates(ctx context.Context, incident *database.Incident) ([]candidateRow, error) {
	cutoff := time.Now().Add(-mergeLookback)
	statuses := []string{
		string(database.IncidentStatusCompleted),
		string(database.IncidentStatusMonitor),
	}

	var rows []candidateRow
	err := m.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("uuid, title, status, response, context, started_at, alert_fingerprint").
		Where("source_kind = ? AND uuid <> ? AND status IN ? AND completed_at >= ? AND started_at < ? AND response <> ''",
			database.IncidentSourceKindAlert, incident.UUID, statuses, cutoff, incident.StartedAt).
		Order("started_at DESC").
		Limit(mergeMaxCandidates).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// mergeIncidents re-points the merged incident's alert rows to the survivor,
// extends the survivor's monitor window, and marks the merged incident with
// status=merged + merged_into_uuid. Both rows are locked in UUID order to
// avoid deadlocks with concurrent completion/resolve transactions.
func (m *IncidentMerger) mergeIncidents(ctx context.Context, mergedUUID, survivorUUID string) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockOrder := []string{mergedUUID, survivorUUID}
		if survivorUUID < mergedUUID {
			lockOrder = []string{survivorUUID, mergedUUID}
		}
		incidents := map[string]*database.Incident{}
		for _, u := range lockOrder {
			var inc database.Incident
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("uuid = ?", u).First(&inc).Error; err != nil {
				return fmt.Errorf("load incident %s: %w", u, err)
			}
			incidents[u] = &inc
		}
		merged, survivor := incidents[mergedUUID], incidents[survivorUUID]

		// Revalidate under lock: both sides must still be in a mergeable
		// state (a concurrent merge or reopened investigation aborts this one).
		if merged.Status != database.IncidentStatusCompleted && merged.Status != database.IncidentStatusMonitor {
			return fmt.Errorf("merged incident %s no longer mergeable (status %s)", mergedUUID, merged.Status)
		}
		if survivor.Status != database.IncidentStatusCompleted && survivor.Status != database.IncidentStatusMonitor {
			return fmt.Errorf("survivor incident %s no longer mergeable (status %s)", survivorUUID, survivor.Status)
		}

		// Re-point alert rows. The uniq_firing_alert partial index is global
		// across incidents, so this cannot introduce a duplicate firing row.
		if err := tx.Model(&database.Alert{}).
			Where("incident_uuid = ?", mergedUUID).
			Update("incident_uuid", survivorUUID).Error; err != nil {
			return fmt.Errorf("re-point alerts: %w", err)
		}

		// Extend the survivor's watch window like LinkAlertToIncident does.
		if survivor.Status == database.IncidentStatusMonitor {
			var settings database.GeneralSettings
			tx.First(&settings) // ignore error: zero value gives 60-min default
			newUntil := time.Now().Add(settings.GetAlertMonitorWindow())
			if survivor.MonitorUntil == nil || newUntil.After(*survivor.MonitorUntil) {
				if err := tx.Model(&database.Incident{}).
					Where("uuid = ?", survivorUUID).
					Update("monitor_until", &newUntil).Error; err != nil {
					return fmt.Errorf("extend survivor monitor window: %w", err)
				}
			}
		}

		if err := tx.Model(&database.Incident{}).
			Where("uuid = ?", mergedUUID).
			Updates(map[string]interface{}{
				"status":           database.IncidentStatusMerged,
				"merged_into_uuid": survivorUUID,
				"monitor_until":    nil,
			}).Error; err != nil {
			return fmt.Errorf("mark incident merged: %w", err)
		}
		return nil
	})
}

// notifyMerged posts a short note in the merged incident's originating thread
// pointing at the survivor. Best-effort: any failure is logged and swallowed.
func (m *IncidentMerger) notifyMerged(ctx context.Context, merged *database.Incident, verdict MergeVerdict) {
	if m.registry == nil || merged.SlackChannelID == "" || merged.SlackMessageTS == "" {
		return
	}

	var channel database.Channel
	if err := m.db.WithContext(ctx).Preload("Integration").
		Where("external_id = ? AND enabled = ? AND can_post = ?", merged.SlackChannelID, true, true).
		First(&channel).Error; err != nil {
		slog.Debug("incident merger: no postable channel for merge note", "external_id", merged.SlackChannelID, "err", err)
		return
	}
	provider, err := m.registry.Get(channel.Integration.Provider)
	if err != nil {
		slog.Debug("incident merger: provider unavailable for merge note", "provider", channel.Integration.Provider, "err", err)
		return
	}

	var survivor database.Incident
	survivorLabel := verdict.IncidentUUID
	if err := m.db.WithContext(ctx).Where("uuid = ?", verdict.IncidentUUID).First(&survivor).Error; err == nil && survivor.Title != "" {
		survivorLabel = fmt.Sprintf("%s (%s)", survivor.Title, shortUUID(verdict.IncidentUUID))
	}

	text := fmt.Sprintf(":twisted_rightwards_arrows: This incident was merged into *%s* — same root cause: %s",
		survivorLabel, verdict.Reasoning)
	if _, err := provider.PostThreadReply(ctx, &channel, merged.SlackMessageTS, text); err != nil {
		slog.Warn("incident merger: merge note failed", "incident", merged.UUID, "err", err)
	}
}

func shortUUID(u string) string {
	if len(u) > 8 {
		return u[:8]
	}
	return u
}

// parseMergeVerdict cleans LLM output and decodes it into a MergeVerdict.
func parseMergeVerdict(raw string) (MergeVerdict, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return MergeVerdict{}, fmt.Errorf("empty response")
	}

	var v MergeVerdict
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		return MergeVerdict{}, fmt.Errorf("decode: %w", err)
	}
	if v.Confidence < 0 {
		v.Confidence = 0
	}
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	v.IncidentUUID = strings.TrimSpace(v.IncidentUUID)
	v.Reasoning = strings.TrimSpace(v.Reasoning)
	return v, nil
}

// buildMergeUserPrompt renders the completed incident's diagnosis and the
// candidate list. Root-cause snippets are longer than the ingest-time
// correlation prompt because the diagnosis text is the primary signal here.
func buildMergeUserPrompt(incident *database.Incident, candidates []candidateRow) string {
	const diagnosisCap = 600
	const candidateCap = 400

	var sb strings.Builder
	sb.WriteString("Just-completed incident:\n")
	sb.WriteString(fmt.Sprintf("  Title: %s\n", sanitizeForPrompt(incident.Title)))
	sb.WriteString(fmt.Sprintf("  Diagnosis: %s\n",
		truncateForPrompt(sanitizeForPrompt(strings.TrimSpace(incident.Response)), diagnosisCap)))

	sb.WriteString("\nEarlier investigated incidents (most recent first):\n")
	now := time.Now()
	for i, cand := range candidates {
		age := now.Sub(cand.StartedAt).Round(time.Minute)
		title := cand.Title
		if title == "" {
			title = "(no title)"
		}
		sb.WriteString(fmt.Sprintf("\n%d. UUID: %s\n   Age: %s\n   Title: %s\n   Diagnosis: %s\n",
			i+1, cand.UUID, age, sanitizeForPrompt(title),
			truncateForPrompt(sanitizeForPrompt(strings.TrimSpace(cand.Response)), candidateCap)))
	}
	return sb.String()
}

const mergeSystemPrompt = `You decide whether a just-completed incident investigation identified the SAME ROOT CAUSE as one of the earlier investigated incidents, meaning they are really one incident (e.g. the same bad deploy, outage, or infrastructure failure affecting multiple hosts).

Return STRICT JSON:
  {"merge": true|false, "incident_uuid": "<UUID or empty string>", "confidence": <0..1>, "reasoning": "<≤200 char explanation>"}

Rules:
- Compare the DIAGNOSED ROOT CAUSES, not just alert names or symptoms.
- Set merge=true ONLY when both investigations clearly point at the same underlying event or cause. Different hosts are fine when the cause is shared (same deploy, same upstream outage, same config change).
- Do NOT merge incidents that merely look similar — the same alert type on unrelated hosts with independent causes stays separate.
- incident_uuid MUST be one of the UUIDs from the candidate list. If merge=false, set it to "".
- When uncertain, prefer merge=false (keeping incidents separate is safe; a wrong merge hides a real event).

Confidence:
  0.9-1.0: both diagnoses name the same specific cause (same deploy/change/outage identifier)
  0.8: diagnoses describe the same failure mechanism with consistent timing
  0.5-0.7: plausibly related but the causes are not clearly the same event
  0.0-0.4: different causes or not enough diagnostic detail

Output JSON only. No code fences.`
