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
	toolType = "gitea"

	ConfigCacheTTL   = 5 * time.Minute
	CacheCleanupTick = time.Minute

	maxJSONResponseBytes = forge.MaxJSONResponseBytes
	maxRawResponseBytes  = forge.MaxRawResponseBytes

	// pageLimit is the page size asked from Gitea. Gitea caps it with
	// MAX_RESPONSE_ITEMS (default 50).
	pageLimit = 50
	// maxPages bounds every paged listing.
	maxPages = 20

	maxCommentBodyBytes = forge.MaxCommentBodyBytes
	maxPRBodyBytes      = forge.MaxPRBodyBytes
	maxReviews          = forge.MaxReviews
	maxComments         = forge.MaxComments
	maxCommentLength    = forge.MaxCommentLength
)

// Config holds one Gitea instance's settings.
type Config struct {
	URL           string
	Token         string
	VerifySSL     bool
	Timeout       int
	AllowedRepos  []string // "owner/repo" or "owner/*"; empty = every repo the token can read
	AllowComments bool
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
	c.AllowedRepos = forge.ParseAllowedRepos(settings["gitea_allowed_repos"])
	if v, ok := settings["gitea_allow_comments"].(bool); ok {
		c.AllowComments = v
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

// resolver applies gitea_allowed_repos and reads pr_url links of this
// instance (a Gitea served under a sub-path is handled).
func resolver(cfg *Config) forge.Resolver {
	return forge.Resolver{
		Allowed: cfg.AllowedRepos,
		Setting: "gitea_allowed_repos",
		ParseURL: func(raw string) (forge.PR, error) {
			return forge.ParsePRURL(cfg.URL, raw, "Gitea")
		},
	}
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
		return nil, false, 0, &apiError{Status: resp.StatusCode, Body: forge.ShortError(data)}
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
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	var pr apiPR
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d", repoPath(p.Owner, p.Name), p.Number), nil, &pr); err != nil {
		return "", err
	}
	body, bodyCut := forge.Cut(pr.Body, maxPRBodyBytes)
	out := map[string]interface{}{
		"repo":                p.Repo(),
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
	return forge.Marshal(out)
}

// ListPullRequestFiles returns the changed files with status and line counts.
func (t *GiteaTool) ListPullRequestFiles(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	files, capped, err := getPaged[apiChangedFile](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/files", repoPath(p.Owner, p.Name), p.Number), nil)
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
		out["truncated"] = fmt.Sprintf("stopped after %d pages; the pull request has more files", maxPages)
	}
	return forge.Marshal(out)
}

// GetPullRequestDiff returns the diff of one file or of the whole pull
// request, cut at max_bytes.
func (t *GiteaTool) GetPullRequestDiff(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	raw, over, _, err := t.do(ctx, cfg, http.MethodGet, fmt.Sprintf("%s/pulls/%d.diff", repoPath(p.Owner, p.Name), p.Number), nil, nil, "text/plain", maxRawResponseBytes)
	if err != nil {
		return "", err
	}
	return forge.RenderDiff(forge.DiffView{
		PR: p, Diff: string(raw), Over: over,
		File: strings.TrimPrefix(forge.StringArg(args, "file"), "/"), MaxBytes: forge.MaxBytesArg(args),
	})
}

// GetFile returns a file at a branch, tag or commit.
func (t *GiteaTool) GetFile(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
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
	// Read a few bytes past max_bytes so the cut can end on a rune boundary.
	raw, over, _, err := t.do(ctx, cfg, http.MethodGet, repoPath(owner, name)+"/raw/"+forge.EscapePath(path), q, nil, "*/*", maxBytes+utf8.UTFMax-1)
	if err != nil {
		return "", err
	}
	return forge.RenderFile(owner+"/"+name, path, ref, raw, over, maxBytes), nil
}

// ListPullRequestComments returns the conversation comments and the reviews
// (with their inline comments) of a pull request.
func (t *GiteaTool) ListPullRequestComments(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	base := repoPath(p.Owner, p.Name)

	// This endpoint has no paging: it returns every comment at once.
	var comments []apiComment
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/issues/%d/comments", base, p.Number), nil, &comments); err != nil {
		return "", err
	}
	commentsCapped := false
	if len(comments) > maxComments {
		commentsCapped = true
		comments = comments[len(comments)-maxComments:]
	}
	conv := make([]map[string]interface{}, 0, len(comments))
	for _, c := range comments {
		body, short := forge.Cut(c.Body, maxCommentBodyBytes)
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
	out := map[string]interface{}{
		"repo":     p.Repo(),
		"number":   p.Number,
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
		body, short := forge.Cut(r.Body, maxCommentBodyBytes)
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
			if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d/reviews/%d/comments", base, p.Number, r.ID), nil, &rcs); err != nil {
				item["comments_error"] = err.Error()
			} else {
				inline := make([]map[string]interface{}, 0, len(rcs))
				for _, rc := range rcs {
					cb, _ := forge.Cut(rc.Body, maxCommentBodyBytes)
					hunk, _ := forge.Cut(rc.DiffHunk, 1500)
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
	return forge.Marshal(out)
}

// SearchPullRequests lists pull requests of a repository, optionally by
// state and a keyword query.
func (t *GiteaTool) SearchPullRequests(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	cfg, err := t.getConfig(ctx, incidentID, extractLogicalName(args))
	if err != nil {
		return "", err
	}
	owner, name, err := resolver(cfg).Repo(args)
	if err != nil {
		return "", err
	}
	state := strings.ToLower(forge.StringArg(args, "state"))
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		return "", fmt.Errorf("state must be open, closed or all, got %q", state)
	}
	limit, ok := forge.IntArg(args, "limit")
	if !ok || limit <= 0 || limit > pageLimit {
		limit = 20
	}
	page, ok := forge.IntArg(args, "page")
	if !ok || page <= 0 {
		page = 1
	}
	q := url.Values{}
	q.Set("type", "pulls")
	q.Set("state", state)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("page", strconv.Itoa(page))
	if query := forge.StringArg(args, "query"); query != "" {
		q.Set("q", query)
	}
	if v := forge.StringArg(args, "labels"); v != "" {
		q.Set("labels", v)
	}
	if v := forge.StringArg(args, "author"); v != "" {
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
	return forge.Marshal(out)
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
	p, err := resolver(cfg).PR(args)
	if err != nil {
		return "", err
	}
	body := forge.StringArg(args, "body")
	if body == "" {
		return "", fmt.Errorf("body is required%s", validation.SuggestParam("body", args))
	}
	if len(body) > maxCommentLength {
		return "", fmt.Errorf("body is %d bytes; the limit is %d", len(body), maxCommentLength)
	}
	data, _, _, err := t.do(ctx, cfg, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(p.Owner, p.Name), p.Number),
		nil, map[string]string{"body": body}, "application/json", maxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var c apiComment
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("decode Gitea response: %w", err)
	}
	return forge.Marshal(map[string]interface{}{
		"repo": p.Repo(), "number": p.Number, "comment_id": c.ID, "html_url": c.HTMLURL, "created_at": c.CreatedAt,
	})
}
