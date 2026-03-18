package services

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTriggerQMDReindex_Success(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update" {
			t.Errorf("expected path /update, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		called.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"updated"}`))
	}))
	defer server.Close()

	svc := &RunbookService{
		qmdURL:     server.URL,
		httpClient: server.Client(),
	}

	svc.triggerQMDReindex()

	if called.Load() != 1 {
		t.Errorf("expected 1 call to /update, got %d", called.Load())
	}
}

func TestTriggerQMDReindex_NoURL(t *testing.T) {
	// Should be a no-op when qmdURL is empty
	svc := &RunbookService{
		qmdURL:     "",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	// Should not panic or make any HTTP calls
	svc.triggerQMDReindex()
}

func TestTriggerQMDReindex_ServerDown(t *testing.T) {
	// Point to a port that's not listening
	svc := &RunbookService{
		qmdURL:     "http://127.0.0.1:19999",
		httpClient: &http.Client{Timeout: 1 * time.Second},
	}

	// Should not panic - errors are logged but not returned
	svc.triggerQMDReindex()
}

func TestTriggerQMDReindex_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := &RunbookService{
		qmdURL:     server.URL,
		httpClient: server.Client(),
	}

	// Should not panic - non-200 is logged but not returned
	svc.triggerQMDReindex()
}

func TestSetQMDURL(t *testing.T) {
	svc := &RunbookService{}

	svc.SetQMDURL("http://qmd:8181/")
	if svc.qmdURL != "http://qmd:8181" {
		t.Errorf("expected trailing slash trimmed, got %s", svc.qmdURL)
	}

	svc.SetQMDURL("http://qmd:8181")
	if svc.qmdURL != "http://qmd:8181" {
		t.Errorf("expected %s, got %s", "http://qmd:8181", svc.qmdURL)
	}

	svc.SetQMDURL("")
	if svc.qmdURL != "" {
		t.Errorf("expected empty, got %s", svc.qmdURL)
	}
}

func TestTriggerQMDReindex_URLTrailingSlash(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := &RunbookService{
		qmdURL:     server.URL, // SetQMDURL already trims trailing slashes
		httpClient: server.Client(),
	}

	svc.triggerQMDReindex()

	if requestedPath != "/update" {
		t.Errorf("expected /update, got %s", requestedPath)
	}
}
