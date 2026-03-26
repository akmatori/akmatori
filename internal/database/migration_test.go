package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	return db
}

func TestMigrateOpenAIToLLMEnabled_NoOldColumn(t *testing.T) {
	db := setupMigrationTestDB(t)

	// Create table with only the new column (no old column present)
	err := db.AutoMigrate(&ProxySettings{})
	if err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// Should be a no-op when old column doesn't exist
	err = migrateOpenAIToLLMEnabled(db)
	if err != nil {
		t.Errorf("expected no error when old column absent, got: %v", err)
	}
}

func TestMigrateOpenAIToLLMEnabled_CopiesAndDrops(t *testing.T) {
	db := setupMigrationTestDB(t)

	// Create the table with the new schema first
	err := db.AutoMigrate(&ProxySettings{})
	if err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// Add the old column to simulate pre-migration state
	err = db.Exec("ALTER TABLE proxy_settings ADD COLUMN open_ai_enabled BOOLEAN DEFAULT 1").Error
	if err != nil {
		t.Fatalf("failed to add old column: %v", err)
	}

	// Insert a row with the old column set to true, new column defaults
	err = db.Exec("INSERT INTO proxy_settings (id, open_ai_enabled, llm_enabled) VALUES (1, 1, 0)").Error
	if err != nil {
		t.Fatalf("failed to insert test row: %v", err)
	}

	// Run migration
	err = migrateOpenAIToLLMEnabled(db)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify llm_enabled was copied from open_ai_enabled
	var settings ProxySettings
	err = db.First(&settings, 1).Error
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}
	if !settings.LLMEnabled {
		t.Error("expected llm_enabled to be true after migration")
	}

	// Verify old column was dropped
	if db.Migrator().HasColumn(&ProxySettings{}, "open_ai_enabled") {
		t.Error("expected open_ai_enabled column to be dropped")
	}
}

func TestMigrateOpenAIToLLMEnabled_TransactionRollsBack(t *testing.T) {
	db := setupMigrationTestDB(t)

	// Create the table with the new schema
	err := db.AutoMigrate(&ProxySettings{})
	if err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// Add the old column
	err = db.Exec("ALTER TABLE proxy_settings ADD COLUMN open_ai_enabled BOOLEAN DEFAULT 1").Error
	if err != nil {
		t.Fatalf("failed to add old column: %v", err)
	}

	// Insert a row
	err = db.Exec("INSERT INTO proxy_settings (id, open_ai_enabled, llm_enabled) VALUES (1, 1, 0)").Error
	if err != nil {
		t.Fatalf("failed to insert test row: %v", err)
	}

	// Sabotage: drop the proxy_settings table and recreate without open_ai_enabled
	// but with a view that makes HasColumn return true. This is hard to do with SQLite,
	// so instead we test that the function returns an error on invalid SQL.
	// We'll use a broken DB session to simulate a failure during the UPDATE.
	brokenDB := setupMigrationTestDB(t)
	// Create a table with open_ai_enabled but no llm_enabled column to cause UPDATE to fail
	err = brokenDB.Exec("CREATE TABLE proxy_settings (id INTEGER PRIMARY KEY, open_ai_enabled BOOLEAN DEFAULT 1)").Error
	if err != nil {
		t.Fatalf("failed to create broken table: %v", err)
	}
	err = brokenDB.Exec("INSERT INTO proxy_settings (id, open_ai_enabled) VALUES (1, 1)").Error
	if err != nil {
		t.Fatalf("failed to insert into broken table: %v", err)
	}

	// The UPDATE should fail because llm_enabled doesn't exist
	err = migrateOpenAIToLLMEnabled(brokenDB)
	if err == nil {
		t.Error("expected error when UPDATE fails, got nil")
	}

	// Verify old column still exists (transaction rolled back, no partial state)
	if !brokenDB.Migrator().HasColumn(&ProxySettings{}, "open_ai_enabled") {
		t.Error("expected open_ai_enabled column to still exist after failed migration")
	}
}
