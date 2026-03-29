package k8s

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
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
	} else if config.CACert != "" {
		// Load custom CA certificate for clusters using private/internal CAs
		certPool, err := x509.SystemCertPool()
		if err != nil {
			certPool = x509.NewCertPool()
		}
		if !certPool.AppendCertsFromPEM([]byte(config.CACert)) {
			t.logger.Printf("Warning: failed to parse custom CA certificate, using system CAs only")
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = certPool
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

// GetNamespaces lists all namespaces in the cluster
func (t *K8sTool) GetNamespaces(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	body, err := t.cachedGet(ctx, incidentID, "/api/v1/namespaces", params, NSCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetPods lists pods in a namespace with optional selectors
func (t *K8sTool) GetPods(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	// If a specific pod name is given, redirect to detail endpoint
	if name := optionalString(args, "name"); name != "" {
		path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", url.PathEscape(namespace), url.PathEscape(name))
		body, err := t.cachedGet(ctx, incidentID, path, nil, PodCacheTTL, logicalName)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, PodCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetPodDetail retrieves detailed information about a specific pod
func (t *K8sTool) GetPodDetail(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}
	name, err := requireString(args, "name")
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", url.PathEscape(namespace), url.PathEscape(name))
	body, err := t.cachedGet(ctx, incidentID, path, nil, PodCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetPodLogs retrieves logs from a specific pod container
func (t *K8sTool) GetPodLogs(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}
	name, err := requireString(args, "name")
	if err != nil {
		return "", err
	}

	params := url.Values{}

	if container := optionalString(args, "container"); container != "" {
		params.Set("container", container)
	}

	// tail_lines defaults to 100
	tailLines := 100
	if v, ok := args["tail_lines"].(float64); ok && v > 0 {
		tailLines = int(v)
		if tailLines < 1 {
			tailLines = 1
		}
		if tailLines > 10000 {
			tailLines = 10000
		}
	}
	params.Set("tailLines", fmt.Sprintf("%d", tailLines))

	if v, ok := args["since_seconds"].(float64); ok && v > 0 {
		params.Set("sinceSeconds", fmt.Sprintf("%d", int(v)))
	}

	if v, ok := args["previous"].(bool); ok && v {
		params.Set("previous", "true")
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log", url.PathEscape(namespace), url.PathEscape(name))
	body, err := t.cachedGet(ctx, incidentID, path, params, LogCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetEvents lists events in a namespace
func (t *K8sTool) GetEvents(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/api/v1/namespaces/%s/events", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, EventCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDeployments lists deployments in a namespace with optional selectors
func (t *K8sTool) GetDeployments(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	// If a specific deployment name is given, redirect to detail endpoint
	if name := optionalString(args, "name"); name != "" {
		path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", url.PathEscape(namespace), url.PathEscape(name))
		body, err := t.cachedGet(ctx, incidentID, path, nil, DeployCacheTTL, logicalName)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, DeployCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDeploymentDetail retrieves detailed information about a specific deployment
func (t *K8sTool) GetDeploymentDetail(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}
	name, err := requireString(args, "name")
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", url.PathEscape(namespace), url.PathEscape(name))
	body, err := t.cachedGet(ctx, incidentID, path, nil, DeployCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetStatefulSets lists statefulsets in a namespace with optional selectors
func (t *K8sTool) GetStatefulSets(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, DeployCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDaemonSets lists daemonsets in a namespace with optional selectors
func (t *K8sTool) GetDaemonSets(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/daemonsets", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, DeployCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetJobs lists jobs in a namespace with optional selectors
func (t *K8sTool) GetJobs(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, JobCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetCronJobs lists cronjobs in a namespace with optional selectors
func (t *K8sTool) GetCronJobs(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/cronjobs", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, DeployCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetNodes lists nodes in the cluster with optional selectors
func (t *K8sTool) GetNodes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	body, err := t.cachedGet(ctx, incidentID, "/api/v1/nodes", params, NodeCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetNodeDetail retrieves detailed information about a specific node
func (t *K8sTool) GetNodeDetail(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	name, err := requireString(args, "name")
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/api/v1/nodes/%s", url.PathEscape(name))
	body, err := t.cachedGet(ctx, incidentID, path, nil, NodeCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetServices lists services in a namespace with optional selectors
func (t *K8sTool) GetServices(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/api/v1/namespaces/%s/services", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, ServiceCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetConfigMaps lists configmaps in a namespace (names/metadata only, no data)
func (t *K8sTool) GetConfigMaps(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, ServiceCacheTTL, logicalName)
	if err != nil {
		return "", err
	}

	// Strip data fields from configmaps to avoid exposing sensitive configuration
	return stripConfigMapData(body), nil
}

// stripConfigMapData removes the "data" and "binaryData" fields from each configmap in a list response
func stripConfigMapData(body []byte) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return string(body) // Return raw if not valid JSON
	}

	items, ok := parsed["items"].([]interface{})
	if !ok {
		// Single item response
		delete(parsed, "data")
		delete(parsed, "binaryData")
		result, _ := json.Marshal(parsed)
		return string(result)
	}

	for _, item := range items {
		if m, ok := item.(map[string]interface{}); ok {
			delete(m, "data")
			delete(m, "binaryData")
		}
	}

	result, _ := json.Marshal(parsed)
	return string(result)
}

// GetIngresses lists ingresses in a namespace with optional selectors
func (t *K8sTool) GetIngresses(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	namespace, err := requireString(args, "namespace")
	if err != nil {
		return "", err
	}

	params := url.Values{}
	addSelectorParams(params, args)
	addLimitParam(params, args)

	path := fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/ingresses", url.PathEscape(namespace))
	body, err := t.cachedGet(ctx, incidentID, path, params, ServiceCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// APIRequest performs a generic GET request to any Kubernetes API endpoint
func (t *K8sTool) APIRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	path, err := requireString(args, "path")
	if err != nil {
		return "", err
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

	// Reject path traversal attempts
	if strings.Contains(decodedPath, "..") {
		return "", fmt.Errorf("invalid path: must not contain '..' segments")
	}

	// Reject paths containing query strings or fragments
	if strings.ContainsAny(decodedPath, "?#") {
		return "", fmt.Errorf("invalid path: must not contain query string or fragment (use params instead)")
	}

	path = decodedPath

	// Validate path starts with /api or /apis for safety
	if path != "/api" && path != "/apis" && !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/apis/") {
		return "", fmt.Errorf("path must start with /api/ or /apis/ (got %q)", path)
	}

	// Block secrets paths to prevent credential exfiltration
	isSecretsPath := strings.HasSuffix(path, "/secrets") || strings.Contains(path, "/secrets/")
	if isSecretsPath {
		return "", fmt.Errorf("access to secrets is not allowed for security reasons")
	}

	// Block streaming parameters to prevent long-polling requests
	if p, ok := args["params"].(map[string]interface{}); ok {
		if _, exists := p["watch"]; exists {
			return "", fmt.Errorf("watch parameter is not allowed (would create a long-polling request)")
		}
		if _, exists := p["follow"]; exists {
			return "", fmt.Errorf("follow parameter is not allowed (would create a streaming request)")
		}
	}

	params := url.Values{}
	if p, ok := args["params"].(map[string]interface{}); ok {
		for k, v := range p {
			switch sv := v.(type) {
			case string:
				params.Set(k, sv)
			case float64:
				params.Set(k, fmt.Sprintf("%v", sv))
			case bool:
				params.Set(k, fmt.Sprintf("%t", sv))
			default:
				params.Set(k, fmt.Sprintf("%v", v))
			}
		}
	}

	// Apply ConfigMap data stripping when fetching configmap paths via generic API
	isConfigMapPath := strings.HasSuffix(path, "/configmaps") || strings.Contains(path, "/configmaps/")

	body, err := t.cachedGet(ctx, incidentID, path, params, ResponseCacheTTL, logicalName)
	if err != nil {
		return "", err
	}

	if isConfigMapPath {
		return stripConfigMapData(body), nil
	}
	return string(body), nil
}
