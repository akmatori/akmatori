package zabbix

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
)

// ZabbixTool handles Zabbix API operations
type ZabbixTool struct {
	logger    *log.Logger
	requestID uint64
}

// NewZabbixTool creates a new Zabbix tool
func NewZabbixTool(logger *log.Logger) *ZabbixTool {
	return &ZabbixTool{logger: logger}
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

// getConfig fetches Zabbix configuration from database
func (t *ZabbixTool) getConfig(ctx context.Context, incidentID string) (*ZabbixConfig, error) {
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

	// Fetch proxy settings from database
	proxySettings, err := database.GetProxySettings(ctx)
	if err == nil && proxySettings != nil {
		if proxySettings.ProxyURL != "" && proxySettings.ZabbixEnabled {
			config.UseProxy = true
			config.ProxyURL = proxySettings.ProxyURL
		}
	}
	// If fetch fails, continue without proxy (fail-open)

	return config, nil
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

// getAuth returns the authentication token
func (t *ZabbixTool) getAuth(ctx context.Context, config *ZabbixConfig) (string, error) {
	if config.Token != "" {
		return config.Token, nil
	}

	if config.Username != "" && config.Password != "" {
		return t.authenticate(ctx, config)
	}

	return "", fmt.Errorf("no authentication method configured")
}

// doRequest performs a Zabbix API request
func (t *ZabbixTool) doRequest(ctx context.Context, config *ZabbixConfig, method string, params interface{}, auth string) (json.RawMessage, error) {
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

// GetHosts retrieves hosts from Zabbix
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

	result, err := t.request(ctx, incidentID, "host.get", params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetProblems retrieves current problems from Zabbix
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

	result, err := t.request(ctx, incidentID, "problem.get", params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetHistory retrieves metric history from Zabbix
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

	result, err := t.request(ctx, incidentID, "history.get", params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetItems retrieves items (metrics) from Zabbix
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

	result, err := t.request(ctx, incidentID, "item.get", params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// GetTriggers retrieves triggers from Zabbix
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

	result, err := t.request(ctx, incidentID, "trigger.get", params)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

// APIRequest performs a raw Zabbix API request
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
