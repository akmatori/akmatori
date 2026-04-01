package database

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

	// For PostgreSQL, pin all migration work to a single pooled connection so
	// that the session-level advisory lock, AutoMigrate DDL, and the unlock
	// all execute on the same backend session. Without pinning, GORM's
	// connection pool can dispatch each Exec to a different connection,
	// causing the lock to protect nothing and potentially leak.
	// SQLite (tests) is single-writer and needs no lock.
	if DB.Dialector.Name() == "postgres" {
		return DB.Connection(func(conn *gorm.DB) error {
			if err := conn.Exec("SELECT pg_advisory_lock(742819001)").Error; err != nil {
				return fmt.Errorf("acquire migration lock: %w", err)
			}
			defer func() {
				if err := conn.Exec("SELECT pg_advisory_unlock(742819001)").Error; err != nil {
					slog.Error("failed to release migration lock", "error", err)
				}
			}()
			return runMigrations(conn)
		})
	}

	return runMigrations(DB)
}

// runMigrations performs the actual schema migration and data migration steps.
// The caller is responsible for any locking. The provided db handle must be
// used for all operations so that connection pinning (if any) is preserved.
func runMigrations(db *gorm.DB) error {
	// Pre-migration: prepare the llm_settings table for the multi-config schema
	// change BEFORE AutoMigrate runs. AutoMigrate will try to add a unique index
	// on the new "name" column, which fails if existing rows all have empty names.
	// It also won't drop the old unique index on "provider", blocking multi-config.
	if err := preMigrateLLMSettings(db); err != nil {
		return err
	}

	// Reset GORM session state before AutoMigrate. The preMigrate step
	// operates on specific tables, leaving internal GORM state (table name,
	// clauses) that can leak into AutoMigrate's processing of other models
	// on this pinned connection.
	err := db.Session(&gorm.Session{NewDB: true}).AutoMigrate(
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
		&RetentionSettings{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Migrate open_ai_enabled → llm_enabled in proxy_settings table.
	// GORM's AutoMigrate already created the new llm_enabled column from the
	// updated model. We need to copy values from the old column and drop it.
	// The old column name is "open_ai_enabled" (GORM's snake_case of OpenAIEnabled).
	if err := migrateOpenAIToLLMEnabled(db); err != nil {
		return err
	}

	slog.Info("database migrations completed successfully")
	return nil
}

// preMigrateLLMSettings prepares the llm_settings table for the multi-config
// schema change. This must run BEFORE AutoMigrate because:
// 1. AutoMigrate adds a uniqueIndex on "name" — fails if existing rows have empty names.
// 2. AutoMigrate won't drop the old uniqueIndex on "provider" — blocks multi-config.
func preMigrateLLMSettings(db *gorm.DB) error {
	if !db.Migrator().HasTable(&LLMSettings{}) {
		return nil // Fresh install — AutoMigrate will create everything correctly.
	}

	// Wrap all pre-migration DDL in an explicit transaction so it commits
	// independently — if AutoMigrate later fails, these changes persist.
	if err := db.Transaction(func(tx *gorm.DB) error {
		// Drop old unique indexes that block the multi-config schema change.
		// Use raw DROP INDEX IF EXISTS instead of HasIndex — GORM's HasIndex checks
		// against the current model struct, which no longer has these fields/tags.
		for _, idx := range []string{
			"idx_llm_settings_provider",       // GORM default naming for old provider unique index
			"uni_llm_settings_provider",       // GORM uniqueIndex naming variant
			"idx_llm_settings_singleton_key",  // Old singleton pattern unique index
		} {
			if err := tx.Exec("DROP INDEX IF EXISTS " + idx).Error; err != nil {
				slog.Warn("failed to drop old index", "index", idx, "error", err)
			}
		}

		// Drop orphaned columns from the old singleton pattern (singleton_key,
		// retention_days, cleanup_interval_hours were added by a previous GORM
		// AutoMigrate when LLMSettings included these fields).
		for _, col := range []string{"singleton_key", "retention_days", "cleanup_interval_hours"} {
			if tx.Migrator().HasColumn(&LLMSettings{}, col) {
				if err := tx.Exec("ALTER TABLE llm_settings DROP COLUMN " + col).Error; err != nil {
					slog.Warn("failed to drop orphaned column", "column", col, "error", err)
				} else {
					slog.Info("dropped orphaned column from llm_settings", "column", col)
				}
			}
		}

		// Add the name column if it doesn't exist.
		if !tx.Migrator().HasColumn(&LLMSettings{}, "name") {
			if err := tx.Exec("ALTER TABLE llm_settings ADD COLUMN name VARCHAR(100) NOT NULL DEFAULT ''").Error; err != nil {
				return fmt.Errorf("add name column to llm_settings: %w", err)
			}
			slog.Info("added name column to llm_settings")
		}

		return nil
	}); err != nil {
		return err
	}

	// Populate empty names with unique values before AutoMigrate adds the unique index.
	return migrateLLMSettingsName(db)
}

// migrateOpenAIToLLMEnabled copies open_ai_enabled values to llm_enabled
// and drops the old column, all within a transaction to prevent partial state.
// Concurrency is handled by the session-level advisory lock in AutoMigrate.
func migrateOpenAIToLLMEnabled(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasColumn(&ProxySettings{}, "open_ai_enabled") {
			return nil
		}
		if err := tx.Exec("UPDATE proxy_settings SET llm_enabled = open_ai_enabled WHERE llm_enabled IS NULL OR llm_enabled != open_ai_enabled").Error; err != nil {
			return fmt.Errorf("copy open_ai_enabled values: %w", err)
		}
		if err := tx.Exec("ALTER TABLE proxy_settings DROP COLUMN open_ai_enabled").Error; err != nil {
			return fmt.Errorf("drop open_ai_enabled column: %w", err)
		}
		slog.Info("migrated proxy_settings: open_ai_enabled → llm_enabled")
		return nil
	})
}

// migrateLLMSettingsName populates the Name field for existing LLM settings rows
// that have an empty name (from before the multi-config migration). Handles
// duplicate providers by appending a numeric suffix (e.g. "OpenAI (2)").
func migrateLLMSettingsName(db *gorm.DB) error {
	var rows []LLMSettings
	if err := db.Where("name = '' OR name IS NULL").Find(&rows).Error; err != nil {
		return fmt.Errorf("query llm_settings with empty name: %w", err)
	}
	// Track assigned names to handle duplicate providers.
	assigned := make(map[string]int)
	// Pre-load existing non-empty names to avoid collisions.
	var existing []LLMSettings
	if err := db.Where("name != '' AND name IS NOT NULL").Find(&existing).Error; err == nil {
		for _, e := range existing {
			assigned[e.Name] = 1
		}
	}
	for _, row := range rows {
		base := ProviderDisplayName(row.Provider)
		name := base
		if assigned[name] > 0 {
			// Find next available suffix.
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s (%d)", base, i)
				if assigned[candidate] == 0 {
					name = candidate
					break
				}
			}
		}
		assigned[name] = 1
		if err := db.Session(&gorm.Session{NewDB: true}).Model(&LLMSettings{}).Where("id = ?", row.ID).Update("name", name).Error; err != nil {
			return fmt.Errorf("set name for llm_settings id=%d: %w", row.ID, err)
		}
	}
	if len(rows) > 0 {
		slog.Info("migrated llm_settings: populated Name field", "count", len(rows))
	}
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

	// Create default retention settings if they don't exist.
	// FirstOrCreate is SELECT+INSERT which can race under concurrent startups:
	// both see no row, both INSERT, loser hits the unique constraint. On any
	// error we fall back to a plain read, which succeeds if the other caller
	// just created the row.
	{
		var rs RetentionSettings
		defaults := DefaultRetentionSettings()
		if err := DB.Where(RetentionSettings{SingletonKey: "default"}).Attrs(defaults).FirstOrCreate(&rs).Error; err != nil {
			if rerr := DB.Where(RetentionSettings{SingletonKey: "default"}).First(&rs).Error; rerr != nil {
				return fmt.Errorf("failed to create default retention settings: %w (retry: %v)", err, rerr)
			}
		}
		if rs.CreatedAt.Equal(rs.UpdatedAt) {
			slog.Info("created default retention settings")
		}
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
			Name:          ProviderDisplayName(p),
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

1. **Understand the problem**: Read the alert/question carefully. Identify the affected system, severity, and symptoms.

2. **MANDATORY - Search runbooks FIRST before using any infrastructure tools**:
   You MUST search for relevant runbooks before performing any other investigation steps.

   Extract 3-5 core keywords from the alert name. Drop hyphens, host names, qualifiers.
   Example: "Nginx-cache test resource connection refused on edge host" → "nginx cache connection refused"

   gateway_call("qmd.query", {"searches": [{"type": "lex", "query": "<short keywords>"}], "limit": 5})

   If no results, retry with fewer or different keywords (e.g., just the service + error type).
   If results are returned (score > 0.7), retrieve the top 2 runbooks:

   gateway_call("qmd.get", {"file": "<file path from search result>"})

   Follow matching runbook procedures as your PRIMARY investigation guide.
   If results are empty after retries, proceed with general investigation.
   Skip this step ONLY if QMD search returns an error (not if results are empty).
   If QMD is unavailable, fall back to browsing /akmatori/runbooks/ directly.

3. **Load relevant skills**: Read the SKILL.md file for each skill relevant to this incident
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

// GetAllLLMSettings returns all LLM configurations ordered by provider then name.
func GetAllLLMSettings() ([]LLMSettings, error) {
	var settings []LLMSettings
	if err := DB.Order("provider asc, name asc").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// GetLLMSettingsByID returns LLM settings for a specific config by ID.
func GetLLMSettingsByID(id uint) (*LLMSettings, error) {
	var settings LLMSettings
	if err := DB.First(&settings, id).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// SetActiveLLMConfig deactivates all LLM configs and activates the one with the given ID.
// Uses SELECT FOR UPDATE to prevent concurrent activation races.
// Returns an error if the target config has no API key (validated under lock).
func SetActiveLLMConfig(id uint) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// Lock all LLM config rows to serialize concurrent activate/update calls
		var allConfigs []LLMSettings
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&allConfigs).Error; err != nil {
			return err
		}
		// Find the target config and validate under lock
		var target *LLMSettings
		for i := range allConfigs {
			if allConfigs[i].ID == id {
				target = &allConfigs[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("LLM config with id %d not found", id)
		}
		if target.APIKey == "" {
			return fmt.Errorf("cannot activate a configuration without an API key")
		}
		if err := tx.Model(&LLMSettings{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		// Set both active and enabled so the config passes IsActive() checks
		// used by incident dispatch (BuildLLMSettingsForWorker).
		return tx.Model(&LLMSettings{}).Where("id = ?", id).Updates(map[string]interface{}{
			"active":  true,
			"enabled": true,
		}).Error
	})
}

// CreateLLMSettings creates a new LLM settings configuration.
func CreateLLMSettings(settings *LLMSettings) error {
	return DB.Create(settings).Error
}

// UpdateLLMSettings atomically updates an LLM config by ID.
// Uses SELECT FOR UPDATE to prevent concurrent update/activate races.
// Returns an error if the update would clear the API key on the active config.
func UpdateLLMSettings(id uint, updates map[string]interface{}) (*LLMSettings, error) {
	var result LLMSettings
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Lock the target row to serialize with concurrent activate calls
		var settings LLMSettings
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&settings, id).Error; err != nil {
			return err
		}
		// Prevent clearing the API key on the active config
		if apiKey, ok := updates["api_key"]; ok {
			if apiKey == "" && settings.Active {
				return fmt.Errorf("cannot clear the API key on the active configuration")
			}
		}
		if err := tx.Model(&settings).Updates(updates).Error; err != nil {
			return err
		}
		// Re-read to get final state
		if err := tx.First(&result, id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteLLMSettings deletes an LLM config by ID within a transaction.
// Returns an error if the config is active or is the last remaining config.
// Uses SELECT FOR UPDATE to prevent concurrent deletion races.
func DeleteLLMSettings(id uint) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// Lock all LLM config rows to serialize concurrent delete/activate calls
		var allConfigs []LLMSettings
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&allConfigs).Error; err != nil {
			return fmt.Errorf("failed to lock LLM configurations: %w", err)
		}
		var settings *LLMSettings
		for i := range allConfigs {
			if allConfigs[i].ID == id {
				settings = &allConfigs[i]
				break
			}
		}
		if settings == nil {
			return fmt.Errorf("LLM config with id %d not found", id)
		}
		if settings.Active {
			return fmt.Errorf("cannot delete the active LLM configuration")
		}
		if len(allConfigs) <= 1 {
			return fmt.Errorf("cannot delete the last LLM configuration")
		}
		return tx.Delete(&LLMSettings{}, id).Error
	})
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
			LLMEnabled:    true,
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

// GetOrCreateRetentionSettings retrieves or creates retention settings (singleton).
// The row is normally seeded by InitializeDefaults at startup; the create path
// here is only a fallback. If FirstOrCreate races with another caller (both see
// no row, both INSERT, one hits unique constraint), we fall back to a plain read.
func GetOrCreateRetentionSettings() (*RetentionSettings, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var settings RetentionSettings
	defaults := DefaultRetentionSettings()
	if err := DB.Where(RetentionSettings{SingletonKey: "default"}).Attrs(defaults).FirstOrCreate(&settings).Error; err != nil {
		// Race: another caller just inserted the row. Read it.
		if rerr := DB.Where(RetentionSettings{SingletonKey: "default"}).First(&settings).Error; rerr != nil {
			return nil, fmt.Errorf("%w (retry: %v)", err, rerr)
		}
	}
	return &settings, nil
}

// UpdateRetentionSettings updates retention settings in the database
func UpdateRetentionSettings(settings *RetentionSettings) error {
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
