package mcpproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/mcp"
)

// fakeMCP is a configurable Streamable HTTP MCP server for tests.
type fakeMCP struct {
	mu        sync.Mutex
	tools     []mcp.Tool
	sse       bool   // answer requests with text/event-stream
	sessionID string // returned on initialize
	requests  []recordedRequest
	callArgs  []map[string]interface{}
	// hook may fully handle a request; return true when it wrote a response.
	hook func(w http.ResponseWriter, r *http.Request, msg rpcMessage) bool
}

type recordedRequest struct {
	method  string
	headers http.Header
}

func (f *fakeMCP) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.method == method {
			n++
		}
	}
	return n
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg rpcMessage
	_ = json.Unmarshal(body, &msg)

	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{method: msg.Method, headers: r.Header.Clone()})
	hook := f.hook
	f.mu.Unlock()

	if hook != nil && hook(w, r, msg) {
		return
	}
	if len(msg.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var result interface{}
	switch msg.Method {
	case "initialize":
		if f.sessionID != "" {
			w.Header().Set("Mcp-Session-Id", f.sessionID)
		}
		result = map[string]interface{}{
			"protocolVersion": MCPProtocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]string{"name": "fake", "version": "9.9"},
		}
	case "tools/list":
		result = mcp.ListToolsResult{Tools: f.tools}
	case "tools/call":
		var p struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		var params json.RawMessage
		_ = json.Unmarshal(body, &struct {
			Params *json.RawMessage `json:"params"`
		}{&params})
		_ = json.Unmarshal(params, &p)
		f.mu.Lock()
		f.callArgs = append(f.callArgs, p.Arguments)
		f.mu.Unlock()
		result = mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("called " + p.Name)}}
	case "ping":
		result = map[string]interface{}{}
	default:
		result = map[string]interface{}{}
	}

	var id interface{}
	_ = json.Unmarshal(msg.ID, &id)
	resp := mcp.NewResponse(id, result)
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// noise first: a server notification and a response to another id
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":99999,\"result\":{\"wrong\":true}}\n\n")
		b, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: message\nid: 1\ndata: %s\n\n", b)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newFakeServer(t *testing.T, f *fakeMCP) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv
}

// realPool is a pool that runs the real initialize handshake.
func realPool(t *testing.T, opts ...PoolOption) *MCPConnectionPool {
	t.Helper()
	p := NewPool(append([]PoolOption{WithNetPolicy(testPolicy), WithBackoff(time.Millisecond, 5*time.Millisecond)}, opts...)...)
	t.Cleanup(p.CloseAll)
	return p
}

// --- C: auth injection ---

func TestAuth_AppliedToEveryRequest(t *testing.T) {
	cases := []struct {
		name       string
		auth       string
		header     string
		wantHeader string
	}{
		{"bearer", `{"type":"bearer","token":"tok-1"}`, "Authorization", "Bearer tok-1"},
		{"header", `{"type":"header","header_name":"x-api-key","value":"k-2"}`, "X-Api-Key", "k-2"},
		{"oauth", `{"type":"oauth","access_token":"at-3","token_type":"bearer","refresh_token":"r"}`, "Authorization", "Bearer at-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMCP{tools: []mcp.Tool{{Name: "t1"}}, sessionID: "sess-1"}
			srv := newFakeServer(t, f)
			pool := realPool(t)
			cfg := MCPServerConfig{Transport: TransportHTTP, URL: srv.URL, AuthConfig: json.RawMessage(tc.auth)}

			if _, err := pool.GetOrConnect(context.Background(), 1, cfg); err != nil {
				t.Fatalf("connect: %v", err)
			}
			if _, err := pool.CallTool(context.Background(), 1, "t1", nil); err != nil {
				t.Fatalf("call: %v", err)
			}
			if err := pool.HealthCheck(context.Background(), 1); err != nil {
				t.Fatalf("ping: %v", err)
			}

			f.mu.Lock()
			defer f.mu.Unlock()
			seen := map[string]bool{}
			for _, r := range f.requests {
				seen[r.method] = true
				if got := r.headers.Get(tc.header); got != tc.wantHeader {
					t.Errorf("%s: %s header = %q, want %q", r.method, tc.header, got, tc.wantHeader)
				}
				if r.method != "initialize" {
					if r.headers.Get("Mcp-Session-Id") != "sess-1" {
						t.Errorf("%s: missing session header", r.method)
					}
					if r.headers.Get("Mcp-Protocol-Version") != MCPProtocolVersion {
						t.Errorf("%s: missing protocol version header", r.method)
					}
				}
			}
			for _, m := range []string{"initialize", "notifications/initialized", "tools/list", "tools/call", "ping"} {
				if !seen[m] {
					t.Errorf("expected a %s request", m)
				}
			}
		})
	}
}

func TestParseAuthConfig(t *testing.T) {
	none := []string{``, `null`, `{}`, `{"type":"none"}`}
	for _, raw := range none {
		if h, err := parseAuthConfig(json.RawMessage(raw)); err != nil || h != nil {
			t.Errorf("%q: expected no auth, got %v / %v", raw, h, err)
		}
	}
	bad := []string{
		`{"type":"bearer"}`,
		`{"type":"oauth","token_type":"Bearer"}`,
		`{"type":"header","header_name":"Host","value":"x"}`,
		`{"type":"header","header_name":"bad name","value":"x"}`,
		`{"type":"header","header_name":"X-K","value":"a\r\nb"}`,
		`{"type":"basic","token":"x"}`,
		`[1,2]`,
	}
	for _, raw := range bad {
		_, err := parseAuthConfig(json.RawMessage(raw))
		if err == nil {
			t.Errorf("%q: expected error", raw)
			continue
		}
		if strings.Contains(err.Error(), "a\r\nb") {
			t.Errorf("error leaks secret: %v", err)
		}
	}
	h, _ := parseAuthConfig(json.RawMessage(`{"type":"bearer","token":"s3cret"}`))
	if strings.Contains(h.String(), "s3cret") || strings.Contains(fmt.Sprintf("%v", h), "s3cret") {
		t.Error("authHeader must not print its secret")
	}
}

// --- E: Streamable HTTP ---

func TestStreamableHTTP_SSEResponse(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "echo"}}, sse: true}
	srv := newFakeServer(t, f)
	pool := realPool(t)

	conn, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: srv.URL})
	if err != nil {
		t.Fatalf("connect over SSE responses: %v", err)
	}
	if len(conn.tools) != 1 || conn.tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", conn.tools)
	}
	res, err := pool.CallTool(context.Background(), 1, "echo", map[string]interface{}{"a": 1})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.Content[0].ExtractText() != "called echo" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestReadSSEResponse_NoMatch(t *testing.T) {
	stream := "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}\n\n"
	if _, err := readSSEResponse(strings.NewReader(stream), 1); err == nil {
		t.Fatal("expected error when no response matches the id")
	}
	// multi-line data and missing trailing blank line
	stream = "data: {\"jsonrpc\":\"2.0\",\ndata: \"id\":1,\"result\":{\"ok\":true}}"
	resp, err := readSSEResponse(strings.NewReader(stream), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Result == nil {
		t.Error("expected result")
	}
}

func TestInitializeFailure_IsClearError(t *testing.T) {
	f := &fakeMCP{hook: func(w http.ResponseWriter, r *http.Request, msg rpcMessage) bool {
		http.Error(w, "nope", http.StatusMethodNotAllowed)
		return true
	}}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	_, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("expected initialize error, got %v", err)
	}
	if f.count("tools/list") != 0 {
		t.Error("must not continue after a failed initialize")
	}
}

func TestSessionExpired404_Reinitializes(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "t"}}, sessionID: "s1"}
	var once sync.Once
	f.hook = func(w http.ResponseWriter, r *http.Request, msg rpcMessage) bool {
		if msg.Method == "tools/call" && r.Header.Get("Mcp-Session-Id") == "s1" {
			expired := false
			once.Do(func() { expired = true })
			if expired {
				f.mu.Lock()
				f.sessionID = "s2"
				f.mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				return true
			}
		}
		return false
	}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	if _, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: srv.URL}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.CallTool(context.Background(), 1, "t", nil); err != nil {
		t.Fatalf("expected call to succeed after re-initialize, got %v", err)
	}
	if n := f.count("initialize"); n != 2 {
		t.Errorf("expected 2 initialize requests, got %d", n)
	}
}

func TestResponseBodyCapped(t *testing.T) {
	f := &fakeMCP{}
	f.hook = func(w http.ResponseWriter, r *http.Request, msg rpcMessage) bool {
		if msg.Method != "initialize" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		big := strings.Repeat("a", MaxResponseBytes+10)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"pad":"%s"}}`, big)
		return true
	}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	_, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: srv.URL})
	if err == nil || !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("expected body cap error, got %v", err)
	}
}

func TestStdioTransportRejected(t *testing.T) {
	pool := realPool(t)
	_, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: "stdio", URL: "http://example.com"})
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("expected unsupported transport error, got %v", err)
	}
}

// --- D: SSRF policy ---

func TestNetPolicy_CheckIP(t *testing.T) {
	cases := []struct {
		ip                       string
		def, private, loopbackOK bool // allowed under: default, BlockPrivate, AllowLoopback
	}{
		{"8.8.8.8", true, true, true},
		{"2606:4700::1111", true, true, true},
		{"127.0.0.1", false, false, true},
		{"127.5.5.5", false, false, true},
		{"::1", false, false, true},
		{"::ffff:127.0.0.1", false, false, true},
		{"169.254.169.254", false, false, false},
		{"::ffff:169.254.169.254", false, false, false},
		{"fe80::1", false, false, false},
		{"0.0.0.0", false, false, false},
		{"0.1.2.3", false, false, false},
		{"::", false, false, false},
		{"224.0.0.1", false, false, false},
		{"ff02::1", false, false, false},
		{"255.255.255.255", false, false, false},
		{"10.1.2.3", true, false, true},
		{"172.16.0.1", true, false, true},
		{"192.168.1.1", true, false, true},
		{"::ffff:192.168.1.1", true, false, true},
		{"100.64.0.1", true, false, true},
		{"fd00::1", true, false, true},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		check := func(p NetPolicy, want bool, label string) {
			err := p.CheckIP(ip)
			if (err == nil) != want {
				t.Errorf("%s %s: allowed=%v, want %v (err=%v)", label, tc.ip, err == nil, want, err)
			}
			if err != nil && !errors.Is(err, ErrBlockedAddress) {
				t.Errorf("%s %s: error must wrap ErrBlockedAddress", label, tc.ip)
			}
		}
		check(NetPolicy{}, tc.def, "default")
		check(NetPolicy{BlockPrivate: true}, tc.private, "block-private")
		check(NetPolicy{AllowLoopback: true}, tc.loopbackOK, "allow-loopback")
	}
}

func TestNetPolicy_FromEnv(t *testing.T) {
	t.Setenv("MCP_ALLOW_LOOPBACK", "true")
	t.Setenv("MCP_BLOCK_PRIVATE_NETWORKS", "TRUE")
	p := NetPolicyFromEnv()
	if !p.AllowLoopback || !p.BlockPrivate {
		t.Errorf("unexpected policy from env: %+v", p)
	}
	t.Setenv("MCP_ALLOW_LOOPBACK", "")
	t.Setenv("MCP_BLOCK_PRIVATE_NETWORKS", "no")
	if p := NetPolicyFromEnv(); p.AllowLoopback || p.BlockPrivate {
		t.Errorf("expected defaults, got %+v", p)
	}
}

func TestNetPolicy_ValidateURL(t *testing.T) {
	p := NetPolicy{}
	for _, raw := range []string{"", "ftp://x.example/", "file:///etc/passwd", "http://", "https://user:pw@x.example/", "http://169.254.169.254/latest", "http://[::1]:8080/"} {
		if err := p.ValidateURL(raw); err == nil {
			t.Errorf("%q: expected rejection", raw)
		}
	}
	for _, raw := range []string{"https://mcp.example.com/mcp", "http://10.0.0.5:8080/mcp"} {
		if err := p.ValidateURL(raw); err != nil {
			t.Errorf("%q: unexpected error %v", raw, err)
		}
	}
}

func TestLoopbackRefusedByDefaultPolicy(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "t"}}}
	srv := newFakeServer(t, f)

	// httptest listens on 127.0.0.1; use a host name so the check happens at dial time.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	url := "http://localhost:" + port

	strict := NewPool(WithNetPolicy(NetPolicy{}))
	defer strict.CloseAll()
	_, err := strict.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: url})
	if err == nil || !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected loopback to be refused at dial time, got %v", err)
	}
	if isTransientError(err) {
		t.Error("policy refusal must not be retried as transient")
	}
	if f.count("initialize") != 0 {
		t.Error("no request may reach the server")
	}

	relaxed := realPool(t) // testPolicy allows loopback
	if _, err := relaxed.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: url}); err != nil {
		t.Fatalf("expected injected test policy to allow loopback, got %v", err)
	}
}

func TestPrivateNetworksToggle(t *testing.T) {
	block := NetPolicy{BlockPrivate: true}
	if err := block.ValidateURL("http://10.0.0.5/mcp"); err == nil {
		t.Error("expected private literal IP to be refused with BlockPrivate")
	}
	if err := block.dialControl("tcp4", "192.168.0.10:443", nil); err == nil {
		t.Error("expected private dial to be refused with BlockPrivate")
	}
	if err := (NetPolicy{}).dialControl("tcp4", "192.168.0.10:443", nil); err != nil {
		t.Errorf("private ranges must be allowed by default: %v", err)
	}
}

func TestRedirectRefused(t *testing.T) {
	target := newFakeServer(t, &fakeMCP{tools: []mcp.Tool{{Name: "t"}}})
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	pool := realPool(t)
	_, err := pool.GetOrConnect(context.Background(), 1, MCPServerConfig{Transport: TransportHTTP, URL: redirector.URL})
	if err == nil || !errors.Is(err, errRedirect) {
		t.Fatalf("expected redirect refusal, got %v", err)
	}
}

// --- F: reload reconciliation ---

func TestReload_ClosesChangedAndRemovedServers(t *testing.T) {
	a := &fakeMCP{tools: []mcp.Tool{{Name: "a_tool"}}}
	b := &fakeMCP{tools: []mcp.Tool{{Name: "b_tool"}}}
	c := &fakeMCP{tools: []mcp.Tool{{Name: "c_tool"}}}
	srvA, srvB, srvC := newFakeServer(t, a), newFakeServer(t, b), newFakeServer(t, c)

	pool := realPool(t)
	h := NewProxyHandler(pool, nil)
	defer h.Stop()

	reg := func(id uint, ns, url, auth string) ServerRegistration {
		return ServerRegistration{InstanceID: id, NamespacePrefix: ns, AuthConfig: json.RawMessage(auth),
			Config: MCPServerConfig{Transport: TransportHTTP, URL: url, NamespacePrefix: ns, AuthConfig: json.RawMessage(auth)}}
	}
	first := []ServerRegistration{
		reg(1, "one", srvA.URL, `{"type":"bearer","token":"x"}`),
		reg(2, "two", srvB.URL, ""),
	}
	if err := h.LoadAndRegister(context.Background(), mockLoader(first)); err != nil {
		t.Fatal(err)
	}
	pool.mu.RLock()
	oldConn1 := pool.connections[1]
	pool.mu.RUnlock()

	// Same config with JSON keys reordered: must not reconnect.
	same := []ServerRegistration{
		reg(1, "one", srvA.URL, `{"token":"x", "type":"bearer"}`),
		reg(2, "two", srvB.URL, ""),
	}
	if err := h.Reload(context.Background(), mockLoader(same)); err != nil {
		t.Fatal(err)
	}
	if a.count("initialize") != 1 {
		t.Errorf("unchanged server must not re-initialize, got %d initializes", a.count("initialize"))
	}

	// Server 1 moves to another URL, server 2 is deleted.
	changed := []ServerRegistration{reg(1, "one", srvC.URL, `{"type":"bearer","token":"x"}`)}
	if err := h.Reload(context.Background(), mockLoader(changed)); err != nil {
		t.Fatal(err)
	}
	oldConn1.mu.RLock()
	closed := oldConn1.closed
	oldConn1.mu.RUnlock()
	if !closed {
		t.Error("old connection for a changed server must be closed")
	}
	if pool.IsConnected(2) || pool.HasConfig(2) {
		t.Error("removed server must be closed and forgotten by the pool")
	}
	h.mu.RLock()
	_, hasLimiter := h.limiters[2]
	h.mu.RUnlock()
	if hasLimiter {
		t.Error("removed server's limiter must be dropped")
	}
	if c.count("initialize") != 1 {
		t.Error("changed server must connect to the new URL")
	}
	if !h.IsProxyTool("one.c_tool") || h.IsProxyTool("one.a_tool") || h.IsProxyTool("two.b_tool") {
		t.Errorf("unexpected tool map after reload: %v", h.GetTools())
	}

	// Changing disabled_tools alone also counts as a change.
	disabled := changed[0]
	disabled.DisabledTools = []string{"c_tool"}
	if err := h.Reload(context.Background(), mockLoader([]ServerRegistration{disabled})); err != nil {
		t.Fatal(err)
	}
	if c.count("initialize") != 2 {
		t.Errorf("disabled_tools change must reconnect, got %d initializes", c.count("initialize"))
	}
}

// --- G: disabled_tools ---

func TestDisabledToolsNotRegistered(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "read"}, {Name: "delete_everything"}}}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	h := NewProxyHandler(pool, nil)
	defer h.Stop()

	regs := []ServerRegistration{{InstanceID: 1, NamespacePrefix: "ext", DisabledTools: []string{"delete_everything"},
		Config: MCPServerConfig{Transport: TransportHTTP, URL: srv.URL}}}
	if err := h.LoadAndRegister(context.Background(), mockLoader(regs)); err != nil {
		t.Fatal(err)
	}
	tools := h.GetTools()
	if len(tools) != 1 || tools[0].Name != "ext.read" {
		t.Fatalf("expected only ext.read, got %+v", tools)
	}
	if _, err := h.CallTool(context.Background(), "ext.delete_everything", nil); err == nil {
		t.Fatal("disabled tool must not be callable")
	}
	if len(f.callArgs) != 0 {
		t.Error("disabled tool call reached the server")
	}
}

// --- contract 6: sanitized names ---

func TestToolNameSanitizing(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{
		{Name: "repo.list", Description: "dotted"},
		{Name: "get issue/v2"},
		{Name: "repo_list", Description: "collides with repo.list"},
		{Name: "ok-name_1"},
	}}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	h := NewProxyHandler(pool, nil)
	defer h.Stop()

	regs := []ServerRegistration{{InstanceID: 1, NamespacePrefix: "ext.gh",
		Config: MCPServerConfig{Transport: TransportHTTP, URL: srv.URL}}}
	if err := h.LoadAndRegister(context.Background(), mockLoader(regs)); err != nil {
		t.Fatal(err)
	}

	names := map[string]string{}
	for _, tool := range h.GetTools() {
		names[tool.Name] = tool.Description
		if ns, _ := mcp.ParseToolName(tool.Name); ns != "ext.gh" {
			t.Errorf("%s: ParseToolName type = %q, want ext.gh", tool.Name, ns)
		}
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 tools (one collision skipped), got %v", names)
	}
	if names["ext.gh.repo_list"] != "dotted" {
		t.Errorf("first tool must win a collision, got %q", names["ext.gh.repo_list"])
	}
	if _, ok := names["ext.gh.get_issue_v2"]; !ok {
		t.Errorf("expected ext.gh.get_issue_v2, got %v", names)
	}
	if _, err := h.CallTool(context.Background(), "ext.gh.get_issue_v2", nil); err != nil {
		t.Fatal(err)
	}
	// Forwarded with the original name.
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.requests[len(f.requests)-1]
	if last.method != "tools/call" {
		t.Fatalf("unexpected last request %s", last.method)
	}

	// The schema refresh callback uses the same mapping.
	h.StartSchemaRefreshLoop(time.Hour)
	h.pool.mu.RLock()
	cb := h.pool.onSchemaRefresh
	h.pool.mu.RUnlock()
	cb(1, []mcp.Tool{{Name: "new.tool"}})
	if !h.IsProxyTool("ext.gh.new_tool") {
		t.Errorf("refresh callback must sanitize names, got %v", h.GetTools())
	}
}

// --- B: routing args ---

func TestCallTool_StripsRoutingArgs(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "q"}}}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	h := NewProxyHandler(pool, nil)
	defer h.Stop()
	if err := h.LoadAndRegister(context.Background(), mockLoader([]ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "ext", Config: MCPServerConfig{Transport: TransportHTTP, URL: srv.URL}},
	})); err != nil {
		t.Fatal(err)
	}

	args := map[string]interface{}{"query": "x", "logical_name": "slug", "tool_instance_id": float64(4)}
	if _, err := h.CallTool(context.Background(), "ext.q", args); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got := f.callArgs[0]
	f.mu.Unlock()
	if _, ok := got["logical_name"]; ok {
		t.Error("logical_name must not be forwarded")
	}
	if _, ok := got["tool_instance_id"]; ok {
		t.Error("tool_instance_id must not be forwarded")
	}
	if got["query"] != "x" {
		t.Errorf("real args must be forwarded, got %v", got)
	}
	if args["logical_name"] != "slug" {
		t.Error("caller's args map must not be mutated")
	}
}

// --- H: endpoints ---

func TestTestServer_Success(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "a", Description: "A"}}, sse: true}
	srv := newFakeServer(t, f)
	pool := realPool(t)
	h := NewProxyHandler(pool, nil)
	defer h.Stop()

	res := h.TestServer(context.Background(), TestServerRequest{URL: srv.URL, Transport: "http"}, nil)
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.ServerInfo.Name != "fake" || res.ProtocolVersion != MCPProtocolVersion || len(res.Tools) != 1 || res.Tools[0].Description != "A" {
		t.Errorf("unexpected result %+v", res)
	}
	if pool.ConnectionCount() != 0 {
		t.Error("test connection must not be stored in the pool")
	}
}

func TestTestServer_AuthRequired(t *testing.T) {
	f := &fakeMCP{hook: func(w http.ResponseWriter, r *http.Request, msg rpcMessage) bool {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}}
	srv := newFakeServer(t, f)
	h := NewProxyHandler(realPool(t), nil)
	defer h.Stop()

	res := h.TestServer(context.Background(), TestServerRequest{URL: srv.URL}, nil)
	if res.OK || !res.AuthRequired || res.StatusCode != 401 || !strings.Contains(res.WWWAuthenticate, "resource_metadata") {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestTestServer_StoredConfigFallback(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "a"}}}
	srv := newFakeServer(t, f)
	h := NewProxyHandler(realPool(t), nil)
	defer h.Stop()

	lookup := func(_ context.Context, id uint) (MCPServerConfig, error) {
		if id != 5 {
			return MCPServerConfig{}, fmt.Errorf("not found")
		}
		return MCPServerConfig{Transport: TransportSSE, URL: srv.URL, AuthConfig: json.RawMessage(`{"type":"bearer","token":"stored"}`)}, nil
	}
	id := uint(5)

	// id only: URL and auth from the DB.
	if res := h.TestServer(context.Background(), TestServerRequest{ID: &id}, lookup); !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	// id + url, auth absent: stored auth is used.
	if res := h.TestServer(context.Background(), TestServerRequest{ID: &id, URL: srv.URL}, lookup); !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	// id + url + explicit auth: body auth wins.
	if res := h.TestServer(context.Background(), TestServerRequest{ID: &id, URL: srv.URL,
		AuthConfig: json.RawMessage(`{"type":"bearer","token":"new"}`)}, lookup); !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	// id + auth, no url: the stored URL with the NEW auth (an override must
	// not be dropped just because the URL was omitted).
	if res := h.TestServer(context.Background(), TestServerRequest{ID: &id,
		AuthConfig: json.RawMessage(`{"type":"bearer","token":"replacement"}`)}, lookup); !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	f.mu.Lock()
	var tokens []string
	for _, r := range f.requests {
		if r.method == "initialize" {
			tokens = append(tokens, r.headers.Get("Authorization"))
		}
	}
	f.mu.Unlock()
	want := []string{"Bearer stored", "Bearer stored", "Bearer new", "Bearer replacement"}
	if strings.Join(tokens, ",") != strings.Join(want, ",") {
		t.Errorf("auth per test = %v, want %v", tokens, want)
	}

	missing := uint(6)
	if res := h.TestServer(context.Background(), TestServerRequest{ID: &missing}, lookup); res.OK || res.Error == "" {
		t.Errorf("expected error for unknown id, got %+v", res)
	}
}

func TestTestServer_PolicyAndTransportErrors(t *testing.T) {
	h := NewProxyHandler(NewPool(WithNetPolicy(NetPolicy{})), nil)
	defer h.Stop()
	for _, req := range []TestServerRequest{
		{URL: "http://127.0.0.1:1/mcp"},
		{URL: "file:///etc/passwd"},
		{URL: "https://example.com/mcp", Transport: "stdio"},
	} {
		if res := h.TestServer(context.Background(), req, nil); res.OK || res.Error == "" {
			t.Errorf("%+v: expected failure, got %+v", req, res)
		}
	}
}

func TestTestHTTPHandler(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "a"}}}
	srv := newFakeServer(t, f)
	h := NewProxyHandler(realPool(t), nil)
	defer h.Stop()

	gw := httptest.NewServer(h.TestHTTPHandler(nil))
	defer gw.Close()
	resp, err := http.Post(gw.URL, "application/json", strings.NewReader(fmt.Sprintf(`{"url":%q,"transport":"http"}`, srv.URL)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || out["ok"] != true {
		t.Fatalf("unexpected response %d %v", resp.StatusCode, out)
	}
	if _, ok := out["server_info"].(map[string]interface{}); !ok {
		t.Errorf("missing server_info: %v", out)
	}
}

func TestStatus_CoversFailedRegistrations(t *testing.T) {
	f := &fakeMCP{tools: []mcp.Tool{{Name: "b"}, {Name: "a"}, {Name: "hidden"}}}
	srv := newFakeServer(t, f)
	h := NewProxyHandler(realPool(t), nil)
	defer h.Stop()

	regs := []ServerRegistration{
		{InstanceID: 1, NamespacePrefix: "good", DisabledTools: []string{"hidden"}, Config: MCPServerConfig{Transport: TransportHTTP, URL: srv.URL}},
		{InstanceID: 2, NamespacePrefix: "bad", Config: MCPServerConfig{Transport: TransportHTTP, URL: "http://127.0.0.1:1/mcp"}},
	}
	if err := h.LoadAndRegister(context.Background(), mockLoader(regs)); err != nil {
		t.Fatal(err)
	}

	gw := httptest.NewServer(h.StatusHTTPHandler())
	defer gw.Close()
	resp, err := http.Get(gw.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Servers []ServerStatus `json:"servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %+v", out.Servers)
	}
	good, bad := out.Servers[0], out.Servers[1]
	if !good.Connected || good.ToolCount != 2 || strings.Join(good.Tools, ",") != "a,b" || good.Error != "" || good.NamespacePrefix != "good" {
		t.Errorf("unexpected good status %+v", good)
	}
	if _, err := time.Parse(time.RFC3339, good.CheckedAt); err != nil {
		t.Errorf("checked_at is not RFC3339: %q", good.CheckedAt)
	}
	if bad.Connected || bad.Error == "" || bad.ToolCount != 0 || bad.Tools == nil {
		t.Errorf("unexpected bad status %+v", bad)
	}
}

// A connect that started before a reload changed (or removed) the server must
// not replace the connection the reload installed.
func TestStaleConnectAfterReloadIsDropped(t *testing.T) {
	oldSrv := newFakeServer(t, &fakeMCP{tools: []mcp.Tool{{Name: "old"}}})
	newSrv := newFakeServer(t, &fakeMCP{tools: []mcp.Tool{{Name: "new"}}})
	p := realPool(t)
	oldCfg := MCPServerConfig{Transport: TransportHTTP, URL: oldSrv.URL}
	newCfg := MCPServerConfig{Transport: TransportHTTP, URL: newSrv.URL}

	// Reload installs the new config.
	p.Expect(7, newCfg)
	if _, err := p.GetOrConnect(context.Background(), 7, newCfg); err != nil {
		t.Fatalf("connect new: %v", err)
	}
	// A late connect with the old config (e.g. a retry started before the
	// reload) must be refused and must not replace the new connection.
	if _, err := p.GetOrConnect(context.Background(), 7, oldCfg); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("expected ErrStaleConfig, got %v", err)
	}
	tools, ok := p.GetTools(7)
	if !ok || len(tools) != 1 || tools[0].Name != "new" {
		t.Fatalf("connection replaced by stale config: %+v", tools)
	}

	// After removal, no config may reconnect until a reload expects one.
	p.Remove(7)
	if _, err := p.GetOrConnect(context.Background(), 7, newCfg); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("expected ErrStaleConfig after removal, got %v", err)
	}
	p.Expect(7, newCfg)
	if _, err := p.GetOrConnect(context.Background(), 7, newCfg); err != nil {
		t.Fatalf("connect after re-expect: %v", err)
	}

	// Instances never expected (system servers) are not checked.
	if _, err := p.GetOrConnect(context.Background(), 900001, oldCfg); err != nil {
		t.Fatalf("unexpected error for unchecked instance: %v", err)
	}
}
