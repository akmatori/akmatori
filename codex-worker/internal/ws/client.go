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
	MessageTypeDeviceAuthStart  MessageType = "device_auth_start"
	MessageTypeDeviceAuthCancel MessageType = "device_auth_cancel"

	// Messages from Codex Worker to API
	MessageTypeCodexOutput        MessageType = "codex_output"
	MessageTypeCodexCompleted     MessageType = "codex_completed"
	MessageTypeCodexError         MessageType = "codex_error"
	MessageTypeHeartbeat          MessageType = "heartbeat"
	MessageTypeStatus             MessageType = "status"
	MessageTypeDeviceAuthResponse MessageType = "device_auth_response"
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

	// Execution metrics (sent with codex_completed)
	TokensUsed      int   `json:"tokens_used,omitempty"`
	ExecutionTimeMs int64 `json:"execution_time_ms,omitempty"`

	// OpenAI settings (received with new_incident)
	OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	ProxyURL        string `json:"proxy_url,omitempty"`
	NoProxy         string `json:"no_proxy,omitempty"`

	// Proxy configuration with toggles (received with new_incident)
	ProxyConfig *ProxyConfig `json:"proxy_config,omitempty"`

	// ChatGPT subscription auth fields (received with new_incident)
	AuthMethod          string `json:"auth_method,omitempty"`
	ChatGPTAccessToken  string `json:"chatgpt_access_token,omitempty"`
	ChatGPTRefreshToken string `json:"chatgpt_refresh_token,omitempty"`
	ChatGPTIDToken      string `json:"chatgpt_id_token,omitempty"`
	ChatGPTExpiresAt    string `json:"chatgpt_expires_at,omitempty"`

	// Updated tokens (sent with codex_completed if tokens were refreshed)
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

// ProxyConfig holds proxy configuration with per-service toggles
type ProxyConfig struct {
	URL           string `json:"url"`
	NoProxy       string `json:"no_proxy"`
	OpenAIEnabled bool   `json:"openai_enabled"`
	SlackEnabled  bool   `json:"slack_enabled"`
	ZabbixEnabled bool   `json:"zabbix_enabled"`
}

// UpdatedTokens holds refreshed OAuth tokens
type UpdatedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    string
}

// DeviceAuthResult holds the result of a device auth operation
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

// SendCompleted sends completion notification with metrics
func (c *Client) SendCompleted(incidentID, sessionID, response string, tokensUsed int, executionTimeMs int64) error {
	return c.SendCompletedWithTokens(incidentID, sessionID, response, tokensUsed, executionTimeMs, nil)
}

// SendCompletedWithTokens sends completion notification with metrics and optionally updated tokens
func (c *Client) SendCompletedWithTokens(incidentID, sessionID, response string, tokensUsed int, executionTimeMs int64, updatedTokens *UpdatedTokens) error {
	msg := Message{
		Type:            MessageTypeCodexCompleted,
		IncidentID:      incidentID,
		SessionID:       sessionID,
		Output:          response,
		TokensUsed:      tokensUsed,
		ExecutionTimeMs: executionTimeMs,
	}

	// Include updated tokens if they were refreshed
	if updatedTokens != nil {
		msg.UpdatedAccessToken = updatedTokens.AccessToken
		msg.UpdatedRefreshToken = updatedTokens.RefreshToken
		msg.UpdatedExpiresAt = updatedTokens.ExpiresAt
	}

	return c.Send(msg)
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

// SendDeviceAuthResponse sends a device auth response to the API
func (c *Client) SendDeviceAuthResponse(result *DeviceAuthResult) error {
	msg := Message{
		Type:             MessageTypeDeviceAuthResponse,
		DeviceCode:       result.DeviceCode,
		UserCode:         result.UserCode,
		VerificationURL:  result.VerificationURL,
		ExpiresIn:        result.ExpiresIn,
		AuthStatus:       result.Status,
		AuthEmail:        result.Email,
		AuthAccessToken:  result.AccessToken,
		AuthRefreshToken: result.RefreshToken,
		AuthIDToken:      result.IDToken,
		AuthExpiresAt:    result.ExpiresAt,
		Error:            result.Error,
	}
	return c.Send(msg)
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
