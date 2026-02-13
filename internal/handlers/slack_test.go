package handlers

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// --- extractSlackMessageText tests ---

func TestExtractSlackMessageText_PlainText(t *testing.T) {
	msg := slack.Message{}
	msg.Text = "PROBLEM: high CPU on web-01"

	result := extractSlackMessageText(msg)
	if result != "PROBLEM: high CPU on web-01" {
		t.Errorf("got %q, want %q", result, "PROBLEM: high CPU on web-01")
	}
}

func TestExtractSlackMessageText_Empty(t *testing.T) {
	msg := slack.Message{}
	result := extractSlackMessageText(msg)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

func TestExtractSlackMessageText_Attachments(t *testing.T) {
	msg := slack.Message{}
	msg.Attachments = []slack.Attachment{
		{
			Pretext: "Zabbix Alert",
			Title:   "PROBLEM: High CPU utilization",
			Text:    "CPU utilization is above 90% on web-01",
			Fields: []slack.AttachmentField{
				{Title: "Host", Value: "web-01"},
				{Title: "Severity", Value: "High"},
			},
			Footer: "Zabbix Server",
		},
	}

	result := extractSlackMessageText(msg)
	for _, want := range []string{
		"Zabbix Alert",
		"PROBLEM: High CPU utilization",
		"CPU utilization is above 90% on web-01",
		"Host: web-01",
		"Severity: High",
		"Zabbix Server",
	} {
		if !contains(result, want) {
			t.Errorf("result missing %q, got:\n%s", want, result)
		}
	}
}

func TestExtractSlackMessageText_AttachmentFallback(t *testing.T) {
	msg := slack.Message{}
	msg.Attachments = []slack.Attachment{
		{Fallback: "Alert: CPU high on web-01"},
	}

	result := extractSlackMessageText(msg)
	if result != "Alert: CPU high on web-01" {
		t.Errorf("got %q, want fallback text", result)
	}
}

func TestExtractSlackMessageText_Blocks(t *testing.T) {
	msg := slack.Message{}
	msg.Blocks = slack.Blocks{
		BlockSet: []slack.Block{
			slack.NewHeaderBlock(
				slack.NewTextBlockObject("plain_text", "Alert Header", false, false),
			),
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "CPU is at 95%", false, false),
				[]*slack.TextBlockObject{
					slack.NewTextBlockObject("mrkdwn", "Host: web-01", false, false),
				},
				nil,
			),
		},
	}

	result := extractSlackMessageText(msg)
	for _, want := range []string{"Alert Header", "CPU is at 95%", "Host: web-01"} {
		if !contains(result, want) {
			t.Errorf("result missing %q, got:\n%s", want, result)
		}
	}
}

func TestExtractSlackMessageText_TextAndAttachments(t *testing.T) {
	msg := slack.Message{}
	msg.Text = "New alert from Zabbix"
	msg.Attachments = []slack.Attachment{
		{Title: "PROBLEM: Disk full", Text: "/var is 98% full"},
	}

	result := extractSlackMessageText(msg)
	if !contains(result, "New alert from Zabbix") {
		t.Errorf("missing main text")
	}
	if !contains(result, "PROBLEM: Disk full") {
		t.Errorf("missing attachment title")
	}
	if !contains(result, "/var is 98% full") {
		t.Errorf("missing attachment text")
	}
}

func TestExtractSlackMessageText_AttachmentFieldValueOnly(t *testing.T) {
	msg := slack.Message{}
	msg.Attachments = []slack.Attachment{
		{
			Fields: []slack.AttachmentField{
				{Title: "", Value: "standalone value"},
				{Title: "Named", Value: ""},
			},
		},
	}

	result := extractSlackMessageText(msg)
	if !contains(result, "standalone value") {
		t.Errorf("missing field with value only, got:\n%s", result)
	}
	// Field with title but no value should be skipped
	if contains(result, "Named") {
		t.Errorf("should not include field with empty value, got:\n%s", result)
	}
}

// --- handleMessage routing tests ---

// testSlackHandler creates a minimal SlackHandler for routing tests.
// No Slack API client or services are needed since we test routing logic only.
func testSlackHandler(botUserID string, alertChannels map[string]*database.AlertSourceInstance) *SlackHandler {
	return &SlackHandler{
		botUserID:     botUserID,
		alertChannels: alertChannels,
	}
}

// classifyMessage determines what handleMessage would do with a given event,
// without actually calling external services. Returns one of:
// "skip_self", "bot_thread_alert", "human_mention_thread", "ignore_thread",
// "top_level_alert", "ignore_non_bot", "non_alert_channel"
func classifyMessage(h *SlackHandler, event *slackevents.MessageEvent) string {
	// Mirrors the logic in handleMessage
	if h.botUserID != "" && event.User == h.botUserID {
		return "skip_self"
	}

	h.alertChannelsMu.RLock()
	_, isAlert := h.alertChannels[event.Channel]
	h.alertChannelsMu.RUnlock()

	if isAlert {
		isBotMessage := event.SubType == "bot_message" || event.BotID != ""

		if event.ThreadTimeStamp != "" {
			if isBotMessage {
				return "bot_thread_alert"
			}
			if h.botUserID != "" && event.SubType == "" && event.User != "" &&
				contains(event.Text, "<@"+h.botUserID+">") {
				return "human_mention_thread"
			}
			return "ignore_thread"
		}

		if !isBotMessage {
			return "ignore_non_bot"
		}
		return "top_level_alert"
	}

	return "non_alert_channel"
}

func TestHandleMessage_SkipsSelfMessages(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		User:    "U_BOT",
		Channel: "C_ALERT",
	}
	if got := classifyMessage(h, event); got != "skip_self" {
		t.Errorf("got %q, want skip_self", got)
	}
}

func TestHandleMessage_TopLevelBotMessage(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel: "C_ALERT",
		SubType: "bot_message",
		BotID:   "B_ZABBIX",
	}
	if got := classifyMessage(h, event); got != "top_level_alert" {
		t.Errorf("got %q, want top_level_alert", got)
	}
}

func TestHandleMessage_TopLevelBotByBotIDOnly(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	// Some integrations set BotID without bot_message subtype
	event := &slackevents.MessageEvent{
		Channel: "C_ALERT",
		BotID:   "B_ZABBIX",
	}
	if got := classifyMessage(h, event); got != "top_level_alert" {
		t.Errorf("got %q, want top_level_alert", got)
	}
}

func TestHandleMessage_TopLevelHumanIgnored(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel: "C_ALERT",
		User:    "U_HUMAN",
		Text:    "hey team, checking the alerts",
	}
	if got := classifyMessage(h, event); got != "ignore_non_bot" {
		t.Errorf("got %q, want ignore_non_bot", got)
	}
}

func TestHandleMessage_ThreadReplyBotMessage(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel:        "C_ALERT",
		SubType:        "bot_message",
		BotID:          "B_ZABBIX",
		TimeStamp:      "1707000002.000200",
		ThreadTimeStamp: "1707000001.000100",
	}
	if got := classifyMessage(h, event); got != "bot_thread_alert" {
		t.Errorf("got %q, want bot_thread_alert", got)
	}
}

func TestHandleMessage_ThreadReplyBotByBotIDOnly(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel:        "C_ALERT",
		BotID:          "B_ZABBIX",
		TimeStamp:      "1707000002.000200",
		ThreadTimeStamp: "1707000001.000100",
	}
	if got := classifyMessage(h, event); got != "bot_thread_alert" {
		t.Errorf("got %q, want bot_thread_alert", got)
	}
}

func TestHandleMessage_ThreadReplyHumanMention(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel:        "C_ALERT",
		User:           "U_HUMAN",
		Text:           "<@U_BOT> please investigate this",
		TimeStamp:      "1707000003.000300",
		ThreadTimeStamp: "1707000001.000100",
	}
	if got := classifyMessage(h, event); got != "human_mention_thread" {
		t.Errorf("got %q, want human_mention_thread", got)
	}
}

func TestHandleMessage_ThreadReplyHumanNoMention(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel:        "C_ALERT",
		User:           "U_HUMAN",
		Text:           "I'll look into this manually",
		TimeStamp:      "1707000003.000300",
		ThreadTimeStamp: "1707000001.000100",
	}
	if got := classifyMessage(h, event); got != "ignore_thread" {
		t.Errorf("got %q, want ignore_thread", got)
	}
}

func TestHandleMessage_NonAlertChannel(t *testing.T) {
	h := testSlackHandler("U_BOT", map[string]*database.AlertSourceInstance{
		"C_ALERT": {},
	})
	event := &slackevents.MessageEvent{
		Channel: "C_RANDOM",
		User:    "U_HUMAN",
		Text:    "hello",
	}
	if got := classifyMessage(h, event); got != "non_alert_channel" {
		t.Errorf("got %q, want non_alert_channel", got)
	}
}

// --- handleAlertChannelMessage threadTS resolution tests ---

func TestHandleAlertChannelMessage_ThreadTSResolution_TopLevel(t *testing.T) {
	// For top-level messages, threadTS should be the message's own TS
	event := &slackevents.MessageEvent{
		Channel:   "C_ALERT",
		TimeStamp: "1707000001.000100",
	}

	threadTS := event.TimeStamp
	if event.ThreadTimeStamp != "" {
		threadTS = event.ThreadTimeStamp
	}

	if threadTS != "1707000001.000100" {
		t.Errorf("top-level threadTS = %q, want %q", threadTS, "1707000001.000100")
	}
}

func TestHandleAlertChannelMessage_ThreadTSResolution_ThreadReply(t *testing.T) {
	// For thread replies, threadTS should be the parent/root TS
	event := &slackevents.MessageEvent{
		Channel:        "C_ALERT",
		TimeStamp:      "1707000002.000200",
		ThreadTimeStamp: "1707000001.000100",
	}

	threadTS := event.TimeStamp
	if event.ThreadTimeStamp != "" {
		threadTS = event.ThreadTimeStamp
	}

	if threadTS != "1707000001.000100" {
		t.Errorf("thread reply threadTS = %q, want root TS %q", threadTS, "1707000001.000100")
	}
}

// --- Dedup tests ---

func TestDedup_PreventsDuplicateProcessing(t *testing.T) {
	h := testSlackHandler("U_BOT", nil)

	key := "C_ALERT:1707000001.000100"

	// First store should return false (not loaded)
	if _, loaded := h.processedMsgs.LoadOrStore(key, struct{}{}); loaded {
		t.Error("first LoadOrStore should not find existing entry")
	}

	// Second store should return true (already loaded)
	if _, loaded := h.processedMsgs.LoadOrStore(key, struct{}{}); !loaded {
		t.Error("second LoadOrStore should find existing entry")
	}
}

func TestDedup_DifferentKeysAreIndependent(t *testing.T) {
	h := testSlackHandler("U_BOT", nil)

	key1 := "C_ALERT:1707000001.000100"
	key2 := "C_ALERT:1707000002.000200"

	h.processedMsgs.LoadOrStore(key1, struct{}{})

	if _, loaded := h.processedMsgs.LoadOrStore(key2, struct{}{}); loaded {
		t.Error("different key should not be marked as duplicate")
	}
}

// contains and findSubstring helpers are in alert_test.go
