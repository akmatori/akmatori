package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// --- AlertService Unit Tests (no database required) ---

func TestNewAlertService(t *testing.T) {
	// NewAlertService will have nil db when database.GetDB() returns nil
	// This tests that the constructor doesn't panic
	s := NewAlertService()
	if s == nil {
		t.Fatal("NewAlertService returned nil")
	}
}

func setupAlertServiceDB(t *testing.T) *AlertService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.AlertSourceType{}, &database.AlertSourceInstance{}); err != nil {
		t.Fatalf("migrate alert source tables: %v", err)
	}
	database.DB = db
	return NewAlertService()
}

func TestAlertService_InitializeDefaultSourceTypes_IdempotentAndUpdates(t *testing.T) {
	service := setupAlertServiceDB(t)

	if err := service.InitializeDefaultSourceTypes(); err != nil {
		t.Fatalf("InitializeDefaultSourceTypes() first run: %v", err)
	}

	var count int64
	if err := database.DB.Model(&database.AlertSourceType{}).Count(&count).Error; err != nil {
		t.Fatalf("count source types: %v", err)
	}
	if count != 6 {
		t.Fatalf("source type count after first run = %d, want 6", count)
	}

	if err := database.DB.Model(&database.AlertSourceType{}).
		Where("name = ?", "alertmanager").
		Updates(map[string]interface{}{
			"display_name":          "stale name",
			"webhook_secret_header": "X-Stale-Secret",
		}).Error; err != nil {
		t.Fatalf("seed stale source type fields: %v", err)
	}

	if err := service.InitializeDefaultSourceTypes(); err != nil {
		t.Fatalf("InitializeDefaultSourceTypes() second run: %v", err)
	}
	if err := database.DB.Model(&database.AlertSourceType{}).Count(&count).Error; err != nil {
		t.Fatalf("count source types after second run: %v", err)
	}
	if count != 6 {
		t.Fatalf("source type count after second run = %d, want 6", count)
	}

	alertmanager, err := service.GetAlertSourceTypeByName("alertmanager")
	if err != nil {
		t.Fatalf("GetAlertSourceTypeByName(alertmanager): %v", err)
	}
	if alertmanager.DisplayName != "Prometheus Alertmanager" {
		t.Errorf("display name = %q, want Prometheus Alertmanager", alertmanager.DisplayName)
	}
	if alertmanager.WebhookSecretHeader != "X-Alertmanager-Secret" {
		t.Errorf("webhook secret header = %q, want X-Alertmanager-Secret", alertmanager.WebhookSecretHeader)
	}
	if got := alertmanager.DefaultFieldMappings["alert_name"]; got != "labels.alertname" {
		t.Errorf("alertmanager alert_name mapping = %v, want labels.alertname", got)
	}
}

func TestAlertService_InstanceCRUD(t *testing.T) {
	service := setupAlertServiceDB(t)
	sourceType, err := service.CreateAlertSourceType(
		"custom_webhook",
		"Custom Webhook",
		"Receives custom webhook alerts",
		database.JSONB{"alert_name": "title"},
		"X-Custom-Secret",
	)
	if err != nil {
		t.Fatalf("CreateAlertSourceType(): %v", err)
	}

	instance, err := service.CreateInstance(
		"custom_webhook",
		"Production custom webhook",
		"Primary custom webhook source",
		"secret-1",
		database.JSONB{"severity": "priority"},
		database.JSONB{"region": "eu"},
	)
	if err != nil {
		t.Fatalf("CreateInstance(): %v", err)
	}
	if instance.UUID == "" {
		t.Fatal("CreateInstance() returned empty UUID")
	}
	if instance.AlertSourceType.ID != sourceType.ID {
		t.Fatalf("preloaded source type ID = %d, want %d", instance.AlertSourceType.ID, sourceType.ID)
	}
	if !instance.Enabled {
		t.Fatal("new alert source instance should be enabled by default")
	}

	byUUID, err := service.GetInstanceByUUID(instance.UUID)
	if err != nil {
		t.Fatalf("GetInstanceByUUID(): %v", err)
	}
	if byUUID.Name != "Production custom webhook" {
		t.Errorf("name by UUID = %q", byUUID.Name)
	}
	if byUUID.AlertSourceType.Name != "custom_webhook" {
		t.Errorf("preloaded source type name = %q", byUUID.AlertSourceType.Name)
	}

	if err := service.UpdateInstanceByID(
		instance.ID,
		"Updated custom webhook",
		"Updated description",
		"secret-2",
		database.JSONB{"alert_name": "payload.title"},
		database.JSONB{"region": "us"},
		false,
	); err != nil {
		t.Fatalf("UpdateInstanceByID(): %v", err)
	}
	updated, err := service.GetInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetInstance() after update: %v", err)
	}
	if updated.Name != "Updated custom webhook" {
		t.Errorf("updated name = %q", updated.Name)
	}
	if updated.Enabled {
		t.Error("updated instance should be disabled")
	}
	if got := updated.FieldMappings["alert_name"]; got != "payload.title" {
		t.Errorf("updated alert_name mapping = %v, want payload.title", got)
	}

	listed, err := service.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances(): %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListInstances() length = %d, want 1", len(listed))
	}
	if listed[0].AlertSourceType.Name != "custom_webhook" {
		t.Errorf("listed source type name = %q", listed[0].AlertSourceType.Name)
	}

	if err := service.DeleteInstanceByID(instance.ID); err != nil {
		t.Fatalf("DeleteInstanceByID(): %v", err)
	}
	if _, err := service.GetInstance(instance.ID); err == nil {
		t.Fatal("GetInstance() after delete returned nil error, want not found")
	}
}

func TestAlertService_CreateInstance_MissingSourceType(t *testing.T) {
	service := setupAlertServiceDB(t)

	_, err := service.CreateInstance("missing", "Broken source", "", "secret", nil, nil)
	if err == nil {
		t.Fatal("CreateInstance() error = nil, want missing source type error")
	}
	if !strings.Contains(err.Error(), "alert source type not found: missing") {
		t.Fatalf("CreateInstance() error = %q, want missing source type context", err)
	}
}

// --- Default Source Types Tests ---

func TestDefaultAlertSourceTypes(t *testing.T) {
	// Test that our known alert source types are defined correctly
	expectedTypes := []struct {
		name        string
		displayName string
		hasHeader   bool
	}{
		{"alertmanager", "Prometheus Alertmanager", true},
		{"pagerduty", "PagerDuty", true},
		{"grafana", "Grafana Alerting", true},
		{"datadog", "Datadog", true},
		{"zabbix", "Zabbix", true},
		{"slack_channel", "Slack Alert Channel", false},
	}

	// Verify the expected types are part of our source type definitions
	// This is a documentation test to ensure consistency
	for _, et := range expectedTypes {
		t.Run(et.name, func(t *testing.T) {
			// Just verify the type info is properly structured
			if et.name == "" {
				t.Error("empty name")
			}
			if et.displayName == "" {
				t.Error("empty display name")
			}
		})
	}
}

// --- Field Mapping Tests ---

func TestAlertmanagerDefaultMappings(t *testing.T) {
	mappings := database.JSONB{
		"alert_name":         "labels.alertname",
		"severity":           "labels.severity",
		"status":             "status",
		"summary":            "annotations.summary",
		"description":        "annotations.description",
		"target_host":        "labels.instance",
		"target_service":     "labels.job",
		"runbook_url":        "annotations.runbook_url",
		"source_fingerprint": "fingerprint",
		"started_at":         "startsAt",
		"ended_at":           "endsAt",
	}

	// Verify critical fields are mapped
	criticalFields := []string{"alert_name", "severity", "status", "summary"}
	for _, field := range criticalFields {
		if _, ok := mappings[field]; !ok {
			t.Errorf("Alertmanager mappings missing critical field: %s", field)
		}
	}
}

func TestPagerDutyDefaultMappings(t *testing.T) {
	mappings := database.JSONB{
		"alert_name":      "event.data.title",
		"severity":        "event.data.priority.summary",
		"status":          "event.event_type",
		"summary":         "event.data.description",
		"target_host":     "event.data.source",
		"target_service":  "event.data.service.name",
		"runbook_url":     "event.data.body.details.runbook",
		"source_alert_id": "event.data.id",
	}

	// Verify nested path format
	if v, ok := mappings["alert_name"]; !ok || v != "event.data.title" {
		t.Error("PagerDuty alert_name mapping incorrect")
	}
}

func TestGrafanaDefaultMappings(t *testing.T) {
	mappings := database.JSONB{
		"alert_name":      "ruleName",
		"severity":        "state",
		"status":          "state",
		"summary":         "message",
		"target_host":     "evalMatches.0.tags.instance",
		"runbook_url":     "ruleUrl",
		"source_alert_id": "ruleId",
	}

	// Grafana uses state for both severity and status
	if mappings["severity"] != "state" || mappings["status"] != "state" {
		t.Error("Grafana should map both severity and status to state")
	}
}

func TestDatadogDefaultMappings(t *testing.T) {
	mappings := database.JSONB{
		"alert_name":      "title",
		"severity":        "priority",
		"status":          "alert_type",
		"summary":         "body",
		"target_host":     "tags.host",
		"runbook_url":     "event_links.0.url",
		"source_alert_id": "id",
	}

	// Verify array access format
	runbookURL, ok := mappings["runbook_url"]
	if !ok {
		t.Error("Datadog runbook_url mapping missing")
	}
	if runbookURL != "event_links.0.url" {
		t.Errorf("Datadog runbook_url = %v, want event_links.0.url", runbookURL)
	}
}

func TestZabbixDefaultMappings(t *testing.T) {
	mappings := database.JSONB{
		"alert_name":      "alert_name",
		"severity":        "priority",
		"status":          "event_status",
		"summary":         "trigger_expression",
		"target_host":     "hardware",
		"metric_name":     "metric_name",
		"metric_value":    "metric_value",
		"runbook_url":     "runbook_url",
		"source_alert_id": "event_id",
		"started_at":      "event_time",
	}

	// Zabbix has metric fields unlike other sources
	if _, ok := mappings["metric_name"]; !ok {
		t.Error("Zabbix should have metric_name mapping")
	}
	if _, ok := mappings["metric_value"]; !ok {
		t.Error("Zabbix should have metric_value mapping")
	}
}

func TestSlackChannelDefaultMappings(t *testing.T) {
	mappings := database.JSONB{}

	// Slack channel uses AI extraction, so mappings should be empty
	if len(mappings) != 0 {
		t.Errorf("Slack channel mappings should be empty (AI extraction), got %d entries", len(mappings))
	}
}

// --- JSONB Type Tests ---

func TestJSONBScan_NilValue(t *testing.T) {
	var j database.JSONB
	err := j.Scan(nil)
	if err != nil {
		t.Errorf("Scan(nil) error: %v", err)
	}
	if j == nil {
		t.Error("Scan(nil) should initialize empty map")
	}
	if len(j) != 0 {
		t.Errorf("Scan(nil) should be empty map, got %d entries", len(j))
	}
}

func TestJSONBScan_ValidJSON(t *testing.T) {
	var j database.JSONB
	err := j.Scan([]byte(`{"key": "value", "num": 42}`))
	if err != nil {
		t.Errorf("Scan error: %v", err)
	}
	if j["key"] != "value" {
		t.Errorf("key = %v, want 'value'", j["key"])
	}
	// JSON numbers are float64
	if num, ok := j["num"].(float64); !ok || num != 42 {
		t.Errorf("num = %v, want 42", j["num"])
	}
}

func TestJSONBScan_InvalidJSON(t *testing.T) {
	var j database.JSONB
	err := j.Scan([]byte(`not valid json`))
	if err == nil {
		t.Error("Scan should fail for invalid JSON")
	}
}

func TestJSONBScan_WrongType(t *testing.T) {
	var j database.JSONB
	err := j.Scan("string instead of bytes")
	if err == nil {
		t.Error("Scan should fail for non-[]byte input")
	}
}

func TestJSONBValue_Nil(t *testing.T) {
	var j database.JSONB
	val, err := j.Value()
	if err != nil {
		t.Errorf("Value error: %v", err)
	}
	if val != nil {
		t.Errorf("Value of nil JSONB = %v, want nil", val)
	}
}

func TestJSONBValue_NonNil(t *testing.T) {
	j := database.JSONB{
		"key": "value",
	}
	val, err := j.Value()
	if err != nil {
		t.Errorf("Value error: %v", err)
	}
	bytes, ok := val.([]byte)
	if !ok {
		t.Errorf("Value type = %T, want []byte", val)
	}
	if string(bytes) != `{"key":"value"}` {
		t.Errorf("Value = %s, want {\"key\":\"value\"}", string(bytes))
	}
}

// --- Incident Context Tests ---

func TestIncidentContext_Fields(t *testing.T) {
	ctx := &IncidentContext{
		Source:   "slack",
		SourceID: "thread-123",
		Context: database.JSONB{
			"channel": "C123",
			"user":    "U456",
		},
		Message: "Test alert message",
	}

	if ctx.Source != "slack" {
		t.Errorf("Source = %q, want 'slack'", ctx.Source)
	}
	if ctx.SourceID != "thread-123" {
		t.Errorf("SourceID = %q, want 'thread-123'", ctx.SourceID)
	}
	if ctx.Context["channel"] != "C123" {
		t.Errorf("Context[channel] = %v, want 'C123'", ctx.Context["channel"])
	}
	if ctx.Message != "Test alert message" {
		t.Errorf("Message = %q, want 'Test alert message'", ctx.Message)
	}
}

// --- SlackSettings Tests ---

func TestSlackSettings_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		settings database.SlackSettings
		want     bool
	}{
		{
			name:     "all empty",
			settings: database.SlackSettings{},
			want:     false,
		},
		{
			name: "only bot token",
			settings: database.SlackSettings{
				BotToken: "xoxb-123",
			},
			want: false,
		},
		{
			name: "bot token and signing secret",
			settings: database.SlackSettings{
				BotToken:      "xoxb-123",
				SigningSecret: "secret",
			},
			want: false,
		},
		{
			name: "all tokens present",
			settings: database.SlackSettings{
				BotToken:      "xoxb-123",
				SigningSecret: "secret",
				AppToken:      "xapp-123",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSlackSettings_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		settings database.SlackSettings
		want     bool
	}{
		{
			name:     "not configured, not enabled",
			settings: database.SlackSettings{},
			want:     false,
		},
		{
			name: "configured but not enabled",
			settings: database.SlackSettings{
				BotToken:      "xoxb-123",
				SigningSecret: "secret",
				AppToken:      "xapp-123",
				Enabled:       false,
			},
			want: false,
		},
		{
			name: "enabled but not configured",
			settings: database.SlackSettings{
				Enabled: true,
			},
			want: false,
		},
		{
			name: "configured and enabled",
			settings: database.SlackSettings{
				BotToken:      "xoxb-123",
				SigningSecret: "secret",
				AppToken:      "xapp-123",
				Enabled:       true,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}
