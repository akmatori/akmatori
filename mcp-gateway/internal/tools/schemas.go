package tools

// ToolTypeSchema defines the configuration schema for a tool type
type ToolTypeSchema struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Version        string         `json:"version"`
	SettingsSchema SettingsSchema `json:"settings_schema"`
	Functions      []ToolFunction `json:"functions"`
}

// SettingsSchema defines the JSON schema for tool settings
type SettingsSchema struct {
	Type       string                    `json:"type"`
	Required   []string                  `json:"required,omitempty"`
	Properties map[string]PropertySchema `json:"properties"`
}

// PropertySchema defines a single property in the settings schema
type PropertySchema struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Secret      bool        `json:"secret,omitempty"`
	Format      string      `json:"format,omitempty"`
	Advanced    bool        `json:"advanced,omitempty"`
	Warning     string      `json:"warning,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Minimum     *int        `json:"minimum,omitempty"`
	Maximum     *int        `json:"maximum,omitempty"`
	MinItems    *int        `json:"minItems,omitempty"`
	Example     interface{} `json:"example,omitempty"`
	Items       *ItemSchema `json:"items,omitempty"`
}

// ItemSchema defines the schema for array items
type ItemSchema struct {
	Type       string                    `json:"type"`
	Required   []string                  `json:"required,omitempty"`
	Properties map[string]PropertySchema `json:"properties,omitempty"`
}

// ToolFunction describes a function provided by a tool
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters,omitempty"`
	Returns     string `json:"returns,omitempty"`
}

// Helper to create int pointer
func intPtr(i int) *int {
	return &i
}

// GetToolSchemas returns all tool type schemas
func GetToolSchemas() map[string]ToolTypeSchema {
	return map[string]ToolTypeSchema{
		"ssh":              getSSHSchema(),
		"zabbix":           getZabbixSchema(),
		"victoria_metrics": getVictoriaMetricsSchema(),
		"grafana":          getGrafanaSchema(),
		"catchpoint":       getCatchpointSchema(),
		"postgresql":       getPostgreSQLSchema(),
		"clickhouse":       getClickHouseSchema(),
		"pagerduty":        getPagerDutySchema(),
		"netbox":           getNetBoxSchema(),
		"kubernetes":       getK8sSchema(),
	}
}

// GetToolSchema returns the schema for a specific tool type
func GetToolSchema(name string) (ToolTypeSchema, bool) {
	schemas := GetToolSchemas()
	schema, ok := schemas[name]
	return schema, ok
}

func getSSHSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "ssh",
		Description: "SSH remote command execution tool. Execute commands across multiple servers in parallel with per-host configuration, jumphost support, and read-only mode for security.",
		Version:     "3.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{},
			Properties: map[string]PropertySchema{
				"ssh_keys": {
					Type:        "array",
					Description: "SSH private keys with unique names. Keys are managed via the SSH Keys API.",
					Items: &ItemSchema{
						Type:     "object",
						Required: []string{"id", "name", "private_key"},
						Properties: map[string]PropertySchema{
							"id": {
								Type:        "string",
								Description: "Unique identifier for the key (UUID)",
							},
							"name": {
								Type:        "string",
								Description: "Unique display name for the key",
							},
							"private_key": {
								Type:        "string",
								Description: "SSH private key content (PEM format)",
								Secret:      true,
								Format:      "textarea",
							},
							"is_default": {
								Type:        "boolean",
								Description: "Whether this is the default key for all hosts",
								Default:     false,
							},
							"created_at": {
								Type:        "string",
								Description: "Timestamp when key was created",
							},
						},
					},
				},
				"ssh_hosts": {
					Type:        "array",
					Description: "List of SSH host configurations",
					Items: &ItemSchema{
						Type:     "object",
						Required: []string{"hostname", "address"},
						Properties: map[string]PropertySchema{
							"hostname": {
								Type:        "string",
								Description: "Display name for this host (e.g., 'web-prod-1')",
								Example:     "web-prod-1",
							},
							"address": {
								Type:        "string",
								Description: "Connection address (IP or FQDN)",
								Example:     "192.168.1.10",
							},
							"user": {
								Type:        "string",
								Description: "SSH username",
								Default:     "root",
								Advanced:    true,
							},
							"port": {
								Type:        "integer",
								Description: "SSH port",
								Default:     22,
								Minimum:     intPtr(1),
								Maximum:     intPtr(65535),
								Advanced:    true,
							},
							"key_id": {
								Type:        "string",
								Description: "ID of the SSH key to use for this host (uses default key if empty)",
								Advanced:    true,
							},
							"jumphost_address": {
								Type:        "string",
								Description: "Bastion/jumphost address (leave empty for direct connection)",
								Advanced:    true,
							},
							"jumphost_user": {
								Type:        "string",
								Description: "Jumphost SSH username (defaults to host user)",
								Advanced:    true,
							},
							"jumphost_port": {
								Type:        "integer",
								Description: "Jumphost SSH port",
								Default:     22,
								Minimum:     intPtr(1),
								Maximum:     intPtr(65535),
								Advanced:    true,
							},
							"allow_write_commands": {
								Type:        "boolean",
								Description: "Allow write/destructive commands (WARNING: security risk)",
								Default:     false,
								Advanced:    true,
								Warning:     "Enabling this allows destructive commands like rm, mv, kill, etc.",
							},
						},
					},
				},
				"ssh_command_timeout": {
					Type:        "integer",
					Description: "Timeout in seconds for each command execution",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(600),
					Advanced:    true,
				},
				"ssh_connection_timeout": {
					Type:        "integer",
					Description: "Timeout in seconds for SSH connection establishment",
					Default:     10,
					Minimum:     intPtr(5),
					Maximum:     intPtr(60),
					Advanced:    true,
				},
				"ssh_known_hosts_policy": {
					Type:        "string",
					Enum:        []string{"strict", "auto_add", "ignore"},
					Description: "SSH known hosts verification policy",
					Default:     "auto_add",
					Advanced:    true,
				},
				"ssh_debug": {
					Type:        "boolean",
					Description: "Enable debug logging",
					Default:     false,
					Advanced:    true,
				},
				"allow_adhoc_connections": {
					Type:        "boolean",
					Description: "Allow SSH connections to servers not in the ssh_hosts list. The agent can connect to any server using default credentials.",
					Default:     false,
				},
				"adhoc_default_user": {
					Type:        "string",
					Description: "Default SSH username for ad-hoc connections",
					Default:     "root",
					Advanced:    true,
				},
				"adhoc_default_port": {
					Type:        "integer",
					Description: "Default SSH port for ad-hoc connections",
					Default:     22,
					Minimum:     intPtr(1),
					Maximum:     intPtr(65535),
					Advanced:    true,
				},
				"adhoc_allow_write_commands": {
					Type:        "boolean",
					Description: "Allow write/destructive commands on ad-hoc connections (WARNING: security risk)",
					Default:     false,
					Advanced:    true,
					Warning:     "Enabling this allows destructive commands like rm, mv, kill on any server the agent connects to.",
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "execute_command",
				Description: "Execute a command on all or specified servers in parallel. Commands are validated against read-only mode (blocks rm, mv, kill, etc. by default).",
				Parameters:  "command: str - The shell command to execute; servers: list[str] - Optional list of hostnames to target (defaults to all)",
				Returns:     "JSON string with per-server results: {results: [{server, success, stdout, stderr, exit_code, duration_ms}], summary: {total, succeeded, failed}}",
			},
			{
				Name:        "test_connectivity",
				Description: "Test SSH connectivity to specified or all configured servers (including through jumphosts if configured). When ad-hoc connections are enabled, can test connectivity to any server.",
				Parameters:  "servers: list[str] - Optional list of server hostnames/addresses to test (defaults to all configured servers)",
				Returns:     "JSON string with connectivity status: {results: [{server, reachable, error}], summary: {total, reachable, unreachable}}",
			},
			{
				Name:        "get_server_info",
				Description: "Get basic system information (hostname, OS, uptime) from all servers",
				Parameters:  "None",
				Returns:     "JSON string with server info: {results: [{server, success, stdout, stderr}]}",
			},
		},
	}
}

func getZabbixSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "zabbix",
		Description: "Zabbix monitoring integration. Query hosts, problems, triggers, items, and history data from your Zabbix server.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"zabbix_url"},
			Properties: map[string]PropertySchema{
				"zabbix_url": {
					Type:        "string",
					Description: "Zabbix server URL (e.g., http://zabbix.example.com/api_jsonrpc.php)",
					Example:     "http://zabbix.example.com/api_jsonrpc.php",
				},
				"auth_method": {
					Type:        "string",
					Description: "Authentication method",
					Enum:        []string{"token", "credentials"},
					Default:     "token",
				},
				"zabbix_token": {
					Type:        "string",
					Description: "Zabbix API token (recommended)",
					Secret:      true,
				},
				"zabbix_username": {
					Type:        "string",
					Description: "Zabbix username (if using credentials auth)",
					Advanced:    true,
				},
				"zabbix_password": {
					Type:        "string",
					Description: "Zabbix password (if using credentials auth)",
					Secret:      true,
					Advanced:    true,
				},
				"zabbix_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
				"zabbix_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "get_hosts",
				Description: "Get hosts from Zabbix monitoring system",
				Parameters:  "output, filter, search, limit",
				Returns:     "JSON array of host objects",
			},
			{
				Name:        "get_problems",
				Description: "Get current problems/alerts from Zabbix",
				Parameters:  "recent, severity_min, hostids, limit",
				Returns:     "JSON array of problem objects",
			},
			{
				Name:        "get_history",
				Description: "Get metric history data from Zabbix",
				Parameters:  "itemids (required), history, time_from, time_till, limit, sortfield, sortorder",
				Returns:     "JSON array of history records",
			},
			{
				Name:        "get_items",
				Description: "Get items (metrics) from Zabbix",
				Parameters:  "hostids, search, output, limit",
				Returns:     "JSON array of item objects",
			},
			{
				Name:        "get_triggers",
				Description: "Get triggers from Zabbix",
				Parameters:  "hostids, only_true, min_severity, output",
				Returns:     "JSON array of trigger objects",
			},
			{
				Name:        "api_request",
				Description: "Make a raw Zabbix API request",
				Parameters:  "method (required), params",
				Returns:     "Raw API response",
			},
		},
	}
}

func getVictoriaMetricsSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "victoria_metrics",
		Description: "VictoriaMetrics time-series database integration. Query metrics using PromQL, explore label values and series metadata.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"vm_url"},
			Properties: map[string]PropertySchema{
				"vm_url": {
					Type:        "string",
					Description: "VictoriaMetrics server URL (e.g., https://victoriametrics.example.com)",
					Example:     "https://victoriametrics.example.com",
				},
				"vm_auth_method": {
					Type:        "string",
					Description: "Authentication method",
					Enum:        []string{"none", "bearer_token", "basic_auth"},
					Default:     "bearer_token",
				},
				"vm_bearer_token": {
					Type:        "string",
					Description: "Bearer token for authentication",
					Secret:      true,
				},
				"vm_username": {
					Type:        "string",
					Description: "Username for basic auth (if using basic_auth method)",
					Advanced:    true,
				},
				"vm_password": {
					Type:        "string",
					Description: "Password for basic auth (if using basic_auth method)",
					Secret:      true,
					Advanced:    true,
				},
				"vm_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
				"vm_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "instant_query",
				Description: "Execute a PromQL instant query",
				Parameters:  "query (required), time, step, timeout",
				Returns:     "JSON with resultType and result array",
			},
			{
				Name:        "range_query",
				Description: "Execute a PromQL range query",
				Parameters:  "query (required), start (required), end (required), step (required), timeout",
				Returns:     "JSON with resultType and result array (matrix)",
			},
			{
				Name:        "label_values",
				Description: "Get label values for a given label name",
				Parameters:  "label_name (required), match, start, end",
				Returns:     "JSON array of label values",
			},
			{
				Name:        "series",
				Description: "Find series matching a label set",
				Parameters:  "match (required), start, end",
				Returns:     "JSON array of series label sets",
			},
			{
				Name:        "api_request",
				Description: "Make a generic HTTP request to VictoriaMetrics API",
				Parameters:  "path (required), method, params",
				Returns:     "Raw API response data",
			},
		},
	}
}

func getGrafanaSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "grafana",
		Description: "Grafana observability platform integration. Search dashboards, query data sources (Prometheus, Loki) via proxy, manage alerts and silences, and create annotations.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"grafana_url", "grafana_api_token"},
			Properties: map[string]PropertySchema{
				"grafana_url": {
					Type:        "string",
					Description: "Grafana server URL (e.g., https://grafana.example.com)",
					Example:     "https://grafana.example.com",
				},
				"grafana_api_token": {
					Type:        "string",
					Description: "Grafana API token (Service Account token recommended)",
					Secret:      true,
				},
				"grafana_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
				"grafana_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "search_dashboards",
				Description: "Search and list Grafana dashboards by query, tag, or folder",
				Parameters:  "query, tag, type (dash-db|dash-folder), folder_id, limit",
				Returns:     "JSON array of dashboard search results",
			},
			{
				Name:        "get_dashboard",
				Description: "Get a full dashboard model by UID",
				Parameters:  "uid (required)",
				Returns:     "JSON dashboard object with metadata and panels",
			},
			{
				Name:        "get_dashboard_panels",
				Description: "Get a summary list of panels from a dashboard",
				Parameters:  "uid (required)",
				Returns:     "JSON array of panel summaries (id, title, type, datasource)",
			},
			{
				Name:        "get_alert_rules",
				Description: "List all provisioned alert rules from Grafana Unified Alerting",
				Parameters:  "None",
				Returns:     "JSON array of alert rule objects",
			},
			{
				Name:        "get_alert_instances",
				Description: "Get firing and pending alert instances from Grafana Alertmanager",
				Parameters:  "filter, silenced, inhibited, active",
				Returns:     "JSON array of alert instance objects",
			},
			{
				Name:        "get_alert_rule",
				Description: "Get a specific alert rule by UID",
				Parameters:  "uid (required)",
				Returns:     "JSON alert rule object with full definition",
			},
			{
				Name:        "silence_alert",
				Description: "Create a silence in Grafana Alertmanager",
				Parameters:  "matchers (required), starts_at (required), ends_at (required), created_by (required), comment (required)",
				Returns:     "JSON with silence ID",
			},
			{
				Name:        "list_data_sources",
				Description: "List all configured data sources in Grafana",
				Parameters:  "None",
				Returns:     "JSON array of data source objects (uid, name, type, url)",
			},
			{
				Name:        "query_data_source",
				Description: "Query a data source via the Grafana unified query API",
				Parameters:  "datasource_uid (required), queries (required), from, to",
				Returns:     "JSON query results",
			},
			{
				Name:        "query_prometheus",
				Description: "Query a Prometheus-type data source via Grafana proxy (instant or range)",
				Parameters:  "datasource_uid (required), expr (required), start, end, step, instant, range, from, to",
				Returns:     "JSON Prometheus query results",
			},
			{
				Name:        "query_loki",
				Description: "Query a Loki-type data source via Grafana proxy (log queries)",
				Parameters:  "datasource_uid (required), expr (required), limit, direction, start, end, from, to",
				Returns:     "JSON Loki query results",
			},
			{
				Name:        "create_annotation",
				Description: "Create an annotation on a Grafana dashboard or globally",
				Parameters:  "text (required), dashboard_id, panel_id, tags, time, time_end",
				Returns:     "JSON with annotation ID",
			},
			{
				Name:        "get_annotations",
				Description: "List annotations with optional filters",
				Parameters:  "from, to, dashboard_id, panel_id, tags, limit, type (annotation|alert)",
				Returns:     "JSON array of annotation objects",
			},
		},
	}
}

func getCatchpointSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "catchpoint",
		Description: "Catchpoint Digital Experience Monitoring integration. Query test performance, alerts, errors, nodes, and internet outages from the Catchpoint API.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"catchpoint_api_token"},
			Properties: map[string]PropertySchema{
				"catchpoint_url": {
					Type:        "string",
					Description: "Catchpoint API base URL",
					Default:     "https://io.catchpoint.com/api",
				},
				"catchpoint_api_token": {
					Type:        "string",
					Description: "Catchpoint API bearer token (static JWT)",
					Secret:      true,
				},
				"catchpoint_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
				"catchpoint_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "get_alerts",
				Description: "Get test alerts from Catchpoint",
				Parameters:  "severity, start_time, end_time, test_ids, page_number, page_size",
				Returns:     "JSON array of alert objects",
			},
			{
				Name:        "get_alert_details",
				Description: "Get detailed information for specific alerts",
				Parameters:  "alert_ids (required)",
				Returns:     "JSON alert detail objects",
			},
			{
				Name:        "get_test_performance",
				Description: "Get aggregated test performance metrics",
				Parameters:  "test_ids (required), start_time, end_time, metrics, dimensions",
				Returns:     "JSON with aggregated performance data",
			},
			{
				Name:        "get_test_performance_raw",
				Description: "Get raw test performance data points",
				Parameters:  "test_ids (required), start_time, end_time, node_ids, page_number, page_size",
				Returns:     "JSON with raw performance data points",
			},
			{
				Name:        "get_tests",
				Description: "List tests from Catchpoint",
				Parameters:  "test_ids, test_type, folder_id, status, page_number, page_size",
				Returns:     "JSON array of test objects",
			},
			{
				Name:        "get_test_details",
				Description: "Get detailed configuration for specific tests",
				Parameters:  "test_ids (required)",
				Returns:     "JSON test detail objects",
			},
			{
				Name:        "get_test_errors",
				Description: "Get raw test error data",
				Parameters:  "test_ids, start_time, end_time, page_number, page_size",
				Returns:     "JSON array of test error records",
			},
			{
				Name:        "get_internet_outages",
				Description: "Get internet outage data from Catchpoint Internet Weather",
				Parameters:  "start_time, end_time, asn, country, page_number, page_size",
				Returns:     "JSON array of outage objects",
			},
			{
				Name:        "get_nodes",
				Description: "List all Catchpoint monitoring nodes",
				Parameters:  "page_number, page_size",
				Returns:     "JSON array of node objects",
			},
			{
				Name:        "get_node_alerts",
				Description: "Get alerts for specific monitoring nodes",
				Parameters:  "node_ids, start_time, end_time, page_number, page_size",
				Returns:     "JSON array of node alert objects",
			},
			{
				Name:        "acknowledge_alerts",
				Description: "Acknowledge, assign, or drop test alerts",
				Parameters:  "alert_ids (required), action (required: acknowledge/assign/drop), assignee",
				Returns:     "JSON confirmation of alert action",
			},
			{
				Name:        "run_instant_test",
				Description: "Trigger an instant (on-demand) test execution",
				Parameters:  "test_id (required)",
				Returns:     "JSON with instant test execution result",
			},
		},
	}
}

func getClickHouseSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "clickhouse",
		Description: "ClickHouse OLAP database integration for read-only queries and system diagnostics. Execute SELECT queries, inspect schema, analyze running queries, merges, replication, and cluster topology.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"ch_host", "ch_database", "ch_username", "ch_password"},
			Properties: map[string]PropertySchema{
				"ch_host": {
					Type:        "string",
					Description: "ClickHouse server hostname or IP address",
					Example:     "clickhouse.example.com",
				},
				"ch_port": {
					Type:        "integer",
					Description: "ClickHouse HTTP protocol port",
					Default:     8123,
					Minimum:     intPtr(1),
					Maximum:     intPtr(65535),
				},
				"ch_database": {
					Type:        "string",
					Description: "Default database name to connect to",
					Example:     "default",
				},
				"ch_username": {
					Type:        "string",
					Description: "Database username",
				},
				"ch_password": {
					Type:        "string",
					Description: "Database password",
					Secret:      true,
				},
				"ch_ssl_enabled": {
					Type:        "boolean",
					Description: "Enable SSL/TLS for the connection",
					Default:     false,
					Advanced:    true,
				},
				"ch_timeout": {
					Type:        "integer",
					Description: "Query timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "execute_query",
				Description: "Execute a read-only SQL query (SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS only)",
				Parameters:  "query (required), limit, timeout_seconds",
				Returns:     "JSON array of row objects",
			},
			{
				Name:        "show_databases",
				Description: "List all databases on the ClickHouse server",
				Parameters:  "",
				Returns:     "JSON array of database names",
			},
			{
				Name:        "show_tables",
				Description: "List tables in a database",
				Parameters:  "database",
				Returns:     "JSON array of table names",
			},
			{
				Name:        "describe_table",
				Description: "Get column definitions and types for a table",
				Parameters:  "table_name (required), database",
				Returns:     "JSON array of column objects",
			},
			{
				Name:        "get_query_log",
				Description: "Get recent queries from system.query_log",
				Parameters:  "min_duration_ms, limit, query_kind",
				Returns:     "JSON array of query log entries",
			},
			{
				Name:        "get_running_queries",
				Description: "Get currently running queries from system.processes",
				Parameters:  "min_elapsed_seconds",
				Returns:     "JSON array of running query objects",
			},
			{
				Name:        "get_merges",
				Description: "Get active merge operations from system.merges",
				Parameters:  "table, database",
				Returns:     "JSON array of merge objects",
			},
			{
				Name:        "get_replication_status",
				Description: "Get replication queue status from system.replication_queue",
				Parameters:  "table, database",
				Returns:     "JSON array of replication queue entries",
			},
			{
				Name:        "get_parts_info",
				Description: "Get parts information from system.parts for a table",
				Parameters:  "table_name (required), database, active_only",
				Returns:     "JSON array of part objects",
			},
			{
				Name:        "get_cluster_info",
				Description: "Get cluster topology from system.clusters",
				Parameters:  "cluster",
				Returns:     "JSON array of cluster node objects",
			},
		},
	}
}

func getPostgreSQLSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "postgresql",
		Description: "PostgreSQL database integration for read-only queries and diagnostics. Execute SELECT queries, inspect schema, analyze performance, and monitor database health.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"pg_host", "pg_database", "pg_username", "pg_password"},
			Properties: map[string]PropertySchema{
				"pg_host": {
					Type:        "string",
					Description: "PostgreSQL server hostname or IP address",
					Example:     "db.example.com",
				},
				"pg_port": {
					Type:        "integer",
					Description: "PostgreSQL server port",
					Default:     5432,
					Minimum:     intPtr(1),
					Maximum:     intPtr(65535),
				},
				"pg_database": {
					Type:        "string",
					Description: "Database name to connect to",
					Example:     "myapp_production",
				},
				"pg_username": {
					Type:        "string",
					Description: "Database username",
				},
				"pg_password": {
					Type:        "string",
					Description: "Database password",
					Secret:      true,
				},
				"pg_ssl_mode": {
					Type:        "string",
					Description: "SSL connection mode",
					Enum:        []string{"disable", "require", "verify-ca", "verify-full"},
					Default:     "require",
					Advanced:    true,
				},
				"pg_timeout": {
					Type:        "integer",
					Description: "Query timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "execute_query",
				Description: "Execute a read-only SQL query (SELECT only)",
				Parameters:  "query (required), limit",
				Returns:     "JSON array of row objects",
			},
			{
				Name:        "list_tables",
				Description: "List tables in a schema with row estimates",
				Parameters:  "schema",
				Returns:     "JSON array of table objects",
			},
			{
				Name:        "describe_table",
				Description: "Get column definitions for a table",
				Parameters:  "table_name (required), schema",
				Returns:     "JSON array of column objects",
			},
			{
				Name:        "get_indexes",
				Description: "Get indexes for a table",
				Parameters:  "table_name (required), schema",
				Returns:     "JSON array of index objects",
			},
			{
				Name:        "get_table_stats",
				Description: "Get table statistics (scans, tuples, vacuum info)",
				Parameters:  "table_name, schema",
				Returns:     "JSON array of table stat objects",
			},
			{
				Name:        "explain_query",
				Description: "Get query execution plan without running the query",
				Parameters:  "query (required)",
				Returns:     "JSON execution plan",
			},
			{
				Name:        "get_active_queries",
				Description: "Get currently running queries from pg_stat_activity",
				Parameters:  "include_idle, min_duration_seconds",
				Returns:     "JSON array of active query objects",
			},
			{
				Name:        "get_locks",
				Description: "Get current lock information with blocking details",
				Parameters:  "blocked_only",
				Returns:     "JSON array of lock objects",
			},
			{
				Name:        "get_replication_status",
				Description: "Get streaming replication status and lag",
				Parameters:  "",
				Returns:     "JSON array of replication slot objects",
			},
			{
				Name:        "get_database_stats",
				Description: "Get database-level statistics and cache hit ratio",
				Parameters:  "",
				Returns:     "JSON object with database stats",
			},
		},
	}
}

func getPagerDutySchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "pagerduty",
		Description: "PagerDuty incident management integration. Query and manage incidents, services, on-call schedules, escalation policies, and send events via the PagerDuty API.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"pagerduty_api_token"},
			Properties: map[string]PropertySchema{
				"pagerduty_api_token": {
					Type:        "string",
					Description: "PagerDuty REST API token (v2)",
					Secret:      true,
				},
				"pagerduty_url": {
					Type:        "string",
					Description: "PagerDuty API base URL",
					Default:     "https://api.pagerduty.com",
				},
				"pagerduty_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
				"pagerduty_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			{
				Name:        "get_incidents",
				Description: "List incidents with filters (status, urgency, service, date range)",
				Parameters:  "statuses, urgencies, service_ids, since, until, sort_by, limit, offset",
				Returns:     "JSON array of incident objects",
			},
			{
				Name:        "get_incident",
				Description: "Get incident details by ID",
				Parameters:  "incident_id (required)",
				Returns:     "JSON incident object with full details",
			},
			{
				Name:        "get_incident_notes",
				Description: "Get notes/timeline for an incident",
				Parameters:  "incident_id (required)",
				Returns:     "JSON array of note objects",
			},
			{
				Name:        "get_incident_alerts",
				Description: "Get alerts grouped under an incident",
				Parameters:  "incident_id (required)",
				Returns:     "JSON array of alert objects",
			},
			{
				Name:        "get_services",
				Description: "List services",
				Parameters:  "query, limit, offset",
				Returns:     "JSON array of service objects",
			},
			{
				Name:        "get_on_calls",
				Description: "Get current on-call users by schedule or escalation policy",
				Parameters:  "schedule_ids, escalation_policy_ids, since, until",
				Returns:     "JSON array of on-call objects",
			},
			{
				Name:        "get_escalation_policies",
				Description: "List escalation policies",
				Parameters:  "query, limit, offset",
				Returns:     "JSON array of escalation policy objects",
			},
			{
				Name:        "list_recent_changes",
				Description: "List recent changes across services",
				Parameters:  "since, until, limit, offset",
				Returns:     "JSON array of recent change objects",
			},
			{
				Name:        "acknowledge_incident",
				Description: "Acknowledge an incident",
				Parameters:  "incident_id (required), requester_email (required)",
				Returns:     "JSON updated incident object",
			},
			{
				Name:        "resolve_incident",
				Description: "Resolve an incident",
				Parameters:  "incident_id (required), requester_email (required)",
				Returns:     "JSON updated incident object",
			},
			{
				Name:        "reassign_incident",
				Description: "Reassign an incident to a different user or escalation policy",
				Parameters:  "incident_id (required), requester_email (required), assignee_ids, escalation_policy_id",
				Returns:     "JSON updated incident object",
			},
			{
				Name:        "add_incident_note",
				Description: "Add a note to an incident",
				Parameters:  "incident_id (required), requester_email (required), content (required)",
				Returns:     "JSON note object",
			},
			{
				Name:        "send_event",
				Description: "Send trigger/acknowledge/resolve events via Events API v2",
				Parameters:  "routing_key (required), event_action (required: trigger/acknowledge/resolve), dedup_key, summary, source, severity, component, group, class, custom_details",
				Returns:     "JSON with status, message, and dedup_key",
			},
		},
	}
}

func getK8sSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "kubernetes",
		Description: "Read-only Kubernetes cluster diagnostics. Query pods, nodes, deployments, services, events, and logs for incident investigation without making any mutations.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"k8s_url", "k8s_token"},
			Properties: map[string]PropertySchema{
				"k8s_url": {
					Type:        "string",
					Description: "Kubernetes API server URL (e.g. https://k8s.example.com:6443)",
				},
				"k8s_token": {
					Type:        "string",
					Description: "Kubernetes Bearer token for authentication (service account token)",
					Secret:      true,
				},
				"k8s_ca_cert": {
					Type:        "string",
					Description: "PEM-encoded CA certificate for the Kubernetes API server",
					Format:      "textarea",
					Advanced:    true,
				},
				"k8s_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates when connecting to the Kubernetes API",
					Default:     true,
					Advanced:    true,
				},
				"k8s_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			// Namespaces
			{
				Name:        "get_namespaces",
				Description: "List all namespaces in the cluster",
				Parameters:  "label_selector, field_selector, limit",
				Returns:     "Kubernetes NamespaceList object with items array",
			},
			// Pods
			{
				Name:        "get_pods",
				Description: "List pods in a namespace with optional filters. When 'name' is provided, returns the single pod detail instead of a list.",
				Parameters:  "namespace (required), name, label_selector, field_selector, limit",
				Returns:     "Kubernetes PodList object with items array, or a single pod detail object when name is provided",
			},
			{
				Name:        "get_pod_detail",
				Description: "Get detailed information about a specific pod including status, containers, and conditions",
				Parameters:  "namespace (required), name (required)",
				Returns:     "JSON pod object with full details",
			},
			{
				Name:        "get_pod_logs",
				Description: "Get logs from a pod's container",
				Parameters:  "namespace (required), name (required), container, tail_lines (default 100), since_seconds, previous",
				Returns:     "Plain text log output",
			},
			// Events
			{
				Name:        "get_events",
				Description: "List events in a namespace (warnings, errors, scheduling events)",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes EventList object with items array",
			},
			// Deployments
			{
				Name:        "get_deployments",
				Description: "List deployments in a namespace with optional filters. When 'name' is provided, returns the single deployment detail instead of a list.",
				Parameters:  "namespace (required), name, label_selector, field_selector, limit",
				Returns:     "Kubernetes DeploymentList object with items array, or a single deployment detail object when name is provided",
			},
			{
				Name:        "get_deployment_detail",
				Description: "Get detailed information about a specific deployment including replicas, conditions, and strategy",
				Parameters:  "namespace (required), name (required)",
				Returns:     "JSON deployment object with full details",
			},
			// StatefulSets
			{
				Name:        "get_statefulsets",
				Description: "List statefulsets in a namespace with optional filters",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes StatefulSetList object with items array",
			},
			// DaemonSets
			{
				Name:        "get_daemonsets",
				Description: "List daemonsets in a namespace with optional filters",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes DaemonSetList object with items array",
			},
			// Jobs
			{
				Name:        "get_jobs",
				Description: "List jobs in a namespace with optional filters",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes JobList object with items array",
			},
			// CronJobs
			{
				Name:        "get_cronjobs",
				Description: "List cronjobs in a namespace with optional filters",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes CronJobList object with items array",
			},
			// Nodes
			{
				Name:        "get_nodes",
				Description: "List nodes with conditions and allocatable resources",
				Parameters:  "label_selector, field_selector, limit",
				Returns:     "Kubernetes NodeList object with items array",
			},
			{
				Name:        "get_node_detail",
				Description: "Get detailed information about a specific node including conditions, capacity, and taints",
				Parameters:  "name (required)",
				Returns:     "JSON node object with full details",
			},
			// Services
			{
				Name:        "get_services",
				Description: "List services in a namespace",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes ServiceList object with items array",
			},
			// ConfigMaps
			{
				Name:        "get_configmaps",
				Description: "List configmaps in a namespace (names and metadata only, not data contents)",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes ConfigMapList object with items array (data/binaryData fields stripped)",
			},
			// Ingresses
			{
				Name:        "get_ingresses",
				Description: "List ingresses in a namespace",
				Parameters:  "namespace (required), label_selector, field_selector, limit",
				Returns:     "Kubernetes IngressList object with items array",
			},
			// Generic
			{
				Name:        "api_request",
				Description: "Generic read-only GET request to any Kubernetes API endpoint",
				Parameters:  "path (required, must start with /api or /apis), params (optional query parameters)",
				Returns:     "JSON response from the Kubernetes API",
			},
		},
	}
}

func getNetBoxSchema() ToolTypeSchema {
	return ToolTypeSchema{
		Name:        "netbox",
		Description: "NetBox CMDB integration. Read-only access to DCIM (devices, sites, racks, interfaces, cables), IPAM (IPs, prefixes, VLANs, VRFs), Circuits, Virtualization (VMs, clusters), and Tenancy data for infrastructure context during incident investigation.",
		Version:     "1.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"netbox_url", "netbox_api_token"},
			Properties: map[string]PropertySchema{
				"netbox_url": {
					Type:        "string",
					Description: "NetBox instance URL (e.g. https://netbox.example.com)",
				},
				"netbox_api_token": {
					Type:        "string",
					Description: "NetBox API token for authentication",
					Secret:      true,
				},
				"netbox_verify_ssl": {
					Type:        "boolean",
					Description: "Verify SSL certificates",
					Default:     true,
					Advanced:    true,
				},
				"netbox_timeout": {
					Type:        "integer",
					Description: "API request timeout in seconds",
					Default:     30,
					Minimum:     intPtr(5),
					Maximum:     intPtr(300),
					Advanced:    true,
				},
			},
		},
		Functions: []ToolFunction{
			// DCIM
			{
				Name:        "get_devices",
				Description: "List/search devices with filters",
				Parameters:  "name, site, role, status, tag, platform, tenant, q, limit, offset",
				Returns:     "JSON array of device objects",
			},
			{
				Name:        "get_device",
				Description: "Get device details by ID",
				Parameters:  "id (required)",
				Returns:     "JSON device object with full details",
			},
			{
				Name:        "get_interfaces",
				Description: "List device interfaces with filters",
				Parameters:  "device, device_id, name, type, enabled, limit, offset",
				Returns:     "JSON array of interface objects",
			},
			{
				Name:        "get_sites",
				Description: "List sites with filters",
				Parameters:  "name, region, status, tag, tenant, q, limit, offset",
				Returns:     "JSON array of site objects",
			},
			{
				Name:        "get_racks",
				Description: "List racks with filters",
				Parameters:  "site, name, status, role, tenant, q, limit, offset",
				Returns:     "JSON array of rack objects",
			},
			{
				Name:        "get_cables",
				Description: "List cable connections with filters",
				Parameters:  "device, site, type, status, limit, offset",
				Returns:     "JSON array of cable objects",
			},
			{
				Name:        "get_device_types",
				Description: "List device types/models with filters",
				Parameters:  "manufacturer, model, q, limit, offset",
				Returns:     "JSON array of device type objects",
			},
			// IPAM
			{
				Name:        "get_ip_addresses",
				Description: "List/search IP addresses with filters",
				Parameters:  "address, device, interface, vrf, tenant, status, q, limit, offset",
				Returns:     "JSON array of IP address objects",
			},
			{
				Name:        "get_prefixes",
				Description: "List IP prefixes/subnets with filters",
				Parameters:  "prefix, site, vrf, vlan, tenant, status, q, limit, offset",
				Returns:     "JSON array of prefix objects",
			},
			{
				Name:        "get_vlans",
				Description: "List VLANs with filters",
				Parameters:  "vid, name, site, group, tenant, q, limit, offset",
				Returns:     "JSON array of VLAN objects",
			},
			{
				Name:        "get_vrfs",
				Description: "List VRFs with filters",
				Parameters:  "name, tenant, q, limit, offset",
				Returns:     "JSON array of VRF objects",
			},
			// Circuits
			{
				Name:        "get_circuits",
				Description: "List circuits with filters",
				Parameters:  "provider, type, status, tenant, q, limit, offset",
				Returns:     "JSON array of circuit objects",
			},
			{
				Name:        "get_providers",
				Description: "List circuit providers with filters",
				Parameters:  "name, q, limit, offset",
				Returns:     "JSON array of provider objects",
			},
			// Virtualization
			{
				Name:        "get_virtual_machines",
				Description: "List virtual machines with filters",
				Parameters:  "name, cluster, site, status, role, tenant, q, limit, offset",
				Returns:     "JSON array of virtual machine objects",
			},
			{
				Name:        "get_clusters",
				Description: "List clusters with filters",
				Parameters:  "name, type, group, site, tenant, q, limit, offset",
				Returns:     "JSON array of cluster objects",
			},
			{
				Name:        "get_vm_interfaces",
				Description: "List VM interfaces with filters",
				Parameters:  "virtual_machine, name, enabled, limit, offset",
				Returns:     "JSON array of VM interface objects",
			},
			// Tenancy
			{
				Name:        "get_tenants",
				Description: "List tenants with filters",
				Parameters:  "name, group, q, limit, offset",
				Returns:     "JSON array of tenant objects",
			},
			{
				Name:        "get_tenant_groups",
				Description: "List tenant groups with filters",
				Parameters:  "name, q, limit, offset",
				Returns:     "JSON array of tenant group objects",
			},
			// Generic
			{
				Name:        "api_request",
				Description: "Generic read-only API request to any NetBox endpoint",
				Parameters:  "path (required), query_params, limit, offset",
				Returns:     "JSON response from the NetBox API",
			},
		},
	}
}
