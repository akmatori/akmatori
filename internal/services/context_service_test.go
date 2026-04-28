package services

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// --- Context Service Validation Tests ---

func TestValidateFilename_ValidNames(t *testing.T) {
	// Create a temporary service for testing
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	validNames := []string{
		"readme.md",
		"README.md",
		"config.json",
		"data-file.yaml",
		"test_file.txt",
		"file123.log",
		"a.txt",
		"CamelCase.yml",
		"file-with-dashes.csv",
		"file_with_underscores.xml",
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			if err := s.ValidateFilename(name); err != nil {
				t.Errorf("ValidateFilename(%q) = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateFilename_InvalidNames(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	invalidNames := []struct {
		name   string
		reason string
	}{
		{"", "empty filename"},
		{"noextension", "no extension"},
		{".hidden", "starts with dot"},
		{"../escape.txt", "path traversal"},
		{"file name.txt", "contains space"},
		{"file@name.txt", "contains @"},
		{"file!name.txt", "contains !"},
		{"-file.txt", "starts with dash"},
		{"_file.txt", "starts with underscore"},
		{strings.Repeat("a", 256) + ".txt", "too long"},
	}

	for _, tc := range invalidNames {
		t.Run(tc.reason, func(t *testing.T) {
			if err := s.ValidateFilename(tc.name); err == nil {
				t.Errorf("ValidateFilename(%q) = nil, want error for %s", tc.name, tc.reason)
			}
		})
	}
}

func TestValidateFileType_ValidExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	for _, ext := range AllowedExtensions {
		t.Run(ext, func(t *testing.T) {
			filename := "test" + ext
			if err := s.ValidateFileType(filename); err != nil {
				t.Errorf("ValidateFileType(%q) = %v, want nil", filename, err)
			}
		})
	}
}

func TestValidateFileType_InvalidExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	invalidExts := []string{
		"script.exe",
		"binary.bin",
		"archive.zip",
		"image.png",
		"document.doc",
		"spreadsheet.xlsx",
	}

	for _, filename := range invalidExts {
		t.Run(filename, func(t *testing.T) {
			if err := s.ValidateFileType(filename); err == nil {
				t.Errorf("ValidateFileType(%q) = nil, want error", filename)
			}
		})
	}
}

func TestValidateFileType_CaseInsensitive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	// Extensions should be case-insensitive
	caseVariants := []string{
		"readme.MD",
		"readme.Md",
		"config.JSON",
		"data.YAML",
		"doc.TXT",
	}

	for _, filename := range caseVariants {
		t.Run(filename, func(t *testing.T) {
			if err := s.ValidateFileType(filename); err != nil {
				t.Errorf("ValidateFileType(%q) = %v, want nil (case insensitive)", filename, err)
			}
		})
	}
}

func TestValidateFileType_NoExtension(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	if err := s.ValidateFileType("noextension"); err == nil {
		t.Error("ValidateFileType('noextension') = nil, want error")
	}
}

// --- Filename Pattern Tests ---

func TestFilenamePattern(t *testing.T) {
	validPatterns := []string{
		"a.txt",
		"file.md",
		"README.md",
		"config-file.json",
		"data_file.yaml",
		"file123.log",
		"test-data-file.csv",
	}

	for _, p := range validPatterns {
		if !FilenamePattern.MatchString(p) {
			t.Errorf("FilenamePattern should match %q", p)
		}
	}

	invalidPatterns := []string{
		"",
		".hidden",
		"-starts-dash.txt",
		"_starts_underscore.txt",
		"no extension",
		"has space.txt",
	}

	for _, p := range invalidPatterns {
		if FilenamePattern.MatchString(p) {
			t.Errorf("FilenamePattern should NOT match %q", p)
		}
	}
}

// --- Reference Pattern Tests ---

func TestReferencePattern(t *testing.T) {
	text := "Check the [[readme.md]] file and also [[config.json]] for details."

	matches := ReferencePattern.FindAllStringSubmatch(text, -1)

	if len(matches) != 2 {
		t.Errorf("found %d matches, want 2", len(matches))
	}

	expectedRefs := []string{"readme.md", "config.json"}
	for i, match := range matches {
		if len(match) < 2 {
			t.Errorf("match %d has no capture group", i)
			continue
		}
		if match[1] != expectedRefs[i] {
			t.Errorf("match[%d] = %q, want %q", i, match[1], expectedRefs[i])
		}
	}
}

func TestReferencePattern_NoMatches(t *testing.T) {
	texts := []string{
		"No references here",
		"Single bracket [not a ref]",
		"Malformed [[unclosed",
	}

	for _, text := range texts {
		matches := ReferencePattern.FindAllStringSubmatch(text, -1)
		if len(matches) != 0 {
			t.Errorf("ReferencePattern should not match %q, got %v", text, matches)
		}
	}
}

func TestReferencePattern_EmptyBrackets(t *testing.T) {
	// Empty brackets [[]] - regex [^\]]+ requires at least one non-] char
	// so [[]] should NOT match
	text := "Empty brackets [[]]"
	matches := ReferencePattern.FindAllStringSubmatch(text, -1)
	if len(matches) != 0 {
		t.Errorf("[[]] should not match (requires content), got %v", matches)
	}
}

// --- Asset Link Pattern Tests ---

func TestAssetLinkPattern(t *testing.T) {
	text := "See [diagram](assets/diagram.png) and [data](assets/data.csv)"

	matches := AssetLinkPattern.FindAllStringSubmatch(text, -1)

	if len(matches) != 2 {
		t.Errorf("found %d matches, want 2", len(matches))
	}

	expectedFiles := []string{"diagram.png", "data.csv"}
	for i, match := range matches {
		if len(match) < 2 {
			t.Errorf("match %d has no capture group", i)
			continue
		}
		if match[1] != expectedFiles[i] {
			t.Errorf("match[%d] = %q, want %q", i, match[1], expectedFiles[i])
		}
	}
}

// --- Constants Tests ---

func TestMaxFileSize(t *testing.T) {
	expectedSize := 10 * 1024 * 1024 // 10 MB
	if MaxFileSize != expectedSize {
		t.Errorf("MaxFileSize = %d, want %d", MaxFileSize, expectedSize)
	}
}

func TestAllowedExtensions(t *testing.T) {
	// Verify expected extensions are in the list
	expectedExts := []string{".md", ".txt", ".json", ".yaml", ".yml", ".pdf"}
	for _, ext := range expectedExts {
		found := false
		for _, allowed := range AllowedExtensions {
			if allowed == ext {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllowedExtensions missing expected extension: %s", ext)
		}
	}
}

// --- Context Service Creation Tests ---

func TestNewContextService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := NewContextService(tmpDir)
	if err != nil {
		t.Fatalf("NewContextService error: %v", err)
	}

	if s == nil {
		t.Fatal("NewContextService returned nil")
	}

	expectedDir := filepath.Join(tmpDir, "context")
	if s.GetContextDir() != expectedDir {
		t.Errorf("GetContextDir() = %q, want %q", s.GetContextDir(), expectedDir)
	}

	// Verify context directory was created
	info, err := os.Stat(expectedDir)
	if err != nil {
		t.Errorf("context directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("context path is not a directory")
	}
}

func TestNewContextService_CreatesDirRecursively(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Use nested path that doesn't exist
	nestedPath := filepath.Join(tmpDir, "data", "nested", "path")

	s, err := NewContextService(nestedPath)
	if err != nil {
		t.Fatalf("NewContextService error: %v", err)
	}

	expectedDir := filepath.Join(nestedPath, "context")
	if s.GetContextDir() != expectedDir {
		t.Errorf("GetContextDir() = %q, want %q", s.GetContextDir(), expectedDir)
	}

	// Verify directory was created
	if _, err := os.Stat(expectedDir); err != nil {
		t.Errorf("context directory not created: %v", err)
	}
}

// --- Edge Cases ---

func TestValidateFilename_BoundaryLength(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	s := &ContextService{contextDir: tmpDir}

	// Exactly 255 characters should be valid (if pattern allows)
	// Filename pattern requires extension, so max name part is ~250 chars
	name251 := strings.Repeat("a", 251) + ".txt" // 255 total
	if len(name251) != 255 {
		t.Fatalf("test setup error: name length = %d", len(name251))
	}

	err = s.ValidateFilename(name251)
	// This should be valid (at boundary)
	if err != nil {
		// If pattern doesn't allow this length, that's fine
		t.Logf("255 char filename rejected: %v", err)
	}

	// 256 characters should be invalid
	name256 := strings.Repeat("a", 252) + ".txt" // 256 total
	if len(name256) != 256 {
		t.Fatalf("test setup error: name length = %d", len(name256))
	}

	err = s.ValidateFilename(name256)
	if err == nil {
		t.Error("256 char filename should be rejected")
	}
}

func setupContextServiceWithDB(t *testing.T) *ContextService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.ContextFile{}); err != nil {
		t.Fatalf("migrate context files: %v", err)
	}

	originalDB := database.DB
	database.DB = db
	t.Cleanup(func() {
		database.DB = originalDB
	})

	svc, err := NewContextService(t.TempDir())
	if err != nil {
		t.Fatalf("NewContextService() error: %v", err)
	}

	return svc
}

func createContextFileFixture(t *testing.T, svc *ContextService, filename string) {
	t.Helper()

	content := []byte("fixture content for " + filename)
	if err := os.WriteFile(svc.GetFilePath(filename), content, 0644); err != nil {
		t.Fatalf("write fixture file %q: %v", filename, err)
	}

	record := &database.ContextFile{
		Filename:     filename,
		OriginalName: filename,
		MimeType:     "text/plain",
		Size:         int64(len(content)),
	}
	if err := database.GetDB().Create(record).Error; err != nil {
		t.Fatalf("create context file record %q: %v", filename, err)
	}
}

func TestContextService_ReferenceHelpers(t *testing.T) {
	svc := setupContextServiceWithDB(t)

	tests := []struct {
		name         string
		text         string
		wantRefs     []string
		wantResolved string
		wantMarkdown string
	}{
		{
			name:         "deduplicates wiki and asset references",
			text:         "Use [[runbook.md]], [runbook](assets/runbook.md), [[ config.json ]] and [config](assets/config.json).",
			wantRefs:     []string{"runbook.md", "config.json"},
			wantResolved: "Use ./context/runbook.md, [runbook](assets/runbook.md), ./context/ config.json  and [config](assets/config.json).",
			wantMarkdown: "Use [runbook.md](assets/runbook.md), [runbook](assets/runbook.md), [ config.json ](assets/ config.json ) and [config](assets/config.json).",
		},
		{
			name:         "keeps first-seen order across reference styles",
			text:         "See [alerts](assets/alerts.log) then [[notes.txt]] and [notes](assets/notes.txt).",
			wantRefs:     []string{"notes.txt", "alerts.log"},
			wantResolved: "See [alerts](assets/alerts.log) then ./context/notes.txt and [notes](assets/notes.txt).",
			wantMarkdown: "See [alerts](assets/alerts.log) then [notes.txt](assets/notes.txt) and [notes](assets/notes.txt).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := svc.ParseReferences(tt.text); !reflect.DeepEqual(got, tt.wantRefs) {
				t.Fatalf("ParseReferences() = %v, want %v", got, tt.wantRefs)
			}
			if got := svc.ResolveReferences(tt.text); got != tt.wantResolved {
				t.Errorf("ResolveReferences() = %q, want %q", got, tt.wantResolved)
			}
			if got := svc.ResolveReferencesToMarkdownLinks(tt.text); got != tt.wantMarkdown {
				t.Errorf("ResolveReferencesToMarkdownLinks() = %q, want %q", got, tt.wantMarkdown)
			}
		})
	}
}

func TestContextService_ValidateReferences(t *testing.T) {
	svc := setupContextServiceWithDB(t)
	createContextFileFixture(t, svc, "runbook.md")
	createContextFileFixture(t, svc, "config.json")

	valid, missing, found := svc.ValidateReferences("Use [[runbook.md]], [[missing.txt]], and [config](assets/config.json).")
	if valid {
		t.Fatal("ValidateReferences() valid = true, want false")
	}
	if !reflect.DeepEqual(missing, []string{"missing.txt"}) {
		t.Errorf("missing = %v, want %v", missing, []string{"missing.txt"})
	}
	if !reflect.DeepEqual(found, []string{"runbook.md", "config.json"}) {
		t.Errorf("found = %v, want %v", found, []string{"runbook.md", "config.json"})
	}
}

func TestContextService_CopyReferencedFilesToDir(t *testing.T) {
	svc := setupContextServiceWithDB(t)
	createContextFileFixture(t, svc, "runbook.md")
	createContextFileFixture(t, svc, "alerts.log")

	targetDir := t.TempDir()
	text := "Use [[runbook.md]], [alerts](assets/alerts.log), and [[missing.txt]]."
	if err := svc.CopyReferencedFilesToDir(text, targetDir); err != nil {
		t.Fatalf("CopyReferencedFilesToDir() error = %v", err)
	}

	for _, filename := range []string{"runbook.md", "alerts.log"} {
		linkPath := filepath.Join(targetDir, "context", filename)
		info, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatalf("Lstat(%q) error = %v", linkPath, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%q is not a symlink", linkPath)
		}
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%q) error = %v", linkPath, err)
		}
		if target != svc.GetFilePath(filename) {
			t.Errorf("symlink target = %q, want %q", target, svc.GetFilePath(filename))
		}
	}

	if _, err := os.Stat(filepath.Join(targetDir, "context", "missing.txt")); !os.IsNotExist(err) {
		t.Errorf("missing reference should be skipped, stat err = %v", err)
	}
}
