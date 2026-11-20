package services

import (
	"context"
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
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
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
	if !strings.Contains(joined, "GIT_CONFIG_KEY_0=http.extraHeader") || !strings.Contains(joined, "Authorization: Basic Ym90OnMzY3JldA==") {
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
		if err := s.checkRemote(context.Background(), u); err == nil {
			t.Errorf("%s: want refusal", u)
		}
	}
	if err := s.checkRemote(context.Background(), "https://10.1.2.3/a.git"); err != nil {
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
