package tools

// ToolTypeSchema defines the configuration schema for a tool type
type ToolTypeSchema struct {
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Version        string                 `json:"version"`
	SettingsSchema SettingsSchema         `json:"settings_schema"`
	Functions      []ToolFunction         `json:"functions"`
}

// SettingsSchema defines the JSON schema for tool settings
type SettingsSchema struct {
	Type       string                    `json:"type"`
	Required   []string                  `json:"required,omitempty"`
	Properties map[string]PropertySchema `json:"properties"`
}

// PropertySchema defines a single property in the settings schema
type PropertySchema struct {
	Type        string                    `json:"type"`
	Description string                    `json:"description,omitempty"`
	Default     interface{}               `json:"default,omitempty"`
	Secret      bool                      `json:"secret,omitempty"`
	Format      string                    `json:"format,omitempty"`
	Advanced    bool                      `json:"advanced,omitempty"`
	Warning     string                    `json:"warning,omitempty"`
	Enum        []string                  `json:"enum,omitempty"`
	Minimum     *int                      `json:"minimum,omitempty"`
	Maximum     *int                      `json:"maximum,omitempty"`
	MinItems    *int                      `json:"minItems,omitempty"`
	Example     interface{}               `json:"example,omitempty"`
	Items       *ItemSchema               `json:"items,omitempty"`
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
		"ssh":    getSSHSchema(),
		"zabbix": getZabbixSchema(),
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
		Version:     "2.0.0",
		SettingsSchema: SettingsSchema{
			Type:     "object",
			Required: []string{"ssh_private_key", "ssh_hosts"},
			Properties: map[string]PropertySchema{
				"ssh_private_key": {
					Type:        "string",
					Description: "SSH private key content (PEM format, shared across all hosts)",
					Secret:      true,
					Format:      "textarea",
				},
				"ssh_hosts": {
					Type:        "array",
					Description: "List of SSH host configurations",
					MinItems:    intPtr(1),
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
				Description: "Test SSH connectivity to all configured servers (including through jumphosts if configured)",
				Parameters:  "None",
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
