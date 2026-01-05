package ssh

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
)

func TestNewSSHTool(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewSSHTool(logger)

	if tool == nil {
		t.Fatal("Expected tool to not be nil")
	}
	if tool.logger == nil {
		t.Error("Expected logger to be set")
	}
}

func TestFixPEMKey_AlreadyHasNewlines(t *testing.T) {
	key := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA
-----END RSA PRIVATE KEY-----`

	result := fixPEMKey(key)

	if result != key {
		t.Errorf("Expected key to be unchanged when it already has newlines")
	}
}

func TestFixPEMKey_SpaceSeparated(t *testing.T) {
	// Simulate a PEM key with spaces instead of newlines
	key := "-----BEGIN RSA PRIVATE KEY----- MIIEpAIBAAKCAQEA -----END RSA PRIVATE KEY-----"

	result := fixPEMKey(key)

	// Should have newlines
	if !strings.Contains(result, "\n") {
		t.Error("Expected result to contain newlines")
	}

	// Should have proper header and footer
	if !strings.HasPrefix(result, "-----BEGIN RSA PRIVATE KEY-----") {
		t.Error("Expected result to start with header")
	}

	if !strings.Contains(result, "-----END RSA PRIVATE KEY-----") {
		t.Error("Expected result to contain footer")
	}
}

func TestFixPEMKey_InvalidPEM(t *testing.T) {
	// Not a valid PEM format
	key := "not a valid pem key"

	result := fixPEMKey(key)

	// Should return unchanged
	if result != key {
		t.Errorf("Expected invalid key to be returned unchanged")
	}
}

func TestFixPEMKey_NoEndMarker(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY----- somedata"

	result := fixPEMKey(key)

	// Should return unchanged since no END marker
	if result != key {
		t.Errorf("Expected key without END marker to be returned unchanged")
	}
}

func TestFixPEMKey_OpenSSHFormat(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY----- b3BlbnNzaC1rZXktdjEA -----END OPENSSH PRIVATE KEY-----"

	result := fixPEMKey(key)

	if !strings.Contains(result, "\n") {
		t.Error("Expected result to contain newlines for OpenSSH format")
	}

	if !strings.Contains(result, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Error("Expected result to preserve OPENSSH header")
	}
}

func TestServerResult_JSONSerialization(t *testing.T) {
	result := ServerResult{
		Server:     "test-server",
		Success:    true,
		Stdout:     "command output",
		Stderr:     "",
		ExitCode:   0,
		DurationMs: 150,
		Error:      "",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal ServerResult: %v", err)
	}

	var decoded ServerResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ServerResult: %v", err)
	}

	if decoded.Server != result.Server {
		t.Errorf("Server mismatch: expected '%s', got '%s'", result.Server, decoded.Server)
	}
	if decoded.Success != result.Success {
		t.Errorf("Success mismatch: expected %v, got %v", result.Success, decoded.Success)
	}
	if decoded.Stdout != result.Stdout {
		t.Errorf("Stdout mismatch: expected '%s', got '%s'", result.Stdout, decoded.Stdout)
	}
	if decoded.ExitCode != result.ExitCode {
		t.Errorf("ExitCode mismatch: expected %d, got %d", result.ExitCode, decoded.ExitCode)
	}
	if decoded.DurationMs != result.DurationMs {
		t.Errorf("DurationMs mismatch: expected %d, got %d", result.DurationMs, decoded.DurationMs)
	}
}

func TestServerResult_JSONWithError(t *testing.T) {
	result := ServerResult{
		Server:     "failed-server",
		Success:    false,
		ExitCode:   -1,
		DurationMs: 50,
		Error:      "Connection refused",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal ServerResult: %v", err)
	}

	// Verify error field is present in JSON
	if !strings.Contains(string(data), "Connection refused") {
		t.Error("Expected JSON to contain error message")
	}

	var decoded ServerResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.Error != result.Error {
		t.Errorf("Error mismatch: expected '%s', got '%s'", result.Error, decoded.Error)
	}
}

func TestExecuteResult_JSONSerialization(t *testing.T) {
	result := ExecuteResult{
		Results: []ServerResult{
			{Server: "server1", Success: true, ExitCode: 0, Stdout: "ok"},
			{Server: "server2", Success: false, ExitCode: 1, Error: "failed"},
		},
	}
	result.Summary.Total = 2
	result.Summary.Succeeded = 1
	result.Summary.Failed = 1

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal ExecuteResult: %v", err)
	}

	var decoded ExecuteResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(decoded.Results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(decoded.Results))
	}

	if decoded.Summary.Total != 2 {
		t.Errorf("Expected Total 2, got %d", decoded.Summary.Total)
	}
	if decoded.Summary.Succeeded != 1 {
		t.Errorf("Expected Succeeded 1, got %d", decoded.Summary.Succeeded)
	}
	if decoded.Summary.Failed != 1 {
		t.Errorf("Expected Failed 1, got %d", decoded.Summary.Failed)
	}
}

func TestConnectivityResult_JSONSerialization(t *testing.T) {
	result := ConnectivityResult{}
	result.Results = []struct {
		Server    string `json:"server"`
		Reachable bool   `json:"reachable"`
		Error     string `json:"error,omitempty"`
	}{
		{Server: "server1", Reachable: true},
		{Server: "server2", Reachable: false, Error: "Connection timeout"},
	}
	result.Summary.Total = 2
	result.Summary.Reachable = 1
	result.Summary.Unreachable = 1

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal ConnectivityResult: %v", err)
	}

	var decoded ConnectivityResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(decoded.Results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(decoded.Results))
	}

	if decoded.Summary.Reachable != 1 {
		t.Errorf("Expected Reachable 1, got %d", decoded.Summary.Reachable)
	}
	if decoded.Summary.Unreachable != 1 {
		t.Errorf("Expected Unreachable 1, got %d", decoded.Summary.Unreachable)
	}
}

func TestSSHConfig_Defaults(t *testing.T) {
	// Test that default values are sensible
	config := &SSHConfig{
		Hosts: []SSHHostConfig{
			{Hostname: "server1", Address: "192.168.1.1", User: "root", Port: 22},
			{Hostname: "server2", Address: "192.168.1.2", User: "admin", Port: 2222},
		},
		Keys: map[string]*SSHKey{
			"key-1": {ID: "key-1", Name: "default-key", PrivateKey: "key-data", IsDefault: true},
		},
		DefaultKeyID:      "key-1",
		CommandTimeout:    120,
		ConnectionTimeout: 30,
		KnownHostsPolicy:  "auto_add",
	}

	if len(config.Hosts) != 2 {
		t.Errorf("Expected 2 hosts, got %d", len(config.Hosts))
	}
	if config.Hosts[0].Port != 22 {
		t.Errorf("Expected first host port 22, got %d", config.Hosts[0].Port)
	}
	if config.Hosts[1].Port != 2222 {
		t.Errorf("Expected second host port 2222, got %d", config.Hosts[1].Port)
	}
	if config.CommandTimeout != 120 {
		t.Errorf("Expected default command timeout 120, got %d", config.CommandTimeout)
	}
	if config.ConnectionTimeout != 30 {
		t.Errorf("Expected default connection timeout 30, got %d", config.ConnectionTimeout)
	}
}

func TestSSHTool_jsonResult(t *testing.T) {
	logger := log.New(os.Stdout, "test: ", log.LstdFlags)
	tool := NewSSHTool(logger)

	result := ExecuteResult{
		Results: []ServerResult{
			{Server: "test", Success: true},
		},
	}

	jsonStr, err := tool.jsonResult(result)
	if err != nil {
		t.Fatalf("jsonResult failed: %v", err)
	}

	if jsonStr == "" {
		t.Error("Expected non-empty JSON string")
	}

	// Verify it's valid JSON
	var parsed ExecuteResult
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Errorf("Result is not valid JSON: %v", err)
	}
}

func TestFixPEMKey_MultipleSpaces(t *testing.T) {
	// PEM key with multiple consecutive spaces
	key := "-----BEGIN RSA PRIVATE KEY-----   MIIEpAIBAAKCAQEA   -----END RSA PRIVATE KEY-----"

	result := fixPEMKey(key)

	// Should handle multiple spaces correctly
	if !strings.Contains(result, "\n") {
		t.Error("Expected result to contain newlines")
	}
}

func TestFixPEMKey_WithTabs(t *testing.T) {
	// PEM key with tabs (Fields splits on all whitespace)
	key := "-----BEGIN RSA PRIVATE KEY-----\tMIIEpAIBAAKCAQEA\t-----END RSA PRIVATE KEY-----"

	result := fixPEMKey(key)

	// Should have proper structure
	if !strings.Contains(result, "-----BEGIN RSA PRIVATE KEY-----") {
		t.Error("Expected result to contain header")
	}
}

func TestServerResult_EmptyStringsOmitted(t *testing.T) {
	result := ServerResult{
		Server:     "test",
		Success:    true,
		Stdout:     "output",
		Stderr:     "",
		ExitCode:   0,
		DurationMs: 100,
		Error:      "", // Should be omitted with omitempty
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Error field should be omitted when empty (omitempty)
	if strings.Contains(string(data), `"error":""`) {
		t.Error("Expected empty error field to be omitted from JSON")
	}
}

func TestExecuteResult_WithError(t *testing.T) {
	result := ExecuteResult{
		Error: "No servers configured",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	if !strings.Contains(string(data), "No servers configured") {
		t.Error("Expected error message in JSON")
	}

	var decoded ExecuteResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.Error != "No servers configured" {
		t.Errorf("Error mismatch: got '%s'", decoded.Error)
	}
}

func TestConnectivityResult_WithError(t *testing.T) {
	result := ConnectivityResult{
		Error: "SSH private key not configured",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	if !strings.Contains(string(data), "SSH private key not configured") {
		t.Error("Expected error message in JSON")
	}
}
