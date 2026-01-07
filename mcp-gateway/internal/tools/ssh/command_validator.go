package ssh

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandValidator validates SSH commands for read-only mode
type CommandValidator struct {
	// ReadOnlyCommands are explicitly allowed in read-only mode
	ReadOnlyCommands map[string]bool

	// DangerousPatterns are patterns that are always blocked when read-only
	DangerousPatterns []string

	// AllowedSubcommands defines safe subcommands for specific base commands
	AllowedSubcommands map[string][]string
}

// NewCommandValidator creates a validator with default safe commands
func NewCommandValidator() *CommandValidator {
	return &CommandValidator{
		ReadOnlyCommands: map[string]bool{
			// File viewing
			"cat": true, "head": true, "tail": true, "less": true, "more": true,
			// Search and find
			"grep": true, "find": true, "locate": true, "which": true, "type": true,
			// Directory listing
			"ls": true, "pwd": true, "tree": true,
			// System info
			"whoami": true, "uname": true, "hostname": true, "date": true, "id": true,
			"uptime": true, "w": true, "who": true, "last": true,
			// Process info
			"ps": true, "top": true, "htop": true, "pgrep": true, "pstree": true,
			// Performance monitoring
			"mpstat": true, "sar": true, "iostat": true, "vmstat": true,
			"nmon": true, "iotop": true, "pidstat": true,
			// Conditional tests
			"test": true, "[": true,
			// Memory/Disk info
			"df": true, "du": true, "free": true, "lsblk": true,
			// Network info
			"netstat": true, "ss": true, "ip": true, "ifconfig": true, "ping": true,
			"traceroute": true, "dig": true, "nslookup": true, "host": true,
			// Environment
			"env": true, "printenv": true, "echo": true,
			// Text processing (read-only operations)
			"wc": true, "sort": true, "uniq": true, "cut": true, "awk": true,
			"sed": true, "tr": true, "diff": true, "comm": true,
			// File info
			"stat": true, "file": true, "md5sum": true, "sha256sum": true,
			// Logs
			"journalctl": true, "dmesg": true,
			// Commands that need subcommand validation
			"docker": true, "kubectl": true, "systemctl": true,
			"dpkg": true, "rpm": true, "apt": true, "yum": true,
		},
		DangerousPatterns: []string{
			// Destructive file operations
			"rm ", "rm\t", "rmdir ", "shred ",
			// File modification
			"mv ", "mv\t", "cp ", "cp\t",
			"chmod ", "chown ", "chgrp ",
			// Note: Output redirects handled by containsWriteRedirect() for smarter detection
			// Process control
			"kill ", "killall ", "pkill ",
			// System control
			"shutdown", "reboot", "halt", "poweroff", "init ",
			// Disk operations
			"dd ", "mkfs", "fdisk ", "parted ", "mount ", "umount ",
			// User management (passwd with space to avoid matching /etc/passwd paths)
			"useradd", "userdel", "usermod", "passwd ", "groupadd",
			// Package modification
			"apt install", "apt remove", "apt purge", "apt-get install", "apt-get remove",
			"yum install", "yum remove", "yum erase",
			"dnf install", "dnf remove",
			"pip install", "pip uninstall",
			"npm install", "npm uninstall",
			// Service control
			"systemctl start", "systemctl stop", "systemctl restart",
			"systemctl enable", "systemctl disable",
			"service start", "service stop", "service restart",
			// Network modification
			"iptables", "firewall-cmd", "ufw ",
			// Dangerous commands
			":(){ :|:& };:", // fork bomb
			"mkfifo", "mknod",
			// Privilege escalation
			"sudo ", "su ",
			// Docker dangerous operations
			"docker rm", "docker rmi", "docker stop", "docker kill",
			"docker exec", "docker run", "docker start",
			// Kubectl dangerous operations
			"kubectl delete", "kubectl apply", "kubectl create",
			"kubectl exec", "kubectl edit", "kubectl patch",
		},
		AllowedSubcommands: map[string][]string{
			"docker":    {"ps", "images", "logs", "inspect", "stats", "top", "info", "version", "network ls", "volume ls"},
			"kubectl":   {"get", "describe", "logs", "top", "version", "config view", "cluster-info"},
			"systemctl": {"status", "is-active", "is-enabled", "list-units", "list-unit-files", "show"},
			"apt":       {"list", "show", "search", "policy"},
			"yum":       {"list", "info", "search"},
			"dpkg":      {"-l", "-L", "-s", "--list", "--listfiles", "--status"},
			"rpm":       {"-qa", "-qi", "-ql", "--query"},
		},
	}
}

// ValidateCommand checks if a command is allowed based on read-only mode
func (v *CommandValidator) ValidateCommand(command string, allowWriteCommands bool) error {
	if allowWriteCommands {
		return nil // All commands allowed
	}

	// Normalize command
	cmd := strings.TrimSpace(command)

	// Check for dangerous patterns first
	for _, pattern := range v.DangerousPatterns {
		if strings.Contains(cmd, pattern) {
			return v.blockedError(fmt.Sprintf("contains dangerous pattern '%s'", strings.TrimSpace(pattern)))
		}
	}

	// Check for dangerous output redirects (> but not 2> or >&)
	if containsWriteRedirect(cmd) {
		return v.blockedError("contains file output redirect '>'")
	}

	// Split on command separators: ; && || |
	// We need to validate each command in the chain
	separatorPattern := regexp.MustCompile(`[;|]|&&|\|\|`)
	parts := separatorPattern.Split(cmd, -1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if err := v.validateSingleCommand(part); err != nil {
			return err
		}
	}
	return nil
}

// validateSingleCommand validates a single command (without pipes)
func (v *CommandValidator) validateSingleCommand(cmd string) error {
	// Extract base command (first word)
	baseCmd := extractBaseCommand(cmd)
	if baseCmd == "" {
		return nil
	}

	// Check if base command is in allowed list
	if !v.ReadOnlyCommands[baseCmd] {
		return v.blockedError(fmt.Sprintf("'%s' is not in the allowed command list", baseCmd))
	}

	// For commands with subcommand restrictions, check subcommands
	if allowedSubs, hasRestrictions := v.AllowedSubcommands[baseCmd]; hasRestrictions {
		if !v.isSubcommandAllowed(cmd, baseCmd, allowedSubs) {
			return v.blockedError(fmt.Sprintf("'%s' subcommand is not allowed", baseCmd))
		}
	}

	return nil
}

// isSubcommandAllowed checks if a command's subcommand is in the allowed list
func (v *CommandValidator) isSubcommandAllowed(fullCmd, baseCmd string, allowedSubs []string) bool {
	// Remove the base command to get the rest
	rest := strings.TrimSpace(strings.TrimPrefix(fullCmd, baseCmd))

	for _, sub := range allowedSubs {
		if strings.HasPrefix(rest, sub) {
			return true
		}
	}
	return false
}

// blockedError creates a detailed error message with allowed commands
func (v *CommandValidator) blockedError(reason string) error {
	return fmt.Errorf(`Command blocked: %s (read-only mode is enabled).

Allowed commands in read-only mode:
  File viewing: cat, head, tail, less, more
  Search: grep, find, locate, which
  Directory: ls, pwd, tree
  System info: whoami, uname, hostname, date, id, uptime
  Processes: ps, top, htop, pgrep, pstree
  Performance: mpstat, sar, iostat, vmstat, pidstat, nmon, iotop
  Resources: df, du, free, lsblk
  Network: netstat, ss, ip, ping, dig, traceroute
  Text processing: wc, sort, uniq, cut, awk, sed, tr
  File info: stat, file, md5sum, sha256sum
  Logs: journalctl, dmesg
  Containers: docker ps/images/logs/inspect/stats, kubectl get/describe/logs

To allow write commands, enable 'Allow Write Commands' for this host.`, reason)
}

// extractBaseCommand extracts the base command from a command string
func extractBaseCommand(cmd string) string {
	// Handle command substitution
	cmd = strings.TrimPrefix(cmd, "$(")
	cmd = strings.TrimSuffix(cmd, ")")
	cmd = strings.TrimPrefix(cmd, "`")
	cmd = strings.TrimSuffix(cmd, "`")

	// Get words
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}

	// Skip inline environment variable assignments (e.g., LANG=C, FOO=bar)
	// Pattern: NAME=VALUE where NAME is uppercase letters, digits, underscores
	envVarPattern := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	for len(parts) > 0 && envVarPattern.MatchString(parts[0]) {
		parts = parts[1:]
	}

	if len(parts) == 0 {
		return ""
	}

	// Handle path prefixes (e.g., /usr/bin/cat -> cat)
	base := parts[0]
	if strings.Contains(base, "/") {
		pathParts := strings.Split(base, "/")
		base = pathParts[len(pathParts)-1]
	}

	return base
}

// containsWriteRedirect checks for output redirects
func containsWriteRedirect(cmd string) bool {
	// Check for output redirects (but not stderr redirects like 2>&1)
	// Match > or >> but not 2> or >&
	patterns := []string{
		`[^2]>\s*[^&]`, // > but not 2> or >&
		`^>\s*`,        // > at start
		`>>\s*`,        // >>
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, cmd); matched {
			return true
		}
	}
	return false
}
