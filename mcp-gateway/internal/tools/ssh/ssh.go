package ssh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

// SSHKey holds an SSH private key with metadata
type SSHKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
	IsDefault  bool   `json:"is_default"`
	CreatedAt  string `json:"created_at"`
}

// SSHHostConfig holds per-host SSH connection configuration
type SSHHostConfig struct {
	Hostname           string `json:"hostname"`                       // Display name (e.g., "web-prod-1")
	Address            string `json:"address"`                        // Real connection address (IP or FQDN)
	User               string `json:"user,omitempty"`                 // SSH username (default: "root")
	Port               int    `json:"port,omitempty"`                 // SSH port (default: 22)
	KeyID              string `json:"key_id,omitempty"`               // Override key for this host (uses default if empty)
	JumphostAddress    string `json:"jumphost_address,omitempty"`     // Bastion/jumphost address
	JumphostUser       string `json:"jumphost_user,omitempty"`        // Jumphost username
	JumphostPort       int    `json:"jumphost_port,omitempty"`        // Jumphost port (default: 22)
	AllowWriteCommands bool   `json:"allow_write_commands,omitempty"` // Allow write/destructive commands (default: false)
}

// SSHConfig holds SSH connection configuration
type SSHConfig struct {
	// Per-host configurations
	Hosts []SSHHostConfig

	// SSH keys (new multi-key support)
	Keys         map[string]*SSHKey // key_id -> SSHKey
	DefaultKeyID string             // ID of the default key

	// Global settings
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
		CommandTimeout:    120,
		ConnectionTimeout: 30,
		KnownHostsPolicy:  "auto_add",
		Keys:              make(map[string]*SSHKey),
	}

	settings := creds.Settings

	// Helper functions
	getInt := func(key string, defaultVal int) int {
		if val, ok := settings[key].(float64); ok {
			return int(val)
		}
		return defaultVal
	}

	// Parse SSH keys (new format)
	if keysData, ok := settings["ssh_keys"].([]interface{}); ok && len(keysData) > 0 {
		for _, keyData := range keysData {
			keyMap, ok := keyData.(map[string]interface{})
			if !ok {
				continue
			}

			key := &SSHKey{}
			if id, ok := keyMap["id"].(string); ok {
				key.ID = id
			}
			if name, ok := keyMap["name"].(string); ok {
				key.Name = name
			}
			if privateKey, ok := keyMap["private_key"].(string); ok {
				// Handle base64 encoded keys
				if strings.HasPrefix(privateKey, "base64:") {
					decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(privateKey, "base64:"))
					if err == nil {
						key.PrivateKey = string(decoded)
					} else {
						key.PrivateKey = privateKey
					}
				} else {
					key.PrivateKey = privateKey
				}
			}
			if isDefault, ok := keyMap["is_default"].(bool); ok {
				key.IsDefault = isDefault
			}
			if createdAt, ok := keyMap["created_at"].(string); ok {
				key.CreatedAt = createdAt
			}

			if key.ID != "" && key.PrivateKey != "" {
				config.Keys[key.ID] = key
				if key.IsDefault {
					config.DefaultKeyID = key.ID
				}
			}
		}
	}

	// Get global timeouts
	config.CommandTimeout = getInt("ssh_command_timeout", 120)
	config.ConnectionTimeout = getInt("ssh_connection_timeout", 30)

	if policy, ok := settings["ssh_known_hosts_policy"].(string); ok {
		config.KnownHostsPolicy = policy
	}

	// Parse ssh_hosts array
	hostsData, ok := settings["ssh_hosts"].([]interface{})
	if !ok || len(hostsData) == 0 {
		return nil, fmt.Errorf("ssh_hosts must be configured with at least one host")
	}

	for _, hostData := range hostsData {
		hostMap, ok := hostData.(map[string]interface{})
		if !ok {
			continue
		}

		host := SSHHostConfig{
			AllowWriteCommands: false, // Default to read-only
		}

		// Required fields
		if hostname, ok := hostMap["hostname"].(string); ok {
			host.Hostname = hostname
		}
		if address, ok := hostMap["address"].(string); ok {
			host.Address = address
		}

		// Optional fields with defaults
		if user, ok := hostMap["user"].(string); ok && user != "" {
			host.User = user
		} else {
			host.User = "root"
		}

		if port, ok := hostMap["port"].(float64); ok && port > 0 {
			host.Port = int(port)
		} else {
			host.Port = 22
		}

		// Per-host key override
		if keyID, ok := hostMap["key_id"].(string); ok {
			host.KeyID = keyID
		}

		// Jumphost configuration
		if addr, ok := hostMap["jumphost_address"].(string); ok {
			host.JumphostAddress = addr
		}
		if user, ok := hostMap["jumphost_user"].(string); ok {
			host.JumphostUser = user
		}
		if port, ok := hostMap["jumphost_port"].(float64); ok && port > 0 {
			host.JumphostPort = int(port)
		} else if host.JumphostAddress != "" {
			host.JumphostPort = 22
		}

		// Security settings
		if allow, ok := hostMap["allow_write_commands"].(bool); ok {
			host.AllowWriteCommands = allow
		}

		config.Hosts = append(config.Hosts, host)
	}

	return config, nil
}

// getKeyForHost returns the private key to use for a specific host
func (t *SSHTool) getKeyForHost(hostConfig *SSHHostConfig, config *SSHConfig) (string, error) {
	// If using new multi-key format
	if len(config.Keys) > 0 {
		// Check for per-host key override
		if hostConfig.KeyID != "" {
			if key, ok := config.Keys[hostConfig.KeyID]; ok {
				return key.PrivateKey, nil
			}
			return "", fmt.Errorf("SSH key with ID '%s' not found for host '%s'", hostConfig.KeyID, hostConfig.Hostname)
		}

		// Use default key
		if config.DefaultKeyID != "" {
			if key, ok := config.Keys[config.DefaultKeyID]; ok {
				return key.PrivateKey, nil
			}
		}

		// If no default set, use the first key
		for _, key := range config.Keys {
			return key.PrivateKey, nil
		}

		return "", fmt.Errorf("no SSH keys configured")
	}

	return "", fmt.Errorf("SSH private key not configured")
}

// connect establishes SSH connection (direct or via jumphost)
func (t *SSHTool) connect(ctx context.Context, hostConfig *SSHHostConfig, config *SSHConfig) (*ssh.Client, error) {
	if hostConfig.JumphostAddress != "" {
		return t.connectViaJumphost(ctx, hostConfig, config)
	}
	return t.connectDirect(ctx, hostConfig, config)
}

// connectDirect establishes a direct SSH connection
func (t *SSHTool) connectDirect(ctx context.Context, hostConfig *SSHHostConfig, config *SSHConfig) (*ssh.Client, error) {
	// Get the appropriate key for this host
	privateKey, err := t.getKeyForHost(hostConfig, config)
	if err != nil {
		return nil, err
	}

	signer, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	user := hostConfig.User
	if user == "" {
		user = "root"
	}

	port := hostConfig.Port
	if port == 0 {
		port = 22
	}

	clientConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: implement proper host key checking
		Timeout:         time.Duration(config.ConnectionTimeout) * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", hostConfig.Address, port)
	t.logger.Printf("Connecting directly to %s as %s", addr, user)

	return ssh.Dial("tcp", addr, clientConfig)
}

// connectViaJumphost establishes SSH connection through a bastion host
func (t *SSHTool) connectViaJumphost(ctx context.Context, hostConfig *SSHHostConfig, config *SSHConfig) (*ssh.Client, error) {
	// Get the appropriate key for this host
	privateKey, err := t.getKeyForHost(hostConfig, config)
	if err != nil {
		return nil, err
	}

	signer, err := parsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Jumphost connection config
	jumphostUser := hostConfig.JumphostUser
	if jumphostUser == "" {
		jumphostUser = hostConfig.User // Fallback to target user
	}
	if jumphostUser == "" {
		jumphostUser = "root"
	}

	jumphostPort := hostConfig.JumphostPort
	if jumphostPort == 0 {
		jumphostPort = 22
	}

	jumphostConfig := &ssh.ClientConfig{
		User: jumphostUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: proper host key checking
		Timeout:         time.Duration(config.ConnectionTimeout) * time.Second,
	}

	// Step 1: Connect to jumphost
	jumphostAddr := fmt.Sprintf("%s:%d", hostConfig.JumphostAddress, jumphostPort)
	t.logger.Printf("Connecting to jumphost %s as %s", jumphostAddr, jumphostUser)

	jumphostConn, err := ssh.Dial("tcp", jumphostAddr, jumphostConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to jumphost: %w", err)
	}

	// Step 2: Dial through jumphost to target
	targetUser := hostConfig.User
	if targetUser == "" {
		targetUser = "root"
	}

	targetPort := hostConfig.Port
	if targetPort == 0 {
		targetPort = 22
	}

	targetAddr := fmt.Sprintf("%s:%d", hostConfig.Address, targetPort)
	t.logger.Printf("Dialing target %s through jumphost", targetAddr)

	targetNetConn, err := jumphostConn.Dial("tcp", targetAddr)
	if err != nil {
		jumphostConn.Close()
		return nil, fmt.Errorf("failed to dial target through jumphost: %w", err)
	}

	// Step 3: Establish SSH client connection over the tunnel
	targetConfig := &ssh.ClientConfig{
		User: targetUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Duration(config.ConnectionTimeout) * time.Second,
	}

	ncc, chans, reqs, err := ssh.NewClientConn(targetNetConn, targetAddr, targetConfig)
	if err != nil {
		targetNetConn.Close()
		jumphostConn.Close()
		return nil, fmt.Errorf("failed to establish SSH connection through jumphost: %w", err)
	}

	return ssh.NewClient(ncc, chans, reqs), nil
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

// executeOnServer executes a command on a single server using per-host config
func (t *SSHTool) executeOnServer(ctx context.Context, hostConfig *SSHHostConfig, command string, config *SSHConfig) ServerResult {
	startTime := time.Now()

	result := ServerResult{
		Server:   hostConfig.Hostname,
		ExitCode: -1,
	}

	// Validate command against read-only mode
	validator := NewCommandValidator()
	if err := validator.ValidateCommand(command, hostConfig.AllowWriteCommands); err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result
	}

	// Connect to server (direct or via jumphost)
	conn, err := t.connect(ctx, hostConfig, config)
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
	if len(config.Hosts) == 0 {
		return t.jsonResult(ExecuteResult{Error: "No servers configured"})
	}
	if len(config.Keys) == 0 {
		return t.jsonResult(ExecuteResult{Error: "SSH private key not configured"})
	}

	// Determine target hosts
	var targetHosts []SSHHostConfig
	if len(servers) > 0 {
		// Build a map of configured hostnames for validation
		hostMap := make(map[string]*SSHHostConfig)
		for i := range config.Hosts {
			hostMap[config.Hosts[i].Hostname] = &config.Hosts[i]
			// Also map by address for backward compatibility
			hostMap[config.Hosts[i].Address] = &config.Hosts[i]
		}

		// Validate and collect specified hosts
		for _, s := range servers {
			if host, ok := hostMap[s]; ok {
				targetHosts = append(targetHosts, *host)
			} else {
				return t.jsonResult(ExecuteResult{Error: fmt.Sprintf("Server not configured: %s", s)})
			}
		}
	} else {
		targetHosts = config.Hosts
	}

	// Execute in parallel
	var wg sync.WaitGroup
	results := make([]ServerResult, len(targetHosts))

	for i := range targetHosts {
		wg.Add(1)
		go func(idx int, host *SSHHostConfig) {
			defer wg.Done()
			results[idx] = t.executeOnServer(ctx, host, command, config)
		}(i, &targetHosts[i])
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

	if len(config.Hosts) == 0 {
		return t.jsonResult(ConnectivityResult{Error: "No servers configured"})
	}
	if len(config.Keys) == 0 {
		return t.jsonResult(ConnectivityResult{Error: "SSH private key not configured"})
	}

	var result ConnectivityResult
	for i := range config.Hosts {
		host := &config.Hosts[i]

		// Try to establish connection (handles both direct and jumphost)
		sshConn, err := t.connect(ctx, host, config)
		if err != nil {
			result.Results = append(result.Results, struct {
				Server    string `json:"server"`
				Reachable bool   `json:"reachable"`
				Error     string `json:"error,omitempty"`
			}{
				Server:    host.Hostname,
				Reachable: false,
				Error:     err.Error(),
			})
			continue
		}
		sshConn.Close()

		result.Results = append(result.Results, struct {
			Server    string `json:"server"`
			Reachable bool   `json:"reachable"`
			Error     string `json:"error,omitempty"`
		}{
			Server:    host.Hostname,
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
