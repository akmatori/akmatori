package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupOneshotTest connects the handler over an httptest WebSocket server and
// returns the handler, a connected fake-worker websocket, and a cleanup func.
//
// Why a real WebSocket round-trip: AgentWSHandler.workerConn is a concrete
// *websocket.Conn that is read in a tight loop in HandleWebSocket. Substituting
// an interface would change production code only to ease testing, so we mirror
// production wiring instead.
func setupOneshotTest(t *testing.T) (*AgentWSHandler, *websocket.Conn, func()) {
	t.Helper()

	// In-memory sqlite so GetOrCreateProxySettings can succeed.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.ProxySettings{}); err != nil {
		t.Fatalf("migrate proxy_settings: %v", err)
	}
	prevDB := database.DB
	database.DB = db

	handler := NewAgentWSHandler()
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWebSocket))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		database.DB = prevDB
		t.Fatalf("dial fake worker: %v", err)
	}

	// Wait for the handler to mark the worker as connected — Upgrade() runs
	// in a separate goroutine so IsWorkerConnected() may briefly lag the dial.
	deadline := time.Now().Add(2 * time.Second)
	for !handler.IsWorkerConnected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !handler.IsWorkerConnected() {
		conn.Close()
		server.Close()
		database.DB = prevDB
		t.Fatal("worker did not register as connected")
	}

	cleanup := func() {
		conn.Close()
		server.Close()
		database.DB = prevDB
	}
	return handler, conn, cleanup
}

// readOneshotRequest waits for a oneshot_llm_request frame on the fake worker.
func readOneshotRequest(t *testing.T, conn *websocket.Conn) AgentMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		var msg AgentMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read worker frame: %v", err)
		}
		if msg.Type == AgentMessageTypeOneshotLLMRequest {
			return msg
		}
	}
}

func writeOneshotResponse(t *testing.T, conn *websocket.Conn, requestID, summary, errMsg string) {
	t.Helper()
	resp := AgentMessage{
		Type:      AgentMessageTypeOneshotLLMResponse,
		RequestID: requestID,
		Summary:   summary,
		Error:     errMsg,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestOneShotLLM_SuccessRoundTrip(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	llm := &LLMSettingsForWorker{Provider: "anthropic", APIKey: "sk-test", Model: "claude-x"}

	type result struct {
		out string
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := handler.OneShotLLM(ctx, llm, "you are a helper", "summarize this", 100, 0.2)
		resCh <- result{out, err}
	}()

	req := readOneshotRequest(t, conn)
	if req.RequestID == "" {
		t.Fatal("request_id should not be empty")
	}
	if req.System != "you are a helper" {
		t.Errorf("system: got %q", req.System)
	}
	if req.User != "summarize this" {
		t.Errorf("user: got %q", req.User)
	}
	if req.MaxTokens != 100 {
		t.Errorf("max_tokens: got %d", req.MaxTokens)
	}
	if req.Temperature != 0.2 {
		t.Errorf("temperature: got %v", req.Temperature)
	}
	if req.Provider != "anthropic" {
		t.Errorf("provider: got %q", req.Provider)
	}
	if req.APIKey != "sk-test" {
		t.Errorf("api_key: got %q", req.APIKey)
	}

	writeOneshotResponse(t, conn, req.RequestID, "the summary", "")

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("unexpected err: %v", r.err)
		}
		if r.out != "the summary" {
			t.Errorf("summary: got %q want %q", r.out, "the summary")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OneShotLLM did not return")
	}

	// pending entry must be cleaned up.
	handler.pendingOneshotMu.Lock()
	pending := len(handler.pendingOneshot)
	handler.pendingOneshotMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingOneshot leaked: %d entries", pending)
	}
}

func TestOneShotLLM_ErrorPropagates(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	resCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := handler.OneShotLLM(ctx, nil, "", "user", 50, 0)
		resCh <- err
	}()

	req := readOneshotRequest(t, conn)
	writeOneshotResponse(t, conn, req.RequestID, "", "provider auth failed")

	select {
	case err := <-resCh:
		if err == nil || err.Error() != "provider auth failed" {
			t.Fatalf("expected provider auth failed error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OneShotLLM did not return")
	}
}

func TestOneShotLLM_ContextCancellationCleansUp(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	resCh := make(chan error, 1)
	go func() {
		_, err := handler.OneShotLLM(ctx, nil, "", "user", 10, 0)
		resCh <- err
	}()

	// Drain the request frame so we know the call has registered its pending entry.
	_ = readOneshotRequest(t, conn)

	cancel()

	select {
	case err := <-resCh:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OneShotLLM did not unblock on cancel")
	}

	handler.pendingOneshotMu.Lock()
	pending := len(handler.pendingOneshot)
	handler.pendingOneshotMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingOneshot leaked after cancel: %d entries", pending)
	}
}

func TestOneShotLLM_WorkerNotConnected(t *testing.T) {
	handler := NewAgentWSHandler()
	_, err := handler.OneShotLLM(context.Background(), nil, "", "user", 10, 0)
	if err != ErrWorkerNotConnected {
		t.Fatalf("expected ErrWorkerNotConnected, got %v", err)
	}
}

func TestOneShotLLM_ConcurrentRequestsRouted(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	// Serialize fake-worker reads via a single goroutine that responds to each
	// request out of order to prove correlation isn't accidental ordering.
	const n = 4
	type result struct {
		idx int
		out string
		err error
	}
	resCh := make(chan result, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, err := handler.OneShotLLM(ctx, nil, "", "user", i, float64(i)/10)
			resCh <- result{i, out, err}
		}(i)
	}

	// Collect requests, then respond in reverse order.
	reqs := make([]AgentMessage, n)
	for i := 0; i < n; i++ {
		reqs[i] = readOneshotRequest(t, conn)
	}
	for i := n - 1; i >= 0; i-- {
		// MaxTokens is the per-call sentinel — echo it back so we can verify
		// each goroutine receives its own response.
		writeOneshotResponse(t, conn, reqs[i].RequestID, formatSummary(reqs[i].MaxTokens), "")
	}

	wg.Wait()
	close(resCh)

	got := make(map[int]string)
	for r := range resCh {
		if r.err != nil {
			t.Fatalf("call %d errored: %v", r.idx, r.err)
		}
		got[r.idx] = r.out
	}
	for i := 0; i < n; i++ {
		want := formatSummary(i)
		if got[i] != want {
			t.Errorf("call %d: got %q want %q", i, got[i], want)
		}
	}

	handler.pendingOneshotMu.Lock()
	pending := len(handler.pendingOneshot)
	handler.pendingOneshotMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingOneshot leaked: %d entries", pending)
	}
}

func formatSummary(i int) string {
	return "summary-" + string(rune('a'+i))
}

// TestOneShotLLM_WorkerDisconnectWakesPending verifies that pending callers are
// unblocked with an error when the worker drops, instead of waiting for the
// per-call context deadline.
func TestOneShotLLM_WorkerDisconnectWakesPending(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	resCh := make(chan error, 1)
	go func() {
		// Use a long timeout so the test relies on the disconnect (not the
		// deadline) to unblock the call.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := handler.OneShotLLM(ctx, nil, "", "user", 1, 0)
		resCh <- err
	}()

	_ = readOneshotRequest(t, conn)

	// Simulate the worker dropping by closing the WebSocket on the worker side.
	conn.Close()

	select {
	case err := <-resCh:
		if err == nil {
			t.Fatal("expected an error after worker disconnect, got nil")
		}
		if !strings.Contains(err.Error(), ErrWorkerNotConnected.Error()) {
			t.Fatalf("expected disconnect error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OneShotLLM did not unblock after worker disconnect")
	}

	handler.pendingOneshotMu.Lock()
	pending := len(handler.pendingOneshot)
	handler.pendingOneshotMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingOneshot leaked after disconnect: %d entries", pending)
	}
}

func TestHandleOneshotLLMResponse_NoListenerDropsSilently(t *testing.T) {
	handler := NewAgentWSHandler()
	// Should not panic, should not deadlock, should not register anything.
	handler.handleOneshotLLMResponse(AgentMessage{
		Type:      AgentMessageTypeOneshotLLMResponse,
		RequestID: "missing",
		Summary:   "ignored",
	})
	handler.pendingOneshotMu.Lock()
	pending := len(handler.pendingOneshot)
	handler.pendingOneshotMu.Unlock()
	if pending != 0 {
		t.Errorf("pendingOneshot should remain empty, got %d", pending)
	}
}
