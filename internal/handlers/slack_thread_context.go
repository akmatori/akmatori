package handlers

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/slack-go/slack"
)

// Limits for the thread messages that go into a follow-up prompt.
const (
	threadContextMaxMessages = 10
	threadContextMaxChars    = 4000
	threadContextMaxPages    = 5
	threadContextPageSize    = 200
)

// fetchThreadContext reads a Slack thread and returns the parent message text
// plus the human/bot messages posted after the bot's last reply and before
// the current message (currentTS). Without these newer messages a follow-up
// @mention only sees the parent, so a request like "please review" in a
// thread where someone later posted a new link runs against the old target.
func (h *SlackHandler) fetchThreadContext(channelID, threadTS, currentTS string) (string, []string) {
	var msgs []slack.Message
	cursor := ""
	for page := 0; page < threadContextMaxPages; page++ {
		batch, hasMore, next, err := h.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
			Limit:     threadContextPageSize,
			Inclusive: true,
			Cursor:    cursor,
		})
		if err != nil {
			slog.Error("failed to fetch thread messages", "channel", channelID, "thread_ts", threadTS, "err", err)
			break
		}
		msgs = append(msgs, batch...)
		if !hasMore || next == "" {
			break
		}
		cursor = next
	}
	if len(msgs) == 0 {
		return "", nil
	}
	parent := extractSlackMessageText(msgs[0])
	return parent, selectThreadMessagesSinceBotReply(msgs, h.botUserID, currentTS)
}

// selectThreadMessagesSinceBotReply returns the text of the replies after the
// bot's last reply and before currentTS, oldest first, capped to
// threadContextMaxMessages and threadContextMaxChars (the newest messages are
// kept). msgs[0] is the thread parent and is never returned.
func selectThreadMessagesSinceBotReply(msgs []slack.Message, botUserID, currentTS string) []string {
	if len(msgs) < 2 {
		return nil
	}
	replies := msgs[1:]

	// Drop the current message and everything after it.
	end := len(replies)
	for i, m := range replies {
		if currentTS != "" && slackTSAtOrAfter(m.Timestamp, currentTS) {
			end = i
			break
		}
	}
	replies = replies[:end]

	// Start after the bot's last reply.
	start := 0
	if botUserID != "" {
		for i := len(replies) - 1; i >= 0; i-- {
			if replies[i].User == botUserID {
				start = i + 1
				break
			}
		}
	}
	replies = replies[start:]

	var out []string
	total := 0
	for i := len(replies) - 1; i >= 0 && len(out) < threadContextMaxMessages; i-- {
		text := strings.TrimSpace(extractSlackMessageText(replies[i]))
		if text == "" {
			continue
		}
		line := text
		if replies[i].User != "" {
			line = fmt.Sprintf("<@%s>: %s", replies[i].User, text)
		}
		if total+len(line) > threadContextMaxChars {
			if len(out) == 0 {
				out = append(out, strings.ToValidUTF8(line[:threadContextMaxChars], ""))
			}
			break
		}
		total += len(line)
		out = append(out, line)
	}
	// Reverse to oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// formatThreadRequest builds the agent task text for an @mention in a thread.
// With no newer messages it keeps the historic format exactly.
func formatThreadRequest(parent string, newer []string, request string) string {
	if parent == "" && len(newer) == 0 {
		return request
	}
	var b strings.Builder
	if parent != "" {
		fmt.Fprintf(&b, "Context — original message in this thread:\n---\n%s\n---\n\n", parent)
	}
	if len(newer) > 0 {
		b.WriteString("Newer messages in this thread (after the last bot reply, oldest first). The request below most likely refers to these, not to the original message:\n---\n")
		b.WriteString(strings.Join(newer, "\n---\n"))
		b.WriteString("\n---\n\n")
	}
	fmt.Fprintf(&b, "User request: %s", request)
	return b.String()
}

// slackTSAtOrAfter reports whether Slack timestamp a is at or after b.
// Slack timestamps are "<seconds>.<6-digit micros>", so equal-length strings
// compare correctly; fall back to comparing the integer part first.
func slackTSAtOrAfter(a, b string) bool {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) > len(bs)
	}
	if as != bs {
		return as > bs
	}
	return af >= bf
}
