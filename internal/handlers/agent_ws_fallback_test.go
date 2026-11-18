package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/gorilla/websocket"
)


func stubFallback(h *AgentWSHandler, fb *LLMSettingsForWorker) {
	h.resolveFallback = func(primary *LLMSettingsForWorker) *LLMSettingsForWorker {
		if primary == nil {
			return nil
		}
		return fb
	}
}

func readFrame(t *testing.T, conn *websocket.Conn, typ AgentMessageType) (AgentMessage, map[string]any) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read worker frame: %v", err)
		}
		var msg AgentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type == typ {
			var raw map[string]any
			_ = json.Unmarshal(data, &raw)
			return msg, raw
		}
	}
}

// One-shot frames carry the fallback with the CALL SITE's sampling values,
// overridden only by the fallback's own settings (not the primary's).
func TestOneShotLLM_SendsFallbackWithMergedSampling(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()
	stubFallback(handler, &LLMSettingsForWorker{
		Name: "Backup", Provider: "openrouter", APIKey: "sk-or", Model: "openai/gpt-5.5",
		ThinkingLevel: "low", TopP: ptr(0.9),
	})

	primary := &LLMSettingsForWorker{
		Name: "Main", Provider: "custom", APIKey: "sk-main", Model: "deepseek",
		BaseURL: "https://llm.internal/v1", Temperature: ptr(0.7), MaxTokens: ptr(9000),
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := handler.OneShotLLM(ctx, primary, "sys", "user", 50, 0.0)
		done <- err
	}()

	msg, _ := readFrame(t, conn, AgentMessageTypeOneshotLLMRequest)
	if msg.LLMName != "Main" {
		t.Errorf("llm_name = %q, want Main", msg.LLMName)
	}
	fb := msg.FallbackLLM
	if fb == nil {
		t.Fatal("fallback_llm missing")
	}
	if fb.Provider != "openrouter" || fb.APIKey != "sk-or" || fb.Model != "openai/gpt-5.5" || fb.Name != "Backup" || fb.ThinkingLevel != "low" {
		t.Errorf("unexpected fallback config: %+v", fb)
	}
	if fb.MaxTokens == nil || *fb.MaxTokens != 50 {
		t.Errorf("fallback max_tokens = %v, want call-site 50", fb.MaxTokens)
	}
	if fb.Temperature == nil || *fb.Temperature != 0 {
		t.Errorf("fallback temperature = %v, want call-site 0 (explicit zero must survive)", fb.Temperature)
	}
	if fb.TopP == nil || *fb.TopP != 0.9 {
		t.Errorf("fallback top_p = %v, want 0.9", fb.TopP)
	}
	// The primary's own overrides still apply to the primary fields.
	if msg.Temperature == nil || *msg.Temperature != 0.7 || msg.MaxTokens == nil || *msg.MaxTokens != 9000 {
		t.Errorf("primary sampling changed: temp=%v max=%v", msg.Temperature, msg.MaxTokens)
	}

	resp := AgentMessage{
		Type: AgentMessageTypeOneshotLLMResponse, RequestID: msg.RequestID, Summary: "ok",
		LLMProvider: "openrouter", LLMModel: "openai/gpt-5.5", LLMFallbackFrom: "custom/deepseek", LLMFallbackReason: "500",
	}
	if err := conn.WriteJSON(resp); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("OneShotLLM: %v", err)
	}
}

func TestOneShotLLM_NoFallbackKeepsFrame(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()
	stubFallback(handler, nil)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, _ = handler.OneShotLLM(ctx, &LLMSettingsForWorker{Provider: "openai", APIKey: "k", Model: "m"}, "", "u", 10, 0)
	}()
	_, raw := readFrame(t, conn, AgentMessageTypeOneshotLLMRequest)
	if _, ok := raw["fallback_llm"]; ok {
		t.Errorf("fallback_llm must be absent when no fallback is set: %v", raw["fallback_llm"])
	}
}

func TestStartIncident_SendsFallback(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()
	stubFallback(handler, &LLMSettingsForWorker{Provider: "anthropic", APIKey: "sk-ant", Model: "claude-x", MaxTokens: ptr(4000)})

	_, err := handler.StartIncident("scope-1", "run-1", "task",
		&LLMSettingsForWorker{Name: "Main", Provider: "custom", APIKey: "k", Model: "m"},
		nil, nil, IncidentCallback{})
	if err != nil {
		t.Fatalf("StartIncident: %v", err)
	}
	msg, _ := readFrame(t, conn, AgentMessageTypeNewIncident)
	if msg.FallbackLLM == nil || msg.FallbackLLM.Model != "claude-x" {
		t.Fatalf("fallback_llm = %+v", msg.FallbackLLM)
	}
	if msg.FallbackLLM.MaxTokens == nil || *msg.FallbackLLM.MaxTokens != 4000 {
		t.Errorf("fallback max_tokens = %v, want its own 4000", msg.FallbackLLM.MaxTokens)
	}
	if msg.FallbackLLM.Temperature != nil {
		t.Errorf("agent frames carry only the fallback's own overrides, got temperature %v", *msg.FallbackLLM.Temperature)
	}
}

// The run record names the config that really answered.
func TestHandleAgentCompleted_RecordsFallbackModel(t *testing.T) {
	db := setupLastSkillDB(t, "run-fb", "run-primary")
	if err := db.Model(&database.AgentRun{}).Where("1 = 1").
		Updates(map[string]any{"provider": "custom", "model": "deepseek", "thinking_level": "medium"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewAgentWSHandler()

	handler.handleAgentCompleted(AgentMessage{
		Type: AgentMessageTypeAgentCompleted, IncidentID: "incident-skill", RunID: "run-fb",
		LLMProvider: "openrouter", LLMModel: "openai/gpt-5.5", LLMThinkingLevel: "low",
		LLMFallbackFrom: "custom/deepseek", LLMFallbackReason: "500 status code (no body)",
	})
	handler.handleAgentError(AgentMessage{
		Type: AgentMessageTypeAgentError, IncidentID: "incident-skill", RunID: "run-primary",
		Error: "401 bad key", LLMProvider: "custom", LLMModel: "deepseek",
	})

	var fb, primary database.AgentRun
	db.Where("run_id = ?", "run-fb").First(&fb)
	db.Where("run_id = ?", "run-primary").First(&primary)
	if fb.Provider != "openrouter" || fb.Model != "openai/gpt-5.5" || fb.ThinkingLevel != "low" {
		t.Errorf("fallback run stamped %s/%s/%s", fb.Provider, fb.Model, fb.ThinkingLevel)
	}
	if fb.FallbackFrom != "custom/deepseek" || fb.FallbackReason != "500 status code (no body)" {
		t.Errorf("fallback_from=%q reason=%q", fb.FallbackFrom, fb.FallbackReason)
	}
	if primary.Provider != "custom" || primary.Model != "deepseek" || primary.FallbackFrom != "" {
		t.Errorf("primary run must stay unchanged: %+v", primary)
	}
}
