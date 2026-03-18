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

	"github.com/akmatori/mcp-gateway/internal/auth"
)

// ToolHandler is a function that handles a tool call
type ToolHandler func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error)

// ToolDiscoverer provides tool search and detail capabilities.
type ToolDiscoverer interface {
	SearchTools(query string, toolType string) []SearchToolsResultItem
	GetToolDetail(toolName string) (*GetToolDetailResult, bool)
	GetAvailableToolTypes() []string
}

// InstanceLookup provides instance information for tool discovery responses.
type InstanceLookup func(toolType string) []ToolDetailInstance

// Server represents an MCP server
type Server struct {
	name            string
	version         string
	tools           map[string]Tool
	handlers        map[string]ToolHandler
	mu              sync.RWMutex
	logger          *log.Logger
	discoverer      ToolDiscoverer
	instanceLookup  InstanceLookup
	authorizer      *auth.Authorizer
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

// SetDiscoverer sets the tool discoverer for search/detail endpoints.
func (s *Server) SetDiscoverer(d ToolDiscoverer) {
	s.discoverer = d
}

// SetInstanceLookup sets the function used to look up enabled instances by tool type.
func (s *Server) SetInstanceLookup(fn InstanceLookup) {
	s.instanceLookup = fn
}

// SetAuthorizer sets the authorizer used to enforce per-incident tool allowlists.
func (s *Server) SetAuthorizer(a *auth.Authorizer) {
	s.authorizer = a
}

// Tools returns the tools map for external read access.
func (s *Server) Tools() map[string]Tool {
	return s.tools
}

// Mu returns the server's read-write mutex for external synchronization.
func (s *Server) Mu() *sync.RWMutex {
	return &s.mu
}

// RegisterTool registers a tool with its handler
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[tool.Name] = tool
	s.handlers[tool.Name] = handler
	s.logger.Printf("Registered tool: %s", tool.Name)
}

// UnregisterTool removes a tool and its handler by name.
func (s *Server) UnregisterTool(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tools, name)
	delete(s.handlers, name)
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

	// Parse and register tool allowlist from header (sent per-request by agent worker)
	if s.authorizer != nil && incidentID != "" {
		if allowlistHeader := r.Header.Get("X-Tool-Allowlist"); allowlistHeader != "" {
			var entries []auth.AllowlistEntry
			if err := json.Unmarshal([]byte(allowlistHeader), &entries); err != nil {
				s.logger.Printf("WARN: malformed X-Tool-Allowlist header for incident %s: %v", incidentID, err)
			} else {
				s.authorizer.SetAllowlist(incidentID, entries)
			}
		}
	}

	// Handle regular HTTP POST for JSON-RPC
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 10MB to prevent memory exhaustion
	const maxRequestBytes = 10 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
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
	// Parse and register tool allowlist from header (same as HTTP POST path)
	if s.authorizer != nil && incidentID != "" {
		if allowlistHeader := r.Header.Get("X-Tool-Allowlist"); allowlistHeader != "" {
			var entries []auth.AllowlistEntry
			if err := json.Unmarshal([]byte(allowlistHeader), &entries); err != nil {
				s.logger.Printf("WARN: malformed X-Tool-Allowlist header for incident %s: %v", incidentID, err)
			} else {
				s.authorizer.SetAllowlist(incidentID, entries)
			}
		}
	}

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
	case "tools/search":
		return s.handleSearchTools(req, incidentID)
	case "tools/detail":
		return s.handleGetToolDetail(req, incidentID)
	case "tools/list_types":
		return s.handleListToolTypes(req, incidentID)
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

	// Inject instance hint into arguments so authorization and tool handlers can use it
	if params.Instance != "" {
		if params.Arguments == nil {
			params.Arguments = make(map[string]interface{})
		}
		if _, exists := params.Arguments["logical_name"]; !exists {
			params.Arguments["logical_name"] = params.Instance
		}
	}

	// Enforce tool allowlist authorization.
	// MCP proxy tools use compound namespaces (e.g., "ext.github") that contain dots.
	// Standard tool types (ssh, zabbix, victoria_metrics) never contain dots.
	// Proxy tools are admin-configured and don't participate in skill-based tool
	// assignment, so they bypass the per-incident allowlist.
	if s.authorizer != nil && incidentID != "" {
		toolType, _ := ParseToolName(params.Name)
		if !strings.Contains(toolType, ".") {
			instanceID := extractInstanceIDFromArgs(params.Arguments)
			logicalName := extractLogicalNameFromArgs(params.Arguments)

			if !s.authorizer.IsAuthorized(incidentID, toolType, instanceID, logicalName) {
				return NewErrorResponse(req.ID, InvalidRequest,
					fmt.Sprintf("Unauthorized: incident %s is not authorized to use tool %s", incidentID, params.Name),
					nil)
			}
		}
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

// handleSearchTools handles the tools/search request
func (s *Server) handleSearchTools(req *Request, incidentID string) Response {
	if s.discoverer == nil {
		return NewErrorResponse(req.ID, InternalError, "Tool discovery not configured", nil)
	}

	var params SearchToolsParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return NewErrorResponse(req.ID, InvalidParams, "Invalid search params", err.Error())
		}
	}

	results := s.discoverer.SearchTools(params.Query, params.ToolType)

	// Populate instance logical names if lookup is available
	if s.instanceLookup != nil {
		for i := range results {
			instances := s.instanceLookup(results[i].ToolType)
			names := make([]string, 0, len(instances))
			for _, inst := range instances {
				if inst.LogicalName != "" {
					names = append(names, inst.LogicalName)
				}
			}
			results[i].Instances = names
		}
	}

	// Filter by allowlist: only include tools with at least one authorized instance
	if s.authorizer != nil && incidentID != "" {
		allowlist := s.authorizer.GetAllowlist(incidentID)
		if allowlist != nil {
			results = filterSearchResultsByAllowlist(results, allowlist)
		}
	}

	if results == nil {
		results = []SearchToolsResultItem{}
	}

	// When no results found, provide a hint about available tool types
	var hint string
	if len(results) == 0 && params.Query != "" {
		availableTypes := s.discoverer.GetAvailableToolTypes()
		// Filter by allowlist if present
		if s.authorizer != nil && incidentID != "" {
			allowlist := s.authorizer.GetAllowlist(incidentID)
			if allowlist != nil {
				authorizedTypes := make(map[string]bool)
				for _, e := range allowlist {
					authorizedTypes[e.ToolType] = true
				}
				var filtered []string
				for _, t := range availableTypes {
					if authorizedTypes[t] {
						filtered = append(filtered, t)
					}
				}
				availableTypes = filtered
			}
		}
		if len(availableTypes) > 0 {
			hint = fmt.Sprintf("No tools matched '%s'. Available tool types: %s. Try: list_tools_for_tool_type({query: '%s'})",
				params.Query, strings.Join(availableTypes, ", "), availableTypes[0])
		} else {
			hint = fmt.Sprintf("No tools matched '%s'. No tool types are available for this incident.", params.Query)
		}
	}

	return NewResponse(req.ID, SearchToolsResult{Tools: results, Hint: hint})
}

// handleGetToolDetail handles the tools/detail request
func (s *Server) handleGetToolDetail(req *Request, incidentID string) Response {
	if s.discoverer == nil {
		return NewErrorResponse(req.ID, InternalError, "Tool discovery not configured", nil)
	}

	var params GetToolDetailParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, InvalidParams, "Invalid detail params", err.Error())
	}

	if params.ToolName == "" {
		return NewErrorResponse(req.ID, InvalidParams, "tool_name is required", nil)
	}

	detail, found := s.discoverer.GetToolDetail(params.ToolName)
	if !found {
		return NewErrorResponse(req.ID, MethodNotFound, fmt.Sprintf("Tool not found: %s", params.ToolName), nil)
	}

	// Populate instances if lookup is available
	if s.instanceLookup != nil {
		detail.Instances = s.instanceLookup(detail.ToolType)
	}

	// Filter instances by allowlist
	if s.authorizer != nil && incidentID != "" {
		allowlist := s.authorizer.GetAllowlist(incidentID)
		if allowlist != nil {
			detail.Instances = filterInstancesByAllowlist(detail.Instances, detail.ToolType, allowlist)
		}
	}

	return NewResponse(req.ID, detail)
}

// handleListToolTypes handles the tools/list_types request
func (s *Server) handleListToolTypes(req *Request, incidentID string) Response {
	if s.discoverer == nil {
		return NewErrorResponse(req.ID, InternalError, "Tool discovery not configured", nil)
	}

	types := s.discoverer.GetAvailableToolTypes()

	// Filter by allowlist if authorizer and incident ID are present
	if s.authorizer != nil && incidentID != "" {
		allowlist := s.authorizer.GetAllowlist(incidentID)
		if allowlist != nil {
			authorizedTypes := make(map[string]bool)
			for _, e := range allowlist {
				authorizedTypes[e.ToolType] = true
			}
			var filtered []string
			for _, t := range types {
				if authorizedTypes[t] {
					filtered = append(filtered, t)
				}
			}
			types = filtered
		}
	}

	if types == nil {
		types = []string{}
	}

	return NewResponse(req.ID, ListToolTypesResult{Types: types})
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

// ParseToolName parses a tool name into namespace (tool type) and action.
// Splits on the last dot so multi-segment namespaces work correctly:
//
//	"ssh.execute_command"       -> ("ssh", "execute_command")
//	"ext.github.create_issue"  -> ("ext.github", "create_issue")
func ParseToolName(name string) (namespace, action string) {
	idx := strings.LastIndex(name, ".")
	if idx >= 0 {
		return name[:idx], name[idx+1:]
	}
	return "", name
}

// extractInstanceIDFromArgs extracts the optional tool_instance_id from tool arguments.
func extractInstanceIDFromArgs(args map[string]interface{}) uint {
	if v, ok := args["tool_instance_id"].(float64); ok && v > 0 {
		return uint(v)
	}
	return 0
}

// extractLogicalNameFromArgs extracts the optional logical_name from tool arguments.
func extractLogicalNameFromArgs(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

// filterSearchResultsByAllowlist removes tools that have no authorized instances.
// A tool is kept if any allowlist entry matches its tool type.
func filterSearchResultsByAllowlist(results []SearchToolsResultItem, allowlist []auth.AllowlistEntry) []SearchToolsResultItem {
	// Build set of authorized tool types
	authorizedTypes := make(map[string]bool)
	for _, e := range allowlist {
		authorizedTypes[e.ToolType] = true
	}

	filtered := make([]SearchToolsResultItem, 0, len(results))
	for _, item := range results {
		if !authorizedTypes[item.ToolType] {
			continue
		}
		// Also filter the instance names to only authorized ones
		if len(item.Instances) > 0 {
			authorizedNames := make([]string, 0)
			for _, name := range item.Instances {
				for _, e := range allowlist {
					if e.ToolType == item.ToolType && e.LogicalName == name {
						authorizedNames = append(authorizedNames, name)
						break
					}
				}
			}
			item.Instances = authorizedNames
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// filterInstancesByAllowlist filters tool detail instances to only those in the allowlist.
func filterInstancesByAllowlist(instances []ToolDetailInstance, toolType string, allowlist []auth.AllowlistEntry) []ToolDetailInstance {
	filtered := make([]ToolDetailInstance, 0, len(instances))
	for _, inst := range instances {
		for _, e := range allowlist {
			if e.ToolType == toolType && (e.InstanceID == inst.ID || (e.LogicalName != "" && e.LogicalName == inst.LogicalName)) {
				filtered = append(filtered, inst)
				break
			}
		}
	}
	return filtered
}
