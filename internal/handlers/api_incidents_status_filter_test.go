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

// seedStatusFilterIncident inserts an incident with the given status.
func seedStatusFilterIncident(t *testing.T, status database.IncidentStatus) string {
	t.Helper()
	db := database.GetDB()
	id := uuid.New().String()
	if err := db.Create(&database.Incident{
		UUID:       id,
		Source:     "test",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: uuid.New().String(),
		Title:      "status filter test: " + string(status),
		Status:     status,
		StartedAt:  time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed incident (status=%s): %v", status, err)
	}
	return id
}

// doIncidentListRequest sends GET /api/incidents with the given raw query string
// and returns the decoded PaginatedResponse data slice as []map[string]any.
func doIncidentListRequest(t *testing.T, query string) ([]map[string]any, api.PaginationMeta) {
	t.Helper()
	mux := http.NewServeMux()
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetupRoutes(mux)

	url := "/api/incidents"
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

// TestHandleIncidents_NoStatusFilter verifies that omitting the status param
// returns all incidents regardless of status.
func TestHandleIncidents_NoStatusFilter(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	seedStatusFilterIncident(t, database.IncidentStatusRunning)
	seedStatusFilterIncident(t, database.IncidentStatusMonitor)
	seedStatusFilterIncident(t, database.IncidentStatusCompleted)

	rows, meta := doIncidentListRequest(t, "")
	if len(rows) != 3 {
		t.Errorf("expected 3 incidents, got %d", len(rows))
	}
	if meta.Total != 3 {
		t.Errorf("expected total=3, got %d", meta.Total)
	}
}

// TestHandleIncidents_SingleStatusFilter verifies that ?status=monitor returns
// only monitor-status rows.
func TestHandleIncidents_SingleStatusFilter(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	seedStatusFilterIncident(t, database.IncidentStatusRunning)
	monitorID := seedStatusFilterIncident(t, database.IncidentStatusMonitor)
	seedStatusFilterIncident(t, database.IncidentStatusCompleted)

	rows, meta := doIncidentListRequest(t, "status=monitor")
	if len(rows) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(rows))
	}
	if meta.Total != 1 {
		t.Errorf("expected total=1, got %d", meta.Total)
	}
	if uuid, _ := rows[0]["uuid"].(string); uuid != monitorID {
		t.Errorf("expected monitor incident UUID %s, got %s", monitorID, uuid)
	}
}

// TestHandleIncidents_MultiStatusFilter verifies that comma-separated statuses
// work as OR: ?status=pending,running,diagnosed,monitor returns open incidents.
func TestHandleIncidents_MultiStatusFilter(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.Alert{})

	seedStatusFilterIncident(t, database.IncidentStatusPending)
	seedStatusFilterIncident(t, database.IncidentStatusRunning)
	seedStatusFilterIncident(t, database.IncidentStatusCompleted)
	seedStatusFilterIncident(t, database.IncidentStatusFailed)

	rows, meta := doIncidentListRequest(t, "status=pending,running,diagnosed,monitor")
	if len(rows) != 2 {
		t.Fatalf("expected 2 open incidents, got %d", len(rows))
	}
	if meta.Total != 2 {
		t.Errorf("expected total=2, got %d", meta.Total)
	}
	for _, row := range rows {
		st, _ := row["status"].(string)
		if st != string(database.IncidentStatusPending) && st != string(database.IncidentStatusRunning) {
			t.Errorf("unexpected status %q in result", st)
		}
	}
}
