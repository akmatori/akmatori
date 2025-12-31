package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// AlertmanagerAdapter handles Prometheus Alertmanager webhooks
type AlertmanagerAdapter struct {
	alerts.BaseAdapter
}

// NewAlertmanagerAdapter creates a new Alertmanager adapter
func NewAlertmanagerAdapter() *AlertmanagerAdapter {
	return &AlertmanagerAdapter{
		BaseAdapter: alerts.BaseAdapter{SourceType: "alertmanager"},
	}
}

// AlertmanagerPayload represents the webhook payload from Alertmanager
type AlertmanagerPayload struct {
	Alerts      []AlertmanagerAlert `json:"alerts"`
	Status      string              `json:"status"`
	GroupLabels map[string]string   `json:"groupLabels"`
	CommonLabels map[string]string  `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL string              `json:"externalURL"`
	Version     string              `json:"version"`
	GroupKey    string              `json:"groupKey"`
}

// AlertmanagerAlert represents a single alert in the payload
type AlertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ValidateWebhookSecret validates the webhook secret header
func (a *AlertmanagerAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	// Check custom header first
	secret := r.Header.Get("X-Alertmanager-Secret")
	if secret == "" {
		// Also check Authorization header for basic auth style
		secret = r.Header.Get("Authorization")
	}

	if secret != instance.WebhookSecret && secret != "Bearer "+instance.WebhookSecret {
		return fmt.Errorf("invalid webhook secret")
	}

	return nil
}

// ParsePayload parses Alertmanager webhook payload into normalized alerts
func (a *AlertmanagerAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload AlertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse alertmanager payload: %w", err)
	}

	// Get field mappings (use instance override or defaults)
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	var normalized []alerts.NormalizedAlert
	for _, alert := range payload.Alerts {
		n := a.parseAlert(alert, mappings)
		normalized = append(normalized, n)
	}

	return normalized, nil
}

func (a *AlertmanagerAdapter) parseAlert(alert AlertmanagerAlert, mappings database.JSONB) alerts.NormalizedAlert {
	// Convert alert to map for field extraction
	alertMap := map[string]interface{}{
		"status":       alert.Status,
		"labels":       alert.Labels,
		"annotations":  alert.Annotations,
		"startsAt":     alert.StartsAt.Format(time.RFC3339),
		"endsAt":       alert.EndsAt.Format(time.RFC3339),
		"generatorURL": alert.GeneratorURL,
		"fingerprint":  alert.Fingerprint,
	}

	// Extract fields using mappings
	alertName := alerts.ExtractString(alertMap, getMapping(mappings, "alert_name"))
	if alertName == "" {
		alertName = alert.Labels["alertname"]
	}

	severity := alerts.ExtractString(alertMap, getMapping(mappings, "severity"))
	if severity == "" {
		severity = alert.Labels["severity"]
	}

	summary := alerts.ExtractString(alertMap, getMapping(mappings, "summary"))
	if summary == "" {
		summary = alert.Annotations["summary"]
	}

	description := alerts.ExtractString(alertMap, getMapping(mappings, "description"))
	if description == "" {
		description = alert.Annotations["description"]
	}

	targetHost := alerts.ExtractString(alertMap, getMapping(mappings, "target_host"))
	if targetHost == "" {
		targetHost = alert.Labels["instance"]
	}

	targetService := alerts.ExtractString(alertMap, getMapping(mappings, "target_service"))
	if targetService == "" {
		targetService = alert.Labels["job"]
	}

	runbookURL := alerts.ExtractString(alertMap, getMapping(mappings, "runbook_url"))
	if runbookURL == "" {
		runbookURL = alert.Annotations["runbook_url"]
	}

	// Parse times
	var startedAt, endedAt *time.Time
	if !alert.StartsAt.IsZero() {
		startedAt = &alert.StartsAt
	}
	if !alert.EndsAt.IsZero() && alert.Status == "resolved" {
		endedAt = &alert.EndsAt
	}

	return alerts.NormalizedAlert{
		AlertName:         alertName,
		Severity:          alerts.NormalizeSeverity(severity, alerts.DefaultSeverityMapping),
		Status:            alerts.NormalizeStatus(alert.Status),
		Summary:           summary,
		Description:       description,
		TargetHost:        targetHost,
		TargetService:     targetService,
		TargetLabels:      alert.Labels,
		RunbookURL:        runbookURL,
		StartedAt:         startedAt,
		EndedAt:           endedAt,
		SourceAlertID:     alert.Fingerprint,
		SourceFingerprint: alert.Fingerprint,
		RawPayload:        alertMap,
	}
}

// GetDefaultMappings returns the default field mappings for Alertmanager
func (a *AlertmanagerAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":         "labels.alertname",
		"severity":           "labels.severity",
		"status":             "status",
		"summary":            "annotations.summary",
		"description":        "annotations.description",
		"target_host":        "labels.instance",
		"target_service":     "labels.job",
		"runbook_url":        "annotations.runbook_url",
		"source_fingerprint": "fingerprint",
		"started_at":         "startsAt",
		"ended_at":           "endsAt",
	}
}

// getMapping safely extracts a mapping value
func getMapping(mappings database.JSONB, key string) string {
	if v, ok := mappings[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
