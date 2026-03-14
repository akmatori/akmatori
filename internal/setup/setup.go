package setup

import (
	"fmt"
	"log/slog"

	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/middleware"
)

// ResolveJWTSecret determines the JWT secret using priority: env var > DB > generate + store.
// Returns the resolved secret string.
func ResolveJWTSecret(envSecret string) string {
	// 1. Environment variable takes priority
	if envSecret != "" {
		slog.Info("Using JWT secret from environment variable")
		return envSecret
	}

	// 2. Try loading from database
	if dbSecret, err := database.GetSystemSetting(database.SystemSettingJWTSecret); err == nil && dbSecret != "" {
		slog.Info("Using JWT secret from database")
		return dbSecret
	}

	// 3. Generate new secret and store in DB
	secret := config.GenerateSecureSecret(32)
	if err := database.SetSystemSetting(database.SystemSettingJWTSecret, secret); err != nil {
		slog.Warn("Failed to store JWT secret in database", "error", err)
	} else {
		slog.Info("Generated and stored new JWT secret in database")
	}
	return secret
}

// ResolveAdminPassword determines the admin password hash using priority: env var > DB > setup required.
// Returns (hash, setupRequired, error).
func ResolveAdminPassword(envPassword string) (string, bool, error) {
	// 1. Environment variable takes priority — hash it
	if envPassword != "" {
		hash, err := middleware.HashPassword(envPassword)
		if err != nil {
			return "", false, fmt.Errorf("failed to hash admin password: %w", err)
		}
		slog.Info("Using admin password from environment variable")
		return hash, false, nil
	}

	// 2. Try loading hash from database
	if dbHash, err := database.GetSystemSetting(database.SystemSettingAdminPasswordHash); err == nil && dbHash != "" {
		slog.Info("Using admin password hash from database")
		return dbHash, false, nil
	}

	// 3. No password configured — setup required
	slog.Info("No admin password configured — setup mode required")
	return "", true, nil
}

// CompleteSetup hashes the password, stores it in the DB, and marks setup as completed.
// Returns the bcrypt hash of the password.
func CompleteSetup(password string) (string, error) {
	hash, err := middleware.HashPassword(password)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	if err := database.SetSystemSetting(database.SystemSettingAdminPasswordHash, hash); err != nil {
		return "", fmt.Errorf("failed to store admin password hash: %w", err)
	}

	if err := database.SetSystemSetting(database.SystemSettingSetupCompleted, "true"); err != nil {
		return "", fmt.Errorf("failed to mark setup as completed: %w", err)
	}

	slog.Info("Initial setup completed — admin password stored in database")
	return hash, nil
}

// IsSetupCompleted checks the database for the setup_completed flag.
func IsSetupCompleted() bool {
	val, err := database.GetSystemSetting(database.SystemSettingSetupCompleted)
	return err == nil && val == "true"
}
