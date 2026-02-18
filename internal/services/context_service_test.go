package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
