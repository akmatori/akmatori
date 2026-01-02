package mcp

import "encoding/json"

// JSON-RPC 2.0 message types for MCP protocol

// Request represents a JSON-RPC 2.0 request
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

// Error represents a JSON-RPC 2.0 error
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC error codes
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// MCP-specific message types

// InitializeParams represents the initialize request params
type InitializeParams struct {
	ProtocolVersion string           `json:"protocolVersion"`
	Capabilities    ClientCapability `json:"capabilities"`
	ClientInfo      ClientInfo       `json:"clientInfo"`
}

// ClientCapability represents client capabilities
type ClientCapability struct {
	Roots   *RootsCapability   `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// RootsCapability represents roots capability
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability represents sampling capability
type SamplingCapability struct{}

// ClientInfo represents client information
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult represents the initialize response
type InitializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	Capabilities    ServerCapability `json:"capabilities"`
	ServerInfo      ServerInfo       `json:"serverInfo"`
}

// ServerCapability represents server capabilities
type ServerCapability struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability represents tools capability
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo represents server information
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool represents an MCP tool definition
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema represents JSON schema for tool parameters
type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]Property    `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// Property represents a property in JSON schema
type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Items       *Items   `json:"items,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
}

// Items represents array items schema
type Items struct {
	Type string `json:"type"`
}

// ListToolsResult represents tools/list response
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolParams represents tools/call request params
type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// CallToolResult represents tools/call response
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content represents tool result content
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// NewTextContent creates a text content response
func NewTextContent(text string) Content {
	return Content{
		Type: "text",
		Text: text,
	}
}

// NewResponse creates a successful JSON-RPC response
func NewResponse(id interface{}, result interface{}) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// NewErrorResponse creates an error JSON-RPC response
func NewErrorResponse(id interface{}, code int, message string, data interface{}) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}
