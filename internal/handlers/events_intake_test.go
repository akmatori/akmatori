package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/alerts/extraction"
	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

// eventsSkillService records what the alert path did with an event-linked
// alert, on top of the correlation-gate stub.
type eventsSkillService struct {
	*corrGateSkillService
	mu             sync.Mutex
	insertedEvents []string
	spawnContexts  []*services.IncidentContext
}

func (s *eventsSkillService) InsertFiringAlert(_ context.Context, _ string, _ string, a alerts.NormalizedAlert, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insertedEvents = append(s.insertedEvents, a.EventUUID)
	return nil
}

func (s *eventsSkillService) SpawnIncidentManager(ctx *services.IncidentContext) (string, string, error) {
	s.mu.Lock()
	s.spawnContexts = append(s.spawnContexts, ctx)
	s.mu.Unlock()
	return s.corrGateSkillService.SpawnIncidentManager(ctx)
}

func setupEventsHandlerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{},
		&database.Alert{},
		&database.Event{},
		&database.LLMSettings{},
		&database.SlackSettings{},
		&database.GeneralSettings{},
	)
	if err := db.Create(&database.LLMSettings{
		Name: "test", Provider: database.LLMProviderAnthropic, APIKey: "test-key",
		Model: "claude-sonnet-4-6", Active: true, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed LLMSettings: %v", err)
	}
	return db
}

func testWebhookInstance() *database.AlertSourceInstance {
	return &database.AlertSourceInstance{
		UUID: "src-uuid", Name: "prom", Enabled: true,
		AlertSourceType: database.AlertSourceType{Name: "alertmanager", DisplayName: "Alertmanager"},
	}
}

func TestIngestWebhookItem_ChangeIsStoredNotInvestigated(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"kind":"change","confidence":0.95,"reasoning":"deploy notice"}`, nil
	}}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetEventService(services.NewEventService(db, caller))

	item := newCorrTestAlert()
	item.AlertName = "DeployFinished"
	item.SourceFingerprint = "fp-deploy"
	h.ingestWebhookItem(testWebhookInstance(), item)

	if svc.getSpawnCount() != 0 {
		t.Fatalf("spawns = %d, want 0 for a change event", svc.getSpawnCount())
	}
	var events []database.Event
	db.Find(&events)
	if len(events) != 1 || events[0].Kind != database.EventKindChange || events[0].SourceKind != database.EventSourceWebhook ||
		events[0].ExternalID != "fp-deploy" || events[0].SourceType != "alertmanager" {
		t.Fatalf("events = %+v, want one webhook change event", events)
	}
}

func TestIngestWebhookItem_AlertCarriesEventUUIDIntoAlertPath(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"kind":"alert","confidence":0.9,"reasoning":"cpu high"}`, nil
	}}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{spawnUUID: "inc-1"}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetEventService(services.NewEventService(db, caller))

	h.ingestWebhookItem(testWebhookInstance(), newCorrTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Fatalf("spawns = %d, want 1", svc.getSpawnCount())
	}
	var ev database.Event
	db.First(&ev)
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.insertedEvents) != 1 || svc.insertedEvents[0] != ev.UUID || ev.UUID == "" {
		t.Fatalf("alert insert got event %v, want %s", svc.insertedEvents, ev.UUID)
	}
}

func TestIngestWebhookItem_NoEventServiceKeepsOldBehaviour(t *testing.T) {
	setupEventsHandlerDB(t)
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	h.ingestWebhookItem(testWebhookInstance(), newCorrTestAlert())

	if svc.getSpawnCount() != 1 {
		t.Fatalf("spawns = %d, want 1 (every item is an alert without event service)", svc.getSpawnCount())
	}
}

func TestProcessReportedAlert_LinksToOpenIncident(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedCorrHandlerSettings(t, db)
	seedHandlerIncident(t, db, "inc-wa2", "wa2 edge down", "running", time.Minute)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"correlated":true,"incident_uuid":"inc-wa2","confidence":0.9,"reasoning":"same site"}`, nil
	}}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)
	h.SetAlertCorrelator(services.NewAlertCorrelator(caller, db))

	reported := newCorrTestAlert()
	reported.AlertName = "wa2 is down"
	reported.TargetHost = ""
	incidentUUID, linked, err := h.ProcessReportedAlert(ReportedAlert{
		SourceUUID: "manual", SourceLabel: "api", SourceDisplay: "API-reported", SourceName: "API",
		Alert: reported,
	})

	if err != nil || !linked || incidentUUID != "inc-wa2" {
		t.Fatalf("got (%q, %v, %v), want linked to inc-wa2", incidentUUID, linked, err)
	}
	if svc.getSpawnCount() != 0 || svc.getLinkCount() != 1 {
		t.Fatalf("spawn=%d link=%d, want 0/1", svc.getSpawnCount(), svc.getLinkCount())
	}
}

func TestProcessReportedAlert_OpensAlertIncident(t *testing.T) {
	setupEventsHandlerDB(t)
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{spawnUUID: "inc-new"}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	incidentUUID, linked, err := h.ProcessReportedAlert(ReportedAlert{
		SourceUUID: "manual", SourceLabel: "api", SourceDisplay: "API-reported", SourceName: "API",
		Alert: newCorrTestAlert(),
	})

	if err != nil || linked || incidentUUID != "inc-new" {
		t.Fatalf("got (%q, %v, %v), want new incident", incidentUUID, linked, err)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.spawnContexts) != 1 || svc.spawnContexts[0].SourceKind != database.IncidentSourceKindAlert {
		t.Fatalf("spawned %+v, want one alert-sourced incident", svc.spawnContexts)
	}
}

// fakeReportedProcessor records reported alerts from the API path.
type fakeReportedProcessor struct {
	got    []ReportedAlert
	result string
	linked bool
}

func (f *fakeReportedProcessor) ProcessReportedAlert(r ReportedAlert) (string, bool, error) {
	f.got = append(f.got, r)
	return f.result, f.linked, nil
}

func TestHandleIncidentsPost_ReportedProblemGoesToCorrelation(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"wa2 down","target_host":"wa2","event_kind":"alert","event_confidence":0.9,"event_reasoning":"outage report"}`, nil
	}}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	reported := &fakeReportedProcessor{result: "inc-wa2", linked: true}
	h := &APIHandler{skillService: svc}
	h.SetEventIntake(services.NewEventService(db, caller), extraction.NewAlertExtractor(caller), reported)

	body, _ := json.Marshal(api.CreateIncidentRequest{Task: "wa2 is down, users get 502"})
	rec := httptest.NewRecorder()
	h.handleIncidents(rec, httptest.NewRequest(http.MethodPost, "/api/incidents", bytes.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp api.CreateIncidentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.UUID != "inc-wa2" || resp.Status != "linked" || resp.EventKind != database.EventKindAlert || resp.EventUUID == "" {
		t.Fatalf("resp = %+v", resp)
	}
	if svc.getSpawnCount() != 0 {
		t.Fatalf("a reported alert must not start a request run, spawns = %d", svc.getSpawnCount())
	}
	if len(reported.got) != 1 || reported.got[0].Alert.EventUUID != resp.EventUUID || reported.got[0].Alert.TargetHost != "wa2" {
		t.Fatalf("reported = %+v", reported.got)
	}
	var ev database.Event
	db.First(&ev, "uuid = ?", resp.EventUUID)
	if ev.SourceKind != database.EventSourceManual || ev.Kind != database.EventKindAlert {
		t.Fatalf("event = %+v", ev)
	}
}

func TestClassifyManualTask_QuestionIsRequest(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"disk usage question","event_kind":"request","event_confidence":0.95,"event_reasoning":"question"}`, nil
	}}
	reported := &fakeReportedProcessor{}
	h := &APIHandler{}
	h.SetEventIntake(services.NewEventService(db, caller), extraction.NewAlertExtractor(caller), reported)

	eventUUID, _, handled := h.classifyManualTask(context.Background(), "what is the disk usage on db1?")

	if handled || eventUUID == "" || len(reported.got) != 0 {
		t.Fatalf("handled=%v event=%q reported=%d, want a request event", handled, eventUUID, len(reported.got))
	}
	var ev database.Event
	db.First(&ev, "uuid = ?", eventUUID)
	if ev.Kind != database.EventKindRequest || ev.Title != "disk usage question" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestClassifyManualTask_NoIntakeWired(t *testing.T) {
	h := &APIHandler{}
	if eventUUID, _, handled := h.classifyManualTask(context.Background(), "x"); handled || eventUUID != "" {
		t.Fatalf("want no-op without intake, got %q %v", eventUUID, handled)
	}
}

func TestHandleIncidentsPost_ChangeIsRecordedOnly(t *testing.T) {
	db := setupEventsHandlerDB(t)
	caller := &corrOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"nginx 1.27 on wa2","target_host":"wa2","event_kind":"change","event_confidence":0.93,"event_reasoning":"deploy notice"}`, nil
	}}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	reported := &fakeReportedProcessor{}
	h := &APIHandler{skillService: svc}
	h.SetEventIntake(services.NewEventService(db, caller), extraction.NewAlertExtractor(caller), reported)

	body, _ := json.Marshal(api.CreateIncidentRequest{Task: "deployed nginx 1.27 to wa2"})
	rec := httptest.NewRecorder()
	h.handleIncidents(rec, httptest.NewRequest(http.MethodPost, "/api/incidents", bytes.NewReader(body)))

	var resp api.CreateIncidentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusCreated || resp.Status != "recorded" || resp.EventKind != database.EventKindChange || resp.UUID != "" || resp.EventUUID == "" {
		t.Fatalf("status=%d resp=%+v, want a recorded change event", rec.Code, resp)
	}
	if svc.getSpawnCount() != 0 || len(reported.got) != 0 {
		t.Fatalf("a change must start nothing: spawns=%d reported=%d", svc.getSpawnCount(), len(reported.got))
	}
	var ev database.Event
	db.First(&ev, "uuid = ?", resp.EventUUID)
	if ev.Kind != database.EventKindChange || ev.Host != "wa2" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestMentionAckText(t *testing.T) {
	if !strings.Contains(mentionAckText(database.EventKindChange), "change event") ||
		!strings.Contains(mentionAckText(database.EventKindInfo), "info event") {
		t.Fatal("ack text must name the class")
	}
}
