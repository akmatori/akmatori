package events

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
	if err := db.AutoMigrate(&database.Event{}, &database.Incident{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

var base = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func seed(t *testing.T, db *gorm.DB, e database.Event) {
	t.Helper()
	if e.Classification == "" {
		e.Classification = "llm"
	}
	if e.SourceKind == "" {
		e.SourceKind = "webhook"
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func search(t *testing.T, tool *EventsTool, args map[string]interface{}) searchResponse {
	t.Helper()
	out, err := tool.Search(context.Background(), "", args)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var resp searchResponse
	if err := json.Unmarshal([]byte(out.(string)), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func uuids(resp searchResponse) string {
	var ids []string
	for _, e := range resp.Events {
		ids = append(ids, e.UUID)
	}
	return strings.Join(ids, ",")
}

func TestSearch_Filters(t *testing.T) {
	db := newTestDB(t)
	tool := NewEventsTool(db, log.Default())
	seed(t, db, database.Event{UUID: "deploy", Kind: "change", Title: "Deploy nginx 1.27", Host: "wa2-edge-1", OccurredAt: base.Add(-30 * time.Minute)})
	seed(t, db, database.Event{UUID: "alert", Kind: "alert", Title: "5xx rate high", Host: "wa2-edge-1", OccurredAt: base, Summary: "502 storm"})
	seed(t, db, database.Event{UUID: "old", Kind: "change", Title: "Kernel update", Host: "fra1", OccurredAt: base.Add(-48 * time.Hour)})
	seed(t, db, database.Event{UUID: "cron", Kind: "request", SourceKind: "cron", Title: "Cron: nightly", OccurredAt: base.Add(-time.Hour)})

	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"newest first", map[string]interface{}{}, "alert,deploy,cron,old"},
		{"changes before the alert", map[string]interface{}{"kind": "change", "since": "2026-10-27T11:00:00Z", "until": "2026-10-27T12:00:00Z"}, "deploy"},
		{"host substring", map[string]interface{}{"host": "WA2"}, "alert,deploy"},
		{"text in summary", map[string]interface{}{"text": "storm"}, "alert"},
		{"source", map[string]interface{}{"source": "cron"}, "cron"},
		{"unix since", map[string]interface{}{"since": float64(base.Add(-90 * time.Minute).Unix())}, "alert,deploy,cron"},
		{"limit", map[string]interface{}{"limit": float64(1)}, "alert"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uuids(search(t, tool, tt.args)); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}

	if _, err := tool.Search(context.Background(), "", map[string]interface{}{"kind": "bogus"}); err == nil {
		t.Fatal("unknown kind must be an error")
	}
}

func TestGet_TruncatesPayload(t *testing.T) {
	db := newTestDB(t)
	tool := NewEventsTool(db, log.Default())
	seed(t, db, database.Event{UUID: "small", Kind: "change", Title: "x", OccurredAt: base,
		RawPayload: database.JSONB{"original_message": "deployed"}, ClassificationReasoning: "deploy notice"})
	seed(t, db, database.Event{UUID: "big", Kind: "alert", Title: "y", OccurredAt: base,
		RawPayload: database.JSONB{"blob": strings.Repeat("a", maxRawPayload*2)}})

	out, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "small"})
	if err != nil {
		t.Fatal(err)
	}
	var small map[string]interface{}
	_ = json.Unmarshal([]byte(out.(string)), &small)
	if small["classification_reasoning"] != "deploy notice" || small["raw_payload"].(map[string]interface{})["original_message"] != "deployed" {
		t.Fatalf("small = %v", small)
	}

	out, err = tool.Get(context.Background(), "", map[string]interface{}{"uuid": "big"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.(string)) > maxRawPayload+2000 || !strings.Contains(out.(string), `"raw_payload_truncated":true`) {
		t.Fatalf("big payload not truncated (len %d)", len(out.(string)))
	}

	if _, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "missing"}); err == nil {
		t.Fatal("missing event must be an error")
	}
}

func seedRun(t *testing.T, db *gorm.DB, uuid, kind string) {
	t.Helper()
	if err := db.Create(&database.Incident{UUID: uuid, Source: "cron", SourceKind: kind, SourceUUID: "job-1",
		Context: database.JSONB{"cron_job_name": "disk check"}}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestReportAlert_CronRunQueuesAlertEvent(t *testing.T) {
	db := newTestDB(t)
	tool := NewEventsTool(db, log.Default())
	seedRun(t, db, "run-1", "cron")

	out, err := tool.ReportAlert(context.Background(), "run-1", map[string]interface{}{
		"alert_name": "Disk usage above 90%", "summary": "/var at 94% on db1", "host": "DB1", "severity": "High",
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp reportResponse
	_ = json.Unmarshal([]byte(out.(string)), &resp)

	var ev database.Event
	if err := db.First(&ev, "uuid = ?", resp.EventUUID).Error; err != nil {
		t.Fatal(err)
	}
	if !ev.PendingDispatch || ev.Kind != "alert" || ev.SourceKind != "cron" || ev.SourceUUID != "job-1" ||
		ev.Status != "firing" || ev.Severity != "high" || ev.Classification != "source" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.ExternalID != "cron:job-1:disk usage above 90%:db1" {
		t.Fatalf("external id = %q, want a stable job+alert+host identity", ev.ExternalID)
	}
	if ev.RawPayload["reported_by_run"] != "run-1" || ev.RawPayload["cron_job_name"] != "disk check" {
		t.Fatalf("payload = %v", ev.RawPayload)
	}
}

func TestReportAlert_Guards(t *testing.T) {
	db := newTestDB(t)
	tool := NewEventsTool(db, log.Default())
	seedRun(t, db, "run-alert", "alert")
	seedRun(t, db, "run-cron", "cron")
	ok := map[string]interface{}{"alert_name": "x", "summary": "y"}

	if _, err := tool.ReportAlert(context.Background(), "run-alert", ok); err == nil || !strings.Contains(err.Error(), "only for cron runs") {
		t.Fatalf("non-cron run: err = %v", err)
	}
	if _, err := tool.ReportAlert(context.Background(), "", ok); err == nil {
		t.Fatal("no run: want error")
	}
	if _, err := tool.ReportAlert(context.Background(), "run-cron", map[string]interface{}{"alert_name": "x"}); err == nil {
		t.Fatal("missing summary: want error")
	}
	if _, err := tool.ReportAlert(context.Background(), "run-cron", map[string]interface{}{"alert_name": "x", "summary": "y", "severity": "sev1"}); err == nil {
		t.Fatal("bad severity: want error")
	}
	for i := 0; i < maxReportsPerRun; i++ {
		if _, err := tool.ReportAlert(context.Background(), "run-cron", ok); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	if _, err := tool.ReportAlert(context.Background(), "run-cron", ok); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over the cap: err = %v", err)
	}
}
