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
