package services

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newContextSectionTestService returns a SkillService whose ContextService is
// backed by an in-memory DB seeded with the given context file rows.
func newContextSectionTestService(t *testing.T, files ...database.ContextFile) *SkillService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.ContextFile{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	tmp := t.TempDir()
	ctxSvc, err := NewContextService(tmp)
	if err != nil {
		t.Fatalf("ctx service: %v", err)
	}
	for i := range files {
		if err := db.Create(&files[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", files[i].Filename, err)
		}
	}
	return &SkillService{
		dataDir:        tmp,
		incidentsDir:   filepath.Join(tmp, "incidents"),
		skillsDir:      filepath.Join(tmp, "skills"),
		memoryDir:      filepath.Join(tmp, "memory"),
		contextService: ctxSvc,
	}
}

func TestGenerateAgentsMd_ListsReferenceFiles(t *testing.T) {
	svc := newContextSectionTestService(t,
		database.ContextFile{Filename: "edge-topology.pdf", Size: 2 * 1024 * 1024, Description: "Edge PoP\nlayout and uplinks"},
		database.ContextFile{Filename: "oncall.md", Size: 512},
	)

	out := filepath.Join(svc.dataDir, "AGENTS.md")
	if err := svc.generateAgentsMd(out, "incident-manager", "test-incident-uuid"); err != nil {
		t.Fatalf("generate AGENTS.md: %v", err)
	}
	got := readFile(t, out)

	for _, want := range []string{
		"## Reference files",
		"mounted read-only at `/akmatori/context/`",
		"- `/akmatori/context/edge-topology.pdf` (2.0 MB) — Edge PoP layout and uplinks\n",
		"- `/akmatori/context/oncall.md` (512 B)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, got)
		}
	}
	if memIdx, refIdx := strings.Index(got, "## Cross-incident Memory"), strings.Index(got, "## Reference files"); memIdx < 0 || refIdx < memIdx {
		t.Errorf("reference files should follow the memory section: memIdx=%d refIdx=%d", memIdx, refIdx)
	}
}

func TestRenderContextFilesSection_EmptyWithoutFiles(t *testing.T) {
	svc := newContextSectionTestService(t)
	if got := svc.renderContextFilesSection(); got != "" {
		t.Errorf("expected no section without files, got:\n%s", got)
	}

	svc.contextService = nil
	if got := svc.renderContextFilesSection(); got != "" {
		t.Errorf("expected no section without a context service, got:\n%s", got)
	}
}

func TestRenderContextFilesSection_CapsFileCount(t *testing.T) {
	var files []database.ContextFile
	for i := 0; i < contextSectionMaxFiles+5; i++ {
		files = append(files, database.ContextFile{Filename: fmt.Sprintf("f%03d.txt", i), Size: 10})
	}
	svc := newContextSectionTestService(t, files...)

	got := svc.renderContextFilesSection()
	if n := strings.Count(got, "\n- `"); n != contextSectionMaxFiles {
		t.Errorf("listed %d files, want %d", n, contextSectionMaxFiles)
	}
	if !strings.Contains(got, "5 more files not listed") {
		t.Errorf("missing truncation note:\n%s", got)
	}
}

func TestRenderContextFilesSection_CapsBytes(t *testing.T) {
	long := strings.Repeat("x", contextSectionDescRunes*2)
	var files []database.ContextFile
	for i := 0; i < 80; i++ {
		files = append(files, database.ContextFile{Filename: fmt.Sprintf("f%03d.txt", i), Size: 10, Description: long})
	}
	svc := newContextSectionTestService(t, files...)

	got := svc.renderContextFilesSection()
	if len(got) > contextSectionMaxBytes+256 {
		t.Errorf("section is %d bytes, want about %d", len(got), contextSectionMaxBytes)
	}
	if !strings.Contains(got, "more files not listed") {
		t.Errorf("missing truncation note:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("x", contextSectionDescRunes+1)) {
		t.Errorf("description was not truncated")
	}
}
