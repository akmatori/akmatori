package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// EventFeedItem is one row of the Events feed: one events row, joined to the
// alert it created (correlation state, live firing/resolved status), the
// incident it opened, joined or continued, and the request work it started
// or continued.
type EventFeedItem struct {
	EventUUID                string    `json:"event_uuid"`
	Kind                     string    `json:"kind"`
	SourceKind               string    `json:"source_kind"`
	SourceType               string    `json:"source_type,omitempty"`
	SourceUUID               string    `json:"source_uuid,omitempty"`
	Classification           string    `json:"classification"`
	ClassificationConfidence *float64  `json:"classification_confidence,omitempty"`
	ClassificationReasoning  string    `json:"classification_reasoning,omitempty"`
	Title                    string    `json:"title"`
	OccurredAt               time.Time `json:"occurred_at"`
	// Status is what the event means now: the alert's firing/resolved state
	// for alert events, the run's status for request events, empty for
	// change and info events.
	Status                string   `json:"status"`
	TargetHost            string   `json:"target_host,omitempty"`
	AlertUUID             string   `json:"alert_uuid,omitempty"`
	IncidentUUID          string   `json:"incident_uuid,omitempty"`
	ScopeUUID             string   `json:"scope_uuid,omitempty"`
	Correlated            bool     `json:"correlated"`
	CorrelationConfidence *float64 `json:"correlation_confidence,omitempty"`
	CorrelationReasoning  string   `json:"correlation_reasoning,omitempty"`
	CorrelationDecision   string   `json:"correlation_decision,omitempty"`
	IncidentTitle         string   `json:"incident_title,omitempty"`
	IncidentStatus        string   `json:"incident_status,omitempty"`
}

// eventsMaxPage bounds page numbers so a huge value cannot overflow the
// offset computation.
const eventsMaxPage = 100_000

// legacyEventTypeFilter maps the pre-events ?type= values onto the new
// filters: "alert" was the alert class, the others were run sources.
func legacyEventTypeFilter(t string) (kind, source string) {
	switch t {
	case "":
		return "", ""
	case database.EventKindAlert, database.EventKindChange, database.EventKindInfo, database.EventKindRequest:
		return t, ""
	default:
		return "", t
	}
}

// handleEvents handles GET /api/events — the paginated events feed, newest
// first. Filters: from/to (unix seconds, on occurred_at), kind
// (alert|change|info|request), source (webhook|slack_listener|slack_mention|
// manual|cron|proposal), search (UUID prefix of the event, alert or incident;
// substring of title or host). The legacy ?type= is still accepted.
func (h *APIHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	params := api.ParsePagination(r)
	q := r.URL.Query()

	if params.Page > eventsMaxPage {
		api.RespondError(w, http.StatusBadRequest, "page exceeds the searchable range; use date filters to narrow the result set")
		return
	}

	kind, source := legacyEventTypeFilter(q.Get("type"))
	if v := q.Get("kind"); v != "" {
		kind = v
	}
	if v := q.Get("source"); v != "" {
		source = v
	}

	base := db.Model(&database.Event{})
	if ts, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
		base = base.Where("occurred_at >= ?", time.Unix(ts, 0))
	}
	if ts, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
		base = base.Where("occurred_at <= ?", time.Unix(ts, 0))
	}
	if kind != "" {
		base = base.Where("kind = ?", kind)
	}
	if source != "" {
		base = base.Where("source_kind = ?", source)
	}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		// UUIDs match by prefix (so a copied short ID finds its event);
		// title and host match by substring. LOWER(...) LIKE keeps the
		// query portable across PostgreSQL (prod) and SQLite (tests).
		prefix := strings.ToLower(search) + "%"
		like := "%" + strings.ToLower(search) + "%"
		base = base.Where(
			"LOWER(uuid) LIKE ? OR LOWER(alert_uuid) LIKE ? OR LOWER(incident_uuid) LIKE ? OR LOWER(scope_uuid) LIKE ? OR LOWER(title) LIKE ? OR LOWER(host) LIKE ?",
			prefix, prefix, prefix, prefix, like, like)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		slog.Error("events: failed to count", "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to fetch events")
		return
	}

	var rows []database.Event
	if err := base.Session(&gorm.Session{}).
		Omit("raw_payload", "labels", "summary").
		Order("occurred_at DESC, id DESC").
		Offset(params.Offset()).Limit(params.PerPage).
		Find(&rows).Error; err != nil {
		slog.Error("events: failed to fetch", "err", err)
		api.RespondError(w, http.StatusInternalServerError, "Failed to fetch events")
		return
	}

	page := make([]EventFeedItem, 0, len(rows))
	for _, e := range rows {
		page = append(page, EventFeedItem{
			EventUUID:                e.UUID,
			Kind:                     e.Kind,
			SourceKind:               e.SourceKind,
			SourceType:               e.SourceType,
			SourceUUID:               e.SourceUUID,
			Classification:           e.Classification,
			ClassificationConfidence: e.ClassificationConfidence,
			ClassificationReasoning:  e.ClassificationReasoning,
			Title:                    e.Title,
			OccurredAt:               e.OccurredAt,
			Status:                   e.Status,
			TargetHost:               e.Host,
			AlertUUID:                e.AlertUUID,
			IncidentUUID:             e.IncidentUUID,
			ScopeUUID:                e.ScopeUUID,
		})
	}
	enrichEventFeedItems(db, page)

	api.RespondJSON(w, http.StatusOK, api.PaginatedResponse{
		Data: page,
		Pagination: api.PaginationMeta{
			Page:       params.Page,
			PerPage:    params.PerPage,
			Total:      total,
			TotalPages: params.TotalPages(total),
		},
	})
}

// enrichEventFeedItems batch-loads the linked alerts and incidents of one
// page: alert correlation state and live status, incident title and status.
func enrichEventFeedItems(db *gorm.DB, page []EventFeedItem) {
	alertUUIDs := map[string]struct{}{}
	incidentUUIDs := map[string]struct{}{}
	for _, item := range page {
		if item.AlertUUID != "" {
			alertUUIDs[item.AlertUUID] = struct{}{}
		}
		if item.IncidentUUID != "" {
			incidentUUIDs[item.IncidentUUID] = struct{}{}
		}
	}

	alertByUUID := map[string]database.Alert{}
	if len(alertUUIDs) > 0 {
		var alerts []database.Alert
		if err := db.Select("uuid, incident_uuid, status, correlated, correlation_confidence, correlation_reasoning, correlation_decision").
			Where("uuid IN ?", mapKeys(alertUUIDs)).Find(&alerts).Error; err != nil {
			slog.Warn("events: failed to batch-fetch alerts", "err", err)
		}
		for _, a := range alerts {
			alertByUUID[a.UUID] = a
			// An alert moved to another incident keeps its event: follow it.
			if a.IncidentUUID != "" {
				incidentUUIDs[a.IncidentUUID] = struct{}{}
			}
		}
	}

	type incSummary struct {
		UUID   string
		Title  string
		Status string
	}
	incByUUID := map[string]incSummary{}
	if len(incidentUUIDs) > 0 {
		var summaries []incSummary
		if err := db.Model(&database.Incident{}).Select("uuid, title, status").
			Where("uuid IN ?", mapKeys(incidentUUIDs)).Scan(&summaries).Error; err != nil {
			slog.Warn("events: failed to batch-fetch incident summaries", "err", err)
		}
		for _, s := range summaries {
			incByUUID[s.UUID] = s
		}
	}

	// Request events show the status of their work's newest run.
	scopeUUIDs := map[string]struct{}{}
	for _, item := range page {
		if item.ScopeUUID != "" {
			scopeUUIDs[item.ScopeUUID] = struct{}{}
		}
	}
	scopeStatus := map[string]string{}
	if len(scopeUUIDs) > 0 {
		var runs []database.AgentRun
		if err := db.Select("id, scope_uuid, status").Where("scope_uuid IN ?", mapKeys(scopeUUIDs)).
			Order("id ASC").Find(&runs).Error; err != nil {
			slog.Warn("events: failed to batch-fetch request runs", "err", err)
		}
		for _, r := range runs {
			scopeStatus[r.ScopeUUID] = requestStatus(r) // ascending: the newest wins
		}
	}

	for i := range page {
		item := &page[i]
		if st, ok := scopeStatus[item.ScopeUUID]; ok && item.Kind == database.EventKindRequest {
			item.Status = st
		}
		// A change or info item may come from a monitoring source in firing
		// state; that state means nothing for an event that is only stored.
		if item.Kind == database.EventKindChange || item.Kind == database.EventKindInfo {
			item.Status = ""
		}
		if a, ok := alertByUUID[item.AlertUUID]; ok {
			item.Status = string(a.Status)
			item.Correlated = a.Correlated
			item.CorrelationConfidence = a.CorrelationConfidence
			item.CorrelationReasoning = a.CorrelationReasoning
			item.CorrelationDecision = a.CorrelationDecision
			if a.IncidentUUID != "" {
				item.IncidentUUID = a.IncidentUUID
			}
		}
		if inc, ok := incByUUID[item.IncidentUUID]; ok {
			item.IncidentTitle = inc.Title
			item.IncidentStatus = inc.Status
			if item.Kind == database.EventKindRequest {
				item.Status = inc.Status
			}
		}
	}
}

func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// handleEventRaw handles GET /api/events/raw?uuid=<event_uuid>. It returns the
// raw payload / original message of one event on demand, so the list stays
// light. Backfilled events carry no payload of their own: it then comes from
// the linked alert row or the incident's context. The legacy ?type= is ignored.
func (h *APIHandler) handleEventRaw(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	eventUUID := r.URL.Query().Get("uuid")
	if eventUUID == "" {
		api.RespondError(w, http.StatusBadRequest, "uuid is required")
		return
	}

	var ev database.Event
	if err := db.Where("uuid = ?", eventUUID).First(&ev).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			api.RespondError(w, http.StatusNotFound, "Event not found")
		} else {
			slog.Error("events raw: failed to load event", "uuid", eventUUID, "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to load event")
		}
		return
	}

	resp := map[string]interface{}{}
	raw := map[string]interface{}(ev.RawPayload)
	if len(raw) == 0 && ev.AlertUUID != "" {
		var alert database.Alert
		if err := db.Select("raw_payload").Where("uuid = ?", ev.AlertUUID).First(&alert).Error; err == nil {
			raw = alert.RawPayload
		}
	}
	if len(raw) == 0 && ev.AlertUUID == "" && ev.IncidentUUID != "" {
		var inc database.Incident
		if err := db.Select("context").Where("uuid = ?", ev.IncidentUUID).Limit(1).Find(&inc).Error; err == nil && inc.Context != nil {
			if rp, ok := inc.Context["raw_payload"].(map[string]interface{}); ok {
				raw = rp
			}
		}
	}
	if len(raw) > 0 {
		resp["raw"] = raw
		if om, ok := raw["original_message"]; ok {
			resp["original_message"] = om
		}
	}
	if _, ok := resp["original_message"]; !ok && ev.Summary != "" {
		resp["original_message"] = ev.Summary
	}
	if len(ev.Labels) > 0 {
		resp["labels"] = ev.Labels
	}

	api.RespondJSON(w, http.StatusOK, resp)
}
