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

	// Initialize system skill (incident-manager)
	if err := InitializeSystemSkill(); err != nil {
		return fmt.Errorf("failed to initialize system skill: %w", err)
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
2. **Identify relevant skills**: Check available skills and their capabilities
3. **Gather data**: Invoke skills to collect metrics, logs, or status information
4. **Correlate findings**: Connect information from multiple sources
5. **Determine root cause**: Identify what triggered the incident
6. **Recommend actions**: Suggest specific remediation steps

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
