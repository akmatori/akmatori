package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestParseMemoryRefs_Classifies(t *testing.T) {
	body := "See [[redis-port]], [[ postgres/data-dir ]] and [[edge-topology.pdf]].\n" +
		"Bad: [[../../etc/passwd]] [[https://evil.example/x]] [[Upper-Case]] [[a/b/c]] [[]].\n" +
		"Repeat: [[redis-port]]"
	got := ParseMemoryRefs(body, "redis")

	want := []MemoryRef{
		{Raw: "redis-port", Kind: MemoryRefKindMemory, Scope: "redis", Name: "redis-port"},
		{Raw: "postgres/data-dir", Kind: MemoryRefKindMemory, Scope: "postgres", Name: "data-dir"},
		{Raw: "edge-topology.pdf", Kind: MemoryRefKindFile, Name: "edge-topology.pdf", Path: "/akmatori/context/edge-topology.pdf"},
		{Raw: "../../etc/passwd", Kind: MemoryRefKindInvalid},
		{Raw: "https://evil.example/x", Kind: MemoryRefKindInvalid},
		{Raw: "Upper-Case", Kind: MemoryRefKindInvalid},
		{Raw: "a/b/c", Kind: MemoryRefKindInvalid},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseMemoryRefs_Caps(t *testing.T) {
	var b strings.Builder
	for i := 0; i < memoryRefsMax+10; i++ {
		fmt.Fprintf(&b, "[[m-%d]] ", i)
	}
	if got := ParseMemoryRefs(b.String(), MemoryScopeGlobal); len(got) != memoryRefsMax {
		t.Errorf("got %d refs, want cap %d", len(got), memoryRefsMax)
	}
}

func TestMemoryService_ResolveRefs(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	if err := svc.db.AutoMigrate(&database.ContextFile{}); err != nil {
		t.Fatalf("migrate context_files: %v", err)
	}
	if err := svc.db.Create(&database.ContextFile{Filename: "edge-topology.pdf"}).Error; err != nil {
		t.Fatalf("seed context file: %v", err)
	}

	globalFact, err := svc.CreateMemory(validMemory("dns-resolvers"))
	if err != nil {
		t.Fatalf("create global: %v", err)
	}
	local := validMemory("redis-port")
	local.Scope = "redis"
	localFact, err := svc.CreateMemory(local)
	if err != nil {
		t.Fatalf("create local: %v", err)
	}
	other := validMemory("data-dir")
	other.Scope = "postgres"
	otherFact, err := svc.CreateMemory(other)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	m := &database.Memory{
		Scope: "redis",
		Body: "[[redis-port]] [[dns-resolvers]] [[postgres/data-dir]] [[edge-topology.pdf]] " +
			"[[gone]] [[missing.txt]] [[redis/dns-resolvers]] [[../x]]",
	}
	got := svc.ResolveRefs(m)

	want := []MemoryRef{
		// Same scope wins.
		{Raw: "redis-port", Kind: MemoryRefKindMemory, Scope: "redis", Name: "redis-port", MemoryID: localFact.ID, Path: "/akmatori/memory/redis/redis-port.md", Exists: true},
		// Bare slug falls back to global.
		{Raw: "dns-resolvers", Kind: MemoryRefKindMemory, Scope: MemoryScopeGlobal, Name: "dns-resolvers", MemoryID: globalFact.ID, Path: "/akmatori/memory/global/dns-resolvers.md", Exists: true},
		{Raw: "postgres/data-dir", Kind: MemoryRefKindMemory, Scope: "postgres", Name: "data-dir", MemoryID: otherFact.ID, Path: "/akmatori/memory/postgres/data-dir.md", Exists: true},
		{Raw: "edge-topology.pdf", Kind: MemoryRefKindFile, Name: "edge-topology.pdf", Path: "/akmatori/context/edge-topology.pdf", Exists: true},
		// Dangling targets keep the referring scope.
		{Raw: "gone", Kind: MemoryRefKindMemory, Scope: "redis", Name: "gone", Path: "/akmatori/memory/redis/gone.md"},
		{Raw: "missing.txt", Kind: MemoryRefKindFile, Name: "missing.txt", Path: "/akmatori/context/missing.txt"},
		// An explicit scope never falls back to global.
		{Raw: "redis/dns-resolvers", Kind: MemoryRefKindMemory, Scope: "redis", Name: "dns-resolvers", Path: "/akmatori/memory/redis/dns-resolvers.md"},
		{Raw: "../x", Kind: MemoryRefKindInvalid},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestIngestFromDisk_KeepsRawRefs(t *testing.T) {
	// References must round-trip through ingest and sync unchanged: nothing
	// may rewrite [[...]] into a path in the stored body.
	svc := setupMemoryServiceTest(t)
	dir := svc.MemoryDir() + "/" + MemoryScopeGlobal
	body := "See [[dns-resolvers]] and [[edge-topology.pdf]]."
	writeMemoryFile(t, dir, "linked", "has links", MemoryTypeHost, MemoryScopeGlobal, "inc-1", body)

	for i := 0; i < 2; i++ {
		if err := svc.IngestFromDisk(t.Context()); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 || !strings.Contains(mems[0].Body, body) {
		t.Fatalf("body changed by round trip: %+v", mems)
	}
	if file := readFile(t, dir+"/linked.md"); !strings.Contains(file, body) {
		t.Errorf("synced file lost raw refs:\n%s", file)
	}
}
