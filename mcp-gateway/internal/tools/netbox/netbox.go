package netbox

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
	ResponseCacheTTL = 60 * time.Second  // Default API response cache TTL (CMDB data is mostly static)
	CacheCleanupTick = time.Minute       // Background cleanup interval
	DCIMCacheTTL     = 60 * time.Second  // Device/site/rack/interface/cable data
	IPAMCacheTTL     = 60 * time.Second  // IP/prefix/VLAN/VRF data
	VMCacheTTL       = 60 * time.Second  // VM/cluster data
	CircuitCacheTTL  = 120 * time.Second // Circuit/provider data (rarely changes)
	TenancyCacheTTL  = 120 * time.Second // Tenant data (rarely changes)
)

// NetBoxConfig holds NetBox connection configuration
type NetBoxConfig struct {
	URL       string // NetBox instance URL (e.g. https://netbox.example.com)
	APIToken  string // API token for authentication
	VerifySSL bool
	Timeout   int
	UseProxy  bool
	ProxyURL  string
}

// NetBoxTool handles NetBox API operations
type NetBoxTool struct {
	logger        *log.Logger
	configCache   *cache.Cache // Cache for credentials (5 min TTL)
	responseCache *cache.Cache // Cache for API responses (60-120 sec TTL)
	rateLimiter   *ratelimit.Limiter
}

// NewNetBoxTool creates a new NetBox tool with optional rate limiter
func NewNetBoxTool(logger *log.Logger, limiter *ratelimit.Limiter) *NetBoxTool {
	return &NetBoxTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *NetBoxTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:netbox", incidentID)
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

// getConfig fetches NetBox configuration from database with caching.
func (t *NetBoxTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*NetBoxConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "netbox", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*NetBoxConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "netbox", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get NetBox credentials: %w", err)
	}

	config := &NetBoxConfig{
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if u, ok := settings["netbox_url"].(string); ok {
		config.URL = strings.TrimSuffix(u, "/")
	}

	if token, ok := settings["netbox_api_token"].(string); ok {
		config.APIToken = token
	}

	if verify, ok := settings["netbox_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["netbox_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.NetBoxEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *NetBoxTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
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

// doRequest performs an HTTP request to NetBox API with rate limiting
func (t *NetBoxTool) doRequest(ctx context.Context, config *NetBoxConfig, method, path string, queryParams url.Values) ([]byte, error) {
	// Validate token before consuming rate limit budget
	if config.APIToken == "" {
		return nil, fmt.Errorf("NetBox API token is required but not configured")
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

	t.logger.Printf("NetBox API call: %s %s", method, path)

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
			t.logger.Printf("NetBox using proxy: %s", proxyURL.Host)
		}
	} else {
		// Explicitly disable proxy (ignore HTTP_PROXY env vars)
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via netbox_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// NetBox uses Token authentication (not Bearer)
	httpReq.Header.Set("Authorization", "Token "+config.APIToken)
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

// cachedGet performs a cached GET request to NetBox API
func (t *NetBoxTool) cachedGet(ctx context.Context, incidentID, path string, queryParams url.Values, ttl time.Duration, logicalName ...string) ([]byte, error) {
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
		return nil, fmt.Errorf("NetBox URL not configured")
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

// addPaginationParams adds optional limit and offset parameters to query values.
// NetBox uses limit/offset pagination (not page_number/page_size like Catchpoint).
func addPaginationParams(params url.Values, args map[string]interface{}) {
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit := int(v)
		if limit > 1000 {
			limit = 1000 // NetBox default max
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if v, ok := args["offset"].(float64); ok && v >= 0 {
		params.Set("offset", fmt.Sprintf("%d", int(v)))
	}
}

// addSearchParams adds common NetBox search/filter parameters to query values.
// These are the most common filters shared across many NetBox endpoints.
func addSearchParams(params url.Values, args map[string]interface{}, filters ...string) {
	for _, filter := range filters {
		if v, ok := args[filter].(string); ok && v != "" {
			params.Set(filter, v)
		} else if v, ok := args[filter].(float64); ok {
			params.Set(filter, fmt.Sprintf("%d", int(v)))
		} else if v, ok := args[filter].(bool); ok {
			params.Set(filter, fmt.Sprintf("%t", v))
		}
	}
}

// --- DCIM Methods ---

// GetDevices lists/searches devices with filters
func (t *NetBoxTool) GetDevices(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "site", "role", "status", "tag", "platform", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/devices/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDevice retrieves a single device by ID
func (t *NetBoxTool) GetDevice(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	id, ok := args["id"].(float64)
	if !ok {
		// Also accept string IDs
		if idStr, ok := args["id"].(string); ok && idStr != "" {
			path := fmt.Sprintf("/api/dcim/devices/%s/", url.PathEscape(idStr))
			body, err := t.cachedGet(ctx, incidentID, path, nil, DCIMCacheTTL, logicalName)
			if err != nil {
				return "", err
			}
			return string(body), nil
		}
		return "", fmt.Errorf("id is required%s", validation.SuggestParam("id", args))
	}

	path := fmt.Sprintf("/api/dcim/devices/%d/", int(id))
	body, err := t.cachedGet(ctx, incidentID, path, nil, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetInterfaces lists device interfaces with filters
func (t *NetBoxTool) GetInterfaces(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "device", "device_id", "name", "type", "enabled")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/interfaces/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetSites lists sites with filters
func (t *NetBoxTool) GetSites(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "region", "status", "tag", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/sites/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetRacks lists racks with filters
func (t *NetBoxTool) GetRacks(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "site", "name", "status", "role", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/racks/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetCables lists cable connections with filters
func (t *NetBoxTool) GetCables(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "device", "site", "type", "status")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/cables/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDeviceTypes lists device types/models with filters
func (t *NetBoxTool) GetDeviceTypes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "manufacturer", "model", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/dcim/device-types/", params, DCIMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- IPAM Methods ---

// GetIPAddresses lists/searches IP addresses with filters
func (t *NetBoxTool) GetIPAddresses(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "address", "device", "interface", "vrf", "tenant", "status", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/ipam/ip-addresses/", params, IPAMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetPrefixes lists IP prefixes/subnets with filters
func (t *NetBoxTool) GetPrefixes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "prefix", "site", "vrf", "vlan", "tenant", "status", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/ipam/prefixes/", params, IPAMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetVLANs lists VLANs with filters
func (t *NetBoxTool) GetVLANs(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "vid", "name", "site", "group", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/ipam/vlans/", params, IPAMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetVRFs lists VRFs with filters
func (t *NetBoxTool) GetVRFs(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/ipam/vrfs/", params, IPAMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- Circuits Methods ---

// GetCircuits lists circuits with filters
func (t *NetBoxTool) GetCircuits(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "provider", "type", "status", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/circuits/circuits/", params, CircuitCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetProviders lists circuit providers with filters
func (t *NetBoxTool) GetProviders(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/circuits/providers/", params, CircuitCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- Virtualization Methods ---

// GetVirtualMachines lists virtual machines with filters
func (t *NetBoxTool) GetVirtualMachines(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "cluster", "site", "status", "role", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/virtualization/virtual-machines/", params, VMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetClusters lists clusters with filters
func (t *NetBoxTool) GetClusters(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "type", "group", "site", "tenant", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/virtualization/clusters/", params, VMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetVMInterfaces lists VM interfaces with filters
func (t *NetBoxTool) GetVMInterfaces(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "virtual_machine", "name", "enabled")

	body, err := t.cachedGet(ctx, incidentID, "/api/virtualization/interfaces/", params, VMCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- Tenancy Methods ---

// GetTenants lists tenants with filters
func (t *NetBoxTool) GetTenants(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "group", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/tenancy/tenants/", params, TenancyCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetTenantGroups lists tenant groups with filters
func (t *NetBoxTool) GetTenantGroups(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	addPaginationParams(params, args)
	addSearchParams(params, args, "name", "q")

	body, err := t.cachedGet(ctx, incidentID, "/api/tenancy/tenant-groups/", params, TenancyCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// --- Generic Method ---

// APIRequest performs a generic read-only API request to any NetBox endpoint
func (t *NetBoxTool) APIRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("path is required%s", validation.SuggestParam("path", args))
	}

	// Ensure path starts with /api/
	if !strings.HasPrefix(path, "/api/") {
		path = strings.TrimLeft(path, "/")
		// Strip "api/" or bare "api" prefix to avoid double "/api/api/"
		if path == "api" {
			path = ""
		} else {
			path = strings.TrimPrefix(path, "api/")
		}
		path = "/api/" + path
	}

	// Decode path repeatedly until stable to prevent double-encoding bypass
	// (consistent with VictoriaMetrics pattern)
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
	path = decodedPath

	// Reject paths containing query strings or fragments
	if strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("invalid path: must not contain query string or fragment (use query_params instead)")
	}

	// Ensure path ends with /
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	params := url.Values{}
	addPaginationParams(params, args)

	// Add any extra query_params
	if qp, ok := args["query_params"].(map[string]interface{}); ok {
		for k, v := range qp {
			switch sv := v.(type) {
			case string:
				params.Set(k, sv)
			case float64:
				params.Set(k, fmt.Sprintf("%d", int(sv)))
			case bool:
				params.Set(k, fmt.Sprintf("%t", sv))
			case []interface{}:
				for _, elem := range sv {
					switch ev := elem.(type) {
					case string:
						params.Add(k, ev)
					case float64:
						params.Add(k, fmt.Sprintf("%d", int(ev)))
					case bool:
						params.Add(k, fmt.Sprintf("%t", ev))
					default:
						return "", fmt.Errorf("unsupported type in query_params array for key %q", k)
					}
				}
			default:
				return "", fmt.Errorf("unsupported type for query_params key %q: must be string, number, bool, or array", k)
			}
		}
	}

	// Select cache TTL based on endpoint path
	ttl := DCIMCacheTTL
	if strings.HasPrefix(path, "/api/circuits/") || strings.HasPrefix(path, "/api/tenancy/") {
		ttl = CircuitCacheTTL
	}

	body, err := t.cachedGet(ctx, incidentID, path, params, ttl, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
