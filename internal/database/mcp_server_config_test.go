package database

import (
	"strings"
	"testing"
)

func validRemoteMCPConfig() *MCPServerConfig {
	return &MCPServerConfig{
		Name:            "github-mcp",
		Transport:       MCPServerTransportHTTP,
		URL:             "https://mcp.example.com/mcp",
		NamespacePrefix: "ext.github",
	}
}

func TestMCPServerConfig_Validate_RemoteTransports(t *testing.T) {
	for _, tr := range []MCPServerTransport{MCPServerTransportHTTP, MCPServerTransportSSE} {
		c := validRemoteMCPConfig()
		c.Transport = tr
		if err := c.Validate(); err != nil {
			t.Errorf("transport %q: expected no error, got %v", tr, err)
		}
	}
	c := validRemoteMCPConfig()
	c.URL = "http://10.0.0.5:8080/mcp"
	if err := c.Validate(); err != nil {
		t.Errorf("plain http URL should be valid, got %v", err)
	}
}

func TestMCPServerConfig_Validate_Errors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MCPServerConfig)
		want   string
	}{
		{"missing name", func(c *MCPServerConfig) { c.Name = "  " }, "name is required"},
		{"missing namespace", func(c *MCPServerConfig) { c.NamespacePrefix = "" }, "namespace_prefix"},
		{"upper-case namespace", func(c *MCPServerConfig) { c.NamespacePrefix = "Ext.GitHub" }, "namespace_prefix"},
		{"namespace with dash", func(c *MCPServerConfig) { c.NamespacePrefix = "ext.git-hub" }, "namespace_prefix"},
		{"namespace trailing dot", func(c *MCPServerConfig) { c.NamespacePrefix = "ext." }, "namespace_prefix"},
		{"stdio", func(c *MCPServerConfig) { c.Transport = MCPServerTransportStdio; c.URL = ""; c.Command = "/bin/mcp" }, "stdio MCP servers are not supported"},
		{"unknown transport", func(c *MCPServerConfig) { c.Transport = "grpc" }, "transport must be 'http'"},
		{"empty transport", func(c *MCPServerConfig) { c.Transport = "" }, "transport must be 'http'"},
		{"command on http", func(c *MCPServerConfig) { c.Command = "/bin/sh" }, "command, args and env_vars are not supported"},
		{"args on http", func(c *MCPServerConfig) { c.Args = JSONB{"args": []interface{}{"x"}} }, "command, args and env_vars are not supported"},
		{"env on http", func(c *MCPServerConfig) { c.EnvVars = JSONB{"K": "v"} }, "command, args and env_vars are not supported"},
		{"missing url", func(c *MCPServerConfig) { c.URL = "" }, "absolute http"},
		{"relative url", func(c *MCPServerConfig) { c.URL = "/mcp" }, "absolute http"},
		{"ftp url", func(c *MCPServerConfig) { c.URL = "ftp://example.com/mcp" }, "absolute http"},
		{"no host", func(c *MCPServerConfig) { c.URL = "https:///mcp" }, "absolute http"},
		{"credentials in url", func(c *MCPServerConfig) { c.URL = "https://user:pass@example.com/mcp" }, "must not contain credentials"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validRemoteMCPConfig()
			tt.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestMCPServerConfig_Validate_NamespaceShapes(t *testing.T) {
	for _, ns := range []string{"github", "ext.github", "ext.github_issues", "a.b.c", "x1_2"} {
		c := validRemoteMCPConfig()
		c.NamespacePrefix = ns
		if err := c.Validate(); err != nil {
			t.Errorf("namespace %q: unexpected error %v", ns, err)
		}
	}
}

func TestStringList_ScanValue(t *testing.T) {
	var l StringList
	if err := l.Scan([]byte(`["a","b"]`)); err != nil || len(l) != 2 || l[1] != "b" {
		t.Fatalf("Scan bytes: %v %v", l, err)
	}
	if err := l.Scan(`["c"]`); err != nil || len(l) != 1 || l[0] != "c" {
		t.Fatalf("Scan string: %v %v", l, err)
	}
	for _, v := range []interface{}{nil, []byte("null"), []byte("")} {
		if err := l.Scan(v); err != nil || l != nil {
			t.Fatalf("Scan %v: want nil list, got %v %v", v, l, err)
		}
	}
	if err := l.Scan(42); err == nil {
		t.Fatal("Scan int: want error")
	}
	if v, err := StringList(nil).Value(); err != nil || v != nil {
		t.Fatalf("nil Value = %v %v", v, err)
	}
	v, err := StringList{"x"}.Value()
	if err != nil || string(v.([]byte)) != `["x"]` {
		t.Fatalf("Value = %v %v", v, err)
	}
}

func TestFlexTime_Scan(t *testing.T) {
	cases := []interface{}{
		"2026-11-01T10:00:00Z",
		"2026-11-01 10:00:00.123456789+00:00",
		"2026-11-01 10:00:00",
		[]byte("2026-11-01 10:00:00.5"),
	}
	for _, c := range cases {
		var f FlexTime
		if err := f.Scan(c); err != nil {
			t.Errorf("Scan(%v): %v", c, err)
			continue
		}
		if f.Time.Year() != 2026 || f.Time.Hour() != 10 {
			t.Errorf("Scan(%v) = %v", c, f.Time)
		}
	}
	var f FlexTime
	if err := f.Scan(nil); err != nil || !f.Time.IsZero() {
		t.Errorf("Scan(nil) = %v %v", f.Time, err)
	}
	if err := f.Scan("yesterday"); err == nil {
		t.Error("Scan(garbage): want error")
	}
	if err := f.Scan(3.5); err == nil {
		t.Error("Scan(float): want error")
	}
}
