package database

import (
	"testing"
	"time"
)

func TestIncidentAlert_TableName(t *testing.T) {
	ia := IncidentAlert{}
	if ia.TableName() != "incident_alerts" {
		t.Errorf("expected table name 'incident_alerts', got '%s'", ia.TableName())
	}
}

func TestIncidentAlert_Fields(t *testing.T) {
	now := time.Now()
	payload := JSONB{"key": "value"}
	labels := JSONB{"env": "prod"}

	ia := IncidentAlert{
		IncidentID:            1,
		SourceType:            "alertmanager",
		SourceFingerprint:     "abc123",
		AlertName:             "HighCPU",
		Severity:              "critical",
		TargetHost:            "prod-db-01",
		TargetService:         "postgresql",
		Summary:               "CPU above 90%",
		Description:           "Detailed description",
		TargetLabels:          labels,
		Status:                "firing",
		AlertPayload:          payload,
		CorrelationConfidence: 0.85,
		CorrelationReason:     "Same host",
		AttachedAt:            now,
	}

	if ia.SourceType != "alertmanager" {
		t.Errorf("expected SourceType 'alertmanager', got '%s'", ia.SourceType)
	}
	if ia.CorrelationConfidence != 0.85 {
		t.Errorf("expected CorrelationConfidence 0.85, got %f", ia.CorrelationConfidence)
	}
}
