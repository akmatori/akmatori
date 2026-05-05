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
type incidentCallbackEntry struct {
	callback IncidentCallback
	conn     *websocket.Conn
	runID    string
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

// IncidentCallback is called when an incident receives updates.
//
// OnSuperseded fires when a newer StartIncident/ContinueIncident displaces
// this callback for the same incident_id (e.g. a second Slack message lands
// in the same thread before the first run finishes). The displaced run has
// been handed off to the new callback — the new run will finalize the
// incident in the DB and Slack — so the old goroutine should unblock and
// exit silently rather than commit a failure that races the replacement's
// success. When OnSuperseded is nil, sendIncidentMessage falls back to
// firing OnError with ErrIncidentSuperseded so legacy callers still unblock.
type IncidentCallback struct {
	OnOutput     func(output string)
	OnCompleted  func(sessionID, response string, tokensUsed int, executionTimeMs int64)
	OnError      func(errorMsg string)
	OnSuperseded func()
}

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
// registered against the given conn, then removes the entry from the map.
// Callbacks owned by other (replacement) conns are left untouched. OnError
// implementations in this codebase only close a sync.Once-guarded done
// channel, so they're non-blocking; we still call them outside callbackMu
// to avoid forcing future callback bodies into a locked critical section.
func (h *AgentWSHandler) failCallbacksForConn(conn *websocket.Conn, errMsg string) {
	h.callbackMu.Lock()
	var failed []IncidentCallback
	for incidentID, entry := range h.callbacks {
		if entry.conn == conn {
			failed = append(failed, entry.callback)
			delete(h.callbacks, incidentID)
		}
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
func (h *AgentWSHandler) handleAgentCompleted(msg AgentMessage) {
	slog.Info("incident completed", "incident_id", msg.IncidentID, "session_id", msg.SessionID, "tokens_used", msg.TokensUsed, "execution_time_ms", msg.ExecutionTimeMs)

	// Append metrics to response (for display in reasoning log and Slack)
	executionTime := time.Duration(msg.ExecutionTimeMs) * time.Millisecond
	responseWithMetrics := utils.AppendMetrics(msg.Output, executionTime, msg.TokensUsed)

	if h.dispatchOnCompleted(msg, responseWithMetrics) {
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
	// test event): update database directly as fallback.
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
// and removes the entry from the map, all under a single write-lock critical
// section. Returns true when a callback was registered (whether the frame was
// delivered or dropped as a superseded-run frame); the caller then skips the
// legacy DB fallback. Returns false when no callback is registered.
func (h *AgentWSHandler) dispatchOnCompleted(msg AgentMessage, responseWithMetrics string) bool {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	entry, exists := h.callbacks[msg.IncidentID]
	if !exists {
		return false
	}
	if entry.runID != "" && msg.RunID != "" && entry.runID != msg.RunID {
		// Late completion from a superseded run. Don't invoke the new
		// callback's OnCompleted, don't remove the new entry from the map.
		slog.Debug("dropping agent_completed from superseded run",
			"incident_id", msg.IncidentID,
			"msg_run_id", msg.RunID,
			"current_run_id", entry.runID)
		return true
	}
	if entry.callback.OnCompleted != nil {
		entry.callback.OnCompleted(msg.SessionID, responseWithMetrics, msg.TokensUsed, msg.ExecutionTimeMs)
	}
	delete(h.callbacks, msg.IncidentID)
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
// frame and remove the entry from the map under a single write-lock critical
// section.
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
	if entry.callback.OnError != nil {
		entry.callback.OnError(msg.Error)
	}
	delete(h.callbacks, msg.IncidentID)
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

// StartIncident sends a new incident to the agent worker
func (h *AgentWSHandler) StartIncident(incidentID, task string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) error {
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

// ContinueIncident sends a follow-up message to an existing incident
func (h *AgentWSHandler) ContinueIncident(incidentID, sessionID, message string, llm *LLMSettingsForWorker, enabledSkills []string, toolAllowlist []services.ToolAllowlistEntry, callback IncidentCallback) error {
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
func (h *AgentWSHandler) sendIncidentMessage(incidentID string, callback IncidentCallback, msg AgentMessage) error {
	runID := uuid.NewString()
	msg.RunID = runID

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	h.mu.Lock()
	conn := h.workerConn
	if conn == nil {
		h.mu.Unlock()
		return ErrWorkerNotConnected
	}
	h.callbackMu.Lock()
	previous, hadPrevious := h.callbacks[incidentID]
	h.callbacks[incidentID] = incidentCallbackEntry{callback: callback, conn: conn, runID: runID}
	h.callbackMu.Unlock()
	if writeErr := conn.WriteMessage(websocket.TextMessage, data); writeErr != nil {
		h.callbackMu.Lock()
		// Restore the displaced entry so its waiter can still be reached by
		// later agent events or disconnect cleanup. Restoring is only correct
		// if no concurrent registration has overwritten our slot in the
		// meantime — in that race, the newest entry wins and we must not
		// clobber it.
		if cur, ok := h.callbacks[incidentID]; ok && cur.conn == conn && cur.runID == runID {
			if hadPrevious {
				h.callbacks[incidentID] = previous
			} else {
				delete(h.callbacks, incidentID)
			}
		}
		h.callbackMu.Unlock()
		h.mu.Unlock()
		return writeErr
	}
	h.mu.Unlock()

	// Fire the displaced callback outside both locks. OnSuperseded is the
	// preferred signal — it tells the displaced caller to unblock and exit
	// without writing a failure to the DB or Slack, since the replacement run
	// will finalize the incident. OnError is the legacy fallback so callers
	// that have not yet adopted OnSuperseded still unblock (with the same
	// ErrIncidentSuperseded sentinel).
	if hadPrevious {
		switch {
		case previous.callback.OnSuperseded != nil:
			previous.callback.OnSuperseded()
		case previous.callback.OnError != nil:
			previous.callback.OnError(ErrIncidentSuperseded.Error())
		}
	}
	return nil
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
