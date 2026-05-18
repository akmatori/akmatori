package services

import (
	"context"
	"errors"

	"github.com/akmatori/akmatori/internal/database"
)

// ErrWorkerNotConnected is returned by OneShotLLMCaller implementations when
// no agent worker is currently connected. Callers use it to fall back to
// deterministic behavior without surfacing the failure to end users.
var ErrWorkerNotConnected = errors.New("agent worker not connected")

// ErrIncidentSuperseded is delivered via OnError to a previously registered
// incident callback when a newer StartIncident/ContinueIncident call replaces
// it for the same incident_id (e.g. a second Slack message lands in the same
// thread before the first run finishes). Without this signal the displaced
// goroutine would block on its done channel forever — its callback entry was
// overwritten so subsequent agent_output/completed/error events route to the
// new waiter, and disconnect cleanup cannot reach the old entry either.
var ErrIncidentSuperseded = errors.New("incident superseded by a newer request")

// LLMSettingsForWorker holds LLM configuration forwarded to the agent worker
// for both incident execution and one-shot LLM calls.
type LLMSettingsForWorker struct {
	Provider      string
	APIKey        string
	Model         string
	ThinkingLevel string
	BaseURL       string
}

// BuildLLMSettingsForWorker creates LLMSettingsForWorker from database LLMSettings.
// Returns nil if settings are nil, disabled, or missing an API key.
func BuildLLMSettingsForWorker(dbSettings *database.LLMSettings) *LLMSettingsForWorker {
	if dbSettings == nil || !dbSettings.IsActive() {
		return nil
	}
	return &LLMSettingsForWorker{
		Provider:      string(dbSettings.Provider),
		APIKey:        dbSettings.APIKey,
		Model:         dbSettings.Model,
		ThinkingLevel: string(dbSettings.ThinkingLevel),
		BaseURL:       dbSettings.BaseURL,
	}
}

// OneShotLLMCaller issues a one-shot, provider-agnostic LLM completion through
// the agent worker. Implementations route the request over the worker WebSocket
// and return the assistant text or an error.
type OneShotLLMCaller interface {
	OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error)
}

// IncidentCallback collects the streaming events emitted while an agent run is
// executing. The handler-side AgentWSHandler aliases this struct; the lift
// into services lets non-handler packages (e.g. CronRunner) start an incident
// without importing internal/handlers.
//
// OnSuperseded fires when a newer StartIncident/ContinueIncident displaces
// this callback for the same incident_id. The displaced run has been handed
// off to the new callback — the new run will finalize the incident — so the
// old goroutine should unblock and exit silently rather than commit a
// failure that races the replacement's success.
type IncidentCallback struct {
	OnOutput     func(output string)
	OnCompleted  func(sessionID, response string, tokensUsed int, executionTimeMs int64)
	OnError      func(errorMsg string)
	OnSuperseded func()
}

// IncidentRunner is the cron/alert-spawn-facing slice of the agent worker
// transport. It is satisfied by *handlers.AgentWSHandler; the services layer
// consumes the interface so CronRunner stays test-friendly (a fake runner
// implements StartIncident/ReleaseRun and drives callbacks deterministically
// without spinning up a real WebSocket).
type IncidentRunner interface {
	IsWorkerConnected() bool
	StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []ToolAllowlistEntry, callback IncidentCallback) (string, error)
	ReleaseRun(incidentID, runID string) bool
}
