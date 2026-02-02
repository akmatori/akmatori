package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// ZabbixAdapter handles Zabbix webhooks
type ZabbixAdapter struct {
	alerts.BaseAdapter
}

// NewZabbixAdapter creates a new Zabbix adapter
func NewZabbixAdapter() *ZabbixAdapter {
	return &ZabbixAdapter{
		BaseAdapter: alerts.BaseAdapter{SourceType: "zabbix"},
	}
}

// ZabbixPayload represents the webhook payload from Zabbix
type ZabbixPayload struct {
	EventTime         string `json:"event_time"`
	AlertName         string `json:"alert_name"`
	Severity          string `json:"severity"`
	Priority          string `json:"priority"`
	MetricName        string `json:"metric_name"`
	MetricValue       string `json:"metric_value"`
	TriggerExpression string `json:"trigger_expression"`
	PendingDuration   string `json:"pending_duration"`
	EventID           string `json:"event_id"`
	Hardware          string `json:"hardware"`
	EventStatus       string `json:"event_status"`
	RunbookURL        string `json:"runbook_url"`
}

// ValidateWebhookSecret validates the Zabbix webhook secret header
func (a *ZabbixAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	secret := r.Header.Get("X-Zabbix-Secret")
	if secret != instance.WebhookSecret {
		return fmt.Errorf("invalid webhook secret")
	}

	return nil
}

// ParsePayload parses Zabbix webhook payload into normalized alerts
func (a *ZabbixAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload ZabbixPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse zabbix payload: %w", err)
	}

	// Capture all raw fields including ones not in ZabbixPayload struct
	var rawFields map[string]interface{}
	if err := json.Unmarshal(body, &rawFields); err != nil {
		rawFields = nil
	}

	// Get field mappings (use instance override or defaults)
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	n := a.parseAlert(payload, rawFields, mappings)
	return []alerts.NormalizedAlert{n}, nil
}

func (a *ZabbixAdapter) parseAlert(payload ZabbixPayload, rawFields map[string]interface{}, mappings database.JSONB) alerts.NormalizedAlert {
	// Start with all raw fields from the original webhook payload
	// This preserves any extra fields not defined in ZabbixPayload struct
	payloadMap := make(map[string]interface{})
	for k, v := range rawFields {
		payloadMap[k] = v
	}
	// Overlay known struct fields to ensure consistent types
	payloadMap["event_time"] = payload.EventTime
	payloadMap["alert_name"] = payload.AlertName
	payloadMap["severity"] = payload.Severity
	payloadMap["priority"] = payload.Priority
	payloadMap["metric_name"] = payload.MetricName
	payloadMap["metric_value"] = payload.MetricValue
	payloadMap["trigger_expression"] = payload.TriggerExpression
	payloadMap["pending_duration"] = payload.PendingDuration
	payloadMap["event_id"] = payload.EventID
	payloadMap["hardware"] = payload.Hardware
	payloadMap["event_status"] = payload.EventStatus
	payloadMap["runbook_url"] = payload.RunbookURL

	// Map Zabbix priority to severity
	severity := a.mapPriorityToSeverity(payload.Priority)

	// Determine status
	status := database.AlertStatusFiring
	if payload.EventStatus == "RESOLVED" || payload.EventStatus == "OK" {
		status = database.AlertStatusResolved
	}

	// Parse event time
	var startedAt *time.Time
	if payload.EventTime != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", payload.EventTime); err == nil {
			startedAt = &t
		} else if t, err := time.Parse(time.RFC3339, payload.EventTime); err == nil {
			startedAt = &t
		}
	}

	// Build target labels
	targetLabels := map[string]string{
		"hardware":           payload.Hardware,
		"trigger_expression": payload.TriggerExpression,
		"pending_duration":   payload.PendingDuration,
	}

	// Extract Slack thread metadata from event_tags if present
	var slackChannelID, slackThreadTS string
	if eventTags, ok := rawFields["event_tags"].(string); ok {
		slackChannelID, slackThreadTS = extractSlackThreadFromTags(eventTags)
	}

	return alerts.NormalizedAlert{
		AlertName:         payload.AlertName,
		Severity:          severity,
		Status:            status,
		Summary:           payload.TriggerExpression,
		Description:       fmt.Sprintf("Metric: %s = %s\nTrigger: %s", payload.MetricName, payload.MetricValue, payload.TriggerExpression),
		TargetHost:        payload.Hardware,
		TargetService:     "",
		TargetLabels:      targetLabels,
		MetricName:        payload.MetricName,
		MetricValue:       payload.MetricValue,
		RunbookURL:        payload.RunbookURL,
		StartedAt:         startedAt,
		SourceAlertID:     payload.EventID,
		SourceFingerprint: payload.EventID,
		RawPayload:        payloadMap,
		SlackChannelID:    slackChannelID,
		SlackThreadTS:     slackThreadTS,
	}
}

// mapPriorityToSeverity maps Zabbix priority (1-5) to normalized severity
func (a *ZabbixAdapter) mapPriorityToSeverity(priority string) database.AlertSeverity {
	switch priority {
	case "5": // Disaster
		return database.AlertSeverityCritical
	case "4": // High
		return database.AlertSeverityHigh
	case "3": // Average
		return database.AlertSeverityWarning
	case "2", "1": // Warning, Information
		return database.AlertSeverityInfo
	default:
		return database.AlertSeverityWarning
	}
}

// GetDefaultMappings returns the default field mappings for Zabbix
func (a *ZabbixAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":      "alert_name",
		"severity":        "priority",
		"status":          "event_status",
		"summary":         "trigger_expression",
		"target_host":     "hardware",
		"metric_name":     "metric_name",
		"metric_value":    "metric_value",
		"runbook_url":     "runbook_url",
		"source_alert_id": "event_id",
		"started_at":      "event_time",
	}
}

// slackTagEntry represents a single tag entry from Zabbix event_tags
type slackTagEntry struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// extractSlackThreadFromTags parses Zabbix event_tags JSON array and extracts
// Slack thread metadata if present. Returns first complete match found.
// Tags format: [{"tag":"__message_ts_#channel","value":"timestamp"}, {"tag":"__channel_id_#channel","value":"C123..."}]
func extractSlackThreadFromTags(eventTags string) (channelID, threadTS string) {
	if eventTags == "" {
		return "", ""
	}

	var tags []slackTagEntry
	if err := json.Unmarshal([]byte(eventTags), &tags); err != nil {
		return "", ""
	}

	// Build maps of channel -> threadTS and channel -> channelID
	threadTSByChannel := make(map[string]string)
	channelIDByChannel := make(map[string]string)

	const (
		messageTSPrefix = "__message_ts_"
		channelIDPrefix = "__channel_id_"
	)

	for _, tag := range tags {
		if strings.HasPrefix(tag.Tag, messageTSPrefix) {
			channel := strings.TrimPrefix(tag.Tag, messageTSPrefix)
			threadTSByChannel[channel] = tag.Value
		} else if strings.HasPrefix(tag.Tag, channelIDPrefix) {
			channel := strings.TrimPrefix(tag.Tag, channelIDPrefix)
			channelIDByChannel[channel] = tag.Value
		}
	}

	// Find first channel that has both threadTS and channelID
	for channel, ts := range threadTSByChannel {
		if id, ok := channelIDByChannel[channel]; ok {
			return id, ts
		}
	}

	return "", ""
}
