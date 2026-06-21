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

const suppressionTimeout = 15 * time.Second

// SuppressionConfig holds tuneable parameters for the AI suppression gate.
// All fields have documented defaults applied via SuppressionConfigWithDefaults.
//
// Defaults: MaxSignatures=50, Threshold=0.7, Enabled=false.
type SuppressionConfig struct {
	Enabled       bool
	Threshold     float64 // minimum confidence to suppress the alert
	MaxSignatures int     // LIMIT on the signature query
}

// SuppressionConfigWithDefaults returns a config with documented defaults
// applied wherever the caller supplied zero values. Enabled is always
// taken from the caller's value (default-false is intentional).
func SuppressionConfigWithDefaults(c SuppressionConfig) SuppressionConfig {
	if c.MaxSignatures <= 0 {
		c.MaxSignatures = 50
	}
	if c.Threshold <= 0 {
		c.Threshold = 0.7
	}
	return c
}

// SuppressionVerdict is the structured output from the suppression gate.
type SuppressionVerdict struct {
	Suppressed    bool    `json:"suppressed"`
	SignatureName string  `json:"signature_name"`
	Confidence    float64 `json:"confidence"`
	Reasoning     string  `json:"reasoning"`
}

// IsConfident returns true when the verdict indicates a match with confidence
// at or above the supplied threshold.
func (v SuppressionVerdict) IsConfident(threshold float64) bool {
	return v.Suppressed && v.Confidence >= threshold
}

// AlertSuppressor uses memory signatures (memories flagged with suppress=true)
// to decide whether an incoming alert is a known false positive that should be
// suppressed without spawning a full investigation.
type AlertSuppressor struct {
	caller OneShotLLMCaller
	db     *gorm.DB
}

// NewAlertSuppressor constructs an AlertSuppressor. Pass nil for caller to
// produce an instance that always returns {Suppressed: false} (fail-open).
// Config is read live from GeneralSettings on each Evaluate call.
func NewAlertSuppressor(caller OneShotLLMCaller, db *gorm.DB) *AlertSuppressor {
	return &AlertSuppressor{caller: caller, db: db}
}

// loadConfig reads GeneralSettings from the DB and applies code defaults to nil
// fields, returning a fully-populated SuppressionConfig for this call.
func (s *AlertSuppressor) loadConfig() (SuppressionConfig, error) {
	if _, err := database.GetOrCreateGeneralSettings(); err != nil {
		return SuppressionConfigWithDefaults(SuppressionConfig{}), fmt.Errorf("load general settings: %w", err)
	}
	return SuppressionConfigWithDefaults(SuppressionConfig{}), nil
}

// Threshold returns the effective suppression confidence threshold from DB settings.
func (s *AlertSuppressor) Threshold() float64 {
	cfg, err := s.loadConfig()
	if err != nil {
		return SuppressionConfigWithDefaults(SuppressionConfig{}).Threshold
	}
	return cfg.Threshold
}

// signatureRow is a minimal projection of suppression-signature memory rows.
type signatureRow struct {
	Name string
	Body string
}

// Evaluate asks the LLM whether the incoming alert matches a known false-positive
// signature. It is safe to call concurrently. Returns {Suppressed: false} on:
//   - flag disabled (reads live from DB)
//   - nil caller
//   - zero signatures in DB (no LLM call made)
//
// ErrWorkerNotConnected is returned as-is so callers can fail-open cleanly.
// Parse failures are logged at debug and treated as "no match".
func (s *AlertSuppressor) Evaluate(ctx context.Context, alert alerts.NormalizedAlert) (SuppressionVerdict, error) {
	noMatch := SuppressionVerdict{}

	if s.caller == nil {
		return noMatch, nil
	}

	cfg, err := s.loadConfig()
	if err != nil {
		return noMatch, fmt.Errorf("suppressor: %w", err)
	}
	if !cfg.Enabled {
		return noMatch, nil
	}

	signatures, err := s.fetchSignatures(ctx, cfg.MaxSignatures)
	if err != nil {
		return noMatch, fmt.Errorf("suppressor: fetch signatures: %w", err)
	}
	if len(signatures) == 0 {
		return noMatch, nil
	}

	settings, err := database.GetLLMSettings()
	if err != nil {
		return noMatch, fmt.Errorf("suppressor: load llm settings: %w", err)
	}
	if settings == nil || settings.APIKey == "" {
		return noMatch, fmt.Errorf("suppressor: LLM settings not configured")
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return noMatch, fmt.Errorf("suppressor: could not build LLM worker settings")
	}

	userPrompt := buildSuppressionUserPrompt(alert, signatures)

	callCtx, cancel := context.WithTimeout(ctx, suppressionTimeout)
	defer cancel()

	raw, err := s.caller.OneShotLLM(callCtx, worker, suppressionSystemPrompt, userPrompt, 300, 0.0)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			return noMatch, err
		}
		return noMatch, fmt.Errorf("suppressor: llm call: %w", err)
	}

	verdict, err := parseSuppressionVerdict(raw)
	if err != nil {
		slog.Debug("alert suppressor: invalid response", "err", err, "raw", raw)
		return noMatch, nil
	}

	// Hallucination guard: reject any signature name the LLM invented that was
	// not in the set we sent it.
	if verdict.Suppressed {
		found := false
		for _, sig := range signatures {
			if sig.Name == verdict.SignatureName {
				found = true
				break
			}
		}
		if !found {
			slog.Debug("alert suppressor: hallucinated signature name rejected", "name", verdict.SignatureName)
			return noMatch, nil
		}
	}

	return verdict, nil
}

// fetchSignatures queries memories flagged with suppress=true for use as
// false-positive pattern signatures.
func (s *AlertSuppressor) fetchSignatures(ctx context.Context, maxSignatures int) ([]signatureRow, error) {
	var rows []signatureRow
	err := s.db.WithContext(ctx).
		Model(&database.Memory{}).
		Select("name, body").
		Where("suppress = ?", true).
		Limit(maxSignatures).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// buildSuppressionUserPrompt produces the numbered signature list shown to the
// LLM. Each entry includes its name and a capped body excerpt.
func buildSuppressionUserPrompt(alert alerts.NormalizedAlert, signatures []signatureRow) string {
	const snippetCap = 300

	var sb strings.Builder
	sb.WriteString("Incoming alert:\n")
	sb.WriteString(fmt.Sprintf("  Name: %s\n", truncateForPrompt(sanitizeForPrompt(alert.AlertName), snippetCap)))
	if alert.TargetHost != "" {
		sb.WriteString(fmt.Sprintf("  Host: %s\n", truncateForPrompt(sanitizeForPrompt(alert.TargetHost), snippetCap)))
	}
	if alert.Summary != "" {
		sb.WriteString(fmt.Sprintf("  Summary: %s\n", truncateForPrompt(sanitizeForPrompt(alert.Summary), snippetCap)))
	}

	sb.WriteString("\nKnown false-positive signatures:\n")
	for i, sig := range signatures {
		body := truncateForPrompt(sanitizeForPrompt(strings.TrimSpace(sig.Body)), snippetCap)
		sb.WriteString(fmt.Sprintf("\n%d. Name: %s\n", i+1, sanitizeForPrompt(sig.Name)))
		if body != "" {
			sb.WriteString(fmt.Sprintf("   Pattern: %s\n", body))
		}
	}
	return sb.String()
}

// parseSuppressionVerdict cleans LLM output and decodes it into a SuppressionVerdict.
func parseSuppressionVerdict(raw string) (SuppressionVerdict, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return SuppressionVerdict{}, fmt.Errorf("empty response")
	}

	var v SuppressionVerdict
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		return SuppressionVerdict{}, fmt.Errorf("decode: %w", err)
	}

	if v.Confidence < 0 {
		v.Confidence = 0
	}
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	v.SignatureName = strings.TrimSpace(v.SignatureName)
	v.Reasoning = strings.TrimSpace(v.Reasoning)

	return v, nil
}

const suppressionSystemPrompt = `You decide whether an incoming alert matches a known false-positive pattern that should be suppressed without investigation.

Return STRICT JSON:
  {"suppressed": true|false, "signature_name": "<name or empty>", "confidence": <0..1>, "reasoning": "<≤200 char explanation>"}

Rules:
- Set suppressed=true ONLY when the alert clearly matches one of the listed signatures (same rule name AND same host pattern).
- signature_name MUST be one of the names from the signature list. If suppressed=false, set it to "".
- Do NOT suppress if the alert shows new or escalating behaviour beyond the known false-positive pattern.
- When uncertain, prefer suppressed=false (a missed suppression creates a new incident; a false suppression hides a real event).

Confidence:
  0.9-1.0: identical rule name + host pattern, textbook false-positive match
  0.7-0.8: same rule/host with minor variation still clearly a known false positive
  0.5-0.6: possibly related but significant uncertainty
  0.0-0.4: different rule, different host, or not convinced

Output JSON only. No code fences.`
