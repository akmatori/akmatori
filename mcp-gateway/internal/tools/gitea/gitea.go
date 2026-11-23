// Package gitea implements the gitea.* gateway tools: read pull requests,
// their files, diffs, comments and repository files from a Gitea (or
// Forgejo) instance, for pre-merge review of infrastructure changes.
//
// Results are compact JSON shaped for an agent, not the raw API objects, and
// every potentially large text (diffs, files, comment bodies) is cut at a byte
// budget with an explicit truncation marker.
package gitea

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/validation"
)

const (
	toolType = "gitea"

	ConfigCacheTTL   = 5 * time.Minute
	CacheCleanupTick = time.Minute

	// maxJSONResponseBytes caps a JSON API response.
	maxJSONResponseBytes = 5 * 1024 * 1024
	// maxRawResponseBytes caps a raw diff or file read from the forge; the
	// agent still only gets max_bytes of it.
	maxRawResponseBytes = 20 * 1024 * 1024

	DefaultMaxBytes = 50_000
	MaxMaxBytes     = 200_000

	// pageLimit is the page size asked from Gitea. Gitea caps it with
	// MAX_RESPONSE_ITEMS (default 50).
	pageLimit = 50
	// maxPages bounds every paged listing.
	maxPages = 20

	maxCommentBodyBytes = 4_000
	maxPRBodyBytes      = 20_000
	maxReviews          = 30
	maxComments         = 100
	maxCommentLength    = 60_000
)

// Config holds one Gitea instance's settings.
type Config struct {
	URL           string
	Token         string
	VerifySSL     bool
	Timeout       int
	AllowedRepos  []string // "owner/repo" or "owner/*"; empty = every repo the token can read
	AllowComments bool

	// Merging (off while MergeTargets is empty).
	MergeTargets      []MergeTarget
	MergeTargetErrors []string // bad gitea_merge_targets lines, skipped
	MergeMaxFiles     int
	MergeMaxLines     int
}

// GiteaTool handles Gitea API calls.
type GiteaTool struct {
	logger      *log.Logger
	configCache *cache.Cache
	rateLimiter *ratelimit.Limiter
}

// NewGiteaTool creates the tool with an optional rate limiter.
func NewGiteaTool(logger *log.Logger, limiter *ratelimit.Limiter) *GiteaTool {
	return &GiteaTool{
		logger:      logger,
		configCache: cache.New(ConfigCacheTTL, CacheCleanupTick),
		rateLimiter: limiter,
	}
}

// Stop releases the caches.
func (t *GiteaTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
}

func configCacheKey(incidentID, logicalName string) string {
	if logicalName != "" {
		return "creds:logical:" + toolType + ":" + logicalName
	}
	return "creds:" + incidentID + ":" + toolType
}

func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

func clampTimeout(timeout int) int {
	switch {
	case timeout <= 0:
		return 30
	case timeout < 5:
		return 5
	case timeout > 300:
		return 300
	}
	return timeout
}

// ParseAllowedRepos reads gitea_allowed_repos: a string with one entry per
// line (or separated by commas or spaces; "#" starts a comment), or a JSON
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

func buildConfig(settings map[string]interface{}) *Config {
	c := &Config{VerifySSL: true, Timeout: 30}
	if u, ok := settings["gitea_url"].(string); ok {
		c.URL = strings.TrimRight(strings.TrimSpace(u), "/")
		// Accept a URL that already ends in the API prefix.
		c.URL = strings.TrimSuffix(c.URL, "/api/v1")
	}
	if tok, ok := settings["gitea_token"].(string); ok {
		c.Token = strings.TrimSpace(tok)
	}
	if v, ok := settings["gitea_verify_ssl"].(bool); ok {
		c.VerifySSL = v
	}
	if v, ok := settings["gitea_timeout"].(float64); ok {
		c.Timeout = int(v)
	}
	c.Timeout = clampTimeout(c.Timeout)
	c.AllowedRepos = ParseAllowedRepos(settings["gitea_allowed_repos"])
	if v, ok := settings["gitea_allow_comments"].(bool); ok {
		c.AllowComments = v
	}
	c.MergeTargets, c.MergeTargetErrors = ParseMergeTargets(settings["gitea_merge_targets"])
	c.MergeMaxFiles = DefaultMergeMaxFiles
	if v, ok := settings["gitea_merge_max_files"].(float64); ok && v >= 1 {
		c.MergeMaxFiles = int(v)
	}
	c.MergeMaxLines = DefaultMergeMaxLines
	if v, ok := settings["gitea_merge_max_lines"].(float64); ok && v >= 1 {
		c.MergeMaxLines = int(v)
	}
	return c
}

func (t *GiteaTool) getConfig(ctx context.Context, incidentID, logicalName string) (*Config, error) {
	key := configCacheKey(incidentID, logicalName)
	if cached, ok := t.configCache.Get(key); ok {
		if c, ok := cached.(*Config); ok {
			return c, nil
		}
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Gitea credentials: %w", err)
	}
	c := buildConfig(creds.Settings)
	t.configCache.Set(key, c)
	return c, nil
}

// verifyCommentGate re-reads the instance settings from the database (not the
// 5-minute cache), so turning gitea_allow_comments off takes effect at once.
// Without a database (unit tests) the given config decides.
func (t *GiteaTool) verifyCommentGate(ctx context.Context, incidentID, logicalName string, cached *Config) (*Config, error) {
	cfg := cached
	if database.DB != nil {
		creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
		if err != nil {
			return nil, fmt.Errorf("failed to verify the Gitea comment setting: %w", err)
		}
		cfg = buildConfig(creds.Settings)
		t.configCache.Set(configCacheKey(incidentID, logicalName), cfg)
	}
	if cfg == nil || !cfg.AllowComments {
		return cfg, fmt.Errorf("comments are disabled for this Gitea instance; an operator must enable gitea_allow_comments")
	}
	return cfg, nil
}

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// splitRepo validates "owner/repo".
func splitRepo(repo string) (owner, name string, err error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !repoPartRe.MatchString(parts[0]) || !repoPartRe.MatchString(parts[1]) ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", "", fmt.Errorf("repo must be \"owner/repo\", got %q", repo)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

// repoAllowed applies gitea_allowed_repos.
func repoAllowed(cfg *Config, owner, name string) bool {
	if len(cfg.AllowedRepos) == 0 {
		return true
	}
	full := strings.ToLower(owner + "/" + name)
	ownerAll := strings.ToLower(owner) + "/*"
	for _, a := range cfg.AllowedRepos {
		if a == full || a == ownerAll || a == "*" {
			return true
		}
	}
	return false
}

var prURLPathRe = regexp.MustCompile(`^/([^/]+)/([^/]+)/(?:pulls|pull)/(\d+)(?:/.*)?$`)

// prTarget is the repository and pull request number a call is about.
type prTarget struct {
	owner, name string
	number      int
}

func (p prTarget) repo() string { return p.owner + "/" + p.name }

// parsePRURL reads https://<host>/<owner>/<repo>/pulls/<n>. The host must be
// the configured instance's host, so a link can never send the token
// elsewhere (requests always go to cfg.URL anyway).
func parsePRURL(cfg *Config, raw string) (prTarget, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return prTarget{}, fmt.Errorf("pr_url is not a URL: %q", raw)
	}
	base, err := url.Parse(cfg.URL)
	if err == nil && base.Host != "" && !strings.EqualFold(base.Host, u.Host) {
		return prTarget{}, fmt.Errorf("pr_url host %q is not this Gitea instance (%s)", u.Host, base.Host)
	}
	p := u.Path
	if base != nil && base.Path != "" && base.Path != "/" {
		// Gitea served under a sub-path, e.g. https://example.com/git
		p = strings.TrimPrefix(p, strings.TrimRight(base.Path, "/"))
	}
	m := prURLPathRe.FindStringSubmatch(p)
	if m == nil {
		return prTarget{}, fmt.Errorf("pr_url must look like https://<host>/<owner>/<repo>/pulls/<number>, got %q", raw)
	}
	owner, name, err := splitRepo(m[1] + "/" + m[2])
	if err != nil {
		return prTarget{}, err
	}
	n, _ := strconv.Atoi(m[3])
	return prTarget{owner: owner, name: name, number: n}, nil
}

func intArg(args map[string]interface{}, key string) (int, bool) {
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

func stringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func maxBytesArg(args map[string]interface{}) int {
	n, ok := intArg(args, "max_bytes")
	if !ok || n <= 0 {
		return DefaultMaxBytes
	}
	if n > MaxMaxBytes {
		return MaxMaxBytes
	}
	return n
}

// resolveRepo validates the repo argument against the allowlist.
func resolveRepo(cfg *Config, args map[string]interface{}) (owner, name string, err error) {
	repo := stringArg(args, "repo")
	if repo == "" {
		return "", "", fmt.Errorf("repo is required (\"owner/repo\")%s", validation.SuggestParam("repo", args))
	}
	owner, name, err = splitRepo(repo)
	if err != nil {
		return "", "", err
	}
	if !repoAllowed(cfg, owner, name) {
		return "", "", fmt.Errorf("repo %s/%s is not in gitea_allowed_repos for this instance", owner, name)
	}
	return owner, name, nil
}

// resolvePR reads repo + number, or pr_url, and applies the allowlist.
func resolvePR(cfg *Config, args map[string]interface{}) (prTarget, error) {
	if raw := stringArg(args, "pr_url"); raw != "" {
		p, err := parsePRURL(cfg, raw)
		if err != nil {
			return prTarget{}, err
		}
		if !repoAllowed(cfg, p.owner, p.name) {
			return prTarget{}, fmt.Errorf("repo %s is not in gitea_allowed_repos for this instance", p.repo())
		}
		return p, nil
	}
	owner, name, err := resolveRepo(cfg, args)
	if err != nil {
		return prTarget{}, err
	}
	n, ok := intArg(args, "number")
	if !ok || n <= 0 {
		return prTarget{}, fmt.Errorf("number is required (the pull request number), or pass pr_url%s", validation.SuggestParam("number", args))
	}
	return prTarget{owner: owner, name: name, number: n}, nil
}

func repoPath(owner, name string) string {
	return "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// apiError is a non-2xx answer from Gitea.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "Gitea returned 401: the token is missing, wrong or expired"
	case http.StatusForbidden:
		return "Gitea returned 403: the token has no access to this repository or action: " + e.Body
	case http.StatusNotFound:
		return "Gitea returned 404: no such repository, pull request, file or ref (or the token cannot see it)"
	}
	return fmt.Sprintf("Gitea returned HTTP %d: %s", e.Status, e.Body)
}

// do performs one request and returns the body, at most limit bytes. The
// second result reports that the body was longer than limit.
func (t *GiteaTool) do(ctx context.Context, cfg *Config, method, path string, query url.Values, body interface{}, accept string, limit int) (data []byte, over bool, total int, err error) {
	if cfg.URL == "" {
		return nil, false, 0, fmt.Errorf("gitea_url is not configured")
	}
	if cfg.Token == "" {
		return nil, false, 0, fmt.Errorf("gitea_token is not configured")
	}
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, false, 0, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}
	full := cfg.URL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, false, 0, fmt.Errorf("encode request: %w", err)
		}
		reqBody = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
	if err != nil {
		return nil, false, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+cfg.Token)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	transport := &http.Transport{DisableKeepAlives: true, Proxy: nil}
	if !cfg.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in via gitea_verify_ssl
	}
	client := &http.Client{
		Timeout:   time.Duration(cfg.Timeout) * time.Second,
		Transport: transport,
		// A redirect to another host would carry the token there; Gitea
		// does not redirect API calls, so refuse them.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.logger.Printf("Gitea API call: %s %s", method, path)
	resp, err := client.Do(req)
	if err != nil {
		// The error text holds the URL but never the token (it is a header).
		return nil, false, 0, fmt.Errorf("request to Gitea failed: %w", err)
	}
	defer resp.Body.Close()

	data, err = io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, false, 0, fmt.Errorf("read Gitea response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(data)
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return nil, false, 0, &apiError{Status: resp.StatusCode, Body: strings.TrimSpace(msg)}
	}
	total = -1
	if v := resp.Header.Get("X-Total-Count"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			total = n
		}
	}
	if len(data) > limit {
		return data[:limit], true, total, nil
	}
	return data, false, total, nil
}

func (t *GiteaTool) getJSON(ctx context.Context, cfg *Config, path string, query url.Values, out interface{}) error {
	_, err := t.getJSONTotal(ctx, cfg, path, query, out)
	return err
}

// getJSONTotal also returns the X-Total-Count header of a list endpoint
// (-1 when it is absent).
func (t *GiteaTool) getJSONTotal(ctx context.Context, cfg *Config, path string, query url.Values, out interface{}) (int, error) {
	data, over, total, err := t.do(ctx, cfg, http.MethodGet, path, query, nil, "application/json", maxJSONResponseBytes)
	if err != nil {
		return 0, err
	}
	if over {
		return 0, fmt.Errorf("Gitea response exceeds %d MB", maxJSONResponseBytes/(1024*1024))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return 0, fmt.Errorf("decode Gitea response: %w", err)
	}
	return total, nil
}

// getPaged reads up to maxPages pages of a list endpoint. It returns the items
// and whether the listing stopped at the page cap.
//
// Gitea may cap the page size below pageLimit (MAX_RESPONSE_ITEMS), so the end
// comes from X-Total-Count; without that header an empty or short page ends it.
func getPaged[T any](ctx context.Context, t *GiteaTool, cfg *Config, path string, query url.Values) ([]T, bool, error) {
	var all []T
	if query == nil {
		query = url.Values{}
	}
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("limit", strconv.Itoa(pageLimit))
		var items []T
		total, err := t.getJSONTotal(ctx, cfg, path, query, &items)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		switch {
		case len(items) == 0:
			return all, false, nil
		case total >= 0 && len(all) >= total:
			return all, false, nil
		case total < 0 && len(items) < pageLimit:
			return all, false, nil
		}
	}
	return all, true, nil
}

// cut keeps at most n bytes of s on a rune boundary.
func cut(s string, n int) (string, bool) {
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

func marshal(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// --- API shapes (only the fields we use) ---

type apiUser struct {
	Login string `json:"login"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type apiBranch struct {
	Ref   string `json:"ref"`
	Sha   string `json:"sha"`
	Label string `json:"label"`
}

type apiPR struct {
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	User           *apiUser   `json:"user"`
	State          string     `json:"state"`
	Draft          bool       `json:"draft"`
	Merged         bool       `json:"merged"`
	Mergeable      bool       `json:"mergeable"`
	MergedBy       *apiUser   `json:"merged_by"`
	MergeCommitSha string     `json:"merge_commit_sha"`
	MergeBase      string     `json:"merge_base"`
	Labels         []apiLabel `json:"labels"`
	Assignees      []apiUser  `json:"assignees"`
	Reviewers      []apiUser  `json:"requested_reviewers"`
	Base           *apiBranch `json:"base"`
	Head           *apiBranch `json:"head"`
	HTMLURL        string     `json:"html_url"`
	Comments       int        `json:"comments"`
	Additions      *int       `json:"additions"`
	Deletions      *int       `json:"deletions"`
	ChangedFiles   *int       `json:"changed_files"`
	CreatedAt      string     `json:"created_at"`
	UpdatedAt      string     `json:"updated_at"`
	MergedAt       *string    `json:"merged_at"`
	ClosedAt       *string    `json:"closed_at"`
}

type apiChangedFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
}

type apiComment struct {
	ID        int64    `json:"id"`
	User      *apiUser `json:"user"`
	Body      string   `json:"body"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	HTMLURL   string   `json:"html_url"`
}

type apiReview struct {
	ID          int64    `json:"id"`
	User        *apiUser `json:"user"`
	State       string   `json:"state"`
	Body        string   `json:"body"`
	CommitID    string   `json:"commit_id"`
	Stale       bool     `json:"stale"`
	Dismissed   bool     `json:"dismissed"`
	Official    bool     `json:"official"`
	CommentsCnt int      `json:"comments_count"`
	SubmittedAt string   `json:"submitted_at"`
}

type apiReviewComment struct {
	ID        int64    `json:"id"`
	User      *apiUser `json:"user"`
	Body      string   `json:"body"`
	Path      string   `json:"path"`
	Position  int      `json:"position"`
	OrigPos   int      `json:"original_position"`
	DiffHunk  string   `json:"diff_hunk"`
	CreatedAt string   `json:"created_at"`
	Resolver  *apiUser `json:"resolver"`
}

type apiIssue struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	User        *apiUser   `json:"user"`
	Labels      []apiLabel `json:"labels"`
	HTMLURL     string     `json:"html_url"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	PullRequest *struct {
		Merged   bool    `json:"merged"`
		MergedAt *string `json:"merged_at"`
		Draft    bool    `json:"draft"`
	} `json:"pull_request"`
}

func login(u *apiUser) string {
	if u == nil {
		return ""
	}
	return u.Login
}

func labelNames(ls []apiLabel) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

func logins(us []apiUser) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Login)
	}
	return out
}

// --- Tools ---

// GetPullRequest returns the pull request's metadata.
func (t *GiteaTool) GetPullRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	var pr apiPR
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d", repoPath(p.owner, p.name), p.number), nil, &pr); err != nil {
		return "", err
	}
	body, bodyCut := cut(pr.Body, maxPRBodyBytes)
	out := map[string]interface{}{
		"repo":                p.repo(),
		"number":              pr.Number,
		"title":               pr.Title,
		"body":                body,
		"author":              login(pr.User),
		"state":               pr.State,
		"draft":               pr.Draft,
		"merged":              pr.Merged,
		"mergeable":           pr.Mergeable,
		"labels":              labelNames(pr.Labels),
		"assignees":           logins(pr.Assignees),
		"requested_reviewers": logins(pr.Reviewers),
		"comments":            pr.Comments,
		"html_url":            pr.HTMLURL,
		"created_at":          pr.CreatedAt,
		"updated_at":          pr.UpdatedAt,
	}
	if bodyCut {
		out["body_truncated"] = true
	}
	if pr.Base != nil {
		out["base"] = map[string]string{"ref": pr.Base.Ref, "sha": pr.Base.Sha}
	}
	if pr.Head != nil {
		out["head"] = map[string]string{"ref": pr.Head.Ref, "sha": pr.Head.Sha, "label": pr.Head.Label}
	}
	if pr.Merged {
		out["merged_by"] = login(pr.MergedBy)
		out["merge_commit_sha"] = pr.MergeCommitSha
		if pr.MergedAt != nil {
			out["merged_at"] = *pr.MergedAt
		}
	}
	if pr.ClosedAt != nil {
		out["closed_at"] = *pr.ClosedAt
	}
	// Older servers omit these, and some report 0 for a merged pull
	// request; a 0 there means "unknown", so leave it out.
	if pr.ChangedFiles != nil && *pr.ChangedFiles > 0 {
		out["changed_files"] = *pr.ChangedFiles
		if pr.Additions != nil {
			out["additions"] = *pr.Additions
		}
		if pr.Deletions != nil {
			out["deletions"] = *pr.Deletions
		}
	}
	return marshal(out)
}

// ListPullRequestFiles returns the changed files with status and line counts.
func (t *GiteaTool) ListPullRequestFiles(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	files, capped, err := getPaged[apiChangedFile](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/files", repoPath(p.owner, p.name), p.number), nil)
	if err != nil {
		return "", err
	}
	adds, dels := 0, 0
	list := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		adds += f.Additions
		dels += f.Deletions
		item := map[string]interface{}{
			"file":      f.Filename,
			"status":    f.Status,
			"additions": f.Additions,
			"deletions": f.Deletions,
		}
		if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
			item["previous_file"] = f.PreviousFilename
		}
		list = append(list, item)
	}
	out := map[string]interface{}{
		"repo":      p.repo(),
		"number":    p.number,
		"count":     len(list),
		"additions": adds,
		"deletions": dels,
		"files":     list,
	}
	if capped {
		out["truncated"] = fmt.Sprintf("stopped after %d pages; the pull request has more files", maxPages)
	}
	return marshal(out)
}

// splitDiff returns the per-file sections of a unified git diff, keyed by
// the new path (and the old path, for renames and deletions).
func splitDiff(diff string) (order []string, sections map[string]string) {
	sections = map[string]string{}
	var cur strings.Builder
	var names []string
	flush := func() {
		if len(names) == 0 {
			return
		}
		text := cur.String()
		order = append(order, names[0])
		for _, n := range names {
			if _, ok := sections[n]; !ok {
				sections[n] = text
			}
		}
	}
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur.Reset()
			names = diffGitNames(strings.TrimRight(line, "\r\n"))
		}
		if len(names) > 0 {
			cur.WriteString(line)
		}
	}
	flush()
	return order, sections
}

// diffGitNames reads `diff --git a/<old> b/<new>` and returns the new path
// first. Git quotes each path on its own (C-style, when it has special or
// non-ASCII characters), so either side can be quoted. An unquoted old path
// with spaces is ambiguous; it is split at the first " b/" (or ` "b/`).
func diffGitNames(header string) []string {
	rest := strings.TrimPrefix(header, "diff --git ")
	var oldRaw, newRaw string
	if strings.HasPrefix(rest, "\"") {
		end := quotedEnd(rest)
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ' ' {
			return []string{rest}
		}
		oldRaw, newRaw = rest[:end+1], rest[end+2:]
	} else {
		i := strings.Index(rest, " b/")
		if j := strings.Index(rest, " \"b/"); j >= 0 && (i < 0 || j < i) {
			i = j
		}
		if i < 0 {
			return []string{rest}
		}
		oldRaw, newRaw = rest[:i], rest[i+1:]
	}
	oldP, ok1 := diffPath(oldRaw, "a/")
	newP, ok2 := diffPath(newRaw, "b/")
	if !ok1 || !ok2 {
		return []string{rest}
	}
	if newP == oldP {
		return []string{newP}
	}
	return []string{newP, oldP}
}

// quotedEnd returns the index of the closing quote of the C-style quoted
// string at the start of s, or -1.
func quotedEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// diffPath unquotes one header path (if quoted) and drops its a/ or b/ prefix.
func diffPath(raw, prefix string) (string, bool) {
	if strings.HasPrefix(raw, "\"") {
		u, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		raw = u
	}
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	return raw[len(prefix):], true
}

// GetPullRequestDiff returns the diff of one file or of the whole pull
// request, cut at max_bytes.
func (t *GiteaTool) GetPullRequestDiff(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	maxBytes := maxBytesArg(args)
	file := strings.TrimPrefix(stringArg(args, "file"), "/")

	raw, over, _, err := t.do(ctx, cfg, http.MethodGet, fmt.Sprintf("%s/pulls/%d.diff", repoPath(p.owner, p.name), p.number), nil, nil, "text/plain", maxRawResponseBytes)
	if err != nil {
		return "", err
	}
	diff := string(raw)
	order, sections := splitDiff(diff)

	header := fmt.Sprintf("# %s#%d diff", p.repo(), p.number)
	partial := false
	if file != "" {
		sec, ok := sections[file]
		if !ok {
			hint := ""
			if over {
				hint = fmt.Sprintf(" (the diff is over %d MB and was read only in part; the file may be in the unread part)", maxRawResponseBytes/(1024*1024))
			}
			names := order
			if len(names) > 30 {
				names = names[:30]
			}
			return "", fmt.Errorf("file %q is not in the diff of %s#%d%s; files: %s", file, p.repo(), p.number, hint, strings.Join(names, ", "))
		}
		header += " — file " + file
		diff = sec
		// The download cap stops inside the last section, so only that file
		// can be partial.
		partial = over && len(order) > 0 && sections[order[len(order)-1]] == sec
	} else {
		partial = over
		header += fmt.Sprintf(" — %d files", len(order))
	}

	total := len(diff)
	body, truncated := cut(diff, maxBytes)
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n")
	b.WriteString(body)
	if truncated || partial {
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		size := fmt.Sprintf("%d bytes", total)
		if partial {
			size = fmt.Sprintf("over %d bytes", total)
		}
		hint := fmt.Sprintf("Call list_pull_request_files, then get_pull_request_diff with file=<path> for each file, or raise max_bytes (max %d).", MaxMaxBytes)
		switch {
		case file != "" && partial && !truncated:
			hint = fmt.Sprintf("The pull request diff is over %d MB and was read only in part, so the end of this file's diff is missing. Read the file with get_file at the head sha instead.", maxRawResponseBytes/(1024*1024))
		case file != "":
			hint = fmt.Sprintf("Raise max_bytes (max %d) to see more of this file.", MaxMaxBytes)
		}
		fmt.Fprintf(&b, "\n[TRUNCATED: showed %d of %s. %s]\n", len(body), size, hint)
	}
	return b.String(), nil
}

// GetFile returns a file at a branch, tag or commit.
func (t *GiteaTool) GetFile(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	owner, name, err := resolveRepo(cfg, args)
	if err != nil {
		return "", err
	}
	path := strings.Trim(stringArg(args, "path"), "/")
	if path == "" {
		return "", fmt.Errorf("path is required%s", validation.SuggestParam("path", args))
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", fmt.Errorf("path %q must be a plain repository path", path)
		}
	}
	ref := stringArg(args, "ref")
	maxBytes := maxBytesArg(args)

	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	raw, over, _, err := t.do(ctx, cfg, http.MethodGet, repoPath(owner, name)+"/raw/"+strings.Join(segs, "/"), q, nil, "*/*", maxBytes+utf8.UTFMax-1)
	if err != nil {
		return "", err
	}
	refText := ref
	if refText == "" {
		refText = "default branch"
	}
	head := fmt.Sprintf("# %s/%s:%s @ %s\n\n", owner, name, path, refText)
	sample := raw
	if len(sample) > 8000 {
		sample = sample[:8000]
	}
	if strings.IndexByte(string(sample), 0) >= 0 {
		return head + "[binary file; content not shown]\n", nil
	}
	// Read a few bytes past max_bytes so cut can end on a rune boundary.
	text, cutHere := cut(string(raw), maxBytes)
	if over || cutHere {
		shown := len(text)
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("\n[TRUNCATED: showed the first %d bytes. Raise max_bytes (max %d) to see more.]\n", shown, MaxMaxBytes)
	}
	return head + text, nil
}

// ListPullRequestComments returns the conversation comments and the reviews
// (with their inline comments) of a pull request.
func (t *GiteaTool) ListPullRequestComments(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	base := repoPath(p.owner, p.name)

	// This endpoint has no paging: it returns every comment at once.
	var comments []apiComment
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/issues/%d/comments", base, p.number), nil, &comments); err != nil {
		return "", err
	}
	commentsCapped := false
	if len(comments) > maxComments {
		commentsCapped = true
		comments = comments[len(comments)-maxComments:]
	}
	conv := make([]map[string]interface{}, 0, len(comments))
	for _, c := range comments {
		body, short := cut(c.Body, maxCommentBodyBytes)
		item := map[string]interface{}{
			"id": c.ID, "author": login(c.User), "created_at": c.CreatedAt, "body": body,
		}
		if short {
			item["body_truncated"] = true
		}
		conv = append(conv, item)
	}

	reviews, _, err := getPaged[apiReview](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/reviews", base, p.number), nil)
	if err != nil {
		return "", err
	}
	out := map[string]interface{}{
		"repo":     p.repo(),
		"number":   p.number,
		"comments": conv,
	}
	if commentsCapped {
		out["comments_truncated"] = fmt.Sprintf("showing the newest %d comments", maxComments)
	}
	revs := make([]map[string]interface{}, 0, len(reviews))
	for i, r := range reviews {
		if i == maxReviews {
			out["reviews_truncated"] = fmt.Sprintf("showing the first %d of %d reviews", maxReviews, len(reviews))
			break
		}
		body, short := cut(r.Body, maxCommentBodyBytes)
		item := map[string]interface{}{
			"id": r.ID, "author": login(r.User), "state": r.State, "submitted_at": r.SubmittedAt,
			"body": body, "commit_id": r.CommitID,
		}
		if short {
			item["body_truncated"] = true
		}
		if r.Stale {
			item["stale"] = true
		}
		if r.Dismissed {
			item["dismissed"] = true
		}
		if r.CommentsCnt > 0 {
			var rcs []apiReviewComment
			if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d/reviews/%d/comments", base, p.number, r.ID), nil, &rcs); err != nil {
				item["comments_error"] = err.Error()
			} else {
				inline := make([]map[string]interface{}, 0, len(rcs))
				for _, rc := range rcs {
					cb, _ := cut(rc.Body, maxCommentBodyBytes)
					hunk, _ := cut(rc.DiffHunk, 1500)
					line := rc.Position
					if line == 0 {
						line = rc.OrigPos
					}
					ic := map[string]interface{}{
						"author": login(rc.User), "file": rc.Path, "line": line, "body": cb,
						"created_at": rc.CreatedAt, "diff_hunk": hunk,
					}
					if rc.Resolver != nil {
						ic["resolved_by"] = rc.Resolver.Login
					}
					inline = append(inline, ic)
				}
				item["inline_comments"] = inline
			}
		}
		revs = append(revs, item)
	}
	out["reviews"] = revs
	return marshal(out)
}

// SearchPullRequests lists pull requests of a repository, optionally by
// state and a keyword query.
func (t *GiteaTool) SearchPullRequests(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	owner, name, err := resolveRepo(cfg, args)
	if err != nil {
		return "", err
	}
	state := strings.ToLower(stringArg(args, "state"))
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		return "", fmt.Errorf("state must be open, closed or all, got %q", state)
	}
	limit, ok := intArg(args, "limit")
	if !ok || limit <= 0 || limit > pageLimit {
		limit = 20
	}
	page, ok := intArg(args, "page")
	if !ok || page <= 0 {
		page = 1
	}
	q := url.Values{}
	q.Set("type", "pulls")
	q.Set("state", state)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("page", strconv.Itoa(page))
	if query := stringArg(args, "query"); query != "" {
		q.Set("q", query)
	}
	if v := stringArg(args, "labels"); v != "" {
		q.Set("labels", v)
	}
	if v := stringArg(args, "author"); v != "" {
		q.Set("created_by", v)
	}
	var issues []apiIssue
	total, err := t.getJSONTotal(ctx, cfg, repoPath(owner, name)+"/issues", q, &issues)
	if err != nil {
		return "", err
	}
	list := make([]map[string]interface{}, 0, len(issues))
	for _, is := range issues {
		item := map[string]interface{}{
			"number": is.Number, "title": is.Title, "state": is.State, "author": login(is.User),
			"labels": labelNames(is.Labels), "updated_at": is.UpdatedAt, "html_url": is.HTMLURL,
		}
		if is.PullRequest != nil {
			item["merged"] = is.PullRequest.Merged
			if is.PullRequest.Draft {
				item["draft"] = true
			}
		}
		list = append(list, item)
	}
	out := map[string]interface{}{
		"repo": owner + "/" + name, "state": state, "page": page, "count": len(list), "pull_requests": list,
	}
	// Gitea may serve fewer items than limit per page (MAX_RESPONSE_ITEMS), so
	// the end comes from X-Total-Count; without it a full page means "maybe more".
	// A short page is either the server's page size or the last page; page*len
	// can only under-count what was served, so at worst next_page is empty.
	if total >= 0 {
		out["total_count"] = total
		if len(list) > 0 && page*len(list) < total {
			out["next_page"] = page + 1
		}
	} else if len(list) == limit {
		out["next_page"] = page + 1
	}
	return marshal(out)
}

// CreatePullRequestComment posts a conversation comment on a pull request.
// It is refused unless gitea_allow_comments is on for the instance.
func (t *GiteaTool) CreatePullRequestComment(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	cfg, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}
	cfg, err = t.verifyCommentGate(ctx, incidentID, logicalName, cfg)
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	body := stringArg(args, "body")
	if body == "" {
		return "", fmt.Errorf("body is required%s", validation.SuggestParam("body", args))
	}
	if len(body) > maxCommentLength {
		return "", fmt.Errorf("body is %d bytes; the limit is %d", len(body), maxCommentLength)
	}
	data, _, _, err := t.do(ctx, cfg, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(p.owner, p.name), p.number),
		nil, map[string]string{"body": body}, "application/json", maxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var c apiComment
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("decode Gitea response: %w", err)
	}
	return marshal(map[string]interface{}{
		"repo": p.repo(), "number": p.number, "comment_id": c.ID, "html_url": c.HTMLURL, "created_at": c.CreatedAt,
	})
}
