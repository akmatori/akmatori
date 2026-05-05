package handlers

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// fakeStreamingClient captures UpdateMessage calls for tests.
type fakeStreamingClient struct {
	mu             sync.Mutex
	updateCalls    []fakeStreamingCall
	updateErr      error
	updateLastText string
}

type fakeStreamingCall struct {
	channel string
	ts      string
	text    string
}

func (f *fakeStreamingClient) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := extractMsgOptionText(channelID, options)
	f.updateCalls = append(f.updateCalls, fakeStreamingCall{channel: channelID, ts: timestamp, text: text})
	f.updateLastText = text
	return channelID, timestamp, "", f.updateErr
}

func (f *fakeStreamingClient) snapshotUpdate() []fakeStreamingCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeStreamingCall, len(f.updateCalls))
	copy(out, f.updateCalls)
	return out
}

// extractMsgOptionText executes a MsgOption chain against a stub config so
// we can inspect what text was sent without hitting the network.
func extractMsgOptionText(channelID string, options []slack.MsgOption) string {
	_, values, err := slack.UnsafeApplyMsgOptions("xoxb-test", channelID, "https://slack.example/api/", options...)
	if err != nil {
		return ""
	}
	if v := values.Get("markdown_text"); v != "" {
		return v
	}
	return values.Get("text")
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
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n✅ Ran: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n❌ Failed: gateway_call\n")
	s.Flush()

	if got := fc.snapshotUpdate(); len(got) != 0 {
		t.Errorf("tool markers should produce no UpdateMessage calls, got %+v", got)
	}
}

func TestSlackProgressStreamer_ThinkingReplacesInPlace(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C123", "TS456", 1*time.Millisecond)

	s.AppendStatus("\n🤔 first thought\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n🤔 second thought\n")

	got := fc.snapshotUpdate()
	if len(got) != 2 {
		t.Fatalf("want 2 UpdateMessage calls, got %d (%+v)", len(got), got)
	}
	if got[0].channel != "C123" || got[0].ts != "TS456" {
		t.Errorf("first call wrong target: %+v", got[0])
	}
	if got[0].text != "🤔 first thought" {
		t.Errorf("first update text = %q, want first thinking line", got[0].text)
	}
	if got[1].text != "🤔 second thought" {
		t.Errorf("second update text = %q, want second thinking line (single-line replace)", got[1].text)
	}
}

func TestSlackProgressStreamer_NonMarkersDropped(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	s.AppendStatus("Some plain reasoning text the model emitted.\n")
	s.AppendStatus("Args:\nfoo\nOutput:\nbar baz\n")

	if got := fc.snapshotUpdate(); len(got) != 0 {
		t.Errorf("expected no UpdateMessage calls for non-marker deltas, got %+v", got)
	}
}

func TestSlackProgressStreamer_PartialDeltaBuffered(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	s.AppendStatus("\n🤔 considering ")
	if got := fc.snapshotUpdate(); len(got) != 0 {
		t.Fatalf("expected no emit before line is complete, got %+v", got)
	}
	s.AppendStatus("the next step\n")

	got := fc.snapshotUpdate()
	if len(got) != 1 || got[0].text != "🤔 considering the next step" {
		t.Errorf("expected single emit with full thinking line, got %+v", got)
	}
}

func TestSlackProgressStreamer_DedupesConsecutive(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	s.AppendStatus("\n🤔 same thought\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n🤔 same thought\n")

	got := fc.snapshotUpdate()
	if len(got) != 1 {
		t.Errorf("expected dedupe to suppress duplicate thinking line, got %d calls: %+v", len(got), got)
	}
}

func TestSlackProgressStreamer_ThrottleWindow_KeepsOnlyLatest(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 50*time.Millisecond)

	s.AppendStatus("\n🤔 first\n")
	// Within throttle window — these should overwrite the buffered line, not queue.
	s.AppendStatus("\n🤔 second\n")
	s.AppendStatus("\n🤔 third\n")

	got := fc.snapshotUpdate()
	if len(got) != 1 {
		t.Fatalf("expected 1 emit during throttle window, got %d: %+v", len(got), got)
	}
	if got[0].text != "🤔 first" {
		t.Errorf("first emit unexpected: %q", got[0].text)
	}

	time.Sleep(60 * time.Millisecond)
	s.AppendStatus("\n🤔 fourth\n")
	got = fc.snapshotUpdate()
	if len(got) != 2 {
		t.Fatalf("expected 2 emits after throttle window, got %d: %+v", len(got), got)
	}
	// Only the most recent buffered line ("third") should be flushed — not the
	// queued "second". The "fourth" arrives just after, but in the same tick
	// it overwrites "third" before flushLocked snapshots, so we expect "fourth"
	// here. That's the desired single-line-replace semantic.
	if got[1].text != "🤔 fourth" {
		t.Errorf("second emit text = %q, want latest thinking line only", got[1].text)
	}
}

// --- Robustness -------------------------------------------------------------

func TestSlackProgressStreamer_NilClient_NoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AppendStatus on nil-client streamer panicked: %v", r)
		}
	}()
	var s *SlackProgressStreamer
	s.AppendStatus("\n🤔 x\n")
	s.Flush()
}

func TestSlackProgressStreamer_EmptyMessageTS_NoOp(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "", 1*time.Millisecond)
	s.AppendStatus("\n🤔 x\n")
	if len(fc.snapshotUpdate()) != 0 {
		t.Errorf("expected no calls when messageTS is empty")
	}
}

func TestSlackProgressStreamer_UpdateErrorIsLoggedNotPanicked(t *testing.T) {
	fc := &fakeStreamingClient{updateErr: errors.New("boom")}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	s.AppendStatus("\n🤔 x\n")
}

func TestSlackProgressStreamer_Flush_EmitsBufferedTrailingStatus(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", 1*time.Millisecond)

	// Thinking line never receives its trailing newline.
	s.AppendStatus("🤔 considering options")
	if got := fc.snapshotUpdate(); len(got) != 0 {
		t.Fatalf("status should remain buffered until Flush, got %+v", got)
	}
	s.Flush()
	got := fc.snapshotUpdate()
	if len(got) != 1 || got[0].text != "🤔 considering options" {
		t.Errorf("Flush should emit buffered status, got %+v", got)
	}
}
