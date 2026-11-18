package tools

import (
	"context"

	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/tools/gitea"
)

const (
	GiteaRatePerSecond = 10 // requests per second
	GiteaBurstCapacity = 20 // burst capacity
)

// prProps are the arguments that name one pull request.
func prProps(extra map[string]mcp.Property) map[string]mcp.Property {
	props := map[string]mcp.Property{
		"repo":   {Type: "string", Description: "Repository as \"owner/repo\" (required unless pr_url is given)"},
		"number": {Type: "number", Description: "Pull request number (required unless pr_url is given)"},
		"pr_url": {Type: "string", Description: "Pull request link from this Gitea instance, e.g. https://git.example.com/infra/puppet/pulls/42 (instead of repo + number)"},
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

// registerGiteaTools registers gitea.* (6 read tools, 1 comment tool that
// is refused unless the instance has gitea_allow_comments on, and 1 merge
// tool that is refused unless gitea_merge_targets lists the target).
func (r *Registry) registerGiteaTools() {
	r.giteaLimit = ratelimit.New(GiteaRatePerSecond, GiteaBurstCapacity)
	r.giteaTool = gitea.NewGiteaTool(r.logger, r.giteaLimit)
	gt := r.giteaTool

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "gitea.get_pull_request",
			Description: "Get a Gitea pull request: title, body, author, base/head branch and sha, state, mergeable, labels, reviewers, line counts.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.GetPullRequest(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "gitea.list_pull_request_files",
			Description: "List the files a Gitea pull request changes, with status (added, changed or modified, deleted, renamed) and added/deleted line counts. Use it first to plan which diffs to read.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.ListPullRequestFiles(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "gitea.get_pull_request_diff",
			Description: "Get the unified diff of a Gitea pull request, for one file (file) or for all files. Output is cut at max_bytes with a [TRUNCATED ...] marker; for a large pull request read one file at a time.",
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
			Name:        "gitea.get_file",
			Description: "Get a file from a Gitea repository at a branch, tag or commit, to read the full context around a change (e.g. the head sha of the pull request).",
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
			Name:        "gitea.list_pull_request_comments",
			Description: "List the conversation comments and the reviews (state, body, inline comments with file and line) of a Gitea pull request.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(nil)},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.ListPullRequestComments(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name:        "gitea.search_pull_requests",
			Description: "List pull requests of a Gitea repository, newest first, by state and an optional keyword query.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"repo":   {Type: "string", Description: "Repository as \"owner/repo\" (required)"},
					"state":  {Type: "string", Description: "open (default), closed or all", Enum: []string{"open", "closed", "all"}},
					"query":  {Type: "string", Description: "Keyword search in title and body"},
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
			Name:        "gitea.create_pull_request_comment",
			Description: "Post a comment on a Gitea pull request (write). Refused unless the instance has gitea_allow_comments enabled.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(map[string]mcp.Property{
				"body": {Type: "string", Description: "Comment text in Markdown (required)"},
			}), Required: []string{"body"}},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.CreatePullRequestComment(ctx, incidentID, args)
		},
	)

	r.server.RegisterTool(
		mcp.Tool{
			Name: "gitea.merge_pull_request",
			Description: "Merge a Gitea pull request (write). Refused unless the repository and base branch are in gitea_merge_targets. " +
				"Hard gates: open, not draft, mergeable, no hold label, head still equals head_sha, author is not the token user, " +
				"changed paths pass the target globs, file and line counts within limits, no reviewer requests changes.",
			InputSchema: mcp.InputSchema{Type: "object", Properties: prProps(map[string]mcp.Property{
				"head_sha": {Type: "string", Description: "Head commit sha you reviewed, from get_pull_request (required; at least 7 hex characters)"},
				"reason":   {Type: "string", Description: "One line on why it is safe to merge; goes into the merge commit message (required)"},
				"style":    {Type: "string", Description: "merge (default), rebase, rebase-merge or squash", Enum: []string{"merge", "rebase", "rebase-merge", "squash"}},
			}), Required: []string{"head_sha", "reason"}},
		},
		func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
			return gt.MergePullRequest(ctx, incidentID, args)
		},
	)

	r.logger.Println("Gitea tools registered (8 methods)")
}
