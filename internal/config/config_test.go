package config

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPPort != 3000 {
		t.Errorf("HTTPPort = %d, want %d", cfg.HTTPPort, 3000)
	}
	if cfg.DatabaseURL != "postgres://akmatori:akmatori@localhost:5432/akmatori?sslmode=disable" {
		t.Errorf("DatabaseURL = %q, want default postgres URL", cfg.DatabaseURL)
	}
	if cfg.AdminUsername != "admin" {
		t.Errorf("AdminUsername = %q, want %q", cfg.AdminUsername, "admin")
	}
	if cfg.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty default", cfg.AdminPassword)
	}
	if cfg.JWTSecret != "" {
		t.Errorf("JWTSecret = %q, want empty default", cfg.JWTSecret)
	}
	if cfg.JWTExpiryHours != 24 {
		t.Errorf("JWTExpiryHours = %d, want %d", cfg.JWTExpiryHours, 24)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://example/test")
	t.Setenv("ADMIN_USERNAME", "root")
	t.Setenv("ADMIN_PASSWORD", "secret")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("JWT_EXPIRY_HOURS", "72")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want %d", cfg.HTTPPort, 8080)
	}
	if cfg.DatabaseURL != "postgres://example/test" {
		t.Errorf("DatabaseURL = %q, want env override", cfg.DatabaseURL)
	}
	if cfg.AdminUsername != "root" {
		t.Errorf("AdminUsername = %q, want %q", cfg.AdminUsername, "root")
	}
	if cfg.AdminPassword != "secret" {
		t.Errorf("AdminPassword = %q, want env override", cfg.AdminPassword)
	}
	if cfg.JWTSecret != "jwt-secret" {
		t.Errorf("JWTSecret = %q, want env override", cfg.JWTSecret)
	}
	if cfg.JWTExpiryHours != 72 {
		t.Errorf("JWTExpiryHours = %d, want %d", cfg.JWTExpiryHours, 72)
	}
}

func TestLoad_InvalidIntegerEnvFallsBackToDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HTTP_PORT", "not-a-port")
	t.Setenv("JWT_EXPIRY_HOURS", "also-invalid")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPPort != 3000 {
		t.Errorf("HTTPPort = %d, want default after invalid env", cfg.HTTPPort)
	}
	if cfg.JWTExpiryHours != 24 {
		t.Errorf("JWTExpiryHours = %d, want default after invalid env", cfg.JWTExpiryHours)
	}
}

func TestGenerateSecureSecret_ReturnsHexString(t *testing.T) {
	const byteCount = 16

	secret := GenerateSecureSecret(byteCount)
	if len(secret) != byteCount*2 {
		t.Fatalf("secret length = %d, want %d", len(secret), byteCount*2)
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Fatalf("secret is not valid hex: %v", err)
	}
	if strings.Trim(secret, "0") == "" {
		t.Fatal("secret is all zeroes, want random bytes")
	}
}

func TestGetEnvHelpers(t *testing.T) {
	t.Setenv("AKMATORI_TEST_STRING", "configured")
	t.Setenv("AKMATORI_TEST_INT", "42")
	t.Setenv("AKMATORI_TEST_BAD_INT", "nope")
	t.Setenv("AKMATORI_TEST_EMPTY", "")

	if got := getEnvOrDefault("AKMATORI_TEST_STRING", "default"); got != "configured" {
		t.Errorf("getEnvOrDefault configured = %q, want %q", got, "configured")
	}
	if got := getEnvOrDefault("AKMATORI_TEST_EMPTY", "default"); got != "default" {
		t.Errorf("getEnvOrDefault empty = %q, want default", got)
	}
	if got := getEnvAsIntOrDefault("AKMATORI_TEST_INT", 7); got != 42 {
		t.Errorf("getEnvAsIntOrDefault configured = %d, want %d", got, 42)
	}
	if got := getEnvAsIntOrDefault("AKMATORI_TEST_BAD_INT", 7); got != 7 {
		t.Errorf("getEnvAsIntOrDefault invalid = %d, want default", got)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"HTTP_PORT",
		"DATABASE_URL",
		"ADMIN_USERNAME",
		"ADMIN_PASSWORD",
		"JWT_SECRET",
		"JWT_EXPIRY_HOURS",
	} {
		t.Setenv(key, "")
	}
}
