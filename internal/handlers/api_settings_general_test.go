package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// TestHandleGeneralSettings_GET_NonNilDefaults verifies that GET
// /api/settings/general returns non-nil values for all alert config fields
// even when the DB row was created with all nullable fields left as NULL.
func TestHandleGeneralSettings_GET_NonNilDefaults(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.GeneralSettings{},
	)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/general", nil)
	rec := httptest.NewRecorder()
	h.handleGeneralSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	fields := []string{
		"alert_correlation_enabled",
		"alert_monitor_window_minutes",
	}
	for _, f := range fields {
		if _, ok := body[f]; !ok {
			t.Errorf("expected field %q in response, but it was missing", f)
		}
		if body[f] == nil {
			t.Errorf("expected field %q to be non-null, but got null", f)
		}
	}

	// Verify effective defaults.
	if v, _ := body["alert_correlation_enabled"].(bool); v {
		t.Error("expected alert_correlation_enabled=false by default")
	}
	if v, _ := body["alert_monitor_window_minutes"].(float64); v != 60 {
		t.Errorf("expected alert_monitor_window_minutes=60, got %v", v)
	}
}

// TestHandleGeneralSettings_AlertMonitorWindowMinutes_PersistAndGet verifies
// that setting alert_monitor_window_minutes via PUT is persisted and returned
// correctly on the subsequent GET.
func TestHandleGeneralSettings_AlertMonitorWindowMinutes_PersistAndGet(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.GeneralSettings{},
	)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	body := `{"alert_monitor_window_minutes": 120}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBufferString(body))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	h.handleGeneralSettings(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", putRec.Code, putRec.Body.String())
	}

	var putBody map[string]interface{}
	if err := json.NewDecoder(putRec.Body).Decode(&putBody); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if v, _ := putBody["alert_monitor_window_minutes"].(float64); v != 120 {
		t.Errorf("PUT response: expected alert_monitor_window_minutes=120, got %v", v)
	}

	// GET to confirm the value was persisted.
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/general", nil)
	getRec := httptest.NewRecorder()
	h.handleGeneralSettings(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", getRec.Code)
	}
	var getBody map[string]interface{}
	if err := json.NewDecoder(getRec.Body).Decode(&getBody); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if v, _ := getBody["alert_monitor_window_minutes"].(float64); v != 120 {
		t.Errorf("GET response: expected alert_monitor_window_minutes=120, got %v", v)
	}
}

// TestHandleGeneralSettings_AlertMonitorWindowMinutes_InvalidValue verifies
// that zero and negative values are rejected with HTTP 400.
func TestHandleGeneralSettings_AlertMonitorWindowMinutes_InvalidValue(t *testing.T) {
	cases := []struct {
		name  string
		value int
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testhelpers.NewGlobalSQLiteDB(t,
				&database.GeneralSettings{},
			)

			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

			b, _ := json.Marshal(map[string]interface{}{
				"alert_monitor_window_minutes": tc.value,
			})
			req := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBuffer(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.handleGeneralSettings(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("value=%d: expected 400, got %d: %s", tc.value, rec.Code, rec.Body.String())
			}
		})
	}
}
