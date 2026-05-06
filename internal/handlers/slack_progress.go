package handlers

import (
	"strings"
	"sync"
	"time"
)

// slackThinkingMaxLen is the per-line cap applied to thinking snippets.
// Slack's loading_messages cap is well above 500 chars, but a status line
// should be short for UX reasons.
const slackThinkingMaxLen = 500

// SlackProgressStreamer condenses agent OnOutput deltas into a single status
// line and forwards the latest reasoning (🤔) line to a sink callback subject
// to a throttle window. Tool start/end markers are intentionally dropped —
// only "what is the agent currently thinking" passes through.
//
// The sink is typically TypingController.UpdateLoadingMessage, which pushes
// the line to Slack as the assistant.threads.setStatus loading_messages
// rotation content. The streamer used to call chat.update directly on a
// progress message body; that path is gone — the placeholder message stays
// static during the run.
//
// It is safe to call AppendStatus from a single goroutine (the agent worker
// callback). Concurrent callers are serialized via the internal mutex.
type SlackProgressStreamer struct {
	sink           func(line string)
	appendInterval time.Duration

	mu              sync.Mutex
	lastUpdateAt    time.Time
	pendingLineBuf  string
	pendingThinking string // latest thinking line waiting to be pushed
	lastThinking    string // last thinking line we actually pushed (for dedup)
}

// NewSlackProgressStreamer constructs a streamer that forwards condensed
// reasoning lines to sink. sink may be nil — in that case the streamer
// becomes a safe no-op (handy when Slack is disabled mid-flight).
func NewSlackProgressStreamer(sink func(line string), appendInterval time.Duration) *SlackProgressStreamer {
	if appendInterval <= 0 {
		appendInterval = slackAppendInterval
	}
	return &SlackProgressStreamer{
		sink:           sink,
		appendInterval: appendInterval,
	}
}

// AppendStatus accepts a delta of agent output text, extracts any reasoning
// (🤔) lines from it, and forwards the most recent line to the sink subject
// to throttling. Older queued thinking lines are discarded — only the latest
// matters for a single-line status indicator.
func (s *SlackProgressStreamer) AppendStatus(delta string) {
	if s == nil || s.sink == nil {
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
	if s == nil || s.sink == nil {
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

// flushLocked snapshots the pending thinking line and invokes the sink
// outside the mutex, so a slow sink (e.g. one that blocks on a Slack HTTP
// call) cannot back up subsequent AppendStatus callers behind it.
//
// Caller must hold s.mu on entry; the lock is released and re-acquired
// before return so the caller's deferred Unlock still works.
func (s *SlackProgressStreamer) flushLocked() {
	if s.pendingThinking == "" || s.pendingThinking == s.lastThinking {
		s.pendingThinking = ""
		return
	}
	text := s.pendingThinking
	s.pendingThinking = ""
	s.lastThinking = text
	s.lastUpdateAt = time.Now()

	sink := s.sink

	s.mu.Unlock()
	defer s.mu.Lock()

	sink(text)
}

// condenseStatusLine inspects a single line of agent output and returns the
// thinking snippet if the line begins with the 🤔 reasoning marker, or ""
// otherwise. Tool start/end markers (🛠️ Running:, ✅ Ran:, ❌ Failed:) are
// intentionally dropped — the Slack progress signal is a single-line
// "what is the agent thinking right now?" status indicator, not a
// transcript.
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
