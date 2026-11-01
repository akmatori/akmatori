package services

import (
	"sort"

	"github.com/akmatori/akmatori/internal/database"
)

// Connector capabilities: what a connector can do for the agent.
const (
	CapabilityTools     = "tools"     // the agent can call it (tool instances / MCP tools)
	CapabilityAlerts    = "alerts"    // it sends alerts to an Akmatori webhook
	CapabilityMessaging = "messaging" // channels to post to (and, for Slack, listen on)
)

// Connector kinds, shown in the Type column of the Connectors page.
const (
	ConnectorKindBuiltin    = "builtin"
	ConnectorKindCustomMCP  = "custom_mcp"
	ConnectorKindCustomHTTP = "custom_http"
)

// Part kinds: the rows a connector groups. Each part table carries a
// nullable connector_id.
const (
	PartToolInstance = "tool_instance"
	PartAlertSource  = "alert_source"
	PartIntegration  = "integration"
	PartMCPServer    = "mcp_server"
)

// CatalogIDCustomMCP is the catalog entry of every custom MCP connector.
const CatalogIDCustomMCP = "custom_mcp"

// httpCatalogPrefix prefixes the catalog id of an HTTP connector definition
// ("http:<tool_type_name>"): each definition is its own catalog entry.
const httpCatalogPrefix = "http:"

// ConnectorCatalogEntry describes one kind of connector an operator can add.
// It decides which part types may join a connector of this entry.
type ConnectorCatalogEntry struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Category          string   `json:"category"`
	Kind              string   `json:"kind"`
	Capabilities      []string `json:"capabilities"`
	ToolTypes         []string `json:"tool_types,omitempty"`
	AlertSourceTypes  []string `json:"alert_source_types,omitempty"`
	MessagingProvider string   `json:"messaging_provider,omitempty"`
	// ListenSupported is false for messaging providers that can only post.
	ListenSupported bool `json:"listen_supported,omitempty"`
	// Note is a short caveat shown on the catalog card, e.g. "Posting only".
	Note string `json:"note,omitempty"`
}

// staticCatalog lists the built-in connectors. Order is the Discover order.
// Credential-less built-in tools (incidents, proposals, events) are not
// connectors: they are part of Akmatori itself.
var staticCatalog = []ConnectorCatalogEntry{
	{ID: "alertmanager", Name: "Prometheus Alertmanager", Category: "monitoring", Description: "Receive alerts from Prometheus Alertmanager.", AlertSourceTypes: []string{"alertmanager"}},
	{ID: "zabbix", Name: "Zabbix", Category: "monitoring", Description: "Query hosts, problems and history; receive Zabbix alerts.", ToolTypes: []string{"zabbix"}, AlertSourceTypes: []string{"zabbix"}},
	{ID: "grafana", Name: "Grafana", Category: "monitoring", Description: "Query dashboards, data sources and alert rules; receive Grafana alerts.", ToolTypes: []string{"grafana"}, AlertSourceTypes: []string{"grafana"}},
	{ID: "victoria_metrics", Name: "VictoriaMetrics", Category: "monitoring", Description: "Run PromQL/MetricsQL queries.", ToolTypes: []string{"victoria_metrics"}},
	{ID: "datadog", Name: "Datadog", Category: "monitoring", Description: "Receive alerts from Datadog monitors.", AlertSourceTypes: []string{"datadog"}},
	{ID: "catchpoint", Name: "Catchpoint", Category: "monitoring", Description: "Digital experience monitoring tests and alerts.", ToolTypes: []string{"catchpoint"}},
	{ID: "pagerduty", Name: "PagerDuty", Category: "incident", Description: "Look up incidents and on-call; receive PagerDuty webhooks.", ToolTypes: []string{"pagerduty"}, AlertSourceTypes: []string{"pagerduty"}},
	{ID: "jira", Name: "Jira", Category: "incident", Description: "Search, read, comment on and transition issues.", ToolTypes: []string{"jira"}},
	{ID: "kubernetes", Name: "Kubernetes", Category: "infrastructure", Description: "Read-only diagnostics for pods, deployments, nodes, events and logs.", ToolTypes: []string{"kubernetes"}},
	{ID: "ssh", Name: "SSH", Category: "infrastructure", Description: "Run commands on your servers over SSH.", ToolTypes: []string{"ssh"}},
	{ID: "netbox", Name: "NetBox", Category: "infrastructure", Description: "CMDB: devices, IPAM, circuits, virtualization and tenancy.", ToolTypes: []string{"netbox"}},
	{ID: "postgresql", Name: "PostgreSQL", Category: "data", Description: "Read-only queries and diagnostics.", ToolTypes: []string{"postgresql"}},
	{ID: "clickhouse", Name: "ClickHouse", Category: "data", Description: "Read-only queries and OLAP diagnostics.", ToolTypes: []string{"clickhouse"}},
	{ID: "slack", Name: "Slack", Category: "messaging", Description: "Post alerts and results; listen to channels and answer mentions.", MessagingProvider: string(database.MessagingProviderSlack), ListenSupported: true},
	{ID: "telegram", Name: "Telegram", Category: "messaging", Description: "Post alerts and results to Telegram chats.", MessagingProvider: string(database.MessagingProviderTelegram), Note: "Posting only"},
	{ID: "discord", Name: "Discord", Category: "messaging", Description: "Post alerts and results to Discord channels.", MessagingProvider: "discord", Note: "Posting only"},
	{ID: CatalogIDCustomMCP, Name: "Custom MCP server", Category: "custom", Kind: ConnectorKindCustomMCP, Description: "Connect a remote MCP server by URL. Its tools become available to the skills you assign it to."},
}

// hiddenToolTypes are credential-less built-in tools. They are part of
// Akmatori itself, so they never appear as connectors.
var hiddenToolTypes = map[string]bool{"incidents": true, "proposals": true, "events": true}

// connectorCatalog is the catalog resolved against the live tool types and
// HTTP connector definitions.
type connectorCatalog struct {
	entries     []ConnectorCatalogEntry
	byID        map[string]*ConnectorCatalogEntry
	byToolType  map[string]string // tool type name -> catalog id
	byAlertType map[string]string // alert source type name -> catalog id
	byProvider  map[string]string // messaging provider -> catalog id
}

// buildConnectorCatalog fills in capabilities and adds one entry per HTTP
// connector definition and per tool type the static list does not know (so
// a tool type added later still shows up instead of disappearing).
func buildConnectorCatalog(toolTypes []database.ToolType, httpTypeNames []string, alertTypes []database.AlertSourceType) *connectorCatalog {
	c := &connectorCatalog{
		byID:        map[string]*ConnectorCatalogEntry{},
		byToolType:  map[string]string{},
		byAlertType: map[string]string{},
		byProvider:  map[string]string{},
	}
	for _, e := range staticCatalog {
		e.ToolTypes = append([]string(nil), e.ToolTypes...)
		e.AlertSourceTypes = append([]string(nil), e.AlertSourceTypes...)
		c.entries = append(c.entries, e)
	}

	known := map[string]bool{}
	for _, e := range staticCatalog {
		for _, t := range e.ToolTypes {
			known[t] = true
		}
	}
	httpTypes := map[string]bool{}
	sorted := append([]string(nil), httpTypeNames...)
	sort.Strings(sorted)
	for _, name := range sorted {
		httpTypes[name] = true
		c.entries = append(c.entries, ConnectorCatalogEntry{
			ID: httpCatalogPrefix + name, Name: name, Category: "custom", Kind: ConnectorKindCustomHTTP,
			Description: "Custom HTTP connector.", ToolTypes: []string{name},
		})
	}
	for _, tt := range toolTypes {
		if known[tt.Name] || httpTypes[tt.Name] || hiddenToolTypes[tt.Name] || tt.Kind == database.ToolTypeKindMCP {
			continue
		}
		c.entries = append(c.entries, ConnectorCatalogEntry{
			ID: tt.Name, Name: tt.Name, Category: "other", Description: tt.Description, ToolTypes: []string{tt.Name},
		})
	}
	knownAlert := map[string]bool{}
	for _, e := range staticCatalog {
		for _, t := range e.AlertSourceTypes {
			knownAlert[t] = true
		}
	}
	for _, at := range alertTypes {
		if knownAlert[at.Name] || at.Deprecated {
			continue
		}
		c.entries = append(c.entries, ConnectorCatalogEntry{
			ID: at.Name, Name: at.DisplayName, Category: "monitoring", Description: at.Description, AlertSourceTypes: []string{at.Name},
		})
	}

	for i := range c.entries {
		e := &c.entries[i]
		if e.Kind == "" {
			e.Kind = ConnectorKindBuiltin
		}
		e.Capabilities = nil
		if len(e.ToolTypes) > 0 || e.Kind == ConnectorKindCustomMCP {
			e.Capabilities = append(e.Capabilities, CapabilityTools)
		}
		if len(e.AlertSourceTypes) > 0 {
			e.Capabilities = append(e.Capabilities, CapabilityAlerts)
		}
		if e.MessagingProvider != "" {
			e.Capabilities = append(e.Capabilities, CapabilityMessaging)
		}
		c.byID[e.ID] = e
		for _, t := range e.ToolTypes {
			c.byToolType[t] = e.ID
		}
		for _, t := range e.AlertSourceTypes {
			c.byAlertType[t] = e.ID
		}
		if e.MessagingProvider != "" {
			c.byProvider[e.MessagingProvider] = e.ID
		}
	}
	return c
}

// catalogIDForToolType returns the catalog entry a tool instance of this type
// belongs to, or "" for hidden built-ins.
func (c *connectorCatalog) catalogIDForToolType(tt database.ToolType) string {
	if tt.Kind == database.ToolTypeKindMCP {
		return CatalogIDCustomMCP
	}
	if hiddenToolTypes[tt.Name] {
		return ""
	}
	if id, ok := c.byToolType[tt.Name]; ok {
		return id
	}
	return tt.Name
}

func (c *connectorCatalog) catalogIDForAlertType(name string) string {
	if id, ok := c.byAlertType[name]; ok {
		return id
	}
	return name
}

func (c *connectorCatalog) catalogIDForProvider(p database.MessagingProvider) string {
	if id, ok := c.byProvider[string(p)]; ok {
		return id
	}
	return string(p)
}

// accepts reports whether a part with the given type may join a connector of
// catalog entry id. kind is a Part* constant; typeName is the tool type,
// alert source type or messaging provider of the part.
func (c *connectorCatalog) accepts(id, kind, typeName string, toolTypeKind string) bool {
	e, ok := c.byID[id]
	if !ok {
		return false
	}
	switch kind {
	case PartToolInstance:
		if toolTypeKind == database.ToolTypeKindMCP {
			return e.Kind == ConnectorKindCustomMCP
		}
		for _, t := range e.ToolTypes {
			if t == typeName {
				return true
			}
		}
	case PartAlertSource:
		for _, t := range e.AlertSourceTypes {
			if t == typeName {
				return true
			}
		}
	case PartIntegration:
		return e.MessagingProvider == typeName
	case PartMCPServer:
		return e.Kind == ConnectorKindCustomMCP
	}
	return false
}
