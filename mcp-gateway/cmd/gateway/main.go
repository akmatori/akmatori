package main

import (
	"encoding/json"
	"log"
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
	// Setup logging
	log := log.New(os.Stdout, "[MCP-Gateway] ", log.LstdFlags|log.Lshortfile)

	log.Println("Starting MCP Gateway...")

	// Get configuration from environment
	port := os.Getenv("MCP_PORT")
	if port == "" {
		port = defaultPort
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	// Connect to database
	log.Println("Connecting to database...")
	if err := database.Connect(databaseURL, logger.Warn); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Database connected")

	// Create MCP server
	server := mcp.NewServer("akmatori-mcp-gateway", version, log)

	// Register all tools
	registry := tools.NewRegistry(server, log)
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
	log.Printf("MCP Gateway listening on %s", addr)

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("Shutting down...")
		os.Exit(0)
	}()

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
