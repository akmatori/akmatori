package database

// Integration mirrors the main API's messaging Integration
// (internal/database/models_channels.go): one configured messaging provider
// with its credentials. Only the columns the gateway reads are mirrored.
type Integration struct {
	ID          uint   `gorm:"primaryKey"`
	Provider    string `gorm:"type:varchar(50);not null;index"`
	Name        string `gorm:"size:128"`
	Credentials JSONB  `gorm:"type:jsonb"`
	Enabled     bool   `gorm:"default:true"`
}

func (Integration) TableName() string {
	return "integrations"
}

// Channel mirrors the main API's messaging Channel: one addressable
// destination (a Slack channel, a Telegram chat) of an Integration.
type Channel struct {
	ID            uint   `gorm:"primaryKey"`
	IntegrationID uint   `gorm:"not null;index"`
	ExternalID    string `gorm:"size:128;not null"`
	DisplayName   string `gorm:"size:255"`
	CanPost       bool
	IsDefaultPost bool
	Enabled       bool `gorm:"default:true"`
}

func (Channel) TableName() string {
	return "channels"
}
