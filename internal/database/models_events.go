package database

import "time"

// EventKind is the class an inbound event gets from classification. It decides
// what Akmatori does with the event.
const (
	// EventKindAlert reports a problem: it creates or updates an alerts row and
	// goes through the correlation gate, which links it to an incident or
	// opens one. Only alert events can open incidents.
	EventKindAlert = "alert"
	// EventKindChange reports a change (deploy, config change, maintenance).
	// It is stored so the agent can ask "what changed before this alert?".
	EventKindChange = "change"
	// EventKindInfo needs no action. It is stored only.
	EventKindInfo = "info"
	// EventKindRequest asks the agent to do work but reports no problem
	// (a question, a cron tick, a proposal chat message). It starts an agent
	// run, never an incident.
	EventKindRequest = "request"
)

// EventClassification records how an event got its Kind.
const (
	// EventClassificationLLM — a one-shot LLM call set the kind.
	EventClassificationLLM = "llm"
	// EventClassificationFallback — the LLM was unavailable, failed, or was
	// not confident, so the event is an alert (a dropped real alert costs more
	// than one extra investigation).
	EventClassificationFallback = "fallback"
	// EventClassificationSource — the source alone decides the kind (cron
	// ticks, proposal chat, resolved notifications, follow-ups in an existing
	// thread). No LLM call.
	EventClassificationSource = "source"
	// EventClassificationBackfill — the row was created by the upgrade
	// migration from an existing alert or agent run.
	EventClassificationBackfill = "backfill"
)

// EventSourceKind says which inbound path produced an event.
const (
	EventSourceWebhook       = "webhook"
	EventSourceSlackListener = "slack_listener"
	EventSourceSlackMention  = "slack_mention"
	EventSourceManual        = "manual"
	EventSourceCron          = "cron"
	EventSourceProposal      = "proposal"
)

// Event is one inbound item as Akmatori received it: a webhook alert item, a
// listener-channel message, a Slack mention, a manual request, a cron tick or
// a proposal chat message. Every inbound item writes exactly one row, then
// classification sets Kind. Only Kind=alert continues to the alerts table and
// the correlation gate.
type Event struct {
	ID   uint   `gorm:"primaryKey" json:"-"`
	UUID string `gorm:"size:36;uniqueIndex;not null" json:"uuid"`

	SourceKind string `gorm:"size:32;index;not null" json:"source_kind"`
	// SourceUUID is the alert source instance, channel, cron job or proposal
	// that produced the event. Slack mentions outside a configured channel
	// use "slack:<channel id>".
	SourceUUID string `gorm:"size:64;index:idx_events_source_fp,priority:1" json:"source_uuid,omitempty"`
	// SourceType is the upstream system: "alertmanager", "zabbix", "slack", ...
	SourceType string `gorm:"size:64" json:"source_type,omitempty"`
	// ExternalID is the source's own id for the item (Alertmanager
	// fingerprint, Slack message ts, ...).
	ExternalID string `gorm:"size:255" json:"external_id,omitempty"`
	// Fingerprint is ComputeAlertFingerprint(source, title, host): the
	// identity used to reuse an earlier classification for a re-send.
	Fingerprint string `gorm:"size:32;index:idx_events_source_fp,priority:2" json:"-"`

	ReceivedAt time.Time `gorm:"index:idx_events_source_fp,priority:3;index" json:"received_at"`
	OccurredAt time.Time `gorm:"index:idx_events_kind_occurred,priority:2" json:"occurred_at"`
	// RepeatCount and LastSeenAt record re-sends of the same item (same
	// source, identity and status within 24 h). A re-send updates this row
	// and keeps its class instead of writing a new row and paying for a new
	// LLM call.
	RepeatCount int        `gorm:"not null;default:0" json:"repeat_count"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`

	Kind                     string   `gorm:"size:16;not null;index:idx_events_kind_occurred,priority:1" json:"kind"`
	Classification           string   `gorm:"size:16;not null" json:"classification"`
	ClassificationConfidence *float64 `json:"classification_confidence,omitempty"`
	ClassificationReasoning  string   `gorm:"type:text" json:"classification_reasoning,omitempty"`

	Title    string `gorm:"size:255" json:"title"`
	Summary  string `gorm:"type:text" json:"summary,omitempty"`
	Host     string `gorm:"size:255;index" json:"host,omitempty"`
	Service  string `gorm:"size:255" json:"service,omitempty"`
	Severity string `gorm:"size:16" json:"severity,omitempty"`
	// Status is "firing" or "resolved" for alert-shaped items; empty otherwise.
	Status     string `gorm:"size:16" json:"status,omitempty"`
	Labels     JSONB  `gorm:"type:jsonb" json:"labels,omitempty"`
	RawPayload JSONB  `gorm:"type:jsonb" json:"-"`

	// AlertUUID is set when the event created or updated an alerts row.
	AlertUUID string `gorm:"size:36;index" json:"alert_uuid,omitempty"`
	// IncidentUUID is the incident row the event opened, joined or continued.
	// Request events point at the row that holds their agent run.
	IncidentUUID string `gorm:"size:36;index" json:"incident_uuid,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Event) TableName() string {
	return "events"
}
