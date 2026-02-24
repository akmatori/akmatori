package tools

import (
	"context"
	"log"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/tools/ssh"
	"github.com/akmatori/mcp-gateway/internal/tools/zabbix"
)

// Rate limit configuration for Zabbix API
const (
	ZabbixRatePerSecond = 10 // requests per second
	ZabbixBurstCapacity = 20 // burst capacity
)

// Registry manages tool registration
type Registry struct {
	server      *mcp.Server
	logger      *log.Logger
	zabbixTool  *zabbix.ZabbixTool
	zabbixLimit *ratelimit.Limiter
}

// NewRegistry creates a new tool registry
func NewRegistry(server *mcp.Server, logger *log.Logger) *Registry {
	return &Registry{
		server: server,
		logger: logger,
	}
}

// RegisterAllTools registers all available tools
func (r *Registry) RegisterAllTools() {
	r.logger.Println("Registering tools...")

	// Create rate limiter for Zabbix: 10 req/sec, burst 20
	r.zabbixLimit = ratelimit.New(ZabbixRatePerSecond, ZabbixBurstCapacity)
	r.logger.Printf("Zabbix rate limiter created: %d req/sec, burst %d", ZabbixRatePerSecond, ZabbixBurstCapacity)

	// Register SSH tools
	r.registerSSHTools()

	// Register Zabbix tools with rate limiter
	r.registerZabbixTools()

	r.logger.Println("All tools registered")
}

// Stop cleans up resources
func (r *Registry) Stop() {
	if r.zabbixTool != nil {
		r.zabbixTool.Stop()
	}
}

// extractInstanceID extracts the optional tool_instance_id from tool arguments.
func extractInstanceID(args map[string]interface{}) *uint {
	if v, ok := args["tool_instance_id"].(float64); ok && v > 0 {
		id := uint(v)
		return &id
	}
	return nil
}

// toolInstanceIDProperty is the shared schema property for tool_instance_id
var toolInstanceIDProperty = mcp.Property{
	Type:        "integer",
	Description: "Optional tool instance ID for routing to a specific configured instance. Provided in SKILL.md when multiple instances of this tool type exist.",
}

// registerSSHTools registers SSH-related tools
func (r *Registry) registerSSHTools() {
	sshTool := ssh.NewSSHTool(r.logger)

	// ssh.execute_command
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "ssh.execute_command",
			Description: "Execute a shell command on configured SSH servers in parallel",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"command": {
						Type:        "string",
						Description: "The shell command to execute on remote servers",
					},
					"servers": {
						Type:        "array",
						Description: "Optional list of specific servers to target (defaults to all configured servers)",
						Items:       &mcp.Items{Type: "string"},
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"command"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			instanceID := extractInstanceID(args)
			command, _ := args["command"].(string)
			var servers []string
			if serversArg, ok := args["servers"].([]interface{}); ok {
				for _, s := range serversArg {
					if str, ok := s.(string); ok {
						servers = append(servers, str)
					}
				}
			}
			return sshTool.ExecuteCommand(ctx, incidentID, command, servers, instanceID)
		},
	)

	// ssh.test_connectivity
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "ssh.test_connectivity",
			Description: "Test SSH connectivity to all configured servers",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			instanceID := extractInstanceID(args)
			return sshTool.TestConnectivity(ctx, incidentID, instanceID)
		},
	)

	// ssh.get_server_info
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "ssh.get_server_info",
			Description: "Get basic system information (hostname, OS, uptime) from specified servers",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"servers": {
						Type:        "array",
						Description: "List of server hostnames/IPs to query (optional, defaults to all)",
						Items:       &mcp.Items{Type: "string"},
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			instanceID := extractInstanceID(args)
			var servers []string
			if serversArg, ok := args["servers"].([]interface{}); ok {
				for _, s := range serversArg {
					if str, ok := s.(string); ok {
						servers = append(servers, str)
					}
				}
			}
			return sshTool.GetServerInfo(ctx, incidentID, servers, instanceID)
		},
	)
}

// registerZabbixTools registers Zabbix-related tools
func (r *Registry) registerZabbixTools() {
	r.zabbixTool = zabbix.NewZabbixTool(r.logger, r.zabbixLimit)

	// zabbix.get_hosts
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_hosts",
			Description: "Get hosts from Zabbix monitoring system",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"output": {
						Type:        "string",
						Description: "Output fields. Server defaults to [hostid, host, name, status, available] if omitted.",
					},
					"filter": {
						Type:        "object",
						Description: "Exact-match filter (e.g., {\"host\": [\"server1\", \"server2\"]}). Prefer over search when exact hostnames are known.",
					},
					"search": {
						Type:        "object",
						Description: "Substring/prefix search conditions (e.g., {\"name\": \"web\"})",
					},
					"start_search": {
						Type:        "boolean",
						Description: "When true, search matches from the beginning of fields only (prefix match). Faster on large Zabbix databases.",
						Default:     true,
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of hosts to return",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetHosts(ctx, incidentID, args)
		},
	)

	// zabbix.get_problems
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_problems",
			Description: "Get current problems/alerts from Zabbix",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"recent": {
						Type:        "boolean",
						Description: "Only return recent problems",
						Default:     true,
					},
					"severity_min": {
						Type:        "integer",
						Description: "Minimum severity level (0-5, where 5 is disaster)",
						Default:     0,
					},
					"hostids": {
						Type:        "array",
						Description: "Filter by host IDs",
						Items:       &mcp.Items{Type: "string"},
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of problems to return",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetProblems(ctx, incidentID, args)
		},
	)

	// zabbix.get_history
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_history",
			Description: "Get metric history data from Zabbix",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"itemids": {
						Type:        "array",
						Description: "Item IDs to get history for",
						Items:       &mcp.Items{Type: "string"},
					},
					"history": {
						Type:        "integer",
						Description: "History type: 0=float, 1=string, 2=log, 3=uint, 4=text",
						Default:     0,
					},
					"time_from": {
						Type:        "integer",
						Description: "Start timestamp (Unix epoch)",
					},
					"time_till": {
						Type:        "integer",
						Description: "End timestamp (Unix epoch)",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of records to return",
					},
					"sortfield": {
						Type:        "string",
						Description: "Field to sort by (clock)",
						Default:     "clock",
					},
					"sortorder": {
						Type:        "string",
						Description: "Sort order: ASC or DESC",
						Default:     "DESC",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"itemids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetHistory(ctx, incidentID, args)
		},
	)

	// zabbix.get_items
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_items",
			Description: "Get items (metrics) from Zabbix",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"hostids": {
						Type:        "array",
						Description: "Filter by host IDs",
						Items:       &mcp.Items{Type: "string"},
					},
					"filter": {
						Type:        "object",
						Description: "Exact-match filter (e.g., {\"key_\": \"system.cpu.util\"}). Prefer over search for exact key matches.",
					},
					"search": {
						Type:        "object",
						Description: "Substring/prefix search conditions (e.g., {\"key_\": \"cpu\"})",
					},
					"start_search": {
						Type:        "boolean",
						Description: "When true, search matches from the beginning of fields only (prefix match). Faster on large Zabbix databases.",
						Default:     true,
					},
					"output": {
						Type:        "string",
						Description: "Output fields. Server defaults to [itemid, hostid, name, key_, value_type, lastvalue, units, state, status] if omitted.",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of items to return",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetItems(ctx, incidentID, args)
		},
	)

	// zabbix.get_items_batch - Batch item search with deduplication
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_items_batch",
			Description: "Get multiple items in a single request with deduplication. More efficient than multiple get_items calls.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"hostids": {
						Type:        "array",
						Description: "Filter by host IDs",
						Items:       &mcp.Items{Type: "string"},
					},
					"searches": {
						Type:        "array",
						Description: "List of search patterns to find items for (e.g., [\"cpu\", \"memory\", \"disk\"])",
						Items:       &mcp.Items{Type: "string"},
					},
					"start_search": {
						Type:        "boolean",
						Description: "When true, search matches from the beginning of key_ only (prefix match). Faster on large Zabbix databases.",
						Default:     true,
					},
					"output": {
						Type:        "string",
						Description: "Output fields. Server defaults to [itemid, hostid, name, key_, value_type, lastvalue, units] if omitted.",
					},
					"limit_per_search": {
						Type:        "integer",
						Description: "Maximum items per search pattern",
						Default:     10,
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"searches"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetItemsBatch(ctx, incidentID, args)
		},
	)

	// zabbix.get_triggers
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.get_triggers",
			Description: "Get triggers from Zabbix",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"hostids": {
						Type:        "array",
						Description: "Filter by host IDs",
						Items:       &mcp.Items{Type: "string"},
					},
					"only_true": {
						Type:        "boolean",
						Description: "Return only triggers in problem state",
						Default:     false,
					},
					"min_severity": {
						Type:        "integer",
						Description: "Minimum severity level",
						Default:     0,
					},
					"output": {
						Type:        "string",
						Description: "Output fields. Server defaults to [triggerid, description, priority, status, value, state] if omitted.",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.zabbixTool.GetTriggers(ctx, incidentID, args)
		},
	)

	// zabbix.api_request
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "zabbix.api_request",
			Description: "Make a raw Zabbix API request",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"method": {
						Type:        "string",
						Description: "Zabbix API method (e.g., 'host.get', 'item.get')",
					},
					"params": {
						Type:        "object",
						Description: "Parameters for the API method",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"method"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			instanceID := extractInstanceID(args)
			method, _ := args["method"].(string)
			params, _ := args["params"].(map[string]interface{})
			return r.zabbixTool.APIRequest(ctx, incidentID, method, params, instanceID)
		},
	)
}

// GetToolCredentials is a helper to fetch credentials from database
func GetToolCredentials(ctx context.Context, incidentID string, toolType string) (*database.ToolCredentials, error) {
	return database.GetToolCredentialsForIncident(ctx, incidentID, toolType)
}
