package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/gorilla/websocket"
)

// CodexMessageType represents the type of WebSocket message
type CodexMessageType string

const (
	// Messages from API to Codex Worker
	CodexMessageTypeNewIncident      CodexMessageType = "new_incident"
	CodexMessageTypeContinueIncident CodexMessageType = "continue_incident"
	CodexMessageTypeCancelIncident   CodexMessageType = "cancel_incident"

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

	// OpenAI settings (sent with new_incident)
	OpenAIAPIKey     string `json:"openai_api_key,omitempty"`
	Model            string `json:"model,omitempty"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
}

// OpenAISettings holds OpenAI configuration for Codex execution
type OpenAISettings struct {
	APIKey          string
	Model           string
	ReasoningEffort string
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

// IncidentCallback is called when an incident receives updates
type IncidentCallback struct {
	OnOutput    func(output string)
	OnCompleted func(sessionID, response string)
	OnError     func(errorMsg string)
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
	log.Printf("Incident %s completed with session %s", msg.IncidentID, msg.SessionID)

	// Call callback if registered
	h.callbackMu.RLock()
	callback, exists := h.callbacks[msg.IncidentID]
	h.callbackMu.RUnlock()

	if exists && callback.OnCompleted != nil {
		callback.OnCompleted(msg.SessionID, msg.Output)
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
			"status":       database.IncidentStatusCompleted,
			"session_id":   msg.SessionID,
			"response":     msg.Output,
			"completed_at": &now,
		}).Error; err != nil {
		log.Printf("Failed to update incident completion: %v", err)
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

// ErrWorkerNotConnected is returned when no worker is connected
var ErrWorkerNotConnected = &WorkerNotConnectedError{}

// WorkerNotConnectedError represents a worker not connected error
type WorkerNotConnectedError struct{}

func (e *WorkerNotConnectedError) Error() string {
	return "codex worker not connected"
}
