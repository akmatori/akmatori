package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// connectorFixture is a small install: one Zabbix tool, the built-in
// incidents tool, an Alertmanager and a Zabbix webhook, a legacy
// slack_channel source, a Slack integration with no channels, and one MCP
// server with its managed tool.
type connectorFixture struct {
	db          *gorm.DB
	svc         *ConnectorService
	mcpSvc      *MCPServerService
	zabbixType  database.ToolType
	zabbixTool  database.ToolInstance
	incidents   database.ToolInstance
	amType      database.AlertSourceType
	zbxAlert    database.AlertSourceType
	am          database.AlertSourceInstance
	zbxHook     database.AlertSourceInstance
	legacy      database.AlertSourceInstance
	slack       database.Integration
	mcp         *database.MCPServerConfig
	deleted     map[string][]string
	afterCalled int
}

func setupConnectorDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testhelpers.NewGlobalSQLiteDB(t,
		&database.Connector{}, &database.ToolType{}, &database.ToolInstance{},
		&database.AlertSourceType{}, &database.AlertSourceInstance{},
		&database.Integration{}, &database.Channel{},
		&database.MCPServerConfig{}, &database.HTTPConnector{},
		&database.Alert{}, &database.SkillTool{}, &database.CronJobTool{},
	)
}

func mustCreate(t *testing.T, db *gorm.DB, v interface{}) {
	t.Helper()
	if err := db.Create(v).Error; err != nil {
		t.Fatalf("create %T: %v", v, err)
	}
}

func newConnectorFixture(t *testing.T) *connectorFixture {
	t.Helper()
	db := setupConnectorDB(t)
	f := &connectorFixture{db: db, deleted: map[string][]string{}}

	f.zabbixType = database.ToolType{Name: "zabbix"}
	mustCreate(t, db, &f.zabbixType)
	incType := database.ToolType{Name: "incidents"}
	mustCreate(t, db, &incType)
	f.zabbixTool = database.ToolInstance{ToolTypeID: f.zabbixType.ID, Name: "Zabbix prod", LogicalName: "zabbix-prod", Enabled: true}
	mustCreate(t, db, &f.zabbixTool)
	f.incidents = database.ToolInstance{ToolTypeID: incType.ID, Name: "Incidents", LogicalName: "incidents", Enabled: true}
	mustCreate(t, db, &f.incidents)

	f.amType = database.AlertSourceType{Name: "alertmanager", DisplayName: "Prometheus Alertmanager"}
	mustCreate(t, db, &f.amType)
	f.zbxAlert = database.AlertSourceType{Name: "zabbix", DisplayName: "Zabbix"}
	mustCreate(t, db, &f.zbxAlert)
	legacyType := database.AlertSourceType{Name: "slack_channel", DisplayName: "Slack", Deprecated: true}
	mustCreate(t, db, &legacyType)
	f.am = database.AlertSourceInstance{UUID: uuid.NewString(), AlertSourceTypeID: f.amType.ID, Name: "Prod Alertmanager", Enabled: true}
	mustCreate(t, db, &f.am)
	f.zbxHook = database.AlertSourceInstance{UUID: uuid.NewString(), AlertSourceTypeID: f.zbxAlert.ID, Name: "Zabbix webhook", Enabled: true}
	mustCreate(t, db, &f.zbxHook)
	f.legacy = database.AlertSourceInstance{UUID: uuid.NewString(), AlertSourceTypeID: legacyType.ID, Name: "legacy", Enabled: true}
	mustCreate(t, db, &f.legacy)

	f.slack = database.Integration{UUID: uuid.NewString(), Provider: database.MessagingProviderSlack, Name: "Slack", Enabled: true}
	mustCreate(t, db, &f.slack)

	f.mcpSvc = NewMCPServerServiceWithDB(db)
	mcp, err := f.mcpSvc.CreateMCPServer(&database.MCPServerConfig{Name: "GitHub", URL: "https://gh.example.com/mcp"})
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}
	f.mcp = mcp

	f.svc = NewConnectorService(db, ConnectorDeps{
		DeleteToolInstance: func(id uint) error {
			f.deleted["tool"] = append(f.deleted["tool"], "x")
			return db.Delete(&database.ToolInstance{}, id).Error
		},
		DeleteAlertSource: func(id uint) error {
			f.deleted["alert"] = append(f.deleted["alert"], "x")
			return db.Delete(&database.AlertSourceInstance{}, id).Error
		},
		DeleteIntegration: func(u string) error {
			f.deleted["integration"] = append(f.deleted["integration"], u)
			return db.Where("uuid = ?", u).Delete(&database.Integration{}).Error
		},
		DeleteMCPServer: func(id uint) error {
			f.deleted["mcp"] = append(f.deleted["mcp"], "x")
			return f.mcpSvc.DeleteMCPServer(id)
		},
		AfterMutation: func() { f.afterCalled++ },
	})
	return f
}

func (f *connectorFixture) connectorOf(t *testing.T, model interface{}, id uint) *uint {
	t.Helper()
	var row struct{ ConnectorID *uint }
	if err := f.db.Model(model).Select("connector_id").Where("id = ?", id).Scan(&row).Error; err != nil {
		t.Fatalf("read connector_id: %v", err)
	}
	return row.ConnectorID
}

func (f *connectorFixture) count(t *testing.T) int64 {
	t.Helper()
	var n int64
	f.db.Model(&database.Connector{}).Count(&n)
	return n
}

func (f *connectorFixture) uuidOf(t *testing.T, id *uint) string {
	t.Helper()
	if id == nil {
		t.Fatal("part has no connector")
	}
	var c database.Connector
	if err := f.db.First(&c, *id).Error; err != nil {
		t.Fatal(err)
	}
	return c.UUID
}

func managedInstanceID(t *testing.T, f *connectorFixture) uint {
	t.Helper()
	id, err := f.mcpSvc.ManagedToolInstanceID(f.mcp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestConnectorReconcile_BackfillsAndIsIdempotent(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// zabbix tool, alertmanager, zabbix webhook, slack, MCP = 5
	if n := f.count(t); n != 5 {
		t.Fatalf("connectors = %d, want 5", n)
	}
	if f.connectorOf(t, &database.ToolInstance{}, f.incidents.ID) != nil {
		t.Error("incidents built-in got a connector")
	}
	if f.connectorOf(t, &database.AlertSourceInstance{}, f.legacy.ID) != nil {
		t.Error("deprecated slack_channel source got a connector")
	}
	mcpConn := f.connectorOf(t, &database.MCPServerConfig{}, f.mcp.ID)
	managed := f.connectorOf(t, &database.ToolInstance{}, managedInstanceID(t, f))
	if mcpConn == nil || managed == nil || *mcpConn != *managed {
		t.Fatalf("managed tool not on the MCP connector: %v vs %v", mcpConn, managed)
	}
	var c database.Connector
	f.db.First(&c, *mcpConn)
	if c.CatalogID != CatalogIDCustomMCP || c.Name != "GitHub" {
		t.Fatalf("MCP connector = %+v", c)
	}
	var amConn database.Connector
	f.db.First(&amConn, *f.connectorOf(t, &database.AlertSourceInstance{}, f.am.ID))
	if amConn.CatalogID != "alertmanager" {
		t.Fatalf("alertmanager catalog id = %q", amConn.CatalogID)
	}

	// Idempotent.
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t); n != 5 {
		t.Fatalf("second reconcile changed count to %d", n)
	}

	// Empty connectors are pruned.
	mustCreate(t, f.db, &database.Connector{UUID: uuid.NewString(), CatalogID: "zabbix", Name: "empty"})
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t); n != 5 {
		t.Fatalf("empty connector not pruned: %d", n)
	}
}

func TestConnectorLinkNew(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	zbxConnUUID := f.uuidOf(t, f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID))

	// Into an existing connector of a compatible type.
	staging := database.ToolInstance{ToolTypeID: f.zabbixType.ID, Name: "Zabbix staging", LogicalName: "zabbix-staging", Enabled: true}
	mustCreate(t, f.db, &staging)
	c, err := f.svc.LinkNew(PartToolInstance, staging.ID, zbxConnUUID)
	if err != nil || c.UUID != zbxConnUUID {
		t.Fatalf("LinkNew into target: %v %v", c, err)
	}

	// Incompatible: an Alertmanager source cannot join a Zabbix connector.
	am2 := database.AlertSourceInstance{UUID: uuid.NewString(), AlertSourceTypeID: f.amType.ID, Name: "AM 2", Enabled: true}
	mustCreate(t, f.db, &am2)
	if _, err := f.svc.LinkNew(PartAlertSource, am2.ID, zbxConnUUID); !errors.Is(err, ErrConnectorPartIncompatible) {
		t.Fatalf("incompatible LinkNew err = %v", err)
	}
	if f.connectorOf(t, &database.AlertSourceInstance{}, am2.ID) != nil {
		t.Fatal("incompatible part was linked")
	}

	// No target: a new connector named after the part.
	nc, err := f.svc.LinkNew(PartAlertSource, am2.ID, "")
	if err != nil || nc.Name != "AM 2" || nc.CatalogID != "alertmanager" {
		t.Fatalf("LinkNew new = %+v %v", nc, err)
	}

	// Unknown target and unknown part.
	if _, err := f.svc.LinkNew(PartToolInstance, staging.ID, "nope"); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("unknown target err = %v", err)
	}
	if _, err := f.svc.LinkNew(PartToolInstance, 9999, ""); !errors.Is(err, ErrConnectorPartNotFound) {
		t.Fatalf("unknown part err = %v", err)
	}
	if _, err := f.svc.LinkNew("widget", 1, ""); err == nil {
		t.Fatal("unknown kind accepted")
	}
	// Hidden built-in: no connector, no error.
	if c, err := f.svc.LinkNew(PartToolInstance, f.incidents.ID, ""); err != nil || c != nil {
		t.Fatalf("hidden built-in LinkNew = %v %v", c, err)
	}
}

func TestConnectorAttachDetach(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	zbxConn := f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID)
	hookConn := f.connectorOf(t, &database.AlertSourceInstance{}, f.zbxHook.ID)
	zbxUUID := f.uuidOf(t, zbxConn)

	// Move the Zabbix webhook into the Zabbix tool's connector.
	if err := f.svc.Attach(zbxUUID, PartAlertSource, f.zbxHook.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if got := f.connectorOf(t, &database.AlertSourceInstance{}, f.zbxHook.ID); got == nil || *got != *zbxConn {
		t.Fatalf("webhook connector = %v, want %d", got, *zbxConn)
	}
	var n int64
	f.db.Model(&database.Connector{}).Where("id = ?", *hookConn).Count(&n)
	if n != 0 {
		t.Fatal("old empty connector not pruned")
	}

	// Incompatible attach.
	if err := f.svc.Attach(zbxUUID, PartAlertSource, f.am.ID); !errors.Is(err, ErrConnectorPartIncompatible) {
		t.Fatalf("incompatible Attach err = %v", err)
	}
	// MCP server cannot join a built-in connector.
	if err := f.svc.Attach(zbxUUID, PartMCPServer, f.mcp.ID); !errors.Is(err, ErrConnectorPartIncompatible) {
		t.Fatalf("MCP into zabbix err = %v", err)
	}

	// Detach the webhook again: new connector, old one keeps the tool.
	d, err := f.svc.Get(context.Background(), zbxUUID)
	if err != nil || d.Counts.AlertSources != 1 || d.Counts.ToolInstances != 1 {
		t.Fatalf("detail before detach: %+v %v", d, err)
	}
	nc, err := f.svc.Detach(zbxUUID, PartAlertSource, f.zbxHook.ID)
	if err != nil || nc.UUID == zbxUUID {
		t.Fatalf("Detach: %v %v", nc, err)
	}
	if got := f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID); got == nil || *got != *zbxConn {
		t.Fatal("tool left its connector")
	}
	// Detaching a part that is not in the connector.
	if _, err := f.svc.Detach(zbxUUID, PartAlertSource, f.am.ID); !errors.Is(err, ErrConnectorPartNotFound) {
		t.Fatalf("foreign detach err = %v", err)
	}
	// MCP parts cannot be detached.
	mcpUUID := f.uuidOf(t, f.connectorOf(t, &database.MCPServerConfig{}, f.mcp.ID))
	if _, err := f.svc.Detach(mcpUUID, PartMCPServer, f.mcp.ID); err == nil {
		t.Fatal("MCP server detach accepted")
	}
	if _, err := f.svc.Detach(mcpUUID, PartToolInstance, managedInstanceID(t, f)); err == nil {
		t.Fatal("managed tool detach accepted")
	}
}

func TestConnectorAttach_MCPServerBringsManagedTool(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	// A second, empty custom MCP connector.
	target := database.Connector{UUID: uuid.NewString(), CatalogID: CatalogIDCustomMCP, Name: "target"}
	mustCreate(t, f.db, &target)
	if err := f.svc.Attach(target.UUID, PartMCPServer, f.mcp.ID); err != nil {
		t.Fatalf("Attach MCP: %v", err)
	}
	if got := f.connectorOf(t, &database.ToolInstance{}, managedInstanceID(t, f)); got == nil || *got != target.ID {
		t.Fatalf("managed tool connector = %v, want %d", got, target.ID)
	}
	// At most one MCP server per connector.
	other, err := f.mcpSvc.CreateMCPServer(&database.MCPServerConfig{Name: "Linear", URL: "https://linear.example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Attach(target.UUID, PartMCPServer, other.ID); err == nil {
		t.Fatal("second MCP server accepted")
	}
}

func TestConnectorRenameDeleteLookup(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	slackUUID, err := f.svc.LookupByPart(PartIntegration, f.slack.ID)
	if err != nil || slackUUID == "" {
		t.Fatalf("LookupByPart: %q %v", slackUUID, err)
	}
	if _, err := f.svc.LookupByPart(PartToolInstance, f.incidents.ID); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("unlinked lookup err = %v", err)
	}
	if _, err := f.svc.LookupByPart(PartToolInstance, 9999); !errors.Is(err, ErrConnectorPartNotFound) {
		t.Fatalf("missing part lookup err = %v", err)
	}

	if _, err := f.svc.Rename(slackUUID, "  "); err == nil {
		t.Fatal("blank rename accepted")
	}
	if _, err := f.svc.Rename("nope", "x"); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("rename unknown err = %v", err)
	}
	c, err := f.svc.Rename(slackUUID, " Ops Slack ")
	if err != nil || c.Name != "Ops Slack" {
		t.Fatalf("Rename: %+v %v", c, err)
	}

	if err := f.svc.Delete(context.Background(), slackUUID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(f.deleted["integration"]) != 1 || f.deleted["integration"][0] != f.slack.UUID || f.afterCalled != 1 {
		t.Fatalf("delete deps: %v after=%d", f.deleted, f.afterCalled)
	}
	if _, err := f.svc.Get(context.Background(), slackUUID); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("connector still there: %v", err)
	}

	// Deleting the MCP connector removes the server (and with it the
	// managed tool) through the MCP dep only.
	mcpUUID := f.uuidOf(t, f.connectorOf(t, &database.MCPServerConfig{}, f.mcp.ID))
	if err := f.svc.Delete(context.Background(), mcpUUID); err != nil {
		t.Fatalf("Delete MCP: %v", err)
	}
	if len(f.deleted["mcp"]) != 1 || len(f.deleted["tool"]) != 0 {
		t.Fatalf("MCP delete deps: %v", f.deleted)
	}
	var n int64
	f.db.Model(&database.ToolInstance{}).Count(&n)
	if n != 2 { // zabbix + incidents
		t.Fatalf("tool instances left = %d, want 2", n)
	}
}

func TestConnectorDelete_StopsOnFailure(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	f.svc.deps.DeleteToolInstance = func(uint) error { return errors.New("boom") }
	u := f.uuidOf(t, f.connectorOf(t, &database.ToolInstance{}, f.zabbixTool.ID))
	if err := f.svc.Delete(context.Background(), u); err == nil {
		t.Fatal("Delete succeeded although the part delete failed")
	}
	if _, err := f.svc.Get(context.Background(), u); err != nil {
		t.Fatalf("connector removed after a failed part delete: %v", err)
	}
}

func findSummary(t *testing.T, list []ConnectorSummary, catalogID string) ConnectorSummary {
	t.Helper()
	for _, s := range list {
		if s.CatalogID == catalogID {
			return s
		}
	}
	t.Fatalf("no connector with catalog id %q", catalogID)
	return ConnectorSummary{}
}

func TestConnectorList_Status(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	// Disable the Zabbix tool; give Alertmanager two alerts.
	f.db.Model(&database.ToolInstance{}).Where("id = ?", f.zabbixTool.ID).Update("enabled", false)
	older := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC)
	mustCreate(t, f.db, &database.Alert{UUID: uuid.NewString(), IncidentUUID: "i1", SourceUUID: f.am.UUID, FiredAt: older, Status: database.AlertStatus("firing")})
	mustCreate(t, f.db, &database.Alert{UUID: uuid.NewString(), IncidentUUID: "i2", SourceUUID: f.am.UUID, FiredAt: older, LastSeenAt: &newer, Status: database.AlertStatus("resolved")})

	var fetchErr error
	statuses := map[uint]MCPServerRuntimeStatus{f.mcp.ID: {ID: f.mcp.ID, Connected: true, ToolCount: 4, CheckedAt: newer}}
	f.svc.SetMCPStatusFetcher(func(context.Context) (map[uint]MCPServerRuntimeStatus, error) {
		return statuses, fetchErr
	})

	list, err := f.svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("list len = %d", len(list))
	}
	if s := findSummary(t, list, "zabbix"); s.Status.State != "disabled" || s.Enabled {
		t.Errorf("zabbix status = %+v", s.Status)
	}
	if s := findSummary(t, list, "slack"); s.Status.State != "needs_setup" || s.Capabilities[0] != CapabilityMessaging {
		t.Errorf("slack status = %+v caps %v", s.Status, s.Capabilities)
	}
	am := findSummary(t, list, "alertmanager")
	if am.Status.State != "ok" || am.LastAlertAt == nil || !am.LastAlertAt.Equal(newer) {
		t.Errorf("alertmanager = %+v last %v", am.Status, am.LastAlertAt)
	}
	mcp := findSummary(t, list, CatalogIDCustomMCP)
	if mcp.Status.State != "connected" || mcp.Counts.MCPTools != 4 || mcp.Kind != ConnectorKindCustomMCP || mcp.ID == 0 {
		t.Errorf("mcp = %+v", mcp)
	}

	statuses = map[uint]MCPServerRuntimeStatus{f.mcp.ID: {ID: f.mcp.ID, Error: "401 Unauthorized", CheckedAt: newer}}
	list, _ = f.svc.List(context.Background())
	if s := findSummary(t, list, CatalogIDCustomMCP); s.Status.State != "error" || s.Status.Detail != "401 Unauthorized" {
		t.Errorf("mcp error status = %+v", s.Status)
	}
	statuses, fetchErr = nil, errors.New("gateway down")
	list, _ = f.svc.List(context.Background())
	if s := findSummary(t, list, CatalogIDCustomMCP); s.Status.State != "unknown" {
		t.Errorf("mcp unknown status = %+v", s.Status)
	}

	// Add a channel: Slack becomes ok. Detail lists parts without secrets.
	mustCreate(t, f.db, &database.Channel{UUID: uuid.NewString(), IntegrationID: f.slack.ID, ExternalID: "C1", CanPost: true, Enabled: true})
	slackUUID, _ := f.svc.LookupByPart(PartIntegration, f.slack.ID)
	d, err := f.svc.Get(context.Background(), slackUUID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status.State != "ok" || d.Counts.Channels != 1 || len(d.Parts.Integrations) != 1 || d.Catalog == nil || !d.Catalog.ListenSupported {
		t.Errorf("slack detail = %+v", d)
	}
	mcpUUID, _ := f.svc.LookupByPart(PartMCPServer, f.mcp.ID)
	d, _ = f.svc.Get(context.Background(), mcpUUID)
	if d.Parts.MCPServer == nil || d.Parts.MCPServer.AuthType != "none" || len(d.Parts.ToolInstances) != 1 || !d.Parts.ToolInstances[0].Managed {
		t.Errorf("mcp detail = %+v", d.Parts)
	}
}

func TestConnectorStatus_NoParts(t *testing.T) {
	enabled, st := connectorStatus(&ConnectorDetail{}, nil)
	if enabled || st.State != "needs_setup" {
		t.Fatalf("empty connector = %v %+v", enabled, st)
	}
	if mcpAuthType(database.JSONB{"type": "bearer", "token": "x"}) != "bearer" || mcpAuthType(nil) != "none" || mcpAuthType(database.JSONB{"token": "x"}) != "none" {
		t.Fatal("mcpAuthType wrong")
	}
}

func TestConnectorCatalog(t *testing.T) {
	f := newConnectorFixture(t)
	mustCreate(t, f.db, &database.ToolType{Name: "custom_thing", Description: "A thing"})
	mustCreate(t, f.db, &database.ToolType{Name: "billing"})
	seedHTTPConnector(t, f.db, "billing")
	mustCreate(t, f.db, &database.AlertSourceType{Name: "custom_hook", DisplayName: "Custom hook"})

	cat, err := f.svc.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ConnectorCatalogEntry{}
	for _, e := range cat {
		byID[e.ID] = e
	}
	if e, ok := byID["http:billing"]; !ok || e.Kind != ConnectorKindCustomHTTP || e.Capabilities[0] != CapabilityTools {
		t.Errorf("http:billing = %+v", e)
	}
	if _, ok := byID["billing"]; ok {
		t.Error("HTTP connector tool type listed twice")
	}
	if e, ok := byID["custom_thing"]; !ok || e.Description != "A thing" {
		t.Errorf("unknown tool type entry = %+v", e)
	}
	if e, ok := byID["custom_hook"]; !ok || e.Capabilities[0] != CapabilityAlerts {
		t.Errorf("unknown alert type entry = %+v", e)
	}
	if _, ok := byID["ext.github"]; ok {
		t.Error("MCP-managed tool type listed in the catalog")
	}
	for _, hidden := range []string{"incidents", "slack_channel"} {
		if _, ok := byID[hidden]; ok {
			t.Errorf("%s listed in the catalog", hidden)
		}
	}
	z := byID["zabbix"]
	if len(z.Capabilities) != 2 || z.Capabilities[0] != CapabilityTools || z.Capabilities[1] != CapabilityAlerts {
		t.Errorf("zabbix capabilities = %v", z.Capabilities)
	}
	if mcp := byID[CatalogIDCustomMCP]; mcp.Kind != ConnectorKindCustomMCP || mcp.Capabilities[0] != CapabilityTools {
		t.Errorf("custom_mcp = %+v", mcp)
	}
	if tg := byID["telegram"]; tg.ListenSupported || tg.Note == "" {
		t.Errorf("telegram = %+v", tg)
	}
}

func TestConnectorPruneEmpty(t *testing.T) {
	f := newConnectorFixture(t)
	if err := f.svc.Reconcile(); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, f.db, &database.Connector{UUID: uuid.NewString(), CatalogID: "zabbix", Name: "empty"})
	f.svc.PruneEmpty()
	if n := f.count(t); n != 5 {
		t.Fatalf("PruneEmpty left %d connectors, want 5", n)
	}
}
