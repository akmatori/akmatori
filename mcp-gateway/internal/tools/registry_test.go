package tools

import (
	"io"
	"log"
	"testing"

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

func TestSearchTools_QueryMatching(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("ssh", "")
	if len(results) != 2 {
		t.Fatalf("expected 2 SSH tools, got %d", len(results))
	}
	for _, r := range results {
		if r.ToolType != "ssh" {
			t.Errorf("expected tool_type 'ssh', got %q", r.ToolType)
		}
	}
}

func TestSearchTools_EmptyResults(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("nonexistent_tool_xyz", "")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchTools_TypeFilter(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("", "zabbix")
	if len(results) != 2 {
		t.Fatalf("expected 2 Zabbix tools, got %d", len(results))
	}
	for _, r := range results {
		if r.ToolType != "zabbix" {
			t.Errorf("expected tool_type 'zabbix', got %q", r.ToolType)
		}
	}
}

func TestSearchTools_QueryAndTypeFilter(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("hosts", "zabbix")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "zabbix.get_hosts" {
		t.Errorf("expected 'zabbix.get_hosts', got %q", results[0].Name)
	}
}

func TestSearchTools_CaseInsensitive(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("SSH", "")
	if len(results) != 2 {
		t.Fatalf("expected 2 results for case-insensitive 'SSH', got %d", len(results))
	}
}

func TestSearchTools_MatchesDescription(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("monitoring", "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result matching description, got %d", len(results))
	}
	if results[0].Name != "zabbix.get_hosts" {
		t.Errorf("expected 'zabbix.get_hosts', got %q", results[0].Name)
	}
}

func TestSearchTools_EmptyQuery_ReturnsAll(t *testing.T) {
	registry, _ := newTestRegistry()

	results := registry.SearchTools("", "")
	if len(results) != 4 {
		t.Fatalf("expected 4 results for empty query, got %d", len(results))
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
