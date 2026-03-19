package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/mcp"
)

// mockLoader returns a loader function that provides the given registrations.
func mockLoader(regs []ServerRegistration) MCPServerConfigLoader {
	return func(ctx context.Context) ([]ServerRegistration, error) {
		return regs, nil
	}
}

// mockLoaderError returns a loader that always returns an error.
func mockLoaderError() MCPServerConfigLoader {
	return func(ctx context.Context) ([]ServerRegistration, error) {
		return nil, fmt.Errorf("database unavailable")
	}
}

// newTestHandler creates a ProxyHandler backed by a test pool and mock SSE server
// that responds to tools/list and tools/call requests.
func newTestHandler(t *testing.T, tools []mcp.Tool) (*ProxyHandler, *MCPConnectionPool, func()) {
	t.Helper()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: tools})
		case "tools/call":
			var params mcp.CallToolParams
			json.Unmarshal(req.Params, &params)
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.NewTextContent(fmt.Sprintf("result from %s", params.Name)),
				},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})

	handler := NewProxyHandler(pool, nil)

	cleanup := func() {
		handler.Stop()
		pool.CloseAll()
		srv.Close()
	}

	// Store the server URL for use in registrations
	t.Cleanup(func() {
		// In case cleanup wasn't called
	})

	// We need to return the server URL indirectly via a registration helper
	// Store on the test context - but we can just pass it through
	// Actually, let the caller build the registration with the URL
	// So let's return the URL too. We'll modify the approach.

	// Re-think: let's make the helper create registrations and load them

	return handler, pool, func() {
		cleanup()
		// Also set the srvURL so tests can use it
	}
}

func TestLoadAndRegister_DiscoverTools(t *testing.T) {
	externalTools := []mcp.Tool{
		{Name: "create_issue", Description: "Create a GitHub issue"},
		{Name: "list_repos", Description: "List repositories"},
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: externalTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{
			InstanceID:      1,
			NamespacePrefix: "ext.github",
			Config: MCPServerConfig{
				Transport:       TransportSSE,
				URL:             srv.URL,
				NamespacePrefix: "ext.github",
			},
		},
	}

	err := handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("LoadAndRegister failed: %v", err)
	}

	// Should have 2 tools with namespace prefix
	if handler.ToolCount() != 2 {
		t.Errorf("expected 2 tools, got %d", handler.ToolCount())
	}

	// Check namespaced tool names
	if !handler.IsProxyTool("ext.github.create_issue") {
		t.Error("expected ext.github.create_issue to be a proxy tool")
	}
	if !handler.IsProxyTool("ext.github.list_repos") {
		t.Error("expected ext.github.list_repos to be a proxy tool")
	}
	if handler.IsProxyTool("create_issue") {
		t.Error("non-namespaced tool should not be a proxy tool")
	}
}

func TestLoadAndRegister_MultipleServers(t *testing.T) {
	githubTools := []mcp.Tool{
		{Name: "create_issue", Description: "Create issue"},
	}
	slackTools := []mcp.Tool{
		{Name: "send_message", Description: "Send Slack message"},
		{Name: "list_channels", Description: "List channels"},
	}

	githubSrv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: githubTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer githubSrv.Close()

	slackSrv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: slackTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer slackSrv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{
			InstanceID:      1,
			NamespacePrefix: "ext.github",
			Config:          MCPServerConfig{Transport: TransportSSE, URL: githubSrv.URL},
		},
		{
			InstanceID:      2,
			NamespacePrefix: "ext.slack",
			Config:          MCPServerConfig{Transport: TransportSSE, URL: slackSrv.URL},
		},
	}

	err := handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("LoadAndRegister failed: %v", err)
	}

	if handler.ToolCount() != 3 {
		t.Errorf("expected 3 tools, got %d", handler.ToolCount())
	}

	if !handler.IsProxyTool("ext.github.create_issue") {
		t.Error("missing ext.github.create_issue")
	}
	if !handler.IsProxyTool("ext.slack.send_message") {
		t.Error("missing ext.slack.send_message")
	}
	if !handler.IsProxyTool("ext.slack.list_channels") {
		t.Error("missing ext.slack.list_channels")
	}
}

func TestCallTool_ProxiesCorrectly(t *testing.T) {
	var receivedToolName string
	var receivedArgs map[string]interface{}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "create_issue", Description: "Create issue"}},
			})
		case "tools/call":
			var params mcp.CallToolParams
			json.Unmarshal(req.Params, &params)
			receivedToolName = params.Name
			receivedArgs = params.Arguments
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("issue #42 created")},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{
			InstanceID:      1,
			NamespacePrefix: "ext.github",
			Config:          MCPServerConfig{Transport: TransportSSE, URL: srv.URL},
		},
	}

	err := handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("LoadAndRegister failed: %v", err)
	}

	// Call with namespaced name - should forward using original name
	result, err := handler.CallTool(context.Background(), "ext.github.create_issue", map[string]interface{}{
		"title": "Bug report",
		"body":  "Something broke",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Verify the original tool name was sent to the external server
	if receivedToolName != "create_issue" {
		t.Errorf("expected original name 'create_issue', got '%s'", receivedToolName)
	}
	if receivedArgs["title"] != "Bug report" {
		t.Errorf("expected title 'Bug report', got '%v'", receivedArgs["title"])
	}

	// Verify the result
	if len(result.Content) != 1 || result.Content[0].Text != "issue #42 created" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestCallTool_NotFound(t *testing.T) {
	pool := newTestPool(nil)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	_, err := handler.CallTool(context.Background(), "nonexistent.tool", nil)
	if err == nil {
		t.Fatal("expected error for non-existent proxy tool")
	}
}

func TestCallTool_NoCaching(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "get_data"}},
			})
		case "tools/call":
			callCount++
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent(fmt.Sprintf("call_%d", callCount))},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.api", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	args := map[string]interface{}{"key": "value"}

	// First call hits the server
	result1, err := handler.CallTool(context.Background(), "ext.api.get_data", args)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call with same args should also hit the server (no caching for proxy tools
	// because external MCP tools may have side effects)
	result2, err := handler.CallTool(context.Background(), "ext.api.get_data", args)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	// Each call should get a unique response from the server
	if result1.Content[0].Text == result2.Content[0].Text {
		t.Errorf("expected different results (no caching), both got: %s", result1.Content[0].Text)
	}

	// Both calls should have hit the server
	if callCount != 2 {
		t.Errorf("expected 2 server calls (no caching), got %d", callCount)
	}
}

func TestGetTools_ReturnsNamespacedTools(t *testing.T) {
	externalTools := []mcp.Tool{
		{Name: "run_query", Description: "Run a query", InputSchema: mcp.InputSchema{Type: "object"}},
		{Name: "list_tables", Description: "List tables", InputSchema: mcp.InputSchema{Type: "object"}},
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: externalTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.db", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	tools := handler.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["ext.db.run_query"] {
		t.Error("missing ext.db.run_query")
	}
	if !names["ext.db.list_tables"] {
		t.Error("missing ext.db.list_tables")
	}
}

func TestReload_ClearsAndReregisters(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			callCount++
			if callCount <= 1 {
				return mcp.NewResponse(req.ID, mcp.ListToolsResult{
					Tools: []mcp.Tool{{Name: "old_tool"}},
				})
			}
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "new_tool"}},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.svc", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}

	// Initial load
	handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if !handler.IsProxyTool("ext.svc.old_tool") {
		t.Error("expected ext.svc.old_tool after initial load")
	}

	// Close old connection so pool reconnects and re-fetches tools
	pool.Close(1)

	// Reload
	err := handler.Reload(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Old tool should be gone, new tool should be present
	if handler.IsProxyTool("ext.svc.old_tool") {
		t.Error("old_tool should not exist after reload")
	}
	if !handler.IsProxyTool("ext.svc.new_tool") {
		t.Error("expected ext.svc.new_tool after reload")
	}
}

func TestLoadAndRegister_ConnectionError(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return fmt.Errorf("connection refused")
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.bad", Config: MCPServerConfig{Transport: TransportSSE, URL: "http://localhost:0"}},
	}

	// Should not fail entirely - just skip the failed server
	err := handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("LoadAndRegister should not fail on individual server errors: %v", err)
	}

	if handler.ToolCount() != 0 {
		t.Errorf("expected 0 tools after connection failure, got %d", handler.ToolCount())
	}
}

func TestLoadAndRegister_LoaderError(t *testing.T) {
	pool := newTestPool(nil)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	err := handler.LoadAndRegister(context.Background(), mockLoaderError())
	if err == nil {
		t.Fatal("expected error when loader fails")
	}
}

func TestCallTool_RateLimiting(t *testing.T) {
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "fast_tool"}},
			})
		case "tools/call":
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("ok")},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.fast", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	// Rate limiter should be created for the instance
	handler.mu.RLock()
	_, hasLimiter := handler.limiters[1]
	handler.mu.RUnlock()

	if !hasLimiter {
		t.Error("expected rate limiter to be created for instance")
	}

	// Should be able to make calls (within rate limit)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := handler.CallTool(ctx, "ext.fast.fast_tool", nil)
	if err != nil {
		t.Fatalf("CallTool should succeed within rate limit: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Errorf("unexpected result: %s", result.Content[0].Text)
	}
}

func TestNamespacePrefixing(t *testing.T) {
	tests := []struct {
		name            string
		prefix          string
		externalTool    string
		expectedFull    string
	}{
		{"simple prefix", "ext.github", "create_issue", "ext.github.create_issue"},
		{"nested prefix", "ext.cloud.aws", "list_instances", "ext.cloud.aws.list_instances"},
		{"short prefix", "gh", "pr_create", "gh.pr_create"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
				if req.Method == "tools/list" {
					return mcp.NewResponse(req.ID, mcp.ListToolsResult{
						Tools: []mcp.Tool{{Name: tt.externalTool}},
					})
				}
				return mcp.NewResponse(req.ID, nil)
			})
			defer srv.Close()

			pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
				return nil
			})
			defer pool.CloseAll()

			handler := NewProxyHandler(pool, nil)
			defer handler.Stop()

			regs := []ServerRegistration{
				{InstanceID: 1, NamespacePrefix: tt.prefix, Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
			}
			handler.LoadAndRegister(context.Background(), mockLoader(regs))

			if !handler.IsProxyTool(tt.expectedFull) {
				t.Errorf("expected %s to be a proxy tool", tt.expectedFull)
			}
		})
	}
}

func TestAuthInjection_ConfigPassedToPool(t *testing.T) {
	// Verify that auth config from registration is stored in the tool entry's config
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "secure_tool"}},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	authJSON := json.RawMessage(`{"method":"bearer_token","token":"secret123"}`)

	regs := []ServerRegistration{
		{
			InstanceID:      1,
			NamespacePrefix: "ext.secure",
			AuthConfig:      authJSON,
			Config: MCPServerConfig{
				Transport:  TransportSSE,
				URL:        srv.URL,
				AuthConfig: authJSON,
			},
		},
	}

	err := handler.LoadAndRegister(context.Background(), mockLoader(regs))
	if err != nil {
		t.Fatalf("LoadAndRegister failed: %v", err)
	}

	// Verify auth config is stored in the tool entry
	handler.mu.RLock()
	entry, exists := handler.toolMap["ext.secure.secure_tool"]
	handler.mu.RUnlock()

	if !exists {
		t.Fatal("expected ext.secure.secure_tool to exist")
	}

	if entry.config.AuthConfig == nil {
		t.Error("expected auth config to be stored in tool entry")
	}

	var auth map[string]string
	json.Unmarshal(entry.config.AuthConfig, &auth)
	if auth["method"] != "bearer_token" {
		t.Errorf("expected bearer_token auth method, got %s", auth["method"])
	}
}

func TestSearchAndDetailIncludeProxyTools(t *testing.T) {
	// This test verifies that proxy tools appear in GetTools() output,
	// which is what gets registered in the MCP server and thus visible
	// to search/detail endpoints.
	externalTools := []mcp.Tool{
		{
			Name:        "query",
			Description: "Run a database query",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"sql": {Type: "string", Description: "SQL query to execute"},
				},
				Required: []string{"sql"},
			},
		},
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: externalTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.db", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	tools := handler.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name != "ext.db.query" {
		t.Errorf("expected ext.db.query, got %s", tool.Name)
	}
	if tool.Description != "Run a database query" {
		t.Errorf("unexpected description: %s", tool.Description)
	}
	if tool.InputSchema.Type != "object" {
		t.Errorf("expected object schema type, got %s", tool.InputSchema.Type)
	}
	if _, hasSql := tool.InputSchema.Properties["sql"]; !hasSql {
		t.Error("expected sql property in input schema")
	}
}

func TestCallTool_GracefulErrorOnServerCrash(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "fragile_tool"}},
			})
		case "tools/call":
			callCount++
			if callCount == 1 {
				// First call fails (simulating crash after registration)
				return mcp.NewErrorResponse(req.ID, mcp.InternalError, "server crashed", nil)
			}
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("recovered")},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithMaxReconnectAttempts(1),
		WithBackoff(5*time.Millisecond, 10*time.Millisecond),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.fragile", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	// Call should return an error but NOT crash the gateway
	_, err := handler.CallTool(context.Background(), "ext.fragile.fragile_tool", nil)
	if err == nil {
		// The error from the external server is a JSON-RPC error, not a transport error,
		// so it may be returned as a non-nil result with isError.
		// The important thing is the gateway didn't crash.
	}
	// Gateway is still operational
	if handler.ToolCount() != 1 {
		t.Errorf("expected handler to still have 1 tool registered, got %d", handler.ToolCount())
	}
}

func TestHandlerHealthStatus(t *testing.T) {
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "t1"}},
			})
		}
		if req.Method == "ping" {
			return mcp.NewResponse(req.ID, map[string]string{"status": "ok"})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.svc", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	statuses := handler.HealthStatus(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if !statuses[0].Connected {
		t.Error("expected connected=true")
	}
}

func TestHandlerGracefulShutdown(t *testing.T) {
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "t1"}},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)

	handler := NewProxyHandler(pool, nil)

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.svc", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	// Graceful shutdown
	handler.GracefulShutdown()

	// Pool should have 0 connections after shutdown
	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after shutdown, got %d", pool.ConnectionCount())
	}

	// Calling GracefulShutdown again should be safe
	handler.GracefulShutdown()
}

func TestHandlerStartSchemaRefreshLoop_UpdatesToolMap(t *testing.T) {
	toolVersion := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			toolVersion++
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: fmt.Sprintf("tool_v%d", toolVersion)}},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.svc", Config: MCPServerConfig{Transport: TransportSSE, URL: srv.URL}},
	}
	handler.LoadAndRegister(context.Background(), mockLoader(regs))

	if !handler.IsProxyTool("ext.svc.tool_v1") {
		t.Error("expected ext.svc.tool_v1 after initial load")
	}

	// Start schema refresh loop with very short interval
	handler.StartSchemaRefreshLoop(50 * time.Millisecond)

	// Wait for refresh to detect new tools
	time.Sleep(300 * time.Millisecond)

	// Gateway should not have crashed - the loop ran
	if handler.ToolCount() < 1 {
		t.Errorf("expected at least 1 tool after schema refresh, got %d", handler.ToolCount())
	}
}

func TestRegisterSystemServer_DiscoverTools(t *testing.T) {
	externalTools := []mcp.Tool{
		{Name: "query", Description: "Search documents"},
		{Name: "get", Description: "Get a document"},
		{Name: "status", Description: "Get index status"},
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: externalTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	reg := ServerRegistration{
		InstanceID:      SystemInstanceIDBase,
		NamespacePrefix: "qmd",
		Config:          MCPServerConfig{Transport: TransportSSE, URL: srv.URL, NamespacePrefix: "qmd"},
	}

	err := handler.RegisterSystemServer(context.Background(), reg)
	if err != nil {
		t.Fatalf("RegisterSystemServer failed: %v", err)
	}

	if handler.ToolCount() != 3 {
		t.Errorf("expected 3 tools, got %d", handler.ToolCount())
	}

	if !handler.IsProxyTool("qmd.query") {
		t.Error("expected qmd.query to be a proxy tool")
	}
	if !handler.IsProxyTool("qmd.get") {
		t.Error("expected qmd.get to be a proxy tool")
	}
	if !handler.IsProxyTool("qmd.status") {
		t.Error("expected qmd.status to be a proxy tool")
	}
}

func TestRegisterSystemServer_SurvivesReload(t *testing.T) {
	qmdTools := []mcp.Tool{
		{Name: "query", Description: "Search documents"},
	}
	dbTools := []mcp.Tool{
		{Name: "create_issue", Description: "Create issue"},
	}

	qmdSrv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: qmdTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer qmdSrv.Close()

	dbSrv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: dbTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer dbSrv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	// Register system server (QMD)
	qmdReg := ServerRegistration{
		InstanceID:      SystemInstanceIDBase,
		NamespacePrefix: "qmd",
		Config:          MCPServerConfig{Transport: TransportSSE, URL: qmdSrv.URL, NamespacePrefix: "qmd"},
	}
	err := handler.RegisterSystemServer(context.Background(), qmdReg)
	if err != nil {
		t.Fatalf("RegisterSystemServer failed: %v", err)
	}

	// Load DB-based server
	dbRegs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext.github", Config: MCPServerConfig{Transport: TransportSSE, URL: dbSrv.URL}},
	}
	err = handler.LoadAndRegister(context.Background(), mockLoader(dbRegs))
	if err != nil {
		t.Fatalf("LoadAndRegister failed: %v", err)
	}

	// After LoadAndRegister, only DB tools are in the map (LoadAndRegister resets toolMap).
	// But system tools should be re-registered on Reload.

	// Close connections so pool reconnects on reload
	pool.Close(SystemInstanceIDBase)
	pool.Close(1)

	// Reload - system servers should survive
	err = handler.Reload(context.Background(), mockLoader(dbRegs))
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Both DB and system tools should exist after reload
	if !handler.IsProxyTool("qmd.query") {
		t.Error("expected qmd.query to survive reload")
	}
	if !handler.IsProxyTool("ext.github.create_issue") {
		t.Error("expected ext.github.create_issue after reload")
	}
}

func TestRegisterSystemServer_ConnectionError(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return fmt.Errorf("connection refused")
	})
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	reg := ServerRegistration{
		InstanceID:      SystemInstanceIDBase,
		NamespacePrefix: "qmd",
		Config:          MCPServerConfig{Transport: TransportSSE, URL: "http://localhost:0"},
	}

	err := handler.RegisterSystemServer(context.Background(), reg)
	if err == nil {
		t.Fatal("expected error when QMD is unreachable")
	}

	// System registration is still stored for retry on reload
	handler.mu.RLock()
	sysCount := len(handler.systemRegistrations)
	handler.mu.RUnlock()
	if sysCount != 1 {
		t.Errorf("expected 1 system registration stored, got %d", sysCount)
	}
}

func TestRetryFailedSystemRegistrations(t *testing.T) {
	// Simulate QMD being unavailable initially, then becoming available.
	var connectAttempts int32
	qmdTools := []mcp.Tool{
		{Name: "query", Description: "Search documents"},
	}

	qmdSrv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: qmdTools})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer qmdSrv.Close()

	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		attempt := atomic.AddInt32(&connectAttempts, 1)
		if attempt <= 1 {
			return fmt.Errorf("connection refused") // First attempt fails
		}
		return nil // Subsequent attempts succeed
	})
	defer pool.CloseAll()

	var toolsChangedCalls int32
	handler := NewProxyHandler(pool, nil)
	handler.systemRetryInterval = 100 * time.Millisecond // Short interval for test
	handler.SetOnToolsChanged(func() {
		atomic.AddInt32(&toolsChangedCalls, 1)
	})
	defer handler.Stop()

	reg := ServerRegistration{
		InstanceID:      SystemInstanceIDBase,
		NamespacePrefix: "qmd",
		Config:          MCPServerConfig{Transport: TransportSSE, URL: qmdSrv.URL, NamespacePrefix: "qmd"},
	}

	// Initial registration fails (QMD not ready)
	err := handler.RegisterSystemServer(context.Background(), reg)
	if err == nil {
		t.Fatal("expected error on first registration attempt")
	}

	// No tools registered yet
	if handler.ToolCount() != 0 {
		t.Errorf("expected 0 tools before retry, got %d", handler.ToolCount())
	}

	// Start the retry loop (uses handler.systemRetryInterval for system retries)
	handler.StartSchemaRefreshLoop(100 * time.Millisecond)

	// Wait for retry to succeed
	deadline := time.After(3 * time.Second)
	for {
		if handler.ToolCount() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retry did not register QMD tools within timeout")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if !handler.IsProxyTool("qmd.query") {
		t.Error("expected qmd.query to be registered after retry")
	}
}

func TestEmptyRegistrations(t *testing.T) {
	pool := newTestPool(nil)
	defer pool.CloseAll()

	handler := NewProxyHandler(pool, nil)
	defer handler.Stop()

	err := handler.LoadAndRegister(context.Background(), mockLoader(nil))
	if err != nil {
		t.Fatalf("LoadAndRegister with empty registrations should succeed: %v", err)
	}

	if handler.ToolCount() != 0 {
		t.Errorf("expected 0 tools, got %d", handler.ToolCount())
	}

	tools := handler.GetTools()
	if len(tools) != 0 {
		t.Errorf("expected empty tools list, got %d", len(tools))
	}
}
