package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/auth"
)

// mockDiscoverer implements ToolDiscoverer for testing
type mockDiscoverer struct {
	tools          []SearchToolsResultItem
	detail         *GetToolDetailResult
	availableTypes []string
}

func (m *mockDiscoverer) SearchTools(query string, toolType string) []SearchToolsResultItem {
	return m.tools
}

func (m *mockDiscoverer) GetToolDetail(toolName string) (*GetToolDetailResult, bool) {
	if m.detail != nil && m.detail.Name == toolName {
		return m.detail, true
	}
	return nil, false
}

func (m *mockDiscoverer) GetAvailableToolTypes() []string {
	return m.availableTypes
}

func newTestServer() *Server {
	return NewServer("test", "1.0.0", nil)
}

func sendJSONRPC(t *testing.T, server *Server, method string, params interface{}) Response {
	t.Helper()
	return sendJSONRPCWithHeaders(t, server, method, params, nil)
}

func sendJSONRPCWithHeaders(t *testing.T, server *Server, method string, params interface{}, headers map[string]string) Response {
	t.Helper()

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("failed to marshal params: %v", err)
		}
		rawParams = b
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  rawParams,
	}

	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	server.HandleHTTP(w, httpReq)

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestHandleSearchTools_QueryMatching(t *testing.T) {
	s := newTestServer()
	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute a shell command on SSH servers",
		InputSchema: InputSchema{Type: "object"},
	}, nil)
	s.RegisterTool(Tool{
		Name:        "zabbix.get_hosts",
		Description: "Get hosts from Zabbix",
		InputSchema: InputSchema{Type: "object"},
	}, nil)

	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute a shell command on SSH servers", ToolType: "ssh"},
		},
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "ssh"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "ssh.execute_command" {
		t.Errorf("expected tool name 'ssh.execute_command', got %q", result.Tools[0].Name)
	}
}

func TestHandleSearchTools_EmptyResults(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{tools: nil})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "nonexistent"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
}

func TestHandleSearchTools_TypeFilter(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "zabbix.get_hosts", Description: "Get hosts", ToolType: "zabbix"},
		},
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "", ToolType: "zabbix"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].ToolType != "zabbix" {
		t.Errorf("expected tool_type 'zabbix', got %q", result.Tools[0].ToolType)
	}
}

func TestHandleSearchTools_WithInstanceLookup(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
		},
	})
	s.SetInstanceLookup(func(toolType string) []ToolDetailInstance {
		if toolType == "ssh" {
			return []ToolDetailInstance{
				{ID: 1, LogicalName: "prod-ssh", Name: "Production SSH"},
				{ID: 2, LogicalName: "staging-ssh", Name: "Staging SSH"},
			}
		}
		return nil
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "ssh"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if len(result.Tools[0].Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(result.Tools[0].Instances))
	}
	if result.Tools[0].Instances[0] != "prod-ssh" {
		t.Errorf("expected instance 'prod-ssh', got %q", result.Tools[0].Instances[0])
	}
}

func TestHandleSearchTools_NoDiscoverer(t *testing.T) {
	s := newTestServer()

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "ssh"})
	if resp.Error == nil {
		t.Fatal("expected error when discoverer not set")
	}
	if resp.Error.Code != InternalError {
		t.Errorf("expected error code %d, got %d", InternalError, resp.Error.Code)
	}
}

func TestHandleGetToolDetail_Found(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		detail: &GetToolDetailResult{
			Name:        "ssh.execute_command",
			Description: "Execute command",
			ToolType:    "ssh",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"command": {Type: "string", Description: "Shell command"},
				},
				Required: []string{"command"},
			},
		},
	})

	resp := sendJSONRPC(t, s, "tools/detail", GetToolDetailParams{ToolName: "ssh.execute_command"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result GetToolDetailResult
	json.Unmarshal(resultBytes, &result)

	if result.Name != "ssh.execute_command" {
		t.Errorf("expected name 'ssh.execute_command', got %q", result.Name)
	}
	if result.ToolType != "ssh" {
		t.Errorf("expected tool_type 'ssh', got %q", result.ToolType)
	}
	if len(result.InputSchema.Required) != 1 || result.InputSchema.Required[0] != "command" {
		t.Errorf("expected required [command], got %v", result.InputSchema.Required)
	}
}

func TestHandleGetToolDetail_NotFound(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{})

	resp := sendJSONRPC(t, s, "tools/detail", GetToolDetailParams{ToolName: "nonexistent.tool"})
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent tool")
	}
	if resp.Error.Code != MethodNotFound {
		t.Errorf("expected error code %d, got %d", MethodNotFound, resp.Error.Code)
	}
}

func TestHandleGetToolDetail_EmptyToolName(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{})

	resp := sendJSONRPC(t, s, "tools/detail", GetToolDetailParams{ToolName: ""})
	if resp.Error == nil {
		t.Fatal("expected error for empty tool name")
	}
	if resp.Error.Code != InvalidParams {
		t.Errorf("expected error code %d, got %d", InvalidParams, resp.Error.Code)
	}
}

func TestHandleGetToolDetail_WithInstanceLookup(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		detail: &GetToolDetailResult{
			Name:        "zabbix.get_hosts",
			Description: "Get hosts",
			ToolType:    "zabbix",
			InputSchema: InputSchema{Type: "object"},
		},
	})
	s.SetInstanceLookup(func(toolType string) []ToolDetailInstance {
		if toolType == "zabbix" {
			return []ToolDetailInstance{
				{ID: 10, LogicalName: "prod-zabbix", Name: "Production Zabbix"},
			}
		}
		return nil
	})

	resp := sendJSONRPC(t, s, "tools/detail", GetToolDetailParams{ToolName: "zabbix.get_hosts"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result GetToolDetailResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(result.Instances))
	}
	if result.Instances[0].LogicalName != "prod-zabbix" {
		t.Errorf("expected logical_name 'prod-zabbix', got %q", result.Instances[0].LogicalName)
	}
	if result.Instances[0].ID != 10 {
		t.Errorf("expected instance ID 10, got %d", result.Instances[0].ID)
	}
}

func TestHandleGetToolDetail_NoDiscoverer(t *testing.T) {
	s := newTestServer()

	resp := sendJSONRPC(t, s, "tools/detail", GetToolDetailParams{ToolName: "ssh.execute_command"})
	if resp.Error == nil {
		t.Fatal("expected error when discoverer not set")
	}
	if resp.Error.Code != InternalError {
		t.Errorf("expected error code %d, got %d", InternalError, resp.Error.Code)
	}
}

// --- Authorization tests ---

func echoHandler(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	return "ok", nil
}

func TestAuthorization_AuthorizedCallPasses(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute command",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ssh.execute_command", Arguments: map[string]interface{}{"command": "uptime"}},
		map[string]string{
			"X-Incident-ID":    "incident-auth-1",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)

	if resp.Error != nil {
		t.Fatalf("expected authorized call to succeed, got error: %s", resp.Error.Message)
	}
}

func TestAuthorization_UnauthorizedCallRejected(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.RegisterTool(Tool{
		Name:        "zabbix.get_hosts",
		Description: "Get hosts",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	// Allowlist only permits SSH, not Zabbix
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "zabbix.get_hosts", Arguments: map[string]interface{}{}},
		map[string]string{
			"X-Incident-ID":    "incident-auth-2",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)

	if resp.Error == nil {
		t.Fatal("expected unauthorized call to be rejected")
	}
	if resp.Error.Code != InvalidRequest {
		t.Errorf("expected error code %d (InvalidRequest), got %d", InvalidRequest, resp.Error.Code)
	}
}

func TestAuthorization_NoAllowlistAllowsAll(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute command",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	// No allowlist header — gateway allows all tools when no allowlist is registered
	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ssh.execute_command", Arguments: map[string]interface{}{"command": "ls"}},
		map[string]string{
			"X-Incident-ID": "incident-no-allowlist",
		},
	)

	if resp.Error != nil {
		t.Fatalf("expected call without allowlist to succeed, got error: %s", resp.Error.Message)
	}
}

func TestAuthorization_UnauthorizedInstanceIDRejected(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute command",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	// Only instance 1 is allowed
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	// Try to use instance 99
	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ssh.execute_command", Arguments: map[string]interface{}{
			"command":          "uptime",
			"tool_instance_id": float64(99),
		}},
		map[string]string{
			"X-Incident-ID":    "incident-auth-instance",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)

	if resp.Error == nil {
		t.Fatal("expected unauthorized instance ID to be rejected")
	}
	if resp.Error.Code != InvalidRequest {
		t.Errorf("expected error code %d, got %d", InvalidRequest, resp.Error.Code)
	}
}

func TestAuthorization_NoAuthorizerAllowsAll(t *testing.T) {
	s := newTestServer()
	// No authorizer set on server

	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute command",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ssh.execute_command", Arguments: map[string]interface{}{"command": "ls"}},
		map[string]string{
			"X-Incident-ID": "incident-no-authorizer",
		},
	)

	if resp.Error != nil {
		t.Fatalf("expected call without authorizer to succeed, got error: %s", resp.Error.Message)
	}
}

func TestAuthorization_ProxyToolBypassesAllowlist(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	// Register a proxy tool with compound namespace (e.g., ext.github.create_issue)
	s.RegisterTool(Tool{
		Name:        "ext.github.create_issue",
		Description: "Create GitHub issue",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	// Set allowlist that only includes ssh — no ext.github entries
	authorizer.SetAllowlist("incident-proxy", []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	// Proxy tool should bypass the allowlist because its toolType ("ext.github")
	// contains a dot, indicating it's not managed by the skill-based assignment system.
	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ext.github.create_issue", Arguments: map[string]interface{}{"title": "test"}},
		map[string]string{
			"X-Incident-ID": "incident-proxy",
		},
	)

	if resp.Error != nil {
		t.Fatalf("expected proxy tool to bypass allowlist, got error: %s", resp.Error.Message)
	}
}

// --- Discovery filtering by allowlist tests ---

func TestHandleSearchTools_FilteredByAllowlist_PartialAuthorization(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
			{Name: "zabbix.get_hosts", Description: "Get hosts", ToolType: "zabbix"},
			{Name: "victoria_metrics.instant_query", Description: "Query metrics", ToolType: "victoria_metrics"},
		},
	})
	s.SetInstanceLookup(func(toolType string) []ToolDetailInstance {
		switch toolType {
		case "ssh":
			return []ToolDetailInstance{
				{ID: 1, LogicalName: "prod-ssh", Name: "Production SSH"},
				{ID: 2, LogicalName: "staging-ssh", Name: "Staging SSH"},
			}
		case "zabbix":
			return []ToolDetailInstance{
				{ID: 3, LogicalName: "prod-zabbix", Name: "Production Zabbix"},
			}
		case "victoria_metrics":
			return []ToolDetailInstance{
				{ID: 4, LogicalName: "prod-vm", Name: "Production VM"},
			}
		}
		return nil
	})

	// Only allow SSH (instance 1) and Zabbix (instance 3)
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 3, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/search",
		SearchToolsParams{Query: ""},
		map[string]string{
			"X-Incident-ID":    "incident-filter-1",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	// Should only return ssh and zabbix, not victoriametrics
	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result.Tools))
	}
	toolTypes := map[string]bool{}
	for _, tool := range result.Tools {
		toolTypes[tool.ToolType] = true
	}
	if toolTypes["victoria_metrics"] {
		t.Error("victoria_metrics should be excluded from results")
	}
	if !toolTypes["ssh"] || !toolTypes["zabbix"] {
		t.Error("ssh and zabbix should be included")
	}

	// SSH instances should be filtered: only prod-ssh, not staging-ssh
	for _, tool := range result.Tools {
		if tool.ToolType == "ssh" {
			if len(tool.Instances) != 1 {
				t.Fatalf("expected 1 ssh instance, got %d", len(tool.Instances))
			}
			if tool.Instances[0] != "prod-ssh" {
				t.Errorf("expected instance 'prod-ssh', got %q", tool.Instances[0])
			}
		}
	}
}

func TestHandleSearchTools_FilteredByAllowlist_NoAuthorization(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
			{Name: "zabbix.get_hosts", Description: "Get hosts", ToolType: "zabbix"},
		},
	})

	// Empty allowlist = nothing authorized
	allowlist := []auth.AllowlistEntry{}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/search",
		SearchToolsParams{Query: ""},
		map[string]string{
			"X-Incident-ID":    "incident-filter-empty",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools with empty allowlist, got %d", len(result.Tools))
	}
}

func TestHandleSearchTools_FilteredByAllowlist_FullAuthorization(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
			{Name: "zabbix.get_hosts", Description: "Get hosts", ToolType: "zabbix"},
		},
	})

	// All types authorized
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/search",
		SearchToolsParams{Query: ""},
		map[string]string{
			"X-Incident-ID":    "incident-filter-full",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 2 {
		t.Errorf("expected 2 tools with full authorization, got %d", len(result.Tools))
	}
}

func TestHandleSearchTools_NoAllowlistReturnsAll(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
			{Name: "zabbix.get_hosts", Description: "Get hosts", ToolType: "zabbix"},
		},
	})

	// No allowlist header — returns all tools when no allowlist is registered
	resp := sendJSONRPCWithHeaders(t, s, "tools/search",
		SearchToolsParams{Query: ""},
		map[string]string{
			"X-Incident-ID": "incident-no-filter",
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 2 {
		t.Errorf("expected 2 tools without allowlist, got %d", len(result.Tools))
	}
}

func TestHandleGetToolDetail_FilteredByAllowlist(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		detail: &GetToolDetailResult{
			Name:        "ssh.execute_command",
			Description: "Execute command",
			ToolType:    "ssh",
			InputSchema: InputSchema{Type: "object"},
		},
	})
	s.SetInstanceLookup(func(toolType string) []ToolDetailInstance {
		if toolType == "ssh" {
			return []ToolDetailInstance{
				{ID: 1, LogicalName: "prod-ssh", Name: "Production SSH"},
				{ID: 2, LogicalName: "staging-ssh", Name: "Staging SSH"},
				{ID: 3, LogicalName: "dev-ssh", Name: "Dev SSH"},
			}
		}
		return nil
	})

	// Only allow instance 1 and 3
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 3, LogicalName: "dev-ssh", ToolType: "ssh"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/detail",
		GetToolDetailParams{ToolName: "ssh.execute_command"},
		map[string]string{
			"X-Incident-ID":    "incident-detail-filter",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result GetToolDetailResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(result.Instances))
	}
	names := map[string]bool{}
	for _, inst := range result.Instances {
		names[inst.LogicalName] = true
	}
	if names["staging-ssh"] {
		t.Error("staging-ssh should be filtered out")
	}
	if !names["prod-ssh"] || !names["dev-ssh"] {
		t.Error("prod-ssh and dev-ssh should be included")
	}
}

func TestHandleGetToolDetail_NoAllowlistReturnsAllInstances(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		detail: &GetToolDetailResult{
			Name:        "ssh.execute_command",
			Description: "Execute command",
			ToolType:    "ssh",
			InputSchema: InputSchema{Type: "object"},
		},
	})
	s.SetInstanceLookup(func(toolType string) []ToolDetailInstance {
		if toolType == "ssh" {
			return []ToolDetailInstance{
				{ID: 1, LogicalName: "prod-ssh", Name: "Production SSH"},
				{ID: 2, LogicalName: "staging-ssh", Name: "Staging SSH"},
			}
		}
		return nil
	})

	// No allowlist — returns all instances when no allowlist is registered
	resp := sendJSONRPCWithHeaders(t, s, "tools/detail",
		GetToolDetailParams{ToolName: "ssh.execute_command"},
		map[string]string{
			"X-Incident-ID": "incident-detail-no-filter",
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result GetToolDetailResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Instances) != 2 {
		t.Errorf("expected 2 instances without allowlist, got %d", len(result.Instances))
	}
}

// --- tools/list_types tests ---

func TestHandleListToolTypes_ReturnsTypes(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		availableTypes: []string{"ssh", "zabbix", "victoria_metrics"},
	})

	resp := sendJSONRPC(t, s, "tools/list_types", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result ListToolTypesResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(result.Types))
	}
}

func TestHandleListToolTypes_FilteredByAllowlist(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		availableTypes: []string{"ssh", "zabbix", "victoria_metrics"},
	})

	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "prod-vm", ToolType: "victoria_metrics"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/list_types", nil,
		map[string]string{
			"X-Incident-ID":    "incident-list-types",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result ListToolTypesResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(result.Types))
	}
	typeSet := map[string]bool{}
	for _, tp := range result.Types {
		typeSet[tp] = true
	}
	if typeSet["zabbix"] {
		t.Error("zabbix should be filtered out")
	}
	if !typeSet["ssh"] || !typeSet["victoria_metrics"] {
		t.Error("ssh and victoria_metrics should be included")
	}
}

func TestHandleListToolTypes_NoAllowlistReturnsAll(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		availableTypes: []string{"ssh", "zabbix"},
	})

	resp := sendJSONRPCWithHeaders(t, s, "tools/list_types", nil,
		map[string]string{
			"X-Incident-ID": "incident-list-types-no-filter",
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result ListToolTypesResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Types) != 2 {
		t.Errorf("expected 2 types without allowlist, got %d", len(result.Types))
	}
}

func TestHandleListToolTypes_NoDiscoverer(t *testing.T) {
	s := newTestServer()

	resp := sendJSONRPC(t, s, "tools/list_types", nil)
	if resp.Error == nil {
		t.Fatal("expected error when discoverer not set")
	}
	if resp.Error.Code != InternalError {
		t.Errorf("expected error code %d, got %d", InternalError, resp.Error.Code)
	}
}

func TestHandleListToolTypes_EmptyTypes(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		availableTypes: nil,
	})

	resp := sendJSONRPC(t, s, "tools/list_types", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result ListToolTypesResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Types) != 0 {
		t.Errorf("expected 0 types, got %d", len(result.Types))
	}
}

func TestHandleSearchTools_EmptyResultsWithHint(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		tools:          nil,
		availableTypes: []string{"ssh", "victoria_metrics", "zabbix"},
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "prometheus"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
	if result.Hint == "" {
		t.Fatal("expected hint when no results found")
	}
	if !strings.Contains(result.Hint, "prometheus") {
		t.Errorf("hint should mention the query, got: %s", result.Hint)
	}
	if !strings.Contains(result.Hint, "victoria_metrics") {
		t.Errorf("hint should list available types, got: %s", result.Hint)
	}
}

func TestHandleSearchTools_EmptyResultsHintRespectsAllowlist(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.SetDiscoverer(&mockDiscoverer{
		tools:          nil,
		availableTypes: []string{"ssh", "victoria_metrics", "zabbix"},
	})

	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-vm", ToolType: "victoria_metrics"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	resp := sendJSONRPCWithHeaders(t, s, "tools/search",
		SearchToolsParams{Query: "nonexistent"},
		map[string]string{
			"X-Incident-ID":    "incident-hint-filter",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if result.Hint == "" {
		t.Fatal("expected hint")
	}
	if strings.Contains(result.Hint, "ssh") {
		t.Errorf("hint should not include unauthorized type 'ssh', got: %s", result.Hint)
	}
	if !strings.Contains(result.Hint, "victoria_metrics") {
		t.Errorf("hint should include authorized type 'victoria_metrics', got: %s", result.Hint)
	}
}

func TestHandleSearchTools_NonEmptyResultsNoHint(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		tools: []SearchToolsResultItem{
			{Name: "ssh.execute_command", Description: "Execute command", ToolType: "ssh"},
		},
		availableTypes: []string{"ssh", "zabbix"},
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: "ssh"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if result.Hint != "" {
		t.Errorf("expected no hint when results are found, got: %s", result.Hint)
	}
}

func TestHandleSearchTools_EmptyQueryNoHint(t *testing.T) {
	s := newTestServer()
	s.SetDiscoverer(&mockDiscoverer{
		tools:          nil,
		availableTypes: []string{"ssh"},
	})

	resp := sendJSONRPC(t, s, "tools/search", SearchToolsParams{Query: ""})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result SearchToolsResult
	json.Unmarshal(resultBytes, &result)

	if result.Hint != "" {
		t.Errorf("expected no hint for empty query, got: %s", result.Hint)
	}
}

func TestAuthorization_AllowlistPersistsAcrossRequests(t *testing.T) {
	s := newTestServer()
	authorizer := auth.NewAuthorizer(time.Hour)
	defer authorizer.Stop()
	s.SetAuthorizer(authorizer)

	s.RegisterTool(Tool{
		Name:        "ssh.execute_command",
		Description: "Execute command",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)
	s.RegisterTool(Tool{
		Name:        "zabbix.get_hosts",
		Description: "Get hosts",
		InputSchema: InputSchema{Type: "object"},
	}, echoHandler)

	// First request: set allowlist via header
	allowlist := []auth.AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	}
	allowlistJSON, _ := json.Marshal(allowlist)

	sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "ssh.execute_command", Arguments: map[string]interface{}{"command": "ls"}},
		map[string]string{
			"X-Incident-ID":    "incident-persist",
			"X-Tool-Allowlist": string(allowlistJSON),
		},
	)

	// Second request: no allowlist header, but the stored allowlist should still be enforced
	resp := sendJSONRPCWithHeaders(t, s, "tools/call",
		CallToolParams{Name: "zabbix.get_hosts", Arguments: map[string]interface{}{}},
		map[string]string{
			"X-Incident-ID": "incident-persist",
		},
	)

	if resp.Error == nil {
		t.Fatal("expected stored allowlist to reject unauthorized tool type on subsequent request")
	}
	if resp.Error.Code != InvalidRequest {
		t.Errorf("expected error code %d, got %d", InvalidRequest, resp.Error.Code)
	}
}
