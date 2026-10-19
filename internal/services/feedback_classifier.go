package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// feedbackClassifyTimeout caps a single classification call. The classifier
// is invoked synchronously from the Slack message handler, so we keep it
// short — false negatives are fine, blocking Slack is not.
const feedbackClassifyTimeout = 15 * time.Second

// FeedbackConfidenceThreshold is the minimum confidence required to persist
// a thread reply as feedback. Hardcoded — tune by editing this constant.
// Pulled deliberately low (0.6) so we err toward capturing feedback;
// false positives are easy to delete later, false negatives are silent.
const FeedbackConfidenceThreshold = 0.6

// FeedbackVerdict is the structured response from the classifier LLM.
type FeedbackVerdict struct {
	IsFeedback bool    `json:"is_feedback"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

// FeedbackClassifier decides whether a Slack thread reply is operator
// feedback worth saving as cross-incident memory. Wraps a one-shot LLM
// call with a strict JSON schema; provider-agnostic via OneShotLLMCaller.
type FeedbackClassifier struct {
	caller OneShotLLMCaller
}

// NewFeedbackClassifier returns a classifier backed by the supplied caller.
// Pass nil to make Classify a silent no-op (useful in startup window).
func NewFeedbackClassifier(caller OneShotLLMCaller) *FeedbackClassifier {
	return &FeedbackClassifier{caller: caller}
}

// Classify returns a verdict for a single thread reply. Returns
// ErrWorkerNotConnected when the worker is offline so callers can drop the
// message silently. All other errors are wrapped — callers should
// log-and-continue rather than retry.
func (c *FeedbackClassifier) Classify(ctx context.Context, message string, incident *database.Incident) (FeedbackVerdict, error) {
	if c == nil || c.caller == nil {
		return FeedbackVerdict{}, ErrWorkerNotConnected
	}
	if incident == nil {
		return FeedbackVerdict{}, fmt.Errorf("classify: incident is nil")
	}
	if strings.TrimSpace(message) == "" {
		return FeedbackVerdict{}, nil
	}

	settings, err := database.GetLLMSettings()
	if err != nil {
		return FeedbackVerdict{}, fmt.Errorf("classify: load llm settings: %w", err)
	}
	if settings == nil || !settings.IsConfigured() {
		return FeedbackVerdict{}, ErrWorkerNotConnected
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return FeedbackVerdict{}, ErrWorkerNotConnected
	}

	systemPrompt := feedbackClassifierSystemPrompt
	userPrompt := buildFeedbackUserPrompt(message, incident)

	callCtx, cancel := context.WithTimeout(ctx, feedbackClassifyTimeout)
	defer cancel()
	raw, err := c.caller.OneShotLLM(callCtx, worker, systemPrompt, userPrompt, 200, 0.0)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			return FeedbackVerdict{}, err
		}
		return FeedbackVerdict{}, fmt.Errorf("classify: llm call: %w", err)
	}

	verdict, err := parseFeedbackVerdict(raw)
	if err != nil {
		// Invalid JSON is a no-op — log at debug and report not-feedback so
		// the caller doesn't bombard the user with parse errors.
		slog.Debug("feedback classifier: invalid response", "err", err, "raw", raw)
		return FeedbackVerdict{}, nil
	}
	return verdict, nil
}

// IsConfidentFeedback returns true when the verdict indicates feedback above
// the configured threshold. Centralized so future threshold tweaks don't
// scatter through callsites.
func (v FeedbackVerdict) IsConfidentFeedback() bool {
	return v.IsFeedback && v.Confidence >= FeedbackConfidenceThreshold
}

const feedbackClassifierSystemPrompt = `You decide whether a Slack thread reply on an incident thread is OPERATOR FEEDBACK that should be saved for future incidents.

Feedback IS:
  - Corrections about facts the bot got wrong (e.g., "the data dir is /mnt/data, not /var/lib/postgresql")
  - New information about hosts, tools, or recurring patterns ("redis prod uses port 16379")
  - Operator policy or runbook adjustments ("for postgres, always check disk space first")

Feedback is NOT:
  - Requests or commands directed at the bot to DO something now ("check freemembers",
    "look into the redis latency", "investigate the disk usage", "run the query again",
    "can you re-check X?"). These are tasks to continue the investigation, not durable
    facts — classify them as NOT feedback even when they name a metric, host, or tool.
  - Casual chat between humans in the thread
  - Status questions ("any update?", "did the alert clear?")
  - The bot's own messages (you should never see those)
  - Acknowledgements ("thanks", "ok", emoji-only replies)

Return STRICT JSON:
  {"is_feedback": bool, "summary": "<≤140 char summary>", "confidence": <0..1>}

Confidence:
  - 0.9-1.0: explicit correction or new fact, unambiguous
  - 0.6-0.8: probably a useful fact but partial/ambiguous
  - 0.0-0.5: chat / status / not actionable

Output JSON only. No code fences.`

func buildFeedbackUserPrompt(message string, incident *database.Incident) string {
	const messageCap = 2000
	const responseCap = 1500
	msg := truncateForPrompt(strings.TrimSpace(message), messageCap)
	resp := truncateForPrompt(strings.TrimSpace(incident.Response), responseCap)
	title := strings.TrimSpace(incident.Title)
	if title == "" {
		title = "(no title)"
	}
	if resp == "" {
		resp = "(no agent response yet)"
	}
	return fmt.Sprintf("Incident title: %s\n\nIncident agent response:\n%s\n\nThread reply to classify:\n%s",
		title, resp, msg)
}

func parseFeedbackVerdict(raw string) (FeedbackVerdict, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return FeedbackVerdict{}, fmt.Errorf("empty")
	}
	var v FeedbackVerdict
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		return FeedbackVerdict{}, fmt.Errorf("decode: %w", err)
	}
	// Clamp confidence so a malformed value doesn't bypass the threshold check.
	if v.Confidence < 0 {
		v.Confidence = 0
	}
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	v.Summary = strings.TrimSpace(v.Summary)
	// Rune-aware truncation: byte-slicing here would split multi-byte UTF-8
	// characters, and buildFeedbackMemory uses this summary as the memory
	// description without further truncation when it's under the 500-byte cap.
	// Postgres rejects invalid UTF-8 with "invalid byte sequence", which
	// would silently lose Slack feedback.
	const summaryMaxRunes = 140
	if r := []rune(v.Summary); len(r) > summaryMaxRunes {
		v.Summary = string(r[:summaryMaxRunes-3]) + "..."
	}
	return v, nil
}
