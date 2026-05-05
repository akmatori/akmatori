package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/output"
)

// slackSummarizerTimeout is the upper bound for a single summarization call when
// the caller does not provide its own deadline. Long agent transcripts can
// take a few seconds to compress, but we never want this to outlive the Slack
// message lifecycle.
const slackSummarizerTimeout = 30 * time.Second

// slackSummarizerSystemPrompt instructs the model to emit a condensed Slack
// message that preserves the verdict, key actions, and the single most
// actionable recommendation. The byte budget is included in the user prompt so
// the LLM has a concrete target.
const slackSummarizerSystemPrompt = `You compress AIOps incident reports into a single Slack message.

Rules:
- Preserve the status verdict (resolved/unresolved/escalate) and the one-line summary.
- Keep at most the two most important actions taken and the single most actionable recommendation.
- Use Slack mrkdwn (asterisks for bold, single asterisk only — never double).
- Do NOT invent details that are not in the input.
- End with a short note pointing the reader to the Akmatori incident UI for the full reasoning log.
- Output the message body only — no headers, no code fences, no preamble.`

// SlackSummarizer compresses long agent output into a Slack-sized message
// using a provider-agnostic one-shot LLM call. When the LLM is unavailable, or
// it returns over-budget output, the deterministic fallback in `internal/output`
// is used so callers always get a payload that fits within the budget.
type SlackSummarizer struct {
	caller OneShotLLMCaller
}

// NewSlackSummarizer returns a SlackSummarizer that issues completions through
// the supplied caller. Pass nil to force the deterministic fallback path (used
// in tests and at startup before the worker is wired up).
func NewSlackSummarizer(caller OneShotLLMCaller) *SlackSummarizer {
	return &SlackSummarizer{caller: caller}
}

// SummarizeForSlack returns a string guaranteed to fit within maxBytes bytes.
// content is the raw agent response (potentially containing structured
// [FINAL_RESULT]/[ESCALATE] blocks); the summarizer parses it, formats it for
// Slack, and only invokes the LLM when the formatted body is over-budget. On
// any LLM-side error — ErrWorkerNotConnected, missing API key, missing
// settings, over-budget output, or empty response — the deterministic
// shortener in `internal/output` runs against the *parsed* structure (so the
// fallback collapses to the FINAL_RESULT verdict + first action +
// recommendation, not just byte-truncated formatted text).
//
// The error return is reserved for unexpected failures (currently none — the
// fallback path always produces a payload). It is kept for forward
// compatibility so callers can choose to surface failures in the future.
func (s *SlackSummarizer) SummarizeForSlack(ctx context.Context, content string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		return "", nil
	}

	parsed := output.Parse(content)
	formatted := output.FormatForSlack(parsed)

	if output.WithinSlackBudget(formatted, maxBytes) {
		return formatted, nil
	}

	// Try the LLM path; fall back deterministically on any miss.
	if s.caller != nil {
		if summary, ok := s.summarizeViaLLM(ctx, formatted, maxBytes); ok {
			return summary, nil
		}
	}

	return output.ShortenForSlackBudget(parsed, maxBytes), nil
}

// summarizeViaLLM attempts to compress formattedText using the configured one-shot
// LLM. Returns (summary, true) only when the call produces a non-empty,
// in-budget result. Any other outcome (missing settings, ErrWorkerNotConnected,
// caller error, over-budget output) returns ("", false) so the caller can fall
// back deterministically.
func (s *SlackSummarizer) summarizeViaLLM(ctx context.Context, formattedText string, maxBytes int) (string, bool) {
	settings, err := database.GetLLMSettings()
	if err != nil {
		slog.Warn("slack summarizer: failed to load llm settings, using fallback", "err", err)
		return "", false
	}
	if settings == nil || settings.APIKey == "" {
		return "", false
	}

	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return "", false
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, slackSummarizerTimeout)
		defer cancel()
	}

	userPrompt := fmt.Sprintf(
		"Compress the following incident report into a Slack message that is at most %d bytes long. Keep all critical context.\n\n---\n%s",
		maxBytes,
		formattedText,
	)

	raw, err := s.caller.OneShotLLM(ctx, worker, slackSummarizerSystemPrompt, userPrompt, 600, 0.2)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			slog.Debug("slack summarizer: worker not connected, using fallback")
		} else {
			slog.Warn("slack summarizer: oneshot LLM failed, using fallback", "err", err)
		}
		return "", false
	}

	summary := strings.TrimSpace(raw)
	if summary == "" {
		return "", false
	}

	if !output.WithinSlackBudget(summary, maxBytes) {
		slog.Debug("slack summarizer: LLM returned over-budget output, using fallback",
			"got_bytes", len(summary), "budget", maxBytes)
		return "", false
	}

	return summary, true
}
