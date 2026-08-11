package ssh

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CommandValidator validates SSH commands using a 4-stage pipeline:
// 1. Global deny list (Claude Code wildcard syntax)
// 2. Default read-only list
// 3. Global custom allowlist (Claude Code wildcard syntax)
// 4. Destructive commands check (gate by allowWriteCommands)
type CommandValidator struct {
	// ReadOnlyCommands are explicitly allowed in read-only mode
	ReadOnlyCommands map[string]bool

	// AllowedSubcommands defines safe subcommands for specific base commands
	AllowedSubcommands map[string][]string

	// GlobalDenyPatterns are Claude Code wildcard patterns that ALWAYS block commands
	// These are configured at the tool level and apply to all hosts
	GlobalDenyPatterns []string
	compiledDenyRegex  []*regexp.Regexp

	// GlobalAllowPatterns are Claude Code wildcard patterns that explicitly allow commands
	// These are configured at the tool level and apply to all hosts
	GlobalAllowPatterns []string
	compiledAllowRegex  []*regexp.Regexp
}

// NewCommandValidator creates a validator with default safe commands and no custom patterns.
func NewCommandValidator() *CommandValidator {
	return &CommandValidator{
		ReadOnlyCommands:   readOnlyCommandsSet(),
		AllowedSubcommands: allowedSubcommandsMap(),
	}
}

// NewCommandValidatorWithPatterns creates a validator with global deny/allow patterns.
// Patterns use Claude Code wildcard syntax:
//   - "rm *" matches "rm -rf /" but not "rmdir"
//   - "shutdown" matches "shutdown" exactly (no args)
//   - "shutdown *" matches "shutdown -h now"
//   - "* --version" matches "docker --version"
//   - "git push *" matches "git push origin main"
func NewCommandValidatorWithPatterns(denyPatterns, allowPatterns []string) *CommandValidator {
	v := &CommandValidator{
		ReadOnlyCommands:    readOnlyCommandsSet(),
		AllowedSubcommands:  allowedSubcommandsMap(),
		GlobalDenyPatterns:  denyPatterns,
		GlobalAllowPatterns: allowPatterns,
		compiledDenyRegex:   compilePatterns(denyPatterns),
		compiledAllowRegex:  compilePatterns(allowPatterns),
	}
	return v
}

// readOnlyCommandsSet returns the default read-only command set.
func readOnlyCommandsSet() map[string]bool {
	return map[string]bool{
		// File viewing
		"cat": true, "head": true, "tail": true, "less": true, "more": true,
		// Search and find
		"grep": true, "rg": true, "fzf": true, "find": true, "locate": true, "which": true, "type": true,
		// Directory listing
		"ls": true, "pwd": true, "tree": true,
		// System info
		"whoami": true, "uname": true, "hostname": true, "date": true, "id": true,
		"uptime": true, "w": true, "who": true, "last": true,
		"nproc": true, "lscpu": true, "getconf": true,
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
		// Sudo - allows running read-only commands with elevated privileges
		"sudo": true,
	}
}

// allowedSubcommandsMap returns the allowed subcommands for specific base commands.
func allowedSubcommandsMap() map[string][]string {
	return map[string][]string{
		"docker":    {"ps", "images", "logs", "inspect", "stats", "top", "info", "version", "network ls", "volume ls"},
		"kubectl":   {"get", "describe", "logs", "top", "version", "config view", "cluster-info"},
		"systemctl": {"status", "is-active", "is-enabled", "list-units", "list-unit-files", "show"},
		"apt":       {"list", "show", "search", "policy"},
		"yum":       {"list", "info", "search"},
		"dpkg":      {"-l", "-L", "-s", "--list", "--listfiles", "--status"},
		"rpm":       {"-qa", "-qi", "-ql", "--query"},
	}
}

// GetReadOnlyCommands returns a copy of the default read-only command set as a sorted slice.
func GetReadOnlyCommands() []string {
	cmds := readOnlyCommandsSet()
	result := make([]string, 0, len(cmds))
	for cmd := range cmds {
		result = append(result, cmd)
	}
	sort.Strings(result)
	return result
}

// GetSubcommandRestrictions returns a copy of the allowed subcommands map.
func GetSubcommandRestrictions() map[string][]string {
	subs := allowedSubcommandsMap()
	result := make(map[string][]string, len(subs))
	for k, v := range subs {
		allowed := make([]string, len(v))
		copy(allowed, v)
		result[k] = allowed
	}
	return result
}

// compilePatterns converts Claude Code wildcard patterns to compiled regexes.
// Pattern rules:
//   - "*" at end (with space before): matches any trailing args → \s+.+
//   - "*:*" at end: equivalent to "* " → \s+.+
//   - "*" in middle or start: matches any non-whitespace sequence → [^\s]+
//   - No "*": exact base command match → command followed by end-of-string or whitespace
//   - Special characters are escaped except "*"
func compilePatterns(patterns []string) []*regexp.Regexp {
	var regexes []*regexp.Regexp
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re := wildcardToRegex(p)
		if re != nil {
			regexes = append(regexes, re)
		}
	}
	return regexes
}

// wildcardToRegex converts a Claude Code wildcard pattern to a *regexp.Regexp.
func wildcardToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		switch ch {
		case '*':
			// Check for ":*" suffix (equivalent to " *")
			if i+1 < len(pattern) && pattern[i+1] == ':' {
				// "cmd:*" → "cmd *" equivalent
				b.WriteString(`\s+.+`)
				i += 2
				continue
			}
			// Check if "*" is at the end of pattern
			if i == len(pattern)-1 {
				// Trailing "*": matches any args
				// Check if previous char is space → \s+.+
				// Check if previous char is not space → [^\s]+
				if i > 0 && pattern[i-1] == ' ' {
					// "cmd *" → cmd\s+.+
					b.WriteString(`.+`)
				} else {
					// "cmd*" → cmd[^\s]+
					b.WriteString(`[^\s]+`)
				}
			} else if i > 0 && pattern[i-1] == ' ' {
				// "cmd * rest" → cmd\s+.+
				b.WriteString(`\s+.+`)
			} else {
				// "* cmd" or "cmd*rest" → [^\s]+
				b.WriteString(`[^\s]+`)
			}
		case '.':
			b.WriteString(`\.`)
		case '^':
			b.WriteString(`\^`)
		case '$':
			b.WriteString(`\$`)
		case '+':
			b.WriteString(`\+`)
		case '?':
			b.WriteString(`\?`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		case '[':
			b.WriteString(`\[`)
		case ']':
			b.WriteString(`\]`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		case '|':
			b.WriteString(`\|`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(ch)
		}
		i++
	}

	// Build final regex: anchored to start, with flexible end
	pattern = strings.TrimSpace(pattern)
	if !strings.Contains(pattern, "*") {
		// Exact match: no wildcards
		return regexp.MustCompile(`^` + b.String() + `(\s+)?$`)
	}
	if strings.HasSuffix(pattern, "*") && pattern[len(pattern)-2] == ' ' {
		// Trailing " *": command with args (args required)
		return regexp.MustCompile(`^` + b.String() + `$`)
	}
	if strings.HasSuffix(pattern, "*") && pattern[len(pattern)-2] != ' ' {
		// Trailing "*": command with optional args (cmd* without space)
		return regexp.MustCompile(`^` + b.String() + `.*$`)
	}
	if strings.HasPrefix(pattern, "*") {
		// Leading "*": prefix match
		return regexp.MustCompile(`^` + b.String() + `$`)
	}
	// General case
	return regexp.MustCompile(`^` + b.String() + `$`)
}

// MatchesDenyList checks if the command matches any deny pattern.
// Returns true if the command should be blocked.
func (v *CommandValidator) MatchesDenyList(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	for _, re := range v.compiledDenyRegex {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// MatchesAllowList checks if the command matches any allow pattern.
// Returns true if the command is explicitly allowed.
func (v *CommandValidator) MatchesAllowList(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	for _, re := range v.compiledAllowRegex {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// ValidateCommand executes the 4-stage validation pipeline:
// 1. Global deny list → reject if matched
// 2. Default read-only list → allow if matched
// 3. Global custom allowlist → allow if matched
// 4. Destructive check → allow only if allowWriteCommands is true
func (v *CommandValidator) ValidateCommand(command string, allowWriteCommands bool) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}

	// Split on command separators: ; && || |
	separatorPattern := regexp.MustCompile(`[;|]|&&|\|\|`)
	parts := separatorPattern.Split(cmd, -1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if err := v.validateSingleCommand(part, allowWriteCommands); err != nil {
			return err
		}
	}
	return nil
}

// validateSingleCommand runs the 4-stage pipeline for a single command.
func (v *CommandValidator) validateSingleCommand(cmd string, allowWriteCommands bool) error {
	// Stage 1: Global deny list (always active)
	if v.MatchesDenyList(cmd) {
		return v.blockedError("command blocked by global deny list")
	}

	// Check for dangerous output redirects (> but not 2> or >&)
	if containsWriteRedirect(cmd) {
		return v.blockedError("contains file output redirect '>'")
	}

	// Extract base command
	baseCmd := extractBaseCommand(cmd)
	if baseCmd == "" {
		return nil
	}

	// Special handling for sudo - recursively validate the inner command
	if baseCmd == "sudo" {
		innerCmd := extractCommandAfterSudo(cmd)
		if innerCmd == "" {
			return v.blockedError("sudo requires a command")
		}
		return v.validateSingleCommand(innerCmd, allowWriteCommands)
	}

	// Stage 2: Default read-only list
	if v.ReadOnlyCommands[baseCmd] {
		// Check subcommand restrictions (only when write is NOT enabled)
		if !allowWriteCommands {
			if allowedSubs, hasRestrictions := v.AllowedSubcommands[baseCmd]; hasRestrictions {
				if !v.isSubcommandAllowed(cmd, baseCmd, allowedSubs) {
					return v.blockedError(fmt.Sprintf("'%s' subcommand is not allowed", baseCmd))
				}
			}
		}
		return nil // Allowed by read-only list
	}

	// Stage 3: Global custom allowlist
	if v.MatchesAllowList(cmd) {
		return nil // Explicitly allowed
	}

	// Stage 4: Destructive check
	// If allowWriteCommands is true, allow commands not in read-only list
	// If false, reject (command not in read-only, not in allowlist)
	if allowWriteCommands {
		// Allow destructive commands
		return nil
	}

	// Command not allowed by any rule
	return v.blockedError(fmt.Sprintf("'%s' is not in the allowed command list", baseCmd))
}

// isSubcommandAllowed checks if a command's subcommand is in the allowed list.
func (v *CommandValidator) isSubcommandAllowed(fullCmd, baseCmd string, allowedSubs []string) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(fullCmd, baseCmd))
	for _, sub := range allowedSubs {
		if strings.HasPrefix(rest, sub) {
			return true
		}
	}
	return false
}

// blockedError creates a detailed error message with allowed commands.
func (v *CommandValidator) blockedError(reason string) error {
	return fmt.Errorf(`command blocked: %s (read-only mode is enabled)

Allowed commands in read-only mode:
  File viewing: cat, head, tail, less, more
  Search: grep, rg, fzf, find, locate, which
  Directory: ls, pwd, tree
  System info: whoami, uname, hostname, date, id, uptime, nproc, lscpu, getconf
  Processes: ps, top, htop, pgrep, pstree
  Performance: mpstat, sar, iostat, vmstat, pidstat, nmon, iotop
  Resources: df, du, free, lsblk
  Network: netstat, ss, ip, ping, dig, traceroute
  Text processing: wc, sort, uniq, cut, awk, sed, tr
  File info: stat, file, md5sum, sha256sum
  Logs: journalctl, dmesg
  Containers: docker ps/images/logs/inspect/stats, kubectl get/describe/logs
  Package managers: apt list/show, yum list/info, dpkg -l, rpm -qa

To allow write commands, enable 'Allow Write Commands' in SSH tool settings`, reason)
}

// extractBaseCommand extracts the base command from a command string.
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

// extractCommandAfterSudo extracts the actual command from a sudo invocation.
func extractCommandAfterSudo(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 || parts[0] != "sudo" {
		return ""
	}

	// Skip "sudo" and any flags
	i := 1
	for i < len(parts) {
		part := parts[i]
		if !strings.HasPrefix(part, "-") {
			break
		}
		flagsWithArgs := map[string]bool{
			"-u": true, "-g": true, "-C": true, "-h": true,
			"-p": true, "-r": true, "-t": true, "-U": true,
		}
		if flagsWithArgs[part] {
			i += 2
		} else {
			i++
		}
	}

	if i >= len(parts) {
		return ""
	}

	return strings.Join(parts[i:], " ")
}

// containsWriteRedirect checks for output redirects.
func containsWriteRedirect(cmd string) bool {
	patterns := []string{
		`[^2]>\s*[^&]`,
		`^>\s*`,
		`>>\s*`,
	}
	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, cmd); matched {
			return true
		}
	}
	return false
}
