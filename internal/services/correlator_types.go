package services

import "time"

// AlertContext contains full alert details for correlation
type AlertContext struct {
	AlertName         string            `json:"alert_name"`
	Severity          string            `json:"severity"`
	TargetHost        string            `json:"target_host"`
	TargetService     string            `json:"target_service"`
	Summary           string            `json:"summary"`
	Description       string            `json:"description"`
	SourceType        string            `json:"source_type"`
	SourceFingerprint string            `json:"source_fingerprint"`
	TargetLabels      map[string]string `json:"target_labels"`
	ReceivedAt        time.Time         `json:"received_at"`
}

// IncidentAlertSummary contains alert details within an incident
type IncidentAlertSummary struct {
	AlertName             string            `json:"alert_name"`
	Severity              string            `json:"severity"`
	TargetHost            string            `json:"target_host"`
	TargetService         string            `json:"target_service"`
	Summary               string            `json:"summary"`
	Description           string            `json:"description"`
	SourceType            string            `json:"source_type"`
	SourceFingerprint     string            `json:"source_fingerprint"`
	TargetLabels          map[string]string `json:"target_labels"`
	Status                string            `json:"status"` // firing, resolved
	AttachedAt            time.Time         `json:"attached_at"`
	CorrelationConfidence float64           `json:"correlation_confidence"`
	CorrelationReason     string            `json:"correlation_reason"`
}

// IncidentSummary contains incident details for correlation
type IncidentSummary struct {
	UUID               string                 `json:"uuid"`
	Title              string                 `json:"title"`
	Status             string                 `json:"status"`
	DiagnosedRootCause string                 `json:"diagnosed_root_cause,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	AgeMinutes         int                    `json:"age_minutes"`
	Alerts             []IncidentAlertSummary `json:"alerts"`
}

// CorrelatorInput is the input to the Codex correlator
type CorrelatorInput struct {
	IncomingAlert AlertContext      `json:"incoming_alert"`
	OpenIncidents []IncidentSummary `json:"open_incidents"`
}

// CorrelatorOutput is the output from the Codex correlator
type CorrelatorOutput struct {
	Decision     string  `json:"decision"`      // "attach" or "new"
	IncidentUUID string  `json:"incident_uuid"` // Only if attach
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
}

// MergeAnalyzerInput is the input to the background merge analyzer
type MergeAnalyzerInput struct {
	OpenIncidents       []IncidentSummary `json:"open_incidents"`
	ConfidenceThreshold float64           `json:"confidence_threshold"`
}

// ProposedMerge represents a suggested incident merge
type ProposedMerge struct {
	SourceIncidentUUID string  `json:"source_incident_uuid"`
	TargetIncidentUUID string  `json:"target_incident_uuid"`
	Confidence         float64 `json:"confidence"`
	Reason             string  `json:"reason"`
}

// MergeAnalyzerOutput is the output from the merge analyzer
type MergeAnalyzerOutput struct {
	ProposedMerges []ProposedMerge `json:"proposed_merges"`
	NoMerge        []struct {
		IncidentUUID string `json:"incident_uuid"`
		Reason       string `json:"reason"`
	} `json:"no_merge"`
}
