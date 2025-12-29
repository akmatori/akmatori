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

	err := DB.AutoMigrate(
		&IncidentManagerConfig{},
		&SlackSettings{},
		&ZabbixSettings{},
		&OpenAISettings{},
		&ContextFile{},
		&Skill{},
		&ToolType{},
		&ToolInstance{},
		&SkillTool{},
		&EventSource{},
		&Incident{},
		&APIKeySettings{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database migrations completed successfully")
	return nil
}

// InitializeDefaults creates default records if they don't exist
func InitializeDefaults() error {
	log.Println("Initializing default database records...")

	// Create default incident manager config if it doesn't exist
	var count int64
	DB.Model(&IncidentManagerConfig{}).Count(&count)
	if count == 0 {
		defaultConfig := &IncidentManagerConfig{
			Prompt: `You're a senior incident manager. Your responsibility is resolving incidents or answering questions related to infrastructure.

When you receive an event (alert, message, or question), follow these steps:
1. Analyze the situation and determine what information you need
2. Call appropriate skills using the $ syntax (e.g., $skill-name task description)
3. Synthesize the information gathered from skills
4. Provide a clear resolution or answer

Please invoke skills as needed. Each skill has specific expertise and tools at their disposal.`,
		}
		if err := DB.Create(defaultConfig).Error; err != nil {
			return fmt.Errorf("failed to create default incident manager config: %w", err)
		}
		log.Println("Created default incident manager configuration")
	}

	// Create default Slack settings if they don't exist
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

	// Create default Zabbix settings if they don't exist
	DB.Model(&ZabbixSettings{}).Count(&count)
	if count == 0 {
		defaultZabbixSettings := &ZabbixSettings{
			Enabled: false, // Disabled by default until configured
		}
		if err := DB.Create(defaultZabbixSettings).Error; err != nil {
			return fmt.Errorf("failed to create default zabbix settings: %w", err)
		}
		log.Println("Created default Zabbix settings (disabled)")
	}

	// Create default OpenAI settings if they don't exist
	DB.Model(&OpenAISettings{}).Count(&count)
	if count == 0 {
		defaultOpenAISettings := &OpenAISettings{
			Model:                "gpt-5.1-codex",
			ModelReasoningEffort: "medium",
			Enabled:              false, // Disabled by default until API key is configured
		}
		if err := DB.Create(defaultOpenAISettings).Error; err != nil {
			return fmt.Errorf("failed to create default openai settings: %w", err)
		}
		log.Println("Created default OpenAI settings (disabled)")
	}

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

// GetZabbixSettings retrieves Zabbix settings from the database
func GetZabbixSettings() (*ZabbixSettings, error) {
	var settings ZabbixSettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateZabbixSettings updates Zabbix settings in the database
func UpdateZabbixSettings(settings *ZabbixSettings) error {
	return DB.Model(&ZabbixSettings{}).Where("id = ?", settings.ID).Updates(settings).Error
}

// GetOpenAISettings retrieves OpenAI settings from the database
func GetOpenAISettings() (*OpenAISettings, error) {
	var settings OpenAISettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateOpenAISettings updates OpenAI settings in the database
func UpdateOpenAISettings(settings *OpenAISettings) error {
	return DB.Model(&OpenAISettings{}).Where("id = ?", settings.ID).Updates(settings).Error
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
