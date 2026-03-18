package database

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SystemSetting stores key-value pairs for system configuration (JWT secret, admin password hash, etc.)
type SystemSetting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// System setting key constants
const (
	SystemSettingJWTSecret         = "jwt_secret"
	SystemSettingAdminPasswordHash = "admin_password_hash"
	SystemSettingSetupCompleted    = "setup_completed"
)

// GetSystemSetting retrieves a system setting by key. Returns empty string and error if not found.
func GetSystemSetting(key string) (string, error) {
	var setting SystemSetting
	if err := DB.Where("key = ?", key).First(&setting).Error; err != nil {
		return "", err
	}
	return setting.Value, nil
}

// SetSystemSetting creates or updates a system setting.
func SetSystemSetting(key, value string) error {
	setting := SystemSetting{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
	}
	return DB.Save(&setting).Error
}

// HasSystemSetting returns true if the key exists in system_settings.
func HasSystemSetting(key string) bool {
	var count int64
	DB.Model(&SystemSetting{}).Where("key = ?", key).Count(&count)
	return count > 0
}

// DB is the global database instance
var DB *gorm.DB

// Connect establishes a connection to the PostgreSQL database
func Connect(dsn string, logLevel logger.LogLevel) error {
	var err error

	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	slog.Info("database connection established")
	return nil
}

// AutoMigrate runs database migrations
func AutoMigrate() error {
	slog.Info("running database migrations")

	err := DB.AutoMigrate(
		&SystemSetting{},
		&SlackSettings{},
		&LLMSettings{},
		&ProxySettings{},
		&ContextFile{},
		&Skill{},
		&ToolType{},
		&ToolInstance{},
		&SkillTool{},
		&EventSource{},
		&Incident{},
		&APIKeySettings{},
		// Alert source models
		&AlertSourceType{},
		&AlertSourceInstance{},
		&GeneralSettings{},
		&Runbook{},
		&HTTPConnector{},
		&MCPServerConfig{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	slog.Info("database migrations completed successfully")
	return nil
}

// InitializeDefaults creates default records if they don't exist
func InitializeDefaults() error {
	slog.Info("initializing default database records")

	// Create default Slack settings if they don't exist
	var count int64
	DB.Model(&SlackSettings{}).Count(&count)
	if count == 0 {
		defaultSlackSettings := &SlackSettings{
			Enabled: false, // Disabled by default until configured
		}
		if err := DB.Create(defaultSlackSettings).Error; err != nil {
			return fmt.Errorf("failed to create default slack settings: %w", err)
		}
		slog.Info("created default Slack settings (disabled)")
	}

	// Migrate LLM settings to per-provider storage.
	// Seed one row per provider so each has its own API key and config.
	if err := seedLLMProviders(); err != nil {
		return fmt.Errorf("failed to seed LLM providers: %w", err)
	}

	// Initialize system skill (incident-manager)
	if err := InitializeSystemSkill(); err != nil {
		return fmt.Errorf("failed to initialize system skill: %w", err)
	}

	return nil
}

// Default models per provider, used when seeding new provider rows.
var defaultModelsPerProvider = map[LLMProvider]string{
	LLMProviderOpenAI:     "gpt-5.4",
	LLMProviderAnthropic:  "claude-sonnet-4-6",
	LLMProviderGoogle:     "gemini-2.5-pro",
	LLMProviderOpenRouter: "anthropic/claude-sonnet-4-6",
	LLMProviderCustom:     "",
}

// seedLLMProviders ensures one row per provider exists in the llm_settings table.
// Creates all provider rows with openai as active if no rows exist yet.
func seedLLMProviders() error {
	var count int64
	DB.Model(&LLMSettings{}).Count(&count)
	if count > 0 {
		return nil
	}

	for _, p := range ValidLLMProviders() {
		row := &LLMSettings{
			Provider:      p,
			Model:         defaultModelsPerProvider[p],
			ThinkingLevel: ThinkingLevelMedium,
			Enabled:       false,
			Active:        p == LLMProviderOpenAI,
		}
		if err := DB.Create(row).Error; err != nil {
			return fmt.Errorf("failed to create LLM settings for %s: %w", p, err)
		}
	}
	slog.Info("created default LLM settings for all providers")
	return nil
}

// DefaultIncidentManagerPrompt is the default prompt for the incident-manager system skill
const DefaultIncidentManagerPrompt = `You are a Senior Incident Manager responsible for triaging, investigating, and resolving infrastructure incidents. You coordinate responses by delegating tasks to specialized skills.

## Your Responsibilities

1. **Triage**: Assess incident severity and impact when alerts or questions arrive
2. **Investigate**: Gather relevant data by invoking appropriate skills
3. **Coordinate**: Orchestrate multiple skills when complex investigation is needed
4. **Resolve**: Provide clear findings, root cause analysis, and remediation steps
5. **Communicate**: Deliver concise, actionable responses

## Investigation Workflow

1. **Understand the problem**: Read the alert/question carefully
2. **Load relevant skills**: Your system prompt lists available skills with descriptions and file paths. Read the SKILL.md file for each skill relevant to this incident — it contains your assigned tools with full parameter schemas and gateway_call usage examples
3. **Call tools via gateway_call**: Use the gateway_call examples from SKILL.md to invoke infrastructure tools. NEVER call tool names directly (e.g. victoria_metrics.label_values) — they are NOT agent tools. Always use gateway_call({ tool_name: "...", args: {...}, instance: "..." })
4. **Fallback discovery**: If SKILL.md doesn't cover a tool you need, use list_tools_for_tool_type and get_tool_detail to discover it
5. **Correlate findings**: Connect information from multiple sources
6. **Determine root cause**: Identify what triggered the incident
7. **Recommend actions**: Suggest specific remediation steps

## Response Guidelines

- Be concise but thorough
- Include specific metrics and timestamps when available
- Clearly state the root cause if identified
- Provide actionable next steps
- Escalate when the issue is beyond your capability to resolve

## When to Escalate

Escalate to human operators when:
- The issue requires manual intervention you cannot perform
- Security incidents are detected
- Data loss or corruption is suspected
- The problem persists after attempted remediation
- You lack the necessary skills or access to resolve the issue

## Runbooks

Before starting your investigation, check the /akmatori/runbooks/ directory for relevant runbooks.
If a matching runbook exists, follow its procedures as your primary investigation guide.`

// InitializeSystemSkill creates the incident-manager system skill if it doesn't exist
func InitializeSystemSkill() error {
	slog.Info("checking for incident-manager system skill")

	var skill Skill
	result := DB.Where("name = ?", "incident-manager").First(&skill)

	if result.Error == nil {
		// Skill exists, ensure it's marked as system
		if !skill.IsSystem {
			DB.Model(&skill).Update("is_system", true)
			slog.Info("updated incident-manager skill to system skill")
		}
		return nil
	}

	// Skill doesn't exist, create it
	// Create the system skill
	skill = Skill{
		Name:        "incident-manager",
		Description: "Core system skill for managing incidents and orchestrating other skills",
		Category:    "system",
		IsSystem:    true,
		Enabled:     true,
	}

	if err := DB.Create(&skill).Error; err != nil {
		return fmt.Errorf("failed to create incident-manager skill: %w", err)
	}

	slog.Info("created incident-manager system skill", "id", skill.ID)

	return nil
}

// GetSlackSettings retrieves Slack settings from the database
func GetSlackSettings() (*SlackSettings, error) {
	var settings SlackSettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateSlackSettings updates Slack settings in the database
func UpdateSlackSettings(settings *SlackSettings) error {
	return DB.Model(&SlackSettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// GetLLMSettings retrieves the active provider's LLM settings.
// This is the primary function used by incident dispatch — it returns the
// provider the user has selected as active.
func GetLLMSettings() (*LLMSettings, error) {
	var settings LLMSettings
	if err := DB.Where("active = ?", true).First(&settings).Error; err != nil {
		// Fallback: return first enabled provider if none is marked active
		if err2 := DB.Where("enabled = ?", true).First(&settings).Error; err2 != nil {
			// Final fallback: return any row
			if err3 := DB.First(&settings).Error; err3 != nil {
				return nil, err3
			}
		}
	}
	return &settings, nil
}

// GetAllLLMSettings returns all provider settings (one row per provider).
func GetAllLLMSettings() ([]LLMSettings, error) {
	var settings []LLMSettings
	if err := DB.Order("id asc").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// GetLLMSettingsByProvider returns settings for a specific provider.
func GetLLMSettingsByProvider(provider LLMProvider) (*LLMSettings, error) {
	var settings LLMSettings
	if err := DB.Where("provider = ?", provider).First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// SetActiveLLMProvider marks the given provider as active and deactivates all others.
func SetActiveLLMProvider(provider LLMProvider) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&LLMSettings{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		return tx.Model(&LLMSettings{}).Where("provider = ?", provider).Update("active", true).Error
	})
}

// UpdateLLMSettings updates LLM settings in the database
func UpdateLLMSettings(settings *LLMSettings) error {
	return DB.Model(&LLMSettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// GetDB returns the database instance
func GetDB() *gorm.DB {
	return DB
}

// GetAPIKeySettings retrieves API key settings from the database
func GetAPIKeySettings() (*APIKeySettings, error) {
	var settings APIKeySettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateAPIKeySettings updates API key settings in the database
func UpdateAPIKeySettings(settings *APIKeySettings) error {
	return DB.Model(&APIKeySettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// Close closes the database connection
func Close() error {
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// GetProxySettings retrieves proxy settings from the database
func GetProxySettings() (*ProxySettings, error) {
	var settings ProxySettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateProxySettings updates proxy settings in the database
func UpdateProxySettings(settings *ProxySettings) error {
	return DB.Model(&ProxySettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// GetOrCreateProxySettings gets existing settings or creates default
func GetOrCreateProxySettings() (*ProxySettings, error) {
	var settings ProxySettings
	err := DB.First(&settings).Error
	if err == gorm.ErrRecordNotFound {
		settings = ProxySettings{
			OpenAIEnabled: true,
			SlackEnabled:  true,
			ZabbixEnabled: false,
		}
		if err := DB.Create(&settings).Error; err != nil {
			return nil, err
		}
		return &settings, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// GetOrCreateGeneralSettings retrieves or creates general settings (singleton)
func GetOrCreateGeneralSettings() (*GeneralSettings, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var settings GeneralSettings
	err := DB.First(&settings).Error
	if err == gorm.ErrRecordNotFound {
		settings = GeneralSettings{}
		if err := DB.Create(&settings).Error; err != nil {
			return nil, err
		}
		return &settings, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateGeneralSettings updates general settings in the database
func UpdateGeneralSettings(settings *GeneralSettings) error {
	return DB.Save(settings).Error
}

// SlugifyLogicalName converts a user-friendly name to a machine-friendly logical name.
// e.g., "Production Zabbix" -> "production-zabbix"
func SlugifyLogicalName(name string) string {
	s := strings.ToLower(name)
	// Replace non-alphanumeric characters with hyphens
	result := make([]byte, 0, len(s))
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
			prevHyphen = false
		} else if !prevHyphen && len(result) > 0 {
			result = append(result, '-')
			prevHyphen = true
		}
	}
	// Trim trailing hyphen
	if len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	if len(result) > 128 {
		result = result[:128]
		if result[len(result)-1] == '-' {
			result = result[:len(result)-1]
		}
	}
	return string(result)
}
