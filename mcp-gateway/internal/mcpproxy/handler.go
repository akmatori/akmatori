package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
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
	// DefaultSystemRetryInterval is the retry interval for failed system registrations
	// (e.g., a system MCP server not ready at startup). Shorter than the general schema
	// refresh interval because system services are expected to come up quickly.
	DefaultSystemRetryInterval = 30 * time.Second
)

// ServerRegistration holds the configuration for a registered external MCP server.
type ServerRegistration struct {
	InstanceID      uint
	Config          MCPServerConfig
	NamespacePrefix string
	AuthConfig      json.RawMessage
	// DisabledTools lists original upstream tool names that must not be
	// registered or callable.
	DisabledTools []string
}

// key is a canonical serialization of everything that affects the connection
// or the registered tool set. Reload compares keys to find changed servers.
func (r ServerRegistration) key() string {
	disabled := append([]string(nil), r.DisabledTools...)
	sort.Strings(disabled)
	b, _ := json.Marshal(struct {
		C string   `json:"c"`
		N string   `json:"n"`
		A string   `json:"a"`
		D []string `json:"d"`
	}{r.Config.configKey(), r.NamespacePrefix, canonicalJSON(r.AuthConfig), disabled})
	return string(b)
}

// registrationStatus is the outcome of the last connect/registration attempt.
type registrationStatus struct {
	err       string
	checkedAt time.Time
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
	systemRegistrations []ServerRegistration      // system-level servers that survive reloads
	toolMap             map[string]proxyToolEntry // namespaced tool name -> entry
	status              map[uint]registrationStatus
	logger              *slog.Logger
	onToolsChanged      func() // called when schema refresh updates the tool map
	stopRetry           chan struct{}
	systemRetryInterval time.Duration // retry interval for failed system registrations
}

// proxyToolEntry maps a namespaced tool name to its external server and original tool name.
type proxyToolEntry struct {
	instanceID   uint
	originalName string
	config       MCPServerConfig
	tool         mcp.Tool // upstream schema (original name)
}

// unsafeToolNameChars matches characters that may not appear in the tool part
// of a namespaced name. Dots are replaced too, so mcp.ParseToolName (which
// splits on the last dot) always yields the namespace prefix as the tool type.
var unsafeToolNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// sanitizeToolName maps an upstream tool name to the tool part of a
// namespaced gateway name.
func sanitizeToolName(name string) string {
	return unsafeToolNameChars.ReplaceAllString(name, "_")
}

// buildToolEntries maps the upstream tools of one registration to namespaced
// entries. Disabled tools are skipped; on a name collision after sanitizing,
// the later tool is skipped with a warning.
func buildToolEntries(reg ServerRegistration, tools []mcp.Tool, logger *slog.Logger) map[string]proxyToolEntry {
	disabled := make(map[string]bool, len(reg.DisabledTools))
	for _, name := range reg.DisabledTools {
		disabled[name] = true
	}
	entries := make(map[string]proxyToolEntry, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || disabled[tool.Name] {
			continue
		}
		namespacedName := reg.NamespacePrefix + "." + sanitizeToolName(tool.Name)
		if existing, dup := entries[namespacedName]; dup {
			logger.Warn("skipping MCP tool whose sanitized name collides with another tool",
				"instance_id", reg.InstanceID,
				"namespace", reg.NamespacePrefix,
				"tool", tool.Name,
				"collides_with", existing.originalName,
				"namespaced_name", namespacedName,
			)
			continue
		}
		entries[namespacedName] = proxyToolEntry{
			instanceID:   reg.InstanceID,
			originalName: tool.Name,
			config:       reg.Config,
			tool:         tool,
		}
	}
	return entries
}

// routingArgs are injected by the gateway for instance routing and must not
// reach the external server.
var routingArgs = []string{"logical_name", "tool_instance_id"}

// stripRoutingArgs returns a copy of args without the gateway routing keys.
func stripRoutingArgs(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		out[k] = v
	}
	for _, k := range routingArgs {
		delete(out, k)
	}
	return out
}

// NewProxyHandler creates a new MCP proxy handler.
func NewProxyHandler(pool *MCPConnectionPool, logger *slog.Logger) *ProxyHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProxyHandler{
		pool:                pool,
		limiters:            make(map[uint]*ratelimit.Limiter),
		toolMap:             make(map[string]proxyToolEntry),
		status:              make(map[uint]registrationStatus),
		logger:              logger,
		stopRetry:           make(chan struct{}),
		systemRetryInterval: DefaultSystemRetryInterval,
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

	newKeys := make(map[uint]string, len(registrations))
	for _, reg := range registrations {
		newKeys[reg.InstanceID] = reg.key()
	}

	h.mu.Lock()
	// Reconcile: drop pool state for servers that were deleted, disabled or
	// changed, so the next connect uses the new URL/auth.
	var stale []uint
	for _, old := range h.registrations {
		if newKey, ok := newKeys[old.InstanceID]; !ok || newKey != old.key() {
			stale = append(stale, old.InstanceID)
			delete(h.limiters, old.InstanceID)
		}
	}
	for id := range h.status {
		if _, ok := newKeys[id]; !ok {
			delete(h.status, id)
		}
	}
	h.registrations = registrations
	h.toolMap = make(map[string]proxyToolEntry)
	h.mu.Unlock()

	for _, id := range stale {
		h.pool.Remove(id)
	}
	// Record what this reload wants before connecting, so a connect still
	// running with an older config cannot replace it.
	for _, reg := range registrations {
		h.pool.Expect(reg.InstanceID, reg.Config)
	}

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
// Use this for infrastructure services configured via environment variables rather than the database.
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
		h.recordStatus(reg.InstanceID, err)
		return fmt.Errorf("connect: %w", err)
	}

	conn.mu.RLock()
	tools := conn.tools
	conn.mu.RUnlock()

	entries := buildToolEntries(reg, tools, h.logger)

	// Create rate limiter for this server if not exists
	h.mu.Lock()
	if _, exists := h.limiters[reg.InstanceID]; !exists {
		h.limiters[reg.InstanceID] = ratelimit.New(DefaultProxyRatePerSecond, DefaultProxyBurstCapacity)
	}
	for name, entry := range h.toolMap {
		if entry.instanceID == reg.InstanceID {
			delete(h.toolMap, name)
		}
	}
	for name, entry := range entries {
		h.toolMap[name] = entry
	}
	h.status[reg.InstanceID] = registrationStatus{checkedAt: time.Now().UTC()}
	h.mu.Unlock()

	h.logger.Info("registered MCP proxy server",
		"instance_id", reg.InstanceID,
		"namespace", reg.NamespacePrefix,
		"tools", len(entries),
	)
	return nil
}

func (h *ProxyHandler) recordStatus(instanceID uint, err error) {
	st := registrationStatus{checkedAt: time.Now().UTC()}
	if err != nil {
		st.err = err.Error()
	}
	h.mu.Lock()
	h.status[instanceID] = st
	h.mu.Unlock()
}

// CallTool proxies a tool call to the appropriate external MCP server.
// The toolName should be the namespaced name (e.g., "ext.github.create_issue").
// Connection failures are handled gracefully with clear error messages returned to the caller.
// For system-registered servers, a failed call triggers an on-demand re-registration
// attempt before returning the error.
func (h *ProxyHandler) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	h.mu.RLock()
	entry, exists := h.toolMap[toolName]
	var limiter *ratelimit.Limiter
	if exists {
		limiter = h.limiters[entry.instanceID]
	}
	h.mu.RUnlock()

	if !exists {
		// Tool not in the map — check if it belongs to a system registration
		// that failed to connect. If so, try re-registering on demand.
		if reg, ok := h.findSystemRegistrationByTool(toolName); ok {
			if result, err := h.retrySystemCallOnce(ctx, reg, toolName, stripRoutingArgs(args)); err == nil {
				return result, nil
			}
		}
		return nil, fmt.Errorf("proxy tool not found: %s", toolName)
	}

	// Never forward the gateway's routing keys to the external server.
	args = stripRoutingArgs(args)

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

		// For system registrations, attempt on-demand re-registration and retry once
		if reg, ok := h.findSystemRegistrationByID(entry.instanceID); ok {
			if retryResult, retryErr := h.retrySystemCallOnce(ctx, reg, toolName, args); retryErr == nil {
				return retryResult, nil
			}
		}

		return nil, fmt.Errorf("external MCP server error for %s: %w", toolName, err)
	}

	return result, nil
}

// findSystemRegistrationByTool finds a system registration whose namespace prefix
// matches the tool name (e.g., "sysproxy" matches "sysproxy.query").
func (h *ProxyHandler) findSystemRegistrationByTool(toolName string) (ServerRegistration, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, reg := range h.systemRegistrations {
		prefix := reg.NamespacePrefix + "."
		if len(toolName) > len(prefix) && toolName[:len(prefix)] == prefix {
			return reg, true
		}
	}
	return ServerRegistration{}, false
}

// findSystemRegistrationByID finds a system registration by instance ID.
func (h *ProxyHandler) findSystemRegistrationByID(instanceID uint) (ServerRegistration, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, reg := range h.systemRegistrations {
		if reg.InstanceID == instanceID {
			return reg, true
		}
	}
	return ServerRegistration{}, false
}

// retrySystemCallOnce re-registers a system server and retries the tool call once.
// Returns the call result on success, or an error if re-registration or the retry fails.
func (h *ProxyHandler) retrySystemCallOnce(ctx context.Context, reg ServerRegistration, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	h.logger.Info("attempting on-demand re-registration for system server",
		"namespace", reg.NamespacePrefix,
		"tool", toolName,
	)

	if err := h.registerServer(ctx, reg); err != nil {
		h.logger.Warn("on-demand re-registration failed",
			"namespace", reg.NamespacePrefix,
			"error", err,
		)
		return nil, err
	}

	h.logger.Info("on-demand re-registration succeeded, retrying tool call",
		"namespace", reg.NamespacePrefix,
		"tool", toolName,
	)

	// Notify the registry to update MCP server tool list
	h.mu.RLock()
	cb := h.onToolsChanged
	h.mu.RUnlock()
	if cb != nil {
		cb()
	}

	// Look up the tool again after re-registration
	h.mu.RLock()
	entry, exists := h.toolMap[toolName]
	h.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tool still not found after re-registration: %s", toolName)
	}

	return h.pool.CallTool(ctx, entry.instanceID, entry.originalName, args)
}

// GetTools returns the MCP tool definitions for all registered proxy tools,
// with namespaced (sanitized) names, sorted by name. These are suitable for
// registration in the gateway's MCP server. Schemas come from the tool map,
// so a connection closed for idleness does not hide its tools.
func (h *ProxyHandler) GetTools() []mcp.Tool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]mcp.Tool, 0, len(h.toolMap))
	for namespacedName, entry := range h.toolMap {
		result = append(result, mcp.Tool{
			Name:        namespacedName,
			Description: entry.tool.Description,
			InputSchema: entry.tool.InputSchema,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
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
// It also retries failed system registrations (e.g., a system MCP server not ready at startup).
func (h *ProxyHandler) StartSchemaRefreshLoop(interval time.Duration) {
	h.pool.StartSchemaRefreshLoop(interval)

	// Retry failed system registrations on a shorter interval than general schema refresh.
	// System services are expected to come up quickly; no need to wait 5 minutes.
	go h.retryFailedSystemRegistrations(h.systemRetryInterval)

	// Also set up a refresh callback to update our tool map when schemas change
	h.pool.SetSchemaRefreshCallback(func(instanceID uint, tools []mcp.Tool) {
		h.mu.Lock()

		// Find the registration (not toolMap), so servers that initially had
		// zero tools can still be updated.
		var reg ServerRegistration
		found := false
		for _, r := range append(append([]ServerRegistration(nil), h.registrations...), h.systemRegistrations...) {
			if r.InstanceID == instanceID {
				reg, found = r, true
				break
			}
		}
		if !found {
			h.mu.Unlock()
			return
		}
		prefix := reg.NamespacePrefix

		// Remove old tools for this instance
		for name, entry := range h.toolMap {
			if entry.instanceID == instanceID {
				delete(h.toolMap, name)
			}
		}

		// Re-add with updated tool list (same sanitizing and disabled_tools rules)
		for name, entry := range buildToolEntries(reg, tools, h.logger) {
			h.toolMap[name] = entry
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
// This handles the case where a system service wasn't ready at gateway startup.
func (h *ProxyHandler) retryFailedSystemRegistrations(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopRetry:
			return
		case <-ticker.C:
		}
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

// Stop cleans up resources and stops background goroutines.
func (h *ProxyHandler) Stop() {
	// Signal the retry goroutine to stop before closing the pool,
	// so it cannot recreate connections after shutdown.
	select {
	case <-h.stopRetry:
		// Already closed
	default:
		close(h.stopRetry)
	}
	h.pool.CloseAll()
}

// GracefulShutdown stops the schema refresh loop and closes all connections.
func (h *ProxyHandler) GracefulShutdown() {
	h.Stop()
	h.logger.Info("proxy handler shut down gracefully")
}
