package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// writeMemoryFile renders a memory-writer-shaped file and drops it on disk.
// Mirrors what the memory-writer subagent emits, so the ingest test exercises
// the same parse path production hits.
func writeMemoryFile(t *testing.T, dir, name, description, memType, scope, incidentUUID, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", description)
	fmt.Fprintf(&sb, "type: %s\n", memType)
	fmt.Fprintf(&sb, "scope: %s\n", scope)
	if incidentUUID != "" {
		fmt.Fprintf(&sb, "incident_uuid: %s\n", incidentUUID)
	}
	sb.WriteString("created_by: agent\n")
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", name)
	fmt.Fprintf(&sb, "%s\n\n", description)
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestIngestFromDisk_NewFilesCreateRows(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	globalDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, globalDir, "prod-db-data-dir", "data dir lives on /mnt/data", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "Postgres on prod-db-01 has its data dir on /mnt/data.")
	writeMemoryFile(t, globalDir, "redis-port", "redis runs on 16379", MemoryTypeToolQuirk, MemoryScopeGlobal, "inc-1", "Redis prod cluster listens on 16379, not 6379.")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("IngestFromDisk: %v", err)
	}

	mems, err := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mems) != 2 {
		t.Fatalf("expected 2 ingested memories, got %d: %+v", len(mems), mems)
	}
	byName := map[string]database.Memory{}
	for _, m := range mems {
		byName[m.Name] = m
	}
	if got := byName["prod-db-data-dir"]; got.Type != MemoryTypeHost || got.CreatedBy != MemoryCreatedByAgent {
		t.Errorf("prod-db-data-dir: %+v", got)
	}
	if got := byName["prod-db-data-dir"]; !strings.Contains(got.Body, "prod-db-01") {
		t.Errorf("prod-db-data-dir body lost its content: %q", got.Body)
	}
	if got := byName["redis-port"]; got.IncidentUUID != "inc-1" {
		t.Errorf("redis-port incident_uuid: %q", got.IncidentUUID)
	}
}

func TestIngestFromDisk_RoundTripWithoutBodyDuplication(t *testing.T) {
	// Regression: parseMemoryFile must strip the `# <name>` header and the
	// description echo from the file body, otherwise every ingest cycle
	// (writer → ingest → SyncMemoryFiles → ingest again) would accumulate a
	// duplicate description in Body.
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, dir, "round-trip", "a single line description", MemoryTypeHost, MemoryScopeGlobal, "", "real body content")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1, got %d", len(mems))
	}
	if got := mems[0].Body; strings.Contains(got, "# round-trip") {
		t.Errorf("body should not contain the markdown header: %q", got)
	}
	if got := mems[0].Body; strings.HasPrefix(got, "a single line description") {
		t.Errorf("body should not start with the description echo: %q", got)
	}
	if got := mems[0].Body; !strings.Contains(got, "real body content") {
		t.Errorf("body lost its real content: %q", got)
	}
}

func TestIngestFromDisk_ModifiedFilesUpdateByScopeAndName(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, dir, "updatable", "first description", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "v1 body")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	first, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(first) != 1 {
		t.Fatalf("expected 1 row after first ingest, got %d", len(first))
	}
	originalID := first[0].ID

	// SyncMemoryFiles renamed the file to <id>-<name>.md and our test file
	// was kept by removeStaleFiles since SyncMemoryFiles only purges
	// unexpected paths within scopeDir. Rewrite the canonical filename so
	// the file format matches what the writer subagent would produce next time.
	canonical := filepath.Join(dir, fmt.Sprintf("%d-updatable.md", originalID))
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("expected canonical file %s after sync: %v", canonical, err)
	}
	// Now rewrite the file with new description + body but the same name.
	writeMemoryFile(t, dir, "updatable", "updated description", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "v2 body")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	second, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(second) != 1 {
		t.Fatalf("expected 1 row after re-ingest, got %d: %+v", len(second), second)
	}
	if second[0].ID != originalID {
		t.Errorf("upsert changed primary key: was %d, now %d", originalID, second[0].ID)
	}
	if !strings.Contains(second[0].Description, "updated description") {
		t.Errorf("description didn't update: %q", second[0].Description)
	}
	if !strings.Contains(second[0].Body, "v2 body") {
		t.Errorf("body didn't update: %q", second[0].Body)
	}
}

func TestIngestFromDisk_IsIdempotent(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, dir, "stable", "static description", MemoryTypeFeedback, MemoryScopeGlobal, "inc-1", "stable body")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("third ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected idempotent single row, got %d: %+v", len(mems), mems)
	}
	if mems[0].CreatedBy != MemoryCreatedByAgent {
		t.Errorf("created_by should be %q, got %q", MemoryCreatedByAgent, mems[0].CreatedBy)
	}
}

func TestIngestFromDisk_SkipsManifestAndInvalidFiles(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Manifest must be skipped silently — it's regenerated by SyncMemoryFiles.
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte("# Manifest"), 0644); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	// Files without YAML frontmatter must be skipped without erroring.
	if err := os.WriteFile(filepath.Join(dir, "no-frontmatter.md"), []byte("just markdown no frontmatter"), 0644); err != nil {
		t.Fatalf("seed bad file: %v", err)
	}
	// Files with invalid memory type must be skipped.
	bad := "---\nname: bad\ndescription: x\ntype: nope\nscope: global\n---\n\n# bad\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "bad-type.md"), []byte(bad), 0644); err != nil {
		t.Fatalf("seed bad-type: %v", err)
	}
	// Files with non-.md suffix must be skipped.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatalf("seed txt: %v", err)
	}

	// One good file mixed in to prove the loop doesn't bail on the first error.
	writeMemoryFile(t, dir, "keeper", "good description", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "keep me")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 || mems[0].Name != "keeper" {
		t.Fatalf("expected only the good file ingested, got %+v", mems)
	}
}

func TestIngestFromDisk_SkipsNonSlugScopeDirs(t *testing.T) {
	// Scope directories must be slug-safe (lowercase a-z/0-9/hyphen). Any
	// other directory under memoryDir is ignored — defense against an
	// operator hand-creating a scope dir that wouldn't pass validation.
	svc := setupMemoryServiceTest(t)
	badScopeDir := filepath.Join(svc.MemoryDir(), "Bad Scope!")
	writeMemoryFile(t, badScopeDir, "should-not-land", "x", MemoryTypeHost, "Bad Scope!", "", "body")

	// And a sibling good scope dir to prove the good path still works.
	goodDir := filepath.Join(svc.MemoryDir(), "redis")
	writeMemoryFile(t, goodDir, "good", "good description", MemoryTypeHost, "redis", "", "good body")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mems, _ := svc.ListMemories("", "")
	if len(mems) != 1 || mems[0].Name != "good" {
		t.Fatalf("expected only good scope to be ingested, got %+v", mems)
	}
}

func TestIngestFromDisk_EmptyDirIsNoOp(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest on empty dir: %v", err)
	}
	mems, _ := svc.ListMemories("", "")
	if len(mems) != 0 {
		t.Errorf("expected no rows on empty ingest, got %+v", mems)
	}
}

func TestIngestFromDisk_MissingDirIsNoOp(t *testing.T) {
	// Regression: if the memory directory hasn't been created yet (fresh
	// install, no incidents completed), ingest must succeed silently rather
	// than erroring out and surfacing as a startup warning.
	svc := setupMemoryServiceTest(t)
	if err := os.RemoveAll(svc.MemoryDir()); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest on missing dir: %v", err)
	}
}

func TestParseMemoryFile_HandlesQuotedDescription(t *testing.T) {
	// renderMemoryFile YAML-encodes descriptions that contain colons or
	// quotes. The parser must unwrap them faithfully so a round-trip
	// description is preserved.
	raw := "---\nname: quoted\ndescription: 'prod-db: data dir moved to /mnt/data'\ntype: host\nscope: global\nincident_uuid: inc-x\ncreated_by: agent\n---\n\n# quoted\n\nprod-db: data dir moved to /mnt/data\n\nbody\n"
	mem, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mem.Description != "prod-db: data dir moved to /mnt/data" {
		t.Errorf("description unwrap failed: %q", mem.Description)
	}
	if mem.IncidentUUID != "inc-x" {
		t.Errorf("incident_uuid: %q", mem.IncidentUUID)
	}
	if mem.Body != "body" {
		t.Errorf("body: %q", mem.Body)
	}
}

// TestParseMemoryFile_PreservesBodyStartingWithDescriptionText guards against
// stripBodyHeader chopping the body in the middle of a sentence when the
// agent's prose legitimately begins with the description text and continues
// on the same line. The echo-strip must only fire when the description sits
// on its own line (the shape renderMemoryFile produces).
func TestParseMemoryFile_PreservesBodyStartingWithDescriptionText(t *testing.T) {
	// Body begins with the description as a topic sentence that keeps going
	// on the same line — no blank line after the description text. The
	// parser must NOT eat the leading bytes.
	raw := "---\nname: topic\ndescription: data dir moved\ntype: host\nscope: global\n---\n\n" +
		"# topic\n\ndata dir moved to /mnt/data and now also tracks WAL on /mnt/wal.\n"
	mem, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasPrefix(mem.Body, "data dir moved to /mnt/data") {
		t.Errorf("body was truncated by stripBodyHeader: %q", mem.Body)
	}
}

func TestParseMemoryFile_RejectsMissingFrontmatter(t *testing.T) {
	if _, err := parseMemoryFile([]byte("no frontmatter here"), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on missing frontmatter")
	}
	if _, err := parseMemoryFile([]byte("---\nname: x\n"), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on unclosed frontmatter")
	}
}

func TestParseMemoryFile_RejectsInvalidType(t *testing.T) {
	raw := "---\nname: bad\ndescription: x\ntype: invalid\nscope: global\n---\n\nbody\n"
	if _, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on invalid type")
	}
}

// TestIngestFromDisk_PreservesOperatorCreatedBy guards against the original
// IngestFromDisk forcing CreatedBy=agent on every parsed file. SyncMemoryFiles
// rolls operator-authored memories to disk with `created_by: operator`; a
// follow-on ingest must NOT silently rewrite them as agent-authored.
func TestIngestFromDisk_PreservesOperatorCreatedBy(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := "---\n" +
		"name: operator-feedback-rename\n" +
		"description: prefer renaming the zabbix host before the upgrade\n" +
		"type: feedback\n" +
		"scope: global\n" +
		"incident_uuid: inc-7\n" +
		"created_by: operator\n" +
		"---\n\n" +
		"# operator-feedback-rename\n\n" +
		"prefer renaming the zabbix host before the upgrade\n\n" +
		"longer body explaining why\n"
	if err := os.WriteFile(filepath.Join(dir, "operator-feedback-rename.md"), []byte(raw), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(mems), mems)
	}
	if mems[0].CreatedBy != MemoryCreatedByOperator {
		t.Errorf("operator authorship should be preserved on ingest; got created_by=%q", mems[0].CreatedBy)
	}
}

// TestIngestFromDisk_AgentRewriteDoesNotOverwriteOperatorAuthorship guards
// the upsert path: when an operator-authored row already exists in the DB
// and the agent later writes a fresh file with the same scope+name but
// `created_by: agent`, the upsert MUST preserve the existing
// `created_by: operator` value. The previous behavior included created_by in
// the DoUpdates clause and silently flipped operator memories to agent every
// time the agent re-wrote them.
func TestIngestFromDisk_AgentRewriteDoesNotOverwriteOperatorAuthorship(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)

	// Step 1: seed an operator memory in DB (mirrors the UI feedback path).
	op := &database.Memory{
		Scope:        MemoryScopeGlobal,
		Type:         MemoryTypeFeedback,
		Name:         "rename-host-before-upgrade",
		Description:  "operator says: rename zabbix host before any upgrade",
		Body:         "operator's original notes",
		IncidentUUID: "op-inc",
		CreatedBy:    MemoryCreatedByOperator,
	}
	if _, err := svc.UpsertByName(op); err != nil {
		t.Fatalf("seed operator row: %v", err)
	}

	// Step 2: agent writes a fresh file at the same scope+name with
	// created_by: agent — simulating the memory-writer subagent revisiting
	// the same slug during a later incident.
	writeMemoryFile(t, dir,
		"rename-host-before-upgrade",
		"agent-rewritten summary",
		MemoryTypeFeedback, MemoryScopeGlobal, "agent-inc",
		"agent's updated body with newer context",
	)

	// Step 3: ingest picks up the agent file.
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(mems), mems)
	}
	if mems[0].CreatedBy != MemoryCreatedByOperator {
		t.Errorf("agent re-ingest flipped authorship: created_by=%q, want %q",
			mems[0].CreatedBy, MemoryCreatedByOperator)
	}
	// Content (body / description / incident_uuid) is expected to update —
	// only authorship is sticky.
	if !strings.Contains(mems[0].Body, "agent's updated body") {
		t.Errorf("body should still update on conflict: %q", mems[0].Body)
	}
	if !strings.Contains(mems[0].Description, "agent-rewritten summary") {
		t.Errorf("description should still update on conflict: %q", mems[0].Description)
	}
}

// TestIngestFromDisk_PrefersAgentFilenameOverCanonical asserts that when both
// `<name>.md` (agent's fresh write) and `<id>-<name>.md` (prior canonical
// snapshot) exist in the same scope dir, the agent's newer content wins —
// regardless of lex-sort order between the two filenames.
func TestIngestFromDisk_PrefersAgentFilenameOverCanonical(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Drop the canonical snapshot first (older body).
	canonical := "---\nname: edge-case\ndescription: old summary\ntype: host\nscope: global\nincident_uuid: inc-1\ncreated_by: agent\n---\n\n# edge-case\n\nold summary\n\nold body content\n"
	if err := os.WriteFile(filepath.Join(dir, "99-edge-case.md"), []byte(canonical), 0644); err != nil {
		t.Fatalf("seed canonical: %v", err)
	}
	// Drop the agent's freshly-written file with the same memory name and
	// newer body. Lex-sort puts "99-..." first, but the dedup rule must
	// still pick the non-canonical version.
	agentFresh := "---\nname: edge-case\ndescription: NEW summary\ntype: host\nscope: global\nincident_uuid: inc-1\ncreated_by: agent\n---\n\n# edge-case\n\nNEW summary\n\nNEW body content\n"
	if err := os.WriteFile(filepath.Join(dir, "edge-case.md"), []byte(agentFresh), 0644); err != nil {
		t.Fatalf("seed agent fresh: %v", err)
	}

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(mems), mems)
	}
	if !strings.Contains(mems[0].Body, "NEW body content") {
		t.Errorf("dedup picked the wrong file: body=%q", mems[0].Body)
	}
	if !strings.Contains(mems[0].Description, "NEW summary") {
		t.Errorf("dedup picked the wrong file: description=%q", mems[0].Description)
	}
}

func TestCanonicalIngestName(t *testing.T) {
	cases := []struct {
		filename string
		name     string
		want     bool
	}{
		{"5-foo.md", "foo", true},
		{"123-foo-bar.md", "foo-bar", true},
		{"foo.md", "foo", false},
		{"-foo.md", "foo", false},          // empty numeric prefix
		{"abc-foo.md", "foo", false},       // non-numeric prefix
		{"5-foo.md", "different", false},   // name mismatch
		{"5-foo-extra.md", "foo", false},   // trailing extra not part of name
		{"5foo.md", "foo", false},          // no hyphen separator
	}
	for _, c := range cases {
		if got := canonicalIngestName(c.filename, c.name); got != c.want {
			t.Errorf("canonicalIngestName(%q, %q) = %v, want %v", c.filename, c.name, got, c.want)
		}
	}
}
