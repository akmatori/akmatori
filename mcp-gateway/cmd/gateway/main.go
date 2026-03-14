package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/tools"
	"gorm.io/gorm/logger"
)

const (
	defaultPort = "8080"
	version     = "1.0.0"
)

func main() {
	// Setup structured logging
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))

	slog.Info("starting MCP Gateway")

	// Get configuration from environment
	port := os.Getenv("MCP_PORT")
	if port == "" {
		port = defaultPort
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		slog.Error("DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	// Connect to database
	slog.Info("connecting to database")
	if err := database.Connect(databaseURL, logger.Warn); err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	slog.Info("database connected")

	// Bridge slog to *log.Logger for internal packages that still accept it
	stdLogger := slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo)

	// Create MCP server
	server := mcp.NewServer("akmatori-mcp-gateway", version, stdLogger)

	// Register all tools
	registry := tools.NewRegistry(server, stdLogger)
	registry.RegisterAllTools()

	// Setup HTTP handlers
	mux := http.NewServeMux()

	// MCP endpoint
	mux.HandleFunc("/mcp", server.HandleHTTP)
	mux.HandleFunc("/mcp/", server.HandleHTTP)

	// SSE endpoint for streaming
	mux.HandleFunc("/sse", server.HandleHTTP)

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	// Tool schemas endpoint
	mux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}

		schemas := tools.GetToolSchemas()
		json.NewEncoder(w).Encode(schemas)
	})

	mux.HandleFunc("/tools/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}

		// Extract tool name from path: /tools/{name}
		toolName := strings.TrimPrefix(r.URL.Path, "/tools/")
		toolName = strings.TrimSuffix(toolName, "/")

		if toolName == "" {
			schemas := tools.GetToolSchemas()
			json.NewEncoder(w).Encode(schemas)
			return
		}

		schema, ok := tools.GetToolSchema(toolName)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "tool not found"})
			return
		}

		json.NewEncoder(w).Encode(schema)
	})

	// Start server
	addr := ":" + port
	slog.Info("MCP Gateway listening", "addr", addr)

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		slog.Info("shutting down")
		os.Exit(0)
	}()

	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
