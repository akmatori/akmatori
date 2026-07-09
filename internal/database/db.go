package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
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

	// Pre-migration: drop the legacy `mode` and `description` columns from
	// cron_jobs. The redesigned cron path uses a single agent execution mode
	// (driven by the cron-agent system skill) and there is no description
	// field. Any existing rows are normalized to mode='agent' before the
	// column is dropped so a downstream rollback would not silently reintroduce
	// a oneshot dispatch path.
	if err := preMigrateCronJobsDropLegacyColumns(db); err != nil {
		return err
	}

	// Pre-migration: drop the legacy `correlated_count` column from incidents.
	// The alert correlation redesign replaces the counter with first-class Alert
	// rows; GORM AutoMigrate never drops columns, so we must remove it explicitly.
	if err := preMigrateIncidentsDropCorrelatedCount(db); err != nil {
		return err
	}

	// Pre-migration: drop the orphaned legacy `incident_alerts` table left behind
	// by the alert correlation redesign. Its no-cascade FK to incidents otherwise
	// blocks retention cleanup. Superseded by the new alerts table.
	if err := preMigrateDropLegacyIncidentAlerts(db); err != nil {
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
		&Memory{},
		&HTTPConnector{},
		&MCPServerConfig{},
		&RetentionSettings{},
		&FormattingSettings{},
		// Channels & cron (unified channels + cron jobs feature)
		&Integration{},
		&Channel{},
		&CronJob{},
		&CronJobTool{},
		// Alerts (first-class alert rows attached to incidents)
		&Alert{},
		// Self-improvement proposals + refinement chat transcripts
		&Proposal{},
		&ProposalChatMessage{},
	)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Partial-unique index: at most one Channel.IsDefaultPost=true per
	// Integration. Combined with the MVP "one Integration per provider"
	// assumption, this enforces "one default channel per provider" at the DB
	// level. The service layer adds an additional check across integrations
	// of the same provider for the case where a deployment configures more
	// than one Integration per provider.
	if err := ensureChannelsDefaultPartialIndex(db); err != nil {
		return fmt.Errorf("failed to ensure channels default partial index: %w", err)
	}

	// Composite indexes on the alerts table.
	if err := ensureAlertsIndexes(db); err != nil {
		return fmt.Errorf("failed to ensure alerts indexes: %w", err)
	}

	// Composite index on (scope, type) for scope-scoped type-filtered queries.
	if err := ensureMemoriesScopeTypeIndex(db); err != nil {
		return fmt.Errorf("failed to ensure memories scope/type index: %w", err)
	}

	// Backfill legacy SlackSettings + slack_channel AlertSourceInstance rows
	// into the new Integration/Channel rows. Read-old → write-new →
	// don't-delete-old-until-verified, one transaction per step, idempotent
	// on re-run.
	if err := migrateSlackSettingsToIntegrations(db); err != nil {
		return err
	}
	if err := migrateSlackChannelAlertSourcesToChannels(db); err != nil {
		return err
	}
	if err := deprecateSlackChannelAlertSourceType(db); err != nil {
		return err
	}

	// Migrate open_ai_enabled → llm_enabled in proxy_settings table.
	// GORM's AutoMigrate already created the new llm_enabled column from the
	// updated model. We need to copy values from the old column and drop it.
	// The old column name is "open_ai_enabled" (GORM's snake_case of OpenAIEnabled).
	if err := migrateOpenAIToLLMEnabled(db); err != nil {
		return err
	}

	// Normalize OpenRouter model IDs that were seeded by older Akmatori
	// releases using a dash-form alias (e.g. anthropic/claude-sonnet-4-6) that
	// was never registered by pi-mono. pi-mono only registers the dot-form
	// (anthropic/claude-sonnet-4.6), so unmigrated rows would fail at runtime
	// once an operator added an API key to the seeded OpenRouter row.
	if err := migrateOpenRouterDashFormModels(db); err != nil {
		return err
	}

	// Backfill alert rows for pre-existing alert-sourced incidents.
	if err := migrateBackfillAlerts(db); err != nil {
		return err
	}

	slog.Info("database migrations completed successfully")
	return nil
}

// preMigrateCronJobsDropLegacyColumns removes the legacy `mode` and
// `description` columns from cron_jobs. The agent-only cron redesign collapses
// the previous oneshot/agent dispatch into a single agent path, so any
// pre-existing rows are first normalized to mode='agent' and then the
// columns are dropped. The function is idempotent: a fresh install where the
// columns never existed becomes a no-op, and a re-run after a successful drop
// is also a no-op.
func preMigrateCronJobsDropLegacyColumns(db *gorm.DB) error {
	if !db.Migrator().HasTable(&CronJob{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		hasMode := tx.Migrator().HasColumn(&CronJob{}, "mode")
		hasDescription := tx.Migrator().HasColumn(&CronJob{}, "description")
		if !hasMode && !hasDescription {
			return nil
		}
		if hasMode {
			// Coerce any non-agent rows to agent before the column disappears.
			// Done as a plain UPDATE so the operation is visible in the SQL
			// audit trail and does not depend on the now-removed model field.
			if err := tx.Exec("UPDATE cron_jobs SET mode = 'agent' WHERE mode IS NULL OR mode <> 'agent'").Error; err != nil {
				return fmt.Errorf("normalize cron_jobs.mode: %w", err)
			}
			if err := tx.Exec("ALTER TABLE cron_jobs DROP COLUMN mode").Error; err != nil {
				return fmt.Errorf("drop cron_jobs.mode column: %w", err)
			}
			slog.Info("dropped cron_jobs.mode column (agent-only redesign)")
		}
		if hasDescription {
			if err := tx.Exec("ALTER TABLE cron_jobs DROP COLUMN description").Error; err != nil {
				return fmt.Errorf("drop cron_jobs.description column: %w", err)
			}
			slog.Info("dropped cron_jobs.description column (agent-only redesign)")
		}
		return nil
	})
}

// preMigrateIncidentsDropCorrelatedCount removes the legacy `correlated_count`
// column that was removed from the Incident model as part of the alert
// correlation redesign. GORM AutoMigrate never drops columns, so upgrade
// installs would otherwise retain the stale column indefinitely. Idempotent.
func preMigrateIncidentsDropCorrelatedCount(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Incident{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&Incident{}, "correlated_count") {
		return nil
	}
	if err := db.Exec("ALTER TABLE incidents DROP COLUMN correlated_count").Error; err != nil {
		return fmt.Errorf("drop incidents.correlated_count column: %w", err)
	}
	slog.Info("dropped incidents.correlated_count column (alert correlation redesign)")
	return nil
}

// preMigrateDropLegacyIncidentAlerts drops the orphaned incident_alerts table.
// The alert correlation redesign removed the IncidentAlert Go model in favour of
// the new first-class Alert model (alerts table). GORM AutoMigrate never drops
// tables, so the legacy table lingers with a no-cascade FK to incidents. That FK
// blocks retention cleanup (DELETE FROM incidents) for every incident that has a
// legacy alert row. The data is superseded by the alerts table, so we drop the
// table (CASCADE also removes the dependent FK constraint). Idempotent.
func preMigrateDropLegacyIncidentAlerts(db *gorm.DB) error {
	if !db.Migrator().HasTable("incident_alerts") {
		return nil
	}
	if err := db.Exec("DROP TABLE IF EXISTS incident_alerts CASCADE").Error; err != nil {
		return fmt.Errorf("drop legacy incident_alerts table: %w", err)
	}
	slog.Info("dropped legacy incident_alerts table (alert correlation redesign)")
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
			"idx_llm_settings_provider",      // GORM default naming for old provider unique index
			"uni_llm_settings_provider",      // GORM uniqueIndex naming variant
			"idx_llm_settings_singleton_key", // Old singleton pattern unique index
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
	if err := db.Session(&gorm.Session{NewDB: true}).Where("name = '' OR name IS NULL").Find(&rows).Error; err != nil {
		return fmt.Errorf("query llm_settings with empty name: %w", err)
	}
	// Track assigned names to handle duplicate providers.
	assigned := make(map[string]int)
	// Pre-load existing non-empty names to avoid collisions.
	var existing []LLMSettings
	if err := db.Session(&gorm.Session{NewDB: true}).Where("name != '' AND name IS NOT NULL").Find(&existing).Error; err == nil {
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

// openRouterDashFormRenames maps stale dash-form OpenRouter model IDs that
// older Akmatori releases seeded as defaults to the dot-form aliases that
// pi-mono actually registers. Keep this list narrow: only entries that were
// previously shipped as defaults belong here, so user-typed values are not
// silently rewritten unless they exactly match a known stale default.
var openRouterDashFormRenames = map[string]string{
	"anthropic/claude-sonnet-4-5": "anthropic/claude-sonnet-4.5",
	"anthropic/claude-sonnet-4-6": "anthropic/claude-sonnet-4.6",
}

// migrateOpenRouterDashFormModels rewrites OpenRouter rows still pinned to a
// previously-seeded dash-form alias to the dot-form alias registered by
// pi-mono. Idempotent: rows already on the dot-form (or any other value) are
// untouched.
func migrateOpenRouterDashFormModels(db *gorm.DB) error {
	for old, replacement := range openRouterDashFormRenames {
		result := db.Model(&LLMSettings{}).
			Where("provider = ? AND model = ?", LLMProviderOpenRouter, old).
			Update("model", replacement)
		if result.Error != nil {
			return fmt.Errorf("rename openrouter model %s → %s: %w", old, replacement, result.Error)
		}
		if result.RowsAffected > 0 {
			slog.Info("migrated openrouter model id", "from", old, "to", replacement, "rows", result.RowsAffected)
		}
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

	// Create default formatting settings if they don't exist.
	// Same race-tolerant FirstOrCreate pattern as retention settings.
	{
		var fs FormattingSettings
		defaults := DefaultFormattingSettings()
		if err := DB.Where(FormattingSettings{SingletonKey: "default"}).Attrs(defaults).FirstOrCreate(&fs).Error; err != nil {
			if rerr := DB.Where(FormattingSettings{SingletonKey: "default"}).First(&fs).Error; rerr != nil {
				return fmt.Errorf("failed to create default formatting settings: %w (retry: %v)", err, rerr)
			}
		}
		if fs.CreatedAt.Equal(fs.UpdatedAt) {
			slog.Info("created default formatting settings")
		}
	}

	// Initialize system skill (incident-manager)
	if err := InitializeSystemSkill(); err != nil {
		return fmt.Errorf("failed to initialize system skill: %w", err)
	}

	// Initialize the cron-agent system skill — the root prompt the redesigned
	// cron path runs as instead of incident-manager.
	if err := InitializeCronAgentSkill(); err != nil {
		return fmt.Errorf("failed to initialize cron-agent skill: %w", err)
	}

	// Initialize the proposal-editor system skill — the root prompt for
	// operator↔agent refinement chats on self-improvement proposals.
	if err := InitializeProposalEditorSkill(); err != nil {
		return fmt.Errorf("failed to initialize proposal-editor skill: %w", err)
	}

	// Seed non-deletable system cron jobs (e.g. memory-curator). Operator can
	// re-enable; the row itself is idempotently re-seeded on every boot.
	if err := SeedSystemCronJobs(); err != nil {
		return fmt.Errorf("failed to seed system cron jobs: %w", err)
	}

	return nil
}

// Default models per provider, used when seeding new provider rows.
// Values must align with the "Recommended" entries in
// web/src/components/settings/llmModelSuggestions.ts, and
// must use IDs registered by the active pi-mono SDK (note OpenRouter aliases
// use dot-form, e.g. anthropic/claude-sonnet-4.6).
var defaultModelsPerProvider = map[LLMProvider]string{
	LLMProviderOpenAI:     "gpt-5.5",
	LLMProviderAnthropic:  "claude-sonnet-4-6",
	LLMProviderGoogle:     "gemini-3-pro-preview",
	LLMProviderOpenRouter: "openai/gpt-5.5",
	LLMProviderCustom:     "",
	LLMProviderNvidiaNIM:  "meta/llama-3.3-70b-instruct",
	LLMProviderMiniMax:    "MiniMax-M3",
	LLMProviderAntLing:    "Ling-2.6-1T",
}

// seedLLMProviders ensures one row per provider exists in the llm_settings table.
// Idempotent: seeds missing providers without touching existing rows so upgrades
// that add new providers (e.g. nvidia, minimax, ant-ling) work correctly.
func seedLLMProviders() error {
	// Determine whether the table has any rows at all so we know whether to set
	// the first-install default (openai active). On an upgrade the active
	// provider is already set and new rows must default to inactive.
	var total int64
	DB.Model(&LLMSettings{}).Count(&total)
	firstInstall := total == 0

	created := 0
	for _, p := range ValidLLMProviders() {
		var count int64
		DB.Model(&LLMSettings{}).Where("provider = ?", p).Count(&count)
		if count > 0 {
			continue
		}
		row := &LLMSettings{
			Name:          ProviderDisplayName(p),
			Provider:      p,
			Model:         defaultModelsPerProvider[p],
			ThinkingLevel: ThinkingLevelMedium,
			Enabled:       false,
			Active:        firstInstall && p == LLMProviderOpenAI,
		}
		if err := DB.Create(row).Error; err != nil {
			return fmt.Errorf("failed to create LLM settings for %s: %w", p, err)
		}
		created++
	}
	if created > 0 {
		slog.Info("created default LLM settings for missing providers", "count", created)
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

1. **Understand the problem**: Read the alert/question carefully. Identify the affected system, severity, and symptoms.

2. **MANDATORY - Search runbooks FIRST before using any infrastructure tools**:
   You MUST search for relevant runbooks before performing any other investigation steps.

   Delegate the search to the runbook-searcher subagent. It runs in its own
   scoped subprocess against the read-only runbook library mounted at
   /akmatori/runbooks/ and returns the top candidate file paths with short
   excerpts.

   subagent({"agent": "runbook-searcher", "task": "<full Original alert text when present, otherwise a one-sentence natural-language summary of the alert>"})

   When the prompt contains an "Original alert text:" block, pass that block
   verbatim as the "task" — the runbook-searcher subagent will extract
   distinctive keywords (sender, source, channel, error string, host) from
   it on its own. When no "Original alert text:" block is present, fall back
   to a one-sentence natural-language summary of the alert (what is broken,
   where, and the most distinctive symptom).

   If the first invocation returns "No runbooks matched" or the top candidate
   is not obviously related, you MAY retry with a different angle (a
   target_service / host alone like "edge nginx" or "auth-service", or the
   summary rephrased as a question).
   Cap total runbook-searcher invocations at 3 (the initial call plus up to 2 retries).

   When the subagent returns candidate paths, read the most relevant runbook
   via the local read tool (the runbook directory is mounted at
   /akmatori/runbooks/ inside this container). Follow matching runbook
   procedures as your PRIMARY investigation guide.

   If the subagent itself errors or is unavailable, fall back to browsing
   /akmatori/runbooks/ directly. Empty results are NOT a reason to skip — only
   subagent errors trigger the filesystem fallback.

3. **MANDATORY - Search cross-incident memory next**:
   Immediately after the runbook search, search the cross-incident memory for
   prior incidents, host quirks, tool quirks, and operator feedback relevant
   to this alert. Do this BEFORE invoking any infrastructure tools.

   Delegate the search to the memory-searcher subagent. It runs in its own
   scoped subprocess against the memory directory mounted at
   /akmatori/memory/ and returns the top candidate file paths with short
   excerpts.

   subagent({"agent": "memory-searcher", "task": "<full Original alert text when present, otherwise a one-sentence natural-language summary of the alert>"})

   When the prompt contains an "Original alert text:" block, pass that block
   verbatim as the "task" — the memory-searcher subagent will extract
   distinctive keywords (host, error pattern, tool quirk, feedback topic) from
   it on its own. When no "Original alert text:" block is present, fall back
   to a one-sentence natural-language summary of the alert.

   If the first invocation returns no useful matches, you MAY retry with a
   narrower angle (target_service / host alone like "edge nginx" or
   "auth-service", or the symptom rephrased).
   Cap total memory-searcher invocations at 3 (the initial call plus up to 2 retries).

   When the subagent returns candidate paths, read the most relevant memory
   file via the local read tool (the memory directory is mounted at
   /akmatori/memory/ inside this container). Use matching memories to inform
   your investigation alongside the runbook procedures.

   If the subagent itself errors or is unavailable, fall back to browsing
   /akmatori/memory/ directly. Empty results are NOT a reason to skip — only
   subagent errors trigger the filesystem fallback.

4. **Load relevant skills**: Read the SKILL.md file for each skill relevant to this incident
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

## Recording durable findings

Before closing the investigation, call the memory-writer subagent to record durable facts, verdicts, and patterns that may be useful for future investigations:

   subagent({"agent": "memory-writer", "task": "Scope: <scope>\nIncident UUID: <uuid>\n\n<reasoning and facts to record>"})

Include root cause, remediation steps taken, and any host or service quirks discovered. These memories are surfaced to future investigations via the memory-searcher subagent.`

// DefaultCronAgentPrompt is the root prompt for the cron-agent system skill.
// It mirrors the incident-manager bootstrap but is scoped to scheduled,
// agent-driven runs rather than incident triage: the agent orients itself,
// optionally consults runbooks, recalls cross-incident memory, executes its
// allowlisted tools, and (when the run surfaces durable findings) records
// them via the memory-writer subagent. The prompt deliberately omits Slack
// thread / triage framing so a cron run is not confused with an alert-driven
// investigation.
const DefaultCronAgentPrompt = `You are the Cron Agent — a scheduled, autonomous operator running a single self-contained task on a recurring cadence. Your job is to follow the task prompt exactly, use only the tools assigned to this cron job, and produce a concise final summary that will be posted to the configured channel.

## Workflow

1. **Orient**: Read the task prompt carefully. Identify what you are being asked to produce (a status check, a consolidation pass, a recurring report, a maintenance action). You are not triaging an incident — there is no on-call audience and no acknowledgement to chase.

2. **Optional — Search runbooks** when the prompt references a procedure or named system:
   If the task explicitly invokes a runbook ("follow runbook X", "check the database health procedure") or otherwise names a system that may have documented steps, delegate the lookup to the runbook-searcher subagent.

   subagent({"agent": "runbook-searcher", "task": "<one-sentence summary of what you are looking for>"})

   Skip this step entirely for tasks that do not reference documented procedures (memory consolidation, scheduled metric snapshots, etc.).

3. **Recall cross-incident memory** when prior runs may have surfaced relevant context:
   Delegate to the memory-searcher subagent against /akmatori/memory/.

   subagent({"agent": "memory-searcher", "task": "<topic, host, or symptom you want to recall>"})

   Read the most relevant file via the local read tool. If memory-searcher errors, fall back to browsing /akmatori/memory/ directly. Skip when the task has no plausible memory dependency.

4. **Execute the task** using only the tools assigned to this cron job. Each cron job declares its own tool allowlist — call those tools via gateway_call(...). Unlike incident-manager runs, no per-skill SKILL.md is loaded for cron-agent: use list_tools_for_tool_type and get_tool_detail to inspect parameter schemas before the first call to a tool you have not used recently. Tools that are NOT in your allowlist will be rejected by the gateway; do not attempt them.

5. **Record durable findings via memory-writer** when the run surfaces durable cross-system facts that will speed up future troubleshooting OR when the task itself instructs you to write/dedupe/delete memory entries.

   subagent({"agent": "memory-writer", "task": "Scope: <scope>\nIncident UUID: <uuid>\n\n<reasoning, then explicit Action: upsert <slug> with body OR Action: delete <slug>>"})

   The memory-writer is idempotent and supports both upserts and deletions. Use Action: delete <slug> to remove a stale or duplicate memory entry. Skip the call when nothing durable was learned and the task did not ask for a memory mutation.

6. **Produce the final summary**: End your run with a concise summary of what you did and what (if anything) the operator should look at. The summary is posted as-is to the cron's configured channel, so keep it scannable: a one-line headline, optional bullet detail, and a single explicit next step when needed.

## Response Guidelines

- Be concise — a scheduled report is read at a glance, not parsed line by line.
- Include specific metrics, file counts, or memory slugs when they are material.
- When the task is "no-op" (nothing changed since last run), say so explicitly rather than padding the response.
- Do NOT frame the output as an incident triage. There is no on-call rotation, no severity, no escalation path.

## What Cron Agent does NOT do

- Does not page humans or open tickets unless the assigned tools include a paging/ticketing tool AND the task prompt explicitly asks for it.
- Does not retry indefinitely; one tick is one execution.
- Does not edit incident-manager state, alert sources, or other crons.`

// InitializeCronAgentSkill creates the cron-agent system skill if it doesn't
// exist, mirroring InitializeSystemSkill's pattern. The cron-agent prompt is
// hardcoded (DefaultCronAgentPrompt) and the row is marked IsSystem=true so
// the skill cannot be deleted by operators.
func InitializeCronAgentSkill() error {
	slog.Info("checking for cron-agent system skill")

	var skill Skill
	result := DB.Where("name = ?", "cron-agent").First(&skill)

	if result.Error == nil {
		if !skill.IsSystem {
			if err := DB.Model(&skill).Update("is_system", true).Error; err != nil {
				return fmt.Errorf("failed to mark cron-agent skill as system: %w", err)
			}
			slog.Info("updated cron-agent skill to system skill")
		}
		return nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("lookup cron-agent skill: %w", result.Error)
	}

	skill = Skill{
		Name:        "cron-agent",
		Description: "Core system skill for scheduled cron-driven agent runs",
		Category:    "system",
		IsSystem:    true,
		Enabled:     true,
	}

	if err := DB.Create(&skill).Error; err != nil {
		return fmt.Errorf("failed to create cron-agent skill: %w", err)
	}

	slog.Info("created cron-agent system skill", "id", skill.ID)
	return nil
}

// DefaultProposalEditorPrompt is the root prompt for the proposal-editor
// system skill. Each chat turn on a proposal runs a fresh agent session under
// this prompt; the task body carries the live proposal JSON, the full chat
// transcript so far, and the operator's new message. The agent persists
// agreed revisions via the proposals gateway tool — it never applies them.
const DefaultProposalEditorPrompt = `You are the Proposal Editor — you refine a single self-improvement proposal in conversation with a human operator. Your task message contains the proposal (kind, title, reasoning, current content snapshot, proposed content as JSON), the conversation so far, and the operator's latest message. Reply to the latest message.

## Workflow

1. **Understand**: Read the proposal and the operator's request carefully. The proposal was generated from evidence in past incidents; the operator wants to polish it before approving.

2. **Verify before editing**: Ground every substantive change in evidence.
   - Re-check the cited incidents: gateway_call("incidents.get", {"uuid": "<uuid>"}) / gateway_call("incidents.list", {...})
   - Related SOPs and memories: delegate to the runbook-searcher / memory-searcher subagents, or read /akmatori/runbooks/, /akmatori/memory/, and /akmatori/skills/<name>/SKILL.md directly.
   - Current cron definitions: gateway_call("proposals.list_cron_jobs", {})
   - The proposal's own latest state: gateway_call("proposals.get", {"uuid": "<proposal uuid>"})

3. **Persist agreed changes**: When you and the operator converge on a revision, save it with
   gateway_call("proposals.update_draft", {"uuid": "<proposal uuid>", "proposed_content": <object>, "title": "...", "reasoning": "..."})
   proposed_content must keep the exact JSON shape it arrived in. Only include the fields you are changing.

4. **Confirm**: End your reply with one short paragraph summarizing what you changed in the draft (or a clarifying question when you need operator input before editing).

## Rules

- You never apply proposals — only the operator approves them via the UI.
- You never call the memory-writer subagent and never edit files; the only mutation you make is proposals.update_draft.
- Keep replies short and conversational; the operator reads them in a chat panel.
- If the operator asks for something outside this proposal's scope, say so and suggest they reject the proposal or wait for the next evaluator run.`

// InitializeProposalEditorSkill creates the proposal-editor system skill if
// it doesn't exist, mirroring InitializeCronAgentSkill's pattern. The prompt
// is hardcoded (DefaultProposalEditorPrompt) and the row is IsSystem=true so
// operators cannot delete it.
func InitializeProposalEditorSkill() error {
	slog.Info("checking for proposal-editor system skill")

	var skill Skill
	result := DB.Where("name = ?", "proposal-editor").First(&skill)

	if result.Error == nil {
		if !skill.IsSystem {
			if err := DB.Model(&skill).Update("is_system", true).Error; err != nil {
				return fmt.Errorf("failed to mark proposal-editor skill as system: %w", err)
			}
			slog.Info("updated proposal-editor skill to system skill")
		}
		return nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("lookup proposal-editor skill: %w", result.Error)
	}

	skill = Skill{
		Name:        "proposal-editor",
		Description: "Core system skill for refining self-improvement proposals in chat",
		Category:    "system",
		IsSystem:    true,
		Enabled:     true,
	}

	if err := DB.Create(&skill).Error; err != nil {
		return fmt.Errorf("failed to create proposal-editor skill: %w", err)
	}

	slog.Info("created proposal-editor system skill", "id", skill.ID)
	return nil
}

// dreamingCronName is the canonical name of the seeded Dreaming system cron
// (formerly "memory-curator"). Lifted into a constant so tests can pin
// idempotency without duplicating the literal.
const dreamingCronName = "Dreaming"

// legacyMemoryCuratorCronName is the pre-rename name of the Dreaming cron;
// SeedSystemCronJobs renames existing rows in place on upgrade.
const legacyMemoryCuratorCronName = "memory-curator"

// dreamingCronPrompt is the task body for the nightly Dreaming
// system cron. It instructs the cron-agent to dedupe and consolidate the
// global-scope memory entries via the memory-writer subagent. The prompt
// asks the agent to keep its mutations explicit (each Action: upsert/delete
// is a separate memory-writer call) so an operator reading the post-run
// summary in the channel can audit what changed without tailing logs.
const dreamingCronPrompt = `You are running the nightly memory consolidation pass over the global cross-incident memory scope (/akmatori/memory/global/).

Goal: keep the global memory scope tight and high-signal. Focus only on the global scope. Process entries updated in the last 14 days — use the memory-searcher subagent to surface candidates from that window. Concretely:

1. Use the memory-searcher subagent to list current entries — search broadly enough to surface duplicates, near-duplicates, and stale rows among entries updated in the last 14 days.
2. For each duplicate or near-duplicate cluster, decide which entry to keep (prefer the most specific, most recent, most operator-validated wording) and which to remove.
3. For each kept entry that should incorporate facts from a soon-to-be-deleted duplicate, prepare a merged body.
4. Apply mutations one at a time via the memory-writer subagent. For each duplicate or superseded entry, emit Action: delete <slug> via memory-writer rather than rewriting both:
   - Action: upsert <slug> — write the merged or refreshed body (memory-writer is idempotent and overwrites by name).
   - Action: delete <slug> — remove a duplicate or obsolete entry.
5. End with a concise summary of the pass: how many entries scanned, how many merged, how many deleted. List the affected slugs.

Use Scope: global and the cron run's incident UUID for every memory-writer call. Do not touch any scope other than global.

If memory-searcher returns no entries or no clear duplicates, exit with a one-line "no-op: nothing to consolidate" summary.`

// dreamingCronSchedule is the canonical schedule for the Dreaming
// system cron (daily at 02:00). Hoisted to a constant so tests can pin it.
const dreamingCronSchedule = "0 2 * * *"

// SeedSystemCronJobs idempotently seeds the non-deletable system cron jobs.
// Each row is keyed by Name + IsSystem=true. On first seed the row is created
// with the seed's default Enabled state. On subsequent runs an existing
// system row is LEFT ALONE — operators may edit
// schedule/prompt/channel/tools/enabled directly per the spec ("can be
// disabled but not deleted; all other fields remain editable"), so
// re-asserting on boot would silently revert those edits. Restoring the
// canonical wording is a deliberate operator action (delete-from-DB +
// re-seed), not a side effect of restart.
//
// Exported so callers outside the database package (e.g. service-package
// tests) can re-run the seed without duplicating the row layout.
func SeedSystemCronJobs() error {
	type seedRow struct {
		Name     string
		Schedule string
		Prompt   string
		Enabled  bool
	}
	seeds := []seedRow{
		{
			Name:     dreamingCronName,
			Schedule: dreamingCronSchedule,
			Prompt:   dreamingCronPrompt,
			Enabled:  true,
		},
	}

	// Upgrade path: the Dreaming cron was originally seeded as
	// "memory-curator". Rename any existing system row in place (all operator
	// edits, including Enabled, are preserved) so upgrades don't grow a second
	// system row for the same job.
	if err := DB.Model(&CronJob{}).
		Where("name = ? AND is_system = ?", legacyMemoryCuratorCronName, true).
		Update("name", dreamingCronName).Error; err != nil {
		return fmt.Errorf("rename %s to %s: %w", legacyMemoryCuratorCronName, dreamingCronName, err)
	}

	for _, s := range seeds {
		// Operators may have created a non-system row with the same name (legacy
		// rename collisions, accidental shadow). Scope the lookup to
		// is_system=true so the seed never silently hijacks an operator row by
		// flipping its is_system flag + overwriting its schedule/prompt.
		var existing CronJob
		err := DB.Where("name = ? AND is_system = ?", s.Name, true).First(&existing).Error
		if err == nil {
			// Row exists — preserve operator edits to schedule/prompt/etc.
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("lookup system cron %s: %w", s.Name, err)
		}

		// Refuse to create when a non-system row shadows the slot — operator
		// rename is the safe recovery path. Surfacing this loudly beats either
		// silently promoting the row or silently skipping the seed.
		var shadow int64
		if err := DB.Model(&CronJob{}).Where("name = ?", s.Name).Count(&shadow).Error; err != nil {
			return fmt.Errorf("shadow check for system cron %s: %w", s.Name, err)
		}
		if shadow > 0 {
			slog.Warn("system cron seed skipped: non-system row shadows the name",
				"name", s.Name)
			continue
		}

		row := &CronJob{
			UUID:     uuid.New().String(),
			Name:     s.Name,
			Schedule: s.Schedule,
			Prompt:   s.Prompt,
			IsSystem: true,
			Enabled:  s.Enabled,
		}
		// Wrap insert + enabled-pin in a transaction so a failed pin cannot
		// leave the seeded row persisted with the wrong enabled state (GORM v2
		// omits zero-value bools from INSERT, so the column default would flip
		// a seeded false).
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(row).Error; err != nil {
				return fmt.Errorf("seed system cron %s: %w", s.Name, err)
			}
			if err := tx.Model(row).Update("enabled", s.Enabled).Error; err != nil {
				return fmt.Errorf("pin seeded system cron %s enabled state: %w", s.Name, err)
			}
			return nil
		}); err != nil {
			return err
		}
		slog.Info("seeded system cron job", "name", s.Name, "enabled", s.Enabled)
	}
	return nil
}

// improvementEvaluatorCronName is the canonical name of the seeded
// improvement-evaluator system cron. Hoisted so tests can pin idempotency.
const improvementEvaluatorCronName = "improvement-evaluator"

// improvementEvaluatorCronSchedule runs the self-improvement review daily at
// 05:00 UTC, after the 02:00 Dreaming pass has consolidated memory.
const improvementEvaluatorCronSchedule = "0 5 * * *"

// improvementEvaluatorCronPrompt is the task body for the improvement-evaluator
// system cron. It runs under the cron-agent root skill with the incidents and
// proposals tools allowlisted, reviews recent incident history plus operator
// feedback, and emits improvement proposals for operator review in the UI.
const improvementEvaluatorCronPrompt = `You are running the periodic self-improvement review. Goal: propose concrete changes that improve root-cause correctness and reduce analysis time for future investigations.

1. Gather recent history. List incidents started in the last 24 hours via gateway_call("incidents.list", {...}) — query status "monitor", "completed", and "failed" separately (source_kind "alert" and "cron"), using the "from" filter derived from the current-time header. Pick up to 10 incidents for deep review, prioritizing: failed runs, the slowest execution_time_ms, and repeated titles. Fetch each with gateway_call("incidents.get", {"uuid": ...}) and study the full_log for wasted investigation steps, missing runbook coverage, wrong initial hypotheses, and tool errors.

2. Gather operator feedback. Ask the memory-searcher subagent for recent operator feedback (type: feedback) and recurring incident patterns relevant to what you saw in step 1.

3. Inspect current assets before proposing changes: read /akmatori/runbooks/ and /akmatori/memory/global/ directly, read /akmatori/skills/<name>/SKILL.md for any non-system skill you want to change, and call gateway_call("proposals.list_cron_jobs", {}) for current cron definitions. Never propose an update to something you have not read in its current form.

4. Dedup. Call gateway_call("proposals.list", {"status": "pending"}) and never re-propose a change that is already pending.

5. Emit at most 5 proposals via gateway_call("proposals.create", {...}). Choose the kind per proposal: runbook_new, runbook_update, memory_new, memory_update, cron_new, cron_update, or skill_prompt_update (non-system skills only). Each proposal needs: a title (imperative, under 80 chars), reasoning that cites the concrete incident UUIDs driving it, source_incident_uuids listing those UUIDs, target_ref for *_update kinds, and proposed_content in the documented JSON shape for the kind. Quality bar: only propose changes backed by at least 2 incidents or explicit operator feedback; prefer one high-confidence proposal over five speculative ones.

6. Final summary: one line per proposal created (title + kind), or "no-op: nothing actionable" when the review surfaced nothing worth proposing.`

// SeedImprovementEvaluatorCron idempotently seeds the improvement-evaluator
// system cron with the incidents + proposals tools attached. Same semantics
// as SeedSystemCronJobs (created disabled, operator edits preserved, shadow
// rows refuse the seed) — but split out because it must run AFTER
// EnsureToolTypes has seeded the credential-less incidents/proposals tool
// instances, which happens later in the boot sequence than InitializeDefaults.
func SeedImprovementEvaluatorCron() error {
	var existing CronJob
	err := DB.Where("name = ? AND is_system = ?", improvementEvaluatorCronName, true).First(&existing).Error
	if err == nil {
		// Row exists — preserve operator edits to schedule/prompt/tools/enabled.
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("lookup system cron %s: %w", improvementEvaluatorCronName, err)
	}

	// Refuse to create when a non-system row shadows the slot (same recovery
	// semantics as SeedSystemCronJobs).
	var shadow int64
	if err := DB.Model(&CronJob{}).Where("name = ?", improvementEvaluatorCronName).Count(&shadow).Error; err != nil {
		return fmt.Errorf("shadow check for system cron %s: %w", improvementEvaluatorCronName, err)
	}
	if shadow > 0 {
		slog.Warn("system cron seed skipped: non-system row shadows the name",
			"name", improvementEvaluatorCronName)
		return nil
	}

	// The allowlist tools must exist before seeding; missing instances mean
	// the boot sequence called this too early (before EnsureToolTypes).
	var tools []ToolInstance
	if err := DB.Where("logical_name IN ?", []string{"incidents", "proposals"}).Find(&tools).Error; err != nil {
		return fmt.Errorf("lookup evaluator cron tools: %w", err)
	}
	if len(tools) < 2 {
		return fmt.Errorf("seed system cron %s: incidents/proposals tool instances not seeded yet (found %d of 2) — call after EnsureToolTypes", improvementEvaluatorCronName, len(tools))
	}

	row := &CronJob{
		UUID:     uuid.New().String(),
		Name:     improvementEvaluatorCronName,
		Schedule: improvementEvaluatorCronSchedule,
		Prompt:   improvementEvaluatorCronPrompt,
		IsSystem: true,
		Enabled:  false, // operator opts in
		Tools:    tools,
	}
	// Same insert + enabled-pin transaction as SeedSystemCronJobs: GORM v2
	// omits zero-value bools from INSERT, so the column default would flip
	// enabled to true without the explicit pin.
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return fmt.Errorf("seed system cron %s: %w", improvementEvaluatorCronName, err)
		}
		if err := tx.Model(row).Update("enabled", false).Error; err != nil {
			return fmt.Errorf("pin seeded system cron %s to disabled: %w", improvementEvaluatorCronName, err)
		}
		return nil
	}); err != nil {
		return err
	}
	slog.Info("seeded system cron job", "name", improvementEvaluatorCronName, "enabled", false)
	return nil
}

// InitializeSystemSkill creates the incident-manager system skill if it doesn't exist
func InitializeSystemSkill() error {
	slog.Info("checking for incident-manager system skill")

	var skill Skill
	result := DB.Where("name = ?", "incident-manager").First(&skill)

	if result.Error == nil {
		// Skill exists, ensure it's marked as system
		if !skill.IsSystem {
			if err := DB.Model(&skill).Update("is_system", true).Error; err != nil {
				return fmt.Errorf("failed to mark incident-manager skill as system: %w", err)
			}
			slog.Info("updated incident-manager skill to system skill")
		}
		return nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("lookup incident-manager skill: %w", result.Error)
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

// GetSlackSettings retrieves Slack settings from the database. It now prefers
// an enabled Slack Integration row (the post-unified-channels source of truth)
// and falls back to the legacy slack_settings row only when no usable
// integration is configured. Callers gating runtime behavior on Slack
// credentials therefore pick up tokens entered via /api/integrations even when
// the legacy slack_settings table is empty (fresh installs).
func GetSlackSettings() (*SlackSettings, error) {
	if settings, ok, err := loadSlackSettingsFromIntegration(); err != nil {
		return nil, err
	} else if ok {
		return settings, nil
	}

	var settings SlackSettings
	if err := DB.First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

// loadSlackSettingsFromIntegration projects the configured Slack Integration
// into the legacy SlackSettings shape so existing call sites
// (slack/manager.go, runtime gates) keep working without a wider refactor.
//
// The presence of *any* Slack Integration row — enabled or not, with or
// without complete credentials — is treated as the marker that the operator
// has moved to the unified Integrations model. In that case the Integration
// is authoritative and we MUST NOT fall back to the legacy slack_settings
// row; otherwise an operator disabling the Integration via /api/integrations
// would silently keep Slack live on the legacy credentials the migration
// preserved (see plan Task 10 note on the deferred slack_settings drop).
//
// Returns (nil, false, nil) only when no Slack Integration row exists at all,
// so the caller can fall back to the legacy slack_settings row for
// fresh/pre-migration installs. Post-migration, that legacy row is
// neutralized (clearLegacySlackSettingsCredentials) so a DELETE of the
// Slack Integration cannot leak migrated credentials through the fall-back.
func loadSlackSettingsFromIntegration() (*SlackSettings, bool, error) {
	if DB == nil {
		return nil, false, nil
	}
	if !DB.Migrator().HasTable(&Integration{}) {
		return nil, false, nil
	}
	var rows []Integration
	if err := DB.Where("provider = ?", MessagingProviderSlack).
		Order("id asc").
		Find(&rows).Error; err != nil {
		return nil, false, fmt.Errorf("load slack integrations: %w", err)
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	// Prefer the first enabled, fully-configured row so the runtime actually
	// connects when there is a usable Integration.
	for _, row := range rows {
		settings := slackSettingsFromIntegration(&row)
		if settings.Enabled && settings.IsConfigured() {
			return settings, true, nil
		}
	}
	// At least one Integration row exists but none are both enabled and
	// configured. Return the first row's projection so callers see Slack as
	// off (Enabled=false or IsConfigured=false ⇒ IsActive=false). Crucially,
	// signal `ok=true` so the caller does NOT fall back to slack_settings.
	return slackSettingsFromIntegration(&rows[0]), true, nil
}

// slackSettingsFromIntegration builds a SlackSettings projection from an
// Integration row so legacy call sites can read tokens entered via the new
// Integrations UI without changing their type signatures.
func slackSettingsFromIntegration(row *Integration) *SlackSettings {
	if row == nil {
		return &SlackSettings{}
	}
	botToken, _ := row.Credentials["bot_token"].(string)
	signingSecret, _ := row.Credentials["signing_secret"].(string)
	appToken, _ := row.Credentials["app_token"].(string)
	return &SlackSettings{
		ID:            row.ID,
		BotToken:      botToken,
		SigningSecret: signingSecret,
		AppToken:      appToken,
		Enabled:       row.Enabled,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
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

// GetOrCreateFormattingSettings retrieves or creates formatting settings (singleton).
// The row is normally seeded by InitializeDefaults at startup; the create path
// here is only a fallback. If FirstOrCreate races with another caller (both see
// no row, both INSERT, one hits unique constraint), we fall back to a plain read.
func GetOrCreateFormattingSettings() (*FormattingSettings, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var settings FormattingSettings
	defaults := DefaultFormattingSettings()
	if err := DB.Where(FormattingSettings{SingletonKey: "default"}).Attrs(defaults).FirstOrCreate(&settings).Error; err != nil {
		if rerr := DB.Where(FormattingSettings{SingletonKey: "default"}).First(&settings).Error; rerr != nil {
			return nil, fmt.Errorf("%w (retry: %v)", err, rerr)
		}
	}
	return &settings, nil
}

// UpdateFormattingSettings persists changes to the formatting settings singleton.
func UpdateFormattingSettings(settings *FormattingSettings) error {
	return DB.Save(settings).Error
}

// ensureAlertsIndexes creates the composite and partial-unique indexes on the
// alerts table. All statements use IF NOT EXISTS and are idempotent.
func ensureAlertsIndexes(db *gorm.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			"idx_alerts_incident_status",
			"CREATE INDEX IF NOT EXISTS idx_alerts_incident_status ON alerts (incident_uuid, status)",
		},
		{
			"idx_alerts_source_sfp_status",
			"CREATE INDEX IF NOT EXISTS idx_alerts_source_sfp_status ON alerts (source_uuid, source_fingerprint, status)",
		},
		{
			"idx_alerts_source_fp_status_fired",
			"CREATE INDEX IF NOT EXISTS idx_alerts_source_fp_status_fired ON alerts (source_uuid, fingerprint, status, fired_at)",
		},
		{
			"uniq_firing_alert",
			"CREATE UNIQUE INDEX IF NOT EXISTS uniq_firing_alert ON alerts (source_uuid, source_fingerprint) WHERE status = 'firing' AND source_fingerprint <> ''",
		},
	}
	for _, s := range stmts {
		if err := db.Exec(s.sql).Error; err != nil {
			return fmt.Errorf("create %s: %w", s.name, err)
		}
	}
	return nil
}

// migrateBackfillAlerts inserts one alerts row for each pre-existing
// alert-sourced incident that does not yet have a matching alerts row.
// Incidents recorded in alert_suppression_logs (if the table still exists) are
// skipped. For already-completed incidents the function also sets monitor_until
// and flips status to "monitor". Failed incidents have their backfilled alert
// row resolved immediately to free the partial unique index slot, but are not
// promoted to monitor (failed investigations should not attract new alerts). Idempotent.
// computeAlertFingerprintDB mirrors services.ComputeAlertFingerprint without
// importing that package (which would create a cycle: database → services →
// database). The algorithm must stay in sync with the services implementation.
func computeAlertFingerprintDB(sourceUUID, alertName, targetHost string) string {
	tuple, _ := json.Marshal([]string{
		sourceUUID,
		strings.ToLower(alertName),
		strings.ToLower(targetHost),
	})
	h := sha256.Sum256(tuple)
	return hex.EncodeToString(h[:])[:32]
}

func migrateBackfillAlerts(db *gorm.DB) error {
	// AutoMigrate pins postgres migrations to a single connection via
	// DB.Connection(...), which hands us a non-cloning session (clone==0):
	// every chained call reuses one Statement, so a Schema set by an earlier
	// operation (e.g. the Raw().Scan() below, or a prior migration step) leaks
	// into the subsequent Create(&alert) and GORM applies the wrong model's
	// field setters to the Alert value (panic: time.Time vs *time.Time). Reset
	// to a fresh-statement session so each Scan/Create/Exec parses its own
	// schema. ConnPool is preserved, keeping the migration on the pinned conn.
	db = db.Session(&gorm.Session{NewDB: true})

	if !db.Migrator().HasTable("alerts") {
		return nil
	}

	q := `
		SELECT uuid, source_uuid, status, started_at, completed_at, created_at, updated_at, context
		FROM incidents
		WHERE source_kind = 'alert'
		  AND NOT EXISTS (SELECT 1 FROM alerts WHERE alerts.incident_uuid = incidents.uuid)`
	if db.Migrator().HasTable("alert_suppression_logs") {
		q += `
		  AND uuid NOT IN (
		    SELECT incident_uuid FROM alert_suppression_logs
		    WHERE incident_uuid IS NOT NULL AND incident_uuid <> ''
		  )`
	}

	var incidents []Incident
	if err := db.Raw(q).Scan(&incidents).Error; err != nil {
		return fmt.Errorf("migrateBackfillAlerts: query: %w", err)
	}
	if len(incidents) == 0 {
		return nil
	}

	window := 60 * time.Minute
	if gs, err := GetOrCreateGeneralSettings(); err == nil && gs != nil {
		window = gs.GetAlertMonitorWindow()
	}

	for _, inc := range incidents {
		sourceFingerprint, _ := inc.Context["source_fingerprint"].(string)
		alertName, _ := inc.Context["alert_name"].(string)
		targetHost, _ := inc.Context["target_host"].(string)
		// Compute fingerprint from canonical fields rather than reading the
		// context key, which is absent on pre-migration incidents.
		fingerprint := computeAlertFingerprintDB(inc.SourceUUID, alertName, targetHost)
		var rawPayload JSONB
		if rp, ok := inc.Context["raw_payload"].(map[string]interface{}); ok {
			rawPayload = JSONB(rp)
		}

		alert := Alert{
			UUID:              uuid.New().String(),
			IncidentUUID:      inc.UUID,
			Status:            AlertStatusFiring,
			Fingerprint:       fingerprint,
			SourceUUID:        inc.SourceUUID,
			SourceFingerprint: sourceFingerprint,
			AlertName:         alertName,
			TargetHost:        targetHost,
			FiredAt:           inc.StartedAt,
			RawPayload:        rawPayload,
			CreatedAt:         inc.CreatedAt,
			UpdatedAt:         inc.UpdatedAt,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&alert).Error; err != nil {
			slog.Warn("migrateBackfillAlerts: insert alert row", "incident_uuid", inc.UUID, "err", err)
			continue
		}

		switch inc.Status {
		case IncidentStatusCompleted:
			if inc.CompletedAt != nil {
				monitorUntil := inc.CompletedAt.Add(window)
				if err := db.Exec(
					"UPDATE incidents SET monitor_until = ?, status = ? WHERE uuid = ? AND status = 'completed'",
					monitorUntil, string(IncidentStatusMonitor), inc.UUID,
				).Error; err != nil {
					slog.Warn("migrateBackfillAlerts: set monitor_until", "incident_uuid", inc.UUID, "err", err)
				}
				// The investigation is done — mark the backfilled alert resolved so it
				// does not appear as a live firing alert and does not occupy the partial
				// unique index slot.
				if err := db.Exec(
					"UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE incident_uuid = ? AND status = 'firing'",
					inc.CompletedAt, inc.UUID,
				).Error; err != nil {
					slog.Warn("migrateBackfillAlerts: resolve alert row", "incident_uuid", inc.UUID, "err", err)
				}
			}
		case IncidentStatusFailed:
			// Failed incidents must not enter the correlation candidate pool, so we
			// do not promote them to monitor. We do resolve the backfilled firing
			// alert so it does not block future alerts with the same source fingerprint.
			resolvedAt := inc.StartedAt
			if inc.CompletedAt != nil {
				resolvedAt = *inc.CompletedAt
			}
			if err := db.Exec(
				"UPDATE alerts SET status = 'resolved', resolved_at = ? WHERE incident_uuid = ? AND status = 'firing'",
				resolvedAt, inc.UUID,
			).Error; err != nil {
				slog.Warn("migrateBackfillAlerts: resolve failed-incident alert row", "incident_uuid", inc.UUID, "err", err)
			}
		}
	}

	slog.Info("migrateBackfillAlerts: backfilled alert rows", "count", len(incidents))
	return nil
}

// ensureMemoriesScopeTypeIndex creates a composite index on (scope, type) to
// speed up scope-scoped, type-filtered memory queries (e.g. memory-searcher
// range queries that filter by scope and type). Idempotent (uses IF NOT EXISTS);
// works on both PostgreSQL and SQLite.
func ensureMemoriesScopeTypeIndex(db *gorm.DB) error {
	stmt := "CREATE INDEX IF NOT EXISTS idx_memories_scope_type ON memories (scope, type)"
	if err := db.Exec(stmt).Error; err != nil {
		return fmt.Errorf("create idx_memories_scope_type: %w", err)
	}
	return nil
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
