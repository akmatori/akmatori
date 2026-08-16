package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

const (
	correlationTimeout       = 15 * time.Second
	correlationMaxCandidates = 25
	correlationThreshold     = 0.7

	// fingerprintFastPathWindow bounds how far back the deterministic
	// fingerprint match will reach. Liveness alone is not enough of a bound:
	// an incident held at "completed" by an alert its source never resolved
	// stays "live" indefinitely, and without a time bound it would absorb
	// every future recurrence of that alert forever, hiding real events.
	//
	// Measured against last activity (see liveIncidentCond), not started_at,
	// so an incident that keeps receiving alerts stays a valid target however
	// old it is. Sized under the stale-close default (3 days) so the fast path
	// stops attaching to an incident well before the sweep closes it.
	//
	// 48h rather than 24h on purpose: real installs carry alerts that re-fire
	// on a near-exact daily cadence, and a 24h window lands directly on that
	// period — observed gaps of 22h46m and 24h00m31s for the same alert on the
	// same host would fall on opposite sides of the boundary, so whether a
	// recurrence collapsed or spawned came down to seconds of jitter. A window
	// clearly wider than the cadence makes the behaviour deterministic: a
	// daily-recurring alert stays one rolling incident. Narrow this only after
	// checking it against the target install's actual re-fire intervals.
	fingerprintFastPathWindow = 48 * time.Hour

	// fingerprintFastPathReasoning is recorded on the alert row in place of an
	// LLM explanation. It has to read as an explanation to an operator looking
	// at why their alert was deduplicated.
	fingerprintFastPathReasoning = "exact alert fingerprint match (same source, alert name, and host) against an alert already on this live incident; linked without an LLM call"
)

// CorrelationConfig holds parameters for the AI correlation gate.
type CorrelationConfig struct {
	Enabled bool
}

// CorrelationVerdict is the structured output from the correlation gate.
type CorrelationVerdict struct {
	Correlated   bool    `json:"correlated"`
	IncidentUUID string  `json:"incident_uuid"`
	Confidence   float64 `json:"confidence"`
	Reasoning    string  `json:"reasoning"`
}

// IsConfident returns true when the verdict indicates a match with confidence
// at or above the supplied threshold.
func (v CorrelationVerdict) IsConfident(threshold float64) bool {
	return v.Correlated && v.Confidence >= threshold
}

// AlertCorrelator runs a one-shot LLM call to decide whether an incoming alert
// is a recurrence of a recent incident rather than a new event.
type AlertCorrelator struct {
	caller OneShotLLMCaller
	db     *gorm.DB
}

// NewAlertCorrelator constructs an AlertCorrelator. Pass nil for caller to
// produce an instance that always returns {Correlated: false} (fail-open).
// Config is read live from GeneralSettings on each Correlate call.
func NewAlertCorrelator(caller OneShotLLMCaller, db *gorm.DB) *AlertCorrelator {
	return &AlertCorrelator{caller: caller, db: db}
}

// loadConfig reads AlertCorrelationEnabled from GeneralSettings.
func (c *AlertCorrelator) loadConfig() (CorrelationConfig, error) {
	gs, err := database.GetOrCreateGeneralSettings()
	if err != nil {
		return CorrelationConfig{}, fmt.Errorf("load general settings: %w", err)
	}
	var cfg CorrelationConfig
	if gs.AlertCorrelationEnabled != nil {
		cfg.Enabled = *gs.AlertCorrelationEnabled
	}
	return cfg, nil
}

// Threshold returns the effective correlation confidence threshold.
func (c *AlertCorrelator) Threshold() float64 {
	return correlationThreshold
}

// candidateRow is a minimal projection of the Incident table used for
// candidate ranking so we don't load full_log into memory.
type candidateRow struct {
	UUID             string
	Title            string
	Status           string
	Response         string
	Context          database.JSONB
	StartedAt        time.Time
	AlertFingerprint string
}

// Correlate asks the LLM whether the incoming alert matches a recent incident.
// It is safe to call concurrently. Returns {Correlated: false} on:
//   - flag disabled (reads live from DB)
//   - nil caller
//   - zero candidates (no LLM call made)
//
// ErrWorkerNotConnected is returned as-is so callers can fail-open cleanly.
// Parse failures are logged at debug and treated as "no match".
func (c *AlertCorrelator) Correlate(ctx context.Context, sourceUUID string, alert alerts.NormalizedAlert) (CorrelationVerdict, error) {
	noMatch := CorrelationVerdict{}

	cfg, err := c.loadConfig()
	if err != nil {
		return noMatch, fmt.Errorf("correlate: %w", err)
	}
	if !cfg.Enabled {
		return noMatch, nil
	}

	// Fast path: an identical fingerprint on a live incident is a recurrence
	// by construction, so link it without an LLM call. Deliberately ahead of
	// the nil-caller check — deterministic deduplication should keep working
	// on an install with no LLM configured or a disconnected worker.
	//
	// Skipped entirely when the alert carries no target host. The fingerprint
	// is only hash(source, alertName, targetHost), so a host-less alert
	// collapses to its rule name across the whole fleet — every
	// "Balancer has more than 25 POPs disabled", wherever it fired, shares one
	// fingerprint. That is too coarse to link on deterministically, so those
	// alerts go to the LLM, which can weigh the summary and labels the
	// fingerprint throws away.
	if alert.TargetHost == "" {
		slog.Debug("alert correlator: no target host, skipping fingerprint fast path",
			"alert_name", alert.AlertName)
	} else {
		fingerprint := ComputeAlertFingerprint(sourceUUID, alert.AlertName, alert.TargetHost)
		matchUUID, err := c.matchByFingerprint(ctx, fingerprint, time.Now())
		if err != nil {
			// Fail-open into the LLM path rather than out of correlation
			// entirely: a DB hiccup should cost the shortcut, not the gate.
			slog.Warn("alert correlator: fingerprint fast path failed, falling back to LLM", "err", err)
		} else if matchUUID != "" {
			slog.Info("alert correlated by fingerprint fast path",
				"incident_uuid", matchUUID, "fingerprint", fingerprint)
			return CorrelationVerdict{
				Correlated:   true,
				IncidentUUID: matchUUID,
				Confidence:   1.0,
				Reasoning:    fingerprintFastPathReasoning,
			}, nil
		}
	}

	if c.caller == nil {
		return noMatch, nil
	}

	candidates, err := c.fetchCandidates(ctx)
	if err != nil {
		return noMatch, fmt.Errorf("correlate: fetch candidates: %w", err)
	}
	if len(candidates) == 0 {
		return noMatch, nil
	}

	settings, err := database.GetLLMSettings()
	if err != nil {
		return noMatch, fmt.Errorf("correlate: load llm settings: %w", err)
	}
	if settings == nil || settings.APIKey == "" {
		return noMatch, fmt.Errorf("correlate: LLM settings not configured")
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return noMatch, fmt.Errorf("correlate: could not build LLM worker settings")
	}

	userPrompt := buildCorrelationUserPrompt(alert, candidates)

	callCtx, cancel := context.WithTimeout(ctx, correlationTimeout)
	defer cancel()

	raw, err := c.caller.OneShotLLM(callCtx, worker, correlationSystemPrompt, userPrompt, 250, 0.0)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			return noMatch, err
		}
		return noMatch, fmt.Errorf("correlate: llm call: %w", err)
	}

	verdict, err := parseCorrelationVerdict(raw)
	if err != nil {
		slog.Debug("alert correlator: invalid response", "err", err, "raw", raw)
		return noMatch, nil
	}

	// Hallucination guard: reject any UUID the LLM invented that was not in the
	// candidate set we sent it.
	if verdict.Correlated {
		found := false
		for _, cand := range candidates {
			if cand.UUID == verdict.IncidentUUID {
				found = true
				break
			}
		}
		if !found {
			slog.Debug("alert correlator: hallucinated UUID rejected", "uuid", verdict.IncidentUUID)
			return noMatch, nil
		}
	}

	return verdict, nil
}

// fetchCandidates queries recent alert-sourced incidents that are viable targets
// for recurrence attachment: active incidents (pending/running/diagnosed),
// monitor incidents whose monitor window has not yet expired, and completed
// incidents that UpdateIncidentComplete held out of monitor mode because an
// alert was still firing when the investigation finished (see
// countFiringAlerts) — those are still open from the alerting system's
// perspective even though status reads "completed", so they must stay
// eligible until ResolveAlertTx promotes them to monitor.
func (c *AlertCorrelator) fetchCandidates(ctx context.Context) ([]candidateRow, error) {
	cond, args := liveTargetCond(time.Now())

	var rows []candidateRow
	err := c.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("uuid, title, status, response, context, started_at, alert_fingerprint").
		Where(cond, args...).
		Order("started_at DESC").
		Limit(correlationMaxCandidates).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// liveTargetCond builds the "viable recurrence target" predicate shared by the
// LLM candidate query and the fingerprint fast path. Keeping one definition
// matters: if the fast path considered an incident live that the candidate
// query did not, the two correlation routes would disagree about where a
// recurrence belongs.
func liveTargetCond(now time.Time) (string, []interface{}) {
	activeStatuses := []string{
		string(database.IncidentStatusPending),
		string(database.IncidentStatusRunning),
		string(database.IncidentStatusDiagnosed),
	}
	cond := "source_kind = ? AND (status IN ? OR (status = ? AND monitor_until >= ?) OR " +
		"(status = ? AND EXISTS (SELECT 1 FROM alerts WHERE alerts.incident_uuid = incidents.uuid " +
		"AND alerts.status = ? AND alerts.resolved_at IS NULL)))"
	args := []interface{}{
		database.IncidentSourceKindAlert, activeStatuses,
		string(database.IncidentStatusMonitor), now,
		string(database.IncidentStatusCompleted), string(database.AlertStatusFiring),
	}
	return cond, args
}

// matchByFingerprint looks for a live incident already carrying the exact same
// alert fingerprint — same source instance, same alert name, same target host —
// with activity inside fingerprintFastPathWindow.
//
// This is the deterministic half of the correlation gate. A repeat fire of an
// identical alert is a recurrence by definition; asking an LLM to confirm that
// costs a call and adds latency. Returns an empty UUID when there is no match,
// which sends the alert on to the LLM.
//
// The fingerprint is matched against BOTH the incident's own
// alert_fingerprint (the alert that spawned it) AND the fingerprint of every
// alert linked to it. The second half matters: once the LLM has decided alert
// Y belongs to incident A, every later fire of Y should follow that decision
// deterministically rather than being re-litigated by another LLM call that
// might answer differently. Matching only the spawning alert would send those
// recurrences back to the LLM forever.
//
// Linked alerts are matched regardless of their own status. A resolved alert
// on a monitor-status incident is precisely the recurrence case monitor mode
// exists for, so restricting this to still-firing alerts would break it.
//
// Most-recently-started wins: when several live incidents share a fingerprint,
// the newest is the one an operator is actually looking at.
func (c *AlertCorrelator) matchByFingerprint(ctx context.Context, fingerprint string, now time.Time) (string, error) {
	if fingerprint == "" {
		return "", nil
	}

	liveCond, liveArgs := liveTargetCond(now)
	activityCond, activityArgs := liveIncidentCond(now.Add(-fingerprintFastPathWindow))

	fingerprintCond := "(incidents.alert_fingerprint = ? OR EXISTS (" +
		"SELECT 1 FROM alerts WHERE alerts.incident_uuid = incidents.uuid AND alerts.fingerprint = ?))"

	args := append([]interface{}{}, liveArgs...)
	args = append(args, activityArgs...)
	args = append(args, fingerprint, fingerprint)

	var uuid string
	err := c.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("uuid").
		Where(liveCond+" AND "+activityCond+" AND "+fingerprintCond, args...).
		Order("started_at DESC").
		Limit(1).
		Scan(&uuid).Error
	if err != nil {
		return "", err
	}
	return uuid, nil
}

// buildCorrelationUserPrompt produces the numbered candidate list shown to the
// LLM. Each candidate includes its UUID, status, age, title, and a capped
// summary snippet so the prompt stays manageable.
func buildCorrelationUserPrompt(alert alerts.NormalizedAlert, candidates []candidateRow) string {
	const snippetCap = 200

	var sb strings.Builder
	sb.WriteString("Incoming alert:\n")
	fmt.Fprintf(&sb, "  Name: %s\n", truncateForPrompt(sanitizeForPrompt(alert.AlertName), snippetCap))
	if alert.TargetHost != "" {
		fmt.Fprintf(&sb, "  Host: %s\n", truncateForPrompt(sanitizeForPrompt(alert.TargetHost), snippetCap))
	}
	if alert.Summary != "" {
		fmt.Fprintf(&sb, "  Summary: %s\n", truncateForPrompt(sanitizeForPrompt(alert.Summary), snippetCap))
	}

	sb.WriteString("\nCandidate incidents (most recent first):\n")
	now := time.Now()
	for i, cand := range candidates {
		age := now.Sub(cand.StartedAt).Round(time.Minute)
		title := cand.Title
		if title == "" {
			title = "(no title yet)"
		}

		snippet := truncateForPrompt(sanitizeForPrompt(strings.TrimSpace(cand.Response)), snippetCap)
		if snippet == "" {
			// Fall back to context summary if no response yet.
			if v, ok := cand.Context["summary"]; ok {
				if s, ok := v.(string); ok {
					snippet = truncateForPrompt(sanitizeForPrompt(s), snippetCap)
				}
			}
		}

		fmt.Fprintf(&sb, "\n%d. UUID: %s\n   Status: %s | Age: %s\n   Title: %s\n",
			i+1, cand.UUID, sanitizeForPrompt(string(cand.Status)), age, sanitizeForPrompt(title))
		if snippet != "" {
			fmt.Fprintf(&sb, "   Snippet: %s\n", snippet)
		}
	}

	return sb.String()
}

// sanitizeForPrompt strips newlines (including Unicode equivalents) from a
// field sourced from external input so it cannot inject additional prompt lines.
func sanitizeForPrompt(s string) string {
	return strings.NewReplacer(
		"\n", " ", "\r", " ", "\v", " ", "\f", " ",
		"\u2028", " ", "\u2029", " ",
	).Replace(s)
}

// parseCorrelationVerdict cleans LLM output and decodes it into a CorrelationVerdict.
func parseCorrelationVerdict(raw string) (CorrelationVerdict, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return CorrelationVerdict{}, fmt.Errorf("empty response")
	}

	var v CorrelationVerdict
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		return CorrelationVerdict{}, fmt.Errorf("decode: %w", err)
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

const correlationSystemPrompt = `You decide whether an incoming alert is a RECURRENCE of a recent incident rather than a new event that needs its own investigation.

Return STRICT JSON:
  {"correlated": true|false, "incident_uuid": "<UUID or empty string>", "confidence": <0..1>, "reasoning": "<≤200 char explanation>"}

Rules:
- Set correlated=true ONLY when the alert describes the same failure on the same host/service as one of the listed candidates.
- incident_uuid MUST be one of the UUIDs from the candidate list. If correlated=false, set it to "".
- Do NOT correlate alerts that have different alert names unless the context makes it unambiguous they are the same root cause.
- When uncertain, prefer correlated=false (creating a new incident is safe; false deduplication hides real events).

Confidence:
  0.9-1.0: identical alert name + host, active incident, timing consistent
  0.7-0.8: same host/service, related alert name, timing consistent
  0.5-0.6: possibly related but significant uncertainty
  0.0-0.4: different alert, different host, or not convinced

Output JSON only. No code fences.`
