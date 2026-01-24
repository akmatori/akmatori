package codex

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Runner executes Codex CLI commands
type Runner struct {
	logger       *log.Logger
	workspaceDir string
	mcpGateway   string
	mu           sync.Mutex
	activeRuns   map[string]context.CancelFunc
}

// NewRunner creates a new Codex runner
func NewRunner(logger *log.Logger, workspaceDir, mcpGateway string) *Runner {
	return &Runner{
		logger:       logger,
		workspaceDir: workspaceDir,
		mcpGateway:   mcpGateway,
		activeRuns:   make(map[string]context.CancelFunc),
	}
}

// CodexEvent represents a parsed Codex JSON event
type CodexEvent struct {
	Type    string                 `json:"type"`
	Content map[string]interface{} `json:"content,omitempty"`
	Message string                 `json:"message,omitempty"`
}

// ExecuteResult holds the result of a Codex execution
type ExecuteResult struct {
	SessionID       string
	Response        string
	FullLog         string
	Error           error
	ErrorMessage    string // Captured error message from Codex JSON events
	TokensUsed      int    // Total tokens used (input + output)
	ExecutionTimeMs int64  // Execution time in milliseconds
	UpdatedTokens   *AuthTokens // Potentially refreshed OAuth tokens (for ChatGPT subscription)
}

// AuthTokens represents OAuth tokens for ChatGPT subscription auth
type AuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	Email        string `json:"email,omitempty"`
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
	AuthMethod          string // "api_key" or "chatgpt_subscription"
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

// OutputCallback is called for each output line from Codex
type OutputCallback func(output string)

// Execute runs Codex with the given task
func (r *Runner) Execute(ctx context.Context, incidentID, task string, openai *OpenAISettings, proxy *ProxyConfig, onOutput OutputCallback) (*ExecuteResult, error) {
	startTime := time.Now()

	// Workspace directory is created by API with .codex/AGENTS.md and .codex/skills
	workDir := filepath.Join(r.workspaceDir, incidentID)

	// Verify workspace exists (API must have created it)
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace not found: %s (API should create it)", workDir)
	}

	// Authenticate Codex CLI if credentials are provided
	if openai != nil && (openai.APIKey != "" || openai.ChatGPTAccessToken != "") {
		if err := r.authenticateCodex(ctx, openai); err != nil {
			return nil, fmt.Errorf("failed to authenticate codex: %w", err)
		}
	}

	// Create a cancelable context
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Store cancel function for this run
	r.mu.Lock()
	r.activeRuns[incidentID] = cancel
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.activeRuns, incidentID)
		r.mu.Unlock()
	}()

	// Build command arguments
	args := []string{
		"exec",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--json",
		task,
	}

	r.logger.Printf("Executing Codex in %s: codex %s", workDir, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = workDir

	// Set environment with OpenAI settings and proxy config
	cmd.Env = r.buildEnvironment(incidentID, openai, proxy)

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start codex: %w", err)
	}

	result := &ExecuteResult{}
	var fullLog strings.Builder
	var tokensUsed int
	var stderrOutput string

	// Use WaitGroup to ensure goroutines complete before returning
	var wg sync.WaitGroup
	wg.Add(2)

	// Process stdout (JSON events)
	go func() {
		defer wg.Done()
		tokensUsed = r.processStdout(stdout, &fullLog, result, onOutput)
	}()

	// Process stderr (for session ID extraction and error capture)
	go func() {
		defer wg.Done()
		stderrOutput = r.processStderr(stderr, result, onOutput)
	}()

	// Wait for command to complete
	err = cmd.Wait()

	// Wait for output processing to complete before checking results
	wg.Wait()

	result.FullLog = fullLog.String()
	result.TokensUsed = tokensUsed
	result.ExecutionTimeMs = time.Since(startTime).Milliseconds()

	// Read back potentially refreshed tokens for ChatGPT subscription auth
	if openai != nil && openai.AuthMethod == "chatgpt_subscription" {
		if updatedTokens, err := r.readAuthTokens(); err == nil {
			// Check if tokens were refreshed (access token changed)
			if updatedTokens.AccessToken != "" && updatedTokens.AccessToken != openai.ChatGPTAccessToken {
				result.UpdatedTokens = updatedTokens
				r.logger.Printf("OAuth tokens were refreshed during execution")
			}
		}
	}

	// Handle errors - return meaningful error messages
	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("execution cancelled")
		}
		// Build error message with all available information
		errMsg := err.Error()
		// Prefer ErrorMessage from JSON events (most descriptive)
		if result.ErrorMessage != "" {
			errMsg = result.ErrorMessage
		} else if stderrOutput != "" {
			errMsg = fmt.Sprintf("%s: %s", err.Error(), strings.TrimSpace(stderrOutput))
		}
		r.logger.Printf("Execution failed: %s", errMsg)
		return result, fmt.Errorf("codex execution failed: %s", errMsg)
	}

	// Check if we got an empty response - might indicate an API error
	if result.Response == "" && result.TokensUsed == 0 {
		// Prefer ErrorMessage from JSON events
		if result.ErrorMessage != "" {
			r.logger.Printf("Execution returned empty response with error: %s", result.ErrorMessage)
			return result, fmt.Errorf("codex error: %s", result.ErrorMessage)
		}
		if stderrOutput != "" {
			errMsg := strings.TrimSpace(stderrOutput)
			r.logger.Printf("Execution returned empty response with stderr: %s", errMsg)
			return result, fmt.Errorf("codex returned empty response: %s", errMsg)
		}
	}

	r.logger.Printf("Execution complete, response: %d chars, session: %s, tokens: %d, time: %dms",
		len(result.Response), result.SessionID, result.TokensUsed, result.ExecutionTimeMs)
	return result, nil
}

// Resume resumes an existing Codex session
func (r *Runner) Resume(ctx context.Context, incidentID, sessionID, message string, openai *OpenAISettings, proxy *ProxyConfig, onOutput OutputCallback) (*ExecuteResult, error) {
	startTime := time.Now()
	workDir := filepath.Join(r.workspaceDir, incidentID)

	// Authenticate Codex CLI if credentials are provided
	if openai != nil && (openai.APIKey != "" || openai.ChatGPTAccessToken != "") {
		if err := r.authenticateCodex(ctx, openai); err != nil {
			return nil, fmt.Errorf("failed to authenticate codex: %w", err)
		}
	}

	// Create a cancelable context
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	r.mu.Lock()
	r.activeRuns[incidentID] = cancel
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.activeRuns, incidentID)
		r.mu.Unlock()
	}()

	// Build resume command
	args := []string{
		"exec",
		"resume",
		sessionID,
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--json",
		message,
	}

	r.logger.Printf("Resuming Codex session %s: %s", sessionID, message)

	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = workDir
	cmd.Env = r.buildEnvironment(incidentID, openai, proxy)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start codex: %w", err)
	}

	result := &ExecuteResult{SessionID: sessionID}
	var fullLog strings.Builder
	var tokensUsed int
	var stderrOutput string

	// Use WaitGroup to ensure goroutines complete before returning
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		tokensUsed = r.processStdout(stdout, &fullLog, result, onOutput)
	}()

	go func() {
		defer wg.Done()
		stderrOutput = r.processStderr(stderr, result, onOutput)
	}()

	err = cmd.Wait()

	// Wait for output processing to complete before checking results
	wg.Wait()

	result.FullLog = fullLog.String()
	result.TokensUsed = tokensUsed
	result.ExecutionTimeMs = time.Since(startTime).Milliseconds()

	// Read back potentially refreshed tokens for ChatGPT subscription auth
	if openai != nil && openai.AuthMethod == "chatgpt_subscription" {
		if updatedTokens, err := r.readAuthTokens(); err == nil {
			// Check if tokens were refreshed (access token changed)
			if updatedTokens.AccessToken != "" && updatedTokens.AccessToken != openai.ChatGPTAccessToken {
				result.UpdatedTokens = updatedTokens
				r.logger.Printf("OAuth tokens were refreshed during resume")
			}
		}
	}

	// Handle errors - return meaningful error messages
	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("execution cancelled")
		}
		// Build error message with all available information
		errMsg := err.Error()
		// Prefer ErrorMessage from JSON events (most descriptive)
		if result.ErrorMessage != "" {
			errMsg = result.ErrorMessage
		} else if stderrOutput != "" {
			errMsg = fmt.Sprintf("%s: %s", err.Error(), strings.TrimSpace(stderrOutput))
		}
		r.logger.Printf("Resume failed: %s", errMsg)
		return result, fmt.Errorf("codex resume failed: %s", errMsg)
	}

	// Check if we got an empty response - might indicate an API error
	if result.Response == "" && result.TokensUsed == 0 {
		// Prefer ErrorMessage from JSON events
		if result.ErrorMessage != "" {
			r.logger.Printf("Resume returned empty response with error: %s", result.ErrorMessage)
			return result, fmt.Errorf("codex error: %s", result.ErrorMessage)
		}
		if stderrOutput != "" {
			errMsg := strings.TrimSpace(stderrOutput)
			r.logger.Printf("Resume returned empty response with stderr: %s", errMsg)
			return result, fmt.Errorf("codex returned empty response: %s", errMsg)
		}
	}

	r.logger.Printf("Resume complete, response: %d chars, session: %s, tokens: %d, time: %dms",
		len(result.Response), result.SessionID, result.TokensUsed, result.ExecutionTimeMs)
	return result, nil
}

// Cancel cancels an active Codex execution
func (r *Runner) Cancel(incidentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cancel, exists := r.activeRuns[incidentID]; exists {
		cancel()
		return true
	}
	return false
}

// DeviceAuthResult holds the result of device auth
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

// DeviceAuthCallback is called with device auth status updates
type DeviceAuthCallback func(result *DeviceAuthResult)

// RunDeviceAuth runs device authentication and returns initial codes
// It continues monitoring in the background and calls onComplete when tokens are obtained
// Accepts optional proxy settings for environments that require proxy configuration
func (r *Runner) RunDeviceAuth(ctx context.Context, proxySettings *OpenAISettings, onUpdate DeviceAuthCallback) error {
	r.logger.Printf("Starting device auth...")

	// Create a cancelable context with timeout (device auth can take up to 15 minutes)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)

	// Store cancel function for potential cancellation
	r.mu.Lock()
	r.activeRuns["device_auth"] = cancel
	r.mu.Unlock()

	go func() {
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.activeRuns, "device_auth")
			r.mu.Unlock()
		}()

		// Execute codex login --device-auth
		cmd := exec.CommandContext(ctx, "codex", "login", "--device-auth")

		// Set environment with proxy settings if configured
		env := os.Environ()
		if proxySettings != nil {
			if proxySettings.BaseURL != "" {
				env = append(env, fmt.Sprintf("OPENAI_BASE_URL=%s", proxySettings.BaseURL))
				r.logger.Printf("Device auth using custom base URL: %s", proxySettings.BaseURL)
			}
			if proxySettings.ProxyURL != "" {
				env = append(env, fmt.Sprintf("HTTP_PROXY=%s", proxySettings.ProxyURL))
				env = append(env, fmt.Sprintf("HTTPS_PROXY=%s", proxySettings.ProxyURL))
				r.logger.Printf("Device auth using proxy: %s", proxySettings.ProxyURL)
			}
			if proxySettings.NoProxy != "" {
				env = append(env, fmt.Sprintf("NO_PROXY=%s", proxySettings.NoProxy))
			}
		}
		cmd.Env = env

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			r.logger.Printf("Failed to create stdout pipe: %v", err)
			onUpdate(&DeviceAuthResult{Status: "failed", Error: err.Error()})
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			r.logger.Printf("Failed to create stderr pipe: %v", err)
			onUpdate(&DeviceAuthResult{Status: "failed", Error: err.Error()})
			return
		}

		if err := cmd.Start(); err != nil {
			r.logger.Printf("Failed to start codex login: %v", err)
			onUpdate(&DeviceAuthResult{Status: "failed", Error: err.Error()})
			return
		}

		result := &DeviceAuthResult{Status: "pending"}
		var stderrOutput strings.Builder
		var sentPending bool

		// Regex to strip ANSI escape codes
		ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)

		// Parse stdout for device code info
		// Expected format from codex login --device-auth:
		// 1. Open this link in your browser...
		//    https://auth.openai.com/codex/device
		// 2. Enter this one-time code...
		//    XXXX-XXXXX
		scanner := bufio.NewScanner(stdout)
		urlRegex := regexp.MustCompile(`https?://[^\s\x1b]+`)
		codeRegex := regexp.MustCompile(`\b([A-Z0-9]{4,}-[A-Z0-9]{4,})\b`)

		for scanner.Scan() {
			line := scanner.Text()
			r.logger.Printf("Device auth stdout: %s", line)

			// Strip ANSI codes for parsing
			cleanLine := ansiRegex.ReplaceAllString(line, "")
			cleanLine = strings.TrimSpace(cleanLine)

			// Extract verification URL (look for any https URL)
			if result.VerificationURL == "" {
				if matches := urlRegex.FindString(cleanLine); matches != "" {
					result.VerificationURL = matches
					// Generate a device code from timestamp since it's not in the URL
					result.DeviceCode = fmt.Sprintf("%d", time.Now().UnixNano())
					r.logger.Printf("Found URL: %s", result.VerificationURL)
				}
			}

			// Extract user code (format like XXXX-XXXXX)
			if result.UserCode == "" {
				if matches := codeRegex.FindStringSubmatch(cleanLine); len(matches) > 1 {
					result.UserCode = matches[1]
					r.logger.Printf("Found user code: %s", result.UserCode)
				}
			}

			// Once we have both URL and code, send the initial pending status (only once)
			if result.VerificationURL != "" && result.UserCode != "" && !sentPending {
				result.ExpiresIn = 900 // 15 minutes default
				r.logger.Printf("Got device auth codes: URL=%s, UserCode=%s", result.VerificationURL, result.UserCode)
				onUpdate(result)
				sentPending = true
			}
		}

		// Also read stderr for any error messages
		go func() {
			stderrScanner := bufio.NewScanner(stderr)
			for stderrScanner.Scan() {
				line := stderrScanner.Text()
				r.logger.Printf("Device auth stderr: %s", line)
				stderrOutput.WriteString(line)
				stderrOutput.WriteString("\n")
			}
		}()

		// Wait for command to complete (user approved or timeout)
		err = cmd.Wait()

		if err != nil {
			if ctx.Err() == context.Canceled {
				r.logger.Printf("Device auth cancelled")
				onUpdate(&DeviceAuthResult{Status: "failed", Error: "cancelled"})
				return
			}
			if ctx.Err() == context.DeadlineExceeded {
				r.logger.Printf("Device auth expired")
				onUpdate(&DeviceAuthResult{Status: "expired", Error: "authentication timeout"})
				return
			}
			errMsg := err.Error()
			if stderrOutput.Len() > 0 {
				errMsg = strings.TrimSpace(stderrOutput.String())
			}
			r.logger.Printf("Device auth failed: %s", errMsg)
			onUpdate(&DeviceAuthResult{Status: "failed", Error: errMsg})
			return
		}

		// Success - read auth tokens from auth.json
		tokens, err := r.readAuthTokens()
		if err != nil {
			r.logger.Printf("Failed to read auth tokens after device auth: %v", err)
			onUpdate(&DeviceAuthResult{Status: "failed", Error: "failed to read tokens after authentication"})
			return
		}

		completeResult := &DeviceAuthResult{
			Status:       "complete",
			DeviceCode:   result.DeviceCode,
			UserCode:     result.UserCode,
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			IDToken:      tokens.IDToken,
			ExpiresAt:    tokens.ExpiresAt,
			Email:        tokens.Email,
		}
		r.logger.Printf("Device auth completed successfully for email: %s", tokens.Email)
		onUpdate(completeResult)
	}()

	return nil
}

// CancelDeviceAuth cancels an ongoing device auth
func (r *Runner) CancelDeviceAuth() bool {
	return r.Cancel("device_auth")
}

// authenticateCodex authenticates Codex CLI using the configured auth method
func (r *Runner) authenticateCodex(ctx context.Context, openai *OpenAISettings) error {
	if openai == nil {
		return fmt.Errorf("no OpenAI settings provided")
	}

	// Determine auth method (default to api_key for backward compatibility)
	authMethod := openai.AuthMethod
	if authMethod == "" {
		authMethod = "api_key"
	}

	switch authMethod {
	case "chatgpt_subscription":
		return r.authenticateWithChatGPTTokens(openai)
	case "api_key":
		return r.authenticateWithAPIKey(ctx, openai.APIKey)
	default:
		return r.authenticateWithAPIKey(ctx, openai.APIKey)
	}
}

// authenticateWithAPIKey runs codex login with the provided API key
func (r *Runner) authenticateWithAPIKey(ctx context.Context, apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("API key is empty")
	}

	r.logger.Printf("Authenticating Codex CLI with API key...")

	cmd := exec.CommandContext(ctx, "codex", "login", "--with-api-key")
	cmd.Stdin = strings.NewReader(apiKey)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex login failed: %w (output: %s)", err, string(output))
	}

	r.logger.Printf("Codex CLI authenticated successfully with API key")
	return nil
}

// authenticateWithChatGPTTokens ensures valid auth.json exists for ChatGPT subscription auth
func (r *Runner) authenticateWithChatGPTTokens(openai *OpenAISettings) error {
	// Get the auth file path (codex user's home directory)
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/home/codex"
	}
	authPath := filepath.Join(homeDir, ".codex", "auth.json")

	// First, check if auth.json already exists with valid tokens (including id_token required by Codex CLI v0.87+)
	if existingTokens, err := r.readAuthTokens(); err == nil {
		if existingTokens.AccessToken != "" && existingTokens.RefreshToken != "" && existingTokens.IDToken != "" {
			r.logger.Printf("Using existing auth.json with valid tokens (email: %s)", existingTokens.Email)
			return nil
		}
		// Existing auth.json is incomplete (missing id_token), will regenerate
		if existingTokens.IDToken == "" {
			r.logger.Printf("Existing auth.json is missing id_token, will regenerate")
		}
	}

	// No valid existing auth.json, check if we have tokens from database
	if openai.ChatGPTAccessToken == "" || openai.ChatGPTRefreshToken == "" {
		return fmt.Errorf("ChatGPT tokens are empty")
	}

	// id_token is required by Codex CLI v0.87+
	// If it's missing, the user needs to re-authenticate
	if openai.ChatGPTIDToken == "" {
		return fmt.Errorf("ChatGPT id_token is missing. Please re-authenticate by going to Settings > OpenAI and clicking 'Authenticate with ChatGPT'")
	}

	r.logger.Printf("Authenticating Codex CLI with ChatGPT subscription tokens from database...")

	// Ensure the directory exists
	authDir := filepath.Dir(authPath)
	if err := os.MkdirAll(authDir, 0700); err != nil {
		return fmt.Errorf("failed to create auth directory: %w", err)
	}

	// Build auth.json content in codex CLI format
	tokensMap := map[string]interface{}{
		"access_token":  openai.ChatGPTAccessToken,
		"refresh_token": openai.ChatGPTRefreshToken,
		"id_token":      openai.ChatGPTIDToken,
	}

	authData := map[string]interface{}{
		"OPENAI_API_KEY": nil,
		"tokens":         tokensMap,
		"last_refresh":   time.Now().UTC().Format(time.RFC3339Nano),
	}

	// Write auth.json
	data, err := json.MarshalIndent(authData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth data: %w", err)
	}

	if err := os.WriteFile(authPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write auth.json: %w", err)
	}

	r.logger.Printf("Codex CLI authenticated successfully with ChatGPT tokens")
	return nil
}

// readAuthTokens reads the current auth.json file and returns any updated tokens
func (r *Runner) readAuthTokens() (*AuthTokens, error) {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/home/codex"
	}
	authPath := filepath.Join(homeDir, ".codex", "auth.json")

	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read auth.json: %w", err)
	}

	var rawAuth map[string]interface{}
	if err := json.Unmarshal(data, &rawAuth); err != nil {
		return nil, fmt.Errorf("failed to parse auth.json: %w", err)
	}

	tokens := &AuthTokens{}

	// Check if tokens are nested under "tokens" key (codex CLI format)
	tokenData := rawAuth
	if nestedTokens, ok := rawAuth["tokens"].(map[string]interface{}); ok {
		tokenData = nestedTokens
	}

	// Extract tokens - handle different possible field names
	if at, ok := tokenData["access_token"].(string); ok {
		tokens.AccessToken = at
	} else if at, ok := tokenData["accessToken"].(string); ok {
		tokens.AccessToken = at
	}

	if rt, ok := tokenData["refresh_token"].(string); ok {
		tokens.RefreshToken = rt
	} else if rt, ok := tokenData["refreshToken"].(string); ok {
		tokens.RefreshToken = rt
	}

	// Extract id_token (required by Codex CLI v0.87+)
	if it, ok := tokenData["id_token"].(string); ok {
		tokens.IDToken = it
	} else if it, ok := tokenData["idToken"].(string); ok {
		tokens.IDToken = it
	}

	if exp, ok := tokenData["expires_at"].(string); ok {
		tokens.ExpiresAt = exp
	} else if exp, ok := tokenData["expiresAt"].(string); ok {
		tokens.ExpiresAt = exp
	}

	// Try to get email from tokenData first
	if email, ok := tokenData["email"].(string); ok {
		tokens.Email = email
	} else if email, ok := rawAuth["email"].(string); ok {
		tokens.Email = email
	}

	// If no email found, try to extract from JWT access_token
	if tokens.Email == "" && tokens.AccessToken != "" {
		tokens.Email = extractEmailFromJWT(tokens.AccessToken)
	}

	return tokens, nil
}

// extractEmailFromJWT extracts email from JWT claims without validation
func extractEmailFromJWT(tokenString string) string {
	// JWT format: header.payload.signature
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return ""
	}

	// Decode payload (base64url encoded)
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return ""
		}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	// Try different claim locations for email
	if email, ok := claims["email"].(string); ok {
		return email
	}

	// Check nested profile claims (OpenAI format)
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]interface{}); ok {
		if email, ok := profile["email"].(string); ok {
			return email
		}
	}

	return ""
}

// buildEnvironment builds the environment for Codex
func (r *Runner) buildEnvironment(incidentID string, openai *OpenAISettings, proxy *ProxyConfig) []string {
	env := os.Environ()

	// Add MCP Gateway URL
	env = append(env, fmt.Sprintf("MCP_GATEWAY_URL=%s", r.mcpGateway))
	env = append(env, fmt.Sprintf("INCIDENT_ID=%s", incidentID))

	// Add OpenAI settings if provided
	if openai != nil {
		if openai.Model != "" {
			env = append(env, fmt.Sprintf("CODEX_MODEL=%s", openai.Model))
			r.logger.Printf("Using model: %s", openai.Model)
		}
		if openai.ReasoningEffort != "" {
			env = append(env, fmt.Sprintf("CODEX_REASONING_EFFORT=%s", openai.ReasoningEffort))
			r.logger.Printf("Using reasoning effort: %s", openai.ReasoningEffort)
		}
		// Set custom base URL if configured (for Azure OpenAI, local LLMs, etc.)
		if openai.BaseURL != "" {
			env = append(env, fmt.Sprintf("OPENAI_BASE_URL=%s", openai.BaseURL))
			r.logger.Printf("Using custom base URL: %s", openai.BaseURL)
		}
	}

	// Set proxy settings if configured AND enabled for OpenAI
	if proxy != nil && proxy.URL != "" && proxy.OpenAIEnabled {
		env = append(env, fmt.Sprintf("HTTP_PROXY=%s", proxy.URL))
		env = append(env, fmt.Sprintf("HTTPS_PROXY=%s", proxy.URL))
		r.logger.Printf("Using proxy for OpenAI: %s", proxy.URL)
	}
	if proxy != nil && proxy.NoProxy != "" {
		env = append(env, fmt.Sprintf("NO_PROXY=%s", proxy.NoProxy))
	}

	return env
}

// processStdout processes Codex JSON output and returns token usage
func (r *Runner) processStdout(stdout io.Reader, fullLog *strings.Builder, result *ExecuteResult, onOutput OutputCallback) int {
	decoder := json.NewDecoder(stdout)
	var lastAgentMessage string
	var streamLog strings.Builder // Accumulated human-readable log for streaming
	var tokensUsed int

	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			// Not valid JSON, try line-by-line
			r.logger.Printf("JSON decode error: %v", err)
			break
		}

		// Log the raw JSON event
		eventJSON, _ := json.Marshal(event)
		fullLog.WriteString(string(eventJSON))
		fullLog.WriteString("\n")

		// Extract useful information
		eventType, _ := event["type"].(string)

		// Log all event types for debugging
		r.logger.Printf("Received event type: %s", eventType)

		// Check for error in any event (some events have inline errors)
		if errMsg, ok := event["error"].(string); ok && errMsg != "" {
			r.logger.Printf("Got error in event: %s", errMsg)
			result.ErrorMessage = errMsg
			streamLog.WriteString(fmt.Sprintf("❌ Error: %s\n", errMsg))
			if onOutput != nil {
				onOutput(streamLog.String())
			}
		}
		// Also check for error object with message field
		if errObj, ok := event["error"].(map[string]interface{}); ok {
			if errMsg, ok := errObj["message"].(string); ok && errMsg != "" {
				r.logger.Printf("Got error object: %s", errMsg)
				result.ErrorMessage = errMsg
				streamLog.WriteString(fmt.Sprintf("❌ Error: %s\n", errMsg))
				if onOutput != nil {
					onOutput(streamLog.String())
				}
			}
		}

		// Extract token usage from any event that has it
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			inputTokens := 0
			outputTokens := 0
			if it, ok := usage["input_tokens"].(float64); ok {
				inputTokens = int(it)
			}
			if ot, ok := usage["output_tokens"].(float64); ok {
				outputTokens = int(ot)
			}
			if inputTokens > 0 || outputTokens > 0 {
				tokensUsed = inputTokens + outputTokens
				r.logger.Printf("Got token usage: input=%d, output=%d, total=%d", inputTokens, outputTokens, tokensUsed)
			}
		}

		switch eventType {
		case "error":
			// Handle explicit error events
			if msg, ok := event["message"].(string); ok && msg != "" {
				r.logger.Printf("Got error event: %s", msg)
				result.ErrorMessage = msg
				streamLog.WriteString(fmt.Sprintf("❌ Error: %s\n", msg))
				if onOutput != nil {
					onOutput(streamLog.String())
				}
			}
			// Also check for error field with code
			if code, ok := event["code"].(string); ok {
				if result.ErrorMessage == "" {
					result.ErrorMessage = fmt.Sprintf("Error code: %s", code)
				} else {
					result.ErrorMessage = fmt.Sprintf("%s (code: %s)", result.ErrorMessage, code)
				}
			}

		case "thread.started":
			// Extract thread_id as session ID
			if threadID, ok := event["thread_id"].(string); ok && threadID != "" {
				result.SessionID = threadID
				r.logger.Printf("Got session/thread ID: %s", threadID)
			}

		case "item.started":
			// Tool call started - we don't show "running" state, just wait for completion
			// This keeps the log cleaner

		case "item.completed":
			// Check for different item types
			if item, ok := event["item"].(map[string]interface{}); ok {
				itemType, _ := item["type"].(string)

				switch itemType {
				case "agent_message":
					// Get text directly from item
					if text, ok := item["text"].(string); ok && text != "" {
						lastAgentMessage = text
						r.logger.Printf("Got agent message: %d chars", len(text))
					}
					// Also try legacy format
					if content, ok := item["content"].([]interface{}); ok && len(content) > 0 {
						if textItem, ok := content[0].(map[string]interface{}); ok {
							if text, _ := textItem["text"].(string); text != "" {
								lastAgentMessage = text
							}
						}
					}

				case "reasoning":
					if text, ok := item["text"].(string); ok && text != "" {
						streamLog.WriteString(fmt.Sprintf("🤔 %s\n", text))
						if onOutput != nil {
							onOutput(streamLog.String())
						}
					}

				case "command_execution":
					// Tool call completed - simple format
					cmd, _ := item["command"].(string)
					output, _ := item["aggregated_output"].(string)

					// Format: ✅ Ran: <command>
					streamLog.WriteString(fmt.Sprintf("✅ Ran: %s\n\n", cmd))

					// Format: Output (separate block, indented) - always show, even if empty
					streamLog.WriteString("📋 Output:\n")
					if output != "" {
						// Truncate long output for display
						displayOutput := strings.TrimSpace(output)
						if len(displayOutput) > 4000 {
							displayOutput = displayOutput[:4000] + "\n... (truncated)"
						}
						// Indent output for visual clarity
						lines := strings.Split(displayOutput, "\n")
						for _, line := range lines {
							streamLog.WriteString(fmt.Sprintf("   %s\n", line))
						}
					} else {
						streamLog.WriteString("   (no output)\n")
					}
					streamLog.WriteString("\n")

					if onOutput != nil {
						onOutput(streamLog.String())
					}
				}
			}

		case "turn.completed":
			// Extract token usage from turn.completed event (most reliable source)
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				inputTokens := 0
				outputTokens := 0
				if it, ok := usage["input_tokens"].(float64); ok {
					inputTokens = int(it)
				}
				if ot, ok := usage["output_tokens"].(float64); ok {
					outputTokens = int(ot)
				}
				if inputTokens > 0 || outputTokens > 0 {
					tokensUsed = inputTokens + outputTokens
					r.logger.Printf("Got final token usage from turn.completed: input=%d, output=%d, total=%d", inputTokens, outputTokens, tokensUsed)
				}
			}

		case "assistant.reasoning":
			// Stream reasoning output (legacy format)
			if text, ok := event["text"].(string); ok {
				streamLog.WriteString(fmt.Sprintf("🤔 %s\n", text))
				if onOutput != nil {
					onOutput(streamLog.String())
				}
			}
		}
	}

	// Set final response
	if lastAgentMessage != "" {
		result.Response = lastAgentMessage
	}
	r.logger.Printf("Stdout processing complete, response: %d chars, tokens: %d", len(result.Response), tokensUsed)
	return tokensUsed
}

// processStderr processes Codex stderr for session ID and captures error messages
func (r *Runner) processStderr(stderr io.Reader, result *ExecuteResult, onOutput OutputCallback) string {
	scanner := bufio.NewScanner(stderr)
	sessionRegex := regexp.MustCompile(`session[_\s]*(?:id)?[:\s]*([a-zA-Z0-9-]+)`)
	var stderrOutput strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		stderrOutput.WriteString(line)
		stderrOutput.WriteString("\n")

		// Try to extract session ID
		if matches := sessionRegex.FindStringSubmatch(strings.ToLower(line)); len(matches) > 1 {
			result.SessionID = matches[1]
		}

		// Forward stderr as progress
		if onOutput != nil && line != "" {
			onOutput(line)
		}
	}
	return stderrOutput.String()
}

// CleanupWorkspace removes the workspace for an incident
func (r *Runner) CleanupWorkspace(incidentID string) error {
	workDir := filepath.Join(r.workspaceDir, incidentID)
	return os.RemoveAll(workDir)
}
