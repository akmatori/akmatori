package services

import (
	"strings"
	"testing"
)

func TestCorrelatorPrompt_Contains_Key_Instructions(t *testing.T) {
	prompt := GetCorrelatorSystemPrompt()

	requiredPhrases := []string{
		"attach",
		"new",
		"confidence",
		"JSON",
		"decision",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(phrase)) {
			t.Errorf("prompt should contain '%s'", phrase)
		}
	}
}

func TestBuildCorrelatorUserPrompt(t *testing.T) {
	input := &CorrelatorInput{
		IncomingAlert: AlertContext{
			AlertName:  "HighCPU",
			TargetHost: "prod-db-01",
		},
		OpenIncidents: []IncidentSummary{
			{UUID: "inc-1", Title: "Test"},
		},
	}

	prompt, err := BuildCorrelatorUserPrompt(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "HighCPU") {
		t.Error("prompt should contain alert name")
	}
	if !strings.Contains(prompt, "inc-1") {
		t.Error("prompt should contain incident UUID")
	}
}
