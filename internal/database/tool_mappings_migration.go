package database

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// credentiallessToolTypes are the built-in tool namespaces. Keep in sync with
// services.BuiltinToolNamespaces (the database package cannot import services).
var credentiallessToolTypes = map[string]bool{
	"incidents": true, "proposals": true, "events": true, "runbooks": true, "memory": true,
}

// IsBuiltinToolType reports whether name is a built-in tool namespace.
func IsBuiltinToolType(name string) bool { return credentiallessToolTypes[name] }

// legacyCronDefaultBuiltins are added to every migrated cron: events was
// always granted (withEventsTool), and runbooks and memory were always
// reachable through the subagents, which the allowlist did not gate.
var legacyCronDefaultBuiltins = []string{"events", "runbooks", "memory"}

// builtinToolOrder is the stored order of builtin_tools.
var builtinToolOrder = []string{"events", "runbooks", "memory", "incidents", "proposals"}

// orderBuiltinTools returns the names of set in builtinToolOrder order.
func orderBuiltinTools(set map[string]bool) StringList {
	out := StringList{}
	for _, n := range builtinToolOrder {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}

// LegacyCronToolFields translates the tool type names of a cron's legacy
// tool mapping (cron_job_tools, or tool_logical_names resolved to types) into
// tools_enabled + builtin_tools: tools_enabled is true when one joined type is
// a connector type; builtin_tools holds the joined built-ins plus the legacy
// defaults. Shared by the migration and the proposal translation.
func LegacyCronToolFields(toolTypes []string) (bool, StringList) {
	set := map[string]bool{}
	for _, n := range legacyCronDefaultBuiltins {
		set[n] = true
	}
	toolsEnabled := false
	for _, tt := range toolTypes {
		if credentiallessToolTypes[tt] {
			set[tt] = true
		} else if tt != "" {
			toolsEnabled = true
		}
	}
	return toolsEnabled, orderBuiltinTools(set)
}

// preMigrateToolMappings replaces the skill_tools and cron_job_tools
// mappings. Every connector tool is available to every skill now; cron jobs
// keep tools_enabled + builtin_tools, backfilled from cron_job_tools. Runs
// before AutoMigrate, in one transaction, and is a no-op once the join table
// is gone (or on a fresh install, where AutoMigrate adds the columns).
func preMigrateToolMappings(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("cron_job_tools") {
		if m.HasTable("skill_tools") {
			if err := db.Exec("DROP TABLE skill_tools").Error; err != nil {
				return fmt.Errorf("drop skill_tools: %w", err)
			}
			slog.Info("dropped skill_tools (connectors are available to all skills)")
		}
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		tm := tx.Migrator()
		if tm.HasTable("cron_jobs") {
			if !tm.HasColumn(&CronJob{}, "tools_enabled") {
				if err := tx.Exec("ALTER TABLE cron_jobs ADD COLUMN tools_enabled BOOLEAN NOT NULL DEFAULT TRUE").Error; err != nil {
					return fmt.Errorf("add cron_jobs.tools_enabled: %w", err)
				}
			}
			if !tm.HasColumn(&CronJob{}, "builtin_tools") {
				if err := tx.Exec("ALTER TABLE cron_jobs ADD COLUMN builtin_tools jsonb").Error; err != nil {
					return fmt.Errorf("add cron_jobs.builtin_tools: %w", err)
				}
			}

			var jobIDs []uint
			if err := tx.Table("cron_jobs").Pluck("id", &jobIDs).Error; err != nil {
				return fmt.Errorf("list cron jobs: %w", err)
			}
			type joinRow struct {
				CronJobID uint
				Name      string
			}
			var rows []joinRow
			if err := tx.Raw(`SELECT cjt.cron_job_id AS cron_job_id, tt.name AS name
				FROM cron_job_tools cjt
				JOIN tool_instances ti ON ti.id = cjt.tool_instance_id
				JOIN tool_types tt ON tt.id = ti.tool_type_id`).Scan(&rows).Error; err != nil {
				return fmt.Errorf("read cron_job_tools: %w", err)
			}
			typesByJob := map[uint][]string{}
			for _, r := range rows {
				typesByJob[r.CronJobID] = append(typesByJob[r.CronJobID], r.Name)
			}
			for _, id := range jobIDs {
				enabled, builtins := LegacyCronToolFields(typesByJob[id])
				if err := tx.Table("cron_jobs").Where("id = ?", id).UpdateColumns(map[string]interface{}{
					"tools_enabled": enabled,
					"builtin_tools": builtins,
				}).Error; err != nil {
					return fmt.Errorf("backfill cron job %d tools: %w", id, err)
				}
			}
			slog.Info("migrated cron job tool mappings", "cron_jobs", len(jobIDs))
		}
		if err := tx.Exec("DROP TABLE cron_job_tools").Error; err != nil {
			return fmt.Errorf("drop cron_job_tools: %w", err)
		}
		if tm.HasTable("skill_tools") {
			if err := tx.Exec("DROP TABLE skill_tools").Error; err != nil {
				return fmt.Errorf("drop skill_tools: %w", err)
			}
		}
		slog.Info("dropped skill_tools and cron_job_tools (connectors are available to all skills)")
		return nil
	})
}
