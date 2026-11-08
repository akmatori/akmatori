package handlers

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// rememberPrefixes start a remember command in a thread reply. The command
// is the dependable way to store operator knowledge from an incident
// thread: it needs no LLM and no classifier verdict. "remember: X",
// "/remember X" and "!remember X" all work, with or without the bot
// mention in front.
var rememberPrefixes = []string{"/remember", "!remember", "remember:"}

// rememberCommand returns the text of a remember command, or ok = false
// when text is not one. The bot mention is stripped first.
func rememberCommand(text, botUserID string) (string, bool) {
	clean := strings.TrimSpace(text)
	if botUserID != "" {
		clean = strings.TrimSpace(strings.Replace(clean, fmt.Sprintf("<@%s>", botUserID), "", 1))
	}
	lower := strings.ToLower(clean)
	for _, p := range rememberPrefixes {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		rest := clean[len(p):]
		// "remember:" needs nothing after the colon; "/remember" and
		// "!remember" need a space, so "/remembering" is not a command.
		if !strings.HasSuffix(p, ":") && rest != "" && rest[0] != ' ' && rest[0] != '\n' && rest[0] != '\t' {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", false
		}
		return rest, true
	}
	return "", false
}

// captureRemember stores a remember command as an operator feedback memory
// on the thread's work (incident or request scope; none when the thread is
// unknown) and acknowledges it: the 👍 reaction, plus a short text when the
// bot was mentioned. It returns the saved memory, nil when nothing was
// stored.
func (h *SlackHandler) captureRemember(channel, threadTS, messageTS, body string, mentioned bool) *database.Memory {
	if h.memoryManager == nil {
		return nil
	}
	scope := ""
	if work, err := lookupSlackThread(threadTS); err == nil && work != nil {
		scope = work.ScopeUUID
	}
	firstLine, _, _ := strings.Cut(body, "\n")
	mem := buildFeedbackMemory(body, services.FeedbackVerdict{Summary: firstLine}, scope)
	mem.EvidenceNote = "operator asked to remember it in the incident thread"
	if _, err := h.memoryManager.UpsertByName(mem); err != nil {
		slog.Warn("remember command: persist failed", "thread", threadTS, "scope", scope, "err", err)
		return nil
	}
	slog.Info("stored a remember command as memory", "scope", scope, "name", mem.Name)
	h.reactFeedback(channel, messageTS)
	if mentioned {
		h.postFeedbackTextAck(channel, threadTS, mem.Name)
	}
	return mem
}
