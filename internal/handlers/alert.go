package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"golang.org/x/sync/singleflight"
)

// slackAppendInterval is the minimum time between chat.update calls on the
// progress message. The streamer only ever holds the latest reasoning line,
// so this gates how often Slack sees that single line replaced.
const slackAppendInterval = 2 * time.Second

// slackMaxTextBytes is the maximum byte size for Slack message text.
// chat.postMessage accepts up to ~40,000 chars; we keep the cap tight at
// 8000 so summaries stay readable and so the SlackSummarizer has a clear
// budget to compress towards.
const slackMaxTextBytes = 8000

// slackSummaryMargin is the byte budget reserved for the trailing footer
// (metrics line + "View reasoning log" link). The summarizer is asked to keep
// its output under (slackMaxTextBytes - slackSummaryMargin) so the footer
// always fits without being clipped.
const slackSummaryMargin = 200

// AlertHandler handles webhook requests from multiple alert sources
type AlertHandler struct {
	config            *config.Config
	slackManager      *slackutil.Manager
	agentExecutor     *executor.Executor
	agentWSHandler    *AgentWSHandler
	skillService      services.SkillIncidentManager
	alertService      services.AlertManager
	channelResolver   *slackutil.ChannelResolver
	slackSummarizer   *services.SlackSummarizer
	responseFormatter *services.ResponseFormatter
	channelService    services.ChannelManager
	providerRegistry  services.ProviderRegistry
	alertCorrelator   *services.AlertCorrelator
	alertSuppressor   *services.AlertSuppressor
	oneShotCaller     services.OneShotLLMCaller

	// spawnGroup deduplicates concurrent alerts with the same
	// (sourceUUID, alertName, targetHost) key so only one incident is created.
	spawnGroup singleflight.Group

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

// SetResponseFormatter wires the ResponseFormatter used to apply the
// configured global formatting prompt to the agent's final response before
// it is persisted and posted to Slack. Optional — when unset (or formatting
// is disabled), the raw agent response flows through unchanged.
func (h *AlertHandler) SetResponseFormatter(f *services.ResponseFormatter) {
	h.responseFormatter = f
}

// SetChannelService wires the ChannelManager used to resolve outbound channels
// from alert source instances. When unset, outbound Slack posting is skipped.
func (h *AlertHandler) SetChannelService(c services.ChannelManager) {
	h.channelService = c
}

// SetProviderRegistry wires the ProviderRegistry used to route outbound posts
// to the channel's provider. When unset (or no provider is registered for the
// channel's provider), the handler falls back to the existing slack client.
func (h *AlertHandler) SetProviderRegistry(r services.ProviderRegistry) {
	h.providerRegistry = r
}

// SetAlertCorrelator wires the AlertCorrelator used to decide whether an
// incoming alert should be attached to a recent incident instead of spawning
// a new investigation. Optional — when nil the handler spawns unconditionally.
func (h *AlertHandler) SetAlertCorrelator(c *services.AlertCorrelator) {
	h.alertCorrelator = c
}

// SetAlertSuppressor wires the AlertSuppressor used to decide whether an
// incoming alert matches a known false-positive signature and should be
// suppressed without spawning a full investigation. Optional — when nil the
// handler never suppresses (fail-open).
func (h *AlertHandler) SetAlertSuppressor(s *services.AlertSuppressor) {
	h.alertSuppressor = s
}

// SetOneShotCaller wires the OneShotLLMCaller used by runRecurrenceUpdate to
// generate a 2-sentence delta note for long-window recurrences. Optional —
// when nil, runRecurrenceUpdate falls through to a full spawn.
func (h *AlertHandler) SetOneShotCaller(c services.OneShotLLMCaller) {
	h.oneShotCaller = c
}

// correlate delegates to the wired AlertCorrelator when present; otherwise
// returns a no-match verdict (fail-open).
func (h *AlertHandler) correlate(ctx context.Context, sourceUUID string, alert alerts.NormalizedAlert) (services.CorrelationVerdict, error) {
	if h.alertCorrelator == nil {
		return services.CorrelationVerdict{}, nil
	}
	return h.alertCorrelator.Correlate(ctx, sourceUUID, alert)
}

// correlationThreshold returns the configured correlation confidence threshold,
// or the hardcoded default (0.7) when no correlator is wired.
func (h *AlertHandler) correlationThreshold() float64 {
	if h.alertCorrelator != nil {
		return h.alertCorrelator.Threshold()
	}
	return 0.7
}

// suppress delegates to the wired AlertSuppressor when present; otherwise
// returns a no-match verdict (fail-open).
func (h *AlertHandler) suppress(ctx context.Context, alert alerts.NormalizedAlert) (services.SuppressionVerdict, error) {
	if h.alertSuppressor == nil {
		return services.SuppressionVerdict{}, nil
	}
	return h.alertSuppressor.Evaluate(ctx, alert)
}

// suppressionThreshold returns the configured suppression confidence threshold,
// or the SuppressionConfigWithDefaults default (0.7) when no suppressor is wired.
func (h *AlertHandler) suppressionThreshold() float64 {
	if h.alertSuppressor != nil {
		return h.alertSuppressor.Threshold()
	}
	return 0.7
}

// recordRecurrence calls LinkAlertToIncident and logs but does not propagate
// errors — a failed recurrence write must never block alert processing.
func (h *AlertHandler) recordRecurrence(ctx context.Context, sourceUUID string, incidentUUID string, alert alerts.NormalizedAlert, verdict services.CorrelationVerdict) {
	if h.skillService == nil {
		return
	}
	if err := h.skillService.LinkAlertToIncident(ctx, incidentUUID, sourceUUID, alert); err != nil {
		slog.Warn("failed to link alert to incident", "incident_uuid", incidentUUID, "err", err)
	}
}

// runRecurrenceUpdate handles a long-window correlation match: instead of
// spawning a full re-investigation, it generates a short delta note via one-shot
// LLM, appends it to the incident via AppendCorrelatedAlert, and posts a brief
// Slack thread reply.
//
// Returns nil when the recurrence was handled successfully (caller should NOT
// spawn a new incident). Returns an error when the cheap path fails — the
// caller should then fall through to a full spawn.
//
// Guards: no caller → error; empty fingerprint → error; LLM error → error.
func (h *AlertHandler) runRecurrenceUpdate(ctx context.Context, sourceUUID, incidentUUID, alertFingerprint string, alert alerts.NormalizedAlert, verdict services.CorrelationVerdict) error {
	if h.oneShotCaller == nil {
		return fmt.Errorf("no one-shot caller configured for recurrence update")
	}
	if alertFingerprint == "" {
		return fmt.Errorf("empty alert fingerprint: cannot use long-window recurrence path")
	}
	if h.skillService == nil {
		return fmt.Errorf("no skill service for recurrence update")
	}

	incident, err := h.skillService.GetIncident(incidentUUID)
	if err != nil {
		return fmt.Errorf("load incident: %w", err)
	}

	settings, err := database.GetLLMSettings()
	if err != nil || settings == nil || settings.APIKey == "" {
		return fmt.Errorf("LLM settings not configured for recurrence update")
	}
	worker := services.BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return fmt.Errorf("could not build LLM worker settings")
	}

	const recurrenceSystemPrompt = `You are an incident timeline writer. Given a recurring alert for a known open incident, write exactly 2 sentences describing what this recurrence means. Be factual and concise. Output plain text only, no JSON.`

	userPrompt := fmt.Sprintf(
		"Incident: %s\nStatus: %s\nAlert: %s (host: %s)\nPrevious correlation reasoning: %s\n\nWrite 2 sentences as a delta update for the incident timeline.",
		sanitizeRecurrenceField(incident.Title),
		sanitizeRecurrenceField(string(incident.Status)),
		sanitizeRecurrenceField(alert.AlertName),
		sanitizeRecurrenceField(alert.TargetHost),
		sanitizeRecurrenceField(verdict.Reasoning),
	)

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	deltaNote, llmErr := h.oneShotCaller.OneShotLLM(callCtx, worker, recurrenceSystemPrompt, userPrompt, 150, 0.3)
	if llmErr != nil {
		return fmt.Errorf("llm delta call failed: %w", llmErr)
	}
	deltaNote = strings.TrimSpace(deltaNote)

	reasoning := verdict.Reasoning
	if deltaNote != "" {
		reasoning = deltaNote
	}

	if err := h.skillService.LinkAlertToIncident(ctx, incidentUUID, sourceUUID, alert); err != nil {
		return fmt.Errorf("link alert to incident: %w", err)
	}

	// Post a short Slack thread reply to the incident's source thread, if known.
	if incident.SlackChannelID != "" && incident.SlackMessageTS != "" {
		msg := fmt.Sprintf("Recurring alert: %s", truncateForSlack(reasoning, 300))
		h.postSlackThreadReply(incident.SlackChannelID, incident.SlackMessageTS, msg)
	}

	slog.Info("long-window recurrence update applied", "incident_uuid", incidentUUID)
	return nil
}

// sanitizeRecurrenceField strips newlines from a field so it cannot inject
// additional prompt lines into the recurrence delta prompt.
func sanitizeRecurrenceField(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(strings.TrimSpace(s))
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
