package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupToolTestDB creates an in-memory SQLite database with tool-related tables
func setupToolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.ToolType{},
		&database.ToolInstance{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	database.DB = db
	return db
}

func TestSlugifyLogicalName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple lowercase", "prod-ssh", "prod-ssh"},
		{"mixed case", "Production Zabbix", "production-zabbix"},
		{"special chars", "My Tool (v2.0)!", "my-tool-v2-0"},
		{"multiple spaces", "foo   bar", "foo-bar"},
		{"leading/trailing special", "---test---", "test"},
		{"empty string", "", ""},
		{"numbers", "server123", "server123"},
		{"unicode", "café-server", "caf-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := database.SlugifyLogicalName(tt.input)
			if got != tt.expected {
				t.Errorf("database.SlugifyLogicalName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCreateToolInstance_SetsLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	// Create a tool type
	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	if instance.LogicalName != "production-ssh" {
		t.Errorf("expected logical_name 'production-ssh', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_LogicalNameUnique(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}

	_, err := svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err != nil {
		t.Fatalf("first CreateToolInstance failed: %v", err)
	}

	// Second instance with same name should fail due to unique constraint on Name
	_, err = svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err == nil {
		t.Error("expected error for duplicate logical name, got nil")
	}
}

func TestUpdateToolInstance_UpdatesLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "zabbix", Description: "Zabbix"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Old Name", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	if instance.LogicalName != "old-name" {
		t.Fatalf("expected logical_name 'old-name', got %q", instance.LogicalName)
	}

	err = svc.UpdateToolInstance(instance.ID, "New Name", "", nil, true)
	if err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	updated, err := svc.GetToolInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetToolInstance failed: %v", err)
	}

	if updated.LogicalName != "new-name" {
		t.Errorf("expected logical_name 'new-name', got %q", updated.LogicalName)
	}
}

func TestLogicalName_ExposedInListResponse(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	_, err := svc.CreateToolInstance(toolType.ID, "My Server", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	instances, err := svc.ListToolInstances()
	if err != nil {
		t.Fatalf("ListToolInstances failed: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	if instances[0].LogicalName != "my-server" {
		t.Errorf("expected logical_name 'my-server', got %q", instances[0].LogicalName)
	}
}

func TestCreateToolInstance_HonorsProvidedLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Production SSH Server", "prod-ssh", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	// User-supplied logical_name is sanitized via SlugifyLogicalName, preserving valid slugs.
	if instance.LogicalName != "prod-ssh" {
		t.Errorf("expected logical_name 'prod-ssh', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_SanitizesUnsafeLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	// A logical name with quotes, backticks, and newlines must be sanitized.
	instance, err := svc.CreateToolInstance(toolType.ID, "Test Server", "evil\"`name\nnewline", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	// SlugifyLogicalName strips non-alphanumeric chars, so the result should be safe.
	if instance.LogicalName != "evil-name-newline" {
		t.Errorf("expected sanitized logical_name 'evil-name-newline', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_InvalidToolTypeID(t *testing.T) {
	setupToolTestDB(t)

	svc := &ToolService{db: database.GetDB()}

	// Use a tool type ID that doesn't exist
	_, err := svc.CreateToolInstance(99999, "Test Instance", "", nil)
	if err == nil {
		t.Error("expected error for invalid tool_type_id, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
	}
}

func TestUpdateToolInstance_HonorsProvidedLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "zabbix", Description: "Zabbix"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Old Name", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	err = svc.UpdateToolInstance(instance.ID, "New Name", "custom-logical", nil, true)
	if err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	updated, err := svc.GetToolInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetToolInstance failed: %v", err)
	}

	// User-supplied logical_name is sanitized via SlugifyLogicalName, preserving valid slugs.
	if updated.LogicalName != "custom-logical" {
		t.Errorf("expected logical_name 'custom-logical', got %q", updated.LogicalName)
	}
}
