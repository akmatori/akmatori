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
// Defaults: Window=30m, MaxCandidates=20, Threshold=0.7, Enabled=false.
type CorrelationConfig struct {
	Enabled       bool
	Window        time.Duration // how far back to search for candidate incidents
	MaxCandidates int           // LIMIT on the candidate query
	Threshold     float64       // minimum confidence to collapse the alert
}

// CorrelationConfigWithDefaults returns a config with documented defaults
// applied wherever the caller supplied zero values. Enabled is always
// taken from the caller's value (default-false is intentional).
func CorrelationConfigWithDefaults(c CorrelationConfig) CorrelationConfig {
	if c.Window <= 0 {
		c.Window = 30 * time.Minute
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 20
	}
	if c.Threshold <= 0 {
		c.Threshold = 0.7
	}
	return c
}

// CorrelationVerdict is the structured output from the correlation gate.
type CorrelationVerdict struct {
	Correlated   bool
	IncidentUUID string
	Confidence   float64
	Reasoning    string
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
	cfg    CorrelationConfig
}

// NewAlertCorrelator constructs an AlertCorrelator. Pass nil for caller to
// produce an instance that always returns {Correlated: false} (fail-open).
func NewAlertCorrelator(caller OneShotLLMCaller, db *gorm.DB, cfg CorrelationConfig) *AlertCorrelator {
	return &AlertCorrelator{
		caller: caller,
		db:     db,
		cfg:    CorrelationConfigWithDefaults(cfg),
	}
}

// Threshold returns the minimum confidence required to consider a verdict
// a confident match.
func (c *AlertCorrelator) Threshold() float64 {
	return c.cfg.Threshold
}

// candidateRow is a minimal projection of the Incident table used for
// candidate ranking so we don't load full_log into memory.
type candidateRow struct {
	UUID      string
	Title     string
	Status    string
	Response  string
	Context   database.JSONB
	StartedAt time.Time
}

// correlationVerdictJSON is the expected JSON shape from the LLM.
type correlationVerdictJSON struct {
	Correlated   bool    `json:"correlated"`
	IncidentUUID string  `json:"incident_uuid"`
	Confidence   float64 `json:"confidence"`
	Reasoning    string  `json:"reasoning"`
}

// Correlate asks the LLM whether the incoming alert matches a recent incident.
// It is safe to call concurrently. Returns {Correlated: false} on:
//   - flag disabled
//   - nil caller
//   - zero candidates in the window (no LLM call made)
//
// ErrWorkerNotConnected is returned as-is so callers can fail-open cleanly.
// Parse failures are logged at debug and treated as "no match".
func (c *AlertCorrelator) Correlate(ctx context.Context, sourceUUID string, alert alerts.NormalizedAlert) (CorrelationVerdict, error) {
	noMatch := CorrelationVerdict{}

	if !c.cfg.Enabled || c.caller == nil {
		return noMatch, nil
	}

	candidates, err := c.fetchCandidates(ctx, sourceUUID)
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
		return noMatch, ErrWorkerNotConnected
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return noMatch, ErrWorkerNotConnected
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

// fetchCandidates queries recent alert-sourced incidents that are still active
// within the correlation window. Failed incidents are excluded because they are
// not viable targets for recurrence attachment.
func (c *AlertCorrelator) fetchCandidates(ctx context.Context, sourceUUID string) ([]candidateRow, error) {
	windowStart := time.Now().Add(-c.cfg.Window)
	activeStatuses := []string{
		string(database.IncidentStatusPending),
		string(database.IncidentStatusRunning),
		string(database.IncidentStatusDiagnosed),
		string(database.IncidentStatusCompleted),
	}

	var rows []candidateRow
	err := c.db.WithContext(ctx).
		Model(&database.Incident{}).
		Select("uuid, title, status, response, context, started_at").
		Where("source_kind = ? AND started_at >= ? AND status IN ?",
			database.IncidentSourceKindAlert, windowStart, activeStatuses).
		Order("started_at DESC").
		Limit(c.cfg.MaxCandidates).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// buildCorrelationUserPrompt produces the numbered candidate list shown to the
// LLM. Each candidate includes its UUID, status, age, title, and a capped
// summary snippet so the prompt stays manageable.
func buildCorrelationUserPrompt(alert alerts.NormalizedAlert, candidates []candidateRow) string {
	const snippetCap = 200

	var sb strings.Builder
	sb.WriteString("Incoming alert:\n")
	sb.WriteString(fmt.Sprintf("  Name: %s\n", alert.AlertName))
	if alert.TargetHost != "" {
		sb.WriteString(fmt.Sprintf("  Host: %s\n", alert.TargetHost))
	}
	if alert.Summary != "" {
		sb.WriteString(fmt.Sprintf("  Summary: %s\n", truncateForPrompt(alert.Summary, snippetCap)))
	}

	sb.WriteString("\nCandidate incidents (most recent first):\n")
	now := time.Now()
	for i, cand := range candidates {
		age := now.Sub(cand.StartedAt).Round(time.Minute)
		title := cand.Title
		if title == "" {
			title = "(no title yet)"
		}

		snippet := truncateForPrompt(strings.TrimSpace(cand.Response), snippetCap)
		if snippet == "" {
			// Fall back to context summary if no response yet.
			if v, ok := cand.Context["summary"]; ok {
				if s, ok := v.(string); ok {
					snippet = truncateForPrompt(s, snippetCap)
				}
			}
		}

		sb.WriteString(fmt.Sprintf("\n%d. UUID: %s\n   Status: %s | Age: %s\n   Title: %s\n",
			i+1, cand.UUID, cand.Status, age, title))
		if snippet != "" {
			sb.WriteString(fmt.Sprintf("   Snippet: %s\n", snippet))
		}
	}

	return sb.String()
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

	var j correlationVerdictJSON
	if err := json.Unmarshal([]byte(cleaned), &j); err != nil {
		return CorrelationVerdict{}, fmt.Errorf("decode: %w", err)
	}

	if j.Confidence < 0 {
		j.Confidence = 0
	}
	if j.Confidence > 1 {
		j.Confidence = 1
	}

	return CorrelationVerdict{
		Correlated:   j.Correlated,
		IncidentUUID: strings.TrimSpace(j.IncidentUUID),
		Confidence:   j.Confidence,
		Reasoning:    strings.TrimSpace(j.Reasoning),
	}, nil
}

const correlationSystemPrompt = `You decide whether an incoming alert is a RECURRENCE of a recent incident rather than a new event that needs its own investigation.

Return STRICT JSON:
  {"correlated": bool, "incident_uuid": "<UUID or empty string>", "confidence": <0..1>, "reasoning": "<≤200 char explanation>"}

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
