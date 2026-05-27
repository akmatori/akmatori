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

// formatterJSONInstruction is appended to the system prompt to enforce a JSON
// output contract. The formatter validates the returned JSON and retries once
// on failure.
const formatterJSONInstruction = `

Return ONLY a single JSON object — no markdown fences, no preamble, no trailing text — with exactly these four keys:
{
  "status": "<resolved|unresolved|escalate>",
  "summary": "<1-3 sentence description>",
  "actions_taken": ["<action 1>", "..."],
  "recommendations": ["<recommendation 1>", "..."]
}
"status" and "summary" must be non-empty strings. "actions_taken" and "recommendations" may be empty arrays.`

// formatterResult is the JSON envelope the LLM must return when formatting is active.
// ActionsTaken and Recommendations use pointer slices so that absent or JSON-null
// values can be distinguished from present-but-empty arrays during validation.
type formatterResult struct {
	Status          string    `json:"status"`
	Summary         string    `json:"summary"`
	ActionsTaken    *[]string `json:"actions_taken"`
	Recommendations *[]string `json:"recommendations"`
}

// validateFormatterResult extracts the JSON object from the response, unmarshals
// it, and validates required fields. Preamble, trailing text, and markdown fences
// are stripped by scanning for the first '{' and last '}'. Returns the parsed
// struct and any validation errors; a non-nil struct is guaranteed when the error
// slice is empty.
func validateFormatterResult(raw string) (*formatterResult, []string) {
	s := strings.TrimSpace(raw)
	// Extract the JSON object by finding the outermost braces so that fences,
	// preamble, or trailing text the LLM may have added do not cause parse failures.
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			s = s[start : end+1]
		}
	}

	var r formatterResult
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, []string{fmt.Sprintf("invalid JSON: %v", err)}
	}

	validStatuses := map[string]bool{"resolved": true, "unresolved": true, "escalate": true}
	var errs []string
	statusVal := strings.ToLower(strings.TrimSpace(r.Status))
	if statusVal == "" {
		errs = append(errs, `"status" must be a non-empty string`)
	} else if !validStatuses[statusVal] {
		errs = append(errs, `"status" must be one of "resolved", "unresolved", or "escalate"`)
	}
	summaryVal := strings.TrimSpace(r.Summary)
	if summaryVal == "" {
		errs = append(errs, `"summary" must be a non-empty string`)
	}
	if r.ActionsTaken == nil {
		errs = append(errs, `"actions_taken" must be a JSON array (may be empty)`)
	}
	if r.Recommendations == nil {
		errs = append(errs, `"recommendations" must be a JSON array (may be empty)`)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	r.Status = statusVal
	r.Summary = summaryVal
	return &r, nil
}

// renderFormatterResult maps a formatterResult to a Slack-formatted string via
// output.FormatForSlack. Returns empty string on nil input.
func renderFormatterResult(r *formatterResult) string {
	if r == nil {
		return ""
	}
	var actionsTaken, recommendations []string
	if r.ActionsTaken != nil {
		actionsTaken = *r.ActionsTaken
	}
	if r.Recommendations != nil {
		recommendations = *r.Recommendations
	}
	fr := output.FinalResult{
		Status:          r.Status,
		Summary:         r.Summary,
		ActionsTaken:    actionsTaken,
		Recommendations: recommendations,
	}
	po := &output.ParsedOutput{FinalResult: &fr}
	return output.FormatForSlack(po)
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
	systemPrompt += formatterJSONInstruction

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

	result, validationErrs := validateFormatterResult(formatted)
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
		result, secondErrs = validateFormatterResult(formatted2)
		if result == nil {
			slog.Warn("response formatter: retry response failed validation, using raw response", "errors", secondErrs)
			return rawResponse
		}
	}

	rendered := renderFormatterResult(result)
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
