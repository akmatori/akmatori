package k8s

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
	"github.com/akmatori/mcp-gateway/internal/validation"
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL = 60 * time.Second  // Default API response cache TTL
	CacheCleanupTick = time.Minute       // Background cleanup interval
	PodCacheTTL      = 30 * time.Second  // Pod data (changes frequently)
	LogCacheTTL      = 15 * time.Second  // Logs (very dynamic)
	EventCacheTTL    = 15 * time.Second  // Events (very dynamic)
	DeployCacheTTL   = 60 * time.Second  // Deployment/StatefulSet/DaemonSet/CronJob data
	JobCacheTTL      = 30 * time.Second  // Job data (changes with completions)
	NodeCacheTTL     = 120 * time.Second // Node data (mostly static)
	ServiceCacheTTL  = 120 * time.Second // Service data (mostly static)
	NSCacheTTL       = 120 * time.Second // Namespace data (rarely changes)
)

// K8sConfig holds Kubernetes connection configuration
type K8sConfig struct {
	URL       string // Kubernetes API server URL (e.g. https://k8s.example.com)
	Token     string // Bearer token for authentication
	CACert    string // Optional CA certificate for TLS verification
	VerifySSL bool
	Timeout   int
	UseProxy  bool
	ProxyURL  string
}

// K8sTool handles Kubernetes API operations
type K8sTool struct {
	logger        *log.Logger
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses
	rateLimiter   *ratelimit.Limiter
}

// NewK8sTool creates a new Kubernetes tool with optional rate limiter
func NewK8sTool(logger *log.Logger, limiter *ratelimit.Limiter) *K8sTool {
	return &K8sTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *K8sTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:kubernetes", incidentID)
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

// getConfig fetches Kubernetes configuration from database with caching.
func (t *K8sTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*K8sConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "kubernetes", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*K8sConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "kubernetes", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes credentials: %w", err)
	}

	config := &K8sConfig{
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if u, ok := settings["k8s_url"].(string); ok {
		config.URL = strings.TrimSuffix(u, "/")
	}

	if token, ok := settings["k8s_token"].(string); ok {
		config.Token = token
	}

	if caCert, ok := settings["k8s_ca_cert"].(string); ok {
		config.CACert = caCert
	}

	if verify, ok := settings["k8s_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["k8s_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.K8sEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *K8sTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
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

// doRequest performs an HTTP request to Kubernetes API with rate limiting
func (t *K8sTool) doRequest(ctx context.Context, config *K8sConfig, method, path string, queryParams url.Values) ([]byte, error) {
	// Validate token before consuming rate limit budget
	if config.Token == "" {
		return nil, fmt.Errorf("Kubernetes API token is required but not configured")
	}

	// Apply rate limiting
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	// Build full URL
	fullURL := buildURL(config.URL, path, queryParams)

	t.logger.Printf("K8s API call: %s %s", method, path)

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
			t.logger.Printf("K8s using proxy: %s", proxyURL.Host)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via k8s_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Kubernetes uses Bearer token authentication
	httpReq.Header.Set("Authorization", "Bearer "+config.Token)
	httpReq.Header.Set("Accept", "application/json")

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

// buildURL constructs a full URL from base URL, path, and query parameters
func buildURL(baseURL, path string, params url.Values) string {
	fullURL := baseURL + path
	if len(params) > 0 {
		fullURL += "?" + params.Encode()
	}
	return fullURL
}

// cachedGet performs a cached GET request to Kubernetes API
func (t *K8sTool) cachedGet(ctx context.Context, incidentID, path string, queryParams url.Values, ttl time.Duration, logicalName ...string) ([]byte, error) {
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
		return nil, fmt.Errorf("Kubernetes API URL not configured")
	}

	respBody, err := t.doRequest(ctx, config, http.MethodGet, path, queryParams)
	if err != nil {
		return nil, err
	}

	// Cache the result
	t.responseCache.SetWithTTL(cacheKey, respBody, ttl)
	t.logger.Printf("Response cached for %s (TTL: %v)", path, ttl)

	return respBody, nil
}

// requireString extracts a required string parameter from args, returning a validation error if missing
func requireString(args map[string]interface{}, key string) (string, error) {
	if v, ok := args[key].(string); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is required%s", key, validation.SuggestParam(key, args))
}

// optionalString extracts an optional string parameter from args
func optionalString(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// addLabelSelector adds label_selector and field_selector to query params if present
func addSelectorParams(params url.Values, args map[string]interface{}) {
	if v := optionalString(args, "label_selector"); v != "" {
		params.Set("labelSelector", v)
	}
	if v := optionalString(args, "field_selector"); v != "" {
		params.Set("fieldSelector", v)
	}
}

// addLimitParam adds limit to query params if present
func addLimitParam(params url.Values, args map[string]interface{}) {
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit := int(v)
		if limit > 1000 {
			limit = 1000
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
}
