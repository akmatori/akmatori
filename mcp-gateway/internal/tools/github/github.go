// Package github implements the github.* gateway tools: read pull requests,
// their files, diffs, comments and repository files from GitHub (github.com,
// GHE.com or GitHub Enterprise Server), for pre-merge review of
// infrastructure changes. The output shapes match gitea.* (package forge).
package github

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
	"github.com/akmatori/mcp-gateway/internal/tools/forge"
	"github.com/akmatori/mcp-gateway/internal/validation"
)

const (
	toolType = "github"

	ConfigCacheTTL   = 5 * time.Minute
	CacheCleanupTick = time.Minute

	// pageLimit is the page size asked from GitHub (its maximum is 100).
	pageLimit = 100
	// maxPages bounds every paged listing. GitHub lists at most 3000 files of
	// a pull request (30 pages of 100).
	maxPages = 30
	// searchPageLimit caps the per_page of search_pull_requests.
	searchPageLimit = 50
	// maxInlineComments caps review (inline) comments in one listing.
	maxInlineComments = 200
	// maxRedirects bounds same-host redirects (renamed or moved repositories).
	maxRedirects = 3

	apiVersion = "2022-11-28"
	userAgent  = "akmatori-mcp-gateway"

	defaultWebURL = "https://github.com"
	defaultAPIURL = "https://api.github.com"
)

// Config holds one GitHub instance's settings.
type Config struct {
	WebURL        string // e.g. https://github.com or https://ghe.example.com
	APIURL        string // e.g. https://api.github.com or https://ghe.example.com/api/v3
	Token         string
	VerifySSL     bool
	Timeout       int
	AllowedRepos  []string // "owner/repo" or "owner/*"; empty = every repo the token can read
	AllowComments bool
	UseProxy      bool
	ProxyURL      string
}

// GitHubTool handles GitHub REST API calls.
type GitHubTool struct {
	logger      *log.Logger
	configCache *cache.Cache
	rateLimiter *ratelimit.Limiter
}

// NewGitHubTool creates the tool with an optional rate limiter.
func NewGitHubTool(logger *log.Logger, limiter *ratelimit.Limiter) *GitHubTool {
	return &GitHubTool{
		logger:      logger,
		configCache: cache.New(ConfigCacheTTL, CacheCleanupTick),
		rateLimiter: limiter,
	}
}

// Stop releases the caches.
func (t *GitHubTool) Stop() {
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

// baseURLs reads github_url, which may be the web URL or the API URL:
//
//	"" or https://github.com or https://api.github.com → github.com
//	https://<sub>.ghe.com or https://api.<sub>.ghe.com  → GHE.com (data residency)
//	https://ghe.example.com[/api/v3]                    → GitHub Enterprise Server
func baseURLs(raw string) (web, api string) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if s == "" {
		return defaultWebURL, defaultAPIURL
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s, s
	}
	host := strings.ToLower(u.Host)
	switch {
	case host == "github.com" || host == "www.github.com" || host == "api.github.com":
		return defaultWebURL, defaultAPIURL
	case strings.HasSuffix(host, ".ghe.com"):
		h := strings.TrimPrefix(host, "api.")
		return u.Scheme + "://" + h, u.Scheme + "://api." + h
	}
	path := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api/v3")
	web = u.Scheme + "://" + u.Host + path
	return web, web + "/api/v3"
}

func buildConfig(settings map[string]interface{}) *Config {
	c := &Config{VerifySSL: true, Timeout: 30}
	u, _ := settings["github_url"].(string)
	c.WebURL, c.APIURL = baseURLs(u)
	if tok, ok := settings["github_token"].(string); ok {
		c.Token = strings.TrimSpace(tok)
	}
	if v, ok := settings["github_verify_ssl"].(bool); ok {
		c.VerifySSL = v
	}
	if v, ok := settings["github_timeout"].(float64); ok {
		c.Timeout = int(v)
	}
	c.Timeout = clampTimeout(c.Timeout)
	c.AllowedRepos = forge.ParseAllowedRepos(settings["github_allowed_repos"])
	if v, ok := settings["github_allow_comments"].(bool); ok {
		c.AllowComments = v
	}
	return c
}

// applyProxy uses the global proxy when its GitHub switch is on.
func (t *GitHubTool) applyProxy(ctx context.Context, c *Config) {
	if database.DB == nil {
		return
	}
	const key = "proxy:settings"
	var ps *database.ProxySettings
	if cached, ok := t.configCache.Get(key); ok {
		ps, _ = cached.(*database.ProxySettings)
	}
	if ps == nil {
		got, err := database.GetProxySettings(ctx)
		if err != nil || got == nil {
			return
		}
		ps = got
		t.configCache.Set(key, ps)
	}
	if ps.ProxyURL != "" && ps.GitHubEnabled {
		c.UseProxy = true
		c.ProxyURL = ps.ProxyURL
	}
}

func (t *GitHubTool) getConfig(ctx context.Context, incidentID, logicalName string) (*Config, error) {
	key := configCacheKey(incidentID, logicalName)
	if cached, ok := t.configCache.Get(key); ok {
		if c, ok := cached.(*Config); ok {
			return c, nil
		}
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub credentials: %w", err)
	}
	c := buildConfig(creds.Settings)
	t.applyProxy(ctx, c)
	t.configCache.Set(key, c)
	return c, nil
}

// verifyCommentGate re-reads the instance settings from the database (not the
// 5-minute cache), so turning github_allow_comments off takes effect at once.
// Without a database (unit tests) the given config decides.
func (t *GitHubTool) verifyCommentGate(ctx context.Context, incidentID, logicalName string, cached *Config) (*Config, error) {
	cfg := cached
	if database.DB != nil {
		creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
		if err != nil {
			return nil, fmt.Errorf("failed to verify the GitHub comment setting: %w", err)
		}
		cfg = buildConfig(creds.Settings)
		t.applyProxy(ctx, cfg)
		t.configCache.Set(configCacheKey(incidentID, logicalName), cfg)
	}
	if cfg == nil || !cfg.AllowComments {
		return cfg, fmt.Errorf("comments are disabled for this GitHub instance; an operator must enable github_allow_comments")
	}
	return cfg, nil
}

// resolver applies github_allowed_repos and reads pr_url links of this instance.
func resolver(cfg *Config) forge.Resolver {
	return forge.Resolver{
		Allowed: cfg.AllowedRepos,
		Setting: "github_allowed_repos",
		ParseURL: func(raw string) (forge.PR, error) {
			return forge.ParsePRURL(cfg.WebURL, raw, "GitHub")
		},
	}
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// apiError is a non-2xx answer from GitHub.
type apiError struct {
	Status     int
	Body       string
	RateLimit  bool
	ResetAt    string
	RetryAfter string
}

func (e *apiError) Error() string {
	if e.RateLimit {
		msg := "GitHub rate limit reached"
		if e.ResetAt != "" {
			msg += "; it resets at " + e.ResetAt
		}
		if e.RetryAfter != "" {
			msg += "; retry after " + e.RetryAfter + " s"
		}
		return msg
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return "GitHub returned 401: the token is missing, wrong or expired"
	case http.StatusForbidden:
		return "GitHub returned 403: the token has no access to this repository or action (a fine-grained token needs Pull requests: read and Contents: read; an organization may require SSO authorization): " + e.Body
	case http.StatusNotFound:
		return "GitHub returned 404: no such repository, pull request, file or ref (a private repository the token cannot see also gives 404)"
	}
	return fmt.Sprintf("GitHub returned HTTP %d: %s", e.Status, e.Body)
}

// response is one GitHub answer, cut at the read limit.
type response struct {
	data        []byte
	over        bool // the body was longer than the limit
	hasNext     bool // a Link header with rel="next"
	contentType string
}

var linkNextRe = regexp.MustCompile(`<[^>]+>;\s*rel="next"`)

// do performs one request and returns the body, at most limit bytes.
func (t *GitHubTool) do(ctx context.Context, cfg *Config, method, path string, query url.Values, body interface{}, accept string, limit int) (*response, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("github_token is not configured")
	}
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}
	full := cfg.APIURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reqBody = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	transport := &http.Transport{DisableKeepAlives: true, Proxy: nil}
	if !cfg.VerifySSL {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in via github_verify_ssl
	}
	if cfg.UseProxy && cfg.ProxyURL != "" {
		if pu, perr := url.Parse(cfg.ProxyURL); perr == nil {
			transport.Proxy = http.ProxyURL(pu)
		} else {
			t.logger.Printf("GitHub: invalid proxy URL, connecting directly: %v", perr)
		}
	}
	apiHost := req.URL.Host
	client := &http.Client{
		Timeout:   time.Duration(cfg.Timeout) * time.Second,
		Transport: transport,
		// GitHub redirects a renamed or moved repository on the same host.
		// A redirect to another host would carry the token there, so refuse it.
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects || !strings.EqualFold(r.URL.Host, apiHost) || r.URL.Scheme != via[0].URL.Scheme {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	t.logger.Printf("GitHub API call: %s %s", method, path)
	resp, err := client.Do(req)
	if err != nil {
		// The error text holds the URL but never the token (it is a header).
		return nil, fmt.Errorf("request to GitHub failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &apiError{Status: resp.StatusCode, Body: forge.ShortError(data)}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" ||
				strings.Contains(strings.ToLower(e.Body), "rate limit") {
				e.RateLimit = true
				e.RetryAfter = resp.Header.Get("Retry-After")
				if v, perr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); perr == nil && v > 0 {
					e.ResetAt = time.Unix(v, 0).UTC().Format(time.RFC3339)
				}
			}
		}
		return nil, e
	}
	out := &response{
		hasNext:     linkNextRe.MatchString(resp.Header.Get("Link")),
		contentType: resp.Header.Get("Content-Type"),
	}
	if len(data) > limit {
		out.data, out.over = data[:limit], true
	} else {
		out.data = data
	}
	return out, nil
}

// getJSON reads one JSON response and reports a rel="next" link.
func (t *GitHubTool) getJSON(ctx context.Context, cfg *Config, path string, query url.Values, out interface{}) (bool, error) {
	r, err := t.do(ctx, cfg, http.MethodGet, path, query, nil, "application/vnd.github+json", forge.MaxJSONResponseBytes)
	if err != nil {
		return false, err
	}
	if r.over {
		return false, fmt.Errorf("GitHub response exceeds %d MB", forge.MaxJSONResponseBytes/(1024*1024))
	}
	if err := json.Unmarshal(r.data, out); err != nil {
		return false, fmt.Errorf("decode GitHub response: %w", err)
	}
	return r.hasNext, nil
}

// getPaged reads up to maxPages pages of a list endpoint, following the Link
// header. It returns the items and whether the listing stopped at the cap.
func getPaged[T any](ctx context.Context, t *GitHubTool, cfg *Config, path string, query url.Values) ([]T, bool, error) {
	var all []T
	if query == nil {
		query = url.Values{}
	}
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", strconv.Itoa(pageLimit))
		var items []T
		next, err := t.getJSON(ctx, cfg, path, query, &items)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if !next || len(items) == 0 {
			return all, false, nil
		}
	}
	return all, true, nil
}

// --- API shapes (only the fields we use) ---

type apiUser struct {
	Login string `json:"login"`
}

type apiTeam struct {
	Slug string `json:"slug"`
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
	Mergeable      *bool      `json:"mergeable"`
	MergeableState string     `json:"mergeable_state"`
	MergedBy       *apiUser   `json:"merged_by"`
	MergeCommitSha string     `json:"merge_commit_sha"`
	Labels         []apiLabel `json:"labels"`
	Assignees      []apiUser  `json:"assignees"`
	Reviewers      []apiUser  `json:"requested_reviewers"`
	Teams          []apiTeam  `json:"requested_teams"`
	Base           *apiBranch `json:"base"`
	Head           *apiBranch `json:"head"`
	HTMLURL        string     `json:"html_url"`
	Comments       int        `json:"comments"`
	ReviewComments int        `json:"review_comments"`
	Commits        int        `json:"commits"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ChangedFiles   int        `json:"changed_files"`
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
	Patch            string `json:"patch"`
}

type apiComment struct {
	ID        int64    `json:"id"`
	User      *apiUser `json:"user"`
	Body      string   `json:"body"`
	CreatedAt string   `json:"created_at"`
	HTMLURL   string   `json:"html_url"`
}

type apiReview struct {
	ID          int64    `json:"id"`
	User        *apiUser `json:"user"`
	State       string   `json:"state"`
	Body        string   `json:"body"`
	CommitID    string   `json:"commit_id"`
	SubmittedAt string   `json:"submitted_at"`
}

type apiReviewComment struct {
	ID           int64    `json:"id"`
	ReviewID     int64    `json:"pull_request_review_id"`
	InReplyTo    int64    `json:"in_reply_to_id"`
	User         *apiUser `json:"user"`
	Body         string   `json:"body"`
	Path         string   `json:"path"`
	Line         *int     `json:"line"`
	OriginalLine *int     `json:"original_line"`
	DiffHunk     string   `json:"diff_hunk"`
	CreatedAt    string   `json:"created_at"`
}

type apiSearchItem struct {
	Number        int        `json:"number"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	User          *apiUser   `json:"user"`
	Labels        []apiLabel `json:"labels"`
	HTMLURL       string     `json:"html_url"`
	RepositoryURL string     `json:"repository_url"`
	UpdatedAt     string     `json:"updated_at"`
	Draft         bool       `json:"draft"`
	PullRequest   *struct {
		MergedAt *string `json:"merged_at"`
	} `json:"pull_request"`
}

type apiSearch struct {
	TotalCount        int             `json:"total_count"`
	IncompleteResults bool            `json:"incomplete_results"`
	Items             []apiSearchItem `json:"items"`
}

type apiContentEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
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
func (t *GitHubTool) GetPullRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	var pr apiPR
	if _, err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d", repoPath(p.Owner, p.Name), p.Number), nil, &pr); err != nil {
		return "", err
	}
	body, bodyCut := forge.Cut(pr.Body, forge.MaxPRBodyBytes)
	reviewers := logins(pr.Reviewers)
	for _, tm := range pr.Teams {
		reviewers = append(reviewers, "team:"+tm.Slug)
	}
	out := map[string]interface{}{
		"repo":                p.Repo(),
		"number":              pr.Number,
		"title":               pr.Title,
		"body":                body,
		"author":              login(pr.User),
		"state":               pr.State,
		"draft":               pr.Draft,
		"merged":              pr.Merged,
		"labels":              labelNames(pr.Labels),
		"assignees":           logins(pr.Assignees),
		"requested_reviewers": reviewers,
		"comments":            pr.Comments,
		"review_comments":     pr.ReviewComments,
		"commits":             pr.Commits,
		"changed_files":       pr.ChangedFiles,
		"additions":           pr.Additions,
		"deletions":           pr.Deletions,
		"html_url":            pr.HTMLURL,
		"created_at":          pr.CreatedAt,
		"updated_at":          pr.UpdatedAt,
	}
	// mergeable is null while GitHub computes it.
	if pr.Mergeable != nil {
		out["mergeable"] = *pr.Mergeable
	}
	if pr.MergeableState != "" && pr.MergeableState != "unknown" {
		out["mergeable_state"] = pr.MergeableState
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
	return forge.Marshal(out)
}

func (t *GitHubTool) listFiles(ctx context.Context, cfg *Config, p forge.PR) ([]apiChangedFile, bool, error) {
	return getPaged[apiChangedFile](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/files", repoPath(p.Owner, p.Name), p.Number), nil)
}

// ListPullRequestFiles returns the changed files with status and line counts.
func (t *GitHubTool) ListPullRequestFiles(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	files, capped, err := t.listFiles(ctx, cfg, p)
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
		"repo":      p.Repo(),
		"number":    p.Number,
		"count":     len(list),
		"additions": adds,
		"deletions": dels,
		"files":     list,
	}
	if capped {
		out["truncated"] = fmt.Sprintf("stopped after %d files; GitHub lists at most %d files of a pull request", len(list), maxPages*pageLimit)
	}
	return forge.Marshal(out)
}

// patchDiff builds a unified diff from the per-file patches of the files
// API, for a pull request whose full diff GitHub does not serve.
func patchDiff(files []apiChangedFile) string {
	var b strings.Builder
	for _, f := range files {
		oldPath := f.Filename
		if f.PreviousFilename != "" {
			oldPath = f.PreviousFilename
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", oldPath, f.Filename)
		if f.Patch == "" {
			b.WriteString("(no text diff: a binary file, a rename without changes, or a file too large for GitHub to show; use get_file)\n")
			continue
		}
		from, to := "a/"+oldPath, "b/"+f.Filename
		switch f.Status {
		case "added":
			from = "/dev/null"
		case "removed":
			to = "/dev/null"
		}
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", from, to)
		b.WriteString(f.Patch)
		if !strings.HasSuffix(f.Patch, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// GetPullRequestDiff returns the diff of one file or of the whole pull
// request, cut at max_bytes. When GitHub refuses the full diff (too many
// files or lines, HTTP 406), the diff is built from the per-file patches.
func (t *GitHubTool) GetPullRequestDiff(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	view := forge.DiffView{
		PR:       p,
		File:     strings.TrimPrefix(forge.StringArg(args, "file"), "/"),
		MaxBytes: forge.MaxBytesArg(args),
	}
	r, err := t.do(ctx, cfg, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(p.Owner, p.Name), p.Number), nil, nil, "application/vnd.github.diff", forge.MaxRawResponseBytes)
	var ae *apiError
	switch {
	case err == nil:
		view.Diff, view.Over = string(r.data), r.over
	case asAPIError(err, &ae) && ae.Status == http.StatusNotAcceptable:
		files, capped, ferr := t.listFiles(ctx, cfg, p)
		if ferr != nil {
			return "", ferr
		}
		view.Diff = patchDiff(files)
		view.Note = "(GitHub does not serve the full diff of this pull request because it is too large; this diff is built from the per-file patches.)"
		if capped {
			view.Note += fmt.Sprintf(" Only the first %d files are listed.", len(files))
		}
	default:
		return "", err
	}
	return forge.RenderDiff(view)
}

func asAPIError(err error, target **apiError) bool {
	e, ok := err.(*apiError)
	if ok {
		*target = e
	}
	return ok
}

// GetFile returns a file at a branch, tag or commit. A directory path gives
// its listing.
func (t *GitHubTool) GetFile(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	owner, name, err := resolver(cfg).Repo(args)
	if err != nil {
		return "", err
	}
	path, err := forge.FilePath(args)
	if err != nil {
		return "", err
	}
	ref := forge.StringArg(args, "ref")
	maxBytes := forge.MaxBytesArg(args)
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	// The raw media type returns a file's bytes, and the JSON listing for a
	// directory. Read a few bytes past max_bytes so the cut can end on a rune.
	r, err := t.do(ctx, cfg, http.MethodGet, repoPath(owner, name)+"/contents/"+forge.EscapePath(path), q, nil, "application/vnd.github.raw+json", maxBytes+utf8.UTFMax-1)
	if err != nil {
		return "", err
	}
	if strings.Contains(r.contentType, "json") && strings.HasPrefix(strings.TrimSpace(string(r.data)), "[") {
		var entries []apiContentEntry
		if !r.over && json.Unmarshal(r.data, &entries) == nil {
			list := make([]map[string]interface{}, 0, len(entries))
			for _, e := range entries {
				item := map[string]interface{}{"name": e.Name, "path": e.Path, "type": e.Type}
				if e.Type == "file" {
					item["size"] = e.Size
				}
				list = append(list, item)
			}
			return forge.Marshal(map[string]interface{}{
				"repo": owner + "/" + name, "path": path, "ref": ref, "type": "dir",
				"note":    "this path is a directory; call get_file with a file path from entries",
				"entries": list,
			})
		}
	}
	return forge.RenderFile(owner+"/"+name, path, ref, r.data, r.over, maxBytes), nil
}

// ListPullRequestComments returns the conversation comments, the reviews and
// their inline comments.
func (t *GitHubTool) ListPullRequestComments(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	base := repoPath(p.Owner, p.Name)

	comments, commentsCapped, err := getPaged[apiComment](ctx, t, cfg, fmt.Sprintf("%s/issues/%d/comments", base, p.Number), nil)
	if err != nil {
		return "", err
	}
	if len(comments) > forge.MaxComments {
		commentsCapped = true
		comments = comments[len(comments)-forge.MaxComments:]
	}
	conv := make([]map[string]interface{}, 0, len(comments))
	for _, c := range comments {
		body, short := forge.Cut(c.Body, forge.MaxCommentBodyBytes)
		item := map[string]interface{}{
			"id": c.ID, "author": login(c.User), "created_at": c.CreatedAt, "body": body,
		}
		if short {
			item["body_truncated"] = true
		}
		conv = append(conv, item)
	}

	reviews, _, err := getPaged[apiReview](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/reviews", base, p.Number), nil)
	if err != nil {
		return "", err
	}
	inline, inlineCapped, inlineErr := getPaged[apiReviewComment](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/comments", base, p.Number), nil)
	if len(inline) > maxInlineComments {
		inlineCapped = true
		inline = inline[len(inline)-maxInlineComments:]
	}
	byReview := map[int64][]map[string]interface{}{}
	for _, rc := range inline {
		cb, _ := forge.Cut(rc.Body, forge.MaxCommentBodyBytes)
		hunk, _ := forge.Cut(rc.DiffHunk, forge.MaxDiffHunkBytes)
		ic := map[string]interface{}{
			"id": rc.ID, "author": login(rc.User), "file": rc.Path, "body": cb,
			"created_at": rc.CreatedAt, "diff_hunk": hunk,
		}
		switch {
		case rc.Line != nil:
			ic["line"] = *rc.Line
		case rc.OriginalLine != nil:
			ic["line"] = *rc.OriginalLine
			ic["outdated"] = true
		}
		if rc.InReplyTo != 0 {
			ic["in_reply_to"] = rc.InReplyTo
		}
		byReview[rc.ReviewID] = append(byReview[rc.ReviewID], ic)
	}

	out := map[string]interface{}{
		"repo":     p.Repo(),
		"number":   p.Number,
		"comments": conv,
	}
	if commentsCapped {
		out["comments_truncated"] = fmt.Sprintf("showing the newest %d comments", len(conv))
	}
	if inlineErr != nil {
		out["inline_comments_error"] = inlineErr.Error()
	} else if inlineCapped {
		out["inline_comments_truncated"] = fmt.Sprintf("showing the newest %d inline comments", len(inline))
	}
	// Show the newest reviews when there are many.
	if len(reviews) > forge.MaxReviews {
		out["reviews_truncated"] = fmt.Sprintf("showing the newest %d of %d reviews", forge.MaxReviews, len(reviews))
		reviews = reviews[len(reviews)-forge.MaxReviews:]
	}
	revs := make([]map[string]interface{}, 0, len(reviews))
	seen := map[int64]bool{}
	for _, r := range reviews {
		seen[r.ID] = true
		body, short := forge.Cut(r.Body, forge.MaxCommentBodyBytes)
		item := map[string]interface{}{
			"id": r.ID, "author": login(r.User), "state": r.State, "submitted_at": r.SubmittedAt,
			"body": body, "commit_id": r.CommitID,
		}
		if short {
			item["body_truncated"] = true
		}
		if ics := byReview[r.ID]; len(ics) > 0 {
			item["inline_comments"] = ics
		}
		revs = append(revs, item)
	}
	out["reviews"] = revs
	// Inline comments of reviews not shown above.
	var other []map[string]interface{}
	for id, ics := range byReview {
		if !seen[id] {
			other = append(other, ics...)
		}
	}
	if len(other) > 0 {
		out["other_inline_comments"] = other
	}
	return forge.Marshal(out)
}

// searchQualifierRe matches search qualifiers that could widen a search past
// the one repository (GitHub ORs several repo: qualifiers).
var searchQualifierRe = regexp.MustCompile(`(?i)^-?(repo|org|user|owner|is|type):`)

// loginRe matches a GitHub login (bots end in "[bot]").
var loginRe = regexp.MustCompile(`^[A-Za-z0-9-]+(\[bot\])?$`)

// SearchPullRequests lists pull requests of a repository, newest updated
// first, by state, labels, author and a keyword query (GitHub search).
func (t *GitHubTool) SearchPullRequests(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	owner, name, err := resolver(cfg).Repo(args)
	if err != nil {
		return "", err
	}
	state := strings.ToLower(forge.StringArg(args, "state"))
	terms := []string{"repo:" + owner + "/" + name, "is:pr"}
	switch state {
	case "":
		state = "open"
		terms = append(terms, "is:open")
	case "open", "closed":
		terms = append(terms, "is:"+state)
	case "all":
	default:
		return "", fmt.Errorf("state must be open, closed or all, got %q", state)
	}
	for _, l := range strings.Split(forge.StringArg(args, "labels"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			terms = append(terms, "label:"+strconv.Quote(l))
		}
	}
	if a := forge.StringArg(args, "author"); a != "" {
		if !loginRe.MatchString(a) {
			return "", fmt.Errorf("author must be a GitHub login, got %q", a)
		}
		terms = append(terms, "author:"+a)
	}
	var dropped []string
	for _, w := range strings.Fields(forge.StringArg(args, "query")) {
		if searchQualifierRe.MatchString(w) {
			dropped = append(dropped, w)
			continue
		}
		terms = append(terms, w)
	}
	limit, ok := forge.IntArg(args, "limit")
	if !ok || limit <= 0 || limit > searchPageLimit {
		limit = 20
	}
	page, ok := forge.IntArg(args, "page")
	if !ok || page <= 0 {
		page = 1
	}
	q := url.Values{}
	q.Set("q", strings.Join(terms, " "))
	q.Set("sort", "updated")
	q.Set("order", "desc")
	q.Set("per_page", strconv.Itoa(limit))
	q.Set("page", strconv.Itoa(page))
	var res apiSearch
	if _, err := t.getJSON(ctx, cfg, "/search/issues", q, &res); err != nil {
		return "", err
	}
	repoSuffix := strings.ToLower("/repos/" + owner + "/" + name)
	list := make([]map[string]interface{}, 0, len(res.Items))
	for _, it := range res.Items {
		// Defense in depth: keep only this repository's items.
		if it.RepositoryURL != "" && !strings.HasSuffix(strings.ToLower(it.RepositoryURL), repoSuffix) {
			continue
		}
		item := map[string]interface{}{
			"number": it.Number, "title": it.Title, "state": it.State, "author": login(it.User),
			"labels": labelNames(it.Labels), "updated_at": it.UpdatedAt, "html_url": it.HTMLURL,
		}
		if it.PullRequest != nil {
			item["merged"] = it.PullRequest.MergedAt != nil
		}
		if it.Draft {
			item["draft"] = true
		}
		list = append(list, item)
	}
	out := map[string]interface{}{
		"repo": owner + "/" + name, "state": state, "page": page, "count": len(list),
		"total_count": res.TotalCount, "pull_requests": list,
	}
	// GitHub search serves at most 1000 results.
	if page*limit < res.TotalCount && page*limit < 1000 {
		out["next_page"] = page + 1
	}
	if res.IncompleteResults {
		out["incomplete_results"] = true
	}
	if len(dropped) > 0 {
		out["ignored_query_terms"] = dropped
	}
	return forge.Marshal(out)
}

// CreatePullRequestComment posts a conversation comment on a pull request.
// It is refused unless github_allow_comments is on for the instance.
func (t *GitHubTool) CreatePullRequestComment(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	cfg, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}
	cfg, err = t.verifyCommentGate(ctx, incidentID, logicalName, cfg)
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	body := forge.StringArg(args, "body")
	if body == "" {
		return "", fmt.Errorf("body is required%s", validation.SuggestParam("body", args))
	}
	if len(body) > forge.MaxCommentLength {
		return "", fmt.Errorf("body is %d bytes; the limit is %d", len(body), forge.MaxCommentLength)
	}
	r, err := t.do(ctx, cfg, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(p.Owner, p.Name), p.Number),
		nil, map[string]string{"body": body}, "application/vnd.github+json", forge.MaxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var c apiComment
	if err := json.Unmarshal(r.data, &c); err != nil {
		return "", fmt.Errorf("decode GitHub response: %w", err)
	}
	return forge.Marshal(map[string]interface{}{
		"repo": p.Repo(), "number": p.Number, "comment_id": c.ID, "html_url": c.HTMLURL, "created_at": c.CreatedAt,
	})
}
