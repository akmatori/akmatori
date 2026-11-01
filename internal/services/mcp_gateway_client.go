package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// MCPGatewayClient talks to the MCP gateway's internal admin endpoints for
// remote MCP servers. The gateway listens only on the internal network.
type MCPGatewayClient struct {
	baseURL string
	http    *http.Client
}

// NewMCPGatewayClient creates a client for the gateway at baseURL.
func NewMCPGatewayClient(baseURL string) *MCPGatewayClient {
	return &MCPGatewayClient{baseURL: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// MCPTestRequest is the body of a connection test. With ID set and URL
// empty, the gateway uses the stored URL and auth; with ID and URL set, the
// stored auth is used when AuthConfig is nil.
type MCPTestRequest struct {
	ID         uint           `json:"id,omitempty"`
	URL        string         `json:"url,omitempty"`
	Transport  string         `json:"transport,omitempty"`
	AuthConfig database.JSONB `json:"auth_config,omitempty"`
}

// MCPTestTool is one tool a test connection discovered.
type MCPTestTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPTestResult is the gateway's answer to a connection test.
type MCPTestResult struct {
	OK              bool              `json:"ok"`
	ServerInfo      map[string]string `json:"server_info,omitempty"`
	ProtocolVersion string            `json:"protocol_version,omitempty"`
	Tools           []MCPTestTool     `json:"tools,omitempty"`
	Error           string            `json:"error,omitempty"`
	AuthRequired    bool              `json:"auth_required,omitempty"`
	WWWAuthenticate string            `json:"www_authenticate,omitempty"`
	StatusCode      int               `json:"status_code,omitempty"`
}

// Test opens a throwaway connection through the gateway (same network
// policy as real calls) and lists the server's tools.
func (c *MCPGatewayClient) Test(ctx context.Context, req MCPTestRequest) (*MCPTestResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mcp-servers/test", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("MCP gateway unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP gateway test returned %d: %s", resp.StatusCode, truncateBytes(raw, 300))
	}
	var out MCPTestResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode MCP gateway test result: %w", err)
	}
	return &out, nil
}

// Status returns the gateway's view of every loaded MCP server.
func (c *MCPGatewayClient) Status(ctx context.Context) (map[uint]MCPServerRuntimeStatus, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/mcp-servers/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("MCP gateway unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP gateway status returned %d", resp.StatusCode)
	}
	var body struct {
		Servers []MCPServerRuntimeStatus `json:"servers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode MCP gateway status: %w", err)
	}
	out := make(map[uint]MCPServerRuntimeStatus, len(body.Servers))
	for _, s := range body.Servers {
		out[s.ID] = s
	}
	return out, nil
}

// Reload asks the gateway to reload MCP server registrations.
func (c *MCPGatewayClient) Reload(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/reload/mcp-servers", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("gateway MCP reload request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway MCP reload returned status %d", resp.StatusCode)
	}
	return nil
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
