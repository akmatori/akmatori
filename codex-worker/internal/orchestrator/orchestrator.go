package orchestrator

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/akmatori/codex-worker/internal/codex"
	"github.com/akmatori/codex-worker/internal/session"
	"github.com/akmatori/codex-worker/internal/ws"
)

// Orchestrator manages Codex execution and communication with API
type Orchestrator struct {
	wsClient     *ws.Client
	runner       *codex.Runner
	sessionStore *session.Store
	logger       *log.Logger
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// Config holds orchestrator configuration
type Config struct {
	APIURL       string
	MCPGateway   string
	WorkspaceDir string
	SessionsFile string
}

// New creates a new orchestrator
func New(config Config, logger *log.Logger) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	return &Orchestrator{
		wsClient:     ws.NewClient(config.APIURL, logger),
		runner:       codex.NewRunner(logger, config.WorkspaceDir, config.MCPGateway),
		sessionStore: session.NewStore(config.SessionsFile),
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start starts the orchestrator
func (o *Orchestrator) Start() error {
	// Connect to API
	if err := o.wsClient.Connect(); err != nil {
		return err
	}

	// Set message handler
	o.wsClient.SetMessageHandler(o.handleMessage)

	// Start heartbeat
	o.wsClient.StartHeartbeat(30 * time.Second)

	// Start read loop
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.wsClient.ReadLoop()
	}()

	// Send initial status
	o.wsClient.Send(ws.Message{
		Type: ws.MessageTypeStatus,
		Data: map[string]interface{}{
			"status": "ready",
		},
	})

	o.logger.Println("Orchestrator started")
	return nil
}

// Stop stops the orchestrator
func (o *Orchestrator) Stop() {
	o.logger.Println("Stopping orchestrator...")
	o.cancel()
	o.wsClient.Close()
	o.wg.Wait()
	o.logger.Println("Orchestrator stopped")
}

// Wait waits for the orchestrator to stop
func (o *Orchestrator) Wait() {
	o.wg.Wait()
}

// handleMessage handles incoming WebSocket messages
func (o *Orchestrator) handleMessage(msg ws.Message) {
	o.logger.Printf("Received message: type=%s incident=%s", msg.Type, msg.IncidentID)

	switch msg.Type {
	case ws.MessageTypeNewIncident:
		// Extract OpenAI settings from message
		var openaiSettings *ws.OpenAISettings
		if msg.OpenAIAPIKey != "" {
			openaiSettings = &ws.OpenAISettings{
				APIKey:          msg.OpenAIAPIKey,
				Model:           msg.Model,
				ReasoningEffort: msg.ReasoningEffort,
				BaseURL:         msg.BaseURL,
				ProxyURL:        msg.ProxyURL,
				NoProxy:         msg.NoProxy,
			}
		}
		go o.handleNewIncident(msg.IncidentID, msg.Task, openaiSettings)

	case ws.MessageTypeContinueIncident:
		// Extract OpenAI settings from message (for re-authentication)
		var openaiSettings *ws.OpenAISettings
		if msg.OpenAIAPIKey != "" {
			openaiSettings = &ws.OpenAISettings{
				APIKey:          msg.OpenAIAPIKey,
				Model:           msg.Model,
				ReasoningEffort: msg.ReasoningEffort,
				BaseURL:         msg.BaseURL,
				ProxyURL:        msg.ProxyURL,
				NoProxy:         msg.NoProxy,
			}
		}
		go o.handleContinueIncident(msg.IncidentID, msg.Message, openaiSettings)

	case ws.MessageTypeCancelIncident:
		o.handleCancelIncident(msg.IncidentID)

	default:
		o.logger.Printf("Unknown message type: %s", msg.Type)
	}
}

// handleNewIncident handles a new incident execution request
func (o *Orchestrator) handleNewIncident(incidentID, task string, openaiSettings *ws.OpenAISettings) {
	o.logger.Printf("Starting new incident: %s", incidentID)

	// Create session
	o.sessionStore.Create(incidentID)

	// Convert OpenAI settings to runner format
	var runnerSettings *codex.OpenAISettings
	if openaiSettings != nil {
		runnerSettings = &codex.OpenAISettings{
			APIKey:          openaiSettings.APIKey,
			Model:           openaiSettings.Model,
			ReasoningEffort: openaiSettings.ReasoningEffort,
			BaseURL:         openaiSettings.BaseURL,
			ProxyURL:        openaiSettings.ProxyURL,
			NoProxy:         openaiSettings.NoProxy,
		}
	}

	// Execute Codex
	result, err := o.runner.Execute(o.ctx, incidentID, task, runnerSettings, func(output string) {
		// Stream output to API
		if err := o.wsClient.SendOutput(incidentID, output); err != nil {
			o.logger.Printf("Failed to send output: %v", err)
		}
	})

	if err != nil {
		o.logger.Printf("Incident %s failed: %v", incidentID, err)
		o.sessionStore.SetFailed(incidentID, err.Error())
		o.wsClient.SendError(incidentID, err.Error())
		return
	}

	// Update session with session ID
	if result.SessionID != "" {
		o.sessionStore.SetRunning(incidentID, result.SessionID)
	}

	// Mark as completed
	o.sessionStore.SetCompleted(incidentID, result.Response, result.FullLog)

	// Send completion with metrics
	o.wsClient.SendCompleted(incidentID, result.SessionID, result.Response, result.TokensUsed, result.ExecutionTimeMs)

	o.logger.Printf("Incident %s completed (tokens: %d, time: %dms)", incidentID, result.TokensUsed, result.ExecutionTimeMs)
}

// handleContinueIncident handles continuing an existing incident
func (o *Orchestrator) handleContinueIncident(incidentID, message string, openaiSettings *ws.OpenAISettings) {
	o.logger.Printf("Continuing incident: %s", incidentID)

	// Get existing session
	sess := o.sessionStore.Get(incidentID)
	if sess == nil || sess.SessionID == "" {
		o.wsClient.SendError(incidentID, "No session found for incident")
		return
	}

	// Convert OpenAI settings to runner format
	var runnerSettings *codex.OpenAISettings
	if openaiSettings != nil {
		runnerSettings = &codex.OpenAISettings{
			APIKey:          openaiSettings.APIKey,
			Model:           openaiSettings.Model,
			ReasoningEffort: openaiSettings.ReasoningEffort,
			BaseURL:         openaiSettings.BaseURL,
			ProxyURL:        openaiSettings.ProxyURL,
			NoProxy:         openaiSettings.NoProxy,
		}
	}

	// Resume Codex session
	result, err := o.runner.Resume(o.ctx, incidentID, sess.SessionID, message, runnerSettings, func(output string) {
		if err := o.wsClient.SendOutput(incidentID, output); err != nil {
			o.logger.Printf("Failed to send output: %v", err)
		}
	})

	if err != nil {
		o.logger.Printf("Continue incident %s failed: %v", incidentID, err)
		o.sessionStore.SetFailed(incidentID, err.Error())
		o.wsClient.SendError(incidentID, err.Error())
		return
	}

	// Update session
	o.sessionStore.SetCompleted(incidentID, result.Response, result.FullLog)

	// Send completion with metrics
	o.wsClient.SendCompleted(incidentID, result.SessionID, result.Response, result.TokensUsed, result.ExecutionTimeMs)

	o.logger.Printf("Continue incident %s completed (tokens: %d, time: %dms)", incidentID, result.TokensUsed, result.ExecutionTimeMs)
}

// handleCancelIncident handles cancelling an incident
func (o *Orchestrator) handleCancelIncident(incidentID string) {
	o.logger.Printf("Cancelling incident: %s", incidentID)

	if o.runner.Cancel(incidentID) {
		o.sessionStore.SetFailed(incidentID, "Cancelled by user")
		o.wsClient.SendError(incidentID, "Execution cancelled")
	}
}

// Reconnect attempts to reconnect to the API
func (o *Orchestrator) Reconnect() error {
	o.logger.Println("Attempting to reconnect...")

	// Close existing connection and reset for reconnection
	o.wsClient.Close()
	o.wsClient.Reset()

	// Create new client and connect
	for {
		select {
		case <-o.ctx.Done():
			return o.ctx.Err()
		default:
			if err := o.wsClient.Connect(); err != nil {
				o.logger.Printf("Reconnect failed: %v, retrying in 5s", err)
				time.Sleep(5 * time.Second)
				continue
			}

			// Reconnected
			o.wsClient.SetMessageHandler(o.handleMessage)
			o.wsClient.StartHeartbeat(30 * time.Second)

			o.wg.Add(1)
			go func() {
				defer o.wg.Done()
				o.wsClient.ReadLoop()
			}()

			o.logger.Println("Reconnected successfully")
			return nil
		}
	}
}
