package config

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strconv"
)

// Config holds all configuration for the application
type Config struct {
	// HTTP Server Configuration
	HTTPPort int

	// Database Configuration
	DatabaseURL string

	// Authentication Configuration
	AdminUsername  string
	AdminPassword  string
	JWTSecret      string
	JWTExpiryHours int
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{}

	// HTTP Port for API server
	cfg.HTTPPort = getEnvAsIntOrDefault("HTTP_PORT", 3000)

	// Database configuration
	cfg.DatabaseURL = getEnvOrDefault("DATABASE_URL", "postgres://akmatori:akmatori@localhost:5432/akmatori?sslmode=disable")

	// Authentication configuration
	cfg.AdminUsername = getEnvOrDefault("ADMIN_USERNAME", "admin")
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD") // Empty is fine — resolved via DB or setup mode
	cfg.JWTExpiryHours = getEnvAsIntOrDefault("JWT_EXPIRY_HOURS", 24)

	// JWT Secret from env var only — DB resolution happens in setup.ResolveJWTSecret
	cfg.JWTSecret = os.Getenv("JWT_SECRET")

	return cfg, nil
}

// GenerateSecureSecret generates a cryptographically secure random hex string.
// The bytes parameter specifies the number of random bytes (output is 2x hex chars).
func GenerateSecureSecret(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		log.Printf("Warning: Could not generate secure random bytes: %v", err)
		return "fallback-insecure-secret-please-set-jwt-secret-env"
	}
	return hex.EncodeToString(b)
}

// getEnvOrDefault returns the value of an environment variable or a default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsIntOrDefault returns the value of an environment variable as an integer or a default value
func getEnvAsIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
