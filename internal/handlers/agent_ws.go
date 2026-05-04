package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/google/uuid"
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
	AgentMessageTypeOneshotLLMRequest AgentMessageType = "oneshot_llm_request"

	// Messages from Agent Worker to API
	AgentMessageTypeAgentOutput        AgentMessageType = "agent_output"
	AgentMessageTypeAgentCompleted     AgentMessageType = "agent_completed"
	AgentMessageTypeAgentError         AgentMessageType = "agent_error"
	AgentMessageTypeHeartbeat          AgentMessageType = "heartbeat"
	AgentMessageTypeStatus             AgentMessageType = "status"
	AgentMessageTypeOneshotLLMResponse AgentMessageType = "oneshot_llm_response"
)

// oneshotLLMDefaultTimeout is used when callers pass a context with no deadline.
const oneshotLLMDefaultTimeout = 60 * time.Second

// ProxyConfig holds proxy configuration with per-service toggles
type ProxyConfig struct {
	URL                    string `json:"url"`
	NoProxy                string `json:"no_proxy"`
	LLMEnabled             bool   `json:"llm_enabled"`
	SlackEnabled           bool   `json:"slack_enabled"`
	ZabbixEnabled          bool   `json:"zabbix_enabled"`
	VictoriaMetricsEnabled bool   `json:"victoria_metrics_enabled"`
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
	APIKey        string `json:"api_key,omitempty"`
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"thinking_level,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`

	// Proxy configuration with toggles (sent with new_incident)
	ProxyConfig *ProxyConfig `json:"proxy_config,omitempty"`

	// Enabled skill names (sent with new_incident to filter skill discovery)
	EnabledSkills []string `json:"enabled_skills,omitempty"`

	// Tool allowlist (sent with new_incident to restrict tool access)
	ToolAllowlist []services.ToolAllowlistEntry `json:"tool_allowlist,omitempty"`

	// One-shot LLM request/response correlation fields
	RequestID   string  `json:"request_id,omitempty"`
	System      string  `json:"system,omitempty"`
	User        string  `json:"user,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Summary     string  `json:"summary,omitempty"`
}

// LLMSettingsForWorker is re-exported from services so handler code that
// referenced it before the lift continues to compile unchanged. The canonical
// definition (and the OneShotLLMCaller interface that consumes it) lives in
// internal/services/llm_settings.go.
type LLMSettingsForWorker = services.LLMSettingsForWorker

// AgentWSHandler handles WebSocket connections from the agent worker
type AgentWSHandler struct {
	upgrader         websocket.Upgrader
	mu               sync.RWMutex
	workerConn       *websocket.Conn
	workerReady      bool
	callbacks        map[string]IncidentCallback // incident_id -> callback
	callbackMu       sync.RWMutex
	pendingOneshot   map[string]chan *AgentMessage // request_id -> response channel
	pendingOneshotMu sync.Mutex
}

// IncidentCallback is called when an incident receives updates
type IncidentCallback struct {
	OnOutput    func(output string)
	OnCompleted func(sessionID, response string, tokensUsed int, executionTimeMs int64)
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
		callbacks:      make(map[string]IncidentCallback),
		pendingOneshot: make(map[string]chan *AgentMessage),
	}
}

// SetupRoutes configures WebSocket routes
func (h *AgentWSHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/agent", h.HandleWebSocket)
}

// HandleWebSocket handles the WebSocket connection from the agent worker
func (h *AgentWSHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("failed to upgrade WebSocket", "err", err)
		return
	}

	slog.Info("agent worker connected", "remote_addr", r.RemoteAddr)

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

		// Notify any in-flight oneshot LLM callers that the worker dropped so
		// they fail fast (with ErrWorkerNotConnected) instead of blocking until
		// their context deadline.
		h.failPendingOneshot(ErrWorkerNotConnected.Error())

		slog.Info("agent worker disconnected")
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

		var msg AgentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Error("failed to parse message", "err", err)
			continue
		}

		h.handleMessage(msg)
	}
}

// handleMessage processes incoming messages from the agent worker
func (h *AgentWSHandler) handleMessage(msg AgentMessage) {
	slog.Info("received message from worker", "type", msg.Type, "incident_id", msg.IncidentID)

	switch msg.Type {
	case AgentMessageTypeHeartbeat:
		// Just a heartbeat, no action needed
		return

	case AgentMessageTypeStatus:
		// Worker status update
		if status, ok := msg.Data["status"].(string); ok {
			slog.Info("worker status", "status", status)
		}
		return

	case AgentMessageTypeAgentOutput:
		h.handleAgentOutput(msg)

	case AgentMessageTypeAgentCompleted:
		h.handleAgentCompleted(msg)

	case AgentMessageTypeAgentError:
		h.handleAgentError(msg)

	case AgentMessageTypeOneshotLLMResponse:
		h.handleOneshotLLMResponse(msg)

	default:
		slog.Warn("unknown message type from worker", "type", msg.Type)
	}
}

// failPendingOneshot delivers an error response to every waiting oneshot
// caller. Used on worker disconnect so callers do not block until their
// context deadline. Each pending channel is buffered=1 and only ever receives
// one response, so a non-blocking send is sufficient.
func (h *AgentWSHandler) failPendingOneshot(errMsg string) {
	h.pendingOneshotMu.Lock()
	pending := h.pendingOneshot
	h.pendingOneshot = make(map[string]chan *AgentMessage)
	h.pendingOneshotMu.Unlock()

	for requestID, ch := range pending {
		resp := &AgentMessage{
			Type:      AgentMessageTypeOneshotLLMResponse,
			RequestID: requestID,
			Error:     errMsg,
		}
		select {
		case ch <- resp:
		default:
		}
	}
}

// handleOneshotLLMResponse routes a oneshot LLM response back to the waiting caller.
// Drops silently (debug-logged) if no listener is registered for the request_id.
func (h *AgentWSHandler) handleOneshotLLMResponse(msg AgentMessage) {
	h.pendingOneshotMu.Lock()
	ch, exists := h.pendingOneshot[msg.RequestID]
	h.pendingOneshotMu.Unlock()

	if !exists {
		slog.Debug("dropping oneshot llm response with no listener", "request_id", msg.RequestID)
		return
	}

	// Make a heap copy so the channel reader sees a stable value.
	respCopy := msg
	select {
	case ch <- &respCopy:
	default:
		slog.Debug("dropping oneshot llm response: channel full or closed", "request_id", msg.RequestID)
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
			slog.Error("failed to update incident log", "err", err)
		}
	}
}

// handleAgentCompleted handles completion notification from the agent
func (h *AgentWSHandler) handleAgentCompleted(msg AgentMessage) {
	slog.Info("incident completed", "incident_id", msg.IncidentID, "session_id", msg.SessionID, "tokens_used", msg.TokensUsed, "execution_time_ms", msg.ExecutionTimeMs)

	// Append metrics to response (for display in reasoning log and Slack)
	executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
	responseWithMetrics := utils.AppendMetrics(msg.Output, executionTime, msg.TokensUsed)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnCompleted != nil {
		callback.OnCompleted(msg.SessionID, responseWithMetrics, msg.TokensUsed, msg.ExecutionTimeMs)
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
			slog.Error("failed to update incident completion", "err", err)
		}
	}

	// Remove callback
	h.callbackMu.Lock()
	delete(h.callbacks, msg.IncidentID)
	h.callbackMu.Unlock()
}

// handleAgentError handles error notification from the agent
func (h *AgentWSHandler) handleAgentError(msg AgentMessage) {
	slog.Error("incident failed", "incident_id", msg.IncidentID, "err", msg.Error)

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
			slog.Error("failed to update incident error", "err", err)
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
func (h *AgentWSHandler) StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) error {
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
		ToolAllowlist: toolAllowlist,
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
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
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
func (h *AgentWSHandler) ContinueIncident(incidentID, sessionID, message string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) error {
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
		ToolAllowlist: toolAllowlist,
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
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
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

// OneShotLLM sends a one-shot LLM request to the agent worker and waits for a response.
// Correlates request and response via a generated request_id. Returns ErrWorkerNotConnected
// when no worker is connected. If ctx has no deadline, applies oneshotLLMDefaultTimeout.
func (h *AgentWSHandler) OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	if !h.IsWorkerConnected() {
		return "", ErrWorkerNotConnected
	}

	requestID := uuid.New().String()
	ch := make(chan *AgentMessage, 1)

	h.pendingOneshotMu.Lock()
	h.pendingOneshot[requestID] = ch
	h.pendingOneshotMu.Unlock()

	defer func() {
		h.pendingOneshotMu.Lock()
		delete(h.pendingOneshot, requestID)
		h.pendingOneshotMu.Unlock()
	}()

	msg := AgentMessage{
		Type:        AgentMessageTypeOneshotLLMRequest,
		RequestID:   requestID,
		System:      system,
		User:        user,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Reuse the same proxy-settings pattern as StartIncident/ContinueIncident.
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
		}
	}

	if err := h.SendToWorker(msg); err != nil {
		return "", err
	}

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, oneshotLLMDefaultTimeout)
		defer cancel()
	}

	select {
	case resp := <-ch:
		if resp.Error != "" {
			return "", errors.New(resp.Error)
		}
		return resp.Summary, nil
	case <-waitCtx.Done():
		return "", waitCtx.Err()
	}
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
			URL:                    settings.ProxyURL,
			NoProxy:                settings.NoProxy,
			LLMEnabled:             settings.LLMEnabled,
			SlackEnabled:           settings.SlackEnabled,
			ZabbixEnabled:          settings.ZabbixEnabled,
			VictoriaMetricsEnabled: settings.VictoriaMetricsEnabled,
		},
	}

	return h.SendToWorker(msg)
}

// BuildLLMSettingsForWorker is a thin re-export of the canonical implementation
// in services so handler-side callers continue to work after the type lift.
var BuildLLMSettingsForWorker = services.BuildLLMSettingsForWorker

// ErrWorkerNotConnected is re-exported from services so existing handler-side
// callers continue to compile after the lift.
var ErrWorkerNotConnected = services.ErrWorkerNotConnected
