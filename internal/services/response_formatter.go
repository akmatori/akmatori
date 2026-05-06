package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/database"
)

// responseFormatterTimeout is the upper bound for a single format call when
// the caller does not provide its own deadline.
const responseFormatterTimeout = 30 * time.Second

// responseFormatterMaxInputBytes caps the combined raw response + reasoning
// trace fed to the LLM. The reasoning trace is truncated from the start so the
// portion immediately preceding the final answer stays in the prompt.
const responseFormatterMaxInputBytes = 60_000

// ResponseFormatter applies a configurable, global system prompt to the agent's
// final incident response. It runs an extra one-shot LLM call with the raw
// response plus the full reasoning log and returns the reformatted text. When
// formatting is disabled, the LLM is unavailable, or the call errors, the raw
// response is returned unchanged so callers can rely on Format() never failing.
type ResponseFormatter struct {
	caller OneShotLLMCaller
}

// NewResponseFormatter returns a ResponseFormatter that issues completions
// through the supplied caller. Pass nil to force the passthrough path (used in
// tests and at startup before the worker is wired up).
func NewResponseFormatter(caller OneShotLLMCaller) *ResponseFormatter {
	return &ResponseFormatter{caller: caller}
}

// Format returns rawResponse rewritten according to the configured formatting
// prompt, or rawResponse unchanged when formatting is disabled, the LLM path
// is unavailable, or the call fails. Format never returns an error — every
// failure mode collapses to passthrough so incident finalization is never
// blocked by formatter problems.
func (f *ResponseFormatter) Format(ctx context.Context, rawResponse, fullLog string) string {
	if f == nil || f.caller == nil {
		return rawResponse
	}

	settings, err := database.GetOrCreateFormattingSettings()
	if err != nil {
		slog.Warn("response formatter: failed to load formatting settings, using raw response", "err", err)
		return rawResponse
	}
	if settings == nil || !settings.Enabled {
		return rawResponse
	}

	systemPrompt := strings.TrimSpace(settings.SystemPrompt)
	if systemPrompt == "" {
		return rawResponse
	}

	llmSettings, err := database.GetLLMSettings()
	if err != nil {
		slog.Warn("response formatter: failed to load llm settings, using raw response", "err", err)
		return rawResponse
	}
	if llmSettings == nil || llmSettings.APIKey == "" {
		return rawResponse
	}

	worker := BuildLLMSettingsForWorker(llmSettings)
	if worker == nil {
		return rawResponse
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, responseFormatterTimeout)
		defer cancel()
	}

	userPrompt := buildFormatterUserPrompt(rawResponse, fullLog, responseFormatterMaxInputBytes)

	maxTokens := settings.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1500
	}

	raw, err := f.caller.OneShotLLM(ctx, worker, systemPrompt, userPrompt, maxTokens, settings.Temperature)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			slog.Debug("response formatter: worker not connected, using raw response")
		} else {
			slog.Warn("response formatter: oneshot LLM failed, using raw response", "err", err)
		}
		return rawResponse
	}

	formatted := strings.TrimSpace(raw)
	if formatted == "" {
		return rawResponse
	}
	return formatted
}

// buildFormatterUserPrompt assembles the user message with clearly delimited
// raw-response and reasoning sections. When the combined size exceeds
// maxBytes, the reasoning trace is truncated from the start (oldest content
// dropped first) so the portion adjacent to the final response is preserved.
func buildFormatterUserPrompt(rawResponse, fullLog string, maxBytes int) string {
	const (
		header         = "Reformat the agent's incident report using the configured output structure. The reasoning trace is provided as supporting context only — do not include it verbatim in the output.\n\n"
		responseLabel  = "--- Raw response ---\n"
		reasoningLabel = "\n\n--- Full reasoning ---\n"
		truncationNote = "[... earlier reasoning truncated ...]\n"
	)

	if maxBytes <= 0 {
		return header + responseLabel + rawResponse
	}

	overhead := len(header) + len(responseLabel) + len(reasoningLabel)
	budgetForLog := maxBytes - overhead - len(rawResponse)

	if strings.TrimSpace(fullLog) == "" {
		return header + responseLabel + rawResponse
	}

	if budgetForLog <= 0 {
		return header + responseLabel + rawResponse
	}

	if len(fullLog) <= budgetForLog {
		return header + responseLabel + rawResponse + reasoningLabel + fullLog
	}

	cutoff := len(fullLog) - budgetForLog + len(truncationNote)
	if cutoff < 0 || cutoff >= len(fullLog) {
		return header + responseLabel + rawResponse + reasoningLabel + truncationNote
	}
	// Advance to the next UTF-8 rune boundary so we never slice mid-rune
	// and feed invalid UTF-8 to the LLM.
	for cutoff < len(fullLog) && !utf8.RuneStart(fullLog[cutoff]) {
		cutoff++
	}
	if cutoff >= len(fullLog) {
		return header + responseLabel + rawResponse + reasoningLabel + truncationNote
	}
	tail := fullLog[cutoff:]
	return fmt.Sprintf("%s%s%s%s%s%s", header, responseLabel, rawResponse, reasoningLabel, truncationNote, tail)
}
