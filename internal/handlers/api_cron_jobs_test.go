package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// mockCronJobManager is a recording stub for services.CronJobManager. It
// keeps tests free from sqlite + scheduler setup so the API surface itself
// (routing, status codes, JSON shapes) is what's under test.
type mockCronJobManager struct {
	jobs []database.CronJob

	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	runErr    error

	lastCreated *database.CronJob
	lastPatch   *services.CronJobUpdate
	lastRunUUID string
}

func (m *mockCronJobManager) ListJobs() ([]database.CronJob, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.jobs, nil
}

func (m *mockCronJobManager) GetJobByUUID(uuid string) (*database.CronJob, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for i := range m.jobs {
		if m.jobs[i].UUID == uuid {
			out := m.jobs[i]
			return &out, nil
		}
	}
	return nil, services.ErrCronJobNotFound
}

func (m *mockCronJobManager) CreateJob(name, description, schedule, prompt string, mode database.CronJobMode, channelUUID string, enabled bool) (*database.CronJob, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	row := &database.CronJob{
		UUID:        "uuid-" + name,
		Name:        name,
		Description: description,
		Schedule:    schedule,
		Prompt:      prompt,
		Mode:        mode,
		Enabled:     enabled,
	}
	m.lastCreated = row
	m.jobs = append(m.jobs, *row)
	return row, nil
}

func (m *mockCronJobManager) UpdateJob(uuid string, patch services.CronJobUpdate) (*database.CronJob, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	m.lastPatch = &patch
	for i := range m.jobs {
		if m.jobs[i].UUID == uuid {
			if patch.Name != nil {
				m.jobs[i].Name = *patch.Name
			}
			if patch.Schedule != nil {
				m.jobs[i].Schedule = *patch.Schedule
			}
			out := m.jobs[i]
			return &out, nil
		}
	}
	return nil, services.ErrCronJobNotFound
}

func (m *mockCronJobManager) DeleteJob(uuid string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for i := range m.jobs {
		if m.jobs[i].UUID == uuid {
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			return nil
		}
	}
	return services.ErrCronJobNotFound
}

func (m *mockCronJobManager) RunNow(uuid string) error {
	m.lastRunUUID = uuid
	if m.runErr != nil {
		return m.runErr
	}
	for _, j := range m.jobs {
		if j.UUID == uuid {
			return nil
		}
	}
	return services.ErrCronJobNotFound
}

func newHandlerWithCronManager(mgr services.CronJobManager) *APIHandler {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetCronJobManager(mgr)
	return h
}

// ===== happy paths =====

func TestHandleCronJobs_ServiceUnavailable(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs", nil)
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleCronJobs_List(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1", Name: "Daily"}}}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs", nil)
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []database.CronJob
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "u1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestHandleCronJobs_Create(t *testing.T) {
	mgr := &mockCronJobManager{}
	h := newHandlerWithCronManager(mgr)

	body, _ := json.Marshal(CreateCronJobRequest{
		Name:     "Daily",
		Schedule: "0 9 * * *",
		Prompt:   "Report",
		Mode:     "oneshot",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastCreated == nil || mgr.lastCreated.Name != "Daily" {
		t.Fatalf("CreateJob not invoked correctly: %+v", mgr.lastCreated)
	}
	if mgr.lastCreated.Mode != database.CronJobModeOneshot {
		t.Errorf("mode not propagated: %q", mgr.lastCreated.Mode)
	}
}

func TestHandleCronJobs_Create_InvalidSchedule(t *testing.T) {
	mgr := &mockCronJobManager{createErr: services.ErrInvalidCronSchedule}
	h := newHandlerWithCronManager(mgr)

	body, _ := json.Marshal(CreateCronJobRequest{
		Name:     "Bad",
		Schedule: "not cron",
		Prompt:   "Report",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCronJobs_Create_MissingChannel(t *testing.T) {
	mgr := &mockCronJobManager{createErr: services.ErrChannelNotFound}
	h := newHandlerWithCronManager(mgr)

	body, _ := json.Marshal(CreateCronJobRequest{
		Name:        "X",
		Schedule:    "0 9 * * *",
		Prompt:      "Report",
		ChannelUUID: "missing",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing channel, got %d", w.Code)
	}
}

func TestHandleCronJobByUUID_Get(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1", Name: "Daily"}}}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs/u1", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleCronJobByUUID_NotFound(t *testing.T) {
	mgr := &mockCronJobManager{}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs/ghost", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleCronJobByUUID_Update(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1", Name: "Daily"}}}
	h := newHandlerWithCronManager(mgr)

	body, _ := json.Marshal(UpdateCronJobRequest{Schedule: ptr("*/15 * * * *")})
	req := httptest.NewRequest(http.MethodPut, "/api/cron-jobs/u1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastPatch == nil || mgr.lastPatch.Schedule == nil || *mgr.lastPatch.Schedule != "*/15 * * * *" {
		t.Fatalf("schedule patch not propagated: %+v", mgr.lastPatch)
	}
}

func TestHandleCronJobByUUID_Delete(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1"}}}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/cron-jobs/u1", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestHandleCronJobByUUID_RunNow(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1"}}}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs/u1/run", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastRunUUID != "u1" {
		t.Errorf("RunNow uuid = %q, want u1", mgr.lastRunUUID)
	}
}

func TestHandleCronJobByUUID_RunNow_NotFound(t *testing.T) {
	mgr := &mockCronJobManager{runErr: services.ErrCronJobNotFound}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs/ghost/run", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleCronJobByUUID_RunNow_WrongMethod(t *testing.T) {
	mgr := &mockCronJobManager{}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs/u1/run", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleCronJobs_Create_InternalErrorSurface(t *testing.T) {
	mgr := &mockCronJobManager{createErr: errors.New("create cron job: db down")}
	h := newHandlerWithCronManager(mgr)

	body, _ := json.Marshal(CreateCronJobRequest{Name: "X", Schedule: "0 9 * * *", Prompt: "p"})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for wrapped DB error, got %d", w.Code)
	}
}

func ptr[T any](v T) *T { return &v }

// TestHandleCronJobs_List_ServiceError surfaces ListJobs failures as 500.
func TestHandleCronJobs_List_ServiceError(t *testing.T) {
	mgr := &mockCronJobManager{listErr: errors.New("db down")}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs", nil)
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestHandleCronJobs_Create_InvalidJSON guards against malformed payloads.
func TestHandleCronJobs_Create_InvalidJSON(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleCronJobs_MethodNotAllowed rejects unsupported verbs.
func TestHandleCronJobs_MethodNotAllowed(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{})
	req := httptest.NewRequest(http.MethodPatch, "/api/cron-jobs", nil)
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_MethodNotAllowed rejects unsupported verbs on the
// per-row endpoint.
func TestHandleCronJobByUUID_MethodNotAllowed(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{jobs: []database.CronJob{{UUID: "u1"}}})
	req := httptest.NewRequest(http.MethodPatch, "/api/cron-jobs/u1", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_UnknownSubpath returns 404 when the suffix is not
// one of the registered sub-routes.
func TestHandleCronJobByUUID_UnknownSubpath(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{})
	req := httptest.NewRequest(http.MethodPost, "/api/cron-jobs/u1/halt", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_EmptyUUID rejects requests with an empty path
// segment.
func TestHandleCronJobByUUID_EmptyUUID(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{})
	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs/", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_Update_InvalidJSON guards malformed PUT payloads.
func TestHandleCronJobByUUID_Update_InvalidJSON(t *testing.T) {
	h := newHandlerWithCronManager(&mockCronJobManager{jobs: []database.CronJob{{UUID: "u1"}}})
	req := httptest.NewRequest(http.MethodPut, "/api/cron-jobs/u1", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_Update_PropagatesModePatch verifies the API maps the
// mode field through to the service layer.
func TestHandleCronJobByUUID_Update_PropagatesModePatch(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{UUID: "u1", Name: "X", Mode: database.CronJobModeOneshot}}}
	h := newHandlerWithCronManager(mgr)

	mode := "agent"
	body, _ := json.Marshal(UpdateCronJobRequest{Mode: &mode})
	req := httptest.NewRequest(http.MethodPut, "/api/cron-jobs/u1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastPatch == nil || mgr.lastPatch.Mode == nil || *mgr.lastPatch.Mode != database.CronJobModeAgent {
		t.Errorf("mode patch not propagated: %+v", mgr.lastPatch)
	}
}

// TestHandleCronJobByUUID_Delete_NotFound surfaces ErrCronJobNotFound as 404.
func TestHandleCronJobByUUID_Delete_NotFound(t *testing.T) {
	mgr := &mockCronJobManager{deleteErr: services.ErrCronJobNotFound}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/cron-jobs/ghost", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestHandleCronJobByUUID_ServiceUnavailable returns 503 when the cron service
// is unset.
func TestHandleCronJobByUUID_ServiceUnavailable(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs/u1", nil)
	w := httptest.NewRecorder()
	h.handleCronJobByUUID(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestHandleCronJobs_ListMasksIntegrationCredentials asserts that
// /api/cron-jobs does not echo plaintext Slack tokens back to the client.
// The model layer eagerly preloads Channel.Integration via the runner, and
// Integration.Credentials is a JSONB blob — without explicit masking the
// bot_token / signing_secret / app_token would land on the wire.
func TestHandleCronJobs_ListMasksIntegrationCredentials(t *testing.T) {
	mgr := &mockCronJobManager{jobs: []database.CronJob{{
		UUID: "u1",
		Name: "Daily",
		Channel: &database.Channel{
			ID:            10,
			UUID:          "ch1",
			IntegrationID: 5,
			ExternalID:    "C12345",
			DisplayName:   "#alerts",
			Integration: database.Integration{
				ID:       5,
				UUID:     "intg-1",
				Provider: database.MessagingProviderSlack,
				Name:     "Slack",
				Credentials: database.JSONB{
					"bot_token":      "xoxb-secret-token",
					"signing_secret": "sssh",
					"app_token":      "xapp-token",
				},
			},
		},
	}}}
	h := newHandlerWithCronManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/cron-jobs", nil)
	w := httptest.NewRecorder()
	h.handleCronJobs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	for _, secret := range []string{"xoxb-secret-token", "sssh", "xapp-token"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked secret %q: %s", secret, body)
		}
	}

	var got []cronJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Channel == nil || got[0].Channel.Integration == nil {
		t.Fatalf("expected one row with integration, got %+v", got)
	}
	maskedToken, _ := got[0].Channel.Integration.Credentials["bot_token"].(string)
	if maskedToken == "xoxb-secret-token" {
		t.Errorf("bot_token not masked: %q", maskedToken)
	}
}
