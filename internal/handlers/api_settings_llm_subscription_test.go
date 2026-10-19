package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

func TestLLMSubscriptionLifecycle(t *testing.T) {
	h := setupLLMHandlerTest(t)
	code, response := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm",
		`{"provider":"openai-codex","name":"Subscription","model":"gpt-5.5"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, response)
	}
	if response["uses_subscription"] != true || response["is_configured"] != true || response["api_key"] != "" {
		t.Fatalf("unexpected subscription configuration: %v", response)
	}
	id := uint(response["id"].(float64))
	path := fmt.Sprintf("/api/settings/llm/%d", id)
	code, response = decodeLLMConfig(t, h, http.MethodPut, path+"/activate", "")
	if code != http.StatusOK {
		t.Fatalf("activate: %d %v", code, response)
	}
	stored, err := database.GetLLMSettingsByID(id)
	if err != nil {
		t.Fatal(err)
	}
	worker := services.BuildLLMSettingsForWorker(stored)
	if worker == nil || worker.Provider != "openai-codex" || worker.APIKey != "" {
		t.Fatalf("subscription not forwarded: %+v", worker)
	}
	code, response = decodeLLMConfig(t, h, http.MethodPut, path, `{"api_key":"","base_url":"","model":"gpt-5.6-terra"}`)
	if code != http.StatusOK || response["enabled"] != true {
		t.Fatalf("empty credential fields must preserve subscription: %d %v", code, response)
	}
	for _, body := range []string{
		`{"api_key":"test-key"}`, `{"base_url":"https://example.com"}`, `{"model":" "}`,
	} {
		code, response = decodeLLMConfig(t, h, http.MethodPut, path, body)
		if code != http.StatusBadRequest {
			t.Errorf("update %s: %d %v", body, code, response)
		}
	}
}

func TestLLMSubscriptionRejectsIncompatibleConfiguration(t *testing.T) {
	h := setupLLMHandlerTest(t)
	for _, body := range []string{
		`{"provider":"openai-codex","name":"Invalid","model":"gpt-5.5","api_key":"test-key"}`,
		`{"provider":"openai-codex","name":"Invalid","model":"gpt-5.5","base_url":"https://example.com"}`,
		`{"provider":"openai-codex","name":"Invalid"}`,
		`{"provider":"claude-code","name":"Unsupported","model":"claude-sonnet-5"}`,
	} {
		code, response := decodeLLMConfig(t, h, http.MethodPost, "/api/settings/llm", body)
		if code != http.StatusBadRequest {
			t.Errorf("create %s: %d %v", body, code, response)
		}
	}
	configs, err := database.GetAllLLMSettings()
	if err != nil || len(configs) != 0 {
		t.Fatalf("invalid configuration persisted: %v %v", configs, err)
	}
}
