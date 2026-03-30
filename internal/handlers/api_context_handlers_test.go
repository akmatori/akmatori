package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupContextHandlerTest(t *testing.T) (*APIHandler, *services.ContextService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.ContextFile{}); err != nil {
		t.Fatalf("migrate context_files: %v", err)
	}
	database.DB = db

	ctxSvc, err := services.NewContextService(t.TempDir())
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}

	return NewAPIHandler(nil, nil, ctxSvc, nil, nil, nil, nil, nil, nil, nil), ctxSvc
}

func TestAPIHandler_HandleContextValidate_ReturnsFoundAndMissingReferences(t *testing.T) {
	h, ctxSvc := setupContextHandlerTest(t)

	if _, err := ctxSvc.SaveFile("guide.md", "guide.md", "text/markdown", "", int64(len("guide")), bytes.NewBufferString("guide")); err != nil {
		t.Fatalf("SaveFile guide.md: %v", err)
	}

	body := bytes.NewBufferString(`{"text":"See [[guide.md]] and [[missing.txt]]"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/context/validate", body)
	w := httptest.NewRecorder()

	h.handleContextValidate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		Valid      bool     `json:"valid"`
		References []string `json:"references"`
		Found      []string `json:"found"`
		Missing    []string `json:"missing"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Valid {
		t.Fatal("valid = true, want false when a reference is missing")
	}
	if len(got.References) != 2 || got.References[0] != "guide.md" || got.References[1] != "missing.txt" {
		t.Fatalf("references = %v, want [guide.md missing.txt]", got.References)
	}
	if len(got.Found) != 1 || got.Found[0] != "guide.md" {
		t.Fatalf("found = %v, want [guide.md]", got.Found)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "missing.txt" {
		t.Fatalf("missing = %v, want [missing.txt]", got.Missing)
	}
}

func TestAPIHandler_HandleContextDownload_ServesStoredFile(t *testing.T) {
	h, ctxSvc := setupContextHandlerTest(t)

	stored, err := ctxSvc.SaveFile("guide.md", "guide.md", "text/markdown", "desc", int64(len("hello world")), bytes.NewBufferString("hello world"))
	if err != nil {
		t.Fatalf("SaveFile guide.md: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/context/1/download", nil)
	w := httptest.NewRecorder()

	h.handleContextDownload(w, req, stored.ID)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/markdown" {
		t.Fatalf("Content-Type = %q, want text/markdown", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="guide.md"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if body := w.Body.String(); body != "hello world" {
		t.Fatalf("body = %q, want hello world", body)
	}

	if _, err := os.Stat(filepath.Join(ctxSvc.GetContextDir(), "guide.md")); err != nil {
		t.Fatalf("saved file missing on disk: %v", err)
	}
}
