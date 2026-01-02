package codex

import (
	"bufio"
	"context"
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
	SessionID string
	Response  string
	FullLog   string
	Error     error
}

// OpenAISettings holds OpenAI configuration for Codex execution
type OpenAISettings struct {
	APIKey          string
	Model           string
	ReasoningEffort string
}

// OutputCallback is called for each output line from Codex
type OutputCallback func(output string)

// Execute runs Codex with the given task
func (r *Runner) Execute(ctx context.Context, incidentID, task string, openai *OpenAISettings, onOutput OutputCallback) (*ExecuteResult, error) {
	// Workspace directory is created by API with .codex/AGENTS.md and .codex/skills
	workDir := filepath.Join(r.workspaceDir, incidentID)

	// Verify workspace exists (API must have created it)
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace not found: %s (API should create it)", workDir)
	}

	// Authenticate Codex CLI if API key provided
	if openai != nil && openai.APIKey != "" {
		if err := r.authenticateCodex(ctx, openai.APIKey); err != nil {
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

	// Set environment with OpenAI settings
	cmd.Env = r.buildEnvironment(incidentID, openai)

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

	// Use WaitGroup to ensure goroutines complete before returning
	var wg sync.WaitGroup
	wg.Add(2)

	// Process stdout (JSON events)
	go func() {
		defer wg.Done()
		r.processStdout(stdout, &fullLog, result, onOutput)
	}()

	// Process stderr (for session ID extraction)
	go func() {
		defer wg.Done()
		r.processStderr(stderr, result, onOutput)
	}()

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("execution cancelled")
		}
		result.Error = err
	}

	// Wait for output processing to complete
	wg.Wait()

	result.FullLog = fullLog.String()
	r.logger.Printf("Execution complete, response: %d chars, session: %s", len(result.Response), result.SessionID)
	return result, nil
}

// Resume resumes an existing Codex session
func (r *Runner) Resume(ctx context.Context, incidentID, sessionID, message string, openai *OpenAISettings, onOutput OutputCallback) (*ExecuteResult, error) {
	workDir := filepath.Join(r.workspaceDir, incidentID)

	// Authenticate Codex CLI if API key provided
	if openai != nil && openai.APIKey != "" {
		if err := r.authenticateCodex(ctx, openai.APIKey); err != nil {
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
	cmd.Env = r.buildEnvironment(incidentID, openai)

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

	// Use WaitGroup to ensure goroutines complete before returning
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		r.processStdout(stdout, &fullLog, result, onOutput)
	}()

	go func() {
		defer wg.Done()
		r.processStderr(stderr, result, onOutput)
	}()

	err = cmd.Wait()
	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("execution cancelled")
		}
		result.Error = err
	}

	// Wait for output processing to complete
	wg.Wait()

	result.FullLog = fullLog.String()
	r.logger.Printf("Resume complete, response: %d chars, session: %s", len(result.Response), result.SessionID)
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

// authenticateCodex runs codex login with the provided API key
func (r *Runner) authenticateCodex(ctx context.Context, apiKey string) error {
	r.logger.Printf("Authenticating Codex CLI...")

	cmd := exec.CommandContext(ctx, "codex", "login", "--with-api-key")
	cmd.Stdin = strings.NewReader(apiKey)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex login failed: %w (output: %s)", err, string(output))
	}

	r.logger.Printf("Codex CLI authenticated successfully")
	return nil
}

// buildEnvironment builds the environment for Codex
func (r *Runner) buildEnvironment(incidentID string, openai *OpenAISettings) []string {
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
	}

	return env
}

// processStdout processes Codex JSON output
func (r *Runner) processStdout(stdout io.Reader, fullLog *strings.Builder, result *ExecuteResult, onOutput OutputCallback) {
	decoder := json.NewDecoder(stdout)
	var lastAgentMessage string
	var streamLog strings.Builder // Accumulated human-readable log for streaming

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

		switch eventType {
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
	r.logger.Printf("Stdout processing complete, response: %d chars", len(result.Response))
}

// processStderr processes Codex stderr for session ID
func (r *Runner) processStderr(stderr io.Reader, result *ExecuteResult, onOutput OutputCallback) {
	scanner := bufio.NewScanner(stderr)
	sessionRegex := regexp.MustCompile(`session[_\s]*(?:id)?[:\s]*([a-zA-Z0-9-]+)`)

	for scanner.Scan() {
		line := scanner.Text()

		// Try to extract session ID
		if matches := sessionRegex.FindStringSubmatch(strings.ToLower(line)); len(matches) > 1 {
			result.SessionID = matches[1]
		}

		// Forward stderr as progress
		if onOutput != nil && line != "" {
			onOutput(line)
		}
	}
}

// CleanupWorkspace removes the workspace for an incident
func (r *Runner) CleanupWorkspace(incidentID string) error {
	workDir := filepath.Join(r.workspaceDir, incidentID)
	return os.RemoveAll(workDir)
}
