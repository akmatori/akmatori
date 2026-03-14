package handlers

import (
	"fmt"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// evaluateAlertAggregation uses Codex to decide if alert should attach to existing incident
func (h *AlertHandler) evaluateAlertAggregation(
	instance *database.AlertSourceInstance,
	normalized alerts.NormalizedAlert,
) (*services.CorrelatorOutput, error) {
	// Check if aggregation service is available
	if h.aggregationService == nil {
		return &services.CorrelatorOutput{Decision: "new", Reason: "Aggregation service not configured"}, nil
	}

	// Check if aggregation is enabled
	settings, err := h.aggregationService.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get aggregation settings: %w", err)
	}
	if !settings.Enabled {
		return &services.CorrelatorOutput{Decision: "new", Reason: "Aggregation disabled"}, nil
	}

	// Build alert context
	alertContext := services.AlertContext{
		AlertName:         normalized.AlertName,
		Severity:          string(normalized.Severity),
		TargetHost:        normalized.TargetHost,
		TargetService:     normalized.TargetService,
		Summary:           normalized.Summary,
		Description:       normalized.Description,
		SourceType:        instance.AlertSourceType.Name,
		SourceFingerprint: normalized.SourceFingerprint,
		TargetLabels:      normalized.TargetLabels,
		ReceivedAt:        time.Now(),
	}

	// Build correlator input
	input, err := h.aggregationService.BuildCorrelatorInput(alertContext)
	if err != nil {
		return nil, fmt.Errorf("failed to build correlator input: %w", err)
	}

	// If no open incidents, create new
	if len(input.OpenIncidents) == 0 {
		return &services.CorrelatorOutput{Decision: "new", Reason: "No open incidents"}, nil
	}

	// TODO: Call Codex correlator (will be implemented in a future task)
	// For now, return "new" as default
	return &services.CorrelatorOutput{Decision: "new", Reason: "Correlator not yet implemented"}, nil
}
