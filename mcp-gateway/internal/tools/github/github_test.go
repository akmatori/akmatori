package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testIncident = "test-incident"

const sampleDiff = `diff --git a/main.tf b/main.tf
index 1111111..2222222 100644
--- a/main.tf
+++ b/main.tf
@@ -1,3 +1,3 @@
 resource "aws_instance" "web" {
-  instance_type = "t3.small"
+  instance_type = "t3.large"
 }
diff --git a/old.tf b/new.tf
similarity index 90%
rename from old.tf
rename to new.tf
`

// newTestTool starts a fake GitHub API and puts its config in the cache.
func newTestTool(t *testing.T, cfg Config, mux *http.ServeMux) (*GitHubTool, *httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
			return
		}
		if r.Header.Get("X-GitHub-Api-Version") == "" || r.Header.Get("User-Agent") == "" {
			http.Error(w, "missing headers", http.StatusBadRequest)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mux.ServeHTTP(w, r)
	}))
	tool := NewGitHubTool(log.New(io.Discard, "", 0), nil)
	cfg.APIURL = srv.URL
	if cfg.WebURL == "" {
		cfg.WebURL = "https://github.com"
	}
	if cfg.Token == "" {
		cfg.Token = "secret-token"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5
	}
	c := cfg
	tool.configCache.Set(configCacheKey(testIncident, ""), &c)
	t.Cleanup(func() {
		tool.Stop()
		srv.Close()
	})
	return tool, srv, &seen
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func decode(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, s)
	}
	return m
}

func TestBaseURLs(t *testing.T) {
	cases := map[string][2]string{
		"":                                {"https://github.com", "https://api.github.com"},
		"https://github.com/":             {"https://github.com", "https://api.github.com"},
		"https://api.github.com":          {"https://github.com", "https://api.github.com"},
		"github.com":                      {"https://github.com", "https://api.github.com"},
		"https://octo.ghe.com":            {"https://octo.ghe.com", "https://api.octo.ghe.com"},
		"https://api.octo.ghe.com":        {"https://octo.ghe.com", "https://api.octo.ghe.com"},
		"https://ghe.example.com":         {"https://ghe.example.com", "https://ghe.example.com/api/v3"},
		"https://ghe.example.com/api/v3/": {"https://ghe.example.com", "https://ghe.example.com/api/v3"},
	}
	for in, want := range cases {
		web, api := baseURLs(in)
		if web != want[0] || api != want[1] {
			t.Errorf("%q: got (%s, %s), want %v", in, web, api, want)
		}
	}
}

func TestBuildConfig(t *testing.T) {
	c := buildConfig(map[string]interface{}{
		"github_token":          " tok ",
		"github_verify_ssl":     false,
		"github_timeout":        float64(1),
		"github_allow_comments": true,
		"github_allowed_repos":  "infra/terraform\ninfra/*",
	})
	if c.WebURL != "https://github.com" || c.APIURL != "https://api.github.com" || c.Token != "tok" || c.VerifySSL ||
		c.Timeout != 5 || !c.AllowComments || len(c.AllowedRepos) != 2 {
		t.Fatalf("unexpected config: %+v", c)
	}
	d := buildConfig(map[string]interface{}{})
	if !d.VerifySSL || d.Timeout != 30 || d.AllowComments || d.UseProxy {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestGetPullRequest_ByURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"number": 42, "title": "Bigger web nodes", "body": "why", "state": "open", "mergeable": nil,
			"mergeable_state": "unknown", "user": map[string]string{"login": "alice"},
			"requested_reviewers": []map[string]string{{"login": "bob"}},
			"requested_teams":     []map[string]string{{"slug": "sre"}},
			"base":                map[string]string{"ref": "main", "sha": "aaa"},
			"head":                map[string]string{"ref": "web-size", "sha": "bbb", "label": "alice:web-size"},
			"changed_files":       2, "additions": 1, "deletions": 1, "commits": 1,
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"pr_url": "https://github.com/infra/terraform/pull/42/files"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["title"] != "Bigger web nodes" || m["author"] != "alice" || m["changed_files"].(float64) != 2 {
		t.Fatalf("unexpected output: %s", out)
	}
	if _, ok := m["mergeable"]; ok {
		t.Fatal("a null mergeable (still computing) must be left out")
	}
	if fmt.Sprint(m["requested_reviewers"]) != "[bob team:sre]" {
		t.Fatalf("reviewers: %v", m["requested_reviewers"])
	}
	if _, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"pr_url": "https://gitlab.com/infra/terraform/pull/42"}); err == nil {
		t.Fatal("a link to another host must be refused")
	}
}

func TestGetPullRequest_Allowlist(t *testing.T) {
	tool, _, seen := newTestTool(t, Config{AllowedRepos: []string{"infra/*"}}, http.NewServeMux())
	_, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "secret/repo", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "github_allowed_repos") {
		t.Fatalf("expected an allowlist error, got %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("no request may be sent for a repo outside the allowlist: %v", *seen)
	}
}

func TestListPullRequestFiles_FollowsLinkHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=2>; rel="last"`)
			writeJSON(w, []map[string]interface{}{{"filename": "main.tf", "status": "modified", "additions": 1, "deletions": 1}})
			return
		}
		writeJSON(w, []map[string]interface{}{{"filename": "new.tf", "previous_filename": "old.tf", "status": "renamed"}})
	})
	tool, _, seen := newTestTool(t, Config{}, mux)
	out, err := tool.ListPullRequestFiles(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["count"].(float64) != 2 || len(*seen) != 2 || !strings.Contains(out, `"previous_file": "old.tf"`) {
		t.Fatalf("unexpected output (%v): %s", *seen, out)
	}
}

func TestGetPullRequestDiff_OneFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github.diff" {
			http.Error(w, "want the diff media type", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, sampleDiff)
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42), "file": "main.tf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `+  instance_type = "t3.large"`) || strings.Contains(out, "rename from") || strings.Contains(out, "TRUNCATED") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	out, err = tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42), "max_bytes": float64(60)})
	if err != nil || !strings.Contains(out, "[TRUNCATED: showed 60 of") {
		t.Fatalf("expected a truncation marker (%v):\n%s", err, out)
	}
}

func TestGetPullRequestDiff_TooLargeFallsBackToPatches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = io.WriteString(w, `{"message":"Sorry, the diff exceeded the maximum number of lines (20000)"}`)
	})
	mux.HandleFunc("/repos/infra/terraform/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{
			{"filename": "main.tf", "status": "modified", "patch": "@@ -1 +1 @@\n-a\n+b"},
			{"filename": "logo.png", "status": "added"},
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "built from the per-file patches") || !strings.Contains(out, "+b") || !strings.Contains(out, "no text diff") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	one, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42), "file": "main.tf"})
	if err != nil || !strings.Contains(one, "--- a/main.tf") || strings.Contains(one, "logo.png") {
		t.Fatalf("one file from patches (%v):\n%s", err, one)
	}
}

func TestGetFile_RawDirectoryAndBinary(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/contents/modules/web/main.tf", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") != "bbb" || !strings.Contains(r.Header.Get("Accept"), "raw") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.github.raw")
		_, _ = io.WriteString(w, "héllo world")
	})
	mux.HandleFunc("/repos/infra/terraform/contents/modules", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"name": "web", "path": "modules/web", "type": "dir"}, {"name": "x.tf", "path": "modules/x.tf", "type": "file", "size": 12}})
	})
	mux.HandleFunc("/repos/infra/terraform/contents/logo.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0, 0, 1})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetFile(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "path": "modules/web/main.tf", "ref": "bbb", "max_bytes": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n\nh\n") || !strings.Contains(out, "showed the first 1 bytes") {
		t.Fatalf("unexpected output:\n%q", out)
	}
	dir, err := tool.GetFile(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "path": "modules"})
	if err != nil || !strings.Contains(dir, `"type": "dir"`) || !strings.Contains(dir, "modules/x.tf") {
		t.Fatalf("directory listing (%v):\n%s", err, dir)
	}
	bin, err := tool.GetFile(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "path": "logo.png"})
	if err != nil || !strings.Contains(bin, "binary file") {
		t.Fatalf("binary (%v): %s", err, bin)
	}
	if _, err := tool.GetFile(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "path": "../etc/passwd"}); err == nil {
		t.Fatal("a .. path must be refused")
	}
}

func TestListPullRequestComments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"id": 1, "body": "LGTM?", "user": map[string]string{"login": "carol"}}})
	})
	mux.HandleFunc("/repos/infra/terraform/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"id": 7, "state": "CHANGES_REQUESTED", "body": "see inline", "user": map[string]string{"login": "bob"}}})
	})
	mux.HandleFunc("/repos/infra/terraform/pulls/42/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{
			{"id": 70, "pull_request_review_id": 7, "path": "main.tf", "line": 2, "body": "too big", "user": map[string]string{"login": "bob"}},
			{"id": 71, "pull_request_review_id": 9, "path": "main.tf", "line": nil, "original_line": 1, "body": "old", "user": map[string]string{"login": "dan"}},
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.ListPullRequestComments(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	revs := m["reviews"].([]interface{})
	inline := revs[0].(map[string]interface{})["inline_comments"].([]interface{})
	if len(m["comments"].([]interface{})) != 1 || len(inline) != 1 || inline[0].(map[string]interface{})["body"] != "too big" {
		t.Fatalf("unexpected output: %s", out)
	}
	other := m["other_inline_comments"].([]interface{})
	if len(other) != 1 || other[0].(map[string]interface{})["outdated"] != true {
		t.Fatalf("an inline comment of an unlisted review must be kept and marked outdated: %s", out)
	}
}

func TestSearchPullRequests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "repo:infra/terraform") || !strings.Contains(q, "is:pr") || !strings.Contains(q, "is:closed") ||
			!strings.Contains(q, `label:"needs review"`) || !strings.Contains(q, "author:alice") || strings.Contains(q, "repo:secret") {
			http.Error(w, "bad query "+q, http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, map[string]interface{}{"total_count": 45, "items": []map[string]interface{}{
			{"number": 42, "title": "web", "state": "closed", "repository_url": "https://api.github.com/repos/infra/terraform", "pull_request": map[string]interface{}{"merged_at": "2026-11-01T00:00:00Z"}},
			{"number": 1, "title": "leak", "state": "open", "repository_url": "https://api.github.com/repos/secret/repo"},
		}})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.SearchPullRequests(context.Background(), testIncident, map[string]interface{}{
		"repo": "infra/terraform", "state": "closed", "labels": "needs review", "author": "alice",
		"query": "instance repo:secret/repo", "limit": float64(20),
	})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["count"].(float64) != 1 || m["next_page"].(float64) != 2 || strings.Contains(out, "leak") {
		t.Fatalf("unexpected output: %s", out)
	}
	if fmt.Sprint(m["ignored_query_terms"]) != "[repo:secret/repo]" {
		t.Fatalf("a repo: qualifier must be dropped and reported: %s", out)
	}
	if _, err := tool.SearchPullRequests(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "author": "a b"}); err == nil {
		t.Fatal("an author that is not a login must be refused")
	}
}

func commentMux(posted *string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*posted = body["body"]
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]interface{}{"id": 99, "html_url": "https://github.com/infra/terraform/pull/42#issuecomment-99"})
	})
	return mux
}

func TestCreatePullRequestComment_Gate(t *testing.T) {
	var posted string
	off, _, seen := newTestTool(t, Config{}, commentMux(&posted))
	_, err := off.CreatePullRequestComment(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42), "body": "hi"})
	if err == nil || !strings.Contains(err.Error(), "github_allow_comments") || len(*seen) != 0 {
		t.Fatalf("comments must be refused by default, got %v (%v)", err, *seen)
	}
	on, _, _ := newTestTool(t, Config{AllowComments: true}, commentMux(&posted))
	out, err := on.CreatePullRequestComment(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(42), "body": "Review: looks fine"})
	if err != nil || posted != "Review: looks fine" || !strings.Contains(out, `"comment_id": 99`) {
		t.Fatalf("got %v, posted %q, out %s", err, posted, out)
	}
}

func TestErrors_RateLimitAndNoToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/infra/terraform/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1790000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"API rate limit exceeded"}`)
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	_, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "rate limit") || !strings.Contains(err.Error(), "resets at") {
		t.Fatalf("expected a rate limit error, got %v", err)
	}
	bad, _, _ := newTestTool(t, Config{Token: "wrong-token"}, mux)
	_, err = bad.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/terraform", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("expected a 401 without the token, got %v", err)
	}
}

func TestDo_RedirectSameHostOnly(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the token must never reach another host (Authorization=%q)", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/old/name/pulls/3", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/repositories/123/pulls/3", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/repositories/123/pulls/3", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"number": 3, "title": "moved"})
	})
	mux.HandleFunc("/repos/evil/x/pulls/4", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "old/name", "number": float64(3)})
	if err != nil || !strings.Contains(out, "moved") {
		t.Fatalf("a same-host redirect (renamed repo) must be followed: %v %s", err, out)
	}
	_, err = tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "evil/x", "number": float64(4)})
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("a redirect to another host must be refused, got %v", err)
	}
}
