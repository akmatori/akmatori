package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// fakeConversationsInfo serves conversations.info from a fixed table keyed by
// channel ID and counts the calls per channel, so tests can tell a cache hit
// from a lookup.
type fakeConversationsInfo struct {
	mu        sync.Mutex
	responses map[string]any
	calls     map[string]int
}

func newLabelTestResolver(t *testing.T, responses map[string]any) (*ChannelResolver, *fakeConversationsInfo) {
	t.Helper()
	fake := &fakeConversationsInfo{responses: responses, calls: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.info" {
			t.Errorf("unexpected Slack API call: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		id := r.FormValue("channel")
		fake.mu.Lock()
		fake.calls[id]++
		resp, ok := fake.responses[id]
		fake.mu.Unlock()
		if !ok {
			resp = map[string]any{"ok": false, "error": "channel_not_found"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	client := slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))
	return NewChannelResolver(client), fake
}

func (f *fakeConversationsInfo) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func channelInfo(fields map[string]any) map[string]any {
	return map[string]any{"ok": true, "channel": fields}
}

func TestResolveLabel_ChannelKinds(t *testing.T) {
	r, _ := newLabelTestResolver(t, map[string]any{
		"C0PUBLIC1": channelInfo(map[string]any{"id": "C0PUBLIC1", "name": "csqo_downtime"}),
		"G0PRIVAT1": channelInfo(map[string]any{"id": "G0PRIVAT1", "name": "ops-private", "is_private": true}),
		"D0DIRECT1": channelInfo(map[string]any{"id": "D0DIRECT1", "is_im": true, "user": "U0PEER123"}),
		"G0GROUPD1": channelInfo(map[string]any{"id": "G0GROUPD1", "name": "mpdm-a--b--c-1", "is_mpim": true}),
	})

	tests := []struct {
		name string
		id   string
		want string
	}{
		{"public channel gets a # name", "C0PUBLIC1", "#csqo_downtime"},
		{"private channel gets a # name", "G0PRIVAT1", "#ops-private"},
		{"DM does not leak the peer", "D0DIRECT1", "direct message"},
		{"group DM does not leak the mpdm name", "G0GROUPD1", "group direct message"},
		{"surrounding whitespace is trimmed", "  C0PUBLIC1 \n", "#csqo_downtime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.ResolveLabel(tt.id); got != tt.want {
				t.Errorf("ResolveLabel(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestResolveLabel_FailuresReturnEmpty(t *testing.T) {
	r, _ := newLabelTestResolver(t, map[string]any{
		"C0NOSCOPE": map[string]any{"ok": false, "error": "missing_scope"},
		"C0NONAME1": channelInfo(map[string]any{"id": "C0NONAME1", "name": ""}),
	})

	for _, id := range []string{"C0NOSCOPE", "C0NONAME1", "C0UNKNOWN"} {
		if got := r.ResolveLabel(id); got != "" {
			t.Errorf("ResolveLabel(%q) = %q, want empty", id, got)
		}
	}
}

func TestResolveLabel_NoLookupWithoutInput(t *testing.T) {
	r, fake := newLabelTestResolver(t, nil)
	if got := r.ResolveLabel("   "); got != "" {
		t.Errorf("ResolveLabel(blank) = %q, want empty", got)
	}
	if n := fake.callCount(""); n != 0 {
		t.Errorf("blank ID made %d API calls, want 0", n)
	}

	var nilResolver *ChannelResolver
	if got := nilResolver.ResolveLabel("C0PUBLIC1"); got != "" {
		t.Errorf("nil resolver ResolveLabel = %q, want empty", got)
	}
	if got := NewChannelResolver(nil).ResolveLabel("C0PUBLIC1"); got != "" {
		t.Errorf("nil client ResolveLabel = %q, want empty", got)
	}
}

func TestResolveLabel_CachesHitsAndMisses(t *testing.T) {
	r, fake := newLabelTestResolver(t, map[string]any{
		"C0PUBLIC1": channelInfo(map[string]any{"id": "C0PUBLIC1", "name": "alerts"}),
		"C0NOSCOPE": map[string]any{"ok": false, "error": "not_in_channel"},
	})

	for i := 0; i < 3; i++ {
		if got := r.ResolveLabel("C0PUBLIC1"); got != "#alerts" {
			t.Fatalf("call %d: ResolveLabel = %q, want #alerts", i, got)
		}
		if got := r.ResolveLabel("C0NOSCOPE"); got != "" {
			t.Fatalf("call %d: ResolveLabel(miss) = %q, want empty", i, got)
		}
	}
	// conversations.info is rate-limited, so a miss is cached like a hit.
	if n := fake.callCount("C0PUBLIC1"); n != 1 {
		t.Errorf("hit: %d API calls, want 1", n)
	}
	if n := fake.callCount("C0NOSCOPE"); n != 1 {
		t.Errorf("miss: %d API calls, want 1", n)
	}
}

func TestResolveLabel_ExpiredEntryIsRefreshed(t *testing.T) {
	r, fake := newLabelTestResolver(t, map[string]any{
		"C0PUBLIC1": channelInfo(map[string]any{"id": "C0PUBLIC1", "name": "renamed-alerts"}),
	})
	// A label cached before the channel was renamed, now past its TTL.
	r.labelCache["C0PUBLIC1"] = channelLabel{label: "#old-alerts", expiresAt: time.Now().Add(-time.Second)}

	if got := r.ResolveLabel("C0PUBLIC1"); got != "#renamed-alerts" {
		t.Errorf("ResolveLabel = %q, want #renamed-alerts", got)
	}
	if n := fake.callCount("C0PUBLIC1"); n != 1 {
		t.Errorf("%d API calls, want 1", n)
	}
	entry := r.labelCache["C0PUBLIC1"]
	if until := time.Until(entry.expiresAt); until < channelLabelTTL-time.Minute || until > channelLabelTTL {
		t.Errorf("new entry expires in %v, want about %v", until, channelLabelTTL)
	}
}

func TestClearLabelCache_ForcesLookup(t *testing.T) {
	r, fake := newLabelTestResolver(t, map[string]any{
		"C0PUBLIC1": channelInfo(map[string]any{"id": "C0PUBLIC1", "name": "alerts"}),
	})
	r.ResolveLabel("C0PUBLIC1")
	r.ClearLabelCache()
	r.ResolveLabel("C0PUBLIC1")
	if n := fake.callCount("C0PUBLIC1"); n != 2 {
		t.Errorf("%d API calls after ClearLabelCache, want 2", n)
	}

	// The zero-value resolver has a nil map; ResolveLabel must still cache.
	zero := &ChannelResolver{client: r.client}
	zero.ResolveLabel("C0PUBLIC1")
	if _, ok := zero.labelCache["C0PUBLIC1"]; !ok {
		t.Error("ResolveLabel on a zero-value resolver did not cache the label")
	}
}

func TestResolveLabel_ConcurrentCallsAreSafe(t *testing.T) {
	r, _ := newLabelTestResolver(t, map[string]any{
		"C0PUBLIC1": channelInfo(map[string]any{"id": "C0PUBLIC1", "name": "alerts"}),
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := r.ResolveLabel("C0PUBLIC1"); got != "#alerts" {
				t.Errorf("ResolveLabel = %q, want #alerts", got)
			}
		}()
	}
	r.ClearLabelCache()
	wg.Wait()
}
