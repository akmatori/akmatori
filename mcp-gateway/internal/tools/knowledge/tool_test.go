package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const testScope = "scope-1"

func newTestTool(t *testing.T) (*Tool, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&database.Memory{}, &database.MemoryUse{}, &database.Runbook{}, &database.Skill{}, &database.AgentRun{},
		&database.GeneralSettings{}, &database.ContextFile{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.Skill{Name: "linux-admin", Enabled: true})
	db.Create(&database.Skill{Name: "connected-tools", IsSystem: true, Enabled: true})
	// run-1 is request work (a Slack mention): the run kind that writes
	// memory. Alert investigations (run-alert) are refused: the API's
	// post-incident pass records their findings.
	db.Create(&database.AgentRun{RunID: "run-1", ScopeUUID: testScope, SourceKind: "slack_mention", Status: "running"})
	db.Create(&database.AgentRun{RunID: "run-alert", ScopeUUID: "inc-1", IncidentUUID: "inc-1", SourceKind: "alert", Status: "running"})
	return NewTool(db, log.New(io.Discard, "", 0)), db
}

func runCtx(runID string) context.Context {
	return mcp.WithRunID(context.Background(), runID)
}

func decode(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(v.(string)), &m); err != nil {
		t.Fatalf("decode %v: %v", v, err)
	}
	return m
}

func write(t *testing.T, tool *Tool, ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	out, err := tool.MemoryWrite(ctx, testScope, args)
	if err != nil {
		return nil, err
	}
	return decode(t, out), nil
}

func memArgs(name, body string) map[string]interface{} {
	return map[string]interface{}{"scope": "global", "name": name, "type": "fact", "description": "desc " + name, "body": body}
}

// ---- shared validation fixture ----

type fixtureCase struct {
	Name                       string            `json:"name"`
	Input                      map[string]string `json:"input"`
	API                        string            `json:"api"`
	Agent                      string            `json:"agent"`
	ExpectType                 string            `json:"expect_type"`
	ExpectDescriptionTruncated bool              `json:"expect_description_truncated"`
	ExpectDescriptionBytes     int               `json:"expect_description_bytes"`
}

func TestMemoryValidationFixture(t *testing.T) {
	b, err := os.ReadFile("../../../../tests/fixtures/memory_validation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []fixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	tool, _ := newTestTool(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			in := memoryInput{Scope: c.Input["scope"], Name: c.Input["name"], Type: c.Input["type"], Description: c.Input["description"], Body: c.Input["body"]}
			truncated, err := tool.prepareWrite(context.Background(), &in)
			if (err == nil) != (c.Agent == "ok") {
				t.Fatalf("agent outcome: err=%v, want %s", err, c.Agent)
			}
			if err != nil {
				return
			}
			if c.ExpectType != "" && in.Type != c.ExpectType {
				t.Errorf("type = %q, want %q", in.Type, c.ExpectType)
			}
			if truncated != c.ExpectDescriptionTruncated {
				t.Errorf("truncated = %v", truncated)
			}
			if c.ExpectDescriptionBytes > 0 && len(in.Description) != c.ExpectDescriptionBytes {
				t.Errorf("description bytes = %d, want %d", len(in.Description), c.ExpectDescriptionBytes)
			}
			if !json.Valid([]byte(`"` + in.Description + `"`)) {
				t.Error("description is not valid UTF-8 after the cut")
			}
		})
	}
}

// ---- writes ----

func TestMemoryWrite_CreateUpdateUnchanged(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")

	r, err := write(t, tool, ctx, memArgs("edge-nic", "v1"))
	if err != nil || r["action"] != "created" {
		t.Fatalf("create: %v %v", r, err)
	}
	var m database.Memory
	db.Where("name = ?", "edge-nic").Take(&m)
	if m.CreatedBy != "agent" || m.IncidentUUID != "" {
		t.Errorf("provenance: %+v", m)
	}
	first := m.UpdatedAt

	r, _ = write(t, tool, ctx, memArgs("edge-nic", "v1"))
	if r["action"] != "unchanged" {
		t.Errorf("same content: %v", r)
	}
	db.Where("name = ?", "edge-nic").Take(&m)
	if !m.UpdatedAt.Equal(first) {
		t.Error("unchanged write moved updated_at")
	}

	tool.now = func() time.Time { return time.Now().Add(time.Minute) }
	r, _ = write(t, tool, ctx, memArgs("edge-nic", "v2"))
	if r["action"] != "updated" {
		t.Errorf("update: %v", r)
	}
	db.Where("name = ?", "edge-nic").Take(&m)
	if m.Body != "v2" || m.CreatedBy != "agent" {
		t.Errorf("after update: %+v", m)
	}
}

func TestMemoryWrite_MissingOrNonStringBodyKeepsBody(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")
	if _, err := write(t, tool, ctx, memArgs("edge-nic", "keep me")); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"missing":    func(a map[string]interface{}) { delete(a, "body") },
		"null":       func(a map[string]interface{}) { a["body"] = nil },
		"non-string": func(a map[string]interface{}) { a["body"] = 42 },
	} {
		args := memArgs("edge-nic", "x")
		mutate(args)
		if _, err := write(t, tool, ctx, args); err == nil {
			t.Errorf("%s body: want an error", name)
		}
	}
	var m database.Memory
	db.Where("name = ?", "edge-nic").Take(&m)
	if m.Body != "keep me" {
		t.Errorf("body = %q, want it unchanged", m.Body)
	}
	// An explicit empty body is still a valid write.
	if _, err := write(t, tool, ctx, memArgs("edge-nic", "")); err != nil {
		t.Errorf("empty body: %v", err)
	}
}

func TestMemoryWrite_Protection(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")
	fileID := uint(7)
	db.Create(&database.Memory{Scope: "global", Name: "op-rule", Type: "feedback", Description: "d", Body: "operator", CreatedBy: "operator"})
	db.Create(&database.Memory{Scope: "global", Name: "renamed-summary", Type: "fact", Description: "d", Body: "summary", CreatedBy: "agent", SourceFileID: &fileID})
	db.Create(&database.Memory{Scope: "global", Name: "legacy", Type: "fact", Description: "d", Body: "old"})

	for _, name := range []string{"op-rule", "renamed-summary"} {
		r, err := write(t, tool, ctx, memArgs(name, "agent overwrite"))
		if err != nil || r["action"] != "refused_protected" {
			t.Errorf("%s write: %v %v", name, r, err)
		}
		out, err := tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": name})
		if err != nil || decode(t, out)["action"] != "refused_protected" {
			t.Errorf("%s delete: %v %v", name, out, err)
		}
		var m database.Memory
		db.Where("name = ?", name).Take(&m)
		if m.Body == "agent overwrite" {
			t.Errorf("%s was overwritten", name)
		}
	}
	// Legacy rows with empty created_by are agent-writable.
	r, _ := write(t, tool, ctx, memArgs("legacy", "new"))
	if r["action"] != "updated" {
		t.Errorf("legacy: %v", r)
	}
	out, _ := tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": "legacy"})
	if decode(t, out)["action"] != "deleted" {
		t.Errorf("legacy delete: %v", out)
	}
	out, _ = tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": "nope"})
	if decode(t, out)["action"] != "not_found" {
		t.Errorf("missing delete: %v", out)
	}
}

func TestMemoryWrite_ConcurrentOperatorEdit(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")
	db.Create(&database.Memory{Scope: "global", Name: "contested", Type: "fact", Description: "d", Body: "agent v1", CreatedBy: "agent"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// The operator takes ownership and edits.
		db.Model(&database.Memory{}).Where("name = ?", "contested").Updates(map[string]interface{}{"created_by": "operator", "body": "operator text"})
	}()
	go func() {
		defer wg.Done()
		_, _ = tool.MemoryWrite(ctx, testScope, memArgs("contested", "agent v2"))
	}()
	wg.Wait()
	// Whatever the order, a later agent write never replaces the operator text.
	_, _ = tool.MemoryWrite(ctx, testScope, memArgs("contested", "agent v3"))
	var m database.Memory
	db.Where("name = ?", "contested").Take(&m)
	if m.CreatedBy != "operator" || m.Body != "operator text" {
		t.Errorf("operator edit lost: %+v", m)
	}
}

func TestMemoryWrite_RunIdentity(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.AgentRun{RunID: "run-other", ScopeUUID: "other-scope", Status: "running"})
	db.Create(&database.AgentRun{RunID: "run-old", ScopeUUID: testScope, Status: "superseded"})
	db.Create(&database.AgentRun{RunID: "run-chat", ScopeUUID: testScope, SourceKind: "proposal", Status: "running"})

	for name, ctx := range map[string]context.Context{
		"missing header": context.Background(),
		"unknown run":    runCtx("nope"),
		"other scope":    runCtx("run-other"),
		"superseded run": runCtx("run-old"),
		"proposal chat":  runCtx("run-chat"),
	} {
		if _, err := tool.MemoryWrite(ctx, testScope, memArgs("x", "b")); err == nil {
			t.Errorf("%s: write accepted", name)
		}
		if _, err := tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": "x"}); err == nil {
			t.Errorf("%s: delete accepted", name)
		}
	}
	// Reads need no run id.
	if _, err := tool.MemoryList(context.Background(), testScope, map[string]interface{}{}); err != nil {
		t.Errorf("list without run id: %v", err)
	}
}

// TestMemoryWrite_AlertRunRefused: an alert investigation does not write
// memory (write and delete); the refusal names the post-incident pass. A
// proposal chat run is refused with its own message first.
func TestMemoryWrite_AlertRunRefused(t *testing.T) {
	tool, db := newTestTool(t)
	_, err := tool.MemoryWrite(runCtx("run-alert"), "inc-1", memArgs("alert-fact", "b"))
	if err == nil || !strings.Contains(err.Error(), "post-incident pass") {
		t.Fatalf("alert run write: %v", err)
	}
	_, err = tool.MemoryDelete(runCtx("run-alert"), "inc-1", map[string]interface{}{"scope": "global", "name": "x"})
	if err == nil || !strings.Contains(err.Error(), "post-incident pass") {
		t.Fatalf("alert run delete: %v", err)
	}
	var n int64
	db.Model(&database.Memory{}).Count(&n)
	if n != 0 {
		t.Errorf("alert run stored %d memories", n)
	}
	db.Create(&database.AgentRun{RunID: "run-chat-inc", ScopeUUID: "inc-2", IncidentUUID: "inc-2", SourceKind: "proposal", Status: "running"})
	_, err = tool.MemoryWrite(runCtx("run-chat-inc"), "inc-2", memArgs("x", "b"))
	if err == nil || !strings.Contains(err.Error(), "proposal") {
		t.Errorf("proposal refusal must come first: %v", err)
	}
	// Request runs (Slack, cron) still write.
	if _, err := write(t, tool, runCtx("run-1"), memArgs("slack-fact", "b")); err != nil {
		t.Errorf("slack run write: %v", err)
	}
}

// TestMemoryWrite_RedactsSecrets: a token in the description, body or note
// is replaced before the upsert and the response says how many.
func TestMemoryWrite_RedactsSecrets(t *testing.T) {
	tool, db := newTestTool(t)
	args := memArgs("api-host", "Call it with Authorization: Bearer abcdefghijklmnop and see the result.")
	args["evidence_note"] = "token=0123456789abcdef in the config"
	r, err := write(t, tool, runCtx("run-1"), args)
	if err != nil {
		t.Fatal(err)
	}
	if r["redacted"] != float64(2) {
		t.Errorf("redacted = %v, want 2", r["redacted"])
	}
	var m database.Memory
	db.Where("name = ?", "api-host").Take(&m)
	if strings.Contains(m.Body, "abcdefghijklmnop") || strings.Contains(m.EvidenceNote, "0123456789abcdef") {
		t.Errorf("secret stored: %+v", m)
	}
	if !strings.Contains(m.Body, redactedSecret) || !strings.Contains(m.EvidenceNote, redactedSecret) {
		t.Errorf("no marker: %+v", m)
	}
}

// TestMemoryWrite_QuotaChargedOnChange: an unchanged write and a refused
// write do not use a slot of the per-run budget; a failed delete neither.
func TestMemoryWrite_QuotaChargedOnChange(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")
	db.Create(&database.Memory{Scope: "global", Name: "op", Type: "fact", Description: "d", Body: "b", CreatedBy: "operator"})
	for i := 0; i < maxWritesPerRun; i++ {
		if _, err := write(t, tool, ctx, memArgs("same", "b")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if _, err := write(t, tool, ctx, memArgs("op", "b")); err != nil {
			t.Fatalf("refused write %d: %v", i, err)
		}
	}
	// One created + many unchanged and refused: the budget still has room.
	if _, err := write(t, tool, ctx, memArgs("another", "b")); err != nil {
		t.Errorf("unchanged and refused writes used the budget: %v", err)
	}
	for i := 0; i < maxDeletesPerRun+2; i++ {
		if _, err := tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": "op"}); err != nil {
			t.Fatalf("refused delete %d: %v", i, err)
		}
	}
	if _, err := tool.MemoryDelete(ctx, testScope, map[string]interface{}{"scope": "global", "name": "same"}); err != nil {
		t.Errorf("refused deletes used the budget: %v", err)
	}
}

func TestTimeArg_UnixSecondsAndMillis(t *testing.T) {
	want := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for name, v := range map[string]interface{}{
		"seconds number": float64(want.Unix()),
		"millis number":  float64(want.UnixMilli()),
		"seconds string": strconv.FormatInt(want.Unix(), 10),
		"millis string":  strconv.FormatInt(want.UnixMilli(), 10),
		"iso":            want.Format(time.RFC3339),
	} {
		got, ok := timeArg(map[string]interface{}{"since": v}, "since")
		if !ok || !got.Equal(want) {
			t.Errorf("%s: got %v ok=%v", name, got, ok)
		}
	}
}

func TestMemoryWrite_RequestRunHasNoIncident(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.AgentRun{RunID: "run-cron", ScopeUUID: "cron-scope", SourceKind: "cron", Status: "running"})
	if _, err := tool.MemoryWrite(runCtx("run-cron"), "cron-scope", memArgs("cron-fact", "b")); err != nil {
		t.Fatal(err)
	}
	var m database.Memory
	db.Where("name = ?", "cron-fact").Take(&m)
	if m.IncidentUUID != "" {
		t.Errorf("incident_uuid = %q for a cron run", m.IncidentUUID)
	}
}

func TestMemoryWrite_PerRunCaps(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.AgentRun{RunID: "run-2", ScopeUUID: testScope, Status: "running"})
	for i := 0; i < maxWritesPerRun; i++ {
		if _, err := write(t, tool, runCtx("run-1"), memArgs("m"+string(rune('a'+i)), "b")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if _, err := write(t, tool, runCtx("run-1"), memArgs("over", "b")); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("cap not enforced: %v", err)
	}
	// A follow-up turn on the same scope is a new run with its own cap.
	if _, err := write(t, tool, runCtx("run-2"), memArgs("next-turn", "b")); err != nil {
		t.Errorf("second run blocked by the first run's cap: %v", err)
	}
	// Only a delete that removed a row uses a slot.
	for i := 0; i < maxDeletesPerRun; i++ {
		name := "zz" + string(rune('a'+i))
		if _, err := write(t, tool, runCtx("run-2"), memArgs(name, "b")); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if _, err := tool.MemoryDelete(runCtx("run-2"), testScope, map[string]interface{}{"scope": "global", "name": name}); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if _, err := tool.MemoryDelete(runCtx("run-2"), testScope, map[string]interface{}{"scope": "global", "name": "next-turn"}); err == nil {
		t.Error("delete cap not enforced")
	}
}

func TestMemoryWrite_Gate(t *testing.T) {
	off, on := false, true
	cases := map[string]func(db *gorm.DB){
		"missing row": func(db *gorm.DB) {},
		"nil":         func(db *gorm.DB) { db.Create(&database.GeneralSettings{}) },
		"true":        func(db *gorm.DB) { db.Create(&database.GeneralSettings{MemoryGenerationEnabled: &on}) },
		"read error":  func(db *gorm.DB) { _ = db.Migrator().DropTable(&database.GeneralSettings{}) },
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			tool, db := newTestTool(t)
			seed(db)
			if _, err := write(t, tool, runCtx("run-1"), memArgs("g", "b")); err != nil {
				t.Errorf("gate must be open: %v", err)
			}
		})
	}
	t.Run("false", func(t *testing.T) {
		tool, db := newTestTool(t)
		db.Create(&database.GeneralSettings{MemoryGenerationEnabled: &off})
		if _, err := write(t, tool, runCtx("run-1"), memArgs("g", "b")); err == nil || !strings.Contains(err.Error(), "memory generation is off") {
			t.Errorf("write with the gate off: %v", err)
		}
		if _, err := tool.MemoryDelete(runCtx("run-1"), testScope, map[string]interface{}{"scope": "global", "name": "g"}); err == nil {
			t.Error("delete with the gate off accepted")
		}
	})
}

// ---- reads ----

func seedCorpus(t *testing.T, db *gorm.DB) {
	t.Helper()
	c := loadCorpus(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, r := range c.Runbooks {
		db.Create(&database.Runbook{ID: r.ID, Title: r.Title, Content: r.Content, UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	for i, m := range c.Memories {
		db.Create(&database.Memory{ID: m.ID, Scope: m.Scope, Type: m.Type, Name: m.Name, Description: m.Description, Body: m.Body,
			CreatedBy: "agent", UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
}

func TestSearchTools(t *testing.T) {
	tool, db := newTestTool(t)
	seedCorpus(t, db)
	ctx := context.Background()

	out, err := tool.RunbooksSearch(ctx, testScope, map[string]interface{}{"query": "502 Bad Gateway on nginx-edge-03"})
	if err != nil {
		t.Fatal(err)
	}
	res := decode(t, out)["results"].([]interface{})
	if len(res) == 0 || res[0].(map[string]interface{})["id"].(float64) != 1 {
		t.Errorf("runbooks.search top = %v", res)
	}
	out, _ = tool.RunbooksGet(ctx, testScope, map[string]interface{}{"id": float64(1)})
	if !strings.Contains(decode(t, out)["content"].(string), "php-fpm") {
		t.Error("runbooks.get content")
	}

	out, _ = tool.MemorySearch(ctx, testScope, map[string]interface{}{"query": "OOMKilled billing-worker", "scopes": []interface{}{"k8s"}})
	res = decode(t, out)["results"].([]interface{})
	if len(res) != 1 || res[0].(map[string]interface{})["name"] != "billing-worker-oom" {
		t.Errorf("scoped memory.search = %v", res)
	}
	out, _ = tool.MemorySearch(ctx, testScope, map[string]interface{}{"query": "OOMKilled billing-worker", "scopes": []interface{}{"global"}})
	for _, r := range decode(t, out)["results"].([]interface{}) {
		if r.(map[string]interface{})["scope"] != "global" {
			t.Errorf("scope filter leaked: %v", r)
		}
	}
	out, _ = tool.MemorySearch(ctx, testScope, map[string]interface{}{"query": "restart", "type": "feedback"})
	for _, r := range decode(t, out)["results"].([]interface{}) {
		if r.(map[string]interface{})["type"] != "feedback" {
			t.Errorf("type filter leaked: %v", r)
		}
	}
	if _, err := tool.MemorySearch(ctx, testScope, map[string]interface{}{}); err == nil {
		t.Error("search without query accepted")
	}
}

func TestMemoryList_PagingAndSince(t *testing.T) {
	tool, db := newTestTool(t)
	seedCorpus(t, db)
	ctx := context.Background()

	seen := map[float64]bool{}
	cursor := ""
	pages := 0
	var prev time.Time
	for {
		args := map[string]interface{}{"limit": float64(7), "scopes": []interface{}{"global"}}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out, err := tool.MemoryList(ctx, testScope, args)
		if err != nil {
			t.Fatal(err)
		}
		page := decode(t, out)
		for _, it := range page["items"].([]interface{}) {
			m := it.(map[string]interface{})
			id := m["id"].(float64)
			if seen[id] {
				t.Fatalf("id %v listed twice", id)
			}
			seen[id] = true
			ts, _ := time.Parse(time.RFC3339Nano, m["updated_at"].(string))
			if !prev.IsZero() && ts.After(prev) {
				t.Errorf("not newest first")
			}
			prev = ts
			if _, ok := m["body"]; ok {
				t.Error("list must not return bodies")
			}
		}
		pages++
		cursor, _ = page["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != 28 || pages != 4 {
		t.Errorf("listed %d global memories in %d pages, want 28 in 4", len(seen), pages)
	}

	since := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) // memories with i >= 24
	out, _ := tool.MemoryList(ctx, testScope, map[string]interface{}{"since": since.Format(time.RFC3339)})
	if n := decode(t, out)["count"].(float64); n != 6 {
		t.Errorf("since: %v items, want 6", n)
	}
	if _, err := tool.MemoryList(ctx, testScope, map[string]interface{}{"cursor": "!!"}); err == nil {
		t.Error("bad cursor accepted")
	}
}

func TestMemoryGet_References(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.Memory{Scope: "global", Name: "target", Type: "fact", Description: "d", Body: "t"})
	db.Create(&database.Memory{Scope: "linux-admin", Name: "src", Type: "fact", Description: "d",
		Body: "See [[target]], [[global/target]], [[runbook.pdf]], [[missing.txt]], [[Bad Ref]] and [[ghost]].", CreatedBy: "operator"})
	db.Create(&database.ContextFile{Filename: "runbook.pdf"})

	out, err := tool.MemoryGet(context.Background(), testScope, map[string]interface{}{"scope": "linux-admin", "name": "src"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["protected"] != true {
		t.Error("operator memory must show protected")
	}
	refs := m["references"].([]interface{})
	want := []struct {
		kind   string
		exists bool
	}{{"memory", true}, {"memory", true}, {"file", true}, {"file", false}, {"invalid", false}, {"memory", false}}
	if len(refs) != len(want) {
		t.Fatalf("refs = %v", refs)
	}
	for i, w := range want {
		r := refs[i].(map[string]interface{})
		if r["kind"] != w.kind || r["exists"] != w.exists {
			t.Errorf("ref %d = %v, want %s exists=%v", i, r, w.kind, w.exists)
		}
	}
	if refs[0].(map[string]interface{})["scope"] != "global" {
		t.Error("bare [[slug]] must fall back to global")
	}
	if _, ok := refs[0].(map[string]interface{})["path"]; ok {
		t.Error("memory refs must not carry a file path")
	}
}

// TestMemoryUsage: memory.get records a use with the run and raises
// use_count; a search hit records a row but moves last_used_at only;
// memory.list returns the counters and takes unused_since; a delete removes
// the uses.
func TestMemoryUsage(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.Memory{Scope: "global", Name: "edge-nic", Type: "fact", Description: "edge nic flaps", Body: "NETDEV WATCHDOG on eth1"})
	db.Create(&database.Memory{Scope: "global", Name: "quiet", Type: "fact", Description: "nothing to do with it", Body: "zzz"})
	var m database.Memory
	db.Where("name = ?", "edge-nic").Take(&m)

	if _, err := tool.MemoryGet(runCtx("run-alert"), "inc-1", map[string]interface{}{"id": float64(m.ID)}); err != nil {
		t.Fatal(err)
	}
	var uses []database.MemoryUse
	db.Find(&uses)
	if len(uses) != 1 || uses[0].Kind != "get" || uses[0].RunID != "run-alert" || uses[0].IncidentUUID != "inc-1" || uses[0].MemoryID != m.ID {
		t.Fatalf("uses after get: %+v", uses)
	}
	db.Where("id = ?", m.ID).Take(&m)
	if m.UseCount != 1 || m.LastUsedAt == nil {
		t.Fatalf("after get: count %d last %v", m.UseCount, m.LastUsedAt)
	}

	if _, err := tool.MemorySearch(context.Background(), testScope, map[string]interface{}{"query": "edge nic watchdog"}); err != nil {
		t.Fatal(err)
	}
	db.Order("id").Find(&uses)
	if len(uses) < 2 || uses[1].Kind != "search" || uses[1].RunID != "" {
		t.Fatalf("uses after search: %+v", uses)
	}
	db.Where("id = ?", m.ID).Take(&m)
	if m.UseCount != 1 {
		t.Errorf("search raised use_count: %d", m.UseCount)
	}

	out, err := tool.MemoryList(context.Background(), testScope, map[string]interface{}{"unused_since": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	r := decode(t, out)
	items := r["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["name"] != "quiet" {
		t.Errorf("unused_since: %v", items)
	}
	out, _ = tool.MemoryList(context.Background(), testScope, map[string]interface{}{})
	for _, it := range decode(t, out)["items"].([]interface{}) {
		item := it.(map[string]interface{})
		if item["name"] == "edge-nic" && item["use_count"] != float64(1) {
			t.Errorf("list use_count: %v", item)
		}
	}

	db.Model(&database.Memory{}).Where("id = ?", m.ID).Update("created_by", "agent")
	if _, err := tool.MemoryDelete(runCtx("run-1"), testScope, map[string]interface{}{"scope": "global", "name": "edge-nic"}); err != nil {
		t.Fatal(err)
	}
	var left int64
	db.Model(&database.MemoryUse{}).Where("memory_id = ?", m.ID).Count(&left)
	if left != 0 {
		t.Errorf("uses left after delete: %d", left)
	}
}
