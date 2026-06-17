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
	// No GeneralSettings row seeded — GetOrCreateGeneralSettings will insert
	// a row with all nullable fields as NULL. The GET handler must hydrate
	// them with effective defaults before responding.

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

	// All alert config fields must be non-null after GET hydration.
	fields := []string{
		"alert_correlation_enabled",
		"alert_correlation_window_minutes",
		"alert_correlation_threshold",
		"alert_correlation_max_candidates",
		"alert_correlation_long_window_days",
		"alert_correlation_fingerprint_window_minutes",
		"alert_suppression_enabled",
		"alert_suppression_threshold",
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
	if v, _ := body["alert_correlation_window_minutes"].(float64); v != 30 {
		t.Errorf("expected alert_correlation_window_minutes=30, got %v", v)
	}
	if v, _ := body["alert_correlation_threshold"].(float64); v != 0.7 {
		t.Errorf("expected alert_correlation_threshold=0.7, got %v", v)
	}
	if v, _ := body["alert_correlation_max_candidates"].(float64); v != 20 {
		t.Errorf("expected alert_correlation_max_candidates=20, got %v", v)
	}
	if v, _ := body["alert_suppression_enabled"].(bool); v {
		t.Error("expected alert_suppression_enabled=false by default")
	}
	if v, _ := body["alert_suppression_threshold"].(float64); v != 0.7 {
		t.Errorf("expected alert_suppression_threshold=0.7, got %v", v)
	}
	if v, _ := body["alert_correlation_fingerprint_window_minutes"].(float64); v != 1440 {
		t.Errorf("expected alert_correlation_fingerprint_window_minutes=1440 by default, got %v", v)
	}
	if v, _ := body["alert_correlation_long_window_days"].(float64); v != 7 {
		t.Errorf("expected alert_correlation_long_window_days=7 by default, got %v", v)
	}
}

// TestHandleGeneralSettings_LongWindowDays_InvalidValue verifies that values
// outside [1, 90] are rejected with HTTP 400.
func TestHandleGeneralSettings_LongWindowDays_InvalidValue(t *testing.T) {
	cases := []struct {
		name  string
		value int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 91},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testhelpers.NewGlobalSQLiteDB(t,
				&database.GeneralSettings{},
			)

			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

			body, _ := json.Marshal(map[string]interface{}{
				"alert_correlation_long_window_days": tc.value,
			})
			req := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.handleGeneralSettings(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("value=%d: expected 400, got %d: %s", tc.value, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestHandleGeneralSettings_FingerprintWindowMinutes_PersistAndGet verifies that
// setting alert_correlation_fingerprint_window_minutes via PUT is persisted and
// returned correctly on the subsequent GET.
func TestHandleGeneralSettings_FingerprintWindowMinutes_PersistAndGet(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t,
		&database.GeneralSettings{},
	)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// PUT with a custom fingerprint window.
	body := `{"alert_correlation_fingerprint_window_minutes": 720}`
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
	if v, _ := putBody["alert_correlation_fingerprint_window_minutes"].(float64); v != 720 {
		t.Errorf("PUT response: expected alert_correlation_fingerprint_window_minutes=720, got %v", v)
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
	if v, _ := getBody["alert_correlation_fingerprint_window_minutes"].(float64); v != 720 {
		t.Errorf("GET response: expected alert_correlation_fingerprint_window_minutes=720, got %v", v)
	}
}

// TestHandleGeneralSettings_FingerprintWindowMinutes_InvalidValue verifies that
// values outside [1, 10080] are rejected with HTTP 400.
func TestHandleGeneralSettings_FingerprintWindowMinutes_InvalidValue(t *testing.T) {
	cases := []struct {
		name  string
		value int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 10081},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testhelpers.NewGlobalSQLiteDB(t,
				&database.GeneralSettings{},
			)

			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

			body, _ := json.Marshal(map[string]interface{}{
				"alert_correlation_fingerprint_window_minutes": tc.value,
			})
			req := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.handleGeneralSettings(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("value=%d: expected 400, got %d: %s", tc.value, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestHandleGeneralSettings_CrossFieldValidation verifies that the cross-field
// check (fpMins <= lwDays*1440) accepts boundary values and rejects only values
// that strictly exceed the long window.
func TestHandleGeneralSettings_CrossFieldValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   map[string]interface{}
		wantOK bool
	}{
		{
			"max fingerprint window with default long window (boundary, must accept)",
			map[string]interface{}{"alert_correlation_fingerprint_window_minutes": 10080},
			true,
		},
		{
			"min long window with default fingerprint window (boundary, must accept)",
			map[string]interface{}{"alert_correlation_long_window_days": 1},
			true,
		},
		{
			"fingerprint window exceeds long window (must reject)",
			map[string]interface{}{
				"alert_correlation_fingerprint_window_minutes": 10081,
				"alert_correlation_long_window_days":           7,
			},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testhelpers.NewGlobalSQLiteDB(t, &database.GeneralSettings{})
			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

			b, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBuffer(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.handleGeneralSettings(rec, req)

			if tc.wantOK && rec.Code != http.StatusOK {
				t.Errorf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
			}
			if !tc.wantOK && rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
