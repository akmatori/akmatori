package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The process_bot_messages column must arrive as TRUE for pre-existing channel
// rows: before the column existed, listener channels processed bot messages
// unconditionally, so a plain AutoMigrate add (zero value false) would
// silently stop alert processing on upgrade installs.

func TestPreMigrateChannelsProcessBotMessages_BackfillsTrue(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	// Simulate an upgrade install: a channels table from before the column
	// existed, with one listener row.
	if err := db.Exec(`CREATE TABLE channels (
		id integer PRIMARY KEY AUTOINCREMENT,
		uuid text,
		integration_id integer,
		external_id text,
		display_name text,
		can_post numeric,
		can_listen numeric,
		is_default_post numeric,
		extraction_prompt text,
		process_human_messages numeric,
		enabled numeric,
		created_at datetime,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := db.Exec(`INSERT INTO channels (uuid, integration_id, external_id, can_post, can_listen, process_human_messages, enabled)
		VALUES ('legacy-uuid', 1, 'C_LEGACY', 0, 1, 0, 1)`).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := preMigrateChannelsProcessBotMessages(db); err != nil {
		t.Fatalf("preMigrateChannelsProcessBotMessages: %v", err)
	}

	var row Channel
	if err := db.Where("uuid = ?", "legacy-uuid").First(&row).Error; err != nil {
		t.Fatalf("load migrated row: %v", err)
	}
	if !row.ProcessBotMessages {
		t.Error("ProcessBotMessages = false after backfill, want true (upgrade installs must keep processing bot messages)")
	}

	// Idempotent: a second run is a no-op.
	if err := preMigrateChannelsProcessBotMessages(db); err != nil {
		t.Fatalf("second preMigrateChannelsProcessBotMessages: %v", err)
	}
}

func TestPreMigrateChannelsProcessBotMessages_FreshInstallIsNoop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// No channels table at all — fresh install; AutoMigrate owns the column.
	if err := preMigrateChannelsProcessBotMessages(db); err != nil {
		t.Fatalf("preMigrateChannelsProcessBotMessages on fresh install: %v", err)
	}
}

func TestChannel_ExplicitProcessBotMessagesFalsePersists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Integration{}, &Channel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	// A humans-only listener: explicit false must survive the insert (no GORM
	// default tag on the field, so the zero value is written literally).
	ch := &Channel{
		UUID:                 "humans-only-uuid",
		IntegrationID:        1,
		ExternalID:           "C_HUMANS",
		CanListen:            true,
		ProcessBotMessages:   false,
		ProcessHumanMessages: true,
		Enabled:              true,
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	var row Channel
	if err := db.Where("uuid = ?", "humans-only-uuid").First(&row).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if row.ProcessBotMessages {
		t.Error("ProcessBotMessages = true after explicit false create, want false")
	}
	if !row.ProcessHumanMessages {
		t.Error("ProcessHumanMessages = false, want true")
	}
}
