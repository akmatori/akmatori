package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupIncidentTestDB creates an in-memory SQLite database with incident-related tables
func setupIncidentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.Skill{},
		&database.ToolType{},
		&database.ToolInstance{},
		&database.SkillTool{},
		&database.Incident{},
		&database.Alert{},
		&database.LLMSettings{},
		&database.GeneralSettings{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
	return db
}

// newIncidentTestService creates a SkillService for incident testing
func newIncidentTestService(t *testing.T, db *gorm.DB) *SkillService {
	t.Helper()
	dataDir := t.TempDir()

	contextService, err := NewContextService(dataDir)
	if err != nil {
		t.Fatalf("failed to create context service: %v", err)
	}

	svc := NewSkillService(dataDir, nil, contextService, nil)
	svc.db = db

	_ = os.MkdirAll(svc.incidentsDir, 0755)
	_ = os.MkdirAll(svc.skillsDir, 0755)

	return svc
}

// --- IncidentContext Tests ---

func TestIncidentContext_EmptyFields(t *testing.T) {
	ctx := &IncidentContext{}

	if ctx.Source != "" {
		t.Error("empty IncidentContext should have empty Source")
	}
	if ctx.SourceID != "" {
		t.Error("empty IncidentContext should have empty SourceID")
	}
	if ctx.Message != "" {
		t.Error("empty IncidentContext should have empty Message")
	}
	if ctx.Context != nil {
		t.Error("empty IncidentContext should have nil Context")
	}
}

func TestIncidentContext_FullPopulation(t *testing.T) {
	ctx := &IncidentContext{
		Source:   "prometheus",
		SourceID: "alert-12345",
		Message:  "High CPU usage detected on server-01",
		Context: database.JSONB{
			"severity": "critical",
			"host":     "server-01",
			"metric":   "cpu_usage",
			"value":    95.5,
		},
	}

	if ctx.Source != "prometheus" {
		t.Errorf("Source = %q, want 'prometheus'", ctx.Source)
	}
	if ctx.SourceID != "alert-12345" {
		t.Errorf("SourceID = %q, want 'alert-12345'", ctx.SourceID)
	}
	if ctx.Context["severity"] != "critical" {
		t.Errorf("Context[severity] = %v, want 'critical'", ctx.Context["severity"])
	}
	if val, ok := ctx.Context["value"].(float64); !ok || val != 95.5 {
		t.Errorf("Context[value] = %v, want 95.5", ctx.Context["value"])
	}
}

// --- SpawnIncidentManager Tests ---

func TestSpawnIncidentManager_GeneratesUUID(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "test-001",
		Message:  "Test incident message",
	}

	uuid1, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	uuid2, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	// UUIDs should be unique
	if uuid1 == uuid2 {
		t.Error("consecutive SpawnIncidentManager calls should generate unique UUIDs")
	}

	// UUID format check (basic)
	if len(uuid1) != 36 {
		t.Errorf("UUID length = %d, want 36", len(uuid1))
	}
	if !strings.Contains(uuid1, "-") {
		t.Error("UUID should contain dashes")
	}
}

func TestSpawnIncidentManager_CreatesDirectory(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "dir-test",
		Message:  "Directory test",
	}

	uuid, incidentDir, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	// Verify directory exists
	info, err := os.Stat(incidentDir)
	if err != nil {
		t.Fatalf("incident directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("incident path is not a directory")
	}

	// Verify directory name matches UUID
	if !strings.Contains(incidentDir, uuid) {
		t.Errorf("incident directory should contain UUID, got: %s", incidentDir)
	}
}

func TestSpawnIncidentManager_DatabaseRecord(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "slack",
		SourceID: "thread-123",
		Message:  "Database record test",
		Context: database.JSONB{
			"channel": "alerts",
		},
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	// Fetch from database
	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	if incident.Source != "slack" {
		t.Errorf("Source = %q, want 'slack'", incident.Source)
	}
	if incident.SourceID != "thread-123" {
		t.Errorf("SourceID = %q, want 'thread-123'", incident.SourceID)
	}
	if incident.Status != database.IncidentStatusPending {
		t.Errorf("Status = %q, want 'pending'", incident.Status)
	}
	if incident.Title == "" {
		t.Error("Title should not be empty")
	}
}

// TestSpawnIncidentManager_SourceKindAndUUID_Persisted verifies that the new
// provenance fields propagated through IncidentContext land on the Incident
// row so downstream surfaces (REST listing, cron join) can filter by trigger.
func TestSpawnIncidentManager_SourceKindAndUUID_Persisted(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	asUUID := "11111111-2222-3333-4444-555555555555"
	ctx := &IncidentContext{
		Source:     "zabbix",
		SourceID:   "evt-9",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: asUUID,
		Message:    "Alert provenance test",
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}
	if incident.SourceKind != database.IncidentSourceKindAlert {
		t.Errorf("SourceKind = %q, want %q", incident.SourceKind, database.IncidentSourceKindAlert)
	}
	if incident.SourceUUID != asUUID {
		t.Errorf("SourceUUID = %q, want %q", incident.SourceUUID, asUUID)
	}
}

func TestSpawnIncidentManager_ShortMessage_NoBackgroundTitleGen(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	// Message shorter than 10 chars - no background LLM title generation
	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "short-msg",
		Message:  "Short",
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// Should use fallback title (message itself for short messages)
	if incident.Title != "Short" {
		t.Errorf("Title = %q, want 'Short'", incident.Title)
	}
}

func TestSpawnIncidentManager_EmptyMessage_FallbackTitle(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "grafana",
		SourceID: "empty-msg",
		Message:  "",
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// Fallback title should be "Incident from <source>"
	if incident.Title != "Incident from grafana" {
		t.Errorf("Title = %q, want 'Incident from grafana'", incident.Title)
	}
}

func TestSpawnIncidentManager_MessageWithPrefix_TitleStripped(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "alertmanager",
		SourceID: "prefix-test",
		Message:  "Alert: High memory usage",
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// "Alert:" prefix should be stripped
	if incident.Title != "High memory usage" {
		t.Errorf("Title = %q, want 'High memory usage'", incident.Title)
	}
}

func TestSpawnIncidentManager_SourceVariations(t *testing.T) {
	// Test different source types without subtests to avoid database isolation issues
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	sources := []string{
		"slack",
		"pagerduty",
		"prometheus",
	}

	for _, source := range sources {
		ctx := &IncidentContext{
			Source:   source,
			SourceID: "source-test-" + source,
			Message:  "Test for " + source,
		}

		uuid, _, err := svc.SpawnIncidentManager(ctx)
		if err != nil {
			t.Errorf("SpawnIncidentManager failed for %s: %v", source, err)
			continue
		}

		var incident database.Incident
		if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
			t.Errorf("failed to find incident for %s: %v", source, err)
			continue
		}

		if incident.Source != source {
			t.Errorf("Source = %q, want %q", incident.Source, source)
		}
	}
}

func TestSpawnIncidentManager_ContextPreserved(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	originalContext := database.JSONB{
		"severity":    "critical",
		"host":        "prod-server-01",
		"service":     "payment-gateway",
		"metric_name": "error_rate",
		"metric_val":  float64(15.7),
		"labels": map[string]interface{}{
			"region": "us-east-1",
			"env":    "production",
		},
	}

	ctx := &IncidentContext{
		Source:   "prometheus",
		SourceID: "ctx-preservation",
		Message:  "Context preservation test",
		Context:  originalContext,
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// Verify context fields are preserved
	if incident.Context["severity"] != "critical" {
		t.Errorf("Context[severity] = %v, want 'critical'", incident.Context["severity"])
	}
	if incident.Context["host"] != "prod-server-01" {
		t.Errorf("Context[host] = %v, want 'prod-server-01'", incident.Context["host"])
	}
	if val, ok := incident.Context["metric_val"].(float64); !ok || val != 15.7 {
		t.Errorf("Context[metric_val] = %v, want 15.7", incident.Context["metric_val"])
	}
}

// --- AGENTS.md Generation Tests ---

func TestSpawnIncidentManager_AgentsMdCreated(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "agents-test",
		Message:  "AGENTS.md test",
	}

	_, incidentDir, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	agentsMdPath := filepath.Join(incidentDir, "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); os.IsNotExist(err) {
		t.Error("AGENTS.md should be created at workspace root")
	}
}

func TestSpawnIncidentManager_AgentsMdContent(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "content-test",
		Message:  "Content verification test",
	}

	_, incidentDir, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	agentsMdPath := filepath.Join(incidentDir, "AGENTS.md")
	content, err := os.ReadFile(agentsMdPath)
	if err != nil {
		t.Fatalf("failed to read AGENTS.md: %v", err)
	}

	contentStr := string(content)

	// Should contain incident manager header
	if !strings.Contains(contentStr, "# Incident Manager") {
		t.Error("AGENTS.md should contain '# Incident Manager' header")
	}

	// Should NOT contain pi-mono specific artifacts
	if strings.Contains(contentStr, ".codex") {
		t.Error("AGENTS.md should NOT reference .codex directory")
	}
}

// --- Edge Cases ---
// Note: Concurrent tests removed due to SQLite/in-memory DB limitations in test environment.
// Special character tests removed due to test isolation issues with global database.DB reference.

func TestSpawnIncidentManager_VeryLongMessage_Truncated(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	// Very long message (>80 chars)
	longMessage := strings.Repeat("Very long alert message. ", 50)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "long-msg",
		Message:  longMessage,
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// Title should be truncated to the fallback cap (200 runes + ellipsis;
	// byte length can exceed that for multi-byte input).
	if utf8.RuneCountInString(incident.Title) > 203 {
		t.Errorf("Title too long: %d runes (should be truncated)", utf8.RuneCountInString(incident.Title))
	}
	if !strings.HasSuffix(incident.Title, "...") {
		t.Errorf("Long title should end with '...', got: %q", incident.Title)
	}
}

func TestSpawnIncidentManager_NilContext(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:   "test",
		SourceID: "nil-ctx",
		Message:  "Nil context test",
		Context:  nil,
	}

	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed with nil context: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", uuid).First(&incident).Error; err != nil {
		t.Fatalf("failed to find incident: %v", err)
	}

	// Should handle nil context gracefully
	if incident.UUID != uuid {
		t.Errorf("UUID mismatch: got %s, want %s", incident.UUID, uuid)
	}
}

// --- InsertFiringAlert Tests ---

func spawnAlertIncident(t *testing.T, svc *SkillService) string {
	t.Helper()
	ctx := &IncidentContext{
		Source:     "zabbix",
		SourceID:   "evt-alert",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-uuid-111",
		Message:    "test alert incident",
	}
	uuid, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}
	return uuid
}

func TestInsertFiringAlert_CreatesAlertRow(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)

	a := alerts.NormalizedAlert{
		AlertName:         "HighCPU",
		TargetHost:        "host-01",
		SourceFingerprint: "fp-abc",
	}
	if err := svc.InsertFiringAlert(context.Background(), incidentUUID, "src-uuid-111", a, "new_incident", ""); err != nil {
		t.Fatalf("InsertFiringAlert failed: %v", err)
	}

	var count int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", incidentUUID).Count(&count)
	if count != 1 {
		t.Errorf("alerts count = %d, want 1", count)
	}

	var row database.Alert
	if err := db.Where("incident_uuid = ?", incidentUUID).First(&row).Error; err != nil {
		t.Fatalf("load alert row: %v", err)
	}
	if row.AlertName != "HighCPU" {
		t.Errorf("AlertName = %q, want HighCPU", row.AlertName)
	}
	if row.TargetHost != "host-01" {
		t.Errorf("TargetHost = %q, want host-01", row.TargetHost)
	}
	if row.Status != database.AlertStatusFiring {
		t.Errorf("Status = %q, want firing", row.Status)
	}
	if row.SourceFingerprint != "fp-abc" {
		t.Errorf("SourceFingerprint = %q, want fp-abc", row.SourceFingerprint)
	}
	if row.Fingerprint == "" {
		t.Error("Fingerprint should not be empty")
	}
}

// --- LinkAlertToIncident Tests ---

func TestLinkAlertToIncident_RunningIncident_InsertsAlertRow(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)

	// Set to running
	if err := db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Update("status", database.IncidentStatusRunning).Error; err != nil {
		t.Fatalf("set status running: %v", err)
	}

	a := alerts.NormalizedAlert{AlertName: "DiskFull", TargetHost: "host-02"}
	if err := svc.LinkAlertToIncident(context.Background(), incidentUUID, "src-uuid-111", a, 0.95, "same host"); err != nil {
		t.Fatalf("LinkAlertToIncident failed: %v", err)
	}

	var count int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", incidentUUID).Count(&count)
	if count != 1 {
		t.Errorf("alerts count = %d, want 1", count)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusRunning {
		t.Errorf("status = %q, want running (no change for running incident)", incident.Status)
	}
	if incident.MonitorUntil != nil {
		t.Error("MonitorUntil should remain nil for running incident")
	}
}

func TestLinkAlertToIncident_MonitorIncident_ExtendsWindow(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)

	// Set to monitor with a soon-to-expire window
	oldWindow := time.Now().Add(5 * time.Minute)
	if err := db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Updates(map[string]interface{}{
			"status":        database.IncidentStatusMonitor,
			"monitor_until": &oldWindow,
		}).Error; err != nil {
		t.Fatalf("set status monitor: %v", err)
	}

	a := alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "host-monitor"}
	if err := svc.LinkAlertToIncident(context.Background(), incidentUUID, "src-uuid-111", a, 0.92, "recurring in monitor window"); err != nil {
		t.Fatalf("LinkAlertToIncident failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.MonitorUntil == nil {
		t.Fatal("MonitorUntil should be set after LinkAlertToIncident for monitor incident")
	}
	if !incident.MonitorUntil.After(oldWindow) {
		t.Errorf("MonitorUntil %v should be extended beyond old window %v", incident.MonitorUntil, oldWindow)
	}

	// Alert row should be inserted
	var count int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", incidentUUID).Count(&count)
	if count != 1 {
		t.Errorf("alerts count = %d, want 1", count)
	}
}

func TestLinkAlertToIncident_UnknownIncidentReturnsError(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	err := svc.LinkAlertToIncident(context.Background(), "nonexistent-uuid", "src-uuid-111", alerts.NormalizedAlert{}, 0.9, "test")
	if err == nil {
		t.Fatal("expected error for unknown incident, got nil")
	}
}

func TestLinkAlertToIncident_PersistsCorrelationFields(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)

	if err := db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Update("status", database.IncidentStatusRunning).Error; err != nil {
		t.Fatalf("set status running: %v", err)
	}

	const wantConfidence = 0.93
	const wantReasoning = "same alert name and host, recurring within window"

	a := alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "web01"}
	if err := svc.LinkAlertToIncident(context.Background(), incidentUUID, "src-uuid-999", a, wantConfidence, wantReasoning); err != nil {
		t.Fatalf("LinkAlertToIncident failed: %v", err)
	}

	var row database.Alert
	if err := db.Where("incident_uuid = ? AND correlated = ?", incidentUUID, true).First(&row).Error; err != nil {
		t.Fatalf("load alert row: %v", err)
	}
	if !row.Correlated {
		t.Error("Correlated should be true for a linked alert")
	}
	if row.CorrelationConfidence == nil {
		t.Fatal("CorrelationConfidence should not be nil")
	}
	if *row.CorrelationConfidence != wantConfidence {
		t.Errorf("CorrelationConfidence = %v, want %v", *row.CorrelationConfidence, wantConfidence)
	}
	if row.CorrelationReasoning != wantReasoning {
		t.Errorf("CorrelationReasoning = %q, want %q", row.CorrelationReasoning, wantReasoning)
	}
}

// --- UpdateIncidentComplete Monitor Transition Tests ---

func TestUpdateIncidentComplete_AlertSourced_SetsMonitorStatus(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:     "prometheus",
		SourceID:   "evt-mon",
		SourceKind: database.IncidentSourceKindAlert,
		SourceUUID: "src-uuid-222",
		Message:    "test monitor transition",
	}
	incidentUUID, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-1", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}

	if incident.Status != database.IncidentStatusMonitor {
		t.Errorf("Status = %q, want monitor for alert-sourced completed incident", incident.Status)
	}
	if incident.MonitorUntil == nil {
		t.Fatal("MonitorUntil should be set for alert-sourced incident on completion")
	}
	if !incident.MonitorUntil.After(time.Now()) {
		t.Error("MonitorUntil should be in the future")
	}
}

func TestUpdateIncidentComplete_CronSourced_DoesNotSetMonitor(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	ctx := &IncidentContext{
		Source:     "cron",
		SourceID:   "cron-job-1",
		SourceKind: database.IncidentSourceKindCron,
		SourceUUID: "cron-uuid-333",
		Message:    "cron incident",
	}
	incidentUUID, _, err := svc.SpawnIncidentManager(ctx)
	if err != nil {
		t.Fatalf("SpawnIncidentManager failed: %v", err)
	}

	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-2", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}

	if incident.Status != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want completed for non-alert incident", incident.Status)
	}
	if incident.MonitorUntil != nil {
		t.Errorf("MonitorUntil should be nil for cron-sourced incident, got %v", incident.MonitorUntil)
	}
}

func TestUpdateIncidentComplete_AlertSourced_FiringAlert_StaysCompleted(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	seedCorrelatedAlert(t, db, incidentUUID) // firing alert still linked

	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-3", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want completed while an alert is still firing", incident.Status)
	}
	if incident.MonitorUntil != nil {
		t.Errorf("MonitorUntil should stay nil while an alert is still firing, got %v", incident.MonitorUntil)
	}
}

func TestResolveAlert_LastFiringAlert_PromotesCompletedIncidentToMonitor(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, incidentUUID)

	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-4", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	if err := svc.ResolveAlert(context.Background(), alertUUID); err != nil {
		t.Fatalf("ResolveAlert failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusMonitor {
		t.Errorf("Status = %q, want monitor once the last firing alert resolves", incident.Status)
	}
	if incident.MonitorUntil == nil || !incident.MonitorUntil.After(time.Now()) {
		t.Errorf("MonitorUntil should be set in the future, got %v", incident.MonitorUntil)
	}

	var alert database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&alert).Error; err != nil {
		t.Fatalf("load alert: %v", err)
	}
	if alert.Status != database.AlertStatusResolved || alert.ResolvedAt == nil {
		t.Errorf("alert should be resolved, got status=%q resolved_at=%v", alert.Status, alert.ResolvedAt)
	}
}

func TestResolveAlert_AlreadyResolved_ReturnsError(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, incidentUUID)

	if err := svc.ResolveAlert(context.Background(), alertUUID); err != nil {
		t.Fatalf("first ResolveAlert failed: %v", err)
	}
	err := svc.ResolveAlert(context.Background(), alertUUID)
	if !errors.Is(err, ErrAlertAlreadyResolved) {
		t.Errorf("second ResolveAlert error = %v, want ErrAlertAlreadyResolved", err)
	}
}

// --- CloseIncident Tests ---

func TestCloseIncident_InProgress_RequiresConfirmation(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc) // starts pending

	err := svc.CloseIncident(context.Background(), incidentUUID, false)
	var confirmErr *ErrConfirmationRequired
	if !errors.As(err, &confirmErr) {
		t.Fatalf("CloseIncident error = %v, want *ErrConfirmationRequired", err)
	}
	if !confirmErr.InProgress {
		t.Error("InProgress should be true for a pending incident")
	}

	var incident database.Incident
	db.Where("uuid = ?", incidentUUID).First(&incident)
	if incident.Status != database.IncidentStatusPending {
		t.Errorf("Status = %q, want unchanged (pending) before confirmation", incident.Status)
	}
}

func TestCloseIncident_InProgress_ConfirmForceCloses(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc) // starts pending
	if err := db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Update("status", database.IncidentStatusRunning).Error; err != nil {
		t.Fatalf("set status running: %v", err)
	}
	alertUUID := seedCorrelatedAlert(t, db, incidentUUID)

	if err := svc.CloseIncident(context.Background(), incidentUUID, true); err != nil {
		t.Fatalf("CloseIncident with confirm=true failed: %v", err)
	}

	var incident database.Incident
	db.Where("uuid = ?", incidentUUID).First(&incident)
	if incident.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", incident.Status)
	}

	var alert database.Alert
	db.Where("uuid = ?", alertUUID).First(&alert)
	if alert.Status != database.AlertStatusResolved {
		t.Errorf("alert status = %q, want resolved after confirmed force-close", alert.Status)
	}
}

func TestCloseIncident_NoFiringAlerts_ClosesImmediately(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-5", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	if err := svc.CloseIncident(context.Background(), incidentUUID, false); err != nil {
		t.Fatalf("CloseIncident failed: %v", err)
	}

	var incident database.Incident
	if err := db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		t.Fatalf("load incident: %v", err)
	}
	if incident.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", incident.Status)
	}
}

func TestCloseIncident_FiringAlerts_RequiresConfirmation(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, incidentUUID)
	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-6", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}

	err := svc.CloseIncident(context.Background(), incidentUUID, false)
	var confirmErr *ErrConfirmationRequired
	if !errors.As(err, &confirmErr) {
		t.Fatalf("CloseIncident error = %v, want *ErrConfirmationRequired", err)
	}
	if confirmErr.FiringAlertCount != 1 {
		t.Errorf("FiringAlertCount = %d, want 1", confirmErr.FiringAlertCount)
	}

	// Nothing should have been mutated yet.
	var incident database.Incident
	db.Where("uuid = ?", incidentUUID).First(&incident)
	if incident.Status != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want unchanged (completed) before confirmation", incident.Status)
	}

	// Confirming resolves the alert and closes the incident.
	if err := svc.CloseIncident(context.Background(), incidentUUID, true); err != nil {
		t.Fatalf("CloseIncident with confirm=true failed: %v", err)
	}
	db.Where("uuid = ?", incidentUUID).First(&incident)
	if incident.Status != database.IncidentStatusClosed {
		t.Errorf("Status = %q, want closed", incident.Status)
	}

	var alert database.Alert
	db.Where("uuid = ?", alertUUID).First(&alert)
	if alert.Status != database.AlertStatusResolved {
		t.Errorf("alert status = %q, want resolved after confirmed close", alert.Status)
	}
}

func TestCloseIncident_AlreadyClosed_ReturnsError(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)
	if err := svc.UpdateIncidentComplete(incidentUUID, database.IncidentStatusCompleted, "sid-7", "log", "response", 100, 500); err != nil {
		t.Fatalf("UpdateIncidentComplete failed: %v", err)
	}
	if err := svc.CloseIncident(context.Background(), incidentUUID, false); err != nil {
		t.Fatalf("first CloseIncident failed: %v", err)
	}

	err := svc.CloseIncident(context.Background(), incidentUUID, false)
	if !errors.Is(err, ErrIncidentAlreadyClosed) {
		t.Errorf("CloseIncident error = %v, want ErrIncidentAlreadyClosed", err)
	}
}

// --- UnlinkAlertFromIncident Tests ---

func seedCorrelatedAlert(t *testing.T, db *gorm.DB, incidentUUID string) string {
	t.Helper()
	conf := 0.92
	alertUUID := "alert-" + incidentUUID[:8]
	if err := db.Create(&database.Alert{
		UUID:                  alertUUID,
		IncidentUUID:          incidentUUID,
		Status:                database.AlertStatusFiring,
		AlertName:             "HighCPU",
		TargetHost:            "host-99",
		SourceUUID:            "src-uuid-555",
		Correlated:            true,
		CorrelationConfidence: &conf,
		CorrelationReasoning:  "same host",
		CorrelationDecision:   "linked",
	}).Error; err != nil {
		t.Fatalf("seed correlated alert: %v", err)
	}
	return alertUUID
}

func TestUnlinkAlertFromIncident_HappyPath(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	// Spawn original incident and create a correlated alert linked to it.
	origIncidentUUID := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, origIncidentUUID)

	newIncidentUUID, err := svc.UnlinkAlertFromIncident(context.Background(), alertUUID)
	if err != nil {
		t.Fatalf("UnlinkAlertFromIncident failed: %v", err)
	}

	if newIncidentUUID == "" {
		t.Fatal("newIncidentUUID should not be empty")
	}
	if newIncidentUUID == origIncidentUUID {
		t.Error("new incident UUID should differ from the original")
	}

	// Verify new incident was created in the DB.
	var newInc database.Incident
	if err := db.Where("uuid = ?", newIncidentUUID).First(&newInc).Error; err != nil {
		t.Fatalf("new incident not found: %v", err)
	}

	// Verify alert row was repointed.
	var row database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&row).Error; err != nil {
		t.Fatalf("load alert row: %v", err)
	}
	if row.IncidentUUID != newIncidentUUID {
		t.Errorf("IncidentUUID = %q, want %q", row.IncidentUUID, newIncidentUUID)
	}
	if row.Correlated {
		t.Error("Correlated should be false after unlink")
	}
	if row.CorrelationDecision != "new_incident" {
		t.Errorf("CorrelationDecision = %q, want new_incident", row.CorrelationDecision)
	}
	if !strings.Contains(row.CorrelationReasoning, origIncidentUUID) {
		t.Errorf("CorrelationReasoning %q should contain original incident UUID %q", row.CorrelationReasoning, origIncidentUUID)
	}
	if row.CorrelationConfidence != nil {
		t.Errorf("CorrelationConfidence should be nil after unlink, got %v", row.CorrelationConfidence)
	}
}

// Origin (non-correlated) alerts can now be unlinked too — the alert is
// repointed to a fresh incident just like a correlated one.
func TestUnlinkAlertFromIncident_OriginAlertSucceeds(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	origIncidentUUID := spawnAlertIncident(t, svc)

	// Seed a non-correlated (origin) alert.
	alertUUID := "alert-origin-abc"
	if err := db.Create(&database.Alert{
		UUID:                alertUUID,
		IncidentUUID:        origIncidentUUID,
		Status:              database.AlertStatusFiring,
		AlertName:           "DiskFull",
		TargetHost:          "host-88",
		SourceUUID:          "src-uuid-777",
		Correlated:          false,
		CorrelationDecision: "new_incident",
	}).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	newIncidentUUID, err := svc.UnlinkAlertFromIncident(context.Background(), alertUUID)
	if err != nil {
		t.Fatalf("UnlinkAlertFromIncident failed: %v", err)
	}
	if newIncidentUUID == "" || newIncidentUUID == origIncidentUUID {
		t.Fatalf("expected a distinct new incident, got %q", newIncidentUUID)
	}

	var row database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&row).Error; err != nil {
		t.Fatalf("load alert row: %v", err)
	}
	if row.IncidentUUID != newIncidentUUID {
		t.Errorf("IncidentUUID = %q, want %q", row.IncidentUUID, newIncidentUUID)
	}
	if row.Correlated {
		t.Error("Correlated should be false after unlink")
	}
}

// Moving an alert to an existing incident links it there without spawning a
// new investigation.
func TestMoveAlertToIncident_LinkToExisting(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	incidentA := spawnAlertIncident(t, svc)
	incidentB := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, incidentA)

	result, err := svc.MoveAlertToIncident(context.Background(), alertUUID, incidentB)
	if err != nil {
		t.Fatalf("MoveAlertToIncident failed: %v", err)
	}
	if result != incidentB {
		t.Errorf("result = %q, want %q", result, incidentB)
	}

	var row database.Alert
	if err := db.Where("uuid = ?", alertUUID).First(&row).Error; err != nil {
		t.Fatalf("load alert row: %v", err)
	}
	if row.IncidentUUID != incidentB {
		t.Errorf("IncidentUUID = %q, want %q", row.IncidentUUID, incidentB)
	}
	if !row.Correlated {
		t.Error("Correlated should be true after linking to an existing incident")
	}
	if row.CorrelationDecision != "linked" {
		t.Errorf("CorrelationDecision = %q, want linked", row.CorrelationDecision)
	}
	if row.CorrelationConfidence != nil {
		t.Errorf("CorrelationConfidence should be nil after a manual link, got %v", row.CorrelationConfidence)
	}
	if !strings.Contains(row.CorrelationReasoning, incidentB) {
		t.Errorf("CorrelationReasoning %q should mention target incident %q", row.CorrelationReasoning, incidentB)
	}
}

func TestMoveAlertToIncident_InvalidTarget(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)

	incidentA := spawnAlertIncident(t, svc)
	alertUUID := seedCorrelatedAlert(t, db, incidentA)

	// Non-existent target.
	if _, err := svc.MoveAlertToIncident(context.Background(), alertUUID, "no-such-incident"); !errors.Is(err, ErrInvalidMoveTarget) {
		t.Errorf("expected ErrInvalidMoveTarget for missing target, got %v", err)
	}

	// Target equal to the alert's current incident.
	if _, err := svc.MoveAlertToIncident(context.Background(), alertUUID, incidentA); !errors.Is(err, ErrInvalidMoveTarget) {
		t.Errorf("expected ErrInvalidMoveTarget for same-incident target, got %v", err)
	}
}

// TestLinkAlertToIncident_MergedIncident_RedirectsToSurvivor covers the race
// where the correlator picked a candidate that got merged into a survivor
// before LinkAlertToIncident ran: the alert must attach to the survivor (and
// extend its monitor window), never to the hidden merged row.
func TestLinkAlertToIncident_MergedIncident_RedirectsToSurvivor(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	survivorUUID := spawnAlertIncident(t, svc)
	mergedUUID := spawnAlertIncident(t, svc)

	oldWindow := time.Now().Add(5 * time.Minute)
	if err := db.Model(&database.Incident{}).Where("uuid = ?", survivorUUID).
		Updates(map[string]interface{}{
			"status":        database.IncidentStatusMonitor,
			"monitor_until": &oldWindow,
		}).Error; err != nil {
		t.Fatalf("set survivor monitor: %v", err)
	}
	if err := db.Model(&database.Incident{}).Where("uuid = ?", mergedUUID).
		Updates(map[string]interface{}{
			"status":           database.IncidentStatusMerged,
			"merged_into_uuid": survivorUUID,
		}).Error; err != nil {
		t.Fatalf("set merged: %v", err)
	}

	a := alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "host-r"}
	if err := svc.LinkAlertToIncident(context.Background(), mergedUUID, "src-uuid-111", a, 0.9, "recurrence"); err != nil {
		t.Fatalf("LinkAlertToIncident failed: %v", err)
	}

	var onMerged, onSurvivor int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", mergedUUID).Count(&onMerged)
	db.Model(&database.Alert{}).Where("incident_uuid = ?", survivorUUID).Count(&onSurvivor)
	if onMerged != 0 {
		t.Errorf("alert attached to merged row (%d rows); must redirect to survivor", onMerged)
	}
	if onSurvivor != 1 {
		t.Errorf("expected 1 alert on survivor, got %d", onSurvivor)
	}

	var survivor database.Incident
	if err := db.Where("uuid = ?", survivorUUID).First(&survivor).Error; err != nil {
		t.Fatalf("load survivor: %v", err)
	}
	if survivor.MonitorUntil == nil || !survivor.MonitorUntil.After(oldWindow) {
		t.Error("survivor monitor window must be extended by the redirected link")
	}
}

// TestLinkAlertToIncident_MergedWithoutPointer_AttachesInPlace verifies the
// degraded path: a merged row with no merged_into_uuid still accepts the
// alert rather than erroring (attaching beats dropping).
func TestLinkAlertToIncident_MergedWithoutPointer_AttachesInPlace(t *testing.T) {
	db := setupIncidentTestDB(t)
	svc := newIncidentTestService(t, db)
	incidentUUID := spawnAlertIncident(t, svc)

	if err := db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Update("status", database.IncidentStatusMerged).Error; err != nil {
		t.Fatalf("set merged: %v", err)
	}

	a := alerts.NormalizedAlert{AlertName: "CPUHigh", TargetHost: "host-x"}
	if err := svc.LinkAlertToIncident(context.Background(), incidentUUID, "src-uuid-111", a, 0.9, "r"); err != nil {
		t.Fatalf("LinkAlertToIncident failed: %v", err)
	}
	var count int64
	db.Model(&database.Alert{}).Where("incident_uuid = ?", incidentUUID).Count(&count)
	if count != 1 {
		t.Errorf("expected alert attached in place, got %d rows", count)
	}
}
