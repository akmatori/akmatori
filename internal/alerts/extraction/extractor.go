package extraction

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
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/utils"
)

// extractionTimeout is the upper bound for a single extraction call when the
// caller does not provide its own deadline.
const extractionTimeout = 30 * time.Second

// LLMSettingsGetter abstracts LLM settings retrieval for testing
type LLMSettingsGetter func() (*database.LLMSettings, error)

// OneShotLLMCaller is the structural shape of the agent worker one-shot path.
// It mirrors services.OneShotLLMCaller; we keep a local alias here so the
// extraction package doesn't have to drag every consumer through services.
type OneShotLLMCaller = services.OneShotLLMCaller

// AlertExtractor extracts alert information from free-form text using a
// provider-agnostic one-shot LLM call routed through the agent worker.
type AlertExtractor struct {
	caller         OneShotLLMCaller
	getLLMSettings LLMSettingsGetter
}

// ExtractedAlert represents the structured data extracted from a message
type ExtractedAlert struct {
	AlertName     string `json:"alert_name"`
	Severity      string `json:"severity"`
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	Description   string `json:"description"`
	TargetHost    string `json:"target_host"`
	TargetService string `json:"target_service"`
	SourceSystem  string `json:"source_system"`
}

// NewAlertExtractor creates a new alert extractor that routes LLM calls through
// the supplied caller. Pass nil to force the deterministic fallback path (used
// in tests and at startup before the worker is wired up).
func NewAlertExtractor(caller OneShotLLMCaller) *AlertExtractor {
	return &AlertExtractor{
		caller:         caller,
		getLLMSettings: database.GetLLMSettings,
	}
}

// NewAlertExtractorWithDeps creates an extractor with injected dependencies (for testing)
func NewAlertExtractorWithDeps(caller OneShotLLMCaller, getLLMSettings LLMSettingsGetter) *AlertExtractor {
	return &AlertExtractor{
		caller:         caller,
		getLLMSettings: getLLMSettings,
	}
}

const defaultExtractionPrompt = `Extract alert information from this Slack message. Return ONLY valid JSON with these fields:
- alert_name: Brief name/title of the alert (required)
- severity: One of "critical", "high", "warning", "info" (infer from context, default to "warning")
- status: "firing" or "resolved" (default to "firing")
- summary: One-line summary of the issue
- description: Full description if available
- target_host: Affected host/server/IP if mentioned
- target_service: Affected service/application if mentioned
- source_system: Originating monitoring system (e.g., "Prometheus", "Datadog", "Zabbix") if identifiable

Use null for fields that cannot be determined from the message.

Message:
%s`

// Extract extracts alert information from a message using AI
func (e *AlertExtractor) Extract(ctx context.Context, messageText string) (*alerts.NormalizedAlert, error) {
	return e.ExtractWithPrompt(ctx, messageText, "")
}

// ExtractWithPrompt extracts alert information using a custom prompt
func (e *AlertExtractor) ExtractWithPrompt(ctx context.Context, messageText, customPrompt string) (*alerts.NormalizedAlert, error) {
	if e.caller == nil {
		slog.Debug("alert extractor has no LLM caller, using fallback")
		return e.createFallbackAlert(messageText), nil
	}

	settings, err := e.getLLMSettings()
	if err != nil {
		slog.Error("Failed to get LLM settings", "error", err)
		return e.createFallbackAlert(messageText), nil
	}

	if settings == nil || settings.APIKey == "" {
		slog.Info("LLM not configured, using fallback extraction")
		return e.createFallbackAlert(messageText), nil
	}

	worker := services.BuildLLMSettingsForWorker(settings)
	if worker == nil {
		slog.Info("LLM settings inactive, using fallback extraction")
		return e.createFallbackAlert(messageText), nil
	}

	// Use custom prompt or default
	prompt := customPrompt
	if prompt == "" {
		prompt = defaultExtractionPrompt
	}

	userPrompt := fmt.Sprintf(prompt, truncateMessage(messageText, 3000))

	callCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, extractionTimeout)
		defer cancel()
	}

	raw, err := e.caller.OneShotLLM(callCtx, worker, "", userPrompt, 500, 0.1)
	if err != nil {
		if errors.Is(err, services.ErrWorkerNotConnected) {
			slog.Debug("oneshot LLM unavailable for alert extraction, using fallback")
		} else {
			slog.Warn("oneshot LLM call failed for alert extraction, using fallback", "err", err)
		}
		return e.createFallbackAlert(messageText), nil
	}

	content := strings.TrimSpace(raw)
	if content == "" {
		slog.Warn("empty oneshot LLM response for alert extraction, using fallback")
		return e.createFallbackAlert(messageText), nil
	}

	// Remove markdown code block if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var extracted ExtractedAlert
	if err := json.Unmarshal([]byte(content), &extracted); err != nil {
		slog.Error("Failed to parse extracted alert JSON", "error", err, "content", content)
		return e.createFallbackAlert(messageText), nil
	}

	return e.toNormalizedAlert(extracted, messageText), nil
}

// toNormalizedAlert converts ExtractedAlert to NormalizedAlert
func (e *AlertExtractor) toNormalizedAlert(extracted ExtractedAlert, originalMessage string) *alerts.NormalizedAlert {
	alertName := extracted.AlertName
	if alertName == "" {
		alertName = "Slack Alert"
	}

	severity := alerts.NormalizeSeverity(extracted.Severity, alerts.DefaultSeverityMapping)

	status := database.AlertStatusFiring
	if strings.ToLower(extracted.Status) == "resolved" {
		status = database.AlertStatusResolved
	}

	summary := extracted.Summary
	if summary == "" {
		summary = truncateMessage(originalMessage, 100)
	}

	description := extracted.Description
	if description == "" {
		description = originalMessage
	}

	return &alerts.NormalizedAlert{
		AlertName:     alertName,
		Severity:      severity,
		Status:        status,
		Summary:       summary,
		Description:   description,
		TargetHost:    extracted.TargetHost,
		TargetService: extracted.TargetService,
		TargetLabels: map[string]string{
			"source_system": extracted.SourceSystem,
		},
		RawPayload: map[string]interface{}{
			"original_message": originalMessage,
			"extracted":        extracted,
		},
	}
}

// createFallbackAlert creates a basic alert when AI extraction fails
func (e *AlertExtractor) createFallbackAlert(messageText string) *alerts.NormalizedAlert {
	alertName := "Slack Alert"
	lines := strings.Split(messageText, "\n")
	if len(lines) > 0 {
		firstLine := strings.TrimSpace(lines[0])
		firstLine = utils.StripSlackMrkdwn(firstLine)

		if len(firstLine) > 0 && len(firstLine) <= 100 {
			alertName = firstLine
		} else if len(firstLine) > 100 {
			alertName = firstLine[:97] + "..."
		}
	}

	return &alerts.NormalizedAlert{
		AlertName:   alertName,
		Summary:     truncateMessage(messageText, 100),
		Description: messageText,
		Severity:    database.AlertSeverityWarning,
		Status:      database.AlertStatusFiring,
		RawPayload: map[string]interface{}{
			"original_message": messageText,
			"extraction_mode":  "fallback",
		},
	}
}

// truncateMessage truncates a message to a specified length
func truncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	truncated := msg[:maxLen-3]
	if idx := strings.LastIndex(truncated, " "); idx > maxLen/2 {
		return truncated[:idx] + "..."
	}
	return truncated + "..."
}
