package database

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// A memories table from before evidence grades: the backfill grades
// operator rows and file summaries observed, and every other row unverified
// with the legacy note, without moving updated_at.
func TestMigrateMemoryEvidence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if memoryEvidenceNeedsBackfill(db) {
		t.Fatal("a missing table needs no backfill")
	}
	if err := db.Exec(`CREATE TABLE memories (
		id integer PRIMARY KEY AUTOINCREMENT, scope text NOT NULL, type text NOT NULL, name text NOT NULL,
		description text NOT NULL, body text NOT NULL DEFAULT '', incident_uuid text, created_by text,
		source_file_id integer, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seed := []struct {
		name, createdBy string
		file            bool
	}{
		{"agent-row", "agent", false},
		{"legacy-row", "", false},
		{"null-row", "NULL", false},
		{"operator-row", "operator", false},
		{"file-row", "agent", true},
	}
	for _, s := range seed {
		var createdBy interface{} = s.createdBy
		if s.createdBy == "NULL" {
			createdBy = nil
		}
		var file interface{}
		if s.file {
			file = 7
		}
		if err := db.Exec("INSERT INTO memories (scope, type, name, description, body, created_by, source_file_id, created_at, updated_at) VALUES ('global', 'fact', ?, 'd', 'b', ?, ?, ?, ?)",
			s.name, createdBy, file, updated, updated).Error; err != nil {
			t.Fatal(err)
		}
	}

	if !memoryEvidenceNeedsBackfill(db) {
		t.Fatal("an old table needs the backfill")
	}
	if err := db.AutoMigrate(&Memory{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := migrateMemoryEvidence(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if memoryEvidenceNeedsBackfill(db) {
		t.Error("after AutoMigrate the column exists: no second backfill")
	}

	want := map[string]struct{ evidence, claim, note string }{
		"agent-row":    {"unverified", "", legacyMemoryEvidenceNote},
		"legacy-row":   {"unverified", "", legacyMemoryEvidenceNote},
		"null-row":     {"unverified", "", legacyMemoryEvidenceNote},
		"operator-row": {"observed", "", ""},
		"file-row":     {"observed", "factual", ""},
	}
	var rows []Memory
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, m := range rows {
		w := want[m.Name]
		if m.Evidence != w.evidence || m.Claim != w.claim || m.EvidenceNote != w.note {
			t.Errorf("%s = %q/%q/%q, want %q/%q/%q", m.Name, m.Evidence, m.Claim, m.EvidenceNote, w.evidence, w.claim, w.note)
		}
		if !m.UpdatedAt.Equal(updated) {
			t.Errorf("%s updated_at moved to %v", m.Name, m.UpdatedAt)
		}
	}
}
