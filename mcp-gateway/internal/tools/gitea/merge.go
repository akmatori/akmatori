package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/validation"
)

// Merging is off unless gitea_merge_targets names the repository and base
// branch. The agent decides whether a pull request is good; the gates below
// are hard limits it cannot talk its way past.

const (
	DefaultMergeMaxFiles = 10
	DefaultMergeMaxLines = 300
	maxMergeReasonBytes  = 4_000
)

// holdLabels block a merge (compared lowercased).
var holdLabels = map[string]bool{
	"hold":          true,
	"on-hold":       true,
	"do-not-merge":  true,
	"do not merge":  true,
	"no-auto-merge": true,
	"wip":           true,
}

// MergeTarget is one line of gitea_merge_targets:
//
//	owner/repo:branch [glob ...] [!glob ...]
//
// A changed file must match at least one plain glob (when there are any) and
// no "!" glob. "**" matches across directories, "*" and "?" do not.
type MergeTarget struct {
	Repo   string // lowercased owner/repo
	Branch string // exact base branch name
	Allow  []string
	Deny   []string
}

// ParseMergeTargets reads gitea_merge_targets (one target per line, "#"
// starts a comment). Bad lines are skipped and returned as errors so the
// operator sees them.
func ParseMergeTargets(v interface{}) ([]MergeTarget, []string) {
	var lines []string
	switch val := v.(type) {
	case string:
		lines = strings.Split(val, "\n")
	case []interface{}:
		for _, item := range val {
			if s, ok := item.(string); ok {
				lines = append(lines, s)
			}
		}
	case []string:
		lines = val
	}
	var out []MergeTarget
	var bad []string
	for _, line := range lines {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		head := fields[0]
		i := strings.Index(head, ":")
		if i <= 0 || i == len(head)-1 {
			bad = append(bad, fmt.Sprintf("%q: expected owner/repo:branch", strings.TrimSpace(line)))
			continue
		}
		owner, name, err := splitRepo(head[:i])
		if err != nil {
			bad = append(bad, fmt.Sprintf("%q: %v", strings.TrimSpace(line), err))
			continue
		}
		t := MergeTarget{Repo: strings.ToLower(owner + "/" + name), Branch: head[i+1:]}
		for _, g := range fields[1:] {
			if strings.HasPrefix(g, "!") {
				if g = strings.TrimPrefix(g, "!"); g != "" {
					t.Deny = append(t.Deny, g)
				}
			} else {
				t.Allow = append(t.Allow, g)
			}
		}
		out = append(out, t)
	}
	return out, bad
}

// findTarget returns the target for a repository and base branch.
func findTarget(cfg *Config, owner, name, branch string) *MergeTarget {
	repo := strings.ToLower(owner + "/" + name)
	for i := range cfg.MergeTargets {
		if cfg.MergeTargets[i].Repo == repo && cfg.MergeTargets[i].Branch == branch {
			return &cfg.MergeTargets[i]
		}
	}
	return nil
}

// globRegexp turns a path glob into an anchored regexp. It reads the glob
// as runes (non-ASCII literals stay intact), and the (?s) flag lets "**"
// match a newline, which Git allows in a path.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?s)^")
	g := []rune(strings.TrimPrefix(glob, "/"))
	for i := 0; i < len(g); i++ {
		rest := string(g[i:])
		switch {
		case strings.HasPrefix(rest, "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(rest, "**"):
			b.WriteString(".*")
			i++
		case g[i] == '*':
			b.WriteString("[^/]*")
		case g[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// pathAllowed applies a target's globs to one changed path.
func (mt *MergeTarget) pathAllowed(p string) bool {
	for _, d := range mt.Deny {
		if globRegexp(d).MatchString(p) {
			return false
		}
	}
	if len(mt.Allow) == 0 {
		return true
	}
	for _, a := range mt.Allow {
		if globRegexp(a).MatchString(p) {
			return true
		}
	}
	return false
}

// verifyMergeGate re-reads the instance settings from the database (not the
// 5-minute cache), so emptying gitea_merge_targets takes effect at once.
func (t *GiteaTool) verifyMergeGate(ctx context.Context, incidentID, logicalName string, cached *Config) (*Config, error) {
	cfg := cached
	if database.DB != nil {
		creds, err := database.ResolveToolCredentials(ctx, incidentID, toolType, nil, logicalName)
		if err != nil {
			return nil, fmt.Errorf("failed to verify the Gitea merge setting: %w", err)
		}
		cfg = buildConfig(creds.Settings)
		t.configCache.Set(configCacheKey(incidentID, logicalName), cfg)
	}
	if cfg == nil || len(cfg.MergeTargets) == 0 {
		return cfg, fmt.Errorf("merging is disabled for this Gitea instance; an operator must list the repository and base branch in gitea_merge_targets")
	}
	return cfg, nil
}

// A short prefix is not safe: commits that share 7 hex characters are cheap
// to make, so a benign commit could be reviewed and a twin merged.
var shaRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// MergePullRequest merges a pull request after hard gates:
//   - the repository and base branch are in gitea_merge_targets
//   - the pull request is open, not a draft, mergeable, has no hold label
//   - its head is exactly the head_sha the agent reviewed (Gitea re-checks it)
//   - the token user is not the author (no self-approval loop)
//   - every changed path passes the target's globs, and the file and line
//     counts are within gitea_merge_max_files / gitea_merge_max_lines
//   - no reviewer's latest review is "request changes"
//
// The state, target, head and label gates run again just before the merge,
// and the paths are checked both on the live file list and on the commits
// pinned by the reviewed sha.
func (t *GiteaTool) MergePullRequest(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	cfg, err := t.getConfig(ctx, incidentID, logicalName)
	if err != nil {
		return "", err
	}
	cfg, err = t.verifyMergeGate(ctx, incidentID, logicalName, cfg)
	if err != nil {
		return "", err
	}
	p, err := resolvePR(cfg, args)
	if err != nil {
		return "", err
	}
	headSha := strings.ToLower(stringArg(args, "head_sha"))
	if !shaRe.MatchString(headSha) {
		return "", fmt.Errorf("head_sha is required: the full head commit sha you reviewed (40 or 64 hex characters, head.sha from get_pull_request)%s", validation.SuggestParam("head_sha", args))
	}
	reason := stringArg(args, "reason")
	if reason == "" {
		return "", fmt.Errorf("reason is required: one line on why this pull request is safe to merge (goes into the merge commit)%s", validation.SuggestParam("reason", args))
	}
	if len(reason) > maxMergeReasonBytes {
		return "", fmt.Errorf("reason is %d bytes; the limit is %d", len(reason), maxMergeReasonBytes)
	}
	style := strings.ToLower(stringArg(args, "style"))
	switch style {
	case "":
		style = "merge"
	case "merge", "rebase", "rebase-merge", "squash":
	default:
		return "", fmt.Errorf("style must be merge, rebase, rebase-merge or squash, got %q", style)
	}

	base := repoPath(p.owner, p.name)
	refuse := func(format string, a ...interface{}) (string, error) {
		return "", fmt.Errorf("merge refused for %s#%d: %s", p.repo(), p.number, fmt.Sprintf(format, a...))
	}

	pr, target, why, err := t.readAndCheckPR(ctx, cfg, p, headSha)
	if err != nil {
		return "", err
	}
	if why != "" {
		return refuse("%s", why)
	}
	currentHead := strings.ToLower(pr.Head.Sha)

	var me apiUser
	if err := t.getJSON(ctx, cfg, "/api/v1/user", nil, &me); err != nil {
		return "", fmt.Errorf("read the token user (the token needs read:user): %w", err)
	}
	if me.Login == "" || strings.EqualFold(me.Login, login(pr.User)) {
		return refuse("the author %q is the token user; it cannot merge its own pull request", login(pr.User))
	}

	// The live file list follows the pull request ref, which can move while we
	// read it. The pinned list comes from compare at the reviewed sha. Both
	// must pass, and the larger line count counts.
	files, capped, err := getPaged[apiChangedFile](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/files", base, p.number), nil)
	if err != nil {
		return "", err
	}
	if capped {
		return refuse("it changes more files than can be listed")
	}
	pinned, pinnedLines, why, err := t.pinnedFiles(ctx, cfg, base, pr.MergeBase, currentHead)
	if err != nil {
		return "", err
	}
	if why != "" {
		return refuse("%s", why)
	}
	liveRenamed := map[string]bool{}
	paths := map[string]bool{}
	lines := 0
	for _, f := range files {
		lines += f.Additions + f.Deletions
		paths[f.Filename] = true
		if f.PreviousFilename != "" {
			paths[f.PreviousFilename] = true
			liveRenamed[f.Filename] = true
		}
	}
	for _, f := range pinned {
		paths[f.Filename] = true
		// compare gives no old path for a rename, so the live list must show it.
		if f.Status == "renamed" && !liveRenamed[f.Filename] {
			return refuse("the reviewed commits rename %q, but the old path is not visible; split the rename into its own pull request", f.Filename)
		}
	}
	if pinnedLines > lines {
		lines = pinnedLines
	}
	if len(paths) == 0 {
		return refuse("it changes no files")
	}
	changed := len(files)
	if len(pinned) > changed {
		changed = len(pinned)
	}
	if changed > cfg.MergeMaxFiles {
		return refuse("it changes %d files; gitea_merge_max_files is %d", changed, cfg.MergeMaxFiles)
	}
	var blocked []string
	for path := range paths {
		if !target.pathAllowed(path) {
			blocked = append(blocked, path)
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return refuse("these paths are not allowed for %s:%s: %s", target.Repo, target.Branch, strings.Join(blocked, ", "))
	}
	if lines > cfg.MergeMaxLines {
		return refuse("it changes %d lines; gitea_merge_max_lines is %d", lines, cfg.MergeMaxLines)
	}

	reviews, reviewsCapped, err := getPaged[apiReview](ctx, t, cfg, fmt.Sprintf("%s/pulls/%d/reviews", base, p.number), nil)
	if err != nil {
		return "", err
	}
	if reviewsCapped {
		return refuse("it has more reviews than can be listed, so a request for changes could be missed")
	}
	latest := map[string]string{}
	for _, r := range reviews {
		if r.Dismissed {
			continue
		}
		switch r.State {
		case "APPROVED", "REQUEST_CHANGES":
			latest[login(r.User)] = r.State
		}
	}
	var against []string
	for who, state := range latest {
		if state == "REQUEST_CHANGES" {
			against = append(against, who)
		}
	}
	if len(against) > 0 {
		sort.Strings(against)
		return refuse("changes requested by %s", strings.Join(against, ", "))
	}

	// Re-read just before the merge: base, labels and state can change while
	// the checks run, and Gitea's merge call only pins the head.
	if _, _, why, err := t.readAndCheckPR(ctx, cfg, p, currentHead); err != nil {
		return "", err
	} else if why != "" {
		return refuse("the pull request changed during the checks: %s", why)
	}

	msg := fmt.Sprintf("%s\n\nMerged by an agent via gitea.merge_pull_request after review of head %s.", reason, shortSha(currentHead))
	body := map[string]interface{}{
		"Do":                        style,
		"head_commit_id":            pr.Head.Sha, // Gitea refuses the merge if the head moved after this read
		"MergeMessageField":         msg,
		"delete_branch_after_merge": false,
	}
	if _, _, _, err := t.do(ctx, cfg, http.MethodPost, fmt.Sprintf("%s/pulls/%d/merge", base, p.number), nil, body, "application/json", maxJSONResponseBytes); err != nil {
		var ae *apiError
		errors.As(err, &ae)
		if ae != nil && ae.Status == http.StatusConflict {
			return refuse("the head moved during the merge; review the new head first")
		}
		if ae != nil && ae.Status == http.StatusMethodNotAllowed {
			return refuse("Gitea refused it (branch protection, required approvals or checks): %s", ae.Body)
		}
		return "", err
	}

	var after apiPR
	out := map[string]interface{}{
		"repo":           p.repo(),
		"number":         p.number,
		"base":           pr.Base.Ref,
		"merged_head":    pr.Head.Sha,
		"style":          style,
		"files":          changed,
		"changed_lines":  lines,
		"html_url":       pr.HTMLURL,
		"merged_by_user": me.Login,
	}
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d", base, p.number), nil, &after); err == nil {
		out["merged"] = after.Merged
		out["merge_commit_sha"] = after.MergeCommitSha
	} else {
		out["merged"] = true
		out["note"] = "merge accepted; re-reading the pull request failed: " + err.Error()
	}
	return marshal(out)
}

// readAndCheckPR reads the pull request and applies the state, target,
// head and label gates. A refusal comes back as a reason, not an error.
func (t *GiteaTool) readAndCheckPR(ctx context.Context, cfg *Config, p prTarget, headSha string) (*apiPR, *MergeTarget, string, error) {
	var pr apiPR
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/pulls/%d", repoPath(p.owner, p.name), p.number), nil, &pr); err != nil {
		return nil, nil, "", err
	}
	switch {
	case pr.Merged:
		return nil, nil, "it is already merged", nil
	case pr.State != "open":
		return nil, nil, fmt.Sprintf("it is %s", pr.State), nil
	case pr.Draft:
		return nil, nil, "it is a draft", nil
	case pr.Base == nil || pr.Head == nil || pr.Head.Sha == "":
		return nil, nil, "Gitea returned no base or head", nil
	}
	target := findTarget(cfg, p.owner, p.name, pr.Base.Ref)
	if target == nil {
		return nil, nil, fmt.Sprintf("base branch %q of %s is not in gitea_merge_targets", pr.Base.Ref, p.repo()), nil
	}
	if cur := strings.ToLower(pr.Head.Sha); cur != headSha {
		return nil, nil, fmt.Sprintf("the head is now %s, not the reviewed %s; review the new head first", shortSha(cur), shortSha(headSha)), nil
	}
	for _, l := range pr.Labels {
		if holdLabels[strings.ToLower(strings.TrimSpace(l.Name))] {
			return nil, nil, fmt.Sprintf("it has the label %q", l.Name), nil
		}
	}
	if !pr.Mergeable {
		return nil, nil, "Gitea reports it is not mergeable (conflicts or failed checks)", nil
	}
	return &pr, target, "", nil
}

type apiCompare struct {
	TotalCommits int `json:"total_commits"`
	Commits      []struct {
		SHA   string `json:"sha"`
		Files []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"`
		} `json:"files"`
		Stats *struct {
			Additions int `json:"additions"`
			Deletions int `json:"deletions"`
		} `json:"stats"`
	} `json:"commits"`
}

type pinnedFile struct {
	Filename string
	Status   string
}

// pinnedFiles lists the files that the commits between the merge base and
// the reviewed head change, from immutable shas. Lines are the sum over
// commits, which can only over-count.
func (t *GiteaTool) pinnedFiles(ctx context.Context, cfg *Config, base, mergeBase, head string) ([]pinnedFile, int, string, error) {
	if !shaRe.MatchString(mergeBase) {
		return nil, 0, "Gitea gave no merge base for the pull request", nil
	}
	var cmp apiCompare
	if err := t.getJSON(ctx, cfg, fmt.Sprintf("%s/compare/%s...%s", base, strings.ToLower(mergeBase), head), nil, &cmp); err != nil {
		return nil, 0, "", fmt.Errorf("compare the reviewed commits: %w", err)
	}
	if len(cmp.Commits) == 0 || len(cmp.Commits) < cmp.TotalCommits {
		return nil, 0, fmt.Sprintf("compare listed %d of %d commits", len(cmp.Commits), cmp.TotalCommits), nil
	}
	seen := map[string]int{}
	var out []pinnedFile
	lines := 0
	for _, c := range cmp.Commits {
		if c.Stats == nil {
			return nil, 0, fmt.Sprintf("compare gave no line counts for commit %s", shortSha(c.SHA)), nil
		}
		lines += c.Stats.Additions + c.Stats.Deletions
		for _, f := range c.Files {
			if i, ok := seen[f.Filename]; ok {
				if f.Status == "renamed" {
					out[i].Status = "renamed"
				}
				continue
			}
			seen[f.Filename] = len(out)
			out = append(out, pinnedFile{Filename: f.Filename, Status: f.Status})
		}
	}
	return out, lines, "", nil
}

func shortSha(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}
