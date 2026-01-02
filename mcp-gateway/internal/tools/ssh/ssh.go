package ssh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"golang.org/x/crypto/ssh"
)

// SSHTool handles SSH operations
type SSHTool struct {
	logger *log.Logger
}

// NewSSHTool creates a new SSH tool
func NewSSHTool(logger *log.Logger) *SSHTool {
	return &SSHTool{logger: logger}
}

// SSHConfig holds SSH connection configuration
type SSHConfig struct {
	Servers           []string
	Username          string
	PrivateKey        string
	Port              int
	CommandTimeout    int
	ConnectionTimeout int
	KnownHostsPolicy  string
}

// ServerResult represents the result of a command on a single server
type ServerResult struct {
	Server     string `json:"server"`
	Success    bool   `json:"success"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// ExecuteResult represents the overall execution result
type ExecuteResult struct {
	Results []ServerResult `json:"results"`
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Error string `json:"error,omitempty"`
}

// ConnectivityResult represents connectivity test result
type ConnectivityResult struct {
	Results []struct {
		Server    string `json:"server"`
		Reachable bool   `json:"reachable"`
		Error     string `json:"error,omitempty"`
	} `json:"results"`
	Summary struct {
		Total       int `json:"total"`
		Reachable   int `json:"reachable"`
		Unreachable int `json:"unreachable"`
	} `json:"summary"`
	Error string `json:"error,omitempty"`
}

// getConfig fetches SSH configuration from database
func (t *SSHTool) getConfig(ctx context.Context, incidentID string) (*SSHConfig, error) {
	creds, err := database.GetToolCredentialsForIncident(ctx, incidentID, "ssh")
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH credentials: %w", err)
	}

	config := &SSHConfig{
		Port:              22,
		CommandTimeout:    120,
		ConnectionTimeout: 30,
		KnownHostsPolicy:  "auto_add",
	}

	// Parse settings
	settings := creds.Settings

	// Helper to get string setting with fallback key (handles both "ssh_servers" and "servers")
	getString := func(primaryKey, fallbackKey string) string {
		if val, ok := settings[primaryKey].(string); ok && val != "" {
			return val
		}
		if val, ok := settings[fallbackKey].(string); ok && val != "" {
			return val
		}
		return ""
	}

	// Get servers (try ssh_servers first, then servers)
	if servers := getString("ssh_servers", "servers"); servers != "" {
		config.Servers = strings.Split(servers, ",")
		for i, s := range config.Servers {
			config.Servers[i] = strings.TrimSpace(s)
		}
	}

	// Get username (try ssh_username first, then username)
	config.Username = getString("ssh_username", "username")

	// Get private key (may be base64 encoded) - try ssh_private_key first, then private_key
	if privateKey := getString("ssh_private_key", "private_key"); privateKey != "" {
		if strings.HasPrefix(privateKey, "base64:") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(privateKey, "base64:"))
			if err == nil {
				config.PrivateKey = string(decoded)
			} else {
				config.PrivateKey = privateKey
			}
		} else {
			config.PrivateKey = privateKey
		}
	}

	// Get port
	if port, ok := settings["ssh_port"].(float64); ok {
		config.Port = int(port)
	}

	// Get timeouts
	if timeout, ok := settings["ssh_command_timeout"].(float64); ok {
		config.CommandTimeout = int(timeout)
	}
	if timeout, ok := settings["ssh_connection_timeout"].(float64); ok {
		config.ConnectionTimeout = int(timeout)
	}

	// Get known hosts policy
	if policy, ok := settings["ssh_known_hosts_policy"].(string); ok {
		config.KnownHostsPolicy = policy
	}

	return config, nil
}

// fixPEMKey reconstructs a PEM key that may have spaces instead of newlines
func fixPEMKey(key string) string {
	// If already has newlines, return as-is
	if strings.Contains(key, "\n") {
		return key
	}

	// Check for valid PEM markers
	if !strings.Contains(key, "-----BEGIN") || !strings.Contains(key, "-----END") {
		return key
	}

	// Split on whitespace and reconstruct
	parts := strings.Fields(key)
	if len(parts) < 4 {
		return key
	}

	var header, footer string
	var bodyParts []string

	for i := 0; i < len(parts); i++ {
		part := parts[i]

		if strings.HasPrefix(part, "-----BEGIN") {
			// Header spans from here to next "-----"
			headerParts := []string{part}
			for j := i + 1; j < len(parts); j++ {
				headerParts = append(headerParts, parts[j])
				if strings.HasSuffix(parts[j], "-----") {
					header = strings.Join(headerParts, " ")
					i = j
					break
				}
			}
		} else if strings.HasPrefix(part, "-----END") {
			// Footer spans from here to end marker
			footerParts := []string{part}
			for j := i + 1; j < len(parts); j++ {
				footerParts = append(footerParts, parts[j])
				if strings.HasSuffix(parts[j], "-----") {
					break
				}
			}
			footer = strings.Join(footerParts, " ")
			break
		} else if header != "" && !strings.HasSuffix(part, "-----") {
			bodyParts = append(bodyParts, part)
		}
	}

	if header == "" || footer == "" {
		return key
	}

	// Join body parts (base64 content has no spaces)
	body := strings.Join(bodyParts, "")

	return header + "\n" + body + "\n" + footer + "\n"
}

// parsePrivateKey parses a PEM-encoded private key
func parsePrivateKey(keyData string) (ssh.Signer, error) {
	// Fix PEM key if it has spaces instead of newlines
	keyData = fixPEMKey(keyData)

	signer, err := ssh.ParsePrivateKey([]byte(keyData))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	return signer, nil
}

// executeOnServer executes a command on a single server
func (t *SSHTool) executeOnServer(ctx context.Context, server, command string, config *SSHConfig) ServerResult {
	startTime := time.Now()

	result := ServerResult{
		Server:   server,
		ExitCode: -1,
	}

	// Parse private key
	signer, err := parsePrivateKey(config.PrivateKey)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to parse private key: %v", err)
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	}

	// Create SSH client config
	clientConfig := &ssh.ClientConfig{
		User: config.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: implement proper host key checking
		Timeout:         time.Duration(config.ConnectionTimeout) * time.Second,
	}

	// Connect to server
	addr := fmt.Sprintf("%s:%d", server, config.Port)
	t.logger.Printf("Connecting to %s as %s", addr, config.Username)

	conn, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		result.Error = fmt.Sprintf("Connection failed: %v", err)
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	}
	defer conn.Close()

	// Create session
	session, err := conn.NewSession()
	if err != nil {
		result.Error = fmt.Sprintf("Session creation failed: %v", err)
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	}
	defer session.Close()

	// Execute command with timeout
	type commandResult struct {
		stdout   []byte
		stderr   []byte
		exitCode int
		err      error
	}

	resultChan := make(chan commandResult, 1)
	go func() {
		var stdout, stderr strings.Builder
		session.Stdout = &stdout
		session.Stderr = &stderr

		err := session.Run(command)

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				exitCode = exitErr.ExitStatus()
				err = nil // Not a real error, just non-zero exit
			}
		}

		resultChan <- commandResult{
			stdout:   []byte(stdout.String()),
			stderr:   []byte(stderr.String()),
			exitCode: exitCode,
			err:      err,
		}
	}()

	// Wait for result or timeout
	select {
	case <-ctx.Done():
		result.Error = "Command timed out"
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	case <-time.After(time.Duration(config.CommandTimeout) * time.Second):
		result.Error = "Command timed out"
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	case cmdResult := <-resultChan:
		if cmdResult.err != nil {
			result.Error = fmt.Sprintf("Command execution failed: %v", cmdResult.err)
		} else {
			result.Success = cmdResult.exitCode == 0
			result.ExitCode = cmdResult.exitCode
		}
		result.Stdout = string(cmdResult.stdout)
		result.Stderr = string(cmdResult.stderr)
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	}
}

// ExecuteCommand executes a command on all or specified servers
func (t *SSHTool) ExecuteCommand(ctx context.Context, incidentID string, command string, servers []string) (string, error) {
	config, err := t.getConfig(ctx, incidentID)
	if err != nil {
		return "", err
	}

	// Validate configuration
	if len(config.Servers) == 0 {
		return t.jsonResult(ExecuteResult{Error: "No servers configured"})
	}
	if config.PrivateKey == "" {
		return t.jsonResult(ExecuteResult{Error: "SSH private key not configured"})
	}
	if config.Username == "" {
		return t.jsonResult(ExecuteResult{Error: "SSH username not configured"})
	}

	// Determine target servers
	targetServers := config.Servers
	if len(servers) > 0 {
		// Validate that all specified servers are configured
		configuredSet := make(map[string]bool)
		for _, s := range config.Servers {
			configuredSet[s] = true
		}
		for _, s := range servers {
			if !configuredSet[s] {
				return t.jsonResult(ExecuteResult{Error: fmt.Sprintf("Server not configured: %s", s)})
			}
		}
		targetServers = servers
	}

	// Execute in parallel
	var wg sync.WaitGroup
	results := make([]ServerResult, len(targetServers))

	for i, server := range targetServers {
		wg.Add(1)
		go func(idx int, srv string) {
			defer wg.Done()
			results[idx] = t.executeOnServer(ctx, srv, command, config)
		}(i, server)
	}

	wg.Wait()

	// Build result
	execResult := ExecuteResult{Results: results}
	for _, r := range results {
		execResult.Summary.Total++
		if r.Success {
			execResult.Summary.Succeeded++
		} else {
			execResult.Summary.Failed++
		}
	}

	return t.jsonResult(execResult)
}

// TestConnectivity tests SSH connectivity to all servers
func (t *SSHTool) TestConnectivity(ctx context.Context, incidentID string) (string, error) {
	config, err := t.getConfig(ctx, incidentID)
	if err != nil {
		return "", err
	}

	if len(config.Servers) == 0 {
		return t.jsonResult(ConnectivityResult{Error: "No servers configured"})
	}
	if config.PrivateKey == "" {
		return t.jsonResult(ConnectivityResult{Error: "SSH private key not configured"})
	}

	signer, err := parsePrivateKey(config.PrivateKey)
	if err != nil {
		return t.jsonResult(ConnectivityResult{Error: fmt.Sprintf("Failed to parse private key: %v", err)})
	}

	clientConfig := &ssh.ClientConfig{
		User: config.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Duration(config.ConnectionTimeout) * time.Second,
	}

	var result ConnectivityResult
	for _, server := range config.Servers {
		addr := fmt.Sprintf("%s:%d", server, config.Port)

		conn, err := net.DialTimeout("tcp", addr, time.Duration(config.ConnectionTimeout)*time.Second)
		if err != nil {
			result.Results = append(result.Results, struct {
				Server    string `json:"server"`
				Reachable bool   `json:"reachable"`
				Error     string `json:"error,omitempty"`
			}{
				Server:    server,
				Reachable: false,
				Error:     fmt.Sprintf("TCP connection failed: %v", err),
			})
			continue
		}
		conn.Close()

		// Try SSH connection
		sshConn, err := ssh.Dial("tcp", addr, clientConfig)
		if err != nil {
			result.Results = append(result.Results, struct {
				Server    string `json:"server"`
				Reachable bool   `json:"reachable"`
				Error     string `json:"error,omitempty"`
			}{
				Server:    server,
				Reachable: false,
				Error:     fmt.Sprintf("SSH connection failed: %v", err),
			})
			continue
		}
		sshConn.Close()

		result.Results = append(result.Results, struct {
			Server    string `json:"server"`
			Reachable bool   `json:"reachable"`
			Error     string `json:"error,omitempty"`
		}{
			Server:    server,
			Reachable: true,
		})
	}

	// Calculate summary
	for _, r := range result.Results {
		result.Summary.Total++
		if r.Reachable {
			result.Summary.Reachable++
		} else {
			result.Summary.Unreachable++
		}
	}

	return t.jsonResult(result)
}

// GetServerInfo gets basic system info from all servers
func (t *SSHTool) GetServerInfo(ctx context.Context, incidentID string) (string, error) {
	infoCommand := `echo "HOSTNAME=$(hostname)" && ` +
		`echo "OS=$(cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d'"' -f2 || uname -s)" && ` +
		`echo "UPTIME=$(uptime -p 2>/dev/null || uptime | awk -F'up ' '{print $2}' | awk -F',' '{print $1}')"`

	return t.ExecuteCommand(ctx, incidentID, infoCommand, nil)
}

// jsonResult converts a result to JSON string
func (t *SSHTool) jsonResult(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
