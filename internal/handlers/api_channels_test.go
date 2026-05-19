package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// TestHandleChannels_MasksIntegrationCredentials asserts the eagerly-preloaded
// Integration on a Channel response carries masked credentials. Otherwise the
// /api/channels surface would re-expose the secrets that /api/integrations
// already masks.
func TestHandleChannels_MasksIntegrationCredentials(t *testing.T) {
	creds := database.JSONB{"bot_token": "xoxb-SECRET-1234"}
	mgr := &mockChannelManager{
		channels: []database.Channel{{
			ID:           1,
			UUID:         "c1",
			ExternalID:   "#ops",
			CanPost:      true,
			Integration:  database.Integration{ID: 9, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "Slack", Credentials: creds, Enabled: true},
		}},
	}
	h := newHandlerWithChannelManager(mgr)
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "xoxb-SECRET-1234") {
		t.Fatalf("channel response leaked raw secret via embedded integration: %s", body)
	}
	if !strings.Contains(body, "****1234") {
		t.Fatalf("expected masked credential in embedded integration: %s", body)
	}
}

// TestHandleChannels_List exercises the happy path of GET /api/channels.
func TestHandleChannels_List(t *testing.T) {
	mgr := &mockChannelManager{
		channels: []database.Channel{
			{ID: 1, UUID: "c1", ExternalID: "#ops", CanPost: true},
			{ID: 2, UUID: "c2", ExternalID: "#alerts", CanListen: true},
		},
	}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []database.Channel
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(got))
	}
}

// TestHandleChannels_List_FilterByCanPost confirms the boolean query param
// reaches the service-layer filter.
func TestHandleChannels_List_FilterByCanPost(t *testing.T) {
	mgr := &mockChannelManager{
		channels: []database.Channel{
			{ID: 1, UUID: "c1", ExternalID: "#ops", CanPost: true},
			{ID: 2, UUID: "c2", ExternalID: "#alerts", CanListen: true},
		},
	}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/channels?can_post=true", nil)
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if mgr.lastListChannelsFilter.CanPost == nil || !*mgr.lastListChannelsFilter.CanPost {
		t.Fatalf("can_post filter not propagated: %+v", mgr.lastListChannelsFilter)
	}
	var got []database.Channel
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 1 || got[0].UUID != "c1" {
		t.Fatalf("unexpected filtered list: %+v", got)
	}
}

// TestHandleChannels_List_InvalidBoolean returns 400 instead of silently
// ignoring a malformed query param.
func TestHandleChannels_List_InvalidBoolean(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{})
	req := httptest.NewRequest(http.MethodGet, "/api/channels?can_listen=maybe", nil)
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleChannels_Create persists a slack channel attached to an
// existing integration.
func TestHandleChannels_Create(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "intu", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{
		IntegrationUUID: "intu",
		ExternalID:      "#ops",
		DisplayName:     "Ops",
		CanPost:         true,
		IsDefaultPost:   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastCreateChannel == nil {
		t.Fatal("CreateChannel was not invoked")
	}
	if mgr.lastCreateChannel.IntegrationID != 1 {
		t.Fatalf("integration not resolved: %d", mgr.lastCreateChannel.IntegrationID)
	}
}

// TestHandleChannels_Create_RequiresIntegrationUUID rejects payloads missing
// the integration pointer.
func TestHandleChannels_Create_RequiresIntegrationUUID(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{ExternalID: "#ops"})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleChannels_Create_RequiresExternalID rejects payloads missing the
// channel handle.
func TestHandleChannels_Create_RequiresExternalID(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "intu", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{IntegrationUUID: "intu"})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleChannels_Create_RejectsBadSlackExternalID prevents commas or
// spaces in slack channel handles since the legacy multi-channel parser
// treats commas as separators.
func TestHandleChannels_Create_RejectsBadSlackExternalID(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "intu", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{IntegrationUUID: "intu", ExternalID: "#a, #b"})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleChannels_Create_IntegrationMissing surfaces ErrIntegrationNotFound
// as a 404 — the integration pointer is the only required FK.
func TestHandleChannels_Create_IntegrationMissing(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{IntegrationUUID: "nope", ExternalID: "#ops"})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestHandleChannels_Create_DuplicateDefault confirms the "one default per
// provider" invariant maps to 409.
func TestHandleChannels_Create_DuplicateDefault(t *testing.T) {
	mgr := &mockChannelManager{
		integrations:     []database.Integration{{ID: 1, UUID: "intu", Provider: database.MessagingProviderSlack}},
		createChannelErr: services.ErrDuplicateDefaultPost,
	}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateChannelRequest{IntegrationUUID: "intu", ExternalID: "#ops", IsDefaultPost: true})
	req := httptest.NewRequest(http.MethodPost, "/api/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// TestHandleChannelByUUID_Get returns the channel JSON.
func TestHandleChannelByUUID_Get(t *testing.T) {
	mgr := &mockChannelManager{channels: []database.Channel{{ID: 1, UUID: "c1", ExternalID: "#ops"}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/c1", nil)
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestHandleChannelByUUID_NotFound translates ErrChannelNotFound to 404.
func TestHandleChannelByUUID_NotFound(t *testing.T) {
	mgr := &mockChannelManager{getChannelErr: services.ErrChannelNotFound}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/missing", nil)
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestHandleChannelByUUID_Update applies the patch and returns 200.
func TestHandleChannelByUUID_Update(t *testing.T) {
	mgr := &mockChannelManager{channels: []database.Channel{{ID: 1, UUID: "c1", DisplayName: "Old"}}}
	h := newHandlerWithChannelManager(mgr)

	newDisplay := "Renamed"
	body, _ := json.Marshal(UpdateChannelRequest{DisplayName: &newDisplay})
	req := httptest.NewRequest(http.MethodPut, "/api/channels/c1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if mgr.lastUpdateChannel == nil || mgr.lastUpdateChannel.DisplayName == nil || *mgr.lastUpdateChannel.DisplayName != "Renamed" {
		t.Fatalf("display patch not propagated: %+v", mgr.lastUpdateChannel)
	}
}

// TestHandleChannelByUUID_Update_RejectsBadSlackExternalID mirrors the
// create-time guard: an operator renaming a Slack channel must not slip in
// commas or spaces that the legacy comma-delimited listener parsing would
// then split across channels.
func TestHandleChannelByUUID_Update_RejectsBadSlackExternalID(t *testing.T) {
	mgr := &mockChannelManager{channels: []database.Channel{{
		ID: 1, UUID: "c1",
		Integration: database.Integration{ID: 7, Provider: database.MessagingProviderSlack},
	}}}
	h := newHandlerWithChannelManager(mgr)

	bad := "#a, #b"
	body, _ := json.Marshal(UpdateChannelRequest{ExternalID: &bad})
	req := httptest.NewRequest(http.MethodPut, "/api/channels/c1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid slack external_id, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastUpdateChannel != nil {
		t.Fatalf("UpdateChannel should not be invoked on validation failure, got patch %+v", mgr.lastUpdateChannel)
	}
}

// TestHandleChannelByUUID_Update_DuplicateDefault returns 409 when the
// service-layer guard rejects a second per-provider default.
func TestHandleChannelByUUID_Update_DuplicateDefault(t *testing.T) {
	mgr := &mockChannelManager{
		channels:         []database.Channel{{ID: 1, UUID: "c1"}},
		updateChannelErr: services.ErrDuplicateDefaultPost,
	}
	h := newHandlerWithChannelManager(mgr)

	def := true
	body, _ := json.Marshal(UpdateChannelRequest{IsDefaultPost: &def})
	req := httptest.NewRequest(http.MethodPut, "/api/channels/c1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

// TestHandleChannelByUUID_Delete removes the row.
func TestHandleChannelByUUID_Delete(t *testing.T) {
	mgr := &mockChannelManager{channels: []database.Channel{{ID: 1, UUID: "c1"}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/c1", nil)
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if len(mgr.channels) != 0 {
		t.Fatalf("channel not removed: %+v", mgr.channels)
	}
}

// TestHandleChannelByUUID_MethodNotAllowed rejects PATCH.
func TestHandleChannelByUUID_MethodNotAllowed(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{channels: []database.Channel{{UUID: "c1"}}})
	req := httptest.NewRequest(http.MethodPatch, "/api/channels/c1", nil)
	w := httptest.NewRecorder()
	h.handleChannelByUUID(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleChannels_NotConfigured asserts 503 when the service is unset.
func TestHandleChannels_NotConfigured(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.handleChannels(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
