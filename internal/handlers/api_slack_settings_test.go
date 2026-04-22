package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupSlackSettingsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&database.SlackSettings{}); err != nil {
		t.Fatalf("migrate slack settings: %v", err)
	}

	originalDB := database.DB
	database.DB = db
	t.Cleanup(func() {
		database.DB = originalDB
	})

	return db
}

func createSlackSettingsFixture(t *testing.T, db *gorm.DB) database.SlackSettings {
	t.Helper()

	settings := database.SlackSettings{
		BotToken:      "xoxb-test-bot-token-1234",
		SigningSecret: "signing-secret-5678",
		AppToken:      "xapp-test-app-token-9012",
		AlertsChannel: "C-alerts",
		Enabled:       true,
	}

	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("create slack settings fixture: %v", err)
	}

	return settings
}

func TestAPIHandler_HandleSlackSettings_GetReturnsMaskedConfiguredSettings(t *testing.T) {
	db := setupSlackSettingsTestDB(t)
	fixture := createSlackSettingsFixture(t, db)

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/slack", nil)
	w := httptest.NewRecorder()

	h.handleSlackSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got["bot_token"] != "****1234" {
		t.Errorf("bot_token = %v, want %q", got["bot_token"], "****1234")
	}
	if got["signing_secret"] != "****5678" {
		t.Errorf("signing_secret = %v, want %q", got["signing_secret"], "****5678")
	}
	if got["app_token"] != "****9012" {
		t.Errorf("app_token = %v, want %q", got["app_token"], "****9012")
	}
	if got["alerts_channel"] != fixture.AlertsChannel {
		t.Errorf("alerts_channel = %v, want %q", got["alerts_channel"], fixture.AlertsChannel)
	}
	if got["enabled"] != fixture.Enabled {
		t.Errorf("enabled = %v, want %v", got["enabled"], fixture.Enabled)
	}
	if got["is_configured"] != true {
		t.Errorf("is_configured = %v, want true", got["is_configured"])
	}
}

func TestAPIHandler_HandleSlackSettings_PutUpdatesOnlyProvidedFields(t *testing.T) {
	db := setupSlackSettingsTestDB(t)
	fixture := createSlackSettingsFixture(t, db)

	payload := map[string]any{
		"alerts_channel": "C-critical",
		"enabled":        false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/slack", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.handleSlackSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var updated database.SlackSettings
	if err := db.First(&updated, fixture.ID).Error; err != nil {
		t.Fatalf("reload updated slack settings: %v", err)
	}

	if updated.AlertsChannel != "C-critical" {
		t.Errorf("alerts_channel = %q, want %q", updated.AlertsChannel, "C-critical")
	}
	if updated.Enabled {
		t.Errorf("enabled = %v, want false", updated.Enabled)
	}
	if updated.BotToken != fixture.BotToken {
		t.Errorf("bot_token changed unexpectedly: got %q want %q", updated.BotToken, fixture.BotToken)
	}
	if updated.SigningSecret != fixture.SigningSecret {
		t.Errorf("signing_secret changed unexpectedly: got %q want %q", updated.SigningSecret, fixture.SigningSecret)
	}
	if updated.AppToken != fixture.AppToken {
		t.Errorf("app_token changed unexpectedly: got %q want %q", updated.AppToken, fixture.AppToken)
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["alerts_channel"] != "C-critical" {
		t.Errorf("response alerts_channel = %v, want %q", got["alerts_channel"], "C-critical")
	}
	if got["enabled"] != false {
		t.Errorf("response enabled = %v, want false", got["enabled"])
	}
	if got["bot_token"] != "****1234" {
		t.Errorf("response bot_token = %v, want %q", got["bot_token"], "****1234")
	}
}

func TestAPIHandler_HandleSlackSettings_PutRejectsInvalidJSON(t *testing.T) {
	setupSlackSettingsTestDB(t)
	createSlackSettingsFixture(t, database.GetDB())

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/slack", bytes.NewBufferString("{"))
	w := httptest.NewRecorder()

	h.handleSlackSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
