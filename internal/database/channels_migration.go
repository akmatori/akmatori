package database

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// channelsDefaultPostIndexName is the name of the partial-unique index that
// enforces at most one Channel.IsDefaultPost=true per Integration.
const channelsDefaultPostIndexName = "idx_channels_default_post_per_integration"

// ensureChannelsDefaultPartialIndex creates the partial-unique index that
// enforces at most one default-post Channel per Integration. Idempotent
// (uses IF NOT EXISTS); supports postgres and sqlite (the test backend).
func ensureChannelsDefaultPartialIndex(db *gorm.DB) error {
	// Both postgres and sqlite support `CREATE UNIQUE INDEX ... WHERE ...`.
	// `IF NOT EXISTS` works on both.
	stmt := fmt.Sprintf(
		"CREATE UNIQUE INDEX IF NOT EXISTS %s ON channels (integration_id) WHERE is_default_post = true",
		channelsDefaultPostIndexName,
	)
	if err := db.Exec(stmt).Error; err != nil {
		return fmt.Errorf("create %s: %w", channelsDefaultPostIndexName, err)
	}
	return nil
}

// migrateSlackSettingsToIntegrations performs the read-old → write-new
// backfill from the legacy SlackSettings singleton row into one Integration
// (provider=slack) and one Channel (is_default_post=true) for the configured
// alerts_channel.
//
// Idempotent on re-run: if an integration of provider=slack already exists
// the function skips the backfill. The legacy slack_settings row is preserved
// (table drop is deferred per /tmp/plan.md) but its credential columns are
// neutralized whenever a Slack Integration exists, so that a later DELETE of
// the Integration cannot silently revive Slack via the legacy fall-back path
// in GetSlackSettings. See the docstring on loadSlackSettingsFromIntegration.
func migrateSlackSettingsToIntegrations(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Bail out if any Integration row already exists for the slack
		// provider — that's the marker that this migration step has run.
		var existing int64
		if err := tx.Model(&Integration{}).
			Where("provider = ?", MessagingProviderSlack).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("count existing slack integrations: %w", err)
		}
		if existing > 0 {
			// Migration ran in a previous startup. Two clean-ups are needed
			// for installs that upgraded *before* the enabled-default and
			// legacy-credential fixes shipped:
			//
			//   1. If the legacy row still has configured tokens with
			//      enabled=false, the original buggy migration (which used
			//      `Enabled: legacy.Enabled` without a follow-up Update) will
			//      have created an Integration with enabled=true because
			//      GORM v2 omits zero-value bools from INSERT and the
			//      column-level default:true won. Repair that matched row by
			//      bot_token so we don't touch unrelated integrations the
			//      operator may have created via the new API.
			//   2. Always neutralize the legacy slack_settings row so a later
			//      DELETE of the Integration cannot revive Slack through the
			//      GetSlackSettings fallback.
			if err := repairPreviouslyMigratedDisabledSlack(tx); err != nil {
				return err
			}
			return clearLegacySlackSettingsCredentials(tx)
		}

		// Read the legacy slack_settings row (if any). Missing table or
		// missing row are both "nothing to migrate".
		if !tx.Migrator().HasTable(&SlackSettings{}) {
			return nil
		}
		var legacy SlackSettings
		err := tx.First(&legacy).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read legacy slack_settings: %w", err)
		}

		// Only migrate when the operator actually configured Slack (tokens
		// present). An empty default row should not produce a half-filled
		// Integration on first startup.
		if !legacy.IsConfigured() {
			return nil
		}

		credentials := JSONB{
			"bot_token":      legacy.BotToken,
			"signing_secret": legacy.SigningSecret,
			"app_token":      legacy.AppToken,
		}
		integration := &Integration{
			UUID:        uuid.New().String(),
			Provider:    MessagingProviderSlack,
			Name:        "Slack",
			Credentials: credentials,
			Enabled:     legacy.Enabled,
		}
		if err := tx.Create(integration).Error; err != nil {
			return fmt.Errorf("create slack integration: %w", err)
		}
		// GORM v2 omits zero-value bools from INSERT, so the column-level
		// `default:true` would flip a disabled legacy SlackSettings into an
		// enabled Integration. Force the column to match the legacy value.
		if !legacy.Enabled {
			if err := tx.Model(integration).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("apply enabled=false on migrated slack integration: %w", err)
			}
		}

		// Backfill the default outbound channel only when alerts_channel was
		// set — an enabled-but-empty configuration is permitted.
		if legacy.AlertsChannel != "" {
			defaultChannel := &Channel{
				UUID:          uuid.New().String(),
				IntegrationID: integration.ID,
				ExternalID:    legacy.AlertsChannel,
				DisplayName:   legacy.AlertsChannel,
				CanPost:       true,
				CanListen:     false,
				IsDefaultPost: true,
				Enabled:       true,
			}
			if err := tx.Create(defaultChannel).Error; err != nil {
				return fmt.Errorf("create default slack channel: %w", err)
			}
		}

		// Strip credentials from the legacy row now that the Integration is
		// authoritative. Without this, deleting the Integration later would
		// fall back to the still-populated slack_settings row and silently
		// revive Slack with the migrated tokens.
		if err := clearLegacySlackSettingsCredentials(tx); err != nil {
			return err
		}

		slog.Info("migrated slack_settings into integrations + default channel", "integration_id", integration.ID)
		return nil
	})
}

// repairPreviouslyMigratedDisabledSlack rolls back the enabled=true state
// that the original buggy migrateSlackSettingsToIntegrations could leave on a
// Slack Integration when the operator had disabled the legacy slack_settings
// row before upgrading. GORM v2 omits zero-value bools from INSERT, so the
// pre-fix migration's `Enabled: legacy.Enabled` was ignored and the
// column-level default:true silently re-enabled Slack on upgrade.
//
// The repair runs on the rerun path (existing > 0) so it has a single chance
// to act before clearLegacySlackSettingsCredentials wipes the legacy
// credentials. It matches the migrated Integration by bot_token so newer
// operator-created Integrations (different workspace, different token) are
// left alone. Idempotent: when the legacy row carries no credentials or
// already shows enabled=true, the function returns without writes.
func repairPreviouslyMigratedDisabledSlack(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&SlackSettings{}) {
		return nil
	}
	var legacy SlackSettings
	err := tx.First(&legacy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy slack_settings during enabled-state repair: %w", err)
	}
	if !legacy.IsConfigured() || legacy.Enabled {
		return nil
	}
	var slackIntegrations []Integration
	if err := tx.Where("provider = ?", MessagingProviderSlack).Find(&slackIntegrations).Error; err != nil {
		return fmt.Errorf("list slack integrations during enabled-state repair: %w", err)
	}
	for _, integration := range slackIntegrations {
		if !integration.Enabled {
			continue
		}
		botToken, _ := integration.Credentials["bot_token"].(string)
		if botToken != legacy.BotToken {
			continue
		}
		if err := tx.Model(&Integration{}).
			Where("id = ?", integration.ID).
			Update("enabled", false).Error; err != nil {
			return fmt.Errorf("repair enabled state on slack integration %d: %w", integration.ID, err)
		}
		slog.Info("repaired enabled=true on previously-migrated slack integration (legacy slack_settings was disabled)",
			"integration_id", integration.ID)
	}
	return nil
}

// clearLegacySlackSettingsCredentials zeroes the credential columns and
// disables the legacy slack_settings row so it cannot act as a fall-back
// source after the unified-Integrations migration. Safe to call when the row
// or table is absent (no-op). Idempotent: re-running on an already-cleared
// row produces no further change.
func clearLegacySlackSettingsCredentials(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&SlackSettings{}) {
		return nil
	}
	updates := map[string]interface{}{
		"bot_token":      "",
		"signing_secret": "",
		"app_token":      "",
		"enabled":        false,
	}
	// Scope the update to rows that still carry credentials so the touch is
	// observable only when there is something to clear. Using a non-nil Where
	// clause is required for gorm bulk updates.
	if err := tx.Model(&SlackSettings{}).
		Where("bot_token <> '' OR signing_secret <> '' OR app_token <> '' OR enabled = ?", true).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("clear legacy slack_settings credentials: %w", err)
	}
	return nil
}

// migrateSlackChannelAlertSourcesToChannels converts each existing
// AlertSourceInstance of type "slack_channel" into a Channel row with
// can_listen=true, copying the extraction prompt and process_human_messages
// flag out of the legacy Settings JSONB. The migrated AlertSourceInstance row
// is deleted at the end of the same transaction.
//
// Idempotent: if the slack_channel alert source type does not exist or has no
// active instances, the function returns without changes.
func migrateSlackChannelAlertSourcesToChannels(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var sourceType AlertSourceType
		err := tx.Where("name = ?", "slack_channel").First(&sourceType).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read slack_channel alert_source_type: %w", err)
		}

		var instances []AlertSourceInstance
		if err := tx.Where("alert_source_type_id = ?", sourceType.ID).Find(&instances).Error; err != nil {
			return fmt.Errorf("list slack_channel alert source instances: %w", err)
		}
		if len(instances) == 0 {
			return nil
		}

		// We need a Slack Integration to attach the new Channels to. If the
		// previous migration step did not create one (e.g. operator never
		// configured slack_settings but still had slack_channel rows from a
		// prior dev run), create a placeholder so the listener migration
		// remains complete on its own.
		var integration Integration
		err = tx.Where("provider = ?", MessagingProviderSlack).First(&integration).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			integration = Integration{
				UUID:        uuid.New().String(),
				Provider:    MessagingProviderSlack,
				Name:        "Slack",
				Credentials: JSONB{},
				Enabled:     false,
			}
			if err := tx.Create(&integration).Error; err != nil {
				return fmt.Errorf("create placeholder slack integration: %w", err)
			}
			// GORM v2 omits the zero-value Enabled=false from INSERT, so
			// the column-level `default:true` would otherwise materialize
			// the placeholder as enabled despite having empty credentials.
			// Force it disabled so the listener path's enabled check
			// correctly skips the placeholder until an operator fills in
			// credentials.
			if err := tx.Model(&integration).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("disable placeholder slack integration: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lookup slack integration: %w", err)
		}

		for _, inst := range instances {
			externalID, _ := inst.Settings["slack_channel_id"].(string)
			if externalID == "" {
				externalID, _ = inst.Settings["channel_id"].(string)
			}
			if externalID == "" {
				// Without an external channel ID the row is unusable.
				// Skip it but still delete the originating AlertSourceInstance
				// row so the migration is convergent on re-run.
				slog.Warn("slack_channel alert source instance missing channel id, dropping during migration",
					"instance_id", inst.ID, "name", inst.Name)
				if err := tx.Delete(&AlertSourceInstance{}, inst.ID).Error; err != nil {
					return fmt.Errorf("delete unmigrated alert source instance %d: %w", inst.ID, err)
				}
				continue
			}

			// If a Channel with this external id already exists on the
			// integration (e.g. partial prior migration), update its
			// listener fields in place rather than creating a duplicate.
			var existing Channel
			lookup := tx.Where("integration_id = ? AND external_id = ?", integration.ID, externalID).First(&existing)
			extractionPrompt, _ := inst.Settings["extraction_prompt"].(string)
			processHumanMessages, _ := inst.Settings["process_human_messages"].(bool)

			if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
				channel := &Channel{
					UUID:                 uuid.New().String(),
					IntegrationID:        integration.ID,
					ExternalID:           externalID,
					DisplayName:          fallbackDisplayName(inst.Name, externalID),
					CanPost:              false,
					CanListen:            true,
					IsDefaultPost:        false,
					ExtractionPrompt:     extractionPrompt,
					ProcessHumanMessages: processHumanMessages,
					Enabled:              inst.Enabled,
				}
				if err := tx.Create(channel).Error; err != nil {
					return fmt.Errorf("create listener channel for instance %d: %w", inst.ID, err)
				}
				// GORM v2 omits zero-value bools from INSERT, so the
				// column-level `default:true` would otherwise flip a disabled
				// slack_channel AlertSourceInstance into an enabled Channel.
				// Force the column to match the source row.
				if !inst.Enabled {
					if err := tx.Model(channel).Update("enabled", false).Error; err != nil {
						return fmt.Errorf("apply enabled=false on migrated listener channel %d: %w", channel.ID, err)
					}
				}
			} else if lookup.Error != nil {
				return fmt.Errorf("lookup existing listener channel: %w", lookup.Error)
			} else {
				// Pre-existing default-post channels do not normally also carry
				// listener capability; surface the dual-role promotion so the
				// operator can review whether that is intentional. The role is
				// still applied so a partial-prior-migration completes idempotently.
				if existing.IsDefaultPost {
					slog.Warn("migration promoting a default-post channel to also act as a listener; review the channel's roles",
						"channel_id", existing.ID,
						"channel_uuid", existing.UUID,
						"integration_id", existing.IntegrationID,
						"external_id", existing.ExternalID,
						"alert_source_instance_id", inst.ID,
					)
				}
				updates := map[string]interface{}{
					"can_listen":             true,
					"extraction_prompt":      extractionPrompt,
					"process_human_messages": processHumanMessages,
				}
				if err := tx.Model(&existing).Updates(updates).Error; err != nil {
					return fmt.Errorf("update existing listener channel %d: %w", existing.ID, err)
				}
			}

			if err := tx.Delete(&AlertSourceInstance{}, inst.ID).Error; err != nil {
				return fmt.Errorf("delete migrated alert source instance %d: %w", inst.ID, err)
			}
			slog.Info("migrated slack_channel alert source to channel",
				"instance_id", inst.ID, "external_id", externalID)
		}

		return nil
	})
}

// fallbackDisplayName picks the best available human-readable label for a
// migrated channel: the instance name if it has one, otherwise the external
// channel id.
func fallbackDisplayName(name, externalID string) string {
	if name != "" {
		return name
	}
	return externalID
}

// deprecateSlackChannelAlertSourceType flips the Deprecated flag on the
// "slack_channel" alert_source_types row so it is hidden from the UI/pickers.
// The row itself is preserved so that any historical incident or audit log
// referencing it remains resolvable. Idempotent.
func deprecateSlackChannelAlertSourceType(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var sourceType AlertSourceType
		err := tx.Where("name = ?", "slack_channel").First(&sourceType).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read slack_channel alert_source_type: %w", err)
		}
		if sourceType.Deprecated {
			return nil
		}
		if err := tx.Model(&sourceType).Update("deprecated", true).Error; err != nil {
			return fmt.Errorf("mark slack_channel alert_source_type deprecated: %w", err)
		}
		slog.Info("marked slack_channel alert_source_type as deprecated")
		return nil
	})
}
