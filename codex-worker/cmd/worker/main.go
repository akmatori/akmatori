package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akmatori/codex-worker/internal/orchestrator"
)

func main() {
	// Setup logging
	logger := log.New(os.Stdout, "[codex-worker] ", log.LstdFlags|log.Lshortfile)

	logger.Println("Starting Codex Worker...")

	// Get configuration from environment
	apiWSURL := os.Getenv("API_WS_URL")
	if apiWSURL == "" {
		apiWSURL = "ws://akmatori-api:3000/ws/codex"
	}

	mcpGateway := os.Getenv("MCP_GATEWAY_URL")
	if mcpGateway == "" {
		mcpGateway = "http://mcp-gateway:8080"
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir = "/workspaces"
	}

	sessionsFile := os.Getenv("SESSIONS_FILE")
	if sessionsFile == "" {
		sessionsFile = "/home/codex/.codex/sessions.json"
	}

	// Create orchestrator
	config := orchestrator.Config{
		APIURL:       apiWSURL,
		MCPGateway:   mcpGateway,
		WorkspaceDir: workspaceDir,
		SessionsFile: sessionsFile,
	}

	orch := orchestrator.New(config, logger)

	// Connect with retry
	for {
		if err := orch.Start(); err != nil {
			logger.Printf("Failed to start orchestrator: %v, retrying in 5s", err)
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal or connection loss
	go func() {
		for {
			orch.Wait()
			// Connection lost, try to reconnect
			logger.Println("Connection lost, attempting reconnect...")
			if err := orch.Reconnect(); err != nil {
				logger.Printf("Reconnect failed: %v", err)
				return
			}
		}
	}()

	<-sigChan
	logger.Println("Received shutdown signal")
	orch.Stop()
	logger.Println("Codex Worker stopped")
}
