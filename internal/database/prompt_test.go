package database

import (
	"strings"
	"testing"
)

func TestDefaultIncidentManagerPrompt_ContainsQMDSearch(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"qmd.query tool reference", `qmd.query`},
		{"qmd.get tool reference", `qmd.get`},
		{"gateway_call usage", `gateway_call`},
		{"search instruction", `search for relevant runbooks`},
		{"fallback mention", `/akmatori/runbooks/`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(DefaultIncidentManagerPrompt, tt.contains) {
				t.Errorf("DefaultIncidentManagerPrompt should contain %q", tt.contains)
			}
		})
	}
}

func TestDefaultIncidentManagerPrompt_HasFallbackInstruction(t *testing.T) {
	if !strings.Contains(DefaultIncidentManagerPrompt, "unavailable") {
		t.Error("prompt should mention fallback when QMD is unavailable")
	}
}
