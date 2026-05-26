package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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
	TokensUsed  int        `json:"tokens_used"`
}

// listResponse is the JSON envelope returned by List.
type listResponse struct {
	Incidents []incidentSummary `json:"incidents"`
	Count     int               `json:"count"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
}

// List returns a paginated, filtered list of incidents (summary fields only).
// Supported args: from (unix int), to (unix int), status (string), source_kind (string),
// limit (int, default 50, max 200), offset (int).
// incidentID is ignored — this tool queries globally.
func (t *IncidentsTool) List(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
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

	q := t.db.Model(&database.Incident{})

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
		if ts := int64(toInt(v, 0)); ts > 0 {
			q = q.Where("started_at >= ?", time.Unix(ts, 0))
		}
	}
	if v, ok := args["to"]; ok {
		if ts := int64(toInt(v, 0)); ts > 0 {
			q = q.Where("started_at <= ?", time.Unix(ts, 0))
		}
	}

	var rows []database.Incident
	if err := q.Order("started_at DESC").
		Limit(limit).
		Offset(offset).
		Select("uuid, title, status, source_kind, source_uuid, started_at, completed_at, tokens_used").
		Find(&rows).Error; err != nil {
		return nil, err
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
			TokensUsed:  r.TokensUsed,
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

// incidentDetail is the get-view projection (excludes internal fields like WorkingDir, Context, SlackChannelID, etc.).
type incidentDetail struct {
	ID              uint       `json:"id"`
	UUID            string     `json:"uuid"`
	Source          string     `json:"source"`
	SourceID        string     `json:"source_id"`
	SourceKind      string     `json:"source_kind"`
	SourceUUID      string     `json:"source_uuid"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	SessionID       string     `json:"session_id"`
	FullLog         string     `json:"full_log"`
	Response        string     `json:"response"`
	TokensUsed      int        `json:"tokens_used"`
	ExecutionTimeMs int64      `json:"execution_time_ms"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Get returns the full incident record for the given uuid.
// FullLog is truncated to 50,000 bytes if longer.
// Internal fields (WorkingDir, Context, SlackChannelID, SlackMessageTS) are omitted.
// incidentID is ignored — this tool queries by the uuid arg.
func (t *IncidentsTool) Get(_ context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	uuidVal, ok := args["uuid"]
	if !ok {
		return nil, errors.New("uuid is required")
	}
	uuidStr, ok := uuidVal.(string)
	if !ok || uuidStr == "" {
		return nil, errors.New("uuid must be a non-empty string")
	}

	var inc database.Incident
	if err := t.db.Where("uuid = ?", uuidStr).First(&inc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("incident not found")
		}
		return nil, err
	}

	fullLog := inc.FullLog
	if len(fullLog) > maxFullLog {
		fullLog = strings.ToValidUTF8(fullLog[:maxFullLog], "")
	}

	detail := incidentDetail{
		ID:              inc.ID,
		UUID:            inc.UUID,
		Source:          inc.Source,
		SourceID:        inc.SourceID,
		SourceKind:      inc.SourceKind,
		SourceUUID:      inc.SourceUUID,
		Title:           inc.Title,
		Status:          inc.Status,
		SessionID:       inc.SessionID,
		FullLog:         fullLog,
		Response:        inc.Response,
		TokensUsed:      inc.TokensUsed,
		ExecutionTimeMs: inc.ExecutionTimeMs,
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

