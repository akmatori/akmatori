package forge

import (
	"strings"
	"testing"
)

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

func TestSplitRepo(t *testing.T) {
	for _, ok := range []string{"infra/puppet", "a.b/c-d_e", "/infra/puppet/", "infra/puppet.git"} {
		if _, _, err := SplitRepo(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"", "infra", "a/b/c", "../x", "a/..", "a b/c", "a/%2e%2e"} {
		if _, _, err := SplitRepo(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestRepoAllowed(t *testing.T) {
	allowed := []string{"infra/puppet", "ops/*"}
	cases := map[string]bool{"infra/puppet": true, "Infra/Puppet": true, "infra/other": false, "ops/anything": true, "opsx/a": false}
	for repo, want := range cases {
		o, n, _ := SplitRepo(repo)
		if got := RepoAllowed(allowed, o, n); got != want {
			t.Errorf("%s: got %v, want %v", repo, got, want)
		}
	}
	if !RepoAllowed(nil, "any", "repo") {
		t.Error("an empty allowlist must allow every repo")
	}
}

func TestSplitDiff(t *testing.T) {
	order, secs := SplitDiff(sampleDiff)
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

func TestCut_RuneBoundary(t *testing.T) {
	s, short := Cut("héllo", 2)
	if !short || s != "h" {
		t.Fatalf("got %q, %v", s, short)
	}
	if s, short := Cut("abc", 10); short || s != "abc" {
		t.Fatalf("got %q, %v", s, short)
	}
}

func TestCut_NotUTF8KeepsText(t *testing.T) {
	latin1 := "caf\xe9 caf\xe9 caf\xe9"
	s, short := Cut(latin1, 10)
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

func TestParsePRURL(t *testing.T) {
	p, err := ParsePRURL("https://git.example.com", "https://git.example.com/infra/puppet/pulls/42/files", "Gitea")
	if err != nil || p.Repo() != "infra/puppet" || p.Number != 42 {
		t.Fatalf("got %+v, %v", p, err)
	}
	if p, err := ParsePRURL("https://github.com", "https://github.com/infra/puppet/pull/9", "GitHub"); err != nil || p.Number != 9 {
		t.Fatalf("GitHub form: got %+v, %v", p, err)
	}
	if _, err := ParsePRURL("https://git.example.com", "https://evil.example.org/infra/puppet/pulls/42", "Gitea"); err == nil {
		t.Fatal("a link to another host must be refused")
	}
	if _, err := ParsePRURL("https://git.example.com", "https://git.example.com/infra/puppet/issues/42", "Gitea"); err == nil {
		t.Fatal("an issue link must be refused")
	}
	p, err = ParsePRURL("https://example.com/git", "https://example.com/git/infra/puppet/pulls/7", "Gitea")
	if err != nil || p.Number != 7 || p.Repo() != "infra/puppet" {
		t.Fatalf("sub-path: got %+v, %v", p, err)
	}
}
