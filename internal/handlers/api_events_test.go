package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// seedEventAlert creates an Alert row attached to incidentUUID.
func seedEventAlert(t *testing.T, incidentUUID string, firedAt time.Time, correlated bool) string {
	t.Helper()
	db := database.GetDB()
	decision := "new_incident"
	if correlated {
		decision = "linked"
	}
	a := database.Alert{
		UUID:                uuid.New().String(),
		IncidentUUID:        incidentUUID,
		Status:              database.AlertStatusFiring,
		AlertName:           "TestAlert",
		TargetHost:          "host-01",
		FiredAt:             firedAt,
		Correlated:          correlated,
		CorrelationDecision: decision,
	}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	return a.UUID
}

func doEventsRequest(t *testing.T, query string) ([]map[string]any, api.PaginationMeta) {
	t.Helper()
	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	url := "/api/events"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

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

// TestHandleEvents_MergedOrderedByOccurredAt verifies that alert and cron rows
// are merged and returned in occurred_at DESC order.
func TestHandleEvents_MergedOrderedByOccurredAt(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-1 * time.Hour)

	// Alert-sourced incident + one alert fired at `older`.
	alertIncUUID := seedEventIncident(t, database.IncidentSourceKindAlert, older, database.IncidentStatusCompleted)
	seedEventAlert(t, alertIncUUID, older, false)

	// Cron incident started at `newer`.
	seedEventIncident(t, database.IncidentSourceKindCron, newer, database.IncidentStatusCompleted)

	rows, meta := doEventsRequest(t, "")
	if meta.Total != 2 {
		t.Fatalf("expected total=2, got %d", meta.Total)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// First row should be the cron (newer occurred_at).
	if et, _ := rows[0]["event_type"].(string); et != database.IncidentSourceKindCron {
		t.Errorf("expected first row event_type=%q, got %q", database.IncidentSourceKindCron, et)
	}
	// Second row should be the alert (older occurred_at).
	if et, _ := rows[1]["event_type"].(string); et != "alert" {
		t.Errorf("expected second row event_type=alert, got %q", et)
	}
}

// TestHandleEvents_TypeFilterAlert verifies that ?type=alert returns only alert rows.
func TestHandleEvents_TypeFilterAlert(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	now := time.Now().UTC()

	alertIncUUID := seedEventIncident(t, database.IncidentSourceKindAlert, now, database.IncidentStatusRunning)
	seedEventAlert(t, alertIncUUID, now, false)
	seedEventIncident(t, database.IncidentSourceKindCron, now, database.IncidentStatusCompleted)

	rows, meta := doEventsRequest(t, "type=alert")
	if meta.Total != 1 {
		t.Fatalf("expected total=1, got %d", meta.Total)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if et, _ := rows[0]["event_type"].(string); et != "alert" {
		t.Errorf("expected event_type=alert, got %q", et)
	}
}

// TestHandleEvents_TypeFilterCron verifies that ?type=cron returns only cron incident rows.
func TestHandleEvents_TypeFilterCron(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	now := time.Now().UTC()

	alertIncUUID := seedEventIncident(t, database.IncidentSourceKindAlert, now, database.IncidentStatusCompleted)
	seedEventAlert(t, alertIncUUID, now, false)
	cronUUID := seedEventIncident(t, database.IncidentSourceKindCron, now.Add(-10*time.Minute), database.IncidentStatusCompleted)

	rows, meta := doEventsRequest(t, "type=cron")
	if meta.Total != 1 {
		t.Fatalf("expected total=1, got %d", meta.Total)
	}
	if rows[0]["event_uuid"] != cronUUID {
		t.Errorf("expected cron incident UUID %s, got %v", cronUUID, rows[0]["event_uuid"])
	}
}

// TestHandleEvents_DeepPageReturns400 verifies that requesting a page whose offset
// exceeds eventsMaxRowFetch (10 000) returns 400 rather than silent empty data.
func TestHandleEvents_DeepPageReturns400(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	// per_page=1, page=10001 → offset=10000 which equals eventsMaxRowFetch.
	req := httptest.NewRequest(http.MethodGet, "/api/events?per_page=1&page=10001", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleEvents_Pagination verifies that page/per_page work correctly.
func TestHandleEvents_Pagination(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	now := time.Now().UTC()

	// Seed 3 cron incidents.
	for i := 0; i < 3; i++ {
		seedEventIncident(t, database.IncidentSourceKindCron, now.Add(time.Duration(-i)*time.Hour), database.IncidentStatusCompleted)
	}

	// Page 1 with per_page=2 should return 2 rows.
	rows, meta := doEventsRequest(t, "per_page=2&page=1")
	if meta.Total != 3 {
		t.Fatalf("expected total=3, got %d", meta.Total)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows on page 1, got %d", len(rows))
	}
	if meta.TotalPages != 2 {
		t.Errorf("expected 2 total pages, got %d", meta.TotalPages)
	}

	// Page 2 should return 1 row.
	rows2, _ := doEventsRequest(t, "per_page=2&page=2")
	if len(rows2) != 1 {
		t.Errorf("expected 1 row on page 2, got %d", len(rows2))
	}
}

// TestHandleEvents_SearchByUUIDPrefixAndTitle verifies the search param:
// UUID prefixes match alert and incident events; title substrings match too;
// non-matching terms return an empty page.
func TestHandleEvents_SearchByUUIDPrefixAndTitle(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	now := time.Now().UTC()
	alertIncUUID := seedEventIncident(t, database.IncidentSourceKindAlert, now, database.IncidentStatusRunning)
	alertUUID := seedEventAlert(t, alertIncUUID, now, false)
	cronUUID := seedEventIncident(t, database.IncidentSourceKindCron, now, database.IncidentStatusCompleted)

	// Search by the alert event's UUID prefix — only that event comes back.
	rows, meta := doEventsRequest(t, "search="+alertUUID[:8])
	if meta.Total != 1 || len(rows) != 1 {
		t.Fatalf("uuid-prefix search: expected 1 row, got total=%d rows=%d", meta.Total, len(rows))
	}
	if rows[0]["event_uuid"] != alertUUID {
		t.Errorf("expected event %s, got %v", alertUUID, rows[0]["event_uuid"])
	}

	// Search by the cron incident's UUID prefix.
	rows, _ = doEventsRequest(t, "search="+cronUUID[:8])
	if len(rows) != 1 || rows[0]["event_uuid"] != cronUUID {
		t.Fatalf("cron uuid search: expected %s, got %v", cronUUID, rows)
	}

	// Title substring matches the alert (name "TestAlert"), case-insensitive.
	rows, _ = doEventsRequest(t, "search=testalert")
	if len(rows) != 1 || rows[0]["event_uuid"] != alertUUID {
		t.Fatalf("title search: expected alert event, got %v", rows)
	}

	// Host substring matches the alert row (host-01).
	rows, _ = doEventsRequest(t, "search=host-01")
	if len(rows) != 1 || rows[0]["event_uuid"] != alertUUID {
		t.Fatalf("host search: expected alert event, got %v", rows)
	}

	// Non-matching term returns nothing.
	rows, meta = doEventsRequest(t, "search=zzz-no-such-event")
	if meta.Total != 0 || len(rows) != 0 {
		t.Errorf("expected empty result, got total=%d rows=%d", meta.Total, len(rows))
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
