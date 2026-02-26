package database

import (
	"fmt"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

	log.Println("Database connection established")
	return nil
}

// AutoMigrate runs database migrations
func AutoMigrate() error {
	log.Println("Running database migrations...")

	// Drop old openai_settings table (replaced by llm_settings)
	if DB.Migrator().HasTable("openai_settings") {
		if err := DB.Migrator().DropTable("openai_settings"); err != nil {
			log.Printf("Warning: failed to drop openai_settings table: %v", err)
		} else {
			log.Println("Dropped old openai_settings table")
		}
	}

	err := DB.AutoMigrate(
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
		// Alert aggregation models
		&IncidentAlert{},
		&IncidentMerge{},
		&AggregationSettings{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Migrate proxy settings from OpenAI settings if they exist
	if err := migrateProxySettings(DB); err != nil {
		log.Printf("Warning: proxy settings migration failed: %v", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}

// InitializeDefaults creates default records if they don't exist
func InitializeDefaults() error {
	log.Println("Initializing default database records...")

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
		log.Println("Created default Slack settings (disabled)")
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
	LLMProviderOpenAI:     "gpt-5.2-codex",
	LLMProviderAnthropic:  "claude-sonnet-4-5",
	LLMProviderGoogle:     "gemini-2.5-pro",
	LLMProviderOpenRouter: "anthropic/claude-sonnet-4-5",
	LLMProviderCustom:     "",
}

// seedLLMProviders ensures one row per provider exists in the llm_settings table.
// On first run (fresh DB), it creates all provider rows with openai as active.
// On upgrade (existing single-row DB), it preserves the existing row and creates
// missing provider rows.
func seedLLMProviders() error {
	var count int64
	DB.Model(&LLMSettings{}).Count(&count)

	if count == 0 {
		// Fresh database: create one row per provider, openai active by default
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
		log.Println("Created default LLM settings for all providers")
		return nil
	}

	// Existing database: migrate from singleton to per-provider.
	// Ensure every provider has a row.
	var hasActive bool
	for _, p := range ValidLLMProviders() {
		var existing LLMSettings
		err := DB.Where("provider = ?", p).First(&existing).Error
		if err == nil {
			if existing.Active {
				hasActive = true
			}
			continue // row exists
		}
		row := &LLMSettings{
			Provider:      p,
			Model:         defaultModelsPerProvider[p],
			ThinkingLevel: ThinkingLevelMedium,
			Enabled:       false,
			Active:        false,
		}
		if err := DB.Create(row).Error; err != nil {
			return fmt.Errorf("failed to create LLM settings for %s: %w", p, err)
		}
		log.Printf("Created LLM settings for provider: %s", p)
	}

	// If no row is marked active (legacy single-row DB), mark the first enabled
	// row as active, or the first row overall.
	if !hasActive {
		var first LLMSettings
		if err := DB.Where("enabled = ?", true).First(&first).Error; err != nil {
			// No enabled row, just pick the first
			DB.First(&first)
		}
		if first.ID > 0 {
			DB.Model(&first).Update("active", true)
			log.Printf("Marked provider %s as active (migration)", first.Provider)
		}
	}

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
2. **Read SKILL.md files**: Each skill's SKILL.md lists assigned tools with their ` + "`tool_instance_id`" + ` values — read these first
3. **Call tools directly**: Use Python wrappers via the bash tool (environment is pre-configured). Do NOT explore the filesystem to discover tools
4. **Gather data**: Invoke skills to collect metrics, logs, or status information
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
- You lack the necessary skills or access to resolve the issue`

// InitializeSystemSkill creates the incident-manager system skill if it doesn't exist
func InitializeSystemSkill() error {
	log.Println("Checking for incident-manager system skill...")

	var skill Skill
	result := DB.Where("name = ?", "incident-manager").First(&skill)

	if result.Error == nil {
		// Skill exists, ensure it's marked as system
		if !skill.IsSystem {
			DB.Model(&skill).Update("is_system", true)
			log.Println("Updated incident-manager skill to system skill")
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

	log.Printf("Created incident-manager system skill (ID: %d)", skill.ID)

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

// GetOpenAISettings retrieves OpenAI settings from the database (legacy, kept for device auth)
func GetOpenAISettings() (*OpenAISettings, error) {
	var settings OpenAISettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateOpenAISettings updates OpenAI settings in the database (legacy)
func UpdateOpenAISettings(settings *OpenAISettings) error {
	return DB.Model(&OpenAISettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// UpdateOpenAIChatGPTTokens updates the ChatGPT OAuth tokens in the database
func UpdateOpenAIChatGPTTokens(settings *OpenAISettings) error {
	return DB.Model(&OpenAISettings{}).Where("id = ?", settings.ID).
		Select("chat_gpt_access_token", "chat_gpt_refresh_token", "chat_gpt_expires_at", "chat_gpt_user_email", "auth_method").
		Updates(settings).Error
}

// ClearOpenAIChatGPTTokens clears all ChatGPT OAuth tokens (for disconnect)
func ClearOpenAIChatGPTTokens(id uint) error {
	return DB.Model(&OpenAISettings{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"chat_gpt_access_token":  "",
			"chat_gpt_refresh_token": "",
			"chat_gpt_expires_at":    nil,
			"chat_gpt_user_email":    "",
		}).Error
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

// GetOrCreateAggregationSettings retrieves or creates aggregation settings (singleton).
// This function accepts a db parameter (rather than using the global DB) to support
// dependency injection, transaction contexts, and easier testing.
func GetOrCreateAggregationSettings(db *gorm.DB) (*AggregationSettings, error) {
	var settings AggregationSettings
	result := db.First(&settings)
	if result.Error == gorm.ErrRecordNotFound {
		settings = *NewDefaultAggregationSettings()
		if err := db.Create(&settings).Error; err != nil {
			return nil, err
		}
	} else if result.Error != nil {
		return nil, result.Error
	}
	return &settings, nil
}

// UpdateAggregationSettings updates aggregation settings.
// Uses Save() which handles both insert and update operations.
// Accepts a db parameter for dependency injection, transaction support, and testing.
func UpdateAggregationSettings(db *gorm.DB, settings *AggregationSettings) error {
	return db.Save(settings).Error
}

// migrateProxySettings ensures proxy settings exist with defaults
func migrateProxySettings(db *gorm.DB) error {
	var count int64
	db.Model(&ProxySettings{}).Count(&count)
	if count > 0 {
		return nil // Already exists
	}
	return nil
}
