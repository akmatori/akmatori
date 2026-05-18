package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/utils"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// AgentMessageType represents the type of WebSocket message
type AgentMessageType string

const (
	// Messages from API to Agent Worker
	AgentMessageTypeNewIncident       AgentMessageType = "new_incident"
	AgentMessageTypeContinueIncident  AgentMessageType = "continue_incident"
	AgentMessageTypeCancelIncident    AgentMessageType = "cancel_incident"
	AgentMessageTypeProxyConfigUpdate AgentMessageType = "proxy_config_update"
	AgentMessageTypeOneshotLLMRequest AgentMessageType = "oneshot_llm_request"

	// Messages from Agent Worker to API
	AgentMessageTypeAgentOutput        AgentMessageType = "agent_output"
	AgentMessageTypeAgentCompleted     AgentMessageType = "agent_completed"
	AgentMessageTypeAgentError         AgentMessageType = "agent_error"
	AgentMessageTypeHeartbeat          AgentMessageType = "heartbeat"
	AgentMessageTypeStatus             AgentMessageType = "status"
	AgentMessageTypeOneshotLLMResponse AgentMessageType = "oneshot_llm_response"
)

// oneshotLLMDefaultTimeout is used when callers pass a context with no deadline.
const oneshotLLMDefaultTimeout = 60 * time.Second

// ProxyConfig holds proxy configuration with per-service toggles
type ProxyConfig struct {
	URL                    string `json:"url"`
	NoProxy                string `json:"no_proxy"`
	LLMEnabled             bool   `json:"llm_enabled"`
	SlackEnabled           bool   `json:"slack_enabled"`
	ZabbixEnabled          bool   `json:"zabbix_enabled"`
	VictoriaMetricsEnabled bool   `json:"victoria_metrics_enabled"`
}

// AgentMessage represents a WebSocket message between API and agent worker
type AgentMessage struct {
	Type       AgentMessageType       `json:"type"`
	IncidentID string                 `json:"incident_id,omitempty"`
	Task       string                 `json:"task,omitempty"`
	Message    string                 `json:"message,omitempty"`
	Output     string                 `json:"output,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`

	// Execution metrics (sent with agent_completed)
	TokensUsed      int   `json:"tokens_used,omitempty"`
	ExecutionTimeMs int64 `json:"execution_time_ms,omitempty"`

	// LLM settings (sent with new_incident)
	Provider      string `json:"provider,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"thinking_level,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`

	// Proxy configuration with toggles (sent with new_incident)
	ProxyConfig *ProxyConfig `json:"proxy_config,omitempty"`

	// Enabled skill names (sent with new_incident to filter skill discovery)
	EnabledSkills []string `json:"enabled_skills,omitempty"`

	// Tool allowlist (sent with new_incident to restrict tool access)
	ToolAllowlist []services.ToolAllowlistEntry `json:"tool_allowlist,omitempty"`

	// One-shot LLM request/response correlation fields
	RequestID   string  `json:"request_id,omitempty"`
	System      string  `json:"system,omitempty"`
	User        string  `json:"user,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Summary     string  `json:"summary,omitempty"`

	// RunID identifies a single StartIncident/ContinueIncident invocation.
	// The API generates a fresh run_id per call; the worker echoes it on every
	// agent_output / agent_completed / agent_error frame for that run. The API
	// drops events whose run_id does not match the currently registered
	// callback so a superseded run cannot leak frames into the new waiter.
	RunID string `json:"run_id,omitempty"`
}

// LLMSettingsForWorker is re-exported from services so handler code that
// referenced it before the lift continues to compile unchanged. The canonical
// definition (and the OneShotLLMCaller interface that consumes it) lives in
// internal/services/llm_settings.go.
type LLMSettingsForWorker = services.LLMSettingsForWorker

// pendingOneshotEntry pairs a oneshot response channel with the worker
// connection that received the request. cleanupWorkerConn uses the conn
// pointer to signal only entries owned by the disconnecting conn so a
// reconnect race never fails a replacement-era caller and never strands an
// A-era caller after B has already taken over workerConn.
type pendingOneshotEntry struct {
	ch   chan *AgentMessage
	conn *websocket.Conn
}

// incidentCallbackEntry pairs an incident callback with the worker conn the
// incident request was sent on. cleanupWorkerConn fails only callbacks owned
// by the disconnecting conn so a reconnect race never fires OnError on a
// replacement-era incident and never strands an A-era caller after B has
// taken over workerConn.
//
// runID identifies the specific Start/Continue call that registered this
// entry. The worker echoes the same run_id on every agent_output /
// agent_completed / agent_error frame, and the dispatch path drops frames
// whose run_id does not match — a superseded run can therefore keep emitting
// late frames without leaking them into the new waiter's callback.
//
// finalized switches to true once OnCompleted has fired. The entry is then
// retained in the map until the waiter goroutine calls ReleaseRun, which
// keeps OnSuperseded reachable while the waiter is still running its
// post-completion work (e.g. blocked in the response formatter). Without
// this, a newer Start/Continue arriving during finalization could not signal
// the displaced waiter, and a stale finalize could overwrite the
// replacement run's result.
type incidentCallbackEntry struct {
	callback  IncidentCallback
	conn      *websocket.Conn
	runID     string
	finalized bool
}

// AgentWSHandler handles WebSocket connections from the agent worker
type AgentWSHandler struct {
	upgrader         websocket.Upgrader
	mu               sync.RWMutex
	workerConn       *websocket.Conn
	workerReady      bool
	callbacks        map[string]incidentCallbackEntry // incident_id -> callback + owning conn
	callbackMu       sync.RWMutex
	pendingOneshot   map[string]pendingOneshotEntry // request_id -> response channel + owning conn
	pendingOneshotMu sync.Mutex
}

// IncidentCallback is re-exported from services so handler code that
// referenced it before the lift continues to compile unchanged. The canonical
// definition (and the IncidentRunner interface that consumes it) lives in
// internal/services/llm_settings.go.
//
// OnSuperseded fires when a newer StartIncident/ContinueIncident displaces
// this callback for the same incident_id (e.g. a second Slack message lands
// in the same thread before the first run finishes). The displaced run has
// been handed off to the new callback — the new run will finalize the
// incident in the DB and Slack — so the old goroutine should unblock and
// exit silently rather than commit a failure that races the replacement's
// success. When OnSuperseded is nil, sendIncidentMessage falls back to
// firing OnError with ErrIncidentSuperseded so legacy callers still unblock.
type IncidentCallback = services.IncidentCallback

// NewAgentWSHandler creates a new agent WebSocket handler
func NewAgentWSHandler() *AgentWSHandler {
	return &AgentWSHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for internal communication
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		callbacks:      make(map[string]incidentCallbackEntry),
		pendingOneshot: make(map[string]pendingOneshotEntry),
	}
}

// SetupRoutes configures WebSocket routes
func (h *AgentWSHandler) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/agent", h.HandleWebSocket)
}

// HandleWebSocket handles the WebSocket connection from the agent worker
func (h *AgentWSHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("failed to upgrade WebSocket", "err", err)
		return
	}

	slog.Info("agent worker connected", "remote_addr", r.RemoteAddr)

	// Store the worker connection
	h.mu.Lock()
	if h.workerConn != nil {
		// Close existing connection
		h.workerConn.Close()
	}
	h.workerConn = conn
	h.workerReady = true
	h.mu.Unlock()

	defer h.cleanupWorkerConn(conn)

	// Read messages from worker
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket read error", "err", err)
			}
			return
		}

		var msg AgentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Error("failed to parse message", "err", err)
			continue
		}

		h.handleMessage(msg)
	}
}

// handleMessage processes incoming messages from the agent worker
func (h *AgentWSHandler) handleMessage(msg AgentMessage) {
	slog.Info("received message from worker", "type", msg.Type, "incident_id", msg.IncidentID)

	switch msg.Type {
	case AgentMessageTypeHeartbeat:
		// Just a heartbeat, no action needed
		return

	case AgentMessageTypeStatus:
		// Worker status update
		if status, ok := msg.Data["status"].(string); ok {
			slog.Info("worker status", "status", status)
		}
		return

	case AgentMessageTypeAgentOutput:
		h.handleAgentOutput(msg)

	case AgentMessageTypeAgentCompleted:
		h.handleAgentCompleted(msg)

	case AgentMessageTypeAgentError:
		h.handleAgentError(msg)

	case AgentMessageTypeOneshotLLMResponse:
		h.handleOneshotLLMResponse(msg)

	default:
		slog.Warn("unknown message type from worker", "type", msg.Type)
	}
}

// cleanupWorkerConn runs the per-connection teardown when HandleWebSocket
// returns. It clears workerConn only if this conn still owns the slot, then
// always fails pending oneshots and incident callbacks that were registered
// against this conn — regardless of whether a reconnect has already installed
// a replacement. Per-conn ownership prevents two reconnect-race orderings
// from misrouting disconnect signals: (1) cleanup runs while a replacement
// has just begun registering its own pending entries (those entries belong
// to B's conn, so A's cleanup leaves them alone); (2) cleanup runs after B
// has already replaced A in workerConn (A's entries are still owned by A
// and would otherwise strand until ctx.Done() or, for incident callers, until
// they block forever on <-done).
func (h *AgentWSHandler) cleanupWorkerConn(conn *websocket.Conn) {
	h.mu.Lock()
	if h.workerConn == conn {
		h.workerConn = nil
		h.workerReady = false
	}
	h.mu.Unlock()
	conn.Close()

	h.failPendingOneshotForConn(conn, ErrWorkerNotConnected.Error())
	h.failCallbacksForConn(conn, ErrWorkerNotConnected.Error())

	slog.Info("agent worker disconnected")
}

// failCallbacksForConn invokes OnError on every incident callback that was
// registered against the given conn, then marks the entry finalized so the
// waiter goroutine can claim ownership of the failure DB write via
// ReleaseRun. Callbacks owned by other (replacement) conns are left
// untouched. Entries already marked finalized (OnCompleted fired) are also
// left in place: the waiter still needs to run its post-completion work
// and call ReleaseRun, and firing OnError on a finalized run would corrupt
// its captured response. Keeping the entry around (instead of deleting it)
// matches dispatchOnCompleted's contract — a concurrently-arriving
// Start/Continue can still displace the entry and fire OnSuperseded, while
// a normal disconnect-induced error lets ReleaseRun succeed so the waiter
// finalizes the incident as failed in the DB. OnError implementations in
// this codebase only close a sync.Once-guarded done channel, so they're
// non-blocking; we still call them outside callbackMu to avoid forcing
// future callback bodies into a locked critical section.
func (h *AgentWSHandler) failCallbacksForConn(conn *websocket.Conn, errMsg string) {
	h.callbackMu.Lock()
	var failed []IncidentCallback
	for incidentID, entry := range h.callbacks {
		if entry.conn != conn || entry.finalized {
			continue
		}
		failed = append(failed, entry.callback)
		entry.finalized = true
		h.callbacks[incidentID] = entry
	}
	h.callbackMu.Unlock()

	for _, cb := range failed {
		if cb.OnError != nil {
			cb.OnError(errMsg)
		}
	}
}

// failPendingOneshotForConn delivers an error response to every waiting
// oneshot caller whose request was sent over the given conn. Each pending
// channel is buffered=1 and only ever receives one response, so a
// non-blocking send is sufficient.
func (h *AgentWSHandler) failPendingOneshotForConn(conn *websocket.Conn, errMsg string) {
	h.pendingOneshotMu.Lock()
	var failed []chan *AgentMessage
	for requestID, entry := range h.pendingOneshot {
		if entry.conn == conn {
			failed = append(failed, entry.ch)
			delete(h.pendingOneshot, requestID)
		}
	}
	h.pendingOneshotMu.Unlock()

	for _, ch := range failed {
		resp := &AgentMessage{
			Type:  AgentMessageTypeOneshotLLMResponse,
			Error: errMsg,
		}
		select {
		case ch <- resp:
		default:
		}
	}
}

// handleOneshotLLMResponse routes a oneshot LLM response back to the waiting caller.
// Drops silently (debug-logged) if no listener is registered for the request_id.
func (h *AgentWSHandler) handleOneshotLLMResponse(msg AgentMessage) {
	h.pendingOneshotMu.Lock()
	entry, exists := h.pendingOneshot[msg.RequestID]
	h.pendingOneshotMu.Unlock()

	if !exists {
		slog.Debug("dropping oneshot llm response with no listener", "request_id", msg.RequestID)
		return
	}

	// Make a heap copy so the channel reader sees a stable value.
	respCopy := msg
	select {
	case entry.ch <- &respCopy:
	default:
		slog.Debug("dropping oneshot llm response: channel full or closed", "request_id", msg.RequestID)
	}
}

// handleAgentOutput handles streaming output from the agent. Drops frames
// from a superseded run (msg.RunID does not match the registered entry's
// runID) so late output from run 1 cannot bleed into run 2's callback. Both
// sides must agree on a non-empty run_id; if either is empty (legacy worker,
// hand-injected test event without RunID) the filter is skipped.
//
// The callback is invoked while the read lock is still held. Releasing the
// lock before invocation would reopen the in-flight TOCTOU window: a
// concurrent sendIncidentMessage could swap the entry and fire OnSuperseded
// between the snapshot read and the callback call, which would race the
// displaced goroutine's early-return path and let stale output overwrite the
// replacement run's progress message. Holding the lock through the call
// blocks sendIncidentMessage / failCallbacksForConn until we're done — both
// are infrequent (incident-start + disconnect) and OnOutput is bounded by the
// 2-second slackAppendInterval throttle on the only Slack HTTP path.
func (h *AgentWSHandler) handleAgentOutput(msg AgentMessage) {
	if h.dispatchOnOutput(msg) {
		return
	}

	// No callback registered. If the frame carries a run_id, the run that
	// produced it has already completed (its callback was deleted) or was
	// superseded; appending late output to full_log would either re-append
	// content the new run already wrote or stamp stale text from a stale
	// run. Drop instead. The legacy fallback below only runs for frames
	// with no run_id (older workers, synthetic test events) so the API can
	// still recover data when the message has no run identity.
	if msg.RunID != "" {
		slog.Debug("dropping agent_output with no live callback",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID)
		return
	}

	if err := database.GetDB().Model(&database.Incident{}).
		Where("uuid = ?", msg.IncidentID).
		Update("full_log", gorm.Expr("COALESCE(full_log, '') || ?", msg.Output)).Error; err != nil {
		slog.Error("failed to update incident log", "err", err)
	}
}

// dispatchOnOutput delivers the frame to the registered callback under the
// read lock. Returns true when the frame was dispatched (or dropped as a
// superseded-run frame) so the caller skips the legacy DB fallback. Returns
// false when no callback is registered.
func (h *AgentWSHandler) dispatchOnOutput(msg AgentMessage) bool {
	h.callbackMu.RLock()
	defer h.callbackMu.RUnlock()
	entry, exists := h.callbacks[msg.IncidentID]
	if !exists {
		return false
	}
	if entry.runID != "" && msg.RunID != "" && entry.runID != msg.RunID {
		slog.Debug("dropping agent_output from superseded run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"current_run_id", entry.runID)
		return true
	}
	if entry.finalized {
		// Late output after OnCompleted has already fired. Drop it: the
		// waiter has captured its final response and is in finalization;
		// appending here would race the DB write or leak stale text into
		// the next run's full_log.
		slog.Debug("dropping agent_output for finalized run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID)
		return true
	}
	if entry.callback.OnOutput != nil {
		entry.callback.OnOutput(msg.Output)
	}
	return true
}

// handleAgentCompleted handles completion notification from the agent. Drops
// completion frames from a superseded run (run_id mismatch) so a late
// completion from run 1 cannot prematurely close run 2's done channel or
// delete run 2's callback.
//
// Like handleAgentOutput, the callback is invoked while the write lock is
// still held so a concurrent sendIncidentMessage cannot swap the entry and
// fire OnSuperseded between snapshot and call.
//
// The OnCompleted callback receives the raw msg.Output (no metrics line).
// Metrics are appended in the handler's finalization path AFTER the
// configurable response formatter runs, so the formatter LLM cannot strip
// or rewrite the time/tokens line and the deterministic footer derived
// from msg.TokensUsed/msg.ExecutionTimeMs always lands in `incident.response`
// and the Slack footer. The DB fallback path below (no live callback) keeps
// appending metrics directly because there is no formatter step there.
func (h *AgentWSHandler) handleAgentCompleted(msg AgentMessage) {
	slog.Info("incident completed", "incident_id", msg.IncidentID, "session_id", msg.SessionID, "tokens_used", msg.TokensUsed, "execution_time_ms", msg.ExecutionTimeMs)

	if h.dispatchOnCompleted(msg, msg.Output) {
		return
	}

	if msg.RunID != "" {
		// No live callback and the frame carries a run_id. The current run
		// already completed (its callback was deleted in the matching final
		// dispatch) so this completion is from a superseded run finishing
		// after the swap. Falling through to the DB fallback would overwrite
		// the replacement run's status / response / session_id with stale
		// values; drop instead.
		slog.Debug("dropping agent_completed with no live callback",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID)
		return
	}

	// No callback registered and no run_id (legacy worker / synthetic
	// test event): update database directly as fallback. Append metrics
	// here because this path does not run the response formatter. Skip
	// the metrics line when msg.Output is empty so this fallback path
	// matches the callback path's appendFinalizeMetrics contract — empty
	// success keeps an empty incident.response in both paths.
	var responseWithMetrics string
	if msg.Output != "" {
		executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
		responseWithMetrics = utils.AppendMetrics(msg.Output, executionTime, msg.TokensUsed)
	}
	now := time.Now()
	if err := database.GetDB().Model(&database.Incident{}).
		Where("uuid = ?", msg.IncidentID).
		Updates(map[string]interface{}{
			"status":            database.IncidentStatusCompleted,
			"session_id":        msg.SessionID,
			"response":          responseWithMetrics,
			"tokens_used":       msg.TokensUsed,
			"execution_time_ms": msg.ExecutionTimeMs,
			"completed_at":      &now,
		}).Error; err != nil {
		slog.Error("failed to update incident completion", "err", err)
	}
}

// dispatchOnCompleted delivers a completion frame to the registered callback
// and marks the entry finalized, all under a single write-lock critical
// section. Returns true when a callback was registered (whether the frame was
// delivered or dropped as a superseded-run frame); the caller then skips the
// legacy DB fallback. Returns false when no callback is registered.
//
// `output` is the raw agent output (no metrics line). The handler-level
// finalize path appends the metrics footer after the response formatter runs.
//
// The entry is intentionally retained in the map after OnCompleted: the
// waiter goroutine may run additional post-completion work (e.g. the
// configurable response formatter LLM call) before its final DB write, and
// keeping the entry around lets a concurrently-arriving Start/Continue fire
// OnSuperseded on the displaced callback. The waiter calls ReleaseRun
// before its final DB write to claim ownership atomically.
func (h *AgentWSHandler) dispatchOnCompleted(msg AgentMessage, output string) bool {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	entry, exists := h.callbacks[msg.IncidentID]
	if !exists {
		return false
	}
	if entry.runID != "" && msg.RunID != "" && entry.runID != msg.RunID {
		// Late completion from a superseded run. Don't invoke the new
		// callback's OnCompleted, don't touch the new entry.
		slog.Debug("dropping agent_completed from superseded run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"current_run_id", entry.runID)
		return true
	}
	if entry.finalized {
		// Duplicate completion frame for an already-finalized run. Ignore
		// silently; the waiter has already captured its response.
		slog.Debug("dropping duplicate agent_completed for finalized run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID)
		return true
	}
	if entry.callback.OnCompleted != nil {
		entry.callback.OnCompleted(msg.SessionID, output, msg.TokensUsed, msg.ExecutionTimeMs)
	}
	entry.finalized = true
	h.callbacks[msg.IncidentID] = entry
	return true
}

// handleAgentError handles error notification from the agent. Drops error
// frames from a superseded run (run_id mismatch) so a late error from run 1
// cannot fire OnError on run 2's callback or remove run 2's entry from the
// callbacks map.
//
// Like handleAgentCompleted, the callback is invoked while the write lock is
// still held so a concurrent sendIncidentMessage cannot swap the entry and
// fire OnSuperseded between snapshot and call.
func (h *AgentWSHandler) handleAgentError(msg AgentMessage) {
	slog.Error("incident failed", "incident_id", msg.IncidentID, "err", msg.Error)

	if h.dispatchOnError(msg) {
		return
	}

	if msg.RunID != "" {
		// No live callback and the frame carries a run_id. Drop instead of
		// overwriting incident status with a late error from a superseded
		// (or already-finalized) run — the replacement run, if any, owns
		// finalization.
		slog.Debug("dropping agent_error with no live callback",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"err", msg.Error)
		return
	}

	// No callback registered and no run_id (legacy worker / synthetic
	// test event): update database directly as fallback.
	now := time.Now()
	if err := database.GetDB().Model(&database.Incident{}).
		Where("uuid = ?", msg.IncidentID).
		Updates(map[string]interface{}{
			"status":       database.IncidentStatusFailed,
			"response":     msg.Error,
			"completed_at": &now,
		}).Error; err != nil {
		slog.Error("failed to update incident error", "err", err)
	}
}

// dispatchOnError mirrors dispatchOnCompleted for error frames: deliver the
// frame and mark the entry finalized under a single write-lock critical
// section. The entry is intentionally retained so the waiter can still call
// ReleaseRun atomically before its final DB write — without that, a normal
// agent_error would leave the waiter unable to distinguish "I just failed"
// from "I was superseded", and finalization would be skipped entirely.
// A concurrently-arriving Start/Continue can still displace this entry and
// fire OnSuperseded so the waiter exits silently, matching the OnCompleted
// path's contract.
func (h *AgentWSHandler) dispatchOnError(msg AgentMessage) bool {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	entry, exists := h.callbacks[msg.IncidentID]
	if !exists {
		return false
	}
	if entry.runID != "" && msg.RunID != "" && entry.runID != msg.RunID {
		slog.Debug("dropping agent_error from superseded run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"current_run_id", entry.runID)
		return true
	}
	if entry.finalized {
		// A late error frame after OnCompleted already finalized this run.
		// Drop it — overwriting the waiter's captured response with the
		// error text would corrupt finalization.
		slog.Debug("dropping agent_error for finalized run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"err", msg.Error)
		return true
	}
	if entry.callback.OnError != nil {
		entry.callback.OnError(msg.Error)
	}
	entry.finalized = true
	h.callbacks[msg.IncidentID] = entry
	return true
}

// IsWorkerConnected returns whether a worker is connected
func (h *AgentWSHandler) IsWorkerConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.workerReady && h.workerConn != nil
}

// SendToWorker sends a message to the agent worker
func (h *AgentWSHandler) SendToWorker(msg AgentMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.workerConn == nil {
		return ErrWorkerNotConnected
	}
	return h.workerConn.WriteMessage(websocket.TextMessage, data)
}

// StartIncident sends a new incident to the agent worker. Returns the
// generated run_id alongside any error so the caller can later identify its
// own run (e.g. via ReleaseRun) without racing concurrent registrations on
// the same incident_id.
func (h *AgentWSHandler) StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) (string, error) {
	msg := AgentMessage{
		Type:          AgentMessageTypeNewIncident,
		IncidentID:    incidentID,
		Task:          task,
		EnabledSkills: enabledSkills,
		ToolAllowlist: toolAllowlist,
	}

	// Include LLM settings if provided
	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Fetch proxy settings from database and include in message
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
		}
	}

	return h.sendIncidentMessage(incidentID, callback, msg)
}

// ContinueIncident sends a follow-up message to an existing incident. See
// StartIncident for the run_id return contract.
func (h *AgentWSHandler) ContinueIncident(incidentID, sessionID, message string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) (string, error) {
	msg := AgentMessage{
		Type:          AgentMessageTypeContinueIncident,
		IncidentID:    incidentID,
		SessionID:     sessionID,
		Message:       message,
		EnabledSkills: enabledSkills,
		ToolAllowlist: toolAllowlist,
	}

	// Include LLM settings so the worker can authenticate with the provider
	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Fetch proxy settings from database and include in message
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
		}
	}

	return h.sendIncidentMessage(incidentID, callback, msg)
}

// ReleaseRun atomically removes the callback entry for incidentID iff it is
// still owned by the supplied runID. Callers (the per-run waiter goroutine)
// invoke this immediately before their final DB write to claim ownership of
// finalization. A false return means a newer Start/Continue has displaced
// us during post-completion work — the caller MUST exit silently and let
// the replacement run own finalization, or the new run's status / response
// could be overwritten by a stale write.
func (h *AgentWSHandler) ReleaseRun(incidentID, runID string) bool {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	entry, exists := h.callbacks[incidentID]
	if !exists {
		return false
	}
	if entry.runID != runID {
		return false
	}
	delete(h.callbacks, incidentID)
	return true
}

// sendIncidentMessage atomically captures workerConn, registers the callback
// against THAT conn, and writes the message — all under h.mu. Tying the
// callback to the conn closes the disconnect-leak window: cleanupWorkerConn
// for conn A only fails A-owned callbacks, so a concurrently-registered
// B-era callback is left alone, and A-era callbacks are still failed
// promptly when A drops mid-investigation. Without this, callers blocking on
// <-done would wait forever after the worker disappears.
//
// Each call generates a fresh run_id (UUID) and stamps it on both the
// outgoing message and the registered callback entry. The worker echoes the
// run_id on every agent_output / agent_completed / agent_error frame; the
// dispatch path filters by run_id so a superseded run cannot leak late frames
// into the new waiter's callback after a second Start/Continue overrides the
// callback for the same incident_id.
//
// When the new registration displaces an existing callback for the same
// incident_id (e.g. a second Slack message lands in the same thread before
// the first run finishes), the previous callback's OnError is fired with
// ErrIncidentSuperseded so the old waiter unblocks. Subsequent agent events
// for incident_id route to the new callback only — without this signal the
// displaced goroutine would block on its done channel forever and disconnect
// cleanup could not reach it (the entry was overwritten in place).
func (h *AgentWSHandler) sendIncidentMessage(incidentID string, callback IncidentCallback, msg AgentMessage) (string, error) {
	runID := uuid.NewString()
	msg.RunID = runID

	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}

	h.mu.Lock()
	conn := h.workerConn
	if conn == nil {
		h.mu.Unlock()
		return "", ErrWorkerNotConnected
	}
	// Hold callbackMu through the write so the swap and the write succeed or
	// fail atomically with respect to other goroutines. Two races are closed
	// at once:
	//
	//   (1) Dispatcher race: if we wrote first and swapped second, an early
	//       agent_output / agent_completed / agent_error frame echoed back by
	//       the worker would be observed by the reader goroutine before the
	//       new callback is in place. The frame carries the new run_id but
	//       the map still holds the previous run's entry, so dispatch drops
	//       it as "from a superseded run" — silently losing early streamed
	//       output, or worse, the only completion / error frame.
	//
	//   (2) Failed-write rollback: if we swapped first (without holding
	//       callbackMu through the write) and the write later failed, the
	//       displaced waiter could race past ReleaseRun (observe Run 2's
	//       runID, return false, exit silently) and would already be gone by
	//       the time we tried to restore `previous`. Holding callbackMu
	//       through the write blocks ReleaseRun until we either commit (swap
	//       stays) or roll back (previous restored), so the displaced run
	//       always observes a coherent state.
	//
	// WriteMessage on a websocket.Conn does not call back into our code and
	// does not block on anything we hold, so there is no deadlock risk.
	// Incident start is infrequent — the same trade-off the dispatch path
	// already accepts when it holds callbackMu.RLock through OnOutput.
	h.callbackMu.Lock()
	previous, hadPrevious := h.callbacks[incidentID]
	h.callbacks[incidentID] = incidentCallbackEntry{callback: callback, conn: conn, runID: runID}
	if writeErr := conn.WriteMessage(websocket.TextMessage, data); writeErr != nil {
		// Roll back the swap before any other goroutine can observe Run 2's
		// entry. The displaced run continues to own its finalization.
		if hadPrevious {
			h.callbacks[incidentID] = previous
		} else {
			delete(h.callbacks, incidentID)
		}
		h.callbackMu.Unlock()
		h.mu.Unlock()
		return "", writeErr
	}
	h.callbackMu.Unlock()
	h.mu.Unlock()

	// Fire the displaced callback outside both locks. OnSuperseded is the
	// preferred signal — it tells the displaced caller to unblock and exit
	// without writing a failure to the DB or Slack, since the replacement run
	// will finalize the incident. OnError is the legacy fallback so callers
	// that have not yet adopted OnSuperseded still unblock (with the same
	// ErrIncidentSuperseded sentinel). The previous entry may already be
	// finalized (OnCompleted fired but ReleaseRun not yet called) — in that
	// case OnSuperseded sets the displaced waiter's `superseded` flag, and
	// the waiter's call to ReleaseRun returns false so it exits silently.
	if hadPrevious {
		switch {
		case previous.callback.OnSuperseded != nil:
			previous.callback.OnSuperseded()
		case previous.callback.OnError != nil:
			previous.callback.OnError(ErrIncidentSuperseded.Error())
		}
	}
	return runID, nil
}

// OneShotLLM sends a one-shot LLM request to the agent worker and waits for a response.
// Correlates request and response via a generated request_id. Returns ErrWorkerNotConnected
// when no worker is connected. If ctx has no deadline, applies oneshotLLMDefaultTimeout.
func (h *AgentWSHandler) OneShotLLM(ctx context.Context, llm *LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	if !h.IsWorkerConnected() {
		return "", ErrWorkerNotConnected
	}

	requestID := uuid.New().String()
	ch := make(chan *AgentMessage, 1)

	msg := AgentMessage{
		Type:        AgentMessageTypeOneshotLLMRequest,
		RequestID:   requestID,
		System:      system,
		User:        user,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	if llm != nil {
		msg.Provider = llm.Provider
		msg.APIKey = llm.APIKey
		msg.Model = llm.Model
		msg.ThinkingLevel = llm.ThinkingLevel
		msg.BaseURL = llm.BaseURL
	}

	// Reuse the same proxy-settings pattern as StartIncident/ContinueIncident.
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		msg.ProxyConfig = &ProxyConfig{
			URL:                    proxySettings.ProxyURL,
			NoProxy:                proxySettings.NoProxy,
			LLMEnabled:             proxySettings.LLMEnabled,
			SlackEnabled:           proxySettings.SlackEnabled,
			ZabbixEnabled:          proxySettings.ZabbixEnabled,
			VictoriaMetricsEnabled: proxySettings.VictoriaMetricsEnabled,
		}
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}

	// Atomically capture the current workerConn, register the pending entry
	// against THAT conn, and write the request — all under h.mu. Tying the
	// entry to the conn closes the reconnect race that a global pendingOneshot
	// map cannot: cleanup of conn A only signals A-owned entries, so a
	// concurrently-registered B-era entry is left alone, and A-era entries
	// are still failed promptly even after B has replaced A in workerConn.
	h.mu.Lock()
	conn := h.workerConn
	if conn == nil {
		h.mu.Unlock()
		return "", ErrWorkerNotConnected
	}
	h.pendingOneshotMu.Lock()
	h.pendingOneshot[requestID] = pendingOneshotEntry{ch: ch, conn: conn}
	h.pendingOneshotMu.Unlock()
	if writeErr := conn.WriteMessage(websocket.TextMessage, data); writeErr != nil {
		h.pendingOneshotMu.Lock()
		delete(h.pendingOneshot, requestID)
		h.pendingOneshotMu.Unlock()
		h.mu.Unlock()
		return "", writeErr
	}
	h.mu.Unlock()

	defer func() {
		h.pendingOneshotMu.Lock()
		delete(h.pendingOneshot, requestID)
		h.pendingOneshotMu.Unlock()
	}()

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, oneshotLLMDefaultTimeout)
		defer cancel()
	}

	select {
	case resp := <-ch:
		if resp.Error != "" {
			// Preserve the ErrWorkerNotConnected sentinel so callers using
			// errors.Is can distinguish a worker drop from a real provider error.
			if resp.Error == ErrWorkerNotConnected.Error() {
				return "", ErrWorkerNotConnected
			}
			return "", errors.New(resp.Error)
		}
		return resp.Summary, nil
	case <-waitCtx.Done():
		return "", waitCtx.Err()
	}
}

// CancelIncident sends a cancellation request to the worker
func (h *AgentWSHandler) CancelIncident(incidentID string) error {
	msg := AgentMessage{
		Type:       AgentMessageTypeCancelIncident,
		IncidentID: incidentID,
	}

	return h.SendToWorker(msg)
}

// BroadcastProxyConfig sends proxy configuration to the connected worker
func (h *AgentWSHandler) BroadcastProxyConfig(settings *database.ProxySettings) error {
	h.mu.RLock()
	conn := h.workerConn
	h.mu.RUnlock()

	if conn == nil {
		return ErrWorkerNotConnected
	}

	msg := AgentMessage{
		Type: AgentMessageTypeProxyConfigUpdate,
		ProxyConfig: &ProxyConfig{
			URL:                    settings.ProxyURL,
			NoProxy:                settings.NoProxy,
			LLMEnabled:             settings.LLMEnabled,
			SlackEnabled:           settings.SlackEnabled,
			ZabbixEnabled:          settings.ZabbixEnabled,
			VictoriaMetricsEnabled: settings.VictoriaMetricsEnabled,
		},
	}

	return h.SendToWorker(msg)
}

// BuildLLMSettingsForWorker is a thin re-export of the canonical implementation
// in services so handler-side callers continue to work after the type lift.
var BuildLLMSettingsForWorker = services.BuildLLMSettingsForWorker

// ErrWorkerNotConnected is re-exported from services so existing handler-side
// callers continue to compile after the lift.
var ErrWorkerNotConnected = services.ErrWorkerNotConnected

// ErrIncidentSuperseded is re-exported from services so existing handler-side
// callers (and tests) can reference the sentinel without an import.
var ErrIncidentSuperseded = services.ErrIncidentSuperseded
