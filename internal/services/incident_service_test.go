package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// Title should be truncated to reasonable length
	if len(incident.Title) > 100 {
		t.Errorf("Title too long: %d chars (should be truncated)", len(incident.Title))
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
	if err := svc.InsertFiringAlert(context.Background(), incidentUUID, "src-uuid-111", a); err != nil {
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
	if err := db.Where("incident_uuid = ?", incidentUUID).First(&row).Error; err != nil {
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
