package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

	return NewAPIHandler(nil, nil, ctxSvc, nil, nil, nil, nil, nil, nil, nil, nil), ctxSvc
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

// fakeContextSummaries records calls from the context handlers.
type fakeContextSummaries struct {
	enqueued  []uint
	forgotten []uint
	ids       map[uint]uint
}

func (f *fakeContextSummaries) Enqueue(id uint) { f.enqueued = append(f.enqueued, id) }
func (f *fakeContextSummaries) Forget(id uint) error {
	f.forgotten = append(f.forgotten, id)
	return nil
}
func (f *fakeContextSummaries) SummaryMemoryIDs([]uint) (map[uint]uint, error) {
	return f.ids, nil
}

func multipartUpload(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.WriteField("filename", filename); err != nil {
		t.Fatalf("field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/context", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIHandler_ContextSummaries(t *testing.T) {
	h, ctxSvc := setupContextHandlerTest(t)
	sums := &fakeContextSummaries{}
	h.SetContextSummaries(sums)

	// Upload enqueues a summary and answers with status pending.
	w := httptest.NewRecorder()
	h.handleContext(w, multipartUpload(t, "notes.md", []byte("# notes")))
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	var created database.ContextFile
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sums.enqueued) != 1 || sums.enqueued[0] != created.ID || created.SummaryStatus != database.ContextSummaryPending {
		t.Fatalf("enqueued = %v, file = %+v", sums.enqueued, created)
	}

	// The list shows the summary memory link.
	sums.ids = map[uint]uint{created.ID: 42}
	w = httptest.NewRecorder()
	h.handleContext(w, httptest.NewRequest(http.MethodGet, "/api/context", nil))
	var list []database.ContextFile
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].SummaryMemoryID == nil || *list[0].SummaryMemoryID != 42 {
		t.Fatalf("list = %+v", list)
	}

	// Summarize runs the job again; an unknown file is 404.
	w = httptest.NewRecorder()
	h.handleContextByID(w, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/context/%d/summarize", created.ID), nil))
	if w.Code != http.StatusAccepted || len(sums.enqueued) != 2 {
		t.Fatalf("summarize status = %d, enqueued = %v", w.Code, sums.enqueued)
	}
	w = httptest.NewRecorder()
	h.handleContextByID(w, httptest.NewRequest(http.MethodPost, "/api/context/999/summarize", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("summarize unknown status = %d", w.Code)
	}

	// Delete removes the file's memories.
	w = httptest.NewRecorder()
	h.handleContextByID(w, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/context/%d", created.ID), nil))
	if w.Code != http.StatusNoContent || len(sums.forgotten) != 1 || sums.forgotten[0] != created.ID {
		t.Fatalf("delete status = %d, forgotten = %v", w.Code, sums.forgotten)
	}
	if ctxSvc.FileExists("notes.md") {
		t.Error("file still exists")
	}
}

func TestAPIHandler_ContextUploadTooLarge(t *testing.T) {
	h, ctxSvc := setupContextHandlerTest(t)
	w := httptest.NewRecorder()
	h.handleContext(w, multipartUpload(t, "big.txt", bytes.Repeat([]byte("a"), services.MaxFileSize+contextUploadFormOverhead+1)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if ctxSvc.FileExists("big.txt") {
		t.Error("oversized file was stored")
	}
}
