package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

func setupMCPServerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	return testhelpers.NewGlobalSQLiteDB(t, &database.MCPServerConfig{}, &database.HTTPConnector{})
}

func validMCPServerConfig() *database.MCPServerConfig {
	return &database.MCPServerConfig{
		Name:            "github-mcp",
		Transport:       database.MCPServerTransportSSE,
		URL:             "https://example.com/sse",
		NamespacePrefix: "github_ext",
	}
}

func TestCreateMCPServer_EnablesConfigAndPersists(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := &MCPServerService{db: db}

	config := validMCPServerConfig()
	config.Enabled = false

	created, err := svc.CreateMCPServer(config)
	if err != nil {
		t.Fatalf("CreateMCPServer failed: %v", err)
	}
	if !created.Enabled {
		t.Fatal("expected config to be enabled on create")
	}

	stored, err := svc.GetMCPServer(created.ID)
	if err != nil {
		t.Fatalf("GetMCPServer failed: %v", err)
	}
	if stored.NamespacePrefix != config.NamespacePrefix {
		t.Fatalf("NamespacePrefix = %q, want %q", stored.NamespacePrefix, config.NamespacePrefix)
	}
}

func TestCreateMCPServer_RejectsConflictingInputs(t *testing.T) {
	tests := []struct {
		name        string
		seed        func(t *testing.T, db *gorm.DB)
		mutate      func(*database.MCPServerConfig)
		wantMessage string
	}{
		{
			name:        "duplicate name",
			seed:        func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "github-mcp", "other_ns") },
			wantMessage: "already exists",
		},
		{
			name:        "reserved namespace",
			mutate:      func(c *database.MCPServerConfig) { c.NamespacePrefix = "grafana" },
			wantMessage: "built-in tool namespace",
		},
		{
			name:        "duplicate namespace",
			seed:        func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "other-mcp", "github_ext") },
			wantMessage: "namespace_prefix",
		},
		{
			name: "HTTP connector namespace collision",
			seed: func(t *testing.T, db *gorm.DB) {
				seedHTTPConnector(t, db, "github_ext")
			},
			wantMessage: "existing HTTP connector namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMCPServerTestDB(t)
			if tt.seed != nil {
				tt.seed(t, db)
			}

			config := validMCPServerConfig()
			if tt.mutate != nil {
				tt.mutate(config)
			}

			_, err := (&MCPServerService{db: db}).CreateMCPServer(config)
			if err == nil {
				t.Fatal("CreateMCPServer error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("CreateMCPServer error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestUpdateMCPServer_RejectsConflictingNamespaceRename(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := &MCPServerService{db: db}

	created, err := svc.CreateMCPServer(validMCPServerConfig())
	if err != nil {
		t.Fatalf("CreateMCPServer failed: %v", err)
	}
	seedHTTPConnector(t, db, "jira_ext")

	_, err = svc.UpdateMCPServer(created.ID, map[string]interface{}{"namespace_prefix": "jira_ext"})
	if err == nil {
		t.Fatal("UpdateMCPServer namespace collision error = nil, want error")
	}
	if !strings.Contains(err.Error(), "existing HTTP connector namespace") {
		t.Fatalf("UpdateMCPServer error = %v, want HTTP namespace collision", err)
	}
}

func TestUpdateMCPServer_PatchesFieldsAndValidates(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := &MCPServerService{db: db}

	created, err := svc.CreateMCPServer(validMCPServerConfig())
	if err != nil {
		t.Fatalf("CreateMCPServer failed: %v", err)
	}

	updated, err := svc.UpdateMCPServer(created.ID, map[string]interface{}{
		"name":             "local-mcp",
		"transport":        string(database.MCPServerTransportStdio),
		"command":          "/usr/local/bin/mcp",
		"url":              "",
		"namespace_prefix": "local_ext",
		"args":             database.JSONB{"args": []interface{}{"--stdio"}},
		"env_vars":         database.JSONB{"TOKEN": "secret"},
		"auth_config":      database.JSONB{"type": "none"},
		"enabled":          false,
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer failed: %v", err)
	}
	if updated.Name != "local-mcp" || updated.Transport != database.MCPServerTransportStdio || updated.Command != "/usr/local/bin/mcp" {
		t.Fatalf("updated config mismatch: %+v", updated)
	}
	if updated.Enabled {
		t.Fatal("expected enabled=false patch to persist")
	}
	if got := updated.Args["args"]; got == nil {
		t.Fatalf("expected args JSONB to persist, got %+v", updated.Args)
	}
}

func TestMCPServerService_NotFoundPaths(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := &MCPServerService{db: db}

	if _, err := svc.GetMCPServer(999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("GetMCPServer missing error = %v, want not found", err)
	}
	if _, err := svc.UpdateMCPServer(999, map[string]interface{}{"name": "missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("UpdateMCPServer missing error = %v, want not found", err)
	}
	if err := svc.DeleteMCPServer(999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("DeleteMCPServer missing error = %v, want not found", err)
	}
}

func TestListMCPServers_ReturnsPersistedRows(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := &MCPServerService{db: db}

	seedMCPServer(t, db, "github", "github_ext")
	seedMCPServer(t, db, "jira", "jira_ext")

	configs, err := svc.ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers failed: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("ListMCPServers returned %d rows, want 2", len(configs))
	}
}

func seedMCPServer(t *testing.T, db *gorm.DB, name, namespace string) {
	t.Helper()

	if err := db.Create(&database.MCPServerConfig{
		Name:            name,
		Transport:       database.MCPServerTransportSSE,
		URL:             "https://example.com/sse",
		NamespacePrefix: namespace,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("failed to seed MCP server: %v", err)
	}
}

func seedHTTPConnector(t *testing.T, db *gorm.DB, toolTypeName string) {
	t.Helper()

	if err := db.Create(&database.HTTPConnector{
		ToolTypeName: toolTypeName,
		BaseURLField: "base_url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{"name": "get_status", "http_method": "GET", "path": "/status"},
			},
		},
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed HTTP connector: %v", err)
	}
}
