package mcpproxy

import (
	"context"
	"fmt"

	"github.com/akmatori/mcp-gateway/internal/mcp"
)

// ProbeResult is what a throwaway test connection learned about a server.
type ProbeResult struct {
	ServerInfo      ServerInfo
	ProtocolVersion string
	Tools           []mcp.Tool
}

// Probe opens a connection that is not stored in the pool, runs initialize
// and tools/list, and closes it. It applies the pool's network policy.
func (p *MCPConnectionPool) Probe(ctx context.Context, config MCPServerConfig) (*ProbeResult, error) {
	if !IsSupportedTransport(config.Transport) {
		return nil, fmt.Errorf("unsupported transport %q: only remote HTTP MCP servers are supported", config.Transport)
	}
	if err := p.policy.ValidateURL(config.URL); err != nil {
		return nil, err
	}
	auth, err := parseAuthConfig(config.AuthConfig)
	if err != nil {
		return nil, err
	}

	conn := p.newConnection(0, config, auth)
	defer conn.close()

	if err := defaultConnect(ctx, conn); err != nil {
		return nil, err
	}
	tools, err := p.fetchToolSchemas(ctx, conn)
	if err != nil {
		return nil, err
	}

	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return &ProbeResult{
		ServerInfo:      conn.serverInfo,
		ProtocolVersion: conn.protocolVersion,
		Tools:           tools,
	}, nil
}
