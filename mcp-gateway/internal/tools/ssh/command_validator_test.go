package ssh

import (
	"strings"
	"testing"
)

func TestNewCommandValidator(t *testing.T) {
	v := NewCommandValidator()

	if v == nil {
		t.Fatal("NewCommandValidator returned nil")
	}

	if len(v.ReadOnlyCommands) == 0 {
		t.Error("ReadOnlyCommands should not be empty")
	}

	if len(v.AllowedSubcommands) == 0 {
		t.Error("AllowedSubcommands should not be empty")
	}
}

func TestNewCommandValidatorWithPatterns(t *testing.T) {
	denyList := []string{"rm *", "shutdown", "kill *"}
	allowList := []string{"curl *", "wget *"}

	v := NewCommandValidatorWithPatterns(denyList, allowList)

	if v == nil {
		t.Fatal("NewCommandValidatorWithPatterns returned nil")
	}

	if len(v.GlobalDenyPatterns) != len(denyList) {
		t.Errorf("Expected %d deny patterns, got %d", len(denyList), len(v.GlobalDenyPatterns))
	}

	if len(v.GlobalAllowPatterns) != len(allowList) {
		t.Errorf("Expected %d allow patterns, got %d", len(allowList), len(v.GlobalAllowPatterns))
	}
}

// ============================================================================
// Stage 1: Deny List Tests
// ============================================================================

func TestDenyList_RmPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"rm *"}, nil)

	// Should match "rm *"
	if !v.MatchesDenyList("rm -rf /") {
		t.Error("Expected 'rm -rf /' to match 'rm *'")
	}
	if !v.MatchesDenyList("rm file.txt") {
		t.Error("Expected 'rm file.txt' to match 'rm *'")
	}

	// Should NOT match "rmdir" (different command)
	if v.MatchesDenyList("rmdir /tmp") {
		t.Error("Expected 'rmdir /tmp' to NOT match 'rm *'")
	}
}

func TestDenyList_ExactPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"shutdown"}, nil)

	if !v.MatchesDenyList("shutdown") {
		t.Error("Expected 'shutdown' to match exact pattern")
	}
	if !v.MatchesDenyList("shutdown ") {
		t.Error("Expected 'shutdown ' to match exact pattern")
	}
	// "shutdown -h now" should NOT match exact "shutdown"
	if v.MatchesDenyList("shutdown -h now") {
		t.Error("Expected 'shutdown -h now' to NOT match exact 'shutdown'")
	}
}

func TestDenyList_ShutdownWithArgs(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"shutdown *"}, nil)

	if !v.MatchesDenyList("shutdown -h now") {
		t.Error("Expected 'shutdown -h now' to match 'shutdown *'")
	}
	if !v.MatchesDenyList("reboot") {
		// "reboot" should NOT match "shutdown *"
	}
}

func TestDenyList_KillPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"kill *"}, nil)

	if !v.MatchesDenyList("kill 1234") {
		t.Error("Expected 'kill 1234' to match 'kill *'")
	}
	if !v.MatchesDenyList("kill -9 1234") {
		t.Error("Expected 'kill -9 1234' to match 'kill *'")
	}
	// "killall" should NOT match "kill *"
	if v.MatchesDenyList("killall nginx") {
		t.Error("Expected 'killall nginx' to NOT match 'kill *'")
	}
}

func TestDenyList_DockerPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"docker stop *", "docker rm *"}, nil)

	if !v.MatchesDenyList("docker stop container") {
		t.Error("Expected 'docker stop container' to match 'docker stop *'")
	}
	if !v.MatchesDenyList("docker rm container") {
		t.Error("Expected 'docker rm container' to match 'docker rm *'")
	}
	// "docker ps" should NOT match
	if v.MatchesDenyList("docker ps") {
		t.Error("Expected 'docker ps' to NOT match deny patterns")
	}
}

func TestDenyList_LeadingWildcard(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"* --version"}, nil)

	if !v.MatchesDenyList("docker --version") {
		t.Error("Expected 'docker --version' to match '* --version'")
	}
	if !v.MatchesDenyList("git --version") {
		t.Error("Expected 'git --version' to match '* --version'")
	}
	// "docker ps" should NOT match
	if v.MatchesDenyList("docker ps") {
		t.Error("Expected 'docker ps' to NOT match '* --version'")
	}
}

func TestDenyList_MultiWordPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"git push *"}, nil)

	if !v.MatchesDenyList("git push origin main") {
		t.Error("Expected 'git push origin main' to match 'git push *'")
	}
	// "git push" without args should NOT match "git push *" (args required)
	if v.MatchesDenyList("git push") {
		t.Error("Expected 'git push' to NOT match 'git push *' (args required)")
	}
	// "git pull" should NOT match
	if v.MatchesDenyList("git pull") {
		t.Error("Expected 'git pull' to NOT match 'git push *'")
	}
}

func TestDenyList_SystemctlPattern(t *testing.T) {
	denyList := []string{
		"systemctl start *",
		"systemctl stop *",
		"systemctl restart *",
		"systemctl enable *",
		"systemctl disable *",
	}
	v := NewCommandValidatorWithPatterns(denyList, nil)

	blocked := []string{
		"systemctl start nginx",
		"systemctl stop nginx",
		"systemctl restart nginx",
		"systemctl enable nginx",
		"systemctl disable nginx",
	}
	for _, cmd := range blocked {
		if !v.MatchesDenyList(cmd) {
			t.Errorf("Expected '%s' to match deny pattern", cmd)
		}
	}

	// Allowed systemctl subcommands
	allowed := []string{
		"systemctl status nginx",
		"systemctl is-active nginx",
		"systemctl list-units",
	}
	for _, cmd := range allowed {
		if v.MatchesDenyList(cmd) {
			t.Errorf("Expected '%s' to NOT match deny pattern", cmd)
		}
	}
}

// ============================================================================
// Stage 2: Read-Only List Tests
// ============================================================================

func TestReadOnly_AllowedCommands(t *testing.T) {
	v := NewCommandValidator()

	allowedCommands := []string{
		"cat /var/log/syslog",
		"head -n 100 /etc/passwd",
		"tail -f /var/log/messages",
		"ls -la /home",
		"pwd",
		"whoami",
		"uname -a",
		"ps aux",
		"df -h",
		"free -m",
		"netstat -tlnp",
		"grep error /var/log/syslog",
		"rg error /var/log/syslog",
		"find /var/log -name '*.log'",
		"wc -l /etc/passwd",
		"stat /etc/hosts",
		"echo hello",
		"env",
		"uptime",
		"hostname",
		"date",
		"id",
	}

	for _, cmd := range allowedCommands {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Command '%s' should be allowed in read-only mode, got error: %v", cmd, err)
		}
	}
}

func TestReadOnly_BlockedCommands(t *testing.T) {
	v := NewCommandValidator()

	blockedCommands := []string{
		"rm /tmp/file",
		"rm -rf /var/log",
		"mv /tmp/a /tmp/b",
		"cp /etc/passwd /tmp/",
		"chmod 777 /tmp/file",
		"chown root:root /tmp/file",
		"kill 1234",
		"killall nginx",
		"shutdown -h now",
		"reboot",
		"su - root",
		"echo test > /tmp/file",
	}

	for _, cmd := range blockedCommands {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

// ============================================================================
// Stage 3: Allow List Tests
// ============================================================================

func TestAllowList_CurlPattern(t *testing.T) {
	v := NewCommandValidatorWithPatterns(nil, []string{"curl *"})

	if !v.MatchesAllowList("curl https://api.example.com") {
		t.Error("Expected 'curl https://api.example.com' to match 'curl *'")
	}
	if !v.MatchesAllowList("curl -X POST https://api.example.com") {
		t.Error("Expected 'curl -X POST ...' to match 'curl *'")
	}
	// "wget" should NOT match
	if v.MatchesAllowList("wget http://example.com") {
		t.Error("Expected 'wget' to NOT match 'curl *'")
	}
}

func TestAllowList_MultiplePatterns(t *testing.T) {
	v := NewCommandValidatorWithPatterns(nil, []string{"curl *", "wget *", "scp *"})

	allowed := []string{
		"curl https://example.com",
		"wget http://example.com/file",
		"scp file user@host:/path",
	}
	for _, cmd := range allowed {
		if !v.MatchesAllowList(cmd) {
			t.Errorf("Expected '%s' to match allow pattern", cmd)
		}
	}

	// Not allowed
	blocked := []string{
		"rsync -avz src dst",
		"ssh user@host",
	}
	for _, cmd := range blocked {
		if v.MatchesAllowList(cmd) {
			t.Errorf("Expected '%s' to NOT match allow pattern", cmd)
		}
	}
}

// ============================================================================
// Stage 4: Destructive Check Tests
// ============================================================================

func TestDestructive_WriteEnabled(t *testing.T) {
	v := NewCommandValidator()

	// When allowWriteCommands is true, commands not in read-only list are allowed
	dangerousCommands := []string{
		"rm -rf /tmp/file",
		"shutdown -h now",
		"kill -9 1234",
		"systemctl restart nginx",
		"chmod 777 /tmp/file",
	}

	for _, cmd := range dangerousCommands {
		err := v.ValidateCommand(cmd, true)
		if err != nil {
			t.Errorf("Command '%s' should be allowed when allowWriteCommands=true, got error: %v", cmd, err)
		}
	}
}

func TestDestructive_WriteDisabled(t *testing.T) {
	v := NewCommandValidator()

	// When allowWriteCommands is false, commands not in read-only list are blocked
	dangerousCommands := []string{
		"rm -rf /tmp/file",
		"shutdown -h now",
		"kill -9 1234",
		"systemctl restart nginx",
		"chmod 777 /tmp/file",
	}

	for _, cmd := range dangerousCommands {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Command '%s' should be blocked when allowWriteCommands=false", cmd)
		}
	}
}

// ============================================================================
// Full Pipeline Tests
// ============================================================================

func TestPipeline_DenyBlocksReadOnly(t *testing.T) {
	// Even though "cat" is in read-only list, deny list takes precedence
	v := NewCommandValidatorWithPatterns([]string{"cat *"}, nil)

	err := v.ValidateCommand("cat /etc/passwd", false)
	if err == nil {
		t.Error("Expected 'cat /etc/passwd' to be blocked by deny list")
	}
}

func TestPipeline_AllowOverridesReadOnly(t *testing.T) {
	// "curl" is NOT in read-only list, but allow list permits it
	v := NewCommandValidatorWithPatterns(nil, []string{"curl *"})

	err := v.ValidateCommand("curl https://api.example.com", false)
	if err != nil {
		t.Errorf("Expected 'curl' to be allowed by allow list, got error: %v", err)
	}
}

func TestPipeline_DenyOverridesAllow(t *testing.T) {
	// "docker *" is in deny list (blocks ALL docker commands), "curl *" is in allow list
	v := NewCommandValidatorWithPatterns([]string{"docker *"}, []string{"curl *"})

	// Normal curl should be allowed
	err := v.ValidateCommand("curl https://api.example.com", false)
	if err != nil {
		t.Errorf("Expected 'curl https://...' to be allowed, got error: %v", err)
	}

	// docker ps should be blocked by deny list (even though docker is in read-only)
	err = v.ValidateCommand("docker ps", false)
	if err == nil {
		t.Error("Expected 'docker ps' to be blocked by deny list")
	}
}

func TestPipeline_FullFlow(t *testing.T) {
	// Deny list: commands that are ALWAYS blocked (even with write enabled)
	denyList := []string{
		"rm *",
		"shutdown",
		"kill *",
		"docker stop *",
		"docker rm *",
		// Note: systemctl patterns are NOT in deny list - they're blocked by read-only
		// but allowed when write is enabled
		"kubectl delete *",
	}
	// Allow list: commands explicitly permitted beyond read-only
	allowList := []string{
		"curl *",
		"wget *",
		"scp *",
	}

	v := NewCommandValidatorWithPatterns(denyList, allowList)

	tests := []struct {
		cmd     string
		write   bool
		wantErr bool
	}{
		// Stage 1: Deny list blocks (always, even with write enabled)
		{"rm -rf /", false, true},
		{"shutdown", false, true},
		{"shutdown", true, true}, // deny list ALWAYS blocks
		{"kill 1234", false, true},
		{"docker stop container", false, true},
		{"docker stop container", true, true}, // deny list ALWAYS blocks
		{"kubectl delete pod my-pod", false, true},

		// Stage 2: Read-only allowed
		{"cat /var/log/syslog", false, false},
		{"ls -la /home", false, false},
		{"ps aux", false, false},
		{"docker ps", false, false},
		{"kubectl get pods", false, false},

		// Stage 3: Allow list permits
		{"curl https://api.example.com", false, false},
		{"wget http://example.com/file", false, false},
		{"scp file user@host:/path", false, false},

		// Stage 4: Destructive check
		{"systemctl restart nginx", false, true}, // Not in read-only, not in allow, destructive
		{"systemctl restart nginx", true, false}, // Write enabled allows it
		{"chmod 777 /tmp/file", false, true},     // Not in read-only, not in allow, destructive
		{"chmod 777 /tmp/file", true, false},     // Write enabled allows it
	}

	for _, tt := range tests {
		err := v.ValidateCommand(tt.cmd, tt.write)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateCommand(%q, %v) error = %v, wantErr %v", tt.cmd, tt.write, err, tt.wantErr)
		}
	}
}

// ============================================================================
// Sudo Tests
// ============================================================================

func TestSudo_RecursiveValidation(t *testing.T) {
	v := NewCommandValidator()

	// sudo cat /etc/passwd should be allowed (cat is read-only)
	err := v.ValidateCommand("sudo cat /etc/passwd", false)
	if err != nil {
		t.Errorf("Expected 'sudo cat /etc/passwd' to be allowed, got error: %v", err)
	}

	// sudo rm -rf / should be blocked (rm is not read-only)
	err = v.ValidateCommand("sudo rm -rf /", false)
	if err == nil {
		t.Error("Expected 'sudo rm -rf /' to be blocked")
	}
}

func TestSudo_DenyList(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"rm *"}, nil)

	// sudo rm should be blocked by deny list (recursive validation)
	err := v.ValidateCommand("sudo rm -rf /", false)
	if err == nil {
		t.Error("Expected 'sudo rm -rf /' to be blocked by deny list")
	}
}

// ============================================================================
// Subcommand Restriction Tests
// ============================================================================

func TestSubcommands_Docker(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"docker ps",
		"docker images",
		"docker logs container",
		"docker inspect container",
		"docker stats",
	}
	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Docker command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	blocked := []string{
		"docker rm container",
		"docker stop container",
		"docker run ubuntu",
	}
	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Docker command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestSubcommands_Kubectl(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"kubectl get pods",
		"kubectl describe pod my-pod",
		"kubectl logs my-pod",
	}
	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Kubectl command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	blocked := []string{
		"kubectl delete pod my-pod",
		"kubectl apply -f manifest.yaml",
		"kubectl exec my-pod -- ls",
	}
	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Kubectl command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestSubcommands_Systemctl(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"systemctl status nginx",
		"systemctl is-active nginx",
		"systemctl list-units",
	}
	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Systemctl command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	blocked := []string{
		"systemctl start nginx",
		"systemctl stop nginx",
		"systemctl restart nginx",
	}
	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Systemctl command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

// ============================================================================
// Pipe/Separator Tests
// ============================================================================

func TestPipes_ReadOnly(t *testing.T) {
	v := NewCommandValidator()

	// Pipe with all read-only commands should pass
	err := v.ValidateCommand("cat /var/log/syslog | grep error", false)
	if err != nil {
		t.Errorf("Expected 'cat ... | grep ...' to be allowed, got error: %v", err)
	}

	// Pipe with dangerous command should fail
	err = v.ValidateCommand("cat /var/log/syslog | rm -f /tmp/out", false)
	if err == nil {
		t.Error("Expected pipe with 'rm' to be blocked")
	}
}

func TestPipes_DenyList(t *testing.T) {
	v := NewCommandValidatorWithPatterns([]string{"rm *"}, nil)

	// Pipe with denied command should fail
	err := v.ValidateCommand("cat /var/log/syslog | rm -f /tmp/out", false)
	if err == nil {
		t.Error("Expected pipe with 'rm' to be blocked by deny list")
	}
}

// ============================================================================
// Write Redirect Tests
// ============================================================================

func TestWriteRedirect_Blocked(t *testing.T) {
	v := NewCommandValidator()

	blocked := []string{
		"echo test > /tmp/file",
		"cat /etc/passwd >> /tmp/out",
		"ls -la > /tmp/list.txt",
	}
	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Command '%s' should be blocked (write redirect)", cmd)
		}
	}
}

// ============================================================================
// Error Message Tests
// ============================================================================

func TestErrorMessage_ContainsHelp(t *testing.T) {
	v := NewCommandValidator()

	err := v.ValidateCommand("rm -rf /", false)
	if err == nil {
		t.Fatal("Expected error for dangerous command")
	}

	errorMsg := err.Error()

	expectedPhrases := []string{
		"command blocked:",
		"read-only mode",
		"File viewing: cat, head, tail",
		"Directory: ls, pwd, tree",
		"Allow Write Commands",
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(errorMsg, phrase) {
			t.Errorf("Error message should contain '%s', got: %s", phrase, errorMsg)
		}
	}
}

// ============================================================================
// Edge Cases
// ============================================================================

func TestEmptyCommand(t *testing.T) {
	v := NewCommandValidator()

	err := v.ValidateCommand("", false)
	if err != nil {
		t.Errorf("Expected empty command to pass, got error: %v", err)
	}

	err = v.ValidateCommand("   ", false)
	if err != nil {
		t.Errorf("Expected whitespace command to pass, got error: %v", err)
	}
}

func TestPathPrefixes(t *testing.T) {
	v := NewCommandValidator()

	// /usr/bin/cat should be recognized as "cat"
	err := v.ValidateCommand("/usr/bin/cat /etc/passwd", false)
	if err != nil {
		t.Errorf("Expected '/usr/bin/cat' to be recognized as 'cat', got error: %v", err)
	}
}

func TestEnvironmentVariablePrefix(t *testing.T) {
	v := NewCommandValidator()

	// LANG=C cat /etc/passwd should work (skip env var prefix)
	err := v.ValidateCommand("LANG=C cat /etc/passwd", false)
	if err != nil {
		t.Errorf("Expected 'LANG=C cat ...' to work, got error: %v", err)
	}
}
