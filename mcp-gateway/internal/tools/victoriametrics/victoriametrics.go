package victoriametrics

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	InstantQueryTTL  = 15 * time.Second // Instant query cache TTL
	RangeQueryTTL    = 30 * time.Second // Range query cache TTL
	LabelValuesTTL   = 60 * time.Second // Label values cache TTL
	SeriesTTL        = 30 * time.Second // Series cache TTL
)

// VMConfig holds VictoriaMetrics connection configuration
type VMConfig struct {
	URL         string
	AuthMethod  string // "none", "bearer_token", "basic_auth"
	BearerToken string
	Username    string
	Password    string
	VerifySSL   bool
	Timeout     int
	UseProxy    bool
	ProxyURL    string
}

// VictoriaMetricsTool handles VictoriaMetrics API operations
type VictoriaMetricsTool struct {
	logger        *log.Logger
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses (15-60 sec TTL)
	rateLimiter   *ratelimit.Limiter
}

// NewVictoriaMetricsTool creates a new VictoriaMetrics tool with optional rate limiter
func NewVictoriaMetricsTool(logger *log.Logger, limiter *ratelimit.Limiter) *VictoriaMetricsTool {
	return &VictoriaMetricsTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *VictoriaMetricsTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// PrometheusResponse represents a standard Prometheus API response
type PrometheusResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data,omitempty"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:victoria_metrics", incidentID)
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

// clampTimeout ensures timeout is within a safe range (1-300 seconds), defaulting to 30.
func clampTimeout(timeout int) int {
	if timeout <= 0 {
		return 30
	}
	if timeout > 300 {
		return 300
	}
	return timeout
}

// getConfig fetches VictoriaMetrics configuration from database with caching.
func (t *VictoriaMetricsTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*VMConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "victoria_metrics", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*VMConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "victoria_metrics", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get VictoriaMetrics credentials: %w", err)
	}

	config := &VMConfig{
		AuthMethod: "bearer_token",
		VerifySSL:  true,
		Timeout:    30,
	}

	settings := creds.Settings

	if u, ok := settings["vm_url"].(string); ok {
		config.URL = strings.TrimSuffix(u, "/")
	}

	if method, ok := settings["vm_auth_method"].(string); ok {
		config.AuthMethod = method
	}

	if token, ok := settings["vm_bearer_token"].(string); ok {
		config.BearerToken = token
	}

	if user, ok := settings["vm_username"].(string); ok {
		config.Username = user
	}

	if pass, ok := settings["vm_password"].(string); ok {
		config.Password = pass
	}

	if verify, ok := settings["vm_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["vm_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	// Clamp timeout to safe range to prevent unlimited or negative timeouts
	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.VictoriaMetricsEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *VictoriaMetricsTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
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

// doRequest performs an HTTP request to VictoriaMetrics with rate limiting
func (t *VictoriaMetricsTool) doRequest(ctx context.Context, config *VMConfig, method, path string, queryParams url.Values) ([]byte, error) {
	// Apply rate limiting
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	// Build full URL
	fullURL := config.URL + path
	if method == http.MethodGet && len(queryParams) > 0 {
		fullURL += "?" + queryParams.Encode()
	}

	t.logger.Printf("VictoriaMetrics API call: %s %s", method, path)

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
			t.logger.Printf("VictoriaMetrics using proxy: %s", proxyURL.Host)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via vm_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	var body io.Reader
	if method == http.MethodPost && len(queryParams) > 0 {
		body = strings.NewReader(queryParams.Encode())
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if method == http.MethodPost && body != nil {
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Inject auth header based on config
	switch config.AuthMethod {
	case "bearer_token":
		if config.BearerToken == "" {
			return nil, fmt.Errorf("auth_method is 'bearer_token' but no token configured")
		}
		httpReq.Header.Set("Authorization", "Bearer "+config.BearerToken)
	case "basic_auth":
		if config.Username == "" {
			return nil, fmt.Errorf("auth_method is 'basic_auth' but no username configured")
		}
		httpReq.SetBasicAuth(config.Username, config.Password)
	case "none":
		// No auth
	default:
		return nil, fmt.Errorf("unknown auth_method '%s'", config.AuthMethod)
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// errVMAPI is returned when VictoriaMetrics returns a valid Prometheus response with error status.
// This is distinct from parse failures (non-Prometheus responses).
type errVMAPI struct {
	ErrorType string
	Message   string
}

func (e *errVMAPI) Error() string {
	return fmt.Sprintf("VictoriaMetrics API error (%s): %s", e.ErrorType, e.Message)
}

// parsePrometheusResponse checks the Prometheus response status and extracts data or error.
// Returns *errVMAPI for valid Prometheus error responses, or a generic error for non-Prometheus payloads.
func parsePrometheusResponse(body []byte) (json.RawMessage, error) {
	var promResp PrometheusResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// No "status" field means this isn't a Prometheus-format response
	if promResp.Status == "" {
		return nil, fmt.Errorf("not a Prometheus response: missing status field")
	}

	if promResp.Status != "success" {
		return nil, &errVMAPI{ErrorType: promResp.ErrorType, Message: promResp.Error}
	}

	return promResp.Data, nil
}

// cachedRequest performs a cached HTTP request to VictoriaMetrics
func (t *VictoriaMetricsTool) cachedRequest(ctx context.Context, incidentID, method, path string, params url.Values, ttl time.Duration, logicalName ...string) (json.RawMessage, error) {
	cacheKey := responseCacheKey(path, params)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("logical:%s:%s", logicalName[0], cacheKey)
	} else {
		cacheKey = fmt.Sprintf("incident:%s:%s", incidentID, cacheKey)
	}

	// Check response cache
	if cached, ok := t.responseCache.Get(cacheKey); ok {
		if result, ok := cached.(json.RawMessage); ok {
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
		return nil, fmt.Errorf("VictoriaMetrics URL not configured")
	}

	body, err := t.doRequest(ctx, config, method, path, params)
	if err != nil {
		return nil, err
	}

	data, err := parsePrometheusResponse(body)
	if err != nil {
		return nil, err
	}

	// Cache the result
	t.responseCache.SetWithTTL(cacheKey, data, ttl)
	t.logger.Printf("Response cached for %s (TTL: %v)", path, ttl)

	return data, nil
}

// InstantQuery executes a PromQL instant query
func (t *VictoriaMetricsTool) InstantQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required%s", validation.SuggestParam("query", args))
	}

	params := url.Values{}
	params.Set("query", query)

	if v, ok := args["time"].(string); ok && v != "" {
		params.Set("time", v)
	}
	if v, ok := args["step"].(string); ok && v != "" {
		params.Set("step", v)
	}
	if v, ok := args["timeout"].(string); ok && v != "" {
		params.Set("timeout", v)
	}

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/query", params, InstantQueryTTL, logicalName)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// RangeQuery executes a PromQL range query
func (t *VictoriaMetricsTool) RangeQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required%s", validation.SuggestParam("query", args))
	}

	start, ok := args["start"].(string)
	if !ok || start == "" {
		return "", fmt.Errorf("start is required%s", validation.SuggestParam("start", args))
	}

	end, ok := args["end"].(string)
	if !ok || end == "" {
		return "", fmt.Errorf("end is required%s", validation.SuggestParam("end", args))
	}

	step, ok := args["step"].(string)
	if !ok || step == "" {
		return "", fmt.Errorf("step is required%s", validation.SuggestParam("step", args))
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("step", step)

	if v, ok := args["timeout"].(string); ok && v != "" {
		params.Set("timeout", v)
	}

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/query_range", params, RangeQueryTTL, logicalName)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// LabelValues retrieves label values for a given label name
func (t *VictoriaMetricsTool) LabelValues(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	labelName, ok := args["label_name"].(string)
	if !ok || labelName == "" {
		return "", fmt.Errorf("label_name is required%s", validation.SuggestParam("label_name", args))
	}

	params := url.Values{}

	if v, ok := args["match"].(string); ok && v != "" {
		params.Set("match[]", v)
	}
	if v, ok := args["start"].(string); ok && v != "" {
		params.Set("start", v)
	}
	if v, ok := args["end"].(string); ok && v != "" {
		params.Set("end", v)
	}

	path := fmt.Sprintf("/api/v1/label/%s/values", url.PathEscape(labelName))

	result, err := t.cachedRequest(ctx, incidentID, http.MethodGet, path, params, LabelValuesTTL, logicalName)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// Series finds series matching a set of label matchers
func (t *VictoriaMetricsTool) Series(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	match, ok := args["match"].(string)
	if !ok || match == "" {
		return "", fmt.Errorf("match is required%s", validation.SuggestParam("match", args))
	}

	params := url.Values{}
	params.Set("match[]", match)

	if v, ok := args["start"].(string); ok && v != "" {
		params.Set("start", v)
	}
	if v, ok := args["end"].(string); ok && v != "" {
		params.Set("end", v)
	}

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/series", params, SeriesTTL, logicalName)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// APIRequest performs a generic API request (not cached)
func (t *VictoriaMetricsTool) APIRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("path is required%s", validation.SuggestParam("path", args))
	}

	// Decode path repeatedly until stable to prevent double-encoding bypass
	decodedPath := path
	for {
		next, err := url.PathUnescape(decodedPath)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
		if next == decodedPath {
			break
		}
		decodedPath = next
	}
	if !strings.HasPrefix(decodedPath, "/") || strings.Contains(decodedPath, "..") {
		return "", fmt.Errorf("invalid path: must start with / and not contain '..'")
	}
	// Reject paths containing query strings or fragments to prevent URL manipulation
	if strings.ContainsAny(decodedPath, "?#") {
		return "", fmt.Errorf("invalid path: must not contain query string or fragment (use params argument instead)")
	}
	path = decodedPath

	method := http.MethodGet
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	if method != http.MethodGet && method != http.MethodPost {
		return "", fmt.Errorf("unsupported HTTP method: %s (allowed: GET, POST)", method)
	}

	params := url.Values{}
	if p, ok := args["params"].(map[string]interface{}); ok {
		for k, v := range p {
			switch val := v.(type) {
			case []interface{}:
				for _, item := range val {
					params.Add(k, fmt.Sprintf("%v", item))
				}
			default:
				params.Set(k, fmt.Sprintf("%v", val))
			}
		}
	}

	// Resolve config and make request directly (no caching)
	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	if config.URL == "" {
		return "", fmt.Errorf("VictoriaMetrics URL not configured")
	}

	body, err := t.doRequest(ctx, config, method, path, params)
	if err != nil {
		return "", err
	}

	// Try to parse as Prometheus response, fall back to raw body
	data, err := parsePrometheusResponse(body)
	if err != nil {
		// If it's a valid Prometheus error response, propagate the error
		var vmErr *errVMAPI
		if errors.As(err, &vmErr) {
			return "", err
		}
		// Not a standard Prometheus response, return raw body
		return string(body), nil
	}

	return string(data), nil
}
