package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// decodeLLMConfig runs a request through the handler and returns the decoded
// JSON body along with the status code.
func decodeLLMConfig(t *testing.T, h *APIHandler, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if path == "/api/settings/llm" {
		h.handleLLMSettings(w, req)
	} else {
		h.handleLLMSettingsByID(w, req)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response (%d): %v: %s", w.Code, err, w.Body.String())
	}
	return w.Code, decoded
}

// TestLLMSampling_DefaultsUnset is the guarantee that adding these knobs did not
// change behaviour for anyone who does not use them: a config created without
// sampling fields stores NULL for all four, and BuildLLMSettingsForWorker
// forwards nils, so the worker sends no sampling parameters at all.
func TestLLMSampling_DefaultsUnset(t *testing.T) {
	h := setupLLMHandlerTest(t)

	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"default","api_key":"sk-x","model":"claude-sonnet-5"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %v", code, resp)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "max_tokens"} {
		if resp[key] != nil {
			t.Errorf("%s: expected null on a config created without sampling params, got %v", key, resp[key])
		}
	}

	stored, err := database.GetLLMSettingsByID(uint(resp["id"].(float64)))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	worker := services.BuildLLMSettingsForWorker(stored)
	if worker == nil {
		t.Fatal("expected worker settings")
	}
	if worker.Temperature != nil || worker.TopP != nil || worker.TopK != nil || worker.MaxTokens != nil {
		t.Errorf("expected all sampling fields nil, got %+v", worker)
	}

	// And nothing lands on the wire frame either.
	msg := AgentMessage{}
	applySamplingSettings(&msg, worker)
	if msg.Temperature != nil || msg.TopP != nil || msg.TopK != nil || msg.MaxTokens != nil {
		t.Errorf("expected an untouched frame, got %+v", msg)
	}
}

func TestLLMSampling_CreateAndPersist(t *testing.T) {
	h := setupLLMHandlerTest(t)

	code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"anthropic","name":"tuned","api_key":"sk-x","temperature":0,"top_p":0.9,"top_k":40,"max_tokens":8192}`)
	if code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %v", code, resp)
	}
	// 0 is a real setting, not "unset" — the whole reason these are pointers.
	if resp["temperature"] != float64(0) {
		t.Errorf("temperature: got %v, want 0", resp["temperature"])
	}
	if resp["top_p"] != 0.9 {
		t.Errorf("top_p: got %v", resp["top_p"])
	}
	if resp["top_k"] != float64(40) {
		t.Errorf("top_k: got %v", resp["top_k"])
	}
	if resp["max_tokens"] != float64(8192) {
		t.Errorf("max_tokens: got %v", resp["max_tokens"])
	}

	stored, err := database.GetLLMSettingsByID(uint(resp["id"].(float64)))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Temperature == nil || *stored.Temperature != 0 {
		t.Errorf("stored temperature: got %v, want 0", stored.Temperature)
	}
	if stored.TopK == nil || *stored.TopK != 40 {
		t.Errorf("stored top_k: got %v", stored.TopK)
	}
}

func TestLLMSampling_CreateRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"temperature high", `{"provider":"openai","name":"a","temperature":2.5}`, "temperature"},
		{"top_p high", `{"provider":"openai","name":"b","top_p":1.5}`, "top_p"},
		{"top_k zero", `{"provider":"openai","name":"c","top_k":0}`, "top_k"},
		{"max_tokens zero", `{"provider":"openai","name":"d","max_tokens":0}`, "max_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := setupLLMHandlerTest(t)
			code, resp := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %v", code, resp)
			}
			msg, _ := resp["error"].(string)
			if msg == "" {
				msg, _ = resp["message"].(string)
			}
			if msg == "" {
				t.Fatalf("expected an error message, got %v", resp)
			}
		})
	}
}

// TestLLMSampling_UpdateOmitVsNull covers the three-state contract on PUT: an
// omitted key leaves the stored override alone, an explicit null clears it.
func TestLLMSampling_UpdateOmitVsNull(t *testing.T) {
	h := setupLLMHandlerTest(t)
	cfg := seedLLMConfig(t, "tuned", database.LLMProviderAnthropic, false)

	temp, topP := 0.4, 0.8
	topK, maxTokens := 20, 4096
	if _, err := database.UpdateLLMSettings(cfg.ID, map[string]interface{}{
		"temperature": &temp, "top_p": &topP, "top_k": &topK, "max_tokens": &maxTokens,
	}); err != nil {
		t.Fatalf("seed sampling: %v", err)
	}

	path := fmt.Sprintf("/api/settings/llm/%d", cfg.ID)

	// Omitting every sampling key must not disturb the stored values.
	code, resp := decodeLLMConfig(t, h, http.MethodPut, path, `{"model":"claude-opus-5"}`)
	if code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %v", code, resp)
	}
	if resp["temperature"] != 0.4 || resp["top_p"] != 0.8 {
		t.Errorf("omitted keys changed stored values: %v", resp)
	}

	// Explicit null clears back to "provider default".
	code, resp = decodeLLMConfig(t, h, http.MethodPut, path,
		`{"temperature":null,"top_p":null,"top_k":null,"max_tokens":null}`)
	if code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %v", code, resp)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "max_tokens"} {
		if resp[key] != nil {
			t.Errorf("%s: expected null after explicit clear, got %v", key, resp[key])
		}
	}

	stored, err := database.GetLLMSettingsByID(cfg.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Temperature != nil || stored.TopP != nil || stored.TopK != nil || stored.MaxTokens != nil {
		t.Errorf("expected all sampling columns NULL, got %+v", stored)
	}
}

func TestLLMSampling_UpdateRejectsOutOfRange(t *testing.T) {
	h := setupLLMHandlerTest(t)
	cfg := seedLLMConfig(t, "tuned", database.LLMProviderAnthropic, false)

	code, resp := decodeLLMConfig(t, h, http.MethodPut,
		fmt.Sprintf("/api/settings/llm/%d", cfg.ID), `{"top_p":2}`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", code, resp)
	}
}

// TestApplySamplingSettings_OverridesOneShotCallSite pins the resolution order
// chosen for one-shot calls: the operator's configured value replaces the
// hardcoded per-call default.
func TestApplySamplingSettings_OverridesOneShotCallSite(t *testing.T) {
	callSiteTemp, callSiteTokens := 0.1, 500
	msg := AgentMessage{Temperature: &callSiteTemp, MaxTokens: &callSiteTokens}

	configured := 0.9
	applySamplingSettings(&msg, &services.LLMSettingsForWorker{Temperature: &configured})

	if msg.Temperature == nil || *msg.Temperature != 0.9 {
		t.Errorf("temperature: got %v, want the configured 0.9", msg.Temperature)
	}
	// Untouched parameters keep the call site's value.
	if msg.MaxTokens == nil || *msg.MaxTokens != 500 {
		t.Errorf("max_tokens: got %v, want the call-site 500", msg.MaxTokens)
	}
}

// TestOneShotFrame_ZeroTemperatureSurvives is the regression guard for the
// `omitempty` bug: with a float64 field, a deliberate temperature of 0 was
// dropped from the JSON frame, so callers asking for deterministic output
// silently ran at the provider default instead.
func TestOneShotFrame_ZeroTemperatureSurvives(t *testing.T) {
	zero := 0.0
	tokens := 200
	msg := AgentMessage{
		Type:        AgentMessageTypeOneshotLLMRequest,
		RequestID:   "req-1",
		MaxTokens:   &tokens,
		Temperature: &zero,
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	value, present := wire["temperature"]
	if !present {
		t.Fatal("temperature 0 was dropped from the frame; the worker would fall back to the provider default")
	}
	if value != float64(0) {
		t.Errorf("temperature: got %v, want 0", value)
	}

	// An unset temperature must still be absent, so the worker omits it.
	empty, err := json.Marshal(AgentMessage{Type: AgentMessageTypeNewIncident})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var emptyWire map[string]interface{}
	if err := json.Unmarshal(empty, &emptyWire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "max_tokens"} {
		if _, present := emptyWire[key]; present {
			t.Errorf("%s should be absent when unset, got %v", key, emptyWire[key])
		}
	}
}
