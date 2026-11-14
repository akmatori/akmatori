package messaging

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func zulipChannel(serverURL string) *database.Channel {
	return &database.Channel{
		ExternalID: "alerts",
		Integration: database.Integration{Credentials: database.JSONB{
			"site_url": serverURL,
			"email":    "bot@example.com",
			"api_key":  "api-key",
			"topic":    "Incidents",
		}},
	}
}

func TestZulipProvider_Name(t *testing.T) {
	if got := NewZulipProvider().Name(); got != database.MessagingProviderZulip {
		t.Errorf("Name = %q, want %q", got, database.MessagingProviderZulip)
	}
}

func TestZulipProvider_PostMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/messages" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST /api/v1/messages", r.Method, r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "bot@example.com" || password != "api-key" {
			t.Fatalf("basic auth = %q:%q (present %t), want bot credentials", username, password, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form; got.Get("type") != "stream" || got.Get("to") != "alerts" || got.Get("topic") != "Incidents" || got.Get("content") != "investigating" {
			t.Errorf("form = %v, want stream destination, configured topic, and content", got)
		}
		_, _ = w.Write([]byte(`{"result":"success","id":42}`))
	}))
	defer server.Close()

	posted, err := NewZulipProvider().PostMessage(context.Background(), zulipChannel(server.URL), "investigating")
	if err != nil {
		t.Fatalf("PostMessage error = %v", err)
	}
	if posted.MessageID != "42" {
		t.Errorf("MessageID = %q, want 42", posted.MessageID)
	}
}

func TestZulipProvider_PostThreadReplyUsesParentTopic(t *testing.T) {
	var posted url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/messages/42":
			if r.Method != http.MethodGet {
				t.Fatalf("thread lookup method = %s, want GET", r.Method)
			}
			// Shape from https://zulip.com/api/get-message: the message is
			// nested; display_recipient is the stream name, subject the topic.
			_, _ = w.Write([]byte(`{"result":"success","msg":"","message":{"type":"stream","display_recipient":"ops","subject":"disk full","stream_id":7}}`))
		case "/api/v1/messages":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			posted = r.Form
			_, _ = w.Write([]byte(`{"result":"success","id":43}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	postedMessage, err := NewZulipProvider().PostThreadReply(context.Background(), zulipChannel(server.URL), "42", "resolved")
	if err != nil {
		t.Fatalf("PostThreadReply error = %v", err)
	}
	if postedMessage.MessageID != "43" {
		t.Errorf("MessageID = %q, want 43", postedMessage.MessageID)
	}
	if posted.Get("to") != "ops" || posted.Get("topic") != "disk full" || posted.Get("content") != "resolved" {
		t.Errorf("reply form = %v, want parent stream and topic", posted)
	}
}

func TestZulipProvider_UpdateMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/messages/42" || r.Method != http.MethodPatch {
			t.Fatalf("request = %s %s, want PATCH /api/v1/messages/42", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "content=updated" {
			t.Errorf("body = %q, want content=updated", body)
		}
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer server.Close()

	if err := NewZulipProvider().UpdateMessage(context.Background(), zulipChannel(server.URL), "42", "updated"); err != nil {
		t.Fatalf("UpdateMessage error = %v", err)
	}
}

func TestZulipProvider_RejectsIncompleteCredentials(t *testing.T) {
	_, err := NewZulipProvider().PostMessage(context.Background(), &database.Channel{ExternalID: "alerts"}, "message")
	if err == nil || !strings.Contains(err.Error(), "site_url, email, and api_key") {
		t.Errorf("PostMessage error = %v, want missing credentials error", err)
	}
}

// zulipFake is a fake Zulip server that records posted messages.
type zulipFake struct {
	posts    []url.Values
	patches  []url.Values
	gets     int
	failGet  bool
	rateOnce bool
	nextID   int64
}

func (f *zulipFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/messages":
			if f.rateOnce {
				f.rateOnce = false
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"result":"error","msg":"API usage exceeded rate limit","code":"RATE_LIMIT_HIT","retry-after":0.5}`))
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			f.posts = append(f.posts, r.PostForm)
			f.nextID++
			_, _ = io.WriteString(w, `{"result":"success","msg":"","id":`+strconv.FormatInt(100+f.nextID, 10)+`}`)
		case r.Method == http.MethodPatch:
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			f.patches = append(f.patches, r.PostForm)
			_, _ = w.Write([]byte(`{"result":"success","msg":""}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/messages/"):
			f.gets++
			if f.failGet {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"result":"error","msg":"Invalid message(s)","code":"BAD_REQUEST"}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":"success","msg":"","message":{"type":"stream","display_recipient":"ops","subject":"disk full"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/me":
			_, _ = w.Write([]byte(`{"result":"success","msg":"","email":"bot@example.com","full_name":"Akmatori Bot","is_bot":true}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}
}

func newZulipTest(t *testing.T) (*ZulipProvider, *zulipFake, *httptest.Server) {
	t.Helper()
	fake := &zulipFake{}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	p := NewZulipProvider()
	p.httpClient = func() *http.Client { return srv.Client() }
	p.sleep = func(context.Context, time.Duration) error { return nil }
	return p, fake, srv
}

func TestZulipProvider_ThreadReplyFallsBackToChannelTopic(t *testing.T) {
	p, fake, srv := newZulipTest(t)
	fake.failGet = true
	if _, err := p.PostThreadReply(context.Background(), zulipChannel(srv.URL), "42", "resolved"); err != nil {
		t.Fatalf("PostThreadReply error = %v", err)
	}
	if got := fake.posts[0]; got.Get("to") != "alerts" || got.Get("topic") != "Incidents" {
		t.Errorf("fallback reply = %v, want channel stream and topic", got)
	}
}

func TestZulipProvider_SplitsLongMessages(t *testing.T) {
	p, fake, srv := newZulipTest(t)
	p.limit = 100
	text := strings.Repeat("line of text that is long enough\n", 10) // ~330 chars
	posted, err := p.PostMessage(context.Background(), zulipChannel(srv.URL), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) < 4 {
		t.Fatalf("posts = %d, want the text split into several messages", len(fake.posts))
	}
	for _, f := range fake.posts {
		if n := len([]rune(f.Get("content"))); n > 100 {
			t.Errorf("part has %d chars, limit 100", n)
		}
	}
	if posted.MessageID != "101" {
		t.Errorf("MessageID = %q, want the first part (101)", posted.MessageID)
	}
}

func TestZulipProvider_UpdateLongMessagePostsRest(t *testing.T) {
	p, fake, srv := newZulipTest(t)
	p.limit = 50
	text := strings.Repeat("word ", 30) // 150 chars
	if err := p.UpdateMessage(context.Background(), zulipChannel(srv.URL), "42", text); err != nil {
		t.Fatal(err)
	}
	if len(fake.patches) != 1 || len(fake.posts) < 2 {
		t.Fatalf("patches = %d, posts = %d", len(fake.patches), len(fake.posts))
	}
	if got := fake.posts[0]; got.Get("to") != "ops" || got.Get("topic") != "disk full" {
		t.Errorf("rest of the update = %v, want the message's stream and topic", got)
	}
}

func TestZulipProvider_RetriesOnceOnRateLimit(t *testing.T) {
	p, fake, srv := newZulipTest(t)
	fake.rateOnce = true
	var waited time.Duration
	p.sleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }
	if _, err := p.PostMessage(context.Background(), zulipChannel(srv.URL), "hi"); err != nil {
		t.Fatal(err)
	}
	if waited != 500*time.Millisecond || len(fake.posts) != 1 {
		t.Errorf("waited %v, posts %d", waited, len(fake.posts))
	}
}

func TestZulipProvider_APIErrorAndKeyNotLeaked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"result":"error","msg":"Channel 'alerts' does not exist","code":"STREAM_DOES_NOT_EXIST"}`))
	}))
	defer srv.Close()
	_, err := NewZulipProvider().PostMessage(context.Background(), zulipChannel(srv.URL), "hi")
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "STREAM_DOES_NOT_EXIST") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "api-key") {
		t.Fatalf("api key leaked: %v", err)
	}
}

func TestZulipProvider_LoadsIntegrationWhenNotPreloaded(t *testing.T) {
	p, fake, srv := newZulipTest(t)
	p.loadIntegration = func(id uint) (*database.Integration, error) {
		if id != 7 {
			t.Fatalf("loaded integration %d", id)
		}
		return &zulipChannel(srv.URL).Integration, nil
	}
	if _, err := p.PostMessage(context.Background(), &database.Channel{ExternalID: "alerts", IntegrationID: 7}, "hi"); err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) != 1 {
		t.Fatalf("posts = %d", len(fake.posts))
	}
}

func TestZulipProvider_TestCredentials(t *testing.T) {
	p, _, srv := newZulipTest(t)
	name, err := p.TestCredentials(context.Background(), &zulipChannel(srv.URL).Integration)
	if err != nil || name != "Akmatori Bot" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	var _ CredentialTester = p
}

func TestZulipProvider_RejectsBadSiteURL(t *testing.T) {
	ch := zulipChannel("ftp://zulip.example.com")
	if _, err := NewZulipProvider().PostMessage(context.Background(), ch, "hi"); err == nil || !strings.Contains(err.Error(), "invalid site_url") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderZulip(t *testing.T) {
	got := RenderZulip("# Root cause\n\nSee <https://grafana/d/1|dashboard>. cc @**all**\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```\ncode @**all**\n```", 0)
	if len(got) != 1 {
		t.Fatalf("chunks = %d", len(got))
	}
	out := got[0]
	for _, want := range []string{"# Root cause", "[dashboard](https://grafana/d/1)", "cc @_**all**", "| a | b |", "```\ncode @**all**\n```"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	long := "```\n" + strings.Repeat("x\n", 80) + "```"
	parts := RenderZulip(long, 60)
	if len(parts) < 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, p := range parts {
		if !strings.HasPrefix(p, "```") || !strings.HasSuffix(p, "```") || len([]rune(p)) > 60 {
			t.Errorf("code part not closed or too long: %q", p)
		}
	}
	if len(RenderZulip("  \n ", 0)) != 0 {
		t.Error("empty text must give no chunks")
	}
}

func TestZulipTopicName(t *testing.T) {
	if got := zulipTopicName("  disk   full "); got != "disk full" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("я", 80)
	if got := []rune(zulipTopicName(long)); len(got) != ZulipMaxTopicChars || got[len(got)-1] != '…' {
		t.Errorf("long topic = %d runes", len(got))
	}
}
