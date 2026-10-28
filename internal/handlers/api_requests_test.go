package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// dbIncidentSkillService loads incidents from the test DB.
type dbIncidentSkillService struct {
	corrGateSkillService
}

func (s *dbIncidentSkillService) GetIncident(uuid string) (*database.Incident, error) {
	var inc database.Incident
	if err := database.GetDB().Where("uuid = ?", uuid).First(&inc).Error; err != nil {
		return nil, err
	}
	return &inc, nil
}

func getJSON(t *testing.T, h *APIHandler, path string, out interface{}) int {
	t.Helper()
	mux := http.NewServeMux()
	h.SetupRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rec.Code
}

func seedRuns(t *testing.T, runs ...database.AgentRun) {
	t.Helper()
	for i := range runs {
		if err := database.GetDB().Create(&runs[i]).Error; err != nil {
			t.Fatalf("seed run: %v", err)
		}
	}
}

// The incident page shows every run: the newest answer, all logs in order,
// the sums, and the runs without their payload.
func TestHandleIncidentByID_FoldsRuns(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.AgentRun{}, &database.Alert{})
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	if err := database.GetDB().Create(&database.Incident{UUID: "inc-1", Source: "zabbix",
		SourceKind: database.IncidentSourceKindAlert, Status: database.IncidentStatusMonitor}).Error; err != nil {
		t.Fatal(err)
	}
	seedRuns(t,
		database.AgentRun{RunID: "r1", ScopeUUID: "inc-1", IncidentUUID: "inc-1", StartedAt: start,
			FullLog: "investigation log", Response: "root cause: disk", TokensUsed: 100, ExecutionTimeMs: 1000},
		database.AgentRun{RunID: "r2", ScopeUUID: "inc-1", IncidentUUID: "inc-1", StartedAt: start.Add(time.Hour),
			FullLog: "follow-up log", Response: "disk cleaned", TokensUsed: 20, ExecutionTimeMs: 300},
	)

	var got database.Incident
	if code := getJSON(t, NewAPIHandler(&dbIncidentSkillService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil), "/api/incidents/inc-1", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.Response != "disk cleaned" || got.TokensUsed != 120 || got.ExecutionTimeMs != 1300 {
		t.Errorf("incident = response %q tokens %d time %d", got.Response, got.TokensUsed, got.ExecutionTimeMs)
	}
	first, second := strings.Index(got.FullLog, "investigation log"), strings.Index(got.FullLog, "follow-up log")
	if first < 0 || second < first || !strings.Contains(got.FullLog, "=== Run 2 of 2") {
		t.Errorf("full_log = %q", got.FullLog)
	}
	if len(got.Runs) != 2 || got.Runs[0].FullLog != "" || got.Runs[1].Response != "" {
		t.Errorf("runs = %+v, want two runs without payload", got.Runs)
	}
}

func TestHandleRequestByID(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.AgentRun{}, &database.Event{})
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	if err := database.GetDB().Create(&database.Event{UUID: "req-1", SourceKind: database.EventSourceSlackMention,
		Kind: database.EventKindRequest, Classification: "llm", Title: "disk on db1?"}).Error; err != nil {
		t.Fatal(err)
	}
	seedRuns(t,
		database.AgentRun{RunID: "a", ScopeUUID: "req-1", EventUUID: "req-1", SourceKind: "slack_mention",
			Status: database.AgentRunStatusCompleted, StartedAt: start, FullLog: "first", Response: "80% used"},
		database.AgentRun{RunID: "b", ScopeUUID: "req-1", SourceKind: "slack_mention",
			Status: database.AgentRunStatusRunning, StartedAt: start.Add(time.Minute), FullLog: "second"},
	)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	var req requestDetail
	if code := getJSON(t, h, "/api/requests/req-1", &req); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if req.Title != "disk on db1?" || req.Status != "running" || req.SourceKind != "slack_mention" ||
		req.Response != "80% used" || req.CompletedAt != nil || len(req.Runs) != 2 || req.EventUUID != "req-1" {
		t.Errorf("request = %+v", req)
	}
	if !strings.Contains(req.FullLog, "first") || !strings.Contains(req.FullLog, "second") {
		t.Errorf("full_log = %q", req.FullLog)
	}

	var short map[string]string
	if code := getJSON(t, h, "/api/requests/req-1/response", &short); code != http.StatusOK {
		t.Fatalf("response status = %d", code)
	}
	if short["response"] != "80% used" || short["status"] != "running" || short["title"] != "disk on db1?" {
		t.Errorf("response projection = %v", short)
	}

	if code := getJSON(t, h, "/api/requests/missing", nil); code != http.StatusNotFound {
		t.Errorf("missing request: %d, want 404", code)
	}
}

// Request events in the Events feed carry their scope and the status of the
// scope's newest run.
func TestHandleEvents_RequestRowsShowRunStatus(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.Incident{}, &database.AgentRun{}, &database.Alert{}, &database.Event{})
	now := time.Now().UTC()
	if err := database.GetDB().Create(&database.Event{UUID: "tick", SourceKind: database.EventSourceCron,
		Kind: database.EventKindRequest, Classification: "source", Title: "Cron: nightly", ScopeUUID: "tick",
		OccurredAt: now, ReceivedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	seedRuns(t, database.AgentRun{RunID: "t1", ScopeUUID: "tick", Status: database.AgentRunStatusFailed, StartedAt: now})

	var page struct {
		Data []EventFeedItem `json:"data"`
	}
	if code := getJSON(t, NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil), "/api/events", &page); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(page.Data) != 1 || page.Data[0].ScopeUUID != "tick" || page.Data[0].Status != "failed" {
		t.Fatalf("feed = %+v", page.Data)
	}
}
