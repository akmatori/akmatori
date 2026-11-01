package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
)

func doJSONRequest(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleIntegrations_BotProvidersRequireToken(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		creds    database.JSONB
		want     int
	}{
		{"telegram missing token", "telegram", nil, http.StatusBadRequest},
		{"telegram blank token", "telegram", database.JSONB{"bot_token": "  "}, http.StatusBadRequest},
		{"discord missing token", "discord", database.JSONB{"other": "x"}, http.StatusBadRequest},
		{"telegram ok", "telegram", database.JSONB{"bot_token": "123456:ABCDEFGHIJKLMNOP"}, http.StatusCreated},
		{"discord ok", "discord", database.JSONB{"bot_token": "MTAxMjM0NTY3ODkw.abcdef.ghijklmnop"}, http.StatusCreated},
		{"slack unchanged (no token check)", "slack", nil, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandlerWithChannelManager(&mockChannelManager{})
			w := doJSONRequest(t, h.handleIntegrations, http.MethodPost, "/api/integrations",
				CreateIntegrationRequest{Provider: tt.provider, Name: "X", Credentials: tt.creds})
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body.String())
			}
			if w.Code == http.StatusBadRequest && !strings.Contains(w.Body.String(), "bot_token") {
				t.Errorf("error should name bot_token: %s", w.Body.String())
			}
		})
	}
}

func TestHandleIntegrations_BotTokenMasked(t *testing.T) {
	const secret = "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	mgr := &mockChannelManager{integrations: []database.Integration{
		{ID: 1, UUID: "tg-1", Provider: database.MessagingProviderTelegram, Name: "TG", Credentials: database.JSONB{"bot_token": secret}},
	}}
	h := newHandlerWithChannelManager(mgr)

	for _, path := range []string{"/api/integrations", "/api/integrations/tg-1"} {
		var w *httptest.ResponseRecorder
		if path == "/api/integrations" {
			w = doJSONRequest(t, h.handleIntegrations, http.MethodGet, path, nil)
		} else {
			w = doJSONRequest(t, h.handleIntegrationByUUID, http.MethodGet, path, nil)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("%s leaks bot_token: %s", path, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"bot_token"`) {
			t.Errorf("%s should still report bot_token (masked): %s", path, w.Body.String())
		}
	}

	// Update with an omitted secret passes empty creds through; the service
	// keeps the stored token (merge semantics), and no create-time check fires.
	w := doJSONRequest(t, h.handleIntegrationByUUID, http.MethodPut, "/api/integrations/tg-1",
		map[string]any{"name": "Renamed", "credentials": map[string]any{"bot_token": ""}})
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d (%s)", w.Code, w.Body.String())
	}
}

func TestHandleChannels_RejectsCanListenForOutboundOnlyProviders(t *testing.T) {
	for _, provider := range []database.MessagingProvider{database.MessagingProviderTelegram, database.MessagingProviderDiscord} {
		t.Run(string(provider), func(t *testing.T) {
			mgr := &mockChannelManager{
				integrations: []database.Integration{{ID: 1, UUID: "int-1", Provider: provider, Name: "X"}},
				channels: []database.Channel{{ID: 1, UUID: "ch-1", IntegrationID: 1, ExternalID: "123",
					Integration: database.Integration{ID: 1, Provider: provider}}},
			}
			h := newHandlerWithChannelManager(mgr)

			w := doJSONRequest(t, h.handleChannels, http.MethodPost, "/api/channels",
				CreateChannelRequest{IntegrationUUID: "int-1", ExternalID: "-100123", CanListen: true, CanPost: true})
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "outbound-only") {
				t.Fatalf("create can_listen: status = %d body = %s", w.Code, w.Body.String())
			}
			if mgr.lastCreateChannel != nil {
				t.Error("channel must not be created")
			}

			w = doJSONRequest(t, h.handleChannels, http.MethodPost, "/api/channels",
				CreateChannelRequest{IntegrationUUID: "int-1", ExternalID: "-100123", CanPost: true})
			if w.Code != http.StatusCreated {
				t.Fatalf("create post-only: status = %d body = %s", w.Code, w.Body.String())
			}

			yes, no := true, false
			w = doJSONRequest(t, h.handleChannelByUUID, http.MethodPut, "/api/channels/ch-1", UpdateChannelRequest{CanListen: &yes})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("update can_listen=true: status = %d", w.Code)
			}
			w = doJSONRequest(t, h.handleChannelByUUID, http.MethodPut, "/api/channels/ch-1", UpdateChannelRequest{CanListen: &no})
			if w.Code != http.StatusOK {
				t.Fatalf("update can_listen=false: status = %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleChannels_SlackCanStillListen(t *testing.T) {
	mgr := &mockChannelManager{integrations: []database.Integration{{ID: 1, UUID: "int-1", Provider: database.MessagingProviderSlack, Name: "S"}}}
	h := newHandlerWithChannelManager(mgr)
	w := doJSONRequest(t, h.handleChannels, http.MethodPost, "/api/channels",
		CreateChannelRequest{IntegrationUUID: "int-1", ExternalID: "C1", CanListen: true})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
}

// testerProvider is a messaging.Provider that also implements
// messaging.CredentialTester.
type testerProvider struct {
	account string
	err     error
	got     *database.Integration
}

func (p *testerProvider) Name() database.MessagingProvider { return database.MessagingProviderTelegram }
func (p *testerProvider) PostMessage(context.Context, *database.Channel, string) (*messaging.PostedMessage, error) {
	return nil, nil
}
func (p *testerProvider) PostThreadReply(context.Context, *database.Channel, string, string) (*messaging.PostedMessage, error) {
	return nil, nil
}
func (p *testerProvider) UpdateMessage(context.Context, *database.Channel, string, string) error {
	return nil
}
func (p *testerProvider) TestCredentials(_ context.Context, in *database.Integration) (string, error) {
	p.got = in
	return p.account, p.err
}

func TestHandleIntegrationTest(t *testing.T) {
	newH := func(p messaging.Provider) *APIHandler {
		mgr := &mockChannelManager{integrations: []database.Integration{
			{ID: 1, UUID: "tg-1", Provider: database.MessagingProviderTelegram, Credentials: database.JSONB{"bot_token": "1:X"}},
		}}
		h := newHandlerWithChannelManager(mgr)
		reg := messaging.NewRegistry()
		if p != nil {
			reg.Register(p)
		}
		h.SetProviderRegistry(reg)
		return h
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder) integrationTestResponse {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
		}
		var out integrationTestResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	p := &testerProvider{account: "@akm_bot"}
	h := newH(p)
	out := decode(t, doJSONRequest(t, h.handleIntegrationByUUID, http.MethodPost, "/api/integrations/tg-1/test", nil))
	if !out.OK || out.Account != "@akm_bot" || p.got == nil || p.got.UUID != "tg-1" {
		t.Errorf("ok case = %+v", out)
	}

	h = newH(&testerProvider{err: errors.New("Unauthorized")})
	out = decode(t, doJSONRequest(t, h.handleIntegrationByUUID, http.MethodPost, "/api/integrations/tg-1/test", nil))
	if out.OK || out.Error != "Unauthorized" {
		t.Errorf("error case = %+v", out)
	}

	h = newH(nil)
	out = decode(t, doJSONRequest(t, h.handleIntegrationByUUID, http.MethodPost, "/api/integrations/tg-1/test", nil))
	if out.OK || out.Error == "" {
		t.Errorf("unregistered provider = %+v", out)
	}

	if w := doJSONRequest(t, h.handleIntegrationByUUID, http.MethodGet, "/api/integrations/tg-1/test", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", w.Code)
	}
	if w := doJSONRequest(t, h.handleIntegrationByUUID, http.MethodPost, "/api/integrations/missing/test", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", w.Code)
	}
}
