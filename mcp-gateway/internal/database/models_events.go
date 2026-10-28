package database

import "time"

// Event mirrors the main API's Event model (internal/database/models_events.go):
// one inbound item and its class. The gateway reads it for the events tool and
// inserts cron-reported alerts with PendingDispatch=true; the main API's
// dispatcher runs those through the alert correlation gate.
type Event struct {
	ID                       uint      `gorm:"primaryKey" json:"-"`
	UUID                     string    `gorm:"size:36;uniqueIndex;not null" json:"uuid"`
	SourceKind               string    `gorm:"size:32;not null" json:"source_kind"`
	SourceUUID               string    `gorm:"size:64" json:"source_uuid,omitempty"`
	SourceType               string    `gorm:"size:64" json:"source_type,omitempty"`
	ExternalID               string    `gorm:"size:255" json:"external_id,omitempty"`
	Fingerprint              string    `gorm:"size:32" json:"-"`
	ReceivedAt               time.Time `json:"received_at"`
	OccurredAt               time.Time `json:"occurred_at"`
	Kind                     string    `gorm:"size:16;not null" json:"kind"`
	Classification           string    `gorm:"size:16;not null" json:"classification"`
	ClassificationConfidence *float64  `json:"classification_confidence,omitempty"`
	ClassificationReasoning  string    `gorm:"type:text" json:"classification_reasoning,omitempty"`
	Title                    string    `gorm:"size:255" json:"title"`
	Summary                  string    `gorm:"type:text" json:"summary,omitempty"`
	Host                     string    `gorm:"size:255" json:"host,omitempty"`
	Service                  string    `gorm:"size:255" json:"service,omitempty"`
	Severity                 string    `gorm:"size:16" json:"severity,omitempty"`
	Status                   string    `gorm:"size:16" json:"status,omitempty"`
	Labels                   JSONB     `gorm:"type:jsonb" json:"labels,omitempty"`
	RawPayload               JSONB     `gorm:"type:jsonb" json:"raw_payload,omitempty"`
	AlertUUID                string    `gorm:"size:36" json:"alert_uuid,omitempty"`
	IncidentUUID             string    `gorm:"size:36" json:"incident_uuid,omitempty"`
	ScopeUUID                string    `gorm:"size:36" json:"scope_uuid,omitempty"`
	PendingDispatch          bool      `gorm:"not null;default:false" json:"-"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

func (Event) TableName() string {
	return "events"
}
