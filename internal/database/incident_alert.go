package database

import "time"

// IncidentAlert tracks alerts that have been aggregated into an incident
type IncidentAlert struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	IncidentID            uint       `gorm:"not null;index" json:"incident_id"`
	SourceType            string     `gorm:"type:varchar(50);not null" json:"source_type"`
	SourceFingerprint     string     `gorm:"type:varchar(255);not null;index" json:"source_fingerprint"`
	AlertName             string     `gorm:"type:varchar(255);not null" json:"alert_name"`
	Severity              string     `gorm:"type:varchar(20)" json:"severity"`
	TargetHost            string     `gorm:"type:varchar(255)" json:"target_host"`
	TargetService         string     `gorm:"type:varchar(255)" json:"target_service"`
	Summary               string     `gorm:"type:text" json:"summary"`
	Description           string     `gorm:"type:text" json:"description"`
	TargetLabels          JSONB      `gorm:"type:jsonb" json:"target_labels"`
	Status                string     `gorm:"type:varchar(20);not null" json:"status"` // firing, resolved
	AlertPayload          JSONB      `gorm:"type:jsonb" json:"alert_payload"`
	CorrelationConfidence float64    `gorm:"type:decimal(3,2)" json:"correlation_confidence"`
	CorrelationReason     string     `gorm:"type:text" json:"correlation_reason"`
	AttachedAt            time.Time  `gorm:"not null" json:"attached_at"`
	ResolvedAt            *time.Time `json:"resolved_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`

	// Belongs to Incident
	Incident Incident `gorm:"foreignKey:IncidentID" json:"-"`
}

func (IncidentAlert) TableName() string {
	return "incident_alerts"
}
