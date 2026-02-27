// Package testhelpers provides additional data builders for testing
package testhelpers

import (
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// ========================================
// Skill Builder
// ========================================

// SkillBuilder builds Skill instances for testing
type SkillBuilder struct {
	skill database.Skill
}

// NewSkillBuilder creates a new skill builder with defaults
func NewSkillBuilder() *SkillBuilder {
	return &SkillBuilder{
		skill: database.Skill{
			Name:        "test-skill",
			Description: "Test skill for unit tests",
			Category:    "testing",
			IsSystem:    false,
			Enabled:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
}

// WithID sets the skill ID
func (b *SkillBuilder) WithID(id uint) *SkillBuilder {
	b.skill.ID = id
	return b
}

// WithName sets the skill name
func (b *SkillBuilder) WithName(name string) *SkillBuilder {
	b.skill.Name = name
	return b
}

// WithDescription sets the description
func (b *SkillBuilder) WithDescription(desc string) *SkillBuilder {
	b.skill.Description = desc
	return b
}

// WithCategory sets the category
func (b *SkillBuilder) WithCategory(category string) *SkillBuilder {
	b.skill.Category = category
	return b
}

// AsSystem marks the skill as a system skill
func (b *SkillBuilder) AsSystem() *SkillBuilder {
	b.skill.IsSystem = true
	return b
}

// Disabled sets the skill as disabled
func (b *SkillBuilder) Disabled() *SkillBuilder {
	b.skill.Enabled = false
	return b
}

// WithTools adds tool instances to the skill
func (b *SkillBuilder) WithTools(tools ...database.ToolInstance) *SkillBuilder {
	b.skill.Tools = append(b.skill.Tools, tools...)
	return b
}

// Build returns the constructed skill
func (b *SkillBuilder) Build() database.Skill {
	return b.skill
}

// ========================================
// Tool Instance Builder
// ========================================

// ToolInstanceBuilder builds ToolInstance instances for testing
type ToolInstanceBuilder struct {
	instance database.ToolInstance
}

// NewToolInstanceBuilder creates a new tool instance builder with defaults
func NewToolInstanceBuilder() *ToolInstanceBuilder {
	return &ToolInstanceBuilder{
		instance: database.ToolInstance{
			ToolTypeID: 1,
			Name:       "test-tool-instance",
			Settings:   database.JSONB{"host": "localhost", "port": 8080},
			Enabled:    true,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		},
	}
}

// WithID sets the instance ID
func (b *ToolInstanceBuilder) WithID(id uint) *ToolInstanceBuilder {
	b.instance.ID = id
	return b
}

// WithToolTypeID sets the tool type ID
func (b *ToolInstanceBuilder) WithToolTypeID(id uint) *ToolInstanceBuilder {
	b.instance.ToolTypeID = id
	return b
}

// WithName sets the instance name
func (b *ToolInstanceBuilder) WithName(name string) *ToolInstanceBuilder {
	b.instance.Name = name
	return b
}

// WithSettings sets the instance settings
func (b *ToolInstanceBuilder) WithSettings(settings database.JSONB) *ToolInstanceBuilder {
	b.instance.Settings = settings
	return b
}

// WithSetting adds a single setting
func (b *ToolInstanceBuilder) WithSetting(key string, value interface{}) *ToolInstanceBuilder {
	if b.instance.Settings == nil {
		b.instance.Settings = database.JSONB{}
	}
	b.instance.Settings[key] = value
	return b
}

// Disabled sets the instance as disabled
func (b *ToolInstanceBuilder) Disabled() *ToolInstanceBuilder {
	b.instance.Enabled = false
	return b
}

// WithToolType sets the tool type
func (b *ToolInstanceBuilder) WithToolType(tt database.ToolType) *ToolInstanceBuilder {
	b.instance.ToolType = tt
	return b
}

// Build returns the constructed tool instance
func (b *ToolInstanceBuilder) Build() database.ToolInstance {
	return b.instance
}

// ========================================
// Tool Type Builder
// ========================================

// ToolTypeBuilder builds ToolType instances for testing
type ToolTypeBuilder struct {
	toolType database.ToolType
}

// NewToolTypeBuilder creates a new tool type builder with defaults
func NewToolTypeBuilder() *ToolTypeBuilder {
	return &ToolTypeBuilder{
		toolType: database.ToolType{
			Name:        "test_tool",
			Description: "Test tool type for unit tests",
			Schema:      database.JSONB{},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
}

// WithID sets the tool type ID
func (b *ToolTypeBuilder) WithID(id uint) *ToolTypeBuilder {
	b.toolType.ID = id
	return b
}

// WithName sets the tool type name
func (b *ToolTypeBuilder) WithName(name string) *ToolTypeBuilder {
	b.toolType.Name = name
	return b
}

// WithDescription sets the description
func (b *ToolTypeBuilder) WithDescription(desc string) *ToolTypeBuilder {
	b.toolType.Description = desc
	return b
}

// WithSchema sets the settings schema
func (b *ToolTypeBuilder) WithSchema(schema database.JSONB) *ToolTypeBuilder {
	b.toolType.Schema = schema
	return b
}

// Build returns the constructed tool type
func (b *ToolTypeBuilder) Build() database.ToolType {
	return b.toolType
}

// ========================================
// Alert Source Instance Builder
// ========================================

// AlertSourceInstanceBuilder builds AlertSourceInstance instances for testing
type AlertSourceInstanceBuilder struct {
	instance database.AlertSourceInstance
}

// NewAlertSourceInstanceBuilder creates a new alert source instance builder with defaults
func NewAlertSourceInstanceBuilder() *AlertSourceInstanceBuilder {
	return &AlertSourceInstanceBuilder{
		instance: database.AlertSourceInstance{
			UUID:              "test-uuid-" + time.Now().Format("20060102150405"),
			AlertSourceTypeID: 1,
			Name:              "Test Alert Source",
			Description:       "Test alert source for unit tests",
			WebhookSecret:     "",
			FieldMappings:     database.JSONB{},
			Settings:          database.JSONB{},
			Enabled:           true,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}
}

// WithID sets the instance ID
func (b *AlertSourceInstanceBuilder) WithID(id uint) *AlertSourceInstanceBuilder {
	b.instance.ID = id
	return b
}

// WithUUID sets the instance UUID
func (b *AlertSourceInstanceBuilder) WithUUID(uuid string) *AlertSourceInstanceBuilder {
	b.instance.UUID = uuid
	return b
}

// WithAlertSourceTypeID sets the alert source type ID
func (b *AlertSourceInstanceBuilder) WithAlertSourceTypeID(id uint) *AlertSourceInstanceBuilder {
	b.instance.AlertSourceTypeID = id
	return b
}

// WithName sets the instance name
func (b *AlertSourceInstanceBuilder) WithName(name string) *AlertSourceInstanceBuilder {
	b.instance.Name = name
	return b
}

// WithDescription sets the description
func (b *AlertSourceInstanceBuilder) WithDescription(desc string) *AlertSourceInstanceBuilder {
	b.instance.Description = desc
	return b
}

// WithWebhookSecret sets the webhook secret
func (b *AlertSourceInstanceBuilder) WithWebhookSecret(secret string) *AlertSourceInstanceBuilder {
	b.instance.WebhookSecret = secret
	return b
}

// WithFieldMappings sets the field mappings
func (b *AlertSourceInstanceBuilder) WithFieldMappings(mappings database.JSONB) *AlertSourceInstanceBuilder {
	b.instance.FieldMappings = mappings
	return b
}

// WithSettings sets the instance settings
func (b *AlertSourceInstanceBuilder) WithSettings(settings database.JSONB) *AlertSourceInstanceBuilder {
	b.instance.Settings = settings
	return b
}

// Disabled sets the instance as disabled
func (b *AlertSourceInstanceBuilder) Disabled() *AlertSourceInstanceBuilder {
	b.instance.Enabled = false
	return b
}

// Build returns the constructed alert source instance
func (b *AlertSourceInstanceBuilder) Build() database.AlertSourceInstance {
	return b.instance
}

// ========================================
// LLM Settings Builder
// ========================================

// LLMSettingsBuilder builds LLMSettings instances for testing
type LLMSettingsBuilder struct {
	settings database.LLMSettings
}

// NewLLMSettingsBuilder creates a new LLM settings builder with defaults
func NewLLMSettingsBuilder() *LLMSettingsBuilder {
	return &LLMSettingsBuilder{
		settings: database.LLMSettings{
			Provider:      database.LLMProviderOpenAI,
			APIKey:        "test-api-key",
			Model:         "gpt-4",
			ThinkingLevel: database.ThinkingLevelMedium,
			BaseURL:       "",
			Enabled:       true,
			Active:        true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		},
	}
}

// WithID sets the settings ID
func (b *LLMSettingsBuilder) WithID(id uint) *LLMSettingsBuilder {
	b.settings.ID = id
	return b
}

// WithProvider sets the LLM provider
func (b *LLMSettingsBuilder) WithProvider(provider database.LLMProvider) *LLMSettingsBuilder {
	b.settings.Provider = provider
	return b
}

// WithAPIKey sets the API key
func (b *LLMSettingsBuilder) WithAPIKey(key string) *LLMSettingsBuilder {
	b.settings.APIKey = key
	return b
}

// WithModel sets the model
func (b *LLMSettingsBuilder) WithModel(model string) *LLMSettingsBuilder {
	b.settings.Model = model
	return b
}

// WithThinkingLevel sets the thinking level
func (b *LLMSettingsBuilder) WithThinkingLevel(level database.ThinkingLevel) *LLMSettingsBuilder {
	b.settings.ThinkingLevel = level
	return b
}

// WithBaseURL sets a custom base URL
func (b *LLMSettingsBuilder) WithBaseURL(url string) *LLMSettingsBuilder {
	b.settings.BaseURL = url
	return b
}

// Disabled sets the settings as disabled
func (b *LLMSettingsBuilder) Disabled() *LLMSettingsBuilder {
	b.settings.Enabled = false
	return b
}

// Inactive sets the settings as inactive
func (b *LLMSettingsBuilder) Inactive() *LLMSettingsBuilder {
	b.settings.Active = false
	return b
}

// Build returns the constructed LLM settings
func (b *LLMSettingsBuilder) Build() database.LLMSettings {
	return b.settings
}

// ========================================
// Slack Settings Builder
// ========================================

// SlackSettingsBuilder builds SlackSettings instances for testing
type SlackSettingsBuilder struct {
	settings database.SlackSettings
}

// NewSlackSettingsBuilder creates a new Slack settings builder with defaults
func NewSlackSettingsBuilder() *SlackSettingsBuilder {
	return &SlackSettingsBuilder{
		settings: database.SlackSettings{
			BotToken:      "xoxb-test-token",
			SigningSecret: "test-signing-secret",
			AppToken:      "xapp-test-token",
			AlertsChannel: "#alerts",
			Enabled:       true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		},
	}
}

// WithID sets the settings ID
func (b *SlackSettingsBuilder) WithID(id uint) *SlackSettingsBuilder {
	b.settings.ID = id
	return b
}

// WithBotToken sets the bot token
func (b *SlackSettingsBuilder) WithBotToken(token string) *SlackSettingsBuilder {
	b.settings.BotToken = token
	return b
}

// WithSigningSecret sets the signing secret
func (b *SlackSettingsBuilder) WithSigningSecret(secret string) *SlackSettingsBuilder {
	b.settings.SigningSecret = secret
	return b
}

// WithAppToken sets the app token
func (b *SlackSettingsBuilder) WithAppToken(token string) *SlackSettingsBuilder {
	b.settings.AppToken = token
	return b
}

// WithAlertsChannel sets the alerts channel
func (b *SlackSettingsBuilder) WithAlertsChannel(channel string) *SlackSettingsBuilder {
	b.settings.AlertsChannel = channel
	return b
}

// Disabled sets the settings as disabled
func (b *SlackSettingsBuilder) Disabled() *SlackSettingsBuilder {
	b.settings.Enabled = false
	return b
}

// Unconfigured clears the required tokens
func (b *SlackSettingsBuilder) Unconfigured() *SlackSettingsBuilder {
	b.settings.BotToken = ""
	b.settings.SigningSecret = ""
	b.settings.AppToken = ""
	return b
}

// Build returns the constructed Slack settings
func (b *SlackSettingsBuilder) Build() database.SlackSettings {
	return b.settings
}
