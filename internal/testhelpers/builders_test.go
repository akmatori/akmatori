package testhelpers

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestSkillBuilder(t *testing.T) {
	skill := NewSkillBuilder().
		WithID(42).
		WithName("zabbix-analyst").
		WithDescription("Analyzes Zabbix alerts").
		WithCategory("monitoring").
		Build()

	if skill.ID != 42 {
		t.Errorf("expected ID 42, got %d", skill.ID)
	}
	if skill.Name != "zabbix-analyst" {
		t.Errorf("expected Name 'zabbix-analyst', got %s", skill.Name)
	}
	if skill.Description != "Analyzes Zabbix alerts" {
		t.Errorf("expected Description 'Analyzes Zabbix alerts', got %s", skill.Description)
	}
	if skill.Category != "monitoring" {
		t.Errorf("expected Category 'monitoring', got %s", skill.Category)
	}
	if !skill.Enabled {
		t.Error("expected Enabled true")
	}
	if skill.IsSystem {
		t.Error("expected IsSystem false")
	}
}

func TestSkillBuilder_AsSystem(t *testing.T) {
	skill := NewSkillBuilder().AsSystem().Build()

	if !skill.IsSystem {
		t.Error("expected IsSystem true")
	}
}

func TestSkillBuilder_Disabled(t *testing.T) {
	skill := NewSkillBuilder().Disabled().Build()

	if skill.Enabled {
		t.Error("expected Enabled false")
	}
}

func TestSkillBuilder_WithTools(t *testing.T) {
	tool1 := NewToolInstanceBuilder().WithName("tool-1").Build()
	tool2 := NewToolInstanceBuilder().WithName("tool-2").Build()

	skill := NewSkillBuilder().WithTools(tool1, tool2).Build()

	if len(skill.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(skill.Tools))
	}
}

func TestToolInstanceBuilder(t *testing.T) {
	instance := NewToolInstanceBuilder().
		WithID(10).
		WithToolTypeID(5).
		WithName("prod-zabbix").
		WithSettings(database.JSONB{"url": "https://zabbix.example.com"}).
		Build()

	if instance.ID != 10 {
		t.Errorf("expected ID 10, got %d", instance.ID)
	}
	if instance.ToolTypeID != 5 {
		t.Errorf("expected ToolTypeID 5, got %d", instance.ToolTypeID)
	}
	if instance.Name != "prod-zabbix" {
		t.Errorf("expected Name 'prod-zabbix', got %s", instance.Name)
	}
	if instance.Settings["url"] != "https://zabbix.example.com" {
		t.Errorf("expected Settings[url] 'https://zabbix.example.com', got %v", instance.Settings["url"])
	}
	if !instance.Enabled {
		t.Error("expected Enabled true")
	}
}

func TestToolInstanceBuilder_WithSetting(t *testing.T) {
	instance := NewToolInstanceBuilder().
		WithSetting("api_key", "secret123").
		WithSetting("timeout", 30).
		Build()

	if instance.Settings["api_key"] != "secret123" {
		t.Errorf("expected api_key 'secret123', got %v", instance.Settings["api_key"])
	}
	if instance.Settings["timeout"] != 30 {
		t.Errorf("expected timeout 30, got %v", instance.Settings["timeout"])
	}
}

func TestToolInstanceBuilder_Disabled(t *testing.T) {
	instance := NewToolInstanceBuilder().Disabled().Build()

	if instance.Enabled {
		t.Error("expected Enabled false")
	}
}

func TestToolTypeBuilder(t *testing.T) {
	toolType := NewToolTypeBuilder().
		WithID(3).
		WithName("zabbix").
		WithDescription("Zabbix monitoring integration").
		WithSchema(database.JSONB{"required": []string{"url", "api_key"}}).
		Build()

	if toolType.ID != 3 {
		t.Errorf("expected ID 3, got %d", toolType.ID)
	}
	if toolType.Name != "zabbix" {
		t.Errorf("expected Name 'zabbix', got %s", toolType.Name)
	}
	if toolType.Description != "Zabbix monitoring integration" {
		t.Errorf("expected Description 'Zabbix monitoring integration', got %s", toolType.Description)
	}
}

func TestAlertSourceInstanceBuilder(t *testing.T) {
	instance := NewAlertSourceInstanceBuilder().
		WithID(100).
		WithUUID("custom-uuid-123").
		WithAlertSourceTypeID(2).
		WithName("Production Alertmanager").
		WithDescription("Prod alerts").
		WithWebhookSecret("supersecret").
		WithFieldMappings(database.JSONB{"severity": "labels.severity"}).
		Build()

	if instance.ID != 100 {
		t.Errorf("expected ID 100, got %d", instance.ID)
	}
	if instance.UUID != "custom-uuid-123" {
		t.Errorf("expected UUID 'custom-uuid-123', got %s", instance.UUID)
	}
	if instance.AlertSourceTypeID != 2 {
		t.Errorf("expected AlertSourceTypeID 2, got %d", instance.AlertSourceTypeID)
	}
	if instance.Name != "Production Alertmanager" {
		t.Errorf("expected Name 'Production Alertmanager', got %s", instance.Name)
	}
	if instance.WebhookSecret != "supersecret" {
		t.Errorf("expected WebhookSecret 'supersecret', got %s", instance.WebhookSecret)
	}
	if !instance.Enabled {
		t.Error("expected Enabled true")
	}
}

func TestAlertSourceInstanceBuilder_Disabled(t *testing.T) {
	instance := NewAlertSourceInstanceBuilder().Disabled().Build()

	if instance.Enabled {
		t.Error("expected Enabled false")
	}
}

func TestLLMSettingsBuilder(t *testing.T) {
	settings := NewLLMSettingsBuilder().
		WithID(1).
		WithProvider(database.LLMProviderAnthropic).
		WithAPIKey("sk-test-key").
		WithModel("claude-3-opus").
		WithThinkingLevel(database.ThinkingLevelHigh).
		WithBaseURL("https://api.anthropic.com").
		Build()

	if settings.ID != 1 {
		t.Errorf("expected ID 1, got %d", settings.ID)
	}
	if settings.Provider != database.LLMProviderAnthropic {
		t.Errorf("expected Provider 'anthropic', got %s", settings.Provider)
	}
	if settings.APIKey != "sk-test-key" {
		t.Errorf("expected APIKey 'sk-test-key', got %s", settings.APIKey)
	}
	if settings.Model != "claude-3-opus" {
		t.Errorf("expected Model 'claude-3-opus', got %s", settings.Model)
	}
	if settings.ThinkingLevel != database.ThinkingLevelHigh {
		t.Errorf("expected ThinkingLevel 'high', got %s", settings.ThinkingLevel)
	}
	if !settings.Enabled {
		t.Error("expected Enabled true")
	}
	if !settings.Active {
		t.Error("expected Active true")
	}
}

func TestLLMSettingsBuilder_DisabledAndInactive(t *testing.T) {
	settings := NewLLMSettingsBuilder().Disabled().Inactive().Build()

	if settings.Enabled {
		t.Error("expected Enabled false")
	}
	if settings.Active {
		t.Error("expected Active false")
	}
}

func TestSlackSettingsBuilder(t *testing.T) {
	settings := NewSlackSettingsBuilder().
		WithID(1).
		WithBotToken("xoxb-custom-token").
		WithSigningSecret("custom-signing-secret").
		WithAppToken("xapp-custom-token").
		WithAlertsChannel("#custom-alerts").
		Build()

	if settings.ID != 1 {
		t.Errorf("expected ID 1, got %d", settings.ID)
	}
	if settings.BotToken != "xoxb-custom-token" {
		t.Errorf("expected BotToken 'xoxb-custom-token', got %s", settings.BotToken)
	}
	if settings.AlertsChannel != "#custom-alerts" {
		t.Errorf("expected AlertsChannel '#custom-alerts', got %s", settings.AlertsChannel)
	}
	if !settings.Enabled {
		t.Error("expected Enabled true")
	}
	if !settings.IsConfigured() {
		t.Error("expected IsConfigured true")
	}
	if !settings.IsActive() {
		t.Error("expected IsActive true")
	}
}

func TestSlackSettingsBuilder_Unconfigured(t *testing.T) {
	settings := NewSlackSettingsBuilder().Unconfigured().Build()

	if settings.IsConfigured() {
		t.Error("expected IsConfigured false")
	}
	if settings.IsActive() {
		t.Error("expected IsActive false (disabled or unconfigured)")
	}
}

func TestSlackSettingsBuilder_Disabled(t *testing.T) {
	settings := NewSlackSettingsBuilder().Disabled().Build()

	if settings.Enabled {
		t.Error("expected Enabled false")
	}
	if settings.IsActive() {
		t.Error("expected IsActive false")
	}
}

// Benchmarks for builders
func BenchmarkSkillBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewSkillBuilder().
			WithID(uint(i)).
			WithName("test-skill").
			WithDescription("Test skill").
			WithCategory("testing").
			Build()
	}
}

func BenchmarkToolInstanceBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewToolInstanceBuilder().
			WithID(uint(i)).
			WithToolTypeID(1).
			WithName("test-instance").
			WithSetting("key", "value").
			Build()
	}
}

func BenchmarkAlertSourceInstanceBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewAlertSourceInstanceBuilder().
			WithID(uint(i)).
			WithName("test-alert-source").
			WithWebhookSecret("secret").
			Build()
	}
}

func BenchmarkLLMSettingsBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewLLMSettingsBuilder().
			WithID(uint(i)).
			WithProvider(database.LLMProviderOpenAI).
			WithAPIKey("sk-test").
			WithModel("gpt-4").
			Build()
	}
}

func BenchmarkSlackSettingsBuilder(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewSlackSettingsBuilder().
			WithID(uint(i)).
			WithBotToken("xoxb-test").
			WithAlertsChannel("#alerts").
			Build()
	}
}
