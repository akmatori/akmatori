package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/gorilla/websocket"
)

// CodexMessageType represents the type of WebSocket message
type CodexMessageType string

const (
	// Messages from API to Codex Worker
	CodexMessageTypeNewIncident       CodexMessageType = "new_incident"
	CodexMessageTypeContinueIncident  CodexMessageType = "continue_incident"
	CodexMessageTypeCancelIncident    CodexMessageType = "cancel_incident"
	CodexMessageTypeProxyConfigUpdate CodexMessageType = "proxy_config_update"

	// Messages from Codex Worker to API
	CodexMessageTypeCodexOutput    CodexMessageType = "codex_output"
	CodexMessageTypeCodexCompleted CodexMessageType = "codex_completed"
	CodexMessageTypeCodexError     CodexMessageType = "codex_error"
	CodexMessageTypeHeartbeat      CodexMessageType = "heartbeat"
	CodexMessageTypeStatus         CodexMessageType = "status"
)

// CodexMessage represents a WebSocket message between API and Codex worker
type CodexMessage struct {
	Type       CodexMessageType       `json:"type"`
	IncidentID string                 `json:"incident_id,omitempty"`
	Task       string                 `json:"task,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Output     string                 `json:"output,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`

	// Execution metrics (sent with codex_completed)
	TokensUsed      int   `json:"tokens_used,omitempty"`
	ExecutionTimeMs int64 `json:"execution_time_ms,omitempty"`

	// OpenAI settings (sent with new_incident)
	OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	ProxyURL        string `json:"proxy_url,omitempty"`
	NoProxy         string `json:"no_proxy,omitempty"`

	// Proxy configuration with toggles (sent with new_incident)
	ProxyConfig *ProxyConfig `json:"proxy_config,omitempty"`
}

// OpenAISettings holds OpenAI configuration for Codex execution
type OpenAISettings struct {
	APIKey          string
	Model           string
	ReasoningEffort string
	BaseURL         string
	ProxyURL        string
	NoProxy         string
}

// CodexWSHandler handles WebSocket connections from the Codex worker
type CodexWSHandler struct {
	upgrader    websocket.Upgrader
	mu          sync.RWMutex
	workerConn  *websocket.Conn
	workerReady bool
	callbacks   map[string]IncidentCallback // incident_id -> callback
	callbackMu  sync.RWMutex
}

// NewCodexWSHandler creates a new Codex WebSocket handler
func NewCodexWSHandler() *CodexWSHandler {
	return &CodexWSHandler{
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
func (h *CodexWSHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/codex", h.HandleWebSocket)
}

// HandleWebSocket handles the WebSocket connection from the Codex worker
func (h *CodexWSHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("failed to upgrade WebSocket", "err", err)
		return
	}

	slog.Info("Codex worker connected", "remote_addr", r.RemoteAddr)

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
		slog.Info("Codex worker disconnected")
	}()

	// Read messages from worker
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket read error", "err", err)
			}
			return
		}

		var msg CodexMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Error("failed to parse message", "err", err)
			continue
		}

		h.handleMessage(msg)
	}
}

// handleMessage processes incoming messages from the Codex worker
func (h *CodexWSHandler) handleMessage(msg CodexMessage) {
	slog.Info("received message from worker", "type", msg.Type, "incident_id", msg.IncidentID)

	switch msg.Type {
	case CodexMessageTypeHeartbeat:
		// Just a heartbeat, no action needed
		return

	case CodexMessageTypeStatus:
		// Worker status update
		if status, ok := msg.Data["status"].(string); ok {
			slog.Info("worker status", "status", status)
		}
		return

	case CodexMessageTypeCodexOutput:
		h.handleCodexOutput(msg)

	case CodexMessageTypeCodexCompleted:
		h.handleCodexCompleted(msg)

	case CodexMessageTypeCodexError:
		h.handleCodexError(msg)

	default:
		slog.Warn("unknown message type from worker", "type", msg.Type)
	}
}

// handleCodexOutput handles streaming output from Codex
func (h *CodexWSHandler) handleCodexOutput(msg CodexMessage) {
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnOutput != nil {
		// Let the callback handle database updates with proper context (task header, etc.)
		callback.OnOutput(msg.Output)
	} else {
		// No callback registered, update database directly as fallback
		if err := database.GetDB().Model(&database.Incident{}).
			Where("uuid = ?", msg.IncidentID).
			Update("full_log", msg.Output).Error; err != nil {
			slog.Error("failed to update incident log", "err", err)
		}
	}
}

// handleCodexCompleted handles completion notification from Codex
func (h *CodexWSHandler) handleCodexCompleted(msg CodexMessage) {
	slog.Info("incident completed", "incident_id", msg.IncidentID, "session_id", msg.SessionID, "tokens_used", msg.TokensUsed, "execution_time_ms", msg.ExecutionTimeMs)

	// Clean LLM "thinking" text (e.g. "Let me investigate...") from the response,
	// keeping only the structured summary starting from the first markdown heading.
	cleanedOutput := utils.CleanLLMResponse(msg.Output)

	// Append metrics to response (for display in reasoning log and Slack)
	executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
	responseWithMetrics := utils.AppendMetrics(cleanedOutput, executionTime, msg.TokensUsed)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnCompleted != nil {
		callback.OnCompleted(msg.SessionID, responseWithMetrics)
	}

	// Remove callback
	h.callbackMu.Lock()
	delete(h.callbacks, msg.IncidentID)
	h.callbackMu.Unlock()

	// Update incident in database
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
		slog.Error("failed to update incident completion", "err", err)
	}
}

// handleCodexError handles error notification from Codex
func (h *CodexWSHandler) handleCodexError(msg CodexMessage) {
	slog.Error("incident failed", "incident_id", msg.IncidentID, "err", msg.Error)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnError != nil {
		callback.OnError(msg.Error)
	}

	// Remove callback
	h.callbackMu.Lock()
	delete(h.callbacks, msg.IncidentID)
	h.callbackMu.Unlock()

	// Update incident in database
	now := time.Now()
	if err := database.GetDB().Model(&database.Incident{}).
		Where("uuid = ?", msg.IncidentID).
		Updates(map[string]interface{}{
			"status":       database.IncidentStatusFailed,
			"response":     msg.Error,
			"completed_at": &now,
		}).Error; err != nil {
		slog.Error("failed to update incident error", "err", err)
	}
}

// IsWorkerConnected returns whether a worker is connected
func (h *CodexWSHandler) IsWorkerConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workerReady && h.workerConn != nil
}

// SendToWorker sends a message to the Codex worker
func (h *CodexWSHandler) SendToWorker(msg CodexMessage) error {
	h.mu.RLock()
	conn := h.workerConn
	h.mu.RUnlock()

	if conn == nil {
		return ErrWorkerNotConnected
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

// StartIncident sends a new incident to the Codex worker
func (h *CodexWSHandler) StartIncident(incidentID, task string, openai *OpenAISettings, callback IncidentCallback) error {
	// Register callback
	h.callbackMu.Lock()
	h.callbacks[incidentID] = callback
	h.callbackMu.Unlock()

	// Send to worker
	msg := CodexMessage{
		Type:       CodexMessageTypeNewIncident,
		IncidentID: incidentID,
		Task:       task,
	}

	// Include OpenAI settings if provided
	if openai != nil {
		msg.OpenAIAPIKey = openai.APIKey
		msg.Model = openai.Model
		msg.ReasoningEffort = openai.ReasoningEffort
		msg.BaseURL = openai.BaseURL
		msg.ProxyURL = openai.ProxyURL
		msg.NoProxy = openai.NoProxy
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
func (h *CodexWSHandler) ContinueIncident(incidentID, sessionID, message string, callback IncidentCallback) error {
	// Register/update callback
	h.callbackMu.Lock()
	h.callbacks[incidentID] = callback
	h.callbackMu.Unlock()

	// Send to worker
	msg := CodexMessage{
		Type:       CodexMessageTypeContinueIncident,
		IncidentID: incidentID,
		SessionID:  sessionID,
		Message:    message,
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
func (h *CodexWSHandler) CancelIncident(incidentID string) error {
	msg := CodexMessage{
		Type:       CodexMessageTypeCancelIncident,
		IncidentID: incidentID,
	}

	return h.SendToWorker(msg)
}

// BroadcastProxyConfig sends proxy configuration to the connected worker
func (h *CodexWSHandler) BroadcastProxyConfig(settings *database.ProxySettings) error {
	h.mu.RLock()
	conn := h.workerConn
	h.mu.RUnlock()

	if conn == nil {
		return ErrWorkerNotConnected
	}

	msg := CodexMessage{
		Type: CodexMessageTypeProxyConfigUpdate,
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

// Note: ErrWorkerNotConnected is defined in agent_ws.go
