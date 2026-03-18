package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

const (
	// DefaultProxyRatePerSecond is the default rate limit per external MCP server.
	DefaultProxyRatePerSecond = 10
	// DefaultProxyBurstCapacity is the default burst capacity per external MCP server.
	DefaultProxyBurstCapacity = 20
)

// ServerRegistration holds the configuration for a registered external MCP server.
type ServerRegistration struct {
	InstanceID      uint
	Config          MCPServerConfig
	NamespacePrefix string
	AuthConfig      json.RawMessage
}

// SystemInstanceIDBase is the starting ID for system-level MCP servers (not from DB).
// Using a high value to avoid collisions with DB auto-increment IDs.
const SystemInstanceIDBase uint = 900000

// ProxyHandler manages MCP proxy tool registration, discovery, and call forwarding.
type ProxyHandler struct {
	mu                  sync.RWMutex
	pool                *MCPConnectionPool
	limiters            map[uint]*ratelimit.Limiter // per-instance rate limiters
	registrations       []ServerRegistration
	systemRegistrations []ServerRegistration // system-level servers that survive reloads
	toolMap             map[string]proxyToolEntry // namespaced tool name -> entry
	logger              *slog.Logger
	onToolsChanged      func() // called when schema refresh updates the tool map
}

// proxyToolEntry maps a namespaced tool name to its external server and original tool name.
type proxyToolEntry struct {
	instanceID   uint
	originalName string
	config       MCPServerConfig
}

// NewProxyHandler creates a new MCP proxy handler.
func NewProxyHandler(pool *MCPConnectionPool, logger *slog.Logger) *ProxyHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProxyHandler{
		pool:     pool,
		limiters: make(map[uint]*ratelimit.Limiter),
		toolMap:  make(map[string]proxyToolEntry),
		logger:   logger,
	}
}

// SetOnToolsChanged sets a callback invoked after the schema refresh loop
// updates the tool map. The registry uses this to re-register proxy tools
// in the MCP server so newly-discovered tools become callable.
func (h *ProxyHandler) SetOnToolsChanged(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onToolsChanged = fn
}

// MCPServerConfigLoader loads enabled MCP server configs from the database.
type MCPServerConfigLoader func(ctx context.Context) ([]ServerRegistration, error)

// LoadAndRegister connects to registered MCP servers, discovers their tools,
// and builds the internal tool map with namespace prefixes.
func (h *ProxyHandler) LoadAndRegister(ctx context.Context, loader MCPServerConfigLoader) error {
	registrations, err := loader(ctx)
	if err != nil {
		return fmt.Errorf("load MCP server configs: %w", err)
	}

	h.mu.Lock()
	h.registrations = registrations
	h.toolMap = make(map[string]proxyToolEntry)
	h.mu.Unlock()

	for _, reg := range registrations {
		if err := h.registerServer(ctx, reg); err != nil {
			h.logger.Warn("failed to register MCP server",
				"instance_id", reg.InstanceID,
				"namespace", reg.NamespacePrefix,
				"error", err,
			)
			continue
		}
	}

	h.logger.Info("MCP proxy tools loaded",
		"servers", len(registrations),
		"tools", h.ToolCount(),
	)
	return nil
}

// Reload unregisters all proxy tools and re-registers from the database.
// System-level servers (registered via RegisterSystemServer) are preserved.
func (h *ProxyHandler) Reload(ctx context.Context, loader MCPServerConfigLoader) error {
	h.mu.Lock()
	h.toolMap = make(map[string]proxyToolEntry)
	sysRegs := make([]ServerRegistration, len(h.systemRegistrations))
	copy(sysRegs, h.systemRegistrations)
	h.mu.Unlock()

	if err := h.LoadAndRegister(ctx, loader); err != nil {
		return err
	}

	// Re-register system servers
	for _, reg := range sysRegs {
		if err := h.registerServer(ctx, reg); err != nil {
			h.logger.Warn("failed to re-register system MCP server on reload",
				"instance_id", reg.InstanceID,
				"namespace", reg.NamespacePrefix,
				"error", err,
			)
		}
	}

	return nil
}

// RegisterSystemServer registers a system-level MCP server that persists across Reload calls.
// Use this for infrastructure services like QMD that are configured via environment variables
// rather than the database.
func (h *ProxyHandler) RegisterSystemServer(ctx context.Context, reg ServerRegistration) error {
	h.mu.Lock()
	h.systemRegistrations = append(h.systemRegistrations, reg)
	h.mu.Unlock()

	if err := h.registerServer(ctx, reg); err != nil {
		return err
	}

	h.logger.Info("registered system MCP server",
		"instance_id", reg.InstanceID,
		"namespace", reg.NamespacePrefix,
	)
	return nil
}

// registerServer connects to an external MCP server and maps its tools.
func (h *ProxyHandler) registerServer(ctx context.Context, reg ServerRegistration) error {
	conn, err := h.pool.GetOrConnect(ctx, reg.InstanceID, reg.Config)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	conn.mu.RLock()
	tools := conn.tools
	conn.mu.RUnlock()

	// Create rate limiter for this server if not exists
	h.mu.Lock()
	if _, exists := h.limiters[reg.InstanceID]; !exists {
		h.limiters[reg.InstanceID] = ratelimit.New(DefaultProxyRatePerSecond, DefaultProxyBurstCapacity)
	}

	for _, tool := range tools {
		namespacedName := reg.NamespacePrefix + "." + tool.Name
		h.toolMap[namespacedName] = proxyToolEntry{
			instanceID:   reg.InstanceID,
			originalName: tool.Name,
			config:       reg.Config,
		}
	}
	h.mu.Unlock()

	h.logger.Info("registered MCP proxy server",
		"instance_id", reg.InstanceID,
		"namespace", reg.NamespacePrefix,
		"tools", len(tools),
	)
	return nil
}

// CallTool proxies a tool call to the appropriate external MCP server.
// The toolName should be the namespaced name (e.g., "ext.github.create_issue").
// Connection failures are handled gracefully with clear error messages returned to the caller.
func (h *ProxyHandler) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	h.mu.RLock()
	entry, exists := h.toolMap[toolName]
	var limiter *ratelimit.Limiter
	if exists {
		limiter = h.limiters[entry.instanceID]
	}
	h.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("proxy tool not found: %s", toolName)
	}

	// Apply rate limiting
	if limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
	}

	// Forward the call to the external server using the original tool name.
	// The pool's CallTool handles transient failures with auto-reconnect.
	// Note: we do not cache proxy tool calls because external MCP tools may have
	// side effects (writes, creates, etc.) and there is no reliable way to determine
	// which tools are read-only.
	result, err := h.pool.CallTool(ctx, entry.instanceID, entry.originalName, args)
	if err != nil {
		h.logger.Error("proxy tool call failed",
			"tool", toolName,
			"original_name", entry.originalName,
			"instance_id", entry.instanceID,
			"error", err,
		)
		return nil, fmt.Errorf("external MCP server error for %s: %w", toolName, err)
	}

	return result, nil
}

// GetTools returns the MCP tool definitions for all registered proxy tools,
// with namespaced names. These are suitable for registration in the gateway's MCP server.
func (h *ProxyHandler) GetTools() []mcp.Tool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Collect unique instance IDs to batch-fetch tools
	instanceTools := make(map[uint][]mcp.Tool)
	instancePrefix := make(map[uint]string)

	for namespacedName, entry := range h.toolMap {
		// We need the original tool schemas from the pool
		if _, seen := instanceTools[entry.instanceID]; !seen {
			tools, ok := h.pool.GetTools(entry.instanceID)
			if ok {
				instanceTools[entry.instanceID] = tools
			}
			// Extract prefix from the namespaced name
			prefix := namespacedName[:len(namespacedName)-len(entry.originalName)-1]
			instancePrefix[entry.instanceID] = prefix
		}
	}

	var result []mcp.Tool
	for instID, tools := range instanceTools {
		prefix := instancePrefix[instID]
		for _, tool := range tools {
			namespacedName := prefix + "." + tool.Name
			if _, mapped := h.toolMap[namespacedName]; mapped {
				result = append(result, mcp.Tool{
					Name:        namespacedName,
					Description: tool.Description,
					InputSchema: tool.InputSchema,
				})
			}
		}
	}

	return result
}

// IsProxyTool checks whether a tool name belongs to a proxy tool.
func (h *ProxyHandler) IsProxyTool(toolName string) bool {
	h.mu.RLock()
	_, exists := h.toolMap[toolName]
	h.mu.RUnlock()
	return exists
}

// ToolCount returns the number of registered proxy tools.
func (h *ProxyHandler) ToolCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.toolMap)
}

// StartSchemaRefreshLoop starts periodic schema refresh for all registered MCP servers.
// When new tools are discovered, the tool map is updated automatically.
// It also retries failed system registrations (e.g., QMD not ready at startup).
func (h *ProxyHandler) StartSchemaRefreshLoop(interval time.Duration) {
	h.pool.StartSchemaRefreshLoop(interval)

	// Retry failed system registrations periodically
	go h.retryFailedSystemRegistrations(interval)

	// Also set up a refresh callback to update our tool map when schemas change
	h.pool.SetSchemaRefreshCallback(func(instanceID uint, tools []mcp.Tool) {
		h.mu.Lock()

		// Find the namespace prefix and config from registrations (not toolMap),
		// so servers that initially had zero tools can still be updated.
		var prefix string
		var config MCPServerConfig
		for _, reg := range h.registrations {
			if reg.InstanceID == instanceID {
				prefix = reg.NamespacePrefix
				config = reg.Config
				break
			}
		}
		// Also check system registrations (e.g., QMD)
		if prefix == "" {
			for _, reg := range h.systemRegistrations {
				if reg.InstanceID == instanceID {
					prefix = reg.NamespacePrefix
					config = reg.Config
					break
				}
			}
		}
		if prefix == "" {
			h.mu.Unlock()
			return
		}

		// Remove old tools for this instance
		for name, entry := range h.toolMap {
			if entry.instanceID == instanceID {
				delete(h.toolMap, name)
			}
		}

		// Re-add with updated tool list, preserving config
		for _, tool := range tools {
			namespacedName := prefix + "." + tool.Name
			h.toolMap[namespacedName] = proxyToolEntry{
				instanceID:   instanceID,
				originalName: tool.Name,
				config:       config,
			}
		}

		h.logger.Info("updated proxy tools after schema refresh",
			"instance_id", instanceID,
			"prefix", prefix,
			"tools", len(tools),
		)

		// Capture callback under lock, then release h.mu before calling it
		// to avoid lock inversion. onToolsChanged acquires r.proxyMu → s.mu,
		// while registerProxyToolsFromHandler acquires r.proxyMu → h.mu (via
		// GetTools). Holding h.mu here would deadlock.
		cb := h.onToolsChanged
		h.mu.Unlock()

		// Notify the registry to re-register proxy tools in the MCP server
		if cb != nil {
			cb()
		}
	})
}

// retryFailedSystemRegistrations periodically checks for system registrations
// that don't have active pool connections and re-attempts registration.
// This handles the case where a system service (e.g., QMD) wasn't ready at gateway startup.
func (h *ProxyHandler) retryFailedSystemRegistrations(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		h.mu.RLock()
		regs := make([]ServerRegistration, len(h.systemRegistrations))
		copy(regs, h.systemRegistrations)
		h.mu.RUnlock()

		for _, reg := range regs {
			if h.pool.IsConnected(reg.InstanceID) {
				continue
			}
			h.logger.Info("retrying failed system MCP server registration",
				"instance_id", reg.InstanceID,
				"namespace", reg.NamespacePrefix,
			)
			ctx, cancel := context.WithTimeout(context.Background(), DefaultConnectTimeout)
			if err := h.registerServer(ctx, reg); err != nil {
				h.logger.Warn("system MCP server retry failed",
					"instance_id", reg.InstanceID,
					"namespace", reg.NamespacePrefix,
					"error", err,
				)
			} else {
				h.logger.Info("system MCP server registration succeeded on retry",
					"instance_id", reg.InstanceID,
					"namespace", reg.NamespacePrefix,
				)
				// Notify the registry to re-register proxy tools
				h.mu.RLock()
				cb := h.onToolsChanged
				h.mu.RUnlock()
				if cb != nil {
					cb()
				}
			}
			cancel()
		}
	}
}

// HealthStatus returns health information for all managed MCP proxy connections.
func (h *ProxyHandler) HealthStatus(ctx context.Context) []ConnectionStatus {
	return h.pool.HealthStatus(ctx)
}

// Stop cleans up resources.
func (h *ProxyHandler) Stop() {
	h.pool.CloseAll()
}

// GracefulShutdown stops the schema refresh loop and closes all connections.
func (h *ProxyHandler) GracefulShutdown() {
	h.Stop()
	h.logger.Info("proxy handler shut down gracefully")
}

