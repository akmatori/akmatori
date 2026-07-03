package proposals

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

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
	if err := db.AutoMigrate(
		&database.Proposal{},
		&database.Incident{},
		&database.Runbook{},
		&database.Memory{},
		&database.CronJob{},
		&database.CronJobTool{},
		&database.ToolInstance{},
		&database.ToolType{},
		&database.Skill{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newTool(db *gorm.DB) *ProposalsTool {
	return NewProposalsTool(db, log.Default())
}

func mustJSON(t *testing.T, result interface{}) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result.(string)), &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

func runbookContent(title, content string) map[string]interface{} {
	return map[string]interface{}{"title": title, "content": content}
}

// ---- Create ----

func TestCreate_RunbookNew(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	// Seed one real incident so the source-uuid guard keeps it and drops the fake.
	db.Create(&database.Incident{UUID: "real-incident", Source: "test", Status: "completed"})

	result, err := tool.Create(context.Background(), "eval-run-1", map[string]interface{}{
		"kind":                  "runbook_new",
		"title":                 "Add nginx 502 runbook",
		"reasoning":             "Two incidents lacked SOP coverage",
		"proposed_content":      runbookContent("Nginx 502 storms", "1. check upstreams"),
		"source_incident_uuids": []interface{}{"real-incident", "hallucinated-uuid"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := mustJSON(t, result)
	if resp["deduplicated"] != false {
		t.Errorf("expected deduplicated=false")
	}
	if resp["uuid"] == "" {
		t.Errorf("expected uuid in response")
	}
	dropped, _ := resp["dropped_source_incident_uuids"].([]interface{})
	if len(dropped) != 1 || dropped[0] != "hallucinated-uuid" {
		t.Errorf("expected hallucinated uuid to be dropped, got %v", dropped)
	}

	var row database.Proposal
	if err := db.Where("kind = ?", "runbook_new").First(&row).Error; err != nil {
		t.Fatalf("row not created: %v", err)
	}
	if row.Status != "pending" || row.CreatedBy != "evaluator" || row.EvaluationRunUUID != "eval-run-1" {
		t.Errorf("unexpected row fields: %+v", row)
	}
	var uuids struct {
		UUIDs []string `json:"uuids"`
	}
	b, _ := json.Marshal(row.SourceIncidentUUIDs)
	_ = json.Unmarshal(b, &uuids)
	if len(uuids.UUIDs) != 1 || uuids.UUIDs[0] != "real-incident" {
		t.Errorf("expected only the real incident kept, got %v", uuids.UUIDs)
	}
}

func TestCreate_InvalidKind(t *testing.T) {
	tool := newTool(newTestDB(t))
	_, err := tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "nonsense",
		"title":            "x",
		"proposed_content": runbookContent("a", "b"),
	})
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestCreate_ContentShapeValidation(t *testing.T) {
	tool := newTool(newTestDB(t))
	cases := []struct {
		name    string
		kind    string
		content map[string]interface{}
	}{
		{"runbook missing content", "runbook_new", map[string]interface{}{"title": "t"}},
		{"memory bad type", "memory_new", map[string]interface{}{"scope": "global", "type": "bogus", "name": "n", "body": "b"}},
		{"memory missing body", "memory_new", map[string]interface{}{"scope": "global", "type": "host", "name": "n"}},
		{"cron bad schedule", "cron_new", map[string]interface{}{"name": "j", "schedule": "hourly", "prompt": "p"}},
		{"cron bad tools", "cron_new", map[string]interface{}{"name": "j", "schedule": "0 5 * * *", "prompt": "p", "tool_logical_names": []interface{}{7}}},
		{"skill missing prompt", "skill_prompt_update", map[string]interface{}{"skill_name": "s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Create(context.Background(), "", map[string]interface{}{
				"kind":             tc.kind,
				"title":            "some title",
				"target_ref":       "whatever",
				"proposed_content": tc.content,
			})
			if err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestCreate_UpdateKindRequiresResolvableTarget(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	// Missing target_ref entirely.
	_, err := tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "runbook_update",
		"title":            "Fix runbook",
		"proposed_content": runbookContent("a", "b"),
	})
	if err == nil || !strings.Contains(err.Error(), "target_ref is required") {
		t.Fatalf("expected target_ref requirement, got %v", err)
	}

	// Unresolvable runbook ID.
	_, err = tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "runbook_update",
		"title":            "Fix runbook",
		"target_ref":       "42",
		"proposed_content": runbookContent("a", "b"),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestCreate_RunbookUpdateFillsSnapshot(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)
	db.Create(&database.Runbook{Title: "Old title", Content: "Old content"})

	var rb database.Runbook
	db.First(&rb)

	result, err := tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "runbook_update",
		"title":            "Refresh the runbook",
		"target_ref":       "1",
		"proposed_content": runbookContent("New title", "New content"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = mustJSON(t, result)

	var row database.Proposal
	db.Where("kind = ?", "runbook_update").First(&row)
	var snap map[string]string
	if err := json.Unmarshal([]byte(row.CurrentSnapshot), &snap); err != nil {
		t.Fatalf("snapshot not JSON: %v", err)
	}
	if snap["title"] != "Old title" || snap["content"] != "Old content" {
		t.Errorf("snapshot should capture live target, got %v", snap)
	}
}

func TestCreate_SkillPromptUpdate(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)
	db.Create(&database.Skill{Name: "linux-engineer", IsSystem: false, Enabled: true})
	db.Create(&database.Skill{Name: "cron-agent", IsSystem: true, Enabled: true})

	// System skill target must be rejected.
	_, err := tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "skill_prompt_update",
		"title":            "Tweak cron agent",
		"target_ref":       "cron-agent",
		"proposed_content": map[string]interface{}{"skill_name": "cron-agent", "prompt": "new"},
	})
	if err == nil || !strings.Contains(err.Error(), "system skill") {
		t.Fatalf("expected system-skill rejection, got %v", err)
	}

	// skill_name / target_ref mismatch must be rejected.
	_, err = tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "skill_prompt_update",
		"title":            "Tweak skill",
		"target_ref":       "linux-engineer",
		"proposed_content": map[string]interface{}{"skill_name": "other", "prompt": "new"},
	})
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected mismatch rejection, got %v", err)
	}

	// Valid target: snapshot stays empty (API backfills lazily).
	result, err := tool.Create(context.Background(), "", map[string]interface{}{
		"kind":             "skill_prompt_update",
		"title":            "Tweak skill",
		"target_ref":       "linux-engineer",
		"proposed_content": map[string]interface{}{"skill_name": "linux-engineer", "prompt": "new prompt"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = mustJSON(t, result)
	var row database.Proposal
	db.Where("kind = ?", "skill_prompt_update").First(&row)
	if row.CurrentSnapshot != "" {
		t.Errorf("skill snapshot should be empty for lazy backfill, got %q", row.CurrentSnapshot)
	}
}

func TestCreate_Dedup(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)
	db.Create(&database.Runbook{Title: "Old", Content: "Old"})

	args := map[string]interface{}{
		"kind":             "runbook_update",
		"title":            "Refresh runbook",
		"target_ref":       "1",
		"proposed_content": runbookContent("New", "New"),
	}
	first, err := tool.Create(context.Background(), "", args)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstResp := mustJSON(t, first)

	second, err := tool.Create(context.Background(), "", args)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	secondResp := mustJSON(t, second)
	if secondResp["deduplicated"] != true {
		t.Errorf("expected dedup on same (kind,target_ref)")
	}
	if secondResp["uuid"] != firstResp["uuid"] {
		t.Errorf("dedup should return the existing uuid")
	}

	// New kinds dedup by case-insensitive title.
	newArgs := map[string]interface{}{
		"kind":             "runbook_new",
		"title":            "Add DNS runbook",
		"proposed_content": runbookContent("DNS", "steps"),
	}
	if _, err := tool.Create(context.Background(), "", newArgs); err != nil {
		t.Fatalf("create new: %v", err)
	}
	newArgs["title"] = "ADD dns RUNBOOK"
	dup, err := tool.Create(context.Background(), "", newArgs)
	if err != nil {
		t.Fatalf("dup create: %v", err)
	}
	if mustJSON(t, dup)["deduplicated"] != true {
		t.Errorf("expected title-based dedup for *_new kinds")
	}
}

// ---- List / Get ----

func TestList_DefaultsToPendingSummaries(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)
	db.Create(&database.Proposal{UUID: "p1", Kind: "runbook_new", Status: "pending", Title: "One", ProposedContent: "{}"})
	db.Create(&database.Proposal{UUID: "p2", Kind: "runbook_new", Status: "rejected", Title: "Two", ProposedContent: "{}"})

	result, err := tool.List(context.Background(), "", map[string]interface{}{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	resp := mustJSON(t, result)
	if int(resp["count"].(float64)) != 1 {
		t.Fatalf("expected only the pending row, got %v", resp["count"])
	}
	row := resp["proposals"].([]interface{})[0].(map[string]interface{})
	if row["uuid"] != "p1" {
		t.Errorf("expected p1, got %v", row["uuid"])
	}
	if _, hasContent := row["proposed_content"]; hasContent {
		t.Errorf("summary rows must not include proposed_content")
	}

	// include_content=true returns bodies.
	result, _ = tool.List(context.Background(), "", map[string]interface{}{"include_content": true})
	resp = mustJSON(t, result)
	row = resp["proposals"].([]interface{})[0].(map[string]interface{})
	if _, hasContent := row["proposed_content"]; !hasContent {
		t.Errorf("include_content should return proposed_content")
	}
}

func TestGet_NotFound(t *testing.T) {
	tool := newTool(newTestDB(t))
	if _, err := tool.Get(context.Background(), "", map[string]interface{}{"uuid": "missing"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

// ---- UpdateDraft ----

func TestUpdateDraft_PendingOnlyAndValidated(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)
	db.Create(&database.Proposal{UUID: "p1", Kind: "runbook_new", Status: "pending", Title: "One", ProposedContent: `{"title":"a","content":"b"}`})
	db.Create(&database.Proposal{UUID: "p2", Kind: "runbook_new", Status: "approved", Title: "Two", ProposedContent: `{"title":"a","content":"b"}`})

	// Non-pending row is immutable.
	_, err := tool.UpdateDraft(context.Background(), "", map[string]interface{}{
		"uuid": "p2", "title": "New",
	})
	if err == nil || !strings.Contains(err.Error(), "only pending") {
		t.Fatalf("expected pending-only guard, got %v", err)
	}

	// Content shape is re-validated on update.
	_, err = tool.UpdateDraft(context.Background(), "", map[string]interface{}{
		"uuid":             "p1",
		"proposed_content": map[string]interface{}{"title": "only-title"},
	})
	if err == nil {
		t.Fatal("expected shape validation on update_draft")
	}

	// Valid revision persists.
	result, err := tool.UpdateDraft(context.Background(), "", map[string]interface{}{
		"uuid":             "p1",
		"title":            "Better title",
		"proposed_content": runbookContent("x", "y"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	resp := mustJSON(t, result)
	if resp["title"] != "Better title" {
		t.Errorf("expected updated title, got %v", resp["title"])
	}
	var row database.Proposal
	db.Where("uuid = ?", "p1").First(&row)
	if !strings.Contains(row.ProposedContent, `"x"`) {
		t.Errorf("content not persisted: %s", row.ProposedContent)
	}
}

// ---- ListCronJobs ----

func TestListCronJobs(t *testing.T) {
	db := newTestDB(t)
	tool := newTool(db)

	tt := database.ToolType{Name: "incidents"}
	db.Create(&tt)
	ti := database.ToolInstance{ToolTypeID: tt.ID, Name: "Incidents", LogicalName: "incidents", Enabled: true}
	db.Create(&ti)
	job := database.CronJob{UUID: "cj1", Name: "digest", Schedule: "0 8 * * *", Prompt: "do it", Enabled: true}
	db.Create(&job)
	db.Create(&database.CronJobTool{CronJobID: job.ID, ToolInstanceID: ti.ID})

	result, err := tool.ListCronJobs(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("list cron jobs: %v", err)
	}
	resp := mustJSON(t, result)
	if int(resp["count"].(float64)) != 1 {
		t.Fatalf("expected 1 cron job, got %v", resp["count"])
	}
	row := resp["cron_jobs"].([]interface{})[0].(map[string]interface{})
	if row["uuid"] != "cj1" || row["schedule"] != "0 8 * * *" {
		t.Errorf("unexpected cron row: %v", row)
	}
	tools := row["tool_logical_names"].([]interface{})
	if len(tools) != 1 || tools[0] != "incidents" {
		t.Errorf("expected tool logical names, got %v", tools)
	}
}
