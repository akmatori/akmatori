package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRunbookServiceTest(t *testing.T) (*RunbookService, string) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.Runbook{}); err != nil {
		t.Fatalf("migrate runbooks: %v", err)
	}
	database.DB = db

	dataDir := t.TempDir()
	svc := NewRunbookService(dataDir)
	return svc, filepath.Join(dataDir, "runbooks")
}

func TestRunbookService_CreateRunbook_SyncsTrimmedTitleToDisk(t *testing.T) {
	svc, runbooksDir := setupRunbookServiceTest(t)

	runbook, err := svc.CreateRunbook("  API outage playbook  ", "Step 1\nStep 2")
	if err != nil {
		t.Fatalf("CreateRunbook() error = %v", err)
	}

	if runbook.Title != "API outage playbook" {
		t.Fatalf("CreateRunbook() title = %q, want %q", runbook.Title, "API outage playbook")
	}

	stored, err := svc.GetRunbook(runbook.ID)
	if err != nil {
		t.Fatalf("GetRunbook() error = %v", err)
	}
	if stored.Content != "Step 1\nStep 2" {
		t.Fatalf("GetRunbook() content = %q, want original content", stored.Content)
	}

	files, err := os.ReadDir(runbooksDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", runbooksDir, err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 runbook file, got %d", len(files))
	}
	if files[0].Name() != "1-api-outage-playbook.md" {
		t.Fatalf("runbook filename = %q, want %q", files[0].Name(), "1-api-outage-playbook.md")
	}

	content, err := os.ReadFile(filepath.Join(runbooksDir, files[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	wantContent := "# API outage playbook\n\nStep 1\nStep 2\n"
	if string(content) != wantContent {
		t.Fatalf("runbook file content = %q, want %q", string(content), wantContent)
	}
}

func TestRunbookService_CreateAndUpdateRunbook_ValidationAndErrors(t *testing.T) {
	svc, _ := setupRunbookServiceTest(t)

	createTests := []struct {
		name        string
		title       string
		content     string
		errContains string
	}{
		{name: "empty title", title: "   ", content: "steps", errContains: "title cannot be empty"},
		{name: "empty content", title: "Runbook", content: "  \n\t ", errContains: "content cannot be empty"},
	}

	for _, tt := range createTests {
		t.Run("create/"+tt.name, func(t *testing.T) {
			_, err := svc.CreateRunbook(tt.title, tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("CreateRunbook() error = %v, want substring %q", err, tt.errContains)
			}
		})
	}

	runbook, err := svc.CreateRunbook("Initial title", "Initial content")
	if err != nil {
		t.Fatalf("CreateRunbook() seed error = %v", err)
	}

	updateTests := []struct {
		name        string
		id          uint
		title       string
		content     string
		errContains string
	}{
		{name: "missing runbook", id: runbook.ID + 100, title: "Updated", content: "Content", errContains: "runbook not found"},
		{name: "empty updated title", id: runbook.ID, title: "   ", content: "Content", errContains: "title cannot be empty"},
		{name: "empty updated content", id: runbook.ID, title: "Updated", content: " \n", errContains: "content cannot be empty"},
	}

	for _, tt := range updateTests {
		t.Run("update/"+tt.name, func(t *testing.T) {
			_, err := svc.UpdateRunbook(tt.id, tt.title, tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("UpdateRunbook() error = %v, want substring %q", err, tt.errContains)
			}
		})
	}
}

func TestRunbookService_UpdateAndDeleteRunbook_RefreshesFiles(t *testing.T) {
	svc, runbooksDir := setupRunbookServiceTest(t)

	runbook, err := svc.CreateRunbook("Old title", "Old content")
	if err != nil {
		t.Fatalf("CreateRunbook() error = %v", err)
	}

	updated, err := svc.UpdateRunbook(runbook.ID, "  New title  ", "New content")
	if err != nil {
		t.Fatalf("UpdateRunbook() error = %v", err)
	}
	if updated.Title != "New title" {
		t.Fatalf("UpdateRunbook() title = %q, want %q", updated.Title, "New title")
	}

	oldPath := filepath.Join(runbooksDir, "1-old-title.md")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old runbook file %q to be removed, stat err = %v", oldPath, err)
	}

	newPath := filepath.Join(runbooksDir, "1-new-title.md")
	content, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", newPath, err)
	}
	if string(content) != "# New title\n\nNew content\n" {
		t.Fatalf("updated runbook file content = %q", string(content))
	}

	if err := svc.DeleteRunbook(runbook.ID); err != nil {
		t.Fatalf("DeleteRunbook() error = %v", err)
	}

	files, err := os.ReadDir(runbooksDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", runbooksDir, err)
	}
	if len(files) != 0 {
		t.Fatalf("expected runbooks directory to be empty after delete, got %d file(s)", len(files))
	}

	if _, err := svc.GetRunbook(runbook.ID); err == nil || !strings.Contains(err.Error(), "runbook not found") {
		t.Fatalf("GetRunbook() after delete error = %v, want runbook not found", err)
	}

	if err := svc.DeleteRunbook(runbook.ID); err == nil || !strings.Contains(err.Error(), "runbook not found") {
		t.Fatalf("DeleteRunbook() second call error = %v, want runbook not found", err)
	}
}

func TestRunbookService_ListRunbooks_ReturnsAlphabeticalTitles(t *testing.T) {
	svc, _ := setupRunbookServiceTest(t)

	for _, title := range []string{"zeta rollback", "Alpha incident", "beta deploy"} {
		if _, err := svc.CreateRunbook(title, title+" content"); err != nil {
			t.Fatalf("CreateRunbook(%q) error = %v", title, err)
		}
	}

	runbooks, err := svc.ListRunbooks()
	if err != nil {
		t.Fatalf("ListRunbooks() error = %v", err)
	}

	gotTitles := []string{runbooks[0].Title, runbooks[1].Title, runbooks[2].Title}
	wantTitles := []string{"Alpha incident", "beta deploy", "zeta rollback"}
	for i := range wantTitles {
		if gotTitles[i] != wantTitles[i] {
			t.Fatalf("ListRunbooks() titles = %v, want %v", gotTitles, wantTitles)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercases and replaces separators", input: "CPU / Memory Runbook", want: "cpu-memory-runbook"},
		{name: "trims repeated non alphanumeric edges", input: "---API outage!!!---", want: "api-outage"},
		{name: "empty fallback", input: "🔥🔥🔥", want: "runbook"},
		{name: "long title trims trailing hyphen", input: strings.Repeat("abc-", 30), want: strings.TrimRight(strings.Repeat("abc-", 25), "-")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slugify(tt.input); got != tt.want {
				t.Fatalf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
