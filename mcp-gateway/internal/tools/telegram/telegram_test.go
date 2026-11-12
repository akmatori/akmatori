package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const testToken = "123456:SECRET-TOKEN"

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Integration{}, &database.Channel{}, &database.AgentRun{}, &database.ProxySettings{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seed adds one Telegram integration with the given chats; the first one is
// the default. It also adds a running alert run "run-1" in scope "scope-1".
func seed(t *testing.T, db *gorm.DB, chats ...string) uint {
	t.Helper()
	in := database.Integration{Provider: "telegram", Name: "TG", Credentials: database.JSONB{"bot_token": testToken}, Enabled: true}
	if err := db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	for i, c := range chats {
		name := c
		if i := strings.Index(c, "="); i > 0 {
			name, c = c[:i], c[i+1:]
		}
		ch := database.Channel{IntegrationID: in.ID, ExternalID: c, DisplayName: name, CanPost: true, IsDefaultPost: i == 0, Enabled: true}
		if err := db.Create(&ch).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.AgentRun{RunID: "run-1", ScopeUUID: "scope-1", IncidentUUID: "scope-1", SourceKind: "alert", Status: "running", StartedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	return in.ID
}

type fakeBot struct {
	mu       sync.Mutex
	calls    []map[string]interface{}
	paths    []string
	failNext []string // canned bodies for the next calls (status 200 unless "429"/"400" prefix)
	nextID   int64
}

func (f *fakeBot) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var payload map[string]interface{}
	_ = json.Unmarshal(body, &payload)
	f.calls = append(f.calls, payload)
	f.paths = append(f.paths, r.URL.Path)
	if len(f.failNext) > 0 {
		resp := f.failNext[0]
		f.failNext = f.failNext[1:]
		switch {
		case strings.HasPrefix(resp, "429"):
			w.WriteHeader(http.StatusTooManyRequests)
			resp = resp[3:]
		case strings.HasPrefix(resp, "400"):
			w.WriteHeader(http.StatusBadRequest)
			resp = resp[3:]
		}
		_, _ = io.WriteString(w, resp)
		return
	}
	f.nextID++
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": map[string]interface{}{"message_id": f.nextID}})
}

func newTool(t *testing.T, db *gorm.DB) (*Tool, *fakeBot) {
	t.Helper()
	bot := &fakeBot{nextID: 100}
	srv := httptest.NewServer(http.HandlerFunc(bot.handler))
	t.Cleanup(srv.Close)
	tool := NewTool(db, log.New(io.Discard, "", 0))
	tool.baseURL = srv.URL
	tool.httpClient = func(context.Context) *http.Client { return srv.Client() }
	tool.sleep = func(context.Context, time.Duration) error { return nil }
	return tool, bot
}

func runCtx(runID string) context.Context {
	return mcp.WithRunID(context.Background(), runID)
}

func TestListChats(t *testing.T) {
	db := newTestDB(t)
	id := seed(t, db, "oncall=-1001", "Alpha=-1002")
	// Not listed: disabled channel, channel that cannot post, other provider,
	// integration without a token.
	db.Create(&database.Channel{IntegrationID: id, ExternalID: "-1003", DisplayName: "off", CanPost: true, Enabled: false})
	db.Model(&database.Channel{}).Where("external_id = ?", "-1003").Update("enabled", false)
	db.Create(&database.Channel{IntegrationID: id, ExternalID: "-1004", DisplayName: "listen", CanPost: false, Enabled: true})
	slack := database.Integration{Provider: "slack", Name: "S", Credentials: database.JSONB{"bot_token": "xoxb"}, Enabled: true}
	db.Create(&slack)
	db.Create(&database.Channel{IntegrationID: slack.ID, ExternalID: "C1", DisplayName: "slack", CanPost: true, Enabled: true})
	noTok := database.Integration{Provider: "telegram", Name: "T2", Credentials: database.JSONB{}, Enabled: true}
	db.Create(&noTok)
	db.Create(&database.Channel{IntegrationID: noTok.ID, ExternalID: "-1005", DisplayName: "notoken", CanPost: true, Enabled: true})

	tool, _ := newTool(t, db)
	out, err := tool.ListChats(context.Background(), "scope-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	got := string(b)
	if !strings.Contains(got, `"chat":"-1001"`) || !strings.Contains(got, `"chat":"-1002"`) || !strings.Contains(got, `"count":2`) {
		t.Fatalf("unexpected chats: %s", got)
	}
	if strings.Index(got, "-1001") > strings.Index(got, "-1002") {
		t.Fatalf("default chat must come first: %s", got)
	}
	if strings.Contains(got, testToken) {
		t.Fatalf("token leaked: %s", got)
	}
}

func TestListChats_None(t *testing.T) {
	tool, _ := newTool(t, newTestDB(t))
	if _, err := tool.ListChats(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "no Telegram chat") {
		t.Fatalf("want no-chat error, got %v", err)
	}
}

func TestSendMessage_DefaultChat(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001:42", "Alpha=-1002")
	tool, bot := newTool(t, db)

	out, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "disk full on db-1"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]interface{})
	if res["chat"] != "-1001:42" {
		t.Fatalf("chat = %v", res["chat"])
	}
	if len(bot.calls) != 1 {
		t.Fatalf("calls = %d", len(bot.calls))
	}
	call := bot.calls[0]
	if call["chat_id"] != "-1001" || call["message_thread_id"] != float64(42) || call["text"] != "disk full on db-1" {
		t.Fatalf("payload = %v", call)
	}
	if _, ok := call["parse_mode"]; ok {
		t.Fatalf("plain text must not set parse_mode: %v", call)
	}
	if bot.paths[0] != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("path = %s", bot.paths[0])
	}
}

func TestSendMessage_ByNameAndID(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001", "Alpha=-1002")
	tool, bot := newTool(t, db)

	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "a", "chat": "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "b", "chat": "-1002"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range bot.calls {
		if c["chat_id"] != "-1002" {
			t.Fatalf("payload = %v", c)
		}
	}
}

func TestSendMessage_RefusesUnknownChat(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, bot := newTool(t, db)
	_, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x", "chat": "-999"})
	if err == nil || !strings.Contains(err.Error(), "not a configured Telegram chat") {
		t.Fatalf("want refusal, got %v", err)
	}
	if len(bot.calls) != 0 {
		t.Fatal("must not call the Bot API")
	}
}

func TestSendMessage_NoDefaultSeveralChats(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001", "Alpha=-1002")
	db.Model(&database.Channel{}).Where("1 = 1").Update("is_default_post", false)
	tool, _ := newTool(t, db)
	_, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"})
	if err == nil || !strings.Contains(err.Error(), "chat is required") {
		t.Fatalf("want chat-required error, got %v", err)
	}
}

func TestSendMessage_DuplicateNames(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "ops=-1001", "ops=-1002")
	tool, _ := newTool(t, db)
	_, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x", "chat": "ops"})
	if err == nil || !strings.Contains(err.Error(), "matches 2 chats") {
		t.Fatalf("want ambiguity error, got %v", err)
	}
}

func TestSendMessage_RunChecks(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	db.Create(&database.AgentRun{RunID: "run-done", ScopeUUID: "scope-1", Status: "completed"})
	db.Create(&database.AgentRun{RunID: "run-prop", ScopeUUID: "scope-p", SourceKind: "proposal", Status: "running"})
	tool, bot := newTool(t, db)
	args := map[string]interface{}{"text": "x"}

	cases := []struct {
		name, run, scope, want string
	}{
		{"no run id", "", "scope-1", "needs the run id"},
		{"unknown run", "nope", "scope-1", "unknown run id"},
		{"other scope", "run-1", "scope-2", "does not belong"},
		{"finished", "run-done", "scope-1", "is completed"},
		{"proposal", "run-prop", "scope-p", "proposal chat"},
	}
	for _, c := range cases {
		_, err := tool.SendMessage(runCtx(c.run), c.scope, args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
	if len(bot.calls) != 0 {
		t.Fatal("must not call the Bot API")
	}
}

func TestSendMessage_TextValidation(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, _ := newTool(t, db)
	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "  "}); err == nil {
		t.Fatal("empty text must fail")
	}
	long := strings.Repeat("x", maxParts*MaxMessageChars+1)
	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": long}); err == nil || !strings.Contains(err.Error(), "the limit is") {
		t.Fatalf("too long text must fail, got %v", err)
	}
}

func TestSendMessage_SplitsLongText(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, bot := newTool(t, db)
	line := strings.Repeat("я", 99) + "\n" // multi-byte runes
	text := strings.Repeat(line, 60)       // 6000 runes
	out, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	if ids := out.(map[string]interface{})["message_ids"].([]int64); len(ids) != 2 {
		t.Fatalf("message_ids = %v", ids)
	}
	total := 0
	for _, c := range bot.calls {
		n := len([]rune(c["text"].(string)))
		if n > MaxMessageChars {
			t.Fatalf("part has %d runes", n)
		}
		total += n
	}
	if total < 5900 {
		t.Fatalf("lost text: %d runes sent", total)
	}
}

func TestSendMessage_QuotaPerRun(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, _ := newTool(t, db)
	for i := 0; i < maxSendsPerRun; i++ {
		if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"}); err == nil || !strings.Contains(err.Error(), "limit per run") {
		t.Fatalf("want quota error, got %v", err)
	}
}

func TestSendMessage_FailedSendIsFree(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, bot := newTool(t, db)
	bot.failNext = []string{`400{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`}
	_, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"})
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("want API error, got %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked: %v", err)
	}
	if !tool.hasQuota("run-1") || tool.counters["run-1"] != nil {
		t.Fatal("a failed send must not use quota")
	}
}

func TestSendMessage_RetriesOnce429(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool, bot := newTool(t, db)
	var waited time.Duration
	tool.sleep = func(_ context.Context, d time.Duration) error { waited = d; return nil }
	bot.failNext = []string{`429{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":3}}`}
	if _, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"}); err != nil {
		t.Fatal(err)
	}
	if waited != 3*time.Second || len(bot.calls) != 2 {
		t.Fatalf("waited %v, calls %d", waited, len(bot.calls))
	}
}

func TestSendMessage_TransportErrorHidesToken(t *testing.T) {
	db := newTestDB(t)
	seed(t, db, "oncall=-1001")
	tool := NewTool(db, log.New(io.Discard, "", 0))
	tool.baseURL = "http://127.0.0.1:1" // nothing listens here
	_, err := tool.SendMessage(runCtx("run-1"), "scope-1", map[string]interface{}{"text": "x"})
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestSplitText(t *testing.T) {
	if got := splitText("hello", 10); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %q", got)
	}
	got := splitText("aaaa bbbb cccc", 10)
	if len(got) != 2 || got[0] != "aaaa bbbb" || got[1] != "cccc" {
		t.Fatalf("got %q", got)
	}
	got = splitText(strings.Repeat("x", 25), 10) // no break point: hard cut
	if len(got) != 3 || got[2] != "xxxxx" {
		t.Fatalf("got %q", got)
	}
}

func TestTarget(t *testing.T) {
	if c, topic := target("-1001:42"); c != "-1001" || topic != 42 {
		t.Fatalf("got %s %d", c, topic)
	}
	if c, topic := target("@channel"); c != "@channel" || topic != 0 {
		t.Fatalf("got %s %d", c, topic)
	}
}
