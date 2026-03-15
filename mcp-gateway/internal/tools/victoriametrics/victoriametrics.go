package victoriametrics

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
	ConfigCacheTTL     = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL   = 30 * time.Second // Default API response cache TTL
	CacheCleanupTick   = time.Minute      // Background cleanup interval
	InstantQueryTTL    = 15 * time.Second // Instant query cache TTL
	RangeQueryTTL      = 30 * time.Second // Range query cache TTL
	LabelValuesTTL     = 60 * time.Second // Label values cache TTL
	SeriesTTL          = 30 * time.Second // Series cache TTL
)

// VMConfig holds VictoriaMetrics connection configuration
type VMConfig struct {
	URL        string
	AuthMethod string // "none", "bearer_token", "basic_auth"
	BearerToken string
	Username   string
	Password   string
	VerifySSL  bool
	Timeout    int
	UseProxy   bool
	ProxyURL   string
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

// extractInstanceID extracts the optional tool_instance_id from tool arguments.
func extractInstanceID(args map[string]interface{}) *uint {
	if v, ok := args["tool_instance_id"].(float64); ok && v > 0 {
		id := uint(v)
		return &id
	}
	return nil
}

// getConfig fetches VictoriaMetrics configuration from database with caching.
func (t *VictoriaMetricsTool) getConfig(ctx context.Context, incidentID string, instanceID *uint) (*VMConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if instanceID != nil {
		cacheKey = fmt.Sprintf("creds:instance:%d", *instanceID)
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*VMConfig); ok {
			t.logger.Printf("Config cache hit for incident %s", incidentID)
			return config, nil
		}
	}

	creds, err := database.ResolveToolCredentials(ctx, incidentID, "victoria_metrics", instanceID)
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

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for incident %s", incidentID)

	return config, nil
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
	transport := &http.Transport{}

	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL %s: %v, proceeding without proxy", config.ProxyURL, err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			t.logger.Printf("VictoriaMetrics using proxy: %s", config.ProxyURL)
		}
	} else {
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
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
		if config.BearerToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+config.BearerToken)
		}
	case "basic_auth":
		if config.Username != "" {
			httpReq.SetBasicAuth(config.Username, config.Password)
		}
	case "none":
		// No auth
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// parsePrometheusResponse checks the Prometheus response status and extracts data or error
func parsePrometheusResponse(body []byte) (json.RawMessage, error) {
	var promResp PrometheusResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if promResp.Status != "success" {
		return nil, fmt.Errorf("VictoriaMetrics API error (%s): %s", promResp.ErrorType, promResp.Error)
	}

	return promResp.Data, nil
}

// cachedRequest performs a cached HTTP request to VictoriaMetrics
func (t *VictoriaMetricsTool) cachedRequest(ctx context.Context, incidentID, method, path string, params url.Values, ttl time.Duration, instanceID *uint) (json.RawMessage, error) {
	cacheKey := responseCacheKey(path, params)
	if instanceID != nil {
		cacheKey = fmt.Sprintf("inst:%d:%s", *instanceID, cacheKey)
	}

	// Check response cache
	if cached, ok := t.responseCache.Get(cacheKey); ok {
		if result, ok := cached.(json.RawMessage); ok {
			t.logger.Printf("Response cache hit for %s", path)
			return result, nil
		}
	}

	// Resolve config and make request
	config, err := t.getConfig(ctx, incidentID, instanceID)
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
	instanceID := extractInstanceID(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required")
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

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/query", params, InstantQueryTTL, instanceID)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// RangeQuery executes a PromQL range query
func (t *VictoriaMetricsTool) RangeQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	instanceID := extractInstanceID(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required")
	}

	start, ok := args["start"].(string)
	if !ok || start == "" {
		return "", fmt.Errorf("start is required")
	}

	end, ok := args["end"].(string)
	if !ok || end == "" {
		return "", fmt.Errorf("end is required")
	}

	step, ok := args["step"].(string)
	if !ok || step == "" {
		return "", fmt.Errorf("step is required")
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("step", step)

	if v, ok := args["timeout"].(string); ok && v != "" {
		params.Set("timeout", v)
	}

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/query_range", params, RangeQueryTTL, instanceID)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// LabelValues retrieves label values for a given label name
func (t *VictoriaMetricsTool) LabelValues(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	instanceID := extractInstanceID(args)

	labelName, ok := args["label_name"].(string)
	if !ok || labelName == "" {
		return "", fmt.Errorf("label_name is required")
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

	result, err := t.cachedRequest(ctx, incidentID, http.MethodGet, path, params, LabelValuesTTL, instanceID)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// Series finds series matching a set of label matchers
func (t *VictoriaMetricsTool) Series(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	instanceID := extractInstanceID(args)

	match, ok := args["match"].(string)
	if !ok || match == "" {
		return "", fmt.Errorf("match is required")
	}

	params := url.Values{}
	params.Set("match[]", match)

	if v, ok := args["start"].(string); ok && v != "" {
		params.Set("start", v)
	}
	if v, ok := args["end"].(string); ok && v != "" {
		params.Set("end", v)
	}

	result, err := t.cachedRequest(ctx, incidentID, http.MethodPost, "/api/v1/series", params, SeriesTTL, instanceID)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// APIRequest performs a generic API request (not cached)
func (t *VictoriaMetricsTool) APIRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	instanceID := extractInstanceID(args)

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("path is required")
	}

	method := http.MethodGet
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	params := url.Values{}
	if p, ok := args["params"].(map[string]interface{}); ok {
		for k, v := range p {
			params.Set(k, fmt.Sprintf("%v", v))
		}
	}

	// Resolve config and make request directly (no caching)
	config, err := t.getConfig(ctx, incidentID, instanceID)
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
		// Not a standard Prometheus response, return raw body
		return string(body), nil
	}

	return string(data), nil
}

// ClearCache clears all caches
func (t *VictoriaMetricsTool) ClearCache() {
	t.configCache.Clear()
	t.responseCache.Clear()
	t.logger.Println("All caches cleared")
}
