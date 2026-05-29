package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/output"
)

// responseFormatterTimeout is the upper bound for the entire Format() call,
// including the optional retry, when the caller does not provide its own deadline.
const responseFormatterTimeout = 30 * time.Second

// responseFormatterMaxInputBytes caps the combined raw response + reasoning
// trace fed to the LLM. The reasoning trace is truncated from the start so the
// portion immediately preceding the final answer stays in the prompt.
const responseFormatterMaxInputBytes = 60_000

// parseAndValidateResponse extracts a JSON object from raw (stripping fences and
// any preamble/trailing text the LLM may have added), unmarshals it to a map, and
// validates it against specs. Returns the parsed map and any validation errors.
func parseAndValidateResponse(raw string, specs []fieldSpec) (map[string]any, []string) {
	s := strings.TrimSpace(raw)
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			s = s[start : end+1]
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil, []string{fmt.Sprintf("invalid JSON: %v", err)}
	}
	return parsed, validateAgainstSpecs(parsed, specs)
}

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

	// The settings UI advertises "leave blank to use the default prompt"; honor
	// that here by falling back to DefaultFormattingPrompt when the operator
	// saved an empty/whitespace value. Disabling the formatter entirely is
	// expressed via Enabled=false, not via a blank prompt.
	systemPrompt := strings.TrimSpace(settings.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = strings.TrimSpace(database.DefaultFormattingPrompt)
		if systemPrompt == "" {
			return rawResponse
		}
	}

	example := strings.TrimSpace(settings.OutputSchemaExample)
	if example == "" {
		example = defaultSchemaExample
	}

	specs, err := inferSchema(example)
	if err != nil {
		slog.Warn("response formatter: failed to infer output schema, using raw response", "err", err)
		return rawResponse
	}

	systemPrompt += buildSchemaInstruction(example)

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

	parsed, validationErrs := parseAndValidateResponse(formatted, specs)
	if len(validationErrs) > 0 {
		// Cap the failed response snippet so the retry prompt stays within a
		// reasonable size. The first call's response should be short JSON, but
		// truncating here prevents the retry prompt from exceeding the input
		// budget when the model emits unexpected verbosity.
		failedSnippet := truncateFromStart(formatted, 2000, "[...truncated...]")
		retryUser := userPrompt + "\n\nYour previous response was:\n" + failedSnippet +
			"\n\nIt had validation errors:\n" +
			strings.Join(validationErrs, "\n") +
			"\nReturn only corrected JSON."
		raw2, err2 := f.caller.OneShotLLM(ctx, worker, systemPrompt, retryUser, maxTokens, settings.Temperature)
		if err2 != nil {
			slog.Warn("response formatter: retry call failed, using raw response", "err", err2)
			return rawResponse
		}
		formatted2 := strings.TrimSpace(raw2)
		if formatted2 == "" {
			slog.Warn("response formatter: retry returned empty response, using raw response")
			return rawResponse
		}
		var secondErrs []string
		parsed, secondErrs = parseAndValidateResponse(formatted2, specs)
		if len(secondErrs) > 0 {
			slog.Warn("response formatter: retry response failed validation, using raw response", "errors", secondErrs)
			return rawResponse
		}
	}

	rendered := output.RenderForSlack(parsed, specs)
	if rendered == "" {
		return rawResponse
	}
	return rendered
}

// buildFormatterUserPrompt assembles the user message with clearly delimited
// raw-response and reasoning sections. When the combined size exceeds
// maxBytes, the reasoning trace is truncated from the start (oldest content
// dropped first) so the portion adjacent to the final response is preserved.
// If the raw response alone exceeds the budget, it is truncated from the
// start as well so the assembled prompt stays within maxBytes.
func buildFormatterUserPrompt(rawResponse, fullLog string, maxBytes int) string {
	const (
		header                 = "Reformat the agent's incident report using the configured output structure. The reasoning trace is provided as supporting context only — do not include it verbatim in the output.\n\n"
		responseLabel          = "--- Raw response ---\n"
		reasoningLabel         = "\n\n--- Full reasoning ---\n"
		truncationNote         = "[... earlier reasoning truncated ...]\n"
		responseTruncationNote = "[... earlier response truncated ...]\n"
	)

	if maxBytes <= 0 {
		return header + responseLabel + rawResponse
	}

	overhead := len(header) + len(responseLabel) + len(reasoningLabel)
	hasReasoning := strings.TrimSpace(fullLog) != ""
	if !hasReasoning {
		overhead = len(header) + len(responseLabel)
	}

	// First, ensure the raw response fits within its share of the budget. If
	// the response alone overflows maxBytes, truncate it from the start so
	// the trailing summary (which is what the agent emits last and the
	// formatter most needs) is preserved. After truncation, the reasoning
	// section may still receive some budget if room remains.
	responseBudget := maxBytes - overhead
	if responseBudget < 0 {
		responseBudget = 0
	}
	rawResponse = truncateFromStart(rawResponse, responseBudget, responseTruncationNote)

	budgetForLog := maxBytes - overhead - len(rawResponse)

	if !hasReasoning {
		return header + responseLabel + rawResponse
	}

	if budgetForLog <= 0 {
		return header + responseLabel + rawResponse
	}

	if len(fullLog) <= budgetForLog {
		return header + responseLabel + rawResponse + reasoningLabel + fullLog
	}

	// If the remaining budget cannot even fit the truncation note alone,
	// drop the reasoning section entirely rather than overflow maxBytes.
	// Without this guard the cutoff >= len(fullLog) branch below would
	// emit reasoningLabel+truncationNote whose combined length exceeds
	// budgetForLog, blowing the input cap that callers depend on.
	if budgetForLog < len(truncationNote) {
		return header + responseLabel + rawResponse
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

// truncateFromStart returns s unchanged when len(s) <= budget; otherwise it
// drops the leading portion (rune-aligned) and prepends note so the tail —
// the final summary the formatter needs most — is preserved within budget.
func truncateFromStart(s string, budget int, note string) string {
	if budget <= 0 || len(s) <= budget {
		if budget <= 0 {
			return ""
		}
		return s
	}
	if len(note) >= budget {
		return s[len(s)-budget:]
	}
	cutoff := len(s) - (budget - len(note))
	if cutoff < 0 {
		cutoff = 0
	}
	for cutoff < len(s) && !utf8.RuneStart(s[cutoff]) {
		cutoff++
	}
	if cutoff >= len(s) {
		return note
	}
	return note + s[cutoff:]
}
