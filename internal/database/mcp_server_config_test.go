package database

import (
	"testing"
)

func TestMCPServerConfig_Validate_ValidSSE(t *testing.T) {
	config := &MCPServerConfig{
		Name:            "github-mcp",
		Transport:       MCPServerTransportSSE,
		URL:             "http://localhost:8080/mcp",
		NamespacePrefix: "ext.github",
	}

	if err := config.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestMCPServerConfig_Validate_ValidStdio(t *testing.T) {
	config := &MCPServerConfig{
		Name:            "local-tool",
		Transport:       MCPServerTransportStdio,
		Command:         "/usr/local/bin/my-mcp-server",
		NamespacePrefix: "ext.local",
	}

	if err := config.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestMCPServerConfig_Validate_MissingName(t *testing.T) {
	config := &MCPServerConfig{
		Transport:       MCPServerTransportSSE,
		URL:             "http://localhost/mcp",
		NamespacePrefix: "ext.test",
	}

	err := config.Validate()
	if err == nil {
		t.Error("expected error for missing name")
	}
	if err.Error() != "name is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPServerConfig_Validate_MissingNamespacePrefix(t *testing.T) {
	config := &MCPServerConfig{
		Name:      "test",
		Transport: MCPServerTransportSSE,
		URL:       "http://localhost/mcp",
	}

	err := config.Validate()
	if err == nil {
		t.Error("expected error for missing namespace_prefix")
	}
	if err.Error() != "namespace_prefix is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPServerConfig_Validate_InvalidTransport(t *testing.T) {
	config := &MCPServerConfig{
		Name:            "test",
		Transport:       MCPServerTransport("invalid"),
		NamespacePrefix: "ext.test",
	}

	err := config.Validate()
	if err == nil {
		t.Error("expected error for invalid transport")
	}
	if err.Error() != "transport must be 'sse' or 'stdio'" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPServerConfig_Validate_SSE_MissingURL(t *testing.T) {
	config := &MCPServerConfig{
		Name:            "test",
		Transport:       MCPServerTransportSSE,
		NamespacePrefix: "ext.test",
	}

	err := config.Validate()
	if err == nil {
		t.Error("expected error for missing URL with SSE transport")
	}
	if err.Error() != "url is required for SSE transport" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPServerConfig_Validate_Stdio_MissingCommand(t *testing.T) {
	config := &MCPServerConfig{
		Name:            "test",
		Transport:       MCPServerTransportStdio,
		NamespacePrefix: "ext.test",
	}

	err := config.Validate()
	if err == nil {
		t.Error("expected error for missing command with stdio transport")
	}
	if err.Error() != "command is required for stdio transport" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPServerConfig_TableName(t *testing.T) {
	config := MCPServerConfig{}
	if config.TableName() != "mcp_server_configs" {
		t.Errorf("expected table name 'mcp_server_configs', got %q", config.TableName())
	}
}

func TestMCPServerConfig_Validate_AllTransportTypes(t *testing.T) {
	tests := []struct {
		name      string
		transport MCPServerTransport
		wantErr   bool
	}{
		{"sse", MCPServerTransportSSE, false},
		{"stdio", MCPServerTransportStdio, false},
		{"empty", MCPServerTransport(""), true},
		{"unknown", MCPServerTransport("grpc"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &MCPServerConfig{
				Name:            "test",
				Transport:       tt.transport,
				URL:             "http://localhost/mcp",
				Command:         "/usr/bin/test",
				NamespacePrefix: "ext.test",
			}
			err := config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
