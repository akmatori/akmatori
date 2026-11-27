package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestCodexConfigUsesWorkerConnection(t *testing.T) {
	worker, conn, cleanup := setupOneshotTest(t)
	defer cleanup()
	if err := database.DB.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatal(err)
	}
	h := &APIHandler{agentWSHandler: worker}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings/llm", h.handleLLMSettings)
	mux.HandleFunc("/api/settings/llm/codex-auth", h.handleCodexAuth)
	mux.HandleFunc("/api/settings/llm/", h.handleLLMSettingsByID)

	go func() {
		var signedIn bool
		for {
			var msg AgentMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			response := AgentMessage{
				Type: AgentMessageTypeCodexAuthResponse, RequestID: msg.RequestID,
				CodexAuth: &CodexAuthStatus{},
			}
			switch msg.CodexAuthAction {
			case "start":
				response.CodexAuth.Pending = true
				response.CodexAuth.UserCode = "TEST-123"
				signedIn = true // Simulate approval before the next request.
			case "disconnect":
				signedIn = false
			case "status":
				response.CodexAuth.Connected = signedIn
			}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
		}
	}()

	request := func(method, route, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/settings/llm"+route, strings.NewReader(body))
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, route, w.Code, want, w.Body)
		}
		return w
	}

	created := request(http.MethodPost, "", `{"name":"Subscription","provider":"openai-codex"}`, http.StatusCreated)
	var config struct {
		ID           uint   `json:"id"`
		APIKey       string `json:"api_key"`
		IsConfigured bool   `json:"is_configured"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config.ID == 0 || config.APIKey != "" || config.IsConfigured {
		t.Fatalf("unexpected disconnected configuration: %+v", config)
	}
	path := fmt.Sprintf("/%d", config.ID)
	request(http.MethodPut, path+"/activate", "", http.StatusBadRequest)
	request(http.MethodPut, path, `{"base_url":"https://example.com"}`, http.StatusBadRequest)

	start := request(http.MethodPost, "/codex-auth", "", http.StatusOK)
	if start.Header().Get("Cache-Control") != "no-store" || !strings.Contains(start.Body.String(), "TEST-123") {
		t.Fatalf("unexpected sign-in response: %s", start.Body)
	}
	request(http.MethodPut, path+"/activate", "", http.StatusOK)
	settings, err := database.GetLLMSettings()
	if err != nil {
		t.Fatal(err)
	}
	if dispatch := BuildLLMSettingsForWorker(settings); dispatch == nil || dispatch.Provider != "openai-codex" || dispatch.APIKey != "" {
		t.Fatalf("unexpected worker settings: %+v", dispatch)
	}

	request(http.MethodDelete, "/codex-auth", "", http.StatusOK)
	get := request(http.MethodGet, path, "", http.StatusOK)
	if err := json.Unmarshal(get.Body.Bytes(), &config); err != nil || config.IsConfigured {
		t.Fatalf("configuration should be disconnected: %s (%v)", get.Body, err)
	}
}
