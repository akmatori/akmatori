package services

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// GitRepoToolType is the tool type of a mirrored git repository. It has no
// gateway functions: the API keeps a read-only mirror of the repository under
// GitRepoRoot, the agent worker mounts that directory read-only, and agents
// read it with git, rg and skill scripts.
const GitRepoToolType = "git_repo"

const (
	gitRepoTick            = 30 * time.Second
	gitRepoDefaultDays     = 30
	gitRepoDefaultInterval = 300 // seconds
	gitRepoMinInterval     = 60
	gitRepoDefaultMaxMB    = 2048
	gitRepoTrimEvery       = 24 * time.Hour
	gitRepoCloneTimeout    = 30 * time.Minute
	gitRepoFetchTimeout    = 5 * time.Minute
	gitRepoStatusSuffix    = ".status.json"
	gitRepoErrMax          = 600
)

// GitRepoRoot is where mirrors live: <root>/<logical_name> plus
// <root>/<logical_name>.status.json. The worker mounts it read-only at the
// same path. Tests override it through NewGitRepoSyncService.
var GitRepoRoot = "/akmatori/repos"

// GitRepoStatus is written next to each mirror for agents (and the
// connected-tools skill) to read. It never holds credentials.
type GitRepoStatus struct {
	Name          string     `json:"name"`
	LogicalName   string     `json:"logical_name"`
	URL           string     `json:"url"`
	Branch        string     `json:"branch"`
	Path          string     `json:"path"`
	HistoryDays   int        `json:"history_days"`
	HistoryFrom   *time.Time `json:"history_from,omitempty"`
	HeadCommit    string     `json:"head_commit,omitempty"`
	HeadCommitAt  *time.Time `json:"head_commit_at,omitempty"`
	SizeBytes     int64      `json:"size_bytes"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastTrimAt    *time.Time `json:"last_trim_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	// Fingerprint identifies url+branch+auth method; a change re-clones.
	Fingerprint string `json:"fingerprint"`
	// Blocked is set when the repository exceeded git_max_size_mb; the
	// mirror is not retried until a setting changes.
	Blocked string `json:"blocked,omitempty"`
	// Unmanaged: the directory existed before Akmatori and is not ours. Such
	// a directory is never written to or deleted, on any later pass either.
	Unmanaged bool `json:"unmanaged,omitempty"`
}

type gitRepoSettings struct {
	URL         string
	Branch      string
	Auth        string // none | token | ssh_key
	Username    string
	Token       string
	SSHKey      string
	KnownHosts  string
	HistoryDays int
	IntervalSec int
	MaxSizeMB   int
}

// GitRepoSyncService keeps one shallow mirror per enabled git_repo tool
// instance: clone on first sight, fetch every git_sync_interval seconds,
// trim history to git_history_days once a day, delete the mirror when the
// instance is disabled or deleted.
type GitRepoSyncService struct {
	db       *gorm.DB
	root     string
	stateDir string // API-only: known_hosts files, temporary SSH keys

	// allowFile lets tests use file:// remotes; production allows only
	// https, http and ssh.
	allowFile     bool
	allowLoopback bool
	now           func() time.Time

	mu sync.Mutex
}

// NewGitRepoSyncService creates the service. root is the mirror directory
// (shared read-only with the worker), stateDir must not be visible to the
// worker.
func NewGitRepoSyncService(db *gorm.DB, root, stateDir string) *GitRepoSyncService {
	GitRepoRoot = root
	return &GitRepoSyncService{db: db, root: root, stateDir: stateDir, now: time.Now}
}

// Start runs a sync pass now and then every gitRepoTick until ctx ends.
func (s *GitRepoSyncService) Start(ctx context.Context) {
	if _, err := exec.LookPath("git"); err != nil {
		slog.Warn("git_repo: git binary not found; repository mirrors are disabled", "err", err)
		return
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		slog.Error("git_repo: cannot create mirror root", "root", s.root, "err", err)
		return
	}
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		slog.Error("git_repo: cannot create state dir", "dir", s.stateDir, "err", err)
		return
	}
	go func() {
		t := time.NewTicker(gitRepoTick)
		defer t.Stop()
		for {
			s.SyncOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// SyncOnce syncs every due mirror and removes mirrors whose instance is gone
// or disabled. Mirrors are synced one after another.
func (s *GitRepoSyncService) SyncOnce(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var instances []database.ToolInstance
	err := s.db.WithContext(ctx).Preload("ToolType").
		Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").
		Where("tool_types.name = ? AND tool_instances.enabled = ?", GitRepoToolType, true).
		Find(&instances).Error
	if err != nil {
		slog.Warn("git_repo: cannot list instances", "err", err)
		return
	}
	wanted := map[string]bool{}
	for _, inst := range instances {
		name := inst.LogicalName
		if !validMirrorName(name) {
			slog.Warn("git_repo: skipping instance with unusable logical name", "id", inst.ID, "logical_name", name)
			continue
		}
		wanted[name] = true
		if ctx.Err() != nil {
			return
		}
		s.syncInstance(ctx, inst)
	}
	s.removeStale(wanted)
}

var mirrorNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

func validMirrorName(n string) bool { return mirrorNameRe.MatchString(n) }

func (s *GitRepoSyncService) statusPath(name string) string {
	return filepath.Join(s.root, name+gitRepoStatusSuffix)
}

// ReadGitRepoStatus reads a mirror's status file from GitRepoRoot.
func ReadGitRepoStatus(name string) (*GitRepoStatus, error) {
	return readStatusFile(filepath.Join(GitRepoRoot, name+gitRepoStatusSuffix))
}

func readStatusFile(p string) (*GitRepoStatus, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var st GitRepoStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *GitRepoSyncService) writeStatus(st *GitRepoStatus) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := s.statusPath(st.LogicalName) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		slog.Warn("git_repo: cannot write status", "name", st.LogicalName, "err", err)
		return
	}
	_ = os.Rename(tmp, s.statusPath(st.LogicalName))
}

// removeStale deletes mirrors that have a status file (so Akmatori made
// them) but no enabled instance any more. Directories without a status file
// are never touched.
func (s *GitRepoSyncService) removeStale(wanted map[string]bool) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".tmp-") {
			_ = os.RemoveAll(filepath.Join(s.root, n)) // an interrupted clone
			continue
		}
		if !strings.HasSuffix(n, gitRepoStatusSuffix) {
			continue
		}
		name := strings.TrimSuffix(n, gitRepoStatusSuffix)
		if wanted[name] || !validMirrorName(name) {
			continue
		}
		if st, err := readStatusFile(filepath.Join(s.root, n)); err != nil || !st.Unmanaged {
			slog.Info("git_repo: removing mirror of a disabled or deleted instance", "name", name)
			_ = os.RemoveAll(filepath.Join(s.root, name))
		}
		_ = os.Remove(filepath.Join(s.root, n))
		_ = os.Remove(filepath.Join(s.stateDir, name+".known_hosts"))
	}
}

func (s *GitRepoSyncService) syncInstance(ctx context.Context, inst database.ToolInstance) {
	name := inst.LogicalName
	dir := filepath.Join(s.root, name)
	prev, _ := readStatusFile(s.statusPath(name))

	cfg, cfgErr := parseGitRepoSettings(inst.Settings)
	st := &GitRepoStatus{
		Name:        inst.Name,
		LogicalName: name,
		URL:         redactGitURL(cfg.URL),
		Branch:      cfg.Branch,
		Path:        filepath.Join(GitRepoRoot, name),
		HistoryDays: cfg.HistoryDays,
		Fingerprint: gitRepoFingerprint(cfg),
	}
	if prev != nil {
		st.LastSuccessAt, st.LastTrimAt = prev.LastSuccessAt, prev.LastTrimAt
		st.HeadCommit, st.HeadCommitAt, st.SizeBytes = prev.HeadCommit, prev.HeadCommitAt, prev.SizeBytes
		st.HistoryFrom = prev.HistoryFrom
		if st.Branch == "" && prev.Fingerprint == st.Fingerprint {
			st.Branch = prev.Branch
		}
	}

	// A directory we did not create is never touched, on this pass or any
	// later one: ownership is the status file without Unmanaged.
	if prev == nil || prev.Unmanaged {
		if _, err := os.Lstat(dir); err == nil {
			st.Unmanaged = true
			st.Error = fmt.Sprintf("%s already exists and is not managed by Akmatori; rename the instance's logical name or remove the directory", st.Path)
			s.finish(st, prev)
			return
		}
		prev = nil // the foreign directory is gone: start fresh
	}
	var pins []string
	if cfgErr == nil {
		pins, cfgErr = s.checkRemote(ctx, cfg.URL)
	}
	if cfgErr != nil {
		st.Error = cfgErr.Error()
		s.finish(st, prev)
		return
	}

	blockKey := fmt.Sprintf("%s|%d|%d", st.Fingerprint, cfg.MaxSizeMB, cfg.HistoryDays)
	if prev != nil && prev.Blocked != "" && prev.Blocked == blockKey {
		st.Blocked, st.Error = prev.Blocked, prev.Error
		s.finish(st, prev) // keep waiting for a settings change
		return
	}

	changed := prev != nil && prev.Fingerprint != st.Fingerprint
	unblocked := prev != nil && prev.Blocked != "" // a size-limit setting changed
	if !changed && !unblocked && prev != nil && prev.LastAttemptAt != nil &&
		s.now().Sub(*prev.LastAttemptAt) < time.Duration(cfg.IntervalSec)*time.Second {
		return // not due
	}
	if changed {
		slog.Info("git_repo: url, branch or auth changed; cloning again", "name", name)
		_ = os.RemoveAll(dir)
		st.HeadCommit, st.HeadCommitAt, st.SizeBytes, st.LastTrimAt, st.HistoryFrom = "", nil, 0, nil, nil
		if cfg.Branch == "" {
			st.Branch = ""
		}
	}

	env, cleanup, err := s.gitEnv(name, cfg, pins...)
	if err != nil {
		st.Error = err.Error()
		s.finish(st, prev)
		return
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		_ = os.RemoveAll(dir) // ours (checked above) but not a repository any more
		err = s.clone(ctx, env, cfg, dir, st)
		if err != nil {
			st.Error = err.Error()
			s.finish(st, prev)
			return
		}
	} else if err := s.fetch(ctx, env, cfg, dir, st); err != nil {
		st.Error = err.Error()
		s.finish(st, prev)
		return
	}

	if st.LastTrimAt == nil || s.now().Sub(*st.LastTrimAt) > gitRepoTrimEvery {
		if err := s.trim(ctx, env, cfg, dir, st); err != nil {
			st.Error = err.Error() // the mirror itself is current; retried next pass
		}
	}

	st.SizeBytes = mirrorSize(dir)
	if cfg.MaxSizeMB > 0 && st.SizeBytes > int64(cfg.MaxSizeMB)<<20 {
		_ = os.RemoveAll(dir)
		st.Error = fmt.Sprintf("mirror is %d MB, over git_max_size_mb=%d; removed. Lower git_history_days or raise the limit", st.SizeBytes>>20, cfg.MaxSizeMB)
		st.Blocked = blockKey
		st.HeadCommit, st.HeadCommitAt = "", nil
		s.finish(st, prev)
		return
	}
	s.readHead(ctx, env, dir, st)
	now := s.now().UTC()
	st.LastSuccessAt = &now
	s.finish(st, prev)
}

func (s *GitRepoSyncService) finish(st, prev *GitRepoStatus) {
	now := s.now().UTC()
	st.LastAttemptAt = &now
	if st.Error != "" && (prev == nil || prev.Error != st.Error) {
		slog.Warn("git_repo: sync failed", "name", st.LogicalName, "err", st.Error)
	}
	s.writeStatus(st)
}

func (s *GitRepoSyncService) clone(ctx context.Context, env []string, cfg gitRepoSettings, dir string, st *GitRepoStatus) error {
	tmp, err := os.MkdirTemp(s.root, ".tmp-"+st.LogicalName+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	args := []string{"clone", "--quiet", "--no-tags", "--single-branch", "--depth", "1"}
	if cfg.Branch != "" {
		args = append(args, "--branch", cfg.Branch)
	}
	args = append(args, "--", cfg.URL, tmp)
	cctx, cancel := context.WithTimeout(ctx, gitRepoCloneTimeout)
	defer cancel()
	if _, err := runGit(cctx, env, "", args...); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	if st.Branch == "" {
		out, err := runGit(ctx, env, tmp, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return fmt.Errorf("detect default branch: %w", err)
		}
		st.Branch = strings.TrimSpace(out)
	}
	if cfg.HistoryDays > 0 {
		if err := s.deepen(ctx, env, cfg, tmp, st); err != nil {
			return err
		}
	}
	_ = os.Chmod(tmp, 0o755) // MkdirTemp creates 0700; the worker must read it
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	now := s.now().UTC()
	st.LastTrimAt = &now
	return nil
}

// deepen (or shorten) the history to the configured window. When the window
// holds no commits (an idle repository), git refuses --shallow-since; the
// mirror then keeps only the newest commit. Other failures are returned.
func (s *GitRepoSyncService) deepen(ctx context.Context, env []string, cfg gitRepoSettings, dir string, st *GitRepoStatus) error {
	since := s.now().Add(-time.Duration(cfg.HistoryDays) * 24 * time.Hour)
	cctx, cancel := context.WithTimeout(ctx, gitRepoCloneTimeout)
	defer cancel()
	_, err := runGit(cctx, env, dir, "fetch", "--quiet", "--no-tags",
		"--shallow-since="+strconv.FormatInt(since.Unix(), 10), "origin", branchRefspec(st.Branch))
	if err != nil && strings.Contains(err.Error(), "no commits selected") {
		st.HistoryFrom = nil
		return s.fetchDepth1(ctx, env, dir, st)
	}
	if err != nil {
		return fmt.Errorf("apply history window: %w", err)
	}
	from := since.UTC()
	st.HistoryFrom = &from
	return nil
}

// fetchDepth1 shortens the mirror to the newest commit.
func (s *GitRepoSyncService) fetchDepth1(ctx context.Context, env []string, dir string, st *GitRepoStatus) error {
	cctx, cancel := context.WithTimeout(ctx, gitRepoFetchTimeout)
	defer cancel()
	if _, err := runGit(cctx, env, dir, "fetch", "--quiet", "--no-tags", "--depth=1", "origin", branchRefspec(st.Branch)); err != nil {
		return fmt.Errorf("fetch newest commit: %w", err)
	}
	return nil
}

func (s *GitRepoSyncService) fetch(ctx context.Context, env []string, cfg gitRepoSettings, dir string, st *GitRepoStatus) error {
	if st.Branch == "" {
		out, err := runGit(ctx, env, dir, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return fmt.Errorf("detect branch: %w", err)
		}
		st.Branch = strings.TrimSpace(out)
	}
	cctx, cancel := context.WithTimeout(ctx, gitRepoFetchTimeout)
	defer cancel()
	args := []string{"fetch", "--quiet", "--no-tags"}
	if cfg.HistoryDays == 0 {
		args = append(args, "--depth=1") // newest commit only: do not grow history
	}
	if _, err := runGit(cctx, env, dir, append(args, "origin", branchRefspec(st.Branch))...); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if _, err := runGit(ctx, env, dir, "reset", "--quiet", "--hard", "refs/remotes/origin/"+st.Branch); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	return nil
}

// trim cuts history older than the window (or everything but the newest
// commit for git_history_days=0) and drops the unreachable objects, so a
// mirror of a busy repository stays bounded. A failure is kept in the status
// and the trim is retried on the next pass.
func (s *GitRepoSyncService) trim(ctx context.Context, env []string, cfg gitRepoSettings, dir string, st *GitRepoStatus) error {
	var err error
	if cfg.HistoryDays > 0 {
		err = s.deepen(ctx, env, cfg, dir, st)
	} else {
		err = s.fetchDepth1(ctx, env, dir, st)
	}
	if err != nil {
		return err
	}
	if _, err := runGit(ctx, env, dir, "reflog", "expire", "--expire=now", "--all"); err != nil {
		return fmt.Errorf("trim: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, gitRepoCloneTimeout)
	defer cancel()
	if _, err := runGit(cctx, env, dir, "gc", "--quiet", "--prune=now"); err != nil {
		return fmt.Errorf("trim: %w", err)
	}
	now := s.now().UTC()
	st.LastTrimAt = &now
	return nil
}

func (s *GitRepoSyncService) readHead(ctx context.Context, env []string, dir string, st *GitRepoStatus) {
	out, err := runGit(ctx, env, dir, "log", "-1", "--format=%H %ct")
	if err != nil {
		return
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return
	}
	st.HeadCommit = f[0]
	if ts, err := strconv.ParseInt(f[1], 10, 64); err == nil {
		t := time.Unix(ts, 0).UTC()
		st.HeadCommitAt = &t
	}
}

func branchRefspec(branch string) string {
	return "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
}

// gitEnv builds the environment for git. Credentials go only into the
// environment (an http.extraHeader through GIT_CONFIG_*, or a temporary key
// file for ssh), never into the mirror's .git/config, which the worker can
// read.
//
// pins are curl --resolve entries (host:port:addr) from checkRemote: git
// connects only to the addresses that were checked, so a DNS answer that
// changes after the check cannot point the fetch at loopback or metadata.
// Redirects are refused for the same reason (http.followRedirects=false).
func (s *GitRepoSyncService) gitEnv(name string, cfg gitRepoSettings, pins ...string) ([]string, func(), error) {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "SSH_AUTH_SOCK=") {
			continue
		}
		env = append(env, kv)
	}
	protocols := "https:http:ssh"
	if s.allowFile {
		protocols += ":file"
	}
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"SSH_ASKPASS=/bin/false",
		"GIT_ALLOW_PROTOCOL="+protocols,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"LC_ALL=C",
	)
	gitCfg := [][2]string{{"http.followRedirects", "false"}}
	for _, p := range pins {
		gitCfg = append(gitCfg, [2]string{"http.curloptResolve", p})
	}
	cleanup := func() {}
	switch cfg.Auth {
	case "token":
		user := cfg.Username
		if user == "" {
			user = "git"
		}
		basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + cfg.Token))
		gitCfg = append(gitCfg, [2]string{"http.extraHeader", "Authorization: Basic " + basic})
	case "ssh_key":
		keyFile, err := os.CreateTemp(s.stateDir, ".key-")
		if err != nil {
			return nil, cleanup, err
		}
		key := strings.ReplaceAll(cfg.SSHKey, "\r\n", "\n")
		if !strings.HasSuffix(key, "\n") {
			key += "\n"
		}
		_ = keyFile.Chmod(0o600)
		if _, err := keyFile.WriteString(key); err != nil {
			keyFile.Close()
			os.Remove(keyFile.Name())
			return nil, cleanup, err
		}
		keyFile.Close()
		cleanup = func() { os.Remove(keyFile.Name()) }
		kh := filepath.Join(s.stateDir, name+".known_hosts")
		strict := "accept-new" // pin the host key on first connect
		if strings.TrimSpace(cfg.KnownHosts) != "" {
			if err := os.WriteFile(kh, []byte(strings.TrimSpace(cfg.KnownHosts)+"\n"), 0o600); err != nil {
				cleanup()
				return nil, func() {}, err
			}
			strict = "yes"
		}
		env = append(env, "GIT_SSH_COMMAND=ssh -F /dev/null -i "+shellQuote(keyFile.Name())+
			" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=20"+
			" -o UserKnownHostsFile="+shellQuote(kh)+" -o StrictHostKeyChecking="+strict)
	default:
		env = append(env, "GIT_SSH_COMMAND=ssh -F /dev/null -o BatchMode=yes -o ConnectTimeout=20"+
			" -o UserKnownHostsFile="+shellQuote(filepath.Join(s.stateDir, name+".known_hosts"))+
			" -o StrictHostKeyChecking=accept-new")
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(gitCfg)))
	for i, kv := range gitCfg {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return env, cleanup, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func runGit(ctx context.Context, env []string, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() == context.DeadlineExceeded {
			msg = "timed out " + msg
		}
		if len(msg) > gitRepoErrMax {
			msg = msg[:gitRepoErrMax] + "…"
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

func mirrorSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func gitRepoFingerprint(cfg gitRepoSettings) string {
	h := sha256.Sum256([]byte(cfg.URL + "\x00" + cfg.Branch + "\x00" + cfg.Auth + "\x00" + cfg.Username))
	return hex.EncodeToString(h[:8])
}

var scpLikeRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/\\].*$|^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:/.+$`)

// parseGitRepoSettings reads and validates the instance settings.
func parseGitRepoSettings(m map[string]interface{}) (gitRepoSettings, error) {
	cfg := gitRepoSettings{
		URL:         strings.TrimSpace(settingString(m, "git_url")),
		Branch:      strings.TrimSpace(settingString(m, "git_branch")),
		Auth:        strings.TrimSpace(settingString(m, "git_auth")),
		Username:    strings.TrimSpace(settingString(m, "git_username")),
		Token:       strings.TrimSpace(settingString(m, "git_token")),
		SSHKey:      strings.TrimSpace(settingString(m, "git_ssh_private_key")),
		KnownHosts:  settingString(m, "git_ssh_known_hosts"),
		HistoryDays: settingInt(m, "git_history_days", gitRepoDefaultDays),
		IntervalSec: settingInt(m, "git_sync_interval", gitRepoDefaultInterval),
		MaxSizeMB:   settingInt(m, "git_max_size_mb", gitRepoDefaultMaxMB),
	}
	if cfg.Auth == "" {
		switch {
		case cfg.SSHKey != "":
			cfg.Auth = "ssh_key"
		case cfg.Token != "":
			cfg.Auth = "token"
		default:
			cfg.Auth = "none"
		}
	}
	if cfg.HistoryDays < 0 {
		cfg.HistoryDays = 0
	}
	if cfg.IntervalSec < gitRepoMinInterval {
		cfg.IntervalSec = gitRepoMinInterval
	}
	if cfg.URL == "" {
		return cfg, errors.New("git_url is required")
	}
	if strings.HasPrefix(cfg.URL, "-") {
		return cfg, errors.New("git_url must not start with '-'")
	}
	if cfg.Branch != "" && (strings.HasPrefix(cfg.Branch, "-") || strings.ContainsAny(cfg.Branch, " :~^?*[\\") || strings.Contains(cfg.Branch, "..")) {
		return cfg, fmt.Errorf("git_branch %q is not a valid branch name", cfg.Branch)
	}
	scheme := ""
	if u, err := url.Parse(cfg.URL); err == nil && u.Scheme != "" && strings.Contains(cfg.URL, "://") {
		scheme = strings.ToLower(u.Scheme)
		if u.User != nil {
			if _, hasPass := u.User.Password(); hasPass {
				return cfg, errors.New("git_url must not contain a password; put the token in git_token")
			}
		}
		if u.Host == "" && scheme != "file" { // file:// is refused later unless a test allows it
			return cfg, errors.New("git_url has no host")
		}
	} else if scpLikeRe.MatchString(cfg.URL) {
		scheme = "ssh"
	} else {
		return cfg, errors.New("git_url must be https://, http://, ssh:// or user@host:path")
	}
	switch scheme {
	case "https", "http", "ssh", "file":
	default:
		return cfg, fmt.Errorf("git_url scheme %q is not allowed (https, http, ssh)", scheme)
	}
	switch cfg.Auth {
	case "none":
	case "token":
		if cfg.Token == "" {
			return cfg, errors.New("git_auth=token needs git_token")
		}
		if scheme != "https" && scheme != "http" {
			return cfg, errors.New("git_auth=token works only with an https:// URL")
		}
	case "ssh_key":
		if cfg.SSHKey == "" {
			return cfg, errors.New("git_auth=ssh_key needs git_ssh_private_key")
		}
		if scheme != "ssh" {
			return cfg, errors.New("git_auth=ssh_key works only with an ssh:// or user@host:path URL")
		}
	default:
		return cfg, fmt.Errorf("git_auth %q is not one of none, token, ssh_key", cfg.Auth)
	}
	return cfg, nil
}

// checkRemote refuses remotes that resolve to loopback, link-local (cloud
// metadata), unspecified or multicast addresses, like the MCP NetPolicy.
// Private ranges are allowed: internal git servers are the main use. For
// http(s) it returns curl --resolve pins so git connects to exactly the
// checked addresses. ssh has no such hook: its dial is checked here only.
func (s *GitRepoSyncService) checkRemote(ctx context.Context, raw string) ([]string, error) {
	host, port, scheme := "", "", ""
	if u, err := url.Parse(raw); err == nil && strings.Contains(raw, "://") {
		scheme = strings.ToLower(u.Scheme)
		if scheme == "file" {
			if s.allowFile {
				return nil, nil
			}
			return nil, errors.New("file:// remotes are not allowed")
		}
		host, port = u.Hostname(), u.Port()
	} else if i := strings.Index(raw, "@"); i >= 0 {
		rest := raw[i+1:]
		host, scheme = rest[:strings.Index(rest, ":")], "ssh"
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil, errors.New("git_url has no host")
	}
	if s.allowLoopback {
		return nil, nil
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(rctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %v", host, err)
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	var list []string
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, fmt.Errorf("git_url host %s resolves to %s, which is not allowed", host, ip)
		}
		if ip.To4() != nil {
			list = append(list, ip.String())
		} else {
			list = append(list, "["+ip.String()+"]")
		}
	}
	if scheme != "http" && scheme != "https" || net.ParseIP(host) != nil || len(list) == 0 {
		return nil, nil
	}
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	return []string{host + ":" + port + ":" + strings.Join(list, ",")}, nil
}

// redactGitURL drops user info from an https URL for display.
func redactGitURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && strings.Contains(raw, "://") && u.User != nil {
		u.User = nil
		return u.String()
	}
	return raw
}

func settingString(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func settingInt(m map[string]interface{}, k string, def int) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// gitRepoDetails renders the connected-tools details of one git_repo
// instance from its status file.
func gitRepoDetails(tool database.ToolInstance) string {
	name := toolLogicalName(tool)
	var b strings.Builder
	path := filepath.Join(GitRepoRoot, name)
	fmt.Fprintf(&b, "Path: `%s` (read-only mirror; status: `%s%s`)\n", path, path, gitRepoStatusSuffix)
	st, err := ReadGitRepoStatus(name)
	if err != nil {
		b.WriteString("Status: not synced yet\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Remote: %s, branch %s\n", st.URL, gitOrDash(st.Branch))
	if st.HistoryDays > 0 {
		fmt.Fprintf(&b, "History: about the last %d days (shallow)\n", st.HistoryDays)
	} else {
		b.WriteString("History: newest commit only\n")
	}
	if st.Error != "" {
		fmt.Fprintf(&b, "Last sync error: %s\n", st.Error)
	}
	return b.String()
}

func gitOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// GitRepoStatuses returns the status of every mirror, sorted by name; used
// by tests and diagnostics.
func (s *GitRepoSyncService) GitRepoStatuses() []GitRepoStatus {
	entries, _ := os.ReadDir(s.root)
	var out []GitRepoStatus
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), gitRepoStatusSuffix) {
			if st, err := readStatusFile(filepath.Join(s.root, e.Name())); err == nil {
				out = append(out, *st)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LogicalName < out[j].LogicalName })
	return out
}
