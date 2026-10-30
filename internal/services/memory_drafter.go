package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// memoryDraftTimeout caps the one-shot call. The operator waits for it in
// the UI, so it is shorter than the correlation deadline; on timeout the
// deterministic draft is used.
const memoryDraftTimeout = 30 * time.Second

// memoryDraftMaxTokens is the answer budget: a slug, a one-line description
// and a short body.
const memoryDraftMaxTokens = 1200

// MemoryTextMaxBytes caps the operator text accepted by the drafter. The
// draft body is capped at MemoryBodyMaxBytes anyway, and a bounded input
// keeps the one-shot prompt small.
const MemoryTextMaxBytes = 4 * 1024

// MemoryDraft is a memory proposed from operator free text.
type MemoryDraft struct {
	Name        string
	Description string
	Body        string
	Type        string
	// FromLLM is false when the deterministic fallback produced the draft.
	FromLLM bool
}

// MemoryDrafter turns operator free text ("redis prod listens on 16379")
// into a memory draft with a one-shot LLM call. Provider-agnostic via
// OneShotLLMCaller; any failure falls back to a deterministic draft so the
// operator's text is never lost.
type MemoryDrafter struct {
	caller OneShotLLMCaller
}

// NewMemoryDrafter returns a drafter backed by caller. A nil caller always
// uses the deterministic draft.
func NewMemoryDrafter(caller OneShotLLMCaller) *MemoryDrafter {
	return &MemoryDrafter{caller: caller}
}

// Draft returns a draft for text. It never fails: when the LLM is not
// available or answers badly, the draft keeps the text as the body, its
// first line as the description, and type feedback.
func (d *MemoryDrafter) Draft(ctx context.Context, text string) MemoryDraft {
	text = strings.TrimSpace(text)
	if d != nil && d.caller != nil {
		draft, err := d.draftWithLLM(ctx, text)
		if err == nil {
			return draft
		}
		slog.Info("memory drafter: using deterministic draft", "err", err)
	}
	return fallbackMemoryDraft(text)
}

func (d *MemoryDrafter) draftWithLLM(ctx context.Context, text string) (MemoryDraft, error) {
	settings, err := database.GetLLMSettings()
	if err != nil {
		return MemoryDraft{}, fmt.Errorf("load llm settings: %w", err)
	}
	if settings == nil || settings.APIKey == "" {
		return MemoryDraft{}, ErrWorkerNotConnected
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return MemoryDraft{}, ErrWorkerNotConnected
	}

	callCtx, cancel := context.WithTimeout(ctx, memoryDraftTimeout)
	defer cancel()
	raw, err := d.caller.OneShotLLM(callCtx, worker, memoryDrafterSystemPrompt, text, memoryDraftMaxTokens, 0.0)
	if err != nil {
		return MemoryDraft{}, fmt.Errorf("llm call: %w", err)
	}
	return parseMemoryDraft(raw)
}

const memoryDrafterSystemPrompt = `An SRE operator typed a note for an incident-response AI agent to remember across incidents. Turn it into ONE memory entry.

Fields:
  - name: slug that identifies the fact, lowercase a-z, 0-9 and single hyphens, at most 80 characters (e.g. "redis-prod-port-16379")
  - description: one line, at most 200 characters, that says the fact itself, not "a note about X"
  - body: the operator's note, cleaned up as short Markdown. Keep every host, path, number, command and name exactly as written. Do not add facts. Keep any [[...]] links unchanged
  - type: one of
      fact     — knowledge about a host, service, tool, environment or recurring incident
      feedback — an instruction about how the agent must work

Return STRICT JSON:
  {"name": "...", "description": "...", "body": "...", "type": "..."}

Output JSON only. No code fences.`

func parseMemoryDraft(raw string) (MemoryDraft, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	var out struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return MemoryDraft{}, fmt.Errorf("invalid json: %w", err)
	}
	draft := MemoryDraft{
		Name:        SlugifyMemoryName(out.Name),
		Description: draftDescription(out.Description),
		Body:        TruncateMemoryBody(strings.TrimSpace(out.Body)),
		Type:        NormalizeMemoryType(strings.TrimSpace(out.Type)),
		FromLLM:     true,
	}
	if draft.Description == "" {
		return MemoryDraft{}, fmt.Errorf("empty description")
	}
	if !ValidMemoryType(draft.Type) {
		draft.Type = MemoryTypeFact
	}
	return draft, nil
}

func fallbackMemoryDraft(text string) MemoryDraft {
	firstLine, _, _ := strings.Cut(text, "\n")
	description := draftDescription(firstLine)
	return MemoryDraft{
		Name:        SlugifyMemoryName(description),
		Description: description,
		Body:        TruncateMemoryBody(text),
		Type:        MemoryTypeFeedback,
	}
}

// draftDescription flattens s to one line and cuts it to fit
// MemoryDescriptionMaxLen bytes without splitting a UTF-8 rune.
func draftDescription(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= MemoryDescriptionMaxLen {
		return s
	}
	const ellipsis = "…"
	cut := MemoryDescriptionMaxLen - len(ellipsis)
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + ellipsis
}
