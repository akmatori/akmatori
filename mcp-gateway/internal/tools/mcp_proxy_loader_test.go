package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcpproxy"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDefaultMCPProxyLoader_HTTPOnlyAndDisabledTools(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.MCPServerConfig{}); err != nil {
		t.Fatal(err)
	}
	prev := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prev })

	rows := []database.MCPServerConfig{
		{Name: "gh", Transport: "http", URL: "https://gh.example/mcp", NamespacePrefix: "github",
			AuthConfig:    database.JSONB{"type": "bearer", "token": "t"},
			DisabledTools: database.JSONStringList{"delete_repo"}, Enabled: true},
		{Name: "legacy", Transport: "sse", URL: "https://legacy.example/mcp", NamespacePrefix: "legacy", Enabled: true},
		{Name: "local", Transport: "stdio", Command: "/bin/sh", NamespacePrefix: "local", Enabled: true},
		{Name: "reserved", Transport: "http", URL: "https://x.example", NamespacePrefix: "ssh", Enabled: true},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	regs, err := DefaultMCPProxyLoader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 {
		t.Fatalf("expected http + legacy sse rows only, got %+v", regs)
	}
	gh := regs[0]
	if gh.NamespacePrefix != "github" || gh.Config.Transport != mcpproxy.TransportHTTP {
		t.Errorf("unexpected registration %+v", gh)
	}
	if len(gh.DisabledTools) != 1 || gh.DisabledTools[0] != "delete_repo" {
		t.Errorf("disabled_tools not loaded: %v", gh.DisabledTools)
	}
	var auth map[string]string
	if err := json.Unmarshal(gh.Config.AuthConfig, &auth); err != nil || auth["token"] != "t" {
		t.Errorf("auth_config not loaded: %s", gh.Config.AuthConfig)
	}
	if regs[1].Config.Transport != mcpproxy.TransportSSE {
		t.Errorf("legacy sse row must load, got %+v", regs[1])
	}

	stored, err := LoadStoredMCPServerConfig(context.Background(), rows[0].ID)
	if err != nil || stored.URL != "https://gh.example/mcp" {
		t.Errorf("LoadStoredMCPServerConfig = %+v, %v", stored, err)
	}
}
