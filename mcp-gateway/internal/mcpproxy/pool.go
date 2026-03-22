package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/mcp"
)

const (
	// DefaultIdleTimeout is how long a connection sits idle before being closed.
	DefaultIdleTimeout = 5 * time.Minute

	// DefaultCleanupInterval is how often the pool checks for idle connections.
	DefaultCleanupInterval = 1 * time.Minute

	// DefaultSchemaRefreshInterval is how often tool schemas are refreshed.
	DefaultSchemaRefreshInterval = 5 * time.Minute

	// DefaultSchemaCacheTTL is the TTL for cached tool schemas.
	DefaultSchemaCacheTTL = 5 * time.Minute

	// DefaultConnectTimeout is the timeout for establishing a new connection.
	DefaultConnectTimeout = 30 * time.Second

	// DefaultMaxReconnectAttempts is how many times to retry a connection on transient failure.
	DefaultMaxReconnectAttempts = 3

	// DefaultBaseBackoff is the initial backoff duration for reconnect retries.
	DefaultBaseBackoff = 500 * time.Millisecond

	// DefaultMaxBackoff is the maximum backoff duration for reconnect retries.
	DefaultMaxBackoff = 10 * time.Second
)

// TransportType defines how to communicate with an external MCP server.
type TransportType string

const (
	TransportSSE   TransportType = "sse"
	TransportStdio TransportType = "stdio"
)

// MCPServerConfig defines the configuration for connecting to an external MCP server.
type MCPServerConfig struct {
	Transport       TransportType     `json:"transport"`
	URL             string            `json:"url,omitempty"`              // For SSE transport
	Command         string            `json:"command,omitempty"`          // For stdio transport
	Args            []string          `json:"args,omitempty"`             // For stdio transport
	EnvVars         map[string]string `json:"env_vars,omitempty"`         // For stdio transport
	NamespacePrefix string            `json:"namespace_prefix,omitempty"` // e.g., "ext.github"
	AuthConfig      json.RawMessage   `json:"auth_config,omitempty"`      // Auth to inject
}

// MCPConnection represents an active connection to an external MCP server.
type MCPConnection struct {
	mu         sync.RWMutex
	config     MCPServerConfig
	instanceID uint
	tools      []mcp.Tool
	lastUsed   time.Time
	connected  bool
	closed     bool

	// SSE transport
	httpClient *http.Client
	sseCancel  context.CancelFunc
	sessionID  string // MCP Streamable HTTP session ID (if established)

	// Stdio transport
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	// JSON-RPC state
	nextID int
	idMu   sync.Mutex

	logger *slog.Logger
}

// ConnectionStatus represents the health status of a single MCP connection.
type ConnectionStatus struct {
	InstanceID uint          `json:"instance_id"`
	Transport  TransportType `json:"transport"`
	Connected  bool          `json:"connected"`
	ToolCount  int           `json:"tool_count"`
	LastUsed   time.Time     `json:"last_used"`
	Error      string        `json:"error,omitempty"`
}

// MCPConnectionPool manages connections to external MCP servers.
type MCPConnectionPool struct {
	mu          sync.RWMutex
	connections map[uint]*MCPConnection
	configs     map[uint]MCPServerConfig // stored configs for reconnection
	schemaCache *cache.Cache
	idleTimeout time.Duration
	stopCleanup chan struct{}
	stopRefresh chan struct{}
	stopped     bool
	logger      *slog.Logger

	// Reconnect settings
	maxReconnectAttempts int
	baseBackoff          time.Duration
	maxBackoff           time.Duration

	// Schema refresh callback (called when tools change during refresh)
	onSchemaRefresh func(instanceID uint, tools []mcp.Tool)

	// For testing: allow overriding connect behavior
	connectFunc func(ctx context.Context, conn *MCPConnection) error
}

// PoolOption configures the connection pool.
type PoolOption func(*MCPConnectionPool)

// WithIdleTimeout sets the idle timeout for connections.
func WithIdleTimeout(d time.Duration) PoolOption {
	return func(p *MCPConnectionPool) {
		p.idleTimeout = d
	}
}

// WithLogger sets the logger for the pool.
func WithLogger(logger *slog.Logger) PoolOption {
	return func(p *MCPConnectionPool) {
		p.logger = logger
	}
}

// WithConnectFunc overrides the connection function (for testing).
func WithConnectFunc(f func(ctx context.Context, conn *MCPConnection) error) PoolOption {
	return func(p *MCPConnectionPool) {
		p.connectFunc = f
	}
}

// WithMaxReconnectAttempts sets the maximum number of reconnect attempts.
func WithMaxReconnectAttempts(n int) PoolOption {
	return func(p *MCPConnectionPool) {
		p.maxReconnectAttempts = n
	}
}

// WithBackoff sets the base and max backoff durations for reconnection.
func WithBackoff(base, max time.Duration) PoolOption {
	return func(p *MCPConnectionPool) {
		p.baseBackoff = base
		p.maxBackoff = max
	}
}

// WithSchemaRefreshCallback sets a callback invoked when tools change during periodic refresh.
func WithSchemaRefreshCallback(cb func(instanceID uint, tools []mcp.Tool)) PoolOption {
	return func(p *MCPConnectionPool) {
		p.onSchemaRefresh = cb
	}
}

// SetSchemaRefreshCallback sets the schema refresh callback in a thread-safe manner.
// Use this when setting the callback after pool construction.
func (p *MCPConnectionPool) SetSchemaRefreshCallback(cb func(instanceID uint, tools []mcp.Tool)) {
	p.mu.Lock()
	p.onSchemaRefresh = cb
	p.mu.Unlock()
}

// NewPool creates a new MCP connection pool.
func NewPool(opts ...PoolOption) *MCPConnectionPool {
	p := &MCPConnectionPool{
		connections:          make(map[uint]*MCPConnection),
		configs:              make(map[uint]MCPServerConfig),
		schemaCache:          cache.New(DefaultSchemaCacheTTL, DefaultCleanupInterval),
		idleTimeout:          DefaultIdleTimeout,
		stopCleanup:          make(chan struct{}),
		stopRefresh:          make(chan struct{}),
		logger:               slog.Default(),
		maxReconnectAttempts: DefaultMaxReconnectAttempts,
		baseBackoff:          DefaultBaseBackoff,
		maxBackoff:           DefaultMaxBackoff,
	}
	for _, opt := range opts {
		opt(p)
	}
	go p.cleanupLoop()
	return p
}

// GetOrConnect returns an existing connection or establishes a new one.
func (p *MCPConnectionPool) GetOrConnect(ctx context.Context, instanceID uint, config MCPServerConfig) (*MCPConnection, error) {
	// Use a full lock to avoid TOCTOU races where two goroutines could both
	// create connections for the same instanceID, leaking one subprocess.
	p.mu.Lock()

	if p.stopped {
		p.mu.Unlock()
		return nil, fmt.Errorf("connection pool is stopped")
	}

	conn, exists := p.connections[instanceID]

	if exists {
		conn.mu.RLock()
		connected := conn.connected && !conn.closed
		conn.mu.RUnlock()

		if connected {
			conn.mu.Lock()
			conn.lastUsed = time.Now()
			conn.mu.Unlock()
			p.mu.Unlock()
			return conn, nil
		}
		// Connection exists but is not healthy — remove and reconnect
		delete(p.connections, instanceID)
		go conn.close()
	}

	// Release pool lock while connecting (slow I/O), but mark the slot
	// so concurrent callers for the same instanceID wait.
	p.mu.Unlock()

	// Create new connection
	conn = &MCPConnection{
		config:     config,
		instanceID: instanceID,
		lastUsed:   time.Now(),
		httpClient: &http.Client{Timeout: DefaultConnectTimeout},
		logger:     p.logger.With("instance_id", instanceID, "transport", config.Transport),
	}

	connectFn := p.connectFunc
	if connectFn == nil {
		connectFn = defaultConnect
	}

	if err := connectFn(ctx, conn); err != nil {
		return nil, fmt.Errorf("connect to MCP server (instance %d): %w", instanceID, err)
	}

	conn.mu.Lock()
	conn.connected = true
	conn.mu.Unlock()

	// Fetch tool schemas
	tools, err := p.fetchToolSchemas(ctx, conn)
	if err != nil {
		conn.close()
		return nil, fmt.Errorf("fetch tool schemas (instance %d): %w", instanceID, err)
	}

	conn.mu.Lock()
	conn.tools = tools
	conn.mu.Unlock()

	// Cache the tools
	cacheKey := fmt.Sprintf("tools:%d", instanceID)
	p.schemaCache.Set(cacheKey, tools)

	// Re-acquire to insert. If the pool was stopped while we were connecting,
	// close the new connection and return an error. If another goroutine raced
	// and inserted first, close the duplicate and return the existing one.
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		conn.close()
		return nil, fmt.Errorf("connection pool is stopped")
	}
	if existing, ok := p.connections[instanceID]; ok {
		p.mu.Unlock()
		conn.close()
		existing.mu.Lock()
		existing.lastUsed = time.Now()
		existing.mu.Unlock()
		return existing, nil
	}
	p.connections[instanceID] = conn
	p.configs[instanceID] = config
	p.mu.Unlock()

	p.logger.Info("connected to MCP server",
		"instance_id", instanceID,
		"transport", config.Transport,
		"tools_count", len(tools),
	)

	return conn, nil
}

// GetCachedTools returns cached tool schemas for an instance without connecting.
func (p *MCPConnectionPool) GetCachedTools(instanceID uint) ([]mcp.Tool, bool) {
	cacheKey := fmt.Sprintf("tools:%d", instanceID)
	if val, ok := p.schemaCache.Get(cacheKey); ok {
		if tools, ok := val.([]mcp.Tool); ok {
			return tools, true
		}
	}
	return nil, false
}

// CallTool forwards a tool call to the external MCP server.
// On transient failures, it attempts to reconnect with exponential backoff.
func (p *MCPConnectionPool) CallTool(ctx context.Context, instanceID uint, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	p.mu.RLock()
	conn, exists := p.connections[instanceID]
	config, hasConfig := p.configs[instanceID]
	p.mu.RUnlock()

	if !exists {
		if !hasConfig {
			return nil, fmt.Errorf("no connection for instance %d", instanceID)
		}
		// Connection was cleaned up (e.g., by idle timeout); re-establish it.
		reconn, err := p.GetOrConnect(ctx, instanceID, config)
		if err != nil {
			// Transient connect failures should be retried with backoff
			// rather than failing immediately.
			p.logger.Warn("idle reconnect failed, attempting retries",
				"instance_id", instanceID,
				"tool", toolName,
				"error", err,
			)
			return p.retryWithBackoff(ctx, instanceID, nil, config, toolName, args, err)
		}
		reconn.mu.Lock()
		reconn.lastUsed = time.Now()
		reconn.mu.Unlock()
		result, callErr := reconn.callTool(ctx, toolName, args)
		if callErr == nil {
			return result, nil
		}
		// The first call on the fresh connection failed. Run the same
		// transient-retry loop that the normal path uses so the caller
		// gets the documented reconnect retries instead of a bare error.
		if !isTransientError(callErr) {
			return nil, callErr
		}
		p.logger.Warn("transient error calling tool after idle reconnect, attempting retries",
			"instance_id", instanceID,
			"tool", toolName,
			"error", callErr,
		)
		return p.retryWithBackoff(ctx, instanceID, reconn, config, toolName, args, callErr)
	}

	conn.mu.Lock()
	conn.lastUsed = time.Now()
	conn.mu.Unlock()

	result, err := conn.callTool(ctx, toolName, args)
	if err == nil {
		return result, nil
	}

	// On failure, attempt reconnection with exponential backoff
	if !hasConfig {
		return nil, fmt.Errorf("tool call failed (no config for reconnect): %w", err)
	}

	if !isTransientError(err) {
		return nil, err
	}

	p.logger.Warn("transient error calling tool, attempting reconnect",
		"instance_id", instanceID,
		"tool", toolName,
		"error", err,
	)

	return p.retryWithBackoff(ctx, instanceID, conn, config, toolName, args, err)
}

// retryWithBackoff attempts to reconnect and retry a tool call with exponential backoff.
func (p *MCPConnectionPool) retryWithBackoff(ctx context.Context, instanceID uint, conn *MCPConnection, config MCPServerConfig, toolName string, args map[string]interface{}, originalErr error) (*mcp.CallToolResult, error) {
	backoff := p.baseBackoff
	lastErr := originalErr
	for attempt := 1; attempt <= p.maxReconnectAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("reconnect cancelled: %w", ctx.Err())
		case <-time.After(backoff):
		}

		// Remove old connection and reconnect
		if conn != nil {
			p.mu.Lock()
			delete(p.connections, instanceID)
			p.mu.Unlock()
			conn.close()
		}

		newConn, connErr := p.GetOrConnect(ctx, instanceID, config)
		if connErr != nil {
			lastErr = connErr
			p.logger.Warn("reconnect attempt failed",
				"instance_id", instanceID,
				"attempt", attempt,
				"error", connErr,
			)
			backoff = nextBackoff(backoff, p.maxBackoff)
			continue
		}

		// Retry the tool call
		result, retryErr := newConn.callTool(ctx, toolName, args)
		if retryErr == nil {
			p.logger.Info("reconnect successful, tool call succeeded",
				"instance_id", instanceID,
				"attempt", attempt,
			)
			return result, nil
		}

		lastErr = retryErr
		p.logger.Warn("tool call failed after reconnect",
			"instance_id", instanceID,
			"attempt", attempt,
			"error", retryErr,
		)
		conn = newConn
		backoff = nextBackoff(backoff, p.maxBackoff)
	}

	return nil, fmt.Errorf("tool call failed after %d reconnect attempts: %w", p.maxReconnectAttempts, lastErr)
}

// isTransientError checks whether an error is likely transient (network, connection closed).
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"connection is closed",
		"EOF",
		"broken pipe",
		"server unreachable",
		"timeout",
		"i/o timeout",
		"write to stdin",
		"read from stdout",
		"send request",
	}
	for _, pattern := range transientPatterns {
		if containsCI(msg, pattern) {
			return true
		}
	}
	return false
}

// containsCI is a simple case-insensitive substring check.
func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// nextBackoff doubles the backoff up to the max.
func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// StartSchemaRefreshLoop starts a background goroutine that periodically
// refreshes tool schemas for all connected instances.
func (p *MCPConnectionPool) StartSchemaRefreshLoop(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.refreshAllSchemas()
			case <-p.stopRefresh:
				return
			}
		}
	}()
}

// refreshAllSchemas refreshes tool schemas for all active connections.
func (p *MCPConnectionPool) refreshAllSchemas() {
	p.mu.RLock()
	ids := make([]uint, 0, len(p.connections))
	for id := range p.connections {
		ids = append(ids, id)
	}
	p.mu.RUnlock()

	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectTimeout)
		oldTools, _ := p.GetTools(id)
		newTools, err := p.RefreshTools(ctx, id)
		cancel()

		if err != nil {
			p.logger.Warn("schema refresh failed", "instance_id", id, "error", err)
			continue
		}

		p.mu.RLock()
		cb := p.onSchemaRefresh
		p.mu.RUnlock()
		if cb != nil && toolsChanged(oldTools, newTools) {
			cb(id, newTools)
		}
	}
}

// toolsChanged compares two tool lists by name.
func toolsChanged(old, new []mcp.Tool) bool {
	if len(old) != len(new) {
		return true
	}
	oldNames := make(map[string]bool, len(old))
	for _, t := range old {
		oldNames[t.Name] = true
	}
	for _, t := range new {
		if !oldNames[t.Name] {
			return true
		}
	}
	return false
}

// HealthStatus returns the status of all managed connections.
func (p *MCPConnectionPool) HealthStatus(ctx context.Context) []ConnectionStatus {
	p.mu.RLock()
	ids := make([]uint, 0, len(p.connections))
	for id := range p.connections {
		ids = append(ids, id)
	}
	p.mu.RUnlock()

	statuses := make([]ConnectionStatus, 0, len(ids))
	for _, id := range ids {
		p.mu.RLock()
		conn, exists := p.connections[id]
		p.mu.RUnlock()

		if !exists {
			continue
		}

		conn.mu.RLock()
		status := ConnectionStatus{
			InstanceID: id,
			Transport:  conn.config.Transport,
			Connected:  conn.connected && !conn.closed,
			ToolCount:  len(conn.tools),
			LastUsed:   conn.lastUsed,
		}
		conn.mu.RUnlock()

		// Ping to check actual health
		if status.Connected {
			if err := conn.ping(ctx); err != nil {
				status.Error = err.Error()
				status.Connected = false
			}
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// Close closes a specific connection.
func (p *MCPConnectionPool) Close(instanceID uint) {
	p.mu.Lock()
	conn, exists := p.connections[instanceID]
	if exists {
		delete(p.connections, instanceID)
	}
	p.mu.Unlock()

	if exists {
		conn.close()
		p.logger.Info("closed MCP connection", "instance_id", instanceID)
	}
}

// CloseAll closes all connections, stops the cleanup and schema refresh goroutines.
func (p *MCPConnectionPool) CloseAll() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	conns := make(map[uint]*MCPConnection, len(p.connections))
	for k, v := range p.connections {
		conns[k] = v
	}
	p.connections = make(map[uint]*MCPConnection)
	p.configs = make(map[uint]MCPServerConfig)
	p.mu.Unlock()

	close(p.stopCleanup)
	close(p.stopRefresh)
	p.schemaCache.Stop()

	for _, conn := range conns {
		conn.close()
	}

	p.logger.Info("closed all MCP connections", "count", len(conns))
}

// ConnectionCount returns the number of active connections.
func (p *MCPConnectionPool) ConnectionCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}

// IsConnected checks if an instance has an active connection.
func (p *MCPConnectionPool) IsConnected(instanceID uint) bool {
	p.mu.RLock()
	conn, exists := p.connections[instanceID]
	p.mu.RUnlock()

	if !exists {
		return false
	}

	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.connected && !conn.closed
}

// GetTools returns the discovered tools for a connected instance.
func (p *MCPConnectionPool) GetTools(instanceID uint) ([]mcp.Tool, bool) {
	p.mu.RLock()
	conn, exists := p.connections[instanceID]
	p.mu.RUnlock()

	if !exists {
		return nil, false
	}

	conn.mu.RLock()
	defer conn.mu.RUnlock()
	if conn.tools == nil {
		return nil, false
	}
	toolsCopy := make([]mcp.Tool, len(conn.tools))
	copy(toolsCopy, conn.tools)
	return toolsCopy, true
}

// HealthCheck checks the health of a specific connection.
func (p *MCPConnectionPool) HealthCheck(ctx context.Context, instanceID uint) error {
	p.mu.RLock()
	conn, exists := p.connections[instanceID]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no connection for instance %d", instanceID)
	}

	return conn.ping(ctx)
}

// RefreshTools re-fetches tool schemas from a connected server.
func (p *MCPConnectionPool) RefreshTools(ctx context.Context, instanceID uint) ([]mcp.Tool, error) {
	p.mu.RLock()
	conn, exists := p.connections[instanceID]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no connection for instance %d", instanceID)
	}

	tools, err := p.fetchToolSchemas(ctx, conn)
	if err != nil {
		return nil, err
	}

	conn.mu.Lock()
	conn.tools = tools
	conn.mu.Unlock()

	cacheKey := fmt.Sprintf("tools:%d", instanceID)
	p.schemaCache.Set(cacheKey, tools)

	return tools, nil
}

// cleanupLoop runs periodically to close idle connections.
func (p *MCPConnectionPool) cleanupLoop() {
	ticker := time.NewTicker(DefaultCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.cleanupIdle()
		case <-p.stopCleanup:
			return
		}
	}
}

// cleanupIdle closes connections that have been idle beyond the timeout.
func (p *MCPConnectionPool) cleanupIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for id, conn := range p.connections {
		conn.mu.RLock()
		idle := now.Sub(conn.lastUsed) > p.idleTimeout
		conn.mu.RUnlock()

		if idle {
			delete(p.connections, id)
			go conn.close()
			p.logger.Info("closed idle MCP connection", "instance_id", id)
		}
	}
}

// fetchToolSchemas sends tools/list to the external MCP server.
func (p *MCPConnectionPool) fetchToolSchemas(ctx context.Context, conn *MCPConnection) ([]mcp.Tool, error) {
	resp, err := conn.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tools/list result: %w", err)
	}

	var listResult mcp.ListToolsResult
	if err := json.Unmarshal(resultBytes, &listResult); err != nil {
		return nil, fmt.Errorf("unmarshal tools/list result: %w", err)
	}

	return listResult.Tools, nil
}

// --- MCPConnection methods ---

func (c *MCPConnection) nextRequestID() int {
	c.idMu.Lock()
	defer c.idMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *MCPConnection) sendRequest(ctx context.Context, method string, params interface{}) (*mcp.Response, error) {
	c.mu.RLock()
	transport := c.config.Transport
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return nil, fmt.Errorf("connection is closed")
	}

	switch transport {
	case TransportSSE:
		return c.sendSSERequest(ctx, method, params)
	case TransportStdio:
		return c.sendStdioRequest(ctx, method, params)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", transport)
	}
}

func (c *MCPConnection) sendSSERequest(ctx context.Context, method string, params interface{}) (*mcp.Response, error) {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	reqBody := mcp.Request{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  method,
		Params:  paramsRaw,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// For SSE transport, POST to the server's /mcp endpoint
	url := c.config.URL
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	// Include MCP session ID if one was established during initialize
	c.mu.RLock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.RUnlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var mcpResp mcp.Response
	if err := json.NewDecoder(resp.Body).Decode(&mcpResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &mcpResp, nil
}

func (c *MCPConnection) sendStdioRequest(ctx context.Context, method string, params interface{}) (*mcp.Response, error) {
	// Grab references to stdin/stdout under the lock, then release it before
	// blocking on I/O to avoid holding the mutex while waiting for the subprocess.
	c.mu.RLock()
	stdinRef := c.stdin
	stdoutRef := c.stdout
	closed := c.closed
	c.mu.RUnlock()

	if closed || stdinRef == nil || stdoutRef == nil {
		return nil, fmt.Errorf("stdio not initialized")
	}

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	reqBody := mcp.Request{
		JSONRPC: "2.0",
		ID:      c.nextRequestID(),
		Method:  method,
		Params:  paramsRaw,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Stdio is a serial protocol — serialize write+read as a pair,
	// but use a dedicated mutex so we don't block close() or status checks.
	c.idMu.Lock()
	defer c.idMu.Unlock()

	// Write request followed by newline
	bodyBytes = append(bodyBytes, '\n')
	if _, err := stdinRef.Write(bodyBytes); err != nil {
		return nil, fmt.Errorf("write to stdin: %w", err)
	}

	// Read response line
	type result struct {
		resp *mcp.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := stdoutRef.ReadBytes('\n')
		if err != nil {
			ch <- result{nil, fmt.Errorf("read from stdout: %w", err)}
			return
		}
		var mcpResp mcp.Response
		if err := json.Unmarshal(line, &mcpResp); err != nil {
			ch <- result{nil, fmt.Errorf("decode response: %w", err)}
			return
		}
		ch <- result{&mcpResp, nil}
	}()

	select {
	case <-ctx.Done():
		// The read goroutine is still running and will consume the next response
		// from stdout, desynchronizing the stdio protocol. Mark the connection
		// as unhealthy so the pool reconnects on the next call.
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		return nil, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}

func (c *MCPConnection) callTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	params := mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	resp, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("tool call error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}

	var callResult mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &callResult); err != nil {
		return nil, fmt.Errorf("unmarshal tool result: %w", err)
	}

	return &callResult, nil
}

func (c *MCPConnection) ping(ctx context.Context) error {
	resp, err := c.sendRequest(ctx, "ping", nil)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("ping error: %s", resp.Error.Message)
	}
	return nil
}

func (c *MCPConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}
	c.closed = true
	c.connected = false

	if c.sseCancel != nil {
		c.sseCancel()
	}

	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.stderr != nil {
		c.stderr.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
}

// defaultConnect establishes a connection based on transport type.
func defaultConnect(ctx context.Context, conn *MCPConnection) error {
	switch conn.config.Transport {
	case TransportSSE:
		return connectSSE(ctx, conn)
	case TransportStdio:
		return connectStdio(ctx, conn)
	default:
		return fmt.Errorf("unsupported transport type: %s", conn.config.Transport)
	}
}

// connectSSE validates the SSE endpoint is reachable.
// It first tries an MCP Streamable HTTP initialize handshake (required by servers
// like QMD). If that fails, it falls back to a basic reachability check for
// legacy SSE servers.
func connectSSE(ctx context.Context, conn *MCPConnection) error {
	if conn.config.URL == "" {
		return fmt.Errorf("URL is required for SSE transport")
	}

	// Try MCP Streamable HTTP initialize handshake first.
	// Servers like QMD require this to establish a session before accepting tool calls.
	initErr := tryMCPInitialize(ctx, conn)
	if initErr == nil {
		return nil
	}
	slog.Debug("MCP initialize handshake failed, falling back to basic reachability check", "url", conn.config.URL, "error", initErr)

	// Fall back to basic reachability check for non-session servers
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.config.URL, nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}

	resp, err := conn.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	resp.Body.Close()

	return nil
}

// tryMCPInitialize performs the MCP Streamable HTTP initialize handshake.
// On success, the session ID is stored on the connection for subsequent requests.
func tryMCPInitialize(ctx context.Context, conn *MCPConnection) error {
	initReq := mcp.Request{
		JSONRPC: "2.0",
		ID:      conn.nextRequestID(),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"akmatori-gateway","version":"1.0.0"}}`),
	}

	bodyBytes, err := json.Marshal(initReq)
	if err != nil {
		return fmt.Errorf("marshal initialize: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.config.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create initialize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := conn.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("initialize returned %d", resp.StatusCode)
	}

	// Capture session ID from response header
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		conn.mu.Lock()
		conn.sessionID = sid
		conn.mu.Unlock()
	}

	// Send initialized notification (required by MCP protocol)
	notifJSON := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	notifReq, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.config.URL, bytes.NewReader(notifJSON))
	if err != nil {
		return nil // Initialize succeeded; notification failure is non-fatal
	}
	notifReq.Header.Set("Content-Type", "application/json")
	notifReq.Header.Set("Accept", "application/json, text/event-stream")
	conn.mu.RLock()
	if conn.sessionID != "" {
		notifReq.Header.Set("Mcp-Session-Id", conn.sessionID)
	}
	conn.mu.RUnlock()
	if notifResp, err := conn.httpClient.Do(notifReq); err == nil {
		notifResp.Body.Close()
	}

	return nil
}

// connectStdio spawns a subprocess and sets up stdin/stdout communication.
func connectStdio(ctx context.Context, conn *MCPConnection) error {
	if conn.config.Command == "" {
		return fmt.Errorf("command is required for stdio transport")
	}

	// Use background context for the subprocess lifetime — not the request context.
	// The subprocess should live as long as the connection pool keeps it, not just
	// for the duration of the originating request.
	cmd := exec.Command(conn.config.Command, conn.config.Args...)

	// Set environment variables (inherit parent env and add custom vars)
	if len(conn.config.EnvVars) > 0 {
		cmd.Env = os.Environ()
		for k, v := range conn.config.EnvVars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	conn.cmd = cmd
	conn.stdin = stdin
	conn.stdout = bufio.NewReader(stdout)
	conn.stderr = stderr

	return nil
}
