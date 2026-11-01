package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
)

// fakeConnectorManager implements services.ConnectorManager.
type fakeConnectorManager struct {
	catalog    []services.ConnectorCatalogEntry
	list       []services.ConnectorSummary
	detail     *services.ConnectorDetail
	err        error // returned by every call that returns an error
	getErr     error // Get only
	linked     []string
	attached   []string
	detached   []string
	renamed    string
	deleted    string
	pruned     int
	lookupUUID string
}

func (f *fakeConnectorManager) Catalog() ([]services.ConnectorCatalogEntry, error) {
	return f.catalog, f.err
}
func (f *fakeConnectorManager) List(context.Context) ([]services.ConnectorSummary, error) {
	return f.list, f.err
}
func (f *fakeConnectorManager) Get(_ context.Context, u string) (*services.ConnectorDetail, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.detail != nil {
		return f.detail, nil
	}
	return &services.ConnectorDetail{ConnectorSummary: services.ConnectorSummary{UUID: u}}, nil
}
func (f *fakeConnectorManager) Rename(u, name string) (*database.Connector, error) {
	f.renamed = u + ":" + name
	return &database.Connector{UUID: u, Name: name}, f.err
}
func (f *fakeConnectorManager) Delete(_ context.Context, u string) error {
	f.deleted = u
	return f.err
}
func (f *fakeConnectorManager) LinkNew(kind string, id uint, u string) (*database.Connector, error) {
	f.linked = append(f.linked, fmt.Sprintf("%s:%d:%s", kind, id, u))
	return &database.Connector{}, f.err
}
func (f *fakeConnectorManager) Attach(u, kind string, id uint) error {
	f.attached = append(f.attached, fmt.Sprintf("%s:%s:%d", u, kind, id))
	return f.err
}
func (f *fakeConnectorManager) Detach(u, kind string, id uint) (*database.Connector, error) {
	f.detached = append(f.detached, fmt.Sprintf("%s:%s:%d", u, kind, id))
	if f.err != nil {
		return nil, f.err
	}
	return &database.Connector{UUID: "new-uuid"}, nil
}
func (f *fakeConnectorManager) PruneEmpty() { f.pruned++ }
func (f *fakeConnectorManager) LookupByPart(kind string, id uint) (string, error) {
	return f.lookupUUID, f.err
}

func doConnectors(h *APIHandler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	if path == "/api/connectors" {
		h.handleConnectors(w, req)
	} else {
		h.handleConnectorByUUID(w, req)
	}
	return w
}

func TestConnectorsAPI_NotConfigured(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, p := range []string{"/api/connectors", "/api/connectors/catalog", "/api/connectors/abc"} {
		if w := doConnectors(h, http.MethodGet, p, ""); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want 503", p, w.Code)
		}
	}
}

func TestConnectorsAPI_Routes(t *testing.T) {
	fake := &fakeConnectorManager{
		catalog:    []services.ConnectorCatalogEntry{{ID: "zabbix", Name: "Zabbix"}},
		list:       []services.ConnectorSummary{{UUID: "c1", Name: "Zabbix prod"}},
		lookupUUID: "c9",
	}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetConnectorService(fake)

	w := doConnectors(h, http.MethodGet, "/api/connectors", "")
	var list []services.ConnectorSummary
	if w.Code != http.StatusOK || json.NewDecoder(w.Body).Decode(&list) != nil || len(list) != 1 || list[0].UUID != "c1" {
		t.Fatalf("list: %d %+v", w.Code, list)
	}
	if w := doConnectors(h, http.MethodPost, "/api/connectors", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST list: %d", w.Code)
	}

	w = doConnectors(h, http.MethodGet, "/api/connectors/catalog", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"zabbix"`) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
	if w := doConnectors(h, http.MethodPost, "/api/connectors/catalog", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST catalog: %d", w.Code)
	}

	w = doConnectors(h, http.MethodGet, "/api/connectors/lookup?kind=tool_instance&id=5", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"c9"`) {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	if w := doConnectors(h, http.MethodGet, "/api/connectors/lookup?kind=tool_instance&id=x", ""); w.Code != http.StatusBadRequest {
		t.Errorf("lookup bad id: %d", w.Code)
	}

	w = doConnectors(h, http.MethodGet, "/api/connectors/c1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"c1"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if w := doConnectors(h, http.MethodPatch, "/api/connectors/c1", `{"name":"Renamed"}`); w.Code != http.StatusOK || fake.renamed != "c1:Renamed" {
		t.Fatalf("patch: %d %q", w.Code, fake.renamed)
	}
	if w := doConnectors(h, http.MethodPatch, "/api/connectors/c1", `{`); w.Code != http.StatusBadRequest {
		t.Errorf("patch bad json: %d", w.Code)
	}
	if w := doConnectors(h, http.MethodDelete, "/api/connectors/c1", ""); w.Code != http.StatusNoContent || fake.deleted != "c1" {
		t.Fatalf("delete: %d %q", w.Code, fake.deleted)
	}
	if w := doConnectors(h, http.MethodPost, "/api/connectors/c1", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST connector: %d", w.Code)
	}

	if w := doConnectors(h, http.MethodPost, "/api/connectors/c1/parts", `{"kind":"alert_source","id":4}`); w.Code != http.StatusOK || fake.attached[0] != "c1:alert_source:4" {
		t.Fatalf("attach: %d %v", w.Code, fake.attached)
	}
	if w := doConnectors(h, http.MethodPost, "/api/connectors/c1/parts", `nope`); w.Code != http.StatusBadRequest {
		t.Errorf("attach bad json: %d", w.Code)
	}
	w = doConnectors(h, http.MethodDelete, "/api/connectors/c1/parts/tool_instance/9", "")
	if w.Code != http.StatusOK || fake.detached[0] != "c1:tool_instance:9" || !strings.Contains(w.Body.String(), "new-uuid") {
		t.Fatalf("detach: %d %v %s", w.Code, fake.detached, w.Body.String())
	}
	if w := doConnectors(h, http.MethodDelete, "/api/connectors/c1/parts/tool_instance/x", ""); w.Code != http.StatusBadRequest {
		t.Errorf("detach bad id: %d", w.Code)
	}
	if w := doConnectors(h, http.MethodGet, "/api/connectors/c1/parts", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET parts: %d", w.Code)
	}
	if w := doConnectors(h, http.MethodGet, "/api/connectors/c1/other", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown subpath: %d", w.Code)
	}
	if w := doConnectors(h, http.MethodGet, "/api/connectors/", ""); w.Code != http.StatusNotFound {
		t.Errorf("empty uuid: %d", w.Code)
	}
}

func TestConnectorsAPI_ErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{services.ErrConnectorNotFound, http.StatusNotFound},
		{services.ErrConnectorPartNotFound, http.StatusNotFound},
		{services.ErrConnectorPartIncompatible, http.StatusBadRequest},
		{errors.New("validation failed: name is required"), http.StatusBadRequest},
		{errors.New("db exploded"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		fake := &fakeConnectorManager{err: c.err}
		h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		h.SetConnectorService(fake)
		for _, req := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/connectors/c1", ""},
			{http.MethodPost, "/api/connectors/c1/parts", `{"kind":"tool_instance","id":1}`},
			{http.MethodDelete, "/api/connectors/c1/parts/tool_instance/1", ""},
			{http.MethodGet, "/api/connectors/lookup?kind=tool_instance&id=1", ""},
		} {
			w := doConnectors(h, req.method, req.path, req.body)
			if w.Code != c.want {
				t.Errorf("%v %s %s: %d, want %d", c.err, req.method, req.path, w.Code, c.want)
			}
			if c.want == http.StatusInternalServerError && strings.Contains(w.Body.String(), "exploded") {
				t.Errorf("internal error leaked: %s", w.Body.String())
			}
		}
	}
}

func TestConnectorHelpers(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// No connector service: helpers are no-ops and the target check passes.
	h.linkNewPart(services.PartToolInstance, 1, "")
	h.afterPartDeleted()
	w := httptest.NewRecorder()
	if !h.checkConnectorTarget(w, httptest.NewRequest(http.MethodGet, "/", nil), "x") {
		t.Fatal("target check failed without a service")
	}

	fake := &fakeConnectorManager{getErr: services.ErrConnectorNotFound}
	h.SetConnectorService(fake)
	w = httptest.NewRecorder()
	if h.checkConnectorTarget(w, httptest.NewRequest(http.MethodGet, "/", nil), "x") || w.Code != http.StatusNotFound {
		t.Fatalf("bad target: ok, code %d", w.Code)
	}
	if !h.checkConnectorTarget(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), "") {
		t.Fatal("empty target should pass")
	}
	fake.err = errors.New("link failed")
	h.linkNewPart(services.PartAlertSource, 3, "c1") // logs, does not panic
	if len(fake.linked) != 1 {
		t.Fatalf("linked = %v", fake.linked)
	}
	if got := connectorUUIDFromQuery(httptest.NewRequest(http.MethodGet, "/x?connector_uuid=%20abc%20", nil)); got != "abc" {
		t.Fatalf("connectorUUIDFromQuery = %q", got)
	}
}

func TestConnectorsAPI_DeleteRunsFollowUps(t *testing.T) {
	fake := &fakeConnectorManager{detail: &services.ConnectorDetail{
		ConnectorSummary: services.ConnectorSummary{UUID: "c1"},
		Parts: services.ConnectorParts{
			AlertSources: []services.ConnectorAlertPart{{ID: 1}},
			MCPServer:    &services.ConnectorMCPPart{ID: 2},
		},
	}}
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.SetConnectorService(fake)
	reloaded := make(chan struct{}, 1)
	h.SetAlertChannelReloader(func() { reloaded <- struct{}{} })
	if w := doConnectors(h, http.MethodDelete, "/api/connectors/c1", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("alert channels not reloaded")
	}
	fake.getErr = services.ErrConnectorNotFound
	if w := doConnectors(h, http.MethodDelete, "/api/connectors/c1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing: %d", w.Code)
	}
}

// ---------- OAuth callback ----------

func oauthLocation(t *testing.T, w *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	body := w.Body.String()
	i := strings.Index(body, `url=`)
	if i < 0 {
		t.Fatalf("no redirect in body: %s", body)
	}
	rest := body[i+4:]
	j := strings.Index(rest, `"`)
	raw := strings.ReplaceAll(rest[:j], "&amp;", "&")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMCPOAuthCallback_ProviderErrorAndMissingCode(t *testing.T) {
	mock := &mockMCPServerService{}
	h := newMCPHandler(mock)
	h.SetMCPOAuthService(services.NewMCPOAuthService(nil, nil, nil))

	w := doMCP(t, h, http.MethodGet, "/api/mcp-servers/oauth/callback?state=unknown&error=access_denied&error_description=user+said+no", "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d headers %v", w.Code, w.Header())
	}
	u := oauthLocation(t, w)
	if u.Path != "/connectors" || u.Query().Get("oauth_error") != "access_denied: user said no" {
		t.Fatalf("redirect = %s", u)
	}

	w = doMCP(t, h, http.MethodGet, "/api/mcp-servers/oauth/callback?state=abc", "")
	if u := oauthLocation(t, w); u.Query().Get("oauth_error") != "missing code or state" {
		t.Fatalf("missing code redirect = %s", u)
	}

	w = doMCP(t, h, http.MethodGet, "/api/mcp-servers/oauth/callback?state=abc&code=xyz", "")
	if u := oauthLocation(t, w); !strings.Contains(u.Query().Get("oauth_error"), "expired") {
		t.Fatalf("unknown state redirect = %s", u)
	}

	if w := doMCP(t, h, http.MethodPost, "/api/mcp-servers/oauth/callback", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST callback: %d", w.Code)
	}
}

func TestMCPOAuth_RedirectToConnector(t *testing.T) {
	db := testhelpers.NewGlobalSQLiteDB(t, &database.Connector{})
	c := database.Connector{UUID: uuid.NewString(), CatalogID: services.CatalogIDCustomMCP, Name: "Linear"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	cfg := newTestMCPServerConfig()
	cfg.ConnectorID = &c.ID
	h := newMCPHandler(&mockMCPServerService{configs: []database.MCPServerConfig{cfg}})

	w := httptest.NewRecorder()
	h.redirectAfterOAuth(w, httptest.NewRequest(http.MethodGet, "/", nil), cfg.ID, "")
	u := oauthLocation(t, w)
	if u.Path != "/connectors/"+c.UUID || u.Query().Get("oauth") != "connected" {
		t.Fatalf("redirect = %s", u)
	}
	// Error text is HTML-escaped in the page.
	w = httptest.NewRecorder()
	h.redirectAfterOAuth(w, httptest.NewRequest(http.MethodGet, "/", nil), cfg.ID, `<script>"x"</script>`)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatalf("unescaped error in page: %s", w.Body.String())
	}
}

// ---------- tools handler: MCP-managed instances ----------

func TestToolsAPI_ManagedToolIsReadOnly(t *testing.T) {
	db := testhelpers.NewGlobalSQLiteDB(t, &database.ToolType{}, &database.ToolInstance{}, &database.SkillTool{}, &database.CronJobTool{})
	tt := database.ToolType{Name: "ext.github", Kind: database.ToolTypeKindMCP}
	if err := db.Create(&tt).Error; err != nil {
		t.Fatal(err)
	}
	inst := database.ToolInstance{ToolTypeID: tt.ID, Name: "GitHub", LogicalName: "github", Enabled: true}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatal(err)
	}
	h := NewAPIHandler(nil, services.NewToolService(), nil, nil, nil, nil, nil, nil, nil, nil, nil)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, fmt.Sprintf("/api/tools/%d", inst.ID), strings.NewReader(`{"name":"x","enabled":true}`))
		w := httptest.NewRecorder()
		h.handleToolByID(w, req)
		if w.Code != http.StatusConflict {
			t.Errorf("%s managed tool: %d, want 409", method, w.Code)
		}
	}
	var n int64
	db.Model(&database.ToolInstance{}).Count(&n)
	if n != 1 {
		t.Fatal("managed tool deleted")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tools", strings.NewReader(fmt.Sprintf(`{"tool_type_id":%d,"name":"Other"}`, tt.ID)))
	w := httptest.NewRecorder()
	h.handleTools(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with managed type: %d, want 400", w.Code)
	}
}
