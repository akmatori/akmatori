package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	// maxRawPayload caps the raw payload returned by Get, in bytes of JSON.
	maxRawPayload = 20_000
	// maxReportsPerRun bounds report_alert calls in one agent run, so a
	// looping cron cannot flood the correlation gate.
	maxReportsPerRun = 20
	maxTitleRunes    = 255
	maxSummaryRunes  = 4000
	// maxRunLog caps the run log Get returns for a request event, in bytes.
	maxRunLog = 50_000
)

// Values mirrored from the main API (internal/database/models_events.go).
const (
	kindAlert            = "alert"
	sourceCron           = "cron"
	classificationSource = "source"
	statusFiring         = "firing"
	runKindCron          = "cron"
)

var validKinds = map[string]bool{"alert": true, "change": true, "info": true, "request": true}

var validSeverities = map[string]bool{"critical": true, "high": true, "warning": true, "info": true}

// EventsTool gives agents read access to Akmatori's events (every inbound
// item with its class) and lets cron runs report a problem as an alert event.
// It queries the gateway's own DB connection, like the incidents tool.
type EventsTool struct {
	db     *gorm.DB
	logger *log.Logger
	now    func() time.Time

	mu      sync.Mutex
	reports map[string]int // run scope (X-Incident-ID) -> report_alert calls
}

// NewEventsTool creates a new EventsTool.
func NewEventsTool(db *gorm.DB, logger *log.Logger) *EventsTool {
	return &EventsTool{db: db, logger: logger, now: time.Now, reports: map[string]int{}}
}

// eventSummary is the search-view projection (no payload).
type eventSummary struct {
	UUID           string    `json:"uuid"`
	Kind           string    `json:"kind"`
	SourceKind     string    `json:"source_kind"`
	SourceType     string    `json:"source_type,omitempty"`
	Title          string    `json:"title"`
	Host           string    `json:"host,omitempty"`
	Service        string    `json:"service,omitempty"`
	Severity       string    `json:"severity,omitempty"`
	Status         string    `json:"status,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
	Classification string    `json:"classification"`
	AlertUUID      string    `json:"alert_uuid,omitempty"`
	IncidentUUID   string    `json:"incident_uuid,omitempty"`
	ScopeUUID      string    `json:"scope_uuid,omitempty"`
}

type searchResponse struct {
	Events []eventSummary `json:"events"`
	Count  int            `json:"count"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// Search returns events newest first, summary fields only.
// Args: kind (alert|change|info|request), source (source_kind), host
// (substring), text (substring of title or summary), since / until (Unix
// seconds or ISO-8601, on occurred_at), limit (default 50, max 200), offset.
func (t *EventsTool) Search(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	limit := toInt(args["limit"], defaultLimit)
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := toInt(args["offset"], 0)
	if offset < 0 {
		offset = 0
	}

	q := t.db.WithContext(ctx).Model(&database.Event{})
	if kind := stringArg(args, "kind"); kind != "" {
		if !validKinds[kind] {
			return nil, fmt.Errorf("kind must be one of alert, change, info, request; got %q", kind)
		}
		q = q.Where("kind = ?", kind)
	}
	if source := stringArg(args, "source"); source != "" {
		q = q.Where("source_kind = ?", source)
	}
	if host := strings.ToLower(stringArg(args, "host")); host != "" {
		q = q.Where("LOWER(host) LIKE ?", "%"+host+"%")
	}
	if text := strings.ToLower(stringArg(args, "text")); text != "" {
		like := "%" + text + "%"
		q = q.Where("LOWER(title) LIKE ? OR LOWER(summary) LIKE ?", like, like)
	}
	if ts := toUnix(args["since"]); ts > 0 {
		q = q.Where("occurred_at >= ?", time.Unix(ts, 0))
	}
	if ts := toUnix(args["until"]); ts > 0 {
		q = q.Where("occurred_at <= ?", time.Unix(ts, 0))
	}

	var rows []database.Event
	if err := q.Order("occurred_at DESC, id DESC").Limit(limit).Offset(offset).
		Select("uuid, kind, source_kind, source_type, title, host, service, severity, status, occurred_at, classification, alert_uuid, incident_uuid, scope_uuid").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]eventSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, eventSummary{
			UUID: r.UUID, Kind: r.Kind, SourceKind: r.SourceKind, SourceType: r.SourceType,
			Title: r.Title, Host: r.Host, Service: r.Service, Severity: r.Severity, Status: r.Status,
			OccurredAt: r.OccurredAt, Classification: r.Classification,
			AlertUUID: r.AlertUUID, IncidentUUID: r.IncidentUUID, ScopeUUID: r.ScopeUUID,
		})
	}
	return marshal(searchResponse{Events: out, Count: len(out), Limit: limit, Offset: offset})
}

// eventDetail is the get-view projection.
type eventDetail struct {
	database.Event
	RawPayload          json.RawMessage `json:"raw_payload,omitempty"`
	RawPayloadTruncated bool            `json:"raw_payload_truncated,omitempty"`
	// Run is the agent work of a request event (a cron run, a Slack or API
	// request): its newest answer and its log.
	Run *database.RunsWork `json:"run,omitempty"`
}

// Get returns one event with its summary, labels, class reasoning and the raw
// payload (cut to maxRawPayload bytes of JSON). A request event also returns
// the work of its runs (answer and log, the log cut to its newest maxRunLog
// bytes).
func (t *EventsTool) Get(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	id := stringArg(args, "uuid")
	if id == "" {
		return nil, errors.New("uuid is required")
	}
	var ev database.Event
	if err := t.db.WithContext(ctx).Where("uuid = ?", id).First(&ev).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("event not found")
		}
		return nil, err
	}
	detail := eventDetail{Event: ev}
	if len(ev.RawPayload) > 0 {
		raw, err := json.Marshal(ev.RawPayload)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxRawPayload {
			// Truncated JSON is not valid JSON: return it as a string.
			cut := strings.ToValidUTF8(string(raw[:maxRawPayload]), "")
			raw, _ = json.Marshal(cut)
			detail.RawPayloadTruncated = true
		}
		detail.RawPayload = raw
	}
	detail.Event.RawPayload = nil
	if ev.ScopeUUID != "" {
		work, err := database.LoadRunsWork(ctx, t.db, "scope_uuid", ev.ScopeUUID, maxRunLog)
		if err != nil {
			return nil, err
		}
		if work.Runs > 0 {
			detail.Run = &work
		}
	}
	return marshal(detail)
}

type reportResponse struct {
	EventUUID string `json:"event_uuid"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// ReportAlert records a problem that a cron run found as an alert event. The
// main API picks it up within seconds and runs it through the alert
// correlation gate: it joins the open incident it belongs to or opens a new
// one. Only cron runs may call it — an incident investigation is already the
// response to an alert.
// Args: alert_name* (short name), summary* (what is wrong, with evidence),
// host, service, severity (critical|high|warning|info, default warning).
func (t *EventsTool) ReportAlert(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
	name := strings.TrimSpace(stringArg(args, "alert_name"))
	summary := strings.TrimSpace(stringArg(args, "summary"))
	if name == "" || summary == "" {
		return nil, errors.New("alert_name and summary are required")
	}
	severity := strings.ToLower(strings.TrimSpace(stringArg(args, "severity")))
	if severity == "" {
		severity = "warning"
	}
	if !validSeverities[severity] {
		return nil, fmt.Errorf("severity must be one of critical, high, warning, info; got %q", severity)
	}
	host := strings.TrimSpace(stringArg(args, "host"))
	service := strings.TrimSpace(stringArg(args, "service"))

	if incidentID == "" {
		return nil, errors.New("report_alert needs the calling agent run")
	}
	// X-Incident-ID is the run's scope; the newest run of the scope is the
	// caller.
	var run database.AgentRun
	if err := t.db.WithContext(ctx).Select("scope_uuid, source_kind, source_uuid").
		Where("scope_uuid = ?", incidentID).Order("id DESC").First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("calling agent run not found")
		}
		return nil, err
	}
	if run.SourceKind != runKindCron {
		return nil, errors.New("report_alert is only for cron runs; an incident investigation is already the response to an alert")
	}

	t.mu.Lock()
	// The counter only has to outlive one run; drop it all when it grows,
	// so a long-lived gateway does not keep one entry per run forever.
	if len(t.reports) > 10_000 {
		t.reports = map[string]int{}
	}
	if t.reports[incidentID] >= maxReportsPerRun {
		t.mu.Unlock()
		return nil, fmt.Errorf("report_alert limit reached: at most %d reports per run", maxReportsPerRun)
	}
	t.reports[incidentID]++
	t.mu.Unlock()

	var jobName string
	var job database.CronJob
	if err := t.db.WithContext(ctx).Select("name").Where("uuid = ?", run.SourceUUID).Limit(1).Find(&job).Error; err == nil {
		jobName = job.Name
	}
	now := t.now()
	ev := database.Event{
		UUID:       uuid.New().String(),
		SourceKind: sourceCron,
		SourceUUID: run.SourceUUID,
		SourceType: "cron",
		// A stable identity per job, alert and host: a later run that reports
		// the same problem updates the same firing alert instead of opening a
		// second incident.
		ExternalID:              cut("cron:"+run.SourceUUID+":"+strings.ToLower(name)+":"+strings.ToLower(host), 255),
		ReceivedAt:              now,
		OccurredAt:              now,
		Kind:                    kindAlert,
		Classification:          classificationSource,
		ClassificationReasoning: "reported by cron run " + incidentID,
		Title:                   cut(name, maxTitleRunes),
		Summary:                 cut(summary, maxSummaryRunes),
		Host:                    cut(host, 255),
		Service:                 cut(service, 255),
		Severity:                severity,
		Status:                  statusFiring,
		RawPayload: database.JSONB{
			"original_message": summary,
			"reported_by_run":  incidentID,
			"cron_job_name":    jobName,
		},
		PendingDispatch: true,
	}
	if err := t.db.WithContext(ctx).Create(&ev).Error; err != nil {
		return nil, fmt.Errorf("store alert event: %w", err)
	}
	t.logger.Printf("events.report_alert: run %s reported %q (event %s)", incidentID, name, ev.UUID)
	return marshal(reportResponse{
		EventUUID: ev.UUID,
		Status:    "queued",
		Note:      "Akmatori runs this alert through correlation within seconds: it joins the open incident it belongs to, or opens a new one. Mention the reported problem in your final summary.",
	})
}

func marshal(v interface{}) (interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func stringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

// cut returns s cut to at most n runes.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// toInt safely extracts an int from interface{}, returning def on failure.
func toInt(v interface{}, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return def
}

// toUnix converts an arg into Unix seconds. Numbers and numeric strings are
// Unix seconds; other strings are parsed as RFC3339 or common date layouts,
// so callers can pass ISO dates instead of hand-computing epoch seconds.
// Returns 0 when the value cannot be read.
func toUnix(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0
		}
		if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
			return iv
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}
