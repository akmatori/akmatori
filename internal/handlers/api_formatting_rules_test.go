//go:build cgo

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupFormattingRulesTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	if err := db.AutoMigrate(&database.FormattingRule{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	origDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = origDB })
}

func formattingRulesMux(h *APIHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/formatting-rules", h.handleFormattingRules)
	mux.HandleFunc("PUT /api/formatting-rules/reorder", h.handleFormattingRulesReorder)
	mux.HandleFunc("PUT /api/formatting-rules/{uuid}", h.handleFormattingRuleByUUID)
	mux.HandleFunc("DELETE /api/formatting-rules/{uuid}", h.handleFormattingRuleByUUID)
	return mux
}

func createRuleViaAPI(t *testing.T, mux *http.ServeMux, body string) database.FormattingRule {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/formatting-rules", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create rule: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var rule database.FormattingRule
	if err := json.NewDecoder(w.Body).Decode(&rule); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}
	return rule
}

func TestFormattingRules_CreateAndList(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	first := createRuleViaAPI(t, mux, `{"name":"alerts","match_source_kind":"alert"}`)
	if first.UUID == "" {
		t.Error("created rule must carry a server-generated UUID")
	}
	if !first.Enabled {
		t.Error("omitted enabled must default to true")
	}
	if first.MaxTokens != 1500 || first.Temperature != 0.2 {
		t.Errorf("defaults not applied: max_tokens=%d temperature=%v", first.MaxTokens, first.Temperature)
	}
	if first.Position != 0 {
		t.Errorf("first rule position = %d, want 0", first.Position)
	}

	second := createRuleViaAPI(t, mux, `{"name":"catch-all","enabled":false,"max_tokens":2000,"temperature":0}`)
	if second.Position != 1 {
		t.Errorf("second rule position = %d, want 1", second.Position)
	}
	if second.Enabled {
		t.Error("explicit enabled=false must persist")
	}
	if second.MaxTokens != 2000 || second.Temperature != 0 {
		t.Errorf("explicit max_tokens/temperature not persisted: %d/%v", second.MaxTokens, second.Temperature)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/formatting-rules", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var rules []database.FormattingRule
	if err := json.NewDecoder(w.Body).Decode(&rules); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rules) != 2 || rules[0].Name != "alerts" || rules[1].Name != "catch-all" {
		t.Errorf("unexpected list order/content: %+v", rules)
	}
}

func TestFormattingRules_CreateValidation(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	longPrompt := strings.Repeat("a", 8*1024+1)
	cases := []struct {
		name string
		body string
	}{
		{"missing name", `{"match_source_kind":"alert"}`},
		{"bad source kind", `{"name":"x","match_source_kind":"webhook"}`},
		{"bad trigger uuid", `{"name":"x","match_source_uuid":"not-a-uuid"}`},
		{"bad channel uuid", `{"name":"x","match_channel_uuid":"nope"}`},
		{"schema not json", `{"name":"x","output_schema_example":"not json"}`},
		{"schema not object", `{"name":"x","output_schema_example":"[1,2]"}`},
		{"max_tokens too large", `{"name":"x","max_tokens":9001}`},
		{"max_tokens zero", `{"name":"x","max_tokens":0}`},
		{"temperature out of range", `{"name":"x","temperature":3}`},
		{"oversized prompt", fmt.Sprintf(`{"name":"x","system_prompt":%q}`, longPrompt)},
		{"invalid json body", `{invalid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/formatting-rules", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	var count int64
	database.DB.Model(&database.FormattingRule{}).Count(&count)
	if count != 0 {
		t.Errorf("invalid creates must not persist rules, found %d", count)
	}
}

func TestFormattingRules_UpdateAndClearConditions(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	rule := createRuleViaAPI(t, mux, `{"name":"alerts","match_source_kind":"alert","match_last_skill":"netbox"}`)

	body := `{"name":"renamed","enabled":false,"match_last_skill":"","system_prompt":"P","max_tokens":2000}`
	req := httptest.NewRequest(http.MethodPut, "/api/formatting-rules/"+rule.UUID, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got database.FormattingRule
	if err := database.DB.Where("uuid = ?", rule.UUID).First(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Name != "renamed" || got.Enabled || got.SystemPrompt != "P" || got.MaxTokens != 2000 {
		t.Errorf("update not applied: %+v", got)
	}
	if got.MatchLastSkill != "" {
		t.Errorf("empty string must clear condition to wildcard, got %q", got.MatchLastSkill)
	}
	if got.MatchSourceKind != "alert" {
		t.Errorf("omitted field must be preserved, got %q", got.MatchSourceKind)
	}

	// Validation applies on update too.
	req = httptest.NewRequest(http.MethodPut, "/api/formatting-rules/"+rule.UUID, strings.NewReader(`{"match_source_kind":"bogus"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad kind on update, got %d", w.Code)
	}

	// Unknown rule → 404.
	req = httptest.NewRequest(http.MethodPut, "/api/formatting-rules/00000000-0000-0000-0000-00000000dead", strings.NewReader(`{"name":"x"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown rule, got %d", w.Code)
	}
}

func TestFormattingRules_Delete(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	rule := createRuleViaAPI(t, mux, `{"name":"to delete"}`)

	req := httptest.NewRequest(http.MethodDelete, "/api/formatting-rules/"+rule.UUID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	var count int64
	database.DB.Model(&database.FormattingRule{}).Count(&count)
	if count != 0 {
		t.Errorf("rule not deleted, %d remain", count)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/formatting-rules/"+rule.UUID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for double delete, got %d", w.Code)
	}
}

func TestFormattingRules_Reorder(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	a := createRuleViaAPI(t, mux, `{"name":"a"}`)
	b := createRuleViaAPI(t, mux, `{"name":"b"}`)
	c := createRuleViaAPI(t, mux, `{"name":"c"}`)

	body := fmt.Sprintf(`{"uuids":[%q,%q,%q]}`, c.UUID, a.UUID, b.UUID)
	req := httptest.NewRequest(http.MethodPut, "/api/formatting-rules/reorder", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reorder: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var ordered []database.FormattingRule
	if err := json.NewDecoder(w.Body).Decode(&ordered); err != nil {
		t.Fatalf("decode reorder response: %v", err)
	}
	names := []string{ordered[0].Name, ordered[1].Name, ordered[2].Name}
	if names[0] != "c" || names[1] != "a" || names[2] != "b" {
		t.Errorf("unexpected order after reorder: %v", names)
	}

	// Set mismatch → 400 and order unchanged.
	for _, bad := range []string{
		fmt.Sprintf(`{"uuids":[%q,%q]}`, a.UUID, b.UUID),                        // missing one
		fmt.Sprintf(`{"uuids":[%q,%q,%q]}`, a.UUID, b.UUID, "not-a-known-uuid"), // unknown
		fmt.Sprintf(`{"uuids":[%q,%q,%q,%q]}`, a.UUID, b.UUID, c.UUID, c.UUID),  // duplicate
		fmt.Sprintf(`{"uuids":[%q,%q,%q]}`, a.UUID, a.UUID, b.UUID),             // duplicate replacing one
	} {
		req = httptest.NewRequest(http.MethodPut, "/api/formatting-rules/reorder", strings.NewReader(bad))
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for mismatched set %s, got %d", bad, w.Code)
		}
	}

	rules, err := database.ListFormattingRules()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if rules[0].Name != "c" {
		t.Errorf("failed reorder attempts must not change order, got first=%q", rules[0].Name)
	}
}

func TestFormattingRules_ExpressionCreateAndValidation(t *testing.T) {
	setupFormattingRulesTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mux := formattingRulesMux(h)

	// Valid expression rule.
	rule := createRuleViaAPI(t, mux,
		`{"name":"expr rule","match_expression":"source_kind == \"alert\" && (channel == \"c-1\" || skill == \"netbox\")"}`)
	if rule.MatchExpression == "" {
		t.Error("match_expression not persisted")
	}

	// Invalid syntax → 400 with a helpful message.
	req := httptest.NewRequest(http.MethodPost, "/api/formatting-rules",
		strings.NewReader(`{"name":"bad","match_expression":"skill == netbox"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid expression, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "quoted") {
		t.Errorf("expected quoting hint in error, got %s", w.Body.String())
	}

	// Expression + simple fields together → 400.
	req = httptest.NewRequest(http.MethodPost, "/api/formatting-rules",
		strings.NewReader(`{"name":"both","match_expression":"skill == \"x\"","match_source_kind":"alert"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when expression and simple fields are both set, got %d", w.Code)
	}

	// Update: switching an expression rule back to simple fields requires
	// clearing the expression in the same request.
	req = httptest.NewRequest(http.MethodPut, "/api/formatting-rules/"+rule.UUID,
		strings.NewReader(`{"match_expression":"","match_source_kind":"cron"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 switching to simple fields, got %d: %s", w.Code, w.Body.String())
	}
	var got database.FormattingRule
	if err := database.DB.Where("uuid = ?", rule.UUID).First(&got).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.MatchExpression != "" || got.MatchSourceKind != "cron" {
		t.Errorf("switch to simple fields not applied: %+v", got)
	}

	// Update that sets an expression while simple fields remain → 400.
	req = httptest.NewRequest(http.MethodPut, "/api/formatting-rules/"+rule.UUID,
		strings.NewReader(`{"match_expression":"skill == \"x\""}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when update leaves both sides set, got %d", w.Code)
	}
}
