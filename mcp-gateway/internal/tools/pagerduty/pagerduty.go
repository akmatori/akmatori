package pagerduty

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
	IncidentCacheTTL = 15 * time.Second // Incidents and alerts cache TTL
	ServiceCacheTTL  = 60 * time.Second // Services and escalation policies cache TTL
	OnCallCacheTTL   = 30 * time.Second // On-call schedules cache TTL
	ChangeCacheTTL   = 30 * time.Second // Recent changes cache TTL
)

// PagerDutyConfig holds PagerDuty connection configuration
type PagerDutyConfig struct {
	URL       string // Default: https://api.pagerduty.com
	APIToken  string // PagerDuty REST API token (v2)
	VerifySSL bool
	Timeout   int
	UseProxy  bool
	ProxyURL  string
}

// PagerDutyTool handles PagerDuty API operations
type PagerDutyTool struct {
	logger        *log.Logger
	configCache   *cache.Cache
	responseCache *cache.Cache
	rateLimiter   *ratelimit.Limiter
}

// NewPagerDutyTool creates a new PagerDuty tool with optional rate limiter
func NewPagerDutyTool(logger *log.Logger, limiter *ratelimit.Limiter) *PagerDutyTool {
	return &PagerDutyTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
}

// Stop cleans up cache resources
func (t *PagerDutyTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:pagerduty", incidentID)
}

// responseCacheKey returns the cache key for API responses
func responseCacheKey(path string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	hash := sha256.Sum256(paramsJSON)
	return fmt.Sprintf("%s:%s", path, hex.EncodeToString(hash[:8]))
}

// extractLogicalName extracts the optional logical_name from tool arguments.
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

// getConfig fetches PagerDuty configuration from database with caching.
func (t *PagerDutyTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*PagerDutyConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "pagerduty", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*PagerDutyConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "pagerduty", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get PagerDuty credentials: %w", err)
	}

	config := &PagerDutyConfig{
		URL:       "https://api.pagerduty.com",
		VerifySSL: true,
		Timeout:   30,
	}

	settings := creds.Settings

	if u, ok := settings["pagerduty_url"].(string); ok {
		config.URL = trimTrailingSlash(u)
	}

	if token, ok := settings["pagerduty_api_token"].(string); ok {
		config.APIToken = token
	}

	if verify, ok := settings["pagerduty_verify_ssl"].(bool); ok {
		config.VerifySSL = verify
	}

	if timeout, ok := settings["pagerduty_timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	config.Timeout = clampTimeout(config.Timeout)

	// Fetch proxy settings from database (also cached)
	proxySettings := t.getCachedProxySettings(ctx)
	if proxySettings != nil && proxySettings.ProxyURL != "" && proxySettings.PagerDutyEnabled {
		config.UseProxy = true
		config.ProxyURL = proxySettings.ProxyURL
	}

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// getCachedProxySettings fetches proxy settings with caching
func (t *PagerDutyTool) getCachedProxySettings(ctx context.Context) *database.ProxySettings {
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

// trimTrailingSlash removes a trailing slash from a URL string.
func trimTrailingSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}

// doRequest performs an HTTP request to PagerDuty API with rate limiting
func (t *PagerDutyTool) doRequest(ctx context.Context, config *PagerDutyConfig, method, path string, queryParams url.Values, body io.Reader) ([]byte, error) {
	// Validate token before consuming rate limit budget
	if config.APIToken == "" {
		return nil, fmt.Errorf("PagerDuty API token is required but not configured")
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

	t.logger.Printf("PagerDuty API call: %s %s", method, path)

	// Create HTTP transport with explicit proxy configuration
	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	// Handle proxy settings
	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL: %v, proceeding without proxy", err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			t.logger.Printf("PagerDuty using proxy: %s", proxyURL.Host)
		}
	} else {
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via pagerduty_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// PagerDuty uses Token-based authentication
	httpReq.Header.Set("Authorization", "Token token="+config.APIToken)
	httpReq.Header.Set("Content-Type", "application/json")

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

// cachedGet performs a cached GET request to PagerDuty API
func (t *PagerDutyTool) cachedGet(ctx context.Context, incidentID, path string, queryParams url.Values, ttl time.Duration, logicalName ...string) ([]byte, error) {
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
		return nil, fmt.Errorf("PagerDuty URL not configured")
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

// GetIncidents retrieves incidents from PagerDuty with optional filters
func (t *PagerDutyTool) GetIncidents(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}

	if v, ok := args["statuses"].(string); ok && v != "" {
		// PagerDuty accepts multiple statuses[] params
		params.Set("statuses[]", v)
	}
	if v, ok := args["urgencies"].(string); ok && v != "" {
		params.Set("urgencies[]", v)
	}
	if v, ok := args["service_ids"].(string); ok && v != "" {
		params.Set("service_ids[]", v)
	}
	if v, ok := args["since"].(string); ok && v != "" {
		params.Set("since", v)
	}
	if v, ok := args["until"].(string); ok && v != "" {
		params.Set("until", v)
	}
	if v, ok := args["sort_by"].(string); ok && v != "" {
		params.Set("sort_by", v)
	}
	if v, ok := args["limit"].(float64); ok && v > 0 {
		params.Set("limit", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["offset"].(float64); ok && v > 0 {
		params.Set("offset", fmt.Sprintf("%d", int(v)))
	}

	body, err := t.cachedGet(ctx, incidentID, "/incidents", params, IncidentCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetIncident retrieves a single incident by ID
func (t *PagerDutyTool) GetIncident(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	path := fmt.Sprintf("/incidents/%s", url.PathEscape(pdIncidentID))

	body, err := t.cachedGet(ctx, incidentID, path, nil, IncidentCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetIncidentNotes retrieves notes for an incident
func (t *PagerDutyTool) GetIncidentNotes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	path := fmt.Sprintf("/incidents/%s/notes", url.PathEscape(pdIncidentID))

	body, err := t.cachedGet(ctx, incidentID, path, nil, IncidentCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetIncidentAlerts retrieves alerts grouped under an incident
func (t *PagerDutyTool) GetIncidentAlerts(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	path := fmt.Sprintf("/incidents/%s/alerts", url.PathEscape(pdIncidentID))

	body, err := t.cachedGet(ctx, incidentID, path, nil, IncidentCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetServices retrieves PagerDuty services
func (t *PagerDutyTool) GetServices(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}

	if v, ok := args["query"].(string); ok && v != "" {
		params.Set("query", v)
	}
	if v, ok := args["limit"].(float64); ok && v > 0 {
		params.Set("limit", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["offset"].(float64); ok && v > 0 {
		params.Set("offset", fmt.Sprintf("%d", int(v)))
	}

	body, err := t.cachedGet(ctx, incidentID, "/services", params, ServiceCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetOnCalls retrieves current on-call users
func (t *PagerDutyTool) GetOnCalls(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}

	if v, ok := args["schedule_ids"].(string); ok && v != "" {
		params.Set("schedule_ids[]", v)
	}
	if v, ok := args["escalation_policy_ids"].(string); ok && v != "" {
		params.Set("escalation_policy_ids[]", v)
	}
	if v, ok := args["since"].(string); ok && v != "" {
		params.Set("since", v)
	}
	if v, ok := args["until"].(string); ok && v != "" {
		params.Set("until", v)
	}

	body, err := t.cachedGet(ctx, incidentID, "/oncalls", params, OnCallCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetEscalationPolicies retrieves escalation policies
func (t *PagerDutyTool) GetEscalationPolicies(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}

	if v, ok := args["query"].(string); ok && v != "" {
		params.Set("query", v)
	}
	if v, ok := args["limit"].(float64); ok && v > 0 {
		params.Set("limit", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["offset"].(float64); ok && v > 0 {
		params.Set("offset", fmt.Sprintf("%d", int(v)))
	}

	body, err := t.cachedGet(ctx, incidentID, "/escalation_policies", params, ServiceCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ListRecentChanges retrieves recent changes across services
func (t *PagerDutyTool) ListRecentChanges(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}

	if v, ok := args["since"].(string); ok && v != "" {
		params.Set("since", v)
	}
	if v, ok := args["until"].(string); ok && v != "" {
		params.Set("until", v)
	}
	if v, ok := args["limit"].(float64); ok && v > 0 {
		params.Set("limit", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["offset"].(float64); ok && v > 0 {
		params.Set("offset", fmt.Sprintf("%d", int(v)))
	}

	body, err := t.cachedGet(ctx, incidentID, "/change_events", params, ChangeCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// updateIncidentStatus updates an incident's status (acknowledge/resolve). Write operation, NOT cached.
func (t *PagerDutyTool) updateIncidentStatus(ctx context.Context, incidentID string, args map[string]interface{}, status string) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	requesterEmail, ok := args["requester_email"].(string)
	if !ok || requesterEmail == "" {
		return "", fmt.Errorf("requester_email is required%s", validation.SuggestParam("requester_email", args))
	}

	reqBody := map[string]interface{}{
		"incident": map[string]interface{}{
			"type":   "incident_reference",
			"status": status,
		},
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/incidents/%s", url.PathEscape(pdIncidentID))

	// PagerDuty requires From header for PUT on incidents
	respBody, err := t.doRequestWithHeaders(ctx, config, http.MethodPut, path, nil, bytes.NewReader(bodyJSON), map[string]string{
		"From": requesterEmail,
	})
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}

// doRequestWithHeaders performs an HTTP request with additional headers
func (t *PagerDutyTool) doRequestWithHeaders(ctx context.Context, config *PagerDutyConfig, method, path string, queryParams url.Values, body io.Reader, extraHeaders map[string]string) ([]byte, error) {
	// Validate token before consuming rate limit budget
	if config.APIToken == "" {
		return nil, fmt.Errorf("PagerDuty API token is required but not configured")
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

	t.logger.Printf("PagerDuty API call: %s %s", method, path)

	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL: %v, proceeding without proxy", err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in via pagerduty_verify_ssl setting
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Token token="+config.APIToken)
	httpReq.Header.Set("Content-Type", "application/json")

	for k, v := range extraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 5 * 1024 * 1024
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

// AcknowledgeIncident acknowledges a PagerDuty incident (write operation, NOT cached)
func (t *PagerDutyTool) AcknowledgeIncident(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	return t.updateIncidentStatus(ctx, incidentID, args, "acknowledged")
}

// ResolveIncident resolves a PagerDuty incident (write operation, NOT cached)
func (t *PagerDutyTool) ResolveIncident(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	return t.updateIncidentStatus(ctx, incidentID, args, "resolved")
}

// ReassignIncident reassigns a PagerDuty incident to different assignees (write operation, NOT cached)
func (t *PagerDutyTool) ReassignIncident(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	requesterEmail, ok := args["requester_email"].(string)
	if !ok || requesterEmail == "" {
		return "", fmt.Errorf("requester_email is required%s", validation.SuggestParam("requester_email", args))
	}

	assigneeIDs, ok := args["assignee_ids"].(string)
	if !ok || assigneeIDs == "" {
		return "", fmt.Errorf("assignee_ids is required (comma-separated user IDs)%s", validation.SuggestParam("assignee_ids", args))
	}

	// Build assignments array
	ids := strings.Split(assigneeIDs, ",")
	assignments := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			assignments = append(assignments, map[string]interface{}{
				"assignee": map[string]interface{}{
					"id":   id,
					"type": "user_reference",
				},
			})
		}
	}

	if len(assignments) == 0 {
		return "", fmt.Errorf("assignee_ids must contain at least one valid user ID")
	}

	// Check for optional escalation_policy_id
	escalationPolicyID, _ := args["escalation_policy_id"].(string)

	incidentBody := map[string]interface{}{
		"type":        "incident",
		"assignments": assignments,
	}
	if escalationPolicyID != "" {
		incidentBody["escalation_policy"] = map[string]interface{}{
			"id":   escalationPolicyID,
			"type": "escalation_policy_reference",
		}
	}

	reqBody := map[string]interface{}{
		"incident": incidentBody,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/incidents/%s", url.PathEscape(pdIncidentID))

	respBody, err := t.doRequestWithHeaders(ctx, config, http.MethodPut, path, nil, bytes.NewReader(bodyJSON), map[string]string{
		"From": requesterEmail,
	})
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}

// AddIncidentNote adds a note to a PagerDuty incident (write operation, NOT cached)
func (t *PagerDutyTool) AddIncidentNote(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	pdIncidentID, ok := args["incident_id"].(string)
	if !ok || pdIncidentID == "" {
		return "", fmt.Errorf("incident_id is required%s", validation.SuggestParam("incident_id", args))
	}

	requesterEmail, ok := args["requester_email"].(string)
	if !ok || requesterEmail == "" {
		return "", fmt.Errorf("requester_email is required%s", validation.SuggestParam("requester_email", args))
	}

	content, ok := args["content"].(string)
	if !ok || content == "" {
		return "", fmt.Errorf("content is required%s", validation.SuggestParam("content", args))
	}

	reqBody := map[string]interface{}{
		"note": map[string]interface{}{
			"content": content,
		},
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/incidents/%s/notes", url.PathEscape(pdIncidentID))

	respBody, err := t.doRequestWithHeaders(ctx, config, http.MethodPost, path, nil, bytes.NewReader(bodyJSON), map[string]string{
		"From": requesterEmail,
	})
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}

// EventsAPIURL is the PagerDuty Events API v2 endpoint
const EventsAPIURL = "https://events.pagerduty.com"

// SendEvent sends an event via PagerDuty Events API v2 (write operation, NOT cached)
func (t *PagerDutyTool) SendEvent(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	routingKey, ok := args["routing_key"].(string)
	if !ok || routingKey == "" {
		return "", fmt.Errorf("routing_key is required%s", validation.SuggestParam("routing_key", args))
	}

	eventAction, ok := args["event_action"].(string)
	if !ok || eventAction == "" {
		return "", fmt.Errorf("event_action is required%s", validation.SuggestParam("event_action", args))
	}

	validActions := map[string]bool{"trigger": true, "acknowledge": true, "resolve": true}
	if !validActions[eventAction] {
		return "", fmt.Errorf("invalid event_action '%s': must be one of trigger, acknowledge, resolve", eventAction)
	}

	reqBody := map[string]interface{}{
		"routing_key":  routingKey,
		"event_action": eventAction,
	}

	// dedup_key is required for acknowledge/resolve
	dedupKey, _ := args["dedup_key"].(string)
	if dedupKey != "" {
		reqBody["dedup_key"] = dedupKey
	} else if eventAction == "acknowledge" || eventAction == "resolve" {
		return "", fmt.Errorf("dedup_key is required for %s events", eventAction)
	}

	// payload is required for trigger events
	if eventAction == "trigger" {
		summary, _ := args["summary"].(string)
		if summary == "" {
			return "", fmt.Errorf("summary is required for trigger events%s", validation.SuggestParam("summary", args))
		}

		severity, _ := args["severity"].(string)
		if severity == "" {
			severity = "error"
		}
		validSeverities := map[string]bool{"critical": true, "error": true, "warning": true, "info": true}
		if !validSeverities[severity] {
			return "", fmt.Errorf("invalid severity '%s': must be one of critical, error, warning, info", severity)
		}

		source, _ := args["source"].(string)
		if source == "" {
			source = "akmatori"
		}

		payload := map[string]interface{}{
			"summary":  summary,
			"severity": severity,
			"source":   source,
		}

		if component, ok := args["component"].(string); ok && component != "" {
			payload["component"] = component
		}
		if group, ok := args["group"].(string); ok && group != "" {
			payload["group"] = group
		}
		if class, ok := args["class"].(string); ok && class != "" {
			payload["class"] = class
		}

		reqBody["payload"] = payload
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	config, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}

	// Events API v2 uses a different endpoint
	eventsURL := EventsAPIURL
	if eventsOverride, ok := args["events_url"].(string); ok && eventsOverride != "" {
		eventsURL = trimTrailingSlash(eventsOverride)
	}

	// Temporarily override config URL for the events API call
	eventsConfig := &PagerDutyConfig{
		URL:       eventsURL,
		APIToken:  config.APIToken,
		VerifySSL: config.VerifySSL,
		Timeout:   config.Timeout,
		UseProxy:  config.UseProxy,
		ProxyURL:  config.ProxyURL,
	}

	// Events API v2 does NOT use Token auth — it uses the routing_key in the body
	// We still need rate limiting and proxy, but auth header is not used
	respBody, err := t.doRequestNoAuth(ctx, eventsConfig, http.MethodPost, "/v2/enqueue", nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}

	return string(respBody), nil
}

// doRequestNoAuth performs an HTTP request without the Authorization header (for Events API v2)
func (t *PagerDutyTool) doRequestNoAuth(ctx context.Context, config *PagerDutyConfig, method, path string, queryParams url.Values, body io.Reader) ([]byte, error) {
	// Apply rate limiting
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	fullURL := config.URL + path
	if len(queryParams) > 0 {
		fullURL += "?" + queryParams.Encode()
	}

	t.logger.Printf("PagerDuty Events API call: %s %s", method, path)

	transport := &http.Transport{
		DisableKeepAlives: true,
	}

	if config.UseProxy && config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			t.logger.Printf("Invalid proxy URL: %v, proceeding without proxy", err)
			transport.Proxy = nil
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = nil
	}

	if !config.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-opt-in
	}

	client := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Transport: transport,
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Events API v2 only needs Content-Type, no Authorization header
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBytes = 5 * 1024 * 1024
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
