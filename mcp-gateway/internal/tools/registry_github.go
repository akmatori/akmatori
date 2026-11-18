package tools

import (
	"context"

	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/tools/github"
)

// GitHub allows 5000 requests per hour per token (search: 30 per minute);
// the local limiter only smooths bursts.
const (
	GitHubRatePerSecond = 5  // requests per second
	GitHubBurstCapacity = 10 // burst capacity
)

// registerGitHubTools registers github.* (6 read tools + 1 comment tool that
// is refused unless the instance has github_allow_comments on).
func (r *Registry) registerGitHubTools() {
	r.githubLimit = ratelimit.New(GitHubRatePerSecond, GitHubBurstCapacity)
	r.githubTool = github.NewGitHubTool(r.logger, r.githubLimit)
	gt := r.githubTool

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.get_pull_request",
			Description: "Get a GitHub pull request: title, body, author, base/head branch and sha, state, mergeable, labels, reviewers, line counts.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.GetPullRequest(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.list_pull_request_files",
			Description: "List the files a GitHub pull request changes, with status (added, modified, removed, renamed, copied) and added/deleted line counts. Use it first to plan which diffs to read.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.ListPullRequestFiles(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.get_pull_request_diff",
			Description: "Get the unified diff of a GitHub pull request, for one file (file) or for all files. Output is cut at max_bytes with a [TRUNCATED ...] marker; for a large pull request read one file at a time. When GitHub refuses the full diff (too large), it is built from per-file patches.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(map[string]mcp.Property{
				"file":      {Type: "string", Description: "Only this file's diff (path as in list_pull_request_files)"},
				"max_bytes": {Type: "number", Description: "Byte budget for the diff text (default 50000, max 200000)"},
			})},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.GetPullRequestDiff(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.get_file",
			Description: "Get a file from a GitHub repository at a branch, tag or commit, to read the full context around a change (e.g. the head sha of the pull request). A directory path returns its listing.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"repo":      {Type: "string", Description: "Repository as \"owner/repo\" (required)"},
					"path":      {Type: "string", Description: "File path in the repository (required)"},
					"ref":       {Type: "string", Description: "Branch, tag or commit sha (default: the default branch)"},
					"max_bytes": {Type: "number", Description: "Byte budget (default 50000, max 200000)"},
				},
				Required: []string{"repo", "path"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.GetFile(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.list_pull_request_comments",
			Description: "List the conversation comments and the reviews (state, body, inline comments with file and line) of a GitHub pull request.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.ListPullRequestComments(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.search_pull_requests",
			Description: "Search pull requests of a GitHub repository, newest updated first, by state, labels, author and keywords. Qualifiers that widen the search (repo:, org:, user:, is:) are ignored.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"repo":   {Type: "string", Description: "Repository as \"owner/repo\" (required)"},
					"state":  {Type: "string", Description: "open (default), closed or all", Enum: []string{"open", "closed", "all"}},
					"query":  {Type: "string", Description: "Keywords for title, body and comments (GitHub search syntax; in:title is allowed)"},
					"labels": {Type: "string", Description: "Comma-separated label names"},
					"author": {Type: "string", Description: "Only pull requests opened by this user (login)"},
					"limit":  {Type: "number", Description: "Results per page (default 20, max 50)"},
					"page":   {Type: "number", Description: "Page number (default 1)"},
				},
				Required: []string{"repo"},
			},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.SearchPullRequests(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "github.create_pull_request_comment",
			Description: "Post a comment on a GitHub pull request (write). Refused unless the instance has github_allow_comments enabled.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(map[string]mcp.Property{
				"body": {Type: "string", Description: "Comment text in Markdown (required)"},
			}), Required: []string{"body"}},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.CreatePullRequestComment(ctx, incidentID, args)
		},
	)

	r.logger.Println("GitHub tools registered (7 methods)")
}
