package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
)

// slackProgressInterval is the minimum time between Slack progress message updates
// to avoid hitting Slack API rate limits during live investigation streaming.
const slackProgressInterval = 5 * time.Second

// slackAppendInterval is the minimum time between chat.appendStream calls.
// AppendStream is cheaper than chat.update (Slack treats it as a streaming
// fragment rather than a full re-render), so it can be ticked faster while
// still respecting Slack's per-method rate limits.
const slackAppendInterval = 2 * time.Second

// slackMaxTextBytes is the maximum byte size for Slack message text.
// Slack's chat.update accepts up to 12,000 bytes for plain mrkdwn messages
// (40,000 for blocks), but we keep the cap at 8000 to leave generous headroom
// for multi-byte characters and the trailing footer, and because the
// summarizer already produces a tight result well under 8000 bytes.
const slackMaxTextBytes = 8000

// slackSummaryMargin is the byte budget reserved for the trailing footer
// (metrics line + "View reasoning log" link). The summarizer is asked to keep
// its output under (slackMaxTextBytes - slackSummaryMargin) so the footer
// always fits without being clipped.
const slackSummaryMargin = 200

// AlertHandler handles webhook requests from multiple alert sources
type AlertHandler struct {
	config          *config.Config
	slackManager    *slackutil.Manager
	agentExecutor   *executor.Executor
	agentWSHandler  *AgentWSHandler
	skillService    services.SkillIncidentManager
	alertService    services.AlertManager
	channelResolver *slackutil.ChannelResolver
	slackSummarizer *services.SlackSummarizer

	// Workspace team ID (required for Streaming API)
	teamID string

	// Registered adapters by source type
	adaptersMu sync.RWMutex
	adapters   map[string]alerts.AlertAdapter
}

// NewAlertHandler creates a new alert handler
func NewAlertHandler(
	cfg *config.Config,
	slackManager *slackutil.Manager,
	agentExecutor *executor.Executor,
	agentWSHandler *AgentWSHandler,
	skillService services.SkillIncidentManager,
	alertService services.AlertManager,
	channelResolver *slackutil.ChannelResolver,
) *AlertHandler {
	h := &AlertHandler{
		config:          cfg,
		slackManager:    slackManager,
		agentExecutor:   agentExecutor,
		agentWSHandler:  agentWSHandler,
		skillService:    skillService,
		alertService:    alertService,
		channelResolver: channelResolver,
		adapters:        make(map[string]alerts.AlertAdapter),
	}

	return h
}

// SetTeamID sets the workspace team ID (used by the Streaming API).
func (h *AlertHandler) SetTeamID(teamID string) {
	h.teamID = teamID
}

// SetSlackSummarizer wires the SlackSummarizer used for compressing final
// Slack messages. Optional — when unset, the handler falls back to the
// existing byte-truncation path.
func (h *AlertHandler) SetSlackSummarizer(s *services.SlackSummarizer) {
	h.slackSummarizer = s
}

// RegisterAdapter registers an alert adapter for a source type
func (h *AlertHandler) RegisterAdapter(adapter alerts.AlertAdapter) {
	h.adaptersMu.Lock()
	h.adapters[adapter.GetSourceType()] = adapter
	h.adaptersMu.Unlock()
	slog.Info("registered alert adapter", "source_type", adapter.GetSourceType())
}

// HandleWebhook processes incoming webhook requests
// Route: /webhook/alert/{instance_uuid}
func (h *AlertHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract instance UUID from path
	path := strings.TrimPrefix(r.URL.Path, "/webhook/alert/")
	instanceUUID := strings.TrimSuffix(path, "/")

	if instanceUUID == "" {
		http.Error(w, "Missing instance UUID", http.StatusBadRequest)
		return
	}

	// Look up instance
	instance, err := h.alertService.GetInstanceByUUID(instanceUUID)
	if err != nil {
		slog.Error("alert instance not found", "instance_uuid", instanceUUID, "err", err)
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	if !instance.Enabled {
		slog.Warn("alert instance disabled", "instance_uuid", instanceUUID)
		http.Error(w, "Instance disabled", http.StatusForbidden)
		return
	}

	// Get adapter for source type
	h.adaptersMu.RLock()
	adapter, ok := h.adapters[instance.AlertSourceType.Name]
	h.adaptersMu.RUnlock()
	if !ok {
		slog.Error("no adapter for source type", "source_type", instance.AlertSourceType.Name)
		http.Error(w, "Unsupported source type", http.StatusBadRequest)
		return
	}

	// Validate webhook secret
	if err := adapter.ValidateWebhookSecret(r, instance); err != nil {
		slog.Warn("webhook secret validation failed", "instance_uuid", instanceUUID, "err", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Read request body (limit to 10 MB to prevent DoS)
	const maxWebhookBodySize = 10 * 1024 * 1024
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		slog.Error("failed to read webhook body", "err", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse payload into normalized alerts
	normalizedAlerts, err := adapter.ParsePayload(body, instance)
	if err != nil {
		slog.Error("failed to parse alert payload", "err", err)
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	slog.Info("received alerts", "count", len(normalizedAlerts), "source_type", instance.AlertSourceType.Name, "instance", instance.Name)

	// Process each alert
	for _, normalizedAlert := range normalizedAlerts {
		go h.processAlert(instance, normalizedAlert)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Received %d alerts", len(normalizedAlerts))
}
