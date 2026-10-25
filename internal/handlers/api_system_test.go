package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func getSystemStatus(t *testing.T, h *APIHandler) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.handleSystemStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Worker map[string]any `json:"worker"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return body.Worker
}

func TestSystemStatus_NoWorker(t *testing.T) {
	for name, h := range map[string]*APIHandler{
		"nil ws handler":    {},
		"nothing connected": {agentWSHandler: NewAgentWSHandler()},
	} {
		t.Run(name, func(t *testing.T) {
			worker := getSystemStatus(t, h)
			if worker["connected"] != false {
				t.Errorf("connected = %v, want false", worker["connected"])
			}
			for _, k := range []string{"connected_at", "last_message_at", "last_message_age_seconds"} {
				if _, ok := worker[k]; ok {
					t.Errorf("%s present for a disconnected worker", k)
				}
			}
		})
	}
}

func TestSystemStatus_ConnectedWorker(t *testing.T) {
	ws, conn, cleanup := setupOneshotTest(t)
	defer cleanup()

	worker := getSystemStatus(t, &APIHandler{agentWSHandler: ws})
	if worker["connected"] != true {
		t.Fatalf("connected = %v, want true", worker["connected"])
	}
	if age, _ := worker["last_message_age_seconds"].(float64); age < 0 || age > 5 {
		t.Errorf("last_message_age_seconds = %v, want about 0", worker["last_message_age_seconds"])
	}
	if _, err := time.Parse(time.RFC3339Nano, worker["connected_at"].(string)); err != nil {
		t.Errorf("connected_at not RFC 3339: %v", err)
	}

	// A heartbeat moves last_message_at forward.
	ws.mu.Lock()
	ws.lastMessageAt = time.Now().Add(-2 * time.Minute)
	ws.mu.Unlock()
	if err := conn.WriteJSON(AgentMessage{Type: AgentMessageTypeHeartbeat}); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Since(ws.WorkerStatus().LastMessageAt) > time.Minute && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if age := time.Since(ws.WorkerStatus().LastMessageAt); age > time.Minute {
		t.Errorf("heartbeat did not refresh last_message_at (age %v)", age)
	}

	// Disconnect clears the status.
	_ = conn.Close()
	deadline = time.Now().Add(2 * time.Second)
	for ws.WorkerStatus().Connected && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st := ws.WorkerStatus(); st.Connected || !st.ConnectedAt.IsZero() || !st.LastMessageAt.IsZero() {
		t.Errorf("after disconnect WorkerStatus = %+v, want zero", st)
	}
}

func TestMarkWorkerSeen_IgnoresReplacedConn(t *testing.T) {
	ws, _, cleanup := setupOneshotTest(t)
	defer cleanup()

	old := time.Now().Add(-time.Hour)
	ws.mu.Lock()
	ws.lastMessageAt = old
	ws.mu.Unlock()

	// A frame read on a conn that no longer owns the slot (a reconnect raced
	// it) must not make the current worker look alive.
	ws.markWorkerSeen(nil)
	if got := ws.WorkerStatus().LastMessageAt; !got.Equal(old) {
		t.Errorf("LastMessageAt = %v, want unchanged %v", got, old)
	}
}
