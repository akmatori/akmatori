package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/mcpproxy"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/tools/httpconnector"
	"github.com/akmatori/mcp-gateway/internal/tools/ssh"
	"github.com/akmatori/mcp-gateway/internal/tools/victoriametrics"
	"github.com/akmatori/mcp-gateway/internal/tools/zabbix"
)

// Rate limit configuration
const (
	ZabbixRatePerSecond = 10 // requests per second
	ZabbixBurstCapacity = 20 // burst capacity
	VMRatePerSecond     = 10 // requests per second
	VMBurstCapacity     = 20 // burst capacity
)

// Registry manages tool registration
type Registry struct {
	server      *mcp.Server
	logger      *log.Logger
	zabbixTool  *zabbix.ZabbixTool
	zabbixLimit *ratelimit.Limiter
	vmTool      *victoriametrics.VictoriaMetricsTool
	vmLimit     *ratelimit.Limiter

	// HTTP connector state
	httpExecutor      *httpconnector.HTTPConnectorExecutor
	httpConnectorTools []string // track registered tool names for reload cleanup
	httpMu            sync.Mutex

	// MCP proxy state
	proxyHandler   *mcpproxy.ProxyHandler
	proxyToolNames []string // track registered proxy tool names for reload cleanup
	proxyMu        sync.Mutex
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

	// Create rate limiter for VictoriaMetrics: 10 req/sec, burst 20
	r.vmLimit = ratelimit.New(VMRatePerSecond, VMBurstCapacity)
	r.logger.Printf("VictoriaMetrics rate limiter created: %d req/sec, burst %d", VMRatePerSecond, VMBurstCapacity)

	// Register VictoriaMetrics tools with rate limiter
	r.registerVictoriaMetricsTools()

	r.logger.Println("All tools registered")
}

// Stop cleans up resources
func (r *Registry) Stop() {
	if r.zabbixTool != nil {
		r.zabbixTool.Stop()
	}
	if r.vmTool != nil {
		r.vmTool.Stop()
	}
	if r.httpExecutor != nil {
		r.httpExecutor.Stop()
	}
	if r.proxyHandler != nil {
		r.proxyHandler.Stop()
	}
}

// DefaultMCPProxyLoader loads MCP server configs from the database and converts them
// to proxy handler registrations.
func DefaultMCPProxyLoader(ctx context.Context) ([]mcpproxy.ServerRegistration, error) {
	configs, err := database.GetAllEnabledMCPServerConfigs(ctx)
	if err != nil {
		return nil, err
	}

	var regs []mcpproxy.ServerRegistration
	for _, cfg := range configs {
		// Convert database Args JSONB to string slice
		var args []string
		if cfg.Args != nil {
			if argsRaw, ok := cfg.Args["args"]; ok {
				if argsJSON, err := json.Marshal(argsRaw); err == nil {
					if err := json.Unmarshal(argsJSON, &args); err != nil {
						slog.Warn("failed to parse MCP server args", "config_id", cfg.ID, "error", err)
					}
				}
			}
		}

		// Convert database EnvVars JSONB to string map
		envVars := make(map[string]string)
		for k, v := range cfg.EnvVars {
			if s, ok := v.(string); ok {
				envVars[k] = s
			}
		}

		var authConfig json.RawMessage
		if cfg.AuthConfig != nil {
			authConfig, _ = json.Marshal(cfg.AuthConfig)
		}

		regs = append(regs, mcpproxy.ServerRegistration{
			InstanceID:      cfg.ID,
			NamespacePrefix: cfg.NamespacePrefix,
			AuthConfig:      authConfig,
			Config: mcpproxy.MCPServerConfig{
				Transport:       mcpproxy.TransportType(cfg.Transport),
				URL:             cfg.URL,
				Command:         cfg.Command,
				Args:            args,
				EnvVars:         envVars,
				NamespacePrefix: cfg.NamespacePrefix,
				AuthConfig:      authConfig,
			},
		})
	}

	return regs, nil
}

// HTTPConnectorLoader is a function that loads enabled HTTP connectors from the database.
// This abstraction allows tests to provide mock loaders without needing a real database.
type HTTPConnectorLoader func(ctx context.Context) ([]database.HTTPConnector, error)

// DefaultHTTPConnectorLoader loads connectors from the database.
func DefaultHTTPConnectorLoader(ctx context.Context) ([]database.HTTPConnector, error) {
	return database.GetAllEnabledHTTPConnectors(ctx)
}

// RegisterHTTPConnectors queries the database for enabled HTTP connector definitions
// and registers their tools in the gateway registry.
func (r *Registry) RegisterHTTPConnectors(loader HTTPConnectorLoader) {
	r.httpMu.Lock()
	defer r.httpMu.Unlock()

	if r.httpExecutor == nil {
		r.httpExecutor = httpconnector.New()
	}

	ctx := context.Background()
	connectors, err := loader(ctx)
	if err != nil {
		r.logger.Printf("Failed to load HTTP connectors: %v", err)
		return
	}

	registered := 0
	for _, conn := range connectors {
		n := r.registerHTTPConnectorTools(conn)
		registered += n
	}

	r.logger.Printf("Registered %d HTTP connector tools from %d connectors", registered, len(connectors))
}

// ReloadHTTPConnectors unregisters all previously registered HTTP connector tools
// and re-registers them from the database. Call this after connector CRUD operations.
func (r *Registry) ReloadHTTPConnectors(loader HTTPConnectorLoader) {
	r.httpMu.Lock()
	defer r.httpMu.Unlock()

	// Unregister all previously registered HTTP connector tools
	for _, name := range r.httpConnectorTools {
		r.server.UnregisterTool(name)
	}
	r.httpConnectorTools = nil

	if r.httpExecutor == nil {
		r.httpExecutor = httpconnector.New()
	}

	ctx := context.Background()
	connectors, err := loader(ctx)
	if err != nil {
		r.logger.Printf("Failed to reload HTTP connectors: %v", err)
		return
	}

	registered := 0
	for _, conn := range connectors {
		n := r.registerHTTPConnectorTools(conn)
		registered += n
	}

	r.logger.Printf("Reloaded %d HTTP connector tools from %d connectors", registered, len(connectors))
}

// SetProxyHandler sets the MCP proxy handler for this registry.
func (r *Registry) SetProxyHandler(h *mcpproxy.ProxyHandler) {
	r.proxyHandler = h
	// When the proxy handler's schema refresh discovers new/removed tools,
	// re-register them in the MCP server so they become callable.
	h.SetOnToolsChanged(func() {
		r.proxyMu.Lock()
		defer r.proxyMu.Unlock()
		// Unregister old proxy tools
		for _, name := range r.proxyToolNames {
			r.server.UnregisterTool(name)
		}
		r.proxyToolNames = nil
		r.registerProxyToolsFromHandler()
	})
}

// RegisterMCPProxyTools loads MCP server registrations and registers their discovered
// tools in the gateway. Each tool is namespaced with the server's prefix.
func (r *Registry) RegisterMCPProxyTools(loader mcpproxy.MCPServerConfigLoader) {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()

	if r.proxyHandler == nil {
		r.logger.Println("MCP proxy handler not configured, skipping proxy tool registration")
		return
	}

	ctx := context.Background()
	if err := r.proxyHandler.LoadAndRegister(ctx, loader); err != nil {
		r.logger.Printf("Failed to load MCP proxy tools: %v", err)
		return
	}

	r.registerProxyToolsFromHandler()
}

// ReloadMCPProxyTools unregisters all proxy tools and re-registers from the database.
func (r *Registry) ReloadMCPProxyTools(loader mcpproxy.MCPServerConfigLoader) {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()

	// Unregister previously registered proxy tools
	for _, name := range r.proxyToolNames {
		r.server.UnregisterTool(name)
	}
	r.proxyToolNames = nil

	if r.proxyHandler == nil {
		return
	}

	ctx := context.Background()
	if err := r.proxyHandler.Reload(ctx, loader); err != nil {
		r.logger.Printf("Failed to reload MCP proxy tools: %v", err)
		return
	}

	r.registerProxyToolsFromHandler()
}

// registerProxyToolsFromHandler registers all tools from the proxy handler in the MCP server.
func (r *Registry) registerProxyToolsFromHandler() {
	tools := r.proxyHandler.GetTools()
	handler := r.proxyHandler

	for _, tool := range tools {
		toolName := tool.Name
		r.server.RegisterTool(tool, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			result, err := handler.CallTool(ctx, toolName, args)
			if err != nil {
				return nil, err
			}
			// Return the call result content as JSON
			if len(result.Content) == 1 {
				return result.Content[0].Text, nil
			}
			return result, nil
		})
		r.proxyToolNames = append(r.proxyToolNames, tool.Name)
	}

	r.logger.Printf("Registered %d MCP proxy tools", len(tools))
}

// registerHTTPConnectorTools registers tools for a single HTTP connector.
// Returns the number of tools registered.
func (r *Registry) registerHTTPConnectorTools(conn database.HTTPConnector) int {
	// Parse tool definitions from JSONB
	toolDefs, err := parseHTTPConnectorToolDefs(conn.Tools)
	if err != nil {
		r.logger.Printf("Failed to parse tools for connector %q: %v", conn.ToolTypeName, err)
		return 0
	}

	// Parse auth config
	authConfig := parseHTTPConnectorAuthConfig(conn.AuthConfig)

	count := 0
	for _, toolDef := range toolDefs {
		fullName := conn.ToolTypeName + "." + toolDef.Name

		// Validate: read-only tools (default) must use GET. Reject at registration
		// rather than failing every call at execution time.
		isReadOnly := toolDef.ReadOnly == nil || *toolDef.ReadOnly
		if isReadOnly && toolDef.HTTPMethod != "GET" {
			r.logger.Printf("Skipping tool %q: read_only (default) but uses HTTP method %s; set read_only to false to allow writes",
				fullName, toolDef.HTTPMethod)
			continue
		}

		description := toolDef.Description
		if description == "" {
			description = fmt.Sprintf("%s %s %s", toolDef.HTTPMethod, toolDef.Path, conn.ToolTypeName)
		}

		// Build input schema from tool params
		inputSchema := buildHTTPConnectorInputSchema(toolDef)

		// Build the connector definition for the executor
		connectorDef := httpconnector.ConnectorDef{
			ToolTypeName: conn.ToolTypeName,
			AuthConfig:   authConfig,
			Tools:        []httpconnector.ToolDef{convertToolDef(toolDef)},
		}

		baseURLField := conn.BaseURLField
		toolName := toolDef.Name
		executor := r.httpExecutor

		r.server.RegisterTool(
			mcp.Tool{
				Name:        fullName,
				Description: description,
				InputSchema: inputSchema,
			},
			func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
				instanceID := extractInstanceID(args)
				logicalName := extractLogicalName(args)

				// Resolve credentials for this connector's tool type
				creds, err := database.ResolveToolCredentials(ctx, incidentID, connectorDef.ToolTypeName, instanceID, logicalName)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve credentials for %s: %w", connectorDef.ToolTypeName, err)
				}

				// Set base URL from instance settings
				def := connectorDef
				if baseURL, ok := creds.Settings[baseURLField].(string); ok {
					def.BaseURL = baseURL
				} else {
					return nil, fmt.Errorf("base URL field %q not found in instance settings", baseURLField)
				}

				// Convert settings to credentials map
				credMap := httpconnector.Credentials{}
				for k, v := range creds.Settings {
					if s, ok := v.(string); ok {
						credMap[k] = s
					}
				}

				return executor.Execute(ctx, def, toolName, args, credMap)
			},
		)

		r.httpConnectorTools = append(r.httpConnectorTools, fullName)
		count++
	}

	return count
}

// parseHTTPConnectorToolDefs parses the JSONB tools field into typed definitions
func parseHTTPConnectorToolDefs(tools database.JSONB) ([]httpConnectorToolDef, error) {
	if tools == nil {
		return nil, nil
	}

	toolsRaw, ok := tools["tools"]
	if !ok {
		return nil, nil
	}

	// Marshal and unmarshal for reliable type conversion
	data, err := json.Marshal(toolsRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tools: %w", err)
	}

	var defs []httpConnectorToolDef
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools: %w", err)
	}

	return defs, nil
}

// httpConnectorToolDef mirrors the main app's HTTPConnectorToolDef for JSON parsing
type httpConnectorToolDef struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	HTTPMethod  string                    `json:"http_method"`
	Path        string                    `json:"path"`
	Params      []httpConnectorToolParam  `json:"params,omitempty"`
	ReadOnly    *bool                     `json:"read_only,omitempty"`
}

type httpConnectorToolParam struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	Required bool        `json:"required"`
	In       string      `json:"in"`
	Default  interface{} `json:"default,omitempty"`
}

// parseHTTPConnectorAuthConfig parses the JSONB auth config
func parseHTTPConnectorAuthConfig(authCfg database.JSONB) *httpconnector.AuthConfig {
	if authCfg == nil {
		return nil
	}

	method, _ := authCfg["method"].(string)
	if method == "" {
		return nil
	}

	config := &httpconnector.AuthConfig{
		Method: httpconnector.AuthMethod(method),
	}
	if v, ok := authCfg["token_field"].(string); ok {
		config.TokenField = v
	}
	if v, ok := authCfg["header_name"].(string); ok {
		config.HeaderName = v
	}
	return config
}

// buildHTTPConnectorInputSchema creates an MCP input schema from HTTP connector tool params
func buildHTTPConnectorInputSchema(toolDef httpConnectorToolDef) mcp.InputSchema {
	properties := map[string]mcp.Property{
		"tool_instance_id": toolInstanceIDProperty,
	}
	var required []string

	for _, param := range toolDef.Params {
		prop := mcp.Property{
			Type:        param.Type,
			Description: fmt.Sprintf("Parameter (%s)", param.In),
		}
		if param.Default != nil {
			prop.Default = param.Default
		}
		properties[param.Name] = prop
		if param.Required {
			required = append(required, param.Name)
		}
	}

	return mcp.InputSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}

// convertToolDef converts the parsed tool def to the executor's ToolDef type
func convertToolDef(def httpConnectorToolDef) httpconnector.ToolDef {
	var params []httpconnector.ToolParam
	for _, p := range def.Params {
		params = append(params, httpconnector.ToolParam{
			Name:     p.Name,
			Type:     p.Type,
			Required: p.Required,
			In:       p.In,
			Default:  p.Default,
		})
	}
	return httpconnector.ToolDef{
		Name:       def.Name,
		Description: def.Description,
		HTTPMethod: def.HTTPMethod,
		Path:       def.Path,
		Params:     params,
		ReadOnly:   def.ReadOnly,
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

// extractLogicalName extracts the optional logical_name from tool arguments.
func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

// extractServers extracts the optional servers string list from tool arguments.
func extractServers(args map[string]interface{}) []string {
	serversArg, ok := args["servers"].([]interface{})
	if !ok {
		return nil
	}
	var servers []string
	for _, s := range serversArg {
		if str, ok := s.(string); ok {
			servers = append(servers, str)
		}
	}
	return servers
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
			logicalName := extractLogicalName(args)
			command, _ := args["command"].(string)
			servers := extractServers(args)
			return sshTool.ExecuteCommand(ctx, incidentID, command, servers, instanceID, logicalName)
		},
	)

	// ssh.test_connectivity
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "ssh.test_connectivity",
			Description: "Test SSH connectivity to configured servers, or specific servers when ad-hoc connections are enabled",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"servers": {
						Type:        "array",
						Description: "Optional list of specific servers to test connectivity to. When ad-hoc connections are enabled, you can test servers not in the configured list.",
						Items:       &mcp.Items{Type: "string"},
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			instanceID := extractInstanceID(args)
			logicalName := extractLogicalName(args)
			servers := extractServers(args)
			return sshTool.TestConnectivity(ctx, incidentID, servers, instanceID, logicalName)
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
			logicalName := extractLogicalName(args)
			servers := extractServers(args)
			return sshTool.GetServerInfo(ctx, incidentID, servers, instanceID, logicalName)
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
			logicalName := extractLogicalName(args)
			method, _ := args["method"].(string)
			params, _ := args["params"].(map[string]interface{})
			return r.zabbixTool.APIRequest(ctx, incidentID, method, params, instanceID, logicalName)
		},
	)
}

// registerVictoriaMetricsTools registers VictoriaMetrics-related tools
func (r *Registry) registerVictoriaMetricsTools() {
	r.vmTool = victoriametrics.NewVictoriaMetricsTool(r.logger, r.vmLimit)

	// victoriametrics.instant_query
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "victoria_metrics.instant_query",
			Description: "Execute a PromQL instant query against VictoriaMetrics",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "PromQL query expression",
					},
					"time": {
						Type:        "string",
						Description: "Evaluation timestamp (RFC3339 or Unix timestamp). Defaults to current time.",
					},
					"step": {
						Type:        "string",
						Description: "Query resolution step width (e.g., '15s', '1m')",
					},
					"timeout": {
						Type:        "string",
						Description: "Evaluation timeout (e.g., '30s')",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"query"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.vmTool.InstantQuery(ctx, incidentID, args)
		},
	)

	// victoriametrics.range_query
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "victoria_metrics.range_query",
			Description: "Execute a PromQL range query against VictoriaMetrics",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "PromQL query expression",
					},
					"start": {
						Type:        "string",
						Description: "Start timestamp (RFC3339, Unix timestamp, or relative like '1h')",
					},
					"end": {
						Type:        "string",
						Description: "End timestamp (RFC3339, Unix timestamp, or relative like 'now')",
					},
					"step": {
						Type:        "string",
						Description: "Query resolution step width (e.g., '15s', '1m')",
					},
					"timeout": {
						Type:        "string",
						Description: "Evaluation timeout (e.g., '30s')",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"query", "start", "end", "step"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.vmTool.RangeQuery(ctx, incidentID, args)
		},
	)

	// victoriametrics.label_values
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "victoria_metrics.label_values",
			Description: "Get label values for a given label name from VictoriaMetrics",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"label_name": {
						Type:        "string",
						Description: "Label name to get values for (e.g., '__name__', 'job', 'instance')",
					},
					"match": {
						Type:        "string",
						Description: "Series selector to filter results (e.g., 'up{job=\"prometheus\"}')",
					},
					"start": {
						Type:        "string",
						Description: "Start timestamp for filtering",
					},
					"end": {
						Type:        "string",
						Description: "End timestamp for filtering",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"label_name"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.vmTool.LabelValues(ctx, incidentID, args)
		},
	)

	// victoriametrics.series
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "victoria_metrics.series",
			Description: "Find series matching a label set from VictoriaMetrics",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"match": {
						Type:        "string",
						Description: "Series selector (e.g., 'up', '{job=\"prometheus\"}')",
					},
					"start": {
						Type:        "string",
						Description: "Start timestamp for filtering",
					},
					"end": {
						Type:        "string",
						Description: "End timestamp for filtering",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"match"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.vmTool.Series(ctx, incidentID, args)
		},
	)

	// victoriametrics.api_request
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "victoria_metrics.api_request",
			Description: "Make a generic HTTP request to VictoriaMetrics API",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"path": {
						Type:        "string",
						Description: "API path (e.g., '/api/v1/status/tsdb')",
					},
					"method": {
						Type:        "string",
						Description: "HTTP method (GET or POST). Defaults to GET.",
					},
					"params": {
						Type:        "object",
						Description: "Query/form parameters as key-value pairs",
					},
					"tool_instance_id": toolInstanceIDProperty,
				},
				Required: []string{"path"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.vmTool.APIRequest(ctx, incidentID, args)
		},
	)
}

// ListToolsByType lists registered tools filtered by tool type.
// If toolType is empty, returns all tools.
func (r *Registry) ListToolsByType(toolType string) []mcp.ToolListItem {
	r.server.Mu().RLock()
	defer r.server.Mu().RUnlock()

	var results []mcp.ToolListItem

	for _, tool := range r.server.Tools() {
		namespace, _ := mcp.ParseToolName(tool.Name)
		if toolType != "" && namespace != toolType {
			continue
		}

		results = append(results, mcp.ToolListItem{
			Name:        tool.Name,
			Description: tool.Description,
			ToolType:    namespace,
		})
	}

	return results
}

// GetAvailableToolTypes returns a deduplicated, sorted list of tool type names
// from all registered tools.
func (r *Registry) GetAvailableToolTypes() []string {
	r.server.Mu().RLock()
	defer r.server.Mu().RUnlock()

	seen := make(map[string]bool)
	for _, tool := range r.server.Tools() {
		namespace, _ := mcp.ParseToolName(tool.Name)
		if namespace != "" {
			seen[namespace] = true
		}
	}

	types := make([]string, 0, len(seen))
	for t := range seen {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// GetToolDetail returns full detail for a specific tool by name.
func (r *Registry) GetToolDetail(toolName string) (*mcp.GetToolDetailResult, bool) {
	r.server.Mu().RLock()
	defer r.server.Mu().RUnlock()

	tool, exists := r.server.Tools()[toolName]
	if !exists {
		return nil, false
	}

	namespace, _ := mcp.ParseToolName(tool.Name)

	return &mcp.GetToolDetailResult{
		Name:        tool.Name,
		Description: tool.Description,
		ToolType:    namespace,
		InputSchema: tool.InputSchema,
	}, true
}

// BuildInstanceLookup returns an InstanceLookup function that queries the database
// for enabled tool instances of a given tool type. Results are cached for 30 seconds
// to avoid repeated database queries on each search/detail call.
func BuildInstanceLookup() mcp.InstanceLookup {
	var (
		mu        sync.Mutex
		cached    []database.ToolInstance
		cachedAt  time.Time
		cacheTTL  = 30 * time.Second
	)

	return func(toolType string) []mcp.ToolDetailInstance {
		mu.Lock()
		if time.Since(cachedAt) > cacheTTL || cached == nil {
			ctx := context.Background()
			instances, err := database.GetAllEnabledToolInstances(ctx)
			if err != nil {
				mu.Unlock()
				return nil
			}
			cached = instances
			cachedAt = time.Now()
		}
		instances := cached
		mu.Unlock()

		var result []mcp.ToolDetailInstance
		for _, inst := range instances {
			if inst.ToolType.Name == toolType {
				result = append(result, mcp.ToolDetailInstance{
					ID:          inst.ID,
					LogicalName: inst.LogicalName,
					Name:        inst.Name,
				})
			}
		}
		return result
	}
}

// GetToolCredentials is a helper to fetch credentials from database
func GetToolCredentials(ctx context.Context, incidentID string, toolType string) (*database.ToolCredentials, error) {
	return database.GetToolCredentialsForIncident(ctx, incidentID, toolType)
}
