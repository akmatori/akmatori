package handlers

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack/slackevents"
)

// changeRunMatches reports whether a listener message that was classified as
// "change" starts an agent run: the channel has on_change=run and can post,
// the message is a thread root (a reply in the thread is discussion, not a
// new change), and the on_change_filter regex (if any) matches the raw text.
func changeRunMatches(channel *database.Channel, event *slackevents.MessageEvent, rawText string) bool {
	if channel == nil || services.NormalizeOnChange(channel.OnChange) != database.ChannelOnChangeRun || !channel.CanPost {
		return false
	}
	if event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.TimeStamp {
		return false
	}
	if channel.OnChangeFilter == "" {
		return true
	}
	re, err := regexp.Compile(channel.OnChangeFilter)
	if err != nil {
		// The API rejects an invalid regex; a bad stored value runs nothing.
		slog.Warn("invalid on_change_filter; change message not run", "channel", channel.UUID, "err", err)
		return false
	}
	return re.MatchString(rawText)
}

// changeRunTask is the agent task for a change announced in a listener
// channel. The message is quoted as posted (Slack link syntax keeps the URLs).
func changeRunTask(message, skill string) string {
	var b strings.Builder
	b.WriteString("A message in this channel announces a change (for example a pull request to review and merge, a deploy or a config change). ")
	b.WriteString("Review the change before it ships:\n")
	b.WriteString("- what changes, and what can break (risk and blast radius);\n")
	b.WriteString("- the test evidence in the message or the change, and what is missing;\n")
	b.WriteString("- how to roll it back.\n")
	b.WriteString("Read the change itself (for example the pull request diff) when your tools reach it. ")
	b.WriteString("Do not merge, deploy or change anything. Reply with a short review.\n")
	if skill != "" {
		fmt.Fprintf(&b, "Use the `%s` skill for this review.\n", skill)
	}
	b.WriteString("\nMessage:\n")
	b.WriteString(message)
	return b.String()
}

// runChangeReview starts request work for a change message and answers in
// the message thread. It never opens an incident. A later @mention in the
// thread continues the same work (lookupSlackThread finds the run).
//
// One message starts at most one run. The in-memory key is the one the
// app_mention path uses, so an @mention of the bot in the same post (which
// that path answers first: it does not wait for classification) wins. The
// thread lookup catches a Slack redelivery after the key expired: the first
// run already stored the thread on its agent_runs row.
func (h *SlackHandler) runChangeReview(channel *database.Channel, event *slackevents.MessageEvent, threadTS, rawText, eventUUID string) {
	dedupeKey := event.Channel + ":" + event.TimeStamp
	if _, loaded := h.processedMsgs.LoadOrStore(dedupeKey, struct{}{}); loaded {
		slog.Info("change message already handled; no change run", "dedupe_key", dedupeKey, "event_uuid", eventUUID)
		return
	}
	go func() {
		time.Sleep(60 * time.Second)
		h.processedMsgs.Delete(dedupeKey)
	}()
	if work, err := lookupSlackThread(threadTS); err == nil && work != nil {
		slog.Info("thread already has work; no change run", "thread_ts", threadTS, "scope", work.ScopeUUID, "incident", work.IncidentUUID)
		return
	}

	scopeUUID, _, err := h.skillService.PrepareRequestScope("incident-manager", eventUUID)
	if err != nil {
		slog.Error("failed to prepare change run", "channel", event.Channel, "err", err)
		h.postThreadError(event.Channel, threadTS, fmt.Sprintf("❌ Failed to start the agent: %v", err))
		return
	}
	h.runInThread(threadRun{
		Channel:    event.Channel,
		ThreadID:   threadTS,
		User:       event.User,
		Text:       rawText,
		Task:       changeRunTask(rawText, channel.OnChangeSkill),
		Work:       &slackThreadWork{ScopeUUID: scopeUUID},
		EventUUID:  eventUUID,
		SourceKind: database.IncidentSourceKindSlackListener,
		SourceUUID: channel.UUID,
	})
}
