package database

import "time"

// MessagingProvider is the string identifier for a messaging integration
// (e.g. "slack", "zulip", "telegram"). Stored on the integrations table as a plain
// string so the registry can resolve a provider implementation without coupling
// the data model to a closed enum.
type MessagingProvider string

const (
	MessagingProviderSlack    MessagingProvider = "slack"
	MessagingProviderTelegram MessagingProvider = "telegram"
	MessagingProviderDiscord  MessagingProvider = "discord"
	MessagingProviderZulip    MessagingProvider = "zulip"
)

// ValidMessagingProviders returns all known messaging provider identifiers.
// Telegram, Discord and Zulip are outbound-only (no listening) for now.
func ValidMessagingProviders() []MessagingProvider {
	return []MessagingProvider{
		MessagingProviderSlack,
		MessagingProviderTelegram,
		MessagingProviderDiscord,
		MessagingProviderZulip,
	}
}

// IsValidMessagingProvider reports whether the given string is one of the
// known messaging provider identifiers.
func IsValidMessagingProvider(p string) bool {
	for _, v := range ValidMessagingProviders() {
		if string(v) == p {
			return true
		}
	}
	return false
}

// Integration represents a configured connection to a messaging provider with
// its credentials. Multiple integrations per provider are allowed at the data
// model level; the MVP UI assumes one per provider.
type Integration struct {
	ID          uint              `gorm:"primaryKey" json:"id"`
	UUID        string            `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Provider    MessagingProvider `gorm:"type:varchar(50);not null;index" json:"provider"`
	Name        string            `gorm:"size:128;not null" json:"name"`
	Credentials JSONB             `gorm:"type:jsonb" json:"credentials"`
	Enabled     bool              `gorm:"default:true" json:"enabled"`
	ConnectorID *uint             `gorm:"index" json:"connector_id,omitempty"` // owning Connector; nil until reconciled
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`

	Channels []Channel `gorm:"foreignKey:IntegrationID" json:"channels,omitempty"`
}

func (Integration) TableName() string {
	return "integrations"
}

// Channel is a specific addressable destination within an Integration (a Slack
// channel, Zulip stream, or Telegram chat). Capability flags determine which triggers can
// reference it. IsDefaultPost marks the per-provider workspace default for
// outbound posting; at most one per provider is enforced by a partial-unique
// DB index plus a service-layer check.
//
// OnChange says what a listener message classified as "change" does: "store"
// (record the event only) or "run" (also start an agent run that replies in
// the message thread; never an incident). OnChangeFilter is an optional regex
// the raw message text must match for "run"; OnChangeSkill is an optional
// skill the run is asked to use.
type Channel struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	UUID                 string    `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	IntegrationID        uint      `gorm:"not null;index" json:"integration_id"`
	ExternalID           string    `gorm:"size:128;not null" json:"external_id"`
	DisplayName          string    `gorm:"size:255" json:"display_name"`
	CanPost              bool      `json:"can_post"`
	CanListen            bool      `json:"can_listen"`
	IsDefaultPost        bool      `json:"is_default_post"`
	ExtractionPrompt     string    `gorm:"type:text" json:"extraction_prompt"`
	ProcessBotMessages   bool      `json:"process_bot_messages"`
	ProcessHumanMessages bool      `json:"process_human_messages"`
	OnChange             string    `gorm:"size:16;not null;default:store" json:"on_change"`
	OnChangeFilter       string    `gorm:"type:text" json:"on_change_filter"`
	OnChangeSkill        string    `gorm:"size:255" json:"on_change_skill"`
	Enabled              bool      `gorm:"default:true" json:"enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	Integration Integration `gorm:"foreignKey:IntegrationID" json:"integration,omitempty"`
}

// Channel.OnChange values.
const (
	ChannelOnChangeStore = "store"
	ChannelOnChangeRun   = "run"
)

func (Channel) TableName() string {
	return "channels"
}
