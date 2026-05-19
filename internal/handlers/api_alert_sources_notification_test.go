package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// notifAlertManager is a focused mock that records UpdateInstance calls so the
// notification_channel_uuid resolution behaviour in api_alert_sources can be
// asserted without spinning up a database. It satisfies services.AlertManager.
type notifAlertManager struct {
	mockAlertManager

	createdInstance *database.AlertSourceInstance
	createErr       error
	updateErr       error

	lastUpdates map[string]interface{}
}

func (m *notifAlertManager) CreateInstance(sourceTypeName, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createdInstance == nil {
		m.createdInstance = &database.AlertSourceInstance{UUID: "asi-uuid-1", Name: name}
	}
	return m.createdInstance, nil
}

func (m *notifAlertManager) UpdateInstance(uuid string, updates map[string]interface{}) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.lastUpdates = updates
	return nil
}

func (m *notifAlertManager) GetInstanceByUUID(uuid string) (*database.AlertSourceInstance, error) {
	if m.createdInstance == nil {
		return nil, nil
	}
	out := *m.createdInstance
	return &out, nil
}

// helper: spin up an APIHandler with the notification mock + a channel mock
// preloaded with a single channel reachable by UUID "ch-1".
func newAlertSourcesHandler(t *testing.T) (*APIHandler, *notifAlertManager, *mockChannelManager) {
	t.Helper()
	alertMgr := &notifAlertManager{}
	chMgr := &mockChannelManager{
		channels: []database.Channel{
			{ID: 42, UUID: "ch-1", IntegrationID: 1, ExternalID: "C_PROD", CanPost: true},
		},
	}
	h := NewAPIHandler(nil, nil, nil, alertMgr, nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelManager(chMgr)
	return h, alertMgr, chMgr
}

func TestHandleAlertSources_Create_SetsNotificationChannelFromUUID(t *testing.T) {
	h, alertMgr, _ := newAlertSourcesHandler(t)

	body := map[string]interface{}{
		"source_type_name":          "alertmanager",
		"name":                      "Prod AM",
		"notification_channel_uuid": "ch-1",
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/alert-sources", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSources(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if alertMgr.lastUpdates == nil {
		t.Fatalf("expected UpdateInstance to be called to set notification_channel_id")
	}
	got, ok := alertMgr.lastUpdates["notification_channel_id"]
	if !ok {
		t.Fatalf("expected notification_channel_id in updates, got: %#v", alertMgr.lastUpdates)
	}
	if got != uint(42) {
		t.Fatalf("expected channel ID 42, got %v (%T)", got, got)
	}
}

// TestHandleAlertSources_Create_RejectsNonPostableChannel covers the
// CanPost capability gating: an alert source cannot reference a listen-only
// channel as its notification destination. CLAUDE.md says: "Channel.CanPost /
// Channel.CanListen capability flags gate which triggers may reference a
// channel." The check runs at write time so the operator sees a clean 400
// rather than a silent fall-through to the default at fire time.
func TestHandleAlertSources_Create_RejectsNonPostableChannel(t *testing.T) {
	alertMgr := &notifAlertManager{}
	chMgr := &mockChannelManager{
		channels: []database.Channel{
			{ID: 99, UUID: "ch-listen", IntegrationID: 1, ExternalID: "C_LISTEN", CanPost: false, CanListen: true},
		},
	}
	h := NewAPIHandler(nil, nil, nil, alertMgr, nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelManager(chMgr)

	body := map[string]interface{}{
		"source_type_name":          "alertmanager",
		"name":                      "Prod AM",
		"notification_channel_uuid": "ch-listen",
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/alert-sources", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSources(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-postable channel, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAlertSources_Create_RejectsUnknownChannelUUID(t *testing.T) {
	h, _, _ := newAlertSourcesHandler(t)

	body := map[string]interface{}{
		"source_type_name":          "alertmanager",
		"name":                      "Prod AM",
		"notification_channel_uuid": "does-not-exist",
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/alert-sources", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSources(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown channel UUID, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAlertSources_Update_ClearsChannelOnEmptyString(t *testing.T) {
	h, alertMgr, _ := newAlertSourcesHandler(t)
	// Seed an existing instance the update handler will look up.
	alertMgr.createdInstance = &database.AlertSourceInstance{
		UUID: "asi-existing",
		Name: "Prod AM",
		AlertSourceType: database.AlertSourceType{
			Name: "alertmanager",
		},
	}

	body := map[string]interface{}{
		"notification_channel_uuid": "",
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/alert-sources/asi-existing", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSourceByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if alertMgr.lastUpdates == nil {
		t.Fatalf("expected UpdateInstance to be called")
	}
	val, ok := alertMgr.lastUpdates["notification_channel_id"]
	if !ok {
		t.Fatalf("expected notification_channel_id key, got: %#v", alertMgr.lastUpdates)
	}
	if val != nil {
		t.Fatalf("expected nil (clear) value, got %v", val)
	}
}

func TestHandleAlertSources_Update_SetsChannelFromUUID(t *testing.T) {
	h, alertMgr, _ := newAlertSourcesHandler(t)
	alertMgr.createdInstance = &database.AlertSourceInstance{
		UUID: "asi-existing",
		Name: "Prod AM",
		AlertSourceType: database.AlertSourceType{
			Name: "alertmanager",
		},
	}

	body := map[string]interface{}{
		"notification_channel_uuid": "ch-1",
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/alert-sources/asi-existing", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSourceByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := alertMgr.lastUpdates["notification_channel_id"]; got != uint(42) {
		t.Fatalf("expected channel ID 42, got %v", got)
	}
}

// deprecatedTypeAlertManager extends notifAlertManager so a named lookup
// returns a row flagged Deprecated. Used to exercise the Task 6 rejection
// of slack_channel (and any other deprecated type) in the alert-source POST
// handler.
type deprecatedTypeAlertManager struct {
	notifAlertManager
	deprecatedName string
}

func (m *deprecatedTypeAlertManager) GetAlertSourceTypeByName(name string) (*database.AlertSourceType, error) {
	if name == m.deprecatedName {
		return &database.AlertSourceType{Name: name, Deprecated: true}, nil
	}
	return nil, nil
}

// TestHandleAlertSources_Create_RejectsDeprecatedSourceType asserts that the
// POST handler refuses to create an instance of a deprecated source type. The
// slack_channel type is the live example after Task 6 of the unified-channels
// plan: operators are expected to configure a Channel row instead.
func TestHandleAlertSources_Create_RejectsDeprecatedSourceType(t *testing.T) {
	alertMgr := &deprecatedTypeAlertManager{deprecatedName: "slack_channel"}
	h := NewAPIHandler(nil, nil, nil, alertMgr, nil, nil, nil, nil, nil, nil, nil)

	body := map[string]interface{}{
		"source_type_name": "slack_channel",
		"name":             "Old Slack Channel",
		"settings":         map[string]interface{}{"slack_channel_id": "C12345678"},
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/alert-sources", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.handleAlertSources(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for deprecated source type, got %d: %s", w.Code, w.Body.String())
	}
	if alertMgr.lastUpdates != nil {
		t.Fatalf("UpdateInstance must not be called when create is rejected, got %#v", alertMgr.lastUpdates)
	}
}
