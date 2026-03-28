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
	"github.com/akmatori/mcp-gateway/internal/tools/catchpoint"
	"github.com/akmatori/mcp-gateway/internal/tools/clickhouse"
	"github.com/akmatori/mcp-gateway/internal/tools/grafana"
	"github.com/akmatori/mcp-gateway/internal/tools/httpconnector"
	"github.com/akmatori/mcp-gateway/internal/tools/pagerduty"
	"github.com/akmatori/mcp-gateway/internal/tools/postgresql"
	"github.com/akmatori/mcp-gateway/internal/tools/ssh"
	"github.com/akmatori/mcp-gateway/internal/tools/victoriametrics"
	"github.com/akmatori/mcp-gateway/internal/tools/zabbix"
)

// Rate limit configuration
const (
	ZabbixRatePerSecond     = 10 // requests per second
	ZabbixBurstCapacity     = 20 // burst capacity
	VMRatePerSecond         = 10 // requests per second
	VMBurstCapacity         = 20 // burst capacity
	CatchpointRatePerSecond  = 10 // requests per second
	CatchpointBurstCapacity  = 20 // burst capacity
	PostgreSQLRatePerSecond  = 10 // requests per second
	PostgreSQLBurstCapacity  = 20 // burst capacity
	GrafanaRatePerSecond     = 10 // requests per second
	GrafanaBurstCapacity     = 20 // burst capacity
	ClickHouseRatePerSecond  = 10 // requests per second
	ClickHouseBurstCapacity  = 20 // burst capacity
	PagerDutyRatePerSecond   = 10 // requests per second
	PagerDutyBurstCapacity   = 20 // burst capacity
)

// Registry manages tool registration
type Registry struct {
	server      *mcp.Server
	logger      *log.Logger
	zabbixTool     *zabbix.ZabbixTool
	zabbixLimit    *ratelimit.Limiter
	vmTool         *victoriametrics.VictoriaMetricsTool
	vmLimit        *ratelimit.Limiter
	catchpointTool   *catchpoint.CatchpointTool
	catchpointLimit  *ratelimit.Limiter
	postgresqlTool   *postgresql.PostgreSQLTool
	postgresqlLimit  *ratelimit.Limiter
	grafanaTool      *grafana.GrafanaTool
	grafanaLimit     *ratelimit.Limiter
	clickhouseTool   *clickhouse.ClickHouseTool
	clickhouseLimit  *ratelimit.Limiter
	pagerdutyTool    *pagerduty.PagerDutyTool
	pagerdutyLimit   *ratelimit.Limiter

	// HTTP connector state
	httpExecutor       *httpconnector.HTTPConnectorExecutor
	httpConnectorTools []string // track registered tool names for reload cleanup
	httpMu             sync.Mutex

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

	// Create rate limiter for Catchpoint: 10 req/sec, burst 20
	r.catchpointLimit = ratelimit.New(CatchpointRatePerSecond, CatchpointBurstCapacity)
	r.logger.Printf("Catchpoint rate limiter created: %d req/sec, burst %d", CatchpointRatePerSecond, CatchpointBurstCapacity)

	// Register Catchpoint tools with rate limiter
	r.registerCatchpointTools()

	// Create rate limiter for PostgreSQL: 10 req/sec, burst 20
	r.postgresqlLimit = ratelimit.New(PostgreSQLRatePerSecond, PostgreSQLBurstCapacity)
	r.logger.Printf("PostgreSQL rate limiter created: %d req/sec, burst %d", PostgreSQLRatePerSecond, PostgreSQLBurstCapacity)

	// Register PostgreSQL tools with rate limiter
	r.registerPostgreSQLTools()

	// Create rate limiter for Grafana: 10 req/sec, burst 20
	r.grafanaLimit = ratelimit.New(GrafanaRatePerSecond, GrafanaBurstCapacity)
	r.logger.Printf("Grafana rate limiter created: %d req/sec, burst %d", GrafanaRatePerSecond, GrafanaBurstCapacity)

	// Register Grafana tools with rate limiter
	r.registerGrafanaTools()

	// Create rate limiter for ClickHouse: 10 req/sec, burst 20
	r.clickhouseLimit = ratelimit.New(ClickHouseRatePerSecond, ClickHouseBurstCapacity)
	r.logger.Printf("ClickHouse rate limiter created: %d req/sec, burst %d", ClickHouseRatePerSecond, ClickHouseBurstCapacity)

	// Register ClickHouse tools with rate limiter
	r.registerClickHouseTools()

	// Create rate limiter for PagerDuty: 10 req/sec, burst 20
	r.pagerdutyLimit = ratelimit.New(PagerDutyRatePerSecond, PagerDutyBurstCapacity)
	r.logger.Printf("PagerDuty rate limiter created: %d req/sec, burst %d", PagerDutyRatePerSecond, PagerDutyBurstCapacity)

	// Register PagerDuty tools with rate limiter
	r.registerPagerDutyTools()

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
	if r.catchpointTool != nil {
		r.catchpointTool.Stop()
	}
	if r.postgresqlTool != nil {
		r.postgresqlTool.Stop()
	}
	if r.grafanaTool != nil {
		r.grafanaTool.Stop()
	}
	if r.clickhouseTool != nil {
		r.clickhouseTool.Stop()
	}
	if r.pagerdutyTool != nil {
		r.pagerdutyTool.Stop()
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

// RegisterSystemMCPProxy registers a single system-level MCP server and its tools.
// System servers persist across reloads (unlike DB-loaded servers).
func (r *Registry) RegisterSystemMCPProxy(ctx context.Context, reg mcpproxy.ServerRegistration) error {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()

	if r.proxyHandler == nil {
		return fmt.Errorf("MCP proxy handler not configured")
	}

	if err := r.proxyHandler.RegisterSystemServer(ctx, reg); err != nil {
		return fmt.Errorf("register system MCP server %q: %w", reg.NamespacePrefix, err)
	}

	r.registerProxyToolsFromHandler()
	return nil
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
	seenNamespaces := make(map[string]bool)

	for _, tool := range tools {
		toolName := tool.Name
		r.server.RegisterTool(tool, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			result, err := handler.CallTool(ctx, toolName, args)
			if err != nil {
				return nil, err
			}
			// Return the call result content as JSON
			if len(result.Content) == 1 {
				return result.Content[0].ExtractText(), nil
			}
			return result, nil
		})
		r.proxyToolNames = append(r.proxyToolNames, tool.Name)

		// Register the namespace as a proxy namespace so it bypasses per-incident
		// allowlist checks. This is needed for single-segment namespaces (e.g., "qmd")
		// that don't contain dots.
		namespace, _ := mcp.ParseToolName(toolName)
		if !seenNamespaces[namespace] {
			r.server.AddProxyNamespace(namespace)
			seenNamespaces[namespace] = true
		}
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
				logicalName := extractLogicalName(args)

				// Resolve credentials for this connector's tool type
				creds, err := database.ResolveToolCredentials(ctx, incidentID, connectorDef.ToolTypeName, nil, logicalName)
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
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	HTTPMethod  string                   `json:"http_method"`
	Path        string                   `json:"path"`
	Params      []httpConnectorToolParam `json:"params,omitempty"`
	ReadOnly    *bool                    `json:"read_only,omitempty"`
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
	properties := map[string]mcp.Property{}
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
		Name:        def.Name,
		Description: def.Description,
		HTTPMethod:  def.HTTPMethod,
		Path:        def.Path,
		Params:      params,
		ReadOnly:    def.ReadOnly,
	}
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
				},
				Required: []string{"command"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			logicalName := extractLogicalName(args)
			command, _ := args["command"].(string)
			servers := extractServers(args)
			return sshTool.ExecuteCommand(ctx, incidentID, command, servers, nil, logicalName)
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
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			logicalName := extractLogicalName(args)
			servers := extractServers(args)
			return sshTool.TestConnectivity(ctx, incidentID, servers, nil, logicalName)
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
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			logicalName := extractLogicalName(args)
			servers := extractServers(args)
			return sshTool.GetServerInfo(ctx, incidentID, servers, nil, logicalName)
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
				},
				Required: []string{"method"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			logicalName := extractLogicalName(args)
			method, _ := args["method"].(string)
			params, _ := args["params"].(map[string]interface{})
			return r.zabbixTool.APIRequest(ctx, incidentID, method, params, logicalName)
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
		mu       sync.Mutex
		cached   []database.ToolInstance
		cachedAt time.Time
		cacheTTL = 30 * time.Second
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

// registerCatchpointTools registers all Catchpoint tool methods
func (r *Registry) registerCatchpointTools() {
	r.catchpointTool = catchpoint.NewCatchpointTool(r.logger, r.catchpointLimit)

	// catchpoint.get_alerts
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_alerts",
			Description: "Get test alerts from Catchpoint",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"severity": {
						Type:        "string",
						Description: "Filter by alert severity",
					},
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs to filter by",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetAlerts(ctx, incidentID, args)
		},
	)

	// catchpoint.get_alert_details
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_alert_details",
			Description: "Get detailed information for specific alerts",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"alert_ids": {
						Type:        "string",
						Description: "Comma-separated alert IDs (required)",
					},
				},
				Required: []string{"alert_ids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetAlertDetails(ctx, incidentID, args)
		},
	)

	// catchpoint.get_test_performance
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_test_performance",
			Description: "Get aggregated test performance metrics",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs (required)",
					},
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"metrics": {
						Type:        "string",
						Description: "Comma-separated metric names to return",
					},
					"dimensions": {
						Type:        "string",
						Description: "Comma-separated dimension names for grouping",
					},
				},
				Required: []string{"test_ids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetTestPerformance(ctx, incidentID, args)
		},
	)

	// catchpoint.get_test_performance_raw
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_test_performance_raw",
			Description: "Get raw test performance data points",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs (required)",
					},
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"node_ids": {
						Type:        "string",
						Description: "Comma-separated node IDs to filter by",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
				Required: []string{"test_ids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetTestPerformanceRaw(ctx, incidentID, args)
		},
	)

	// catchpoint.get_tests
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_tests",
			Description: "List tests from Catchpoint",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs to filter by",
					},
					"test_type": {
						Type:        "string",
						Description: "Filter by test type",
					},
					"folder_id": {
						Type:        "string",
						Description: "Filter by folder ID",
					},
					"status": {
						Type:        "string",
						Description: "Filter by test status",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetTests(ctx, incidentID, args)
		},
	)

	// catchpoint.get_test_details
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_test_details",
			Description: "Get detailed configuration for specific tests",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs (required)",
					},
				},
				Required: []string{"test_ids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetTestDetails(ctx, incidentID, args)
		},
	)

	// catchpoint.get_test_errors
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_test_errors",
			Description: "Get raw test error data",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_ids": {
						Type:        "string",
						Description: "Comma-separated test IDs to filter by",
					},
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetTestErrors(ctx, incidentID, args)
		},
	)

	// catchpoint.get_internet_outages
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_internet_outages",
			Description: "Get internet outage data from Catchpoint Internet Weather",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"asn": {
						Type:        "string",
						Description: "Filter by Autonomous System Number",
					},
					"country": {
						Type:        "string",
						Description: "Filter by country code",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetInternetOutages(ctx, incidentID, args)
		},
	)

	// catchpoint.get_nodes
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_nodes",
			Description: "List all Catchpoint monitoring nodes",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetNodes(ctx, incidentID, args)
		},
	)

	// catchpoint.get_node_alerts
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.get_node_alerts",
			Description: "Get alerts for specific monitoring nodes",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"node_ids": {
						Type:        "string",
						Description: "Comma-separated node IDs to filter by",
					},
					"start_time": {
						Type:        "string",
						Description: "Start time filter (ISO 8601)",
					},
					"end_time": {
						Type:        "string",
						Description: "End time filter (ISO 8601)",
					},
					"page_number": {
						Type:        "number",
						Description: "Page number for pagination",
					},
					"page_size": {
						Type:        "number",
						Description: "Page size (max 100)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.GetNodeAlerts(ctx, incidentID, args)
		},
	)

	// catchpoint.acknowledge_alerts (write operation)
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.acknowledge_alerts",
			Description: "Acknowledge, assign, or drop test alerts",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"alert_ids": {
						Type:        "string",
						Description: "Comma-separated alert IDs to act on (required)",
					},
					"action": {
						Type:        "string",
						Description: "Action to perform: acknowledge, assign, or drop (required)",
					},
					"assignee": {
						Type:        "string",
						Description: "User to assign alerts to (required when action is 'assign')",
					},
				},
				Required: []string{"alert_ids", "action"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.AcknowledgeAlerts(ctx, incidentID, args)
		},
	)

	// catchpoint.run_instant_test (write operation)
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "catchpoint.run_instant_test",
			Description: "Trigger an instant (on-demand) test execution",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"test_id": {
						Type:        "string",
						Description: "Test ID to execute (required)",
					},
				},
				Required: []string{"test_id"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.catchpointTool.RunInstantTest(ctx, incidentID, args)
		},
	)

	r.logger.Println("Catchpoint tools registered (12 methods)")
}

func (r *Registry) registerPostgreSQLTools() {
	r.postgresqlTool = postgresql.NewPostgreSQLTool(r.logger, r.postgresqlLimit)

	// postgresql.execute_query
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.execute_query",
			Description: "Execute a read-only SQL query (SELECT only) against a PostgreSQL database",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "SQL SELECT query to execute (required)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of rows to return (default 100, max 1000)",
					},
				},
				Required: []string{"query"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.ExecuteQuery(ctx, incidentID, args)
		},
	)

	// postgresql.list_tables
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.list_tables",
			Description: "List tables in a PostgreSQL database schema with row estimates",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"schema": {
						Type:        "string",
						Description: "Schema name (default: public)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.ListTables(ctx, incidentID, args)
		},
	)

	// postgresql.describe_table
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.describe_table",
			Description: "Describe columns of a PostgreSQL table including types, nullability, and defaults",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table_name": {
						Type:        "string",
						Description: "Table name to describe (required)",
					},
					"schema": {
						Type:        "string",
						Description: "Schema name (default: public)",
					},
				},
				Required: []string{"table_name"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.DescribeTable(ctx, incidentID, args)
		},
	)

	// postgresql.get_indexes
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_indexes",
			Description: "Get indexes for a PostgreSQL table including definitions and uniqueness",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table_name": {
						Type:        "string",
						Description: "Table name to get indexes for (required)",
					},
					"schema": {
						Type:        "string",
						Description: "Schema name (default: public)",
					},
				},
				Required: []string{"table_name"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetIndexes(ctx, incidentID, args)
		},
	)

	// postgresql.get_table_stats
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_table_stats",
			Description: "Get table statistics from pg_stat_user_tables (scans, tuples, vacuum info)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table_name": {
						Type:        "string",
						Description: "Table name (optional, returns all tables if omitted)",
					},
					"schema": {
						Type:        "string",
						Description: "Schema name to filter by (optional)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetTableStats(ctx, incidentID, args)
		},
	)

	// postgresql.explain_query
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.explain_query",
			Description: "Get the execution plan for a SELECT query (EXPLAIN without ANALYZE)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "SQL SELECT query to explain (required)",
					},
				},
				Required: []string{"query"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.ExplainQuery(ctx, incidentID, args)
		},
	)

	// postgresql.get_active_queries
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_active_queries",
			Description: "Get currently active queries from pg_stat_activity",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"include_idle": {
						Type:        "boolean",
						Description: "Include idle connections (default: false)",
					},
					"min_duration_seconds": {
						Type:        "number",
						Description: "Only show queries running longer than this many seconds",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetActiveQueries(ctx, incidentID, args)
		},
	)

	// postgresql.get_locks
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_locks",
			Description: "Get lock information from pg_locks joined with pg_stat_activity",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"blocked_only": {
						Type:        "boolean",
						Description: "Only show blocked (waiting) locks (default: false)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetLocks(ctx, incidentID, args)
		},
	)

	// postgresql.get_replication_status
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_replication_status",
			Description: "Get replication status from pg_stat_replication (LSN positions, lag)",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetReplicationStatus(ctx, incidentID, args)
		},
	)

	// postgresql.get_database_stats
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "postgresql.get_database_stats",
			Description: "Get database-level statistics (connections, transactions, cache hit ratio, size)",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.postgresqlTool.GetDatabaseStats(ctx, incidentID, args)
		},
	)

	r.logger.Println("PostgreSQL tools registered (10 methods)")
}

// registerGrafanaTools registers all Grafana tool methods
func (r *Registry) registerGrafanaTools() {
	r.grafanaTool = grafana.NewGrafanaTool(r.logger, r.grafanaLimit)

	// grafana.search_dashboards
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.search_dashboards",
			Description: "Search and list Grafana dashboards by query, tag, or folder",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "Search query string",
					},
					"tag": {
						Type:        "string",
						Description: "Filter by dashboard tag",
					},
					"type": {
						Type:        "string",
						Description: "Result type: dash-db or dash-folder (default: dash-db)",
					},
					"folder_id": {
						Type:        "number",
						Description: "Filter by folder ID",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results (max 5000)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.SearchDashboards(ctx, incidentID, args)
		},
	)

	// grafana.get_dashboard
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_dashboard",
			Description: "Get a full dashboard model by UID",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"uid": {
						Type:        "string",
						Description: "Dashboard UID (required)",
					},
				},
				Required: []string{"uid"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetDashboardByUID(ctx, incidentID, args)
		},
	)

	// grafana.get_dashboard_panels
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_dashboard_panels",
			Description: "Get a summary list of panels from a dashboard",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"uid": {
						Type:        "string",
						Description: "Dashboard UID (required)",
					},
				},
				Required: []string{"uid"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetDashboardPanels(ctx, incidentID, args)
		},
	)

	// grafana.get_alert_rules
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_alert_rules",
			Description: "List all provisioned alert rules from Grafana Unified Alerting",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetAlertRules(ctx, incidentID, args)
		},
	)

	// grafana.get_alert_instances
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_alert_instances",
			Description: "Get firing and pending alert instances from Grafana Alertmanager",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"filter": {
						Type:        "string",
						Description: "Alertmanager filter expression",
					},
					"silenced": {
						Type:        "boolean",
						Description: "Include silenced alerts",
					},
					"inhibited": {
						Type:        "boolean",
						Description: "Include inhibited alerts",
					},
					"active": {
						Type:        "boolean",
						Description: "Include active alerts",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetAlertInstances(ctx, incidentID, args)
		},
	)

	// grafana.get_alert_rule
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_alert_rule",
			Description: "Get a specific alert rule by UID",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"uid": {
						Type:        "string",
						Description: "Alert rule UID (required)",
					},
				},
				Required: []string{"uid"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetAlertRuleByUID(ctx, incidentID, args)
		},
	)

	// grafana.silence_alert (write operation)
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.silence_alert",
			Description: "Create a silence in Grafana Alertmanager",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"matchers": {
						Type:        "array",
						Description: "Label matchers for the silence (array of {name, value, isRegex, isEqual})",
					},
					"starts_at": {
						Type:        "string",
						Description: "Silence start time (RFC3339 timestamp, required)",
					},
					"ends_at": {
						Type:        "string",
						Description: "Silence end time (RFC3339 timestamp, required)",
					},
					"created_by": {
						Type:        "string",
						Description: "Creator of the silence (required)",
					},
					"comment": {
						Type:        "string",
						Description: "Reason for the silence (required)",
					},
				},
				Required: []string{"matchers", "starts_at", "ends_at", "created_by", "comment"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.SilenceAlert(ctx, incidentID, args)
		},
	)

	// grafana.list_data_sources
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.list_data_sources",
			Description: "List all configured data sources in Grafana",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.ListDataSources(ctx, incidentID, args)
		},
	)

	// grafana.query_data_source
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.query_data_source",
			Description: "Query a data source via the Grafana unified query API",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"datasource_uid": {
						Type:        "string",
						Description: "Data source UID (required)",
					},
					"queries": {
						Type:        "array",
						Description: "Array of query objects with refId and datasource (required)",
					},
					"from": {
						Type:        "string",
						Description: "Start of time range (epoch ms or relative string)",
					},
					"to": {
						Type:        "string",
						Description: "End of time range (epoch ms or relative string)",
					},
				},
				Required: []string{"datasource_uid", "queries"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.QueryDataSource(ctx, incidentID, args)
		},
	)

	// grafana.query_prometheus
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.query_prometheus",
			Description: "Query a Prometheus-type data source via Grafana proxy (instant or range)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"datasource_uid": {
						Type:        "string",
						Description: "Prometheus data source UID (required)",
					},
					"expr": {
						Type:        "string",
						Description: "PromQL expression (required)",
					},
					"start": {
						Type:        "string",
						Description: "Range query start time",
					},
					"end": {
						Type:        "string",
						Description: "Range query end time",
					},
					"step": {
						Type:        "string",
						Description: "Range query step interval",
					},
					"instant": {
						Type:        "boolean",
						Description: "Execute as instant query",
					},
					"range": {
						Type:        "boolean",
						Description: "Execute as range query",
					},
					"from": {
						Type:        "string",
						Description: "Start of time range for the query request",
					},
					"to": {
						Type:        "string",
						Description: "End of time range for the query request",
					},
				},
				Required: []string{"datasource_uid", "expr"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.QueryPrometheus(ctx, incidentID, args)
		},
	)

	// grafana.query_loki
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.query_loki",
			Description: "Query a Loki-type data source via Grafana proxy (log queries)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"datasource_uid": {
						Type:        "string",
						Description: "Loki data source UID (required)",
					},
					"expr": {
						Type:        "string",
						Description: "LogQL expression (required)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of log lines to return",
					},
					"direction": {
						Type:        "string",
						Description: "Log order direction (forward or backward)",
					},
					"start": {
						Type:        "string",
						Description: "Query start time",
					},
					"end": {
						Type:        "string",
						Description: "Query end time",
					},
					"from": {
						Type:        "string",
						Description: "Start of time range for the query request",
					},
					"to": {
						Type:        "string",
						Description: "End of time range for the query request",
					},
				},
				Required: []string{"datasource_uid", "expr"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.QueryLoki(ctx, incidentID, args)
		},
	)

	// grafana.create_annotation (write operation)
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.create_annotation",
			Description: "Create an annotation on a Grafana dashboard or globally",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"text": {
						Type:        "string",
						Description: "Annotation text (required)",
					},
					"dashboard_id": {
						Type:        "number",
						Description: "Dashboard ID to attach the annotation to",
					},
					"panel_id": {
						Type:        "number",
						Description: "Panel ID to attach the annotation to",
					},
					"tags": {
						Type:        "array",
						Description: "Tags for the annotation",
					},
					"time": {
						Type:        "number",
						Description: "Annotation timestamp (epoch milliseconds)",
					},
					"time_end": {
						Type:        "number",
						Description: "Annotation end timestamp for region annotations (epoch milliseconds)",
					},
				},
				Required: []string{"text"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.CreateAnnotation(ctx, incidentID, args)
		},
	)

	// grafana.get_annotations
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "grafana.get_annotations",
			Description: "List annotations with optional filters",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"from": {
						Type:        "number",
						Description: "Start time filter (epoch milliseconds)",
					},
					"to": {
						Type:        "number",
						Description: "End time filter (epoch milliseconds)",
					},
					"dashboard_id": {
						Type:        "number",
						Description: "Filter by dashboard ID",
					},
					"panel_id": {
						Type:        "number",
						Description: "Filter by panel ID",
					},
					"tags": {
						Type:        "string",
						Description: "Filter by tags (comma-separated)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results (max 5000)",
					},
					"type": {
						Type:        "string",
						Description: "Filter by type: annotation or alert",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.grafanaTool.GetAnnotations(ctx, incidentID, args)
		},
	)

	r.logger.Println("Grafana tools registered (13 methods)")
}

// registerClickHouseTools registers all ClickHouse tool methods
func (r *Registry) registerClickHouseTools() {
	r.clickhouseTool = clickhouse.NewClickHouseTool(r.logger, r.clickhouseLimit)

	// clickhouse.execute_query
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.execute_query",
			Description: "Execute a read-only SQL query (SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS only) against a ClickHouse database",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "SQL query to execute (required)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of rows to return (default 100, max 1000)",
					},
					"timeout_seconds": {
						Type:        "number",
						Description: "Query timeout in seconds (default 30, range 5-300)",
					},
				},
				Required: []string{"query"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.ExecuteQuery(ctx, incidentID, args)
		},
	)

	// clickhouse.show_databases
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.show_databases",
			Description: "List all databases on the ClickHouse server",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]mcp.Property{},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.ShowDatabases(ctx, incidentID, args)
		},
	)

	// clickhouse.show_tables
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.show_tables",
			Description: "List tables in a ClickHouse database",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"database": {
						Type:        "string",
						Description: "Database name (defaults to configured database)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.ShowTables(ctx, incidentID, args)
		},
	)

	// clickhouse.describe_table
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.describe_table",
			Description: "Get column definitions and types for a ClickHouse table",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table_name": {
						Type:        "string",
						Description: "Table name to describe (required)",
					},
					"database": {
						Type:        "string",
						Description: "Database name (defaults to configured database)",
					},
				},
				Required: []string{"table_name"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.DescribeTable(ctx, incidentID, args)
		},
	)

	// clickhouse.get_query_log
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_query_log",
			Description: "Get recent queries from system.query_log",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"min_duration_ms": {
						Type:        "number",
						Description: "Minimum query duration in milliseconds to filter",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of entries to return (default 50, max 1000)",
					},
					"query_kind": {
						Type:        "string",
						Description: "Filter by query kind (e.g. Select, Insert)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetQueryLog(ctx, incidentID, args)
		},
	)

	// clickhouse.get_running_queries
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_running_queries",
			Description: "Get currently running queries from system.processes",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"min_elapsed_seconds": {
						Type:        "number",
						Description: "Only show queries running longer than this many seconds",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetRunningQueries(ctx, incidentID, args)
		},
	)

	// clickhouse.get_merges
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_merges",
			Description: "Get active merge operations from system.merges",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table": {
						Type:        "string",
						Description: "Filter by table name",
					},
					"database": {
						Type:        "string",
						Description: "Filter by database name",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetMerges(ctx, incidentID, args)
		},
	)

	// clickhouse.get_replication_status
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_replication_status",
			Description: "Get replication queue status from system.replication_queue",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table": {
						Type:        "string",
						Description: "Filter by table name",
					},
					"database": {
						Type:        "string",
						Description: "Filter by database name",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetReplicationStatus(ctx, incidentID, args)
		},
	)

	// clickhouse.get_parts_info
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_parts_info",
			Description: "Get parts information from system.parts for a ClickHouse table",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"table_name": {
						Type:        "string",
						Description: "Table name to get parts info for (required)",
					},
					"database": {
						Type:        "string",
						Description: "Database name (defaults to configured database)",
					},
					"active_only": {
						Type:        "boolean",
						Description: "Only show active parts (default: true)",
					},
				},
				Required: []string{"table_name"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetPartsInfo(ctx, incidentID, args)
		},
	)

	// clickhouse.get_cluster_info
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "clickhouse.get_cluster_info",
			Description: "Get cluster topology from system.clusters",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"cluster": {
						Type:        "string",
						Description: "Filter by cluster name",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.clickhouseTool.GetClusterInfo(ctx, incidentID, args)
		},
	)

	r.logger.Println("ClickHouse tools registered (10 methods)")
}

// registerPagerDutyTools registers all PagerDuty tool methods
func (r *Registry) registerPagerDutyTools() {
	r.pagerdutyTool = pagerduty.NewPagerDutyTool(r.logger, r.pagerdutyLimit)

	// pagerduty.get_incidents
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_incidents",
			Description: "List PagerDuty incidents with optional filters (status, urgency, service, date range)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"statuses": {
						Type:        "string",
						Description: "Filter by status (triggered, acknowledged, resolved)",
					},
					"urgencies": {
						Type:        "string",
						Description: "Filter by urgency (high, low)",
					},
					"service_ids": {
						Type:        "string",
						Description: "Filter by service ID",
					},
					"since": {
						Type:        "string",
						Description: "Start date filter (ISO 8601)",
					},
					"until": {
						Type:        "string",
						Description: "End date filter (ISO 8601)",
					},
					"sort_by": {
						Type:        "string",
						Description: "Sort field (e.g. incident_number:asc)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results to return",
					},
					"offset": {
						Type:        "number",
						Description: "Pagination offset",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetIncidents(ctx, incidentID, args)
		},
	)

	// pagerduty.get_incident
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_incident",
			Description: "Get detailed information for a specific PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
				},
				Required: []string{"incident_id"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetIncident(ctx, incidentID, args)
		},
	)

	// pagerduty.get_incident_notes
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_incident_notes",
			Description: "Get notes and timeline entries for a PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
				},
				Required: []string{"incident_id"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetIncidentNotes(ctx, incidentID, args)
		},
	)

	// pagerduty.get_incident_alerts
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_incident_alerts",
			Description: "Get alerts grouped under a PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
				},
				Required: []string{"incident_id"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetIncidentAlerts(ctx, incidentID, args)
		},
	)

	// pagerduty.get_services
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_services",
			Description: "List PagerDuty services with optional search query",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "Search query to filter services by name",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results to return",
					},
					"offset": {
						Type:        "number",
						Description: "Pagination offset",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetServices(ctx, incidentID, args)
		},
	)

	// pagerduty.get_on_calls
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_on_calls",
			Description: "Get current on-call users by schedule or escalation policy",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"schedule_ids": {
						Type:        "string",
						Description: "Filter by schedule ID",
					},
					"escalation_policy_ids": {
						Type:        "string",
						Description: "Filter by escalation policy ID",
					},
					"since": {
						Type:        "string",
						Description: "Start of on-call window (ISO 8601)",
					},
					"until": {
						Type:        "string",
						Description: "End of on-call window (ISO 8601)",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetOnCalls(ctx, incidentID, args)
		},
	)

	// pagerduty.get_escalation_policies
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.get_escalation_policies",
			Description: "List PagerDuty escalation policies with optional search query",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"query": {
						Type:        "string",
						Description: "Search query to filter policies by name",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results to return",
					},
					"offset": {
						Type:        "number",
						Description: "Pagination offset",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.GetEscalationPolicies(ctx, incidentID, args)
		},
	)

	// pagerduty.list_recent_changes
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.list_recent_changes",
			Description: "List recent changes across PagerDuty services",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"since": {
						Type:        "string",
						Description: "Start date filter (ISO 8601)",
					},
					"until": {
						Type:        "string",
						Description: "End date filter (ISO 8601)",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of results to return",
					},
					"offset": {
						Type:        "number",
						Description: "Pagination offset",
					},
				},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.ListRecentChanges(ctx, incidentID, args)
		},
	)

	// pagerduty.acknowledge_incident
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.acknowledge_incident",
			Description: "Acknowledge a PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
					"requester_email": {
						Type:        "string",
						Description: "Email address of the user acknowledging the incident (required)",
					},
				},
				Required: []string{"incident_id", "requester_email"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.AcknowledgeIncident(ctx, incidentID, args)
		},
	)

	// pagerduty.resolve_incident
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.resolve_incident",
			Description: "Resolve a PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
					"requester_email": {
						Type:        "string",
						Description: "Email address of the user resolving the incident (required)",
					},
				},
				Required: []string{"incident_id", "requester_email"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.ResolveIncident(ctx, incidentID, args)
		},
	)

	// pagerduty.reassign_incident
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.reassign_incident",
			Description: "Reassign a PagerDuty incident to different users or escalation policy",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
					"requester_email": {
						Type:        "string",
						Description: "Email address of the user reassigning the incident (required)",
					},
					"assignee_ids": {
						Type:        "string",
						Description: "Comma-separated user IDs to assign the incident to (required)",
					},
					"escalation_policy_id": {
						Type:        "string",
						Description: "Escalation policy ID to assign (optional)",
					},
				},
				Required: []string{"incident_id", "requester_email", "assignee_ids"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.ReassignIncident(ctx, incidentID, args)
		},
	)

	// pagerduty.add_incident_note
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.add_incident_note",
			Description: "Add a note to a PagerDuty incident",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"incident_id": {
						Type:        "string",
						Description: "PagerDuty incident ID (required)",
					},
					"requester_email": {
						Type:        "string",
						Description: "Email address of the user adding the note (required)",
					},
					"content": {
						Type:        "string",
						Description: "Note content text (required)",
					},
				},
				Required: []string{"incident_id", "requester_email", "content"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.AddIncidentNote(ctx, incidentID, args)
		},
	)

	// pagerduty.send_event
	r.server.RegisterTool(
		mcp.Tool{
			Name:        "pagerduty.send_event",
			Description: "Send an event via PagerDuty Events API v2 (trigger, acknowledge, or resolve)",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"routing_key": {
						Type:        "string",
						Description: "Events API v2 routing/integration key (required)",
					},
					"event_action": {
						Type:        "string",
						Description: "Event action: trigger, acknowledge, or resolve (required)",
					},
					"dedup_key": {
						Type:        "string",
						Description: "Deduplication key (required for acknowledge/resolve)",
					},
					"summary": {
						Type:        "string",
						Description: "Event summary (required for trigger events)",
					},
					"severity": {
						Type:        "string",
						Description: "Event severity: critical, error, warning, info (default: error, for trigger events)",
					},
					"source": {
						Type:        "string",
						Description: "Event source (default: akmatori, for trigger events)",
					},
					"component": {
						Type:        "string",
						Description: "Component name (optional, for trigger events)",
					},
					"group": {
						Type:        "string",
						Description: "Logical grouping (optional, for trigger events)",
					},
					"class": {
						Type:        "string",
						Description: "Event class/type (optional, for trigger events)",
					},
					"custom_details": {
						Type:        "object",
						Description: "Additional custom key-value details to include in the event payload (optional, for trigger events)",
					},
				},
				Required: []string{"routing_key", "event_action"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return r.pagerdutyTool.SendEvent(ctx, incidentID, args)
		},
	)

	r.logger.Println("PagerDuty tools registered (13 methods)")
}
