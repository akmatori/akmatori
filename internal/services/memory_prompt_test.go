package services

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newSkillServiceForPromptTest builds a minimal SkillService backed by an
// in-memory database: the manifest is rendered from the memories table.
func newSkillServiceForPromptTest(t *testing.T) *SkillService {
	t.Helper()
	tmp := t.TempDir()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Memory{}, &database.Skill{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &SkillService{
		db:           db,
		dataDir:      tmp,
		incidentsDir: filepath.Join(tmp, "incidents"),
		skillsDir:    filepath.Join(tmp, "skills"),
		memoryDir:    filepath.Join(tmp, "memory"),
	}
}

// writeManifest stores one memory in scope whose description is body, so the
// rendered manifest contains body.
func writeManifest(t *testing.T, svc *SkillService, scope, body string) {
	t.Helper()
	name := SlugifyMemoryName(scope + "-entry")
	if err := svc.db.Create(&database.Memory{Scope: scope, Name: name, Type: MemoryTypeFact, Description: body, Body: "b"}).Error; err != nil {
		t.Fatalf("seed memory: %v", err)
	}
}

func TestRenderMemoryRecallSection_WithManifest(t *testing.T) {
	svc := newSkillServiceForPromptTest(t)
	writeManifest(t, svc, MemoryScopeGlobal, "data dir on /mnt/data")

	got := svc.renderMemoryRecallSection(MemoryScopeGlobal)
	for _, want := range []string{
		"## Cross-incident Memory",
		`gateway_call("memory.search"`,
		`gateway_call("memory.get"`,
		"### Record durable findings",
		`gateway_call("memory.write", {"scope": "global"`,
		"# Memory Manifest — scope: global",
		"data dir on /mnt/data",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected substring %q, got:\n%s", want, got)
		}
	}
	for _, banned := range []string{"subagent(", "memory-searcher", "memory-writer", "/akmatori/memory"} {
		if strings.Contains(got, banned) {
			t.Errorf("recall section must not contain %q; got:\n%s", banned, got)
		}
	}
}

// TestRenderMemoryRecallSection_ScopedToSkillUsesSkillScope verifies the
// memory.write example bakes in the caller's scope.
func TestRenderMemoryRecallSection_ScopedToSkillUsesSkillScope(t *testing.T) {
	svc := newSkillServiceForPromptTest(t)
	got := svc.renderMemoryRecallSection("redis-skill")
	if !strings.Contains(got, `{"scope": "redis-skill"`) {
		t.Errorf("expected memory.write scope redis-skill, got:\n%s", got)
	}
}

// TestRenderMemoryRecallSection_ManifestFollowsDatabase: a memory written by
// the gateway shows up in the next render with no disk sync.
func TestRenderMemoryRecallSection_ManifestFollowsDatabase(t *testing.T) {
	svc := newSkillServiceForPromptTest(t)
	if got := svc.renderMemoryRecallSection("redis-skill"); !strings.Contains(got, "_No memories indexed") {
		t.Fatalf("empty scope placeholder missing:\n%s", got)
	}
	writeManifest(t, svc, "redis-skill", "maxmemory is 8gb")
	if got := svc.renderMemoryRecallSection("redis-skill"); !strings.Contains(got, "maxmemory is 8gb") {
		t.Errorf("new memory not in the manifest:\n%s", got)
	}
}

func TestRenderMemoryRecallSection_NoManifest(t *testing.T) {
	svc := newSkillServiceForPromptTest(t)

	got := svc.renderMemoryRecallSection(MemoryScopeGlobal)
	if !strings.Contains(got, "## Cross-incident Memory") {
		t.Errorf("expected header even when no manifest exists, got:\n%s", got)
	}
	if !strings.Contains(got, memoryRecallInstruction) {
		t.Errorf("expected recall instruction even when no manifest, got:\n%s", got)
	}
	if !strings.Contains(got, "_No memories indexed") {
		t.Errorf("expected empty-scope placeholder, got:\n%s", got)
	}
}

func TestRenderMemoryRecallSection_NoDatabase(t *testing.T) {
	svc := &SkillService{}
	if got := svc.renderMemoryRecallSection(MemoryScopeGlobal); got != "" {
		t.Errorf("expected empty string without a database, got %q", got)
	}
}

func TestGenerateSkillMd_InjectsScopeMemoryBeforeTools(t *testing.T) {
	tmp := t.TempDir()
	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx service: %v", err)
	}
	svc := newSkillServiceForPromptTest(t)
	svc.contextService = ctxSvc
	writeManifest(t, svc, "redis-skill", "manifest body for redis-skill")

	out := svc.generateSkillMd("redis-skill", "Redis investigator", "## Body\n\nDo redis stuff.\n")

	if !strings.Contains(out, "## Cross-incident Memory") {
		t.Fatalf("memory section not injected:\n%s", out)
	}
	if !strings.Contains(out, "manifest body for redis-skill") {
		t.Errorf("scope manifest body missing:\n%s", out)
	}
	bodyIdx := strings.Index(out, "Do redis stuff.")
	memIdx := strings.Index(out, "## Cross-incident Memory")
	if bodyIdx < 0 || memIdx < 0 || bodyIdx > memIdx {
		t.Fatalf("body should appear before memory section: bodyIdx=%d memIdx=%d", bodyIdx, memIdx)
	}
}

func TestGenerateIncidentAgentsMd_InjectsGlobalManifest(t *testing.T) {
	tmp := t.TempDir()
	svc := newSkillServiceForPromptTest(t)
	writeManifest(t, svc, MemoryScopeGlobal, "manifest for global scope")

	out := filepath.Join(tmp, "AGENTS.md")
	if err := svc.generateAgentsMd(out, "incident-manager", "test-incident-uuid"); err != nil {
		t.Fatalf("generate AGENTS.md: %v", err)
	}
	got := readFile(t, out)
	if !strings.Contains(got, "Incident Manager") {
		t.Errorf("AGENTS.md should still contain incident-manager prompt")
	}
	if !strings.Contains(got, "## Cross-incident Memory") {
		t.Errorf("expected global memory section in AGENTS.md, got:\n%s", got)
	}
	if !strings.Contains(got, "manifest for global scope") {
		t.Errorf("expected global manifest body to be inlined, got:\n%s", got)
	}
}

func TestRegenerateSkillMd_PicksUpCurrentManifest(t *testing.T) {
	// Regression: previously, writing a memory under scope=<skill_name>
	// updated MEMORY.md but left SKILL.md stale. RegenerateSkillMd must
	// re-render the file so the manifest copy in SKILL.md tracks reality.
	tmp := t.TempDir()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Skill{}, &database.ToolInstance{}, &database.ToolType{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&database.Skill{Name: "test-skill", Description: "test", Enabled: true}).Error; err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	database.DB = db

	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx: %v", err)
	}
	svc := &SkillService{
		db:             db,
		dataDir:        tmp,
		incidentsDir:   filepath.Join(tmp, "incidents"),
		skillsDir:      filepath.Join(tmp, "skills"),
		memoryDir:      filepath.Join(tmp, "memory"),
		contextService: ctxSvc,
	}

	// First regeneration with no memory → SKILL.md gets the no-memories placeholder.
	if err := svc.RegenerateSkillMd("test-skill"); err != nil {
		t.Fatalf("first regen: %v", err)
	}
	skillMdPath := filepath.Join(svc.skillsDir, "test-skill", "SKILL.md")
	first := readFile(t, skillMdPath)
	if !strings.Contains(first, "_No memories indexed") {
		t.Errorf("first regen should show empty-manifest placeholder, got:\n%s", first)
	}

	// Now store a memory (as the gateway would), then regenerate.
	if err := db.AutoMigrate(&database.Memory{}); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, svc, "test-skill", "manifest body for test-skill — fresh content")
	if err := svc.RegenerateSkillMd("test-skill"); err != nil {
		t.Fatalf("second regen: %v", err)
	}
	second := readFile(t, skillMdPath)
	if !strings.Contains(second, "manifest body for test-skill — fresh content") {
		t.Errorf("second regen should reflect updated manifest, got:\n%s", second)
	}
	if strings.Contains(second, "_No memories indexed") {
		t.Errorf("second regen should not still show empty placeholder")
	}
}

func TestRegenerateSkillMd_NoOpForIncidentManager(t *testing.T) {
	svc := &SkillService{}
	if err := svc.RegenerateSkillMd("incident-manager"); err != nil {
		t.Errorf("incident-manager regen should be a silent no-op, got %v", err)
	}
	if err := svc.RegenerateSkillMd(""); err != nil {
		t.Errorf("empty name regen should be a silent no-op, got %v", err)
	}
}

func TestGenerateIncidentAgentsMd_NoManifestFallsBackGracefully(t *testing.T) {
	tmp := t.TempDir()
	svc := newSkillServiceForPromptTest(t)
	out := filepath.Join(tmp, "AGENTS.md")
	if err := svc.generateAgentsMd(out, "incident-manager", "test-incident-uuid"); err != nil {
		t.Fatalf("generate AGENTS.md: %v", err)
	}
	got := readFile(t, out)
	if !strings.Contains(got, memoryRecallInstruction) {
		t.Errorf("expected recall instruction even when no manifest, got:\n%s", got)
	}
}

func TestGenerateSkillMd_RoundTripDoesNotDuplicateMemorySection(t *testing.T) {
	tmp := t.TempDir()
	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx service: %v", err)
	}
	svc := newSkillServiceForPromptTest(t)
	svc.contextService = ctxSvc
	writeManifest(t, svc, "redis-skill", "manifest body")

	originalBody := "## Body\n\nDo redis stuff.\n"
	out := svc.generateSkillMd("redis-skill", "Redis investigator", originalBody)

	// Reverse the round-trip: split on `---`, take body, run through the
	// strip helper. This is exactly what GetSkillPrompt does in production.
	parts := strings.SplitN(out, "---", 3)
	if len(parts) < 3 {
		t.Fatalf("expected 3 parts after frontmatter split, got %d", len(parts))
	}
	body := strings.TrimLeft(parts[2], " \t\n\r")
	stripped := stripAutoGeneratedSections(body)

	if strings.Contains(stripped, "## Cross-incident Memory") {
		t.Fatalf("memory section should be stripped on round-trip, but survived:\n%s", stripped)
	}
	if strings.Contains(stripped, "## Assigned Tools") {
		t.Fatalf("tools section should be stripped on round-trip, but survived:\n%s", stripped)
	}
	if !strings.Contains(stripped, "Do redis stuff.") {
		t.Fatalf("user body should survive the strip:\n%s", stripped)
	}

	// Now feed the stripped body BACK into generateSkillMd and verify we
	// don't accumulate a second memory section. This is the actual
	// regression case — RegenerateAllSkillMds at startup feeds the result
	// of GetSkillPrompt back into generateSkillMd.
	regenerated := svc.generateSkillMd("redis-skill", "Redis investigator", stripped)
	if count := strings.Count(regenerated, "## Cross-incident Memory"); count != 1 {
		t.Fatalf("regeneration produced %d memory sections (want exactly 1):\n%s", count, regenerated)
	}
}

func TestStripAutoGeneratedSections_HandlesMemoryWithoutTools(t *testing.T) {
	body := "User content\n\n## Cross-incident Memory\n\nrecall instructions and manifest"
	got := stripAutoGeneratedSections(body)
	if got != "User content" {
		t.Errorf("got %q, want %q", got, "User content")
	}
}

func TestStripAutoGeneratedSections_HandlesToolsWithoutMemory(t *testing.T) {
	body := "User content\n\n## Assigned Tools\n\ntool list"
	got := stripAutoGeneratedSections(body)
	if got != "User content" {
		t.Errorf("got %q, want %q", got, "User content")
	}
}

// Regression: empty user prompt + auto-generated section. After
// generateSkillMd writes "---\n\n## Cross-incident Memory\n…", GetSkillPrompt
// TrimLefts the body so it begins with "## Cross-incident Memory" — without
// a leading "\n\n" the marker can't anchor and the section survives as
// "user prompt", then accumulates on every regen.
func TestStripAutoGeneratedSections_HandlesMemoryAtBodyStart(t *testing.T) {
	body := "## Cross-incident Memory\n\nrecall instructions and manifest"
	got := stripAutoGeneratedSections(body)
	if got != "" {
		t.Errorf("got %q, want \"\" (entire body was auto-generated)", got)
	}
}

func TestStripAutoGeneratedSections_HandlesToolsAtBodyStart(t *testing.T) {
	body := "## Assigned Tools\n\ntool list"
	got := stripAutoGeneratedSections(body)
	if got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestGenerateSkillMd_RoundTripWithEmptyBody(t *testing.T) {
	// End-to-end regression for the empty-prompt case: write SKILL.md,
	// reverse the GetSkillPrompt parsing pipeline (split + TrimLeft +
	// stripAutoGeneratedSections), then re-feed the result into
	// generateSkillMd. The auto-generated section must NOT accumulate.
	tmp := t.TempDir()
	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx service: %v", err)
	}
	svc := newSkillServiceForPromptTest(t)
	svc.contextService = ctxSvc
	writeManifest(t, svc, "redis-skill", "fresh manifest body")

	// Empty user prompt — generateSkillMd should still emit a memory section,
	// but stripping it on round-trip must leave an empty body.
	first := svc.generateSkillMd("redis-skill", "Redis investigator", "")
	parts := strings.SplitN(first, "---", 3)
	if len(parts) < 3 {
		t.Fatalf("expected frontmatter delimiters, got: %s", first)
	}
	body := strings.TrimLeft(parts[2], " \t\n\r")
	stripped := stripAutoGeneratedSections(body)
	if stripped != "" {
		t.Fatalf("empty-prompt round-trip should strip clean, got: %q", stripped)
	}

	// Feeding the stripped body back must NOT accumulate sections.
	regenerated := svc.generateSkillMd("redis-skill", "Redis investigator", stripped)
	if count := strings.Count(regenerated, "## Cross-incident Memory"); count != 1 {
		t.Errorf("got %d memory sections after re-render (want 1):\n%s", count, regenerated)
	}
	// And a third pass — simulating multiple restarts — still keeps it at one.
	parts2 := strings.SplitN(regenerated, "---", 3)
	body2 := strings.TrimLeft(parts2[2], " \t\n\r")
	stripped2 := stripAutoGeneratedSections(body2)
	regenerated3 := svc.generateSkillMd("redis-skill", "Redis investigator", stripped2)
	if count := strings.Count(regenerated3, "## Cross-incident Memory"); count != 1 {
		t.Errorf("multi-restart accumulation: got %d memory sections, want 1", count)
	}
}

func TestGenerateSkillMd_NoTools_NoToolsSection(t *testing.T) {
	tmp := t.TempDir()
	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx service: %v", err)
	}
	svc := newSkillServiceForPromptTest(t)
	svc.contextService = ctxSvc

	out := svc.generateSkillMd("plain-skill", "Test", "Body content")

	if strings.Contains(out, "## Assigned Tools") {
		t.Errorf("expected no tools section when none assigned, got:\n%s", out)
	}
	// Memory section is always present (recall instruction is always-on).
	if !strings.Contains(out, "## Cross-incident Memory") {
		t.Errorf("expected memory section even with no tools and no manifest")
	}
}
