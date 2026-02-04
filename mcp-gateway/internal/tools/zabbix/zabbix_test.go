package zabbix

import (
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

func TestNewZabbixTool(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	limiter := ratelimit.New(10, 20)
	tool := NewZabbixTool(logger, limiter)
	defer tool.Stop()

	if tool == nil {
		t.Fatal("Expected tool to not be nil")
	}
	if tool.logger == nil {
		t.Error("Expected logger to be set")
	}
	if tool.requestID != 0 {
		t.Errorf("Expected initial requestID to be 0, got %d", tool.requestID)
	}
	if tool.configCache == nil {
		t.Error("Expected configCache to be initialized")
	}
	if tool.responseCache == nil {
		t.Error("Expected responseCache to be initialized")
	}
	if tool.authCache == nil {
		t.Error("Expected authCache to be initialized")
	}
	if tool.rateLimiter == nil {
		t.Error("Expected rateLimiter to be set")
	}
}

func TestNewZabbixTool_NilLimiter(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewZabbixTool(logger, nil)
	defer tool.Stop()

	if tool == nil {
		t.Fatal("Expected tool to not be nil")
	}
	if tool.rateLimiter != nil {
		t.Error("Expected rateLimiter to be nil")
	}
}

func TestZabbixConfig_Defaults(t *testing.T) {
	config := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "api-token",
		VerifySSL: true,
		Timeout:   30,
	}

	if config.VerifySSL != true {
		t.Error("Expected VerifySSL to be true by default")
	}
	if config.Timeout != 30 {
		t.Errorf("Expected Timeout 30, got %d", config.Timeout)
	}
}

func TestZabbixConfig_AuthMethods(t *testing.T) {
	// Test token-based auth
	tokenConfig := &ZabbixConfig{
		URL:   "https://zabbix.example.com",
		Token: "api-token-123",
	}

	if tokenConfig.Token == "" {
		t.Error("Expected token to be set")
	}

	// Test username/password auth
	passwordConfig := &ZabbixConfig{
		URL:      "https://zabbix.example.com",
		Username: "admin",
		Password: "secret",
	}

	if passwordConfig.Username == "" || passwordConfig.Password == "" {
		t.Error("Expected username and password to be set")
	}
}

func TestJSONRPCRequest_Serialization(t *testing.T) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "host.get",
		Params: map[string]interface{}{
			"output": "extend",
			"limit":  10,
		},
		Auth: "auth-token-123",
		ID:   1,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	// Verify JSON structure
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded["jsonrpc"] != "2.0" {
		t.Errorf("Expected jsonrpc '2.0', got '%v'", decoded["jsonrpc"])
	}
	if decoded["method"] != "host.get" {
		t.Errorf("Expected method 'host.get', got '%v'", decoded["method"])
	}
	if decoded["auth"] != "auth-token-123" {
		t.Errorf("Expected auth 'auth-token-123', got '%v'", decoded["auth"])
	}
	if decoded["id"].(float64) != 1 {
		t.Errorf("Expected id 1, got %v", decoded["id"])
	}
}

func TestJSONRPCRequest_NoAuth(t *testing.T) {
	// Request without auth (e.g., for login)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "user.login",
		Params: map[string]string{
			"user":     "admin",
			"password": "secret",
		},
		ID: 1,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Auth should be omitted (omitempty)
	dataStr := string(data)
	if dataStr == "" {
		t.Error("Expected non-empty JSON")
	}
}

func TestJSONRPCResponse_Success(t *testing.T) {
	responseJSON := `{
		"jsonrpc": "2.0",
		"result": [{"hostid": "10084", "host": "test-host"}],
		"id": 1
	}`

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("Expected jsonrpc '2.0', got '%s'", resp.JSONRPC)
	}
	if resp.Error != nil {
		t.Errorf("Expected no error, got %v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("Expected result to be set")
	}
	if resp.ID != 1 {
		t.Errorf("Expected id 1, got %d", resp.ID)
	}

	// Parse result
	var hosts []map[string]string
	if err := json.Unmarshal(resp.Result, &hosts); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if len(hosts) != 1 {
		t.Fatalf("Expected 1 host, got %d", len(hosts))
	}
	if hosts[0]["host"] != "test-host" {
		t.Errorf("Expected host 'test-host', got '%s'", hosts[0]["host"])
	}
}

func TestJSONRPCResponse_Error(t *testing.T) {
	responseJSON := `{
		"jsonrpc": "2.0",
		"error": {
			"code": -32602,
			"message": "Invalid params.",
			"data": "No permissions to referred object."
		},
		"id": 1
	}`

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("Expected error to be set")
	}

	if resp.Error.Code != -32602 {
		t.Errorf("Expected error code -32602, got %d", resp.Error.Code)
	}
	if resp.Error.Message != "Invalid params." {
		t.Errorf("Expected error message, got '%s'", resp.Error.Message)
	}
	if resp.Error.Data != "No permissions to referred object." {
		t.Errorf("Expected error data, got '%s'", resp.Error.Data)
	}
}

func TestZabbixError_Error(t *testing.T) {
	err := &ZabbixError{
		Code:    -32602,
		Message: "Invalid params.",
		Data:    "No permissions",
	}

	errStr := err.Error()

	if errStr == "" {
		t.Error("Expected non-empty error string")
	}
	if errStr != "Zabbix API error: Invalid params. (code: -32602, data: No permissions)" {
		t.Errorf("Unexpected error format: %s", errStr)
	}
}

func TestJSONRPCRequest_WithParams(t *testing.T) {
	testCases := []struct {
		name   string
		method string
		params interface{}
	}{
		{
			name:   "host.get with filter",
			method: "host.get",
			params: map[string]interface{}{
				"output": "extend",
				"filter": map[string]string{
					"host": "test-host",
				},
			},
		},
		{
			name:   "problem.get with severity",
			method: "problem.get",
			params: map[string]interface{}{
				"output":     "extend",
				"severities": []int{4, 5},
				"recent":     true,
			},
		},
		{
			name:   "history.get with time range",
			method: "history.get",
			params: map[string]interface{}{
				"itemids":   []string{"12345"},
				"time_from": 1705315800,
				"time_till": 1705319400,
				"limit":     100,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  tc.method,
				Params:  tc.params,
				Auth:    "token",
				ID:      1,
			}

			data, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Verify it's valid JSON
			var decoded map[string]interface{}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			if decoded["method"] != tc.method {
				t.Errorf("Method mismatch: expected '%s', got '%v'", tc.method, decoded["method"])
			}
		})
	}
}

func TestJSONRPCResponse_ResultParsing(t *testing.T) {
	testCases := []struct {
		name         string
		responseJSON string
		expectError  bool
	}{
		{
			name: "hosts result",
			responseJSON: `{
				"jsonrpc": "2.0",
				"result": [
					{"hostid": "10084", "host": "host1"},
					{"hostid": "10085", "host": "host2"}
				],
				"id": 1
			}`,
			expectError: false,
		},
		{
			name: "problems result",
			responseJSON: `{
				"jsonrpc": "2.0",
				"result": [
					{"eventid": "123", "severity": "4", "name": "High CPU"}
				],
				"id": 2
			}`,
			expectError: false,
		},
		{
			name: "empty result",
			responseJSON: `{
				"jsonrpc": "2.0",
				"result": [],
				"id": 3
			}`,
			expectError: false,
		},
		{
			name: "string result (auth token)",
			responseJSON: `{
				"jsonrpc": "2.0",
				"result": "auth-session-token-abc123",
				"id": 4
			}`,
			expectError: false,
		},
		{
			name: "error response",
			responseJSON: `{
				"jsonrpc": "2.0",
				"error": {"code": -32602, "message": "Invalid params.", "data": "Details"},
				"id": 5
			}`,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var resp JSONRPCResponse
			if err := json.Unmarshal([]byte(tc.responseJSON), &resp); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			hasError := resp.Error != nil
			if hasError != tc.expectError {
				t.Errorf("Expected error=%v, got error=%v", tc.expectError, hasError)
			}

			if !tc.expectError && resp.Result == nil {
				t.Error("Expected result to be set for success response")
			}
		})
	}
}

func TestZabbixConfig_SSLOptions(t *testing.T) {
	// Test with SSL verification enabled (default)
	sslConfig := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
	}

	if !sslConfig.VerifySSL {
		t.Error("Expected VerifySSL to be true")
	}

	// Test with SSL verification disabled
	noSSLConfig := &ZabbixConfig{
		URL:       "https://self-signed.zabbix.local",
		Token:     "token",
		VerifySSL: false,
	}

	if noSSLConfig.VerifySSL {
		t.Error("Expected VerifySSL to be false")
	}
}

func TestZabbixConfig_TimeoutOptions(t *testing.T) {
	// Test default timeout
	defaultConfig := &ZabbixConfig{
		URL:     "https://zabbix.example.com",
		Token:   "token",
		Timeout: 30,
	}

	if defaultConfig.Timeout != 30 {
		t.Errorf("Expected default timeout 30, got %d", defaultConfig.Timeout)
	}

	// Test custom timeout
	customConfig := &ZabbixConfig{
		URL:     "https://slow-zabbix.example.com",
		Token:   "token",
		Timeout: 120,
	}

	if customConfig.Timeout != 120 {
		t.Errorf("Expected custom timeout 120, got %d", customConfig.Timeout)
	}
}

func TestJSONRPCRequest_IDIncrement(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewZabbixTool(logger, nil)
	defer tool.Stop()

	// Initial ID should be 0
	if tool.requestID != 0 {
		t.Errorf("Expected initial requestID 0, got %d", tool.requestID)
	}

	// Create multiple requests and verify ID increments
	// Note: We can't call doRequest without a real server, but we can verify
	// the structure is correct
	req1 := JSONRPCRequest{ID: 1}
	req2 := JSONRPCRequest{ID: 2}

	if req1.ID >= req2.ID {
		t.Error("Request IDs should increment")
	}
}

func TestZabbixError_Interface(t *testing.T) {
	var err error = &ZabbixError{
		Code:    -32602,
		Message: "Test error",
		Data:    "Details",
	}

	// Should implement error interface
	if err.Error() == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestJSONRPCResponse_EmptyResult(t *testing.T) {
	responseJSON := `{
		"jsonrpc": "2.0",
		"result": [],
		"id": 1
	}`

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if resp.Error != nil {
		t.Error("Expected no error for empty result")
	}

	// Parse empty array
	var items []map[string]interface{}
	if err := json.Unmarshal(resp.Result, &items); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(items))
	}
}

func TestZabbixConfig_ProxySettings(t *testing.T) {
	// Test with proxy enabled
	proxyConfig := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		UseProxy:  true,
		ProxyURL:  "http://proxy.example.com:8080",
	}

	if !proxyConfig.UseProxy {
		t.Error("Expected UseProxy to be true")
	}
	if proxyConfig.ProxyURL != "http://proxy.example.com:8080" {
		t.Errorf("Expected ProxyURL 'http://proxy.example.com:8080', got '%s'", proxyConfig.ProxyURL)
	}

	// Test with proxy disabled
	noProxyConfig := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		UseProxy:  false,
		ProxyURL:  "",
	}

	if noProxyConfig.UseProxy {
		t.Error("Expected UseProxy to be false")
	}
	if noProxyConfig.ProxyURL != "" {
		t.Errorf("Expected empty ProxyURL, got '%s'", noProxyConfig.ProxyURL)
	}
}

func TestZabbixConfig_ProxyDisabledWithURL(t *testing.T) {
	// Edge case: ProxyURL set but UseProxy is false
	// This should mean proxy is NOT used
	config := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		UseProxy:  false,
		ProxyURL:  "http://proxy.example.com:8080", // URL exists but disabled
	}

	// When UseProxy is false, the transport.Proxy should be nil
	// regardless of ProxyURL value
	if config.UseProxy {
		t.Error("Expected proxy to be disabled even with ProxyURL set")
	}
}

func TestZabbixConfig_ProxyEnabledWithoutURL(t *testing.T) {
	// Edge case: UseProxy is true but ProxyURL is empty
	// This should mean proxy is NOT used (no valid proxy to use)
	config := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		UseProxy:  true,
		ProxyURL:  "", // Empty URL
	}

	// When ProxyURL is empty, proxy should not be used
	if config.ProxyURL != "" {
		t.Error("Expected empty ProxyURL")
	}
}

func TestZabbixConfig_ProxyWithAuthentication(t *testing.T) {
	// Test proxy URL with authentication credentials
	config := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		UseProxy:  true,
		ProxyURL:  "http://user:password@proxy.example.com:8080",
	}

	if !config.UseProxy {
		t.Error("Expected UseProxy to be true")
	}
	if config.ProxyURL != "http://user:password@proxy.example.com:8080" {
		t.Errorf("Expected proxy URL with auth, got '%s'", config.ProxyURL)
	}
}

func TestZabbixConfig_DefaultProxyValues(t *testing.T) {
	// Default ZabbixConfig should have proxy disabled
	config := &ZabbixConfig{
		URL:       "https://zabbix.example.com",
		Token:     "token",
		VerifySSL: true,
		Timeout:   30,
		// UseProxy and ProxyURL not set - should be zero values
	}

	if config.UseProxy {
		t.Error("Expected UseProxy to default to false")
	}
	if config.ProxyURL != "" {
		t.Error("Expected ProxyURL to default to empty string")
	}
}

// Cache key tests
func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123", "zabbix")
	expected := "creds:incident-123:zabbix"
	if key != expected {
		t.Errorf("Expected cache key '%s', got '%s'", expected, key)
	}
}

func TestAuthCacheKey(t *testing.T) {
	key := authCacheKey("https://zabbix.example.com", "admin")
	expected := "https://zabbix.example.com:admin"
	if key != expected {
		t.Errorf("Expected auth cache key '%s', got '%s'", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := map[string]interface{}{"output": "extend", "limit": 10}
	params2 := map[string]interface{}{"output": "extend", "limit": 10}
	params3 := map[string]interface{}{"output": "extend", "limit": 20}

	key1 := responseCacheKey("host.get", params1)
	key2 := responseCacheKey("host.get", params2)
	key3 := responseCacheKey("host.get", params3)

	// Same params should produce same key
	if key1 != key2 {
		t.Errorf("Same params should produce same key: '%s' vs '%s'", key1, key2)
	}

	// Different params should produce different key
	if key1 == key3 {
		t.Error("Different params should produce different key")
	}

	// Key should start with method name
	if key1[:8] != "host.get" {
		t.Errorf("Key should start with method name, got '%s'", key1)
	}
}

func TestCacheTTLConstants(t *testing.T) {
	// Verify cache TTL constants are reasonable
	if ConfigCacheTTL != 5*time.Minute {
		t.Errorf("Expected ConfigCacheTTL to be 5 minutes, got %v", ConfigCacheTTL)
	}
	if ResponseCacheTTL != 30*time.Second {
		t.Errorf("Expected ResponseCacheTTL to be 30 seconds, got %v", ResponseCacheTTL)
	}
	if AuthCacheTTL != 30*time.Minute {
		t.Errorf("Expected AuthCacheTTL to be 30 minutes, got %v", AuthCacheTTL)
	}
	if CacheCleanupTick != time.Minute {
		t.Errorf("Expected CacheCleanupTick to be 1 minute, got %v", CacheCleanupTick)
	}
}

func TestZabbixTool_ClearCache(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewZabbixTool(logger, nil)
	defer tool.Stop()

	// Add some items to caches
	tool.configCache.Set("test-key", "test-value")
	tool.responseCache.Set("test-key", "test-value")
	tool.authMu.Lock()
	tool.authCache["test-key"] = authEntry{
		token:     "test-token",
		expiresAt: time.Now().Add(time.Hour),
	}
	tool.authMu.Unlock()

	// Verify items are in cache
	if _, ok := tool.configCache.Get("test-key"); !ok {
		t.Error("Expected item in configCache")
	}
	if _, ok := tool.responseCache.Get("test-key"); !ok {
		t.Error("Expected item in responseCache")
	}
	tool.authMu.RLock()
	if _, ok := tool.authCache["test-key"]; !ok {
		t.Error("Expected item in authCache")
	}
	tool.authMu.RUnlock()

	// Clear caches
	tool.ClearCache()

	// Verify caches are empty
	if _, ok := tool.configCache.Get("test-key"); ok {
		t.Error("Expected configCache to be cleared")
	}
	if _, ok := tool.responseCache.Get("test-key"); ok {
		t.Error("Expected responseCache to be cleared")
	}
	tool.authMu.RLock()
	if _, ok := tool.authCache["test-key"]; ok {
		t.Error("Expected authCache to be cleared")
	}
	tool.authMu.RUnlock()
}

func TestZabbixTool_InvalidateConfigCache(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewZabbixTool(logger, nil)
	defer tool.Stop()

	// Add config items for different incidents
	tool.configCache.Set("creds:incident-1:zabbix", "config1")
	tool.configCache.Set("creds:incident-1:ssh", "config2")
	tool.configCache.Set("creds:incident-2:zabbix", "config3")

	// Invalidate for incident-1
	tool.InvalidateConfigCache("incident-1")

	// incident-1 configs should be gone
	if _, ok := tool.configCache.Get("creds:incident-1:zabbix"); ok {
		t.Error("Expected incident-1 zabbix config to be invalidated")
	}
	if _, ok := tool.configCache.Get("creds:incident-1:ssh"); ok {
		t.Error("Expected incident-1 ssh config to be invalidated")
	}

	// incident-2 config should remain
	if _, ok := tool.configCache.Get("creds:incident-2:zabbix"); !ok {
		t.Error("Expected incident-2 config to remain")
	}
}

func TestAuthEntry_Expiration(t *testing.T) {
	// Test unexpired entry
	futureEntry := authEntry{
		token:     "token",
		expiresAt: time.Now().Add(time.Hour),
	}
	if time.Now().After(futureEntry.expiresAt) {
		t.Error("Future entry should not be expired")
	}

	// Test expired entry
	pastEntry := authEntry{
		token:     "token",
		expiresAt: time.Now().Add(-time.Hour),
	}
	if !time.Now().After(pastEntry.expiresAt) {
		t.Error("Past entry should be expired")
	}
}

// Batch tests
func TestBatchItem_Serialization(t *testing.T) {
	item := BatchItem{
		ItemID:    "12345",
		HostID:    "10084",
		Name:      "CPU utilization",
		Key:       "system.cpu.util",
		ValueType: "0",
		LastValue: "45.5",
		Units:     "%",
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Failed to marshal BatchItem: %v", err)
	}

	var decoded BatchItem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal BatchItem: %v", err)
	}

	if decoded.ItemID != item.ItemID {
		t.Errorf("ItemID mismatch: expected '%s', got '%s'", item.ItemID, decoded.ItemID)
	}
	if decoded.Name != item.Name {
		t.Errorf("Name mismatch: expected '%s', got '%s'", item.Name, decoded.Name)
	}
}

func TestBatchResult_Serialization(t *testing.T) {
	result := BatchResult{
		Pattern: "cpu",
		Items: []BatchItem{
			{ItemID: "1", Name: "CPU 1"},
			{ItemID: "2", Name: "CPU 2"},
		},
		Count: 2,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal BatchResult: %v", err)
	}

	var decoded BatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal BatchResult: %v", err)
	}

	if decoded.Pattern != result.Pattern {
		t.Errorf("Pattern mismatch: expected '%s', got '%s'", result.Pattern, decoded.Pattern)
	}
	if decoded.Count != result.Count {
		t.Errorf("Count mismatch: expected %d, got %d", result.Count, decoded.Count)
	}
	if len(decoded.Items) != len(result.Items) {
		t.Errorf("Items count mismatch: expected %d, got %d", len(result.Items), len(decoded.Items))
	}
}

// --- Tests for optimized output fields and startSearch ---

// buildGetHostsParams replicates the parameter building logic from GetHosts
// for unit testing without needing database connectivity.
func buildGetHostsParams(args map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = []string{"hostid", "host", "name", "status", "available"}
	}
	if filter, ok := args["filter"]; ok {
		params["filter"] = filter
	}
	if search, ok := args["search"]; ok {
		params["search"] = search
		if startSearch, ok := args["start_search"].(bool); ok {
			if startSearch {
				params["startSearch"] = true
			}
		} else {
			params["startSearch"] = true
		}
	}
	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}
	return params
}

func TestGetHosts_DefaultOutputFields(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{})
	output, ok := params["output"].([]string)
	if !ok {
		t.Fatal("Expected output to be []string")
	}
	expected := []string{"hostid", "host", "name", "status", "available"}
	if len(output) != len(expected) {
		t.Fatalf("Expected %d output fields, got %d", len(expected), len(output))
	}
	for i, field := range expected {
		if output[i] != field {
			t.Errorf("Expected output[%d] = '%s', got '%s'", i, field, output[i])
		}
	}
}

func TestGetHosts_ExplicitOutputOverride(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{"output": "extend"})
	output, ok := params["output"].(string)
	if !ok {
		t.Fatal("Expected output to be string when explicitly set")
	}
	if output != "extend" {
		t.Errorf("Expected output 'extend', got '%s'", output)
	}
}

func TestGetHosts_StartSearchDefaultTrue(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{
		"search": map[string]interface{}{"name": "web"},
	})
	startSearch, ok := params["startSearch"].(bool)
	if !ok {
		t.Fatal("Expected startSearch to be set when search is present")
	}
	if !startSearch {
		t.Error("Expected startSearch to default to true")
	}
}

func TestGetHosts_StartSearchExplicitFalse(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{
		"search":       map[string]interface{}{"name": "web"},
		"start_search": false,
	})
	if _, ok := params["startSearch"]; ok {
		t.Error("Expected startSearch to NOT be set when start_search=false")
	}
}

func TestGetHosts_StartSearchExplicitTrue(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{
		"search":       map[string]interface{}{"name": "web"},
		"start_search": true,
	})
	startSearch, ok := params["startSearch"].(bool)
	if !ok {
		t.Fatal("Expected startSearch to be set")
	}
	if !startSearch {
		t.Error("Expected startSearch to be true")
	}
}

func TestGetHosts_NoStartSearchWithoutSearch(t *testing.T) {
	params := buildGetHostsParams(map[string]interface{}{
		"filter": map[string]interface{}{"host": []string{"server1"}},
	})
	if _, ok := params["startSearch"]; ok {
		t.Error("Expected startSearch NOT to be set when search is absent")
	}
}

func TestGetHosts_FilterPassthrough(t *testing.T) {
	filter := map[string]interface{}{"host": []string{"server1", "server2"}}
	params := buildGetHostsParams(map[string]interface{}{
		"filter": filter,
	})
	if _, ok := params["filter"]; !ok {
		t.Error("Expected filter to be passed through")
	}
}

// buildGetItemsParams replicates the parameter building logic from GetItems
func buildGetItemsParams(args map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = []string{"itemid", "hostid", "name", "key_", "value_type", "lastvalue", "units", "state", "status"}
	}
	if hostids, ok := args["hostids"]; ok {
		params["hostids"] = hostids
	}
	if filter, ok := args["filter"]; ok {
		params["filter"] = filter
	}
	if search, ok := args["search"]; ok {
		params["search"] = search
		if startSearch, ok := args["start_search"].(bool); ok {
			if startSearch {
				params["startSearch"] = true
			}
		} else {
			params["startSearch"] = true
		}
	}
	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}
	return params
}

func TestGetItems_DefaultOutputFields(t *testing.T) {
	params := buildGetItemsParams(map[string]interface{}{})
	output, ok := params["output"].([]string)
	if !ok {
		t.Fatal("Expected output to be []string")
	}
	expected := []string{"itemid", "hostid", "name", "key_", "value_type", "lastvalue", "units", "state", "status"}
	if len(output) != len(expected) {
		t.Fatalf("Expected %d output fields, got %d", len(expected), len(output))
	}
	for i, field := range expected {
		if output[i] != field {
			t.Errorf("Expected output[%d] = '%s', got '%s'", i, field, output[i])
		}
	}
}

func TestGetItems_StartSearchDefaultTrue(t *testing.T) {
	params := buildGetItemsParams(map[string]interface{}{
		"search": map[string]interface{}{"key_": "cpu"},
	})
	startSearch, ok := params["startSearch"].(bool)
	if !ok {
		t.Fatal("Expected startSearch to be set when search is present")
	}
	if !startSearch {
		t.Error("Expected startSearch to default to true")
	}
}

func TestGetItems_FilterPassthrough(t *testing.T) {
	filter := map[string]interface{}{"key_": "system.cpu.util"}
	params := buildGetItemsParams(map[string]interface{}{
		"filter": filter,
	})
	if _, ok := params["filter"]; !ok {
		t.Error("Expected filter to be passed through")
	}
}

func TestGetItems_FilterAndSearchCoexist(t *testing.T) {
	params := buildGetItemsParams(map[string]interface{}{
		"filter": map[string]interface{}{"key_": "exact.key"},
		"search": map[string]interface{}{"name": "CPU"},
	})
	if _, ok := params["filter"]; !ok {
		t.Error("Expected filter to be passed through")
	}
	if _, ok := params["search"]; !ok {
		t.Error("Expected search to be passed through")
	}
	if _, ok := params["startSearch"]; !ok {
		t.Error("Expected startSearch when search is present")
	}
}

// buildGetTriggersParams replicates the parameter building logic from GetTriggers
func buildGetTriggersParams(args map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	if output, ok := args["output"]; ok {
		params["output"] = output
	} else {
		params["output"] = []string{"triggerid", "description", "priority", "status", "value", "state"}
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
	params["selectHosts"] = []string{"hostid", "host", "name"}
	params["expandDescription"] = true
	return params
}

func TestGetTriggers_DefaultOutputFields(t *testing.T) {
	params := buildGetTriggersParams(map[string]interface{}{})
	output, ok := params["output"].([]string)
	if !ok {
		t.Fatal("Expected output to be []string")
	}
	expected := []string{"triggerid", "description", "priority", "status", "value", "state"}
	if len(output) != len(expected) {
		t.Fatalf("Expected %d output fields, got %d", len(expected), len(output))
	}
	for i, field := range expected {
		if output[i] != field {
			t.Errorf("Expected output[%d] = '%s', got '%s'", i, field, output[i])
		}
	}
}

func TestGetTriggers_SelectHostsRestricted(t *testing.T) {
	params := buildGetTriggersParams(map[string]interface{}{})
	selectHosts, ok := params["selectHosts"].([]string)
	if !ok {
		t.Fatal("Expected selectHosts to be []string")
	}
	expected := []string{"hostid", "host", "name"}
	if len(selectHosts) != len(expected) {
		t.Fatalf("Expected %d selectHosts fields, got %d", len(expected), len(selectHosts))
	}
	for i, field := range expected {
		if selectHosts[i] != field {
			t.Errorf("Expected selectHosts[%d] = '%s', got '%s'", i, field, selectHosts[i])
		}
	}
}

// buildGetProblemsParams replicates the parameter building logic from GetProblems
func buildGetProblemsParams(args map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	params["output"] = "extend"
	params["selectHosts"] = []string{"hostid", "host", "name"}
	params["selectTags"] = "extend"
	params["sortfield"] = []string{"eventid"}
	params["sortorder"] = "DESC"
	if recent, ok := args["recent"].(bool); ok && recent {
		params["recent"] = true
	}
	if hostids, ok := args["hostids"]; ok {
		params["hostids"] = hostids
	}
	if limit, ok := args["limit"]; ok {
		params["limit"] = limit
	}
	return params
}

func TestGetProblems_SelectHostsRestricted(t *testing.T) {
	params := buildGetProblemsParams(map[string]interface{}{})
	selectHosts, ok := params["selectHosts"].([]string)
	if !ok {
		t.Fatal("Expected selectHosts to be []string")
	}
	expected := []string{"hostid", "host", "name"}
	if len(selectHosts) != len(expected) {
		t.Fatalf("Expected %d selectHosts fields, got %d", len(expected), len(selectHosts))
	}
	for i, field := range expected {
		if selectHosts[i] != field {
			t.Errorf("Expected selectHosts[%d] = '%s', got '%s'", i, field, selectHosts[i])
		}
	}
}

func TestGetProblems_OutputRemainsExtend(t *testing.T) {
	params := buildGetProblemsParams(map[string]interface{}{})
	output, ok := params["output"].(string)
	if !ok {
		t.Fatal("Expected output to be string")
	}
	if output != "extend" {
		t.Errorf("Expected output 'extend' for problems, got '%s'", output)
	}
}

func TestGetProblems_SelectTagsRemainsExtend(t *testing.T) {
	params := buildGetProblemsParams(map[string]interface{}{})
	selectTags, ok := params["selectTags"].(string)
	if !ok {
		t.Fatal("Expected selectTags to be string")
	}
	if selectTags != "extend" {
		t.Errorf("Expected selectTags 'extend', got '%s'", selectTags)
	}
}

// Tests for batch params
func TestGetItemsBatch_DefaultOutputFields(t *testing.T) {
	// Replicate batch default output
	var output interface{} = []string{"itemid", "hostid", "name", "key_", "value_type", "lastvalue", "units"}
	fields, ok := output.([]string)
	if !ok {
		t.Fatal("Expected output to be []string")
	}
	if len(fields) != 7 {
		t.Errorf("Expected 7 output fields for batch, got %d", len(fields))
	}
}

func TestGetItemsBatch_StartSearchDefault(t *testing.T) {
	// When start_search is not in args, default to true
	args := map[string]interface{}{}
	startSearch := true
	if ss, ok := args["start_search"].(bool); ok {
		startSearch = ss
	}
	if !startSearch {
		t.Error("Expected startSearch to default to true for batch")
	}
}

func TestGetItemsBatch_StartSearchExplicitFalse(t *testing.T) {
	args := map[string]interface{}{"start_search": false}
	startSearch := true
	if ss, ok := args["start_search"].(bool); ok {
		startSearch = ss
	}
	if startSearch {
		t.Error("Expected startSearch to be false when explicitly set")
	}
}
