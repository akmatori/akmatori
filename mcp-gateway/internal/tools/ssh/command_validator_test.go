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

	if len(v.DangerousPatterns) == 0 {
		t.Error("DangerousPatterns should not be empty")
	}

	if len(v.AllowedSubcommands) == 0 {
		t.Error("AllowedSubcommands should not be empty")
	}
}

func TestCommandValidator_AllowWriteCommands(t *testing.T) {
	v := NewCommandValidator()

	// When allowWriteCommands is true, all commands should pass
	dangerousCommands := []string{
		"rm -rf /",
		"shutdown -h now",
		"kill -9 1234",
		"sudo reboot",
	}

	for _, cmd := range dangerousCommands {
		err := v.ValidateCommand(cmd, true)
		if err != nil {
			t.Errorf("Command '%s' should be allowed when allowWriteCommands=true, got error: %v", cmd, err)
		}
	}
}

func TestCommandValidator_ReadOnlyAllowed(t *testing.T) {
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
		"fzf --version",
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

func TestCommandValidator_ReadOnlyBlocked(t *testing.T) {
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
		"sudo apt update",
		"su - root",
		"echo test > /tmp/file",
		"cat /etc/passwd >> /tmp/out",
	}

	for _, cmd := range blockedCommands {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestCommandValidator_ErrorContainsAllowedCommands(t *testing.T) {
	v := NewCommandValidator()

	err := v.ValidateCommand("rm -rf /", false)
	if err == nil {
		t.Fatal("Expected error for dangerous command")
	}

	errorMsg := err.Error()

	// Check that the error message contains the allowed commands list
	expectedPhrases := []string{
		"Command blocked:",
		"Allowed commands in read-only mode:",
		"File viewing: cat, head, tail",
		"Directory: ls, pwd, tree",
		"To allow write commands, enable 'Allow Write Commands'",
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(errorMsg, phrase) {
			t.Errorf("Error message should contain '%s', got: %s", phrase, errorMsg)
		}
	}
}

func TestCommandValidator_DockerSubcommands(t *testing.T) {
	v := NewCommandValidator()

	// Allowed docker subcommands
	allowed := []string{
		"docker ps",
		"docker ps -a",
		"docker images",
		"docker logs container_name",
		"docker inspect container_name",
		"docker stats",
		"docker top container_name",
		"docker info",
		"docker version",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Docker command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked docker subcommands
	blocked := []string{
		"docker rm container_name",
		"docker rmi image_name",
		"docker stop container_name",
		"docker kill container_name",
		"docker exec container_name ls",
		"docker run ubuntu",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Docker command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestCommandValidator_KubectlSubcommands(t *testing.T) {
	v := NewCommandValidator()

	// Allowed kubectl subcommands
	allowed := []string{
		"kubectl get pods",
		"kubectl get pods -n kube-system",
		"kubectl describe pod my-pod",
		"kubectl logs my-pod",
		"kubectl top pods",
		"kubectl version",
		"kubectl cluster-info",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Kubectl command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked kubectl subcommands
	blocked := []string{
		"kubectl delete pod my-pod",
		"kubectl apply -f manifest.yaml",
		"kubectl create deployment nginx",
		"kubectl exec my-pod -- ls",
		"kubectl edit deployment nginx",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Kubectl command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestCommandValidator_SystemctlSubcommands(t *testing.T) {
	v := NewCommandValidator()

	// Allowed systemctl subcommands
	allowed := []string{
		"systemctl status nginx",
		"systemctl is-active nginx",
		"systemctl is-enabled nginx",
		"systemctl list-units",
		"systemctl list-unit-files",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Systemctl command '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked systemctl subcommands
	blocked := []string{
		"systemctl start nginx",
		"systemctl stop nginx",
		"systemctl restart nginx",
		"systemctl enable nginx",
		"systemctl disable nginx",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Systemctl command '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestCommandValidator_PipeChains(t *testing.T) {
	v := NewCommandValidator()

	// Allowed pipe chains
	allowed := []string{
		"cat /var/log/syslog | grep error",
		"ps aux | grep nginx | wc -l",
		"ls -la | head -n 10",
		"df -h | grep /dev",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Pipe chain '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked pipe chains (contains dangerous command)
	blocked := []string{
		"cat /etc/passwd | rm /tmp/file",
		"ls | sudo cat /etc/shadow",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Pipe chain '%s' should be blocked in read-only mode", cmd)
		}
	}
}

func TestCommandValidator_UnknownCommand(t *testing.T) {
	v := NewCommandValidator()

	unknownCommands := []string{
		"customtool --help",
		"mytool run",
		"unknownbinary",
	}

	for _, cmd := range unknownCommands {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Unknown command '%s' should be blocked in read-only mode", cmd)
		}
		if !strings.Contains(err.Error(), "not in the allowed command list") {
			t.Errorf("Error for unknown command should mention 'not in the allowed command list', got: %v", err)
		}
	}
}

func TestExtractBaseCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"cat /etc/passwd", "cat"},
		{"/usr/bin/cat /etc/passwd", "cat"},
		{"ls -la", "ls"},
		{"  grep error", "grep"},
		{"$(whoami)", "whoami"},
		{"`hostname`", "hostname"},
		{"", ""},
		// Inline environment variables
		{"LANG=C ps aux", "ps"},
		{"LC_ALL=C LANG=C df -h", "df"},
		{"FOO=bar BAZ=qux echo hello", "echo"},
		{"TZ=UTC date", "date"},
		// Edge cases
		{"VAR=value", ""}, // Only env var, no command
	}

	for _, test := range tests {
		result := extractBaseCommand(test.input)
		if result != test.expected {
			t.Errorf("extractBaseCommand(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestCommandValidator_InlineEnvVars(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"LANG=C ps aux",
		"LC_ALL=C df -h",
		"TZ=UTC date",
		"LANG=C LC_ALL=C free -m",
		"FOO=bar echo hello",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Command with env var '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked - dangerous command with env var prefix
	blocked := []string{
		"LANG=C rm -rf /tmp",
		"FOO=bar sudo reboot",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Command '%s' should be blocked", cmd)
		}
	}
}

func TestCommandValidator_EmptyCommand(t *testing.T) {
	v := NewCommandValidator()

	err := v.ValidateCommand("", false)
	if err != nil {
		t.Errorf("Empty command should not error, got: %v", err)
	}

	err = v.ValidateCommand("   ", false)
	if err != nil {
		t.Errorf("Whitespace-only command should not error, got: %v", err)
	}
}

func TestCommandValidator_SemicolonChains(t *testing.T) {
	v := NewCommandValidator()

	// Allowed semicolon chains
	allowed := []string{
		"uptime; echo '---'; mpstat 1 3",
		"ps aux; df -h; free -m",
		"uptime; echo '---'; ps -eo pid,comm,pcpu,pmem | head -n 15",
		"vmstat 1 5; iostat -x 1 3",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Semicolon chain '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	// Blocked semicolon chains
	blocked := []string{
		"uptime; rm -rf /tmp",
		"ls; sudo reboot",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Semicolon chain '%s' should be blocked", cmd)
		}
	}
}

func TestCommandValidator_AndOrChains(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"test -f /etc/hosts && cat /etc/hosts",
		"grep error /var/log/syslog || echo 'no errors'",
		"ls /tmp && df -h",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Chain '%s' should be allowed, got error: %v", cmd, err)
		}
	}

	blocked := []string{
		"test -f /etc/hosts && rm /etc/hosts",
		"ls || sudo reboot",
	}

	for _, cmd := range blocked {
		err := v.ValidateCommand(cmd, false)
		if err == nil {
			t.Errorf("Chain '%s' should be blocked", cmd)
		}
	}
}

func TestCommandValidator_MonitoringCommands(t *testing.T) {
	v := NewCommandValidator()

	allowed := []string{
		"mpstat 1 3",
		"vmstat 1 5",
		"iostat -x 1 3",
		"sar -u 1 3",
		"pidstat 1 3",
		"nmon -f -s 1 -c 3",
		"iotop -b -n 1",
	}

	for _, cmd := range allowed {
		err := v.ValidateCommand(cmd, false)
		if err != nil {
			t.Errorf("Monitoring command '%s' should be allowed, got error: %v", cmd, err)
		}
	}
}

func TestCommandValidator_ComplexRealWorldCommand(t *testing.T) {
	v := NewCommandValidator()

	// Test the exact command from the user's example
	cmd := "uptime; echo '---'; mpstat 1 3; echo '---'; ps -eo pid,comm,pcpu,pmem,etime,user --sort=-pcpu | head -n 15"
	err := v.ValidateCommand(cmd, false)
	if err != nil {
		t.Errorf("Real-world command '%s' should be allowed, got error: %v", cmd, err)
	}
}
