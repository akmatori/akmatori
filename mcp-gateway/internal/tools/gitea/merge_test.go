package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	headSHA      = "4ae1f63c0ffee0000000000000000000000abcde"
	mergeBaseSHA = "f9588df000000000000000000000000000000001"
)

// fakeMergeGitea is a configurable pull request 42 in infra/puppet.
type fakeMergeGitea struct {
	base      string
	head      string
	mergeBase string
	author    string
	tokenUser string
	draft     bool
	state     string
	mergeable bool
	labels    []string
	files     []map[string]interface{} // live pulls/42/files
	pinned    []map[string]interface{} // compare commit files; nil = same names as files
	pinnedAdd int                      // compare stats additions; -1 = sum of files
	compareN  int                      // total_commits override (0 = len(commits))
	reviews   []map[string]interface{}
	reviewsN  int // serve this many reviews, 50 per page (overrides reviews)
	mergeCode int
	merged    bool
	posted    map[string]interface{}

	prReads   int
	afterRead func(f *fakeMergeGitea, n int) // runs after the n-th pull request read
}

func newFakeMerge() *fakeMergeGitea {
	return &fakeMergeGitea{
		base: "preprod", head: headSHA, mergeBase: mergeBaseSHA, author: "alice", tokenUser: "akmatori-bot",
		state: "open", mergeable: true, mergeCode: http.StatusOK, pinnedAdd: -1,
		files: []map[string]interface{}{
			{"filename": "modules/nginx/manifests/init.pp", "status": "modified", "additions": 3, "deletions": 1},
		},
	}
}

func (f *fakeMergeGitea) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		labels := []map[string]string{}
		for _, l := range f.labels {
			labels = append(labels, map[string]string{"name": l})
		}
		pr := map[string]interface{}{
			"number": 42, "state": f.state, "draft": f.draft, "mergeable": f.mergeable, "merged": f.merged,
			"user": map[string]string{"login": f.author}, "labels": labels, "merge_base": f.mergeBase,
			"base":     map[string]string{"ref": f.base, "sha": "f9588df"},
			"head":     map[string]string{"ref": "feature", "sha": f.head},
			"html_url": "https://git/infra/puppet/pulls/42",
		}
		if f.merged {
			pr["merge_commit_sha"] = "deadbeef"
		}
		writeJSON(w, pr)
		f.prReads++
		if f.afterRead != nil {
			f.afterRead(f, f.prReads)
		}
	})
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"login": f.tokenUser})
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Count", itoa(len(f.files)))
		if r.URL.Query().Get("page") != "1" {
			writeJSON(w, []interface{}{})
			return
		}
		writeJSON(w, f.files)
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/compare/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/compare/"+f.mergeBase+"..."+f.head) {
			http.Error(w, `{"message":"unexpected compare `+r.URL.Path+`"}`, http.StatusBadRequest)
			return
		}
		files := f.pinned
		adds, dels := 0, 0
		if files == nil {
			for _, x := range f.files {
				files = append(files, map[string]interface{}{"filename": x["filename"], "status": x["status"]})
				if a, ok := x["additions"].(int); ok {
					adds += a
				}
				if d, ok := x["deletions"].(int); ok {
					dels += d
				}
			}
		}
		if f.pinnedAdd >= 0 {
			adds, dels = f.pinnedAdd, 0
		}
		total := f.compareN
		if total == 0 {
			total = 1
		}
		writeJSON(w, map[string]interface{}{
			"total_commits": total,
			"commits": []interface{}{map[string]interface{}{
				"sha": f.head, "files": files, "stats": map[string]int{"additions": adds, "deletions": dels},
			}},
		})
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		if f.reviewsN > 0 {
			w.Header().Set("X-Total-Count", itoa(f.reviewsN))
			page := []interface{}{}
			for i := 0; i < pageLimit; i++ {
				page = append(page, map[string]interface{}{"id": i, "user": map[string]string{"login": "bob"}, "state": "COMMENT"})
			}
			writeJSON(w, page)
			return
		}
		if f.reviews == nil {
			writeJSON(w, []interface{}{})
			return
		}
		writeJSON(w, f.reviews)
	})
	mux.HandleFunc("/api/v1/repos/infra/puppet/pulls/42/merge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&f.posted)
		if f.mergeCode != http.StatusOK {
			http.Error(w, `{"message":"refused"}`, f.mergeCode)
			return
		}
		f.merged = true
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func mergeCfg() Config {
	targets, _ := ParseMergeTargets("infra/puppet:preprod !.gocd.yaml !Puppetfile !**/secrets/** !café.txt")
	return Config{MergeTargets: targets, MergeMaxFiles: DefaultMergeMaxFiles, MergeMaxLines: DefaultMergeMaxLines}
}

func mergeArgs() map[string]interface{} {
	return map[string]interface{}{"repo": "infra/puppet", "number": float64(42), "head_sha": headSHA, "reason": "reviewed: APPROVED"}
}

func TestParseMergeTargets(t *testing.T) {
	got, bad := ParseMergeTargets("Ops/Prometheus-Rules:test rules/**/*.rule  # alerts\n\ninfra/puppet:preprod !.gocd.yaml\nnocolon\nx/y:\n")
	if len(got) != 2 || len(bad) != 2 {
		t.Fatalf("got %+v, bad %v", got, bad)
	}
	if got[0].Repo != "ops/prometheus-rules" || got[0].Branch != "test" || len(got[0].Allow) != 1 || len(got[0].Deny) != 0 {
		t.Fatalf("line 1: %+v", got[0])
	}
	if got[1].Branch != "preprod" || len(got[1].Deny) != 1 || got[1].Deny[0] != ".gocd.yaml" {
		t.Fatalf("line 2: %+v", got[1])
	}
	if c := buildConfig(map[string]interface{}{}); len(c.MergeTargets) != 0 || c.MergeMaxFiles != DefaultMergeMaxFiles || c.MergeMaxLines != DefaultMergeMaxLines {
		t.Fatalf("defaults: %+v", c)
	}
	if c := buildConfig(map[string]interface{}{"gitea_merge_max_files": float64(3), "gitea_merge_max_lines": float64(0)}); c.MergeMaxFiles != 3 || c.MergeMaxLines != DefaultMergeMaxLines {
		t.Fatalf("limits: %+v", c)
	}
}

func TestPathAllowed(t *testing.T) {
	rules := MergeTarget{Allow: []string{"rules/**/*.rule"}}
	for p, want := range map[string]bool{
		"rules/cdn-ops/hw.rule": true,
		"rules/hw.rule":         true,
		"rules/a/b/c.rule":      true,
		"rules/hw.rule.bak":     false,
		"rules/hw\n.rule.bak":   false,
		".gocd.yaml":            false,
		"pint.hcl":              false,
	} {
		if got := rules.pathAllowed(p); got != want {
			t.Errorf("rules %q: got %v, want %v", p, got, want)
		}
	}
	puppet := MergeTarget{Deny: []string{".gocd.yaml", "Puppetfile", "**/secrets/**", "café.txt", "docs/*é?.md"}}
	for p, want := range map[string]bool{
		"modules/nginx/manifests/init.pp": true,
		".gocd.yaml":                      false,
		"Puppetfile":                      false,
		"modules/x/secrets/key.pem":       false,
		"modules/secrets/key\n.pem":       false, // ** must match a newline
		"modules/secrets\n/key.pem":       true,  // a different directory name
		"café.txt":                        false, // non-ASCII literal kept
		"cafÃ©.txt":                       true,
		"docs/abéc.md":                    false,
		"sub/.gocd.yaml":                  true, // a plain glob is anchored at the repo root
	} {
		if got := puppet.pathAllowed(p); got != want {
			t.Errorf("puppet %q: got %v, want %v", p, got, want)
		}
	}
}

func TestMerge_DisabledByDefault(t *testing.T) {
	f := newFakeMerge()
	tool, _, seen := newTestTool(t, Config{}, f.mux())
	_, err := tool.MergePullRequest(context.Background(), testIncident, mergeArgs())
	if err == nil || !strings.Contains(err.Error(), "gitea_merge_targets") {
		t.Fatalf("expected the merge gate error, got %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused merge must not reach Gitea: %v", *seen)
	}
}

func TestMerge_Success(t *testing.T) {
	f := newFakeMerge()
	f.reviews = []map[string]interface{}{
		{"id": 1, "user": map[string]string{"login": "bob"}, "state": "REQUEST_CHANGES"},
		{"id": 2, "user": map[string]string{"login": "bob"}, "state": "COMMENT"},
		{"id": 3, "user": map[string]string{"login": "bob"}, "state": "APPROVED"},
		{"id": 4, "user": map[string]string{"login": "carol"}, "state": "REQUEST_CHANGES", "dismissed": true},
	}
	tool, _, seen := newTestTool(t, mergeCfg(), f.mux())
	args := mergeArgs()
	args["head_sha"] = strings.ToUpper(headSHA) // case does not matter
	out, err := tool.MergePullRequest(context.Background(), testIncident, args)
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["merged"] != true || m["merge_commit_sha"] != "deadbeef" || m["base"] != "preprod" {
		t.Fatalf("output: %s", out)
	}
	if f.posted["Do"] != "merge" || f.posted["head_commit_id"] != headSHA || f.posted["delete_branch_after_merge"] != false {
		t.Fatalf("posted: %v", f.posted)
	}
	if msg, _ := f.posted["MergeMessageField"].(string); !strings.HasPrefix(msg, "reviewed: APPROVED") || !strings.Contains(msg, "4ae1f63c0f") {
		t.Fatalf("merge message: %q", msg)
	}
	// read, re-read before the POST, read after the merge
	if f.prReads != 3 {
		t.Fatalf("expected 3 pull request reads, got %d: %v", f.prReads, *seen)
	}
}

func TestMerge_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeMergeGitea)
		args  func(a map[string]interface{})
		want  string
	}{
		{"wrong base branch", func(f *fakeMergeGitea) { f.base = "master" }, nil, `base branch "master"`},
		{"head moved", func(f *fakeMergeGitea) { f.head = "9999999" + headSHA[7:] }, nil, "review the new head"},
		{"draft", func(f *fakeMergeGitea) { f.draft = true }, nil, "draft"},
		{"closed", func(f *fakeMergeGitea) { f.state = "closed" }, nil, "it is closed"},
		{"already merged", func(f *fakeMergeGitea) { f.merged = true }, nil, "already merged"},
		{"not mergeable", func(f *fakeMergeGitea) { f.mergeable = false }, nil, "not mergeable"},
		{"hold label", func(f *fakeMergeGitea) { f.labels = []string{"Do-Not-Merge"} }, nil, "label"},
		{"self merge", func(f *fakeMergeGitea) { f.author = "Akmatori-Bot" }, nil, "token user"},
		{"denied path", func(f *fakeMergeGitea) {
			f.files = append(f.files, map[string]interface{}{"filename": ".gocd.yaml", "status": "modified", "additions": 1})
		}, nil, ".gocd.yaml"},
		{"renamed from denied path", func(f *fakeMergeGitea) {
			f.files = []map[string]interface{}{{"filename": "x.pp", "previous_filename": "Puppetfile", "status": "renamed"}}
		}, nil, "Puppetfile"},
		{"too many files", func(f *fakeMergeGitea) {
			f.files = nil
			for i := 0; i < 11; i++ {
				f.files = append(f.files, map[string]interface{}{"filename": "m/f" + itoa(i) + ".pp", "additions": 1})
			}
		}, nil, "11 files"},
		{"too many lines", func(f *fakeMergeGitea) { f.files[0]["additions"] = 400 }, nil, "lines"},
		{"changes requested", func(f *fakeMergeGitea) {
			f.reviews = []map[string]interface{}{
				{"id": 1, "user": map[string]string{"login": "bob"}, "state": "APPROVED"},
				{"id": 2, "user": map[string]string{"login": "bob"}, "state": "REQUEST_CHANGES"},
			}
		}, nil, "changes requested by bob"},

		// Codex review of #106
		{"short head_sha", nil, func(a map[string]interface{}) { a["head_sha"] = headSHA[:7] }, "full head commit sha"},
		{"39-char head_sha", nil, func(a map[string]interface{}) { a["head_sha"] = headSHA[:39] }, "full head commit sha"},
		{"base retargeted during checks", func(f *fakeMergeGitea) {
			f.afterRead = func(f *fakeMergeGitea, n int) {
				if n == 1 {
					f.base = "main"
				}
			}
		}, nil, `changed during the checks: base branch "main"`},
		{"hold label added during checks", func(f *fakeMergeGitea) {
			f.afterRead = func(f *fakeMergeGitea, n int) {
				if n == 1 {
					f.labels = []string{"hold"}
				}
			}
		}, nil, `changed during the checks: it has the label "hold"`},
		{"pinned commits touch a denied path", func(f *fakeMergeGitea) {
			// live list shows a benign head, the reviewed commits change .gocd.yaml
			f.pinned = []map[string]interface{}{{"filename": ".gocd.yaml", "status": "modified"}}
		}, nil, ".gocd.yaml"},
		{"pinned commits are larger", func(f *fakeMergeGitea) { f.pinnedAdd = 999 }, nil, "999 lines"},
		{"pinned rename not in live list", func(f *fakeMergeGitea) {
			f.pinned = []map[string]interface{}{{"filename": "x.pp", "status": "renamed"}}
		}, nil, `rename "x.pp"`},
		{"compare incomplete", func(f *fakeMergeGitea) { f.compareN = 300 }, nil, "compare listed 1 of 300 commits"},
		{"no merge base", func(f *fakeMergeGitea) { f.mergeBase = "" }, nil, "no merge base"},
		{"review list truncated", func(f *fakeMergeGitea) { f.reviewsN = 1001 }, nil, "more reviews than can be listed"},

		{"no head_sha", nil, func(a map[string]interface{}) { delete(a, "head_sha") }, "head_sha is required"},
		{"bad head_sha", nil, func(a map[string]interface{}) { a["head_sha"] = "abc" }, "head_sha is required"},
		{"no reason", nil, func(a map[string]interface{}) { delete(a, "reason") }, "reason is required"},
		{"bad style", nil, func(a map[string]interface{}) { a["style"] = "fast-forward-only" }, "style must be"},
		{"gitea conflict", func(f *fakeMergeGitea) { f.mergeCode = http.StatusConflict }, nil, "head moved during the merge"},
		{"gitea protection", func(f *fakeMergeGitea) { f.mergeCode = http.StatusMethodNotAllowed }, nil, "branch protection"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeMerge()
			if c.setup != nil {
				c.setup(f)
			}
			args := mergeArgs()
			if c.args != nil {
				c.args(args)
			}
			tool, _, _ := newTestTool(t, mergeCfg(), f.mux())
			_, err := tool.MergePullRequest(context.Background(), testIncident, args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error with %q, got %v", c.want, err)
			}
			if f.posted != nil && c.name != "gitea conflict" && c.name != "gitea protection" {
				t.Fatalf("a refused merge must not POST /merge: %v", f.posted)
			}
		})
	}
}

func TestMerge_RepoAllowlistStillApplies(t *testing.T) {
	f := newFakeMerge()
	cfg := mergeCfg()
	cfg.AllowedRepos = []string{"ops/*"}
	tool, _, seen := newTestTool(t, cfg, f.mux())
	_, err := tool.MergePullRequest(context.Background(), testIncident, mergeArgs())
	if err == nil || !strings.Contains(err.Error(), "gitea_allowed_repos") {
		t.Fatalf("expected the repo allowlist error, got %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("no request expected: %v", *seen)
	}
}
