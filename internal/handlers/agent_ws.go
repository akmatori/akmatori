package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// AgentMessageType represents the type of WebSocket message
type AgentMessageType string

const (
	// Messages from API to Agent Worker
	AgentMessageTypeNewIncident       AgentMessageType = "new_incident"
	AgentMessageTypeContinueIncident  AgentMessageType = "continue_incident"
	AgentMessageTypeCancelIncident    AgentMessageType = "cancel_incident"
	AgentMessageTypeProxyConfigUpdate AgentMessageType = "proxy_config_update"

	// Messages from Agent Worker to API
	AgentMessageTypeAgentOutput    AgentMessageType = "codex_output"
	AgentMessageTypeAgentCompleted AgentMessageType = "codex_completed"
	AgentMessageTypeAgentError     AgentMessageType = "codex_error"
	AgentMessageTypeHeartbeat      AgentMessageType = "heartbeat"
	AgentMessageTypeStatus         AgentMessageType = "status"
)

// ProxyConfig holds proxy configuration with per-service toggles
type ProxyConfig struct {
	URL           string `json:"url"`
	NoProxy       string `json:"no_proxy"`
	OpenAIEnabled bool   `json:"openai_enabled"`
	SlackEnabled  bool   `json:"slack_enabled"`
	ZabbixEnabled bool   `json:"zabbix_enabled"`
}

// AgentMessage represents a WebSocket message between API and agent worker
type AgentMessage struct {
	Type       AgentMessageType       `json:"type"`
	IncidentID string                 `json:"incident_id,omitempty"`
	Task       string                 `json:"task,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Output     string                 `json:"output,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`

	// Execution metrics (sent with agent_completed)
	TokensUsed      int   `json:"tokens_used,omitempty"`
	ExecutionTimeMs int64 `json:"execution_time_ms,omitempty"`

	// LLM settings (sent with new_incident)
	Provider      string `json:"provider,omitempty"`
	APIKey        string `json:"openai_api_key,omitempty"` // Keep wire format for backward compat with agent-worker
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"reasoning_effort,omitempty"` // Keep wire format for backward compat
	BaseURL       string `json:"base_url,omitempty"`

	// Proxy configuration with toggles (sent with new_incident)
	ProxyConfig *ProxyConfig `json:"proxy_config,omitempty"`

	// Enabled skill names (sent with new_incident to filter skill discovery)
	EnabledSkills []string `json:"enabled_skills,omitempty"`
}

// LLMSettingsForWorker holds LLM configuration for agent execution
type LLMSettingsForWorker struct {
	Provider      string
	APIKey        string
	Model         string
	ThinkingLevel string
	BaseURL       string
}

// AgentWSHandler handles WebSocket connections from the agent worker
type AgentWSHandler struct {
	upgrader    websocket.Upgrader
	mu          sync.RWMutex
	workerConn  *websocket.Conn
	workerReady bool
	callbacks   map[string]IncidentCallback // incident_id -> callback
	callbackMu  sync.RWMutex
}

// IncidentCallback is called when an incident receives updates
type IncidentCallback struct {
	OnOutput    func(output string)
	OnCompleted func(sessionID, response string)
	OnError     func(errorMsg string)
}

// NewAgentWSHandler creates a new agent WebSocket handler
func NewAgentWSHandler() *AgentWSHandler {
	return &AgentWSHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for internal communication
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		callbacks: make(map[string]IncidentCallback),
	}
}

// SetupRoutes configures WebSocket routes
// Keep endpoint as /ws/codex for now to avoid config churn
func (h *AgentWSHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/codex", h.HandleWebSocket)
}

// HandleWebSocket handles the WebSocket connection from the agent worker
func (h *AgentWSHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade WebSocket: %v", err)
		return
	}

	log.Printf("Agent worker connected from %s", r.RemoteAddr)

	// Store the worker connection
	h.mu.Lock()
	if h.workerConn != nil {
		// Close existing connection
		h.workerConn.Close()
	}
	h.workerConn = conn
	h.workerReady = true
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		if h.workerConn == conn {
			h.workerConn = nil
			h.workerReady = false
		}
		h.mu.Unlock()
		conn.Close()
		log.Printf("Agent worker disconnected")
	}()

	// Read messages from worker
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		var msg AgentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Failed to parse message: %v", err)
			continue
		}

		h.handleMessage(msg)
	}
}

// handleMessage processes incoming messages from the agent worker
func (h *AgentWSHandler) handleMessage(msg AgentMessage) {
	log.Printf("Received message from worker: type=%s incident=%s", msg.Type, msg.IncidentID)

	switch msg.Type {
	case AgentMessageTypeHeartbeat:
		// Just a heartbeat, no action needed
		return

	case AgentMessageTypeStatus:
		// Worker status update
		if status, ok := msg.Data["status"].(string); ok {
			log.Printf("Worker status: %s", status)
		}
		return

	case AgentMessageTypeAgentOutput:
		h.handleAgentOutput(msg)

	case AgentMessageTypeAgentCompleted:
		h.handleAgentCompleted(msg)

	case AgentMessageTypeAgentError:
		h.handleAgentError(msg)

	default:
		log.Printf("Unknown message type from worker: %s", msg.Type)
	}
}

// handleAgentOutput handles streaming output from the agent
func (h *AgentWSHandler) handleAgentOutput(msg AgentMessage) {
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnOutput != nil {
		callback.OnOutput(msg.Output)
	} else {
		// No callback registered, append to database directly as fallback
		if err := database.GetDB().Model(&database.Incident{}).
			Where("uuid = ?", msg.IncidentID).
			Update("full_log", gorm.Expr("COALESCE(full_log, '') || ?", msg.Output)).Error; err != nil {
			log.Printf("Failed to update incident log: %v", err)
		}
	}
}

// handleAgentCompleted handles completion notification from the agent
func (h *AgentWSHandler) handleAgentCompleted(msg AgentMessage) {
	log.Printf("Incident %s completed with session %s, tokens: %d, time: %dms",
		msg.IncidentID, msg.SessionID, msg.TokensUsed, msg.ExecutionTimeMs)

	// Append metrics to response (for display in reasoning log and Slack)
	executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
	responseWithMetrics := utils.AppendMetrics(msg.Output, executionTime, msg.TokensUsed)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnCompleted != nil {
		callback.OnCompleted(msg.SessionID, responseWithMetrics)
	} else {
		// No callback registered, update database directly as fallback
		now := time.Now()
		if err := database.GetDB().Model(&database.Incident{}).
			Where("uuid = ?", msg.IncidentID).
			Updates(map[string]interface{}{
				"status":            database.IncidentStatusCompleted,
				"session_id":        msg.SessionID,
				"response":          responseWithMetrics,
				"tokens_used":       msg.TokensUsed,
				"execution_time_ms": msg.ExecutionTimeMs,
				"completed_at":      &now,
			}).Error; err != nil {
			log.Printf("Failed to update incident completion: %v", err)
		}
	}

	// Remove callback
	h.callbackMu.Lock()
	delete(h.callbacks, msg.IncidentID)
	h.callbackMu.Unlock()
}

// handleAgentError handles error notification from the agent
func (h *AgentWSHandler) handleAgentError(msg AgentMessage) {
	log.Printf("Incident %s failed: %s", msg.IncidentID, msg.Error)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnError != nil {
		callback.OnError(msg.Error)
	} else {
		// No callback registered, update database directly as fallback
		now := time.Now()
		if err := database.GetDB().Model(&database.Incident{}).
			Where("uuid = ?", msg.IncidentID).
			Updates(map[string]interface{}{
				"status":       database.IncidentStatusFailed,
				"response":     msg.Error,
				"completed_at": &now,
			}).Error; err != nil {
			log.Printf("Failed to update incident error: %v", err)
		}
	}

	// Remove callback
	h.callbackMu.Lock()
	delete(h.callbacks, msg.IncidentID)
	h.callbackMu.Unlock()
}

// IsWorkerConnected returns whether a worker is connected
func (h *AgentWSHandler) IsWorkerConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workerReady && h.workerConn != nil
}

// SendToWorker sends a message to the agent worker
func (h *AgentWSHandler) SendToWorker(msg AgentMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.workerConn == nil {
		return ErrWorkerNotConnected
	}
	return h.workerConn.WriteMessage(websocket.TextMessage, data)
}

// StartIncident sends a new incident to the agent worker
func (h *AgentWSHandler) StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, callback IncidentCallback) error {
	// Register callback
	h.callbackMu.Lock()
	h.callbacks[incidentID] = callback
	h.callbackMu.Unlock()

	// Send to worker
	msg := AgentMessage{
		Type:          AgentMessageTypeNewIncident,
		IncidentID:    incidentID,
		Task:          task,
		EnabledSkills: enabledSkills,
	}

	// Include LLM settings if provided
	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Fetch proxy settings from database and include in message
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:           proxySettings.ProxyURL,
			NoProxy:       proxySettings.NoProxy,
			OpenAIEnabled: proxySettings.OpenAIEnabled,
			SlackEnabled:  proxySettings.SlackEnabled,
			ZabbixEnabled: proxySettings.ZabbixEnabled,
		}
	}

	if err := h.SendToWorker(msg); err != nil {
		// Remove callback on error
		h.callbackMu.Lock()
		delete(h.callbacks, incidentID)
		h.callbackMu.Unlock()
		return err
	}

	return nil
}

// ContinueIncident sends a follow-up message to an existing incident
func (h *AgentWSHandler) ContinueIncident(incidentID, sessionID, message string, llm *LLMSettingsForWorker, enabledSkills []string, callback IncidentCallback) error {
	// Register/update callback
	h.callbackMu.Lock()
	h.callbacks[incidentID] = callback
	h.callbackMu.Unlock()

	// Send to worker
	msg := AgentMessage{
		Type:          AgentMessageTypeContinueIncident,
		IncidentID:    incidentID,
		SessionID:     sessionID,
		Message:       message,
		EnabledSkills: enabledSkills,
	}

	// Include LLM settings so the worker can authenticate with the provider
	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Fetch proxy settings from database and include in message
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:           proxySettings.ProxyURL,
			NoProxy:       proxySettings.NoProxy,
			OpenAIEnabled: proxySettings.OpenAIEnabled,
			SlackEnabled:  proxySettings.SlackEnabled,
			ZabbixEnabled: proxySettings.ZabbixEnabled,
		}
	}

	if err := h.SendToWorker(msg); err != nil {
		// Remove callback on error
		h.callbackMu.Lock()
		delete(h.callbacks, incidentID)
		h.callbackMu.Unlock()
		return err
	}

	return nil
}

// CancelIncident sends a cancellation request to the worker
func (h *AgentWSHandler) CancelIncident(incidentID string) error {
	msg := AgentMessage{
		Type:       AgentMessageTypeCancelIncident,
		IncidentID: incidentID,
	}

	return h.SendToWorker(msg)
}

// BroadcastProxyConfig sends proxy configuration to the connected worker
func (h *AgentWSHandler) BroadcastProxyConfig(settings *database.ProxySettings) error {
	h.mu.RLock()
	conn := h.workerConn
	h.mu.RUnlock()

	if conn == nil {
		return ErrWorkerNotConnected
	}

	msg := AgentMessage{
		Type: AgentMessageTypeProxyConfigUpdate,
		ProxyConfig: &ProxyConfig{
			URL:           settings.ProxyURL,
			NoProxy:       settings.NoProxy,
			OpenAIEnabled: settings.OpenAIEnabled,
			SlackEnabled:  settings.SlackEnabled,
			ZabbixEnabled: settings.ZabbixEnabled,
		},
	}

	return h.SendToWorker(msg)
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

// ErrWorkerNotConnected is returned when no worker is connected
var ErrWorkerNotConnected = errors.New("agent worker not connected")
