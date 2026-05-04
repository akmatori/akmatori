package handlers

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// slackStreamingClient is the subset of the Slack client API used by
// SlackProgressStreamer. Defining it as an interface keeps the streamer
// testable without spinning up a real Slack client.
type slackStreamingClient interface {
	AppendStream(channelID, timestamp string, options ...slack.MsgOption) (string, string, error)
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
}

// slackStatusMaxLen caps the length of a single condensed status line so the
// stream does not balloon when a tool name or thinking snippet is very long.
const slackStatusMaxLen = 160

// slackThinkingMaxLen is the per-line cap applied to thinking snippets,
// which can be much longer than tool-status lines.
const slackThinkingMaxLen = 120

// slackFallbackMaxLines bounds the number of accumulated status lines kept
// in the chat.update fallback path so the message stays well within the
// Slack text-byte budget.
const slackFallbackMaxLines = 20

// SlackProgressStreamer condenses agent OnOutput deltas into short status
// lines and forwards them to Slack via chat.appendStream (when streaming is
// available) or chat.update (fallback for older workspaces).
//
// It is safe to call AppendStatus from a single goroutine (the agent worker
// callback). Concurrent callers are serialized via the internal mutex.
type SlackProgressStreamer struct {
	client         slackStreamingClient
	channel        string
	messageTS      string
	isStreaming    bool
	appendInterval time.Duration

	mu              sync.Mutex
	lastAppendAt    time.Time
	pendingLineBuf  string
	pendingStatuses []string
	lastStatus      string
	fallbackLines   []string
}

// NewSlackProgressStreamer constructs a streamer bound to a specific Slack
// message. If isStreaming is true, AppendStatus calls chat.appendStream;
// otherwise it falls back to chat.update.
func NewSlackProgressStreamer(client slackStreamingClient, channel, messageTS string, isStreaming bool, appendInterval time.Duration) *SlackProgressStreamer {
	if appendInterval <= 0 {
		appendInterval = slackAppendInterval
	}
	return &SlackProgressStreamer{
		client:         client,
		channel:        channel,
		messageTS:      messageTS,
		isStreaming:    isStreaming,
		appendInterval: appendInterval,
	}
}

// AppendStatus accepts a delta of agent output text, extracts any condensed
// status lines from it (tool start/end + thinking markers), and emits them
// to Slack subject to throttling. Statuses arriving while the throttle window
// is still closed are buffered and flushed on the next eligible call.
func (s *SlackProgressStreamer) AppendStatus(delta string) {
	if s == nil || s.messageTS == "" || s.client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pendingLineBuf += delta
	for {
		idx := strings.Index(s.pendingLineBuf, "\n")
		if idx < 0 {
			break
		}
		line := s.pendingLineBuf[:idx]
		s.pendingLineBuf = s.pendingLineBuf[idx+1:]
		status := condenseStatusLine(line)
		if status == "" {
			continue
		}
		if status == s.lastStatus {
			continue
		}
		s.lastStatus = status
		s.pendingStatuses = append(s.pendingStatuses, status)
	}

	if len(s.pendingStatuses) == 0 {
		return
	}
	if !s.lastAppendAt.IsZero() && time.Since(s.lastAppendAt) < s.appendInterval {
		return
	}

	s.flushLocked()
}

// Flush emits any buffered status lines, ignoring the throttle window. It is
// intended for end-of-stream cleanup so the final status is not lost in the
// pending buffer.
func (s *SlackProgressStreamer) Flush() {
	if s == nil || s.messageTS == "" || s.client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if remaining := strings.TrimSpace(s.pendingLineBuf); remaining != "" {
		if status := condenseStatusLine(remaining); status != "" && status != s.lastStatus {
			s.lastStatus = status
			s.pendingStatuses = append(s.pendingStatuses, status)
		}
		s.pendingLineBuf = ""
	}
	if len(s.pendingStatuses) == 0 {
		return
	}
	s.flushLocked()
}

// flushLocked snapshots pending statuses, releases the mutex, then issues the
// Slack HTTP call. Caller must hold s.mu on entry; the lock is released and
// re-acquired before return so the caller's deferred Unlock still works.
//
// Releasing the mutex around the network call prevents an unresponsive Slack
// API from cascading into AppendStatus callers (the agent worker callback),
// which would otherwise queue up behind a single in-flight HTTP request.
func (s *SlackProgressStreamer) flushLocked() {
	if len(s.pendingStatuses) == 0 {
		return
	}
	statuses := s.pendingStatuses
	s.pendingStatuses = nil
	s.lastAppendAt = time.Now()

	var (
		isStreaming = s.isStreaming
		channel     = s.channel
		messageTS   = s.messageTS
		body        string
	)
	if !isStreaming {
		s.fallbackLines = append(s.fallbackLines, statuses...)
		if len(s.fallbackLines) > slackFallbackMaxLines {
			s.fallbackLines = s.fallbackLines[len(s.fallbackLines)-slackFallbackMaxLines:]
		}
		body = fmt.Sprintf("*Investigating...*\n```\n%s\n```", strings.Join(s.fallbackLines, "\n"))
	}
	client := s.client

	s.mu.Unlock()
	defer s.mu.Lock()

	if isStreaming {
		text := strings.Join(statuses, "\n") + "\n"
		if _, _, err := client.AppendStream(channel, messageTS, slack.MsgOptionMarkdownText(text)); err != nil {
			slog.Warn("failed to append stream", "ts", messageTS, "err", err)
		}
		return
	}

	if _, _, _, err := client.UpdateMessage(channel, messageTS, slack.MsgOptionText(body, false)); err != nil {
		slog.Warn("failed to update progress message", "ts", messageTS, "err", err)
	}
}

// condenseStatusLine inspects a single line of agent output and returns a
// short status string suitable for the Slack progress stream, or "" if the
// line should be filtered out.
//
// Recognized markers (emitted by agent-worker/src/agent-runner.ts):
//   - "🛠️ Running: <tool>"          → tool start
//   - "✅ Ran: <tool>"               → tool success
//   - "❌ Failed: <tool>"            → tool failure
//   - "🤔 <thought>"                 → reasoning snippet
//
// Any "Args:"/"Output:" body, plain text deltas, and lines without a marker
// are intentionally dropped — Slack should never mirror the full transcript.
func condenseStatusLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(trimmed, "🛠️ Running:"):
		return truncateStatus(trimmed, slackStatusMaxLen)
	case strings.HasPrefix(trimmed, "✅ Ran:"):
		return truncateStatus(trimmed, slackStatusMaxLen)
	case strings.HasPrefix(trimmed, "❌ Failed:"):
		return truncateStatus(trimmed, slackStatusMaxLen)
	case strings.HasPrefix(trimmed, "🤔"):
		return truncateStatus(trimmed, slackThinkingMaxLen)
	}
	return ""
}

// truncateStatus shortens a status line to a byte budget, appending an
// ellipsis when truncation occurs. It uses byte length so the cap matches
// Slack's byte-based limits.
func truncateStatus(line string, max int) string {
	if max <= 0 || len(line) <= max {
		return line
	}
	const ellipsis = "…" // 3 bytes in UTF-8
	cutoff := max - len(ellipsis)
	if cutoff < 1 {
		cutoff = 1
	}
	// Avoid splitting a multi-byte UTF-8 sequence: walk back to a rune boundary.
	for cutoff > 0 && (line[cutoff]&0xC0) == 0x80 {
		cutoff--
	}
	return line[:cutoff] + ellipsis
}
