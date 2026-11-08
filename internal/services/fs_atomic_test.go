package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new content"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new content" {
		t.Fatalf("content %q err %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	// A missing directory fails without creating anything.
	if err := writeFileAtomic(filepath.Join(dir, "missing", "x"), []byte("x"), 0644); err == nil {
		t.Error("write into a missing directory succeeded")
	}
}
