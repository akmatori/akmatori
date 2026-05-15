package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupMemoryServiceTest(t *testing.T) *MemoryService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Force a single underlying connection so concurrent tests all see the
	// same in-memory database. Without this, GORM's pool may dispatch
	// goroutines to different SQLite connections — each gets its own
	// fresh `:memory:` DB and AutoMigrate's table is invisible to the others.
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&database.Memory{}); err != nil {
		t.Fatalf("migrate memories: %v", err)
	}
	database.DB = db
	return NewMemoryService(t.TempDir())
}

func validMemory(name string) *database.Memory {
	return &database.Memory{
		Scope:       MemoryScopeGlobal,
		Type:        MemoryTypeHost,
		Name:        name,
		Description: "host fact",
		Body:        "host body",
		CreatedBy:   MemoryCreatedByOperator,
	}
}

func TestMemoryService_Create_Validation(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	cases := []struct {
		name string
		mut  func(*database.Memory)
		want string
	}{
		{
			name: "empty scope",
			mut:  func(m *database.Memory) { m.Scope = "  " },
			want: "scope cannot be empty",
		},
		{
			name: "invalid type",
			mut:  func(m *database.Memory) { m.Type = "bogus" },
			want: "invalid memory type",
		},
		{
			name: "empty name",
			mut:  func(m *database.Memory) { m.Name = " " },
			want: "name cannot be empty",
		},
		{
			name: "non-slug name",
			mut:  func(m *database.Memory) { m.Name = "Has Spaces" },
			want: "slug-safe",
		},
		{
			name: "uppercase name",
			mut:  func(m *database.Memory) { m.Name = "UpperCase" },
			want: "slug-safe",
		},
		{
			name: "empty description",
			mut:  func(m *database.Memory) { m.Description = "" },
			want: "description cannot be empty",
		},
		{
			name: "description too long",
			mut:  func(m *database.Memory) { m.Description = strings.Repeat("x", MemoryDescriptionMaxLen+1) },
			want: "description exceeds",
		},
		{
			name: "body too large",
			mut:  func(m *database.Memory) { m.Body = strings.Repeat("x", MemoryBodyMaxBytes+1) },
			want: "body exceeds",
		},
		{
			name: "invalid created_by",
			mut:  func(m *database.Memory) { m.CreatedBy = "bot" },
			want: "created_by",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMemory("base")
			tc.mut(m)
			_, err := svc.CreateMemory(m)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestMemoryService_Create_PersistsAndReads(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	m, err := svc.CreateMemory(validMemory("prod-db-data-dir"))
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if m.ID == 0 {
		t.Fatalf("expected ID assigned")
	}

	got, err := svc.GetMemory(m.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.Name != "prod-db-data-dir" || got.Scope != MemoryScopeGlobal {
		t.Fatalf("got = %+v", got)
	}

	_, err = svc.GetMemory(99999)
	if !IsMemoryNotFoundErr(err) {
		t.Fatalf("expected memory-not-found, got %v", err)
	}
}

func TestMemoryService_UniqueScopeName(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	if _, err := svc.CreateMemory(validMemory("dup-name")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateMemory(validMemory("dup-name"))
	if err == nil {
		t.Fatalf("expected duplicate error on (scope, name)")
	}

	// Same name, different scope is allowed.
	other := validMemory("dup-name")
	other.Scope = "postgres-skill"
	if _, err := svc.CreateMemory(other); err != nil {
		t.Fatalf("expected scope-segregated insert to succeed: %v", err)
	}
}

func TestMemoryService_UpsertByName_Idempotent(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	first := validMemory("recurring")
	first.Description = "v1 desc"
	first.Body = "v1 body"
	created, err := svc.UpsertByName(first)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	originalID := created.ID

	second := validMemory("recurring")
	second.Description = "v2 desc"
	second.Body = "v2 body"
	second.IncidentUUID = "incident-42"
	second.CreatedBy = MemoryCreatedByAgent
	updated, err := svc.UpsertByName(second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if updated.ID != originalID {
		t.Fatalf("upsert mutated ID: got %d, want %d", updated.ID, originalID)
	}
	if updated.Description != "v2 desc" || updated.Body != "v2 body" {
		t.Fatalf("upsert didn't update fields: %+v", updated)
	}
	if updated.IncidentUUID != "incident-42" {
		t.Fatalf("upsert didn't update incident_uuid: %+v", updated)
	}
	// created_by is intentionally sticky on conflict: the first row was
	// authored by the operator (validMemory default) and a follow-up
	// agent-flavored upsert must not silently flip authorship.
	if updated.CreatedBy != MemoryCreatedByOperator {
		t.Fatalf("upsert overwrote operator authorship on conflict: %+v", updated)
	}

	all, err := svc.ListMemories("", "")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 row after idempotent upsert, got %d", len(all))
	}
}

func TestMemoryService_AtCapNameSyncsBelowFilesystemLimit(t *testing.T) {
	// Regression: previously the name cap (255) ignored the on-disk
	// filename overhead (`<id>-…<.md>`), so a name at the cap could push
	// the filename past NAME_MAX (255 bytes on most filesystems). The DB
	// row would land but SyncMemoryFiles would fail with ENAMETOOLONG,
	// leaving the API to return a sync-failed error and every subsequent
	// sync to keep failing on the same path until the row was edited.
	svc := setupMemoryServiceTest(t)

	// Name exactly at the new cap. Slug-safe (all 'a').
	atCapName := strings.Repeat("a", MemoryNameMaxLen)

	m := validMemory(atCapName)
	if _, err := svc.CreateMemory(m); err != nil {
		t.Fatalf("at-cap name should sync cleanly, got: %v", err)
	}

	// The on-disk filename must fit under the typical NAME_MAX of 255 bytes.
	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	entries, err := os.ReadDir(scopeDir)
	if err != nil {
		t.Fatalf("read scope dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == manifestFile {
			continue
		}
		if len(e.Name()) > 255 {
			t.Errorf("filename %d bytes exceeds NAME_MAX (255): %q", len(e.Name()), e.Name())
		}
	}
}

func TestMemoryService_OverCapNameIsRejected(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	overCap := strings.Repeat("a", MemoryNameMaxLen+1)
	m := validMemory(overCap)
	_, err := svc.CreateMemory(m)
	if err == nil {
		t.Fatalf("over-cap name should be rejected by validation")
	}
	if !strings.Contains(err.Error(), "slug-safe") {
		t.Errorf("expected slug-safe error message, got: %v", err)
	}
}

func TestMemoryService_UpsertByName_AtomicUnderConcurrency(t *testing.T) {
	// Regression: the previous lookup-then-create pattern raced under
	// concurrent calls (e.g. a Slack retry + the original event). Both
	// workers could miss the SELECT and then one Create would fail with
	// "UNIQUE constraint failed", dropping the later upsert on a path
	// that's contractually idempotent.
	//
	// SQLite's single-writer lock serializes statements, so this is more
	// a "no panics, all callers succeed, exactly one row" smoke test than
	// a true thread-race test — but it does exercise the OnConflict path
	// for every concurrent call and verifies callers never see the
	// unique-constraint error from the old code path.
	svc := setupMemoryServiceTest(t)

	const goroutines = 16
	results := make(chan error, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			m := validMemory("concurrent-fact")
			m.Description = fmt.Sprintf("description from caller %d", i)
			m.Body = fmt.Sprintf("body from caller %d", i)
			m.IncidentUUID = "inc-shared"
			m.CreatedBy = MemoryCreatedByOperator
			_, err := svc.UpsertByName(m)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Fatalf("concurrent upsert returned error (would be a unique-constraint race): %v", err)
		}
	}

	all, err := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 row after %d concurrent upserts, got %d", goroutines, len(all))
	}
	if all[0].Name != "concurrent-fact" {
		t.Errorf("got %+v", all[0])
	}
}

func TestMemoryService_UpsertByName_RejectsInvalid(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	bad := validMemory("bad")
	bad.Type = "nope"
	if _, err := svc.UpsertByName(bad); err == nil {
		t.Fatalf("expected validation error from UpsertByName")
	}
}

func TestMemoryService_Update(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	m, err := svc.CreateMemory(validMemory("orig"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := svc.UpdateMemory(m.ID, &database.Memory{
		Type:        MemoryTypeFeedback,
		Description: "updated desc",
		Body:        "updated body",
		CreatedBy:   MemoryCreatedByAgent,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Type != MemoryTypeFeedback || updated.Description != "updated desc" {
		t.Fatalf("update didn't apply: %+v", updated)
	}
	if updated.Name != "orig" {
		t.Fatalf("update changed name: got %q", updated.Name)
	}

	_, err = svc.UpdateMemory(99999, &database.Memory{Description: "x", Body: "y"})
	if !IsMemoryNotFoundErr(err) {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestMemoryService_Update_PartialPreservesOmittedFields(t *testing.T) {
	// Regression: PUT /api/memories/{id} with only {"type":"feedback"} used
	// to clobber Description and Body with "" and fail validation. Empty
	// fields must mean "leave unchanged" for ALL fields, not just scope/name/type.
	svc := setupMemoryServiceTest(t)

	original := validMemory("partial")
	original.Description = "original description"
	original.Body = "original body"
	created, err := svc.CreateMemory(original)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Send only Type — the rest of the request is empty.
	updated, err := svc.UpdateMemory(created.ID, &database.Memory{
		Type: MemoryTypeFeedback,
	})
	if err != nil {
		t.Fatalf("partial update should succeed, got: %v", err)
	}
	if updated.Type != MemoryTypeFeedback {
		t.Errorf("type not applied: %q", updated.Type)
	}
	if updated.Description != "original description" {
		t.Errorf("description clobbered to %q (want unchanged)", updated.Description)
	}
	if updated.Body != "original body" {
		t.Errorf("body clobbered to %q (want unchanged)", updated.Body)
	}
}

func TestMemoryService_Delete(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	m, err := svc.CreateMemory(validMemory("doomed"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.DeleteMemory(m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetMemory(m.ID); !IsMemoryNotFoundErr(err) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
	if err := svc.DeleteMemory(m.ID); !IsMemoryNotFoundErr(err) {
		t.Fatalf("second delete should be not-found, got %v", err)
	}
}

func TestMemoryService_ListByScope(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	g1 := validMemory("g1")
	g2 := validMemory("g2")
	s1 := validMemory("s1")
	s1.Scope = "redis"

	for _, m := range []*database.Memory{g1, g2, s1} {
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("seed %s: %v", m.Name, err)
		}
	}

	global, err := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("list global: %v", err)
	}
	if len(global) != 2 {
		t.Fatalf("global count = %d, want 2", len(global))
	}

	redis, err := svc.ListMemoriesByScope("redis")
	if err != nil {
		t.Fatalf("list redis: %v", err)
	}
	if len(redis) != 1 || redis[0].Name != "s1" {
		t.Fatalf("redis listing = %+v", redis)
	}

	scopes, err := svc.ListAllScopes()
	if err != nil {
		t.Fatalf("list scopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %v", scopes)
	}
}

func TestMemoryService_ListMemories_Filters(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	host := validMemory("host-a")
	host.Type = MemoryTypeHost
	pat := validMemory("pat-a")
	pat.Type = MemoryTypeIncidentPattern
	feedback := validMemory("fb-a")
	feedback.Type = MemoryTypeFeedback
	feedback.Scope = "redis"

	for _, m := range []*database.Memory{host, pat, feedback} {
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("seed %s: %v", m.Name, err)
		}
	}

	hosts, _ := svc.ListMemories("", MemoryTypeHost)
	if len(hosts) != 1 || hosts[0].Name != "host-a" {
		t.Fatalf("host filter = %+v", hosts)
	}

	redisOnly, _ := svc.ListMemories("redis", "")
	if len(redisOnly) != 1 || redisOnly[0].Name != "fb-a" {
		t.Fatalf("scope filter = %+v", redisOnly)
	}

	combo, _ := svc.ListMemories("redis", MemoryTypeHost)
	if len(combo) != 0 {
		t.Fatalf("scope+type combo should be empty, got %+v", combo)
	}
}

func TestMemoryService_CountByIncidentUUID(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	a := validMemory("a")
	a.IncidentUUID = "x"
	a.CreatedBy = MemoryCreatedByAgent
	b := validMemory("b")
	b.IncidentUUID = "x"
	b.CreatedBy = MemoryCreatedByOperator
	c := validMemory("c")
	c.IncidentUUID = "y"
	c.CreatedBy = MemoryCreatedByAgent

	for _, m := range []*database.Memory{a, b, c} {
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := svc.CountByIncidentUUID("x", "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 2 {
		t.Fatalf("count(x, any) = %d, want 2", got)
	}

	gotAgent, err := svc.CountByIncidentUUID("x", MemoryCreatedByAgent)
	if err != nil {
		t.Fatalf("count agent: %v", err)
	}
	if gotAgent != 1 {
		t.Fatalf("count(x, agent) = %d, want 1 (operator-authored row excluded)", gotAgent)
	}

	gotOperator, err := svc.CountByIncidentUUID("x", MemoryCreatedByOperator)
	if err != nil {
		t.Fatalf("count operator: %v", err)
	}
	if gotOperator != 1 {
		t.Fatalf("count(x, operator) = %d, want 1", gotOperator)
	}

	gotEmpty, err := svc.CountByIncidentUUID("missing", "")
	if err != nil || gotEmpty != 0 {
		t.Fatalf("count(missing) = %d, %v", gotEmpty, err)
	}
}

func TestTruncateMemoryBody(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		check  func(t *testing.T, got string)
	}{
		{
			name: "short ASCII unchanged",
			in:   "hello world",
			check: func(t *testing.T, got string) {
				if got != "hello world" {
					t.Errorf("got %q, want unchanged", got)
				}
			},
		},
		{
			name: "long ASCII trimmed to cap",
			in:   strings.Repeat("a", MemoryBodyMaxBytes+100),
			check: func(t *testing.T, got string) {
				if len(got) > MemoryBodyMaxBytes {
					t.Errorf("got %d bytes, want ≤ %d", len(got), MemoryBodyMaxBytes)
				}
				if !strings.HasPrefix(got, "aaaa") {
					t.Errorf("expected prefix preserved")
				}
			},
		},
		{
			name: "multibyte trimmed at rune boundary",
			// "日" is 3 bytes; build a string that lands the cap in the
			// middle of a rune so naive slicing would split it.
			in: strings.Repeat("日", (MemoryBodyMaxBytes/3)+10),
			check: func(t *testing.T, got string) {
				if len(got) > MemoryBodyMaxBytes {
					t.Errorf("got %d bytes, want ≤ %d", len(got), MemoryBodyMaxBytes)
				}
				// The result MUST be valid UTF-8 — i.e., the byte length
				// must be a multiple of 3 (every char is 3 bytes).
				if len(got)%3 != 0 {
					t.Errorf("got %d bytes — not on a UTF-8 boundary for 3-byte runes", len(got))
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, TruncateMemoryBody(tc.in))
		})
	}
}

func TestValidMemoryType(t *testing.T) {
	for _, ok := range AllMemoryTypes() {
		if !ValidMemoryType(ok) {
			t.Errorf("expected %q to be valid", ok)
		}
	}
	for _, bad := range []string{"", "Host", "incident-pattern", "feedback "} {
		if ValidMemoryType(bad) {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}

func TestSlugifyMemoryName(t *testing.T) {
	cases := map[string]string{
		"Postgres Data Dir":     "postgres-data-dir",
		"  Big-Important Note ": "big-important-note",
		"":                      "memory",
		"🔥🔥":                    "memory",
	}
	for in, want := range cases {
		if got := SlugifyMemoryName(in); got != want {
			t.Errorf("SlugifyMemoryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrMemoryNotFoundIs(t *testing.T) {
	wrapped := errors.New("wrapper")
	if IsMemoryNotFoundErr(wrapped) {
		t.Fatal("unrelated error should not match")
	}
	if !IsMemoryNotFoundErr(errMemoryNotFound) {
		t.Fatal("sentinel should match itself")
	}
}
