package database

import "time"

// AlertCorrelationLog records each correlation decision made by the AI gate so
// operators can audit why alerts were collapsed into existing incidents.
type AlertCorrelationLog struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	SourceUUID           string    `gorm:"type:varchar(36);index;not null" json:"source_uuid"`
	AlertName            string    `gorm:"type:varchar(255);not null" json:"alert_name"`
	TargetHost           string    `gorm:"type:varchar(255)" json:"target_host"`
	MatchedIncidentUUID  string    `gorm:"type:varchar(36);index;not null" json:"matched_incident_uuid"`
	Confidence           float64   `gorm:"not null" json:"confidence"`
	Reasoning            string    `gorm:"type:text" json:"reasoning"`
	CreatedAt            time.Time `json:"created_at"`
}

func (AlertCorrelationLog) TableName() string {
	return "alert_correlation_logs"
}
