package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

// statusSkillService embeds corrGateSkillService for all no-op stubs and
// overrides ResolveAlert/CloseIncident with configurable hooks.
type statusSkillService struct {
	corrGateSkillService
	resolveFn func(ctx context.Context, alertUUID string) error
	closeFn   func(ctx context.Context, incidentUUID string, confirm bool) error
}

func (s *statusSkillService) ResolveAlert(ctx context.Context, alertUUID string) error {
	if s.resolveFn != nil {
		return s.resolveFn(ctx, alertUUID)
	}
	return nil
}

func (s *statusSkillService) CloseIncident(ctx context.Context, incidentUUID string, confirm bool) error {
	if s.closeFn != nil {
		return s.closeFn(ctx, incidentUUID, confirm)
	}
	return nil
}

func TestHandleAlertResolve_200_HappyPath(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	var capturedUUID string
	svc := &statusSkillService{
		resolveFn: func(_ context.Context, alertUUID string) error {
			capturedUUID = alertUUID
			return nil
		},
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/alert-1/resolve", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedUUID != "alert-1" {
		t.Errorf("ResolveAlert called with %q, want %q", capturedUUID, "alert-1")
	}
}

func TestHandleAlertResolve_404_NotFound(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	svc := &statusSkillService{
		resolveFn: func(context.Context, string) error { return gorm.ErrRecordNotFound },
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/missing/resolve", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAlertResolve_409_AlreadyResolved(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	svc := &statusSkillService{
		resolveFn: func(context.Context, string) error { return services.ErrAlertAlreadyResolved },
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/alert-1/resolve", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleIncidentClose_200_HappyPath(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	var capturedUUID string
	var capturedConfirm bool
	svc := &statusSkillService{
		closeFn: func(_ context.Context, incidentUUID string, confirm bool) error {
			capturedUUID = incidentUUID
			capturedConfirm = confirm
			return nil
		},
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/close", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedUUID != "inc-1" {
		t.Errorf("CloseIncident called with %q, want %q", capturedUUID, "inc-1")
	}
	if capturedConfirm {
		t.Error("confirm should default to false when omitted from body")
	}
}

func TestHandleIncidentClose_409_InProgressRequiresConfirmation(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	svc := &statusSkillService{
		closeFn: func(_ context.Context, _ string, confirm bool) error {
			if confirm {
				return nil
			}
			return &services.ErrConfirmationRequired{InProgress: true}
		},
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/close", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["in_progress"] != true {
		t.Errorf("in_progress = %v, want true", body["in_progress"])
	}

	// Retry with confirm=true succeeds.
	confirmBody, _ := json.Marshal(map[string]bool{"confirm": true})
	req2 := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/close", bytes.NewReader(confirmBody))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on confirmed retry, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestHandleIncidentClose_409_RequiresConfirmation(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	svc := &statusSkillService{
		closeFn: func(_ context.Context, _ string, confirm bool) error {
			if confirm {
				return nil
			}
			return &services.ErrConfirmationRequired{FiringAlertCount: 3}
		},
	}
	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/close", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["requires_confirmation"] != true {
		t.Errorf("requires_confirmation = %v, want true", body["requires_confirmation"])
	}
	if body["firing_alert_count"] != float64(3) {
		t.Errorf("firing_alert_count = %v, want 3", body["firing_alert_count"])
	}

	// Retry with confirm=true succeeds.
	confirmBody, _ := json.Marshal(map[string]bool{"confirm": true})
	req2 := httptest.NewRequest(http.MethodPost, "/api/incidents/inc-1/close", bytes.NewReader(confirmBody))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on confirmed retry, got %d: %s", rec2.Code, rec2.Body.String())
	}
}
