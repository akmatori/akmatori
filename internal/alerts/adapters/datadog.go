package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// DatadogAdapter handles Datadog webhooks
type DatadogAdapter struct {
	alerts.BaseAdapter
}

// NewDatadogAdapter creates a new Datadog adapter
func NewDatadogAdapter() *DatadogAdapter {
	return &DatadogAdapter{
		BaseAdapter: alerts.BaseAdapter{SourceType: "datadog"},
	}
}

// DatadogPayload represents the webhook payload from Datadog
type DatadogPayload struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	AlertType    string   `json:"alert_type"` // error, warning, info, success
	EventType    string   `json:"event_type"`
	Priority     string   `json:"priority"` // normal, low
	AlertID      string   `json:"alert_id"`
	AlertTitle   string   `json:"alert_title"`
	AlertStatus  string   `json:"alert_status"` // Triggered, Recovered, etc.
	Hostname     string   `json:"hostname"`
	OrgID        string   `json:"org_id"`
	OrgName      string   `json:"org_name"`
	Snapshot     string   `json:"snapshot"`
	Date         int64    `json:"date"`
	Tags         []string `json:"tags"`
	EventLinks   []struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	} `json:"event_links"`
	// Additional fields for monitor alerts
	AlertCycleKey string `json:"alert_cycle_key"`
	AlertMetric   string `json:"alert_metric"`
	AlertQuery    string `json:"alert_query"`
	AlertScope    string `json:"alert_scope"`
	LastUpdated   int64  `json:"last_updated"`
}

// ValidateWebhookSecret validates the Datadog webhook secret
func (a *DatadogAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	// Check custom header or Authorization
	secret := r.Header.Get("X-Datadog-Signature")
	if secret == "" {
		secret = r.Header.Get("DD-API-KEY")
	}
	if secret == "" {
		secret = r.Header.Get("Authorization")
	}

	if secret != instance.WebhookSecret && secret != "Bearer "+instance.WebhookSecret {
		return fmt.Errorf("invalid webhook secret")
	}

	return nil
}

// ParsePayload parses Datadog webhook payload into normalized alerts
func (a *DatadogAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload DatadogPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse datadog payload: %w", err)
	}

	// Get field mappings
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	n := a.parseAlert(payload, mappings)
	return []alerts.NormalizedAlert{n}, nil
}

func (a *DatadogAdapter) parseAlert(payload DatadogPayload, mappings database.JSONB) alerts.NormalizedAlert {
	// Map alert_type to severity
	severity := a.mapAlertTypeToSeverity(payload.AlertType, payload.Priority)

	// Map alert_status to status
	status := a.mapAlertStatusToStatus(payload.AlertStatus)

	// Parse tags into map
	targetLabels := a.parseTags(payload.Tags)

	// Extract host from tags or hostname field
	targetHost := payload.Hostname
	if targetHost == "" {
		if host, ok := targetLabels["host"]; ok {
			targetHost = host
		}
	}

	// Extract service from tags
	targetService := ""
	if service, ok := targetLabels["service"]; ok {
		targetService = service
	}

	// Get runbook URL from event_links
	var runbookURL string
	for _, link := range payload.EventLinks {
		if strings.Contains(strings.ToLower(link.Name), "runbook") {
			runbookURL = link.URL
			break
		}
	}
	// If no explicit runbook, use first link
	if runbookURL == "" && len(payload.EventLinks) > 0 {
		runbookURL = payload.EventLinks[0].URL
	}

	// Use alert_title or title
	alertName := payload.AlertTitle
	if alertName == "" {
		alertName = payload.Title
	}

	// Build raw payload
	rawPayload := map[string]interface{}{
		"id":           payload.ID,
		"title":        payload.Title,
		"body":         payload.Body,
		"alert_type":   payload.AlertType,
		"event_type":   payload.EventType,
		"priority":     payload.Priority,
		"alert_id":     payload.AlertID,
		"alert_title":  payload.AlertTitle,
		"alert_status": payload.AlertStatus,
		"hostname":     payload.Hostname,
		"org_id":       payload.OrgID,
		"org_name":     payload.OrgName,
		"date":         payload.Date,
		"tags":         payload.Tags,
		"event_links":  payload.EventLinks,
		"alert_metric": payload.AlertMetric,
		"alert_query":  payload.AlertQuery,
		"alert_scope":  payload.AlertScope,
	}

	// Use alert_id or id as source ID
	sourceID := payload.AlertID
	if sourceID == "" {
		sourceID = payload.ID
	}

	return alerts.NormalizedAlert{
		AlertName:         alertName,
		Severity:          severity,
		Status:            status,
		Summary:           payload.Body,
		Description:       payload.Body,
		TargetHost:        targetHost,
		TargetService:     targetService,
		TargetLabels:      targetLabels,
		MetricName:        payload.AlertMetric,
		RunbookURL:        runbookURL,
		SourceAlertID:     sourceID,
		SourceFingerprint: payload.AlertCycleKey,
		RawPayload:        rawPayload,
	}
}

// mapAlertTypeToSeverity maps Datadog alert_type to normalized severity
func (a *DatadogAdapter) mapAlertTypeToSeverity(alertType, priority string) database.AlertSeverity {
	switch strings.ToLower(alertType) {
	case "error":
		return database.AlertSeverityCritical
	case "warning":
		return database.AlertSeverityWarning
	case "info":
		return database.AlertSeverityInfo
	case "success":
		return database.AlertSeverityInfo
	}

	// Fall back to priority
	switch strings.ToLower(priority) {
	case "normal":
		return database.AlertSeverityWarning
	case "low":
		return database.AlertSeverityInfo
	}

	return database.AlertSeverityWarning
}

// mapAlertStatusToStatus maps Datadog alert_status to normalized status
func (a *DatadogAdapter) mapAlertStatusToStatus(alertStatus string) database.AlertStatus {
	status := strings.ToLower(alertStatus)
	switch {
	case strings.Contains(status, "recovered") || strings.Contains(status, "resolved") || strings.Contains(status, "ok"):
		return database.AlertStatusResolved
	default:
		return database.AlertStatusFiring
	}
}

// parseTags parses Datadog tags array into a map
func (a *DatadogAdapter) parseTags(tags []string) map[string]string {
	result := make(map[string]string)
	for _, tag := range tags {
		parts := strings.SplitN(tag, ":", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		} else {
			result[tag] = "true"
		}
	}
	return result
}

// GetDefaultMappings returns the default field mappings for Datadog
func (a *DatadogAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":      "title",
		"severity":        "priority",
		"status":          "alert_type",
		"summary":         "body",
		"target_host":     "tags.host",
		"runbook_url":     "event_links.0.url",
		"source_alert_id": "id",
	}
}
