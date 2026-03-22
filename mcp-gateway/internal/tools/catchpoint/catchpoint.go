package catchpoint

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
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/validation"
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL = 30 * time.Second // Default API response cache TTL
	CacheCleanupTick = time.Minute      // Background cleanup interval
	AlertsCacheTTL   = 15 * time.Second // Alerts and error data cache TTL
	PerfCacheTTL     = 30 * time.Second // Performance data cache TTL
	InventoryCacheTTL = 60 * time.Second // Tests, nodes, and inventory cache TTL
)

// CatchpointConfig holds Catchpoint connection configuration
type CatchpointConfig struct {
	URL       string // Default: https://io.catchpoint.com/api
	APIToken  string // Static JWT bearer token
	VerifySSL bool
	Timeout   int
	UseProxy  bool
	ProxyURL  string
}

// CatchpointTool handles Catchpoint API operations
type CatchpointTool struct {
	logger        *log.Logger
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses (15-60 sec TTL)
	rateLimiter   *ratelimit.Limiter
}

// NewCatchpointTool creates a new Catchpoint tool with optional rate limiter
func NewCatchpointTool(logger *log.Logger, limiter *ratelimit.Limiter) *CatchpointTool {
	return &CatchpointTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *CatchpointTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:catchpoint", incidentID)
}

// responseCacheKey returns the cache key for API responses
func responseCacheKey(path string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	hash := sha256.Sum256(paramsJSON)
	return fmt.Sprintf("%s:%s", path, hex.EncodeToString(hash[:8]))
}

// extractLogicalName extracts the optional logical_name from tool arguments.
// The MCP server injects this from the gateway_call instance hint.
func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

// escapeCSVPathSegment escapes each element in a comma-separated string for use in a URL path,
// preserving commas as literal characters (e.g., "123,456" → "123,456", not "123%2C456").
func escapeCSVPathSegment(csv string) string {
	parts := strings.Split(csv, ",")
	for i, part := range parts {
		parts[i] = url.PathEscape(strings.TrimSpace(part))
	}
	return strings.Join(parts, ",")
}

// clampTimeout ensures timeout is within a safe range (5-300 seconds), defaulting to 30.
func clampTimeout(timeout int) int {
	if timeout <= 0 {
		return 30
	}
	if timeout < 5 {
		return 5
	}
	if timeout > 300 {
		return 300
	}
	return timeout
}

// getConfig fetches Catchpoint configuration from database with caching.
func (t *CatchpointTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*CatchpointConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "catchpoint", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*CatchpointConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "catchpoint", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get Catchpoint credentials: %w", err)
	}

	config := &CatchpointConfig{
		URL:       "https://io.catchpoint.com/api",
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if u, ok := settings["catchpoint_url"].(string); ok {
		config.URL = strings.TrimSuffix(u, "/")
	}

	if token, ok := settings["catchpoint_api_token"].(string); ok {
		config.APIToken = token
	}

	if verify, ok := settings["catchpoint_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["catchpoint_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.CatchpointEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *CatchpointTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
	cacheKey := "proxy:settings"
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if settings, ok := cached.(*database.ProxySettings); ok {
			return settings
		}
	}

	proxySettings, err := database.GetProxySettings(ctx)
	if err != nil || proxySettings == nil {
		return nil
	}

	t.configCache.Set(cacheKey, proxySettings)

	return proxySettings
}

// doRequest performs an HTTP request to Catchpoint API with rate limiting
func (t *CatchpointTool) doRequest(ctx context.Context, config *CatchpointConfig, method, path string, queryParams url.Values, body io.Reader) ([]byte, error) {
	// Apply rate limiting
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	// Build full URL
	fullURL := config.URL + path
	if len(queryParams) > 0 {
		fullURL += "?" + queryParams.Encode()
	}

	t.logger.Printf("Catchpoint API call: %s %s", method, path)

	// Create HTTP transport with explicit proxy configuration
	// DisableKeepAlives prevents connection pool leakage since we create a new transport per request
	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	// Handle proxy settings - MUST explicitly set Proxy to prevent env var usage
	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL: %v, proceeding without proxy", err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			t.logger.Printf("Catchpoint using proxy: %s", proxyURL.Host)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via catchpoint_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Catchpoint uses static JWT bearer token auth
	if config.APIToken == "" {
		return nil, fmt.Errorf("Catchpoint API token is required but not configured")
	}
	httpReq.Header.Set("Authorization", "Bearer "+config.APIToken)

	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 5 * 1024 * 1024 // 5 MB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(respBody) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d MB limit", maxResponseBytes/(1024*1024))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := string(respBody)
		if len(errMsg) > 500 {
			errMsg = errMsg[:500] + "... (truncated)"
		}
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, errMsg)
	}

	return respBody, nil
}

// cachedGet performs a cached GET request to Catchpoint API
func (t *CatchpointTool) cachedGet(ctx context.Context, incidentID, path string, queryParams url.Values, ttl time.Duration, logicalName ...string) ([]byte, error) {
	cacheKey := responseCacheKey(path, queryParams)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("logical:%s:%s", logicalName[0], cacheKey)
	} else {
		cacheKey = fmt.Sprintf("incident:%s:%s", incidentID, cacheKey)
	}

	// Check response cache
	if cached, ok := t.responseCache.Get(cacheKey); ok {
		if result, ok := cached.([]byte); ok {
			t.logger.Printf("Response cache hit for %s", path)
			return result, nil
		}
	}

	// Resolve config and make request
	config, err := t.getConfig(ctx, incidentID, logicalName...)
	if err != nil {
		return nil, err
	}

	if config.URL == "" {
		return nil, fmt.Errorf("Catchpoint URL not configured")
	}

	respBody, err := t.doRequest(ctx, config, http.MethodGet, path, queryParams, nil)
	if err != nil {
		return nil, err
	}

	// Cache the result
	t.responseCache.SetWithTTL(cacheKey, respBody, ttl)
	t.logger.Printf("Response cached for %s (TTL: %v)", path, ttl)

	return respBody, nil
}

// clampPageSize ensures page_size does not exceed Catchpoint API maximum of 100
func clampPageSize(size int) int {
	if size <= 0 {
		return 0 // Don't set if not specified
	}
	if size > 100 {
		return 100
	}
	return size
}

// addPaginationParams adds optional pagination parameters to query values
func addPaginationParams(params url.Values, args map[string]interface{}) {
	if v, ok := args["page_number"].(float64); ok && v > 0 {
		params.Set("pageNumber", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["page_size"].(float64); ok && v > 0 {
		clamped := clampPageSize(int(v))
		if clamped > 0 {
			params.Set("pageSize", fmt.Sprintf("%d", clamped))
		}
	}
}

// addTimeParams adds optional start_time and end_time parameters
func addTimeParams(params url.Values, args map[string]interface{}) {
	if v, ok := args["start_time"].(string); ok && v != "" {
		params.Set("startTime", v)
	}
	if v, ok := args["end_time"].(string); ok && v != "" {
		params.Set("endTime", v)
	}
}

// GetAlerts retrieves test alerts from Catchpoint
func (t *CatchpointTool) GetAlerts(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addTimeParams(params, args)
	addPaginationParams(params, args)

	if v, ok := args["severity"].(string); ok && v != "" {
		params.Set("severity", v)
	}
	if v, ok := args["test_ids"].(string); ok && v != "" {
		params.Set("testIds", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/tests/alerts", params, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetAlertDetails retrieves details for specific alerts
func (t *CatchpointTool) GetAlertDetails(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	alertIDs, ok := args["alert_ids"].(string)
	if !ok || alertIDs == "" {
		return "", fmt.Errorf("alert_ids is required%s", validation.SuggestParam("alert_ids", args))
	}

	path := fmt.Sprintf("/v4/tests/alerts/%s", escapeCSVPathSegment(alertIDs))

	body, err := t.cachedGet(ctx, incidentID, path, nil, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTestPerformance retrieves aggregated test performance data
func (t *CatchpointTool) GetTestPerformance(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	testIDs, ok := args["test_ids"].(string)
	if !ok || testIDs == "" {
		return "", fmt.Errorf("test_ids is required%s", validation.SuggestParam("test_ids", args))
	}

	params := url.Values{}
	params.Set("testIds", testIDs)
	addTimeParams(params, args)

	if v, ok := args["metrics"].(string); ok && v != "" {
		params.Set("metrics", v)
	}
	if v, ok := args["dimensions"].(string); ok && v != "" {
		params.Set("dimensions", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/tests/explorer/aggregated", params, PerfCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTestPerformanceRaw retrieves raw test performance data
func (t *CatchpointTool) GetTestPerformanceRaw(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	testIDs, ok := args["test_ids"].(string)
	if !ok || testIDs == "" {
		return "", fmt.Errorf("test_ids is required%s", validation.SuggestParam("test_ids", args))
	}

	params := url.Values{}
	params.Set("testIds", testIDs)
	addTimeParams(params, args)
	addPaginationParams(params, args)

	if v, ok := args["node_ids"].(string); ok && v != "" {
		params.Set("nodeIds", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/tests/explorer/raw", params, PerfCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTests retrieves test definitions
func (t *CatchpointTool) GetTests(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)

	if v, ok := args["test_ids"].(string); ok && v != "" {
		params.Set("testIds", v)
	}
	if v, ok := args["test_type"].(string); ok && v != "" {
		params.Set("testType", v)
	}
	if v, ok := args["folder_id"].(string); ok && v != "" {
		params.Set("folderId", v)
	}
	if v, ok := args["status"].(string); ok && v != "" {
		params.Set("status", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/tests", params, InventoryCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTestDetails retrieves detailed information for specific tests
func (t *CatchpointTool) GetTestDetails(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	testIDs, ok := args["test_ids"].(string)
	if !ok || testIDs == "" {
		return "", fmt.Errorf("test_ids is required%s", validation.SuggestParam("test_ids", args))
	}

	path := fmt.Sprintf("/v4/tests/%s", escapeCSVPathSegment(testIDs))

	body, err := t.cachedGet(ctx, incidentID, path, nil, InventoryCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTestErrors retrieves raw test error data
func (t *CatchpointTool) GetTestErrors(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addTimeParams(params, args)
	addPaginationParams(params, args)

	if v, ok := args["test_ids"].(string); ok && v != "" {
		params.Set("testIds", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/tests/errors/raw", params, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetInternetOutages retrieves internet outage data from Catchpoint Internet Weather
func (t *CatchpointTool) GetInternetOutages(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addTimeParams(params, args)
	addPaginationParams(params, args)

	if v, ok := args["asn"].(string); ok && v != "" {
		params.Set("asn", v)
	}
	if v, ok := args["country"].(string); ok && v != "" {
		params.Set("country", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/iw/outages", params, PerfCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetNodes retrieves all monitoring nodes
func (t *CatchpointTool) GetNodes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)

	body, err := t.cachedGet(ctx, incidentID, "/v4/nodes/all", params, InventoryCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetNodeAlerts retrieves node-level alerts
func (t *CatchpointTool) GetNodeAlerts(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addTimeParams(params, args)
	addPaginationParams(params, args)

	if v, ok := args["node_ids"].(string); ok && v != "" {
		params.Set("nodeIds", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/v4/node/alerts", params, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// AcknowledgeAlerts acknowledges, assigns, or drops alerts (write operation, NOT cached)
func (t *CatchpointTool) AcknowledgeAlerts(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	alertIDs, ok := args["alert_ids"].(string)
	if !ok || alertIDs == "" {
		return "", fmt.Errorf("alert_ids is required%s", validation.SuggestParam("alert_ids", args))
	}

	action, ok := args["action"].(string)
	if !ok || action == "" {
		return "", fmt.Errorf("action is required%s", validation.SuggestParam("action", args))
	}

	// Validate action enum
	validActions := map[string]bool{"acknowledge": true, "assign": true, "drop": true}
	if !validActions[action] {
		return "", fmt.Errorf("invalid action '%s': must be one of acknowledge, assign, drop", action)
	}

	// Validate assignee is provided for assign action
	assignee, _ := args["assignee"].(string)
	if action == "assign" && assignee == "" {
		return "", fmt.Errorf("assignee is required when action is 'assign'")
	}

	// Build request body
	reqBody := map[string]interface{}{
		"alertIds": alertIDs,
		"action":   action,
	}
	if assignee != "" {
		reqBody["assignee"] = assignee
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	respBody, err := t.doRequest(ctx, config, http.MethodPatch, "/v4/tests/alerts", nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}

// RunInstantTest triggers an instant test execution (write operation, NOT cached)
func (t *CatchpointTool) RunInstantTest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	testID, ok := args["test_id"].(string)
	if !ok || testID == "" {
		return "", fmt.Errorf("test_id is required%s", validation.SuggestParam("test_id", args))
	}

	path := fmt.Sprintf("/v4/instanttests/%s", url.PathEscape(testID))

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	respBody, err := t.doRequest(ctx, config, http.MethodPost, path, nil, nil)
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}
