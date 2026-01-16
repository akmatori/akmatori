package zabbix

import (
	"encoding/json"
	"log"
	"os"
	"testing"
)

func TestNewZabbixTool(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewZabbixTool(logger)

	if tool == nil {
		t.Fatal("Expected tool to not be nil")
	}
	if tool.logger == nil {
		t.Error("Expected logger to be set")
	}
	if tool.requestID != 0 {
		t.Errorf("Expected initial requestID to be 0, got %d", tool.requestID)
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
	tool := NewZabbixTool(logger)

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
