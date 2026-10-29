package database

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupLegacyIncidentsDB builds the schema as it was before the runs split:
// the incidents table still has the agent-work columns.
func setupLegacyIncidentsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&Incident{}, &AgentRun{}, &Alert{}, &Event{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE incidents ADD COLUMN session_id text",
		"CREATE INDEX idx_incidents_session_id ON incidents(session_id)",
		"ALTER TABLE incidents ADD COLUMN working_dir text",
		"ALTER TABLE incidents ADD COLUMN full_log text",
		"ALTER TABLE incidents ADD COLUMN response text",
		"ALTER TABLE incidents ADD COLUMN tokens_used integer",
		"ALTER TABLE incidents ADD COLUMN execution_time_ms integer",
		"ALTER TABLE incidents ADD COLUMN last_skill_used text",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return db
}

func legacyIncident(t *testing.T, db *gorm.DB, inc Incident, fullLog, response string, tokens int) {
	t.Helper()
	if err := db.Create(&inc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE incidents SET session_id = ?, working_dir = ?, full_log = ?, response = ?,
		tokens_used = ?, execution_time_ms = ?, last_skill_used = ? WHERE uuid = ?`,
		"sess-"+inc.UUID, "/akmatori/incidents/"+inc.UUID, fullLog, response, tokens, 1000, "zabbix", inc.UUID).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrateSplitAgentRuns(t *testing.T) {
	db := setupLegacyIncidentsDB(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	// Alert incident with two runs (a Slack follow-up): the work lands on
	// the newest run.
	legacyIncident(t, db, Incident{UUID: "inc-alert", Source: "alertmanager", SourceKind: IncidentSourceKindAlert,
		Status: IncidentStatusMonitor, StartedAt: start}, "alert log", "alert answer", 500)
	for _, id := range []string{"run-a1", "run-a2"} {
		if err := db.Create(&AgentRun{RunID: id, IncidentUUID: "inc-alert", SourceKind: "alert", Model: "m", StartedAt: start}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Old alert incident with no source_kind and no runs: alerts mark it as
	// alert work; a synthetic run holds its work.
	legacyIncident(t, db, Incident{UUID: "inc-old", Source: "zabbix", Status: IncidentStatusClosed, StartedAt: start},
		"old log", "old answer", 70)
	if err := db.Create(&Alert{UUID: "al-old", IncidentUUID: "inc-old", Status: AlertStatusResolved, FiredAt: start}).Error; err != nil {
		t.Fatal(err)
	}
	// Slack mention: request work. Its event and run move to the scope.
	legacyIncident(t, db, Incident{UUID: "inc-mention", Source: "slack", SourceID: "1700.01", SourceKind: IncidentSourceKindSlackMention,
		Status: IncidentStatusCompleted, StartedAt: start, Context: JSONB{"channel": "C1", "text": "disk on db1?"}},
		"mention log", "mention answer", 30)
	if err := db.Create(&AgentRun{RunID: "run-m1", IncidentUUID: "inc-mention", SourceKind: "slack_mention", StartedAt: start}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Event{UUID: "ev-mention", SourceKind: EventSourceSlackMention, Kind: EventKindRequest,
		Classification: EventClassificationBackfill, IncidentUUID: "inc-mention", ReceivedAt: start}).Error; err != nil {
		t.Fatal(err)
	}
	// Cron run with no run row.
	legacyIncident(t, db, Incident{UUID: "inc-cron", Source: "cron", SourceKind: IncidentSourceKindCron, SourceUUID: "job-1",
		Status: IncidentStatusFailed, StartedAt: start}, "cron log", "cron failed", 0)

	if err := migrateSplitAgentRuns(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	run := func(id string) AgentRun {
		t.Helper()
		var r AgentRun
		if err := db.Where("run_id = ?", id).First(&r).Error; err != nil {
			t.Fatalf("run %s: %v", id, err)
		}
		return r
	}
	if r := run("run-a1"); r.ScopeUUID != "inc-alert" || r.FullLog != "" || r.RootSkill != "incident-manager" {
		t.Fatalf("older alert run = %+v, want scope and no work", r)
	}
	if r := run("run-a2"); r.ScopeUUID != "inc-alert" || r.IncidentUUID != "inc-alert" || r.FullLog != "alert log" ||
		r.Response != "alert answer" || r.SessionID != "sess-inc-alert" || r.LastSkillUsed != "zabbix" ||
		r.WorkingDir != "/akmatori/incidents/inc-alert" {
		t.Fatalf("newest alert run = %+v", r)
	}
	if r := run("run-m1"); r.ScopeUUID != "inc-mention" || r.IncidentUUID != "" || r.EventUUID != "ev-mention" ||
		r.SlackThreadTS != "1700.01" || r.SlackChannelID != "C1" || r.Response != "mention answer" {
		t.Fatalf("mention run = %+v", r)
	}

	var old AgentRun
	if err := db.Where("incident_uuid = ?", "inc-old").First(&old).Error; err != nil {
		t.Fatalf("synthetic run for inc-old: %v", err)
	}
	if old.ScopeUUID != "inc-old" || old.Response != "old answer" || old.TokensUsed != 70 ||
		old.Status != AgentRunStatusCompleted || old.Model != "" || !old.CreatedAt.Equal(start) {
		t.Fatalf("synthetic run = %+v", old)
	}
	var cron AgentRun
	if err := db.Where("scope_uuid = ?", "inc-cron").First(&cron).Error; err != nil {
		t.Fatalf("cron run: %v", err)
	}
	if cron.IncidentUUID != "" || cron.Status != AgentRunStatusFailed || cron.RootSkill != "cron-agent" || cron.SourceUUID != "job-1" {
		t.Fatalf("cron run = %+v", cron)
	}

	var left []string
	db.Model(&Incident{}).Order("uuid").Pluck("uuid", &left)
	if len(left) != 2 || left[0] != "inc-alert" || left[1] != "inc-old" {
		t.Fatalf("incidents left = %v, want only the alert incidents", left)
	}
	var ev Event
	db.Where("uuid = ?", "ev-mention").First(&ev)
	if ev.ScopeUUID != "inc-mention" || ev.IncidentUUID != "" {
		t.Fatalf("mention event = %+v, want scope set and incident link cleared", ev)
	}
	for _, c := range legacyIncidentWorkColumns {
		if db.Migrator().HasColumn("incidents", c) {
			t.Fatalf("column incidents.%s still exists", c)
		}
	}

	// Second start: no columns, no work.
	if err := migrateSplitAgentRuns(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var runs int64
	db.Model(&AgentRun{}).Count(&runs)
	if runs != 5 {
		t.Fatalf("runs = %d, want 5 (no new synthetic runs)", runs)
	}
}

// A re-run after a failed migration must not copy stale incident work over a
// run that already has its own.
func TestSplitIncidentRuns_KeepsExistingWork(t *testing.T) {
	db := setupLegacyIncidentsDB(t)
	legacyIncident(t, db, Incident{UUID: "inc", SourceKind: IncidentSourceKindAlert, Status: IncidentStatusCompleted},
		"stale log", "stale answer", 1)
	if err := db.Create(&AgentRun{RunID: "new", IncidentUUID: "inc", FullLog: "fresh log", Response: "fresh"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateSplitAgentRuns(db); err != nil {
		t.Fatal(err)
	}
	var r AgentRun
	db.Where("run_id = ?", "new").First(&r)
	if r.FullLog != "fresh log" || r.Response != "fresh" {
		t.Fatalf("run = %+v, want its own work kept", r)
	}
}
