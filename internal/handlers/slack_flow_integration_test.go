package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ========================================
// Slack Event Processing Integration Tests
// ========================================

// TestSlackFlow_EventClassification_Comprehensive tests comprehensive event classification
func TestSlackFlow_EventClassification_Comprehensive(t *testing.T) {
	alertChannels := map[string]*database.AlertSourceInstance{
		"C_ALERTS":     {UUID: "uuid-1", Name: "alerts"},
		"C_MONITORING": {UUID: "uuid-2", Name: "monitoring"},
	}

	tests := []struct {
		name     string
		event    *slackevents.MessageEvent
		botID    string
		expected string
	}{
		// Top-level messages in alert channels
		{
			name: "bot message in alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_ALERTS",
				BotID:   "B_ZABBIX",
				SubType: "bot_message",
			},
			botID:    "U_AKMATORI",
			expected: "top_level_alert",
		},
		{
			name: "human message at top level in alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_ALERTS",
				User:    "U_HUMAN",
				Text:    "Hello team",
			},
			botID:    "U_AKMATORI",
			expected: "ignore_non_bot",
		},

		// PagerDuty thread_ts == ts (thread root, not a reply)
		{
			name: "pagerduty bot message with thread_ts == ts treated as top-level",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERTS",
				BotID:           "B_PAGERDUTY",
				TimeStamp:       "1707000001.000100",
				ThreadTimeStamp: "1707000001.000100",
			},
			botID:    "U_AKMATORI",
			expected: "top_level_alert",
		},

		// Thread replies in alert channels
		{
			name: "bot thread reply in alert channel",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERTS",
				BotID:           "B_PAGERDUTY",
				TimeStamp:       "1707000002.000200",
				ThreadTimeStamp: "1707000001.000100",
			},
			botID:    "U_AKMATORI",
			expected: "ignore_thread",
		},
		{
			name: "human reply without mention in alert thread",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERTS",
				User:            "U_HUMAN",
				Text:            "I'm investigating this",
				TimeStamp:       "1707000003.000300",
				ThreadTimeStamp: "1707000001.000100",
			},
			botID:    "U_AKMATORI",
			expected: "ignore_thread",
		},
		{
			name: "human reply with bot mention in alert thread",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERTS",
				User:            "U_HUMAN",
				Text:            "<@U_AKMATORI> what's the status?",
				TimeStamp:       "1707000003.000300",
				ThreadTimeStamp: "1707000001.000100",
			},
			botID:    "U_AKMATORI",
			expected: "human_mention_thread",
		},

		// Non-alert channels
		{
			name: "message in general channel",
			event: &slackevents.MessageEvent{
				Channel: "C_GENERAL",
				User:    "U_HUMAN",
				Text:    "Good morning!",
			},
			botID:    "U_AKMATORI",
			expected: "non_alert_channel",
		},
		{
			name: "bot message in non-alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_GENERAL",
				BotID:   "B_OTHER",
				SubType: "bot_message",
			},
			botID:    "U_AKMATORI",
			expected: "non_alert_channel",
		},

		// Edge cases
		{
			name: "message from akmatori itself",
			event: &slackevents.MessageEvent{
				Channel: "C_ALERTS",
				User:    "U_AKMATORI",
				Text:    "Investigation complete",
			},
			botID:    "U_AKMATORI",
			expected: "skip_self",
		},
		{
			name: "thread reply from akmatori",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERTS",
				User:            "U_AKMATORI",
				Text:            "Looking into it...",
				TimeStamp:       "1707000002.000200",
				ThreadTimeStamp: "1707000001.000100",
			},
			botID:    "U_AKMATORI",
			expected: "skip_self",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := testSlackHandler(tt.botID, alertChannels)
			result := classifyMessage(h, tt.event)
			testhelpers.AssertEqual(t, tt.expected, result, "classification")
		})
	}
}

// TestSlackFlow_MessageExtraction_AllBlockTypes tests extraction from various block types
func TestSlackFlow_MessageExtraction_AllBlockTypes(t *testing.T) {
	tests := []struct {
		name        string
		msg         slack.Message
		mustContain []string
	}{
		{
			name: "header and section blocks only",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Blocks = slack.Blocks{
					BlockSet: []slack.Block{
						slack.NewHeaderBlock(
							slack.NewTextBlockObject("plain_text", "Source: Zabbix", false, false),
						),
						slack.NewSectionBlock(
							slack.NewTextBlockObject("mrkdwn", "Triggered: 5m ago", false, false),
							nil,
							nil,
						),
					},
				}
				return m
			}(),
			mustContain: []string{"Zabbix", "5m ago"},
		},
		{
			name: "divider and rich text combination",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Text = "Alert notification"
				m.Blocks = slack.Blocks{
					BlockSet: []slack.Block{
						slack.NewSectionBlock(
							slack.NewTextBlockObject("mrkdwn", "🚨 *Critical Alert*", false, false),
							nil,
							nil,
						),
						slack.NewDividerBlock(),
						slack.NewSectionBlock(
							slack.NewTextBlockObject("mrkdwn", "Host: web-01", false, false),
							nil,
							nil,
						),
					},
				}
				return m
			}(),
			mustContain: []string{"Critical Alert", "web-01"},
		},
		{
			name: "attachment with fields array",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Attachments = []slack.Attachment{
					{
						Title: "System Alert",
						Text:  "Multiple issues detected",
						Fields: []slack.AttachmentField{
							{Title: "Host", Value: "db-master-01", Short: true},
							{Title: "Service", Value: "postgresql", Short: true},
							{Title: "Severity", Value: "Critical", Short: true},
							{Title: "Metric", Value: "connections=500", Short: true},
						},
					},
				}
				return m
			}(),
			mustContain: []string{"System Alert", "db-master-01", "postgresql", "Critical", "connections=500"},
		},
		{
			name: "mixed blocks and attachments",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Blocks = slack.Blocks{
					BlockSet: []slack.Block{
						slack.NewHeaderBlock(
							slack.NewTextBlockObject("plain_text", "🔥 INCIDENT", false, false),
						),
					},
				}
				m.Attachments = []slack.Attachment{
					{
						Color: "danger",
						Text:  "Production database failing",
					},
				}
				return m
			}(),
			mustContain: []string{"INCIDENT", "Production database failing"},
		},
		{
			name: "message with mrkdwn links",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Text = "Check <https://grafana.example.com/dashboard|Dashboard> for details"
				return m
			}(),
			mustContain: []string{"Dashboard", "details"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSlackMessageText(tt.msg)
			for _, want := range tt.mustContain {
				testhelpers.AssertContains(t, result, want, "extracted text")
			}
		})
	}
}

// TestSlackFlow_ThreadTracking tests thread timestamp tracking
func TestSlackFlow_ThreadTracking(t *testing.T) {
	tests := []struct {
		name           string
		ts             string
		threadTS       string
		expectedRootTS string
		isThreadReply  bool
	}{
		{
			name:           "top-level message",
			ts:             "1707000001.000100",
			threadTS:       "",
			expectedRootTS: "1707000001.000100",
			isThreadReply:  false,
		},
		{
			name:           "first thread reply",
			ts:             "1707000002.000200",
			threadTS:       "1707000001.000100",
			expectedRootTS: "1707000001.000100",
			isThreadReply:  true,
		},
		{
			name:           "deeply nested reply",
			ts:             "1707000099.009900",
			threadTS:       "1707000001.000100",
			expectedRootTS: "1707000001.000100",
			isThreadReply:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &slackevents.MessageEvent{
				TimeStamp:       tt.ts,
				ThreadTimeStamp: tt.threadTS,
			}

			// Determine root thread TS
			rootTS := event.TimeStamp
			if event.ThreadTimeStamp != "" {
				rootTS = event.ThreadTimeStamp
			}

			testhelpers.AssertEqual(t, tt.expectedRootTS, rootTS, "root thread TS")

			// Check if it's a thread reply
			isReply := event.ThreadTimeStamp != ""
			testhelpers.AssertEqual(t, tt.isThreadReply, isReply, "is thread reply")
		})
	}
}

// TestSlackFlow_DeduplicationRobustness tests deduplication under various conditions
func TestSlackFlow_DeduplicationRobustness(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)

	t.Run("same message different channels", func(t *testing.T) {
		key1 := "C_ALERTS:1707000001.000100"
		key2 := "C_MONITORING:1707000001.000100"

		_, loaded1 := h.processedMsgs.LoadOrStore(key1, struct{}{})
		_, loaded2 := h.processedMsgs.LoadOrStore(key2, struct{}{})

		testhelpers.AssertEqual(t, false, loaded1, "first key should not be loaded")
		testhelpers.AssertEqual(t, false, loaded2, "second key should not be loaded (different channel)")
	})

	t.Run("rapid fire same message", func(t *testing.T) {
		key := "C_ALERTS:1707000002.000200"
		var firstCount, loadedCount int
		var mu sync.Mutex

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, loaded := h.processedMsgs.LoadOrStore(key, struct{}{})
				mu.Lock()
				if loaded {
					loadedCount++
				} else {
					firstCount++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()

		testhelpers.AssertEqual(t, 1, firstCount, "exactly one should be first")
		testhelpers.AssertEqual(t, 99, loadedCount, "99 should find it already loaded")
	})
}

// TestSlackFlow_AlertChannelRefresh tests alert channel refresh behavior
func TestSlackFlow_AlertChannelRefresh(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)

	t.Run("initial empty state", func(t *testing.T) {
		h.alertChannelsMu.RLock()
		count := len(h.alertChannels)
		h.alertChannelsMu.RUnlock()

		testhelpers.AssertEqual(t, 0, count, "should start with no alert channels")
	})

	t.Run("add channels safely", func(t *testing.T) {
		h.alertChannelsMu.Lock()
		h.alertChannels["C_NEW_ALERTS"] = &database.AlertSourceInstance{
			UUID: "new-uuid",
			Name: "new-alerts-channel",
		}
		h.alertChannelsMu.Unlock()

		h.alertChannelsMu.RLock()
		_, ok := h.alertChannels["C_NEW_ALERTS"]
		h.alertChannelsMu.RUnlock()

		testhelpers.AssertEqual(t, true, ok, "channel should be added")
	})

	t.Run("remove channels safely", func(t *testing.T) {
		h.alertChannelsMu.Lock()
		delete(h.alertChannels, "C_NEW_ALERTS")
		h.alertChannelsMu.Unlock()

		h.alertChannelsMu.RLock()
		_, ok := h.alertChannels["C_NEW_ALERTS"]
		h.alertChannelsMu.RUnlock()

		testhelpers.AssertEqual(t, false, ok, "channel should be removed")
	})
}

// ========================================
// Slack Message Building Integration Tests
// ========================================

// TestSlackFlow_TruncationBehavior tests truncation behavior
func TestSlackFlow_TruncationBehavior(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLen    int
		checkFunc func(string) bool
	}{
		{
			name:   "short input unchanged",
			input:  "Short message",
			maxLen: 100,
			checkFunc: func(s string) bool {
				return s == "Short message"
			},
		},
		{
			name:   "truncation adds marker",
			input:  strings.Repeat("x", 1000),
			maxLen: 100,
			checkFunc: func(s string) bool {
				return strings.Contains(s, "...(truncated)") && len(s) < 200
			},
		},
		{
			name:   "empty input returns empty",
			input:  "",
			maxLen: 100,
			checkFunc: func(s string) bool {
				return s == ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateLogForSlack(tt.input, tt.maxLen)
			if !tt.checkFunc(result) {
				t.Errorf("truncation check failed for %q: got %q", tt.name, result)
			}
		})
	}
}

// ========================================
// Integration with Alert Handler Tests
// ========================================

// TestSlackFlow_AlertHandlerInteraction tests slack handler interaction with alert handler
func TestSlackFlow_AlertHandlerInteraction(t *testing.T) {
	slackHandler := NewSlackHandler(nil, nil, nil, nil, nil)
	alertHandler := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil)

	// Wire them together
	slackHandler.SetAlertHandler(alertHandler)

	// Verify connection
	if slackHandler.alertHandler != alertHandler {
		t.Error("alert handler should be set")
	}
}

// TestSlackFlow_BotUserIDConfiguration tests bot user ID configuration
func TestSlackFlow_BotUserIDConfiguration(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)

	t.Run("initial state empty", func(t *testing.T) {
		testhelpers.AssertEqual(t, "", h.botUserID, "should start empty")
	})

	t.Run("set bot user ID", func(t *testing.T) {
		h.SetBotUserID("U1234567890")
		testhelpers.AssertEqual(t, "U1234567890", h.botUserID, "should be set")
	})

	t.Run("update bot user ID", func(t *testing.T) {
		h.SetBotUserID("U0987654321")
		testhelpers.AssertEqual(t, "U0987654321", h.botUserID, "should be updated")
	})
}

// ========================================
// Timing and Rate Limit Tests
// ========================================

// TestSlackFlow_ProgressUpdateInterval tests progress update interval configuration
func TestSlackFlow_ProgressUpdateInterval(t *testing.T) {
	// Verify interval is reasonable for Slack API rate limits
	if progressUpdateInterval < 2*time.Second {
		t.Errorf("progress interval %v may hit Slack rate limits (min 2s recommended)", progressUpdateInterval)
	}
	if progressUpdateInterval > 15*time.Second {
		t.Errorf("progress interval %v too slow for good UX (max 15s recommended)", progressUpdateInterval)
	}
}

// ========================================
// Final Message Summarization Integration Tests
// ========================================

// fakeFinalizeOneShotCaller is a tiny stub used to drive finalizeSlackMessageBody
// through the SlackSummarizer without spinning up the agent worker.
type fakeFinalizeOneShotCaller struct {
	calls   int
	respond func() (string, error)
}

func (f *fakeFinalizeOneShotCaller) OneShotLLM(ctx context.Context, llm *services.LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	f.calls++
	if f.respond == nil {
		return "", nil
	}
	return f.respond()
}

func setupFinalizeTestDB(t *testing.T) func() {
	t.Helper()
	prevDB := database.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&database.LLMSettings{}, &database.GeneralSettings{}); err != nil {
		t.Fatalf("migrate sqlite db: %v", err)
	}
	database.DB = db
	return func() { database.DB = prevDB }
}

func seedFinalizeLLMSettings(t *testing.T) {
	t.Helper()
	if err := database.DB.Exec("DELETE FROM llm_settings").Error; err != nil {
		t.Fatalf("clear llm_settings: %v", err)
	}
	if err := database.DB.Create(&database.LLMSettings{
		Name:     "active-config",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-test",
		Model:    "claude-sonnet-4",
		Enabled:  true,
		Active:   true,
	}).Error; err != nil {
		t.Fatalf("seed llm_settings: %v", err)
	}
}

// TestFinalizeSlackMessageBody_ShortResponseBypassesSummarizer verifies that
// a short final response is returned with the footer appended without ever
// invoking the LLM summarizer.
func TestFinalizeSlackMessageBody_ShortResponseBypassesSummarizer(t *testing.T) {
	cleanup := setupFinalizeTestDB(t)
	defer cleanup()
	seedFinalizeLLMSettings(t)

	caller := &fakeFinalizeOneShotCaller{respond: func() (string, error) {
		t.Fatal("LLM caller must not be invoked when response fits the budget")
		return "", nil
	}}
	summarizer := services.NewSlackSummarizer(caller)

	got := finalizeSlackMessageBody(context.Background(), summarizer, "Investigation complete. No issues found.", "uuid-short")
	if !strings.Contains(got, "Investigation complete") {
		t.Errorf("expected response body in result, got %q", got)
	}
	if !strings.Contains(got, "/incidents/uuid-short") {
		t.Errorf("expected footer link in result, got %q", got)
	}
	if len(got) > slackMaxTextBytes {
		t.Errorf("result %d bytes exceeds cap %d", len(got), slackMaxTextBytes)
	}
	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls for short response, got %d", caller.calls)
	}
}

// TestFinalizeSlackMessageBody_LongResponseTriggersSummarizer verifies that an
// over-budget final response is compressed via the SlackSummarizer and the
// returned body fits the slackMaxTextBytes budget.
func TestFinalizeSlackMessageBody_LongResponseTriggersSummarizer(t *testing.T) {
	cleanup := setupFinalizeTestDB(t)
	defer cleanup()
	seedFinalizeLLMSettings(t)

	long := strings.Repeat("Detailed investigation log line.\n", 500) +
		"\n[FINAL_RESULT]\nstatus: resolved\nsummary: cluster recovered\n[/FINAL_RESULT]"

	caller := &fakeFinalizeOneShotCaller{respond: func() (string, error) {
		return "✅ *Resolved*\nCluster recovered after failover.\nView the Akmatori UI for full reasoning log.", nil
	}}
	summarizer := services.NewSlackSummarizer(caller)

	got := finalizeSlackMessageBody(context.Background(), summarizer, long, "uuid-long")
	if caller.calls != 1 {
		t.Errorf("expected 1 LLM call when response exceeds budget, got %d", caller.calls)
	}
	if !strings.Contains(got, "Cluster recovered after failover") {
		t.Errorf("expected LLM summary in result, got %q", got)
	}
	if !strings.Contains(got, "/incidents/uuid-long") {
		t.Errorf("expected footer link in result, got %q", got)
	}
	if len(got) > slackMaxTextBytes {
		t.Errorf("result %d bytes exceeds cap %d", len(got), slackMaxTextBytes)
	}
}

// TestFinalizeSlackMessageBody_WorkerNotConnectedUsesFallback verifies that
// when the worker is unavailable the deterministic byte-truncation fallback
// is used and a single in-budget message is still produced.
func TestFinalizeSlackMessageBody_WorkerNotConnectedUsesFallback(t *testing.T) {
	cleanup := setupFinalizeTestDB(t)
	defer cleanup()
	seedFinalizeLLMSettings(t)

	long := strings.Repeat("x", 12000) + "\n[FINAL_RESULT]\nstatus: resolved\nsummary: handled\n[/FINAL_RESULT]"

	caller := &fakeFinalizeOneShotCaller{respond: func() (string, error) {
		return "", services.ErrWorkerNotConnected
	}}
	summarizer := services.NewSlackSummarizer(caller)

	got := finalizeSlackMessageBody(context.Background(), summarizer, long, "uuid-fallback")
	if caller.calls != 1 {
		t.Errorf("expected 1 LLM call attempt before fallback, got %d", caller.calls)
	}
	if !strings.Contains(got, "/incidents/uuid-fallback") {
		t.Errorf("expected footer link in result even on fallback, got %q", got)
	}
	if len(got) > slackMaxTextBytes {
		t.Errorf("result %d bytes exceeds cap %d", len(got), slackMaxTextBytes)
	}
}

// TestFinalizeSlackMessageBody_NilSummarizerUsesDeterministicTruncation
// ensures that when the summarizer hasn't been wired up yet the deterministic
// truncation path produces a valid in-budget message.
func TestFinalizeSlackMessageBody_NilSummarizerUsesDeterministicTruncation(t *testing.T) {
	long := strings.Repeat("y", 12000)

	got := finalizeSlackMessageBody(context.Background(), nil, long, "uuid-nil")
	if !strings.Contains(got, "/incidents/uuid-nil") {
		t.Errorf("expected footer link even without summarizer, got len=%d", len(got))
	}
	if len(got) > slackMaxTextBytes {
		t.Errorf("result %d bytes exceeds cap %d", len(got), slackMaxTextBytes)
	}
}

// TestSlackProgressStreamer_LiveProgressFromSimulatedInvestigation simulates
// agent OnOutput deltas during an investigation and verifies that
// AppendStream is invoked with non-empty status text — the live-progress
// equivalent of openclaw's chat.appendStream behaviour.
func TestSlackProgressStreamer_LiveProgressFromSimulatedInvestigation(t *testing.T) {
	fc := &fakeStreamingClient{}
	streamer := NewSlackProgressStreamer(fc, "C_ALERTS", "1707000001.000100", true, 1*time.Millisecond)

	// Simulate the agent emitting deltas that match the markers produced by
	// agent-worker/src/agent-runner.ts.
	deltas := []string{
		"\n🛠️ Running: gateway_call\n",
		"Args:\n{\"tool\":\"ssh.execute_command\"}\n",
		"Output:\nuptime: 5d\n",
		"\n✅ Ran: gateway_call\n",
		"\n🤔 considering follow-up\n",
	}
	for _, d := range deltas {
		streamer.AppendStatus(d)
		// Wait past the throttle window so each marker emits.
		time.Sleep(3 * time.Millisecond)
	}
	streamer.Flush()

	calls := fc.snapshotAppend()
	if len(calls) == 0 {
		t.Fatalf("expected at least one AppendStream call during investigation")
	}
	for _, c := range calls {
		if c.text == "" {
			t.Errorf("AppendStream called with empty text: %+v", c)
		}
	}
	if len(fc.snapshotUpdate()) != 0 {
		t.Errorf("UpdateMessage must not be called in streaming mode")
	}
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkSlackFlow_MessageClassification(b *testing.B) {
	alertChannels := map[string]*database.AlertSourceInstance{
		"C_ALERTS": {},
	}
	h := testSlackHandler("U_BOT", alertChannels)
	event := &slackevents.MessageEvent{
		Channel:         "C_ALERTS",
		BotID:           "B_ZABBIX",
		TimeStamp:       "1707000001.000100",
		ThreadTimeStamp: "",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classifyMessage(h, event)
	}
}

func BenchmarkSlackFlow_MessageExtraction(b *testing.B) {
	msg := slack.Message{}
	msg.Text = "Alert from monitoring system"
	msg.Attachments = []slack.Attachment{
		{
			Title: "Critical Alert",
			Text:  "CPU usage exceeded threshold",
			Fields: []slack.AttachmentField{
				{Title: "Host", Value: "server-01"},
				{Title: "Severity", Value: "Critical"},
			},
		},
	}
	msg.Blocks = slack.Blocks{
		BlockSet: []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "*Alert Details*", false, false),
				nil,
				nil,
			),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractSlackMessageText(msg)
	}
}

func BenchmarkSlackFlow_Deduplication(b *testing.B) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "C_ALERTS:" + string(rune('0'+i%10)) + ".000" + string(rune('0'+i%100))
		h.processedMsgs.LoadOrStore(key, struct{}{})
	}
}

// Note: testSlackHandler is defined in slack_test.go
