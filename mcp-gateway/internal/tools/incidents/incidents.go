package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"gorm.io/gorm"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxFullLog   = 50_000
)

// IncidentsTool provides read-only access to Akmatori's own incident records.
type IncidentsTool struct {
	db     *gorm.DB
	logger *log.Logger
}

// NewIncidentsTool creates a new IncidentsTool.
func NewIncidentsTool(db *gorm.DB, logger *log.Logger) *IncidentsTool {
	return &IncidentsTool{db: db, logger: logger}
}

// incidentSummary is the list-view projection (no FullLog/Response).
type incidentSummary struct {
	UUID        string     `json:"uuid"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	SourceKind  string     `json:"source_kind"`
	SourceUUID  string     `json:"source_uuid"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	TokensUsed  int64      `json:"tokens_used"`
}

// listResponse is the JSON envelope returned by List.
type listResponse struct {
	Incidents []incidentSummary `json:"incidents"`
	Count     int               `json:"count"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
}

// List returns a paginated, filtered list of incidents (summary fields only).
// Only alerts open incidents; cron runs and other request work are events
// (events.search source=cron, events.get).
// Supported args: from (unix int), to (unix int), status (string), source_kind (string),
// limit (int, default 50, max 200), offset (int).
// incidentID is ignored — this tool queries globally.
func (t *IncidentsTool) List(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	limit := defaultLimit
	if v, ok := args["limit"]; ok {
		limit = toInt(v, defaultLimit)
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	offset := 0
	if v, ok := args["offset"]; ok {
		offset = toInt(v, 0)
	}
	if offset < 0 {
		offset = 0
	}

	q := t.db.WithContext(ctx).Model(&database.Incident{})

	if v, ok := args["status"]; ok {
		if s, ok := v.(string); ok && s != "" {
			q = q.Where("status = ?", s)
		}
	}
	if v, ok := args["source_kind"]; ok {
		if s, ok := v.(string); ok && s != "" {
			q = q.Where("source_kind = ?", s)
		}
	}
	if v, ok := args["from"]; ok {
		if ts := toUnix(v); ts > 0 {
			q = q.Where("started_at >= ?", time.Unix(ts, 0))
		}
	}
	if v, ok := args["to"]; ok {
		if ts := toUnix(v); ts > 0 {
			q = q.Where("started_at <= ?", time.Unix(ts, 0))
		}
	}

	var rows []database.Incident
	if err := q.Order("started_at DESC").
		Limit(limit).
		Offset(offset).
		Select("uuid, title, status, source_kind, source_uuid, started_at, completed_at").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// Tokens are spent by the incident's agent runs.
	tokens := map[string]int64{}
	if len(rows) > 0 {
		uuids := make([]string, len(rows))
		for i, r := range rows {
			uuids[i] = r.UUID
		}
		var sums []struct {
			IncidentUUID string
			Tokens       int64
		}
		if err := t.db.WithContext(ctx).Model(&database.AgentRun{}).
			Select("incident_uuid, SUM(tokens_used) AS tokens").
			Where("incident_uuid IN ?", uuids).Group("incident_uuid").Scan(&sums).Error; err != nil {
			return nil, err
		}
		for _, s := range sums {
			tokens[s.IncidentUUID] = s.Tokens
		}
	}

	summaries := make([]incidentSummary, 0, len(rows))
	for _, r := range rows {
		summaries = append(summaries, incidentSummary{
			UUID:        r.UUID,
			Title:       r.Title,
			Status:      r.Status,
			SourceKind:  r.SourceKind,
			SourceUUID:  r.SourceUUID,
			StartedAt:   r.StartedAt,
			CompletedAt: r.CompletedAt,
			TokensUsed:  tokens[r.UUID],
		})
	}

	resp := listResponse{
		Incidents: summaries,
		Count:     len(summaries),
		Limit:     limit,
		Offset:    offset,
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// incidentDetail is the get-view projection (excludes internal fields like Context, SlackChannelID, etc.).
// The agent work comes from the incident's runs: the newest answer, every
// run's log and the sums.
type incidentDetail struct {
	UUID            string     `json:"uuid"`
	Source          string     `json:"source"`
	SourceID        string     `json:"source_id"`
	SourceKind      string     `json:"source_kind"`
	SourceUUID      string     `json:"source_uuid"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Runs            int        `json:"runs"`
	FullLog         string     `json:"full_log"`
	Response        string     `json:"response"`
	TokensUsed      int64      `json:"tokens_used"`
	ExecutionTimeMs int64      `json:"execution_time_ms"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Get returns the full incident record for the given uuid, with the work of
// its agent runs. FullLog keeps its newest 50,000 bytes.
// Internal fields (Context, SlackChannelID, SlackMessageTS) are omitted.
// incidentID is ignored — this tool queries by the uuid arg.
func (t *IncidentsTool) Get(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	uuidVal, ok := args["uuid"]
	if !ok {
		return nil, errors.New("uuid is required")
	}
	uuidStr, ok := uuidVal.(string)
	if !ok || uuidStr == "" {
		return nil, errors.New("uuid must be a non-empty string")
	}

	var inc database.Incident
	if err := t.db.WithContext(ctx).Where("uuid = ?", uuidStr).First(&inc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("incident not found")
		}
		return nil, err
	}

	work, err := database.LoadRunsWork(ctx, t.db, "incident_uuid", inc.UUID, maxFullLog)
	if err != nil {
		return nil, err
	}

	detail := incidentDetail{
		UUID:            inc.UUID,
		Source:          inc.Source,
		SourceID:        inc.SourceID,
		SourceKind:      inc.SourceKind,
		SourceUUID:      inc.SourceUUID,
		Title:           inc.Title,
		Status:          inc.Status,
		Runs:            work.Runs,
		FullLog:         work.FullLog,
		Response:        work.Response,
		TokensUsed:      work.TokensUsed,
		ExecutionTimeMs: work.ExecutionTimeMs,
		StartedAt:       inc.StartedAt,
		CompletedAt:     inc.CompletedAt,
		CreatedAt:       inc.CreatedAt,
		UpdatedAt:       inc.UpdatedAt,
	}

	b, err := json.Marshal(detail)
	if err != nil {
		return nil, err
	}
	return string(b), nil
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

// toUnix converts an arg into a Unix timestamp (seconds). Numeric values (and
// numeric strings) are treated as Unix seconds; string values are also parsed
// as RFC3339 or common date/datetime layouts. This lets callers pass ISO dates
// (e.g. "2026-07-01T00:00:00Z") instead of hand-computing epoch seconds, which
// LLMs frequently get wrong. Returns 0 when the value cannot be interpreted.
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
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}
