package models

// ZabbixAlert represents an alert from Zabbix webhook
type ZabbixAlert struct {
	EventTime         string `json:"event_time"`
	AlertName         string `json:"alert_name"` // Note: Zabbix had typo "alert_nae", we'll handle both
	Severity          string `json:"severity"`
	Priority          string `json:"priority"`
	MetricName        string `json:"metric_name"`
	MetricValue       string `json:"metric_value"`
	TriggerExpression string `json:"trigger_expression"`
	PendingDuration   string `json:"pending_duration"`
	EventID           string `json:"event_id"`
	Hardware          string `json:"hardware"`
}

// GetSeverityEmoji returns an emoji for the alert severity
func (a *ZabbixAlert) GetSeverityEmoji() string {
	switch a.Priority {
	case "5":
		return "🔴" // Disaster
	case "4":
		return "🟠" // High
	case "3":
		return "🟡" // Average
	case "2":
		return "🔵" // Warning
	case "1":
		return "⚪" // Information
	default:
		return "⚫"
	}
}

// GetSeverityLabel returns a human-readable severity label
func (a *ZabbixAlert) GetSeverityLabel() string {
	switch a.Priority {
	case "5":
		return "Disaster"
	case "4":
		return "High"
	case "3":
		return "Average"
	case "2":
		return "Warning"
	case "1":
		return "Information"
	default:
		return "Unknown"
	}
}
