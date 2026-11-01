package database

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// Connector is one configured connection to an outside system, as the
// operator sees it on the Connectors page: "Zabbix prod", "Slack",
// "Alertmanager", "GitHub MCP". It owns no configuration itself. The parts
// that do the work keep their own tables and point here through a nullable
// connector_id:
//
//   - tool_instances         (the agent can call it)
//   - alert_source_instances (it sends alerts to a webhook)
//   - integrations           (messaging: channels to listen and post)
//   - mcp_server_configs     (a custom remote MCP server)
//
// Ownership is always explicit. It is never inferred from a type or a name:
// a Zabbix webhook joins the "Zabbix prod" connector only when an operator
// (or the create request) says so. Upgrade installs get one connector per
// existing part (ConnectorService.Reconcile); parts can then be moved
// between connectors. CatalogID names the catalog entry (zabbix, slack,
// custom_mcp, ...) that decides which part types may join.
type Connector struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UUID      string    `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	CatalogID string    `gorm:"size:64;not null;index" json:"catalog_id"`
	Name      string    `gorm:"size:255;not null" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Connector) TableName() string {
	return "connectors"
}

// ToolTypeKindMCP marks a tool type that the API manages for a custom MCP
// server: one per MCPServerConfig, named exactly as its namespace prefix.
// Its single tool instance lets skills, cron jobs and the per-run allowlist
// treat MCP tools exactly like built-in tools. Operators never create or
// edit these rows directly; the MCP server CRUD keeps them in sync.
const ToolTypeKindMCP = "mcp"

// StringList is a JSON array of strings stored in a jsonb column.
type StringList []string

// Scan implements the sql.Scanner interface.
func (l *StringList) Scan(value interface{}) error {
	if value == nil {
		*l = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("StringList: unsupported scan type")
	}
	if len(raw) == 0 || string(raw) == "null" {
		*l = nil
		return nil
	}
	return json.Unmarshal(raw, l)
}

// Value implements the driver.Valuer interface.
func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	return json.Marshal([]string(l))
}

// FlexTime scans a timestamp that the driver may return as time.Time
// (PostgreSQL) or as text (SQLite aggregates such as MAX(datetime)).
type FlexTime struct {
	Time time.Time
}

// flexTimeLayouts are the text forms SQLite and the Go driver produce.
var flexTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// Scan implements the sql.Scanner interface.
func (f *FlexTime) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		f.Time = time.Time{}
		return nil
	case time.Time:
		f.Time = v
		return nil
	case []byte:
		return f.parse(string(v))
	case string:
		return f.parse(v)
	}
	return errors.New("FlexTime: unsupported scan type")
}

func (f *FlexTime) parse(s string) error {
	for _, layout := range flexTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			f.Time = t
			return nil
		}
	}
	return errors.New("FlexTime: unrecognized time format: " + s)
}
