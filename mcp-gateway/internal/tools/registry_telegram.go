package tools

import (
	"context"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/tools/telegram"
)

// registerTelegramTools registers telegram.list_chats and
// telegram.send_message. They use the bot token and chats of the Telegram
// connector, read from the gateway's own DB connection.
func (r *Registry) registerTelegramTools() {
	r.telegramTool = telegram.NewTool(database.DB, r.logger)
	tt := r.telegramTool

	r.server.RegisterTool(mcp.Tool{
		Name:        "telegram.list_chats",
		Description: "List the Telegram chats you can send to with telegram.send_message: chat id, name, and which one is the default. These are the chats the operator added to the Telegram connector.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return tt.ListChats(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name: "telegram.send_message",
		Description: "Send a plain-text message to a Telegram chat of the Telegram connector, for example to notify the on-call team about a finding while you investigate. " +
			"Only chats from telegram.list_chats are accepted. Without chat, the default chat is used. " +
			"Your final answer is posted to the run's channel without this tool: use it for messages beyond the final answer, when a skill, runbook or operator asks for one or the team must know now. " +
			"At most 10 calls per run; text over 4096 characters is split into several messages (limit 16384). Returns {chat, name, message_ids}.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"text": {Type: "string", Description: "Message text, plain text (required)"},
			"chat": {Type: "string", Description: "Chat id or chat name from telegram.list_chats (default: the default chat)"},
		}, Required: []string{"text"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return tt.SendMessage(ctx, incidentID, args)
	})
}
