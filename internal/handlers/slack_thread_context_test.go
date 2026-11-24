package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func msg(ts, user, text string) slack.Message {
	return slack.Message{Msg: slack.Msg{Timestamp: ts, User: user, Text: text}}
}

// A canary PR is posted, the bot reviews it, the author posts the master PR in
// the same thread, then a human asks the bot "please review". The master PR
// link must reach the prompt.
func TestSelectThreadMessages_NewLinkAfterBotReply(t *testing.T) {
	msgs := []slack.Message{
		msg("1794985158.149059", "UAUTHOR", "please check pulls/7878 (canary)"),
		msg("1794985456.406249", "BOT", "review of 7878 ... Verdict: APPROVED"),
		msg("1794988297.342589", "UHUMAN", "Ok merged"),
		msg("1794996112.337899", "UAUTHOR", "please check pulls/7886 (master)"),
		msg("1794996435.924419", "UHUMAN", "<@BOT> please review"),
	}
	got := selectThreadMessagesSinceBotReply(msgs, "BOT", "1794996435.924419")
	want := []string{"<@UHUMAN>: Ok merged", "<@UAUTHOR>: please check pulls/7886 (master)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	text := formatThreadRequest(extractSlackMessageText(msgs[0]), got, "please review")
	for _, s := range []string{"pulls/7878", "pulls/7886", "Newer messages in this thread", "User request: please review"} {
		if !strings.Contains(text, s) {
			t.Errorf("prompt misses %q:\n%s", s, text)
		}
	}
	if strings.Index(text, "pulls/7886") < strings.Index(text, "pulls/7878") {
		t.Errorf("newer message must come after the original message:\n%s", text)
	}
}

// No bot reply yet: every reply between the parent and the mention counts.
func TestSelectThreadMessages_NoBotReply(t *testing.T) {
	msgs := []slack.Message{
		msg("100.000001", "U1", "alert"),
		msg("100.000002", "U2", "looks like fr5"),
		msg("100.000003", "U3", "<@BOT> check"),
	}
	got := selectThreadMessagesSinceBotReply(msgs, "BOT", "100.000003")
	if len(got) != 1 || got[0] != "<@U2>: looks like fr5" {
		t.Fatalf("got %q", got)
	}
}

// The mention is the first reply: no newer messages, and the prompt keeps the
// historic format byte for byte.
func TestFormatThreadRequest_HistoricFormatWithoutNewer(t *testing.T) {
	msgs := []slack.Message{msg("1.000001", "U1", "alert"), msg("1.000002", "U2", "<@BOT> check")}
	got := selectThreadMessagesSinceBotReply(msgs, "BOT", "1.000002")
	if len(got) != 0 {
		t.Fatalf("got %q, want none", got)
	}
	want := "Context — original message in this thread:\n---\nalert\n---\n\nUser request: check"
	if s := formatThreadRequest("alert", got, "check"); s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
	if s := formatThreadRequest("", nil, "check"); s != "check" {
		t.Fatalf("empty context: got %q", s)
	}
}

// Messages after the current mention (a race with a fast reply) are ignored.
func TestSelectThreadMessages_IgnoresMessagesAfterCurrent(t *testing.T) {
	msgs := []slack.Message{
		msg("1.000001", "U1", "alert"),
		msg("1.000002", "U2", "before"),
		msg("1.000003", "U2", "<@BOT> check"),
		msg("1.000004", "U2", "after"),
	}
	got := selectThreadMessagesSinceBotReply(msgs, "BOT", "1.000003")
	if len(got) != 1 || got[0] != "<@U2>: before" {
		t.Fatalf("got %q", got)
	}
}

// Caps keep the newest messages.
func TestSelectThreadMessages_Caps(t *testing.T) {
	msgs := []slack.Message{msg("1.000000", "U1", "parent")}
	for i := 1; i <= 15; i++ {
		msgs = append(msgs, msg(fmt.Sprintf("1.%06d", i), "U2", fmt.Sprintf("m%d", i)))
	}
	got := selectThreadMessagesSinceBotReply(msgs, "BOT", "")
	if len(got) != threadContextMaxMessages || got[0] != "<@U2>: m6" || got[len(got)-1] != "<@U2>: m15" {
		t.Fatalf("count cap: got %q", got)
	}

	big := strings.Repeat("x", 3000)
	msgs = []slack.Message{msg("1.000000", "U1", "parent"), msg("1.000001", "U2", big+"old"), msg("1.000002", "U2", big+"new")}
	got = selectThreadMessagesSinceBotReply(msgs, "BOT", "")
	if len(got) != 1 || !strings.HasSuffix(got[0], "new") {
		t.Fatalf("char cap must keep the newest message, got %d msgs", len(got))
	}

	huge := strings.Repeat("y", threadContextMaxChars+500)
	msgs = []slack.Message{msg("1.000000", "U1", "parent"), msg("1.000001", "U2", huge)}
	got = selectThreadMessagesSinceBotReply(msgs, "BOT", "")
	if len(got) != 1 || len(got[0]) != threadContextMaxChars {
		t.Fatalf("single oversized message must be truncated to the cap, got len %d", len(got[0]))
	}
}

func TestSlackTSAtOrAfter(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1794996435.924419", "1794996435.924419", true},
		{"1794996435.924420", "1794996435.924419", true},
		{"1794996112.337899", "1794996435.924419", false},
		{"999999999.000001", "1000000000.000000", false},
	}
	for _, c := range cases {
		if got := slackTSAtOrAfter(c.a, c.b); got != c.want {
			t.Errorf("slackTSAtOrAfter(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}
