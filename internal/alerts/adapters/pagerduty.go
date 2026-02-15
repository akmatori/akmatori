package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// PagerDutyAdapter handles PagerDuty webhooks
type PagerDutyAdapter struct {
	alerts.BaseAdapter
}

// NewPagerDutyAdapter creates a new PagerDuty adapter
func NewPagerDutyAdapter() *PagerDutyAdapter {
	return &PagerDutyAdapter{
		BaseAdapter: alerts.BaseAdapter{SourceType: "pagerduty"},
	}
}

// PagerDutyPayload represents the webhook payload from PagerDuty
type PagerDutyPayload struct {
	Event struct {
		ID        string `json:"id"`
		EventType string `json:"event_type"` // incident.triggered, incident.resolved, etc.
		Data      struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Status      string `json:"status"`
			Urgency     string `json:"urgency"`
			Priority    struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"priority"`
			Service struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Summary string `json:"summary"`
			} `json:"service"`
			Source string `json:"source"`
			Body   struct {
				Type    string `json:"type"`
				Details struct {
					Runbook string `json:"runbook"`
				} `json:"details"`
			} `json:"body"`
		} `json:"data"`
	} `json:"event"`
}

// ValidateWebhookSecret validates the PagerDuty webhook signature
func (a *PagerDutyAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	// PagerDuty uses HMAC-SHA256 signature
	signature := r.Header.Get("X-PagerDuty-Signature")
	if signature == "" {
		// Also check for custom header
		signature = r.Header.Get("Authorization")
		if signature == instance.WebhookSecret || signature == "Bearer "+instance.WebhookSecret {
			return nil
		}
		return fmt.Errorf("missing webhook signature")
	}

	// For HMAC validation, we'd need the body - simplified check here
	if !strings.HasPrefix(signature, "v1=") {
		return fmt.Errorf("invalid signature format")
	}

	return nil
}

// ParsePayload parses PagerDuty webhook payload into normalized alerts
func (a *PagerDutyAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload PagerDutyPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pagerduty payload: %w", err)
	}

	// Get field mappings
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	n := a.parseAlert(payload, mappings)
	return []alerts.NormalizedAlert{n}, nil
}

func (a *PagerDutyAdapter) parseAlert(payload PagerDutyPayload, mappings database.JSONB) alerts.NormalizedAlert {
	event := payload.Event
	data := event.Data

	// Map event type to status
	status := database.AlertStatusFiring
	if strings.Contains(event.EventType, "resolved") || strings.Contains(event.EventType, "acknowledged") {
		status = database.AlertStatusResolved
	}

	// Map urgency/priority to severity
	severity := a.mapUrgencyToSeverity(data.Urgency, data.Priority.Summary)

	// Build target labels
	targetLabels := map[string]string{
		"service_id":   data.Service.ID,
		"service_name": data.Service.Name,
		"urgency":      data.Urgency,
		"priority_id":  data.Priority.ID,
	}

	// Build raw payload map
	rawPayload := map[string]interface{}{
		"event": map[string]interface{}{
			"id":         event.ID,
			"event_type": event.EventType,
			"data": map[string]interface{}{
				"id":          data.ID,
				"title":       data.Title,
				"description": data.Description,
				"status":      data.Status,
				"urgency":     data.Urgency,
				"service":     data.Service,
				"priority":    data.Priority,
				"source":      data.Source,
			},
		},
	}

	return alerts.NormalizedAlert{
		AlertName:         data.Title,
		Severity:          severity,
		Status:            status,
		Summary:           data.Description,
		Description:       data.Description,
		TargetHost:        data.Source,
		TargetService:     data.Service.Name,
		TargetLabels:      targetLabels,
		RunbookURL:        data.Body.Details.Runbook,
		SourceAlertID:     data.ID,
		SourceFingerprint: data.ID,
		RawPayload:        rawPayload,
	}
}

// mapUrgencyToSeverity maps PagerDuty urgency to normalized severity
func (a *PagerDutyAdapter) mapUrgencyToSeverity(urgency, priority string) database.AlertSeverity {
	// Check priority first
	priority = strings.ToLower(priority)
	if strings.Contains(priority, "p1") || strings.Contains(priority, "critical") {
		return database.AlertSeverityCritical
	}
	if strings.Contains(priority, "p2") || strings.Contains(priority, "high") {
		return database.AlertSeverityHigh
	}

	// Then check urgency
	switch strings.ToLower(urgency) {
	case "high":
		return database.AlertSeverityHigh
	case "low":
		return database.AlertSeverityInfo
	default:
		return database.AlertSeverityWarning
	}
}

// GetDefaultMappings returns the default field mappings for PagerDuty
func (a *PagerDutyAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":      "event.data.title",
		"severity":        "event.data.priority.summary",
		"status":          "event.event_type",
		"summary":         "event.data.description",
		"target_host":     "event.data.source",
		"target_service":  "event.data.service.name",
		"runbook_url":     "event.data.body.details.runbook",
		"source_alert_id": "event.data.id",
	}
}

// NOTE: HMAC signature validation for PagerDuty webhooks can be implemented here
// when needed. See: https://developer.pagerduty.com/docs/webhooks/v3-overview/
