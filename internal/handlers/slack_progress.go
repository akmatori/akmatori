package handlers

import (
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
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
}

// slackThinkingMaxLen is the per-line cap applied to thinking snippets. The
// progress message is a regular chat.postMessage (not a streamed message),
// so chat.update accepts up to ~40000 chars; this cap exists for UX — a
// status line should be short — not to satisfy an API limit.
const slackThinkingMaxLen = 120

// SlackProgressStreamer condenses agent OnOutput deltas into a single status
// line and replaces the bound Slack message body with the latest reasoning
// (🤔) line via chat.update. Tool start/end markers are intentionally
// dropped — the user only wants to see what the agent is currently thinking.
//
// It is safe to call AppendStatus from a single goroutine (the agent worker
// callback). Concurrent callers are serialized via the internal mutex.
type SlackProgressStreamer struct {
	client         slackStreamingClient
	channel        string
	messageTS      string
	appendInterval time.Duration

	mu              sync.Mutex
	lastUpdateAt    time.Time
	pendingLineBuf  string
	pendingThinking string // latest thinking line waiting to be pushed to Slack
	lastThinking    string // last thinking line we actually pushed (for dedup)
}

// NewSlackProgressStreamer constructs a streamer bound to a specific Slack
// message. Updates are issued via chat.update; the caller is responsible for
// posting the initial message (typically a plain chat.postMessage).
func NewSlackProgressStreamer(client slackStreamingClient, channel, messageTS string, appendInterval time.Duration) *SlackProgressStreamer {
	if appendInterval <= 0 {
		appendInterval = slackAppendInterval
	}
	return &SlackProgressStreamer{
		client:         client,
		channel:        channel,
		messageTS:      messageTS,
		appendInterval: appendInterval,
	}
}

// AppendStatus accepts a delta of agent output text, extracts any reasoning
// (🤔) lines from it, and replaces the Slack message body with the most
// recent line subject to throttling. Older queued thinking lines are
// discarded — only the latest matters for a single-line status indicator.
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
		if status := condenseStatusLine(line); status != "" {
			s.pendingThinking = status
		}
	}

	if s.pendingThinking == "" {
		return
	}
	if !s.lastUpdateAt.IsZero() && time.Since(s.lastUpdateAt) < s.appendInterval {
		return
	}

	s.flushLocked()
}

// Flush emits the latest buffered thinking line, ignoring the throttle
// window. It is intended for end-of-stream cleanup so the final status is
// not lost in the pending buffer.
func (s *SlackProgressStreamer) Flush() {
	if s == nil || s.messageTS == "" || s.client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if remaining := strings.TrimSpace(s.pendingLineBuf); remaining != "" {
		if status := condenseStatusLine(remaining); status != "" {
			s.pendingThinking = status
		}
		s.pendingLineBuf = ""
	}
	if s.pendingThinking == "" {
		return
	}
	s.flushLocked()
}

// flushLocked snapshots the pending thinking line, releases the mutex, then
// issues the Slack chat.update call. Caller must hold s.mu on entry; the
// lock is released and re-acquired before return so the caller's deferred
// Unlock still works.
//
// Releasing the mutex around the network call prevents an unresponsive Slack
// API from cascading into AppendStatus callers (the agent worker callback),
// which would otherwise queue up behind a single in-flight HTTP request.
func (s *SlackProgressStreamer) flushLocked() {
	if s.pendingThinking == "" || s.pendingThinking == s.lastThinking {
		s.pendingThinking = ""
		return
	}
	text := s.pendingThinking
	s.pendingThinking = ""
	s.lastThinking = text
	s.lastUpdateAt = time.Now()

	channel := s.channel
	messageTS := s.messageTS
	client := s.client

	s.mu.Unlock()
	defer s.mu.Lock()

	if _, _, _, err := client.UpdateMessage(channel, messageTS, slack.MsgOptionText(text, false)); err != nil {
		slog.Warn("failed to update progress message", "ts", messageTS, "err", err)
	}
}

// condenseStatusLine inspects a single line of agent output and returns the
// thinking snippet if the line begins with the 🤔 reasoning marker, or ""
// otherwise. Tool start/end markers (🛠️ Running:, ✅ Ran:, ❌ Failed:) are
// intentionally dropped — the Slack progress message is a single-line
// "what is the agent thinking right now?" status indicator, not a transcript.
//
// Marker source: agent-worker/src/agent-runner.ts.
func condenseStatusLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "🤔") {
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
