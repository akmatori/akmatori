package zabbix

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
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL = 30 * time.Second // API response cache TTL
	AuthCacheTTL     = 30 * time.Minute // Auth token cache TTL
	CacheCleanupTick = time.Minute      // Background cleanup interval
)

// authEntry holds cached authentication token with expiration
type authEntry struct {
	token     string
	expiresAt time.Time
}

// ZabbixTool handles Zabbix API operations
type ZabbixTool struct {
	logger        *log.Logger
	requestID     uint64
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses (30-60 sec TTL)
	authCache     map[string]authEntry
	authMu        sync.RWMutex
	rateLimiter   *ratelimit.Limiter
}

// NewZabbixTool creates a new Zabbix tool with optional rate limiter
func NewZabbixTool(logger *log.Logger, limiter *ratelimit.Limiter) *ZabbixTool {
	return &ZabbixTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		authCache:     make(map[string]authEntry),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *ZabbixTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// ZabbixConfig holds Zabbix connection configuration
type ZabbixConfig struct {
	URL       string
	Token     string
	Username  string
	Password  string
	VerifySSL bool
	Timeout   int
	UseProxy  bool   // Whether to use proxy (from ZabbixEnabled setting)
	ProxyURL  string // Proxy URL if enabled
}

// JSONRPCRequest represents a Zabbix JSON-RPC request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	Auth    string      `json:"auth,omitempty"`
	ID      uint64      `json:"id"`
}

// JSONRPCResponse represents a Zabbix JSON-RPC response
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ZabbixError    `json:"error,omitempty"`
	ID      uint64          `json:"id"`
}

// ZabbixError represents a Zabbix API error
type ZabbixError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *ZabbixError) Error() string {
	return fmt.Sprintf("Zabbix API error: %s (code: %d, data: %s)", e.Message, e.Code, e.Data)
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID, toolType string) string {
	return fmt.Sprintf("creds:%s:%s", incidentID, toolType)
}

// authCacheKey returns the cache key for auth tokens
func authCacheKey(zabbixURL, username string) string {
	return fmt.Sprintf("%s:%s", zabbixURL, username)
}

// responseCacheKey returns the cache key for API responses
func responseCacheKey(method string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	hash := sha256.Sum256(paramsJSON)
	return fmt.Sprintf("%s:%s", method, hex.EncodeToString(hash[:8]))
}

// getConfig fetches Zabbix configuration from database with caching
func (t *ZabbixTool) getConfig(ctx context.Context, incidentID string) (*ZabbixConfig, error) {
	cacheKey := configCacheKey(incidentID, "zabbix")

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*ZabbixConfig); ok {
			t.logger.Printf("Config cache hit for incident %s", incidentID)
			return config, nil
		}
	}

	creds, err := database.GetToolCredentialsForIncident(ctx, incidentID, "zabbix")
	if err != nil {
		return nil, fmt.Errorf("failed to get Zabbix credentials: %w", err)
	}

	config := &ZabbixConfig{
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	// Get URL
	if url, ok := settings["zabbix_url"].(string); ok {
		config.URL = url
	}

	// Get authentication - prefer token over username/password
	if token, ok := settings["zabbix_token"].(string); ok && token != "" {
		config.Token = token
	} else {
		if user, ok := settings["zabbix_user"].(string); ok {
			config.Username = user
		}
		if pass, ok := settings["zabbix_password"].(string); ok {
			config.Password = pass
		}
	}

	// Get SSL verification setting
	if verify, ok := settings["zabbix_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	// Get timeout
	if timeout, ok := settings["zabbix_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.ZabbixEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for incident %s", incidentID)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *ZabbixTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
	cacheKey := "proxy:settings"

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if settings, ok := cached.(*database.ProxySettings); ok {
			return settings
		}
	}

	// Fetch from database
	proxySettings, err := database.GetProxySettings(ctx)
	if err != nil || proxySettings == nil {
		return nil
	}

	// Cache the settings
	t.configCache.Set(cacheKey, proxySettings)

	return proxySettings
}

// authenticate performs username/password authentication and returns a session token
func (t *ZabbixTool) authenticate(ctx context.Context, config *ZabbixConfig) (string, error) {
	params := map[string]string{
		"user":     config.Username,
		"password": config.Password,
	}

	result, err := t.doRequest(ctx, config, "user.login", params, "")
	if err != nil {
		return "", err
	}

	var token string
	if err := json.Unmarshal(result, &token); err != nil {
		return "", fmt.Errorf("failed to parse auth token: %w", err)
	}

	return token, nil
}

// getAuth returns the authentication token with caching
func (t *ZabbixTool) getAuth(ctx context.Context, config *ZabbixConfig) (string, error) {
	// If using API token, return directly
	if config.Token != "" {
		return config.Token, nil
	}

	if config.Username == "" || config.Password == "" {
		return "", fmt.Errorf("no authentication method configured")
	}

	// Check auth cache for session token
	cacheKey := authCacheKey(config.URL, config.Username)

	t.authMu.RLock()
	entry, exists := t.authCache[cacheKey]
	t.authMu.RUnlock()

	if exists && time.Now().Before(entry.expiresAt) {
		t.logger.Printf("Auth cache hit for %s", config.Username)
		return entry.token, nil
	}

	// Authenticate and cache the token
	token, err := t.authenticate(ctx, config)
	if err != nil {
		return "", err
	}

	t.authMu.Lock()
	t.authCache[cacheKey] = authEntry{
		token:     token,
		expiresAt: time.Now().Add(AuthCacheTTL),
	}
	t.authMu.Unlock()
	t.logger.Printf("Auth token cached for %s (TTL: %v)", config.Username, AuthCacheTTL)

	return token, nil
}

// doRequest performs a Zabbix API request with rate limiting
func (t *ZabbixTool) doRequest(ctx context.Context, config *ZabbixConfig, method string, params interface{}, auth string) (json.RawMessage, error) {
	// Apply rate limiting if configured
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	reqID := atomic.AddUint64(&t.requestID, 1)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		Auth:    auth,
		ID:      reqID,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	t.logger.Printf("Zabbix API call: %s", method)

	// Create HTTP transport with explicit proxy configuration
	transport := &http.Transport{}

	// Handle proxy settings - MUST explicitly set Proxy to prevent env var usage
	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL %s: %v, proceeding without proxy", config.ProxyURL, err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			t.logger.Printf("Zabbix using proxy: %s", config.ProxyURL)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	// Apply SSL verification setting
	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	// Ensure URL ends with /api_jsonrpc.php
	apiURL := config.URL
	if !strings.HasSuffix(apiURL, "/api_jsonrpc.php") {
		apiURL = strings.TrimSuffix(apiURL, "/") + "/api_jsonrpc.php"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json-rpc")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp.Result, nil
}

// cachedRequest performs a cached API request for read-only methods
func (t *ZabbixTool) cachedRequest(ctx context.Context, incidentID string, method string, params interface{}, ttl time.Duration) (json.RawMessage, error) {
	cacheKey := responseCacheKey(method, params)

	// Check cache first
	if cached, ok := t.responseCache.Get(cacheKey); ok {
		if result, ok := cached.(json.RawMessage); ok {
			t.logger.Printf("Response cache hit for %s", method)
			return result, nil
		}
	}

	// Make the actual request
	result, err := t.request(ctx, incidentID, method, params)
	if err != nil {
		return nil, err
	}

	// Cache the result
	t.responseCache.SetWithTTL(cacheKey, result, ttl)
	t.logger.Printf("Response cached for %s (TTL: %v)", method, ttl)

	return result, nil
}

// request performs an authenticated Zabbix API request
func (t *ZabbixTool) request(ctx context.Context, incidentID string, method string, params interface{}) (json.RawMessage, error) {
	config, err := t.getConfig(ctx, incidentID)
	if err != nil {
		return nil, err
	}

	if config.URL == "" {
		return nil, fmt.Errorf("Zabbix URL not configured")
	}

	auth, err := t.getAuth(ctx, config)
	if err != nil {
		return nil, err
	}

	return t.doRequest(ctx, config, method, params, auth)
}

// GetHosts retrieves hosts from Zabbix with caching
func (t *ZabbixTool) GetHosts(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	params := make(map[string]interface{})

	// Copy relevant parameters
	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = "extend"
	}

	if filter, ok := args["filter"]; ok {
		params["filter"] = filter
	}

	if search, ok := args["search"]; ok {
		params["search"] = search
	}

	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}

	result, err := t.cachedRequest(ctx, incidentID, "host.get", params, ResponseCacheTTL)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetProblems retrieves current problems from Zabbix with caching
func (t *ZabbixTool) GetProblems(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	params := make(map[string]interface{})

	// Set defaults
	params["output"] = "extend"
	params["selectHosts"] = "extend"
	params["selectTags"] = "extend"
	params["sortfield"] = []string{"eventid"}
	params["sortorder"] = "DESC"

	if recent, ok := args["recent"].(bool); ok && recent {
		params["recent"] = true
	}

	if severityMin, ok := args["severity_min"].(float64); ok {
		params["severities"] = []int{}
		for i := int(severityMin); i <= 5; i++ {
			params["severities"] = append(params["severities"].([]int), i)
		}
	}

	if hostids, ok := args["hostids"]; ok {
		params["hostids"] = hostids
	}

	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}

	// Use shorter TTL for problems (15 seconds) since they change frequently
	result, err := t.cachedRequest(ctx, incidentID, "problem.get", params, 15*time.Second)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetHistory retrieves metric history from Zabbix with caching
func (t *ZabbixTool) GetHistory(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	params := make(map[string]interface{})

	// Required: itemids
	if itemids, ok := args["itemids"]; ok {
		params["itemids"] = itemids
	} else {
		return "", fmt.Errorf("itemids is required")
	}

	// History type (default: 0 = float)
	if history, ok := args["history"]; ok {
		params["history"] = history
	} else {
		params["history"] = 0
	}

	// Time range
	if timeFrom, ok := args["time_from"]; ok {
		params["time_from"] = timeFrom
	}
	if timeTill, ok := args["time_till"]; ok {
		params["time_till"] = timeTill
	}

	// Limit
	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}

	// Sorting
	if sortfield, ok := args["sortfield"]; ok {
		params["sortfield"] = sortfield
	} else {
		params["sortfield"] = "clock"
	}
	if sortorder, ok := args["sortorder"]; ok {
		params["sortorder"] = sortorder
	} else {
		params["sortorder"] = "DESC"
	}

	params["output"] = "extend"

	result, err := t.cachedRequest(ctx, incidentID, "history.get", params, ResponseCacheTTL)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetItems retrieves items (metrics) from Zabbix with caching
func (t *ZabbixTool) GetItems(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	params := make(map[string]interface{})

	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = "extend"
	}

	if hostids, ok := args["hostids"]; ok {
		params["hostids"] = hostids
	}

	if search, ok := args["search"]; ok {
		params["search"] = search
	}

	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}

	result, err := t.cachedRequest(ctx, incidentID, "item.get", params, ResponseCacheTTL)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetTriggers retrieves triggers from Zabbix with caching
func (t *ZabbixTool) GetTriggers(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	params := make(map[string]interface{})

	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = "extend"
	}

	if hostids, ok := args["hostids"]; ok {
		params["hostids"] = hostids
	}

	if onlyTrue, ok := args["only_true"].(bool); ok && onlyTrue {
		params["only_true"] = 1
	}

	if minSeverity, ok := args["min_severity"].(float64); ok {
		params["min_severity"] = int(minSeverity)
	}

	params["selectHosts"] = "extend"
	params["expandDescription"] = true

	result, err := t.cachedRequest(ctx, incidentID, "trigger.get", params, ResponseCacheTTL)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// APIRequest performs a raw Zabbix API request (not cached for flexibility)
func (t *ZabbixTool) APIRequest(ctx context.Context, incidentID string, method string, params map[string]interface{}) (string, error) {
	if params == nil {
		params = make(map[string]interface{})
	}

	result, err := t.request(ctx, incidentID, method, params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// ClearCache clears all caches (useful for testing or forcing refresh)
func (t *ZabbixTool) ClearCache() {
	t.configCache.Clear()
	t.responseCache.Clear()

	t.authMu.Lock()
	t.authCache = make(map[string]authEntry)
	t.authMu.Unlock()

	t.logger.Println("All caches cleared")
}

// InvalidateConfigCache invalidates config cache for a specific incident
func (t *ZabbixTool) InvalidateConfigCache(incidentID string) {
	t.configCache.DeleteByPrefix(fmt.Sprintf("creds:%s:", incidentID))
	t.logger.Printf("Config cache invalidated for incident %s", incidentID)
}
