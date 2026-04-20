package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupHTTPConnectorTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	if err := db.AutoMigrate(&database.HTTPConnector{}, &database.MCPServerConfig{}); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	database.DB = db
	return db
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
