package grafana

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
	ConfigCacheTTL    = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL  = 30 * time.Second // Default API response cache TTL
	CacheCleanupTick  = time.Minute      // Background cleanup interval
	AlertsCacheTTL      = 15 * time.Second // Alerts and firing instances cache TTL
	DashboardCacheTTL   = 30 * time.Second // Dashboard data cache TTL
	InventoryCacheTTL   = 60 * time.Second // Data sources and static config cache TTL
	AnnotationsCacheTTL = 30 * time.Second // Annotations cache TTL
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

	if config.URL == "" {
		return nil, fmt.Errorf("Grafana URL not configured")
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	return t.doRequest(ctx, config, http.MethodPost, path, nil, bytes.NewReader(bodyJSON))
}

// SearchDashboards searches and lists Grafana dashboards.
// Supports query, tag, type (dash-db, dash-folder), folder ID, and pagination via limit.
func (t *GrafanaTool) SearchDashboards(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	// Default to dash-db type if not specified
	params.Set("type", "dash-db")

	if v, ok := args["query"].(string); ok && v != "" {
		params.Set("query", v)
	}
	if v, ok := args["tag"].(string); ok && v != "" {
		params.Set("tag", v)
	}
	if v, ok := args["type"].(string); ok && v != "" {
		params.Set("type", v)
	}
	if v, ok := args["folder_id"].(float64); ok && v > 0 {
		params.Set("folderIds", fmt.Sprintf("%d", int(v)))
	}
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit := int(v)
		if limit > 5000 {
			limit = 5000
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	body, err := t.cachedGet(ctx, incidentID, "/api/search", params, DashboardCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDashboardByUID retrieves a full dashboard model by its UID.
func (t *GrafanaTool) GetDashboardByUID(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return "", fmt.Errorf("uid is required%s", validation.SuggestParam("uid", args))
	}

	path := fmt.Sprintf("/api/dashboards/uid/%s", url.PathEscape(uid))

	body, err := t.cachedGet(ctx, incidentID, path, nil, DashboardCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetDashboardPanels extracts a summary list of panels from a dashboard for quick overview.
// Returns panel id, title, type, and datasource for each panel.
func (t *GrafanaTool) GetDashboardPanels(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return "", fmt.Errorf("uid is required%s", validation.SuggestParam("uid", args))
	}

	path := fmt.Sprintf("/api/dashboards/uid/%s", url.PathEscape(uid))

	body, err := t.cachedGet(ctx, incidentID, path, nil, DashboardCacheTTL, logicalName)
	if err != nil {
		return "", err
	}

	// Parse the dashboard response and extract panels
	var dashResponse struct {
		Dashboard struct {
			Panels []json.RawMessage `json:"panels"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(body, &dashResponse); err != nil {
		return "", fmt.Errorf("failed to parse dashboard response: %w", err)
	}

	type panelSummary struct {
		ID         interface{} `json:"id"`
		Title      string      `json:"title"`
		Type       string      `json:"type"`
		Datasource interface{} `json:"datasource,omitempty"`
	}

	var panels []panelSummary
	for _, raw := range dashResponse.Dashboard.Panels {
		var p struct {
			ID         interface{}       `json:"id"`
			Title      string            `json:"title"`
			Type       string            `json:"type"`
			Datasource interface{}       `json:"datasource"`
			Panels     []json.RawMessage `json:"panels"` // Nested panels in rows
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		panels = append(panels, panelSummary{
			ID:         p.ID,
			Title:      p.Title,
			Type:       p.Type,
			Datasource: p.Datasource,
		})
		// Handle row panels that contain nested panels
		for _, nestedRaw := range p.Panels {
			var nested struct {
				ID         interface{} `json:"id"`
				Title      string      `json:"title"`
				Type       string      `json:"type"`
				Datasource interface{} `json:"datasource"`
			}
			if err := json.Unmarshal(nestedRaw, &nested); err != nil {
				continue
			}
			panels = append(panels, panelSummary{
				ID:         nested.ID,
				Title:      nested.Title,
				Type:       nested.Type,
				Datasource: nested.Datasource,
			})
		}
	}

	result, err := json.Marshal(panels)
	if err != nil {
		return "", fmt.Errorf("failed to marshal panel list: %w", err)
	}
	return string(result), nil
}

// GetAlertRules lists alert rules from Grafana Unified Alerting.
// Returns all provisioned alert rules (GET /api/v1/provisioning/alert-rules).
func (t *GrafanaTool) GetAlertRules(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	body, err := t.cachedGet(ctx, incidentID, "/api/v1/provisioning/alert-rules", nil, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetAlertInstances retrieves firing and pending alert instances from Grafana Alertmanager.
// Returns active alerts (GET /api/alertmanager/grafana/api/v2/alerts).
func (t *GrafanaTool) GetAlertInstances(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	if v, ok := args["filter"].(string); ok && v != "" {
		params.Set("filter", v)
	}
	if v, ok := args["silenced"].(bool); ok {
		params.Set("silenced", fmt.Sprintf("%t", v))
	}
	if v, ok := args["inhibited"].(bool); ok {
		params.Set("inhibited", fmt.Sprintf("%t", v))
	}
	if v, ok := args["active"].(bool); ok {
		params.Set("active", fmt.Sprintf("%t", v))
	}

	body, err := t.cachedGet(ctx, incidentID, "/api/alertmanager/grafana/api/v2/alerts", params, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetAlertRuleByUID retrieves a specific alert rule by its UID.
// Returns the full rule definition (GET /api/v1/provisioning/alert-rules/:uid).
func (t *GrafanaTool) GetAlertRuleByUID(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return "", fmt.Errorf("uid is required%s", validation.SuggestParam("uid", args))
	}

	path := fmt.Sprintf("/api/v1/provisioning/alert-rules/%s", url.PathEscape(uid))

	body, err := t.cachedGet(ctx, incidentID, path, nil, AlertsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// SilenceAlert creates a silence in Grafana Alertmanager.
// Requires matchers (label matchers), starts_at, ends_at, created_by, and comment.
// This is a write operation - no caching (POST /api/alertmanager/grafana/api/v2/silences).
func (t *GrafanaTool) SilenceAlert(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	// Validate required fields
	matchers, ok := args["matchers"]
	if !ok {
		return "", fmt.Errorf("matchers is required (array of {name, value, isRegex, isEqual})%s", validation.SuggestParam("matchers", args))
	}

	startsAt, ok := args["starts_at"].(string)
	if !ok || startsAt == "" {
		return "", fmt.Errorf("starts_at is required (RFC3339 timestamp)%s", validation.SuggestParam("starts_at", args))
	}
	startsAtTime, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return "", fmt.Errorf("starts_at must be a valid RFC3339 timestamp (e.g. 2026-03-27T00:00:00Z): %w", err)
	}

	endsAt, ok := args["ends_at"].(string)
	if !ok || endsAt == "" {
		return "", fmt.Errorf("ends_at is required (RFC3339 timestamp)%s", validation.SuggestParam("ends_at", args))
	}
	endsAtTime, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		return "", fmt.Errorf("ends_at must be a valid RFC3339 timestamp (e.g. 2026-03-28T00:00:00Z): %w", err)
	}

	if !endsAtTime.After(startsAtTime) {
		return "", fmt.Errorf("ends_at must be after starts_at")
	}

	createdBy, ok := args["created_by"].(string)
	if !ok || createdBy == "" {
		return "", fmt.Errorf("created_by is required%s", validation.SuggestParam("created_by", args))
	}
	comment, ok := args["comment"].(string)
	if !ok || comment == "" {
		return "", fmt.Errorf("comment is required%s", validation.SuggestParam("comment", args))
	}

	reqBody := map[string]interface{}{
		"matchers":  matchers,
		"startsAt":  startsAt,
		"endsAt":    endsAt,
		"createdBy": createdBy,
		"comment":   comment,
	}

	body, err := t.doPost(ctx, incidentID, "/api/alertmanager/grafana/api/v2/silences", reqBody, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ListDataSources lists all configured data sources in Grafana.
// Returns data source metadata including uid, name, type, url (GET /api/datasources).
func (t *GrafanaTool) ListDataSources(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	body, err := t.cachedGet(ctx, incidentID, "/api/datasources", nil, InventoryCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// QueryDataSource queries a data source via the Grafana unified query API (POST /api/ds/query).
// Requires datasource_uid, and either queries (raw query objects) or expression.
// Supports from/to time range as epoch milliseconds or relative strings.
func (t *GrafanaTool) QueryDataSource(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	dsUID, ok := args["datasource_uid"].(string)
	if !ok || dsUID == "" {
		return "", fmt.Errorf("datasource_uid is required%s", validation.SuggestParam("datasource_uid", args))
	}

	queries, hasQueries := args["queries"]
	if !hasQueries {
		return "", fmt.Errorf("queries is required (array of query objects with refId and datasource)")
	}

	// Inject/override datasource UID in all queries to enforce the required top-level datasource_uid.
	// Any embedded datasource is replaced to prevent routing queries to the wrong data source.
	if querySlice, ok := queries.([]interface{}); ok {
		for i, q := range querySlice {
			if qMap, ok := q.(map[string]interface{}); ok {
				qMap["datasource"] = map[string]interface{}{"uid": dsUID}
				querySlice[i] = qMap
			}
		}
		queries = querySlice
	}

	reqBody := map[string]interface{}{
		"queries": queries,
	}

	// Optional time range
	if from, ok := args["from"].(string); ok && from != "" {
		reqBody["from"] = from
	}
	if to, ok := args["to"].(string); ok && to != "" {
		reqBody["to"] = to
	}

	body, err := t.doPost(ctx, incidentID, "/api/ds/query", reqBody, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// QueryPrometheus is a convenience wrapper for querying Prometheus-type data sources via Grafana proxy.
// Supports both instant queries (expr only) and range queries (expr + start + end + step).
// Uses POST /api/ds/query with Prometheus-specific query structure.
func (t *GrafanaTool) QueryPrometheus(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	dsUID, ok := args["datasource_uid"].(string)
	if !ok || dsUID == "" {
		return "", fmt.Errorf("datasource_uid is required%s", validation.SuggestParam("datasource_uid", args))
	}

	expr, ok := args["expr"].(string)
	if !ok || expr == "" {
		return "", fmt.Errorf("expr is required (PromQL expression)%s", validation.SuggestParam("expr", args))
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]interface{}{
			"uid":  dsUID,
			"type": "prometheus",
		},
		"expr": expr,
	}

	// Range query parameters
	if start, ok := args["start"].(string); ok && start != "" {
		query["start"] = start
	}
	if end, ok := args["end"].(string); ok && end != "" {
		query["end"] = end
	}
	if step, ok := args["step"].(string); ok && step != "" {
		query["step"] = step
	}

	// Instant vs range
	if instant, ok := args["instant"].(bool); ok {
		query["instant"] = instant
	}
	if rangeQ, ok := args["range"].(bool); ok {
		query["range"] = rangeQ
	}

	reqBody := map[string]interface{}{
		"queries": []interface{}{query},
	}

	if from, ok := args["from"].(string); ok && from != "" {
		reqBody["from"] = from
	}
	if to, ok := args["to"].(string); ok && to != "" {
		reqBody["to"] = to
	}

	body, err := t.doPost(ctx, incidentID, "/api/ds/query", reqBody, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// QueryLoki is a convenience wrapper for querying Loki-type data sources via Grafana proxy.
// Supports log queries with optional limit, start, end, and direction.
// Uses POST /api/ds/query with Loki-specific query structure.
func (t *GrafanaTool) QueryLoki(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	dsUID, ok := args["datasource_uid"].(string)
	if !ok || dsUID == "" {
		return "", fmt.Errorf("datasource_uid is required%s", validation.SuggestParam("datasource_uid", args))
	}

	expr, ok := args["expr"].(string)
	if !ok || expr == "" {
		return "", fmt.Errorf("expr is required (LogQL expression)%s", validation.SuggestParam("expr", args))
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]interface{}{
			"uid":  dsUID,
			"type": "loki",
		},
		"expr": expr,
	}

	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		maxLines := int(limit)
		if maxLines > 5000 {
			maxLines = 5000
		}
		query["maxLines"] = maxLines
	}
	if direction, ok := args["direction"].(string); ok && direction != "" {
		query["direction"] = direction
	}
	if start, ok := args["start"].(string); ok && start != "" {
		query["start"] = start
	}
	if end, ok := args["end"].(string); ok && end != "" {
		query["end"] = end
	}

	reqBody := map[string]interface{}{
		"queries": []interface{}{query},
	}

	if from, ok := args["from"].(string); ok && from != "" {
		reqBody["from"] = from
	}
	if to, ok := args["to"].(string); ok && to != "" {
		reqBody["to"] = to
	}

	body, err := t.doPost(ctx, incidentID, "/api/ds/query", reqBody, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// CreateAnnotation creates an annotation on a Grafana dashboard or globally.
// Requires text; optional: dashboard_id, panel_id, tags, time, time_end.
// This is a write operation - no caching (POST /api/annotations).
func (t *GrafanaTool) CreateAnnotation(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	text, ok := args["text"].(string)
	if !ok || text == "" {
		return "", fmt.Errorf("text is required%s", validation.SuggestParam("text", args))
	}

	reqBody := map[string]interface{}{
		"text": text,
	}

	if dashID, ok := args["dashboard_id"].(float64); ok && dashID > 0 {
		reqBody["dashboardId"] = int(dashID)
	}
	if panelID, ok := args["panel_id"].(float64); ok && panelID > 0 {
		reqBody["panelId"] = int(panelID)
	}
	if tags, ok := args["tags"]; ok {
		reqBody["tags"] = tags
	}
	if ts, ok := args["time"].(float64); ok && ts > 0 {
		reqBody["time"] = int64(ts)
	}
	if tsEnd, ok := args["time_end"].(float64); ok && tsEnd > 0 {
		reqBody["timeEnd"] = int64(tsEnd)
	}

	body, err := t.doPost(ctx, incidentID, "/api/annotations", reqBody, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// GetAnnotations lists annotations with optional filters.
// Supports from, to (epoch ms), dashboard_id, panel_id, tags, limit, type (annotation/alert).
// GET /api/annotations
func (t *GrafanaTool) GetAnnotations(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	params := url.Values{}
	if from, ok := args["from"].(float64); ok && from > 0 {
		params.Set("from", fmt.Sprintf("%d", int64(from)))
	}
	if to, ok := args["to"].(float64); ok && to > 0 {
		params.Set("to", fmt.Sprintf("%d", int64(to)))
	}
	if dashID, ok := args["dashboard_id"].(float64); ok && dashID > 0 {
		params.Set("dashboardId", fmt.Sprintf("%d", int(dashID)))
	}
	if panelID, ok := args["panel_id"].(float64); ok && panelID > 0 {
		params.Set("panelId", fmt.Sprintf("%d", int(panelID)))
	}
	if tags, ok := args["tags"].(string); ok && tags != "" {
		for _, tag := range strings.Split(tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				params.Add("tags", tag)
			}
		}
	}
	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		l := int(limit)
		if l > 5000 {
			l = 5000
		}
		params.Set("limit", fmt.Sprintf("%d", l))
	}
	if annType, ok := args["type"].(string); ok && annType != "" {
		params.Set("type", annType)
	}

	body, err := t.cachedGet(ctx, incidentID, "/api/annotations", params, AnnotationsCacheTTL, logicalName)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
