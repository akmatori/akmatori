package config

import (
	"encoding/hex"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTPPort != 3000 {
		t.Errorf("HTTPPort = %d, want 3000", cfg.HTTPPort)
	}
	if cfg.DatabaseURL != "postgres://akmatori:akmatori@localhost:5432/akmatori?sslmode=disable" {
		t.Errorf("DatabaseURL = %q, want default postgres URL", cfg.DatabaseURL)
	}
	if cfg.AdminUsername != "admin" {
		t.Errorf("AdminUsername = %q, want admin", cfg.AdminUsername)
	}
	if cfg.AdminPassword != "" {
		t.Errorf("AdminPassword = %q, want empty default", cfg.AdminPassword)
	}
	if cfg.JWTSecret != "" {
		t.Errorf("JWTSecret = %q, want empty default", cfg.JWTSecret)
	}
	if cfg.JWTExpiryHours != 24 {
		t.Errorf("JWTExpiryHours = %d, want 24", cfg.JWTExpiryHours)
	}
}

func TestLoad_EnvironmentOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("ADMIN_USERNAME", "root")
	t.Setenv("ADMIN_PASSWORD", "secret")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("JWT_EXPIRY_HOURS", "48")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.DatabaseURL != "postgres://example/db" {
		t.Errorf("DatabaseURL = %q, want override", cfg.DatabaseURL)
	}
	if cfg.AdminUsername != "root" {
		t.Errorf("AdminUsername = %q, want root", cfg.AdminUsername)
	}
	if cfg.AdminPassword != "secret" {
		t.Errorf("AdminPassword = %q, want secret", cfg.AdminPassword)
	}
	if cfg.JWTSecret != "jwt-secret" {
		t.Errorf("JWTSecret = %q, want jwt-secret", cfg.JWTSecret)
	}
	if cfg.JWTExpiryHours != 48 {
		t.Errorf("JWTExpiryHours = %d, want 48", cfg.JWTExpiryHours)
	}
}

func TestLoad_InvalidIntegerOverridesUseDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HTTP_PORT", "not-a-port")
	t.Setenv("JWT_EXPIRY_HOURS", "tomorrow")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTPPort != 3000 {
		t.Errorf("HTTPPort = %d, want default 3000 for invalid env value", cfg.HTTPPort)
	}
	if cfg.JWTExpiryHours != 24 {
		t.Errorf("JWTExpiryHours = %d, want default 24 for invalid env value", cfg.JWTExpiryHours)
	}
}

func TestGenerateSecureSecret_ReturnsHexOfRequestedByteLength(t *testing.T) {
	const byteLength = 32

	secret := GenerateSecureSecret(byteLength)

	if len(secret) != byteLength*2 {
		t.Fatalf("GenerateSecureSecret(%d) length = %d, want %d", byteLength, len(secret), byteLength*2)
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatalf("GenerateSecureSecret(%d) returned non-hex string %q: %v", byteLength, secret, err)
	}
	if len(decoded) != byteLength {
		t.Errorf("decoded secret length = %d, want %d", len(decoded), byteLength)
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
