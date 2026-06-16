package handlers

import (
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
}
