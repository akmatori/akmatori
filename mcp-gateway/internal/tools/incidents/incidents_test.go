package incidents

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&database.Incident{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newTool(db *gorm.DB) *IncidentsTool {
	return NewIncidentsTool(db, log.Default())
}

var baseTime = time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)

func insertIncident(t *testing.T, db *gorm.DB, uuid, title, status, sourceKind, sourceUUID string, startedAt time.Time, fullLog string) {
	t.Helper()
	inc := database.Incident{
		UUID:        uuid,
		Source:      "test",
		Title:       title,
		Status:      status,
		SourceKind:  sourceKind,
		SourceUUID:  sourceUUID,
		StartedAt:   startedAt,
		FullLog:     fullLog,
		Response:    "some response",
		TokensUsed:  100,
	}
	if err := db.Create(&inc).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// ---- List tests ----

func TestList_Empty(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	result, err := tool.List(context.Background(), "", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected 0 count, got %d", resp.Count)
	}
	if len(resp.Incidents) != 0 {
		t.Errorf("expected empty incidents slice")
	}
	if resp.Limit != defaultLimit {
		t.Errorf("expected default limit %d, got %d", defaultLimit, resp.Limit)
	}
}

func TestList_StatusFilter(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-1", "Title A", "resolved", "alert", "", baseTime, "")
	insertIncident(t, db, "uuid-2", "Title B", "pending", "alert", "", baseTime.Add(time.Hour), "")
	insertIncident(t, db, "uuid-3", "Title C", "resolved", "cron", "", baseTime.Add(2*time.Hour), "")

	result, err := tool.List(context.Background(), "", map[string]interface{}{"status": "resolved"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("expected 2 resolved, got %d", resp.Count)
	}
	for _, inc := range resp.Incidents {
		if inc.Status != "resolved" {
			t.Errorf("non-resolved returned: %s", inc.Status)
		}
	}
}

func TestList_SourceKindFilter(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-1", "Title A", "resolved", "alert", "", baseTime, "")
	insertIncident(t, db, "uuid-2", "Title B", "resolved", "cron", "cron-uuid", baseTime.Add(time.Hour), "")

	result, err := tool.List(context.Background(), "", map[string]interface{}{"source_kind": "cron"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("expected 1 cron, got %d", resp.Count)
	}
	if resp.Incidents[0].SourceKind != "cron" {
		t.Errorf("expected source_kind cron, got %s", resp.Incidents[0].SourceKind)
	}
}

func TestList_TimeRangeFilter(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)

	insertIncident(t, db, "uuid-1", "Early", "resolved", "alert", "", t1, "")
	insertIncident(t, db, "uuid-2", "Mid", "resolved", "alert", "", t2, "")
	insertIncident(t, db, "uuid-3", "Late", "resolved", "alert", "", t3, "")

	from := float64(t2.Unix())
	to := float64(t2.Add(24 * time.Hour).Unix())

	result, err := tool.List(context.Background(), "", map[string]interface{}{"from": from, "to": to})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("expected 1, got %d", resp.Count)
	}
	if resp.Incidents[0].UUID != "uuid-2" {
		t.Errorf("expected uuid-2, got %s", resp.Incidents[0].UUID)
	}
}

func TestList_LimitClampedTo200(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	result, err := tool.List(context.Background(), "", map[string]interface{}{"limit": float64(9999)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Limit != maxLimit {
		t.Errorf("expected limit clamped to %d, got %d", maxLimit, resp.Limit)
	}
}

func TestList_Offset(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-1", "First", "resolved", "alert", "", baseTime, "")
	insertIncident(t, db, "uuid-2", "Second", "resolved", "alert", "", baseTime.Add(time.Hour), "")
	insertIncident(t, db, "uuid-3", "Third", "resolved", "alert", "", baseTime.Add(2*time.Hour), "")

	result, err := tool.List(context.Background(), "", map[string]interface{}{"limit": float64(2), "offset": float64(1)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp listResponse
	if err := json.Unmarshal([]byte(result.(string)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("expected 2, got %d", resp.Count)
	}
	if resp.Offset != 1 {
		t.Errorf("expected offset 1, got %d", resp.Offset)
	}
	// DESC order + offset=1: skips uuid-3 (newest), returns uuid-2 then uuid-1
	if len(resp.Incidents) != 2 {
		t.Fatalf("expected 2 incidents, got %d", len(resp.Incidents))
	}
	if resp.Incidents[0].UUID != "uuid-2" {
		t.Errorf("expected uuid-2 at index 0, got %s", resp.Incidents[0].UUID)
	}
	if resp.Incidents[1].UUID != "uuid-1" {
		t.Errorf("expected uuid-1 at index 1, got %s", resp.Incidents[1].UUID)
	}
}

func TestList_NoFullLogOrResponse(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-1", "Title", "resolved", "alert", "", baseTime, "this is the full log")

	result, err := tool.List(context.Background(), "", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw := result.(string)

	if strings.Contains(raw, "full_log") {
		t.Errorf("list result should not contain full_log field")
	}
	if strings.Contains(raw, `"response"`) {
		t.Errorf("list result should not contain response field")
	}
}

// ---- Get tests ----

func TestGet_Found(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-full", "Full Incident", "resolved", "alert", "src-uuid", baseTime, "logcontent")

	result, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "uuid-full"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inc incidentDetail
	if err := json.Unmarshal([]byte(result.(string)), &inc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if inc.UUID != "uuid-full" {
		t.Errorf("expected uuid-full, got %s", inc.UUID)
	}
	if inc.Title != "Full Incident" {
		t.Errorf("expected 'Full Incident', got %s", inc.Title)
	}
	if inc.FullLog != "logcontent" {
		t.Errorf("expected 'logcontent', got %s", inc.FullLog)
	}
	if inc.SourceKind != "alert" {
		t.Errorf("expected 'alert', got %s", inc.SourceKind)
	}
}

func TestGet_NotFound(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	_, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if err.Error() != "incident not found" {
		t.Errorf("expected 'incident not found', got %q", err.Error())
	}
}

func TestGet_FullLogTruncated(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	bigLog := strings.Repeat("x", maxFullLog+1000)
	insertIncident(t, db, "uuid-big", "Big Log", "resolved", "alert", "", baseTime, bigLog)

	result, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "uuid-big"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inc incidentDetail
	if err := json.Unmarshal([]byte(result.(string)), &inc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(inc.FullLog) > maxFullLog {
		t.Errorf("expected FullLog truncated to at most %d, got %d", maxFullLog, len(inc.FullLog))
	}
}

func TestGet_MissingUUID(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	_, err := tool.Get(context.Background(), "", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing uuid")
	}
}

func TestGet_EmptyStringUUID(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	_, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": ""})
	if err == nil {
		t.Fatal("expected error for empty string uuid")
	}
}

func TestGet_NonStringUUID(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	_, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": 42})
	if err == nil {
		t.Fatal("expected error for non-string uuid")
	}
}

func TestGet_ExcludesInternalFields(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-internal", "Internal Test", "resolved", "alert", "", baseTime, "log")

	result, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "uuid-internal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw := result.(string)

	for _, field := range []string{"working_dir", "slack_channel_id", "slack_message_ts", "context"} {
		if strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("get result should not contain field %q", field)
		}
	}
}

func TestGet_IgnoresIncidentID(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	insertIncident(t, db, "uuid-x", "Incident X", "resolved", "alert", "", baseTime, "")

	// passing a different incidentID as the second parameter should not affect lookup
	result, err := tool.Get(context.Background(), "some-other-incident", map[string]interface{}{"uuid": "uuid-x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var inc incidentDetail
	if err := json.Unmarshal([]byte(result.(string)), &inc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if inc.UUID != "uuid-x" {
		t.Errorf("expected uuid-x, got %s", inc.UUID)
	}
}
