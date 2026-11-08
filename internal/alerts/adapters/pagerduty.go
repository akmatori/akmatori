package adapters

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// maxSignedBodySize limits how much of the body is read to check the
// signature. It matches the webhook handler's own body limit.
const maxSignedBodySize = 10 * 1024 * 1024

// ValidateWebhookSecret checks a PagerDuty webhook against the instance secret.
//
// It accepts either a v3 webhook signature (X-PagerDuty-Signature: v1=<hex
// HMAC-SHA256 of the raw body, keyed with the secret>; PagerDuty may send
// several comma-separated signatures while a secret rotates) or the secret
// itself in the Authorization header (plain or Bearer), which PagerDuty can
// send as a custom header. The body is read to compute the HMAC and then
// restored for the handler.
func (a *PagerDutyAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	if auth := r.Header.Get("Authorization"); auth != "" {
		if secretEqual(auth, instance.WebhookSecret) || secretEqual(auth, "Bearer "+instance.WebhookSecret) {
			return nil
		}
	}

	header := r.Header.Get("X-PagerDuty-Signature")
	if header == "" {
		return fmt.Errorf("missing webhook signature")
	}

	body, err := readAndRestoreBody(r)
	if err != nil {
		return fmt.Errorf("read body for signature check: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(instance.WebhookSecret))
	mac.Write(body)
	expected := mac.Sum(nil)

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		hexSig, ok := strings.CutPrefix(part, "v1=")
		if !ok {
			continue
		}
		got, err := hex.DecodeString(hexSig)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			return nil
		}
	}
	return fmt.Errorf("invalid webhook signature")
}

// readAndRestoreBody reads up to maxSignedBodySize bytes of the body and puts
// them back in front of any unread rest, so the handler reads the same bytes.
func readAndRestoreBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSignedBodySize))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
	return body, err
}

func secretEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ParsePayload parses PagerDuty webhook payload into normalized alerts
func (a *PagerDutyAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload PagerDutyPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse pagerduty payload: %w", err)
	}

	// Get field mappings
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	status, ok := pagerDutyEventStatus(payload.Event.EventType)
	if !ok {
		// Not an incident lifecycle change (acknowledged, annotated,
		// reassigned, escalated, ping, ...): nothing to investigate or close.
		return []alerts.NormalizedAlert{}, nil
	}

	n := a.parseAlert(payload, mappings)
	n.Status = status
	return []alerts.NormalizedAlert{n}, nil
}

// pagerDutyEventStatus maps a v3 webhook event type to an alert status.
// Only triggered, reopened and resolved change whether the incident is open.
// incident.acknowledged means a person took the page, not that the problem
// is gone, so it must not resolve the alert.
func pagerDutyEventStatus(eventType string) (database.AlertStatus, bool) {
	switch eventType {
	case "incident.triggered", "incident.reopened":
		return database.AlertStatusFiring, true
	case "incident.resolved":
		return database.AlertStatusResolved, true
	default:
		return "", false
	}
}

func (a *PagerDutyAdapter) parseAlert(payload PagerDutyPayload, mappings database.JSONB) alerts.NormalizedAlert {
	event := payload.Event
	data := event.Data

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
