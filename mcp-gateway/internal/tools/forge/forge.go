// Package forge holds the parts of the pull request review tools (gitea.*,
// github.*) that do not depend on the forge's API: argument parsing, the
// repository allowlist, byte budgets, the per-file diff split and the text
// layout of diffs and files. Both tools give the agent the same output shapes.
package forge

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/akmatori/mcp-gateway/internal/validation"
)

const (
	DefaultMaxBytes = 50_000
	MaxMaxBytes     = 200_000

	// MaxRawResponseBytes caps a raw diff or file read from the forge; the
	// agent still only gets max_bytes of it.
	MaxRawResponseBytes = 20 * 1024 * 1024
	// MaxJSONResponseBytes caps a JSON API response.
	MaxJSONResponseBytes = 5 * 1024 * 1024

	MaxCommentBodyBytes = 4_000
	MaxPRBodyBytes      = 20_000
	MaxReviews          = 30
	MaxComments         = 100
	MaxCommentLength    = 60_000
	MaxDiffHunkBytes    = 1_500
)

// ParseAllowedRepos reads an allowed-repos setting: a string with one entry
// per line (or separated by commas or spaces; "#" starts a comment), or a JSON
// array of strings. Entries are lowercased.
func ParseAllowedRepos(v interface{}) []string {
	var raw []string
	switch val := v.(type) {
	case string:
		for _, line := range strings.Split(val, "\n") {
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			raw = append(raw, strings.FieldsFunc(line, func(r rune) bool {
				return r == ',' || r == ' ' || r == '\t' || r == '\r'
			})...)
		}
	case []interface{}:
		for _, item := range val {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	case []string:
		raw = val
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.ToLower(strings.Trim(strings.TrimSpace(s), "/"))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// RepoAllowed applies an allowlist of "owner/repo", "owner/*" or "*" entries.
// An empty list allows every repository.
func RepoAllowed(allowed []string, owner, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	full := strings.ToLower(owner + "/" + name)
	ownerAll := strings.ToLower(owner) + "/*"
	for _, a := range allowed {
		if a == full || a == ownerAll || a == "*" {
			return true
		}
	}
	return false
}

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// SplitRepo validates "owner/repo" and drops a ".git" suffix.
func SplitRepo(repo string) (owner, name string, err error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !repoPartRe.MatchString(parts[0]) || !repoPartRe.MatchString(parts[1]) ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", "", fmt.Errorf("repo must be \"owner/repo\", got %q", repo)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

// PR is the repository and pull request number a call is about.
type PR struct {
	Owner, Name string
	Number      int
}

// Repo returns "owner/name".
func (p PR) Repo() string { return p.Owner + "/" + p.Name }

var prURLPathRe = regexp.MustCompile(`^/([^/]+)/([^/]+)/(?:pulls|pull)/(\d+)(?:/.*)?$`)

// ParsePRURL reads <web>/<owner>/<repo>/pull(s)/<n>[/...]. The host must be
// webBase's host, so a link can never point the tool at another server
// (requests always go to the configured API URL anyway). A path prefix in
// webBase (a forge served under a sub-path) is removed first. forgeName is
// used in error messages.
func ParsePRURL(webBase, raw, forgeName string) (PR, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return PR{}, fmt.Errorf("pr_url is not a URL: %q", raw)
	}
	base, err := url.Parse(webBase)
	if err == nil && base.Host != "" && !strings.EqualFold(base.Host, u.Host) {
		return PR{}, fmt.Errorf("pr_url host %q is not this %s instance (%s)", u.Host, forgeName, base.Host)
	}
	p := u.Path
	if base != nil && base.Path != "" && base.Path != "/" {
		p = strings.TrimPrefix(p, strings.TrimRight(base.Path, "/"))
	}
	m := prURLPathRe.FindStringSubmatch(p)
	if m == nil {
		return PR{}, fmt.Errorf("pr_url must look like https://<host>/<owner>/<repo>/pull/<number>, got %q", raw)
	}
	owner, name, err := SplitRepo(m[1] + "/" + m[2])
	if err != nil {
		return PR{}, err
	}
	n, _ := strconv.Atoi(m[3])
	return PR{Owner: owner, Name: name, Number: n}, nil
}

// IntArg reads a number argument (JSON number, int, or a string like "42" or "#42").
func IntArg(args map[string]interface{}, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case string:
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(v), "#"))
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

// StringArg reads a trimmed string argument.
func StringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// MaxBytesArg reads max_bytes (default DefaultMaxBytes, at most MaxMaxBytes).
func MaxBytesArg(args map[string]interface{}) int {
	n, ok := IntArg(args, "max_bytes")
	if !ok || n <= 0 {
		return DefaultMaxBytes
	}
	if n > MaxMaxBytes {
		return MaxMaxBytes
	}
	return n
}

// Resolver turns repo / number / pr_url arguments into a target and applies
// the repository allowlist before any request is made.
type Resolver struct {
	Allowed []string
	// Setting names the allowlist setting in errors, e.g. "gitea_allowed_repos".
	Setting string
	// ParseURL reads a pr_url for this instance.
	ParseURL func(raw string) (PR, error)
}

// Repo reads and checks the repo argument.
func (r Resolver) Repo(args map[string]interface{}) (owner, name string, err error) {
	repo := StringArg(args, "repo")
	if repo == "" {
		return "", "", fmt.Errorf("repo is required (\"owner/repo\")%s", validation.SuggestParam("repo", args))
	}
	owner, name, err = SplitRepo(repo)
	if err != nil {
		return "", "", err
	}
	if !RepoAllowed(r.Allowed, owner, name) {
		return "", "", fmt.Errorf("repo %s/%s is not in %s for this instance", owner, name, r.Setting)
	}
	return owner, name, nil
}

// PR reads repo + number, or pr_url, and checks the allowlist.
func (r Resolver) PR(args map[string]interface{}) (PR, error) {
	if raw := StringArg(args, "pr_url"); raw != "" {
		p, err := r.ParseURL(raw)
		if err != nil {
			return PR{}, err
		}
		if !RepoAllowed(r.Allowed, p.Owner, p.Name) {
			return PR{}, fmt.Errorf("repo %s is not in %s for this instance", p.Repo(), r.Setting)
		}
		return p, nil
	}
	owner, name, err := r.Repo(args)
	if err != nil {
		return PR{}, err
	}
	n, ok := IntArg(args, "number")
	if !ok || n <= 0 {
		return PR{}, fmt.Errorf("number is required (the pull request number), or pass pr_url%s", validation.SuggestParam("number", args))
	}
	return PR{Owner: owner, Name: name, Number: n}, nil
}

// FilePath validates the path argument of get_file.
func FilePath(args map[string]interface{}) (string, error) {
	path := strings.Trim(StringArg(args, "path"), "/")
	if path == "" {
		return "", fmt.Errorf("path is required%s", validation.SuggestParam("path", args))
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", fmt.Errorf("path %q must be a plain repository path", path)
		}
	}
	return path, nil
}

// EscapePath escapes each segment of a repository path.
func EscapePath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// Cut keeps at most n bytes of s on a rune boundary.
func Cut(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	// Step back over continuation bytes only (at most UTFMax-1), so text that
	// is not UTF-8 is cut at n and not emptied.
	for i := 0; i < utf8.UTFMax-1 && n > 0 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n], true
}

// Marshal renders an indented JSON result.
func Marshal(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ShortError keeps the first 300 bytes of an error body.
func ShortError(data []byte) string {
	msg := string(data)
	if len(msg) > 300 {
		msg, _ = Cut(msg, 300)
		msg += "..."
	}
	return strings.TrimSpace(msg)
}
