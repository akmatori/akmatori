package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

func TestHandleMemoryByID_UpdateBodyKeyPresence(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	created, _ := mock.CreateMemory(&database.Memory{Scope: "global", Type: services.MemoryTypeHost, Name: "m", Description: "d", Body: "old body"})
	path := fmt.Sprintf("/api/memories/%d", created.ID)

	// No "body" key: the body stays.
	w := doJSON(t, h, http.MethodPut, path, map[string]string{"description": "new d"})
	if w.Code != http.StatusOK {
		t.Fatalf("partial put status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := mock.lastUpdated.Body; got != "old body" {
		t.Errorf("partial put changed body to %q", got)
	}

	// "body": "" clears it.
	w = doJSON(t, h, http.MethodPut, path, map[string]string{"body": ""})
	if w.Code != http.StatusOK {
		t.Fatalf("clear put status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := mock.lastUpdated.Body; got != "" {
		t.Errorf("clear put left body %q", got)
	}
}

func TestHandleMemories_List_IncidentFilter(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	for _, m := range []*database.Memory{
		{Scope: "global", Type: services.MemoryTypeHost, Name: "a1", Description: "d", IncidentUUID: "inc-a"},
		{Scope: "redis", Type: services.MemoryTypeHost, Name: "a2", Description: "d", IncidentUUID: "inc-a"},
		{Scope: "global", Type: services.MemoryTypeHost, Name: "b1", Description: "d", IncidentUUID: "inc-b"},
	} {
		if _, err := mock.CreateMemory(m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	w := doJSON(t, h, http.MethodGet, "/api/memories?incident_uuid=inc-a", nil)
	var got []database.Memory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("incident filter = %d memories, want 2: %+v", len(got), got)
	}
}

func TestHandleMemoryExport(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock)
	if _, err := mock.CreateMemory(&database.Memory{Scope: "global", Type: services.MemoryTypeHost, Name: "fact", Description: "d", Body: "b"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := doJSON(t, h, http.MethodGet, "/api/memories/export", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="akmatori-memory-`) {
		t.Errorf("content disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "global/fact.md" {
		t.Errorf("zip entries = %v", zr.File)
	}

	if w := doJSON(t, h, http.MethodPost, "/api/memories/export", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST export status = %d, want 405", w.Code)
	}
}

func TestHandleMemoryFromText(t *testing.T) {
	mock := newMockMemoryService()
	h := newMemoryAPIHandler(mock) // no drafter wired: deterministic draft

	w := doJSON(t, h, http.MethodPost, "/api/memories/from-text", MemoryFromTextRequest{Text: "  Redis prod listens on 16379\nnot 6379  "})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got database.Memory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := database.Memory{
		Scope:       services.MemoryScopeGlobal,
		Type:        services.MemoryTypeFeedback,
		Name:        "redis-prod-listens-on-16379",
		Description: "Redis prod listens on 16379",
		Body:        "Redis prod listens on 16379\nnot 6379",
		CreatedBy:   services.MemoryCreatedByOperator,
	}
	if got.Scope != want.Scope || got.Type != want.Type || got.Name != want.Name || got.Description != want.Description || got.Body != want.Body || got.CreatedBy != want.CreatedBy {
		t.Errorf("created = %+v, want %+v", got, want)
	}

	// The same text again does not overwrite the first memory.
	w = doJSON(t, h, http.MethodPost, "/api/memories/from-text", MemoryFromTextRequest{Text: "Redis prod listens on 16379", Scope: services.MemoryScopeGlobal})
	if w.Code != http.StatusCreated {
		t.Fatalf("second status = %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "redis-prod-listens-on-16379-2" {
		t.Errorf("second name = %q", got.Name)
	}

	// A skill scope is kept.
	w = doJSON(t, h, http.MethodPost, "/api/memories/from-text", MemoryFromTextRequest{Text: "use replica for reads", Scope: "postgres"})
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Scope != "postgres" {
		t.Errorf("scoped create = %+v, err %v", got, err)
	}
}

func TestHandleMemoryFromText_Rejects(t *testing.T) {
	h := newMemoryAPIHandler(newMockMemoryService())
	cases := map[string]struct {
		method string
		body   interface{}
		want   int
	}{
		"empty text": {http.MethodPost, MemoryFromTextRequest{Text: "   "}, http.StatusBadRequest},
		"too long":   {http.MethodPost, MemoryFromTextRequest{Text: strings.Repeat("x", services.MemoryTextMaxBytes+1)}, http.StatusBadRequest},
		"GET":        {http.MethodGet, nil, http.StatusMethodNotAllowed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if w := doJSON(t, h, c.method, "/api/memories/from-text", c.body); w.Code != c.want {
				t.Errorf("status = %d, want %d (body %s)", w.Code, c.want, w.Body.String())
			}
		})
	}
}
