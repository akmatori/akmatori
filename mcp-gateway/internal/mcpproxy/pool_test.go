package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/mcp"
)

// newTestPool creates a pool with short timeouts for testing.
func newTestPool(connectFunc func(ctx context.Context, conn *MCPConnection) error) *MCPConnectionPool {
	return NewPool(
		WithIdleTimeout(100*time.Millisecond),
		WithConnectFunc(connectFunc),
	)
}

// mockSSEServer creates a test HTTP server that responds to JSON-RPC requests.
func mockSSEServer(t *testing.T, handler func(req mcp.Request) mcp.Response) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := handler(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestGetOrConnect_LazyConnection(t *testing.T) {
	connectCalls := 0
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		connectCalls++
		// Set up a mock SSE transport that returns tools
		conn.config.Transport = TransportSSE
		return nil
	})
	defer pool.CloseAll()

	// Pool starts with no connections
	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections, got %d", pool.ConnectionCount())
	}

	// Create a mock server for tools/list
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{Name: "test_tool", Description: "A test tool"},
				},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	config := MCPServerConfig{
		Transport: TransportSSE,
		URL:       srv.URL,
	}

	conn, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("GetOrConnect failed: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil connection")
	}
	if connectCalls != 1 {
		t.Errorf("expected 1 connect call, got %d", connectCalls)
	}
	if pool.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection, got %d", pool.ConnectionCount())
	}
}

func TestGetOrConnect_ReusesExisting(t *testing.T) {
	connectCalls := 0
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		connectCalls++
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{
			Tools: []mcp.Tool{{Name: "t1"}},
		})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	conn1, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("first connect failed: %v", err)
	}

	conn2, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("second connect failed: %v", err)
	}

	if conn1 != conn2 {
		t.Error("expected same connection instance on reuse")
	}
	if connectCalls != 1 {
		t.Errorf("expected 1 connect call, got %d", connectCalls)
	}
}

func TestGetOrConnect_ReconnectsOnFailure(t *testing.T) {
	connectCalls := 0
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		connectCalls++
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{
			Tools: []mcp.Tool{{Name: "t1"}},
		})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	conn1, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("first connect failed: %v", err)
	}

	// Simulate connection failure
	conn1.mu.Lock()
	conn1.connected = false
	conn1.mu.Unlock()

	conn2, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}

	if conn1 == conn2 {
		t.Error("expected different connection instance after reconnect")
	}
	if connectCalls != 2 {
		t.Errorf("expected 2 connect calls, got %d", connectCalls)
	}
}

func TestGetOrConnect_ConnectError(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return fmt.Errorf("connection refused")
	})
	defer pool.CloseAll()

	config := MCPServerConfig{Transport: TransportSSE, URL: "http://localhost:0"}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err == nil {
		t.Fatal("expected error on connection failure")
	}
	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after error, got %d", pool.ConnectionCount())
	}
}

func TestIdleCleanup(t *testing.T) {
	pool := NewPool(
		WithIdleTimeout(50*time.Millisecond),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if pool.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection, got %d", pool.ConnectionCount())
	}

	// Wait for idle timeout + cleanup interval
	time.Sleep(200 * time.Millisecond)

	// Trigger cleanup manually
	pool.cleanupIdle()

	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after idle cleanup, got %d", pool.ConnectionCount())
	}
}

func TestClose_SpecificInstance(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect 1 failed: %v", err)
	}
	_, err = pool.GetOrConnect(context.Background(), 2, config)
	if err != nil {
		t.Fatalf("connect 2 failed: %v", err)
	}

	if pool.ConnectionCount() != 2 {
		t.Errorf("expected 2 connections, got %d", pool.ConnectionCount())
	}

	pool.Close(1)
	if pool.ConnectionCount() != 1 {
		t.Errorf("expected 1 connection after close, got %d", pool.ConnectionCount())
	}
	if pool.IsConnected(1) {
		t.Error("instance 1 should not be connected")
	}
	if !pool.IsConnected(2) {
		t.Error("instance 2 should still be connected")
	}
}

func TestCloseAll(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	for i := uint(1); i <= 3; i++ {
		_, err := pool.GetOrConnect(context.Background(), i, config)
		if err != nil {
			t.Fatalf("connect %d failed: %v", i, err)
		}
	}

	pool.CloseAll()
	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after CloseAll, got %d", pool.ConnectionCount())
	}

	// Calling CloseAll again should be safe
	pool.CloseAll()
}

func TestCallTool_SSE(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "echo", Description: "Echo tool"}},
			})
		case "tools/call":
			var params mcp.CallToolParams
			json.Unmarshal(req.Params, &params)
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.NewTextContent(fmt.Sprintf("echoed: %v", params.Arguments["msg"])),
				},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	result, err := pool.CallTool(context.Background(), 1, "echo", map[string]interface{}{"msg": "hello"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	if result.Content[0].Text != "echoed: hello" {
		t.Errorf("expected 'echoed: hello', got '%s'", result.Content[0].Text)
	}
}

func TestCallTool_NoConnection(t *testing.T) {
	pool := newTestPool(nil)
	defer pool.CloseAll()

	_, err := pool.CallTool(context.Background(), 999, "test", nil)
	if err == nil {
		t.Fatal("expected error for non-existent connection")
	}
}

func TestGetTools(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	expectedTools := []mcp.Tool{
		{Name: "tool_a", Description: "Tool A"},
		{Name: "tool_b", Description: "Tool B"},
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: expectedTools})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	tools, ok := pool.GetTools(1)
	if !ok {
		t.Fatal("expected tools to be available")
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "tool_a" || tools[1].Name != "tool_b" {
		t.Errorf("unexpected tools: %v", tools)
	}

	// Non-existent instance
	_, ok = pool.GetTools(999)
	if ok {
		t.Error("expected no tools for non-existent instance")
	}
}

func TestGetCachedTools(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{
			Tools: []mcp.Tool{{Name: "cached_tool"}},
		})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	tools, ok := pool.GetCachedTools(1)
	if !ok {
		t.Fatal("expected cached tools")
	}
	if len(tools) != 1 || tools[0].Name != "cached_tool" {
		t.Errorf("unexpected cached tools: %v", tools)
	}

	// Non-existent
	_, ok = pool.GetCachedTools(999)
	if ok {
		t.Error("expected no cached tools for non-existent instance")
	}
}

func TestHealthCheck(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "ping" {
			return mcp.NewResponse(req.ID, map[string]string{"status": "ok"})
		}
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	err = pool.HealthCheck(context.Background(), 1)
	if err != nil {
		t.Errorf("health check should pass: %v", err)
	}

	// Non-existent instance
	err = pool.HealthCheck(context.Background(), 999)
	if err == nil {
		t.Error("expected error for non-existent instance")
	}
}

func TestRefreshTools(t *testing.T) {
	callCount := 0
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			callCount++
			name := fmt.Sprintf("tool_v%d", callCount)
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: name}},
			})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Initial tools
	tools, _ := pool.GetTools(1)
	if tools[0].Name != "tool_v1" {
		t.Errorf("expected tool_v1, got %s", tools[0].Name)
	}

	// Refresh
	refreshed, err := pool.RefreshTools(context.Background(), 1)
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if refreshed[0].Name != "tool_v2" {
		t.Errorf("expected tool_v2, got %s", refreshed[0].Name)
	}

	// Verify cached tools are also updated
	cached, ok := pool.GetCachedTools(1)
	if !ok || cached[0].Name != "tool_v2" {
		t.Error("cached tools should be updated after refresh")
	}

	// Non-existent instance
	_, err = pool.RefreshTools(context.Background(), 999)
	if err == nil {
		t.Error("expected error for non-existent instance")
	}
}

func TestIsConnected(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	if pool.IsConnected(1) {
		t.Error("should not be connected before GetOrConnect")
	}

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if !pool.IsConnected(1) {
		t.Error("should be connected after GetOrConnect")
	}

	pool.Close(1)
	if pool.IsConnected(1) {
		t.Error("should not be connected after Close")
	}
}

func TestConcurrentAccess(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Concurrent GetOrConnect calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			_, err := pool.GetOrConnect(context.Background(), id, config)
			if err != nil {
				errCh <- err
			}
		}(uint(i%3 + 1)) // 3 different instance IDs
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			pool.IsConnected(id)
			pool.GetTools(id)
			pool.ConnectionCount()
		}(uint(i%3 + 1))
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestFetchToolSchemas_Error(t *testing.T) {
	pool := newTestPool(func(ctx context.Context, conn *MCPConnection) error {
		return nil
	})
	defer pool.CloseAll()

	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewErrorResponse(req.ID, mcp.InternalError, "server error", nil)
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err == nil {
		t.Fatal("expected error when tools/list fails")
	}
	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after schema fetch failure, got %d", pool.ConnectionCount())
	}
}

func TestMCPServerConfig_Fields(t *testing.T) {
	config := MCPServerConfig{
		Transport:       TransportStdio,
		Command:         "/usr/bin/my-mcp-server",
		Args:            []string{"--port", "8080"},
		EnvVars:         map[string]string{"API_KEY": "secret"},
		NamespacePrefix: "ext.myservice",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded MCPServerConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Transport != TransportStdio {
		t.Errorf("expected stdio, got %s", decoded.Transport)
	}
	if decoded.Command != "/usr/bin/my-mcp-server" {
		t.Errorf("unexpected command: %s", decoded.Command)
	}
	if len(decoded.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(decoded.Args))
	}
	if decoded.NamespacePrefix != "ext.myservice" {
		t.Errorf("unexpected prefix: %s", decoded.NamespacePrefix)
	}
	if decoded.EnvVars["API_KEY"] != "secret" {
		t.Error("unexpected env var value")
	}
}

func TestMCPConnection_Close_Idempotent(t *testing.T) {
	conn := &MCPConnection{}
	conn.close()
	conn.close() // Should not panic
}

func TestWithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewPool(
		WithIdleTimeout(100*time.Millisecond),
		WithLogger(logger),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	if pool.logger != logger {
		t.Error("expected custom logger to be set")
	}
}

func TestCallTool_ClosedConnection_Reconnects(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{Tools: []mcp.Tool{{Name: "t1"}}})
		case "tools/call":
			callCount++
			return mcp.NewResponse(req.ID, mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("ok")},
			})
		default:
			return mcp.NewResponse(req.ID, nil)
		}
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(100*time.Millisecond),
		WithMaxReconnectAttempts(2),
		WithBackoff(10*time.Millisecond, 50*time.Millisecond),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)
	defer pool.CloseAll()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}

	conn, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Close the underlying connection
	conn.close()

	// CallTool should auto-reconnect and succeed
	result, err := pool.CallTool(context.Background(), 1, "t1", nil)
	if err != nil {
		t.Fatalf("expected auto-reconnect to succeed, got: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Errorf("unexpected result: %s", result.Content[0].Text)
	}
}

func TestPoolOptions(t *testing.T) {
	pool := NewPool(
		WithIdleTimeout(10*time.Minute),
	)
	defer pool.CloseAll()

	if pool.idleTimeout != 10*time.Minute {
		t.Errorf("expected 10m idle timeout, got %v", pool.idleTimeout)
	}
}

func TestCallTool_ReconnectsOnTransientError(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		switch req.Method {
		case "tools/list":
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "test_tool"}},
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

	connectCalls := 0
	pool := NewPool(
		WithIdleTimeout(100*time.Millisecond),
		WithMaxReconnectAttempts(2),
		WithBackoff(10*time.Millisecond, 50*time.Millisecond),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			connectCalls++
			return nil
		}),
	)
	defer pool.CloseAll()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	conn, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("initial connect failed: %v", err)
	}

	// Simulate a closed connection (transient failure)
	conn.mu.Lock()
	conn.closed = true
	conn.mu.Unlock()

	// CallTool should detect the failure and reconnect
	result, err := pool.CallTool(context.Background(), 1, "test_tool", nil)
	if err != nil {
		t.Fatalf("CallTool after reconnect failed: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected non-empty result")
	}

	// Should have connected more than once (initial + reconnect)
	if connectCalls < 2 {
		t.Errorf("expected at least 2 connect calls, got %d", connectCalls)
	}
}

func TestCallTool_FailsAfterMaxReconnectAttempts(t *testing.T) {
	pool := NewPool(
		WithIdleTimeout(100*time.Millisecond),
		WithMaxReconnectAttempts(2),
		WithBackoff(5*time.Millisecond, 10*time.Millisecond),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return fmt.Errorf("connection refused")
		}),
	)
	defer pool.CloseAll()

	// Manually insert a connection and config so CallTool has something to work with
	conn := &MCPConnection{
		config:     MCPServerConfig{Transport: TransportSSE, URL: "http://localhost:0"},
		instanceID: 1,
		lastUsed:   time.Now(),
		connected:  true,
		closed:     true, // Simulate closed connection
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	pool.mu.Lock()
	pool.connections[1] = conn
	pool.configs[1] = conn.config
	pool.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.CallTool(ctx, 1, "test_tool", nil)
	if err == nil {
		t.Fatal("expected error after exhausting reconnect attempts")
	}
}

func TestCallTool_ConnectionTimeout(t *testing.T) {
	// Server that never responds (simulates timeout)
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "slow_tool"}},
			})
		}
		// For tools/call, sleep longer than context timeout
		time.Sleep(5 * time.Second)
		return mcp.NewResponse(req.ID, nil)
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

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Use a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = pool.CallTool(ctx, 1, "slow_tool", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"connection refused", fmt.Errorf("connection refused"), true},
		{"connection reset", fmt.Errorf("connection reset by peer"), true},
		{"connection closed", fmt.Errorf("connection is closed"), true},
		{"EOF", fmt.Errorf("unexpected EOF"), true},
		{"timeout", fmt.Errorf("i/o timeout"), true},
		{"broken pipe", fmt.Errorf("broken pipe"), true},
		{"server unreachable", fmt.Errorf("server unreachable: dial failed"), true},
		{"non-transient", fmt.Errorf("invalid parameter: missing name"), false},
		{"json parse error", fmt.Errorf("json: cannot unmarshal"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.err)
			if got != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		current  time.Duration
		max      time.Duration
		expected time.Duration
	}{
		{100 * time.Millisecond, 1 * time.Second, 200 * time.Millisecond},
		{500 * time.Millisecond, 1 * time.Second, 1 * time.Second},
		{800 * time.Millisecond, 1 * time.Second, 1 * time.Second},
		{1 * time.Second, 1 * time.Second, 1 * time.Second},
	}

	for _, tt := range tests {
		got := nextBackoff(tt.current, tt.max)
		if got != tt.expected {
			t.Errorf("nextBackoff(%v, %v) = %v, want %v", tt.current, tt.max, got, tt.expected)
		}
	}
}

func TestSchemaRefresh(t *testing.T) {
	refreshCallCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			refreshCallCount++
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: fmt.Sprintf("tool_v%d", refreshCallCount)}},
			})
		}
		if req.Method == "ping" {
			return mcp.NewResponse(req.ID, map[string]string{"status": "ok"})
		}
		return mcp.NewResponse(req.ID, nil)
	})
	defer srv.Close()

	var refreshedIDs []uint
	var refreshMu sync.Mutex

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
		WithSchemaRefreshCallback(func(instanceID uint, tools []mcp.Tool) {
			refreshMu.Lock()
			refreshedIDs = append(refreshedIDs, instanceID)
			refreshMu.Unlock()
		}),
	)
	defer pool.CloseAll()

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Manually trigger schema refresh
	pool.refreshAllSchemas()

	// Should have refreshed and detected the tool name change
	refreshMu.Lock()
	refreshCount := len(refreshedIDs)
	refreshMu.Unlock()

	if refreshCount != 1 {
		t.Errorf("expected 1 schema refresh callback, got %d", refreshCount)
	}

	// Verify tools were updated
	tools, ok := pool.GetTools(1)
	if !ok {
		t.Fatal("expected tools to be available")
	}
	if tools[0].Name != "tool_v2" {
		t.Errorf("expected tool_v2 after refresh, got %s", tools[0].Name)
	}
}

func TestHealthStatus(t *testing.T) {
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "t1"}, {Name: "t2"}},
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

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	statuses := pool.HealthStatus(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	status := statuses[0]
	if status.InstanceID != 1 {
		t.Errorf("expected instance_id 1, got %d", status.InstanceID)
	}
	if !status.Connected {
		t.Error("expected connected=true")
	}
	if status.ToolCount != 2 {
		t.Errorf("expected 2 tools, got %d", status.ToolCount)
	}
	if status.Error != "" {
		t.Errorf("expected no error, got %s", status.Error)
	}
}

func TestHealthStatus_UnhealthyConnection(t *testing.T) {
	// Server that rejects ping
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: "t1"}},
			})
		}
		if req.Method == "ping" {
			return mcp.NewErrorResponse(req.ID, mcp.InternalError, "server overloaded", nil)
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

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	statuses := pool.HealthStatus(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	if statuses[0].Connected {
		t.Error("expected connected=false for unhealthy connection")
	}
	if statuses[0].Error == "" {
		t.Error("expected error message for unhealthy connection")
	}
}

func TestToolsChanged(t *testing.T) {
	tests := []struct {
		name    string
		old     []mcp.Tool
		new     []mcp.Tool
		changed bool
	}{
		{
			"identical",
			[]mcp.Tool{{Name: "a"}, {Name: "b"}},
			[]mcp.Tool{{Name: "a"}, {Name: "b"}},
			false,
		},
		{
			"different count",
			[]mcp.Tool{{Name: "a"}},
			[]mcp.Tool{{Name: "a"}, {Name: "b"}},
			true,
		},
		{
			"different names",
			[]mcp.Tool{{Name: "a"}, {Name: "b"}},
			[]mcp.Tool{{Name: "a"}, {Name: "c"}},
			true,
		},
		{
			"both empty",
			nil,
			nil,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolsChanged(tt.old, tt.new)
			if got != tt.changed {
				t.Errorf("toolsChanged() = %v, want %v", got, tt.changed)
			}
		})
	}
}

func TestGracefulShutdown(t *testing.T) {
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		return mcp.NewResponse(req.ID, mcp.ListToolsResult{
			Tools: []mcp.Tool{{Name: "t1"}},
		})
	})
	defer srv.Close()

	pool := NewPool(
		WithIdleTimeout(time.Minute),
		WithConnectFunc(func(ctx context.Context, conn *MCPConnection) error {
			return nil
		}),
	)

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	_, err = pool.GetOrConnect(context.Background(), 2, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if pool.ConnectionCount() != 2 {
		t.Errorf("expected 2 connections, got %d", pool.ConnectionCount())
	}

	// Graceful shutdown
	pool.CloseAll()

	if pool.ConnectionCount() != 0 {
		t.Errorf("expected 0 connections after shutdown, got %d", pool.ConnectionCount())
	}

	// Should not be able to connect after shutdown
	if pool.IsConnected(1) || pool.IsConnected(2) {
		t.Error("should not be connected after shutdown")
	}
}

func TestStartSchemaRefreshLoop(t *testing.T) {
	callCount := 0
	srv := mockSSEServer(t, func(req mcp.Request) mcp.Response {
		if req.Method == "tools/list" {
			callCount++
			return mcp.NewResponse(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{{Name: fmt.Sprintf("t_%d", callCount)}},
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

	config := MCPServerConfig{Transport: TransportSSE, URL: srv.URL}
	_, err := pool.GetOrConnect(context.Background(), 1, config)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	initialCount := callCount

	// Start refresh loop with very short interval
	pool.StartSchemaRefreshLoop(50 * time.Millisecond)

	// Wait for at least one refresh
	time.Sleep(200 * time.Millisecond)

	if callCount <= initialCount {
		t.Errorf("expected schema refresh to have run, initial calls=%d, current=%d",
			initialCount, callCount)
	}
}
