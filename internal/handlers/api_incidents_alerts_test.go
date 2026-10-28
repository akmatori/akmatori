package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
)

// TestHandleIncidentAlerts_OrderedByFiredAt verifies that GET
// /api/incidents/{uuid}/alerts returns alert rows ordered by fired_at ASC and
// that correlation fields are present on correlated rows.
func TestHandleIncidentAlerts_OrderedByFiredAt(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{}, &database.AgentRun{},
		&database.Alert{},
	)
	db := database.GetDB()

	incUUID := uuid.New().String()
	now := time.Now().UTC()
	firstFired := now.Add(-30 * time.Minute)
	secondFired := now.Add(-10 * time.Minute)

	if err := db.Create(&database.Incident{
		UUID:       incUUID,
		Source:     "alertmanager",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-alerts-test",
		Title:      "alert listing test",
		Status:     database.IncidentStatusRunning,
		StartedAt:  firstFired,
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	conf := 0.92
	originAlert := database.Alert{
		UUID:         uuid.New().String(),
		IncidentUUID: incUUID,
		Status:       database.AlertStatusFiring,
		AlertName:    "HighCPU",
		TargetHost:   "web-01",
		FiredAt:      firstFired,
		Correlated:   false,
	}
	correlatedAlert := database.Alert{
		UUID:                  uuid.New().String(),
		IncidentUUID:          incUUID,
		Status:                database.AlertStatusFiring,
		AlertName:             "HighCPU",
		TargetHost:            "web-01",
		FiredAt:               secondFired,
		Correlated:            true,
		CorrelationConfidence: &conf,
		CorrelationReasoning:  "Same alert name and host, same incident.",
	}
	for _, a := range []database.Alert{originAlert, correlatedAlert} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("seed alert: %v", err)
		}
	}

	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/"+incUUID+"/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var alerts []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&alerts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}

	// First row is the origin (not correlated), second is the recurrence.
	if correlated, _ := alerts[0]["correlated"].(bool); correlated {
		t.Error("first alert should not be correlated (origin)")
	}
	if correlated, _ := alerts[1]["correlated"].(bool); !correlated {
		t.Error("second alert should be correlated")
	}
	if confidence, _ := alerts[1]["correlation_confidence"].(float64); confidence < 0.9 {
		t.Errorf("correlation_confidence = %v, want >= 0.9", confidence)
	}
	if reasoning, _ := alerts[1]["correlation_reasoning"].(string); reasoning == "" {
		t.Error("correlation_reasoning should be set on correlated alert")
	}

	// Verify ordering: first FiredAt should be before second FiredAt.
	firstTS, _ := time.Parse(time.RFC3339Nano, alerts[0]["fired_at"].(string))
	secondTS, _ := time.Parse(time.RFC3339Nano, alerts[1]["fired_at"].(string))
	if !firstTS.Before(secondTS) {
		t.Errorf("alerts not ordered by fired_at ASC: first=%v second=%v", firstTS, secondTS)
	}
}

// TestHandleIncidentAlerts_EmptySlice verifies that a valid incident with no
// alerts returns 200 with an empty JSON array (not 404 or 500).
func TestHandleIncidentAlerts_EmptySlice(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{}, &database.AgentRun{},
		&database.Alert{},
	)
	db := database.GetDB()

	incUUID := uuid.New().String()
	if err := db.Create(&database.Incident{
		UUID:       incUUID,
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-empty-alerts",
		Title:      "empty alerts test",
		Status:     database.IncidentStatusRunning,
		StartedAt:  time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/"+incUUID+"/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var alerts []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&alerts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected empty alerts array, got %d alerts", len(alerts))
	}
}

// TestHandleIncidentAlerts_NotFound verifies that a 404 is returned for an
// unknown incident UUID.
func TestHandleIncidentAlerts_NotFound(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.Incident{}, &database.AgentRun{},
		&database.Alert{},
	)

	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/does-not-exist/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
