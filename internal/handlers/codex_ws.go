package handlers

import (
	"encoding/json"
	"log"
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
	CodexMessageTypeDeviceAuthStart   CodexMessageType = "device_auth_start"
	CodexMessageTypeDeviceAuthCancel  CodexMessageType = "device_auth_cancel"
	CodexMessageTypeProxyConfigUpdate CodexMessageType = "proxy_config_update"

	// Messages from Codex Worker to API
	CodexMessageTypeCodexOutput        CodexMessageType = "codex_output"
	CodexMessageTypeCodexCompleted     CodexMessageType = "codex_completed"
	CodexMessageTypeCodexError         CodexMessageType = "codex_error"
	CodexMessageTypeHeartbeat          CodexMessageType = "heartbeat"
	CodexMessageTypeStatus             CodexMessageType = "status"
	CodexMessageTypeDeviceAuthResponse CodexMessageType = "device_auth_response"
)

// ProxyConfig holds proxy configuration with per-service toggles
type ProxyConfig struct {
	URL           string `json:"url"`
	NoProxy       string `json:"no_proxy"`
	OpenAIEnabled bool   `json:"openai_enabled"`
	SlackEnabled  bool   `json:"slack_enabled"`
	ZabbixEnabled bool   `json:"zabbix_enabled"`
}

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

	// ChatGPT subscription auth fields (sent with new_incident)
	AuthMethod          string `json:"auth_method,omitempty"`
	ChatGPTAccessToken  string `json:"chatgpt_access_token,omitempty"`
	ChatGPTRefreshToken string `json:"chatgpt_refresh_token,omitempty"`
	ChatGPTIDToken      string `json:"chatgpt_id_token,omitempty"`
	ChatGPTExpiresAt    string `json:"chatgpt_expires_at,omitempty"`

	// Updated tokens (returned from worker if tokens were refreshed)
	UpdatedAccessToken  string `json:"updated_access_token,omitempty"`
	UpdatedRefreshToken string `json:"updated_refresh_token,omitempty"`
	UpdatedExpiresAt    string `json:"updated_expires_at,omitempty"`

	// Device auth fields (sent with device_auth_response)
	DeviceCode      string `json:"device_code,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	ExpiresIn       int    `json:"expires_in,omitempty"`
	AuthStatus      string `json:"auth_status,omitempty"` // "pending", "complete", "expired", "failed"
	AuthEmail       string `json:"auth_email,omitempty"`
	// Tokens returned when auth is complete
	AuthAccessToken  string `json:"auth_access_token,omitempty"`
	AuthRefreshToken string `json:"auth_refresh_token,omitempty"`
	AuthIDToken      string `json:"auth_id_token,omitempty"`
	AuthExpiresAt    string `json:"auth_expires_at,omitempty"`
}

// OpenAISettings holds OpenAI configuration for Codex execution
type OpenAISettings struct {
	APIKey          string
	Model           string
	ReasoningEffort string
	BaseURL         string
	ProxyURL        string
	NoProxy         string
	// ChatGPT subscription auth fields
	AuthMethod          string
	ChatGPTAccessToken  string
	ChatGPTRefreshToken string
	ChatGPTIDToken      string
	ChatGPTExpiresAt    string
}

// CodexWSHandler handles WebSocket connections from the Codex worker
type CodexWSHandler struct {
	upgrader           websocket.Upgrader
	mu                 sync.RWMutex
	workerConn         *websocket.Conn
	workerReady        bool
	callbacks          map[string]IncidentCallback // incident_id -> callback
	callbackMu         sync.RWMutex
	deviceAuthCallback DeviceAuthCallback
	deviceAuthMu       sync.RWMutex
}

// IncidentCallback is called when an incident receives updates
type IncidentCallback struct {
	OnOutput    func(output string)
	OnCompleted func(sessionID, response string)
	OnError     func(errorMsg string)
}

// DeviceAuthResult holds the result of device authentication
type DeviceAuthResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Status          string // "pending", "complete", "expired", "failed"
	Email           string
	AccessToken     string
	RefreshToken    string
	IDToken         string
	ExpiresAt       string
	Error           string
}

// DeviceAuthCallback is called when device auth receives updates
type DeviceAuthCallback func(result *DeviceAuthResult)

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
		log.Printf("Failed to upgrade WebSocket: %v", err)
		return
	}

	log.Printf("Codex worker connected from %s", r.RemoteAddr)

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
		log.Printf("Codex worker disconnected")
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

		var msg CodexMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Failed to parse message: %v", err)
			continue
		}

		h.handleMessage(msg)
	}
}

// handleMessage processes incoming messages from the Codex worker
func (h *CodexWSHandler) handleMessage(msg CodexMessage) {
	log.Printf("Received message from worker: type=%s incident=%s", msg.Type, msg.IncidentID)

	switch msg.Type {
	case CodexMessageTypeHeartbeat:
		// Just a heartbeat, no action needed
		return

	case CodexMessageTypeStatus:
		// Worker status update
		if status, ok := msg.Data["status"].(string); ok {
			log.Printf("Worker status: %s", status)
		}
		return

	case CodexMessageTypeCodexOutput:
		h.handleCodexOutput(msg)

	case CodexMessageTypeCodexCompleted:
		h.handleCodexCompleted(msg)

	case CodexMessageTypeCodexError:
		h.handleCodexError(msg)

	case CodexMessageTypeDeviceAuthResponse:
		h.handleDeviceAuthResponse(msg)

	default:
		log.Printf("Unknown message type from worker: %s", msg.Type)
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
			log.Printf("Failed to update incident log: %v", err)
		}
	}
}

// handleCodexCompleted handles completion notification from Codex
func (h *CodexWSHandler) handleCodexCompleted(msg CodexMessage) {
	log.Printf("Incident %s completed with session %s, tokens: %d, time: %dms",
		msg.IncidentID, msg.SessionID, msg.TokensUsed, msg.ExecutionTimeMs)

	// Append metrics to response (for display in reasoning log and Slack)
	executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
	responseWithMetrics := utils.AppendMetrics(msg.Output, executionTime, msg.TokensUsed)

	// Persist refreshed OAuth tokens if they were updated
	if msg.UpdatedAccessToken != "" || msg.UpdatedRefreshToken != "" {
		h.persistRefreshedTokens(msg)
	}

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
		log.Printf("Failed to update incident completion: %v", err)
	}
}

// persistRefreshedTokens saves refreshed OAuth tokens to the database
func (h *CodexWSHandler) persistRefreshedTokens(msg CodexMessage) {
	log.Printf("Persisting refreshed OAuth tokens for incident %s", msg.IncidentID)

	settings, err := database.GetOpenAISettings()
	if err != nil {
		log.Printf("Failed to get OpenAI settings for token refresh: %v", err)
		return
	}

	// Only update if we're using ChatGPT subscription auth
	if settings.AuthMethod != database.AuthMethodChatGPTSubscription {
		return
	}

	// Update tokens
	if msg.UpdatedAccessToken != "" {
		settings.ChatGPTAccessToken = msg.UpdatedAccessToken
	}
	if msg.UpdatedRefreshToken != "" {
		settings.ChatGPTRefreshToken = msg.UpdatedRefreshToken
	}
	if msg.UpdatedExpiresAt != "" {
		// Parse the expires_at timestamp
		if t, err := time.Parse(time.RFC3339, msg.UpdatedExpiresAt); err == nil {
			settings.ChatGPTExpiresAt = &t
		}
	}

	if err := database.UpdateOpenAIChatGPTTokens(settings); err != nil {
		log.Printf("Failed to persist refreshed tokens: %v", err)
	} else {
		log.Printf("Successfully persisted refreshed OAuth tokens")
	}
}

// handleDeviceAuthResponse handles device auth response from worker
func (h *CodexWSHandler) handleDeviceAuthResponse(msg CodexMessage) {
	log.Printf("Device auth response: status=%s, code=%s", msg.AuthStatus, msg.UserCode)

	h.deviceAuthMu.RLock()
	callback := h.deviceAuthCallback
	h.deviceAuthMu.RUnlock()

	if callback != nil {
		result := &DeviceAuthResult{
			DeviceCode:      msg.DeviceCode,
			UserCode:        msg.UserCode,
			VerificationURL: msg.VerificationURL,
			ExpiresIn:       msg.ExpiresIn,
			Status:          msg.AuthStatus,
			Email:           msg.AuthEmail,
			AccessToken:     msg.AuthAccessToken,
			RefreshToken:    msg.AuthRefreshToken,
			IDToken:         msg.AuthIDToken,
			ExpiresAt:       msg.AuthExpiresAt,
			Error:           msg.Error,
		}
		callback(result)
	}

	// Clear callback if auth is complete or failed
	if msg.AuthStatus == "complete" || msg.AuthStatus == "failed" || msg.AuthStatus == "expired" {
		h.deviceAuthMu.Lock()
		h.deviceAuthCallback = nil
		h.deviceAuthMu.Unlock()
	}
}

// handleCodexError handles error notification from Codex
func (h *CodexWSHandler) handleCodexError(msg CodexMessage) {
	log.Printf("Incident %s failed: %s", msg.IncidentID, msg.Error)

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
		log.Printf("Failed to update incident error: %v", err)
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
		// ChatGPT subscription auth fields
		msg.AuthMethod = openai.AuthMethod
		msg.ChatGPTAccessToken = openai.ChatGPTAccessToken
		msg.ChatGPTRefreshToken = openai.ChatGPTRefreshToken
		msg.ChatGPTIDToken = openai.ChatGPTIDToken
		msg.ChatGPTExpiresAt = openai.ChatGPTExpiresAt
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

// StartDeviceAuth initiates device authentication via the codex worker
// Accepts optional OpenAI settings for proxy configuration
func (h *CodexWSHandler) StartDeviceAuth(callback DeviceAuthCallback, settings *OpenAISettings) error {
	// Store callback
	h.deviceAuthMu.Lock()
	h.deviceAuthCallback = callback
	h.deviceAuthMu.Unlock()

	// Send to worker with proxy settings if configured
	msg := CodexMessage{
		Type: CodexMessageTypeDeviceAuthStart,
	}

	// Include proxy settings if available
	if settings != nil {
		msg.BaseURL = settings.BaseURL
		msg.ProxyURL = settings.ProxyURL
		msg.NoProxy = settings.NoProxy
	}

	if err := h.SendToWorker(msg); err != nil {
		// Clear callback on error
		h.deviceAuthMu.Lock()
		h.deviceAuthCallback = nil
		h.deviceAuthMu.Unlock()
		return err
	}

	return nil
}

// CancelDeviceAuth cancels an ongoing device authentication
func (h *CodexWSHandler) CancelDeviceAuth() error {
	// Clear callback
	h.deviceAuthMu.Lock()
	h.deviceAuthCallback = nil
	h.deviceAuthMu.Unlock()

	// Send cancel to worker
	msg := CodexMessage{
		Type: CodexMessageTypeDeviceAuthCancel,
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

// ErrWorkerNotConnected is returned when no worker is connected
var ErrWorkerNotConnected = &WorkerNotConnectedError{}

// WorkerNotConnectedError represents a worker not connected error
type WorkerNotConnectedError struct{}

func (e *WorkerNotConnectedError) Error() string {
	return "codex worker not connected"
}
