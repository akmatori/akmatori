package config

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD") // No default - must be set
	cfg.JWTExpiryHours = getEnvAsIntOrDefault("JWT_EXPIRY_HOURS", 24)

	// JWT Secret: auto-generate and persist if not provided via env var
	// Data directory is hardcoded to /akmatori in main.go
	cfg.JWTSecret = loadOrGenerateJWTSecret("/akmatori/.jwt_secret")

	return cfg, nil
}

// loadOrGenerateJWTSecret loads JWT secret from file or generates a new one
func loadOrGenerateJWTSecret(secretPath string) string {
	// First check if JWT_SECRET env var is set (allows override)
	if envSecret := os.Getenv("JWT_SECRET"); envSecret != "" {
		log.Printf("Using JWT secret from environment variable")
		return envSecret
	}

	// Try to load existing secret from file
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if secret != "" {
			log.Printf("Loaded JWT secret from %s", secretPath)
			return secret
		}
	}

	// Generate new secret
	secret := generateSecureSecret(32) // 256 bits

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(secretPath), 0755); err != nil {
		log.Printf("Warning: Could not create directory for JWT secret: %v", err)
		return secret
	}

	// Save secret to file
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		log.Printf("Warning: Could not save JWT secret to file: %v", err)
	} else {
		log.Printf("Generated and saved new JWT secret to %s", secretPath)
	}

	return secret
}

// generateSecureSecret generates a cryptographically secure random string
func generateSecureSecret(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a less secure but functional default (should never happen)
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
