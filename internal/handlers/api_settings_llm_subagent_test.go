package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// The subagent model override is nullable: null/omitted/"" all mean "same as
// parent", which is what every pre-existing config has.

func TestLLMSubagent_DefaultsUnset(t *testing.T) {
	h := setupLLMHandlerTest(t)

	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"plain","api_key":"sk-x"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %v", code, resp)
	}
	for _, key := range []string{"subagent_model", "subagent_thinking_level"} {
		if v, ok := resp[key]; !ok || v != nil {
			t.Errorf("%s: expected explicit null in response, got %v (present=%v)", key, v, ok)
		}
	}

	stored, err := database.GetLLMSettingsByID(uint(resp["id"].(float64)))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	worker := BuildLLMSettingsForWorker(stored)
	if worker.SubagentModel != nil || worker.SubagentThinkingLevel != nil {
		t.Errorf("worker settings should carry nil overrides, got %+v", worker)
	}
	var msg AgentMessage
	applySubagentSettings(&msg, worker)
	if msg.SubagentModel != nil || msg.SubagentThinkingLevel != nil {
		t.Errorf("frame should not carry subagent fields when unset: %+v", msg)
	}
}

func TestLLMSubagent_CreateAndPersist(t *testing.T) {
	h := setupLLMHandlerTest(t)

	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"tiered","api_key":"sk-x","model":"claude-sonnet-5","subagent_model":"claude-haiku-4-5","subagent_thinking_level":"low"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %v", code, resp)
	}
	if resp["subagent_model"] != "claude-haiku-4-5" || resp["subagent_thinking_level"] != "low" {
		t.Errorf("response: %v", resp)
	}

	stored, err := database.GetLLMSettingsByID(uint(resp["id"].(float64)))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.SubagentModel == nil || *stored.SubagentModel != "claude-haiku-4-5" {
		t.Errorf("stored subagent_model: %v", stored.SubagentModel)
	}
	var msg AgentMessage
	applySubagentSettings(&msg, BuildLLMSettingsForWorker(stored))
	if msg.SubagentModel == nil || *msg.SubagentModel != "claude-haiku-4-5" {
		t.Errorf("frame subagent_model: %v", msg.SubagentModel)
	}
	if msg.SubagentThinkingLevel == nil || *msg.SubagentThinkingLevel != "low" {
		t.Errorf("frame subagent_thinking_level: %v", msg.SubagentThinkingLevel)
	}
}

func TestLLMSubagent_CreateRejectsBadThinkingLevel(t *testing.T) {
	h := setupLLMHandlerTest(t)
	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"bad","api_key":"sk-x","subagent_thinking_level":"turbo"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", code, resp)
	}
}

func TestLLMSubagent_UpdateOmitVsNull(t *testing.T) {
	h := setupLLMHandlerTest(t)
	cfg := seedLLMConfig(t, "tiered", database.LLMProviderAnthropic, false)
	path := fmt.Sprintf("/api/settings/llm/%d", cfg.ID)

	code, resp := decodeLLMConfig(t, h, http.MethodPut, path,
		`{"subagent_model":"claude-haiku-4-5","subagent_thinking_level":"minimal"}`)
	if code != http.StatusOK {
		t.Fatalf("set: expected 200, got %d: %v", code, resp)
	}
	if resp["subagent_model"] != "claude-haiku-4-5" {
		t.Errorf("set: %v", resp)
	}

	// Omitting the keys leaves the override alone.
	code, resp = decodeLLMConfig(t, h, http.MethodPut, path, `{"model":"claude-opus-5"}`)
	if code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %v", code, resp)
	}
	if resp["subagent_model"] != "claude-haiku-4-5" || resp["subagent_thinking_level"] != "minimal" {
		t.Errorf("omitted keys changed stored values: %v", resp)
	}

	// Explicit null (or "") clears back to "same as parent".
	code, resp = decodeLLMConfig(t, h, http.MethodPut, path, `{"subagent_model":null,"subagent_thinking_level":""}`)
	if code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %v", code, resp)
	}
	if resp["subagent_model"] != nil || resp["subagent_thinking_level"] != nil {
		t.Errorf("expected null after clear, got %v", resp)
	}
	stored, err := database.GetLLMSettingsByID(cfg.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.SubagentModel != nil || stored.SubagentThinkingLevel != nil {
		t.Errorf("expected NULL columns, got %+v", stored)
	}

	code, resp = decodeLLMConfig(t, h, http.MethodPut, path, `{"subagent_thinking_level":"turbo"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad level, got %d: %v", code, resp)
	}
}
