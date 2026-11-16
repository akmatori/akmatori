package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts/extraction"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestChangeRunMatches(t *testing.T) {
	runCh := func(filter string) *database.Channel {
		return &database.Channel{UUID: "ch", CanListen: true, CanPost: true, OnChange: "run", OnChangeFilter: filter}
	}
	top := &slackevents.MessageEvent{TimeStamp: "1.1"}
	tests := []struct {
		name    string
		channel *database.Channel
		event   *slackevents.MessageEvent
		text    string
		want    bool
	}{
		{"run, no filter", runCh(""), top, "deploy api v2", true},
		{"store", &database.Channel{CanPost: true, OnChange: "store"}, top, "x", false},
		{"empty means store", &database.Channel{CanPost: true}, top, "x", false},
		{"silent listener", &database.Channel{OnChange: "run"}, top, "x", false},
		{"thread reply", runCh(""), &slackevents.MessageEvent{TimeStamp: "1.2", ThreadTimeStamp: "1.1"}, "x", false},
		{"thread root with own ts", runCh(""), &slackevents.MessageEvent{TimeStamp: "1.1", ThreadTimeStamp: "1.1"}, "x", true},
		{"filter matches link target", runCh(`github\.com/[^/]+/[^/]+/pull/\d+`),
			top, "please merge <https://github.com/acme/api/pull/12|PR #12>", true},
		{"filter does not match", runCh(`/pull/\d+`), top, "deploy at 15:00", false},
		{"invalid stored filter", runCh(`(`), top, "x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changeRunMatches(tt.channel, tt.event, tt.text); got != tt.want {
				t.Errorf("changeRunMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangeRunTask(t *testing.T) {
	task := changeRunTask("merge <https://git/x/pull/1|PR 1>", "")
	if !strings.Contains(task, "https://git/x/pull/1") || !strings.Contains(task, "roll it back") {
		t.Errorf("task misses the message or the review points:\n%s", task)
	}
	if strings.Contains(task, "skill for this review") {
		t.Errorf("task names a skill when none is set:\n%s", task)
	}
	if !strings.Contains(changeRunTask("m", "pr-review"), "Use the `pr-review` skill") {
		t.Error("task does not name the configured skill")
	}
}

// A top-level change message on an on_change=run channel starts request work
// that answers in the message thread; no incident, no alert.
func TestHandleAlertChannelMessage_ChangeStartsRunInThread(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"merge api PR 12","event_kind":"change","event_confidence":0.95,"event_reasoning":"PR to merge"}`, nil
	}}
	fake := &fakeSlackAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)

	svc := &requestRunSkillService{}
	h := &SlackHandler{
		client:         slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")),
		skillService:   svc,
		alertExtractor: extraction.NewAlertExtractor(caller),
		eventService:   services.NewEventService(db, caller),
	}
	channel := &database.Channel{
		UUID: "ch-changes", ExternalID: "CCHG", CanListen: true, CanPost: true,
		OnChange: "run", OnChangeFilter: `/pull/\d+`, OnChangeSkill: "pr-review",
	}
	h.handleAlertChannelMessage(&slackevents.MessageEvent{
		Channel: "CCHG", User: "U1", TimeStamp: "1700.1",
		Text: "please review and merge <https://github.com/acme/api/pull/12|PR 12>, tested on staging",
	}, channel)

	svc.runMu.Lock()
	starts := append([]services.RunStart(nil), svc.starts...)
	svc.runMu.Unlock()
	if len(starts) != 1 {
		t.Fatalf("runs started = %d, want 1", len(starts))
	}
	s := starts[0]
	if s.SourceKind != database.IncidentSourceKindSlackListener || s.SourceUUID != "ch-changes" ||
		s.SlackChannelID != "CCHG" || s.SlackThreadTS != "1700.1" || s.IncidentUUID != "" || s.EventUUID == "" || s.ScopeUUID != s.EventUUID {
		t.Fatalf("run start = %+v, want request work in the message thread", s)
	}
	var ev database.Event
	if err := db.Where("uuid = ?", s.EventUUID).First(&ev).Error; err != nil || ev.Kind != database.EventKindChange {
		t.Fatalf("event = %+v (%v), want a change event", ev, err)
	}
	var incidents int64
	db.Model(&database.Incident{}).Count(&incidents)
	if incidents != 0 {
		t.Fatalf("incidents = %d, want none", incidents)
	}
	// No worker in the test: the run ends with an error reply in the thread.
	posts := fake.postsTo("CCHG")
	if len(posts) == 0 || posts[len(posts)-1]["thread_ts"] != "1700.1" {
		t.Fatalf("posts = %+v, want a reply in the message thread", posts)
	}
}

// With on_change=store (the default) a change message is stored only.
func TestHandleAlertChannelMessage_ChangeStoredByDefault(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"deploy","event_kind":"change","event_confidence":0.95,"event_reasoning":"deploy"}`, nil
	}}
	srv := httptest.NewServer(http.HandlerFunc((&fakeSlackAPI{}).handler))
	t.Cleanup(srv.Close)
	svc := &requestRunSkillService{}
	h := &SlackHandler{
		client:         slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")),
		skillService:   svc,
		alertExtractor: extraction.NewAlertExtractor(caller),
		eventService:   services.NewEventService(db, caller),
	}
	h.handleAlertChannelMessage(&slackevents.MessageEvent{Channel: "CCHG", TimeStamp: "1700.2", Text: "deploying api v2"},
		&database.Channel{UUID: "ch", CanListen: true, CanPost: true})
	if len(svc.starts) != 0 {
		t.Fatalf("runs started = %d, want 0", len(svc.starts))
	}
}

// A redelivered message, or one the app_mention path already answered,
// starts no second run; nor does a thread that already has work.
func TestRunChangeReview_OneRunPerMessage(t *testing.T) {
	db := setupEventsHandlerDB(t)
	srv := httptest.NewServer(http.HandlerFunc((&fakeSlackAPI{}).handler))
	t.Cleanup(srv.Close)
	svc := &requestRunSkillService{}
	h := &SlackHandler{client: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), skillService: svc}
	channel := &database.Channel{UUID: "ch", CanListen: true, CanPost: true, OnChange: "run"}
	ev := &slackevents.MessageEvent{Channel: "CCHG", TimeStamp: "1700.3", Text: "merge PR 3"}

	h.runChangeReview(channel, ev, "1700.3", ev.Text, "ev-1")
	h.runChangeReview(channel, ev, "1700.3", ev.Text, "ev-2") // redelivery
	if len(svc.starts) != 1 {
		t.Fatalf("runs after redelivery = %d, want 1", len(svc.starts))
	}

	// The key expired, but the thread already has request work.
	h.processedMsgs.Delete("CCHG:1700.3")
	if err := db.Create(&database.AgentRun{RunID: "r-1", ScopeUUID: "ev-1", SlackThreadTS: "1700.3"}).Error; err != nil {
		t.Fatal(err)
	}
	h.runChangeReview(channel, ev, "1700.3", ev.Text, "ev-3")
	if len(svc.starts) != 1 {
		t.Fatalf("runs after late redelivery = %d, want 1", len(svc.starts))
	}

	// The app_mention path answered the post first.
	h.processedMsgs.Store("CCHG:1700.4", struct{}{})
	h.runChangeReview(channel, &slackevents.MessageEvent{Channel: "CCHG", TimeStamp: "1700.4"}, "1700.4", "x", "ev-4")
	if len(svc.starts) != 1 {
		t.Fatalf("runs after mention = %d, want 1", len(svc.starts))
	}
}

func TestExtractSlackMessageText_AttachmentTitleLink(t *testing.T) {
	msg := slack.Message{Msg: slack.Msg{Attachments: []slack.Attachment{{
		Title: "PR #12: bump timeout", TitleLink: "https://github.com/acme/api/pull/12", Text: "tested on staging",
	}}}}
	raw := extractSlackMessageText(msg)
	if !strings.Contains(raw, "<https://github.com/acme/api/pull/12|PR #12: bump timeout>") {
		t.Errorf("raw text = %q, want the title as a Slack link", raw)
	}
	if !changeRunMatches(&database.Channel{OnChange: "run", CanPost: true, OnChangeFilter: `/pull/\d+`},
		&slackevents.MessageEvent{TimeStamp: "1"}, raw) {
		t.Error("filter does not match the attachment link")
	}
}

// A finished run adds no white_check_mark in any channel: on an alert it reads
// as "solved" although the agent only investigated, and in a change channel
// it reads as the approver's "merged" marker. A failed run keeps the x.
func TestFinishSlackMessage_NoCheckMarkReaction(t *testing.T) {
	fake := &fakeSlackAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	h := &SlackHandler{
		client: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")),
		alertChannels: map[string]*database.Channel{
			"CCHG": {ExternalID: "CCHG", CanListen: true, CanPost: true, OnChange: "run"},
			"CALR": {ExternalID: "CALR", CanListen: true, CanPost: true, OnChange: "store"},
		},
	}

	h.finishSlackMessage("CCHG", "1700.1", "", "U1", "review PR", "review", "review", "", false, "")
	h.finishSlackMessage("CALR", "1700.2", "", "U1", "alert", "report", "report", "", false, "")
	h.finishSlackMessage("CMENTION", "1700.3", "", "U1", "question", "answer", "answer", "", false, "")
	h.finishSlackMessage("CALR", "1700.4", "", "U1", "alert", "failed", "failed", "", true, "")

	if got := fake.reactionsOn("CCHG"); len(got) != 0 {
		t.Fatalf("change channel reactions = %v, want none", got)
	}
	if got := fake.reactionsOn("CMENTION"); len(got) != 0 {
		t.Fatalf("mention channel reactions = %v, want none", got)
	}
	if got := fake.reactionsOn("CALR"); len(got) != 1 || got[0] != "x" {
		t.Fatalf("alert channel reactions = %v, want only the error x", got)
	}
	for _, ch := range []string{"CCHG", "CALR", "CMENTION"} {
		if len(fake.postsTo(ch)) == 0 {
			t.Fatalf("no thread reply in %s", ch)
		}
	}
}
