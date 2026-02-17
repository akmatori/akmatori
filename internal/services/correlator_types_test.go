package services

import (
	"encoding/json"
	"testing"
	"time"
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

func TestAlertContext_JSON_AllFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	ctx := AlertContext{
		AlertName:         "MemoryExhaustion",
		Severity:          "high",
		TargetHost:        "web-server-03",
		TargetService:     "api-gateway",
		Summary:           "Memory usage above 95%",
		Description:       "The web server is running out of memory due to a memory leak in the API gateway service.",
		SourceType:        "prometheus",
		SourceFingerprint: "abc123def456",
		TargetLabels: map[string]string{
			"env":     "production",
			"cluster": "us-east-1",
			"pod":     "api-gateway-5d4f8c9b7-x2k9m",
		},
		ReceivedAt: now,
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("failed to marshal AlertContext: %v", err)
	}

	var decoded AlertContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal AlertContext: %v", err)
	}

	// Verify all fields
	if decoded.AlertName != "MemoryExhaustion" {
		t.Errorf("AlertName = %q, want %q", decoded.AlertName, "MemoryExhaustion")
	}
	if decoded.Severity != "high" {
		t.Errorf("Severity = %q, want %q", decoded.Severity, "high")
	}
	if decoded.TargetHost != "web-server-03" {
		t.Errorf("TargetHost = %q, want %q", decoded.TargetHost, "web-server-03")
	}
	if decoded.TargetService != "api-gateway" {
		t.Errorf("TargetService = %q, want %q", decoded.TargetService, "api-gateway")
	}
	if decoded.SourceFingerprint != "abc123def456" {
		t.Errorf("SourceFingerprint = %q, want %q", decoded.SourceFingerprint, "abc123def456")
	}
	if len(decoded.TargetLabels) != 3 {
		t.Errorf("TargetLabels length = %d, want 3", len(decoded.TargetLabels))
	}
	if decoded.TargetLabels["env"] != "production" {
		t.Errorf("TargetLabels[env] = %q, want %q", decoded.TargetLabels["env"], "production")
	}
	if !decoded.ReceivedAt.Equal(now) {
		t.Errorf("ReceivedAt = %v, want %v", decoded.ReceivedAt, now)
	}
}

func TestAlertContext_JSON_EmptyFields(t *testing.T) {
	ctx := AlertContext{
		AlertName: "SimpleAlert",
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("failed to marshal AlertContext: %v", err)
	}

	var decoded AlertContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal AlertContext: %v", err)
	}

	if decoded.AlertName != "SimpleAlert" {
		t.Errorf("AlertName = %q, want %q", decoded.AlertName, "SimpleAlert")
	}
	if decoded.Severity != "" {
		t.Errorf("Severity should be empty, got %q", decoded.Severity)
	}
	if decoded.TargetLabels != nil && len(decoded.TargetLabels) > 0 {
		t.Errorf("TargetLabels should be nil or empty")
	}
}

func TestIncidentAlertSummary_JSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	summary := IncidentAlertSummary{
		AlertName:             "DiskSpaceLow",
		Severity:              "warning",
		TargetHost:            "storage-01",
		TargetService:         "postgres",
		Summary:               "Disk usage above 80%",
		Description:           "Database server disk is filling up",
		SourceType:            "zabbix",
		SourceFingerprint:     "zab-123",
		TargetLabels:          map[string]string{"dc": "dc1"},
		Status:                "firing",
		AttachedAt:            now,
		CorrelationConfidence: 0.95,
		CorrelationReason:     "Same host and service",
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal IncidentAlertSummary: %v", err)
	}

	var decoded IncidentAlertSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal IncidentAlertSummary: %v", err)
	}

	if decoded.AlertName != "DiskSpaceLow" {
		t.Errorf("AlertName = %q, want %q", decoded.AlertName, "DiskSpaceLow")
	}
	if decoded.Status != "firing" {
		t.Errorf("Status = %q, want %q", decoded.Status, "firing")
	}
	if decoded.CorrelationConfidence != 0.95 {
		t.Errorf("CorrelationConfidence = %f, want %f", decoded.CorrelationConfidence, 0.95)
	}
	if decoded.CorrelationReason != "Same host and service" {
		t.Errorf("CorrelationReason = %q, want %q", decoded.CorrelationReason, "Same host and service")
	}
}

func TestIncidentSummary_JSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	summary := IncidentSummary{
		UUID:               "inc-uuid-001",
		Title:              "Database cluster outage",
		Status:             "running",
		DiagnosedRootCause: "Network partition between primary and replicas",
		CreatedAt:          now,
		AgeMinutes:         45,
		Alerts: []IncidentAlertSummary{
			{AlertName: "Alert1", Status: "firing"},
			{AlertName: "Alert2", Status: "resolved"},
		},
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal IncidentSummary: %v", err)
	}

	var decoded IncidentSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal IncidentSummary: %v", err)
	}

	if decoded.UUID != "inc-uuid-001" {
		t.Errorf("UUID = %q, want %q", decoded.UUID, "inc-uuid-001")
	}
	if decoded.DiagnosedRootCause != "Network partition between primary and replicas" {
		t.Errorf("DiagnosedRootCause = %q, want %q", decoded.DiagnosedRootCause, "Network partition between primary and replicas")
	}
	if decoded.AgeMinutes != 45 {
		t.Errorf("AgeMinutes = %d, want %d", decoded.AgeMinutes, 45)
	}
	if len(decoded.Alerts) != 2 {
		t.Errorf("Alerts length = %d, want 2", len(decoded.Alerts))
	}
}

func TestIncidentSummary_JSON_OmitsEmptyRootCause(t *testing.T) {
	summary := IncidentSummary{
		UUID:   "inc-002",
		Title:  "Test incident",
		Status: "running",
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal IncidentSummary: %v", err)
	}

	// Check that diagnosed_root_cause is omitted when empty
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}

	if _, exists := raw["diagnosed_root_cause"]; exists {
		t.Error("diagnosed_root_cause should be omitted when empty")
	}
}

func TestCorrelatorOutput_JSON_NewDecision(t *testing.T) {
	output := CorrelatorOutput{
		Decision:   "new",
		Confidence: 0.75,
		Reason:     "No matching incidents found",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal CorrelatorOutput: %v", err)
	}

	var decoded CorrelatorOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CorrelatorOutput: %v", err)
	}

	if decoded.Decision != "new" {
		t.Errorf("Decision = %q, want %q", decoded.Decision, "new")
	}
	if decoded.IncidentUUID != "" {
		t.Errorf("IncidentUUID should be empty for 'new' decision, got %q", decoded.IncidentUUID)
	}
}

func TestMergeAnalyzerInput_JSON(t *testing.T) {
	input := MergeAnalyzerInput{
		OpenIncidents: []IncidentSummary{
			{UUID: "inc-1", Title: "Incident 1"},
			{UUID: "inc-2", Title: "Incident 2"},
		},
		ConfidenceThreshold: 0.85,
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal MergeAnalyzerInput: %v", err)
	}

	var decoded MergeAnalyzerInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal MergeAnalyzerInput: %v", err)
	}

	if len(decoded.OpenIncidents) != 2 {
		t.Errorf("OpenIncidents length = %d, want 2", len(decoded.OpenIncidents))
	}
	if decoded.ConfidenceThreshold != 0.85 {
		t.Errorf("ConfidenceThreshold = %f, want %f", decoded.ConfidenceThreshold, 0.85)
	}
}

func TestProposedMerge_JSON(t *testing.T) {
	merge := ProposedMerge{
		SourceIncidentUUID: "inc-source",
		TargetIncidentUUID: "inc-target",
		Confidence:         0.92,
		Reason:             "Same root cause detected",
	}

	data, err := json.Marshal(merge)
	if err != nil {
		t.Fatalf("failed to marshal ProposedMerge: %v", err)
	}

	var decoded ProposedMerge
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ProposedMerge: %v", err)
	}

	if decoded.SourceIncidentUUID != "inc-source" {
		t.Errorf("SourceIncidentUUID = %q, want %q", decoded.SourceIncidentUUID, "inc-source")
	}
	if decoded.TargetIncidentUUID != "inc-target" {
		t.Errorf("TargetIncidentUUID = %q, want %q", decoded.TargetIncidentUUID, "inc-target")
	}
	if decoded.Confidence != 0.92 {
		t.Errorf("Confidence = %f, want %f", decoded.Confidence, 0.92)
	}
}

func TestMergeAnalyzerOutput_JSON(t *testing.T) {
	output := MergeAnalyzerOutput{
		ProposedMerges: []ProposedMerge{
			{
				SourceIncidentUUID: "inc-1",
				TargetIncidentUUID: "inc-2",
				Confidence:         0.88,
				Reason:             "Related alerts",
			},
		},
		NoMerge: []struct {
			IncidentUUID string `json:"incident_uuid"`
			Reason       string `json:"reason"`
		}{
			{IncidentUUID: "inc-3", Reason: "Unrelated to other incidents"},
		},
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal MergeAnalyzerOutput: %v", err)
	}

	var decoded MergeAnalyzerOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal MergeAnalyzerOutput: %v", err)
	}

	if len(decoded.ProposedMerges) != 1 {
		t.Errorf("ProposedMerges length = %d, want 1", len(decoded.ProposedMerges))
	}
	if len(decoded.NoMerge) != 1 {
		t.Errorf("NoMerge length = %d, want 1", len(decoded.NoMerge))
	}
	if decoded.NoMerge[0].IncidentUUID != "inc-3" {
		t.Errorf("NoMerge[0].IncidentUUID = %q, want %q", decoded.NoMerge[0].IncidentUUID, "inc-3")
	}
}

func TestCorrelatorInput_EmptyIncidents(t *testing.T) {
	input := CorrelatorInput{
		IncomingAlert: AlertContext{
			AlertName: "Test",
		},
		OpenIncidents: []IncidentSummary{},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal CorrelatorInput: %v", err)
	}

	var decoded CorrelatorInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CorrelatorInput: %v", err)
	}

	if decoded.OpenIncidents == nil {
		t.Error("OpenIncidents should not be nil after unmarshal")
	}
	if len(decoded.OpenIncidents) != 0 {
		t.Errorf("OpenIncidents length = %d, want 0", len(decoded.OpenIncidents))
	}
}
