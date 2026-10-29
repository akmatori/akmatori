package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/slack-go/slack"
)

// fakeSlackAPI records chat.postMessage calls and answers every Slack Web
// API call with ok.
type fakeSlackAPI struct {
	mu    sync.Mutex
	posts []map[string]string
}

func (f *fakeSlackAPI) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
		f.mu.Lock()
		f.posts = append(f.posts, map[string]string{
			"channel": r.Form.Get("channel"), "text": r.Form.Get("text"), "thread_ts": r.Form.Get("thread_ts"),
		})
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"channel":"` + r.Form.Get("channel") + `","ts":"1700000000.000100"}`))
		return
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (f *fakeSlackAPI) postsTo(channel string) []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]string
	for _, p := range f.posts {
		if p["channel"] == channel {
			out = append(out, p)
		}
	}
	return out
}

// newSlackAlertHandler builds an AlertHandler whose Slack client talks to a
// fake Slack server, with Slack enabled in settings.
func newSlackAlertHandler(t *testing.T, svc *eventsSkillService) (*AlertHandler, *fakeSlackAPI) {
	t.Helper()
	fake := &fakeSlackAPI{}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	mgr := slackutil.NewManager()
	mgr.SetClientForTest(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")))
	if err := database.GetDB().Create(&database.SlackSettings{
		BotToken: "xoxb-test", SigningSecret: "s", AppToken: "xapp-test", Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return NewAlertHandler(nil, mgr, nil, nil, svc, nil, nil), fake
}

func cronPostChannel() *database.Channel {
	return &database.Channel{
		UUID: "ch-ops", ExternalID: "COPS", Enabled: true, CanPost: true,
		Integration: database.Integration{Provider: database.MessagingProviderSlack, Enabled: true},
	}
}

func cronReport(post *database.Channel) ReportedAlert {
	alert := newCorrTestAlert()
	alert.AlertName = "disk full"
	return ReportedAlert{
		SourceUUID: "job-1", SourceLabel: database.IncidentSourceKindCron,
		SourceDisplay: "cron-reported", SourceName: "disk check",
		Alert: alert, PostChannel: post,
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessReportedAlert_CronReportPostsToJobChannel(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedHandlerIncident(t, db, "inc-new", "", "pending", 0)
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{spawnUUID: "inc-new"}}
	h, fake := newSlackAlertHandler(t, svc)

	incidentUUID, linked, err := h.ProcessReportedAlert(cronReport(cronPostChannel()))
	if err != nil || linked || incidentUUID != "inc-new" {
		t.Fatalf("got (%q, %v, %v), want new incident", incidentUUID, linked, err)
	}

	posts := fake.postsTo("COPS")
	if len(posts) == 0 || posts[0]["thread_ts"] != "" ||
		!strings.Contains(posts[0]["text"], "Alert: disk full") || !strings.Contains(posts[0]["text"], "cron-reported (disk check)") {
		t.Fatalf("posts to COPS = %+v, want the alert as a top-level message", posts)
	}
	var inc database.Incident
	if err := db.Where("uuid = ?", "inc-new").First(&inc).Error; err != nil {
		t.Fatal(err)
	}
	if inc.SlackChannelID != "COPS" || inc.SlackMessageTS != "1700000000.000100" {
		t.Fatalf("incident Slack context = %q/%q, want the alert thread", inc.SlackChannelID, inc.SlackMessageTS)
	}
	// The investigation runs in that thread: the stub cannot begin a run, so
	// the run loop reports the failure as a thread reply.
	waitFor(t, func() bool {
		for _, p := range fake.postsTo("COPS") {
			if p["thread_ts"] == "1700000000.000100" {
				return true
			}
		}
		return false
	})
}

func TestProcessReportedAlert_CronReportWithoutChannelStaysInUI(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedHandlerIncident(t, db, "inc-new", "", "pending", 0)
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{spawnUUID: "inc-new"}}
	h, fake := newSlackAlertHandler(t, svc)

	if _, _, err := h.ProcessReportedAlert(cronReport(nil)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the run goroutine finish
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.posts) != 0 {
		t.Fatalf("posts = %+v, want none for a cron that does not post", fake.posts)
	}
	var inc database.Incident
	if err := db.Where("uuid = ?", "inc-new").First(&inc).Error; err != nil {
		t.Fatal(err)
	}
	if inc.SlackChannelID != "" || inc.SlackMessageTS != "" {
		t.Fatalf("incident Slack context = %q/%q, want none", inc.SlackChannelID, inc.SlackMessageTS)
	}
}
