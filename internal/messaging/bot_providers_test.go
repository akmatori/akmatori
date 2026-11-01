package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// recordedCall is one request captured by the fake API servers.
type recordedCall struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

type fakeAPI struct {
	mu      sync.Mutex
	calls   []recordedCall
	handler func(n int, c recordedCall, w http.ResponseWriter)
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := recordedCall{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.Body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		n := len(f.calls)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		f.handler(n, c, w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAPI) all() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.calls...)
}

func noSleep(slept *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return nil
	}
}

func tgChannel(externalID string) *database.Channel {
	return &database.Channel{
		ExternalID:  externalID,
		Integration: database.Integration{ID: 1, Provider: database.MessagingProviderTelegram, Credentials: database.JSONB{"bot_token": "123:ABC"}},
	}
}

func newTestTelegram(srv *httptest.Server, slept *[]time.Duration) *TelegramProvider {
	return &TelegramProvider{botHTTP: botHTTP{baseURL: srv.URL, httpClient: srv.Client, sleep: noSleep(slept)}}
}

func tgOK(w http.ResponseWriter, id int) {
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":`+jsonNum(id)+`}}`)
}

func jsonNum(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestTelegramProvider_PostMessage(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { tgOK(w, 100+n) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)

	got, err := p.PostMessage(context.Background(), tgChannel("-1001234567890"), "**Alert** <x> & [run](https://e.x/r)")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if got.MessageID != "101" {
		t.Errorf("MessageID = %q, want 101", got.MessageID)
	}
	calls := api.all()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Path != "/bot123:ABC/sendMessage" {
		t.Errorf("path = %q", c.Path)
	}
	if c.Body["chat_id"] != "-1001234567890" || c.Body["parse_mode"] != "HTML" || c.Body["disable_web_page_preview"] != true {
		t.Errorf("unexpected body: %v", c.Body)
	}
	if want := `<b>Alert</b> &lt;x&gt; &amp; <a href="https://e.x/r">run</a>`; c.Body["text"] != want {
		t.Errorf("text = %q, want %q", c.Body["text"], want)
	}
	if _, ok := c.Body["reply_parameters"]; ok {
		t.Errorf("top-level post must not reply: %v", c.Body)
	}
}

func TestTelegramProvider_PostThreadReply_WithTopic(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { tgOK(w, 7) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)

	if _, err := p.PostThreadReply(context.Background(), tgChannel("-100555:42"), "99", "done"); err != nil {
		t.Fatalf("PostThreadReply: %v", err)
	}
	c := api.all()[0]
	if c.Body["chat_id"] != "-100555" || c.Body["message_thread_id"] != float64(42) {
		t.Errorf("topic not split from chat id: %v", c.Body)
	}
	rp, _ := c.Body["reply_parameters"].(map[string]any)
	if rp["message_id"] != float64(99) {
		t.Errorf("reply_parameters = %v, want message_id 99", c.Body["reply_parameters"])
	}
	if _, err := p.PostThreadReply(context.Background(), tgChannel("@chan"), "", "x"); err == nil {
		t.Error("expected error without parent id")
	}
}

func TestTelegramProvider_SplitsLongMessages(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { tgOK(w, n) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)

	long := strings.Repeat("A paragraph with **bold** text & more.\n\n", 300) // ~12k chars
	got, err := p.PostMessage(context.Background(), tgChannel("@chan"), long)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	calls := api.all()
	if len(calls) < 3 {
		t.Fatalf("expected >= 3 messages, got %d", len(calls))
	}
	if got.MessageID != "1" {
		t.Errorf("MessageID = %q, want first chunk id 1", got.MessageID)
	}
	for _, c := range calls {
		text, _ := c.Body["text"].(string)
		assertTelegramChunkOK(t, text, TelegramMaxMessageChars)
	}
}

func TestTelegramProvider_RetriesOn429(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`)
			return
		}
		tgOK(w, 5)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)

	got, err := p.PostMessage(context.Background(), tgChannel("@chan"), "hi")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if got.MessageID != "5" || len(api.all()) != 2 {
		t.Errorf("want one retry and id 5, got id %q after %d calls", got.MessageID, len(api.all()))
	}
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Errorf("slept = %v, want [3s]", slept)
	}
}

func TestTelegramProvider_429TwiceFails(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)
	if _, err := p.PostMessage(context.Background(), tgChannel("@chan"), "hi"); err == nil {
		t.Fatal("expected error after second 429")
	}
	if n := len(api.all()); n != 2 {
		t.Errorf("calls = %d, want exactly 2 (one retry)", n)
	}
}

func TestTelegramProvider_FallsBackToPlainTextOnParseError(t *testing.T) {
	api := &fakeAPI{handler: func(n int, c recordedCall, w http.ResponseWriter) {
		if c.Body["parse_mode"] == "HTML" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unsupported start tag"}`)
			return
		}
		tgOK(w, 9)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)

	got, err := p.PostMessage(context.Background(), tgChannel("@chan"), "**bold** & [x](https://e.x)")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	calls := api.all()
	if len(calls) != 2 || got.MessageID != "9" {
		t.Fatalf("calls = %d id = %q", len(calls), got.MessageID)
	}
	if _, ok := calls[1].Body["parse_mode"]; ok {
		t.Errorf("retry must be plain text: %v", calls[1].Body)
	}
	if calls[1].Body["text"] != "bold & x (https://e.x)" {
		t.Errorf("plain text = %q", calls[1].Body["text"])
	}
}

func TestTelegramProvider_OtherAPIErrorSurfaces(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)
	_, err := p.PostMessage(context.Background(), tgChannel("@chan"), "hi")
	var apiErr *telegramAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != 403 {
		t.Fatalf("err = %v, want telegramAPIError 403", err)
	}
	if strings.Contains(err.Error(), "123:ABC") {
		t.Errorf("error leaks bot token: %v", err)
	}
	if len(api.all()) != 1 {
		t.Errorf("non-parse 4xx must not be retried")
	}
}

func TestTelegramProvider_UpdateMessage(t *testing.T) {
	api := &fakeAPI{handler: func(n int, c recordedCall, w http.ResponseWriter) {
		if strings.HasSuffix(c.Path, "/editMessageText") {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		tgOK(w, 200+n)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)
	p.limit = 50

	text := strings.Repeat("line of words\n\n", 10)
	if err := p.UpdateMessage(context.Background(), tgChannel("@chan"), "77", text); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	calls := api.all()
	if len(calls) < 2 || !strings.HasSuffix(calls[0].Path, "/editMessageText") {
		t.Fatalf("expected edit then replies, got %+v", calls)
	}
	if calls[0].Body["message_id"] != float64(77) {
		t.Errorf("edit message_id = %v", calls[0].Body["message_id"])
	}
	for _, c := range calls[1:] {
		rp, _ := c.Body["reply_parameters"].(map[string]any)
		if !strings.HasSuffix(c.Path, "/sendMessage") || rp["message_id"] != float64(77) {
			t.Errorf("overflow chunk must reply to 77: %+v", c)
		}
	}
	if err := p.UpdateMessage(context.Background(), tgChannel("@chan"), "abc", "x"); err == nil {
		t.Error("expected error for non-numeric message id")
	}
}

func TestTelegramProvider_LoadsIntegrationWhenNotPreloaded(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { tgOK(w, 1) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)
	p.loadIntegration = func(id uint) (*database.Integration, error) {
		if id != 4 {
			t.Errorf("loaded integration %d, want 4", id)
		}
		return &database.Integration{ID: 4, Credentials: database.JSONB{"bot_token": "999:LOADED"}}, nil
	}
	if _, err := p.PostMessage(context.Background(), &database.Channel{ExternalID: "@c", IntegrationID: 4}, "hi"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if api.all()[0].Path != "/bot999:LOADED/sendMessage" {
		t.Errorf("path = %q", api.all()[0].Path)
	}

	noToken := &database.Channel{ExternalID: "@c", Integration: database.Integration{ID: 1, Credentials: database.JSONB{}}}
	if _, err := p.PostMessage(context.Background(), noToken, "hi"); !errors.Is(err, errMissingBotToken) {
		t.Errorf("err = %v, want errMissingBotToken", err)
	}
}

func TestTelegramProvider_TestCredentials(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Akm","username":"akm_bot"}}`)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestTelegram(srv, &slept)
	got, err := p.TestCredentials(context.Background(), &database.Integration{Credentials: database.JSONB{"bot_token": "1:X"}})
	if err != nil || got != "@akm_bot" {
		t.Fatalf("TestCredentials = %q, %v", got, err)
	}
	if api.all()[0].Path != "/bot1:X/getMe" {
		t.Errorf("path = %q", api.all()[0].Path)
	}
}

func TestTelegramTarget(t *testing.T) {
	tests := []struct {
		in    string
		chat  string
		topic int64
	}{
		{"-1001234567890", "-1001234567890", 0},
		{"-1001234567890:42", "-1001234567890", 42},
		{"@channel", "@channel", 0},
		{"@channel:abc", "@channel:abc", 0},
	}
	for _, tt := range tests {
		chat, topic := telegramTarget(tt.in)
		if chat != tt.chat || topic != tt.topic {
			t.Errorf("telegramTarget(%q) = %q,%d want %q,%d", tt.in, chat, topic, tt.chat, tt.topic)
		}
	}
}

// --- Discord ---

func dcChannel() *database.Channel {
	return &database.Channel{
		ExternalID:  "112233",
		Integration: database.Integration{ID: 2, Provider: database.MessagingProviderDiscord, Credentials: database.JSONB{"bot_token": "dc-token"}},
	}
}

func newTestDiscord(srv *httptest.Server, slept *[]time.Duration) *DiscordProvider {
	return &DiscordProvider{botHTTP: botHTTP{baseURL: srv.URL, httpClient: srv.Client, sleep: noSleep(slept)}}
}

func dcOK(w http.ResponseWriter, id int) {
	_, _ = io.WriteString(w, `{"id":"`+jsonNum(id)+`"}`)
}

func TestDiscordProvider_PostMessage(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { dcOK(w, 500+n) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)

	got, err := p.PostMessage(context.Background(), dcChannel(), "#### Heading\n\n:warning: @everyone check")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if got.MessageID != "501" {
		t.Errorf("MessageID = %q", got.MessageID)
	}
	c := api.all()[0]
	if c.Method != http.MethodPost || c.Path != "/channels/112233/messages" || c.Auth != "Bot dc-token" {
		t.Errorf("unexpected request %+v", c)
	}
	if c.Body["content"] != "**Heading**\n\n⚠️ @everyone check" {
		t.Errorf("content = %q", c.Body["content"])
	}
	am, _ := c.Body["allowed_mentions"].(map[string]any)
	if parse, ok := am["parse"].([]any); !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions must disable pings: %v", c.Body["allowed_mentions"])
	}
}

func TestDiscordProvider_PostThreadReplyAndSplit(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { dcOK(w, n) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)

	long := "```\n" + strings.Repeat("log line here\n", 400) + "```"
	got, err := p.PostThreadReply(context.Background(), dcChannel(), "4242", long)
	if err != nil {
		t.Fatalf("PostThreadReply: %v", err)
	}
	calls := api.all()
	if len(calls) < 3 || got.MessageID != "1" {
		t.Fatalf("calls = %d id = %q", len(calls), got.MessageID)
	}
	for _, c := range calls {
		ref, _ := c.Body["message_reference"].(map[string]any)
		if ref["message_id"] != "4242" {
			t.Errorf("missing message_reference: %v", c.Body)
		}
		content, _ := c.Body["content"].(string)
		if utf16Len(content) > DiscordMaxMessageChars || strings.Count(content, "```")%2 != 0 {
			t.Errorf("bad chunk (len %d)", utf16Len(content))
		}
	}
}

func TestDiscordProvider_UpdateMessage(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) { dcOK(w, 900) }}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)
	if err := p.UpdateMessage(context.Background(), dcChannel(), "777", "**done**"); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	c := api.all()[0]
	if c.Method != http.MethodPatch || c.Path != "/channels/112233/messages/777" || c.Body["content"] != "**done**" {
		t.Errorf("unexpected request %+v", c)
	}
}

func TestDiscordProvider_RetriesOn429(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"You are being rate limited.","retry_after":0.5,"global":false}`)
			return
		}
		dcOK(w, 3)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)
	got, err := p.PostMessage(context.Background(), dcChannel(), "hi")
	if err != nil || got.MessageID != "3" {
		t.Fatalf("PostMessage = %v, %v", got, err)
	}
	if len(slept) != 1 || slept[0] != 500*time.Millisecond {
		t.Errorf("slept = %v, want [500ms]", slept)
	}
}

func TestDiscordProvider_ErrorSurfaces(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"Missing Access","code":50001}`)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)
	_, err := p.PostMessage(context.Background(), dcChannel(), "hi")
	var apiErr *discordAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != 50001 {
		t.Fatalf("err = %v, want discordAPIError 50001", err)
	}
}

func TestDiscordProvider_TestCredentials(t *testing.T) {
	api := &fakeAPI{handler: func(n int, _ recordedCall, w http.ResponseWriter) {
		_, _ = io.WriteString(w, `{"id":"1","username":"akmatori-bot"}`)
	}}
	srv := api.server(t)
	var slept []time.Duration
	p := newTestDiscord(srv, &slept)
	got, err := p.TestCredentials(context.Background(), &database.Integration{Credentials: database.JSONB{"bot_token": "t"}})
	if err != nil || got != "akmatori-bot" {
		t.Fatalf("TestCredentials = %q, %v", got, err)
	}
	c := api.all()[0]
	if c.Method != http.MethodGet || c.Path != "/users/@me" || c.Auth != "Bot t" {
		t.Errorf("unexpected request %+v", c)
	}
}

func TestBotProviders_Names(t *testing.T) {
	if NewTelegramProvider().Name() != database.MessagingProviderTelegram {
		t.Error("telegram name")
	}
	if NewDiscordProvider().Name() != database.MessagingProviderDiscord {
		t.Error("discord name")
	}
}
