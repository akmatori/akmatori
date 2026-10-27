package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
)

// seedEventIncident creates an Incident row for events feed tests.
func seedEventIncident(t *testing.T, sourceKind string, startedAt time.Time, status database.IncidentStatus) string {
	t.Helper()
	db := database.GetDB()
	id := uuid.New().String()
	inc := database.Incident{
		UUID:       id,
		Source:     "test",
		SourceKind: sourceKind,
		SourceUUID: uuid.New().String(),
		Title:      "evt-inc: " + sourceKind,
		Status:     status,
		StartedAt:  startedAt,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	return id
}

// seedFeedEvent creates an events row for feed tests.
func seedFeedEvent(t *testing.T, e database.Event) string {
	t.Helper()
	if e.UUID == "" {
		e.UUID = uuid.New().String()
	}
	if e.Classification == "" {
		e.Classification = database.EventClassificationLLM
	}
	if e.SourceKind == "" {
		e.SourceKind = database.EventSourceWebhook
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = e.OccurredAt
	}
	if err := database.GetDB().Create(&e).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return e.UUID
}

func setupFeedDB(t *testing.T) {
	t.Helper()
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{}, &database.Event{})
}

func doEventsRequest(t *testing.T, query string) ([]map[string]any, api.PaginationMeta) {
	t.Helper()
	rec := doEventsRaw(t, "/api/events", query)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data       []map[string]any   `json:"data"`
		Pagination api.PaginationMeta `json:"pagination"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.Data, resp.Pagination
}

func doEventsRaw(t *testing.T, path, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)
	url := path
	if query != "" {
		url += "?" + query
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func uuidsOf(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["event_uuid"].(string))
	}
	return out
}

func TestHandleEvents_NewestFirstWithLinks(t *testing.T) {
	setupFeedDB(t)
	db := database.GetDB()
	now := time.Now().UTC().Truncate(time.Second)

	incUUID := seedEventIncident(t, database.IncidentSourceKindAlert, now.Add(-3*time.Hour), database.IncidentStatusMonitor)
	conf := 0.82
	alert := database.Alert{
		UUID: uuid.New().String(), IncidentUUID: incUUID, Status: database.AlertStatusResolved,
		AlertName: "HostDown", FiredAt: now.Add(-3 * time.Hour), Correlated: true,
		CorrelationConfidence: &conf, CorrelationReasoning: "same site", CorrelationDecision: "linked",
	}
	db.Create(&alert)
	alertEv := seedFeedEvent(t, database.Event{Kind: database.EventKindAlert, Title: "HostDown", Status: "firing",
		OccurredAt: now.Add(-3 * time.Hour), AlertUUID: alert.UUID, IncidentUUID: incUUID})

	runUUID := seedEventIncident(t, database.IncidentSourceKindCron, now.Add(-2*time.Hour), database.IncidentStatusRunning)
	cronEv := seedFeedEvent(t, database.Event{Kind: database.EventKindRequest, SourceKind: database.EventSourceCron,
		Classification: database.EventClassificationSource, Title: "Cron: nightly", OccurredAt: now.Add(-2 * time.Hour), IncidentUUID: runUUID})

	changeEv := seedFeedEvent(t, database.Event{Kind: database.EventKindChange, Title: "Deploy nginx", Status: "firing",
		OccurredAt: now.Add(-time.Hour), Host: "wa2"})

	rows, meta := doEventsRequest(t, "")
	if meta.Total != 3 {
		t.Fatalf("total = %d, want 3", meta.Total)
	}
	got := uuidsOf(rows)
	if got[0] != changeEv || got[1] != cronEv || got[2] != alertEv {
		t.Fatalf("order = %v, want change, cron, alert", got)
	}

	if rows[0]["status"] != "" || rows[0]["kind"] != "change" || rows[0]["target_host"] != "wa2" {
		t.Errorf("change row = %v (stored-only events have no status)", rows[0])
	}
	if rows[1]["status"] != "running" || rows[1]["source_kind"] != "cron" || rows[1]["incident_title"] != "evt-inc: cron" {
		t.Errorf("request row = %v, want the run's status and title", rows[1])
	}
	a := rows[2]
	if a["status"] != "resolved" || a["correlated"] != true || a["correlation_reasoning"] != "same site" ||
		a["alert_uuid"] != alert.UUID || a["incident_status"] != "monitor" {
		t.Errorf("alert row = %v, want live alert state + incident", a)
	}
}

func TestHandleEvents_FollowsMovedAlert(t *testing.T) {
	setupFeedDB(t)
	db := database.GetDB()
	now := time.Now().UTC()
	oldInc := seedEventIncident(t, database.IncidentSourceKindAlert, now, database.IncidentStatusRunning)
	newInc := seedEventIncident(t, database.IncidentSourceKindAlert, now, database.IncidentStatusRunning)
	alert := database.Alert{UUID: uuid.New().String(), IncidentUUID: newInc, Status: database.AlertStatusFiring, FiredAt: now}
	db.Create(&alert)
	seedFeedEvent(t, database.Event{Kind: database.EventKindAlert, OccurredAt: now, AlertUUID: alert.UUID, IncidentUUID: oldInc})

	rows, _ := doEventsRequest(t, "")
	if rows[0]["incident_uuid"] != newInc {
		t.Fatalf("incident = %v, want the alert's current incident %s", rows[0]["incident_uuid"], newInc)
	}
}

func TestHandleEvents_Filters(t *testing.T) {
	setupFeedDB(t)
	now := time.Now().UTC()
	alertEv := seedFeedEvent(t, database.Event{Kind: database.EventKindAlert, Title: "CPU high", Host: "web-01", OccurredAt: now.Add(-time.Minute)})
	changeEv := seedFeedEvent(t, database.Event{Kind: database.EventKindChange, Title: "Deploy", OccurredAt: now.Add(-2 * time.Minute)})
	cronEv := seedFeedEvent(t, database.Event{Kind: database.EventKindRequest, SourceKind: database.EventSourceCron, Title: "Cron", OccurredAt: now.Add(-3 * time.Minute)})
	oldEv := seedFeedEvent(t, database.Event{Kind: database.EventKindAlert, Title: "Old", OccurredAt: now.Add(-48 * time.Hour)})

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"kind", "kind=change", []string{changeEv}},
		{"source", "source=cron", []string{cronEv}},
		{"legacy type=alert", "type=alert", []string{alertEv, oldEv}},
		{"legacy type=cron", "type=cron", []string{cronEv}},
		{"time range", fmt.Sprintf("from=%d", now.Add(-time.Hour).Unix()), []string{alertEv, changeEv, cronEv}},
		{"search title", "search=deploy", []string{changeEv}},
		{"search host", "search=WEB-01", []string{alertEv}},
		{"search uuid prefix", "search=" + cronEv[:8], []string{cronEv}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, meta := doEventsRequest(t, tt.query)
			got := uuidsOf(rows)
			if int(meta.Total) != len(tt.want) || strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v (total %d), want %v", got, meta.Total, tt.want)
			}
		})
	}
}

func TestHandleEvents_Pagination(t *testing.T) {
	setupFeedDB(t)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		seedFeedEvent(t, database.Event{Kind: database.EventKindInfo, OccurredAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	rows, meta := doEventsRequest(t, "page=2&per_page=2")
	if len(rows) != 2 || meta.Total != 5 || meta.TotalPages != 3 {
		t.Fatalf("rows=%d meta=%+v", len(rows), meta)
	}
	if rec := doEventsRaw(t, "/api/events", "page=100001"); rec.Code != http.StatusBadRequest {
		t.Fatalf("huge page: status %d, want 400", rec.Code)
	}
}

func TestHandleEventRaw(t *testing.T) {
	setupFeedDB(t)
	db := database.GetDB()
	now := time.Now().UTC()

	own := seedFeedEvent(t, database.Event{Kind: database.EventKindChange, OccurredAt: now,
		RawPayload: database.JSONB{"original_message": "deployed nginx"}, Labels: database.JSONB{"team": "cdn"}})
	alert := database.Alert{UUID: uuid.New().String(), IncidentUUID: "i", Status: database.AlertStatusFiring, FiredAt: now,
		RawPayload: database.JSONB{"labels": "from alert"}}
	db.Create(&alert)
	backfilled := seedFeedEvent(t, database.Event{Kind: database.EventKindAlert, OccurredAt: now, AlertUUID: alert.UUID,
		Classification: database.EventClassificationBackfill})
	request := seedFeedEvent(t, database.Event{Kind: database.EventKindRequest, OccurredAt: now, Summary: "what is the disk usage?"})

	decode := func(uuid string) map[string]any {
		rec := doEventsRaw(t, "/api/events/raw", "uuid="+uuid)
		if rec.Code != http.StatusOK {
			t.Fatalf("raw %s: %d %s", uuid, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	if m := decode(own); m["original_message"] != "deployed nginx" || m["labels"] == nil {
		t.Errorf("own payload = %v", m)
	}
	if m := decode(backfilled); m["raw"] == nil || m["raw"].(map[string]any)["labels"] != "from alert" {
		t.Errorf("backfilled payload = %v, want the alert's payload", m)
	}
	if m := decode(request); m["original_message"] != "what is the disk usage?" {
		t.Errorf("request payload = %v, want the summary", m)
	}
	if rec := doEventsRaw(t, "/api/events/raw", "uuid=missing"); rec.Code != http.StatusNotFound {
		t.Errorf("missing event: %d, want 404", rec.Code)
	}
}

// TestHandleIncidentResponse_LightweightProjection verifies the /response
// endpoint returns title/status/response without the heavy fields, and 404s
// for unknown incidents.
func TestHandleIncidentResponse_LightweightProjection(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	db := database.GetDB()
	incUUID := seedEventIncident(t, database.IncidentSourceKindAlert, time.Now().UTC(), database.IncidentStatusCompleted)
	if err := db.Model(&database.Incident{}).Where("uuid = ?", incUUID).
		Updates(map[string]interface{}{"response": "root cause: bad deploy", "full_log": "huge log"}).Error; err != nil {
		t.Fatalf("set response: %v", err)
	}

	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/"+incUUID+"/response", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["response"] != "root cause: bad deploy" {
		t.Errorf("expected response text, got %v", resp["response"])
	}
	if _, hasLog := resp["full_log"]; hasLog {
		t.Error("full_log must not be included in the lightweight projection")
	}

	req404 := httptest.NewRequest(http.MethodGet, "/api/incidents/does-not-exist/response", nil)
	rec404 := httptest.NewRecorder()
	mux.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown incident, got %d", rec404.Code)
	}
}
