package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrConnectorNotFound is returned when no connector has the given UUID.
var ErrConnectorNotFound = errors.New("connector not found")

// ErrConnectorPartNotFound is returned when the part to move does not exist.
var ErrConnectorPartNotFound = errors.New("connector part not found")

// ErrConnectorPartIncompatible is returned when a part cannot join a
// connector because the connector's catalog entry does not accept its type.
var ErrConnectorPartIncompatible = errors.New("this part cannot join this connector")

// MCPServerRuntimeStatus is the gateway's view of one MCP server connection.
type MCPServerRuntimeStatus struct {
	ID        uint      `json:"id"`
	Connected bool      `json:"connected"`
	ToolCount int       `json:"tool_count"`
	Tools     []string  `json:"tools"`
	Error     string    `json:"error"`
	CheckedAt time.Time `json:"checked_at"`
}

// MCPStatusFetcher returns the gateway's MCP connection status by config ID.
type MCPStatusFetcher func(ctx context.Context) (map[uint]MCPServerRuntimeStatus, error)

// ConnectorDeps are the part deleters a connector delete fans out to. They
// are the same service methods the per-part endpoints use, so a connector
// delete cleans up exactly like deleting each part by hand.
type ConnectorDeps struct {
	DeleteToolInstance func(id uint) error
	DeleteAlertSource  func(id uint) error
	DeleteIntegration  func(uuid string) error
	DeleteMCPServer    func(id uint) error
	// AfterMutation runs after parts were deleted (e.g. gateway reloads).
	AfterMutation func()
}

// ConnectorService groups tool instances, alert sources, messaging
// integrations and MCP servers into connectors (see database.Connector).
type ConnectorService struct {
	db        *gorm.DB
	deps      ConnectorDeps
	mcpStatus MCPStatusFetcher
}

// NewConnectorService creates a connector service.
func NewConnectorService(db *gorm.DB, deps ConnectorDeps) *ConnectorService {
	return &ConnectorService{db: db, deps: deps}
}

// SetMCPStatusFetcher wires the gateway status lookup. Nil disables it.
func (s *ConnectorService) SetMCPStatusFetcher(f MCPStatusFetcher) { s.mcpStatus = f }

// ---------- DTOs ----------

// ConnectorStatus is a coarse health state for the list's Status column.
// States: "ok" (configured and nothing known to be wrong), "connected"
// (gateway reports a live MCP connection), "disabled", "needs_setup",
// "error", "unknown" (status could not be checked). Webhook silence is not a
// failure, so alert-only connectors are "ok" with a last-alert time.
type ConnectorStatus struct {
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// ConnectorCounts summarizes a connector's parts.
type ConnectorCounts struct {
	ToolInstances int `json:"tool_instances"`
	AlertSources  int `json:"alert_sources"`
	Integrations  int `json:"integrations"`
	Channels      int `json:"channels"`
	MCPTools      int `json:"mcp_tools"`
}

// ConnectorSummary is one row of the Connectors list. It carries no
// credentials.
type ConnectorSummary struct {
	ID           uint            `json:"id"`
	UUID         string          `json:"uuid"`
	Name         string          `json:"name"`
	CatalogID    string          `json:"catalog_id"`
	CatalogName  string          `json:"catalog_name"`
	Kind         string          `json:"kind"`
	Category     string          `json:"category"`
	Capabilities []string        `json:"capabilities"`
	Enabled      bool            `json:"enabled"`
	Status       ConnectorStatus `json:"status"`
	Counts       ConnectorCounts `json:"counts"`
	LastAlertAt  *time.Time      `json:"last_alert_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ConnectorToolPart is a tool instance in a connector detail.
type ConnectorToolPart struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	LogicalName string `json:"logical_name"`
	ToolType    string `json:"tool_type"`
	Enabled     bool   `json:"enabled"`
	// Managed is true for the tool instance the API keeps for an MCP server;
	// edit it through the MCP server, not /api/tools.
	Managed bool `json:"managed"`
}

// ConnectorAlertPart is an alert source in a connector detail.
type ConnectorAlertPart struct {
	ID              uint       `json:"id"`
	UUID            string     `json:"uuid"`
	Name            string     `json:"name"`
	SourceType      string     `json:"source_type"`
	SourceTypeLabel string     `json:"source_type_label"`
	Enabled         bool       `json:"enabled"`
	LastAlertAt     *time.Time `json:"last_alert_at,omitempty"`
}

// ConnectorIntegrationPart is a messaging integration in a connector detail.
type ConnectorIntegrationPart struct {
	ID           uint   `json:"id"`
	UUID         string `json:"uuid"`
	Provider     string `json:"provider"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	ChannelCount int    `json:"channel_count"`
}

// ConnectorMCPPart is the MCP server of a custom MCP connector.
type ConnectorMCPPart struct {
	ID              uint                    `json:"id"`
	URL             string                  `json:"url"`
	NamespacePrefix string                  `json:"namespace_prefix"`
	Enabled         bool                    `json:"enabled"`
	AuthType        string                  `json:"auth_type"`
	DisabledTools   []string                `json:"disabled_tools"`
	Runtime         *MCPServerRuntimeStatus `json:"runtime,omitempty"`
}

// ConnectorParts lists everything a connector groups.
type ConnectorParts struct {
	ToolInstances []ConnectorToolPart        `json:"tool_instances"`
	AlertSources  []ConnectorAlertPart       `json:"alert_sources"`
	Integrations  []ConnectorIntegrationPart `json:"integrations"`
	MCPServer     *ConnectorMCPPart          `json:"mcp_server,omitempty"`
}

// ConnectorDetail is the connector detail page payload.
type ConnectorDetail struct {
	ConnectorSummary
	Catalog *ConnectorCatalogEntry `json:"catalog,omitempty"`
	Parts   ConnectorParts         `json:"parts"`
}

// ---------- catalog ----------

func (s *ConnectorService) loadCatalog() (*connectorCatalog, error) {
	var toolTypes []database.ToolType
	if err := s.db.Find(&toolTypes).Error; err != nil {
		return nil, fmt.Errorf("list tool types: %w", err)
	}
	var httpNames []string
	if err := s.db.Model(&database.HTTPConnector{}).Pluck("tool_type_name", &httpNames).Error; err != nil {
		return nil, fmt.Errorf("list http connectors: %w", err)
	}
	var alertTypes []database.AlertSourceType
	if err := s.db.Find(&alertTypes).Error; err != nil {
		return nil, fmt.Errorf("list alert source types: %w", err)
	}
	return buildConnectorCatalog(toolTypes, httpNames, alertTypes), nil
}

// Catalog returns every kind of connector an operator can add.
func (s *ConnectorService) Catalog() ([]ConnectorCatalogEntry, error) {
	cat, err := s.loadCatalog()
	if err != nil {
		return nil, err
	}
	return cat.entries, nil
}

// ---------- loading ----------

type connectorRows struct {
	connectors   []database.Connector
	tools        []database.ToolInstance
	alerts       []database.AlertSourceInstance
	integrations []database.Integration
	mcp          []database.MCPServerConfig
	channelCount map[uint]int
	lastAlert    map[string]time.Time // alert source uuid -> newest alert
}

func (s *ConnectorService) loadRows(connectorIDs []uint) (*connectorRows, error) {
	rows := &connectorRows{channelCount: map[uint]int{}, lastAlert: map[string]time.Time{}}
	q := s.db.Order("name ASC, id ASC")
	if connectorIDs != nil {
		q = q.Where("id IN ?", connectorIDs)
	}
	if err := q.Find(&rows.connectors).Error; err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	ids := make([]uint, 0, len(rows.connectors))
	for _, c := range rows.connectors {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return rows, nil
	}
	if err := s.db.Preload("ToolType").Where("connector_id IN ?", ids).Order("name ASC").Find(&rows.tools).Error; err != nil {
		return nil, fmt.Errorf("list tool instances: %w", err)
	}
	if err := s.db.Preload("AlertSourceType").Where("connector_id IN ?", ids).Order("name ASC").Find(&rows.alerts).Error; err != nil {
		return nil, fmt.Errorf("list alert sources: %w", err)
	}
	if err := s.db.Where("connector_id IN ?", ids).Order("name ASC").Find(&rows.integrations).Error; err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	if err := s.db.Where("connector_id IN ?", ids).Find(&rows.mcp).Error; err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}

	if len(rows.integrations) > 0 {
		integIDs := make([]uint, 0, len(rows.integrations))
		for _, in := range rows.integrations {
			integIDs = append(integIDs, in.ID)
		}
		type cnt struct {
			IntegrationID uint
			N             int
		}
		var counts []cnt
		if err := s.db.Model(&database.Channel{}).Select("integration_id, COUNT(*) AS n").
			Where("integration_id IN ?", integIDs).Group("integration_id").Scan(&counts).Error; err != nil {
			return nil, fmt.Errorf("count channels: %w", err)
		}
		for _, c := range counts {
			rows.channelCount[c.IntegrationID] = c.N
		}
	}

	if len(rows.alerts) > 0 {
		uuids := make([]string, 0, len(rows.alerts))
		for _, a := range rows.alerts {
			uuids = append(uuids, a.UUID)
		}
		// Newest alert per source. Scan the max as a string-friendly value:
		// SQLite returns MAX() of a datetime as text, PostgreSQL as a time.
		type last struct {
			SourceUUID string
			Last       database.FlexTime
		}
		var lasts []last
		if err := s.db.Model(&database.Alert{}).
			Select("source_uuid, MAX(COALESCE(last_seen_at, fired_at)) AS last").
			Where("source_uuid IN ?", uuids).Group("source_uuid").Scan(&lasts).Error; err != nil {
			return nil, fmt.Errorf("last alert per source: %w", err)
		}
		for _, l := range lasts {
			if !l.Last.Time.IsZero() {
				rows.lastAlert[l.SourceUUID] = l.Last.Time
			}
		}
	}
	return rows, nil
}

func (s *ConnectorService) fetchMCPStatus(ctx context.Context, need bool) (map[uint]MCPServerRuntimeStatus, error) {
	if !need || s.mcpStatus == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.mcpStatus(ctx)
}

// build turns loaded rows into details, in connector order.
func (s *ConnectorService) build(ctx context.Context, rows *connectorRows, cat *connectorCatalog) []ConnectorDetail {
	byID := map[uint]*ConnectorDetail{}
	out := make([]ConnectorDetail, len(rows.connectors))
	for i, c := range rows.connectors {
		d := &out[i]
		d.ID, d.UUID, d.Name, d.CatalogID, d.CreatedAt = c.ID, c.UUID, c.Name, c.CatalogID, c.CreatedAt
		d.Parts = ConnectorParts{
			ToolInstances: []ConnectorToolPart{},
			AlertSources:  []ConnectorAlertPart{},
			Integrations:  []ConnectorIntegrationPart{},
		}
		if e, ok := cat.byID[c.CatalogID]; ok {
			entry := *e
			d.Catalog = &entry
			d.CatalogName, d.Kind, d.Category, d.Capabilities = e.Name, e.Kind, e.Category, e.Capabilities
		} else {
			d.CatalogName, d.Kind, d.Category, d.Capabilities = c.CatalogID, ConnectorKindBuiltin, "other", []string{}
		}
		byID[c.ID] = d
	}

	for _, t := range rows.tools {
		d := byID[*t.ConnectorID]
		d.Parts.ToolInstances = append(d.Parts.ToolInstances, ConnectorToolPart{
			ID: t.ID, Name: t.Name, LogicalName: t.LogicalName, ToolType: t.ToolType.Name,
			Enabled: t.Enabled, Managed: t.ToolType.Kind == database.ToolTypeKindMCP,
		})
	}
	for _, a := range rows.alerts {
		d := byID[*a.ConnectorID]
		part := ConnectorAlertPart{
			ID: a.ID, UUID: a.UUID, Name: a.Name, SourceType: a.AlertSourceType.Name,
			SourceTypeLabel: a.AlertSourceType.DisplayName, Enabled: a.Enabled,
		}
		if t, ok := rows.lastAlert[a.UUID]; ok {
			tt := t
			part.LastAlertAt = &tt
			if d.LastAlertAt == nil || tt.After(*d.LastAlertAt) {
				d.LastAlertAt = &tt
			}
		}
		d.Parts.AlertSources = append(d.Parts.AlertSources, part)
	}
	for _, in := range rows.integrations {
		d := byID[*in.ConnectorID]
		d.Parts.Integrations = append(d.Parts.Integrations, ConnectorIntegrationPart{
			ID: in.ID, UUID: in.UUID, Provider: string(in.Provider), Name: in.Name,
			Enabled: in.Enabled, ChannelCount: rows.channelCount[in.ID],
		})
	}

	statuses, statusErr := s.fetchMCPStatus(ctx, len(rows.mcp) > 0)
	for _, m := range rows.mcp {
		d := byID[*m.ConnectorID]
		part := &ConnectorMCPPart{
			ID: m.ID, URL: m.URL, NamespacePrefix: m.NamespacePrefix, Enabled: m.Enabled,
			AuthType: mcpAuthType(m.AuthConfig), DisabledTools: []string(m.DisabledTools),
		}
		if part.DisabledTools == nil {
			part.DisabledTools = []string{}
		}
		if st, ok := statuses[m.ID]; ok {
			stc := st
			part.Runtime = &stc
		}
		d.Parts.MCPServer = part
	}

	for i := range out {
		d := &out[i]
		d.Counts = ConnectorCounts{
			ToolInstances: len(d.Parts.ToolInstances),
			AlertSources:  len(d.Parts.AlertSources),
			Integrations:  len(d.Parts.Integrations),
		}
		for _, in := range d.Parts.Integrations {
			d.Counts.Channels += in.ChannelCount
		}
		if d.Parts.MCPServer != nil && d.Parts.MCPServer.Runtime != nil {
			d.Counts.MCPTools = d.Parts.MCPServer.Runtime.ToolCount
		}
		d.Enabled, d.Status = connectorStatus(d, statusErr)
	}
	return out
}

// connectorStatus derives the coarse state of a connector from its parts.
func connectorStatus(d *ConnectorDetail, mcpStatusErr error) (bool, ConnectorStatus) {
	enabled := false
	total := 0
	for _, t := range d.Parts.ToolInstances {
		total++
		enabled = enabled || t.Enabled
	}
	for _, a := range d.Parts.AlertSources {
		total++
		enabled = enabled || a.Enabled
	}
	for _, in := range d.Parts.Integrations {
		total++
		enabled = enabled || in.Enabled
	}
	if m := d.Parts.MCPServer; m != nil {
		total++
		enabled = enabled || m.Enabled
	}
	if total == 0 {
		return false, ConnectorStatus{State: "needs_setup", Detail: "Nothing is configured yet."}
	}
	if !enabled {
		return false, ConnectorStatus{State: "disabled"}
	}
	if m := d.Parts.MCPServer; m != nil && m.Enabled {
		switch {
		case m.Runtime != nil && m.Runtime.Connected:
			at := m.Runtime.CheckedAt
			return true, ConnectorStatus{State: "connected", Detail: fmt.Sprintf("%d tools", m.Runtime.ToolCount), ObservedAt: &at}
		case m.Runtime != nil && m.Runtime.Error != "":
			at := m.Runtime.CheckedAt
			return true, ConnectorStatus{State: "error", Detail: m.Runtime.Error, ObservedAt: &at}
		case mcpStatusErr != nil:
			return true, ConnectorStatus{State: "unknown", Detail: "Could not reach the MCP gateway."}
		default:
			return true, ConnectorStatus{State: "unknown", Detail: "Not connected yet."}
		}
	}
	for _, in := range d.Parts.Integrations {
		if in.Enabled && in.ChannelCount == 0 {
			return true, ConnectorStatus{State: "needs_setup", Detail: "Add a channel."}
		}
	}
	st := ConnectorStatus{State: "ok"}
	if d.LastAlertAt != nil {
		st.ObservedAt = d.LastAlertAt
		st.Detail = "Last alert received"
	}
	return true, st
}

// mcpAuthType returns the auth type of a stored auth_config ("none" when
// empty). It never returns secret values.
func mcpAuthType(cfg database.JSONB) string {
	if len(cfg) == 0 {
		return "none"
	}
	if t, ok := cfg["type"].(string); ok && t != "" {
		return t
	}
	return "none"
}

// ---------- public queries ----------

// List returns every connector with its status.
func (s *ConnectorService) List(ctx context.Context) ([]ConnectorSummary, error) {
	cat, err := s.loadCatalog()
	if err != nil {
		return nil, err
	}
	rows, err := s.loadRows(nil)
	if err != nil {
		return nil, err
	}
	details := s.build(ctx, rows, cat)
	out := make([]ConnectorSummary, len(details))
	for i := range details {
		out[i] = details[i].ConnectorSummary
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CatalogName != out[j].CatalogName {
			return strings.ToLower(out[i].CatalogName) < strings.ToLower(out[j].CatalogName)
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Get returns one connector with its parts.
func (s *ConnectorService) Get(ctx context.Context, connectorUUID string) (*ConnectorDetail, error) {
	c, err := s.byUUID(s.db, connectorUUID)
	if err != nil {
		return nil, err
	}
	cat, err := s.loadCatalog()
	if err != nil {
		return nil, err
	}
	rows, err := s.loadRows([]uint{c.ID})
	if err != nil {
		return nil, err
	}
	details := s.build(ctx, rows, cat)
	if len(details) != 1 {
		return nil, ErrConnectorNotFound
	}
	return &details[0], nil
}

func (s *ConnectorService) byUUID(tx *gorm.DB, connectorUUID string) (*database.Connector, error) {
	var c database.Connector
	if err := tx.Where("uuid = ?", connectorUUID).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrConnectorNotFound
		}
		return nil, fmt.Errorf("load connector: %w", err)
	}
	return &c, nil
}

// ---------- mutations ----------

// Rename sets the display name.
func (s *ConnectorService) Rename(connectorUUID, name string) (*database.Connector, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("validation failed: name is required")
	}
	if len(name) > 255 {
		return nil, fmt.Errorf("validation failed: name is longer than 255 characters")
	}
	c, err := s.byUUID(s.db, connectorUUID)
	if err != nil {
		return nil, err
	}
	if err := s.db.Model(c).Update("name", name).Error; err != nil {
		return nil, fmt.Errorf("rename connector: %w", err)
	}
	c.Name = name
	return c, nil
}

// Delete removes the connector and every part it groups, through the same
// service methods the per-part endpoints use. It stops at the first failure;
// parts deleted before it stay deleted and the connector row stays, so the
// operator sees what is left.
func (s *ConnectorService) Delete(ctx context.Context, connectorUUID string) error {
	d, err := s.Get(ctx, connectorUUID)
	if err != nil {
		return err
	}
	changed := false
	defer func() {
		if changed && s.deps.AfterMutation != nil {
			s.deps.AfterMutation()
		}
	}()
	// The MCP server first: its delete also removes its managed tool instance.
	if m := d.Parts.MCPServer; m != nil && s.deps.DeleteMCPServer != nil {
		if err := s.deps.DeleteMCPServer(m.ID); err != nil {
			return fmt.Errorf("delete MCP server: %w", err)
		}
		changed = true
	}
	for _, t := range d.Parts.ToolInstances {
		if t.Managed || s.deps.DeleteToolInstance == nil {
			continue
		}
		if err := s.deps.DeleteToolInstance(t.ID); err != nil {
			return fmt.Errorf("delete tool %q: %w", t.Name, err)
		}
		changed = true
	}
	for _, a := range d.Parts.AlertSources {
		if s.deps.DeleteAlertSource == nil {
			continue
		}
		if err := s.deps.DeleteAlertSource(a.ID); err != nil {
			return fmt.Errorf("delete alert source %q: %w", a.Name, err)
		}
		changed = true
	}
	for _, in := range d.Parts.Integrations {
		if s.deps.DeleteIntegration == nil {
			continue
		}
		if err := s.deps.DeleteIntegration(in.UUID); err != nil {
			return fmt.Errorf("delete %s integration: %w", in.Provider, err)
		}
		changed = true
	}
	if err := s.db.Where("uuid = ?", connectorUUID).Delete(&database.Connector{}).Error; err != nil {
		return fmt.Errorf("delete connector: %w", err)
	}
	return nil
}

// partInfo is what the service needs to know about one part row.
type partInfo struct {
	table        string
	id           uint
	name         string
	typeName     string // tool type / alert source type / provider
	toolTypeKind string
	connectorID  *uint
}

func (s *ConnectorService) loadPart(tx *gorm.DB, kind string, id uint) (*partInfo, error) {
	notFound := func(err error) error {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConnectorPartNotFound
		}
		return err
	}
	switch kind {
	case PartToolInstance:
		var t database.ToolInstance
		if err := tx.Preload("ToolType").First(&t, id).Error; err != nil {
			return nil, notFound(err)
		}
		return &partInfo{"tool_instances", t.ID, t.Name, t.ToolType.Name, t.ToolType.Kind, t.ConnectorID}, nil
	case PartAlertSource:
		var a database.AlertSourceInstance
		if err := tx.Preload("AlertSourceType").First(&a, id).Error; err != nil {
			return nil, notFound(err)
		}
		return &partInfo{"alert_source_instances", a.ID, a.Name, a.AlertSourceType.Name, "", a.ConnectorID}, nil
	case PartIntegration:
		var in database.Integration
		if err := tx.First(&in, id).Error; err != nil {
			return nil, notFound(err)
		}
		return &partInfo{"integrations", in.ID, in.Name, string(in.Provider), "", in.ConnectorID}, nil
	case PartMCPServer:
		var m database.MCPServerConfig
		if err := tx.First(&m, id).Error; err != nil {
			return nil, notFound(err)
		}
		return &partInfo{"mcp_server_configs", m.ID, m.Name, "", "", m.ConnectorID}, nil
	}
	return nil, fmt.Errorf("validation failed: unknown part kind %q", kind)
}

func (s *ConnectorService) catalogIDForPart(cat *connectorCatalog, kind string, p *partInfo) string {
	switch kind {
	case PartToolInstance:
		return cat.catalogIDForToolType(database.ToolType{Name: p.typeName, Kind: p.toolTypeKind})
	case PartAlertSource:
		return cat.catalogIDForAlertType(p.typeName)
	case PartIntegration:
		return cat.catalogIDForProvider(database.MessagingProvider(p.typeName))
	case PartMCPServer:
		return CatalogIDCustomMCP
	}
	return ""
}

// LinkNew attaches a just-created part. With connectorUUID set, the part
// joins that connector (it must accept the part's type); otherwise a new
// connector is created for it, named after the part. Errors leave the part
// unlinked; Reconcile links it on the next start.
func (s *ConnectorService) LinkNew(kind string, id uint, connectorUUID string) (*database.Connector, error) {
	cat, err := s.loadCatalog()
	if err != nil {
		return nil, err
	}
	var linked *database.Connector
	err = s.db.Transaction(func(tx *gorm.DB) error {
		p, err := s.loadPart(tx, kind, id)
		if err != nil {
			return err
		}
		if connectorUUID != "" {
			c, err := s.byUUID(tx, connectorUUID)
			if err != nil {
				return err
			}
			if !cat.accepts(c.CatalogID, kind, p.typeName, p.toolTypeKind) {
				return ErrConnectorPartIncompatible
			}
			linked = c
		} else {
			catalogID := s.catalogIDForPart(cat, kind, p)
			if catalogID == "" {
				return nil // hidden built-in: not a connector
			}
			c := &database.Connector{UUID: uuid.New().String(), CatalogID: catalogID, Name: p.name}
			if err := tx.Create(c).Error; err != nil {
				return fmt.Errorf("create connector: %w", err)
			}
			linked = c
		}
		if err := tx.Table(p.table).Where("id = ?", p.id).Update("connector_id", linked.ID).Error; err != nil {
			return err
		}
		if kind == PartMCPServer {
			return linkManagedTool(tx, p.id, linked.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return linked, nil
}

// Attach moves an existing part into this connector. The connector it
// leaves is deleted when it has nothing left.
func (s *ConnectorService) Attach(connectorUUID, kind string, id uint) error {
	cat, err := s.loadCatalog()
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		c, err := s.byUUID(tx, connectorUUID)
		if err != nil {
			return err
		}
		p, err := s.loadPart(tx, kind, id)
		if err != nil {
			return err
		}
		if !cat.accepts(c.CatalogID, kind, p.typeName, p.toolTypeKind) {
			return ErrConnectorPartIncompatible
		}
		if kind == PartMCPServer {
			var n int64
			if err := tx.Model(&database.MCPServerConfig{}).Where("connector_id = ? AND id <> ?", c.ID, id).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("validation failed: a connector has at most one MCP server")
			}
		}
		if err := tx.Table(p.table).Where("id = ?", p.id).Update("connector_id", c.ID).Error; err != nil {
			return err
		}
		// The managed tool instance follows its MCP server.
		if kind == PartMCPServer {
			if err := linkManagedTool(tx, id, c.ID); err != nil {
				return err
			}
		}
		if p.connectorID != nil && *p.connectorID != c.ID {
			return pruneConnector(tx, *p.connectorID)
		}
		return nil
	})
}

// Detach moves a part out of this connector into a new connector of its own.
func (s *ConnectorService) Detach(connectorUUID, kind string, id uint) (*database.Connector, error) {
	if kind == PartMCPServer {
		return nil, fmt.Errorf("validation failed: the MCP server is the connector; delete the connector instead")
	}
	c, err := s.byUUID(s.db, connectorUUID)
	if err != nil {
		return nil, err
	}
	p, err := s.loadPart(s.db, kind, id)
	if err != nil {
		return nil, err
	}
	if p.connectorID == nil || *p.connectorID != c.ID {
		return nil, ErrConnectorPartNotFound
	}
	if p.toolTypeKind == database.ToolTypeKindMCP {
		return nil, fmt.Errorf("validation failed: this tool belongs to the MCP server")
	}
	nc, err := s.LinkNew(kind, id, "")
	if err != nil {
		return nil, err
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error { return pruneConnector(tx, c.ID) }); err != nil {
		slog.Warn("connector prune after detach failed", "connector", connectorUUID, "err", err)
	}
	return nc, nil
}

// linkManagedTool points the managed tool instance of MCP server mcpID at
// connector connectorID.
func linkManagedTool(tx *gorm.DB, mcpID, connectorID uint) error {
	var m database.MCPServerConfig
	if err := tx.First(&m, mcpID).Error; err != nil {
		return err
	}
	var typeIDs []uint
	if err := tx.Model(&database.ToolType{}).Where("kind = ? AND name = ?", database.ToolTypeKindMCP, m.NamespacePrefix).Pluck("id", &typeIDs).Error; err != nil {
		return err
	}
	if len(typeIDs) == 0 {
		return nil
	}
	return tx.Model(&database.ToolInstance{}).Where("tool_type_id IN ?", typeIDs).Update("connector_id", connectorID).Error
}

// LookupByPart returns the UUID of the connector a part belongs to.
func (s *ConnectorService) LookupByPart(kind string, id uint) (string, error) {
	p, err := s.loadPart(s.db, kind, id)
	if err != nil {
		return "", err
	}
	if p.connectorID == nil {
		return "", ErrConnectorNotFound
	}
	var c database.Connector
	if err := s.db.First(&c, *p.connectorID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrConnectorNotFound
		}
		return "", err
	}
	return c.UUID, nil
}

// pruneConnector deletes a connector that has no parts left.
func pruneConnector(tx *gorm.DB, id uint) error {
	for _, model := range []interface{}{&database.ToolInstance{}, &database.AlertSourceInstance{}, &database.Integration{}, &database.MCPServerConfig{}} {
		var n int64
		if err := tx.Model(model).Where("connector_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
	return tx.Delete(&database.Connector{}, id).Error
}

// Reconcile gives every unlinked part a connector of its own (upgrade
// backfill, and parts created by a client that did not link them), points
// MCP-managed tool instances at their MCP server's connector, and deletes
// connectors with nothing left. Idempotent; safe on every start.
func (s *ConnectorService) Reconcile() error {
	cat, err := s.loadCatalog()
	if err != nil {
		return err
	}
	created := 0
	link := func(kind string, ids []uint) error {
		for _, id := range ids {
			if _, err := s.LinkNew(kind, id, ""); err != nil {
				return fmt.Errorf("link %s %d: %w", kind, id, err)
			}
			created++
		}
		return nil
	}

	// MCP servers first, so their managed tool instances can follow them.
	var mcpIDs []uint
	if err := s.db.Model(&database.MCPServerConfig{}).Where("connector_id IS NULL").Order("id").Pluck("id", &mcpIDs).Error; err != nil {
		return err
	}
	if err := link(PartMCPServer, mcpIDs); err != nil {
		return err
	}
	var mcps []database.MCPServerConfig
	if err := s.db.Where("connector_id IS NOT NULL").Find(&mcps).Error; err != nil {
		return err
	}
	for _, m := range mcps {
		if err := linkManagedTool(s.db, m.ID, *m.ConnectorID); err != nil {
			return err
		}
	}

	var toolRows []database.ToolInstance
	if err := s.db.Preload("ToolType").Where("connector_id IS NULL").Order("id").Find(&toolRows).Error; err != nil {
		return err
	}
	var toolIDs []uint
	for _, t := range toolRows {
		if t.ToolType.Kind == database.ToolTypeKindMCP {
			continue // orphan managed instance; the MCP service owns it
		}
		if cat.catalogIDForToolType(t.ToolType) == "" {
			continue // hidden built-in
		}
		toolIDs = append(toolIDs, t.ID)
	}
	if err := link(PartToolInstance, toolIDs); err != nil {
		return err
	}

	var alertRows []database.AlertSourceInstance
	if err := s.db.Preload("AlertSourceType").Where("connector_id IS NULL").Order("id").Find(&alertRows).Error; err != nil {
		return err
	}
	var alertIDs []uint
	for _, a := range alertRows {
		if a.AlertSourceType.Deprecated {
			continue // legacy slack_channel rows: listening lives on Channels now
		}
		alertIDs = append(alertIDs, a.ID)
	}
	if err := link(PartAlertSource, alertIDs); err != nil {
		return err
	}

	var integIDs []uint
	if err := s.db.Model(&database.Integration{}).Where("connector_id IS NULL").Order("id").Pluck("id", &integIDs).Error; err != nil {
		return err
	}
	if err := link(PartIntegration, integIDs); err != nil {
		return err
	}

	var all []database.Connector
	if err := s.db.Find(&all).Error; err != nil {
		return err
	}
	pruned := 0
	for _, c := range all {
		before := c.ID
		if err := s.db.Transaction(func(tx *gorm.DB) error { return pruneConnector(tx, before) }); err != nil {
			return err
		}
		var n int64
		s.db.Model(&database.Connector{}).Where("id = ?", before).Count(&n)
		if n == 0 {
			pruned++
		}
	}
	if created > 0 || pruned > 0 {
		slog.Info("connectors reconciled", "created", created, "pruned", pruned)
	}
	return nil
}

// PruneEmpty deletes every connector with no parts. Call after a part was
// deleted through its own endpoint.
func (s *ConnectorService) PruneEmpty() {
	var ids []uint
	if err := s.db.Model(&database.Connector{}).Pluck("id", &ids).Error; err != nil {
		slog.Warn("connector prune: list failed", "err", err)
		return
	}
	for _, id := range ids {
		cid := id
		if err := s.db.Transaction(func(tx *gorm.DB) error { return pruneConnector(tx, cid) }); err != nil {
			slog.Warn("connector prune failed", "connector_id", cid, "err", err)
		}
	}
}
