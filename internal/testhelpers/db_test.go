package testhelpers

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestNewSQLiteDB_MigratesModels(t *testing.T) {
	db := NewSQLiteDB(t, &database.HTTPConnector{})

	connector := database.HTTPConnector{
		ToolTypeName: "billing-api",
		Description:  "Billing API",
		BaseURLField: "base_url",
		Tools:        database.JSONB{"tools": []interface{}{}},
		Enabled:      true,
	}
	if err := db.Create(&connector).Error; err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	var got database.HTTPConnector
	if err := db.First(&got, "tool_type_name = ?", "billing-api").Error; err != nil {
		t.Fatalf("failed to read connector: %v", err)
	}
	if got.Description != "Billing API" {
		t.Fatalf("expected migrated connector to round-trip, got description %q", got.Description)
	}
}

func TestNewSQLiteDB_IsolatesTestsByName(t *testing.T) {
	t.Run("first", func(t *testing.T) {
		db := NewSQLiteDB(t, &database.SystemSetting{})
		if err := db.Create(&database.SystemSetting{Key: "shared-key", Value: "first"}).Error; err != nil {
			t.Fatalf("failed to seed setting: %v", err)
		}
	})

	t.Run("second", func(t *testing.T) {
		db := NewSQLiteDB(t, &database.SystemSetting{})
		var count int64
		if err := db.Model(&database.SystemSetting{}).Where("key = ?", "shared-key").Count(&count).Error; err != nil {
			t.Fatalf("failed to count settings: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected isolated database, found %d leaked settings", count)
		}
	})
}

func TestNewGlobalSQLiteDB_RestoresDatabaseHandle(t *testing.T) {
	previous := database.DB

	t.Run("sets global db", func(t *testing.T) {
		db := NewGlobalSQLiteDB(t, &database.SystemSetting{})
		if database.DB != db {
			t.Fatal("expected helper to assign database.DB")
		}
	})

	if database.DB != previous {
		t.Fatal("expected helper cleanup to restore database.DB")
	}
}

func TestNewCronSQLiteDB_MigratesCronToolJoinTable(t *testing.T) {
	db := NewCronSQLiteDB(t, &database.ToolType{}, &database.ToolInstance{})

	toolType := database.ToolType{Name: "kubectl", Description: "kubectl wrapper"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to seed tool type: %v", err)
	}

	tool := database.ToolInstance{
		ToolTypeID:  toolType.ID,
		Name:        "prod-k8s",
		LogicalName: "prod-k8s",
		Enabled:     true,
	}
	if err := db.Create(&tool).Error; err != nil {
		t.Fatalf("failed to seed tool: %v", err)
	}

	job := database.CronJob{
		UUID:     "cron-1",
		Name:     "daily-check",
		Schedule: "0 2 * * *",
		Prompt:   "Check production",
		Tools:    []database.ToolInstance{tool},
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to create cron job: %v", err)
	}

	var reloaded database.CronJob
	if err := db.Preload("Tools").First(&reloaded, job.ID).Error; err != nil {
		t.Fatalf("failed to reload cron job: %v", err)
	}
	if len(reloaded.Tools) != 1 || reloaded.Tools[0].LogicalName != "prod-k8s" {
		t.Fatalf("expected cron tool association to round-trip, got %#v", reloaded.Tools)
	}
}
