package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockDiscoverer implements ToolDiscoverer for testing
type mockDiscoverer struct {
	tools  []SearchToolsResultItem
	detail *GetToolDetailResult
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

func newTestServer() *Server {
	return NewServer("test", "1.0.0", nil)
}

func sendJSONRPC(t *testing.T, server *Server, method string, params interface{}) Response {
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
