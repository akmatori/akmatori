package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

// MCPProtocolVersion is the MCP protocol revision the gateway asks for in
// initialize. The server may answer with another revision; the gateway then
// uses the server's answer.
const MCPProtocolVersion = "2025-06-18"

// TransportType defines how to communicate with an external MCP server.
// Only remote MCP over Streamable HTTP is supported.
type TransportType string

const (
	// TransportHTTP is the canonical value: an MCP Streamable HTTP endpoint URL.
	TransportHTTP TransportType = "http"
	// TransportSSE is the legacy stored value; it means the same as TransportHTTP.
	TransportSSE TransportType = "sse"
)

// IsSupportedTransport reports whether t is a remote HTTP transport value.
// An empty value is treated as "http".
func IsSupportedTransport(t TransportType) bool {
	switch t {
	case "", TransportHTTP, TransportSSE:
		return true
	}
	return false
}

// MCPServerConfig defines the configuration for connecting to an external MCP server.
type MCPServerConfig struct {
	Transport       TransportType   `json:"transport"`
	URL             string          `json:"url,omitempty"`              // MCP Streamable HTTP endpoint
	NamespacePrefix string          `json:"namespace_prefix,omitempty"` // e.g., "ext.github"
	AuthConfig      json.RawMessage `json:"auth_config,omitempty"`      // see parseAuthConfig
}

// configKey is a canonical serialization of the connection-relevant fields,
// used to detect config changes (not pointer or byte-for-byte equality).
func (c MCPServerConfig) configKey() string {
	transport := c.Transport
	if transport == "" || transport == TransportSSE {
		transport = TransportHTTP
	}
	b, _ := json.Marshal(struct {
		T string `json:"t"`
		U string `json:"u"`
		N string `json:"n"`
		A string `json:"a"`
	}{string(transport), strings.TrimSpace(c.URL), c.NamespacePrefix, canonicalJSON(c.AuthConfig)})
	return string(b)
}

// canonicalJSON re-encodes raw so that key order and whitespace do not matter.
func canonicalJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return string(trimmed)
	}
	b, _ := json.Marshal(v) // map keys are sorted by encoding/json
	return string(b)
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

	// Streamable HTTP transport
	httpClient      *http.Client
	auth            *authHeader // injected into every request; secret
	sessionID       string      // MCP Streamable HTTP session ID (if established)
	protocolVersion string      // negotiated in initialize
	serverInfo      ServerInfo  // from the initialize result

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
	// desired is the config key the last reload wants per DB instance
	// (removedConfigKey after removal). A connect that finishes with any
	// other config is stale (it started before the reload) and is dropped
	// instead of replacing the current connection. Instances never set
	// here (system servers, tests) are not checked.
	desired     map[uint]string
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

	// SSRF policy for every outbound request
	policy NetPolicy

	// For testing: allow overriding connect behavior
	connectFunc func(ctx context.Context, conn *MCPConnection) error
}

// ServerInfo is the serverInfo object from an MCP initialize result.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// WithNetPolicy sets the SSRF policy for outbound connections. The default
// is NetPolicyFromEnv().
func WithNetPolicy(policy NetPolicy) PoolOption {
	return func(p *MCPConnectionPool) {
		p.policy = policy
	}
}

// Policy returns the pool's network policy.
func (p *MCPConnectionPool) Policy() NetPolicy {
	return p.policy
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
		desired:              make(map[uint]string),
		schemaCache:          cache.New(DefaultSchemaCacheTTL, DefaultCleanupInterval),
		idleTimeout:          DefaultIdleTimeout,
		stopCleanup:          make(chan struct{}),
		stopRefresh:          make(chan struct{}),
		logger:               slog.Default(),
		maxReconnectAttempts: DefaultMaxReconnectAttempts,
		baseBackoff:          DefaultBaseBackoff,
		maxBackoff:           DefaultMaxBackoff,
		policy:               NetPolicyFromEnv(),
	}
	for _, opt := range opts {
		opt(p)
	}
	go p.cleanupLoop()
	return p
}

// GetOrConnect returns an existing connection or establishes a new one.
func (p *MCPConnectionPool) GetOrConnect(ctx context.Context, instanceID uint, config MCPServerConfig) (*MCPConnection, error) {
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

	// Use a full lock to avoid TOCTOU races where two goroutines could both
	// create connections for the same instanceID.
	p.mu.Lock()

	if p.stopped {
		p.mu.Unlock()
		return nil, fmt.Errorf("connection pool is stopped")
	}

	if p.isStaleLocked(instanceID, config) {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w (instance %d)", ErrStaleConfig, instanceID)
	}

	conn, exists := p.connections[instanceID]

	if exists {
		conn.mu.RLock()
		connected := conn.connected && !conn.closed
		sameConfig := conn.config.configKey() == config.configKey()
		conn.mu.RUnlock()

		if connected && sameConfig {
			conn.mu.Lock()
			conn.lastUsed = time.Now()
			conn.mu.Unlock()
			p.mu.Unlock()
			return conn, nil
		}
		// Connection exists but is not healthy, or its config changed — remove and reconnect
		delete(p.connections, instanceID)
		go conn.close()
	}

	// Release pool lock while connecting (slow I/O), but mark the slot
	// so concurrent callers for the same instanceID wait.
	p.mu.Unlock()

	// Create new connection
	conn = p.newConnection(instanceID, config, auth)

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
	if p.isStaleLocked(instanceID, config) {
		// A reload changed or removed this server while we were connecting.
		p.mu.Unlock()
		conn.close()
		return nil, fmt.Errorf("%w (instance %d)", ErrStaleConfig, instanceID)
	}
	if existing, ok := p.connections[instanceID]; ok && existing.config.configKey() == config.configKey() {
		p.mu.Unlock()
		conn.close()
		existing.mu.Lock()
		existing.lastUsed = time.Now()
		existing.mu.Unlock()
		return existing, nil
	}
	replaced := p.connections[instanceID]
	p.connections[instanceID] = conn
	p.configs[instanceID] = config
	p.mu.Unlock()
	if replaced != nil {
		replaced.close()
	}

	p.logger.Info("connected to MCP server",
		"instance_id", instanceID,
		"transport", config.Transport,
		"tools_count", len(tools),
	)

	return conn, nil
}

// newConnection builds an unconnected connection that uses the pool's policy.
func (p *MCPConnectionPool) newConnection(instanceID uint, config MCPServerConfig, auth *authHeader) *MCPConnection {
	return &MCPConnection{
		config:     config,
		instanceID: instanceID,
		lastUsed:   time.Now(),
		httpClient: p.policy.NewHTTPClient(DefaultConnectTimeout),
		auth:       auth,
		logger:     p.logger.With("instance_id", instanceID, "transport", config.Transport),
	}
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
	// Policy refusals never become transient: retrying cannot change them.
	if errors.Is(err, ErrBlockedAddress) || errors.Is(err, errRedirect) || errors.Is(err, errBodyTooLarge) {
		return false
	}
	if errors.Is(err, errSessionExpired) {
		return true
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
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

// ErrStaleConfig is returned when a connect used a config that a reload has
// since replaced or removed.
var ErrStaleConfig = errors.New("MCP server configuration changed during connect")

// removedConfigKey marks an instance that the last reload removed.
const removedConfigKey = "\x00removed"

// Expect records the config the current reload wants for an instance.
// Connects with any other config are dropped as stale.
func (p *MCPConnectionPool) Expect(instanceID uint, config MCPServerConfig) {
	p.mu.Lock()
	p.desired[instanceID] = config.configKey()
	p.mu.Unlock()
}

// isStaleLocked reports whether config is not the one the last reload wants.
// Caller holds p.mu.
func (p *MCPConnectionPool) isStaleLocked(instanceID uint, config MCPServerConfig) bool {
	want, ok := p.desired[instanceID]
	return ok && want != config.configKey()
}

// Remove closes a connection and forgets its stored config and cached tools,
// so the next GetOrConnect uses the config it is given. Reload calls it for
// deleted, disabled and changed servers.
func (p *MCPConnectionPool) Remove(instanceID uint) {
	p.mu.Lock()
	conn, exists := p.connections[instanceID]
	delete(p.connections, instanceID)
	delete(p.configs, instanceID)
	// Until a reload expects a config again, connects for this instance
	// (e.g. a retry that started before the removal) are stale.
	p.desired[instanceID] = removedConfigKey
	p.mu.Unlock()

	p.schemaCache.Delete(fmt.Sprintf("tools:%d", instanceID))
	if exists {
		conn.close()
		p.logger.Info("removed MCP connection", "instance_id", instanceID)
	}
}

// HasConfig reports whether the pool keeps a config for the instance (it can
// reconnect on demand, e.g. after an idle close).
func (p *MCPConnectionPool) HasConfig(instanceID uint) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.configs[instanceID]
	return ok
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

// errSessionExpired marks a 404 for a request that carried a session ID: the
// server dropped the session, so the pool must re-initialize.
var errSessionExpired = errors.New("MCP session expired (server returned 404)")

// HTTPStatusError is a non-success HTTP status from an external MCP server.
type HTTPStatusError struct {
	StatusCode      int
	WWWAuthenticate string
	Body            string
}

func (e *HTTPStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("server returned %d", e.StatusCode)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Body)
}

func (c *MCPConnection) nextRequestID() int {
	c.idMu.Lock()
	defer c.idMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *MCPConnection) sendRequest(ctx context.Context, method string, params interface{}) (*mcp.Response, error) {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return nil, fmt.Errorf("connection is closed")
	}
	return c.sendHTTPRequest(ctx, method, params)
}

// rpcMessage is any JSON-RPC message the server may send back.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcp.Error      `json:"error,omitempty"`
}

func (m *rpcMessage) toResponse() *mcp.Response {
	resp := &mcp.Response{JSONRPC: m.JSONRPC, Error: m.Error}
	if len(m.ID) > 0 {
		var id interface{}
		_ = json.Unmarshal(m.ID, &id)
		resp.ID = id
	}
	if len(m.Result) > 0 {
		resp.Result = m.Result
	}
	return resp
}

func idMatches(raw json.RawMessage, id int) bool {
	s := strings.TrimSpace(string(raw))
	return s == strconv.Itoa(id) || s == `"`+strconv.Itoa(id)+`"`
}

func (c *MCPConnection) sendHTTPRequest(ctx context.Context, method string, params interface{}) (*mcp.Response, error) {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}

	id := c.nextRequestID()
	bodyBytes, err := json.Marshal(mcp.Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.post(ctx, bodyBytes, &id)
}

// sendNotification posts a JSON-RPC notification (no id, no response expected).
func (c *MCPConnection) sendNotification(ctx context.Context, method string) error {
	body, _ := json.Marshal(map[string]string{"jsonrpc": "2.0", "method": method})
	_, err := c.post(ctx, body, nil)
	return err
}

// post sends one JSON-RPC message with the MCP Streamable HTTP rules: auth
// header on every request, session and protocol-version headers once known,
// JSON or SSE response bodies, and 202 Accepted for notifications. When
// expectID is nil the call is a notification and the response body is ignored.
func (c *MCPConnection) post(ctx context.Context, body []byte, expectID *int) (*mcp.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	c.mu.RLock()
	sessionID := c.sessionID
	protocolVersion := c.protocolVersion
	auth := c.auth
	client := c.httpClient
	c.mu.RUnlock()
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set("Mcp-Protocol-Version", protocolVersion)
	}
	auth.apply(req.Header)

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlockedAddress) || errors.Is(err, errRedirect) {
			return nil, fmt.Errorf("request refused: %w", err)
		}
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	if resp.StatusCode == http.StatusNotFound && sessionID != "" {
		c.mu.Lock()
		if c.sessionID == sessionID {
			c.sessionID = ""
		}
		c.connected = false
		c.mu.Unlock()
		return nil, errSessionExpired
	}

	if expectID == nil {
		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			return nil, nil
		}
		return nil, statusError(resp)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	reader := newCappedReader(resp.Body, MaxResponseBytes)
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mediaType == "text/event-stream" {
		return readSSEResponse(reader, *expectID)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("decode response: empty body")
	}
	var msg rpcMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return msg.toResponse(), nil
}

func statusError(resp *http.Response) *HTTPStatusError {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &HTTPStatusError{
		StatusCode:      resp.StatusCode,
		WWWAuthenticate: resp.Header.Get("WWW-Authenticate"),
		Body:            strings.TrimSpace(string(b)),
	}
}

// readSSEResponse reads Server-Sent Events until it finds the JSON-RPC
// response whose id matches. Server requests, notifications and responses to
// other ids are skipped.
func readSSEResponse(r io.Reader, id int) (*mcp.Response, error) {
	br := bufio.NewReader(r)
	var data []string
	dispatch := func() *mcp.Response {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		var msg rpcMessage
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return nil // not a single JSON-RPC message (e.g. a batch); skip
		}
		if msg.Method != "" || !idMatches(msg.ID, id) {
			return nil
		}
		return msg.toResponse()
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 || err == nil {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if resp := dispatch(); resp != nil {
					return resp, nil
				}
			case strings.HasPrefix(line, ":"):
				// comment / keep-alive
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			default:
				// event:, id:, retry: are not needed here
			}
		}
		if err != nil {
			if resp := dispatch(); resp != nil {
				return resp, nil
			}
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("SSE stream ended without a response to request %d: unexpected EOF", id)
			}
			return nil, fmt.Errorf("read SSE stream: %w", err)
		}
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

	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// defaultConnect runs the MCP initialize handshake.
func defaultConnect(ctx context.Context, conn *MCPConnection) error {
	if !IsSupportedTransport(conn.config.Transport) {
		return fmt.Errorf("unsupported transport type: %s", conn.config.Transport)
	}
	if conn.config.URL == "" {
		return fmt.Errorf("URL is required")
	}
	return initialize(ctx, conn)
}

// initialize performs the MCP initialize handshake and sends the
// notifications/initialized notification. A server that cannot initialize is
// not usable, so any failure here is returned to the caller.
func initialize(ctx context.Context, conn *MCPConnection) error {
	params := map[string]interface{}{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "akmatori-gateway", "version": "1.0.0"},
	}
	resp, err := conn.sendHTTPRequest(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	var result struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	resultBytes, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return fmt.Errorf("initialize: invalid result: %w", err)
	}
	if result.ProtocolVersion == "" {
		result.ProtocolVersion = MCPProtocolVersion
	}

	conn.mu.Lock()
	conn.protocolVersion = result.ProtocolVersion
	conn.serverInfo = result.ServerInfo
	conn.mu.Unlock()

	if err := conn.sendNotification(ctx, "notifications/initialized"); err != nil {
		// Initialize succeeded, so auth and URL are good; some servers reject
		// the notification. Keep going.
		logger := conn.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("notifications/initialized failed", "error", err)
	}
	return nil
}
