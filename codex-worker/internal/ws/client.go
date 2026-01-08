package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// Messages from API to Codex Worker
	MessageTypeNewIncident      MessageType = "new_incident"
	MessageTypeContinueIncident MessageType = "continue_incident"
	MessageTypeCancelIncident   MessageType = "cancel_incident"

	// Messages from Codex Worker to API
	MessageTypeCodexOutput    MessageType = "codex_output"
	MessageTypeCodexCompleted MessageType = "codex_completed"
	MessageTypeCodexError     MessageType = "codex_error"
	MessageTypeHeartbeat      MessageType = "heartbeat"
	MessageTypeStatus         MessageType = "status"
)

// Message represents a WebSocket message
type Message struct {
	Type       MessageType            `json:"type"`
	IncidentID string                 `json:"incident_id,omitempty"`
	Task       string                 `json:"task,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Output     string                 `json:"output,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`

	// OpenAI settings (received with new_incident)
	OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	ProxyURL        string `json:"proxy_url,omitempty"`
	NoProxy         string `json:"no_proxy,omitempty"`
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

// Client represents a WebSocket client
type Client struct {
	url       string
	conn      *websocket.Conn
	logger    *log.Logger
	mu        sync.Mutex
	done      chan struct{}
	doneClosed bool
	onMessage func(Message)
	connected bool
}

// NewClient creates a new WebSocket client
func NewClient(url string, logger *log.Logger) *Client {
	return &Client{
		url:    url,
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Connect establishes a WebSocket connection
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.logger.Printf("Connecting to WebSocket: %s", c.url)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		return err
	}

	c.conn = conn
	c.connected = true
	c.logger.Println("WebSocket connected")

	return nil
}

// SetMessageHandler sets the callback for incoming messages
func (c *Client) SetMessageHandler(handler func(Message)) {
	c.onMessage = handler
}

// ReadLoop reads messages from the WebSocket connection
func (c *Client) ReadLoop() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.done:
			return
		default:
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					c.logger.Printf("WebSocket read error: %v", err)
				}
				return
			}

			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil {
				c.logger.Printf("Failed to parse message: %v", err)
				continue
			}

			if c.onMessage != nil {
				c.onMessage(msg)
			}
		}
	}
}

// Send sends a message through the WebSocket
func (c *Client) Send(msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return websocket.ErrCloseSent
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendOutput sends Codex output to the API
func (c *Client) SendOutput(incidentID, output string) error {
	return c.Send(Message{
		Type:       MessageTypeCodexOutput,
		IncidentID: incidentID,
		Output:     output,
	})
}

// SendCompleted sends completion notification
func (c *Client) SendCompleted(incidentID, sessionID, response string) error {
	return c.Send(Message{
		Type:       MessageTypeCodexCompleted,
		IncidentID: incidentID,
		SessionID:  sessionID,
		Output:     response,
	})
}

// SendError sends an error notification
func (c *Client) SendError(incidentID, errorMsg string) error {
	return c.Send(Message{
		Type:       MessageTypeCodexError,
		IncidentID: incidentID,
		Error:      errorMsg,
	})
}

// SendHeartbeat sends a heartbeat message
func (c *Client) SendHeartbeat() error {
	return c.Send(Message{
		Type: MessageTypeHeartbeat,
	})
}

// Close closes the WebSocket connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close done channel only once
	if !c.doneClosed {
		close(c.done)
		c.doneClosed = true
	}

	c.connected = false

	if c.conn != nil {
		err := c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		if err != nil {
			c.logger.Printf("Error sending close message: %v", err)
		}
		return c.conn.Close()
	}
	return nil
}

// Reset resets the client for reconnection
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.done = make(chan struct{})
	c.doneClosed = false
	c.connected = false
	c.conn = nil
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// StartHeartbeat starts sending periodic heartbeats
func (c *Client) StartHeartbeat(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				if c.IsConnected() {
					if err := c.SendHeartbeat(); err != nil {
						c.logger.Printf("Heartbeat failed: %v", err)
					}
				}
			}
		}
	}()
}
