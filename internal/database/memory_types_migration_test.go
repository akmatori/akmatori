package database

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyMemoryTypes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// No table yet: a no-op, not an error.
	if err := migrateLegacyMemoryTypes(db); err != nil {
		t.Fatalf("migrate without table: %v", err)
	}

	if err := db.AutoMigrate(&Memory{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for name, memType := range map[string]string{
		"a-host": "host", "a-pattern": "incident_pattern", "a-quirk": "tool_quirk",
		"a-feedback": "feedback", "a-fact": "fact",
	} {
		m := Memory{Scope: "global", Type: memType, Name: name, Description: "d", UpdatedAt: updated}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	for i := 0; i < 2; i++ { // idempotent
		if err := migrateLegacyMemoryTypes(db); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}

	var rows []Memory
	if err := db.Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[string]string{"a-fact": "fact", "a-feedback": "feedback", "a-host": "fact", "a-pattern": "fact", "a-quirk": "fact"}
	for _, m := range rows {
		if m.Type != want[m.Name] {
			t.Errorf("%s type = %q, want %q", m.Name, m.Type, want[m.Name])
		}
		if !m.UpdatedAt.Equal(updated) {
			t.Errorf("%s updated_at changed to %v; the manifest order depends on it", m.Name, m.UpdatedAt)
		}
	}
}
