package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	eventClassifyTimeout   = 15 * time.Second
	eventClassifyThreshold = 0.7
	// eventClassifyMaxTokens is the answer budget for the verdict JSON; the
	// worker adds reasoning headroom on top for reasoning models.
	eventClassifyMaxTokens = 200

	// eventReuseWindow bounds how far back a re-send of the same item copies
	// the class of an LLM-classified earlier event instead of making a new
	// call. Alertmanager re-sends a firing group every repeat_interval.
	eventReuseWindow = 24 * time.Hour

	eventPromptFieldMax = 1500
	eventPromptLabelMax = 30
)

// EventVerdict is the classification of one inbound event.
type EventVerdict struct {
	Kind           string
	Classification string
	Confidence     *float64
	Reasoning      string
}

// FallbackEventVerdict is the verdict used when classification cannot run,
// cannot be trusted, or is turned off.
func FallbackEventVerdict(kind, reason string) EventVerdict {
	return EventVerdict{Kind: kind, Classification: database.EventClassificationFallback, Reasoning: reason}
}

// SourceEventVerdict is the verdict for an event whose source alone decides
// its class (no LLM call).
func SourceEventVerdict(kind string) EventVerdict {
	return EventVerdict{Kind: kind, Classification: database.EventClassificationSource}
}

// ResolveEventVerdict turns a raw LLM answer into a verdict. An unknown class
// or a non-fallback class below the confidence threshold becomes the fallback
// class: when the model is not sure, the safe action wins.
func ResolveEventVerdict(kind string, confidence float64, reasoning string, allowed []string, fallback string) EventVerdict {
	kind = strings.ToLower(strings.TrimSpace(kind))
	reasoning = strings.TrimSpace(reasoning)
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	if !slices.Contains(allowed, kind) {
		return FallbackEventVerdict(fallback, fmt.Sprintf("model returned unknown class %q", kind))
	}
	if kind != fallback && confidence < eventClassifyThreshold {
		v := FallbackEventVerdict(fallback,
			fmt.Sprintf("model said %s with low confidence %.2f: %s", kind, confidence, reasoning))
		v.Confidence = &confidence
		return v
	}
	return EventVerdict{
		Kind:           kind,
		Classification: database.EventClassificationLLM,
		Confidence:     &confidence,
		Reasoning:      reasoning,
	}
}

// EventInput describes one inbound item before it is stored.
type EventInput struct {
	SourceKind string
	SourceUUID string
	SourceType string
	ExternalID string

	Title       string
	Summary     string
	Description string
	Host        string
	Service     string
	Severity    string
	Status      string
	Labels      map[string]string
	RawPayload  map[string]interface{}
	OccurredAt  *time.Time

	// IncidentUUID links the event to the incident it continues, and
	// ScopeUUID to the request work it continues, when that is known at
	// record time. BeginRun links a request event that starts new work.
	IncidentUUID string
	ScopeUUID    string
}

// DefaultEventClassificationRules is the operator-editable part of the event
// classification prompt. The class list and JSON contract are appended by
// code, so an edit cannot break verdict parsing.
const DefaultEventClassificationRules = `You classify one inbound event received by Akmatori, an AI SRE agent.

Classes:
- alert: reports a problem that may need investigation: a failure, errors, degraded performance, a threshold breach, a host or service down, a security finding. A monitoring item in firing state is an alert unless a rule below says otherwise.
- change: reports a change made to the system: a deploy, release, rollout, rollback, config change, feature flag, scaling action, planned restart, maintenance start or end.
- info: needs no action and is not a change: a test message, a heartbeat or watchdog signal, a report, an acknowledgement, a status update on something already known, chat.
- request: a message addressed to the agent that asks it to do something or to answer a question, and does not report a problem.

Rules:
- A message that reports a problem AND asks the agent to look at it is an alert.
- When you are not sure, use the lowest confidence that is true. A low-confidence answer falls back to the safe class.`

// eventClassificationContract is the fixed tail of the classification prompt.
func eventClassificationContract(allowed []string) string {
	return fmt.Sprintf(`Allowed classes for this event: %s. Use only one of them.

Return STRICT JSON:
  {"kind": "<class>", "confidence": <0..1>, "reasoning": "<≤200 char explanation>"}

Output JSON only. No code fences.`, strings.Join(allowed, ", "))
}

// EventClassificationSystemPrompt joins the effective rules with the contract.
func EventClassificationSystemPrompt(rules string, allowed []string) string {
	if strings.TrimSpace(rules) == "" {
		rules = DefaultEventClassificationRules
	}
	return strings.TrimSpace(rules) + "\n\n" + eventClassificationContract(allowed)
}

// EventClassificationExtractionAddendum is appended to the alert-extraction
// prompt of free-text sources (Slack listener messages) so ONE LLM call both
// extracts the alert fields and classifies the message.
func EventClassificationExtractionAddendum(rules string, allowed []string) string {
	if strings.TrimSpace(rules) == "" {
		rules = DefaultEventClassificationRules
	}
	return fmt.Sprintf(`

Also classify the message. %s

Add these fields to the same JSON object:
- event_kind: one of %s
- event_confidence: number from 0 to 1
- event_reasoning: ≤200 char explanation of the class`, strings.TrimSpace(rules), strings.Join(allowed, ", "))
}

// EventService stores inbound events and classifies the ones whose class the
// source does not decide.
type EventService struct {
	db             *gorm.DB
	caller         OneShotLLMCaller
	getLLMSettings func() (*database.LLMSettings, error)
	now            func() time.Time
}

// NewEventService constructs an EventService. A nil caller makes every
// classification fall back.
func NewEventService(db *gorm.DB, caller OneShotLLMCaller) *EventService {
	return &EventService{db: db, caller: caller, getLLMSettings: database.GetLLMSettings, now: time.Now}
}

// SetCaller wires the one-shot LLM caller after construction (the worker
// handler is created after the services).
func (s *EventService) SetCaller(caller OneShotLLMCaller) {
	s.caller = caller
}

// RecordClassified stores a monitoring item (webhook alert item) after
// classifying it. Every item gets its own row. A re-send of an item that the
// LLM classified inside eventReuseWindow copies that class with no LLM call
// (classification=reused). Resolved items are class alert by source: they
// only ever close an existing alert.
//
// Never fails the caller: a storage error is logged and the returned event
// still carries the verdict, so the alert path keeps working without the
// events table.
func (s *EventService) RecordClassified(ctx context.Context, in EventInput) *database.Event {
	ev := s.newEvent(in)

	// Reuse only while classification is on: turning it off must send every
	// item down the alert path at once, not after the 24 h reuse window of
	// an earlier change/info verdict. When off, Classify gives the fallback.
	if LoadEventClassificationConfig().Enabled {
		if prev := s.findClassified(ctx, ev); prev != nil {
			applyVerdict(ev, EventVerdict{
				Kind:           prev.Kind,
				Classification: database.EventClassificationReused,
				Confidence:     prev.ClassificationConfidence,
				Reasoning:      fmt.Sprintf("same item as event %s: %s", prev.UUID, prev.ClassificationReasoning),
			})
			s.insert(ctx, ev)
			return ev
		}
	}

	var v EventVerdict
	if strings.EqualFold(in.Status, string(database.AlertStatusResolved)) {
		v = SourceEventVerdict(database.EventKindAlert)
	} else {
		v = s.Classify(ctx, in, []string{database.EventKindAlert, database.EventKindChange, database.EventKindInfo}, database.EventKindAlert)
	}
	applyVerdict(ev, v)
	s.insert(ctx, ev)
	return ev
}

// Record stores an event whose verdict is already known (source-decided, or
// classified together with extraction).
func (s *EventService) Record(ctx context.Context, in EventInput, v EventVerdict) *database.Event {
	ev := s.newEvent(in)
	applyVerdict(ev, v)
	s.insert(ctx, ev)
	return ev
}

// LinkAlert stamps the alerts row and incident an event created or joined.
// Best-effort.
func (s *EventService) LinkAlert(ctx context.Context, eventUUID, alertUUID, incidentUUID string) {
	if s == nil || eventUUID == "" {
		return
	}
	updates := map[string]interface{}{}
	if alertUUID != "" {
		updates["alert_uuid"] = alertUUID
	}
	if incidentUUID != "" {
		updates["incident_uuid"] = incidentUUID
	}
	if len(updates) == 0 {
		return
	}
	if err := s.db.WithContext(ctx).Model(&database.Event{}).Where("uuid = ?", eventUUID).Updates(updates).Error; err != nil {
		slog.Warn("events: link alert failed", "event_uuid", eventUUID, "err", err)
	}
}

// LinkIncident stamps the incident (or agent-run row) an event started or
// continued. Best-effort.
func (s *EventService) LinkIncident(ctx context.Context, eventUUID, incidentUUID string) {
	s.LinkAlert(ctx, eventUUID, "", incidentUUID)
}

// Classify runs one LLM call. It never returns an error: every failure is the
// fallback class with the cause in Reasoning.
func (s *EventService) Classify(ctx context.Context, in EventInput, allowed []string, fallback string) EventVerdict {
	cfg := LoadEventClassificationConfig()
	if !cfg.Enabled {
		return FallbackEventVerdict(fallback, eventClassificationOffReason)
	}
	if s.caller == nil {
		return FallbackEventVerdict(fallback, "no LLM caller")
	}
	settings, err := s.getLLMSettings()
	if err != nil || settings == nil || settings.APIKey == "" {
		return FallbackEventVerdict(fallback, "LLM not configured")
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return FallbackEventVerdict(fallback, "LLM settings inactive")
	}

	callCtx, cancel := context.WithTimeout(ctx, eventClassifyTimeout)
	defer cancel()
	raw, err := s.caller.OneShotLLM(callCtx, worker,
		EventClassificationSystemPrompt(cfg.Rules, allowed),
		buildEventClassificationUserPrompt(in), eventClassifyMaxTokens, 0.0)
	if err != nil {
		if errors.Is(err, ErrWorkerNotConnected) {
			return FallbackEventVerdict(fallback, "worker not connected")
		}
		slog.Warn("event classifier: llm call failed", "err", err)
		return FallbackEventVerdict(fallback, "classification call failed")
	}

	var parsed struct {
		Kind       string  `json:"kind"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(stripCodeFence(raw)), &parsed); err != nil {
		slog.Warn("event classifier: invalid response", "err", err, "raw", truncateForPrompt(raw, 500))
		return FallbackEventVerdict(fallback, "unparseable classification answer")
	}
	return ResolveEventVerdict(parsed.Kind, parsed.Confidence, parsed.Reasoning, allowed, fallback)
}

// EventClassificationConfig is the live classification configuration.
type EventClassificationConfig struct {
	Enabled bool
	// Rules is the operator's rules text; empty means the default.
	Rules string
}

// LoadEventClassificationConfig reads the gate and rules from
// GeneralSettings on every call, so a Settings change applies at once. When
// the settings cannot be read, classification stays on with the default rules.
func LoadEventClassificationConfig() EventClassificationConfig {
	gs, err := database.GetOrCreateGeneralSettings()
	if err != nil || gs == nil {
		return EventClassificationConfig{Enabled: true}
	}
	cfg := EventClassificationConfig{Enabled: gs.GetEventClassificationEnabled()}
	if gs.EventClassificationPrompt != nil {
		cfg.Rules = *gs.EventClassificationPrompt
	}
	return cfg
}

// eventClassificationOffReason is the reasoning stored when the gate is off.
const eventClassificationOffReason = "event classification is turned off in Settings"

func stripCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func buildEventClassificationUserPrompt(in EventInput) string {
	var b strings.Builder
	line := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, sanitizeForPrompt(truncateForPrompt(value, eventPromptFieldMax)))
		}
	}
	line("Source", in.SourceType)
	line("Title", in.Title)
	line("Status", in.Status)
	line("Severity", in.Severity)
	line("Host", in.Host)
	line("Service", in.Service)
	line("Summary", in.Summary)
	if in.Description != in.Summary {
		line("Description", in.Description)
	}
	if len(in.Labels) > 0 {
		keys := make([]string, 0, len(in.Labels))
		for k := range in.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > eventPromptLabelMax {
			keys = keys[:eventPromptLabelMax]
		}
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			if in.Labels[k] != "" {
				pairs = append(pairs, k+"="+in.Labels[k])
			}
		}
		line("Labels", strings.Join(pairs, ", "))
	}
	return "Event:\n" + b.String()
}

func (s *EventService) newEvent(in EventInput) *database.Event {
	now := s.now()
	occurred := now
	if in.OccurredAt != nil && !in.OccurredAt.IsZero() {
		occurred = *in.OccurredAt
	}
	var labels database.JSONB
	if len(in.Labels) > 0 {
		labels = database.JSONB{}
		for k, v := range in.Labels {
			labels[k] = v
		}
	}
	var raw database.JSONB
	if len(in.RawPayload) > 0 {
		raw = database.JSONB(in.RawPayload)
	}
	summary := in.Summary
	if summary == "" {
		summary = in.Description
	}
	return &database.Event{
		UUID:         uuid.New().String(),
		SourceKind:   in.SourceKind,
		SourceUUID:   in.SourceUUID,
		SourceType:   in.SourceType,
		ExternalID:   firstRunes(in.ExternalID, 255),
		Fingerprint:  ComputeAlertFingerprint(in.SourceUUID, in.Title, in.Host),
		ReceivedAt:   now,
		OccurredAt:   occurred,
		Title:        firstRunes(in.Title, 255),
		Summary:      summary,
		Host:         firstRunes(in.Host, 255),
		Service:      firstRunes(in.Service, 255),
		Severity:     firstRunes(in.Severity, 16),
		Status:       firstRunes(in.Status, 16),
		Labels:       labels,
		RawPayload:   raw,
		IncidentUUID: in.IncidentUUID,
		ScopeUUID:    in.ScopeUUID,
	}
}

func applyVerdict(ev *database.Event, v EventVerdict) {
	ev.Kind = v.Kind
	ev.Classification = v.Classification
	ev.ClassificationConfidence = v.Confidence
	ev.ClassificationReasoning = v.Reasoning
}

// findClassified returns the newest earlier event for a re-send of the same
// item: same source, same identity (external id when the source has one, else
// the title+host fingerprint) and same status, received inside
// eventReuseWindow, whose class came from the LLM or the source. Reused rows
// are not a source of reuse, so the class is re-checked at least once per
// window; fallback rows are not either, so the next re-send retries the LLM.
func (s *EventService) findClassified(ctx context.Context, ev *database.Event) *database.Event {
	if ev.SourceUUID == "" {
		return nil
	}
	q := s.db.WithContext(ctx).
		Where("source_uuid = ? AND status = ? AND received_at >= ?", ev.SourceUUID, ev.Status, ev.ReceivedAt.Add(-eventReuseWindow)).
		Where("classification IN ?", []string{database.EventClassificationLLM, database.EventClassificationSource})
	if ev.ExternalID != "" {
		q = q.Where("external_id = ?", ev.ExternalID)
	} else {
		q = q.Where("fingerprint = ?", ev.Fingerprint)
	}
	var prev database.Event
	if err := q.Order("received_at DESC").Limit(1).Find(&prev).Error; err != nil {
		slog.Warn("events: reuse lookup failed", "err", err)
		return nil
	}
	if prev.UUID == "" {
		return nil
	}
	return &prev
}

func (s *EventService) insert(ctx context.Context, ev *database.Event) {
	if err := s.db.WithContext(ctx).Create(ev).Error; err != nil {
		slog.Warn("events: insert failed", "source_kind", ev.SourceKind, "kind", ev.Kind, "err", err)
	}
}

// ClassificationOffVerdict is the verdict for every event while the gate is
// off: the fallback class, with no LLM call.
func ClassificationOffVerdict(fallback string) EventVerdict {
	return FallbackEventVerdict(fallback, eventClassificationOffReason)
}

// EventsToolType is the gateway tool type of the built-in events tool.
const EventsToolType = "events"

// withEventsTool adds the built-in events tool to an agent run's allowlist.
// It is credential-less and every run may use it: investigations search
// recent changes, cron runs report problems. A type-only entry is a wildcard
// for credential-less namespaces in the gateway authorizer.
func withEventsTool(entries []ToolAllowlistEntry) []ToolAllowlistEntry {
	for _, e := range entries {
		if e.ToolType == EventsToolType {
			return entries
		}
	}
	return append(entries, ToolAllowlistEntry{ToolType: EventsToolType})
}
