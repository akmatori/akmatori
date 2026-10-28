package services

import (
	"context"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func rule(name string, position int, enabled bool, mutate func(*database.FormattingRule)) database.FormattingRule {
	r := database.FormattingRule{
		UUID:     "uuid-" + name,
		Name:     name,
		Enabled:  enabled,
		Position: position,
	}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func TestMatchFormattingRule_WildcardMatchesAnything(t *testing.T) {
	rules := []database.FormattingRule{rule("catch-all", 0, true, nil)}

	for _, flow := range []FormatFlow{
		{},
		{SourceKind: "alert", TriggerUUID: "t-1", ChannelUUID: "c-1", LastSkill: "victoria-metrics"},
	} {
		if got := MatchFormattingRule(rules, flow); got == nil || got.Name != "catch-all" {
			t.Errorf("flow %+v: expected catch-all match, got %v", flow, got)
		}
	}
}

func TestMatchFormattingRule_ConditionsAreANDed(t *testing.T) {
	rules := []database.FormattingRule{
		rule("specific", 0, true, func(r *database.FormattingRule) {
			r.MatchSourceKind = "alert"
			r.MatchChannelUUID = "chan-1"
		}),
	}

	if got := MatchFormattingRule(rules, FormatFlow{SourceKind: "alert", ChannelUUID: "chan-1"}); got == nil {
		t.Error("expected match when all conditions equal")
	}
	if got := MatchFormattingRule(rules, FormatFlow{SourceKind: "alert", ChannelUUID: "chan-2"}); got != nil {
		t.Error("expected no match when one condition differs")
	}
	if got := MatchFormattingRule(rules, FormatFlow{SourceKind: "alert"}); got != nil {
		t.Error("expected no match when flow field empty but condition set")
	}
}

func TestMatchFormattingRule_FirstByPositionWins(t *testing.T) {
	// Rules arrive pre-sorted (position ASC) from ListFormattingRules.
	rules := []database.FormattingRule{
		rule("first", 0, true, func(r *database.FormattingRule) { r.MatchSourceKind = "alert" }),
		rule("second", 1, true, nil),
	}

	if got := MatchFormattingRule(rules, FormatFlow{SourceKind: "alert"}); got == nil || got.Name != "first" {
		t.Errorf("expected first rule to win, got %v", got)
	}
	if got := MatchFormattingRule(rules, FormatFlow{SourceKind: "cron"}); got == nil || got.Name != "second" {
		t.Errorf("expected fall-through to second rule, got %v", got)
	}
}

func TestMatchFormattingRule_DisabledRulesSkipped(t *testing.T) {
	rules := []database.FormattingRule{
		rule("disabled", 0, false, nil),
		rule("enabled", 1, true, nil),
	}

	if got := MatchFormattingRule(rules, FormatFlow{}); got == nil || got.Name != "enabled" {
		t.Errorf("expected disabled rule to be skipped, got %v", got)
	}
	if got := MatchFormattingRule(rules[:1], FormatFlow{}); got != nil {
		t.Errorf("expected nil when only rule is disabled, got %v", got)
	}
}

func TestMatchFormattingRule_TrimsConditionsAndValues(t *testing.T) {
	rules := []database.FormattingRule{
		rule("trimmed", 0, true, func(r *database.FormattingRule) { r.MatchLastSkill = "  netbox  " }),
	}
	if got := MatchFormattingRule(rules, FormatFlow{LastSkill: "netbox"}); got == nil {
		t.Error("expected trimmed condition to match")
	}
}

func TestMatchFormattingRule_EmptyList(t *testing.T) {
	if got := MatchFormattingRule(nil, FormatFlow{SourceKind: "alert"}); got != nil {
		t.Errorf("expected nil for empty rule list, got %v", got)
	}
}

func TestBuildFormatFlow_LoadsRunIdentity(t *testing.T) {
	setupFormatterTestDB(t)
	run := database.AgentRun{
		RunID:         "run-1",
		ScopeUUID:     "inc-1",
		IncidentUUID:  "inc-1",
		SourceKind:    database.IncidentSourceKindAlert,
		SourceUUID:    "trigger-1",
		LastSkillUsed: "victoria-metrics",
	}
	if err := database.DB.Create(&run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}

	flow := BuildFormatFlow("run-1", "chan-9")
	if flow.SourceKind != "alert" || flow.TriggerUUID != "trigger-1" || flow.ChannelUUID != "chan-9" || flow.LastSkill != "victoria-metrics" {
		t.Errorf("unexpected flow: %+v", flow)
	}
}

func TestBuildFormatFlow_MissingRunKeepsChannel(t *testing.T) {
	setupFormatterTestDB(t)
	flow := BuildFormatFlow("no-such-run", "chan-9")
	if flow.ChannelUUID != "chan-9" || flow.SourceKind != "" || flow.TriggerUUID != "" || flow.LastSkill != "" {
		t.Errorf("unexpected flow for missing run: %+v", flow)
	}
}

func TestFormatForFlow_NoMatchPassthroughForAllKinds(t *testing.T) {
	setupFormatterTestDB(t)
	seedFormatterSettings(t, database.FormattingRule{
		Enabled:         true,
		MatchSourceKind: database.IncidentSourceKindAlert,
		MatchLastSkill:  "netbox",
	})
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		t.Fatal("LLM caller must not be invoked when no rule matches")
		return "", nil
	}}
	f := NewResponseFormatter(caller)

	for _, kind := range []string{
		database.IncidentSourceKindAlert, // skill condition unmet
		database.IncidentSourceKindCron,
		database.IncidentSourceKindSlackMention,
		database.IncidentSourceKindManual,
		database.IncidentSourceKindProposal,
	} {
		got := f.FormatForFlow(context.Background(), "raw output", "log", FormatFlow{SourceKind: kind})
		if got != "raw output" {
			t.Errorf("kind %s: expected passthrough, got %q", kind, got)
		}
	}
	if caller.callCount() != 0 {
		t.Errorf("expected 0 LLM calls, got %d", caller.callCount())
	}
}

func TestFormatForFlow_MatchedRuleConfigUsed(t *testing.T) {
	setupFormatterTestDB(t)
	// Two rules: a skill-scoped one first, a catch-all second.
	if err := database.DB.Exec("DELETE FROM formatting_rules").Error; err != nil {
		t.Fatalf("clear rules: %v", err)
	}
	skillRule := database.FormattingRule{
		UUID: "u-skill", Name: "netbox rule", Enabled: true, Position: 0,
		MatchLastSkill:      "netbox",
		SystemPrompt:        "NETBOX PROMPT",
		OutputSchemaExample: `{"device_summary":"text"}`,
		MaxTokens:           777,
		Temperature:         0.9,
	}
	catchAll := database.FormattingRule{
		UUID: "u-all", Name: "catch-all", Enabled: true, Position: 1,
		SystemPrompt: "GENERIC PROMPT",
	}
	if err := database.DB.Create(&skillRule).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := database.DB.Create(&catchAll).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedFormatterLLM(t, defaultLLMForFormatter())

	caller := &fakeOneShotLLMCaller{respond: func(ctx context.Context) (string, error) {
		return `{"device_summary":"Switch rebooted."}`, nil
	}}
	f := NewResponseFormatter(caller)

	got := f.FormatForFlow(context.Background(), "raw", "log", FormatFlow{SourceKind: "alert", LastSkill: "netbox"})
	if !strings.Contains(got, "*Device Summary:*") || !strings.Contains(got, "Switch rebooted.") {
		t.Errorf("expected netbox-rule schema rendering, got %q", got)
	}
	if !strings.Contains(caller.lastSystem, "NETBOX PROMPT") {
		t.Errorf("expected matched rule's prompt, got %q", caller.lastSystem)
	}
	if caller.lastMaxTok != 777 || caller.lastTemp != 0.9 {
		t.Errorf("expected rule's max_tokens/temperature, got %d/%v", caller.lastMaxTok, caller.lastTemp)
	}

	// A flow that misses the skill rule falls through to the catch-all.
	caller.respond = func(ctx context.Context) (string, error) {
		return `{"status":"resolved","summary":"ok","actions_taken":[],"recommendations":[]}`, nil
	}
	got = f.FormatForFlow(context.Background(), "raw", "log", FormatFlow{SourceKind: "cron"})
	if !strings.Contains(caller.lastSystem, "GENERIC PROMPT") {
		t.Errorf("expected catch-all prompt for cron flow, got %q", caller.lastSystem)
	}
	if !strings.Contains(got, "*Status:*") {
		t.Errorf("expected default-schema rendering from catch-all, got %q", got)
	}
}
