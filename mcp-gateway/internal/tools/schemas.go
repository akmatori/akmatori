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
		"catchpoint":       getCatchpointSchema(),
		"postgresql":       getPostgreSQLSchema(),
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
				Parameters:  "table_name",
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
