package handlers

import (
	"context"
	"encoding/json"
	"errors"
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
		// The sentinel must survive the WebSocket round-trip so callers using
		// errors.Is can branch on a worker drop vs. a real provider error.
		if !errors.Is(err, ErrWorkerNotConnected) {
			t.Fatalf("expected ErrWorkerNotConnected sentinel, got %v", err)
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

// TestCleanupWorkerConn_PerConnRouting pins down the two reconnect-race
// orderings the per-conn ownership fix has to handle:
//
//	(1) A's cleanup runs after B has replaced workerConn. Pending entries
//	    owned by A MUST still be failed (otherwise A-era callers strand
//	    until ctx.Done()), and pending entries owned by B MUST NOT be
//	    touched.
//
//	(2) The mirror case where A's cleanup runs and a B-era entry has been
//	    registered concurrently in the global map. The B-era entry MUST
//	    NOT be failed by A's cleanup.
//
// We exercise cleanupWorkerConn directly with both pending entries planted
// so the routing is deterministic in a single run.
func TestCleanupWorkerConn_PerConnRouting(t *testing.T) {
	handler := NewAgentWSHandler()

	server := httptest.NewServer(http.HandlerFunc(handler.HandleWebSocket))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	connA, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !handler.IsWorkerConnected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	handler.mu.Lock()
	connAServer := handler.workerConn
	handler.mu.Unlock()
	if connAServer == nil {
		t.Fatal("expected workerConn to be set after dial")
	}

	connB, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.Close()
	deadline = time.Now().Add(2 * time.Second)
	var connBServer *websocket.Conn
	for time.Now().Before(deadline) {
		handler.mu.RLock()
		current := handler.workerConn
		handler.mu.RUnlock()
		if current != nil && current != connAServer {
			connBServer = current
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if connBServer == nil {
		t.Fatal("expected workerConn to flip to B's conn")
	}

	// Plant one A-owned entry (must be failed by A's cleanup) and one B-owned
	// entry (must NOT be touched). Direct map manipulation models the state
	// the racing OneShotLLM paths leave behind.
	aRequestID := "a-era-request"
	bRequestID := "b-era-request"
	chA := make(chan *AgentMessage, 1)
	chB := make(chan *AgentMessage, 1)
	handler.pendingOneshotMu.Lock()
	handler.pendingOneshot[aRequestID] = pendingOneshotEntry{ch: chA, conn: connAServer}
	handler.pendingOneshot[bRequestID] = pendingOneshotEntry{ch: chB, conn: connBServer}
	handler.pendingOneshotMu.Unlock()

	handler.cleanupWorkerConn(connAServer)

	// A-era caller must receive ErrWorkerNotConnected promptly.
	select {
	case msg := <-chA:
		if msg == nil || msg.Error == "" {
			t.Fatalf("A-era cleanup must signal an error response, got %+v", msg)
		}
		if msg.Error != ErrWorkerNotConnected.Error() {
			t.Fatalf("A-era cleanup error mismatch: got %q want %q", msg.Error, ErrWorkerNotConnected.Error())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("A-era cleanup did not signal the A-owned pending entry")
	}

	// B-era caller must remain untouched — it belongs to a different conn.
	select {
	case msg := <-chB:
		t.Fatalf("A's cleanup signaled B-owned pending entry: %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: no signal
	}

	// A-era entry should have been deleted; B-era entry should still be present.
	handler.pendingOneshotMu.Lock()
	if _, stillPresent := handler.pendingOneshot[aRequestID]; stillPresent {
		t.Error("A-era pending entry should have been deleted after cleanup")
	}
	if _, present := handler.pendingOneshot[bRequestID]; !present {
		t.Error("B-era pending entry should remain after A's cleanup")
	}
	delete(handler.pendingOneshot, bRequestID)
	handler.pendingOneshotMu.Unlock()
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

// readNewIncidentRequest waits for a new_incident frame on the fake worker so
// callback-disconnect tests can confirm StartIncident reached the worker
// before forcing a drop.
func readNewIncidentRequest(t *testing.T, conn *websocket.Conn) AgentMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		var msg AgentMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read worker frame: %v", err)
		}
		if msg.Type == AgentMessageTypeNewIncident {
			return msg
		}
	}
}

// TestStartIncident_WorkerDisconnectFiresOnError verifies that callers blocking
// on <-done are unblocked via OnError when the worker drops mid-investigation.
// Without per-conn callback ownership, cleanupWorkerConn left h.callbacks
// untouched and Slack/alert/API flows would hang indefinitely.
func TestStartIncident_WorkerDisconnectFiresOnError(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	done := make(chan struct{})
	var errMsg string
	var sawCompleted bool
	cb := IncidentCallback{
		OnCompleted: func(string, string, int, int64) { sawCompleted = true; close(done) },
		OnError: func(msg string) {
			errMsg = msg
			close(done)
		},
	}

	if err := handler.StartIncident("incident-disconnect", "task", nil, nil, nil, cb); err != nil {
		t.Fatalf("StartIncident: %v", err)
	}

	// Confirm the worker actually saw the request; otherwise the cleanup would
	// race the registration and the test would not exercise the bug.
	_ = readNewIncidentRequest(t, conn)

	// Force the worker drop. cleanupWorkerConn must wake the registered callback.
	conn.Close()

	select {
	case <-done:
		if sawCompleted {
			t.Fatal("disconnect should fire OnError, not OnCompleted")
		}
		if errMsg != ErrWorkerNotConnected.Error() {
			t.Fatalf("OnError msg: got %q want %q", errMsg, ErrWorkerNotConnected.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not invoked after worker disconnect — callback leaked")
	}

	handler.callbackMu.RLock()
	remaining := len(handler.callbacks)
	handler.callbackMu.RUnlock()
	if remaining != 0 {
		t.Errorf("callbacks map should be empty after cleanup, got %d entries", remaining)
	}
}

// TestStartIncident_NoWorkerReturnsError pins down the pre-condition that
// makes the per-conn ownership story sound: registration + send happen
// atomically under h.mu, so a not-yet-connected handler refuses the request
// instead of registering an orphan callback that nothing can ever drain.
func TestStartIncident_NoWorkerReturnsError(t *testing.T) {
	// StartIncident calls GetOrCreateProxySettings before the connect check,
	// so seed an in-memory DB or that path nil-pointers before we ever reach
	// the assertion.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.ProxySettings{}); err != nil {
		t.Fatalf("migrate proxy_settings: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	defer func() { database.DB = prevDB }()

	handler := NewAgentWSHandler()
	cb := IncidentCallback{}
	err = handler.StartIncident("incident-no-worker", "task", nil, nil, nil, cb)
	if !errors.Is(err, ErrWorkerNotConnected) {
		t.Fatalf("expected ErrWorkerNotConnected, got %v", err)
	}
	handler.callbackMu.RLock()
	remaining := len(handler.callbacks)
	handler.callbackMu.RUnlock()
	if remaining != 0 {
		t.Errorf("callbacks map should be empty when send fails, got %d entries", remaining)
	}
}

// TestStartIncident_SupersedingCallbackUnblocksPrevious verifies that when a
// second StartIncident call lands on the same incident_id while the previous
// run is still in flight (e.g. a user posts a second Slack message in the
// same thread before the first agent finishes), the displaced callback's
// OnError fires with ErrIncidentSuperseded so the older goroutine unblocks
// instead of hanging on <-done forever. Subsequent agent events for the new
// run's run_id route to the new callback; events that still carry the old
// run's run_id are dropped so a late frame from the superseded run cannot
// leak into the new waiter's callback.
func TestStartIncident_SupersedingCallbackUnblocksPrevious(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	prevDone := make(chan string, 1)
	prevCb := IncidentCallback{
		OnOutput: func(string) { t.Errorf("previous callback should not receive OnOutput after supersession") },
		OnCompleted: func(string, string, int, int64) {
			t.Errorf("previous callback should not receive OnCompleted after supersession")
		},
		OnError: func(msg string) { prevDone <- msg },
	}

	if err := handler.StartIncident("incident-supersede", "task-1", nil, nil, nil, prevCb); err != nil {
		t.Fatalf("first StartIncident: %v", err)
	}
	firstReq := readNewIncidentRequest(t, conn)
	if firstReq.RunID == "" {
		t.Fatal("first new_incident must carry a run_id")
	}
	oldRunID := firstReq.RunID

	newOutputs := make(chan string, 1)
	newCompleted := make(chan string, 1)
	newCb := IncidentCallback{
		OnOutput:    func(out string) { newOutputs <- out },
		OnCompleted: func(_, response string, _ int, _ int64) { newCompleted <- response },
		OnError:     func(msg string) { t.Errorf("new callback should not receive OnError: %q", msg) },
	}

	if err := handler.StartIncident("incident-supersede", "task-2", nil, nil, nil, newCb); err != nil {
		t.Fatalf("second StartIncident: %v", err)
	}
	secondReq := readNewIncidentRequest(t, conn)
	if secondReq.RunID == "" || secondReq.RunID == oldRunID {
		t.Fatalf("second new_incident must carry a fresh run_id (got %q, old %q)", secondReq.RunID, oldRunID)
	}
	newRunID := secondReq.RunID

	select {
	case msg := <-prevDone:
		if msg != ErrIncidentSuperseded.Error() {
			t.Fatalf("previous OnError msg: got %q want %q", msg, ErrIncidentSuperseded.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("previous callback was not unblocked after supersession")
	}

	// A late frame stamped with the OLD run_id must be dropped — without
	// run_id filtering, run 1's tool-output / completion would leak into
	// run 2's callback (the original Codex bug).
	handler.handleAgentOutput(AgentMessage{
		Type:       AgentMessageTypeAgentOutput,
		IncidentID: "incident-supersede",
		Output:     "stale output from superseded run",
		RunID:      oldRunID,
	})

	// Frames stamped with the NEW run_id route to the new callback.
	handler.handleAgentOutput(AgentMessage{
		Type:       AgentMessageTypeAgentOutput,
		IncidentID: "incident-supersede",
		Output:     "live output for run 2",
		RunID:      newRunID,
	})
	select {
	case got := <-newOutputs:
		if got != "live output for run 2" {
			t.Fatalf("OnOutput payload: got %q (stale frame may have leaked)", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("new callback did not receive OnOutput for the current run_id")
	}

	// A late completion stamped with the OLD run_id must NOT close the new
	// waiter's done channel or remove the new callback from the map.
	handler.handleAgentCompleted(AgentMessage{
		Type:       AgentMessageTypeAgentCompleted,
		IncidentID: "incident-supersede",
		Output:     "stale final response",
		SessionID:  "session-1",
		RunID:      oldRunID,
	})
	select {
	case <-newCompleted:
		t.Fatal("late completion from superseded run leaked into new callback")
	case <-time.After(100 * time.Millisecond):
		// expected: stale completion was dropped
	}
	handler.callbackMu.RLock()
	_, stillThere := handler.callbacks["incident-supersede"]
	handler.callbackMu.RUnlock()
	if !stillThere {
		t.Fatal("stale completion deleted the current run's callback entry")
	}

	// The matching completion must close the new run normally.
	handler.handleAgentCompleted(AgentMessage{
		Type:       AgentMessageTypeAgentCompleted,
		IncidentID: "incident-supersede",
		Output:     "final response",
		SessionID:  "session-2",
		RunID:      newRunID,
	})
	select {
	case <-newCompleted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("new callback did not receive OnCompleted for the current run_id")
	}

	handler.callbackMu.RLock()
	_, stillPresent := handler.callbacks["incident-supersede"]
	handler.callbackMu.RUnlock()
	if stillPresent {
		t.Error("callback should be removed after handleAgentCompleted")
	}
}

// TestHandleAgentOutput_SupersedeWaitsForInFlightCallback pins down the
// in-flight TOCTOU contract that the runID-only filter does not enforce on
// its own: when an agent_output frame is already inside the dispatch path
// for run A, a concurrent StartIncident for run B must NOT swap the entry
// and fire OnSuperseded until A.OnOutput has returned. Otherwise the
// displaced goroutine begins its early-return path (StopStream, "superseded"
// progress message update) while OnOutput is still mutating the same
// progress streamer + closure variables, racing the replacement.
//
// We force the race by blocking inside A.OnOutput, then issuing the second
// StartIncident from another goroutine. The fix holds the read lock across
// the OnOutput call so sendIncidentMessage cannot acquire its write lock —
// and therefore cannot fire OnSuperseded — until OnOutput returns.
func TestHandleAgentOutput_SupersedeWaitsForInFlightCallback(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	onOutputStarted := make(chan struct{})
	onOutputRelease := make(chan struct{})
	onOutputDone := make(chan struct{})
	onSupersededFired := make(chan struct{}, 1)

	prevCb := IncidentCallback{
		OnOutput: func(string) {
			close(onOutputStarted)
			<-onOutputRelease
			close(onOutputDone)
		},
		OnSuperseded: func() {
			onSupersededFired <- struct{}{}
		},
	}

	if err := handler.StartIncident("incident-toctou", "task-1", nil, nil, nil, prevCb); err != nil {
		t.Fatalf("first StartIncident: %v", err)
	}
	firstReq := readNewIncidentRequest(t, conn)
	if firstReq.RunID == "" {
		t.Fatal("first new_incident must carry a run_id")
	}
	aRunID := firstReq.RunID

	dispatchDone := make(chan struct{})
	go func() {
		handler.handleAgentOutput(AgentMessage{
			Type:       AgentMessageTypeAgentOutput,
			IncidentID: "incident-toctou",
			Output:     "in-flight delta",
			RunID:      aRunID,
		})
		close(dispatchDone)
	}()

	select {
	case <-onOutputStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("OnOutput did not start — dispatcher never reached the callback")
	}

	// Race the second StartIncident. It must block on the dispatcher's read
	// lock until A.OnOutput returns. The new_incident frame is drained later
	// (line below readNewIncidentRequest) once StartIncident returns.
	secondStarted := make(chan error, 1)
	go func() {
		secondStarted <- handler.StartIncident("incident-toctou", "task-2", nil, nil, nil, IncidentCallback{})
	}()

	// While OnOutput is still in flight, OnSuperseded must NOT fire — the fix
	// blocks sendIncidentMessage's write-lock acquisition behind the
	// dispatcher's read lock.
	select {
	case <-onSupersededFired:
		t.Fatal("OnSuperseded fired while A.OnOutput was still running — TOCTOU race")
	case <-time.After(150 * time.Millisecond):
	}

	close(onOutputRelease)

	select {
	case <-onOutputDone:
	case <-time.After(2 * time.Second):
		t.Fatal("OnOutput did not finish after release")
	}
	select {
	case <-dispatchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handleAgentOutput goroutine did not return")
	}

	select {
	case err := <-secondStarted:
		if err != nil {
			t.Fatalf("second StartIncident: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second StartIncident did not unblock after OnOutput released the lock")
	}
	_ = readNewIncidentRequest(t, conn)

	select {
	case <-onSupersededFired:
	case <-time.After(2 * time.Second):
		t.Fatal("OnSuperseded did not fire on the displaced callback")
	}
}

// TestStartIncident_SupersedingPrefersOnSuperseded verifies that when the
// displaced callback exposes OnSuperseded, sendIncidentMessage routes the
// supersession signal to it instead of OnError. This is the path the four
// production callsites use to unblock without writing a "superseded" failure
// to the DB / Slack — a regression here would race the replacement run's
// success update.
func TestStartIncident_SupersedingPrefersOnSuperseded(t *testing.T) {
	handler, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	supersededFired := make(chan struct{}, 1)
	prevCb := IncidentCallback{
		OnError:      func(string) { t.Errorf("OnError must not fire when OnSuperseded is set") },
		OnSuperseded: func() { supersededFired <- struct{}{} },
	}

	if err := handler.StartIncident("incident-prefer-supersede", "task-1", nil, nil, nil, prevCb); err != nil {
		t.Fatalf("first StartIncident: %v", err)
	}
	_ = readNewIncidentRequest(t, conn)

	newCb := IncidentCallback{}
	if err := handler.StartIncident("incident-prefer-supersede", "task-2", nil, nil, nil, newCb); err != nil {
		t.Fatalf("second StartIncident: %v", err)
	}
	_ = readNewIncidentRequest(t, conn)

	select {
	case <-supersededFired:
	case <-time.After(2 * time.Second):
		t.Fatal("OnSuperseded did not fire on the displaced callback")
	}
}

// TestHandleAgentOutput_NoCallbackWithRunIDDrops verifies that a late frame
// carrying a run_id but with no live callback is dropped instead of falling
// through to the blind full_log append. Without this filter, a stale frame
// from a superseded run that arrives after the replacement run's callback
// has been deleted would corrupt the incident's full_log.
func TestHandleAgentOutput_NoCallbackWithRunIDDrops(t *testing.T) {
	// In-memory sqlite so the fallback's UPDATE would succeed if it ran. We
	// then confirm it did NOT run by reading the row back — full_log must
	// remain empty.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate incident: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	if err := db.Create(&database.Incident{UUID: "incident-late-output", FullLog: ""}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	handler := NewAgentWSHandler()
	handler.handleAgentOutput(AgentMessage{
		Type:       AgentMessageTypeAgentOutput,
		IncidentID: "incident-late-output",
		Output:     "stale output",
		RunID:      "stale-run",
	})

	var got database.Incident
	if err := db.Where("uuid = ?", "incident-late-output").First(&got).Error; err != nil {
		t.Fatalf("re-read incident: %v", err)
	}
	if got.FullLog != "" {
		t.Errorf("full_log should remain empty when frame has run_id and no callback, got %q", got.FullLog)
	}

	// Sanity check: a frame WITHOUT run_id still falls through to the
	// fallback path (legacy worker / synthetic events) so the path itself
	// is intact.
	handler.handleAgentOutput(AgentMessage{
		Type:       AgentMessageTypeAgentOutput,
		IncidentID: "incident-late-output",
		Output:     "legacy output",
	})
	if err := db.Where("uuid = ?", "incident-late-output").First(&got).Error; err != nil {
		t.Fatalf("re-read incident: %v", err)
	}
	if got.FullLog != "legacy output" {
		t.Errorf("legacy fallback should still write full_log, got %q", got.FullLog)
	}
}

// TestHandleAgentCompleted_NoCallbackWithRunIDDrops verifies the parallel
// drop-on-late-frame logic for completion frames: a late frame from a
// superseded run must not overwrite the replacement run's status / response /
// session_id after the replacement has already finalized.
func TestHandleAgentCompleted_NoCallbackWithRunIDDrops(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate incident: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	if err := db.Create(&database.Incident{
		UUID:      "incident-late-completed",
		Status:    database.IncidentStatusCompleted,
		Response:  "real response",
		SessionID: "real-session",
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	handler := NewAgentWSHandler()
	handler.handleAgentCompleted(AgentMessage{
		Type:       AgentMessageTypeAgentCompleted,
		IncidentID: "incident-late-completed",
		Output:     "stale response",
		SessionID:  "stale-session",
		RunID:      "stale-run",
	})

	var got database.Incident
	if err := db.Where("uuid = ?", "incident-late-completed").First(&got).Error; err != nil {
		t.Fatalf("re-read incident: %v", err)
	}
	if got.Response != "real response" {
		t.Errorf("response was overwritten by stale completion: got %q", got.Response)
	}
	if got.SessionID != "real-session" {
		t.Errorf("session_id was overwritten by stale completion: got %q", got.SessionID)
	}
}

// TestHandleAgentError_NoCallbackWithRunIDDrops verifies the same drop-on-
// late-frame contract for error frames: a late error from a superseded run
// must not flip the incident to FAILED after the replacement run completed.
func TestHandleAgentError_NoCallbackWithRunIDDrops(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate incident: %v", err)
	}
	prevDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = prevDB })

	if err := db.Create(&database.Incident{
		UUID:     "incident-late-error",
		Status:   database.IncidentStatusCompleted,
		Response: "real response",
	}).Error; err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	handler := NewAgentWSHandler()
	handler.handleAgentError(AgentMessage{
		Type:       AgentMessageTypeAgentError,
		IncidentID: "incident-late-error",
		Error:      "stale error",
		RunID:      "stale-run",
	})

	var got database.Incident
	if err := db.Where("uuid = ?", "incident-late-error").First(&got).Error; err != nil {
		t.Fatalf("re-read incident: %v", err)
	}
	if got.Status != database.IncidentStatusCompleted {
		t.Errorf("status flipped by stale error frame: got %q want %q", got.Status, database.IncidentStatusCompleted)
	}
	if got.Response != "real response" {
		t.Errorf("response overwritten by stale error: got %q", got.Response)
	}
}

// TestCleanupWorkerConn_PerConnCallbackRouting mirrors the oneshot per-conn
// routing test for incident callbacks. Pending callbacks owned by the dying
// conn must be failed via OnError; callbacks owned by a replacement conn must
// be left alone so the reconnect race never fires OnError on a fresh incident.
func TestCleanupWorkerConn_PerConnCallbackRouting(t *testing.T) {
	handler := NewAgentWSHandler()

	server := httptest.NewServer(http.HandlerFunc(handler.HandleWebSocket))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	connA, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !handler.IsWorkerConnected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	handler.mu.Lock()
	connAServer := handler.workerConn
	handler.mu.Unlock()
	if connAServer == nil {
		t.Fatal("expected workerConn after dial A")
	}

	connB, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer connB.Close()
	deadline = time.Now().Add(2 * time.Second)
	var connBServer *websocket.Conn
	for time.Now().Before(deadline) {
		handler.mu.RLock()
		current := handler.workerConn
		handler.mu.RUnlock()
		if current != nil && current != connAServer {
			connBServer = current
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if connBServer == nil {
		t.Fatal("expected workerConn to flip to B's conn")
	}

	aFiredCh := make(chan string, 1)
	bFiredCh := make(chan string, 1)
	cbA := IncidentCallback{OnError: func(msg string) { aFiredCh <- msg }}
	cbB := IncidentCallback{OnError: func(msg string) { bFiredCh <- msg }}

	handler.callbackMu.Lock()
	handler.callbacks["a-incident"] = incidentCallbackEntry{callback: cbA, conn: connAServer}
	handler.callbacks["b-incident"] = incidentCallbackEntry{callback: cbB, conn: connBServer}
	handler.callbackMu.Unlock()

	handler.cleanupWorkerConn(connAServer)

	select {
	case msg := <-aFiredCh:
		if msg != ErrWorkerNotConnected.Error() {
			t.Fatalf("A OnError msg: got %q want %q", msg, ErrWorkerNotConnected.Error())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("A's cleanup did not fire OnError on the A-owned callback")
	}

	select {
	case msg := <-bFiredCh:
		t.Fatalf("A's cleanup fired OnError on a B-owned callback: %q", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: B is untouched
	}

	handler.callbackMu.Lock()
	if _, present := handler.callbacks["a-incident"]; present {
		t.Error("A-owned callback should be deleted after cleanup")
	}
	if _, present := handler.callbacks["b-incident"]; !present {
		t.Error("B-owned callback should remain after A's cleanup")
	}
	delete(handler.callbacks, "b-incident")
	handler.callbackMu.Unlock()
}
