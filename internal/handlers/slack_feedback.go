package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
)

// feedbackReaction is the emoji name we attach to messages that the
// classifier accepted as feedback. "+1" renders as 👍 in Slack.
const feedbackReaction = "+1"

// feedbackAcker abstracts the Slack-side acknowledgment calls used after a
// confident feedback verdict. It mirrors the runMentionContinuation seam: a
// default adapter is wired only when a real *slack.Client is present, and
// tests substitute a fake so ack behaviour can be asserted without a live
// client.
type feedbackAcker interface {
	AddReaction(name string, item slack.ItemRef) error
	PostThreadText(channel, threadTS, text string) error
}

// slackFeedbackAcker is the production feedbackAcker backed by a real
// *slack.Client.
type slackFeedbackAcker struct {
	client *slack.Client
}

func (a slackFeedbackAcker) AddReaction(name string, item slack.ItemRef) error {
	return a.client.AddReaction(name, item)
}

func (a slackFeedbackAcker) PostThreadText(channel, threadTS, text string) error {
	_, _, err := a.client.PostMessage(channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))
	return err
}

// maybeCaptureSlackFeedback runs the LLM-backed classifier against a single
// non-mention thread reply on an incident thread. When the classifier is
// confident the reply is operator feedback, it persists a Memory and
// acknowledges with the 👍 reaction only — Akmatori must not post text into a
// thread unless explicitly @mentioned. Stays silent on negatives so we don't
// spam non-feedback chatter.
//
// All branches are intentionally fire-and-forget — Slack's Socket Mode
// handler thread cannot afford to block on an LLM round-trip.
func (h *SlackHandler) maybeCaptureSlackFeedback(channel, threadTS, messageTS, text, user string) {
	if user == h.botUserID {
		return
	}
	verdict, incident, err := h.classifyThreadReplyForFeedback(threadTS, text)
	if err != nil {
		return
	}
	if !verdict.IsConfidentFeedback() {
		// Silent on negatives by design: classifying every thread reply is fine,
		// noisy logging is not.
		return
	}
	// Non-mention path: persist + emoji reaction only. Akmatori must not post
	// text into a thread unless it was explicitly @mentioned, so there is no
	// text ack here.
	if mem := h.persistFeedback(threadTS, text, verdict, incident); mem != nil {
		h.reactFeedback(channel, messageTS)
	}
}

// classifyThreadReplyForFeedback resolves a thread reply to an incident and
// runs the LLM-backed feedback classifier against the mention-stripped text.
// Returns a non-nil error for every fall-through case (nil deps, empty text,
// no incident match, classifier failure) so callers can `if err != nil` and
// route to the agent path. ErrWorkerNotConnected is surfaced verbatim.
func (h *SlackHandler) classifyThreadReplyForFeedback(threadTS, text string) (services.FeedbackVerdict, *database.Incident, error) {
	if h.feedbackClassifier == nil || h.memoryManager == nil {
		return services.FeedbackVerdict{}, nil, fmt.Errorf("feedback classifier or memory manager unavailable")
	}
	clean := strings.TrimSpace(text)
	if clean == "" {
		return services.FeedbackVerdict{}, nil, fmt.Errorf("empty text")
	}
	if h.botUserID != "" {
		clean = strings.TrimSpace(strings.Replace(clean, fmt.Sprintf("<@%s>", h.botUserID), "", 1))
	}
	if clean == "" {
		return services.FeedbackVerdict{}, nil, fmt.Errorf("text empty after mention strip")
	}

	// Resolve the thread to an incident. Two routes — DM-originated incidents
	// keyed by source_id, and alert-channel incidents keyed by slack_message_ts.
	// Mirrors slack_processor.go::94-115.
	incident, err := lookupIncidentByThread(threadTS)
	if err != nil {
		// No incident → not a thread the classifier should fire on.
		return services.FeedbackVerdict{}, nil, err
	}

	verdict, err := h.feedbackClassifier.Classify(context.Background(), clean, incident)
	if err != nil {
		if errors.Is(err, services.ErrWorkerNotConnected) {
			slog.Debug("feedback classifier skipped: worker offline", "thread", threadTS)
		} else {
			slog.Warn("feedback classifier failed", "thread", threadTS, "err", err)
		}
		return services.FeedbackVerdict{}, incident, err
	}
	return verdict, incident, nil
}

// persistFeedback writes the feedback memory and returns the saved record
// (nil on failure). The `originalText` argument carries the message body
// verbatim (un-mention-stripped) so the persisted memory reflects what the
// operator typed. Persisting never depends on Slack ack succeeding.
func (h *SlackHandler) persistFeedback(threadTS, originalText string, verdict services.FeedbackVerdict, incident *database.Incident) *database.Memory {
	mem := buildFeedbackMemory(originalText, verdict, incident.UUID)
	if _, err := h.memoryManager.UpsertByName(mem); err != nil {
		slog.Warn("feedback persist failed", "thread", threadTS, "incident", incident.UUID, "err", err)
		return nil
	}
	slog.Info("captured slack feedback as memory", "incident", incident.UUID, "name", mem.Name, "confidence", verdict.Confidence)
	return mem
}

// reactFeedback attaches the 👍 reaction to the operator's message. Best-effort
// and routed through the feedbackAcker seam: a nil acker (no live client) skips
// silently, and a failed reaction must never roll back the persisted memory.
func (h *SlackHandler) reactFeedback(channel, messageTS string) {
	if h.feedbackAcker == nil {
		return
	}
	if err := h.feedbackAcker.AddReaction(feedbackReaction, slack.ItemRef{Channel: channel, Timestamp: messageTS}); err != nil {
		slog.Debug("feedback reaction failed", "err", err)
	}
}

// postFeedbackTextAck posts the short threaded confirmation. Only the @mention
// path uses this — Akmatori must not post text into a thread unless explicitly
// mentioned. Best-effort and routed through the feedbackAcker seam; a nil acker
// skips silently.
func (h *SlackHandler) postFeedbackTextAck(channel, threadTS, memName string) {
	if h.feedbackAcker == nil {
		return
	}
	ack := fmt.Sprintf("Thanks — saved to memory as `%s`. Future incidents will recall it.", memName)
	if err := h.feedbackAcker.PostThreadText(channel, threadTS, ack); err != nil {
		slog.Debug("feedback ack post failed", "err", err)
	}
}

// lookupIncidentByThread mirrors the resolution logic in slack_processor.go:
// first try source=slack/source_id (DM-originated), then slack_message_ts
// (alert-channel incidents). Returns the incident or an error so callers can
// distinguish "no thread match" from a database error.
func lookupIncidentByThread(threadTS string) (*database.Incident, error) {
	if threadTS == "" {
		return nil, fmt.Errorf("empty thread")
	}
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("db unavailable")
	}
	var incident database.Incident
	if err := db.Where("source = ? AND source_id = ?", "slack", threadTS).First(&incident).Error; err == nil {
		return &incident, nil
	}
	if err := db.Where("slack_message_ts = ?", threadTS).First(&incident).Error; err == nil {
		return &incident, nil
	}
	return nil, fmt.Errorf("no incident for thread %s", threadTS)
}

// buildFeedbackMemory derives the Memory record we write for a confident
// feedback verdict. Description is the LLM's summary; Body is the original
// message verbatim (truncated to the body cap). Name embeds an incident
// UUID prefix so similar feedback across incidents stays distinct.
func buildFeedbackMemory(text string, verdict services.FeedbackVerdict, incidentUUID string) *database.Memory {
	summary := strings.TrimSpace(verdict.Summary)
	if summary == "" {
		summary = strings.TrimSpace(text)
	}

	description := truncateBytesUTF8Safe(summary, services.MemoryDescriptionMaxLen)
	// Postgres rejects invalid UTF-8 — share the services helper instead of
	// raw byte slicing, which would split a multi-byte rune at the cap.
	body := services.TruncateMemoryBody(text)

	name := services.SlugifyMemoryName(summary)
	if prefix := slugFromUUID(incidentUUID); prefix != "" {
		name = name + "-" + prefix
		if len(name) > services.MemoryNameMaxLen {
			name = name[:services.MemoryNameMaxLen]
		}
	}

	return &database.Memory{
		Scope:        services.MemoryScopeGlobal,
		Type:         services.MemoryTypeFeedback,
		Name:         name,
		Description:  description,
		Body:         body,
		IncidentUUID: incidentUUID,
		CreatedBy:    services.MemoryCreatedByOperator,
	}
}

// slugFromUUID returns up to 8 slug-safe characters from a UUID. Keeps the
// derived memory name deterministic across re-classifications of the same
// thread (UpsertByName then collapses repeats into a single row).
func slugFromUUID(uuid string) string {
	out := strings.ToLower(strings.TrimSpace(uuid))
	keep := make([]byte, 0, 8)
	for i := 0; i < len(out) && len(keep) < 8; i++ {
		c := out[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			keep = append(keep, c)
		}
	}
	return string(keep)
}

// truncateBytesUTF8Safe trims to maxBytes without slicing mid-character.
// Reserves 3 bytes for the trailing "…" so the result still fits the cap.
func truncateBytesUTF8Safe(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	if budget < 0 {
		return s[:maxBytes]
	}
	cut := budget
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimRight(s[:cut], " ") + ellipsis
}
