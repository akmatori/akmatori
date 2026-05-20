package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	mem, tombstone, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tombstone {
		t.Fatalf("non-deleted file was parsed as tombstone")
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
	mem, tombstone, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tombstone {
		t.Fatalf("non-deleted file was parsed as tombstone")
	}
	if !strings.HasPrefix(mem.Body, "data dir moved to /mnt/data") {
		t.Errorf("body was truncated by stripBodyHeader: %q", mem.Body)
	}
}

func TestParseMemoryFile_RejectsMissingFrontmatter(t *testing.T) {
	if _, _, err := parseMemoryFile([]byte("no frontmatter here"), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on missing frontmatter")
	}
	if _, _, err := parseMemoryFile([]byte("---\nname: x\n"), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on unclosed frontmatter")
	}
}

func TestParseMemoryFile_RejectsInvalidType(t *testing.T) {
	raw := "---\nname: bad\ndescription: x\ntype: invalid\nscope: global\n---\n\nbody\n"
	if _, _, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on invalid type")
	}
}

func TestParseMemoryFile_TombstoneRequiresOnlyName(t *testing.T) {
	// A tombstone file from the memory-writer subagent declares the slug to
	// delete via `deleted: true`. Type/description/body are not required and
	// any values present are ignored — IngestFromDisk uses (scope, name) to
	// locate and delete the corresponding row.
	raw := "---\nname: delete-me\ndeleted: true\n---\n"
	mem, tombstone, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal)
	if err != nil {
		t.Fatalf("parse tombstone: %v", err)
	}
	if !tombstone {
		t.Fatalf("expected tombstone=true")
	}
	if mem.Name != "delete-me" || mem.Scope != MemoryScopeGlobal {
		t.Errorf("tombstone identity wrong: scope=%q name=%q", mem.Scope, mem.Name)
	}
}

func TestParseMemoryFile_TombstoneRejectsMissingName(t *testing.T) {
	raw := "---\ndeleted: true\n---\n"
	if _, _, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal); err == nil {
		t.Fatal("expected error on tombstone without name")
	}
}

// TestParseMemoryFile_TombstoneRejectsInvalidSlug locks the defense-in-depth
// check on tombstone names. Although the SQL DELETE is parameterized, a
// non-slug `name:` (e.g. "../foo" or whitespace-bearing strings) could point at
// rows the canonical write path could never have created. Rejecting at parse
// time turns a poisoned tombstone into a no-op rather than a silent miss.
func TestParseMemoryFile_TombstoneRejectsInvalidSlug(t *testing.T) {
	cases := []string{
		"---\nname: ../escape\ndeleted: true\n---\n",
		"---\nname: bad name\ndeleted: true\n---\n",
		"---\nname: \"   \"\ndeleted: true\n---\n",
		"---\nname: UPPERCASE\ndeleted: true\n---\n",
	}
	for _, raw := range cases {
		if _, _, err := parseMemoryFile([]byte(raw), MemoryScopeGlobal); err == nil {
			t.Errorf("expected error on tombstone with non-slug name; payload=%q", raw)
		}
	}
}

// TestIngestFromDisk_AgentCannotSpoofOperatorAuthorship guards against
// privilege escalation via prompt injection: the memory directory is rw-mounted
// into the agent worker, so a prompt-injected memory-writer could write
// `created_by: operator` into its frontmatter to make the new DB row appear
// operator-authored. IngestFromDisk must ignore the file-level claim and force
// agent authorship for any new row it creates. Operator authorship still
// round-trips for rows that already exist in the DB (covered by
// TestIngestFromDisk_AgentRewriteDoesNotOverwriteOperatorAuthorship).
func TestIngestFromDisk_AgentCannotSpoofOperatorAuthorship(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := "---\n" +
		"name: spoofed-operator-claim\n" +
		"description: agent file claiming operator authorship\n" +
		"type: feedback\n" +
		"scope: global\n" +
		"incident_uuid: inc-7\n" +
		"created_by: operator\n" +
		"---\n\n" +
		"# spoofed-operator-claim\n\n" +
		"body\n"
	if err := os.WriteFile(filepath.Join(dir, "spoofed-operator-claim.md"), []byte(raw), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(mems), mems)
	}
	if mems[0].CreatedBy != MemoryCreatedByAgent {
		t.Errorf("agent file's spoofed operator claim should be rejected; got created_by=%q", mems[0].CreatedBy)
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

func TestIngestFromDisk_SkipsSymlinks(t *testing.T) {
	// The agent worker has rw access to the memory mount, so a prompt-
	// injected memory-writer could plant a symlink pointing outside the
	// memory root. IngestFromDisk must skip symlinks rather than follow
	// them via os.ReadFile, which would happily resolve /etc/passwd or any
	// other readable path under the API container's mount namespace.
	svc := setupMemoryServiceTest(t)
	globalDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, globalDir, "real", "real memory", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "real body")

	// Drop a sensitive target outside the memory root and symlink to it
	// from inside the scope. If symlink-following ever returns, this would
	// be parsed and either ingested or surface in logs.
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\nname: leaked\ndescription: leaked\ntype: tool_quirk\nscope: global\n---\n\n# leaked\n\nleaked\n"), 0644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	linkPath := filepath.Join(globalDir, "evil.md")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 || mems[0].Name != "real" {
		t.Fatalf("symlinked file should be skipped; got %+v", mems)
	}
}

func TestReadMemoryFileFromRoot_RejectsSymlinkAtPath(t *testing.T) {
	// The IngestFromDisk readdir-time DirEntry.Type() check is a fast-path
	// only; the open in readMemoryFileFromRoot is what closes the TOCTOU gap
	// where a writer swaps a regular file for a symlink between readdir and
	// read. O_NOFOLLOW must refuse to open a symlink at the final path
	// component regardless of what readdir saw.
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(target, []byte("payload"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	if _, ok := readMemoryFileFromRoot(root, "link.md"); ok {
		t.Fatalf("readMemoryFileFromRoot should refuse to open a symlink path")
	}
}

func TestReadMemoryFileFromRoot_RejectsSymlinkAtParentComponent(t *testing.T) {
	// Defense in depth against the parent-component swap: even though
	// os.Root scopes traversal to a single inode, we still reject a symlink
	// at the final scope/file component. A planted symlink at <root>/<scope>
	// pointing to another path inside the root would let an attacker funnel
	// reads through a name they control. os.Root.OpenFile with O_NOFOLLOW
	// catches this when the symlink IS the final component, and refuses to
	// resolve symlinks outside the root in any case.
	dir := t.TempDir()
	realScope := filepath.Join(dir, "real-scope")
	if err := os.MkdirAll(realScope, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(realScope, "x.md")
	if err := os.WriteFile(target, []byte("payload"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	// Plant a symlink at <dir>/escape pointing OUTSIDE the root.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	// os.Root.Open must refuse to traverse the escape symlink because its
	// target lives outside the pinned root.
	if _, err := root.Open("escape"); err == nil {
		t.Fatalf("root.Open should refuse to traverse a symlink that escapes the root")
	}
}

func TestReadMemoryFileFromRoot_RejectsFifoAtPath(t *testing.T) {
	// A FIFO planted at the path would block os.ReadFile indefinitely.
	// O_NONBLOCK lets the open return immediately and the fstat-then-mode
	// check rejects it as non-regular without hanging.
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.md")
	if err := syscall.Mkfifo(fifo, 0644); err != nil {
		t.Skipf("mkfifo not supported in this environment: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	done := make(chan struct {
		ok bool
	}, 1)
	go func() {
		_, ok := readMemoryFileFromRoot(root, "pipe.md")
		done <- struct{ ok bool }{ok: ok}
	}()
	select {
	case res := <-done:
		if res.ok {
			t.Fatalf("readMemoryFileFromRoot should refuse to read from a FIFO")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("readMemoryFileFromRoot hung on a FIFO instead of returning")
	}
}

func TestIngestFromDisk_SkipsOversizedFiles(t *testing.T) {
	// A hostile (or buggy) memory-writer could plant a multi-GB .md file.
	// os.ReadFile would slurp it into memory and OOM the API. The size
	// gate must reject anything larger than maxMemoryFileBytes before
	// reading.
	svc := setupMemoryServiceTest(t)
	globalDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeMemoryFile(t, globalDir, "small", "small memory", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "small body")

	huge := make([]byte, maxMemoryFileBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(globalDir, "huge.md"), huge, 0644); err != nil {
		t.Fatalf("write huge: %v", err)
	}

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 || mems[0].Name != "small" {
		t.Fatalf("oversized file should be skipped; got %+v", mems)
	}
}

// TestSyncMemoryFiles_DoesNotFollowSymlinkAtFilePath guards the write path
// against a memory-writer-planted symlink at <memoryDir>/<scope>/<file>
// pointing outside the memory root. Plain os.WriteFile would follow that
// symlink and truncate the API-owned target (e.g. /akmatori/secrets/...) as
// UID 1000. SyncMemoryFiles must refuse to follow the link and instead
// either rewrite the slot in place or unlink it cleanly.
func TestSyncMemoryFiles_DoesNotFollowSymlinkAtFilePath(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	mem := &database.Memory{
		Scope:       MemoryScopeGlobal,
		Type:        MemoryTypeHost,
		Name:        "real-memory",
		Description: "real description",
		Body:        "real body",
		CreatedBy:   MemoryCreatedByOperator,
	}
	if _, err := svc.CreateMemory(mem); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Plant a "secret" file outside the memory root, then replace the
	// canonical memory file with a symlink pointing at it. A vulnerable
	// SyncMemoryFiles would follow the link and overwrite the secret.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret")
	secretContent := []byte("SUPER-SECRET-CONTENT")
	if err := os.WriteFile(secretPath, secretContent, 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	canonicalFile := filepath.Join(scopeDir, fmt.Sprintf("%d-real-memory.md", mem.ID))
	if err := os.Remove(canonicalFile); err != nil {
		t.Fatalf("remove canonical: %v", err)
	}
	if err := os.Symlink(secretPath, canonicalFile); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Trigger a sync (Update touches mtime; UpsertByName re-runs SyncMemoryFiles).
	if _, err := svc.UpsertByName(&database.Memory{
		Scope:       MemoryScopeGlobal,
		Type:        MemoryTypeHost,
		Name:        "real-memory",
		Description: "updated description",
		Body:        "updated body",
		CreatedBy:   MemoryCreatedByOperator,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Secret must NOT be overwritten by SyncMemoryFiles.
	got, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read secret after sync: %v", err)
	}
	if string(got) != string(secretContent) {
		t.Fatalf("symlink followed: secret was rewritten\nwant: %q\ngot:  %q", secretContent, got)
	}

	// The canonical filename should now hold the rewritten memory content,
	// not point at the secret. Either the sync replaced the link with a
	// regular file (preferred — O_NOFOLLOW + create-fresh after stale-cleanup)
	// or it left it broken; both are acceptable as long as the secret is
	// intact. We assert the canonical file is no longer a symlink to the
	// outside path so the next ingest cycle doesn't keep tripping over it.
	info, err := os.Lstat(canonicalFile)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(canonicalFile)
		if target == secretPath {
			t.Fatalf("symlink at canonical path still points outside the memory root: %s -> %s", canonicalFile, target)
		}
	}
}

// TestSyncMemoryFiles_DoesNotHangOnFifoAtFilePath guards the write path
// against a memory-writer-planted FIFO at the canonical file slot. A plain
// open(O_WRONLY) on a FIFO with no reader blocks indefinitely, which would
// hold syncMu and stall every subsequent SyncMemoryFiles call. The pre-Lstat
// in writeMemoryFileInRoot must unlink the FIFO before opening so the write
// proceeds without hanging.
func TestSyncMemoryFiles_DoesNotHangOnFifoAtFilePath(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	mem := &database.Memory{
		Scope:       MemoryScopeGlobal,
		Type:        MemoryTypeHost,
		Name:        "fifo-target",
		Description: "real description",
		Body:        "real body",
		CreatedBy:   MemoryCreatedByOperator,
	}
	if _, err := svc.CreateMemory(mem); err != nil {
		t.Fatalf("seed: %v", err)
	}

	scopeDir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	canonicalFile := filepath.Join(scopeDir, fmt.Sprintf("%d-fifo-target.md", mem.ID))
	if err := os.Remove(canonicalFile); err != nil {
		t.Fatalf("remove canonical: %v", err)
	}
	if err := syscall.Mkfifo(canonicalFile, 0644); err != nil {
		t.Skipf("mkfifo not supported in this environment: %v", err)
	}

	// Trigger a sync from a goroutine with a watchdog. Without the pre-Lstat
	// fix, UpsertByName's SyncMemoryFiles call blocks forever inside open()
	// on the FIFO and the watchdog fires.
	done := make(chan error, 1)
	go func() {
		_, err := svc.UpsertByName(&database.Memory{
			Scope:       MemoryScopeGlobal,
			Type:        MemoryTypeHost,
			Name:        "fifo-target",
			Description: "updated description",
			Body:        "updated body",
			CreatedBy:   MemoryCreatedByOperator,
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("SyncMemoryFiles hung on a FIFO instead of unlinking it and writing")
	}

	// After the sync, the canonical slot must hold a regular file (the
	// freshly rewritten memory content), not the FIFO.
	info, err := os.Lstat(canonicalFile)
	if err != nil {
		t.Fatalf("lstat canonical after sync: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe != 0 {
		t.Fatalf("canonical slot still holds a FIFO after sync; expected a regular file")
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("canonical slot is not a regular file after sync: mode=%v", info.Mode())
	}
}

// TestSyncMemoryFiles_SkipsScopeDirReplacedWithSymlink guards against a
// memory-writer swapping a scope directory for a symlink to outside the
// memory root. SyncMemoryFiles must detect the symlink, skip the scope, and
// leave the link's target untouched. Without the os.Root + Lstat guard, the
// follow-up MkdirAll/Chmod would resolve through the link and either widen
// permissions on an attacker-chosen directory or write into it.
func TestSyncMemoryFiles_SkipsScopeDirReplacedWithSymlink(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	// Seed a real memory so the sync has something to write.
	mem := &database.Memory{
		Scope:       "redis",
		Type:        MemoryTypeHost,
		Name:        "redis-port",
		Description: "redis runs on 16379",
		Body:        "redis prod cluster",
		CreatedBy:   MemoryCreatedByOperator,
	}
	if _, err := svc.CreateMemory(mem); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Replace the scope dir with a symlink to an outside path that has
	// mode 0700 (so we can detect a chmod-widening regression).
	scopeDir := filepath.Join(svc.MemoryDir(), "redis")
	if err := os.RemoveAll(scopeDir); err != nil {
		t.Fatalf("rm scope: %v", err)
	}
	outsideDir := t.TempDir()
	sensitive := filepath.Join(outsideDir, "sensitive")
	if err := os.MkdirAll(sensitive, 0700); err != nil {
		t.Fatalf("mkdir sensitive: %v", err)
	}
	if err := os.Symlink(sensitive, scopeDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Sync; should skip the scope and not touch the link's target.
	if err := svc.SyncMemoryFiles(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	info, err := os.Stat(sensitive)
	if err != nil {
		t.Fatalf("stat sensitive: %v", err)
	}
	// chmod through the symlink would have widened to 0777. The Lstat-then-
	// skip guard must prevent that.
	if info.Mode().Perm() != 0700 {
		t.Errorf("symlink target permissions were widened: got %o, want 0700", info.Mode().Perm())
	}
	// No files written under the link target either.
	entries, _ := os.ReadDir(sensitive)
	if len(entries) != 0 {
		t.Errorf("symlink target was written into: %v", entries)
	}
}

// writeTombstoneFile drops a `deleted: true` memory file at <dir>/<name>.md
// matching what the memory-writer subagent emits when asked to remove a slug.
func writeTombstoneFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndeleted: true\n---\n"
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write tombstone %s: %v", path, err)
	}
	return path
}

// TestIngestFromDisk_TombstoneDeletesRowAndFiles asserts the round-trip the
// memory-writer subagent relies on for `Action: delete <slug>`: after the
// agent overwrites the canonical file with a tombstone, IngestFromDisk must
// remove the DB row and the post-batch SyncMemoryFiles must purge both the
// bare `<slug>.md` tombstone and the prior `<id>-<slug>.md` snapshot.
func TestIngestFromDisk_TombstoneDeletesRowAndFiles(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)

	// Step 1: agent writes a memory the normal way and ingest persists it.
	writeMemoryFile(t, dir, "stale-fact", "fact to be retracted", MemoryTypeHost, MemoryScopeGlobal, "inc-1", "body to remove")
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("seed ingest: %v", err)
	}
	mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal)
	if len(mems) != 1 {
		t.Fatalf("expected 1 row after seed, got %d: %+v", len(mems), mems)
	}
	canonical := filepath.Join(dir, fmt.Sprintf("%d-stale-fact.md", mems[0].ID))
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("canonical file missing after seed sync: %v", err)
	}

	// Step 2: agent writes a tombstone at <slug>.md (mirrors the subagent's
	// edit-to-empty-frontmatter behavior). The canonical snapshot still
	// exists alongside it.
	writeTombstoneFile(t, dir, "stale-fact")

	// Step 3: ingest picks up the tombstone, deletes the DB row, and the
	// post-batch sync purges both the tombstone and the canonical file.
	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("tombstone ingest: %v", err)
	}
	if mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal); len(mems) != 0 {
		t.Fatalf("expected 0 rows after tombstone, got %d: %+v", len(mems), mems)
	}
	if _, err := os.Stat(canonical); !os.IsNotExist(err) {
		t.Errorf("canonical file should be purged after tombstone ingest; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale-fact.md")); !os.IsNotExist(err) {
		t.Errorf("bare tombstone file should be purged after ingest; stat err=%v", err)
	}
}

// TestIngestFromDisk_TombstoneAndCanonicalSameScope ensures the dedup pass
// inside IngestFromDisk lets the tombstone win even when the canonical
// snapshot is read first (lex-sort: `99-foo.md` < `foo.md`). Without the
// tombstone-wins-always rule, the parser could end up upserting the snapshot
// instead of deleting the row.
func TestIngestFromDisk_TombstoneAndCanonicalSameScope(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Drop a canonical snapshot with a numeric prefix that sorts before "foo.md".
	canonical := "---\nname: foo\ndescription: about to delete\ntype: host\nscope: global\nincident_uuid: inc-1\ncreated_by: agent\n---\n\n# foo\n\nabout to delete\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "1-foo.md"), []byte(canonical), 0644); err != nil {
		t.Fatalf("seed canonical: %v", err)
	}
	writeTombstoneFile(t, dir, "foo")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal); len(mems) != 0 {
		t.Fatalf("tombstone+canonical should resolve to deletion, got %d rows: %+v", len(mems), mems)
	}
}

// TestIngestFromDisk_TombstoneForUnknownSlugIsNoOp asserts that a tombstone
// targeting a slug with no matching DB row does not error and still results
// in the tombstone file being purged from disk.
func TestIngestFromDisk_TombstoneForUnknownSlugIsNoOp(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	dir := filepath.Join(svc.MemoryDir(), MemoryScopeGlobal)
	writeTombstoneFile(t, dir, "never-existed")

	if err := svc.IngestFromDisk(context.Background()); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if mems, _ := svc.ListMemoriesByScope(MemoryScopeGlobal); len(mems) != 0 {
		t.Fatalf("expected no rows, got %+v", mems)
	}
	if _, err := os.Stat(filepath.Join(dir, "never-existed.md")); !os.IsNotExist(err) {
		t.Errorf("orphan tombstone should be purged from disk; stat err=%v", err)
	}
}
