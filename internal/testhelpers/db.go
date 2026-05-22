package testhelpers

import (
	"net/url"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// NewSQLiteDB opens an isolated in-memory SQLite database and migrates the
// supplied models. The connection is closed automatically at test cleanup.
func NewSQLiteDB(t testing.TB, models ...interface{}) *gorm.DB {
	t.Helper()

	name := strings.TrimSpace(t.Name())
	if name == "" {
		name = "akmatori-test"
	}

	dsn := "file:" + url.PathEscape(name) + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("failed to migrate test database: %v", err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to access test database handle: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close test database: %v", err)
		}
	})

	return db
}

// NewGlobalSQLiteDB opens a test database, assigns it to database.DB, and
// restores the previous global handle at cleanup.
func NewGlobalSQLiteDB(t testing.TB, models ...interface{}) *gorm.DB {
	t.Helper()

	previous := database.DB
	db := NewSQLiteDB(t, models...)
	database.DB = db
	t.Cleanup(func() {
		database.DB = previous
	})

	return db
}
