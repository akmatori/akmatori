package handlers

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
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
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

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
	wantNonAlert := typeParam == "" || (typeParam != "alert")

	// Build the merged result using two queries then merge+sort in Go.
	// This avoids UNION ALL compatibility issues across SQLite (tests) and PostgreSQL (prod).

	var alertItems []EventFeedItem
	if wantAlerts {
		alertQ := db.Model(&database.Alert{}).
			Select("uuid, incident_uuid, alert_name, fired_at, status, correlated, correlation_confidence, correlation_reasoning, correlation_decision, target_host, source_uuid")
		if fromTime != nil {
			alertQ = alertQ.Where("fired_at >= ?", *fromTime)
		}
		if toTime != nil {
			alertQ = alertQ.Where("fired_at <= ?", *toTime)
		}

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

	var incidentItems []EventFeedItem
	if wantNonAlert {
		// Non-alert incidents (cron, slack_mention, api, etc.)
		incQ := db.Model(&database.Incident{}).
			Select("uuid, title, started_at, status, source_kind, source_uuid").
			Where("source_kind != ?", database.IncidentSourceKindAlert)
		if typeParam != "" && typeParam != "alert" {
			incQ = incQ.Where("source_kind = ?", typeParam)
		}
		if fromTime != nil {
			incQ = incQ.Where("started_at >= ?", *fromTime)
		}
		if toTime != nil {
			incQ = incQ.Where("started_at <= ?", *toTime)
		}

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

	total := int64(len(merged))

	// Apply pagination.
	offset := params.Offset()
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

