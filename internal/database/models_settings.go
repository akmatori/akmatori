package database

import "time"

// SlackSettings stores Slack integration configuration
type SlackSettings struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	BotToken      string    `gorm:"type:text" json:"bot_token"`
	SigningSecret string    `gorm:"type:text" json:"signing_secret"`
	AppToken      string    `gorm:"type:text" json:"app_token"`
	AlertsChannel string    `gorm:"type:varchar(255)" json:"alerts_channel"`
	Enabled       bool      `gorm:"default:false" json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IsConfigured returns true if all required Slack tokens are set
func (s *SlackSettings) IsConfigured() bool {
	return s.BotToken != "" && s.SigningSecret != "" && s.AppToken != ""
}

// IsActive returns true if Slack is enabled and configured
func (s *SlackSettings) IsActive() bool {
	return s.Enabled && s.IsConfigured()
}

func (SlackSettings) TableName() string {
	return "slack_settings"
}

// LLMProvider represents the LLM provider
type LLMProvider string

const (
	LLMProviderOpenAI     LLMProvider = "openai"
	LLMProviderAnthropic  LLMProvider = "anthropic"
	LLMProviderGoogle     LLMProvider = "google"
	LLMProviderOpenRouter LLMProvider = "openrouter"
	LLMProviderCustom     LLMProvider = "custom"
)

// ValidLLMProviders returns all valid LLM provider values
func ValidLLMProviders() []LLMProvider {
	return []LLMProvider{
		LLMProviderOpenAI,
		LLMProviderAnthropic,
		LLMProviderGoogle,
		LLMProviderOpenRouter,
		LLMProviderCustom,
	}
}

// IsValidLLMProvider checks if a provider string is valid
func IsValidLLMProvider(provider string) bool {
	for _, p := range ValidLLMProviders() {
		if string(p) == provider {
			return true
		}
	}
	return false
}

// ThinkingLevel represents the thinking/reasoning level for the LLM
type ThinkingLevel string

const (
	ThinkingLevelOff     ThinkingLevel = "off"
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

// ValidThinkingLevels returns all valid thinking level values
func ValidThinkingLevels() []ThinkingLevel {
	return []ThinkingLevel{
		ThinkingLevelOff,
		ThinkingLevelMinimal,
		ThinkingLevelLow,
		ThinkingLevelMedium,
		ThinkingLevelHigh,
		ThinkingLevelXHigh,
	}
}

// IsValidThinkingLevel checks if a thinking level string is valid
func IsValidThinkingLevel(level string) bool {
	for _, l := range ValidThinkingLevels() {
		if string(l) == level {
			return true
		}
	}
	return false
}

// LLMSettings stores per-provider LLM configuration.
// Each provider (openai, anthropic, etc.) has its own row with separate API key, model, and settings.
// The Active field indicates which provider is currently selected for use.
type LLMSettings struct {
	ID            uint          `gorm:"primaryKey" json:"id"`
	Provider      LLMProvider   `gorm:"type:varchar(50);uniqueIndex;not null" json:"provider"`
	APIKey        string        `gorm:"type:text" json:"api_key"`
	Model         string        `gorm:"type:varchar(100)" json:"model"`
	ThinkingLevel ThinkingLevel `gorm:"type:varchar(50);default:'medium'" json:"thinking_level"`
	BaseURL       string        `gorm:"type:text" json:"base_url"`
	Enabled       bool          `gorm:"default:false" json:"enabled"`
	Active        bool          `gorm:"default:false" json:"active"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// IsConfigured returns true if the LLM provider has an API key set
func (l *LLMSettings) IsConfigured() bool {
	return l.APIKey != ""
}

// IsActive returns true if the LLM settings are enabled and configured
func (l *LLMSettings) IsActive() bool {
	return l.Enabled && l.IsConfigured()
}

func (LLMSettings) TableName() string {
	return "llm_settings"
}

// ProxySettings stores HTTP proxy configuration with per-service toggles
type ProxySettings struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	ProxyURL               string    `gorm:"type:text" json:"proxy_url"`                    // HTTP/HTTPS proxy URL
	NoProxy                string    `gorm:"type:text" json:"no_proxy"`                     // Comma-separated hosts to bypass proxy
	LLMEnabled             bool      `gorm:"column:llm_enabled;default:true" json:"llm_enabled"` // Use proxy for LLM API calls (all providers)
	SlackEnabled           bool      `gorm:"default:true" json:"slack_enabled"`             // Use proxy for Slack
	ZabbixEnabled          bool      `gorm:"default:false" json:"zabbix_enabled"`           // Use proxy for Zabbix API
	VictoriaMetricsEnabled bool      `gorm:"default:false" json:"victoria_metrics_enabled"` // Use proxy for VictoriaMetrics API
	CatchpointEnabled      bool      `gorm:"default:false" json:"catchpoint_enabled"`       // Use proxy for Catchpoint API
	GrafanaEnabled         bool      `gorm:"default:false" json:"grafana_enabled"`          // Use proxy for Grafana API
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// TableName overrides the table name
func (ProxySettings) TableName() string {
	return "proxy_settings"
}

// IsConfigured returns true if a proxy URL is set
func (p *ProxySettings) IsConfigured() bool {
	return p.ProxyURL != ""
}

// GeneralSettings stores general instance configuration (singleton)
type GeneralSettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BaseURL   string    `gorm:"type:text" json:"base_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (GeneralSettings) TableName() string {
	return "general_settings"
}

// APIKeySettings stores API authentication configuration
type APIKeySettings struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Enabled   bool      `gorm:"default:false" json:"enabled"`
	Keys      JSONB     `gorm:"type:jsonb" json:"keys"` // Array of {key, name, enabled, created_at}
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// APIKeyEntry represents a single API key entry
type APIKeyEntry struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// GetActiveKeys returns all enabled API keys
func (a *APIKeySettings) GetActiveKeys() []string {
	if a.Keys == nil {
		return []string{}
	}

	keysData, ok := a.Keys["keys"].([]interface{})
	if !ok {
		return []string{}
	}

	var activeKeys []string
	for _, k := range keysData {
		keyMap, ok := k.(map[string]interface{})
		if !ok {
			continue
		}
		enabled, _ := keyMap["enabled"].(bool)
		key, _ := keyMap["key"].(string)
		if enabled && key != "" {
			activeKeys = append(activeKeys, key)
		}
	}
	return activeKeys
}

// IsActive returns true if API key authentication is enabled
func (a *APIKeySettings) IsActive() bool {
	return a.Enabled && len(a.GetActiveKeys()) > 0
}

func (APIKeySettings) TableName() string {
	return "api_key_settings"
}

// RetentionSettings stores incident data retention policy configuration (singleton).
// SingletonKey with a unique index ensures only one row can exist at the DB level,
// preventing duplicate rows from concurrent FirstOrCreate calls.
type RetentionSettings struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	SingletonKey         string    `gorm:"uniqueIndex;default:'default';not null" json:"-"`
	Enabled              bool      `gorm:"default:true" json:"enabled"`
	RetentionDays        int       `gorm:"default:90" json:"retention_days"`
	CleanupIntervalHours int       `gorm:"default:6" json:"cleanup_interval_hours"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (RetentionSettings) TableName() string {
	return "retention_settings"
}

// DefaultRetentionSettings returns the default retention settings values.
func DefaultRetentionSettings() *RetentionSettings {
	return &RetentionSettings{
		SingletonKey:         "default",
		Enabled:              true,
		RetentionDays:        90,
		CleanupIntervalHours: 6,
	}
}
