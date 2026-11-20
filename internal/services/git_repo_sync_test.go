package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// gitT runs git for test fixtures with a fixed identity.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitTEnv(t, dir, nil, args...)
}

func gitTEnv(t *testing.T, dir string, extra []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"), extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newOrigin makes a bare repository with one commit on main and returns its
// file:// URL and a work tree that pushes to it.
func newOrigin(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "origin.git")
	work := filepath.Join(base, "work")
	gitT(t, base, "init", "-q", "--bare", bare)
	gitT(t, base, "init", "-q", work)
	commitFile(t, work, "router-1", "hostname router-1\n")
	gitT(t, work, "remote", "add", "origin", bare)
	gitT(t, work, "push", "-q", "origin", "main")
	return "file://" + bare, work
}

func commitFile(t *testing.T, work, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "add", name)
	gitT(t, work, "commit", "-q", "-m", "update "+name)
	return gitT(t, work, "rev-parse", "HEAD")
}

type gitRepoFixture struct {
	db    *gorm.DB
	svc   *GitRepoSyncService
	root  string
	clock time.Time
	ttID  uint
}

func newGitRepoFixture(t *testing.T) *gitRepoFixture {
	t.Helper()
	requireGit(t)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.ToolType{}, &database.ToolInstance{}); err != nil {
		t.Fatal(err)
	}
	tt := database.ToolType{Name: GitRepoToolType}
	if err := db.Create(&tt).Error; err != nil {
		t.Fatal(err)
	}
	oldRoot := GitRepoRoot
	t.Cleanup(func() { GitRepoRoot = oldRoot })
	base := t.TempDir()
	f := &gitRepoFixture{db: db, root: filepath.Join(base, "repos"), clock: time.Now(), ttID: tt.ID}
	f.svc = NewGitRepoSyncService(db, f.root, filepath.Join(base, "state"))
	f.svc.allowFile, f.svc.allowLoopback = true, true
	f.svc.now = func() time.Time { return f.clock }
	for _, d := range []string{f.root, filepath.Join(base, "state")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *gitRepoFixture) addInstance(t *testing.T, logical string, settings map[string]interface{}) *database.ToolInstance {
	t.Helper()
	inst := &database.ToolInstance{ToolTypeID: f.ttID, Name: logical, LogicalName: logical, Settings: settings, Enabled: true}
	if err := f.db.Create(inst).Error; err != nil {
		t.Fatal(err)
	}
	return inst
}

func (f *gitRepoFixture) status(t *testing.T, name string) *GitRepoStatus {
	t.Helper()
	st, err := ReadGitRepoStatus(name)
	if err != nil {
		t.Fatalf("status %s: %v", name, err)
	}
	return st
}

func TestGitRepoSync_CloneFetchAndRemove(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	inst := f.addInstance(t, "cbnet", map[string]interface{}{"git_url": url, "git_history_days": float64(30)})

	f.svc.SyncOnce(context.Background())
	st := f.status(t, "cbnet")
	if st.Error != "" {
		t.Fatalf("unexpected error: %s", st.Error)
	}
	if st.Branch != "main" || st.HeadCommit == "" || st.HeadCommitAt == nil || st.LastSuccessAt == nil {
		t.Fatalf("incomplete status: %+v", st)
	}
	if b, err := os.ReadFile(filepath.Join(f.root, "cbnet", "router-1")); err != nil || string(b) != "hostname router-1\n" {
		t.Fatalf("mirror content: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(f.root, "cbnet")); err != nil || fi.Mode().Perm()&0o005 != 0o005 {
		t.Fatalf("mirror must be world-readable for the worker: %v %v", fi.Mode(), err)
	}

	// A new commit is not fetched before the interval ...
	head2 := commitFile(t, work, "router-1", "hostname router-1\ndescription new\n")
	gitT(t, work, "push", "-q", "origin", "main")
	f.svc.SyncOnce(context.Background())
	if got := f.status(t, "cbnet").HeadCommit; got == head2 {
		t.Fatal("fetched before git_sync_interval elapsed")
	}
	// ... and is after it.
	f.clock = f.clock.Add(6 * time.Minute)
	f.svc.SyncOnce(context.Background())
	if got := f.status(t, "cbnet").HeadCommit; got != head2 {
		t.Fatalf("head = %s, want %s", got, head2)
	}
	if n := gitT(t, filepath.Join(f.root, "cbnet"), "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("history commits = %s, want 2", n)
	}

	// Disabling the instance removes the mirror and its status file.
	if err := f.db.Model(inst).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "cbnet")); !os.IsNotExist(err) {
		t.Fatalf("mirror still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "cbnet"+gitRepoStatusSuffix)); !os.IsNotExist(err) {
		t.Fatalf("status still present: %v", err)
	}
}

func TestGitRepoSync_LeavesUnmanagedDirectoryAlone(t *testing.T) {
	f := newGitRepoFixture(t)
	url, _ := newOrigin(t)
	manual := filepath.Join(f.root, "cbnet")
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(manual, "hand-made")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.addInstance(t, "cbnet", map[string]interface{}{"git_url": url})
	f.svc.SyncOnce(context.Background())
	if st := f.status(t, "cbnet"); !strings.Contains(st.Error, "not managed by Akmatori") {
		t.Fatalf("want unmanaged error, got %q", st.Error)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("hand-made directory was touched: %v", err)
	}
}

func TestGitRepoSync_BranchChangeReclones(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	gitT(t, work, "checkout", "-q", "-b", "staging")
	commitFile(t, work, "router-2", "hostname router-2\n")
	gitT(t, work, "push", "-q", "origin", "staging")

	inst := f.addInstance(t, "cfg", map[string]interface{}{"git_url": url})
	f.svc.SyncOnce(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "cfg", "router-2")); !os.IsNotExist(err) {
		t.Fatal("default branch mirror has the staging file")
	}
	inst.Settings = database.JSONB{"git_url": url, "git_branch": "staging"}
	if err := f.db.Save(inst).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background()) // fingerprint changed: due immediately
	st := f.status(t, "cfg")
	if st.Error != "" || st.Branch != "staging" {
		t.Fatalf("status after branch change: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.root, "cfg", "router-2")); err != nil {
		t.Fatalf("staging file missing: %v", err)
	}
}

func TestGitRepoSync_SizeLimitBlocksUntilSettingsChange(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	commitFile(t, work, "big", strings.Repeat("0123456789abcdef", 1<<17)) // 2 MiB
	gitT(t, work, "push", "-q", "origin", "main")

	inst := f.addInstance(t, "big", map[string]interface{}{"git_url": url, "git_max_size_mb": float64(1)})
	f.svc.SyncOnce(context.Background())
	st := f.status(t, "big")
	if st.Blocked == "" || !strings.Contains(st.Error, "git_max_size_mb") {
		t.Fatalf("want blocked size error, got %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.root, "big")); !os.IsNotExist(err) {
		t.Fatal("oversized mirror was kept")
	}
	attempt := *st.LastAttemptAt
	f.clock = f.clock.Add(time.Hour)
	f.svc.SyncOnce(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "big")); !os.IsNotExist(err) {
		t.Fatal("blocked mirror was cloned again")
	}
	if !f.status(t, "big").LastAttemptAt.After(attempt) {
		t.Fatal("status not refreshed while blocked")
	}

	inst.Settings = database.JSONB{"git_url": url, "git_max_size_mb": float64(50)}
	if err := f.db.Save(inst).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background())
	if st := f.status(t, "big"); st.Error != "" || st.Blocked != "" || st.HeadCommit == "" {
		t.Fatalf("raised limit should sync: %+v", st)
	}
}

func TestGitRepoSync_TokenStaysOutOfMirror(t *testing.T) {
	f := newGitRepoFixture(t)
	env, cleanup, err := f.svc.gitEnv("x", gitRepoSettings{Auth: "token", Token: "s3cret", Username: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "=http.extraHeader") || !strings.Contains(joined, "Authorization: Basic Ym90OnMzY3JldA==") {
		t.Fatalf("token header missing from env")
	}
	if !strings.Contains(joined, "GIT_ALLOW_PROTOCOL=https:http:ssh") {
		t.Fatal("protocol allowlist missing")
	}

	// And a real clone writes nothing of the env into .git/config.
	url, _ := newOrigin(t)
	f.addInstance(t, "tok", map[string]interface{}{"git_url": url})
	f.svc.SyncOnce(context.Background())
	cfg, err := os.ReadFile(filepath.Join(f.root, "tok", ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "Authorization") || strings.Contains(string(cfg), "extraHeader") {
		t.Fatalf(".git/config holds credentials:\n%s", cfg)
	}
}

func TestGitRepoSync_SSHKeyFileIsRemoved(t *testing.T) {
	f := newGitRepoFixture(t)
	env, cleanup, err := f.svc.gitEnv("x", gitRepoSettings{Auth: "ssh_key", SSHKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"})
	if err != nil {
		t.Fatal(err)
	}
	var keyPath string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			i := strings.Index(kv, "-i '")
			keyPath = kv[i+4 : i+4+strings.Index(kv[i+4:], "'")]
			if !strings.Contains(kv, "StrictHostKeyChecking=accept-new") {
				t.Fatalf("want accept-new without known_hosts: %s", kv)
			}
		}
	}
	fi, err := os.Stat(keyPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %s: %v %v", keyPath, fi, err)
	}
	cleanup()
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("key file left behind")
	}
}

func TestParseGitRepoSettings(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]interface{}
		wantErr string
		auth    string
	}{
		{"https public", map[string]interface{}{"git_url": "https://git.example.com/a/b.git"}, "", "none"},
		{"https token", map[string]interface{}{"git_url": "https://git.example.com/a/b.git", "git_token": "t"}, "", "token"},
		{"ssh key", map[string]interface{}{"git_url": "ssh://git@git.example.com:2300/a/b.git", "git_ssh_private_key": "k"}, "", "ssh_key"},
		{"scp-like", map[string]interface{}{"git_url": "git@github.com:a/b.git", "git_ssh_private_key": "k"}, "", "ssh_key"},
		{"missing url", map[string]interface{}{}, "git_url is required", ""},
		{"option injection", map[string]interface{}{"git_url": "--upload-pack=touch /tmp/x"}, "must not start", ""},
		{"ext transport", map[string]interface{}{"git_url": "ext::sh -c touch% /tmp/x"}, "must be https", ""},
		{"password in url", map[string]interface{}{"git_url": "https://u:p@git.example.com/a.git"}, "must not contain a password", ""},
		{"token as url user", map[string]interface{}{"git_url": "https://ghp_secret@git.example.com/a.git"}, "must not contain a user name", ""},
		{"token over ssh", map[string]interface{}{"git_url": "ssh://git@h/a.git", "git_auth": "token", "git_token": "t"}, "only with an https", ""},
		{"key over https", map[string]interface{}{"git_url": "https://h/a.git", "git_auth": "ssh_key", "git_ssh_private_key": "k"}, "only with an ssh", ""},
		{"token missing", map[string]interface{}{"git_url": "https://h/a.git", "git_auth": "token"}, "needs git_token", ""},
		{"bad branch", map[string]interface{}{"git_url": "https://h/a.git", "git_branch": "--force"}, "not a valid branch", ""},
		{"bad scheme", map[string]interface{}{"git_url": "ftp://h/a.git"}, "not allowed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseGitRepoSettings(c.in)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Auth != c.auth {
				t.Fatalf("auth = %s, want %s", cfg.Auth, c.auth)
			}
		})
	}
	cfg, _ := parseGitRepoSettings(map[string]interface{}{"git_url": "https://h/a.git", "git_sync_interval": float64(5), "git_history_days": "-3"})
	if cfg.IntervalSec != gitRepoMinInterval || cfg.HistoryDays != 0 {
		t.Fatalf("clamping: %+v", cfg)
	}
}

func TestGitRepoCheckRemote(t *testing.T) {
	s := &GitRepoSyncService{}
	for _, u := range []string{"https://127.0.0.1/a.git", "ssh://git@169.254.169.254/a.git", "git@[::1]:a.git", "file:///etc"} {
		if _, err := s.checkRemote(context.Background(), u); err == nil {
			t.Errorf("%s: want refusal", u)
		}
	}
	if _, err := s.checkRemote(context.Background(), "https://10.1.2.3/a.git"); err != nil {
		t.Errorf("private address must be allowed: %v", err)
	}
}

func TestGitRepoDetails(t *testing.T) {
	f := newGitRepoFixture(t)
	url, _ := newOrigin(t)
	inst := f.addInstance(t, "cbnet", map[string]interface{}{"git_url": url, "git_history_days": float64(7)})
	inst.ToolType = database.ToolType{Name: GitRepoToolType}
	if d := gitRepoDetails(*inst); !strings.Contains(d, "not synced yet") {
		t.Fatalf("before sync: %s", d)
	}
	f.svc.SyncOnce(context.Background())
	d := gitRepoDetails(*inst)
	for _, want := range []string{filepath.Join(f.root, "cbnet"), "branch main", "last 7 days"} {
		if !strings.Contains(d, want) {
			t.Errorf("details missing %q:\n%s", want, d)
		}
	}
	usage := generateToolUsageExample(*inst)
	if !strings.Contains(usage, "cd "+filepath.Join(f.root, "cbnet")) || strings.Contains(usage, "gateway_call(") {
		t.Errorf("usage example:\n%s", usage)
	}
}

func (f *gitRepoFixture) commits(t *testing.T, name string) string {
	t.Helper()
	return gitT(t, filepath.Join(f.root, name), "rev-list", "--count", "HEAD")
}

// An unmanaged directory stays untouched on every later pass, and when its
// instance is disabled; only the status file goes.
func TestGitRepoSync_UnmanagedDirectorySurvivesLaterPasses(t *testing.T) {
	f := newGitRepoFixture(t)
	url, _ := newOrigin(t)
	manual := filepath.Join(f.root, "cbnet")
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(manual, "hand-made")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := f.addInstance(t, "cbnet", map[string]interface{}{"git_url": url})
	for i := 0; i < 3; i++ {
		f.clock = f.clock.Add(10 * time.Minute)
		f.svc.SyncOnce(context.Background())
		if st := f.status(t, "cbnet"); !st.Unmanaged || !strings.Contains(st.Error, "not managed") {
			t.Fatalf("pass %d: %+v", i, st)
		}
	}
	// A settings change must not re-clone over it either.
	inst.Settings = database.JSONB{"git_url": url, "git_branch": "other"}
	if err := f.db.Save(inst).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background())
	if err := f.db.Model(inst).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unmanaged directory was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "cbnet"+gitRepoStatusSuffix)); !os.IsNotExist(err) {
		t.Fatal("status file of a removed instance kept")
	}
}

// Once the operator removes the foreign directory, the mirror is created.
func TestGitRepoSync_UnmanagedDirectoryRemovedThenCloned(t *testing.T) {
	f := newGitRepoFixture(t)
	url, _ := newOrigin(t)
	if err := os.MkdirAll(filepath.Join(f.root, "cbnet"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.addInstance(t, "cbnet", map[string]interface{}{"git_url": url})
	f.svc.SyncOnce(context.Background())
	if err := os.RemoveAll(filepath.Join(f.root, "cbnet")); err != nil {
		t.Fatal(err)
	}
	f.svc.SyncOnce(context.Background())
	st := f.status(t, "cbnet")
	if st.Unmanaged || st.Error != "" || st.HeadCommit == "" {
		t.Fatalf("want a managed mirror: %+v", st)
	}
}

// git_history_days=0 keeps exactly the newest commit across fetches.
func TestGitRepoSync_ZeroDaysKeepsNewestCommitOnly(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	f.addInstance(t, "z", map[string]interface{}{"git_url": url, "git_history_days": float64(0)})
	f.svc.SyncOnce(context.Background())
	for i := 0; i < 3; i++ {
		head := commitFile(t, work, "router-1", fmt.Sprintf("rev %d\n", i))
		gitT(t, work, "push", "-q", "origin", "main")
		f.clock = f.clock.Add(6 * time.Minute)
		f.svc.SyncOnce(context.Background())
		if st := f.status(t, "z"); st.HeadCommit != head || st.Error != "" {
			t.Fatalf("pass %d: %+v", i, st)
		}
		if n := f.commits(t, "z"); n != "1" {
			t.Fatalf("pass %d: %s commits, want 1", i, n)
		}
	}
}

// A repository whose newest commit is older than the window keeps only the
// newest commit, both on clone and when it goes idle later.
func TestGitRepoSync_EmptyWindowFallsBackToNewestCommit(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	old := f.clock.Add(-100 * 24 * time.Hour).Format(time.RFC3339)
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(work, "old"), []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, work, "add", "old")
		gitTEnv(t, work, []string{"GIT_COMMITTER_DATE=" + old, "GIT_AUTHOR_DATE=" + old}, "commit", "-q", "-m", "old")
	}
	gitT(t, work, "push", "-q", "origin", "main")

	f.addInstance(t, "idle", map[string]interface{}{"git_url": url, "git_history_days": float64(30)})
	f.svc.SyncOnce(context.Background())
	if st := f.status(t, "idle"); st.Error != "" || st.HeadCommit == "" || st.HistoryFrom != nil {
		t.Fatalf("clone of an idle repo: %+v", st)
	}
	if n := f.commits(t, "idle"); n != "1" {
		t.Fatalf("%s commits, want 1", n)
	}
}

func TestGitRepoSync_HistoryTrimmedWhenRepoGoesIdle(t *testing.T) {
	f := newGitRepoFixture(t)
	url, work := newOrigin(t)
	commitFile(t, work, "router-1", "second\n")
	gitT(t, work, "push", "-q", "origin", "main")
	f.addInstance(t, "c", map[string]interface{}{"git_url": url, "git_history_days": float64(30)})
	f.svc.SyncOnce(context.Background())
	if n := f.commits(t, "c"); n != "3" && n != "2" {
		t.Fatalf("initial history %s", n)
	}
	f.clock = f.clock.Add(60 * 24 * time.Hour) // every commit is now outside the window
	f.svc.SyncOnce(context.Background())
	st := f.status(t, "c")
	if st.Error != "" {
		t.Fatalf("trim error: %s", st.Error)
	}
	if n := f.commits(t, "c"); n != "1" {
		t.Fatalf("after idle trim: %s commits, want 1", n)
	}
	if st.LastTrimAt == nil || !st.LastTrimAt.Equal(f.clock.UTC()) {
		t.Fatalf("trim time not recorded: %+v", st.LastTrimAt)
	}
}

func TestGitRepoEnv_NoRedirectsAndPinnedAddresses(t *testing.T) {
	f := newGitRepoFixture(t)
	env, cleanup, err := f.svc.gitEnv("x", gitRepoSettings{Auth: "token", Token: "t"}, "git.example.com:443:10.0.0.7")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got := map[string]string{}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_CONFIG_") {
			i := strings.Index(kv, "=")
			got[kv[:i]] = kv[i+1:]
		}
	}
	if got["GIT_CONFIG_COUNT"] != "3" {
		t.Fatalf("config entries: %v", got)
	}
	want := map[string]string{"http.followRedirects": "false", "http.curloptResolve": "git.example.com:443:10.0.0.7", "http.extraHeader": "Authorization: Basic Z2l0OnQ="}
	for i := 0; i < 3; i++ {
		k, v := got[fmt.Sprintf("GIT_CONFIG_KEY_%d", i)], got[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)]
		if want[k] != v {
			t.Errorf("%s = %q, want %q", k, v, want[k])
		}
	}
}

func TestGitRepoSync_FailedURLChangeStillReclonesLater(t *testing.T) {
	f := newGitRepoFixture(t)
	urlA, _ := newOrigin(t)
	urlB, workB := newOrigin(t)
	commitFile(t, workB, "router-b", "hostname router-b\n")
	gitT(t, workB, "push", "-q", "origin", "main")

	inst := f.addInstance(t, "cfg", map[string]interface{}{"git_url": urlA})
	f.svc.SyncOnce(context.Background())
	applied := f.status(t, "cfg").Fingerprint

	inst.Settings = database.JSONB{"git_url": urlB}
	if err := f.db.Save(inst).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.allowFile = false // the new remote fails the preflight check (like a DNS error)
	f.svc.SyncOnce(context.Background())
	st := f.status(t, "cfg")
	if st.Error == "" || st.Fingerprint != applied {
		t.Fatalf("failed change must keep the applied fingerprint: %+v", st)
	}

	f.svc.allowFile = true // the remote is reachable again
	f.svc.SyncOnce(context.Background())
	if st := f.status(t, "cfg"); st.Error != "" {
		t.Fatalf("status: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.root, "cfg", "router-b")); err != nil {
		t.Fatalf("mirror still follows the old origin: %v", err)
	}
	if got := gitT(t, filepath.Join(f.root, "cfg"), "remote", "get-url", "origin"); strings.TrimSpace(got) != urlB {
		t.Fatalf("origin = %q, want %q", got, urlB)
	}
}

func TestGitRepoEnv_DropsProxy(t *testing.T) {
	f := newGitRepoFixture(t)
	for _, k := range []string{"HTTPS_PROXY", "http_proxy", "ALL_PROXY", "NO_PROXY"} {
		t.Setenv(k, "http://proxy.example:3128")
	}
	env, cleanup, err := f.svc.gitEnv("x", gitRepoSettings{Auth: "none"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, kv := range env {
		if isProxyEnv(kv) {
			t.Errorf("proxy variable passed to git: %s", kv)
		}
	}
}
