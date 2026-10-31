package services

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func TestMemoryService_UpdateMemory_SetBodyClears(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	m, err := svc.CreateMemory(validMemory("clearable"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Empty body without setBody keeps the old body (partial update).
	kept, err := svc.UpdateMemory(m.ID, &database.Memory{Description: "new description"}, false)
	if err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if kept.Body != "host body" {
		t.Errorf("partial update changed body to %q", kept.Body)
	}

	cleared, err := svc.UpdateMemory(m.ID, &database.Memory{}, true)
	if err != nil {
		t.Fatalf("clear update: %v", err)
	}
	if cleared.Body != "" || cleared.Description != "new description" {
		t.Errorf("clear update = %+v", cleared)
	}
}

func TestMemoryService_ListMemories_IncidentFilter(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	for name, uuid := range map[string]string{"from-a": "inc-a", "also-a": "inc-a", "from-b": "inc-b"} {
		m := validMemory(name)
		m.IncidentUUID = uuid
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	got, err := svc.ListMemories(MemoryFilter{IncidentUUID: "inc-a"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d memories for inc-a, want 2: %+v", len(got), got)
	}
}

func TestMemoryService_CreateWithUniqueName(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	first, err := svc.CreateWithUniqueName(validMemory("redis-port"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.CreateWithUniqueName(validMemory("redis-port"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	third, err := svc.CreateWithUniqueName(validMemory("redis-port"))
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if first.Name != "redis-port" || second.Name != "redis-port-2" || third.Name != "redis-port-3" {
		t.Errorf("names = %q, %q, %q", first.Name, second.Name, third.Name)
	}

	// Another scope does not collide.
	other := validMemory("redis-port")
	other.Scope = "redis"
	if got, err := svc.CreateWithUniqueName(other); err != nil || got.Name != "redis-port" {
		t.Errorf("other scope = %+v, %v", got, err)
	}

	// A name at the cap still gets its suffix inside the limit.
	long := validMemory(strings.Repeat("a", MemoryNameMaxLen))
	if _, err := svc.CreateWithUniqueName(long); err != nil {
		t.Fatalf("long first: %v", err)
	}
	got, err := svc.CreateWithUniqueName(validMemory(strings.Repeat("a", MemoryNameMaxLen)))
	if err != nil {
		t.Fatalf("long second: %v", err)
	}
	if len(got.Name) > MemoryNameMaxLen || !strings.HasSuffix(got.Name, "-2") {
		t.Errorf("long suffixed name = %q (%d)", got.Name, len(got.Name))
	}
}

func TestMemoryService_ExportZip(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	g := validMemory("dns-resolvers")
	g.Body = "See [[edge-topology.pdf]]."
	if _, err := svc.CreateMemory(g); err != nil {
		t.Fatalf("create global: %v", err)
	}
	r := validMemory("redis-port")
	r.Scope = "redis"
	if _, err := svc.CreateMemory(r); err != nil {
		t.Fatalf("create redis: %v", err)
	}

	var buf bytes.Buffer
	if err := svc.ExportZip(&buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(data)
	}
	if len(files) != 2 {
		t.Fatalf("zip has %d files: %v", len(files), files)
	}
	if body := files["global/dns-resolvers.md"]; !strings.Contains(body, "name: dns-resolvers") || !strings.Contains(body, "[[edge-topology.pdf]]") {
		t.Errorf("global/dns-resolvers.md = %q", body)
	}
	if _, ok := files["redis/redis-port.md"]; !ok {
		t.Errorf("missing redis/redis-port.md: %v", files)
	}
}

func TestMemoryDrafter_UsesLLMDraft(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(ctx context.Context) (string, error) {
		return "```json\n" + `{"name": "Redis Prod Port", "description": "Redis prod listens on 16379, not 6379", "body": "Redis prod cluster uses port **16379**.", "type": "host"}` + "\n```", nil
	}
	draft := NewMemoryDrafter(caller).Draft(context.Background(), "redis prod is on 16379 not 6379")

	want := MemoryDraft{Name: "redis-prod-port", Description: "Redis prod listens on 16379, not 6379", Body: "Redis prod cluster uses port **16379**.", Type: MemoryTypeFact, FromLLM: true}
	if draft != want {
		t.Errorf("draft = %+v, want %+v", draft, want)
	}
	if caller.lastUser != "redis prod is on 16379 not 6379" {
		t.Errorf("user prompt = %q", caller.lastUser)
	}
}

func TestMemoryDrafter_FallsBack(t *testing.T) {
	text := "Always page the DBA before failover\nThey own the replica promotion."
	want := MemoryDraft{
		Name:        "always-page-the-dba-before-failover",
		Description: "Always page the DBA before failover",
		Body:        text,
		Type:        MemoryTypeFeedback,
	}

	cases := map[string]func(ctx context.Context) (string, error){
		"llm error":    func(ctx context.Context) (string, error) { return "", errors.New("boom") },
		"invalid json": func(ctx context.Context) (string, error) { return "sure! here is a memory", nil },
		"empty summary": func(ctx context.Context) (string, error) {
			return `{"name":"x","description":"  ","body":"b","type":"host"}`, nil
		},
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			caller := setupClassifierTest(t)
			caller.respond = respond
			if got := NewMemoryDrafter(caller).Draft(context.Background(), text); got != want {
				t.Errorf("draft = %+v, want %+v", got, want)
			}
		})
	}

	t.Run("no caller", func(t *testing.T) {
		if got := NewMemoryDrafter(nil).Draft(context.Background(), text); got != want {
			t.Errorf("draft = %+v, want %+v", got, want)
		}
		var nilDrafter *MemoryDrafter
		if got := nilDrafter.Draft(context.Background(), text); got != want {
			t.Errorf("nil drafter draft = %+v, want %+v", got, want)
		}
	})
}

func TestMemoryDrafter_UnknownTypeBecomesFact(t *testing.T) {
	for llmType, want := range map[string]string{"opinion": MemoryTypeFact, "tool_quirk": MemoryTypeFact, "feedback": MemoryTypeFeedback} {
		draft, err := parseMemoryDraft(`{"name":"n","description":"d","body":"b","type":"` + llmType + `"}`)
		if err != nil {
			t.Fatalf("parse %q: %v", llmType, err)
		}
		if draft.Type != want {
			t.Errorf("type %q drafted as %q, want %q", llmType, draft.Type, want)
		}
	}
}

func TestDraftDescription_CapsBytesOnRuneBoundary(t *testing.T) {
	s := strings.Repeat("ж", MemoryDescriptionMaxLen) // 2 bytes per rune
	got := draftDescription(s)
	if len(got) > MemoryDescriptionMaxLen {
		t.Errorf("len = %d, want ≤ %d", len(got), MemoryDescriptionMaxLen)
	}
	if !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "жж") {
		t.Errorf("got %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("split a rune: %q", got)
	}
}

func TestMemoryDrafter_PromptGivesDateAndFeedbackShape(t *testing.T) {
	caller := setupClassifierTest(t)
	caller.respond = func(context.Context) (string, error) {
		return `{"name": "x", "description": "x", "body": "x", "type": "fact"}`, nil
	}
	NewMemoryDrafter(caller).Draft(context.Background(), "redis moved to 16379 yesterday")
	if !strings.Contains(caller.lastSystem, "Today is "+time.Now().UTC().Format("2006-01-02")) {
		t.Errorf("system prompt has no date:\n%s", caller.lastSystem)
	}
	for _, want := range []string{"**Why:**", "**How to apply:**", "never invent a reason"} {
		if !strings.Contains(caller.lastSystem, want) {
			t.Errorf("system prompt misses %q", want)
		}
	}
}
