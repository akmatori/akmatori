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

	// Insert two rows: one with true, one with false.
	// The false case is critical because llm_enabled has default:true —
	// if the migration didn't actually run, the default would mask the bug.
	err = db.Exec("INSERT INTO proxy_settings (id, open_ai_enabled, llm_enabled) VALUES (1, 1, 0)").Error
	if err != nil {
		t.Fatalf("failed to insert test row 1: %v", err)
	}
	err = db.Exec("INSERT INTO proxy_settings (id, open_ai_enabled, llm_enabled) VALUES (2, 0, 1)").Error
	if err != nil {
		t.Fatalf("failed to insert test row 2: %v", err)
	}

	// Run migration
	err = migrateOpenAIToLLMEnabled(db)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify llm_enabled was copied from open_ai_enabled (true case)
	var settings1 ProxySettings
	err = db.First(&settings1, 1).Error
	if err != nil {
		t.Fatalf("failed to read settings row 1: %v", err)
	}
	if !settings1.LLMEnabled {
		t.Error("row 1: expected llm_enabled to be true after migration")
	}

	// Verify llm_enabled was copied from open_ai_enabled (false case)
	var settings2 ProxySettings
	err = db.First(&settings2, 2).Error
	if err != nil {
		t.Fatalf("failed to read settings row 2: %v", err)
	}
	if settings2.LLMEnabled {
		t.Error("row 2: expected llm_enabled to be false after migration")
	}

	// Verify old column was dropped
	if db.Migrator().HasColumn(&ProxySettings{}, "open_ai_enabled") {
		t.Error("expected open_ai_enabled column to be dropped")
	}
}

func TestMigrateOpenAIToLLMEnabled_TransactionRollsBack(t *testing.T) {
	// Create a database with open_ai_enabled but no llm_enabled column.
	// This causes the UPDATE to fail, verifying error propagation and
	// that the old column is not partially dropped.
	brokenDB := setupMigrationTestDB(t)
	err := brokenDB.Exec("CREATE TABLE proxy_settings (id INTEGER PRIMARY KEY, open_ai_enabled BOOLEAN DEFAULT 1)").Error
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

	// Verify old column still exists (no partial state)
	if !brokenDB.Migrator().HasColumn(&ProxySettings{}, "open_ai_enabled") {
		t.Error("expected open_ai_enabled column to still exist after failed migration")
	}
}
