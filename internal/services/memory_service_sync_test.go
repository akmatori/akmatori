package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gopkg.in/yaml.v3"
)

func TestMemoryService_Sync_WritesFilesAndManifest(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	first := validMemory("postgres-data-dir")
	first.Description = "data dir lives on /mnt/data"
	first.Body = "Postgres on prod-db-01 has its data dir on /mnt/data."
	if _, err := svc.CreateMemory(first); err != nil {
		t.Fatalf("create first: %v", err)
	}

	second := validMemory("redis-port")
	second.Type = MemoryTypeToolQuirk
	second.Description = "redis runs on 16379"
	second.Body = "Redis prod cluster listens on 16379, not 6379."
	if _, err := svc.CreateMemory(second); err != nil {
		t.Fatalf("create second: %v", err)
	}

	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	files, err := os.ReadDir(scopeDir)
	if err != nil {
		t.Fatalf("read scope dir: %v", err)
	}

	wantFiles := map[string]bool{
		manifestFile:        true,
		"1-postgres-data-dir.md": true,
		"2-redis-port.md":   true,
	}
	for _, f := range files {
		if !wantFiles[f.Name()] {
			t.Errorf("unexpected file %q in scope dir", f.Name())
		}
		delete(wantFiles, f.Name())
	}
	for missing := range wantFiles {
		t.Errorf("expected file %q in scope dir, not found", missing)
	}

	manifest := readFile(t, filepath.Join(scopeDir, manifestFile))
	if !strings.Contains(manifest, "scope: global") {
		t.Errorf("manifest missing scope header: %s", manifest)
	}
	for _, want := range []string{"`postgres-data-dir`", "`redis-port`", MemoryTypeHost, MemoryTypeToolQuirk} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q\n%s", want, manifest)
		}
	}

	body := readFile(t, filepath.Join(scopeDir, "1-postgres-data-dir.md"))
	for _, want := range []string{
		"name: postgres-data-dir",
		"type: " + MemoryTypeHost,
		"scope: " + MemoryScopeGlobal,
		"# postgres-data-dir",
		"Postgres on prod-db-01",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("memory file missing %q\n%s", want, body)
		}
	}
}

func TestMemoryService_Sync_RemovesStaleFiles(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	m, err := svc.CreateMemory(validMemory("doomed"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)

	// Add an orphan file the next sync should clean up.
	orphan := filepath.Join(scopeDir, "999-orphan.md")
	if err := os.WriteFile(orphan, []byte("stale"), 0644); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	if err := svc.DeleteMemory(m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Scope dir is now stale (no entries) — should be removed entirely.
	if _, err := os.Stat(scopeDir); !os.IsNotExist(err) {
		t.Fatalf("expected scope dir removed, stat err = %v", err)
	}
}

func TestMemoryService_Sync_ScopeRenamePurgesOldDir(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	m, err := svc.CreateMemory(validMemory("movable"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	oldDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old scope dir missing: %v", err)
	}

	// Move to a new scope.
	updated, err := svc.UpdateMemory(m.ID, &database.Memory{
		Scope:       "redis",
		Type:        MemoryTypeHost,
		Description: "moved",
		Body:        "moved",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Scope != "redis" {
		t.Fatalf("scope did not move: %+v", updated)
	}

	// Old scope dir should be gone (no remaining entries); new one present.
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expected old scope dir removed, stat err = %v", err)
	}
	newDir := filepath.Join(svc.MemoryDir(), "redis")
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("new scope dir missing: %v", err)
	}
}

func TestRenderManifest_TruncatesAtLineCap(t *testing.T) {
	entries := make([]database.Memory, manifestMaxLines+50)
	for i := range entries {
		entries[i] = database.Memory{
			ID:          uint(i + 1),
			Scope:       MemoryScopeGlobal,
			Type:        MemoryTypeHost,
			Name:        "memory-" + strings.Repeat("x", 5),
			Description: "row",
		}
	}
	got := renderManifest(MemoryScopeGlobal, entries)
	lines := strings.Count(got, "\n")
	if lines > manifestMaxLines+5 { // allow header + truncation marker overhead
		t.Errorf("manifest exceeded line cap: %d lines\n%s", lines, got[:200])
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation marker, got %s", got)
	}
}

func TestRenderManifest_TruncatesAtByteCap(t *testing.T) {
	// Each row gets a long description to push the byte counter quickly.
	entries := make([]database.Memory, 100)
	for i := range entries {
		entries[i] = database.Memory{
			ID:          uint(i + 1),
			Scope:       MemoryScopeGlobal,
			Type:        MemoryTypeIncidentPattern,
			Name:        "n" + strings.Repeat("x", 50),
			Description: strings.Repeat("y", 600),
		}
	}
	got := renderManifest(MemoryScopeGlobal, entries)
	if len(got) > manifestMaxBytes+1024 {
		t.Errorf("manifest exceeded byte cap: %d bytes", len(got))
	}
	if !strings.Contains(got, "truncated") && !strings.Contains(got, "byte cap") {
		t.Errorf("expected truncation marker; got tail: %s", got[max(0, len(got)-200):])
	}
}

func TestRenderManifest_EmptyScope(t *testing.T) {
	got := renderManifest("redis", nil)
	if !strings.Contains(got, "_No memories yet._") {
		t.Errorf("empty manifest missing placeholder: %s", got)
	}
}

func TestRenderMemoryFile_FrontmatterIsValidYAML(t *testing.T) {
	// Regression: previously frontmatter was hand-formatted with raw
	// fmt.Fprintf, so a description containing YAML-significant characters
	// (colons, brackets, quotes) made the file invalid YAML and broke
	// downstream consumers.
	cases := []struct {
		name        string
		description string
	}{
		{"colon and value", "prod-db: data dir moved to /mnt/data"},
		{"double quotes", `the value is "important"`},
		{"single quotes", "operator's note about server"},
		{"brackets and braces", "host [prod-1] timed out: see {logs}"},
		{"yaml special chars", "*alias and &anchor and #comment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := database.Memory{
				ID:          1,
				Scope:       "global",
				Type:        MemoryTypeHost,
				Name:        "test-mem",
				Description: tc.description,
				Body:        "body",
			}
			out := renderMemoryFile(m)

			// Extract just the frontmatter (between the two `---` lines).
			parts := strings.SplitN(out, "---", 3)
			if len(parts) < 3 {
				t.Fatalf("expected frontmatter delimiters, got: %s", out)
			}
			frontmatter := parts[1]

			var fm map[string]interface{}
			if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
				t.Fatalf("frontmatter is not valid YAML: %v\nfrontmatter:\n%s", err, frontmatter)
			}
			if got, _ := fm["description"].(string); got != tc.description {
				t.Errorf("description round-trip failed:\n  in:  %q\n  out: %q", tc.description, got)
			}
			if got, _ := fm["name"].(string); got != "test-mem" {
				t.Errorf("name round-trip failed: %q", got)
			}
			if got, _ := fm["type"].(string); got != MemoryTypeHost {
				t.Errorf("type round-trip failed: %q", got)
			}
		})
	}
}

func TestRenderMemoryFile_Frontmatter(t *testing.T) {
	m := database.Memory{
		ID:           7,
		Scope:        "redis",
		Type:         MemoryTypeFeedback,
		Name:         "tune-maxmemory",
		Description:  "run with maxmemory-policy=allkeys-lru\nfor cache workloads",
		Body:         "Set maxmemory-policy=allkeys-lru on prod-redis",
		IncidentUUID: "abc-123",
		CreatedBy:    MemoryCreatedByAgent,
	}
	got := renderMemoryFile(m)
	for _, want := range []string{
		"name: tune-maxmemory",
		"type: " + MemoryTypeFeedback,
		"scope: redis",
		"incident_uuid: abc-123",
		"created_by: " + MemoryCreatedByAgent,
		// Newline in the description is normalized to a space so the YAML stays
		// single-line and parsable.
		"description: run with maxmemory-policy=allkeys-lru for cache workloads",
		"# tune-maxmemory",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered file missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "incident_uuid: \n") {
		t.Errorf("empty incident_uuid leaked into output: %s", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestSyncMemoryFiles_ManifestTruncatesToPerScopeCap verifies that when a scope
// has more than manifestMaxEntriesPerScope entries, the MEMORY.md manifest is
// capped to that many entries with a truncation comment, while all files remain
// on disk.
func TestSyncMemoryFiles_ManifestTruncatesToPerScopeCap(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	total := manifestMaxEntriesPerScope + 1
	for i := 0; i < total; i++ {
		m := validMemory(fmt.Sprintf("entry-%04d", i))
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("create memory %d: %v", i, err)
		}
	}

	if err := svc.SyncMemoryFiles(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)

	// All files must still be on disk (manifest cap does not delete files).
	files, err := os.ReadDir(scopeDir)
	if err != nil {
		t.Fatalf("read scope dir: %v", err)
	}
	mdCount := 0
	for _, f := range files {
		if f.Name() != manifestFile && strings.HasSuffix(f.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != total {
		t.Errorf("expected %d .md files on disk, got %d", total, mdCount)
	}

	// Manifest must be truncated and carry the HTML comment.
	manifest := readFile(t, filepath.Join(scopeDir, manifestFile))
	if !strings.Contains(manifest, "<!-- truncated:") {
		t.Errorf("expected truncation comment in manifest; got:\n%s", manifest)
	}

	// Count table rows (lines starting with "| `").
	rows := 0
	for _, line := range strings.Split(manifest, "\n") {
		if strings.HasPrefix(line, "| `") {
			rows++
		}
	}
	if rows != manifestMaxEntriesPerScope {
		t.Errorf("expected %d manifest rows, got %d", manifestMaxEntriesPerScope, rows)
	}
}

// TestSyncMemoryFiles_SuppressEntryAlwaysInManifest verifies that a suppress-
// flagged memory appears in the manifest even when non-suppress entries fill
// the manifestMaxEntriesPerScope quota.
func TestSyncMemoryFiles_SuppressEntryAlwaysInManifest(t *testing.T) {
	svc := setupMemoryServiceTest(t)

	// Fill the non-suppress quota exactly.
	for i := 0; i < manifestMaxEntriesPerScope; i++ {
		m := validMemory(fmt.Sprintf("regular-%04d", i))
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatalf("create regular memory %d: %v", i, err)
		}
	}

	// Create a suppress-flagged memory and force an old updated_at so it ranks
	// below all regular entries by recency.
	sup := validMemory("suppress-sig")
	sup.Suppress = true
	sup.Description = "known false-positive suppression signature"
	created, err := svc.CreateMemory(sup)
	if err != nil {
		t.Fatalf("create suppress memory: %v", err)
	}
	oldTime := time.Now().Add(-365 * 24 * time.Hour)
	if err := svc.db.Model(created).Update("updated_at", oldTime).Error; err != nil {
		t.Fatalf("force old updated_at: %v", err)
	}

	if err := svc.SyncMemoryFiles(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	manifest := readFile(t, filepath.Join(svc.MemoryDir(), MemoryScopeGlobal, manifestFile))
	if !strings.Contains(manifest, "suppress-sig") {
		t.Errorf("suppress-flagged entry missing from manifest:\n%s", manifest)
	}
}

// TestLimitManifestEntries verifies the helper's core semantics directly.
func TestLimitManifestEntries(t *testing.T) {
	now := time.Now()
	makeEntry := func(name string, suppress bool, age time.Duration) database.Memory {
		return database.Memory{
			Name:      name,
			Suppress:  suppress,
			UpdatedAt: now.Add(-age),
		}
	}

	t.Run("under cap returns unchanged", func(t *testing.T) {
		entries := []database.Memory{
			makeEntry("a", false, time.Hour),
			makeEntry("b", false, 2*time.Hour),
		}
		limited, truncated := limitManifestEntries(entries, 5)
		if len(limited) != 2 || truncated != 0 {
			t.Errorf("got len=%d truncated=%d, want len=2 truncated=0", len(limited), truncated)
		}
	})

	t.Run("non-suppress capped, suppress always included", func(t *testing.T) {
		var entries []database.Memory
		entries = append(entries, makeEntry("suppress-old", true, 100*24*time.Hour))
		for i := 0; i < 5; i++ {
			entries = append(entries, makeEntry(fmt.Sprintf("reg-%d", i), false, time.Duration(i)*time.Hour))
		}
		// cap=4: 1 suppress always + 3 non-suppress slots (4-1=3), 2 non-suppress truncated
		limited, truncated := limitManifestEntries(entries, 4)
		if truncated != 2 {
			t.Errorf("expected 2 truncated, got %d", truncated)
		}
		hasSup := false
		for _, m := range limited {
			if m.Name == "suppress-old" {
				hasSup = true
			}
		}
		if !hasSup {
			t.Errorf("suppress entry missing from limited set: %+v", limited)
		}
		if len(limited) != 4 {
			t.Errorf("expected 4 entries, got %d", len(limited))
		}
	})

	t.Run("most-recently-updated non-suppress are retained", func(t *testing.T) {
		entries := []database.Memory{
			makeEntry("old", false, 10*time.Hour),
			makeEntry("newest", false, time.Minute),
			makeEntry("mid", false, 5*time.Hour),
		}
		limited, truncated := limitManifestEntries(entries, 2)
		if truncated != 1 {
			t.Errorf("expected 1 truncated, got %d", truncated)
		}
		for _, m := range limited {
			if m.Name == "old" {
				t.Errorf("oldest entry should have been excluded: %+v", limited)
			}
		}
	})
}
