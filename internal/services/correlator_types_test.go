package services

import (
	"encoding/json"
	"testing"
)

func TestCorrelatorInput_JSON(t *testing.T) {
	input := CorrelatorInput{
		IncomingAlert: AlertContext{
			AlertName:   "HighCPU",
			Severity:    "critical",
			TargetHost:  "prod-db-01",
			Summary:     "CPU above 90%",
			SourceType:  "alertmanager",
		},
		OpenIncidents: []IncidentSummary{
			{
				UUID:   "inc-001",
				Title:  "Test incident",
				Status: "running",
			},
		},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal CorrelatorInput: %v", err)
	}

	var decoded CorrelatorInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CorrelatorInput: %v", err)
	}

	if decoded.IncomingAlert.AlertName != "HighCPU" {
		t.Errorf("expected AlertName 'HighCPU', got '%s'", decoded.IncomingAlert.AlertName)
	}
	if len(decoded.OpenIncidents) != 1 {
		t.Errorf("expected 1 open incident, got %d", len(decoded.OpenIncidents))
	}
}

func TestCorrelatorOutput_JSON(t *testing.T) {
	output := CorrelatorOutput{
		Decision:     "attach",
		IncidentUUID: "inc-001",
		Confidence:   0.89,
		Reason:       "Same host, related alerts",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal CorrelatorOutput: %v", err)
	}

	var decoded CorrelatorOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CorrelatorOutput: %v", err)
	}

	if decoded.Decision != "attach" {
		t.Errorf("expected Decision 'attach', got '%s'", decoded.Decision)
	}
	if decoded.Confidence != 0.89 {
		t.Errorf("expected Confidence 0.89, got %f", decoded.Confidence)
	}
}
