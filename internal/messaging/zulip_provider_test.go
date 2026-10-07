package messaging

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
			_, _ = w.Write([]byte(`{"result":"success","stream":"ops","subject":"disk full"}`))
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
