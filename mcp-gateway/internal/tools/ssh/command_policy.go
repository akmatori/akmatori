package ssh

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandPolicy is the operator's command policy for one SSH tool instance:
// patterns that are allowed in read-only mode, and patterns that are always
// denied. It applies to every host of the instance, ad-hoc hosts included.
//
// Pattern syntax: words separated by spaces, matched against the words of
// one simple command (the parts of a chain split on ; | && ||):
//   - a word without "*" matches the same word
//   - "*" inside a word matches any characters in that word ("nginx*")
//   - a lone "*" matches exactly one word, except as the last word, where it
//     matches any number of remaining words, also none
//
// Allow patterns must match the whole command: "systemctl restart nginx"
// allows exactly that command, "systemctl restart *" allows any arguments.
// Deny patterns match the start of the command after env assignments, sudo
// and the path of the program are removed: "reboot" denies "sudo /sbin/reboot
// now". A deny is a guard rail, not a sandbox: with write commands allowed, a
// shell can always hide a command from it (for example "sh -c").
type CommandPolicy struct {
	Allow []string
	Deny  []string

	allow []commandPattern
	deny  []commandPattern
}

type commandPattern struct {
	raw   string
	words []*regexp.Regexp // nil entry = lone "*"
	rest  bool             // last word is a lone "*"
}

// NewCommandPolicy compiles allow and deny pattern lists. Empty lines and
// lines that start with "#" are skipped.
func NewCommandPolicy(allow, deny []string) *CommandPolicy {
	p := &CommandPolicy{}
	for _, raw := range allow {
		if cp, ok := compileCommandPattern(raw); ok {
			p.Allow = append(p.Allow, cp.raw)
			p.allow = append(p.allow, cp)
		}
	}
	for _, raw := range deny {
		if cp, ok := compileCommandPattern(raw); ok {
			p.Deny = append(p.Deny, cp.raw)
			p.deny = append(p.deny, cp)
		}
	}
	return p
}

// ParseCommandPatterns reads a pattern list setting: a string with one
// pattern per line (the UI's textarea), or a JSON array of strings.
func ParseCommandPatterns(v interface{}) []string {
	var lines []string
	switch t := v.(type) {
	case string:
		lines = strings.Split(t, "\n")
	case []interface{}:
		for _, item := range t {
			if s, ok := item.(string); ok {
				lines = append(lines, s)
			}
		}
	case []string:
		lines = t
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func compileCommandPattern(raw string) (commandPattern, bool) {
	words := strings.Fields(raw)
	if len(words) == 0 || strings.HasPrefix(words[0], "#") {
		return commandPattern{}, false
	}
	cp := commandPattern{raw: strings.Join(words, " ")}
	if words[len(words)-1] == "*" {
		cp.rest = true
		words = words[:len(words)-1]
	}
	for _, w := range words {
		if w == "*" {
			cp.words = append(cp.words, nil)
			continue
		}
		parts := strings.Split(w, "*")
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		cp.words = append(cp.words, regexp.MustCompile("^"+strings.Join(parts, ".*")+"$"))
	}
	return cp, true
}

// matches reports whether the pattern matches words: the whole list, or only
// its start when prefix is set.
func (cp commandPattern) matches(words []string, prefix bool) bool {
	if len(words) < len(cp.words) {
		return false
	}
	for i, re := range cp.words {
		if re != nil && !re.MatchString(words[i]) {
			return false
		}
	}
	return prefix || cp.rest || len(words) == len(cp.words)
}

// HasAllow reports whether the policy allows any command beyond read-only.
func (p *CommandPolicy) HasAllow() bool { return p != nil && len(p.allow) > 0 }

// HasDeny reports whether the policy denies any command.
func (p *CommandPolicy) HasDeny() bool { return p != nil && len(p.deny) > 0 }

// allowMetachars are characters an allowed command may not hold: they could
// run another command (substitution, background, redirect, subshell) next to
// the allowed one.
const allowMetachars = "$`<>&(){}\\\n\r"

// allowed reports whether one simple command matches an allow pattern.
func (p *CommandPolicy) allowed(part string) bool {
	if !p.HasAllow() || strings.ContainsAny(part, allowMetachars) {
		return false
	}
	words := strings.Fields(part)
	if len(words) == 0 {
		return false
	}
	for _, cp := range p.allow {
		if cp.matches(words, false) {
			return true
		}
	}
	return false
}

var (
	envAssignPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	substitutionPattern = regexp.MustCompile("\\$\\(([^()]*)\\)|`([^`]*)`")
)

// denyWords normalizes one simple command for deny matching: it drops env
// assignments, sudo with its flags, and the path of the program.
func denyWords(part string) []string {
	words := strings.Fields(part)
	for {
		for len(words) > 0 && envAssignPattern.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			return nil
		}
		if i := strings.LastIndex(words[0], "/"); i >= 0 && i < len(words[0])-1 {
			words[0] = words[0][i+1:]
		}
		if words[0] != "sudo" {
			return words
		}
		inner := extractCommandAfterSudo(strings.Join(words, " "))
		words = strings.Fields(inner)
	}
}

// denied returns the deny pattern that matches the command, or "". It checks
// every simple command of the chain and of $(...) and `...` substitutions.
func (p *CommandPolicy) denied(command string) string {
	if !p.HasDeny() {
		return ""
	}
	pending := []string{command}
	for len(pending) > 0 {
		cmd := pending[0]
		pending = pending[1:]
		for _, m := range substitutionPattern.FindAllStringSubmatch(cmd, -1) {
			pending = append(pending, m[1]+m[2])
		}
		for _, part := range splitCommandChain(cmd) {
			part = strings.Trim(part, "$()` \t")
			words := denyWords(part)
			if len(words) == 0 {
				continue
			}
			for _, cp := range p.deny {
				if cp.matches(words, true) {
					return cp.raw
				}
			}
		}
	}
	return ""
}

var chainSeparator = regexp.MustCompile(`&&|\|\||[;|&\n]`)

// splitCommandChain splits a command on ; | && || & and new lines.
func splitCommandChain(cmd string) []string {
	var out []string
	for _, part := range chainSeparator.Split(cmd, -1) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ValidateCommandWithPolicy checks a command against the operator policy and
// read-only mode:
//  1. a command that matches a deny pattern is blocked, also when write
//     commands are allowed
//  2. with write commands allowed, everything else runs
//  3. in read-only mode each simple command must match an allow pattern or
//     pass the read-only rules
//
// Without allow patterns, step 3 is ValidateCommand unchanged.
func (v *CommandValidator) ValidateCommandWithPolicy(command string, allowWriteCommands bool, policy *CommandPolicy) error {
	if pattern := policy.denied(command); pattern != "" {
		return fmt.Errorf("command blocked: matches the denied pattern %q set by the operator for this SSH tool", pattern)
	}
	if allowWriteCommands || !policy.HasAllow() {
		err := v.ValidateCommand(command, allowWriteCommands)
		return policy.withAllowHint(err)
	}

	cmd := strings.TrimSpace(command)
	parts := splitCommandChain(cmd)
	allAllowed := true
	for _, part := range parts {
		if !policy.allowed(part) {
			allAllowed = false
			break
		}
	}
	if allAllowed {
		return nil
	}
	// Check the parts that no allow pattern covers with the read-only rules.
	for _, part := range parts {
		if policy.allowed(part) {
			continue
		}
		if err := v.ValidateCommand(part, false); err != nil {
			return policy.withAllowHint(err)
		}
	}
	return nil
}

// withAllowHint appends the operator's allow patterns to a blocked error.
func (p *CommandPolicy) withAllowHint(err error) error {
	if err == nil || !p.HasAllow() {
		return err
	}
	return fmt.Errorf("%w\n\nAlso allowed on this SSH tool (operator patterns; no $, `, <, >, &, (, ), {, }, \\ in such commands):\n  %s",
		err, strings.Join(p.Allow, "\n  "))
}
