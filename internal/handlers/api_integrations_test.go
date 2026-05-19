package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/akmatori/akmatori/internal/services"
)

// mockChannelManager implements services.ChannelManager so the integrations
// and channels handlers can be tested without spinning up a database. Each
// call records its arguments so assertions can inspect the last invocation.
type mockChannelManager struct {
	integrations []database.Integration
	channels     []database.Channel

	listIntegrationsErr  error
	getIntegrationErr    error
	createIntegrationErr error
	updateIntegrationErr error
	deleteIntegrationErr error

	listChannelsErr  error
	getChannelErr    error
	createChannelErr error
	updateChannelErr error
	deleteChannelErr error

	resolveDefaultErr     error
	resolveAlertSourceErr error

	lastCreateIntegration *database.Integration
	lastUpdateName        *string
	lastUpdateCreds       database.JSONB
	lastUpdateEnabled     *bool

	lastListChannelsFilter services.ListChannelsFilter
	lastCreateChannel      *database.Channel
	lastUpdateChannel      *services.ChannelUpdate
}

func (m *mockChannelManager) ListIntegrations() ([]database.Integration, error) {
	if m.listIntegrationsErr != nil {
		return nil, m.listIntegrationsErr
	}
	return m.integrations, nil
}

func (m *mockChannelManager) GetIntegrationByUUID(uuid string) (*database.Integration, error) {
	if m.getIntegrationErr != nil {
		return nil, m.getIntegrationErr
	}
	for i := range m.integrations {
		if m.integrations[i].UUID == uuid {
			out := m.integrations[i]
			return &out, nil
		}
	}
	return nil, services.ErrIntegrationNotFound
}

func (m *mockChannelManager) CreateIntegration(provider database.MessagingProvider, name string, credentials database.JSONB, enabled bool) (*database.Integration, error) {
	if m.createIntegrationErr != nil {
		return nil, m.createIntegrationErr
	}
	row := &database.Integration{
		ID:          uint(len(m.integrations) + 1),
		UUID:        "uuid-int-" + name,
		Provider:    provider,
		Name:        name,
		Credentials: credentials,
		Enabled:     enabled,
	}
	m.lastCreateIntegration = row
	m.integrations = append(m.integrations, *row)
	return row, nil
}

func (m *mockChannelManager) UpdateIntegration(uuid string, name *string, credentials database.JSONB, enabled *bool) (*database.Integration, error) {
	if m.updateIntegrationErr != nil {
		return nil, m.updateIntegrationErr
	}
	for i := range m.integrations {
		if m.integrations[i].UUID == uuid {
			m.lastUpdateName = name
			m.lastUpdateCreds = credentials
			m.lastUpdateEnabled = enabled
			if name != nil {
				m.integrations[i].Name = *name
			}
			if credentials != nil {
				m.integrations[i].Credentials = credentials
			}
			if enabled != nil {
				m.integrations[i].Enabled = *enabled
			}
			out := m.integrations[i]
			return &out, nil
		}
	}
	return nil, services.ErrIntegrationNotFound
}

func (m *mockChannelManager) DeleteIntegration(uuid string) error {
	if m.deleteIntegrationErr != nil {
		return m.deleteIntegrationErr
	}
	for i := range m.integrations {
		if m.integrations[i].UUID == uuid {
			m.integrations = append(m.integrations[:i], m.integrations[i+1:]...)
			return nil
		}
	}
	return services.ErrIntegrationNotFound
}

func (m *mockChannelManager) ListChannels(filter services.ListChannelsFilter) ([]database.Channel, error) {
	m.lastListChannelsFilter = filter
	if m.listChannelsErr != nil {
		return nil, m.listChannelsErr
	}
	out := make([]database.Channel, 0, len(m.channels))
	for _, c := range m.channels {
		if filter.CanPost != nil && c.CanPost != *filter.CanPost {
			continue
		}
		if filter.CanListen != nil && c.CanListen != *filter.CanListen {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *mockChannelManager) GetChannelByUUID(uuid string) (*database.Channel, error) {
	if m.getChannelErr != nil {
		return nil, m.getChannelErr
	}
	for i := range m.channels {
		if m.channels[i].UUID == uuid {
			out := m.channels[i]
			return &out, nil
		}
	}
	return nil, services.ErrChannelNotFound
}

func (m *mockChannelManager) CreateChannel(c *database.Channel) (*database.Channel, error) {
	if m.createChannelErr != nil {
		return nil, m.createChannelErr
	}
	c.ID = uint(len(m.channels) + 1)
	if c.UUID == "" {
		c.UUID = "uuid-ch-" + c.ExternalID
	}
	m.lastCreateChannel = c
	m.channels = append(m.channels, *c)
	return c, nil
}

func (m *mockChannelManager) UpdateChannel(uuid string, patch services.ChannelUpdate) (*database.Channel, error) {
	if m.updateChannelErr != nil {
		return nil, m.updateChannelErr
	}
	m.lastUpdateChannel = &patch
	for i := range m.channels {
		if m.channels[i].UUID == uuid {
			if patch.DisplayName != nil {
				m.channels[i].DisplayName = *patch.DisplayName
			}
			if patch.IsDefaultPost != nil {
				m.channels[i].IsDefaultPost = *patch.IsDefaultPost
			}
			out := m.channels[i]
			return &out, nil
		}
	}
	return nil, services.ErrChannelNotFound
}

func (m *mockChannelManager) DeleteChannel(uuid string) error {
	if m.deleteChannelErr != nil {
		return m.deleteChannelErr
	}
	for i := range m.channels {
		if m.channels[i].UUID == uuid {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)
			return nil
		}
	}
	return services.ErrChannelNotFound
}

func (m *mockChannelManager) ResolveDefault(provider database.MessagingProvider) (*database.Channel, error) {
	if m.resolveDefaultErr != nil {
		return nil, m.resolveDefaultErr
	}
	for i := range m.channels {
		if m.channels[i].IsDefaultPost && m.channels[i].Integration.Provider == provider {
			out := m.channels[i]
			return &out, nil
		}
	}
	return nil, services.ErrChannelNotFound
}

func (m *mockChannelManager) ResolveForAlertSource(asi *database.AlertSourceInstance, provider database.MessagingProvider) (*database.Channel, error) {
	if m.resolveAlertSourceErr != nil {
		return nil, m.resolveAlertSourceErr
	}
	return m.ResolveDefault(provider)
}

func newHandlerWithChannelManager(mgr services.ChannelManager) *APIHandler {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetChannelManager(mgr)
	return h
}

// TestHandleIntegrations_List exercises the happy path of GET /api/integrations.
func TestHandleIntegrations_List(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "Slack"}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got []database.Integration
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "u1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

// TestHandleIntegrations_ListError ensures internal failures surface as 500.
func TestHandleIntegrations_ListError(t *testing.T) {
	mgr := &mockChannelManager{listIntegrationsErr: errors.New("boom")}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestHandleIntegrations_Create persists a new slack integration.
func TestHandleIntegrations_Create(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateIntegrationRequest{
		Provider:    "slack",
		Name:        "Slack Prod",
		Credentials: database.JSONB{"bot_token": "xoxb-test"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastCreateIntegration == nil || mgr.lastCreateIntegration.Provider != database.MessagingProviderSlack {
		t.Fatalf("integration not created with slack provider: %+v", mgr.lastCreateIntegration)
	}
	if mgr.lastCreateIntegration.Name != "Slack Prod" {
		t.Fatalf("name not propagated: %q", mgr.lastCreateIntegration.Name)
	}
}

// TestHandleIntegrations_Create_InvalidProvider asserts unknown providers
// surface as 400 rather than 500. The validation rejects values that are
// neither in the model whitelist nor registered in the provider registry.
func TestHandleIntegrations_Create_InvalidProvider(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateIntegrationRequest{Provider: "discord", Name: "Discord"})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleIntegrations_Create_RequiredFields rejects empty payloads.
func TestHandleIntegrations_Create_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		body CreateIntegrationRequest
	}{
		{name: "missing provider", body: CreateIntegrationRequest{Name: "x"}},
		{name: "missing name", body: CreateIntegrationRequest{Provider: "slack"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandlerWithChannelManager(&mockChannelManager{})
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.handleIntegrations(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		})
	}
}

// TestHandleIntegrations_NotConfigured asserts the handler degrades gracefully
// when the service is unset (e.g. running tests without main wiring).
func TestHandleIntegrations_NotConfigured(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_Get exercises the by-UUID lookup.
func TestHandleIntegrationByUUID_Get(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/u1", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_NotFound returns 404 when the row is absent.
func TestHandleIntegrationByUUID_NotFound(t *testing.T) {
	mgr := &mockChannelManager{getIntegrationErr: services.ErrIntegrationNotFound}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/missing", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_Update patches the integration.
func TestHandleIntegrationByUUID_Update(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "old"}}}
	h := newHandlerWithChannelManager(mgr)

	newName := "Renamed"
	body, _ := json.Marshal(UpdateIntegrationRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/u1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastUpdateName == nil || *mgr.lastUpdateName != "Renamed" {
		t.Fatalf("name patch not applied: %+v", mgr.lastUpdateName)
	}
}

// TestHandleIntegrationByUUID_Delete removes the row.
func TestHandleIntegrationByUUID_Delete(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1"}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodDelete, "/api/integrations/u1", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if len(mgr.integrations) != 0 {
		t.Fatalf("integration not removed: %+v", mgr.integrations)
	}
}

// TestHandleIntegrationByUUID_MethodNotAllowed rejects PATCH.
func TestHandleIntegrationByUUID_MethodNotAllowed(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{integrations: []database.Integration{{UUID: "u1"}}})
	req := httptest.NewRequest(http.MethodPatch, "/api/integrations/u1", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleIntegrations_DuplicateDefault surfaces the service-layer guard as
// a 409 — this is the cross-integration "one default per provider" invariant
// that the partial-unique index in postgres also enforces.
func TestHandleIntegrations_Create_PropagatesValidationError(t *testing.T) {
	mgr := &mockChannelManager{createIntegrationErr: errors.New("integration name cannot be empty")}
	h := newHandlerWithChannelManager(mgr)

	body, _ := json.Marshal(CreateIntegrationRequest{Provider: "slack", Name: "Slack"})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for plain validation message, got %d", w.Code)
	}
}

// TestHandleIntegrations_Create_TriggersAlertChannelReload asserts that
// creating a Slack integration fires the listener-channel reload callback so
// channels gated on Integration.Enabled get re-evaluated against the new row
// without an API restart.
func TestHandleIntegrations_Create_TriggersAlertChannelReload(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)
	reloaded := make(chan struct{}, 1)
	h.SetAlertChannelReloader(func() { reloaded <- struct{}{} })

	body, _ := json.Marshal(CreateIntegrationRequest{
		Provider:    "slack",
		Name:        "Slack Prod",
		Credentials: database.JSONB{"bot_token": "xoxb-test"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-reloaded:
		// expected
	case <-time.After(time.Second):
		t.Fatal("expected alert channel reloader to fire after integration create")
	}
}

// TestHandleIntegrationByUUID_Update_TriggersReload asserts that an integration
// PUT triggers the listener-channel reload so a credential rotation or enabled
// toggle is reflected in the running Slack handler.
func TestHandleIntegrationByUUID_Update_TriggersReload(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "old"}}}
	h := newHandlerWithChannelManager(mgr)
	reloaded := make(chan struct{}, 1)
	h.SetAlertChannelReloader(func() { reloaded <- struct{}{} })

	newName := "Renamed"
	body, _ := json.Marshal(UpdateIntegrationRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/u1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-reloaded:
	case <-time.After(time.Second):
		t.Fatal("expected alert channel reloader to fire after integration update")
	}
}

// TestHandleIntegrationByUUID_Delete_TriggersReload asserts that deleting an
// integration triggers the listener-channel reload — the cascade-delete on the
// service side removes listener channels in the DB, but the in-memory map
// would otherwise keep posting events to channels that no longer exist.
func TestHandleIntegrationByUUID_Delete_TriggersReload(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)
	reloaded := make(chan struct{}, 1)
	h.SetAlertChannelReloader(func() { reloaded <- struct{}{} })

	req := httptest.NewRequest(http.MethodDelete, "/api/integrations/u1", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	select {
	case <-reloaded:
	case <-time.After(time.Second):
		t.Fatal("expected alert channel reloader to fire after integration delete")
	}
}

// TestHandleSlackSettings_ReturnsGoneAfterRetirement asserts the legacy
// settings endpoint surfaces 410 Gone with a clear pointer to the replacement
// surface. The endpoint is retained only so clients on the old URL receive a
// machine-readable signal to migrate; any access (regardless of channel
// service wiring) must return 410.
func TestHandleSlackSettings_ReturnsGoneAfterRetirement(t *testing.T) {
	cases := []struct {
		name string
		mgr  services.ChannelManager
	}{
		{"with channel manager wired", &mockChannelManager{}},
		{"without channel manager", nil},
	}
	methods := []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete}
	for _, tc := range cases {
		for _, m := range methods {
			t.Run(tc.name+"/"+m, func(t *testing.T) {
				h := newHandlerWithChannelManager(tc.mgr)

				req := httptest.NewRequest(m, "/api/settings/slack", nil)
				w := httptest.NewRecorder()
				h.handleSlackSettings(w, req)

				if w.Code != http.StatusGone {
					t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
				}
				if body := w.Body.String(); !strings.Contains(body, "/api/integrations") || !strings.Contains(body, "/api/channels") {
					t.Errorf("expected response body to point to /api/integrations and /api/channels, got %q", body)
				}
			})
		}
	}
}

// stubProviderRegistry is a minimal services.ProviderRegistry used to verify
// integrations validation consults the registry when wired.
type stubProviderRegistry struct {
	known map[database.MessagingProvider]bool
}

func (s *stubProviderRegistry) Get(name database.MessagingProvider) (messaging.Provider, error) {
	if s.known[name] {
		return nil, nil
	}
	return nil, messaging.ErrProviderNotRegistered
}

func (s *stubProviderRegistry) List() []database.MessagingProvider {
	out := make([]database.MessagingProvider, 0, len(s.known))
	for k := range s.known {
		out = append(out, k)
	}
	return out
}

// TestHandleIntegrations_AcceptsRegistryProviderEvenIfModelDoesNot covers the
// case where a deployment registers a non-model provider name (e.g. a future
// connector). The handler should accept it because the registry confirms the
// runtime can address it.
func TestHandleIntegrations_AcceptsRegistryProviderEvenIfModelDoesNot(t *testing.T) {
	mgr := &mockChannelManager{}
	h := newHandlerWithChannelManager(mgr)
	h.SetProviderRegistry(&stubProviderRegistry{known: map[database.MessagingProvider]bool{
		"slack": true,
	}})

	body, _ := json.Marshal(CreateIntegrationRequest{Provider: "slack", Name: "Slack"})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
}

// TestHandleIntegrations_Create_InvalidJSON returns 400 for malformed bodies.
func TestHandleIntegrations_Create_InvalidJSON(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{})
	req := httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleIntegrations_MethodNotAllowed rejects unsupported verbs on the
// collection endpoint.
func TestHandleIntegrations_MethodNotAllowed(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{})
	req := httptest.NewRequest(http.MethodPatch, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_ServiceUnavailable returns 503 when the channel
// service is not wired.
func TestHandleIntegrationByUUID_ServiceUnavailable(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/u1", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_InvalidUUID rejects paths with embedded slashes
// rather than treating them as nested resources.
func TestHandleIntegrationByUUID_InvalidUUID(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{})
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/u1/extra", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_Update_InvalidJSON guards the PUT decode path.
func TestHandleIntegrationByUUID_Update_InvalidJSON(t *testing.T) {
	h := newHandlerWithChannelManager(&mockChannelManager{integrations: []database.Integration{{UUID: "u1"}}})
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/u1", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestHandleIntegrationByUUID_Update_PropagatesCredentials confirms the JSONB
// blob travels intact from request body to the service layer patch.
func TestHandleIntegrationByUUID_Update_PropagatesCredentials(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack}}}
	h := newHandlerWithChannelManager(mgr)

	creds := database.JSONB{"bot_token": "xoxb-new"}
	body, _ := json.Marshal(UpdateIntegrationRequest{Credentials: &creds})
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/u1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.lastUpdateCreds["bot_token"] != "xoxb-new" {
		t.Errorf("credentials not propagated: %+v", mgr.lastUpdateCreds)
	}
}

// TestHandleIntegrations_MasksCredentialsInResponses asserts that secret
// credential fields (bot_token, signing_secret, app_token) are masked in API
// responses — preserving the posture of the retired /api/settings/slack
// endpoint, which never echoed live tokens back to authenticated callers.
func TestHandleIntegrations_MasksCredentialsInResponses(t *testing.T) {
	creds := database.JSONB{
		"bot_token":      "xoxb-LONG-SECRET-1234",
		"signing_secret": "abcdef0123456789",
		"app_token":      "xapp-VERY-SECRET-AAAA",
	}
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "Slack", Credentials: creds, Enabled: true}}}
	h := newHandlerWithChannelManager(mgr)

	// GET /api/integrations
	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, raw := range []string{"xoxb-LONG-SECRET-1234", "abcdef0123456789", "xapp-VERY-SECRET-AAAA"} {
		if strings.Contains(body, raw) {
			t.Fatalf("list response leaked raw secret %q in body: %s", raw, body)
		}
	}
	if !strings.Contains(body, "****1234") || !strings.Contains(body, "****6789") || !strings.Contains(body, "****AAAA") {
		t.Fatalf("expected masked credentials with last-4 suffixes in body: %s", body)
	}

	// GET /api/integrations/u1
	req = httptest.NewRequest(http.MethodGet, "/api/integrations/u1", nil)
	w = httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "xoxb-LONG-SECRET-1234") {
		t.Fatalf("by-uuid response leaked raw secret: %s", w.Body.String())
	}

	// POST /api/integrations — create response must also be masked
	postBody, _ := json.Marshal(CreateIntegrationRequest{
		Provider:    "slack",
		Name:        "New Slack",
		Credentials: creds,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/integrations", bytes.NewReader(postBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "xoxb-LONG-SECRET-1234") {
		t.Fatalf("create response leaked raw secret: %s", w.Body.String())
	}
}

// TestHandleIntegrations_MasksUnknownProviderSecrets verifies the
// substring-matching masking catches credential keys that no provider has yet
// registered. Future providers (Telegram, on-prem bots, etc.) whose secret
// keys aren't enumerated must still be redacted on the wire.
func TestHandleIntegrations_MasksUnknownProviderSecrets(t *testing.T) {
	creds := database.JSONB{
		"telegram_bot_token": "tg-LONG-SECRET-9999",
		"some_password":      "supersecret-7777",
		"private_key":        "PRIVATE-KEY-AAAA",
		"webhook_url":        "https://example.com/hook?secret=BBBB",
		"workspace_id":       "T01234567",
	}
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "u1", Provider: database.MessagingProviderSlack, Name: "Slack", Credentials: creds, Enabled: true}}}
	h := newHandlerWithChannelManager(mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	h.handleIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, raw := range []string{"tg-LONG-SECRET-9999", "supersecret-7777", "PRIVATE-KEY-AAAA", "secret=BBBB"} {
		if strings.Contains(body, raw) {
			t.Fatalf("response leaked raw secret %q (substring matcher missed it): %s", raw, body)
		}
	}
	// Non-secret identifier should remain in plaintext so the UI can render
	// "configured workspace: T01234567".
	if !strings.Contains(body, "T01234567") {
		t.Fatalf("expected non-secret identifier to remain in plaintext: %s", body)
	}
}

// TestHandleIntegrationByUUID_Delete_PropagatesNotFound surfaces a 404 when
// the service reports the integration is gone.
func TestHandleIntegrationByUUID_Delete_PropagatesNotFound(t *testing.T) {
	mgr := &mockChannelManager{deleteIntegrationErr: services.ErrIntegrationNotFound}
	h := newHandlerWithChannelManager(mgr)
	req := httptest.NewRequest(http.MethodDelete, "/api/integrations/ghost", nil)
	w := httptest.NewRecorder()
	h.handleIntegrationByUUID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
