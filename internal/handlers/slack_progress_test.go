package handlers

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// fakeStreamingClient captures calls made to a slackStreamingClient for tests.
type fakeStreamingClient struct {
	mu             sync.Mutex
	appendCalls    []fakeStreamingCall
	updateCalls    []fakeStreamingCall
	appendErr      error
	updateErr      error
	appendLastText string
	updateLastText string
}

type fakeStreamingCall struct {
	channel string
	ts      string
	text    string
}

func (f *fakeStreamingClient) AppendStream(channelID, timestamp string, options ...slack.MsgOption) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := extractMsgOptionText(channelID, options)
	f.appendCalls = append(f.appendCalls, fakeStreamingCall{channel: channelID, ts: timestamp, text: text})
	f.appendLastText = text
	return channelID, timestamp, f.appendErr
}

func (f *fakeStreamingClient) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := extractMsgOptionText(channelID, options)
	f.updateCalls = append(f.updateCalls, fakeStreamingCall{channel: channelID, ts: timestamp, text: text})
	f.updateLastText = text
	return channelID, timestamp, "", f.updateErr
}

func (f *fakeStreamingClient) snapshotAppend() []fakeStreamingCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeStreamingCall, len(f.appendCalls))
	copy(out, f.appendCalls)
	return out
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
		{"running", "🛠️ Running: gateway_call", "🛠️ Running: gateway_call"},
		{"running trimmed", "  🛠️ Running: gateway_call  ", "🛠️ Running: gateway_call"},
		{"ran", "✅ Ran: gateway_call", "✅ Ran: gateway_call"},
		{"failed", "❌ Failed: gateway_call", "❌ Failed: gateway_call"},
		{"thinking", "🤔 considering options", "🤔 considering options"},
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
	// Should end with the truncation marker
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("expected truncation ellipsis, got %q", got)
	}
}

func TestCondenseStatusLine_ToolNameTruncated(t *testing.T) {
	long := "🛠️ Running: " + repeat('z', 400)
	got := condenseStatusLine(long)
	if len(got) > slackStatusMaxLen {
		t.Errorf("running status length %d exceeds cap %d", len(got), slackStatusMaxLen)
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

// --- SlackProgressStreamer streaming path -----------------------------------

func TestSlackProgressStreamer_StreamingPath_AppendsMarkdown(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C123", "TS456", true, 1*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: gateway_call\n")
	// Wait past the throttle window for the second status emit.
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n✅ Ran: gateway_call\nArgs:\n{}\nOutput:\nok\n")

	got := fc.snapshotAppend()
	if len(got) != 2 {
		t.Fatalf("want 2 AppendStream calls, got %d (%+v)", len(got), got)
	}
	if got[0].channel != "C123" || got[0].ts != "TS456" {
		t.Errorf("first call wrong target: %+v", got[0])
	}
	if got[0].text != "🛠️ Running: gateway_call\n" {
		t.Errorf("first call text = %q, want running marker", got[0].text)
	}
	if got[1].text != "✅ Ran: gateway_call\n" {
		t.Errorf("second call text = %q, want ran marker", got[1].text)
	}
	if len(fc.snapshotUpdate()) != 0 {
		t.Errorf("UpdateMessage should not be called on streaming path")
	}
}

func TestSlackProgressStreamer_StreamingPath_DropsNonMarkers(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 1*time.Millisecond)

	s.AppendStatus("Some plain reasoning text the model emitted.\n")
	s.AppendStatus("Args:\nfoo\nOutput:\nbar baz\n")

	if len(fc.snapshotAppend()) != 0 {
		t.Errorf("expected no AppendStream calls for non-marker deltas, got %d", len(fc.snapshotAppend()))
	}
}

func TestSlackProgressStreamer_StreamingPath_PartialDeltaBuffered(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 1*time.Millisecond)

	// Marker arrives split across two deltas with no terminating newline yet.
	s.AppendStatus("\n🛠️ Running: gateway_")
	if got := fc.snapshotAppend(); len(got) != 0 {
		t.Fatalf("expected no emit before line is complete, got %+v", got)
	}
	s.AppendStatus("call\n")

	got := fc.snapshotAppend()
	if len(got) != 1 || got[0].text != "🛠️ Running: gateway_call\n" {
		t.Errorf("expected single emit with full marker, got %+v", got)
	}
}

func TestSlackProgressStreamer_StreamingPath_DedupesConsecutive(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 1*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	// Same marker repeated should be dropped (dedup)
	s.AppendStatus("\n🛠️ Running: gateway_call\n")

	got := fc.snapshotAppend()
	if len(got) != 1 {
		t.Errorf("expected dedupe to suppress duplicate marker, got %d calls: %+v", len(got), got)
	}
}

func TestSlackProgressStreamer_StreamingPath_ThrottleWindow(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 50*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: tool_a\n")
	// Within throttle window — should NOT emit yet.
	s.AppendStatus("\n✅ Ran: tool_a\n")

	got := fc.snapshotAppend()
	if len(got) != 1 {
		t.Fatalf("expected 1 emit during throttle window, got %d: %+v", len(got), got)
	}
	if got[0].text != "🛠️ Running: tool_a\n" {
		t.Errorf("first emit unexpected: %q", got[0].text)
	}

	// After window: next AppendStatus should flush the buffered status.
	time.Sleep(60 * time.Millisecond)
	s.AppendStatus("\n🛠️ Running: tool_b\n")
	got = fc.snapshotAppend()
	if len(got) != 2 {
		t.Fatalf("expected 2 emits after throttle window, got %d: %+v", len(got), got)
	}
	// The flushed batch should include both the previously buffered ✅ Ran and
	// the new 🛠️ Running line.
	if got[1].text != "✅ Ran: tool_a\n🛠️ Running: tool_b\n" {
		t.Errorf("second emit text = %q, want batched flush", got[1].text)
	}
}

// --- SlackProgressStreamer fallback path ------------------------------------

func TestSlackProgressStreamer_FallbackPath_UsesUpdateMessage(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", false, 1*time.Millisecond)

	s.AppendStatus("\n🛠️ Running: gateway_call\n")
	time.Sleep(3 * time.Millisecond)
	s.AppendStatus("\n✅ Ran: gateway_call\n")

	if len(fc.snapshotAppend()) != 0 {
		t.Errorf("AppendStream must not be called on fallback path")
	}
	got := fc.snapshotUpdate()
	if len(got) != 2 {
		t.Fatalf("want 2 UpdateMessage calls, got %d (%+v)", len(got), got)
	}
	// Second update should contain both accumulated status lines (replace, not append).
	if !contains(got[1].text, "🛠️ Running: gateway_call") || !contains(got[1].text, "✅ Ran: gateway_call") {
		t.Errorf("fallback update text missing accumulated lines: %q", got[1].text)
	}
	if !contains(got[1].text, "*Investigating...*") {
		t.Errorf("fallback update text missing header: %q", got[1].text)
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
	s.AppendStatus("\n🛠️ Running: x\n")
	s.Flush()
}

func TestSlackProgressStreamer_EmptyMessageTS_NoOp(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "", true, 1*time.Millisecond)
	s.AppendStatus("\n🛠️ Running: x\n")
	if len(fc.snapshotAppend()) != 0 {
		t.Errorf("expected no calls when messageTS is empty")
	}
}

func TestSlackProgressStreamer_AppendErrorIsLoggedNotPanicked(t *testing.T) {
	fc := &fakeStreamingClient{appendErr: errors.New("boom")}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 1*time.Millisecond)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	s.AppendStatus("\n🛠️ Running: x\n")
}

func TestSlackProgressStreamer_Flush_EmitsBufferedTrailingStatus(t *testing.T) {
	fc := &fakeStreamingClient{}
	s := NewSlackProgressStreamer(fc, "C", "TS", true, 1*time.Millisecond)

	// Marker line never receives its trailing newline.
	s.AppendStatus("🛠️ Running: gateway_call")
	if got := fc.snapshotAppend(); len(got) != 0 {
		t.Fatalf("status should remain buffered until Flush, got %+v", got)
	}
	s.Flush()
	got := fc.snapshotAppend()
	if len(got) != 1 || got[0].text != "🛠️ Running: gateway_call\n" {
		t.Errorf("Flush should emit buffered status, got %+v", got)
	}
}
