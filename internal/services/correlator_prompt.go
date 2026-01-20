package services

import (
	"encoding/json"
	"fmt"
)

// GetCorrelatorSystemPrompt returns the system prompt for the correlator
func GetCorrelatorSystemPrompt() string {
	return `You are an alert correlation engine. Your job is to decide whether an incoming alert should be attached to an existing incident or create a new incident.

## Decision Criteria

### ATTACH to existing incident when:
- Same target host with related alert types (e.g., HighCPU + HighMemory)
- Cascading failure pattern (one problem causing another)
- Alert arrived within minutes of incident creation
- More severe version of existing alert
- Common root cause pattern

### CREATE new incident when:
- Different target host/service with no clear relationship
- Unrelated problem category
- Incident is very old (>1 hour) and likely resolved
- No logical connection to existing incidents

## Output Format

Return ONLY valid JSON with this exact structure:
{
  "decision": "attach" or "new",
  "incident_uuid": "uuid-here" (only if decision is "attach"),
  "confidence": 0.0 to 1.0,
  "reason": "Brief explanation of your decision"
}

Do not include any text outside the JSON block.`
}

// BuildCorrelatorUserPrompt creates the user prompt with the input data
func BuildCorrelatorUserPrompt(input *CorrelatorInput) (string, error) {
	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal correlator input: %w", err)
	}

	return fmt.Sprintf(`Analyze this incoming alert and decide whether to attach it to an existing incident or create a new one.

%s

Return your decision as JSON.`, string(inputJSON)), nil
}
