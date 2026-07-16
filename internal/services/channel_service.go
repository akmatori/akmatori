package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrChannelNotFound is returned by ChannelService lookups when the requested
// row is absent. Surfacing a typed error lets handlers translate it to 404
// without GORM-specific imports.
var ErrChannelNotFound = errors.New("channel not found")

// ErrIntegrationNotFound is returned when the integration that a channel
// references (or that the caller asked about directly) does not exist.
var ErrIntegrationNotFound = errors.New("integration not found")

// ErrDuplicateDefaultPost is returned when creating/updating a channel would
// produce two is_default_post=true rows for the same messaging provider. The
// DB partial-unique index covers the per-Integration scope; this guard widens
// the check across multiple integrations of the same provider so the MVP
// "one default per provider" invariant holds even if an operator configures
// more than one Integration row for the same provider.
var ErrDuplicateDefaultPost = errors.New("another channel is already the default post target for this provider")

// ChannelService implements the CRUD + resolution surface for Integration
// and Channel rows. It is intentionally provider-agnostic: routing to the
// right SaaS happens in the messaging package via ProviderRegistry.
type ChannelService struct {
	db *gorm.DB
}

// NewChannelService constructs a ChannelService bound to the global DB
// instance. Tests build their own instance via newChannelServiceWithDB.
func NewChannelService() *ChannelService {
	return &ChannelService{db: database.GetDB()}
}

// newChannelServiceWithDB is the seam used by unit tests so an in-memory
// sqlite handle can be injected. Kept package-private because production
// callers never need it.
func newChannelServiceWithDB(db *gorm.DB) *ChannelService {
	return &ChannelService{db: db}
}

// ========== Integration CRUD ==========

// ListIntegrations returns every integration ordered by provider then name.
func (s *ChannelService) ListIntegrations() ([]database.Integration, error) {
	var rows []database.Integration
	if err := s.db.Order("provider asc, name asc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	return rows, nil
}

// GetIntegrationByUUID looks up an integration by its public UUID handle.
func (s *ChannelService) GetIntegrationByUUID(uuidStr string) (*database.Integration, error) {
	var row database.Integration
	if err := s.db.Where("uuid = ?", uuidStr).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrIntegrationNotFound
		}
		return nil, fmt.Errorf("get integration %s: %w", uuidStr, err)
	}
	return &row, nil
}

// CreateIntegration persists a new integration. The Provider value must be a
// registered messaging provider (slack / telegram); the caller supplies
// credentials as a JSONB blob since each provider expects different fields.
func (s *ChannelService) CreateIntegration(provider database.MessagingProvider, name string, credentials database.JSONB, enabled bool) (*database.Integration, error) {
	if !database.IsValidMessagingProvider(string(provider)) {
		return nil, fmt.Errorf("invalid messaging provider %q", provider)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("integration name cannot be empty")
	}
	row := &database.Integration{
		UUID:        uuid.New().String(),
		Provider:    provider,
		Name:        name,
		Credentials: credentials,
		Enabled:     enabled,
	}
	if err := s.db.Create(row).Error; err != nil {
		return nil, fmt.Errorf("create integration: %w", err)
	}
	// GORM v2 omits zero-value bools from INSERT, so the column-level
	// `default:true` flips a caller-requested Enabled=false back to true.
	// Force the column when the caller explicitly asked for disabled.
	if !enabled {
		if err := s.db.Model(row).Update("enabled", false).Error; err != nil {
			return nil, fmt.Errorf("apply enabled=false on create: %w", err)
		}
	}
	return row, nil
}

// UpdateIntegration applies the supplied non-zero fields to an existing
// integration. Provider is immutable on update — operators must delete and
// re-create when switching backends so credential shape stays consistent.
func (s *ChannelService) UpdateIntegration(uuidStr string, name *string, credentials database.JSONB, enabled *bool) (*database.Integration, error) {
	row, err := s.GetIntegrationByUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return nil, fmt.Errorf("integration name cannot be empty")
		}
		updates["name"] = trimmed
	}
	if credentials != nil {
		// Merge the supplied keys into the existing credential blob so
		// rotating one secret (e.g. bot_token) doesn't erase the others.
		// Empty-string values are treated as "no change" since the UI
		// strips blanks from edit submissions; explicit clears would have
		// to land via a different code path (delete + recreate).
		merged := database.JSONB{}
		for k, v := range row.Credentials {
			merged[k] = v
		}
		for k, v := range credentials {
			if s, ok := v.(string); ok && s == "" {
				continue
			}
			merged[k] = v
		}
		updates["credentials"] = merged
	}
	if enabled != nil {
		updates["enabled"] = *enabled
	}
	if len(updates) == 0 {
		return row, nil
	}
	if err := s.db.Model(row).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update integration: %w", err)
	}
	if err := s.db.First(row, row.ID).Error; err != nil {
		return nil, fmt.Errorf("reload integration after update: %w", err)
	}
	return row, nil
}

// DeleteIntegration removes an integration. Channels that reference it are
// cascaded; AlertSourceInstance.NotificationChannelID and CronJob.ChannelID
// references to any of those channels are nulled out in the same transaction
// so triggers fall back to the per-provider default rather than carrying a
// dangling FK.
func (s *ChannelService) DeleteIntegration(uuidStr string) error {
	row, err := s.GetIntegrationByUUID(uuidStr)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var channelIDs []uint
		if err := tx.Model(&database.Channel{}).
			Where("integration_id = ?", row.ID).
			Pluck("id", &channelIDs).Error; err != nil {
			return fmt.Errorf("list channels for integration %d: %w", row.ID, err)
		}
		if len(channelIDs) > 0 {
			if err := tx.Model(&database.AlertSourceInstance{}).
				Where("notification_channel_id IN ?", channelIDs).
				Update("notification_channel_id", nil).Error; err != nil {
				return fmt.Errorf("clear alert source channel refs: %w", err)
			}
			if err := tx.Model(&database.CronJob{}).
				Where("channel_id IN ?", channelIDs).
				Update("channel_id", nil).Error; err != nil {
				return fmt.Errorf("clear cron job channel refs: %w", err)
			}
		}
		if err := tx.Where("integration_id = ?", row.ID).Delete(&database.Channel{}).Error; err != nil {
			return fmt.Errorf("delete channels for integration %d: %w", row.ID, err)
		}
		if err := tx.Delete(row).Error; err != nil {
			return fmt.Errorf("delete integration %d: %w", row.ID, err)
		}
		return nil
	})
}

// ========== Channel CRUD ==========

// ListChannelsFilter narrows ListChannels by the most common attributes.
// Zero-valued fields are ignored.
type ListChannelsFilter struct {
	IntegrationUUID string
	CanPost         *bool
	CanListen       *bool
}

// ListChannels returns channels matching the supplied filter, eagerly loading
// the parent Integration so callers can render provider-aware UI without an
// extra query.
func (s *ChannelService) ListChannels(filter ListChannelsFilter) ([]database.Channel, error) {
	q := s.db.Preload("Integration").Order("display_name asc")
	if filter.IntegrationUUID != "" {
		integration, err := s.GetIntegrationByUUID(filter.IntegrationUUID)
		if err != nil {
			return nil, err
		}
		q = q.Where("integration_id = ?", integration.ID)
	}
	if filter.CanPost != nil {
		q = q.Where("can_post = ?", *filter.CanPost)
	}
	if filter.CanListen != nil {
		q = q.Where("can_listen = ?", *filter.CanListen)
	}
	var rows []database.Channel
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return rows, nil
}

// GetChannelByUUID resolves a channel by its public UUID, preloading the
// integration so the caller can route through the provider registry.
func (s *ChannelService) GetChannelByUUID(uuidStr string) (*database.Channel, error) {
	var row database.Channel
	if err := s.db.Preload("Integration").Where("uuid = ?", uuidStr).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("get channel %s: %w", uuidStr, err)
	}
	return &row, nil
}

// CreateChannel persists a new channel and enforces the "one default-post
// channel per provider" invariant. The DB partial-unique index already
// blocks two defaults on the same integration; this code path also blocks
// two defaults across different integrations of the same provider.
func (s *ChannelService) CreateChannel(c *database.Channel) (*database.Channel, error) {
	if c == nil {
		return nil, fmt.Errorf("channel cannot be nil")
	}
	if c.IntegrationID == 0 {
		return nil, fmt.Errorf("channel must reference an integration")
	}
	if strings.TrimSpace(c.ExternalID) == "" {
		return nil, fmt.Errorf("channel external_id cannot be empty")
	}
	c.ExternalID = strings.TrimSpace(c.ExternalID)
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	if c.DisplayName == "" {
		c.DisplayName = c.ExternalID
	}
	if c.UUID == "" {
		c.UUID = uuid.New().String()
	}

	if c.IsDefaultPost && !c.CanPost {
		return nil, fmt.Errorf("channel marked is_default_post must also have can_post=true")
	}

	requestedEnabled := c.Enabled
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if c.IsDefaultPost {
			if err := s.assertNoOtherDefaultPostTx(tx, c.IntegrationID, 0); err != nil {
				return err
			}
		}
		if err := tx.Create(c).Error; err != nil {
			return fmt.Errorf("create channel: %w", err)
		}
		// GORM v2 omits zero-value bools from INSERT; the column-level
		// `default:true` would otherwise silently flip a caller-requested
		// Enabled=false back to true. Force the column when the caller
		// explicitly asked for disabled.
		if !requestedEnabled {
			if err := tx.Model(&database.Channel{}).Where("id = ?", c.ID).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("apply enabled=false on create: %w", err)
			}
		}
		return tx.Preload("Integration").First(c, c.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateChannel mutates the supplied channel by UUID. Fields are applied
// only if their pointer is non-nil so partial updates remain ergonomic from
// the API layer.
type ChannelUpdate struct {
	ExternalID           *string
	DisplayName          *string
	CanPost              *bool
	CanListen            *bool
	IsDefaultPost        *bool
	ExtractionPrompt     *string
	ProcessBotMessages   *bool
	ProcessHumanMessages *bool
	Enabled              *bool
}

// UpdateChannel applies the supplied patch to an existing channel.
func (s *ChannelService) UpdateChannel(uuidStr string, patch ChannelUpdate) (*database.Channel, error) {
	row, err := s.GetChannelByUUID(uuidStr)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{}
	if patch.ExternalID != nil {
		trimmed := strings.TrimSpace(*patch.ExternalID)
		if trimmed == "" {
			return nil, fmt.Errorf("channel external_id cannot be empty")
		}
		updates["external_id"] = trimmed
	}
	if patch.DisplayName != nil {
		updates["display_name"] = strings.TrimSpace(*patch.DisplayName)
	}
	if patch.CanPost != nil {
		updates["can_post"] = *patch.CanPost
	}
	if patch.CanListen != nil {
		updates["can_listen"] = *patch.CanListen
	}
	if patch.IsDefaultPost != nil {
		updates["is_default_post"] = *patch.IsDefaultPost
	}
	if patch.ExtractionPrompt != nil {
		updates["extraction_prompt"] = *patch.ExtractionPrompt
	}
	if patch.ProcessBotMessages != nil {
		updates["process_bot_messages"] = *patch.ProcessBotMessages
	}
	if patch.ProcessHumanMessages != nil {
		updates["process_human_messages"] = *patch.ProcessHumanMessages
	}
	if patch.Enabled != nil {
		updates["enabled"] = *patch.Enabled
	}
	if len(updates) == 0 {
		return row, nil
	}

	// Compute effective post-update values for the invariant guard: take the
	// patched value when supplied, otherwise fall back to the existing row.
	// Forbid is_default_post=true && can_post=false regardless of which side
	// the patch touches — otherwise an operator can flip can_post=false on a
	// default-post row and produce a ghost default that fails ResolveDefault's
	// can_post=true filter without surfacing as an error at write time.
	effectiveIsDefaultPost := row.IsDefaultPost
	if patch.IsDefaultPost != nil {
		effectiveIsDefaultPost = *patch.IsDefaultPost
	}
	effectiveCanPost := row.CanPost
	if patch.CanPost != nil {
		effectiveCanPost = *patch.CanPost
	}
	if effectiveIsDefaultPost && !effectiveCanPost {
		return nil, fmt.Errorf("channel marked is_default_post must also have can_post=true")
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if patch.IsDefaultPost != nil && *patch.IsDefaultPost {
			if err := s.assertNoOtherDefaultPostTx(tx, row.IntegrationID, row.ID); err != nil {
				return err
			}
		}
		if err := tx.Model(&database.Channel{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update channel: %w", err)
		}
		return tx.Preload("Integration").First(row, row.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// DeleteChannel removes a channel by UUID. AlertSourceInstance and CronJob
// rows referencing this channel have their FK nulled in the same transaction
// so the triggers fall back to the per-provider default at runtime rather
// than carrying a dangling reference.
func (s *ChannelService) DeleteChannel(uuidStr string) error {
	row, err := s.GetChannelByUUID(uuidStr)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.AlertSourceInstance{}).
			Where("notification_channel_id = ?", row.ID).
			Update("notification_channel_id", nil).Error; err != nil {
			return fmt.Errorf("clear alert source channel refs: %w", err)
		}
		if err := tx.Model(&database.CronJob{}).
			Where("channel_id = ?", row.ID).
			Update("channel_id", nil).Error; err != nil {
			return fmt.Errorf("clear cron job channel refs: %w", err)
		}
		if err := tx.Delete(row).Error; err != nil {
			return fmt.Errorf("delete channel: %w", err)
		}
		return nil
	})
}

// ========== Resolution ==========

// ResolveDefault returns the default outbound channel for the given provider,
// or ErrChannelNotFound when no default is configured. The query joins
// channels with their integration so callers do not have to chase the FK.
// Filters on can_post=true so a default flagged on a listener-only channel
// never leaks through to outbound posting.
func (s *ChannelService) ResolveDefault(provider database.MessagingProvider) (*database.Channel, error) {
	var row database.Channel
	err := s.db.
		Preload("Integration").
		Joins("JOIN integrations ON integrations.id = channels.integration_id").
		Where("channels.is_default_post = ? AND channels.can_post = ? AND channels.enabled = ? AND integrations.enabled = ? AND integrations.provider = ?", true, true, true, true, provider).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("resolve default channel for %s: %w", provider, err)
	}
	return &row, nil
}

// FindByExternalID resolves a channel by its provider-native identifier
// (e.g. a Slack channel ID). Best-effort helper for formatting-rule flow
// identification: returns ErrChannelNotFound when no enabled row matches.
// External IDs are unique per integration in practice but not DB-enforced;
// the first enabled match wins.
func (s *ChannelService) FindByExternalID(provider database.MessagingProvider, externalID string) (*database.Channel, error) {
	if externalID == "" {
		return nil, ErrChannelNotFound
	}
	var row database.Channel
	err := s.db.
		Preload("Integration").
		Joins("JOIN integrations ON integrations.id = channels.integration_id").
		Where("channels.external_id = ? AND channels.enabled = ? AND integrations.provider = ?", externalID, true, provider).
		Order("channels.id ASC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("find channel by external id: %w", err)
	}
	return &row, nil
}

// ResolveForAlertSource picks the Channel that should receive outbound posts
// for the given alert source instance. The explicit NotificationChannelID
// wins (provided the channel and its integration are both enabled and the
// channel can post); otherwise the per-provider default channel is used. The
// provider argument selects which default to consult — most callers pass
// MessagingProviderSlack until the multi-provider UI lands.
func (s *ChannelService) ResolveForAlertSource(asi *database.AlertSourceInstance, provider database.MessagingProvider) (*database.Channel, error) {
	if asi != nil && asi.NotificationChannelID != nil {
		var row database.Channel
		err := s.db.Preload("Integration").First(&row, *asi.NotificationChannelID).Error
		if err == nil {
			if row.Enabled && row.CanPost && row.Integration.Enabled {
				return &row, nil
			}
			// Explicit channel exists but is unusable for posting; fall back
			// to the default so the alert still surfaces somewhere.
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("resolve alert source channel: %w", err)
		}
		// FK target gone or unusable — fall through to the default.
	}
	return s.ResolveDefault(provider)
}

// assertNoOtherDefaultPostTx is the cross-integration default-post invariant
// check. The DB partial-unique index only scopes to a single integration; this
// guard widens to all integrations sharing the same provider. excludeID lets
// the update path skip the row being modified (otherwise re-saving a default
// channel would fail its own check).
//
// The count is NOT filtered by integrations.enabled — a disabled integration's
// default-post row still blocks creation under a different integration so that
// re-enabling the prior integration cannot produce two concurrent default-post
// channels for the same provider (ResolveDefault would then non-deterministically
// pick one). The returned error names the conflicting integration so the
// operator can unset the old default before adding a new one.
func (s *ChannelService) assertNoOtherDefaultPostTx(tx *gorm.DB, integrationID uint, excludeID uint) error {
	var integration database.Integration
	if err := tx.First(&integration, integrationID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIntegrationNotFound
		}
		return fmt.Errorf("load integration %d: %w", integrationID, err)
	}
	q := tx.Model(&database.Channel{}).
		Joins("JOIN integrations ON integrations.id = channels.integration_id").
		Where("channels.is_default_post = ?", true).
		Where("integrations.provider = ?", integration.Provider)
	if excludeID > 0 {
		q = q.Where("channels.id <> ?", excludeID)
	}
	var existing int64
	if err := q.Count(&existing).Error; err != nil {
		return fmt.Errorf("count existing default-post channels: %w", err)
	}
	if existing > 0 {
		return ErrDuplicateDefaultPost
	}
	return nil
}
