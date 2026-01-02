package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ToolHandler is a function that handles a tool call
type ToolHandler func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error)

// Server represents an MCP server
type Server struct {
	name       string
	version    string
	tools      map[string]Tool
	handlers   map[string]ToolHandler
	mu         sync.RWMutex
	logger     *log.Logger
}

// NewServer creates a new MCP server
func NewServer(name, version string, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		name:     name,
		version:  version,
		tools:    make(map[string]Tool),
		handlers: make(map[string]ToolHandler),
		logger:   logger,
	}
}

// RegisterTool registers a tool with its handler
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[tool.Name] = tool
	s.handlers[tool.Name] = handler
	s.logger.Printf("Registered tool: %s", tool.Name)
}

// HandleHTTP handles HTTP requests for MCP protocol
// Supports both regular HTTP POST and SSE for streaming
func (s *Server) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract incident ID from header or query param
	incidentID := r.Header.Get("X-Incident-ID")
	if incidentID == "" {
		incidentID = r.URL.Query().Get("incident_id")
	}

	// Handle SSE endpoint for streaming
	if r.URL.Path == "/sse" || r.Header.Get("Accept") == "text/event-stream" {
		s.handleSSE(w, r, incidentID)
		return
	}

	// Handle regular HTTP POST for JSON-RPC
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.sendHTTPError(w, nil, ParseError, "Failed to read request body", nil)
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		s.sendHTTPError(w, nil, ParseError, "Invalid JSON", err.Error())
		return
	}

	resp := s.handleRequest(r.Context(), &req, incidentID)
	s.sendHTTPResponse(w, resp)
}

// handleSSE handles Server-Sent Events connection for MCP
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, incidentID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Send initial connection event
	fmt.Fprintf(w, "event: open\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Read messages from request body (for stdin-over-HTTP pattern)
	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.sendSSEError(w, flusher, nil, ParseError, "Invalid JSON", err.Error())
			continue
		}

		resp := s.handleRequest(r.Context(), &req, incidentID)
		s.sendSSEResponse(w, flusher, resp)
	}
}

// handleRequest processes a single JSON-RPC request
func (s *Server) handleRequest(ctx context.Context, req *Request, incidentID string) Response {
	if req.JSONRPC != "2.0" {
		return NewErrorResponse(req.ID, InvalidRequest, "Invalid JSON-RPC version", nil)
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		// Notification, no response needed
		return Response{}
	case "tools/list":
		return s.handleListTools(req)
	case "tools/call":
		return s.handleCallTool(ctx, req, incidentID)
	case "ping":
		return NewResponse(req.ID, map[string]interface{}{})
	default:
		return NewErrorResponse(req.ID, MethodNotFound, fmt.Sprintf("Unknown method: %s", req.Method), nil)
	}
}

// handleInitialize handles the initialize request
func (s *Server) handleInitialize(req *Request) Response {
	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: ServerCapability{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
		},
		ServerInfo: ServerInfo{
			Name:    s.name,
			Version: s.version,
		},
	}
	return NewResponse(req.ID, result)
}

// handleListTools handles the tools/list request
func (s *Server) handleListTools(req *Request) Response {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tools := make([]Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, tool)
	}

	return NewResponse(req.ID, ListToolsResult{Tools: tools})
}

// handleCallTool handles the tools/call request
func (s *Server) handleCallTool(ctx context.Context, req *Request, incidentID string) Response {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, InvalidParams, "Invalid tool call params", err.Error())
	}

	s.mu.RLock()
	handler, exists := s.handlers[params.Name]
	s.mu.RUnlock()

	if !exists {
		return NewErrorResponse(req.ID, MethodNotFound, fmt.Sprintf("Tool not found: %s", params.Name), nil)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	s.logger.Printf("Calling tool: %s (incident: %s)", params.Name, incidentID)

	result, err := handler(ctx, incidentID, params.Arguments)
	if err != nil {
		s.logger.Printf("Tool %s failed: %v", params.Name, err)
		return NewResponse(req.ID, CallToolResult{
			Content: []Content{NewTextContent(fmt.Sprintf("Error: %v", err))},
			IsError: true,
		})
	}

	// Convert result to string if needed
	var textResult string
	switch v := result.(type) {
	case string:
		textResult = v
	case []byte:
		textResult = string(v)
	default:
		jsonBytes, err := json.Marshal(result)
		if err != nil {
			textResult = fmt.Sprintf("%v", result)
		} else {
			textResult = string(jsonBytes)
		}
	}

	return NewResponse(req.ID, CallToolResult{
		Content: []Content{NewTextContent(textResult)},
	})
}

// sendHTTPResponse sends a JSON-RPC response over HTTP
func (s *Server) sendHTTPResponse(w http.ResponseWriter, resp Response) {
	// Skip empty responses (for notifications)
	if resp.JSONRPC == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// sendHTTPError sends an error response over HTTP
func (s *Server) sendHTTPError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	resp := NewErrorResponse(id, code, message, data)
	s.sendHTTPResponse(w, resp)
}

// sendSSEResponse sends a JSON-RPC response over SSE
func (s *Server) sendSSEResponse(w http.ResponseWriter, flusher http.Flusher, resp Response) {
	// Skip empty responses
	if resp.JSONRPC == "" {
		return
	}

	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	flusher.Flush()
}

// sendSSEError sends an error response over SSE
func (s *Server) sendSSEError(w http.ResponseWriter, flusher http.Flusher, id interface{}, code int, message string, data interface{}) {
	resp := NewErrorResponse(id, code, message, data)
	s.sendSSEResponse(w, flusher, resp)
}

// ParseToolName parses a tool name into namespace and action
// e.g., "ssh.execute_command" -> ("ssh", "execute_command")
func ParseToolName(name string) (namespace, action string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", name
}
