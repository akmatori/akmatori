package tools

import (
	"context"
	"fmt"
	"io"
	"log"
	"testing"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
)

func TestExtractInstanceID(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want *uint
	}{
		{"present", map[string]interface{}{"tool_instance_id": float64(5)}, uintPtr(5)},
		{"zero", map[string]interface{}{"tool_instance_id": float64(0)}, nil},
		{"missing", map[string]interface{}{}, nil},
		{"wrong type", map[string]interface{}{"tool_instance_id": "5"}, nil},
		{"negative", map[string]interface{}{"tool_instance_id": float64(-1)}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInstanceID(tt.args)
			if tt.want == nil {
				if got != nil {
					t.Errorf("extractInstanceID() = %v, want nil", *got)
				}
			} else {
				if got == nil {
					t.Errorf("extractInstanceID() = nil, want %d", *tt.want)
				} else if *got != *tt.want {
					t.Errorf("extractInstanceID() = %d, want %d", *got, *tt.want)
				}
			}
		})
	}
}

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-ssh"}, "prod-ssh"},
		{"empty string", map[string]interface{}{"logical_name": ""}, ""},
		{"missing", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 123}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLogicalName(tt.args)
			if got != tt.want {
				t.Errorf("extractLogicalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractServers(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want []string
	}{
		{"present", map[string]interface{}{"servers": []interface{}{"a", "b"}}, []string{"a", "b"}},
		{"missing", map[string]interface{}{}, nil},
		{"empty", map[string]interface{}{"servers": []interface{}{}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractServers(tt.args)
			if len(got) != len(tt.want) {
				t.Errorf("extractServers() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("extractServers()[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func uintPtr(v uint) *uint { return &v }

func newTestRegistry() (*Registry, *mcp.Server) {
	stdLogger := log.New(io.Discard, "", 0)
	server := mcp.NewServer("test", "1.0.0", stdLogger)
	registry := NewRegistry(server, stdLogger)

	// Register a few tools for testing
	server.RegisterTool(mcp.Tool{
		Name:        "ssh.execute_command",
		Description: "Execute a shell command on SSH servers",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"command": {Type: "string", Description: "Shell command"},
			},
			Required: []string{"command"},
		},
	}, nil)
	server.RegisterTool(mcp.Tool{
		Name:        "ssh.test_connectivity",
		Description: "Test SSH connectivity to servers",
		InputSchema: mcp.InputSchema{Type: "object"},
	}, nil)
	server.RegisterTool(mcp.Tool{
		Name:        "zabbix.get_hosts",
		Description: "Get hosts from Zabbix monitoring",
		InputSchema: mcp.InputSchema{Type: "object"},
	}, nil)
	server.RegisterTool(mcp.Tool{
		Name:        "zabbix.get_problems",
		Description: "Get current problems from Zabbix",
		InputSchema: mcp.InputSchema{Type: "object"},
	}, nil)

	return registry, server
}

func TestListToolsByType_TypeFilter(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.ListToolsByType("zabbix")
	if len(results) != 2 {
		t.Fatalf("expected 2 Zabbix tools, got %d", len(results))
	}
	for _, r := range results {
		if r.ToolType != "zabbix" {
			t.Errorf("expected tool_type 'zabbix', got %q", r.ToolType)
		}
	}
}

func TestListToolsByType_NoType_ReturnsAll(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.ListToolsByType("")
	if len(results) != 4 {
		t.Fatalf("expected 4 results for empty type, got %d", len(results))
	}
}

func TestGetToolDetail_Found(t *testing.T) {
	registry, _ := newTestRegistry()

	detail, found := registry.GetToolDetail("ssh.execute_command")
	if !found {
		t.Fatal("expected tool to be found")
	}
	if detail.Name != "ssh.execute_command" {
		t.Errorf("expected name 'ssh.execute_command', got %q", detail.Name)
	}
	if detail.ToolType != "ssh" {
		t.Errorf("expected tool_type 'ssh', got %q", detail.ToolType)
	}
	if len(detail.InputSchema.Required) != 1 || detail.InputSchema.Required[0] != "command" {
		t.Errorf("expected required [command], got %v", detail.InputSchema.Required)
	}
	if _, ok := detail.InputSchema.Properties["command"]; !ok {
		t.Error("expected 'command' property in input schema")
	}
}

func TestGetToolDetail_NotFound(t *testing.T) {
	registry, _ := newTestRegistry()

	_, found := registry.GetToolDetail("nonexistent.tool")
	if found {
		t.Error("expected tool not to be found")
	}
}

// --- HTTP Connector Dynamic Registration Tests ---

func mockConnectorLoader(connectors []database.HTTPConnector) HTTPConnectorLoader {
	return func(ctx context.Context) ([]database.HTTPConnector, error) {
		return connectors, nil
	}
}

func mockConnectorLoaderError() HTTPConnectorLoader {
	return func(ctx context.Context) ([]database.HTTPConnector, error) {
		return nil, fmt.Errorf("database unavailable")
	}
}

func makeBillingConnector() database.HTTPConnector {
	return database.HTTPConnector{
		ID:           1,
		ToolTypeName: "internal-billing",
		Description:  "Internal billing API",
		BaseURLField: "base_url",
		AuthConfig: database.JSONB{
			"method":      "bearer_token",
			"token_field": "api_token",
		},
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_invoice",
					"description": "Get an invoice by ID",
					"http_method": "GET",
					"path":        "/api/invoices/{{invoice_id}}",
					"params": []interface{}{
						map[string]interface{}{
							"name":     "invoice_id",
							"type":     "string",
							"required": true,
							"in":       "path",
						},
					},
				},
				map[string]interface{}{
					"name":        "list_invoices",
					"description": "List invoices with filters",
					"http_method": "GET",
					"path":        "/api/invoices",
					"params": []interface{}{
						map[string]interface{}{
							"name":     "status",
							"type":     "string",
							"required": false,
							"in":       "query",
						},
						map[string]interface{}{
							"name":     "limit",
							"type":     "integer",
							"required": false,
							"in":       "query",
							"default":  float64(20),
						},
					},
				},
			},
		},
		Enabled: true,
	}
}

func TestRegisterHTTPConnectors_RegistersTools(t *testing.T) {
	registry, server := newTestRegistry()

	connector := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{connector}))

	// Verify tools are registered
	tools := server.Tools()
	if _, ok := tools["internal-billing.get_invoice"]; !ok {
		t.Error("expected 'internal-billing.get_invoice' to be registered")
	}
	if _, ok := tools["internal-billing.list_invoices"]; !ok {
		t.Error("expected 'internal-billing.list_invoices' to be registered")
	}
}

func TestRegisterHTTPConnectors_ToolsAppearInList(t *testing.T) {
	registry, _ := newTestRegistry()

	connector := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{connector}))

	// List by connector type name
	results := registry.ListToolsByType("internal-billing")
	if len(results) != 2 {
		t.Fatalf("expected 2 billing tools in list, got %d", len(results))
	}
	for _, r := range results {
		if r.ToolType != "internal-billing" {
			t.Errorf("expected tool_type 'internal-billing', got %q", r.ToolType)
		}
	}
}

func TestRegisterHTTPConnectors_ToolsAppearInDetail(t *testing.T) {
	registry, _ := newTestRegistry()

	connector := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{connector}))

	detail, found := registry.GetToolDetail("internal-billing.get_invoice")
	if !found {
		t.Fatal("expected tool detail to be found")
	}
	if detail.ToolType != "internal-billing" {
		t.Errorf("expected tool_type 'internal-billing', got %q", detail.ToolType)
	}
	if _, ok := detail.InputSchema.Properties["invoice_id"]; !ok {
		t.Error("expected 'invoice_id' property in input schema")
	}
	if len(detail.InputSchema.Required) != 1 || detail.InputSchema.Required[0] != "invoice_id" {
		t.Errorf("expected required [invoice_id], got %v", detail.InputSchema.Required)
	}
}

func TestRegisterHTTPConnectors_InputSchemaHasToolInstanceID(t *testing.T) {
	registry, _ := newTestRegistry()

	connector := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{connector}))

	detail, found := registry.GetToolDetail("internal-billing.get_invoice")
	if !found {
		t.Fatal("expected tool to be found")
	}
	if _, ok := detail.InputSchema.Properties["tool_instance_id"]; !ok {
		t.Error("expected 'tool_instance_id' property in input schema")
	}
}

func TestRegisterHTTPConnectors_MultipleConnectors(t *testing.T) {
	registry, server := newTestRegistry()
	initialToolCount := len(server.Tools())

	conn1 := makeBillingConnector()
	conn2 := database.HTTPConnector{
		ID:           2,
		ToolTypeName: "crm-api",
		BaseURLField: "url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_customer",
					"http_method": "GET",
					"path":        "/customers/{{id}}",
					"params": []interface{}{
						map[string]interface{}{
							"name":     "id",
							"type":     "string",
							"required": true,
							"in":       "path",
						},
					},
				},
			},
		},
		Enabled: true,
	}

	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{conn1, conn2}))

	// 2 from billing + 1 from CRM = 3 new tools
	newToolCount := len(server.Tools()) - initialToolCount
	if newToolCount != 3 {
		t.Errorf("expected 3 new tools, got %d", newToolCount)
	}
}

func TestRegisterHTTPConnectors_NoConnectors(t *testing.T) {
	registry, server := newTestRegistry()
	initialToolCount := len(server.Tools())

	registry.RegisterHTTPConnectors(mockConnectorLoader(nil))

	if len(server.Tools()) != initialToolCount {
		t.Error("expected no new tools when no connectors")
	}
}

func TestRegisterHTTPConnectors_LoaderError(t *testing.T) {
	registry, server := newTestRegistry()
	initialToolCount := len(server.Tools())

	registry.RegisterHTTPConnectors(mockConnectorLoaderError())

	if len(server.Tools()) != initialToolCount {
		t.Error("expected no new tools on loader error")
	}
}

func TestRegisterHTTPConnectors_InvalidToolDefs(t *testing.T) {
	registry, server := newTestRegistry()
	initialToolCount := len(server.Tools())

	connector := database.HTTPConnector{
		ID:           1,
		ToolTypeName: "bad-connector",
		BaseURLField: "url",
		Tools:        database.JSONB{"tools": "not-an-array"},
		Enabled:      true,
	}

	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{connector}))

	if len(server.Tools()) != initialToolCount {
		t.Error("expected no new tools for invalid tool defs")
	}
}

func TestReloadHTTPConnectors_CleansUpOldTools(t *testing.T) {
	registry, server := newTestRegistry()

	// Initial registration
	conn1 := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{conn1}))

	if _, ok := server.Tools()["internal-billing.get_invoice"]; !ok {
		t.Fatal("expected billing tool after initial registration")
	}

	// Reload with different connector (replacing billing with CRM)
	conn2 := database.HTTPConnector{
		ID:           2,
		ToolTypeName: "crm-api",
		BaseURLField: "url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_customer",
					"http_method": "GET",
					"path":        "/customers/{{id}}",
					"params": []interface{}{
						map[string]interface{}{
							"name":     "id",
							"type":     "string",
							"required": true,
							"in":       "path",
						},
					},
				},
			},
		},
		Enabled: true,
	}

	registry.ReloadHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{conn2}))

	// Old billing tools should be gone
	if _, ok := server.Tools()["internal-billing.get_invoice"]; ok {
		t.Error("expected billing tool to be removed after reload")
	}
	if _, ok := server.Tools()["internal-billing.list_invoices"]; ok {
		t.Error("expected billing list tool to be removed after reload")
	}

	// New CRM tool should be present
	if _, ok := server.Tools()["crm-api.get_customer"]; !ok {
		t.Error("expected CRM tool after reload")
	}
}

func TestReloadHTTPConnectors_EmptyReload(t *testing.T) {
	registry, server := newTestRegistry()
	initialToolCount := len(server.Tools())

	// Register some connector tools
	conn := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{conn}))

	if len(server.Tools()) <= initialToolCount {
		t.Fatal("expected tools to be added")
	}

	// Reload with no connectors - should remove all HTTP connector tools
	registry.ReloadHTTPConnectors(mockConnectorLoader(nil))

	if len(server.Tools()) != initialToolCount {
		t.Errorf("expected %d tools after empty reload, got %d", initialToolCount, len(server.Tools()))
	}
}

func TestReloadHTTPConnectors_LoaderError_KeepsCleanState(t *testing.T) {
	registry, server := newTestRegistry()

	// Register initial tools
	conn := makeBillingConnector()
	registry.RegisterHTTPConnectors(mockConnectorLoader([]database.HTTPConnector{conn}))

	// Reload with error - old tools should be unregistered, but new ones can't load
	registry.ReloadHTTPConnectors(mockConnectorLoaderError())

	// Billing tools should be gone (they were unregistered before the load attempt)
	if _, ok := server.Tools()["internal-billing.get_invoice"]; ok {
		t.Error("expected old tools to be removed even on reload error")
	}
}

func TestParseHTTPConnectorToolDefs(t *testing.T) {
	tools := database.JSONB{
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "get_item",
				"description": "Get an item",
				"http_method": "GET",
				"path":        "/items/{{id}}",
				"read_only":   true,
				"params": []interface{}{
					map[string]interface{}{
						"name":     "id",
						"type":     "string",
						"required": true,
						"in":       "path",
					},
				},
			},
		},
	}

	defs, err := parseHTTPConnectorToolDefs(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool def, got %d", len(defs))
	}
	if defs[0].Name != "get_item" {
		t.Errorf("expected name 'get_item', got %q", defs[0].Name)
	}
	if defs[0].HTTPMethod != "GET" {
		t.Errorf("expected method GET, got %q", defs[0].HTTPMethod)
	}
	if len(defs[0].Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(defs[0].Params))
	}
	if defs[0].Params[0].Name != "id" {
		t.Errorf("expected param name 'id', got %q", defs[0].Params[0].Name)
	}
}

func TestParseHTTPConnectorToolDefs_NilTools(t *testing.T) {
	defs, err := parseHTTPConnectorToolDefs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if defs != nil {
		t.Errorf("expected nil defs, got %v", defs)
	}
}

func TestParseHTTPConnectorToolDefs_MissingToolsKey(t *testing.T) {
	tools := database.JSONB{"other": "value"}
	defs, err := parseHTTPConnectorToolDefs(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if defs != nil {
		t.Errorf("expected nil defs, got %v", defs)
	}
}

func TestParseHTTPConnectorAuthConfig(t *testing.T) {
	tests := []struct {
		name       string
		authCfg    database.JSONB
		wantNil    bool
		wantMethod string
	}{
		{"nil config", nil, true, ""},
		{"empty config", database.JSONB{}, true, ""},
		{"bearer", database.JSONB{"method": "bearer_token", "token_field": "api_token"}, false, "bearer_token"},
		{"api_key", database.JSONB{"method": "api_key", "token_field": "key", "header_name": "X-Custom"}, false, "api_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHTTPConnectorAuthConfig(tt.authCfg)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if string(result.Method) != tt.wantMethod {
				t.Errorf("expected method %q, got %q", tt.wantMethod, result.Method)
			}
		})
	}
}

func TestBuildHTTPConnectorInputSchema(t *testing.T) {
	toolDef := httpConnectorToolDef{
		Name:       "test_tool",
		HTTPMethod: "GET",
		Path:       "/test",
		Params: []httpConnectorToolParam{
			{Name: "id", Type: "string", Required: true, In: "path"},
			{Name: "status", Type: "string", Required: false, In: "query", Default: "active"},
		},
	}

	schema := buildHTTPConnectorInputSchema(toolDef)

	if schema.Type != "object" {
		t.Errorf("expected type 'object', got %q", schema.Type)
	}
	// Should have id, status, and tool_instance_id
	if len(schema.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(schema.Properties))
	}
	if _, ok := schema.Properties["tool_instance_id"]; !ok {
		t.Error("expected tool_instance_id property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "id" {
		t.Errorf("expected required [id], got %v", schema.Required)
	}
	statusProp := schema.Properties["status"]
	if statusProp.Default != "active" {
		t.Errorf("expected default 'active' for status, got %v", statusProp.Default)
	}
}
