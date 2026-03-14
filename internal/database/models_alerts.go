package database

import "time"

// ========== Alert Source Models ==========

// AlertSourceType represents a type of alert source (e.g., Alertmanager, PagerDuty)
type AlertSourceType struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	Name                 string    `gorm:"uniqueIndex;size:64;not null" json:"name"`         // snake_case: "alertmanager", "pagerduty"
	DisplayName          string    `gorm:"size:128;not null" json:"display_name"`            // Human-friendly: "Prometheus Alertmanager"
	Description          string    `gorm:"type:text" json:"description"`
	DefaultFieldMappings JSONB     `gorm:"type:jsonb" json:"default_field_mappings"`         // Default field mappings for this source
	WebhookSecretHeader  string    `gorm:"size:128" json:"webhook_secret_header"`            // e.g., "X-Alertmanager-Secret"
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	// Relationships
	Instances []AlertSourceInstance `gorm:"foreignKey:AlertSourceTypeID" json:"instances,omitempty"`
}

func (AlertSourceType) TableName() string {
	return "alert_source_types"
}

// AlertSourceInstance represents a configured instance of an alert source
type AlertSourceInstance struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	UUID              string    `gorm:"uniqueIndex;size:36;not null" json:"uuid"`           // UUID for webhook URL
	AlertSourceTypeID uint      `gorm:"not null;index" json:"alert_source_type_id"`
	Name              string    `gorm:"uniqueIndex;size:128;not null" json:"name"`          // User-friendly name
	Description       string    `gorm:"type:text" json:"description"`
	WebhookSecret     string    `gorm:"type:text" json:"webhook_secret"`                    // Instance-specific secret
	FieldMappings     JSONB     `gorm:"type:jsonb" json:"field_mappings"`                   // Override default mappings
	Settings          JSONB     `gorm:"type:jsonb" json:"settings"`                         // Additional instance settings
	Enabled           bool      `gorm:"default:true" json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	// Relationships
	AlertSourceType AlertSourceType `gorm:"foreignKey:AlertSourceTypeID" json:"alert_source_type,omitempty"`
}

func (AlertSourceInstance) TableName() string {
	return "alert_source_instances"
}

// GetWebhookURL returns the webhook URL for this instance
func (a *AlertSourceInstance) GetWebhookURL(baseURL string) string {
	return baseURL + "/webhook/alert/" + a.UUID
}

// AlertSeverity represents normalized severity levels (used in incident context)
type AlertSeverity string

const (
	AlertSeverityCritical AlertSeverity = "critical"
	AlertSeverityHigh     AlertSeverity = "high"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityInfo     AlertSeverity = "info"
)

// AlertStatus represents normalized alert status
type AlertStatus string

const (
	AlertStatusFiring   AlertStatus = "firing"
	AlertStatusResolved AlertStatus = "resolved"
)

// GetSeverityEmoji returns an emoji for the alert severity
func GetSeverityEmoji(severity AlertSeverity) string {
	switch severity {
	case AlertSeverityCritical:
		return ":red_circle:"
	case AlertSeverityHigh:
		return ":large_orange_circle:"
	case AlertSeverityWarning:
		return ":large_yellow_circle:"
	case AlertSeverityInfo:
		return ":large_blue_circle:"
	default:
		return ":white_circle:"
	}
}
