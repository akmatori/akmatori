package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// The subagent model override is gone with the subagents: the fields are
// unknown (400) and no longer in responses.
func TestLLMSettings_SubagentFieldsRefused(t *testing.T) {
	h := setupLLMHandlerTest(t)
	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"x","api_key":"sk-x","subagent_model":"claude-haiku-5-5"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("create with subagent_model: expected 400, got %d: %v", code, resp)
	}

	cfg := seedLLMConfig(t, "main", database.LLMProviderAnthropic, false)
	path := fmt.Sprintf("/api/settings/llm/%d", cfg.ID)
	code, resp = decodeLLMConfig(t, h, http.MethodPut, path, `{"subagent_thinking_level":"low"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("update with subagent_thinking_level: expected 400, got %d: %v", code, resp)
	}
	code, resp = decodeLLMConfig(t, h, http.MethodPut, path, `{"model":"claude-opus-5-5"}`)
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, resp)
	}
	if _, ok := resp["subagent_model"]; ok {
		t.Errorf("response still carries subagent_model: %v", resp)
	}
}
