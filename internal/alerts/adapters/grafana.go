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

// GrafanaPayload represents the webhook payload from Grafana
// Supports both legacy alerting and Grafana Alerting (unified alerting)
type GrafanaPayload struct {
	// Unified Alerting format
	Receiver string         `json:"receiver"`
	Status   string         `json:"status"`
	Alerts   []GrafanaAlert `json:"alerts"`

	// Legacy alerting format
	RuleName  string `json:"ruleName"`
	State     string `json:"state"`
	Message   string `json:"message"`
	RuleURL   string `json:"ruleUrl"`
	RuleID    int    `json:"ruleId"`
	Title     string `json:"title"`
	OrgID     int    `json:"orgId"`
	DashboardID int  `json:"dashboardId"`
	PanelID   int    `json:"panelId"`
	EvalMatches []struct {
		Value  float64           `json:"value"`
		Metric string            `json:"metric"`
		Tags   map[string]string `json:"tags"`
	} `json:"evalMatches"`
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

	// Get field mappings
	mappings := alerts.MergeMappings(a.GetDefaultMappings(), instance.FieldMappings)

	var normalized []alerts.NormalizedAlert

	// Check if this is unified alerting (has alerts array) or legacy
	if len(payload.Alerts) > 0 {
		// Unified Alerting format
		for _, alert := range payload.Alerts {
			n := a.parseUnifiedAlert(alert, mappings)
			normalized = append(normalized, n)
		}
	} else {
		// Legacy alerting format
		n := a.parseLegacyAlert(payload, mappings)
		normalized = append(normalized, n)
	}

	return normalized, nil
}

func (a *GrafanaAdapter) parseUnifiedAlert(alert GrafanaAlert, mappings database.JSONB) alerts.NormalizedAlert {
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

func (a *GrafanaAdapter) parseLegacyAlert(payload GrafanaPayload, mappings database.JSONB) alerts.NormalizedAlert {
	// Map state to status
	status := database.AlertStatusFiring
	state := strings.ToLower(payload.State)
	if state == "ok" || state == "no_data" || state == "paused" {
		status = database.AlertStatusResolved
	}

	// Map state to severity
	severity := a.mapStateToSeverity(payload.State)

	// Extract target host from evalMatches
	var targetHost string
	var metricValue string
	targetLabels := make(map[string]string)

	if len(payload.EvalMatches) > 0 {
		match := payload.EvalMatches[0]
		metricValue = fmt.Sprintf("%v", match.Value)
		if instance, ok := match.Tags["instance"]; ok {
			targetHost = instance
		}
		for k, v := range match.Tags {
			targetLabels[k] = v
		}
	}

	// Build raw payload
	rawPayload := map[string]interface{}{
		"ruleName":    payload.RuleName,
		"state":       payload.State,
		"message":     payload.Message,
		"ruleUrl":     payload.RuleURL,
		"ruleId":      payload.RuleID,
		"title":       payload.Title,
		"orgId":       payload.OrgID,
		"dashboardId": payload.DashboardID,
		"panelId":     payload.PanelID,
		"evalMatches": payload.EvalMatches,
	}

	alertName := payload.RuleName
	if alertName == "" {
		alertName = payload.Title
	}

	return alerts.NormalizedAlert{
		AlertName:         alertName,
		Severity:          severity,
		Status:            status,
		Summary:           payload.Message,
		Description:       payload.Message,
		TargetHost:        targetHost,
		TargetService:     "",
		TargetLabels:      targetLabels,
		MetricValue:       metricValue,
		RunbookURL:        payload.RuleURL,
		SourceAlertID:     fmt.Sprintf("%d", payload.RuleID),
		SourceFingerprint: fmt.Sprintf("%d-%d-%d", payload.OrgID, payload.DashboardID, payload.RuleID),
		RawPayload:        rawPayload,
	}
}

// mapStateToSeverity maps Grafana state to normalized severity
func (a *GrafanaAdapter) mapStateToSeverity(state string) database.AlertSeverity {
	switch strings.ToLower(state) {
	case "alerting":
		return database.AlertSeverityCritical
	case "pending":
		return database.AlertSeverityWarning
	case "no_data":
		return database.AlertSeverityInfo
	case "ok", "paused":
		return database.AlertSeverityInfo
	default:
		return database.AlertSeverityWarning
	}
}

// GetDefaultMappings returns the default field mappings for Grafana
func (a *GrafanaAdapter) GetDefaultMappings() database.JSONB {
	return database.JSONB{
		"alert_name":      "ruleName",
		"severity":        "state",
		"status":          "state",
		"summary":         "message",
		"target_host":     "evalMatches.0.tags.instance",
		"runbook_url":     "ruleUrl",
		"source_alert_id": "ruleId",
	}
}
