package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL = 30 * time.Second // API response cache TTL
	CacheCleanupTick = time.Minute      // Background cleanup interval
)

// TelegramConfig holds Telegram Bot API configuration
type TelegramConfig struct {
	BotToken  string
	ChatID    string
	BaseURL   string // Default: https://api.telegram.org
	VerifySSL bool
	Timeout   int
}

// TelegramTool handles Telegram Bot API operations
type TelegramTool struct {
	logger        *log.Logger
	configCache   *cache.Cache
	responseCache *cache.Cache
	rateLimiter   *ratelimit.Limiter
}

// NewTelegramTool creates a new Telegram tool with optional rate limiter
func NewTelegramTool(logger *log.Logger, limiter *ratelimit.Limiter) *TelegramTool {
	return &TelegramTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *TelegramTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:telegram", incidentID)
}

// responseCacheKey returns the cache key for API responses
func responseCacheKey(method string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	hash := sha256.Sum256(paramsJSON)
	return fmt.Sprintf("%s:%s", method, hex.EncodeToString(hash[:8]))
}

// extractLogicalName extracts the optional logical_name from tool arguments.
func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

// extractChatID extracts the optional chat_id override from tool arguments.
func extractChatID(args map[string]interface{}) string {
	if v, ok := args["chat_id"].(string); ok && v != "" {
		return v
	}
	return ""
}

// clampTimeout ensures timeout is within a safe range (5-60 seconds), defaulting to 30.
func clampTimeout(timeout int) int {
	if timeout <= 0 {
		return 30
	}
	if timeout < 5 {
		return 5
	}
	if timeout > 60 {
		return 60
	}
	return timeout
}

// getConfig fetches Telegram configuration from database with caching.
func (t *TelegramTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*TelegramConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "telegram", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*TelegramConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "telegram", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get Telegram credentials: %w", err)
	}

	config := &TelegramConfig{
		BaseURL:   "https://api.telegram.org",
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if token, ok := settings["telegram_bot_token"].(string); ok {
		config.BotToken = token
	}

	if chatID, ok := settings["telegram_chat_id"].(string); ok {
		config.ChatID = chatID
	}

	if baseURL, ok := settings["telegram_base_url"].(string); ok {
		config.BaseURL = baseURL
	}

	if verify, ok := settings["telegram_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["telegram_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// TelegramAPIResponse represents the standard Telegram Bot API response
type TelegramAPIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
}

// TelegramMessage represents a Telegram message object
type TelegramMessage struct {
	MessageID int `json:"message_id"`
}

// doRequest performs a Telegram Bot API request
func (t *TelegramTool) doRequest(ctx context.Context, config *TelegramConfig, method string, payload interface{}) (*TelegramAPIResponse, error) {
	if t.rateLimiter != nil {
		t.rateLimiter.Wait(ctx)
	}

	url := fmt.Sprintf("%s/bot%s/%s", config.BaseURL, config.BotToken, method)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !config.VerifySSL,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp TelegramAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.OK {
		return &apiResp, fmt.Errorf("Telegram API error %d: %s", apiResp.ErrorCode, apiResp.Description)
	}

	return &apiResp, nil
}

// SendMessage sends a message to a Telegram chat.
// If chat_id is provided in args, it overrides the configured chat_id.
func (t *TelegramTool) SendMessage(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
	config, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return nil, err
	}

	if config.BotToken == "" {
		return nil, fmt.Errorf("telegram_bot_token is not configured")
	}

	text, ok := args["text"].(string)
	if !ok || text == "" {
		return nil, fmt.Errorf("text parameter is required")
	}

	chatID := extractChatID(args)
	if chatID == "" {
		chatID = config.ChatID
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required (pass as argument or configure in settings)")
	}

	parseMode := "Markdown"
	if pm, ok := args["parse_mode"].(string); ok && pm != "" {
		parseMode = pm
	}

	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": parseMode,
	}

	// Optional: disable notification
	if silent, ok := args["disable_notification"].(bool); ok && silent {
		payload["disable_notification"] = true
	}

	apiResp, err := t.doRequest(ctx, config, "sendMessage", payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send Telegram message: %w", err)
	}

	var msg TelegramMessage
	if err := json.Unmarshal(apiResp.Result, &msg); err != nil {
		return map[string]interface{}{
			"success": true,
			"result":  string(apiResp.Result),
		}, nil
	}

	return map[string]interface{}{
		"success":    true,
		"message_id": msg.MessageID,
		"chat_id":    chatID,
	}, nil
}

// GetMe retrieves basic information about the bot
func (t *TelegramTool) GetMe(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
	config, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return nil, err
	}

	if config.BotToken == "" {
		return nil, fmt.Errorf("telegram_bot_token is not configured")
	}

	apiResp, err := t.doRequest(ctx, config, "getMe", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to call getMe: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(apiResp.Result, &result); err != nil {
		return map[string]interface{}{
			"success": true,
			"result":  string(apiResp.Result),
		}, nil
	}

	return map[string]interface{}{
		"success": true,
		"result":  result,
	}, nil
}

// GetChatInfo retrieves information about a chat
func (t *TelegramTool) GetChatInfo(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
	config, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return nil, err
	}

	if config.BotToken == "" {
		return nil, fmt.Errorf("telegram_bot_token is not configured")
	}

	chatID := extractChatID(args)
	if chatID == "" {
		chatID = config.ChatID
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat_id is required (pass as argument or configure in settings)")
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
	}

	apiResp, err := t.doRequest(ctx, config, "getChat", payload)
	if err != nil {
		return nil, fmt.Errorf("failed to call getChat: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(apiResp.Result, &result); err != nil {
		return map[string]interface{}{
			"success": true,
			"result":  string(apiResp.Result),
		}, nil
	}

	return map[string]interface{}{
		"success": true,
		"result":  result,
	}, nil
}