package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

func TestTelegramProvider_Name(t *testing.T) {
	p := NewTelegramProvider()
	if p.Name() != database.MessagingProviderTelegram {
		t.Errorf("Name = %q, want %q", p.Name(), database.MessagingProviderTelegram)
	}
}

func TestTelegramProvider_PostMessage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("path = %s, want /sendMessage", r.URL.Path)
		}

		var req telegramSendMessageRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ChatID.(string) != "-100123" {
			t.Errorf("chat_id = %v, want -100123", req.ChatID)
		}
		if req.ParseMode != "MarkdownV2" {
			t.Errorf("parse_mode = %s, want MarkdownV2", req.ParseMode)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(telegramResponse{
			OK:     true,
			Result: json.RawMessage(`{"message_id": 42}`),
		})
	}))
	defer server.Close()

	// Patch the base URL for this test
	origBase := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = origBase }()

	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	got, err := p.PostMessage(context.Background(), channel, "*hello* world")
	if err != nil {
		t.Fatalf("PostMessage error = %v", err)
	}
	if got.MessageID != "42" {
		t.Errorf("MessageID = %q, want 42", got.MessageID)
	}
}

func TestTelegramProvider_PostMessage_NilChannel(t *testing.T) {
	p := NewTelegramProvider()
	_, err := p.PostMessage(context.Background(), nil, "hello")
	if err == nil {
		t.Fatal("expected error for nil channel")
	}
}

func TestTelegramProvider_PostMessage_MissingBotToken(t *testing.T) {
	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:          1,
			Name:        "Test Bot",
			Credentials: database.JSONB{},
		},
	}

	_, err := p.PostMessage(context.Background(), channel, "hello")
	if err == nil {
		t.Fatal("expected error for missing bot token")
	}
	if !strings.Contains(err.Error(), "bot_token") {
		t.Errorf("error = %v, want bot_token in message", err)
	}
}

func TestTelegramProvider_PostMessage_EmptyChatID(t *testing.T) {
	p := NewTelegramProvider()
	channel := &database.Channel{
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID: 1,
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	_, err := p.PostMessage(context.Background(), channel, "hello")
	if err == nil {
		t.Fatal("expected error for empty chat ID")
	}
}

func TestTelegramProvider_PostThreadReply_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req telegramSendMessageRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ReplyToMessageID != 99 {
			t.Errorf("reply_to_message_id = %d, want 99", req.ReplyToMessageID)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(telegramResponse{
			OK:     true,
			Result: json.RawMessage(`{"message_id": 100}`),
		})
	}))
	defer server.Close()

	origBase := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = origBase }()

	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	got, err := p.PostThreadReply(context.Background(), channel, "99", "reply text")
	if err != nil {
		t.Fatalf("PostThreadReply error = %v", err)
	}
	if got.MessageID != "100" {
		t.Errorf("MessageID = %q, want 100", got.MessageID)
	}
}

func TestTelegramProvider_PostThreadReply_EmptyParentID(t *testing.T) {
	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	_, err := p.PostThreadReply(context.Background(), channel, "", "reply")
	if err == nil {
		t.Fatal("expected error for empty parent message ID")
	}
}

func TestTelegramProvider_UpdateMessage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/editMessageText") {
			t.Errorf("path = %s, want /editMessageText", r.URL.Path)
		}

		var req telegramEditMessageRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.MessageID != 42 {
			t.Errorf("message_id = %d, want 42", req.MessageID)
		}
		if req.Text != "updated text" {
			t.Errorf("text = %s, want updated text", req.Text)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(telegramResponse{OK: true})
	}))
	defer server.Close()

	origBase := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = origBase }()

	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	err := p.UpdateMessage(context.Background(), channel, "42", "updated text")
	if err != nil {
		t.Fatalf("UpdateMessage error = %v", err)
	}
}

func TestTelegramProvider_SendChatAction_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendChatAction") {
			t.Errorf("path = %s, want /sendChatAction", r.URL.Path)
		}

		var req telegramSendChatActionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Action != "typing" {
			t.Errorf("action = %s, want typing", req.Action)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(telegramResponse{OK: true})
	}))
	defer server.Close()

	origBase := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = origBase }()

	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	err := p.SendChatAction(context.Background(), channel, "typing")
	if err != nil {
		t.Fatalf("SendChatAction error = %v", err)
	}
}

func TestTelegramProvider_PostMessage_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(telegramResponse{
			OK:          false,
			Description: "Not Found",
		})
	}))
	defer server.Close()

	origBase := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = origBase }()

	p := NewTelegramProvider()
	channel := &database.Channel{
		ExternalID:  "-100123",
		DisplayName: "Test Chat",
		Integration: database.Integration{
			ID:   1,
			Name: "Test Bot",
			Credentials: database.JSONB{
				"bot_token": "123456:ABC",
			},
		},
	}

	_, err := p.PostMessage(context.Background(), channel, "hello")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestEscapeMarkdownV2(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"hello world", "hello world"},
		{"hello *world*", "hello \\*world\\*"},
		{"hello _world_", "hello \\_world\\_"},
		{"[link](url)", "\\[link\\]\\(url\\)"},
		{"code `block`", "code \\`block\\`"},
		{"a > b", "a \\> b"},
		{"# heading", "\\# heading"},
		{"100%", "100%"},
		{"test@example.com", "test@example\\.com"},
		{"", ""},
	}

	for _, tc := range tests {
		got := EscapeMarkdownV2(tc.input)
		if got != tc.output {
			t.Errorf("EscapeMarkdownV2(%q) = %q, want %q", tc.input, got, tc.output)
		}
	}
}

func TestConvertSlackLinksToMarkdownV2(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{
			input:  "See <https://example.com|this link>",
			output: "See [this link](https://example\\.com)",
		},
		{
			input:  "No links here",
			output: "No links here",
		},
		{
			input:  "<https://example.com|Link> and <https://other.com|Other>",
			output: "[Link](https://example\\.com) and [Other](https://other\\.com)",
		},
		{
			input:  "Unclosed <tag",
			output: "Unclosed <tag",
		},
	}

	for _, tc := range tests {
		got := convertSlackLinksToMarkdownV2(tc.input)
		if got != tc.output {
			t.Errorf("convertSlackLinksToMarkdownV2(%q) = %q, want %q", tc.input, got, tc.output)
		}
	}
}

func TestTruncateForTelegram(t *testing.T) {
	long := strings.Repeat("a", 5000)
	got := TruncateForTelegram(long, TelegramMaxMessageLength)
	if len(got) > TelegramMaxMessageLength {
		t.Errorf("truncated length = %d, want <= %d", len(got), TelegramMaxMessageLength)
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("truncated text should end with ...")
	}

	short := "hello"
	got2 := TruncateForTelegram(short, TelegramMaxMessageLength)
	if got2 != short {
		t.Errorf("short text should not be truncated: %q", got2)
	}
}

func TestFormatAlertMessage(t *testing.T) {
	alert := alerts.NormalizedAlert{
		AlertName:     "High CPU",
		TargetHost:    "server-01",
		TargetService: "web",
		Severity:      "critical",
		Summary:       "CPU usage above 90%",
		RunbookURL:    "https://wiki.example.com/cpu",
	}

	msg := FormatAlertMessage(alert, "Prometheus", "prod-prometheus")
	if !strings.Contains(msg, "**Alert: High CPU**") {
		t.Errorf("message should contain alert name: %s", msg)
	}
	if !strings.Contains(msg, "server") {
		t.Errorf("message should contain host: %s", msg)
	}
	if !strings.Contains(msg, "Runbook") {
		t.Errorf("message should contain runbook link: %s", msg)
	}
}
