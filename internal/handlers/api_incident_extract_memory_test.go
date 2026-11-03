package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/services"
	"gorm.io/gorm"
)

type fakeMemoryExtractor struct {
	err   error
	last  string
	since time.Time
}

func (f *fakeMemoryExtractor) Rerun(_ context.Context, uuid string) error {
	f.last = uuid
	return f.err
}

func (f *fakeMemoryExtractor) Backfill(_ context.Context, since time.Time) (int64, error) {
	f.since = since
	return 3, f.err
}

// TestHandleIncidentExtractMemory maps the rerun outcomes to status codes:
// 202 queued, 404 missing, 409 not closed or busy, 503 without the service.
func TestHandleIncidentExtractMemory(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"queued", nil, http.StatusAccepted},
		{"missing", gorm.ErrRecordNotFound, http.StatusNotFound},
		{"not closed", services.ErrExtractionNotClosed, http.StatusConflict},
		{"busy", services.ErrExtractionBusy, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			fake := &fakeMemoryExtractor{err: c.err}
			h.SetMemoryExtractor(fake)
			mux := http.NewServeMux()
			h.SetupRoutes(mux)
			req := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/extract-memory", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body.String())
			}
			if fake.last != "inc-1" {
				t.Errorf("rerun called with %q", fake.last)
			}
		})
	}

	t.Run("backfill", func(t *testing.T) {
		h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		fake := &fakeMemoryExtractor{}
		h.SetMemoryExtractor(fake)
		mux := http.NewServeMux()
		h.SetupRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidents/extract-memory-backfill", strings.NewReader(`{"days": 7}`)))
		if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"queued":3`) {
			t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
		}
		if d := time.Since(fake.since); d < 6*24*time.Hour || d > 8*24*time.Hour {
			t.Errorf("since = %v", fake.since)
		}
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidents/extract-memory-backfill", strings.NewReader(`{"days": 400}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("days 400: status = %d", rec.Code)
		}
	})

	t.Run("no service", func(t *testing.T) {
		h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		mux := http.NewServeMux()
		h.SetupRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/extract-memory", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}
