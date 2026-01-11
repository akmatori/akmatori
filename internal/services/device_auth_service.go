package services

import (
	"fmt"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// DeviceAuthStatus represents the status of a device auth flow
type DeviceAuthStatus string

const (
	DeviceAuthStatusPending  DeviceAuthStatus = "pending"
	DeviceAuthStatusComplete DeviceAuthStatus = "complete"
	DeviceAuthStatusExpired  DeviceAuthStatus = "expired"
	DeviceAuthStatusFailed   DeviceAuthStatus = "failed"
)

// DeviceAuthResponse is the response from starting device auth
type DeviceAuthResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"` // seconds until code expires
}

// DeviceAuthStatusResponse is the response from checking device auth status
type DeviceAuthStatusResponse struct {
	Status DeviceAuthStatus `json:"status"`
	Email  string           `json:"email,omitempty"`
	Error  string           `json:"error,omitempty"`
}

// AuthTokens represents the OAuth tokens from auth.json
type AuthTokens struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Email        string     `json:"email,omitempty"`
}

// DeviceAuthService manages device code authentication flow
// This service now delegates to the codex worker via WebSocket
type DeviceAuthService struct {
	mu         sync.Mutex
	activeFlow *deviceAuthFlow
	resultChan chan *DeviceAuthResult
}

// deviceAuthFlow tracks an in-progress device auth flow
type deviceAuthFlow struct {
	deviceCode      string
	userCode        string
	verificationURL string
	expiresIn       int
	startedAt       time.Time
	status          DeviceAuthStatus
	email           string
	tokens          *AuthTokens
	error           string
}

// DeviceAuthResult is the result received from the WebSocket handler
type DeviceAuthResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Status          string // "pending", "complete", "expired", "failed"
	Email           string
	AccessToken     string
	RefreshToken    string
	ExpiresAt       string
	Error           string
}

// NewDeviceAuthService creates a new device auth service
func NewDeviceAuthService() *DeviceAuthService {
	return &DeviceAuthService{
		resultChan: make(chan *DeviceAuthResult, 10),
	}
}

// HandleDeviceAuthResult processes a result from the WebSocket handler
// This is called by the API handler when a device_auth_response is received
func (s *DeviceAuthService) HandleDeviceAuthResult(result *DeviceAuthResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Handle initial pending response (with codes)
	if result.Status == "pending" && result.UserCode != "" {
		s.activeFlow = &deviceAuthFlow{
			deviceCode:      result.DeviceCode,
			userCode:        result.UserCode,
			verificationURL: result.VerificationURL,
			expiresIn:       result.ExpiresIn,
			startedAt:       time.Now(),
			status:          DeviceAuthStatusPending,
		}
		// Send to result channel for blocking call
		select {
		case s.resultChan <- result:
		default:
		}
		return
	}

	// Handle immediate failure (before any codes were received)
	if s.activeFlow == nil && result.Status == "failed" {
		// Send to result channel so WaitForInitialResponse can report the error
		select {
		case s.resultChan <- result:
		default:
		}
		return
	}

	// Handle completion or failure for active flow
	if s.activeFlow == nil {
		return
	}

	switch result.Status {
	case "complete":
		var expiresAt *time.Time
		if result.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, result.ExpiresAt); err == nil {
				expiresAt = &t
			}
		}
		s.activeFlow.status = DeviceAuthStatusComplete
		s.activeFlow.email = result.Email
		s.activeFlow.tokens = &AuthTokens{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
			ExpiresAt:    expiresAt,
			Email:        result.Email,
		}
		// Auto-save tokens to database
		if s.activeFlow.tokens != nil {
			s.saveTokensToDatabase(s.activeFlow.tokens)
		}

	case "expired":
		s.activeFlow.status = DeviceAuthStatusExpired
		s.activeFlow.error = result.Error

	case "failed":
		s.activeFlow.status = DeviceAuthStatusFailed
		s.activeFlow.error = result.Error
	}
}

// WaitForInitialResponse waits for the initial pending response with codes
// Returns the response or error after timeout
func (s *DeviceAuthService) WaitForInitialResponse(timeout time.Duration) (*DeviceAuthResponse, error) {
	select {
	case result := <-s.resultChan:
		if result.Status == "pending" && result.UserCode != "" {
			return &DeviceAuthResponse{
				DeviceCode:      result.DeviceCode,
				UserCode:        result.UserCode,
				VerificationURL: result.VerificationURL,
				ExpiresIn:       result.ExpiresIn,
			}, nil
		}
		if result.Status == "failed" {
			return nil, fmt.Errorf("device auth failed: %s", result.Error)
		}
		return nil, fmt.Errorf("unexpected result status: %s", result.Status)
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for device auth codes")
	}
}

// GetDeviceAuthStatus checks the status of an active device auth flow
func (s *DeviceAuthService) GetDeviceAuthStatus(deviceCode string) (*DeviceAuthStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeFlow == nil {
		return &DeviceAuthStatusResponse{
			Status: DeviceAuthStatusExpired,
			Error:  "no active authentication flow",
		}, nil
	}

	if s.activeFlow.deviceCode != deviceCode {
		return &DeviceAuthStatusResponse{
			Status: DeviceAuthStatusExpired,
			Error:  "device code mismatch",
		}, nil
	}

	// Check if flow should be expired based on time
	if s.activeFlow.expiresIn > 0 {
		expiresAt := s.activeFlow.startedAt.Add(time.Duration(s.activeFlow.expiresIn) * time.Second)
		if time.Now().After(expiresAt) {
			s.activeFlow = nil
			return &DeviceAuthStatusResponse{
				Status: DeviceAuthStatusExpired,
				Error:  "authentication code expired",
			}, nil
		}
	}

	return &DeviceAuthStatusResponse{
		Status: s.activeFlow.status,
		Email:  s.activeFlow.email,
		Error:  s.activeFlow.error,
	}, nil
}

// GetAuthTokens returns the tokens from a completed auth flow
func (s *DeviceAuthService) GetAuthTokens() (*AuthTokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeFlow == nil {
		return nil, fmt.Errorf("no active authentication flow")
	}

	if s.activeFlow.status != DeviceAuthStatusComplete {
		return nil, fmt.Errorf("authentication not complete: %s", s.activeFlow.status)
	}

	return s.activeFlow.tokens, nil
}

// CancelDeviceAuth cancels an active device auth flow (clears local state)
func (s *DeviceAuthService) CancelDeviceAuth() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeFlow = nil
}

// HasActiveFlow returns true if there's an active device auth flow
func (s *DeviceAuthService) HasActiveFlow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeFlow != nil
}

// ClearFlow clears the active flow (used before starting a new one)
func (s *DeviceAuthService) ClearFlow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeFlow = nil
	// Drain the result channel
	select {
	case <-s.resultChan:
	default:
	}
}

// saveTokensToDatabase saves the auth tokens to the OpenAI settings in the database
func (s *DeviceAuthService) saveTokensToDatabase(tokens *AuthTokens) error {
	settings, err := database.GetOpenAISettings()
	if err != nil {
		return fmt.Errorf("failed to get OpenAI settings: %w", err)
	}

	settings.AuthMethod = database.AuthMethodChatGPTSubscription
	settings.ChatGPTAccessToken = tokens.AccessToken
	settings.ChatGPTRefreshToken = tokens.RefreshToken
	settings.ChatGPTExpiresAt = tokens.ExpiresAt
	settings.ChatGPTUserEmail = tokens.Email

	if err := database.UpdateOpenAIChatGPTTokens(settings); err != nil {
		return fmt.Errorf("failed to save tokens to database: %w", err)
	}

	return nil
}

// SaveTokensToDatabase is a public version that saves tokens to database
func (s *DeviceAuthService) SaveTokensToDatabase(tokens *AuthTokens) error {
	return s.saveTokensToDatabase(tokens)
}
