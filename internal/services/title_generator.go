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
	"github.com/akmatori/akmatori/internal/utils"
)

// titleGenerationTimeout is the upper bound for a single title-generation call
// when the caller does not provide its own deadline.
const titleGenerationTimeout = 30 * time.Second

// TitleGenerator generates concise titles for incidents using a provider-agnostic
// one-shot LLM call routed through the agent worker.
type TitleGenerator struct {
	caller OneShotLLMCaller
}

// NewTitleGenerator returns a TitleGenerator that issues completions through the
// supplied caller. Pass nil to force the deterministic fallback path (used in
// tests and at startup before the worker is wired up).
func NewTitleGenerator(caller OneShotLLMCaller) *TitleGenerator {
	return &TitleGenerator{caller: caller}
}

// GenerateTitle generates a concise title for an incident based on the incoming message/alert.
// Falls back deterministically whenever the LLM path is unavailable or errors out — every
// caller in the codebase relies on this method never failing for transient reasons.
func (t *TitleGenerator) GenerateTitle(messageOrAlert string, source string) (string, error) {
	messageOrAlert = strings.TrimSpace(messageOrAlert)
	if len(messageOrAlert) < 10 {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	if t.caller == nil {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	settings, err := database.GetLLMSettings()
	if err != nil {
		return "", fmt.Errorf("failed to get LLM settings: %w", err)
	}

	if settings.APIKey == "" {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	systemPrompt := `You are a concise title generator. Create a short title (max 80 characters) that accurately summarizes the given message.

IMPORTANT RULES:
- ONLY use information present in the message - do NOT invent or assume details
- If the message is vague or unclear, create a generic title like "User inquiry" or "General request"
- Do NOT make up technical issues, error types, or problems that aren't mentioned
- Keep it factual and based solely on what's written
- Do not start with "Alert:" or "Incident:"
- Use sentence case

Respond with ONLY the title, nothing else.`

	userPrompt := fmt.Sprintf("Source: %s\n\nMessage:\n%s", source, truncateForPrompt(messageOrAlert, 2000))

	ctx, cancel := context.WithTimeout(context.Background(), titleGenerationTimeout)
	defer cancel()

	raw, err := t.caller.OneShotLLM(ctx, worker, systemPrompt, userPrompt, 50, 0.3)
	if err != nil {
		// ErrWorkerNotConnected is the expected miss; everything else gets logged
		// at warn so we still notice transient breakage in dashboards.
		if errors.Is(err, ErrWorkerNotConnected) {
			slog.Debug("oneshot LLM unavailable for title generation, using fallback")
		} else {
			slog.Warn("oneshot LLM call failed for title generation, using fallback", "err", err)
		}
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	title := strings.TrimSpace(raw)
	title = strings.Trim(title, "\"'")
	if title == "" {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	if utf8.RuneCountInString(title) > 255 {
		title = truncateRunesWithEllipsis(title, 255)
	}
	return title, nil
}

// GenerateFallbackTitle creates a simple title when LLM is not available
func (t *TitleGenerator) GenerateFallbackTitle(message string, source string) string {
	// Strip any Slack mrkdwn formatting that may have leaked through
	message = utils.StripSlackMrkdwn(message)

	// Remove common prefixes
	message = strings.TrimPrefix(message, "Alert:")
	message = strings.TrimPrefix(message, "alert:")
	message = strings.TrimPrefix(message, "Incident:")
	message = strings.TrimPrefix(message, "incident:")
	message = strings.TrimSpace(message)

	// Take first line only
	if idx := strings.Index(message, "\n"); idx > 0 {
		message = message[:idx]
	}

	// Truncate to reasonable length. Fallback titles carry the raw alert
	// text, so keep as much context as fits comfortably in the 255-char
	// title column — the incidents list truncates visually and exposes the
	// stored title via hover/detail, so a tight cap here just loses data.
	const fallbackTitleMaxRunes = 200
	if utf8.RuneCountInString(message) > fallbackTitleMaxRunes {
		// Try to cut at word boundary
		prefix := firstRunes(message, fallbackTitleMaxRunes)
		idx := strings.LastIndex(prefix, " ")
		if idx > 0 && utf8.RuneCountInString(prefix[:idx]) > fallbackTitleMaxRunes/2 {
			message = message[:idx] + "..."
		} else {
			message = truncateRunesWithEllipsis(message, fallbackTitleMaxRunes)
		}
	}

	if message == "" {
		return fmt.Sprintf("Incident from %s", source)
	}

	return message
}

// truncateForPrompt truncates a string to fit in the prompt without splitting
// UTF-8 multi-byte sequences, which would panic at slice boundaries.
func truncateForPrompt(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	return truncateRunesWithEllipsis(s, maxLen)
}

func truncateRunesWithEllipsis(s string, maxRunes int) string {
	if maxRunes <= 3 {
		return "..."
	}
	return firstRunes(s, maxRunes-3) + "..."
}

func firstRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	for idx := range s {
		if maxRunes == 0 {
			return s[:idx]
		}
		maxRunes--
	}
	return s
}
