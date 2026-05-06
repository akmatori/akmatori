package handlers

import (
	"sync"
	"testing"
	"time"
)

// captureSink is a minimal sink the streamer pushes lines into. It records
// every line the streamer would forward to TypingController in production.
type captureSink struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureSink) sink(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
}

func (c *captureSink) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

// --- condenseStatusLine -----------------------------------------------------

func TestCondenseStatusLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \t  ", ""},
		{"plain text dropped", "Hello, world!", ""},
		{"args body dropped", "Args:", ""},
		{"output body dropped", "Output: foo bar baz", ""},
		{"running dropped", "🛠️ Running: gateway_call", ""},
		{"ran dropped", "✅ Ran: gateway_call", ""},
		{"failed dropped", "❌ Failed: gateway_call", ""},
		{"thinking", "🤔 considering options", "🤔 considering options"},
		{"thinking trimmed", "  🤔 considering options  ", "🤔 considering options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := condenseStatusLine(tc.in)
			if got != tc.want {
				t.Errorf("condenseStatusLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCondenseStatusLine_ThinkingTruncated(t *testing.T) {
	long := "🤔 " + repeat('a', 500)
	got := condenseStatusLine(long)
	if got == "" {
		t.Fatalf("expected non-empty status for long thinking line")
	}
	if len(got) > slackThinkingMaxLen {
		t.Errorf("thinking status length %d exceeds cap %d", len(got), slackThinkingMaxLen)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("expected truncation ellipsis, got %q", got)
	}
}

func repeat(c byte, n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = c
	}
	return string(buf)
}

// --- SlackProgressStreamer single-line replace semantics --------------------

func TestSlackProgressStreamer_ToolMarkersDropped(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n✅ Ran: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n❌ Failed: gateway_call\n")
	s.Flush()

	if got := cap.snapshot(); len(got) != 0 {
		t.Errorf("tool markers should produce no sink calls, got %+v", got)
	}
}

func TestSlackProgressStreamer_ThinkingReplacesInPlace(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	s.AppendStatus("\n🤔 first thought\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n🤔 second thought\n")

	got := cap.snapshot()
	if len(got) != 2 {
		t.Fatalf("want 2 sink calls, got %d (%+v)", len(got), got)
	}
	if got[0] != "🤔 first thought" {
		t.Errorf("first sink call = %q, want first thinking line", got[0])
	}
	if got[1] != "🤔 second thought" {
		t.Errorf("second sink call = %q, want second thinking line (single-line replace)", got[1])
	}
}

func TestSlackProgressStreamer_NonMarkersDropped(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	s.AppendStatus("Some plain reasoning text the model emitted.\n")
	s.AppendStatus("Args:\nfoo\nOutput:\nbar baz\n")

	if got := cap.snapshot(); len(got) != 0 {
		t.Errorf("expected no sink calls for non-marker deltas, got %+v", got)
	}
}

func TestSlackProgressStreamer_PartialDeltaBuffered(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	s.AppendStatus("\n🤔 considering ")
	if got := cap.snapshot(); len(got) != 0 {
		t.Fatalf("expected no emit before line is complete, got %+v", got)
	}
	s.AppendStatus("the next step\n")

	got := cap.snapshot()
	if len(got) != 1 || got[0] != "🤔 considering the next step" {
		t.Errorf("expected single emit with full thinking line, got %+v", got)
	}
}

func TestSlackProgressStreamer_DedupesConsecutive(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	s.AppendStatus("\n🤔 same thought\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n🤔 same thought\n")

	got := cap.snapshot()
	if len(got) != 1 {
		t.Errorf("expected dedupe to suppress duplicate thinking line, got %d calls: %+v", len(got), got)
	}
}

func TestSlackProgressStreamer_ThrottleWindow_KeepsOnlyLatest(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 50*time.Millisecond)

	s.AppendStatus("\n🤔 first\n")
	// Within throttle window — these should overwrite the buffered line, not queue.
	s.AppendStatus("\n🤔 second\n")
	s.AppendStatus("\n🤔 third\n")

	got := cap.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 emit during throttle window, got %d: %+v", len(got), got)
	}
	if got[0] != "🤔 first" {
		t.Errorf("first emit unexpected: %q", got[0])
	}

	time.Sleep(60 * time.Millisecond)
	s.AppendStatus("\n🤔 fourth\n")
	got = cap.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 emits after throttle window, got %d: %+v", len(got), got)
	}
	// Only the most recent buffered line is flushed; "fourth" arrives just after,
	// but in the same tick it overwrites "third" before flushLocked snapshots, so
	// we expect "fourth" here. That's the desired single-line-replace semantic.
	if got[1] != "🤔 fourth" {
		t.Errorf("second emit text = %q, want latest thinking line only", got[1])
	}
}

// --- Robustness -------------------------------------------------------------

func TestSlackProgressStreamer_NilStreamer_NoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AppendStatus on nil streamer panicked: %v", r)
		}
	}()
	var s *SlackProgressStreamer
	s.AppendStatus("\n🤔 x\n")
	s.Flush()
}

func TestSlackProgressStreamer_NilSink_NoOp(t *testing.T) {
	s := NewSlackProgressStreamer(nil, 1*time.Millisecond)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AppendStatus with nil sink panicked: %v", r)
		}
	}()
	s.AppendStatus("\n🤔 x\n")
	s.Flush()
}

func TestSlackProgressStreamer_Flush_EmitsBufferedTrailingStatus(t *testing.T) {
	cap := &captureSink{}
	s := NewSlackProgressStreamer(cap.sink, 1*time.Millisecond)

	// Thinking line never receives its trailing newline.
	s.AppendStatus("🤔 considering options")
	if got := cap.snapshot(); len(got) != 0 {
		t.Fatalf("status should remain buffered until Flush, got %+v", got)
	}
	s.Flush()
	got := cap.snapshot()
	if len(got) != 1 || got[0] != "🤔 considering options" {
		t.Errorf("Flush should emit buffered status, got %+v", got)
	}
}
