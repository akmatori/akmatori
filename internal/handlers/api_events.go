package handlers

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// EventFeedItem is one entry in the unified events feed.
// Alert rows use event_type="alert" and occurred_at=fired_at.
// Non-alert incident rows use event_type=source_kind and occurred_at=started_at.
type EventFeedItem struct {
	EventType             string     `json:"event_type"`
	EventUUID             string     `json:"event_uuid"`
	Title                 string     `json:"title"`
	OccurredAt            time.Time  `json:"occurred_at"`
	Status                string     `json:"status"`
	IncidentUUID          string     `json:"incident_uuid"`
	Correlated            bool       `json:"correlated"`
	CorrelationConfidence *float64   `json:"correlation_confidence,omitempty"`
	CorrelationReasoning  string     `json:"correlation_reasoning,omitempty"`
	CorrelationDecision   string     `json:"correlation_decision,omitempty"`
	TargetHost            string     `json:"target_host,omitempty"`
	SourceUUID            string     `json:"source_uuid,omitempty"`
	IncidentTitle         string     `json:"incident_title,omitempty"`
	IncidentStatus        string     `json:"incident_status,omitempty"`
}

// handleEvents handles GET /api/events — unified paginated feed of alerts and
// non-alert incidents, merged and ordered by occurred_at DESC.
func (h *APIHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	db := database.GetDB()
	params := api.ParsePagination(r)

	fromParam := r.URL.Query().Get("from")
	toParam := r.URL.Query().Get("to")
	typeParam := r.URL.Query().Get("type")

	var fromTime, toTime *time.Time
	if fromParam != "" {
		if ts, err := strconv.ParseInt(fromParam, 10, 64); err == nil {
			t := time.Unix(ts, 0)
			fromTime = &t
		}
	}
	if toParam != "" {
		if ts, err := strconv.ParseInt(toParam, 10, 64); err == nil {
			t := time.Unix(ts, 0)
			toTime = &t
		}
	}

	wantAlerts := typeParam == "" || typeParam == "alert"
	wantNonAlert := typeParam != "alert"

	// Build the merged result using two queries then merge+sort in Go.
	// This avoids UNION ALL compatibility issues across SQLite (tests) and PostgreSQL (prod).
	//
	// Use separate COUNT queries for the total, and limit each data query to
	// (offset + perPage) rows — sufficient to serve any page without loading the
	// entire table. Hard-cap the fetch to eventsMaxRowFetch to prevent large
	// page numbers from pulling unbounded rows into memory.
	const eventsMaxRowFetch = 10_000
	// Reject page numbers that exceed the fetch cap before multiplying. Any
	// page > eventsMaxRowFetch would produce offset >= eventsMaxRowFetch even
	// at per_page=1, but very large page values can also overflow int and wrap
	// to a small positive offset, bypassing the guard below.
	if params.Page > eventsMaxRowFetch {
		api.RespondError(w, http.StatusBadRequest, "page exceeds the searchable range; use date filters to narrow the result set")
		return
	}
	offset := params.Offset()
	// Reject pages whose offset exceeds the row-fetch cap. The in-memory merge
	// can only cover rows 0…eventsMaxRowFetch-1; beyond that the result would
	// be silently empty while the client's total_pages would still show the full
	// uncapped count. Instruct the caller to use date filters instead.
	if offset < 0 || offset >= eventsMaxRowFetch {
		api.RespondError(w, http.StatusBadRequest, "page exceeds the searchable range; use date filters to narrow the result set")
		return
	}
	rowLimit := offset + params.PerPage
	if rowLimit > eventsMaxRowFetch {
		rowLimit = eventsMaxRowFetch
	}

	var alertCount int64
	var alertItems []EventFeedItem
	if wantAlerts {
		alertBaseQ := db.Model(&database.Alert{})
		if fromTime != nil {
			alertBaseQ = alertBaseQ.Where("fired_at >= ?", *fromTime)
		}
		if toTime != nil {
			alertBaseQ = alertBaseQ.Where("fired_at <= ?", *toTime)
		}

		if err := alertBaseQ.Session(&gorm.Session{}).Count(&alertCount).Error; err != nil {
			slog.Error("events: failed to count alert rows", "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to fetch events")
			return
		}

		alertQ := alertBaseQ.
			Select("uuid, incident_uuid, alert_name, fired_at, status, correlated, correlation_confidence, correlation_reasoning, correlation_decision, target_host, source_uuid").
			Order("fired_at DESC").
			Limit(rowLimit)

		type alertRow struct {
			UUID                  string
			IncidentUUID          string
			AlertName             string
			FiredAt               time.Time
			Status                string
			Correlated            bool
			CorrelationConfidence *float64
			CorrelationReasoning  string
			CorrelationDecision   string
			TargetHost            string
			SourceUUID            string
		}
		var aRows []alertRow
		if err := alertQ.Scan(&aRows).Error; err != nil {
			slog.Warn("events: failed to scan alert rows", "err", err)
		}
		for _, a := range aRows {
			alertItems = append(alertItems, EventFeedItem{
				EventType:             "alert",
				EventUUID:             a.UUID,
				Title:                 a.AlertName,
				OccurredAt:            a.FiredAt,
				Status:                a.Status,
				IncidentUUID:          a.IncidentUUID,
				Correlated:            a.Correlated,
				CorrelationConfidence: a.CorrelationConfidence,
				CorrelationReasoning:  a.CorrelationReasoning,
				CorrelationDecision:   a.CorrelationDecision,
				TargetHost:            a.TargetHost,
				SourceUUID:            a.SourceUUID,
			})
		}
	}

	var incidentCount int64
	var incidentItems []EventFeedItem
	if wantNonAlert {
		// Non-alert incidents (cron, slack_mention, api, etc.)
		incBaseQ := db.Model(&database.Incident{}).
			Where("source_kind != ?", database.IncidentSourceKindAlert)
		if typeParam != "" && typeParam != "alert" {
			incBaseQ = incBaseQ.Where("source_kind = ?", typeParam)
		}
		if fromTime != nil {
			incBaseQ = incBaseQ.Where("started_at >= ?", *fromTime)
		}
		if toTime != nil {
			incBaseQ = incBaseQ.Where("started_at <= ?", *toTime)
		}

		if err := incBaseQ.Session(&gorm.Session{}).Count(&incidentCount).Error; err != nil {
			slog.Error("events: failed to count incident rows", "err", err)
			api.RespondError(w, http.StatusInternalServerError, "Failed to fetch events")
			return
		}

		incQ := incBaseQ.
			Select("uuid, title, started_at, status, source_kind, source_uuid").
			Order("started_at DESC").
			Limit(rowLimit)

		type incRow struct {
			UUID       string
			Title      string
			StartedAt  time.Time
			Status     string
			SourceKind string
			SourceUUID string
		}
		var iRows []incRow
		if err := incQ.Scan(&iRows).Error; err != nil {
			slog.Warn("events: failed to scan incident rows", "err", err)
		}
		for _, inc := range iRows {
			incidentItems = append(incidentItems, EventFeedItem{
				EventType:    inc.SourceKind,
				EventUUID:    inc.UUID,
				Title:        inc.Title,
				OccurredAt:   inc.StartedAt,
				Status:       inc.Status,
				IncidentUUID: inc.UUID,
				SourceUUID:   inc.SourceUUID,
			})
		}
	}

	// Merge and sort by OccurredAt DESC.
	merged := make([]EventFeedItem, 0, len(alertItems)+len(incidentItems))
	merged = append(merged, alertItems...)
	merged = append(merged, incidentItems...)
	sortEventFeedItems(merged)
	// Trim to the fetch-window cap so that pagination slices stay consistent
	// with the capped total. Without this, the last page could return more
	// rows than (total - offset) when both streams each contributed up to
	// eventsMaxRowFetch rows.
	if len(merged) > eventsMaxRowFetch {
		merged = merged[:eventsMaxRowFetch]
	}

	total := alertCount + incidentCount
	// Cap the reported total to the fetch-window maximum so pagination
	// metadata never advertises page counts beyond what can actually be served.
	if total > int64(eventsMaxRowFetch) {
		total = int64(eventsMaxRowFetch)
	}

	// Apply pagination.
	var page []EventFeedItem
	if offset < len(merged) {
		end := offset + params.PerPage
		if end > len(merged) {
			end = len(merged)
		}
		page = merged[offset:end]
	} else {
		page = []EventFeedItem{}
	}

	// Batch-enrich with incident title+status for alert rows.
	incidentUUIDs := make(map[string]struct{})
	for _, item := range page {
		if item.EventType == "alert" && item.IncidentUUID != "" {
			incidentUUIDs[item.IncidentUUID] = struct{}{}
		}
	}
	if len(incidentUUIDs) > 0 {
		uuids := make([]string, 0, len(incidentUUIDs))
		for u := range incidentUUIDs {
			uuids = append(uuids, u)
		}
		type incSummary struct {
			UUID   string
			Title  string
			Status string
		}
		var summaries []incSummary
		if err := db.Model(&database.Incident{}).
			Select("uuid, title, status").
			Where("uuid IN ?", uuids).
			Scan(&summaries).Error; err != nil {
			slog.Warn("events: failed to batch-fetch incident summaries", "err", err)
		}
		incMap := make(map[string]incSummary, len(summaries))
		for _, s := range summaries {
			incMap[s.UUID] = s
		}
		for i := range page {
			if page[i].EventType == "alert" {
				if s, ok := incMap[page[i].IncidentUUID]; ok {
					page[i].IncidentTitle = s.Title
					page[i].IncidentStatus = s.Status
				}
			}
		}
	}

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

// sortEventFeedItems sorts items by OccurredAt DESC (most recent first).
func sortEventFeedItems(items []EventFeedItem) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
}
