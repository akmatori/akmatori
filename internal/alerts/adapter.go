package alerts

import (
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// NormalizedAlert is the common alert format all adapters produce
type NormalizedAlert struct {
	AlertName   string
	Severity    database.AlertSeverity
	Status      database.AlertStatus
	Summary     string
	Description string

	TargetHost    string
	TargetService string
	TargetLabels  map[string]string

	MetricName     string
	MetricValue    string
	ThresholdValue string

	RunbookURL string

	StartedAt *time.Time
	EndedAt   *time.Time

	SourceAlertID     string
	SourceFingerprint string
	RawPayload        map[string]interface{}
}

// AlertAdapter defines the interface for source-specific alert parsing
type AlertAdapter interface {
	// GetSourceType returns the source type name (e.g., "alertmanager")
	GetSourceType() string

	// ValidateWebhookSecret validates the incoming webhook using the instance's secret
	ValidateWebhookSecret(r *http.Request, instance *database.AlertSourceInstance) error

	// ParsePayload parses the raw request body into normalized alerts
	// A single webhook can contain multiple alerts (e.g., Alertmanager groups)
	ParsePayload(body []byte, instance *database.AlertSourceInstance) ([]NormalizedAlert, error)

	// GetDefaultMappings returns the default field mappings for this source type
	GetDefaultMappings() database.JSONB
}

// BaseAdapter provides common functionality for all adapters
type BaseAdapter struct {
	SourceType string
}

// GetSourceType returns the source type name
func (b *BaseAdapter) GetSourceType() string {
	return b.SourceType
}

// ExtractNestedValue extracts a value using dot notation (e.g., "labels.alertname")
func ExtractNestedValue(data map[string]interface{}, path string) interface{} {
	if path == "" {
		return nil
	}

	parts := strings.Split(path, ".")
	current := interface{}(data)

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		case map[string]string:
			current = v[part]
		default:
			return nil
		}
		if current == nil {
			return nil
		}
	}

	return current
}

// ExtractString extracts a string value using dot notation
func ExtractString(data map[string]interface{}, path string) string {
	val := ExtractNestedValue(data, path)
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

// MergeMappings merges instance-specific mappings over defaults
func MergeMappings(defaults, overrides database.JSONB) database.JSONB {
	result := make(database.JSONB)
	for k, v := range defaults {
		result[k] = v
	}
	if overrides != nil {
		for k, v := range overrides {
			result[k] = v
		}
	}
	return result
}

// NormalizeSeverity normalizes severity strings to standard values
func NormalizeSeverity(severity string, severityMapping map[string][]string) database.AlertSeverity {
	severity = strings.ToLower(severity)

	// Check direct match first
	switch severity {
	case "critical":
		return database.AlertSeverityCritical
	case "high":
		return database.AlertSeverityHigh
	case "warning":
		return database.AlertSeverityWarning
	case "info", "informational":
		return database.AlertSeverityInfo
	}

	// Check severity mapping
	if severityMapping != nil {
		for normalized, aliases := range severityMapping {
			for _, alias := range aliases {
				if strings.ToLower(alias) == severity {
					switch normalized {
					case "critical":
						return database.AlertSeverityCritical
					case "high":
						return database.AlertSeverityHigh
					case "warning":
						return database.AlertSeverityWarning
					case "info":
						return database.AlertSeverityInfo
					}
				}
			}
		}
	}

	// Default to warning if unknown
	return database.AlertSeverityWarning
}

// NormalizeStatus normalizes status strings to standard values
func NormalizeStatus(status string) database.AlertStatus {
	status = strings.ToLower(status)
	switch status {
	case "firing", "alerting", "triggered", "active", "problem":
		return database.AlertStatusFiring
	case "resolved", "ok", "recovery", "inactive":
		return database.AlertStatusResolved
	default:
		return database.AlertStatusFiring
	}
}

// DefaultSeverityMapping provides default mapping for common severity values
var DefaultSeverityMapping = map[string][]string{
	"critical": {"critical", "disaster", "p1", "5", "emergency", "fatal"},
	"high":     {"high", "major", "p2", "4", "error", "severe"},
	"warning":  {"warning", "minor", "p3", "3", "average", "warn"},
	"info":     {"info", "informational", "p4", "1", "2", "low", "notice", "debug"},
}
