package database

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	err = db.AutoMigrate(&ToolType{}, &ToolInstance{}, &Skill{})
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	DB = db
}

func seedToolInstance(t *testing.T, name, logicalName, toolTypeName string, enabled bool) *ToolInstance {
	t.Helper()
	var tt ToolType
	if err := DB.Where("name = ?", toolTypeName).First(&tt).Error; err != nil {
		tt = ToolType{Name: toolTypeName, Description: toolTypeName + " tool"}
		if err := DB.Create(&tt).Error; err != nil {
			t.Fatalf("failed to create tool type: %v", err)
		}
	}
	inst := &ToolInstance{
		ToolTypeID:  tt.ID,
		Name:        name,
		LogicalName: logicalName,
		Settings:    JSONB{"url": "http://example.com"},
		Enabled:     enabled,
	}
	if err := DB.Create(inst).Error; err != nil {
		t.Fatalf("failed to create tool instance: %v", err)
	}
	// GORM's default:true tag causes false to be ignored on Create, so explicitly update
	if !enabled {
		if err := DB.Model(inst).Update("enabled", false).Error; err != nil {
			t.Fatalf("failed to disable tool instance: %v", err)
		}
	}
	return inst
}

func TestGetToolCredentialsByLogicalName_HappyPath(t *testing.T) {
	setupTestDB(t)
	seedToolInstance(t, "Production SSH", "prod-ssh", "ssh", true)

	ctx := context.Background()
	creds, err := GetToolCredentialsByLogicalName(ctx, "prod-ssh", "ssh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.LogicalName != "prod-ssh" {
		t.Errorf("expected logical_name 'prod-ssh', got %q", creds.LogicalName)
	}
	if creds.ToolType != "ssh" {
		t.Errorf("expected tool_type 'ssh', got %q", creds.ToolType)
	}
}

func TestGetToolCredentialsByLogicalName_NotFound(t *testing.T) {
	setupTestDB(t)

	ctx := context.Background()
	_, err := GetToolCredentialsByLogicalName(ctx, "nonexistent", "ssh")
	if err == nil {
		t.Fatal("expected error for nonexistent logical name, got nil")
	}
}

func TestGetToolCredentialsByLogicalName_TypeMismatch(t *testing.T) {
	setupTestDB(t)
	seedToolInstance(t, "Production SSH", "prod-ssh", "ssh", true)

	ctx := context.Background()
	_, err := GetToolCredentialsByLogicalName(ctx, "prod-ssh", "zabbix")
	if err == nil {
		t.Fatal("expected error for type mismatch, got nil")
	}
}

func TestGetToolCredentialsByLogicalName_DisabledInstance(t *testing.T) {
	setupTestDB(t)
	seedToolInstance(t, "Disabled SSH", "disabled-ssh", "ssh", false)

	ctx := context.Background()
	_, err := GetToolCredentialsByLogicalName(ctx, "disabled-ssh", "ssh")
	if err == nil {
		t.Fatal("expected error for disabled instance, got nil")
	}
}

func TestResolveToolCredentials_PriorityInstanceID(t *testing.T) {
	setupTestDB(t)
	inst := seedToolInstance(t, "SSH by ID", "ssh-by-id", "ssh", true)
	seedToolInstance(t, "SSH by Name", "ssh-by-name", "ssh", true)

	ctx := context.Background()
	id := inst.ID
	creds, err := ResolveToolCredentials(ctx, "incident-1", "ssh", &id, "ssh-by-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Instance ID takes priority over logical name
	if creds.InstanceID != inst.ID {
		t.Errorf("expected instance ID %d, got %d", inst.ID, creds.InstanceID)
	}
}

func TestResolveToolCredentials_FallbackToLogicalName(t *testing.T) {
	setupTestDB(t)
	seedToolInstance(t, "SSH Named", "ssh-named", "ssh", true)

	ctx := context.Background()
	creds, err := ResolveToolCredentials(ctx, "incident-1", "ssh", nil, "ssh-named")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.LogicalName != "ssh-named" {
		t.Errorf("expected logical_name 'ssh-named', got %q", creds.LogicalName)
	}
}

func TestResolveToolCredentials_FallbackToTypeDefault(t *testing.T) {
	setupTestDB(t)
	seedToolInstance(t, "Default SSH", "default-ssh", "ssh", true)

	ctx := context.Background()
	creds, err := ResolveToolCredentials(ctx, "incident-1", "ssh", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.ToolType != "ssh" {
		t.Errorf("expected tool_type 'ssh', got %q", creds.ToolType)
	}
}
