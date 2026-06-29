package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

func setupHTTPConnectorTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	return testhelpers.NewGlobalSQLiteDB(t, &database.HTTPConnector{}, &database.MCPServerConfig{})
}

func validHTTPConnector() *database.HTTPConnector {
	return &database.HTTPConnector{
		ToolTypeName: "internal-billing",
		Description:  "Internal billing API",
		BaseURLField: "base_url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "get_invoice",
					"http_method": "GET",
					"path":        "/invoices/{{invoice_id}}",
					"params": []interface{}{
						map[string]interface{}{"name": "invoice_id", "type": "string", "required": true, "in": "path"},
					},
				},
			},
		},
	}
}

func TestCreateHTTPConnector_RejectsReservedNamespace(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	connector := validHTTPConnector()
	connector.ToolTypeName = "grafana"

	_, err := svc.CreateHTTPConnector(connector)
	if err == nil {
		t.Fatal("expected reserved namespace error, got nil")
	}
	if !strings.Contains(err.Error(), "built-in tool namespace") {
		t.Fatalf("expected built-in namespace error, got %v", err)
	}
}

func TestCreateHTTPConnector_RejectsMCPNamespaceCollision(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	if err := db.Create(&database.MCPServerConfig{
		Name:            "GitHub MCP",
		Transport:       database.MCPServerTransportSSE,
		URL:             "https://example.com/sse",
		NamespacePrefix: "github-ext",
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("failed to seed MCP server: %v", err)
	}

	svc := &HTTPConnectorService{db: db}
	connector := validHTTPConnector()
	connector.ToolTypeName = "github-ext"

	_, err := svc.CreateHTTPConnector(connector)
	if err == nil {
		t.Fatal("expected MCP namespace collision error, got nil")
	}
	if !strings.Contains(err.Error(), "existing MCP server namespace") {
		t.Fatalf("expected MCP collision error, got %v", err)
	}
}

func TestCreateHTTPConnector_EnablesConnectorAndPersists(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	connector := validHTTPConnector()
	connector.Enabled = false

	created, err := svc.CreateHTTPConnector(connector)
	if err != nil {
		t.Fatalf("CreateHTTPConnector failed: %v", err)
	}
	if !created.Enabled {
		t.Fatal("expected connector to be enabled on create")
	}

	stored, err := svc.GetHTTPConnector(created.ID)
	if err != nil {
		t.Fatalf("GetHTTPConnector failed: %v", err)
	}
	if stored.ToolTypeName != connector.ToolTypeName {
		t.Fatalf("expected tool_type_name %q, got %q", connector.ToolTypeName, stored.ToolTypeName)
	}
}

func TestUpdateHTTPConnector_RejectsDuplicateToolTypeName(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	first := validHTTPConnector()
	first.ToolTypeName = "billing"
	createdFirst, err := svc.CreateHTTPConnector(first)
	if err != nil {
		t.Fatalf("CreateHTTPConnector(first) failed: %v", err)
	}

	second := validHTTPConnector()
	second.ToolTypeName = "crm"
	createdSecond, err := svc.CreateHTTPConnector(second)
	if err != nil {
		t.Fatalf("CreateHTTPConnector(second) failed: %v", err)
	}

	_, err = svc.UpdateHTTPConnector(createdSecond.ID, map[string]interface{}{"tool_type_name": createdFirst.ToolTypeName})
	if err == nil {
		t.Fatal("expected duplicate tool_type_name error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate name error, got %v", err)
	}
}

func TestUpdateHTTPConnector_RejectsReservedNamespaceOnRename(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	created, err := svc.CreateHTTPConnector(validHTTPConnector())
	if err != nil {
		t.Fatalf("CreateHTTPConnector failed: %v", err)
	}

	_, err = svc.UpdateHTTPConnector(created.ID, map[string]interface{}{"tool_type_name": "postgresql"})
	if err == nil {
		t.Fatal("expected reserved namespace error, got nil")
	}
	if !strings.Contains(err.Error(), "built-in tool namespace") {
		t.Fatalf("expected reserved namespace error, got %v", err)
	}
}

func TestUpdateHTTPConnector_RejectsMCPNamespaceCollisionOnRename(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	if err := db.Create(&database.MCPServerConfig{
		Name:            "GitHub MCP",
		Transport:       database.MCPServerTransportSSE,
		URL:             "https://example.com/sse",
		NamespacePrefix: "github-ext",
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("failed to seed MCP server: %v", err)
	}
	svc := &HTTPConnectorService{db: db}

	created, err := svc.CreateHTTPConnector(validHTTPConnector())
	if err != nil {
		t.Fatalf("CreateHTTPConnector failed: %v", err)
	}

	_, err = svc.UpdateHTTPConnector(created.ID, map[string]interface{}{"tool_type_name": "github-ext"})
	if err == nil {
		t.Fatal("expected MCP namespace collision error, got nil")
	}
	if !strings.Contains(err.Error(), "existing MCP server namespace") {
		t.Fatalf("expected MCP collision error, got %v", err)
	}
}

func TestUpdateHTTPConnector_UpdatesFieldsAndListReflectsChanges(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	created, err := svc.CreateHTTPConnector(validHTTPConnector())
	if err != nil {
		t.Fatalf("CreateHTTPConnector failed: %v", err)
	}

	updatedTools := database.JSONB{
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "create_invoice",
				"http_method": "POST",
				"path":        "/invoices",
			},
		},
	}
	updatedAuth := database.JSONB{
		"method":       "api_key",
		"key_name":     "X-API-Key",
		"key_location": "header",
	}
	updated, err := svc.UpdateHTTPConnector(created.ID, map[string]interface{}{
		"tool_type_name": "billing-v2",
		"description":    "Updated billing API",
		"base_url_field": "endpoint",
		"auth_config":    updatedAuth,
		"tools":          updatedTools,
		"enabled":        false,
	})
	if err != nil {
		t.Fatalf("UpdateHTTPConnector failed: %v", err)
	}
	if updated.ToolTypeName != "billing-v2" {
		t.Fatalf("expected renamed connector, got %q", updated.ToolTypeName)
	}
	if updated.Description != "Updated billing API" {
		t.Fatalf("expected updated description, got %q", updated.Description)
	}
	if updated.BaseURLField != "endpoint" {
		t.Fatalf("expected updated base_url_field, got %q", updated.BaseURLField)
	}
	if updated.Enabled {
		t.Fatal("expected connector to be disabled")
	}
	if updated.AuthConfig["method"] != "api_key" {
		t.Fatalf("expected updated auth_config, got %#v", updated.AuthConfig)
	}
	defs, err := updated.GetToolDefs()
	if err != nil {
		t.Fatalf("GetToolDefs failed: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "create_invoice" {
		t.Fatalf("expected updated tool definition, got %#v", defs)
	}

	connectors, err := svc.ListHTTPConnectors()
	if err != nil {
		t.Fatalf("ListHTTPConnectors failed: %v", err)
	}
	if len(connectors) != 1 {
		t.Fatalf("expected one connector, got %d", len(connectors))
	}
	if connectors[0].ToolTypeName != "billing-v2" {
		t.Fatalf("expected listed connector to reflect rename, got %q", connectors[0].ToolTypeName)
	}
}

func TestDeleteHTTPConnector_ReturnsNotFoundForMissingRow(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	err := svc.DeleteHTTPConnector(999)
	if err == nil {
		t.Fatal("expected not found error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}
