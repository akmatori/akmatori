package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// ========================================
// Slack Handler Integration Tests
// ========================================

// TestSlackHandler_NewHandler tests handler creation
func TestSlackHandler_NewHandler(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	if h == nil {
		t.Fatal("NewSlackHandler returned nil")
	}
	if h.alertChannels == nil {
		t.Error("alertChannels map should be initialized")
	}
	if h.alertExtractor == nil {
		t.Error("alertExtractor should be initialized")
	}
}

// TestSlackHandler_SetBotUserID tests bot user ID setting
func TestSlackHandler_SetBotUserID(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	h.SetBotUserID("U12345678")

	if h.botUserID != "U12345678" {
		t.Errorf("botUserID = %q, want %q", h.botUserID, "U12345678")
	}
}

// TestSlackHandler_SetAlertHandler tests alert handler setting
func TestSlackHandler_SetAlertHandler(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)
	alertHandler := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	h.SetAlertHandler(alertHandler)

	if h.alertHandler != alertHandler {
		t.Error("alertHandler not set correctly")
	}
}

// TestSlackHandler_SetAlertService tests alert service setting
func TestSlackHandler_SetAlertService(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	// Setting nil should not panic
	h.SetAlertService(nil)

	if h.alertService != nil {
		t.Error("alertService should be nil")
	}
}

// ========================================
// Alert Channel Loading Tests
// ========================================

func TestSlackHandler_LoadAlertChannels_NoService(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	// Should not error when service is nil
	err := h.LoadAlertChannels()
	if err != nil {
		t.Errorf("LoadAlertChannels with nil service should not error: %v", err)
	}
}

// ========================================
// Message Classification Tests
// ========================================

// TestSlackHandler_MessageClassification_ThreadReplies tests thread reply classification
func TestSlackHandler_MessageClassification_ThreadReplies(t *testing.T) {
	tests := []struct {
		name           string
		event          *slackevents.MessageEvent
		isAlertChannel bool
		botUserID      string
		expected       string
	}{
		{
			name: "bot thread reply in alert channel",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERT",
				BotID:           "B_ZABBIX",
				TimeStamp:       "1707000002.000200",
				ThreadTimeStamp: "1707000001.000100",
			},
			isAlertChannel: true,
			botUserID:      "U_BOT",
			expected:       "bot_thread_alert",
		},
		{
			name: "human reply without mention in alert channel thread",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERT",
				User:            "U_HUMAN",
				Text:            "Looking into this",
				TimeStamp:       "1707000003.000300",
				ThreadTimeStamp: "1707000001.000100",
			},
			isAlertChannel: true,
			botUserID:      "U_BOT",
			expected:       "ignore_thread",
		},
		{
			name: "human reply with bot mention in alert channel thread",
			event: &slackevents.MessageEvent{
				Channel:         "C_ALERT",
				User:            "U_HUMAN",
				Text:            "<@U_BOT> can you help?",
				TimeStamp:       "1707000003.000300",
				ThreadTimeStamp: "1707000001.000100",
			},
			isAlertChannel: true,
			botUserID:      "U_BOT",
			expected:       "human_mention_thread",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertChannels := make(map[string]*database.AlertSourceInstance)
			if tt.isAlertChannel {
				alertChannels["C_ALERT"] = &database.AlertSourceInstance{}
			}
			h := testSlackHandler(tt.botUserID, alertChannels)

			result := classifyMessage(h, tt.event)
			if result != tt.expected {
				t.Errorf("classifyMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestSlackHandler_MessageClassification_TopLevel tests top-level message classification
func TestSlackHandler_MessageClassification_TopLevel(t *testing.T) {
	tests := []struct {
		name           string
		event          *slackevents.MessageEvent
		isAlertChannel bool
		botUserID      string
		expected       string
	}{
		{
			name: "bot message at top level in alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_ALERT",
				BotID:   "B_ZABBIX",
				SubType: "bot_message",
			},
			isAlertChannel: true,
			botUserID:      "U_BOT",
			expected:       "top_level_alert",
		},
		{
			name: "human message at top level in alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_ALERT",
				User:    "U_HUMAN",
				Text:    "Hey everyone",
			},
			isAlertChannel: true,
			botUserID:      "U_BOT",
			expected:       "ignore_non_bot",
		},
		{
			name: "message in non-alert channel",
			event: &slackevents.MessageEvent{
				Channel: "C_GENERAL",
				User:    "U_HUMAN",
				Text:    "Hello",
			},
			isAlertChannel: false,
			botUserID:      "U_BOT",
			expected:       "non_alert_channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertChannels := make(map[string]*database.AlertSourceInstance)
			if tt.isAlertChannel {
				alertChannels["C_ALERT"] = &database.AlertSourceInstance{}
			}
			h := testSlackHandler(tt.botUserID, alertChannels)

			result := classifyMessage(h, tt.event)
			if result != tt.expected {
				t.Errorf("classifyMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// ========================================
// Message Extraction Integration Tests
// ========================================

// TestExtractSlackMessageText_ComplexAttachments tests complex attachment extraction
func TestExtractSlackMessageText_ComplexAttachments(t *testing.T) {
	tests := []struct {
		name        string
		msg         slack.Message
		mustContain []string
	}{
		{
			name: "multiple attachments",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Attachments = []slack.Attachment{
					{Title: "First Alert", Text: "CPU high"},
					{Title: "Second Alert", Text: "Memory low"},
				}
				return m
			}(),
			mustContain: []string{"First Alert", "Second Alert", "CPU high", "Memory low"},
		},
		{
			name: "attachment with color and actions",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Attachments = []slack.Attachment{
					{
						Color:   "danger",
						Title:   "Critical Alert",
						Text:    "Server down",
						Actions: []slack.AttachmentAction{{Name: "ack", Text: "Acknowledge"}},
					},
				}
				return m
			}(),
			mustContain: []string{"Critical Alert", "Server down"},
		},
		{
			name: "attachment with footer",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Attachments = []slack.Attachment{
					{
						Title:  "Alert",
						Text:   "Threshold exceeded",
						Footer: "Monitoring Bot",
					},
				}
				return m
			}(),
			mustContain: []string{"Alert", "Threshold exceeded", "Monitoring Bot"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSlackMessageText(tt.msg)
			for _, want := range tt.mustContain {
				if !contains(result, want) {
					t.Errorf("result missing %q, got:\n%s", want, result)
				}
			}
		})
	}
}

// TestExtractSlackMessageText_BlocksIntegration tests block extraction (integration)
func TestExtractSlackMessageText_BlocksIntegration(t *testing.T) {
	tests := []struct {
		name        string
		msg         slack.Message
		mustContain []string
	}{
		{
			name: "header and section blocks",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Blocks = slack.Blocks{
					BlockSet: []slack.Block{
						slack.NewHeaderBlock(
							slack.NewTextBlockObject("plain_text", "System Alert", false, false),
						),
						slack.NewSectionBlock(
							slack.NewTextBlockObject("mrkdwn", "CPU usage is at *95%*", false, false),
							nil,
							nil,
						),
					},
				}
				return m
			}(),
			mustContain: []string{"System Alert", "CPU usage"},
		},
		{
			name: "section with fields",
			msg: func() slack.Message {
				m := slack.Message{}
				m.Blocks = slack.Blocks{
					BlockSet: []slack.Block{
						slack.NewSectionBlock(
							slack.NewTextBlockObject("mrkdwn", "Alert Details", false, false),
							[]*slack.TextBlockObject{
								slack.NewTextBlockObject("mrkdwn", "*Host:* web-01", false, false),
								slack.NewTextBlockObject("mrkdwn", "*Severity:* High", false, false),
							},
							nil,
						),
					},
				}
				return m
			}(),
			mustContain: []string{"Alert Details", "web-01", "High"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSlackMessageText(tt.msg)
			for _, want := range tt.mustContain {
				if !contains(result, want) {
					t.Errorf("result missing %q, got:\n%s", want, result)
				}
			}
		})
	}
}

// ========================================
// Deduplication Tests
// ========================================

func TestSlackHandler_Deduplication_ConcurrentAccess(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	var wg sync.WaitGroup
	numGoroutines := 100
	key := "C_ALERT:1707000001.000100"

	loadedCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, loaded := h.processedMsgs.LoadOrStore(key, struct{}{}); loaded {
				mu.Lock()
				loadedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Exactly numGoroutines-1 should find the key already loaded
	if loadedCount != numGoroutines-1 {
		t.Errorf("expected %d goroutines to find key loaded, got %d", numGoroutines-1, loadedCount)
	}
}

func TestSlackHandler_Deduplication_MultipleKeys(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	keys := []string{
		"C_ALERT:1707000001.000100",
		"C_ALERT:1707000002.000200",
		"C_OTHER:1707000001.000100",
	}

	// Store all keys
	for _, key := range keys {
		if _, loaded := h.processedMsgs.LoadOrStore(key, struct{}{}); loaded {
			t.Errorf("key %q should not be loaded on first store", key)
		}
	}

	// All keys should now be loaded
	for _, key := range keys {
		if _, loaded := h.processedMsgs.Load(key); !loaded {
			t.Errorf("key %q should be loaded after store", key)
		}
	}
}

// ========================================
// Alert Channel Mapping Tests
// ========================================

func TestSlackHandler_AlertChannelMapping(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil)

	// Add some alert channels
	h.alertChannelsMu.Lock()
	h.alertChannels["C_ALERTS"] = &database.AlertSourceInstance{
		UUID: "uuid-1",
		Name: "alerts-channel",
	}
	h.alertChannels["C_MONITORING"] = &database.AlertSourceInstance{
		UUID: "uuid-2",
		Name: "monitoring-channel",
	}
	h.alertChannelsMu.Unlock()

	t.Run("check existing channel", func(t *testing.T) {
		h.alertChannelsMu.RLock()
		_, ok := h.alertChannels["C_ALERTS"]
		h.alertChannelsMu.RUnlock()

		if !ok {
			t.Error("C_ALERTS should be in alertChannels")
		}
	})

	t.Run("check non-existing channel", func(t *testing.T) {
		h.alertChannelsMu.RLock()
		_, ok := h.alertChannels["C_RANDOM"]
		h.alertChannelsMu.RUnlock()

		if ok {
			t.Error("C_RANDOM should not be in alertChannels")
		}
	})
}

// ========================================
// ThreadTS Resolution Tests
// ========================================

func TestSlackHandler_ThreadTSResolution(t *testing.T) {
	tests := []struct {
		name         string
		ts           string
		threadTS     string
		expectedRoot string
	}{
		{
			name:         "top-level message",
			ts:           "1707000001.000100",
			threadTS:     "",
			expectedRoot: "1707000001.000100",
		},
		{
			name:         "thread reply",
			ts:           "1707000002.000200",
			threadTS:     "1707000001.000100",
			expectedRoot: "1707000001.000100",
		},
		{
			name:         "nested reply (uses root)",
			ts:           "1707000003.000300",
			threadTS:     "1707000001.000100",
			expectedRoot: "1707000001.000100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &slackevents.MessageEvent{
				TimeStamp:       tt.ts,
				ThreadTimeStamp: tt.threadTS,
			}

			// Simulate the threadTS resolution logic from handleAlertChannelMessage
			threadTS := event.TimeStamp
			if event.ThreadTimeStamp != "" {
				threadTS = event.ThreadTimeStamp
			}

			if threadTS != tt.expectedRoot {
				t.Errorf("threadTS = %q, want %q", threadTS, tt.expectedRoot)
			}
		})
	}
}

// ========================================
// Progress Update Interval Test
// ========================================

func TestSlackHandler_ProgressUpdateInterval(t *testing.T) {
	// Verify the constant is set to a reasonable value
	if progressUpdateInterval < time.Second {
		t.Errorf("progressUpdateInterval too low: %v", progressUpdateInterval)
	}
	if progressUpdateInterval > 10*time.Second {
		t.Errorf("progressUpdateInterval too high: %v", progressUpdateInterval)
	}
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkExtractSlackMessageText_PlainText(b *testing.B) {
	msg := slack.Message{}
	msg.Text = "PROBLEM: High CPU utilization on web-01 - Current value: 95%"

	for i := 0; i < b.N; i++ {
		extractSlackMessageText(msg)
	}
}

func BenchmarkExtractSlackMessageText_Attachments(b *testing.B) {
	msg := slack.Message{}
	msg.Attachments = []slack.Attachment{
		{
			Pretext: "Alert from Monitoring",
			Title:   "PROBLEM: High CPU",
			Text:    "CPU utilization exceeded threshold",
			Fields: []slack.AttachmentField{
				{Title: "Host", Value: "web-01"},
				{Title: "Severity", Value: "High"},
				{Title: "Value", Value: "95%"},
			},
		},
	}

	for i := 0; i < b.N; i++ {
		extractSlackMessageText(msg)
	}
}

func BenchmarkSlackHandler_Deduplication(b *testing.B) {
	h := NewSlackHandler(nil, nil, nil, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "C_ALERT:" + string(rune('0'+i%10)) + ".000" + string(rune('0'+i%10))
		h.processedMsgs.LoadOrStore(key, struct{}{})
	}
}

func BenchmarkClassifyMessage(b *testing.B) {
	alertChannels := map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	}
	h := testSlackHandler("U_BOT", alertChannels)
	event := &slackevents.MessageEvent{
		Channel: "C_ALERT",
		BotID:   "B_ZABBIX",
		SubType: "bot_message",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classifyMessage(h, event)
	}
}
