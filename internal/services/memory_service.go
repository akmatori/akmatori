package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MemoryService manages cross-incident memory CRUD and on-disk synchronization.
// PostgreSQL is the source of truth; files mirror to
// /akmatori/memory/<scope>/<id>-<slug>.md plus a per-scope MEMORY.md manifest.
type MemoryService struct {
	db        *gorm.DB
	memoryDir string
	syncMu    sync.Mutex
}

// NewMemoryService creates a new memory service rooted at <dataDir>/memory.
func NewMemoryService(dataDir string) *MemoryService {
	dir := filepath.Join(dataDir, "memory")
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Warn("failed to create memory directory", "dir", dir, "err", err)
	}
	return &MemoryService{
		db:        database.GetDB(),
		memoryDir: dir,
	}
}

// MemoryDir returns the root memory directory (used by tests and the gateway tool).
func (s *MemoryService) MemoryDir() string {
	return s.memoryDir
}

// CreateMemory inserts a new memory and syncs files. Caller-supplied
// IncidentUUID and CreatedBy are passed through verbatim.
func (s *MemoryService) CreateMemory(m *database.Memory) (*database.Memory, error) {
	if err := s.validate(m); err != nil {
		return nil, err
	}
	m.Scope = strings.TrimSpace(m.Scope)
	m.Name = strings.TrimSpace(m.Name)
	if err := s.db.Create(m).Error; err != nil {
		return nil, fmt.Errorf("failed to create memory: %w", err)
	}
	if err := s.SyncMemoryFiles(); err != nil {
		return nil, fmt.Errorf("memory created but file sync failed: %w", err)
	}
	return m, nil
}

// UpdateMemory mutates an existing memory by ID. All fields use the same
// "empty = leave unchanged" convention so that PUT /api/memories/{id} can
// supply only the fields the caller actually wants to change. Without this
// guard a partial PUT (e.g. just {"type":"feedback"}) would clobber the
// existing Description with "" and fail validation.
func (s *MemoryService) UpdateMemory(id uint, m *database.Memory) (*database.Memory, error) {
	var existing database.Memory
	if err := s.db.First(&existing, id).Error; err != nil {
		return nil, errMemoryNotFound
	}
	merged := existing
	if m.Scope != "" {
		merged.Scope = strings.TrimSpace(m.Scope)
	}
	if m.Type != "" {
		merged.Type = m.Type
	}
	if m.Name != "" {
		merged.Name = strings.TrimSpace(m.Name)
	}
	if m.Description != "" {
		merged.Description = m.Description
	}
	if m.Body != "" {
		merged.Body = m.Body
	}
	if m.IncidentUUID != "" {
		merged.IncidentUUID = m.IncidentUUID
	}
	if m.CreatedBy != "" {
		merged.CreatedBy = m.CreatedBy
	}
	if err := s.validate(&merged); err != nil {
		return nil, err
	}
	if err := s.db.Save(&merged).Error; err != nil {
		return nil, fmt.Errorf("failed to update memory: %w", err)
	}
	if err := s.SyncMemoryFiles(); err != nil {
		return nil, fmt.Errorf("memory updated but file sync failed: %w", err)
	}
	return &merged, nil
}

// UpsertByName inserts or updates a memory keyed by (scope, name).
// Used by the extractor and Slack feedback flows for idempotent writes —
// e.g. a Slack retry firing a second classify on the same message, or two
// feedback submissions producing the same generated name.
//
// The implementation uses a database-level upsert (ON CONFLICT (scope, name)
// DO UPDATE) so concurrent callers cannot collide on the unique index. The
// previous SELECT-then-INSERT-or-UPDATE pattern was racy: two callers could
// both miss the lookup and then one Create would fail with a unique-constraint
// error, dropping the later update on a path that's contractually idempotent.
//
// On conflict, type/description/body/incident_uuid are overwritten with the
// new request — every caller of UpsertByName supplies a complete record, so
// there's no "merge selectively" semantic. The existing row's ID and
// created_at are preserved. created_by is intentionally excluded from the
// conflict update so operator authorship stays sticky: an agent re-ingest
// cannot silently flip `operator` rows to `agent` (see upsertByNameNoSync
// comments and TestIngestFromDisk_AgentRewriteDoesNotOverwriteOperatorAuthorship).
//
// Both PostgreSQL and SQLite (≥3.24) support the ON CONFLICT clause used here.
func (s *MemoryService) UpsertByName(m *database.Memory) (*database.Memory, error) {
	saved, err := s.upsertByNameNoSync(m)
	if err != nil {
		return nil, err
	}
	if err := s.SyncMemoryFiles(); err != nil {
		return nil, fmt.Errorf("memory upserted but file sync failed: %w", err)
	}
	return saved, nil
}

// DeleteMemory removes a memory by ID and re-syncs files.
func (s *MemoryService) DeleteMemory(id uint) error {
	result := s.db.Delete(&database.Memory{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete memory: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return errMemoryNotFound
	}
	if err := s.SyncMemoryFiles(); err != nil {
		return fmt.Errorf("memory deleted but file sync failed: %w", err)
	}
	return nil
}

// GetMemory retrieves a single memory by ID.
func (s *MemoryService) GetMemory(id uint) (*database.Memory, error) {
	var m database.Memory
	if err := s.db.First(&m, id).Error; err != nil {
		return nil, errMemoryNotFound
	}
	return &m, nil
}

// ListMemoriesByScope returns all memories in a scope ordered by created_at desc.
// If scope is empty, returns all memories across scopes.
func (s *MemoryService) ListMemoriesByScope(scope string) ([]database.Memory, error) {
	var memories []database.Memory
	q := s.db.Order("created_at desc")
	if scope != "" {
		q = q.Where("scope = ?", scope)
	}
	if err := q.Find(&memories).Error; err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	return memories, nil
}

// ListMemories applies optional scope and type filters. Empty filters mean
// "no filter on that field". Used by the REST API filter endpoint.
func (s *MemoryService) ListMemories(scope, memType string) ([]database.Memory, error) {
	var memories []database.Memory
	q := s.db.Order("created_at desc")
	if scope != "" {
		q = q.Where("scope = ?", scope)
	}
	if memType != "" {
		q = q.Where("type = ?", memType)
	}
	if err := q.Find(&memories).Error; err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	return memories, nil
}

// ListAllScopes returns the set of distinct scopes present in the table.
func (s *MemoryService) ListAllScopes() ([]string, error) {
	var scopes []string
	if err := s.db.Model(&database.Memory{}).Distinct("scope").Order("scope asc").Pluck("scope", &scopes).Error; err != nil {
		return nil, fmt.Errorf("failed to list scopes: %w", err)
	}
	return scopes, nil
}

// CountByIncidentUUID returns how many memories already record this incident
// as origin. When createdBy is non-empty, the count is restricted to memories
// authored by that role (e.g. "agent" or "operator").
//
// The extractor passes MemoryCreatedByAgent so that operator feedback written
// against the same incident — either via the UI feedback endpoint or the
// Slack feedback classifier — does NOT mark extraction as "already done"
// and short-circuit the post-completion run.
func (s *MemoryService) CountByIncidentUUID(incidentUUID string, createdBy string) (int64, error) {
	var n int64
	q := s.db.Model(&database.Memory{}).Where("incident_uuid = ?", incidentUUID)
	if createdBy != "" {
		q = q.Where("created_by = ?", createdBy)
	}
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("failed to count memories by incident: %w", err)
	}
	return n, nil
}

// TruncateMemoryBody trims s to at most MemoryBodyMaxBytes bytes without
// splitting a UTF-8 character mid-byte. No ellipsis is added — body content
// is large and the size cap is approximate by design.
//
// PostgreSQL text columns require valid UTF-8, so naive byte slicing on
// multibyte input would cause INSERT to fail with "invalid byte sequence".
// Used by both feedback ingest paths (HTTP and Slack).
func TruncateMemoryBody(s string) string {
	if len(s) <= MemoryBodyMaxBytes {
		return s
	}
	cut := MemoryBodyMaxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// errMemoryNotFound is the canonical "not found" error returned to handlers.
// Use errors.Is(err, errMemoryNotFound) to detect.
var errMemoryNotFound = errors.New("memory not found")

// IsMemoryNotFoundErr reports whether err signals a missing memory.
func IsMemoryNotFoundErr(err error) bool {
	return errors.Is(err, errMemoryNotFound)
}

// validate enforces field caps and type membership.
func (s *MemoryService) validate(m *database.Memory) error {
	scope := strings.TrimSpace(m.Scope)
	if scope == "" {
		return fmt.Errorf("scope cannot be empty")
	}
	// validMemoryName enforces both the slug pattern AND the
	// MemoryNameMaxLen cap, which keeps the scope under the filesystem
	// NAME_MAX limit. Same helper for both fields by design.
	if !validMemoryName(scope) {
		return fmt.Errorf("scope must be slug-safe (lowercase a-z, 0-9, hyphen) and ≤%d chars", MemoryNameMaxLen)
	}
	if !ValidMemoryType(m.Type) {
		return fmt.Errorf("invalid memory type %q (allowed: %s)", m.Type, strings.Join(AllMemoryTypes(), ", "))
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !validMemoryName(name) {
		return fmt.Errorf("name must be slug-safe (lowercase a-z, 0-9, hyphen) and ≤%d chars", MemoryNameMaxLen)
	}
	if len(m.Description) > MemoryDescriptionMaxLen {
		return fmt.Errorf("description exceeds %d chars", MemoryDescriptionMaxLen)
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("description cannot be empty")
	}
	if len(m.Body) > MemoryBodyMaxBytes {
		return fmt.Errorf("body exceeds %d bytes", MemoryBodyMaxBytes)
	}
	if m.CreatedBy != "" && m.CreatedBy != MemoryCreatedByAgent && m.CreatedBy != MemoryCreatedByOperator {
		return fmt.Errorf("created_by must be %q, %q, or empty", MemoryCreatedByAgent, MemoryCreatedByOperator)
	}
	return nil
}

var memoryNameRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// validMemoryName enforces a slug-safe name. We use the same convention for
// scope dirs and filenames so the on-disk layout has no surprises.
func validMemoryName(name string) bool {
	if name == "" || len(name) > MemoryNameMaxLen {
		return false
	}
	return memoryNameRegex.MatchString(name)
}

// SlugifyMemoryName converts a free-form description/title into a slug-safe
// memory name. Returns "memory" if the input is empty after slugification.
// Exposed so the Slack feedback flow and extractor can derive names from
// LLM-generated summaries.
func SlugifyMemoryName(s string) string {
	out := slugify(s)
	// slugify already lowercases, hyphenates, and trims to 100 chars; "runbook"
	// fallback is its choice. We override to "memory" so log lines disambiguate.
	if out == "" || out == "runbook" {
		return "memory"
	}
	return out
}

// Manifest caps. Manifests are injected into prompts and stay small; a hard
// byte cap also protects the prompt from runaway growth as memory accumulates.
const (
	manifestMaxLines = 200
	manifestMaxBytes = 25 * 1024
	manifestFile     = "MEMORY.md"

	// maxMemoryFileBytes caps the per-file size IngestFromDisk will read.
	// The agent worker has rw access to the shared memory mount, so a
	// prompt-injected or buggy memory-writer could plant a huge .md and
	// OOM the API. 1 MiB is well above any reasonable agent-produced
	// memory and small enough that a hostile file is rejected cheaply.
	maxMemoryFileBytes int64 = 1 << 20
)

// SyncMemoryFiles writes one directory per scope under memoryDir:
//
//	<memoryDir>/<scope>/MEMORY.md           — manifest (≤ manifestMax* caps)
//	<memoryDir>/<scope>/<id>-<name>.md      — one file per memory, with
//	                                          YAML frontmatter and body.
//
// Stale scope directories and files are removed.
//
// The agent worker has rw access to /akmatori/memory, so a prompt-injected
// memory-writer could plant a symlink at <memoryDir>/<scope> or
// <memoryDir>/<scope>/<file> pointing at API-owned files outside the memory
// tree (e.g. /akmatori/secrets/postgres_password). Plain os.WriteFile would
// follow such a symlink and truncate the target as UID 1000. To prevent that,
// every path lookup goes through *os.Root (openat-style, rejects symlinks
// that escape the root) and every file write uses O_NOFOLLOW (rejects a
// symlink at the final component). Scope dirs are Lstat'd before any write
// so a swapped-in symlink/non-directory is detected and skipped rather than
// followed.
func (s *MemoryService) SyncMemoryFiles() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// /akmatori/memory is shared between the API (UID 1000) and the agent
	// worker (UID 1001, different group) — the memory-writer subagent writes
	// directly into the scope directories the API creates here. Default 0755
	// dirs / 0644 files would block the agent's "other" writes, so widen to
	// 0777 / 0666 explicitly. init-dirs runs `chmod -R g+rwX,o+rwX
	// /akmatori/memory` once at compose-up; this MkdirAll + chmod keeps the
	// widening idempotent for scope directories the API creates later.
	if err := os.MkdirAll(s.memoryDir, 0777); err != nil {
		return fmt.Errorf("failed to create memory directory: %w", err)
	}
	if err := os.Chmod(s.memoryDir, 0777); err != nil {
		slog.Warn("failed to widen memory directory permissions", "dir", s.memoryDir, "err", err)
	}

	root, err := os.OpenRoot(s.memoryDir)
	if err != nil {
		return fmt.Errorf("open memory root: %w", err)
	}
	defer root.Close()

	var memories []database.Memory
	if err := s.db.Order("created_at desc").Find(&memories).Error; err != nil {
		return fmt.Errorf("failed to query memories: %w", err)
	}

	byScope := make(map[string][]database.Memory)
	for _, m := range memories {
		byScope[m.Scope] = append(byScope[m.Scope], m)
	}

	expectedScopes := make(map[string]bool, len(byScope))
	for scope, entries := range byScope {
		expectedScopes[scope] = true

		// Lstat before any mkdir/write: if the agent planted a symlink or a
		// non-directory at this scope path, refuse to follow it. We do NOT
		// remove it — that's the agent's mount and an operator may want to
		// inspect what got planted before it disappears.
		if info, lerr := root.Lstat(scope); lerr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				slog.Warn("memory sync: scope path is a symlink; skipping", "scope", scope)
				continue
			}
			if !info.IsDir() {
				slog.Warn("memory sync: scope path is not a directory; skipping", "scope", scope, "mode", info.Mode().String())
				continue
			}
		}

		if err := root.MkdirAll(scope, 0777); err != nil {
			return fmt.Errorf("failed to create scope dir %s: %w", scope, err)
		}
		// MkdirAll honors the process umask, so re-chmod to the intended mode.
		if err := root.Chmod(scope, 0777); err != nil {
			slog.Warn("failed to widen scope dir permissions", "scope", scope, "err", err)
		}

		scopeRoot, err := root.OpenRoot(scope)
		if err != nil {
			slog.Warn("memory sync: open scope subroot", "scope", scope, "err", err)
			continue
		}

		expectedFiles := map[string]bool{manifestFile: true}
		var writeErr error
		for _, m := range entries {
			fileName := fmt.Sprintf("%d-%s.md", m.ID, m.Name)
			expectedFiles[fileName] = true
			body := renderMemoryFile(m)
			if err := writeMemoryFileInRoot(scopeRoot, fileName, []byte(body)); err != nil {
				writeErr = fmt.Errorf("failed to write memory file %s/%s: %w", scope, fileName, err)
				break
			}
		}
		if writeErr != nil {
			scopeRoot.Close()
			return writeErr
		}

		manifest := renderManifest(scope, entries)
		if err := writeMemoryFileInRoot(scopeRoot, manifestFile, []byte(manifest)); err != nil {
			scopeRoot.Close()
			return fmt.Errorf("failed to write manifest %s/%s: %w", scope, manifestFile, err)
		}

		if err := removeStaleFilesInRoot(scopeRoot, expectedFiles); err != nil {
			slog.Warn("failed to clean stale memory files", "scope", scope, "err", err)
		}
		scopeRoot.Close()
	}

	if err := removeStaleScopesInRoot(root, expectedScopes); err != nil {
		slog.Warn("failed to clean stale memory scope dirs", "err", err)
	}

	return nil
}

// writeTmpCounter disambiguates concurrent writes to the same scope from
// within a single process. Combined with pid + nanosecond clock + the
// original basename, it produces tmp names that cannot collide in practice.
var writeTmpCounter uint64

// writeMemoryFileInRoot writes data to <root>/<name> using a temp-file-and-
// rename pattern: open a fresh tmp file with O_CREATE|O_EXCL|O_NOFOLLOW,
// write the data, then atomically rename it onto <name>.
//
// Why this shape: the agent worker has rw access to the memory mount and a
// prompt-injected memory-writer could plant a symlink, FIFO, or socket at
// the canonical slot. Opening <name> directly for write is TOCTOU-unsafe —
// even with a pre-Lstat, a writer could swap a FIFO with a pre-attached
// reader into the slot between the check and the open, and our memory
// content would flow to the attacker's reader instead of landing on disk.
// rename(2) on Linux replaces the destination entry atomically without
// opening it for write, so an attacker-planted FIFO/symlink/regular file
// at <name> is replaced by our fresh regular file and the original target
// (for a symlink) is never touched.
//
// O_EXCL on the tmp open guarantees we create a fresh regular file at a
// previously-unused name. O_NOFOLLOW rejects a symlink at the tmp slot
// too. Any tmp files left behind by a crash between open and rename are
// cleaned up by removeStaleFilesInRoot on the next successful sync (their
// names aren't in expectedFiles).
func writeMemoryFileInRoot(root *os.Root, name string, data []byte) error {
	tmpName := fmt.Sprintf("%s.tmp.%d.%d.%d", name, os.Getpid(), time.Now().UnixNano(), atomic.AddUint64(&writeTmpCounter, 1))

	openFlags := os.O_WRONLY | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW
	f, err := root.OpenFile(tmpName, openFlags, 0666)
	if err != nil {
		return fmt.Errorf("open tmp %s: %w", tmpName, err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		_ = root.Remove(tmpName)
		return writeErr
	}
	if closeErr != nil {
		_ = root.Remove(tmpName)
		return closeErr
	}
	if err := root.Rename(tmpName, name); err != nil {
		_ = root.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, name, err)
	}
	return nil
}

// IngestFromDisk walks <memoryDir>/<scope>/*.md (skipping MEMORY.md) and
// upserts each well-formed memory file into the database keyed by (scope, name).
// Paths that escape a known scope directory are rejected. Files that fail to
// parse or validate are logged and skipped — ingest is best-effort and must
// never partial-fail the caller (called from post-incident hooks).
//
// Idempotency: re-running on the same directory state is a no-op at the row
// level (UpsertByName conflict on (scope, name) overwrites in place). The
// caller (UpdateIncidentComplete) tolerates being called more than once for
// the same incident.
//
// CreatedBy is forced to "agent" for every parsed file regardless of what the
// frontmatter claims. The memory directory is rw-mounted into the agent worker
// so a prompt-injected memory-writer could write `created_by: operator` to
// escalate authorship; the file-level field is untrusted. Operator authorship
// is still preserved at the DB level because upsertByNameNoSync's DoUpdates
// excludes created_by, so an operator-authored row that already exists in DB
// (created via the API's authenticated CreateMemory / UpsertByName paths) is
// never flipped to agent on re-ingest of its SyncMemoryFiles-written canonical
// file.
//
// All files are read and parsed first, then upserted in a single batch. The
// per-row UpsertByName path otherwise triggers SyncMemoryFiles, which renames
// new files into the canonical <id>-<name>.md form and purges "stale" files
// from the scope dir — including any file the read pass hasn't gotten to yet.
// Collecting all parsed memories first decouples reading from rewriting and
// keeps the cost linear instead of O(N²) full re-syncs.
func (s *MemoryService) IngestFromDisk(ctx context.Context) error {
	if s.memoryDir == "" {
		return fmt.Errorf("memory directory not configured")
	}

	rootAbs, err := filepath.Abs(s.memoryDir)
	if err != nil {
		return fmt.Errorf("resolve memory dir: %w", err)
	}

	// os.OpenRoot pins the directory tree to a single inode and rejects any
	// path component that escapes the root via symlink or `..`. The previous
	// implementation used path-string operations (os.ReadDir on rootAbs and
	// then on filepath.Join(rootAbs, scope)), which only protected against
	// symlinks at the *final* component via O_NOFOLLOW. A prompt-injected
	// memory-writer could plant a symlink at <memoryDir>/<scope> pointing
	// outside the tree and the path-string walk would silently follow it
	// during ReadDir. Going through *os.Root for all subsequent reads /
	// opens makes the whole walk rooted at the original inode.
	root, err := os.OpenRoot(rootAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open memory root: %w", err)
	}
	defer root.Close()

	scopeEntries, err := readDirFromRoot(root, ".")
	if err != nil {
		return fmt.Errorf("read memory dir: %w", err)
	}

	// Two-pass walk: collect first, upsert second. See doc comment above.
	type parsedEntry struct {
		mem       *database.Memory
		canonical bool // true if filename matches `<id>-<name>.md`
		tombstone bool // true if frontmatter carried `deleted: true`
	}
	var parsed []*parsedEntry
	for _, scopeEnt := range scopeEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !scopeEnt.IsDir() {
			continue
		}
		scope := scopeEnt.Name()
		if !validMemoryName(scope) {
			slog.Debug("memory ingest: skipping non-slug scope dir", "scope", scope)
			continue
		}

		files, err := readDirFromRoot(root, scope)
		if err != nil {
			slog.Warn("memory ingest: read scope dir", "scope", scope, "err", err)
			continue
		}
		// Deduplicate by (scope, name): the same memory may exist on disk as
		// both `<name>.md` (the agent's freshly written file) and
		// `<id>-<name>.md` (the canonical form from a prior sync). Prefer the
		// non-canonical entry — that's the agent's newer write. Falling back
		// to lex-sort order is unreliable: a short numeric id can be smaller
		// than the first character of the name (e.g. id 5, name "3foo"
		// produces "5-3foo.md" > "3foo.md") and the canonical form would
		// wrongly win.
		seenInScope := map[string]int{}
		for _, f := range files {
			if f.IsDir() || f.Name() == manifestFile {
				continue
			}
			if !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			// readDirFromRoot returns leaf names only (no slashes, no ".."),
			// so building the rel path can't escape `scope`. The agent worker
			// has rw access to this mount (docker-compose.yml :184), so a
			// prompt-injected memory-writer could plant a symlink, FIFO,
			// socket, or oversized file. We defend in depth: DirEntry.Type()
			// is a cheap fast-path that rejects obvious non-regular entries
			// seen at readdir time, then the root.OpenFile + fstat below is
			// what makes the check TOCTOU-free (the descriptor pins a
			// specific inode and *os.Root refuses to traverse outside the
			// pinned root, so a writer swapping any path component
			// afterwards can't redirect our read).
			if !f.Type().IsRegular() {
				slog.Warn("memory ingest: skipping non-regular file", "scope", scope, "name", f.Name(), "mode", f.Type().String())
				continue
			}
			rel := filepath.Join(scope, f.Name())

			data, ok := readMemoryFileFromRoot(root, rel)
			if !ok {
				continue
			}
			mem, tombstone, err := parseMemoryFile(data, scope)
			if err != nil {
				slog.Warn("memory ingest: parse file", "path", rel, "err", err)
				continue
			}
			if !tombstone {
				// Force agent authorship: the file's frontmatter is agent-
				// controlled and cannot be trusted to claim operator
				// authorship. Operator authorship is preserved at the DB
				// level via upsertByNameNoSync's DoUpdates exclusion of
				// created_by — see this function's doc comment.
				mem.CreatedBy = MemoryCreatedByAgent
			}

			entry := &parsedEntry{mem: mem, canonical: canonicalIngestName(f.Name(), mem.Name), tombstone: tombstone}
			if idx, ok := seenInScope[mem.Name]; ok {
				prior := parsed[idx]
				switch {
				case entry.tombstone:
					// Agent's deletion intent always wins, regardless of
					// which file (canonical or bare) the tombstone landed on.
					parsed[idx] = entry
				case prior.tombstone:
					// Existing tombstone wins; a sibling non-tombstone file
					// is a stale snapshot that the post-batch sync will purge.
				case prior.canonical && !entry.canonical:
					// Prefer the agent's freshly written `<name>.md` over a
					// prior canonical `<id>-<name>.md` snapshot.
					parsed[idx] = entry
				case prior.canonical == entry.canonical:
					// Same form, later wins (stable for re-ingest determinism).
					parsed[idx] = entry
				}
				continue
			}
			seenInScope[mem.Name] = len(parsed)
			parsed = append(parsed, entry)
		}
	}

	ingested := 0
	deleted := 0
	for _, entry := range parsed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.tombstone {
			n, err := s.deleteByScopeAndNameNoSync(entry.mem.Scope, entry.mem.Name)
			if err != nil {
				slog.Warn("memory ingest: tombstone delete", "scope", entry.mem.Scope, "name", entry.mem.Name, "err", err)
				continue
			}
			if n > 0 {
				deleted++
			}
			// Always count as a change so the post-batch sync runs and the
			// tombstone file itself is purged from disk even when the DB row
			// had already been removed by an earlier ingest.
			continue
		}
		if _, err := s.upsertByNameNoSync(entry.mem); err != nil {
			slog.Warn("memory ingest: upsert", "scope", entry.mem.Scope, "name", entry.mem.Name, "err", err)
			continue
		}
		ingested++
	}

	// Single sync after the batch instead of one per row: SyncMemoryFiles
	// reads the full memories table and rewrites every file, so calling it
	// inside the loop turned ingest into O(N²) disk churn. A sync after
	// deletions cleans up both the bare `<name>.md` tombstone the agent
	// wrote and the prior canonical `<id>-<name>.md` snapshot, since
	// neither matches an existing row in expectedFiles.
	if ingested > 0 || deleted > 0 || len(parsed) > 0 {
		if err := s.SyncMemoryFiles(); err != nil {
			slog.Warn("memory ingest: post-batch sync failed", "err", err)
		}
	}

	slog.Info("memory ingest complete", "ingested", ingested, "deleted", deleted)
	return nil
}

// deleteByScopeAndNameNoSync removes the memory row keyed by (scope, name)
// without triggering SyncMemoryFiles. Returns the number of rows deleted (0
// when no matching row exists — a tombstone for an unknown slug is a no-op,
// not an error, so the post-batch sync still purges the orphaned file).
func (s *MemoryService) deleteByScopeAndNameNoSync(scope, name string) (int64, error) {
	scope = strings.TrimSpace(scope)
	name = strings.TrimSpace(name)
	if scope == "" || name == "" {
		return 0, fmt.Errorf("scope and name required for tombstone delete")
	}
	result := s.db.Where("scope = ? AND name = ?", scope, name).Delete(&database.Memory{})
	if result.Error != nil {
		return 0, fmt.Errorf("delete memory by scope/name: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// readDirFromRoot is a thin wrapper around root.Open + Readdir that mirrors
// os.ReadDir's semantics (sorted by name, returning DirEntry) but keeps every
// path lookup pinned to *os.Root. The standard library's os.ReadDir resolves
// its path argument via the global filesystem namespace, which would re-walk
// /akmatori/memory/<scope> through any symlink an attacker swapped in after
// the previous readdir saw a real directory there. Using root.Open with
// path components like "." or "<scope>" makes the resolution happen via
// openat(rootFd, name) so a swapped-in symlink at any intermediate level
// can only resolve to something still under the original root inode.
func readDirFromRoot(root *os.Root, name string) ([]os.DirEntry, error) {
	dir, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// readMemoryFileFromRoot opens `rel` relative to the pinned root with
// O_NOFOLLOW|O_NONBLOCK so:
//   - a symlink swapped in at any intermediate path component can only
//     resolve to a location still under the original root (os.Root refuses
//     to traverse outside its pinned directory),
//   - O_NOFOLLOW rejects a symlink at the final component,
//   - O_NONBLOCK keeps us from hanging if a FIFO ever slips past the
//     readdir-time DirEntry.Type() filter (O_NOFOLLOW alone wouldn't catch
//     a pre-existing pipe or socket),
//   - fstat on the descriptor (rather than a pre-read Lstat on the path) is
//     the only TOCTOU-free regular-file check — a writer can't invalidate
//     it after the fact because we hold the fd,
//   - the size check + io.LimitReader cap the read at maxMemoryFileBytes
//     so a file growing under us can't OOM the API.
//
// Returns (data, true) on success, (nil, false) on any skip reason
// already logged.
func readMemoryFileFromRoot(root *os.Root, rel string) ([]byte, bool) {
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		slog.Warn("memory ingest: open file", "path", rel, "err", err)
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		slog.Warn("memory ingest: fstat file", "path", rel, "err", err)
		return nil, false
	}
	if !info.Mode().IsRegular() {
		slog.Warn("memory ingest: skipping non-regular file (fstat)", "path", rel, "mode", info.Mode().String())
		return nil, false
	}
	if info.Size() > maxMemoryFileBytes {
		slog.Warn("memory ingest: skipping oversized file", "path", rel, "size", info.Size(), "limit", maxMemoryFileBytes)
		return nil, false
	}

	data, err := io.ReadAll(io.LimitReader(f, maxMemoryFileBytes))
	if err != nil {
		slog.Warn("memory ingest: read file", "path", rel, "err", err)
		return nil, false
	}
	return data, true
}

// canonicalIngestName reports whether the on-disk filename matches the
// `<id>-<name>.md` shape produced by SyncMemoryFiles. The agent's freshly
// written files are just `<name>.md`; when both forms exist for the same
// memory we prefer the latter (agent's newer write) over the former (prior
// canonical snapshot).
func canonicalIngestName(filename, name string) bool {
	plain := name + ".md"
	if filename == plain {
		return false
	}
	suffix := "-" + plain
	if !strings.HasSuffix(filename, suffix) {
		return false
	}
	prefix := filename[:len(filename)-len(suffix)]
	if prefix == "" {
		return false
	}
	for _, r := range prefix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// upsertByNameNoSync is the shared upsert primitive used by both UpsertByName
// (which then triggers a single-row SyncMemoryFiles) and IngestFromDisk
// (which batches one sync at the end of the walk to avoid O(N²) disk churn,
// since each per-row sync re-reads the whole table and rewrites every file).
//
// created_by is intentionally NOT in DoUpdates: on conflict the existing
// authorship is preserved so an agent re-ingest of an operator-authored slug
// cannot silently flip the row from `operator` to `agent`. New rows still get
// their created_by set via the INSERT path. Operator edits flow through
// MemoryService.Update (a plain UPDATE), so this restriction only affects
// the ingest/upsert collision case.
func (s *MemoryService) upsertByNameNoSync(m *database.Memory) (*database.Memory, error) {
	if err := s.validate(m); err != nil {
		return nil, err
	}
	m.Scope = strings.TrimSpace(m.Scope)
	m.Name = strings.TrimSpace(m.Name)

	if err := s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "scope"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"type", "description", "body", "incident_uuid", "updated_at",
		}),
	}).Create(m).Error; err != nil {
		return nil, fmt.Errorf("failed to upsert memory: %w", err)
	}

	var saved database.Memory
	if err := s.db.Where("scope = ? AND name = ?", m.Scope, m.Name).First(&saved).Error; err != nil {
		return nil, fmt.Errorf("failed to read upserted memory: %w", err)
	}
	return &saved, nil
}

// parseMemoryFile decodes a memory markdown file (YAML frontmatter + body)
// into a Memory ready for UpsertByName. The scope argument is the on-disk
// directory the file came from; if the file's frontmatter scope is empty or
// mismatches, the on-disk scope wins (the directory layout is authoritative).
//
// When the frontmatter contains `deleted: true`, the file is parsed as a
// tombstone: only scope and name are populated on the returned Memory and the
// second return value is true. Description/type/body are not required and any
// values present are ignored — the caller deletes the corresponding DB row.
func parseMemoryFile(data []byte, scope string) (*database.Memory, bool, error) {
	src := strings.TrimLeft(string(data), " \t\r\n")
	const fence = "---"
	if !strings.HasPrefix(src, fence) {
		return nil, false, fmt.Errorf("missing opening frontmatter fence")
	}
	rest := strings.TrimLeft(src[len(fence):], "\r\n")
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return nil, false, fmt.Errorf("missing closing frontmatter fence")
	}
	fmBytes := rest[:end]
	body := strings.TrimLeft(rest[end+len("\n"+fence):], "\r\n")

	var fm memoryFrontmatter
	if err := yaml.Unmarshal([]byte(fmBytes), &fm); err != nil {
		return nil, false, fmt.Errorf("parse frontmatter: %w", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		return nil, false, fmt.Errorf("frontmatter missing name")
	}

	if fm.Deleted {
		// Defense in depth: the deletion path bypasses upsert's validate(), so
		// reject tombstones whose name is not a slug-safe identifier. The SQL
		// is parameterized so injection is not possible, but a name like
		// "../foo" or one containing whitespace points the agent at a row that
		// the canonical path could never have written. Refusing parse here
		// makes the tombstone a no-op rather than a silent miss.
		tombstoneName := strings.TrimSpace(fm.Name)
		if !validMemoryName(tombstoneName) {
			return nil, false, fmt.Errorf("invalid tombstone name %q", tombstoneName)
		}
		return &database.Memory{
			Scope: scope,
			Name:  tombstoneName,
		}, true, nil
	}

	if strings.TrimSpace(fm.Description) == "" {
		return nil, false, fmt.Errorf("frontmatter missing description")
	}
	if !ValidMemoryType(fm.Type) {
		return nil, false, fmt.Errorf("invalid memory type %q", fm.Type)
	}

	// Strip the `# <name>` header and the description echo so the persisted
	// Body field doesn't duplicate frontmatter content on every round-trip.
	body = stripBodyHeader(body, fm.Name, fm.Description)

	return &database.Memory{
		Scope:        scope,
		Type:         fm.Type,
		Name:         strings.TrimSpace(fm.Name),
		Description:  strings.ReplaceAll(strings.TrimSpace(fm.Description), "\n", " "),
		Body:         body,
		IncidentUUID: strings.TrimSpace(fm.IncidentUUID),
		CreatedBy:    strings.TrimSpace(fm.CreatedBy),
	}, false, nil
}

// stripBodyHeader removes the `# <name>` heading and the description echo
// that renderMemoryFile emits at the top of the body, leaving only the
// caller-supplied long-form body. The renderer is the inverse of this
// helper — together they form a clean round-trip.
func stripBodyHeader(body, name, description string) string {
	body = strings.TrimLeft(body, " \t\n\r")

	header := "# " + name
	if strings.HasPrefix(body, header+"\n") {
		body = body[len(header)+1:]
	} else if strings.HasPrefix(body, header+"\r\n") {
		body = body[len(header)+2:]
	}
	body = strings.TrimLeft(body, " \t\n\r")

	desc := strings.ReplaceAll(strings.TrimSpace(description), "\n", " ")
	if desc != "" && strings.HasPrefix(body, desc) {
		rest := body[len(desc):]
		// Only treat the prefix as the rendered description echo when it
		// ends at a line boundary. renderMemoryFile always emits the
		// description on its own line (`%s\n\n`), so if the body begins
		// with the description as a topic sentence that continues onto
		// the same line, we must leave it alone — stripping it would
		// silently corrupt the agent's intended prose.
		if len(rest) == 0 || rest[0] == '\n' || rest[0] == '\r' {
			body = strings.TrimLeft(rest, " \t\n\r")
		}
	}
	// Trim trailing whitespace so a roundtrip (parse → render) doesn't keep
	// accumulating a blank line on every cycle. renderMemoryFile re-adds the
	// single terminating newline as needed.
	return strings.TrimRight(body, " \t\n\r")
}

// memoryFrontmatter is the on-disk YAML schema for memory files. yaml.Marshal
// handles quoting and escaping for values containing YAML-significant chars
// (colons, quotes, brackets) — interpolating m.Description raw into the
// frontmatter would let a description like "prod-db: data dir moved" turn
// the file into invalid YAML and break downstream consumers.
//
// The Deleted field is set to true by the memory-writer subagent when it is
// asked to remove a memory (Action: delete <slug>). IngestFromDisk uses that
// marker to delete the corresponding DB row and clean up both the bare
// `<name>.md` tombstone and the canonical `<id>-<name>.md` snapshot.
type memoryFrontmatter struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description,omitempty"`
	Type         string `yaml:"type,omitempty"`
	Scope        string `yaml:"scope,omitempty"`
	IncidentUUID string `yaml:"incident_uuid,omitempty"`
	CreatedBy    string `yaml:"created_by,omitempty"`
	Deleted      bool   `yaml:"deleted,omitempty"`
}

// renderMemoryFile produces the full markdown body for a single memory file.
// YAML frontmatter contains the metadata subagent searchers will surface.
func renderMemoryFile(m database.Memory) string {
	fm := memoryFrontmatter{
		Name: m.Name,
		// Flatten newlines so the frontmatter stays single-line per field —
		// downstream consumers read the rendered file and a single-line
		// description keeps each entry on one indexable row.
		Description:  strings.ReplaceAll(m.Description, "\n", " "),
		Type:         m.Type,
		Scope:        m.Scope,
		IncidentUUID: m.IncidentUUID,
		CreatedBy:    m.CreatedBy,
	}
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		// Defensive fallback — yaml.Marshal of a flat struct of strings
		// shouldn't fail in practice. Log so we notice if it ever does.
		slog.Warn("failed to marshal memory frontmatter, falling back to minimal", "name", m.Name, "err", err)
		yamlBytes = []byte(fmt.Sprintf("name: %q\ntype: %q\nscope: %q\n", m.Name, m.Type, m.Scope))
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", m.Name)
	fmt.Fprintf(&b, "%s\n\n", m.Description)
	if strings.TrimSpace(m.Body) != "" {
		b.WriteString(m.Body)
		if !strings.HasSuffix(m.Body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderManifest builds the per-scope MEMORY.md table. Hard-capped at
// manifestMaxLines / manifestMaxBytes — the manifest is injected into prompts,
// so it MUST stay small even as memory accumulates. When an entry would push
// the manifest past either cap, we stop and emit a truncation marker telling
// the agent to use the memory-searcher subagent for the rest.
func renderManifest(scope string, entries []database.Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Memory Manifest — scope: %s\n\n", scope)
	if len(entries) == 0 {
		b.WriteString("_No memories yet._\n")
		return b.String()
	}
	b.WriteString("| name | type | description |\n")
	b.WriteString("| --- | --- | --- |\n")

	header := b.Len()
	emitted := 0
	truncated := 0

	for _, m := range entries {
		// Inline pipes break Markdown table parsing; replace with bullets.
		desc := strings.ReplaceAll(strings.ReplaceAll(m.Description, "\n", " "), "|", "·")
		row := fmt.Sprintf("| `%s` | %s | %s |\n", m.Name, m.Type, desc)
		// linesEmitted = header table (2 lines: header + separator) + rows so far.
		linesEmitted := 2 + emitted
		if linesEmitted+1 > manifestMaxLines || b.Len()+len(row) > manifestMaxBytes {
			truncated = len(entries) - emitted
			break
		}
		b.WriteString(row)
		emitted++
	}

	if truncated > 0 {
		fmt.Fprintf(&b, "\n_… %d more memories truncated. Use the memory-searcher subagent to find them._\n", truncated)
	}

	// If the table never emitted a row (e.g., header alone exceeded the cap),
	// keep at least the header for diagnostic clarity.
	if b.Len() == header {
		b.WriteString("_Manifest exceeded byte cap; use the memory-searcher subagent to access entries._\n")
	}

	return b.String()
}

// removeStaleFilesInRoot deletes regular files in the pinned scope root whose
// names are not in keep. Symlinks and any other non-regular entries are also
// purged — they have no business being there and the agent could swap a real
// file for one between syncs. Subdirectories are left alone (we don't expect
// nested layout inside a scope dir, and RemoveAll on an unknown subdirectory
// could destroy operator state we don't recognize).
//
// All path lookups go through *os.Root so a symlink at any component cannot
// redirect Remove() to a target outside the scope directory.
func removeStaleFilesInRoot(scopeRoot *os.Root, keep map[string]bool) error {
	dir, err := scopeRoot.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !keep[e.Name()] {
			// scopeRoot.Remove on a symlink unlinks the symlink itself
			// (not its target). This is the safe operation here — we want
			// to drop the planted link without touching whatever it points
			// at, which may be an API-owned file outside the memory tree.
			if err := scopeRoot.Remove(e.Name()); err != nil {
				slog.Warn("failed to remove stale memory file", "file", e.Name(), "err", err)
			}
		}
	}
	return nil
}

// removeStaleScopesInRoot deletes scope directories no longer present in keep.
// Entries that aren't directories (e.g. an agent-planted symlink at a scope
// path) are purged too so a future sync can recreate the slot from scratch.
// All operations go through *os.Root.
func removeStaleScopesInRoot(root *os.Root, keep map[string]bool) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if keep[name] {
			continue
		}
		if !e.IsDir() {
			// A non-directory at scope-root level (e.g. an agent-planted
			// symlink or stray file) gets cleaned up — Remove unlinks the
			// entry itself without following any symlink target.
			if err := root.Remove(name); err != nil {
				slog.Warn("failed to remove stale scope entry", "name", name, "err", err)
			}
			continue
		}
		if err := root.RemoveAll(name); err != nil {
			slog.Warn("failed to remove stale scope dir", "dir", name, "err", err)
		}
	}
	return nil
}
