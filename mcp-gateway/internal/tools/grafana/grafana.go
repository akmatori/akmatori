package grafana

import (
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
)

// Cache TTL constants
const (
	ConfigCacheTTL    = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL  = 30 * time.Second // Default API response cache TTL
	CacheCleanupTick  = time.Minute      // Background cleanup interval
	AlertsCacheTTL    = 15 * time.Second // Alerts and firing instances cache TTL
	DashboardCacheTTL = 30 * time.Second // Dashboard data cache TTL
	InventoryCacheTTL = 60 * time.Second // Data sources and static config cache TTL
)

// GrafanaConfig holds Grafana connection configuration
type GrafanaConfig struct {
	URL       string // Grafana base URL (e.g., https://grafana.example.com)
	APIToken  string // Grafana API token (Bearer auth)
	VerifySSL bool
	Timeout   int
	UseProxy  bool
	ProxyURL  string
}

// GrafanaTool handles Grafana API operations
type GrafanaTool struct {
	logger        *log.Logger
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses (15-60 sec TTL)
	rateLimiter   *ratelimit.Limiter
}

// NewGrafanaTool creates a new Grafana tool with optional rate limiter
func NewGrafanaTool(logger *log.Logger, limiter *ratelimit.Limiter) *GrafanaTool {
	return &GrafanaTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *GrafanaTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:grafana", incidentID)
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

// getConfig fetches Grafana configuration from database with caching.
func (t *GrafanaTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*GrafanaConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "grafana", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*GrafanaConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "grafana", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana credentials: %w", err)
	}

	config := &GrafanaConfig{
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if u, ok := settings["grafana_url"].(string); ok {
		config.URL = strings.TrimSuffix(u, "/")
	}

	if token, ok := settings["grafana_api_token"].(string); ok {
		config.APIToken = token
	}

	if verify, ok := settings["grafana_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["grafana_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.GrafanaEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *GrafanaTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
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

// doRequest performs an HTTP request to Grafana API with rate limiting
func (t *GrafanaTool) doRequest(ctx context.Context, config *GrafanaConfig, method, path string, queryParams url.Values, body io.Reader) ([]byte, error) {
	// Validate token before consuming rate limit budget
	if config.APIToken == "" {
		return nil, fmt.Errorf("Grafana API token is required but not configured")
	}

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

	t.logger.Printf("Grafana API call: %s %s", method, path)

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
			t.logger.Printf("Grafana using proxy: %s", proxyURL.Host)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via grafana_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Grafana uses Bearer token auth
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

// cachedGet performs a cached GET request to Grafana API
func (t *GrafanaTool) cachedGet(ctx context.Context, incidentID, path string, queryParams url.Values, ttl time.Duration, logicalName ...string) ([]byte, error) {
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
		return nil, fmt.Errorf("Grafana URL not configured")
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

// doPost performs a non-cached POST request to Grafana API (for write operations)
func (t *GrafanaTool) doPost(ctx context.Context, incidentID, path string, reqBody interface{}, logicalName ...string) ([]byte, error) {
	config, err := t.getConfig(ctx, incidentID, logicalName...)
	if err != nil {
		return nil, err
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	return t.doRequest(ctx, config, http.MethodPost, path, nil, strings.NewReader(string(bodyJSON)))
}
