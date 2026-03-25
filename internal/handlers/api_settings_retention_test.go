package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRetentionHandlerTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	if err := db.AutoMigrate(&database.RetentionSettings{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	db.Exec("DELETE FROM retention_settings")

	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
}

func TestHandleRetentionSettings_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	methods := []string{http.MethodPost, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/settings/retention", nil)
			w := httptest.NewRecorder()

			h.handleRetentionSettings(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405, got %d", w.Code)
			}
		})
	}
}

func TestHandleRetentionSettings_PUT_InvalidJSON(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/retention", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()

	h.handleRetentionSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRetentionSettings_GET_ReturnsDefaults(t *testing.T) {
	setupRetentionHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/retention", nil)
	w := httptest.NewRecorder()

	h.handleRetentionSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var settings database.RetentionSettings
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !settings.Enabled {
		t.Error("expected default Enabled=true")
	}
	if settings.RetentionDays != 90 {
		t.Errorf("expected default RetentionDays=90, got %d", settings.RetentionDays)
	}
	if settings.CleanupIntervalHours != 6 {
		t.Errorf("expected default CleanupIntervalHours=6, got %d", settings.CleanupIntervalHours)
	}
}

func TestHandleRetentionSettings_PUT_ValidUpdate(t *testing.T) {
	setupRetentionHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	body := `{"enabled": false, "retention_days": 30, "cleanup_interval_hours": 12}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/retention", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleRetentionSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var settings database.RetentionSettings
	if err := json.NewDecoder(w.Body).Decode(&settings); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if settings.Enabled {
		t.Error("expected Enabled=false after update")
	}
	if settings.RetentionDays != 30 {
		t.Errorf("expected RetentionDays=30, got %d", settings.RetentionDays)
	}
	if settings.CleanupIntervalHours != 12 {
		t.Errorf("expected CleanupIntervalHours=12, got %d", settings.CleanupIntervalHours)
	}
}

func TestHandleRetentionSettings_PUT_ValidationBounds(t *testing.T) {
	setupRetentionHandlerTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name string
		body string
	}{
		{"retention_days_zero", `{"retention_days": 0}`},
		{"retention_days_negative", `{"retention_days": -1}`},
		{"retention_days_too_high", `{"retention_days": 3651}`},
		{"cleanup_interval_zero", `{"cleanup_interval_hours": 0}`},
		{"cleanup_interval_negative", `{"cleanup_interval_hours": -1}`},
		{"cleanup_interval_too_high", `{"cleanup_interval_hours": 8761}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/settings/retention", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			h.handleRetentionSettings(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
