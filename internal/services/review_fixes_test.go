package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// --- OAuth: stale writes, empty metadata, env proxy ---

func TestMCPOAuth_CallbackAfterSignOutIsStale(t *testing.T) {
	f := newOAuthFixture(t)
	q := f.start(t)
	if err := f.svc.Disconnect(f.server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); !errors.Is(err, ErrMCPAuthStale) {
		t.Fatalf("expected ErrMCPAuthStale, got %v", err)
	}
	stored, _ := f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != nil {
		t.Fatalf("tokens stored after sign-out: %v", stored.AuthConfig)
	}
}

func TestMCPOAuth_CallbackAfterURLChangeIsStale(t *testing.T) {
	f := newOAuthFixture(t)
	q := f.start(t)
	newURL := "https://other.example.com/mcp"
	if _, err := f.mcp.UpdateMCPServer(f.server.ID, MCPServerPatch{URL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); !errors.Is(err, ErrMCPAuthStale) {
		t.Fatalf("expected ErrMCPAuthStale, got %v", err)
	}
	stored, _ := f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != nil {
		t.Fatalf("old sign-in installed tokens on the changed server: %v", stored.AuthConfig)
	}
}

// signOutDuringRefresh signs the operator out while the refresh request is
// in flight, before the token endpoint answers.
type signOutDuringRefresh struct {
	base http.RoundTripper
	f    *oauthFixture
	t    *testing.T
}

func (s signOutDuringRefresh) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/token" {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") == "refresh_token" {
			if err := s.f.svc.Disconnect(s.f.server.ID); err != nil {
				s.t.Errorf("disconnect: %v", err)
			}
		}
		// ParseForm consumed the body; re-encode it for the real request.
		body := r.PostForm.Encode()
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(strings.NewReader(body))
		r2.ContentLength = int64(len(body))
		return s.base.RoundTrip(r2)
	}
	return s.base.RoundTrip(r)
}

func TestMCPOAuth_RefreshLosesToSignOut(t *testing.T) {
	f := newOAuthFixture(t)
	q := f.start(t)
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); err != nil {
		t.Fatal(err)
	}
	client := f.auth.srv.Client()
	client.Transport = signOutDuringRefresh{base: client.Transport, f: f, t: t}
	f.svc.http = client
	f.now = f.now.Add(58 * time.Minute)
	f.svc.RefreshDue(context.Background())

	stored, _ := f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != nil || stored.AuthConfig["refresh_token"] != nil {
		t.Fatalf("refresh undid the sign-out: %v", stored.AuthConfig)
	}
}

func TestMCPOAuth_MetadataWithoutServersFallsBack(t *testing.T) {
	f := newOAuthFixture(t)
	f.auth.emptyPRM = true
	q := f.start(t) // must not panic; falls back to the server origin
	if q.Get("client_id") == "" {
		t.Fatalf("no client_id in %v", q)
	}
}

func TestOutboundHTTPClient_IgnoresEnvProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	c := newOutboundHTTPClient(time.Second)
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatal("outbound OAuth client must not use the env proxy")
	}
}

// --- connectors: ownership, cardinality, dangling links ---

func TestConnectorDelete_SkipsPartMovedAway(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	toolConn := f.uuidOf(t, f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID))
	if err := f.svc.Attach(toolConn, PartAlertSource, f.zbxHook.ID); err != nil {
		t.Fatal(err)
	}
	// Another connector the webhook moves to while the delete runs.
	other := database.Connector{UUID: "other", CatalogID: "zabbix", Name: "other"}
	mustCreate(t, f.db, &other)
	orig := f.svc.deps.DeleteToolInstance
	f.svc.deps.DeleteToolInstance = func(id uint) error {
		if err := f.db.Model(&database.AlertSourceInstance{}).Where("id = ?", f.zbxHook.ID).Update("connector_id", other.ID).Error; err != nil {
			t.Fatal(err)
		}
		return orig(id)
	}
	if err := f.svc.Delete(context.Background(), toolConn); err != nil {
		t.Fatal(err)
	}
	var n int64
	f.db.Model(&database.AlertSourceInstance{}).Where("id = ?", f.zbxHook.ID).Count(&n)
	if n != 1 {
		t.Fatal("deleted a webhook that had moved to another connector")
	}
}

func TestConnectorLinkNew_SecondMCPServerRefused(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	target := f.uuidOf(t, f.connectorOf(t, &database.MCPServerConfig{}, f.mcp.ID))
	second, err := f.mcpSvc.CreateMCPServer(&database.MCPServerConfig{Name: "Second", URL: "https://two.example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LinkNew(PartMCPServer, second.ID, target); !errors.Is(err, ErrConnectorHasMCPServer) {
		t.Fatalf("expected ErrConnectorHasMCPServer, got %v", err)
	}
	if f.connectorOf(t, &database.MCPServerConfig{}, second.ID) != nil {
		t.Fatal("second MCP server was linked")
	}
}

func TestConnectorReconcile_RepairsDanglingLinks(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.db.Model(&database.ToolInstance{}).Where("id = ?", f.zabbixTool.ID).Update("connector_id", 9999).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	id := f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID)
	if id == nil || *id == 9999 {
		t.Fatalf("dangling link not repaired: %v", id)
	}
	f.uuidOf(t, id) // must exist
}

func TestUpdateMCPServer_BumpsAuthVersionOnURLOrAuth(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	srv, err := svc.CreateMCPServer(&database.MCPServerConfig{Name: "A", URL: "https://a.example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	name := "B"
	u, _ := svc.UpdateMCPServer(srv.ID, MCPServerPatch{Name: &name})
	if u.AuthVersion != srv.AuthVersion {
		t.Fatal("rename must not bump AuthVersion")
	}
	url := "https://b.example.com/mcp"
	u, _ = svc.UpdateMCPServer(srv.ID, MCPServerPatch{URL: &url})
	if u.AuthVersion != srv.AuthVersion+1 {
		t.Fatalf("URL change: version %d", u.AuthVersion)
	}
	u, _ = svc.UpdateMCPServer(srv.ID, MCPServerPatch{Auth: &MCPAuthPatch{Clear: true}})
	if u.AuthVersion != srv.AuthVersion+2 {
		t.Fatalf("auth change: version %d", u.AuthVersion)
	}
	if _, err := svc.StoreAuthIfVersion(srv.ID, srv.AuthVersion, database.JSONB{"type": "oauth"}); !errors.Is(err, ErrMCPAuthStale) {
		t.Fatalf("stale write accepted: %v", err)
	}
}
