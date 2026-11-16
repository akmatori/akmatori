package gitea

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testIncident = "test-incident"

const sampleDiff = `diff --git a/hieradata/prod.yaml b/hieradata/prod.yaml
index 1111111..2222222 100644
--- a/hieradata/prod.yaml
+++ b/hieradata/prod.yaml
@@ -1,3 +1,3 @@
 nginx::workers: 4
-nginx::keepalive: 60
+nginx::keepalive: 120
 nginx::gzip: true
diff --git a/manifests/old.pp b/manifests/new.pp
similarity index 90%
rename from manifests/old.pp
rename to manifests/new.pp
--- a/manifests/old.pp
+++ b/manifests/new.pp
@@ -1 +1 @@
-class old {}
+class new {}
`

// newTestTool starts a fake Gitea and puts its config in the cache.
func newTestTool(t *testing.T, cfg Config, mux *http.ServeMux) (*GiteaTool, *httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "token secret-token" {
			http.Error(w, `{"message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mux.ServeHTTP(w, r)
	}))
	tool := NewGiteaTool(log.New(io.Discard, "", 0), nil)
	cfg.URL = srv.URL
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
	w.Header().Set("Content-Type", "application/json")
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

func TestParseAllowedRepos(t *testing.T) {
	got := ParseAllowedRepos("Infra/Puppet\n# comment\ninfra/*, ops/alerts  # trailing\n\n")
	want := []string{"infra/puppet", "infra/*", "ops/alerts"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = ParseAllowedRepos([]interface{}{"a/b", 3, " c/d/ "})
	if strings.Join(got, "|") != "a/b|c/d" {
		t.Fatalf("array form: got %v", got)
	}
	if len(ParseAllowedRepos(nil)) != 0 {
		t.Fatal("nil must give no entries")
	}
}

func TestBuildConfig(t *testing.T) {
	c := buildConfig(map[string]interface{}{
		"gitea_url":            "https://git.example.com/api/v1/",
		"gitea_token":          " tok ",
		"gitea_verify_ssl":     false,
		"gitea_timeout":        float64(1),
		"gitea_allow_comments": true,
		"gitea_allowed_repos":  "infra/puppet",
	})
	if c.URL != "https://git.example.com" || c.Token != "tok" || c.VerifySSL || c.Timeout != 5 || !c.AllowComments || len(c.AllowedRepos) != 1 {
		t.Fatalf("unexpected config: %+v", c)
	}
	d := buildConfig(map[string]interface{}{})
	if !d.VerifySSL || d.Timeout != 30 || d.AllowComments {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestSplitRepo(t *testing.T) {
	for _, ok := range []string{"infra/puppet", "a.b/c-d_e", "/infra/puppet/", "infra/puppet.git"} {
		if _, _, err := splitRepo(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"", "infra", "a/b/c", "../x", "a/..", "a b/c", "a/%2e%2e"} {
		if _, _, err := splitRepo(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestRepoAllowed(t *testing.T) {
	c := &Config{AllowedRepos: []string{"infra/puppet", "ops/*"}}
	cases := map[string]bool{"infra/puppet": true, "Infra/Puppet": true, "infra/other": false, "ops/anything": true, "opsx/a": false}
	for repo, want := range cases {
		o, n, _ := splitRepo(repo)
		if got := repoAllowed(c, o, n); got != want {
			t.Errorf("%s: got %v, want %v", repo, got, want)
		}
	}
	if !repoAllowed(&Config{}, "any", "repo") {
		t.Error("an empty allowlist must allow every repo")
	}
}

func TestParsePRURL(t *testing.T) {
	cfg := &Config{URL: "https://git.example.com"}
	p, err := parsePRURL(cfg, "https://git.example.com/infra/puppet/pulls/42/files")
	if err != nil || p.repo() != "infra/puppet" || p.number != 42 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if _, err := parsePRURL(cfg, "https://evil.example.org/infra/puppet/pulls/42"); err == nil {
		t.Fatal("a link to another host must be refused")
	}
	if _, err := parsePRURL(cfg, "https://git.example.com/infra/puppet/issues/42"); err == nil {
		t.Fatal("an issue link must be refused")
	}
	sub := &Config{URL: "https://example.com/git"}
	p, err = parsePRURL(sub, "https://example.com/git/infra/puppet/pulls/7")
	if err != nil || p.number != 7 || p.repo() != "infra/puppet" {
		t.Fatalf("sub-path: got %+v, %v", p, err)
	}
}

func TestSplitDiff(t *testing.T) {
	order, secs := splitDiff(sampleDiff)
	if len(order) != 2 || order[0] != "hieradata/prod.yaml" || order[1] != "manifests/new.pp" {
		t.Fatalf("order = %v", order)
	}
	if !strings.Contains(secs["hieradata/prod.yaml"], "+nginx::keepalive: 120") || strings.Contains(secs["hieradata/prod.yaml"], "class new") {
		t.Fatalf("wrong section: %q", secs["hieradata/prod.yaml"])
	}
	if secs["manifests/old.pp"] != secs["manifests/new.pp"] {
		t.Fatal("a renamed file must be found by its old path too")
	}
}

func TestGetPullRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"number": 42, "title": "Raise keepalive", "body": "tested on preprod", "state": "open",
			"mergeable": true, "user": map[string]string{"login": "alice"},
			"labels":    []map[string]string{{"name": "prod"}},
			"base":      map[string]string{"ref": "main", "sha": "aaa"},
			"head":      map[string]string{"ref": "keepalive", "sha": "bbb"},
			"additions": 1, "deletions": 1, "changed_files": 2,
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["title"] != "Raise keepalive" || m["author"] != "alice" || m["mergeable"] != true || m["changed_files"].(float64) != 2 {
		t.Fatalf("unexpected output: %s", out)
	}
	if m["head"].(map[string]interface{})["sha"] != "bbb" {
		t.Fatalf("head sha missing: %s", out)
	}
}

func TestGetPullRequest_PRURLAndAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"number": 42, "title": "x"})
	})
	tool, srv, seen := newTestTool(t, Config{AllowedRepos: []string{"infra/puppet"}}, mux)
	if _, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"pr_url": srv.URL + "/infra/puppet/pulls/42"}); err != nil {
		t.Fatalf("pr_url: %v", err)
	}
	_, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/secrets", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "gitea_allowed_repos") {
		t.Fatalf("expected an allowlist error, got %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("a refused repo must not reach Gitea; calls: %v", *seen)
	}
}

func TestGetPullRequest_MissingArgs(t *testing.T) {
	tool, _, _ := newTestTool(t, Config{}, http.NewServeMux())
	if _, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet"}); err == nil {
		t.Fatal("expected an error without number")
	}
	if _, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"number": float64(1)}); err == nil {
		t.Fatal("expected an error without repo")
	}
}

func TestListPullRequestFiles_PagesByTotalCount(t *testing.T) {
	mux := http.NewServeMux()
	// The server caps pages at 2 items (MAX_RESPONSE_ITEMS lower than our limit).
	all := []map[string]interface{}{
		{"filename": "a", "status": "modified", "additions": 1, "deletions": 0},
		{"filename": "b", "status": "added", "additions": 5, "deletions": 0},
		{"filename": "c", "status": "renamed", "previous_filename": "c0", "additions": 0, "deletions": 2},
	}
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("X-Total-Count", "3")
		switch page {
		case "1":
			writeJSON(w, all[:2])
		case "2":
			writeJSON(w, all[2:])
		default:
			writeJSON(w, []interface{}{})
		}
	})
	tool, _, seen := newTestTool(t, Config{}, mux)
	out, err := tool.ListPullRequestFiles(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": "#42"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["count"].(float64) != 3 || m["additions"].(float64) != 6 || m["deletions"].(float64) != 2 {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, `"previous_file": "c0"`) {
		t.Fatalf("rename source missing: %s", out)
	}
	if len(*seen) != 2 {
		t.Fatalf("expected 2 page calls, got %v", *seen)
	}
}

func diffMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42.diff", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sampleDiff)
	})
	return mux
}

func TestGetPullRequestDiff_OneFile(t *testing.T) {
	tool, _, _ := newTestTool(t, Config{}, diffMux())
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "file": "hieradata/prod.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+nginx::keepalive: 120") || strings.Contains(out, "class new") || strings.Contains(out, "TRUNCATED") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestGetPullRequestDiff_UnknownFileListsFiles(t *testing.T) {
	tool, _, _ := newTestTool(t, Config{}, diffMux())
	_, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "file": "nope"})
	if err == nil || !strings.Contains(err.Error(), "hieradata/prod.yaml") {
		t.Fatalf("expected an error listing the files, got %v", err)
	}
}

func TestGetPullRequestDiff_Truncates(t *testing.T) {
	tool, _, _ := newTestTool(t, Config{}, diffMux())
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "max_bytes": float64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[TRUNCATED: showed 100 of") || strings.Contains(out, "class new") {
		t.Fatalf("expected a truncation marker:\n%s", out)
	}
}

func TestGetFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/raw/manifests/site.pp", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") != "bbb" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "node default {}\n")
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/raw/big.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 500))
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/raw/bin.dat", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0x7f, 'E', 'L', 'F', 0, 1})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	ctx := context.Background()

	out, err := tool.GetFile(ctx, testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "manifests/site.pp", "ref": "bbb"})
	if err != nil || !strings.Contains(out, "node default {}") {
		t.Fatalf("got %q, %v", out, err)
	}
	out, err = tool.GetFile(ctx, testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "big.txt", "max_bytes": float64(100)})
	if err != nil || !strings.Contains(out, "[TRUNCATED: showed the first 100 bytes") {
		t.Fatalf("got %q, %v", out, err)
	}
	out, err = tool.GetFile(ctx, testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "bin.dat"})
	if err != nil || !strings.Contains(out, "[binary file") {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := tool.GetFile(ctx, testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "a/../../x"}); err == nil {
		t.Fatal("a path with .. must be refused")
	}
	_, err = tool.GetFile(ctx, testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "manifests/site.pp", "ref": "nope"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected a 404 error, got %v", err)
	}
}

func TestListPullRequestComments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"id": 1, "body": "please review, tested on preprod", "user": map[string]string{"login": "alice"}}})
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{
			{"id": 7, "state": "REQUEST_CHANGES", "body": "rollback?", "comments_count": 1, "user": map[string]string{"login": "bob"}},
			{"id": 8, "state": "APPROVED", "comments_count": 0},
		})
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/reviews/7/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]interface{}{{"body": "why 120?", "path": "hieradata/prod.yaml", "position": 3, "user": map[string]string{"login": "bob"}}})
	})
	tool, _, seen := newTestTool(t, Config{}, mux)
	out, err := tool.ListPullRequestComments(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tested on preprod", "REQUEST_CHANGES", "why 120?", "hieradata/prod.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, c := range *seen {
		if strings.Contains(c, "/reviews/8/") {
			t.Errorf("a review without comments must not be fetched: %v", *seen)
		}
	}
}

func TestSearchPullRequests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type") != "pulls" || q.Get("state") != "all" || q.Get("q") != "keepalive" || q.Get("limit") != "2" {
			http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		writeJSON(w, []map[string]interface{}{
			{"number": 42, "title": "Raise keepalive", "state": "open", "pull_request": map[string]interface{}{"merged": false}},
			{"number": 40, "title": "Lower keepalive", "state": "closed", "pull_request": map[string]interface{}{"merged": true}},
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.SearchPullRequests(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "state": "all", "query": "keepalive", "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["count"].(float64) != 2 || m["next_page"].(float64) != 2 {
		t.Fatalf("unexpected output: %s", out)
	}
	if _, err := tool.SearchPullRequests(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "state": "merged"}); err == nil {
		t.Fatal("an unknown state must be refused")
	}
}

func commentMux(posted *string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*posted = body["body"]
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]interface{}{"id": 99, "html_url": "https://git/x#issuecomment-99"})
	})
	return mux
}

func TestCreatePullRequestComment_DisabledByDefault(t *testing.T) {
	var posted string
	tool, _, seen := newTestTool(t, Config{}, commentMux(&posted))
	_, err := tool.CreatePullRequestComment(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "body": "LGTM"})
	if err == nil || !strings.Contains(err.Error(), "gitea_allow_comments") {
		t.Fatalf("expected the comment gate error, got %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused comment must not reach Gitea: %v", *seen)
	}
}

func TestCreatePullRequestComment_Allowed(t *testing.T) {
	var posted string
	tool, _, _ := newTestTool(t, Config{AllowComments: true}, commentMux(&posted))
	out, err := tool.CreatePullRequestComment(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "body": "Scope: prod nginx only. Rollback: revert."})
	if err != nil {
		t.Fatal(err)
	}
	if posted != "Scope: prod nginx only. Rollback: revert." || !strings.Contains(out, `"comment_id": 99`) {
		t.Fatalf("posted %q, output %s", posted, out)
	}
}

func TestErrors_NoTokenLeak(t *testing.T) {
	mux := http.NewServeMux()
	tool, _, _ := newTestTool(t, Config{Token: "wrong-token"}, mux)
	_, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Fatal("the error must not contain the token")
	}
}

func TestDo_RefusesRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect target must not be called (Authorization %q)", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	_, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(1)})
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("expected an HTTP 302 error, got %v", err)
	}
}

func TestCut_RuneBoundary(t *testing.T) {
	s, short := cut("héllo", 2)
	if !short || s != "h" {
		t.Fatalf("got %q, %v", s, short)
	}
	if s, short := cut("abc", 10); short || s != "abc" {
		t.Fatalf("got %q, %v", s, short)
	}
}

func TestGetPullRequestDiff_OneFileTruncatedHint(t *testing.T) {
	tool, _, _ := newTestTool(t, Config{}, diffMux())
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "file": "hieradata/prod.yaml", "max_bytes": float64(50)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Raise max_bytes") || strings.Contains(out, "list_pull_request_files") {
		t.Fatalf("a one-file diff must not point to list_pull_request_files:\n%s", out)
	}
}

func TestGetPullRequest_ZeroCountsOmitted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/5", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"number": 5, "merged": true, "additions": 0, "deletions": 0, "changed_files": 0})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequest(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "changed_files") || strings.Contains(out, "additions") {
		t.Fatalf("zero counts mean unknown and must be left out: %s", out)
	}
}

func TestCut_NotUTF8KeepsText(t *testing.T) {
	latin1 := "caf\xe9 caf\xe9 caf\xe9"
	s, short := cut(latin1, 10)
	if !short || len(s) != 10 {
		t.Fatalf("non-UTF-8 text must be cut at n, got %q", s)
	}
}

func TestDiffGitNames_Quoting(t *testing.T) {
	cases := []struct {
		header string
		want   []string
	}{
		{`diff --git a/x.pp b/x.pp`, []string{"x.pp"}},
		{`diff --git a/old.pp b/new.pp`, []string{"new.pp", "old.pp"}},
		{`diff --git "a/x y.pp" "b/x y.pp"`, []string{"x y.pp"}},
		{`diff --git a/plain.txt "b/caf\303\251.txt"`, []string{"café.txt", "plain.txt"}},
		{`diff --git "a/caf\303\251.txt" b/plain.txt`, []string{"plain.txt", "café.txt"}},
		{`diff --git "a/q\"uote.txt" b/q.txt`, []string{"q.txt", `q"uote.txt`}},
	}
	for _, c := range cases {
		got := diffGitNames(c.header)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.header, got, c.want)
		}
	}
}

func TestGetPullRequestDiff_OneFileCutByDownloadCap(t *testing.T) {
	// The first file fills the download cap except for a few bytes, so the
	// second file's diff is read only in part but fits max_bytes.
	first := "diff --git a/big.txt b/big.txt\n--- a/big.txt\n+++ b/big.txt\n@@ -0,0 +1 @@\n+"
	pad := maxRawResponseBytes - len(first) - 1 - 60
	diff := first + strings.Repeat("x", pad) + "\n" +
		"diff --git a/small.txt b/small.txt\n--- a/small.txt\n+++ b/small.txt\n@@ -1 +1 @@\n-old line\n+new line\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42.diff", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, diff)
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetPullRequestDiff(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "file": "small.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[TRUNCATED:") || !strings.Contains(out, "get_file") {
		t.Fatalf("a file cut by the download cap must be marked:\n%s", out)
	}
}

func TestSearchPullRequests_ServerCapsPageSize(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/issues", func(w http.ResponseWriter, r *http.Request) {
		// The client asks for 20; the server serves 2 per page of 5.
		w.Header().Set("X-Total-Count", "5")
		writeJSON(w, []map[string]interface{}{
			{"number": 5, "title": "a", "state": "open"},
			{"number": 4, "title": "b", "state": "open"},
		})
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.SearchPullRequests(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["total_count"].(float64) != 5 || m["next_page"] == nil || m["next_page"].(float64) != 2 {
		t.Fatalf("a capped page must still report next_page: %s", out)
	}
}

func TestGetFile_CutOnRuneBoundary(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/raw/a.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "héllo world")
	})
	tool, _, _ := newTestTool(t, Config{}, mux)
	out, err := tool.GetFile(context.Background(), testIncident, map[string]interface{}{"repo": "infra/puppet", "path": "a.txt", "max_bytes": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\xc3\n") || !strings.Contains(out, "\n\nh\n") || !strings.Contains(out, "showed the first 1 bytes") {
		t.Fatalf("unexpected output:\n%q", out)
	}
}
