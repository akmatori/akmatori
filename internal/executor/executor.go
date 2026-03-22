package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/utils"
)

// JSONEvent represents a JSON event from codex --json output
type JSONEvent struct {
	Type    string                 `json:"type"`
	Error   map[string]interface{} `json:"error,omitempty"` // Error can be an object
	Message string                 `json:"message,omitempty"`
	Item    *struct {
		ID               string `json:"id"`
		Type             string `json:"type"`
		Text             string `json:"text,omitempty"`
		Command          string `json:"command,omitempty"`
		AggregatedOutput string `json:"aggregated_output,omitempty"`
		ExitCode         int    `json:"exit_code,omitempty"`
		Status           string `json:"status,omitempty"`
	} `json:"item,omitempty"`
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// Result represents the result of a Codex execution
type Result struct {
	Output        string
	SessionID     string
	Error         error
	ExecutionTime time.Duration
	TokensUsed    int
	FullLog       string   // Complete log with all reasoning, commands, and outputs
	ErrorMessages []string // Error messages from Codex JSON events
}

// SlackResult represents the formatted result for Slack
type SlackResult struct {
	Response      string
	SessionID     string
	Error         error
	ExecutionTime time.Duration
	TokensUsed    int
	FullLog       string
}

// safeEnvVars is the allowlist of environment variables that are safe to pass to Codex.
// This prevents Codex from accessing sensitive variables like DATABASE_URL, JWT_SECRET, etc.
var safeEnvVars = map[string]bool{
	// Essential system variables
	"HOME":            true,
	"USER":            true,
	"PATH":            true,
	"SHELL":           true,
	"TERM":            true,
	"LANG":            true,
	"LC_ALL":          true,
	"TZ":              true,
	"TMPDIR":          true,
	"XDG_CONFIG_HOME": true,
	"XDG_DATA_HOME":   true,
	"XDG_CACHE_HOME":  true,

	// Node.js / npm (needed for Codex CLI)
	"NODE_PATH":         true,
	"NPM_CONFIG_PREFIX": true,

	// Git (for version control operations)
	"GIT_AUTHOR_NAME":     true,
	"GIT_AUTHOR_EMAIL":    true,
	"GIT_COMMITTER_NAME":  true,
	"GIT_COMMITTER_EMAIL": true,

	// Python (for scripts)
	"PYTHONPATH":              true,
	"PYTHONDONTWRITEBYTECODE": true,

	// Editor preferences
	"EDITOR": true,
	"VISUAL": true,

	// Color output
	"CLICOLOR":     true,
	"FORCE_COLOR":  true,
	"NO_COLOR":     true,
	"COLORTERM":    true,
	"TERM_PROGRAM": true,
}

// buildSafeEnvironment creates a filtered environment for Codex execution.
// Only allowlisted variables are passed through to prevent credential leakage.
func buildSafeEnvironment() []string {
	var env []string

	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]

		// Check if variable is in allowlist
		if safeEnvVars[key] {
			env = append(env, e)
			continue
		}

		// Also allow CODEX_* variables (for Codex configuration)
		if strings.HasPrefix(key, "CODEX_") {
			env = append(env, e)
			continue
		}
	}

	return env
}

// PrependGuidance adds the current time, task framing, and runbook-first
// instruction to a task. The runbook search reminder is placed in the user
// message (not just the system prompt) because models follow user-turn
// instructions more reliably than long system prompt sections.
func PrependGuidance(task string) string {
	currentTime := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	return fmt.Sprintf(`Current time: %s

IMPORTANT: Before using any infrastructure tools, you MUST search for relevant runbooks first.
Use short, simple keywords (3-5 words). Remove hyphens, extra qualifiers, and host names.
Example: alert "Nginx-cache test resource connection refused on edge host" → search "nginx cache connection refused"

  gateway_call("qmd.query", {"searches": [{"type": "lex", "query": "<short keywords>"}], "limit": 5})

If no results, retry with fewer/different keywords. If results have score > 0.7, retrieve the runbook:
  gateway_call("qmd.get", {"file": "<path from results>"})
Follow matching runbook procedures as your PRIMARY investigation guide.

Please help with the following incident or request:

%s`,
		currentTime, task)
}

// Executor handles Codex CLI execution
type Executor struct{}

// NewExecutor creates a new Codex executor
func NewExecutor() *Executor {
	return &Executor{}
}

// ensureCodexLogin ensures the codex CLI is authenticated with the API key from database.
// Codex requires `codex login --with-api-key` - it doesn't read OPENAI_API_KEY env var directly.
func (e *Executor) ensureCodexLogin(ctx context.Context) error {
	llmSettings, err := database.GetLLMSettings()
	if err != nil {
		return fmt.Errorf("failed to get LLM settings: %w", err)
	}
	if llmSettings.APIKey == "" {
		return fmt.Errorf("API key not configured in database settings")
	}

	// Run codex login --with-api-key, piping the API key to stdin
	cmd := exec.CommandContext(ctx, "codex", "login", "--with-api-key")
	cmd.Stdin = strings.NewReader(llmSettings.APIKey)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex login failed: %w (output: %s)", err, string(output))
	}

	slog.Info("Codex CLI authenticated successfully")
	return nil
}

// ExecuteInDirectory runs a Codex task in a specific working directory and streams stderr updates.
// This allows running Codex in different directories (e.g., per-skill or per-incident workspaces).
//
// onStderrUpdate is called periodically with the latest stderr output (last 15 lines)
func (e *Executor) ExecuteInDirectory(ctx context.Context, task string, sessionID string, workingDir string, onStderrUpdate func(string)) (*Result, error) {
	// Ensure codex is authenticated before executing
	if err := e.ensureCodexLogin(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate codex: %w", err)
	}

	var args []string

	if sessionID == "" {
		// New session - use --json to get reliable token usage from stdout
		// Bypass sandbox completely to avoid 2-minute command timeout
		// (codex has internal timeout for sandboxed commands that can't be configured)
		args = []string{
			"exec",
			"--skip-git-repo-check",
			"--dangerously-bypass-approvals-and-sandbox",
			"--json",
			task,
		}
	} else {
		// Resume existing session - use --json to get reliable token usage from stdout
		// Bypass sandbox completely to avoid 2-minute command timeout
		args = []string{
			"exec", "resume", sessionID,
			"--dangerously-bypass-approvals-and-sandbox",
			"--json",
			"--message", task,
		}
	}

	// Create command
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = workingDir

	// Set up a filtered environment for Codex - only pass safe variables
	// This prevents Codex from accessing sensitive env vars like DATABASE_URL, JWT_SECRET, etc.
	cmd.Env = buildSafeEnvironment()

	// Add model settings from database
	// Note: API key is handled via `codex login` in ensureCodexLogin()
	llmSettings, _ := database.GetLLMSettings()
	if llmSettings != nil {
		// Set model if configured
		if llmSettings.Model != "" {
			cmd.Env = append(cmd.Env, "CODEX_MODEL="+llmSettings.Model)
			slog.Info("Using model", "model", llmSettings.Model)
		}
		// Set reasoning effort if configured
		if string(llmSettings.ThinkingLevel) != "" {
			cmd.Env = append(cmd.Env, "CODEX_REASONING_EFFORT="+string(llmSettings.ThinkingLevel))
			slog.Info("Using reasoning effort", "reasoning_effort", llmSettings.ThinkingLevel)
		}
		// Set custom base URL if configured (for Azure OpenAI, local LLMs, etc.)
		if llmSettings.BaseURL != "" {
			cmd.Env = append(cmd.Env, "OPENAI_BASE_URL="+llmSettings.BaseURL)
			slog.Info("Using custom base URL", "base_url", llmSettings.BaseURL)
		}
	}

	// Log the exact command being executed
	slog.Info("Executing codex command", "working_dir", workingDir, "args", args)

	// Create stdin pipe to match Node.js stdio: ['pipe', 'pipe', 'pipe']
	// We create but don't close it - letting it stay open like Node.js does
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	_ = stdin // Keep reference; pipe closes automatically when cmd.Wait() completes

	// Set up stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Set up stderr pipe
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command and track execution time
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start codex: %w", err)
	}
	slog.Info("Started codex", "pid", cmd.Process.Pid, "session_id", sessionID)

	// Read stderr using io.Copy to avoid Scanner's line buffering issues
	var stderrBuf bytes.Buffer
	var stderrLines []string
	var extractedSessionID string
	stderrDone := make(chan struct{})
	sessionIDRegex := regexp.MustCompile(`Session ID: ([a-zA-Z0-9-]+)`)

	go func() {
		defer close(stderrDone)

		// Read in chunks and process progressively for real-time updates
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				stderrBuf.Write(buf[:n])

				// Split into lines for progress updates
				text := stderrBuf.String()
				lines := strings.Split(text, "\n")

				// Keep last incomplete line in buffer if no newline at end
				if !strings.HasSuffix(text, "\n") && len(lines) > 0 {
					// Last line is incomplete, keep it in buffer
					stderrBuf.Reset()
					stderrBuf.WriteString(lines[len(lines)-1])
					lines = lines[:len(lines)-1]
				} else {
					// All lines are complete
					stderrBuf.Reset()
				}

				// Update stderrLines with new complete lines
				if len(lines) > 0 {
					stderrLines = append(stderrLines, lines...)

					// Extract session ID from new lines
					for _, line := range lines {
						if matches := sessionIDRegex.FindStringSubmatch(line); len(matches) > 1 {
							extractedSessionID = matches[1]
							slog.Info("Extracted Codex session ID", "session_id", extractedSessionID)
						}
					}

					// Raw stderr not sent to UI - use JSON events for output
				}
			}

			if err == io.EOF {
				break
			}
			if err != nil {
				slog.Error("Error reading stderr", "error", err)
				break
			}
		}

		// Flush any remaining partial line in stderrBuf (line without trailing newline)
		if stderrBuf.Len() > 0 {
			finalLine := stderrBuf.String()
			stderrLines = append(stderrLines, finalLine)

			// Check if this final line contains the session ID
			if matches := sessionIDRegex.FindStringSubmatch(finalLine); len(matches) > 1 {
				extractedSessionID = matches[1]
				slog.Info("Extracted Codex session ID from final line", "session_id", extractedSessionID)
			}

			// Raw stderr not sent to UI - use JSON events for output
		}

		slog.Debug("Codex stderr reading complete", "line_count", len(stderrLines))
	}()

	// Read stdout as JSONL (JSON lines) - with --json flag, stdout contains JSON events
	// Use json.Decoder instead of bufio.Scanner to avoid 64 KB line limit
	// (Codex can output huge JSON events with large code blocks or files)
	var outputText strings.Builder
	var lastReasoningText string // Fallback output if no agent_message is produced
	var tokensUsed int
	var progressMessages []string
	var errorMessages []string // Collect error messages from events
	stdoutDone := make(chan struct{})

	go func() {
		defer close(stdoutDone)
		decoder := json.NewDecoder(stdout)
		eventCount := 0

		for {
			var event JSONEvent
			if err := decoder.Decode(&event); err != nil {
				if err == io.EOF {
					break
				}
				slog.Error("Error parsing JSON event", "event_index", eventCount, "error", err)
				// Don't continue on error - break to avoid infinite loop if decoder is broken
				break
			}
			eventCount++

			// Log each event for debugging
			slog.Debug("Received JSON event", "event_index", eventCount, "type", event.Type)

			// Log error details if present
			if event.Type == "error" {
				if len(event.Error) > 0 {
					errorJSON, _ := json.Marshal(event.Error)
					slog.Error("Codex error event", "error_detail", string(errorJSON))
				}
				if event.Message != "" {
					slog.Error("Codex error message", "message", event.Message)
					// Collect error messages for user-facing output
					errorMessages = append(errorMessages, event.Message)
					// Also add to progress messages for full log
					progressLine := fmt.Sprintf("❌ Error: %s", event.Message)
					progressMessages = append(progressMessages, progressLine)
				}
			}

			if event.Item != nil {
				slog.Debug("Codex item event",
					"item_type", event.Item.Type,
					"item_id", event.Item.ID,
					"item_status", event.Item.Status)
				if event.Item.Text != "" {
					slog.Debug("Codex item text", "text_length", len(event.Item.Text))
				}
				if event.Item.Command != "" {
					slog.Debug("Codex item command", "command", event.Item.Command)
				}
			}
			if event.Usage != nil {
				slog.Debug("Codex usage",
					"input_tokens", event.Usage.InputTokens,
					"cached_tokens", event.Usage.CachedInputTokens,
					"output_tokens", event.Usage.OutputTokens)
			}

			// Extract agent message text
			if event.Type == "item.completed" && event.Item != nil && event.Item.Type == "agent_message" {
				if outputText.Len() > 0 {
					outputText.WriteString("\n")
				}
				outputText.WriteString(event.Item.Text)
			}

			// Track reasoning and tool execution for progress updates
			if event.Type == "item.completed" && event.Item != nil {
				var progressLine string
				switch event.Item.Type {
				case "reasoning":
					progressLine = fmt.Sprintf("🤔 %s", event.Item.Text)
					// Keep full reasoning text as fallback if no agent_message is produced
					lastReasoningText = event.Item.Text
				case "command_execution":
					if event.Item.Status == "completed" {
						if event.Item.AggregatedOutput != "" {
							progressLine = fmt.Sprintf("✅ Ran: %s\nOutput:\n%s", event.Item.Command, event.Item.AggregatedOutput)
						} else {
							progressLine = fmt.Sprintf("✅ Ran: %s", event.Item.Command)
						}
					} else {
						if event.Item.AggregatedOutput != "" {
							progressLine = fmt.Sprintf("❌ Failed: %s\nOutput:\n%s", event.Item.Command, event.Item.AggregatedOutput)
						} else {
							progressLine = fmt.Sprintf("❌ Failed: %s", event.Item.Command)
						}
					}
				case "agent_message":
					progressLine = fmt.Sprintf("📝 Response ready (%d chars)", len(event.Item.Text))
				}
				if progressLine != "" {
					progressMessages = append(progressMessages, progressLine)

					// Send progress update with all lines
					if onStderrUpdate != nil {
						progressLog := strings.Join(progressMessages, "\n")
						onStderrUpdate(progressLog)
					}
				}
			}

			// Extract token usage from turn.completed event
			if event.Type == "turn.completed" && event.Usage != nil {
				tokensUsed = event.Usage.InputTokens + event.Usage.OutputTokens
				slog.Debug("Extracted token usage from JSON",
					"input_tokens", event.Usage.InputTokens,
					"output_tokens", event.Usage.OutputTokens,
					"total_tokens", tokensUsed)
			}
		}

		slog.Debug("Codex JSON reading complete", "event_count", eventCount, "tokens_used", tokensUsed)
	}()

	// IMPORTANT: Wait for I/O goroutines to finish BEFORE calling cmd.Wait()
	// cmd.Wait() closes the pipes, so the goroutines must finish reading first.
	// This fixes the race condition where "read |0: file already closed" errors occur.
	slog.Debug("Waiting for stdout goroutine to finish")
	<-stdoutDone
	slog.Debug("Stdout goroutine finished")
	slog.Debug("Waiting for stderr goroutine to finish")
	<-stderrDone
	slog.Debug("Stderr goroutine finished")

	// Now wait for command to complete - this closes the pipes (but they're already drained)
	slog.Debug("Waiting for codex process to complete")
	err = cmd.Wait()
	executionTime := time.Since(startTime)
	slog.Info("Codex process completed", "execution_time", executionTime, "exit_error", err)

	if err != nil {
		slog.Error("Codex exited with error",
			"error", err,
			"stderr_lines", strings.Join(stderrLines, "\n"),
			"json_event_count", len(progressMessages))
	}

	// Get the output text from JSON parsing (safe to access after stdoutDone is closed)
	finalOutput := strings.TrimSpace(outputText.String())

	// Fallback: If no agent_message was produced but we have reasoning, use the last reasoning
	// This handles cases where models like gpt-5.2 produce reasoning without a final text response
	if finalOutput == "" && lastReasoningText != "" {
		slog.Info("No agent_message produced, using last reasoning text as fallback", "reasoning_length", len(lastReasoningText))
		finalOutput = lastReasoningText
	}

	// Build full log from progress messages
	fullLog := strings.Join(progressMessages, "\n")

	result := &Result{
		Output:        finalOutput,
		SessionID:     extractedSessionID,
		Error:         err,
		ExecutionTime: executionTime,
		TokensUsed:    tokensUsed,
		FullLog:       fullLog,
		ErrorMessages: errorMessages,
	}

	slog.Info("Execute complete",
		"output_bytes", len(result.Output),
		"session_id", result.SessionID,
		"execution_time", result.ExecutionTime,
		"tokens_used", result.TokensUsed)
	if result.Output == "" {
		slog.Warn("Stdout is empty, no output from codex")
		slog.Debug("Stderr line count", "count", len(stderrLines))
		if len(stderrLines) > 0 {
			slog.Debug("Last stderr lines", "lines", strings.Join(stderrLines[utils.Max(0, len(stderrLines)-10):], "\n"))
		}
		slog.Debug("Progress message count", "count", len(progressMessages))
		if len(progressMessages) > 0 {
			slog.Debug("Last progress messages", "messages", strings.Join(progressMessages[utils.Max(0, len(progressMessages)-5):], "\n"))
		}
	}
	return result, nil
}

// ExecuteForSlackInDirectory is a wrapper around ExecuteInDirectory that formats the result for Slack
func (e *Executor) ExecuteForSlackInDirectory(ctx context.Context, task string, sessionID string, workingDir string, onStderrUpdate func(string)) *SlackResult {
	result, err := e.ExecuteInDirectory(ctx, task, sessionID, workingDir, onStderrUpdate)

	if err != nil {
		slog.Error("Error executing codex", "error", err)

		// Guard against nil result (can happen on early failures like cmd.Start errors)
		var executionTime time.Duration
		var tokensUsed int
		if result != nil {
			executionTime = result.ExecutionTime
			tokensUsed = result.TokensUsed
		}

		// Check if it's a context cancellation
		if ctx.Err() == context.Canceled {
			return &SlackResult{
				Response:      "⚠️ Task was canceled",
				SessionID:     "",
				Error:         err,
				ExecutionTime: executionTime,
				TokensUsed:    tokensUsed,
				FullLog:       "",
			}
		}

		// Include error messages from Codex if available
		errorDetails := ""
		if result != nil && len(result.ErrorMessages) > 0 {
			errorDetails = "\n\n**Errors:**\n"
			for i, msg := range result.ErrorMessages {
				errorDetails += fmt.Sprintf("%d. %s\n", i+1, msg)
			}
		}

		return &SlackResult{
			Response:      fmt.Sprintf("❌ Error executing task: %v%s", err, errorDetails),
			SessionID:     "",
			Error:         err,
			ExecutionTime: executionTime,
			TokensUsed:    tokensUsed,
			FullLog:       "",
		}
	}

	// Format response
	response := result.Output
	if response == "" {
		// Check if there were errors during execution
		if len(result.ErrorMessages) > 0 {
			response = "❌ **Task failed with errors:**\n\n"
			for i, msg := range result.ErrorMessages {
				response += fmt.Sprintf("%d. %s\n", i+1, msg)
			}
		} else {
			response = "✅ Task completed (no output)"
		}
	}

	// Append metrics to the response
	metricsLine := utils.AppendMetrics(response, result.ExecutionTime, result.TokensUsed)
	slog.Info("Final response lengths", "before_metrics", len(response), "after_metrics", len(metricsLine))

	slackResult := &SlackResult{
		Response:      metricsLine,
		SessionID:     result.SessionID,
		Error:         nil,
		ExecutionTime: result.ExecutionTime,
		TokensUsed:    result.TokensUsed,
		FullLog:       result.FullLog,
	}
	slog.Info("Returning SlackResult", "response_length", len(slackResult.Response), "full_log_length", len(slackResult.FullLog))
	return slackResult
}
