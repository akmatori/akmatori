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

const correlationTimeout = 15 * time.Second

// CorrelationConfig holds tuneable parameters for the AI correlation gate.
// All fields have documented defaults; zero values fall back to those defaults
// when the config is constructed via CorrelationConfigWithDefaults.
//
// Defaults: Window=30m, FingerprintWindow=24h, MaxCandidates=20, Threshold=0.7, LongWindowDays=7, Enabled=false.
type CorrelationConfig struct {
	Enabled            bool
	Window             time.Duration // how far back to search for non-fingerprint candidate incidents
	FingerprintWindow  time.Duration // wider lookback for fingerprint-matched candidate incidents
	MaxCandidates      int           // LIMIT on the candidate query
	Threshold          float64       // minimum confidence to collapse the alert
	LongWindowDays     int           // lookback in days for fingerprint-matching unresolved incidents
}

// CorrelationConfigWithDefaults returns a config with documented defaults
// applied wherever the caller supplied zero values. Enabled is always
// taken from the caller's value (default-false is intentional).
func CorrelationConfigWithDefaults(c CorrelationConfig) CorrelationConfig {
	if c.Window <= 0 {
		c.Window = 30 * time.Minute
	}
	if c.FingerprintWindow <= 0 {
		c.FingerprintWindow = 24 * time.Hour // 1440 minutes
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 20
	}
	if c.Threshold <= 0 {
		c.Threshold = 0.7
	}
	if c.LongWindowDays <= 0 {
		c.LongWindowDays = 7
	}
	return c
}

// CorrelationVerdict is the structured output from the correlation gate.
type CorrelationVerdict struct {
	Correlated      bool    `json:"correlated"`
	IncidentUUID    string  `json:"incident_uuid"`
	Confidence      float64 `json:"confidence"`
	Reasoning       string  `json:"reasoning"`
	IsLongWindowMatch bool  `json:"-"` // true when match came exclusively from the long-window query
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

// loadConfig reads GeneralSettings from the DB and applies code defaults to nil
// fields, returning a fully-populated CorrelationConfig for this call.
func (c *AlertCorrelator) loadConfig() (CorrelationConfig, error) {
	gs, err := database.GetOrCreateGeneralSettings()
	if err != nil {
		return CorrelationConfigWithDefaults(CorrelationConfig{}), fmt.Errorf("load general settings: %w", err)
	}
	var cfg CorrelationConfig
	if gs.AlertCorrelationEnabled != nil {
		cfg.Enabled = *gs.AlertCorrelationEnabled
	}
	return CorrelationConfigWithDefaults(cfg), nil
}

// Threshold returns the effective correlation confidence threshold from DB settings.
func (c *AlertCorrelator) Threshold() float64 {
	cfg, err := c.loadConfig()
	if err != nil {
		return CorrelationConfigWithDefaults(CorrelationConfig{}).Threshold
	}
	return cfg.Threshold
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
//   - zero candidates in the window (no LLM call made)
//
// ErrWorkerNotConnected is returned as-is so callers can fail-open cleanly.
// Parse failures are logged at debug and treated as "no match".
func (c *AlertCorrelator) Correlate(ctx context.Context, sourceUUID string, alert alerts.NormalizedAlert) (CorrelationVerdict, error) {
	noMatch := CorrelationVerdict{}

	if c.caller == nil {
		return noMatch, nil
	}

	cfg, err := c.loadConfig()
	if err != nil {
		return noMatch, fmt.Errorf("correlate: %w", err)
	}
	if !cfg.Enabled {
		return noMatch, nil
	}

	fingerprint := ComputeAlertFingerprint(sourceUUID, alert.AlertName, alert.TargetHost)

	candidates, longWindowUUIDs, err := c.fetchCandidates(ctx, fingerprint, cfg.Window, cfg.FingerprintWindow, cfg.LongWindowDays, cfg.MaxCandidates)
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

	userPrompt := buildCorrelationUserPrompt(alert, candidates, longWindowUUIDs)

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
		// Mark as long-window when the matched incident was sourced exclusively
		// from the long-window query (not present in the standard-window candidates).
		if _, ok := longWindowUUIDs[verdict.IncidentUUID]; ok {
			verdict.IsLongWindowMatch = true
		}
	}

	return verdict, nil
}

// fetchCandidates queries recent alert-sourced incidents that are viable targets
// for recurrence attachment. Failed incidents are always excluded.
//
// Three complementary queries are merged and deduplicated:
//
//  1. Standard window (window, typically 30m): incidents with an empty/null
//     fingerprint (legacy rows) OR all incidents when the incoming fingerprint
//     is empty.
//
//  2. Fingerprint window (fingerprintWindow, default 24h): incidents whose
//     alert_fingerprint matches the incoming fingerprint exactly, covering the
//     wider look-back for exact same-rule/same-host recurrences. Only runs
//     when fingerprint is non-empty.
//
//  3. Long-window (longWindowDays, default 7d): fingerprint-matching incidents
//     with status IN ('running','diagnosed') — known-open blocked incidents.
//     UUIDs found exclusively here are tracked in longWindowUUIDs so the
//     caller can set IsLongWindowMatch on the verdict.
func (c *AlertCorrelator) fetchCandidates(ctx context.Context, fingerprint string, window time.Duration, fingerprintWindow time.Duration, longWindowDays int, maxCandidates int) ([]candidateRow, map[string]struct{}, error) {
	windowStart := time.Now().Add(-window)
	activeStatuses := []string{
		string(database.IncidentStatusPending),
		string(database.IncidentStatusRunning),
		string(database.IncidentStatusDiagnosed),
		string(database.IncidentStatusCompleted),
	}

	// Query 1: standard window.
	// When we have a fingerprint, restrict to legacy (empty/null) fingerprint rows
	// so that fingerprint-matching rows are not double-counted with query 2.
	var rows []candidateRow
	q := c.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("uuid, title, status, response, context, started_at, alert_fingerprint").
		Where("source_kind = ? AND started_at >= ? AND status IN ?",
			database.IncidentSourceKindAlert, windowStart, activeStatuses).
		Where("NOT EXISTS (SELECT 1 FROM alert_suppression_logs WHERE incident_uuid = incidents.uuid)")

	if fingerprint != "" {
		q = q.Where("alert_fingerprint = '' OR alert_fingerprint IS NULL")
	}

	if err := q.Order("started_at DESC").Limit(maxCandidates).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}

	// Track all UUIDs to dedup subsequent queries.
	seenUUIDs := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		seenUUIDs[r.UUID] = struct{}{}
	}

	longWindowUUIDs := make(map[string]struct{})

	if fingerprint != "" {
		// Query 2: fingerprint-gated wider window. Exclude completed incidents so a
		// closed investigation cannot be correlated-into via AppendCorrelatedAlert,
		// which has no status guard. The LLM system prompt also says not to
		// correlate resolved incidents, but code-level filtering is the safer boundary.
		fpWindowStart := time.Now().Add(-fingerprintWindow)
		nonCompletedStatuses := []string{
			string(database.IncidentStatusPending),
			string(database.IncidentStatusRunning),
			string(database.IncidentStatusDiagnosed),
		}
		var fpRows []candidateRow
		fpErr := c.db.WithContext(ctx).
			Model(&database.Incident{}).
			Select("uuid, title, status, response, context, started_at, alert_fingerprint").
			Where("source_kind = ? AND started_at >= ? AND status IN ?",
				database.IncidentSourceKindAlert, fpWindowStart, nonCompletedStatuses).
			Where("alert_fingerprint = ?", fingerprint).
			Where("NOT EXISTS (SELECT 1 FROM alert_suppression_logs WHERE incident_uuid = incidents.uuid)").
			Order("started_at DESC").
			Limit(maxCandidates).
			Scan(&fpRows).Error
		if fpErr != nil {
			slog.Warn("alert correlator: fingerprint-window query failed", "err", fpErr)
		} else {
			for _, r := range fpRows {
				if _, already := seenUUIDs[r.UUID]; !already {
					rows = append(rows, r)
					seenUUIDs[r.UUID] = struct{}{}
				}
			}
		}

		// Query 3: long-window (running/diagnosed only) for known-open blocked incidents.
		if longWindowDays > 0 {
			longWindowStart := time.Now().Add(-time.Duration(longWindowDays) * 24 * time.Hour)
			openStatuses := []string{
				string(database.IncidentStatusRunning),
				string(database.IncidentStatusDiagnosed),
			}

			var longRows []candidateRow
			longErr := c.db.WithContext(ctx).
				Model(&database.Incident{}).
				Select("uuid, title, status, response, context, started_at, alert_fingerprint").
				Where("source_kind = ? AND started_at >= ? AND status IN ?",
					database.IncidentSourceKindAlert, longWindowStart, openStatuses).
				Where("alert_fingerprint = ?", fingerprint).
				Where("NOT EXISTS (SELECT 1 FROM alert_suppression_logs WHERE incident_uuid = incidents.uuid)").
				Order("started_at DESC").
				Limit(maxCandidates).
				Scan(&longRows).Error
			if longErr != nil {
				slog.Warn("alert correlator: long-window query failed", "err", longErr)
			} else {
				for _, r := range longRows {
					if _, already := seenUUIDs[r.UUID]; !already {
						rows = append(rows, r)
						longWindowUUIDs[r.UUID] = struct{}{}
						seenUUIDs[r.UUID] = struct{}{}
					}
				}
			}
		}
	}

	// Apply a global cap so that combined results from all three sub-queries never
	// exceed maxCandidates regardless of individual per-query limits.
	if len(rows) > maxCandidates {
		rows = rows[:maxCandidates]
	}

	return rows, longWindowUUIDs, nil
}

// buildCorrelationUserPrompt produces the numbered candidate list shown to the
// LLM. Each candidate includes its UUID, status, age, title, and a capped
// summary snippet so the prompt stays manageable. Long-window candidates
// (incidents that are known-open beyond the standard correlation window) are
// labeled as "[KNOWN OPEN ISSUE]" so the LLM can distinguish them from
// recently-started candidates.
func buildCorrelationUserPrompt(alert alerts.NormalizedAlert, candidates []candidateRow, longWindowUUIDs map[string]struct{}) string {
	const snippetCap = 200

	var sb strings.Builder
	sb.WriteString("Incoming alert:\n")
	sb.WriteString(fmt.Sprintf("  Name: %s\n", truncateForPrompt(sanitizeForPrompt(alert.AlertName), snippetCap)))
	if alert.TargetHost != "" {
		sb.WriteString(fmt.Sprintf("  Host: %s\n", truncateForPrompt(sanitizeForPrompt(alert.TargetHost), snippetCap)))
	}
	if alert.Summary != "" {
		sb.WriteString(fmt.Sprintf("  Summary: %s\n", truncateForPrompt(sanitizeForPrompt(alert.Summary), snippetCap)))
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

		statusLabel := cand.Status
		if _, isLong := longWindowUUIDs[cand.UUID]; isLong {
			statusLabel = cand.Status + " [KNOWN OPEN ISSUE — still active after extended period]"
		}

		sb.WriteString(fmt.Sprintf("\n%d. UUID: %s\n   Status: %s | Age: %s\n   Title: %s\n",
			i+1, cand.UUID, statusLabel, age, sanitizeForPrompt(title)))
		if snippet != "" {
			sb.WriteString(fmt.Sprintf("   Snippet: %s\n", snippet))
		}
	}

	return sb.String()
}

// sanitizeForPrompt strips newlines from a field sourced from external input so
// it cannot be used to inject additional prompt lines.
func sanitizeForPrompt(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
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
- Do NOT correlate resolved incidents with new firing alerts.
- Do NOT correlate alerts that have different alert names unless the context makes it unambiguous they are the same root cause.
- When uncertain, prefer correlated=false (creating a new incident is safe; false deduplication hides real events).

Confidence:
  0.9-1.0: identical alert name + host, active incident, timing consistent
  0.7-0.8: same host/service, related alert name, timing consistent
  0.5-0.6: possibly related but significant uncertainty
  0.0-0.4: different alert, different host, or not convinced

Output JSON only. No code fences.`
