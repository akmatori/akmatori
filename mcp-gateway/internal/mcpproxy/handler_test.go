package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCallTool_ResponseCaching(t *testing.T) {
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

	// Second call with same args should hit cache
	result2, err := handler.CallTool(context.Background(), "ext.api.get_data", args)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	// Both should return the same result (from cache)
	if result1.Content[0].Text != result2.Content[0].Text {
		t.Errorf("expected cached result, got different: %s vs %s",
			result1.Content[0].Text, result2.Content[0].Text)
	}

	// Only one actual server call
	if callCount != 1 {
		t.Errorf("expected 1 server call (cached), got %d", callCount)
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
