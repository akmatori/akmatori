package setup

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/middleware"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) func() {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Set global DB
	database.DB = db

	// Run migrations for SystemSetting
	if err := db.AutoMigrate(&database.SystemSetting{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				t.Fatalf("Failed to close DB: %v", err)
			}
		}
	}
}

func TestResolveJWTSecret_EnvVarTakesPriority(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Store a different secret in DB
	if err := database.SetSystemSetting(database.SystemSettingJWTSecret, "db-secret"); err != nil {
		t.Fatalf("Failed to set system setting: %v", err)
	}

	// Env var should take priority
	result := ResolveJWTSecret("env-secret")
	if result != "env-secret" {
		t.Errorf("Expected 'env-secret', got '%s'", result)
	}
}

func TestResolveJWTSecret_FallsBackToDB(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	if err := database.SetSystemSetting(database.SystemSettingJWTSecret, "db-secret"); err != nil {
		t.Fatalf("Failed to set system setting: %v", err)
	}

	result := ResolveJWTSecret("")
	if result != "db-secret" {
		t.Errorf("Expected 'db-secret', got '%s'", result)
	}
}

func TestResolveJWTSecret_GeneratesAndStoresNewSecret(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	result := ResolveJWTSecret("")

	if result == "" {
		t.Error("Expected generated secret, got empty string")
	}

	// Should be stored in DB
	dbVal, err := database.GetSystemSetting(database.SystemSettingJWTSecret)
	if err != nil {
		t.Fatalf("Expected secret to be stored in DB: %v", err)
	}
	if dbVal != result {
		t.Errorf("DB value '%s' doesn't match returned value '%s'", dbVal, result)
	}
}

func TestResolveAdminPassword_EnvVarTakesPriority(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	hash, setupRequired, err := ResolveAdminPassword("my-password")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if setupRequired {
		t.Error("Should not require setup when env var is set")
	}
	if hash == "" {
		t.Error("Expected non-empty hash")
	}
	// Verify it's a valid bcrypt hash
	if !middleware.CheckPassword("my-password", hash) {
		t.Error("Hash should validate against the original password")
	}
}

func TestResolveAdminPassword_FallsBackToDB(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Store a hash in DB
	storedHash, err := middleware.HashPassword("stored-password")
	if err != nil {
		t.Fatalf("Failed to hash: %v", err)
	}
	if err := database.SetSystemSetting(database.SystemSettingAdminPasswordHash, storedHash); err != nil {
		t.Fatalf("Failed to set system setting: %v", err)
	}

	hash, setupRequired, err := ResolveAdminPassword("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if setupRequired {
		t.Error("Should not require setup when hash is in DB")
	}
	if hash != storedHash {
		t.Error("Should return the stored hash from DB")
	}
}

func TestResolveAdminPassword_SetupRequired(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	hash, setupRequired, err := ResolveAdminPassword("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !setupRequired {
		t.Error("Should require setup when no password is configured")
	}
	if hash != "" {
		t.Error("Expected empty hash when setup is required")
	}
}

func TestResolveAdminPassword_IgnoresEmptyStoredHash(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	if err := database.SetSystemSetting(database.SystemSettingAdminPasswordHash, ""); err != nil {
		t.Fatalf("Failed to set system setting: %v", err)
	}

	hash, setupRequired, err := ResolveAdminPassword("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !setupRequired {
		t.Error("Should require setup when stored hash is empty")
	}
	if hash != "" {
		t.Errorf("Expected empty hash when stored hash is empty, got %q", hash)
	}
}

func TestCompleteSetup(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	hash, err := CompleteSetup("secure-password-123")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if hash == "" {
		t.Error("Expected non-empty hash")
	}

	// Verify password hash is stored in DB
	dbHash, err := database.GetSystemSetting(database.SystemSettingAdminPasswordHash)
	if err != nil {
		t.Fatalf("Expected hash in DB: %v", err)
	}
	if dbHash != hash {
		t.Error("DB hash should match returned hash")
	}

	// Verify setup_completed flag is set
	if !IsSetupCompleted() {
		t.Error("Setup should be marked as completed")
	}

	// Verify the hash validates against the password
	if !middleware.CheckPassword("secure-password-123", hash) {
		t.Error("Hash should validate against the original password")
	}
}

func TestIsSetupCompleted_NotCompleted(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	if IsSetupCompleted() {
		t.Error("Setup should not be completed on fresh DB")
	}
}

func TestIsSetupCompleted_Completed(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	if err := database.SetSystemSetting(database.SystemSettingSetupCompleted, "true"); err != nil {
		t.Fatalf("Failed to set system setting: %v", err)
	}

	if !IsSetupCompleted() {
		t.Error("Setup should be completed when flag is set")
	}
}

func TestIsSetupCompleted_OnlyAcceptsLiteralTrue(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	testCases := []struct {
		name     string
		stored   string
		expected bool
	}{
		{name: "literal true", stored: "true", expected: true},
		{name: "false", stored: "false", expected: false},
		{name: "uppercase true", stored: "TRUE", expected: false},
		{name: "empty", stored: "", expected: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.DB.Where("key = ?", database.SystemSettingSetupCompleted).Delete(&database.SystemSetting{}).Error; err != nil {
				t.Fatalf("Failed to reset setup flag: %v", err)
			}
			if tc.stored != "" {
				if err := database.SetSystemSetting(database.SystemSettingSetupCompleted, tc.stored); err != nil {
					t.Fatalf("Failed to set system setting: %v", err)
				}
			}

			if got := IsSetupCompleted(); got != tc.expected {
				t.Errorf("IsSetupCompleted() = %v, want %v for stored value %q", got, tc.expected, tc.stored)
			}
		})
	}
}

func TestResolveJWTSecret_Deterministic(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// First call generates
	secret1 := ResolveJWTSecret("")
	// Second call should return the same (from DB)
	secret2 := ResolveJWTSecret("")

	if secret1 != secret2 {
		t.Errorf("Expected same secret on second call, got '%s' and '%s'", secret1, secret2)
	}
}

func TestCompleteSetup_ThenResolvePassword(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Complete setup
	hash, err := CompleteSetup("test-password")
	if err != nil {
		t.Fatalf("CompleteSetup failed: %v", err)
	}

	// Now ResolveAdminPassword should find it in DB
	resolvedHash, setupRequired, err := ResolveAdminPassword("")
	if err != nil {
		t.Fatalf("ResolveAdminPassword failed: %v", err)
	}
	if setupRequired {
		t.Error("Should not require setup after CompleteSetup")
	}
	if resolvedHash != hash {
		t.Error("Resolved hash should match the one from CompleteSetup")
	}
}
