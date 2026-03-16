package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// GrafanaAdapter handles Grafana alerting webhooks
type GrafanaAdapter struct {
	alerts.BaseAdapter
}

// NewGrafanaAdapter creates a new Grafana adapter
func NewGrafanaAdapter() *GrafanaAdapter {
	return &GrafanaAdapter{
		BaseAdapter: alerts.BaseAdapter{SourceType: "grafana"},
	}
}

// GrafanaPayload represents the webhook payload from Grafana unified alerting
type GrafanaPayload struct {
	Receiver string         `json:"receiver"`
	Status   string         `json:"status"`
	Alerts   []GrafanaAlert `json:"alerts"`
}

// GrafanaAlert represents a single alert in unified alerting
type GrafanaAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	Fingerprint  string            `json:"fingerprint"`
	GeneratorURL string            `json:"generatorURL"`
}

// ValidateWebhookSecret validates the Grafana webhook secret header
func (a *GrafanaAdapter) ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error {
	if instance.WebhookSecret == "" {
		return nil // No secret configured, allow request
	}

	// Check custom header
	secret := r.Header.Get("X-Grafana-Secret")
	if secret == "" {
		secret = r.Header.Get("Authorization")
	}

	if secret != instance.WebhookSecret && secret != "Bearer "+instance.WebhookSecret {
		return fmt.Errorf("invalid webhook secret")
	}

	return nil
}

// ParsePayload parses Grafana webhook payload into normalized alerts
func (a *GrafanaAdapter) ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]alerts.NormalizedAlert, error) {
	var payload GrafanaPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse grafana payload: %w", err)
	}

	var normalized []alerts.NormalizedAlert
	for _, alert := range payload.Alerts {
		n := a.parseUnifiedAlert(alert)
		normalized = append(normalized, n)
	}

	return normalized, nil
}

func (a *GrafanaAdapter) parseUnifiedAlert(alert GrafanaAlert) alerts.NormalizedAlert {
	// Map status
	status := database.AlertStatusFiring
	if strings.ToLower(alert.Status) == "resolved" {
		status = database.AlertStatusResolved
	}

	// Get alert name from labels
	alertName := alert.Labels["alertname"]
	if alertName == "" {
		alertName = "Grafana Alert"
	}

	// Get severity from labels
	severity := alerts.NormalizeSeverity(alert.Labels["severity"], alerts.DefaultSeverityMapping)

	// Build raw payload
	rawPayload := map[string]interface{}{
		"status":       alert.Status,
		"labels":       alert.Labels,
		"annotations":  alert.Annotations,
		"startsAt":     alert.StartsAt,
		"endsAt":       alert.EndsAt,
		"fingerprint":  alert.Fingerprint,
		"generatorURL": alert.GeneratorURL,
	}

	return alerts.NormalizedAlert{
		AlertName:         alertName,
		Severity:          severity,
		Status:            status,
		Summary:           alert.Annotations["summary"],
		Description:       alert.Annotations["description"],
		TargetHost:        alert.Labels["instance"],
		TargetService:     alert.Labels["job"],
		TargetLabels:      alert.Labels,
		RunbookURL:        alert.Annotations["runbook_url"],
		SourceAlertID:     alert.Fingerprint,
		SourceFingerprint: alert.Fingerprint,
		RawPayload:        rawPayload,
	}
}

// GetDefaultMappings returns the default field mappings for Grafana unified alerting
func (a *GrafanaAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":      "labels.alertname",
		"severity":        "labels.severity",
		"status":          "status",
		"summary":         "annotations.summary",
		"target_host":     "labels.instance",
		"runbook_url":     "annotations.runbook_url",
		"source_alert_id": "fingerprint",
	}
}
