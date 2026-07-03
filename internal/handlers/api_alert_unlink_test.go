package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
)

// unlinkSkillService embeds corrGateSkillService for all no-op stubs and
// overrides MoveAlertToIncident (which backs both /unlink and /move) with a
// configurable hook.
type unlinkSkillService struct {
	corrGateSkillService
	moveFn func(ctx context.Context, alertUUID, target string) (string, error)
}

func (s *unlinkSkillService) MoveAlertToIncident(ctx context.Context, alertUUID, target string) (string, error) {
	if s.moveFn != nil {
		return s.moveFn(ctx, alertUUID, target)
	}
	return "new-incident-uuid", nil
}

// seedUnlinkTestAlert inserts an alert row suitable for unlink handler tests.
func seedUnlinkTestAlert(t *testing.T, incidentUUID string, correlated bool) string {
	t.Helper()
	db := database.GetDB()
	alertUUID := uuid.New().String()
	a := database.Alert{
		UUID:         alertUUID,
		IncidentUUID: incidentUUID,
		Status:       database.AlertStatusFiring,
		AlertName:    "HighCPU",
		TargetHost:   "host-01",
		Correlated:   correlated,
	}
	if correlated {
		conf := 0.9
		a.CorrelationConfidence = &conf
		a.CorrelationReasoning = "same host"
		a.CorrelationDecision = "linked"
	} else {
		a.CorrelationDecision = "new_incident"
	}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	return alertUUID
}

func TestHandleAlertUnlink_200_HappyPath(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	incUUID := "inc-" + uuid.New().String()
	alertUUID := seedUnlinkTestAlert(t, incUUID, true)

	newIncidentUUID := "new-" + uuid.New().String()
	var capturedAlertUUID, capturedTarget string
	svc := &unlinkSkillService{
		moveFn: func(ctx context.Context, auuid, target string) (string, error) {
			capturedAlertUUID = auuid
			capturedTarget = target
			return newIncidentUUID, nil
		},
	}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/"+alertUUID+"/unlink", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAlertUUID != alertUUID {
		t.Errorf("MoveAlertToIncident called with UUID %q, want %q", capturedAlertUUID, alertUUID)
	}
	if capturedTarget != "" {
		t.Errorf("unlink should pass empty target, got %q", capturedTarget)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["incident_uuid"] != newIncidentUUID {
		t.Errorf("incident_uuid = %q, want %q", resp["incident_uuid"], newIncidentUUID)
	}
}

// Origin (non-correlated) alerts can now be unlinked too — the old
// "409 not correlated" restriction was removed.
func TestHandleAlertUnlink_200_OriginAlert(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	incUUID := "inc-" + uuid.New().String()
	alertUUID := seedUnlinkTestAlert(t, incUUID, false)

	svc := &unlinkSkillService{
		moveFn: func(ctx context.Context, auuid, target string) (string, error) {
			return "new-incident-uuid", nil
		},
	}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/"+alertUUID+"/unlink", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAlertUnlink_409_ConcurrentMove(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	incUUID := "inc-" + uuid.New().String()
	alertUUID := seedUnlinkTestAlert(t, incUUID, true)

	svc := &unlinkSkillService{
		moveFn: func(ctx context.Context, auuid, target string) (string, error) {
			return "", services.ErrAlertAlreadyMoved
		},
	}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/"+alertUUID+"/unlink", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAlertUnlink_404_NotFound(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	svc := &unlinkSkillService{}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/alerts/nonexistent-uuid/unlink", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The /move endpoint links an alert to an existing incident when a target is
// supplied — no new investigation is spawned and the target is forwarded.
func TestHandleAlertMove_200_LinkToExisting(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	incUUID := "inc-" + uuid.New().String()
	alertUUID := seedUnlinkTestAlert(t, incUUID, true)
	targetUUID := "target-" + uuid.New().String()

	var capturedTarget string
	svc := &unlinkSkillService{
		moveFn: func(ctx context.Context, auuid, target string) (string, error) {
			capturedTarget = target
			return target, nil
		},
	}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	body := strings.NewReader(`{"target_incident_uuid":"` + targetUUID + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/"+alertUUID+"/move", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedTarget != targetUUID {
		t.Errorf("target forwarded = %q, want %q", capturedTarget, targetUUID)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["incident_uuid"] != targetUUID {
		t.Errorf("incident_uuid = %q, want %q", resp["incident_uuid"], targetUUID)
	}
}

func TestHandleAlertMove_400_InvalidTarget(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	incUUID := "inc-" + uuid.New().String()
	alertUUID := seedUnlinkTestAlert(t, incUUID, true)

	svc := &unlinkSkillService{
		moveFn: func(ctx context.Context, auuid, target string) (string, error) {
			return "", services.ErrInvalidMoveTarget
		},
	}

	h := NewAPIHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	body := strings.NewReader(`{"target_incident_uuid":"does-not-exist"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/"+alertUUID+"/move", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
