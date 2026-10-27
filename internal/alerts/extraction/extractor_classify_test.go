package extraction

import (
	"context"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

func activeLLMSettings() (*database.LLMSettings, error) {
	return &database.LLMSettings{
		APIKey: "test-key", Provider: database.LLMProviderOpenAI, Model: "gpt-4o", Enabled: true, Active: true,
	}, nil
}

var listenerClasses = []string{database.EventKindAlert, database.EventKindChange, database.EventKindInfo}

func TestExtractAndClassify_OneCallGivesFieldsAndClass(t *testing.T) {
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"nginx deploy","severity":"info","status":"firing","summary":"nginx 1.27 to wa2",
			"target_host":"wa2","event_kind":"change","event_confidence":0.93,"event_reasoning":"deploy notice"}`, nil
	}}
	extractor := NewAlertExtractorWithDeps(caller, activeLLMSettings)

	alert, verdict := extractor.ExtractAndClassify(context.Background(), "deployed nginx 1.27 to wa2", "", listenerClasses, database.EventKindAlert)

	if caller.callCount() != 1 {
		t.Fatalf("LLM calls = %d, want 1", caller.callCount())
	}
	if alert.AlertName != "nginx deploy" || alert.TargetHost != "wa2" {
		t.Errorf("fields not extracted: %+v", alert)
	}
	if verdict.Kind != database.EventKindChange || verdict.Classification != database.EventClassificationLLM {
		t.Errorf("verdict = %+v, want llm change", verdict)
	}
	if !strings.Contains(caller.lastUser, "event_kind: one of alert, change, info") {
		t.Errorf("prompt lacks the classification addendum:\n%s", caller.lastUser)
	}
	if caller.lastMaxTok != 500+classifyExtraTokens {
		t.Errorf("max tokens = %d, want %d", caller.lastMaxTok, 500+classifyExtraTokens)
	}
}

func TestExtractAndClassify_CustomPromptKeepsAddendum(t *testing.T) {
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) {
		return `{"alert_name":"x","event_kind":"info","event_confidence":0.8,"event_reasoning":"chat"}`, nil
	}}
	extractor := NewAlertExtractorWithDeps(caller, activeLLMSettings)

	_, verdict := extractor.ExtractAndClassify(context.Background(), "hello team", "Channel rules. Message: %s", listenerClasses, database.EventKindAlert)

	if !strings.HasPrefix(caller.lastUser, "Channel rules. Message: hello team") || !strings.Contains(caller.lastUser, "event_kind") {
		t.Errorf("custom prompt or addendum missing:\n%s", caller.lastUser)
	}
	if verdict.Kind != database.EventKindInfo {
		t.Errorf("verdict = %+v, want info", verdict)
	}
}

func TestExtractAndClassify_Fallbacks(t *testing.T) {
	tests := []struct {
		name     string
		answer   string
		fallback string
	}{
		{"no class fields", `{"alert_name":"x"}`, database.EventKindAlert},
		{"class not allowed", `{"alert_name":"x","event_kind":"request","event_confidence":0.99}`, database.EventKindAlert},
		{"low confidence", `{"alert_name":"x","event_kind":"info","event_confidence":0.4}`, database.EventKindAlert},
		{"invalid json", `not json`, database.EventKindAlert},
		{"request fallback for mentions", `not json`, database.EventKindRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return tt.answer, nil }}
			extractor := NewAlertExtractorWithDeps(caller, activeLLMSettings)
			alert, verdict := extractor.ExtractAndClassify(context.Background(), "some text", "", listenerClasses, tt.fallback)
			if alert == nil {
				t.Fatal("alert must never be nil")
			}
			if verdict.Kind != tt.fallback || verdict.Classification != database.EventClassificationFallback {
				t.Errorf("verdict = %+v, want fallback %s", verdict, tt.fallback)
			}
		})
	}
}

func TestExtractAndClassify_NoCallerFallsBack(t *testing.T) {
	extractor := NewAlertExtractor(nil)
	alert, verdict := extractor.ExtractAndClassify(context.Background(), "wa2 is down", "", listenerClasses, database.EventKindAlert)
	if alert.AlertName != "wa2 is down" || verdict.Classification != database.EventClassificationFallback || verdict.Kind != database.EventKindAlert {
		t.Errorf("got %+v / %+v", alert, verdict)
	}
}

func TestExtractWithPrompt_HasNoClassification(t *testing.T) {
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return `{"alert_name":"x"}`, nil }}
	extractor := NewAlertExtractorWithDeps(caller, activeLLMSettings)
	if _, err := extractor.Extract(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(caller.lastUser, "event_kind") || caller.lastMaxTok != 500 {
		t.Errorf("plain extraction changed: max=%d prompt=%s", caller.lastMaxTok, caller.lastUser)
	}
}

func TestExtractAndClassify_GateOffExtractsWithoutClass(t *testing.T) {
	db := testhelpers.NewGlobalSQLiteDB(t, &database.GeneralSettings{})
	off := false
	db.Create(&database.GeneralSettings{EventClassificationEnabled: &off})
	caller := &fakeOneShotLLMCaller{respond: func(context.Context) (string, error) { return `{"alert_name":"disk full","target_host":"db1"}`, nil }}
	extractor := NewAlertExtractorWithDeps(caller, activeLLMSettings)

	alert, verdict := extractor.ExtractAndClassify(context.Background(), "disk full on db1", "", listenerClasses, database.EventKindAlert)

	if alert.TargetHost != "db1" || strings.Contains(caller.lastUser, "event_kind") || caller.lastMaxTok != 500 {
		t.Errorf("extraction changed with gate off: host=%q max=%d", alert.TargetHost, caller.lastMaxTok)
	}
	if verdict.Kind != database.EventKindAlert || verdict.Classification != database.EventClassificationFallback ||
		!strings.Contains(verdict.Reasoning, "turned off") {
		t.Errorf("verdict = %+v", verdict)
	}
}
