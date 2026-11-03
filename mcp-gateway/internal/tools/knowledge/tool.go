package knowledge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultSearchLimit = 5
	maxSearchLimit     = 20
	defaultListLimit   = 50
	maxListLimit       = 200
	// corpusCap bounds the rows one search loads (newest first). Bodies are at
	// most 8 KiB, so the worst case is about 40 MB per call.
	corpusCap        = 5000
	maxWritesPerRun  = 20
	maxDeletesPerRun = 10
	// counterTTL drops per-run counters of runs that ended long ago.
	counterTTL = 6 * time.Hour
)

// Run statuses that may no longer write (mirrors the API's AgentRunStatus).
var finishedRunStatuses = map[string]bool{"completed": true, "failed": true, "superseded": true}

// errMemoryGenerationOff is returned by writes when the operator turned agent
// memory off.
var errMemoryGenerationOff = errors.New("memory generation is off for this install; findings are not stored")

// Tool serves runbooks.* and memory.* from the gateway's DB connection.
type Tool struct {
	db     *gorm.DB
	logger *log.Logger
	now    func() time.Time

	mu        sync.Mutex
	counters  map[string]*runCounter // run id -> writes / deletes
	warnedCap time.Time
}

type runCounter struct {
	writes, deletes int
	seen            time.Time
}

// NewTool creates the knowledge tool.
func NewTool(db *gorm.DB, logger *log.Logger) *Tool {
	return &Tool{db: db, logger: logger, now: time.Now, counters: map[string]*runCounter{}}
}

func marshal(v interface{}) (interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func stringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

// requireStringArgs returns an error unless every key is present with a
// string value ("" is allowed).
func requireStringArgs(args map[string]interface{}, keys ...string) error {
	for _, k := range keys {
		v, ok := args[k]
		if !ok || v == nil {
			return fmt.Errorf("%s is required", k)
		}
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s must be a string", k)
		}
	}
	return nil
}

// optionalStringArgs returns an error when a key is present with a value
// that is not a string (null counts as absent).
func optionalStringArgs(args map[string]interface{}, keys ...string) error {
	for _, k := range keys {
		if v, ok := args[k]; ok && v != nil {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s must be a string", k)
			}
		}
	}
	return nil
}

// rawStringArg returns a string arg without trimming (descriptions, bodies).
func rawStringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

func intArg(args map[string]interface{}, key string, def, max int) int {
	n := def
	switch v := args[key].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case int64:
		n = int(v)
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			n = i
		}
	}
	if n <= 0 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

func stringListArg(args map[string]interface{}, key string) []string {
	var out []string
	switch v := args[key].(type) {
	case []interface{}:
		for _, e := range v {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case []string:
		out = v
	case string:
		if strings.TrimSpace(v) != "" {
			out = []string{strings.TrimSpace(v)}
		}
	}
	return out
}

// timeArg parses ISO-8601 or Unix seconds. ok=false when absent or unreadable.
func timeArg(args map[string]interface{}, key string) (time.Time, bool) {
	switch v := args[key].(type) {
	case float64:
		return unixArg(int64(v)), true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return time.Time{}, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return unixArg(i), true
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// unixMillisThreshold separates Unix seconds from milliseconds: a value
// above it is read as milliseconds (seconds would mean the year 5138).
const unixMillisThreshold = 100_000_000_000

// unixArg reads a Unix time in seconds or milliseconds; a millisecond value
// (what Date.now() gives) used to become a date in the far future and an
// empty list.
func unixArg(v int64) time.Time {
	if v > unixMillisThreshold || v < -unixMillisThreshold {
		return time.UnixMilli(v).UTC()
	}
	return time.Unix(v, 0).UTC()
}

func (t *Tool) warnTruncated(kind string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.now().Sub(t.warnedCap) < time.Hour {
		return
	}
	t.warnedCap = t.now()
	t.logger.Printf("WARN: knowledge %s search loaded the %d newest rows only; older rows are not searched", kind, corpusCap)
}

// ---------- runbooks ----------

type runbookHit struct {
	ID        uint      `json:"id"`
	Title     string    `json:"title"`
	Excerpt   string    `json:"excerpt"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunbooksSearch ranks runbooks against the query (BM25 over title and
// content).
func (t *Tool) RunbooksSearch(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	query := stringArg(args, "query")
	if query == "" {
		return nil, errors.New("query is required")
	}
	limit := intArg(args, "limit", defaultSearchLimit, maxSearchLimit)
	var rows []database.Runbook
	if err := t.db.WithContext(ctx).Select("id", "title", "content", "updated_at").
		Order("updated_at DESC").Order("id DESC").Limit(corpusCap).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load runbooks: %w", err)
	}
	docs := make([]document, len(rows))
	for i, r := range rows {
		docs[i] = document{ID: r.ID, Title: r.Title, Body: r.Content, UpdatedAt: r.UpdatedAt}
	}
	hits := search(docs, query, limit)
	out := make([]runbookHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, runbookHit{ID: h.Doc.ID, Title: h.Doc.Title, Excerpt: excerpt(h.Doc.Body, h.Terms), UpdatedAt: h.Doc.UpdatedAt})
	}
	resp := map[string]interface{}{"results": out, "count": len(out)}
	if len(rows) == corpusCap {
		resp["truncated_corpus"] = true
		t.warnTruncated("runbook")
	}
	return marshal(resp)
}

// RunbooksGet returns one runbook in full.
func (t *Tool) RunbooksGet(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	id := intArg(args, "id", 0, 1<<31-1)
	if id <= 0 {
		return nil, errors.New("id is required (from runbooks.search)")
	}
	var rb database.Runbook
	if err := t.db.WithContext(ctx).First(&rb, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("runbook %d not found", id)
		}
		return nil, err
	}
	return marshal(map[string]interface{}{"id": rb.ID, "title": rb.Title, "content": rb.Content, "updated_at": rb.UpdatedAt})
}

// ---------- memory reads ----------

type memoryHit struct {
	ID          uint       `json:"id"`
	Scope       string     `json:"scope"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Excerpt     string     `json:"excerpt,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
	Evidence    string     `json:"evidence"`
	Claim       string     `json:"claim,omitempty"`
	UseCount    int        `json:"use_count"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// recordUses records one use of each memory by the calling run: a row in
// memory_uses and the denormalised counters (get raises use_count, search
// moves last_used_at only). Reads need no run id; without one the row has
// no run. Best effort: a failure is logged, the read still answers.
func (t *Tool) recordUses(ctx context.Context, kind string, ids []uint) {
	if len(ids) == 0 {
		return
	}
	runID := mcp.RunIDFromContext(ctx)
	incidentUUID := ""
	if runID != "" {
		var run database.AgentRun
		if err := t.db.WithContext(ctx).Select("incident_uuid").Where("run_id = ?", runID).Take(&run).Error; err == nil {
			incidentUUID = run.IncidentUUID
		}
	}
	now := t.now().UTC()
	rows := make([]database.MemoryUse, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, database.MemoryUse{MemoryID: id, RunID: runID, IncidentUUID: incidentUUID, Kind: kind, At: now})
	}
	err := t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		values := map[string]interface{}{"last_used_at": now}
		if kind != memoryUseSearch {
			values["use_count"] = gorm.Expr("use_count + 1")
		}
		return tx.Model(&database.Memory{}).Where("id IN ?", ids).UpdateColumns(values).Error
	})
	if err != nil {
		t.logger.Printf("memory usage: record %s uses run=%s: %v", kind, runID, err)
	}
}

// Kinds of a memory use, as the API's database.MemoryUse*.
const (
	memoryUseSearch = "search"
	memoryUseGet    = "get"
)

// hitEvidence is the grade to show: a row written before grades by a
// gateway ahead of the API has none, and reads as unverified.
func hitEvidence(m database.Memory) string {
	if m.Evidence == "" {
		return evidenceUnverified
	}
	return m.Evidence
}

func memoryQuery(db *gorm.DB, args map[string]interface{}) (*gorm.DB, error) {
	q := db
	if scopes := stringListArg(args, "scopes"); len(scopes) > 0 {
		q = q.Where("scope IN ?", scopes)
	}
	if typ := stringArg(args, "type"); typ != "" {
		if typ != typeFact && typ != typeFeedback {
			return nil, fmt.Errorf("type must be fact or feedback")
		}
		q = q.Where("type = ?", typ)
	}
	if ev := stringArg(args, "evidence"); ev != "" {
		if !validEvidence(ev) {
			return nil, fmt.Errorf("evidence must be confirmed, observed or unverified")
		}
		q = q.Where("evidence = ?", ev)
	}
	return q, nil
}

// MemorySearch ranks memories against the query (BM25; name and
// description weigh more than the body).
func (t *Tool) MemorySearch(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	query := stringArg(args, "query")
	if query == "" {
		return nil, errors.New("query is required; use memory.list to enumerate")
	}
	limit := intArg(args, "limit", defaultSearchLimit, maxSearchLimit)
	q, err := memoryQuery(t.db.WithContext(ctx), args)
	if err != nil {
		return nil, err
	}
	var rows []database.Memory
	if err := q.Select("id", "scope", "name", "type", "description", "body", "created_by", "source_file_id", "evidence", "claim", "use_count", "last_used_at", "updated_at").
		Order("updated_at DESC").Order("id DESC").Limit(corpusCap).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load memories: %w", err)
	}
	byID := make(map[uint]database.Memory, len(rows))
	docs := make([]document, len(rows))
	for i, m := range rows {
		byID[m.ID] = m
		docs[i] = document{ID: m.ID, Title: memoryTitle(m.Name, m.Description), Body: m.Body, UpdatedAt: m.UpdatedAt}
	}
	hits := search(docs, query, limit)
	out := make([]memoryHit, 0, len(hits))
	ids := make([]uint, 0, len(hits))
	for _, h := range hits {
		m := byID[h.Doc.ID]
		ids = append(ids, m.ID)
		out = append(out, memoryHit{ID: m.ID, Scope: m.Scope, Name: m.Name, Type: m.Type, Description: m.Description,
			Excerpt: excerpt(m.Body, h.Terms), CreatedBy: m.CreatedBy, Evidence: hitEvidence(m), Claim: m.Claim,
			UseCount: m.UseCount, LastUsedAt: m.LastUsedAt, UpdatedAt: m.UpdatedAt})
	}
	t.recordUses(ctx, memoryUseSearch, ids)
	resp := map[string]interface{}{"results": out, "count": len(out)}
	if len(rows) == corpusCap {
		resp["truncated_corpus"] = true
		t.warnTruncated("memory")
	}
	return marshal(resp)
}

func encodeCursor(m database.Memory) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(m.UpdatedAt.UnixNano(), 10) + ":" + strconv.FormatUint(uint64(m.ID), 10)))
}

func decodeCursor(s string) (time.Time, uint, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	ts, id, ok := strings.Cut(string(b), ":")
	if !ok {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	ns, err1 := strconv.ParseInt(ts, 10, 64)
	n, err2 := strconv.ParseUint(id, 10, 64)
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	return time.Unix(0, ns).UTC(), uint(n), nil
}

// MemoryList enumerates memories newest first (no bodies), with optional
// scope, type and since filters, paged with next_cursor.
func (t *Tool) MemoryList(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	limit := intArg(args, "limit", defaultListLimit, maxListLimit)
	q, err := memoryQuery(t.db.WithContext(ctx), args)
	if err != nil {
		return nil, err
	}
	if since, ok := timeArg(args, "since"); ok {
		q = q.Where("updated_at >= ?", since)
	} else if stringArg(args, "since") != "" {
		return nil, errors.New("since must be ISO-8601 or Unix seconds")
	}
	if unused, ok := timeArg(args, "unused_since"); ok {
		q = q.Where("(last_used_at IS NULL OR last_used_at < ?)", unused)
	} else if stringArg(args, "unused_since") != "" {
		return nil, errors.New("unused_since must be ISO-8601 or Unix seconds")
	}
	if c := stringArg(args, "cursor"); c != "" {
		ts, id, err := decodeCursor(c)
		if err != nil {
			return nil, err
		}
		q = q.Where("(updated_at < ? OR (updated_at = ? AND id < ?))", ts, ts, id)
	}
	var rows []database.Memory
	if err := q.Select("id", "scope", "name", "type", "description", "created_by", "evidence", "claim", "use_count", "last_used_at", "created_at", "updated_at").
		Order("updated_at DESC").Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeCursor(rows[limit-1])
	}
	items := make([]memoryHit, 0, len(rows))
	for _, m := range rows {
		items = append(items, memoryHit{ID: m.ID, Scope: m.Scope, Name: m.Name, Type: m.Type, Description: m.Description, CreatedBy: m.CreatedBy,
			Evidence: hitEvidence(m), Claim: m.Claim, UseCount: m.UseCount, LastUsedAt: m.LastUsedAt, UpdatedAt: m.UpdatedAt})
	}
	return marshal(map[string]interface{}{"items": items, "count": len(items), "next_cursor": next})
}

var memoryRefPattern = regexp.MustCompile(`\[\[([^\[\]\n]{1,256})\]\]`)
var contextFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*\.[a-zA-Z0-9]+$`)

// memoryRef is one [[...]] reference of a memory body, resolved.
type memoryRef struct {
	Raw    string `json:"raw"`
	Kind   string `json:"kind"`
	Scope  string `json:"scope,omitempty"`
	Name   string `json:"name,omitempty"`
	ID     uint   `json:"id,omitempty"`
	Path   string `json:"path,omitempty"`
	Exists bool   `json:"exists"`
}

// resolveRefs mirrors the API's ParseMemoryRefs + ResolveRefs, but returns
// memory links by scope and name (no file paths).
func (t *Tool) resolveRefs(ctx context.Context, body, scope string) []memoryRef {
	var refs []memoryRef
	seen := map[string]bool{}
	for _, m := range memoryRefPattern.FindAllStringSubmatch(body, -1) {
		raw := strings.TrimSpace(m[1])
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		ref := memoryRef{Raw: raw, Kind: "invalid"}
		scopes := []string{}
		switch {
		case strings.Contains(raw, "."):
			if contextFilename.MatchString(raw) {
				ref.Kind = "file"
				ref.Name = raw
				ref.Path = "/akmatori/context/" + raw
				var n int64
				if err := t.db.WithContext(ctx).Model(&database.ContextFile{}).Where("filename = ?", raw).Count(&n).Error; err == nil {
					ref.Exists = n > 0
				}
			}
		case strings.Contains(raw, "/"):
			s, n, _ := strings.Cut(raw, "/")
			if validSlug(s) && validSlug(n) {
				ref.Kind, ref.Scope, ref.Name = "memory", s, n
				scopes = []string{s}
			}
		case validSlug(raw):
			ref.Kind, ref.Scope, ref.Name = "memory", scope, raw
			scopes = []string{scope}
			if scope != memoryScopeGlobal {
				scopes = append(scopes, memoryScopeGlobal)
			}
		}
		for _, s := range scopes {
			var target database.Memory
			if err := t.db.WithContext(ctx).Select("id").Where("scope = ? AND name = ?", s, ref.Name).Take(&target).Error; err == nil {
				ref.Scope, ref.ID, ref.Exists = s, target.ID, true
				break
			}
		}
		refs = append(refs, ref)
		if len(refs) == 50 {
			break
		}
	}
	return refs
}

// MemoryGet returns one memory in full with its references.
func (t *Tool) MemoryGet(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	var m database.Memory
	q := t.db.WithContext(ctx)
	if id := intArg(args, "id", 0, 1<<31-1); id > 0 {
		q = q.Where("id = ?", id)
	} else if scope, name := stringArg(args, "scope"), stringArg(args, "name"); scope != "" && name != "" {
		q = q.Where("scope = ? AND name = ?", scope, name)
	} else {
		return nil, errors.New("pass id, or scope and name")
	}
	if err := q.Take(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("memory not found")
		}
		return nil, err
	}
	refs := t.resolveRefs(ctx, m.Body, m.Scope)
	if refs == nil {
		refs = []memoryRef{}
	}
	t.recordUses(ctx, memoryUseGet, []uint{m.ID})
	return marshal(map[string]interface{}{
		"id": m.ID, "scope": m.Scope, "name": m.Name, "type": m.Type, "description": m.Description,
		"body": m.Body, "created_by": m.CreatedBy, "incident_uuid": m.IncidentUUID,
		"evidence": hitEvidence(m), "claim": m.Claim, "evidence_note": m.EvidenceNote,
		"use_count": m.UseCount + 1, "last_used_at": t.now().UTC(),
		"protected": isProtected(m), "updated_at": m.UpdatedAt, "references": refs,
	})
}

// ---------- memory writes ----------

// isProtected: operator-authored memories and context-file summaries are
// never changed by the agent.
func isProtected(m database.Memory) bool {
	return m.CreatedBy == createdByOperator || m.SourceFileID != nil
}

// writableWhere is the protection, in the statement itself.
const writableWhere = "scope = ? AND name = ? AND (created_by IS NULL OR created_by <> 'operator') AND source_file_id IS NULL"

// memoryGenerationEnabled reads the operator gate; fail open.
func (t *Tool) memoryGenerationEnabled(ctx context.Context) bool {
	var gs database.GeneralSettings
	if err := t.db.WithContext(ctx).Order("id ASC").Take(&gs).Error; err != nil {
		return true
	}
	return gs.MemoryGenerationEnabled == nil || *gs.MemoryGenerationEnabled
}

// requireRun checks the X-Run-ID of a write: the run exists, belongs to the
// scope (X-Incident-ID) and has not finished.
func (t *Tool) requireRun(ctx context.Context, scopeID string) (*database.AgentRun, error) {
	runID := mcp.RunIDFromContext(ctx)
	if runID == "" || scopeID == "" {
		return nil, errors.New("run does not belong to this scope: memory writes need the run id (X-Run-ID) and the scope")
	}
	var run database.AgentRun
	if err := t.db.WithContext(ctx).Select("id", "run_id", "scope_uuid", "incident_uuid", "source_kind", "status").
		Where("run_id = ?", runID).Take(&run).Error; err != nil {
		return nil, errors.New("run does not belong to this scope: unknown run id")
	}
	if run.ScopeUUID != scopeID {
		return nil, errors.New("run does not belong to this scope")
	}
	if finishedRunStatuses[run.Status] {
		return nil, fmt.Errorf("run %s is %s; memory writes are refused", runID, run.Status)
	}
	if run.SourceKind == "proposal" {
		return nil, errors.New("proposal chat does not write memories: change the proposal's proposed_content with proposals.update_draft")
	}
	if run.IncidentUUID != "" {
		return nil, errors.New(errAlertRunWrites)
	}
	return &run, nil
}

// errAlertRunWrites: an alert investigation does not write memory; the
// post-incident pass (API, IncidentMemoryExtractor) records the findings
// when the incident closes, from the final answer.
const errAlertRunWrites = "alert investigations do not write memory: the post-incident pass records findings when the incident closes; put the timeline and your findings in the final answer"

// hasQuota reports whether the run may still write (or delete) one more
// memory; it charges nothing. takeQuota counts the slot once the write
// changed something, so a refused, unchanged or failed write is free.
func (t *Tool) hasQuota(runID string, isDelete bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.counters[runID]
	if c == nil {
		return true
	}
	if isDelete {
		return c.deletes < maxDeletesPerRun
	}
	return c.writes < maxWritesPerRun
}

// takeQuota counts one write or delete of a run; false when over the cap.
func (t *Tool) takeQuota(runID string, isDelete bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for id, c := range t.counters {
		if now.Sub(c.seen) > counterTTL {
			delete(t.counters, id)
		}
	}
	c := t.counters[runID]
	if c == nil {
		c = &runCounter{}
		t.counters[runID] = c
	}
	c.seen = now
	if isDelete {
		if c.deletes >= maxDeletesPerRun {
			return false
		}
		c.deletes++
		return true
	}
	if c.writes >= maxWritesPerRun {
		return false
	}
	c.writes++
	return true
}

// validScope checks the agent-only scope rule: global or an existing
// non-system skill. The error lists the valid scopes.
func (t *Tool) validScope(ctx context.Context, scope string) error {
	if scope == memoryScopeGlobal {
		return nil
	}
	var names []string
	if err := t.db.WithContext(ctx).Model(&database.Skill{}).Where("is_system = ?", false).Order("name").Pluck("name", &names).Error; err != nil {
		return fmt.Errorf("check scope: %w", err)
	}
	for _, n := range names {
		if n == scope {
			return nil
		}
	}
	return fmt.Errorf("scope %q is not valid; use one of: %s", scope, strings.Join(append([]string{memoryScopeGlobal}, names...), ", "))
}

// prepareWrite validates a memory.write input (agent rules + shared rules).
func (t *Tool) prepareWrite(ctx context.Context, in *memoryInput) (truncated bool, err error) {
	in.normalize()
	if truncated, err = applyAgentPolicy(in); err != nil {
		return false, err
	}
	if _, err = applyAgentEvidence(in); err != nil {
		return false, err
	}
	if err = validateShared(in); err != nil {
		return false, err
	}
	if err = t.validScope(ctx, in.Scope); err != nil {
		return false, err
	}
	return truncated, nil
}

// MemoryWrite creates or updates the agent memory scope/name. Operator and
// context-summary memories are never changed (action refused_protected).
func (t *Tool) MemoryWrite(ctx context.Context, scopeID string, args map[string]interface{}) (interface{}, error) {
	if !t.memoryGenerationEnabled(ctx) {
		return nil, errMemoryGenerationOff
	}
	// The dispatcher does not enforce the input schema. A missing or
	// non-string body would read as "" and erase the stored body.
	if err := requireStringArgs(args, "scope", "name", "type", "description", "body"); err != nil {
		return nil, err
	}
	if err := optionalStringArgs(args, "evidence", "claim", "evidence_note"); err != nil {
		return nil, err
	}
	run, err := t.requireRun(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	in := memoryInput{
		Scope: stringArg(args, "scope"), Name: stringArg(args, "name"), Type: stringArg(args, "type"),
		Description: rawStringArg(args, "description"), Body: rawStringArg(args, "body"),
		Evidence: stringArg(args, "evidence"), Claim: stringArg(args, "claim"), EvidenceNote: stringArg(args, "evidence_note"),
	}
	requested := in.Evidence
	// A token the agent saw during the run must not become a memory.
	var redacted int
	in.Description, in.Body, in.EvidenceNote, redacted = redactMemoryInput(in.Description, in.Body, in.EvidenceNote)
	truncated, err := t.prepareWrite(ctx, &in)
	if err != nil {
		return nil, err
	}
	raiseRefused, err := t.keepStoredEvidence(ctx, &in, requested == "")
	if err != nil {
		return nil, err
	}
	if !t.hasQuota(run.RunID, false) {
		return nil, fmt.Errorf("write limit reached: at most %d memory writes per run", maxWritesPerRun)
	}

	action, saved, err := t.upsert(ctx, in, run.IncidentUUID)
	if err != nil {
		return nil, err
	}
	// A slot is used by a write that changed something; a refused or an
	// unchanged write does not use up the run's budget.
	if action == "created" || action == "updated" {
		t.takeQuota(run.RunID, false)
	}
	t.logger.Printf("memory.write run=%s scope=%s name=%s action=%s", run.RunID, in.Scope, in.Name, action)
	resp := map[string]interface{}{"scope": in.Scope, "name": in.Name, "action": action}
	if saved != nil {
		resp["id"] = saved.ID
	}
	// The grade in the response is what is stored: for a refused or an
	// unchanged write that is the stored row, not the input.
	if saved != nil && action != "created" && action != "updated" {
		resp["evidence"], resp["claim"] = hitEvidence(*saved), saved.Claim
	} else {
		resp["evidence"], resp["claim"] = in.Evidence, in.Claim
		switch {
		case raiseRefused:
			resp["evidence_note"] = in.EvidenceNote
			resp["note_on_evidence"] = "Stays unverified: to mark it observed, say in evidence_note what you saw and where."
		case in.Evidence == evidenceUnverified || (requested != "" && requested != in.Evidence):
			resp["evidence_note"] = in.EvidenceNote
			resp["note_on_evidence"] = "Stored as unverified: a cause or an unchecked memory stays a lead until an operator confirms it."
		}
	}
	if redacted > 0 {
		resp["redacted"] = redacted
	}
	if truncated {
		resp["description_truncated"] = true
		resp["note"] = "The description was cut to 500 bytes. Keep the full detail in the body."
	}
	if action == "refused_protected" {
		resp["note"] = "This memory is operator-authored or summarizes a context file; it is not changed. Write a new memory with another name if the facts differ."
	}
	return marshal(resp)
}

// keepStoredEvidence applies the stored row's grade to an update
// (mergeStoredEvidence): a merge or a rewrite is not a check. raiseRefused
// is true when the call asked for observed on an unverified row without a
// note.
func (t *Tool) keepStoredEvidence(ctx context.Context, in *memoryInput, evidenceOmitted bool) (raiseRefused bool, err error) {
	var stored database.Memory
	err = t.db.WithContext(ctx).Select("description", "body", "evidence", "claim", "evidence_note").
		Where("scope = ? AND name = ?", in.Scope, in.Name).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read memory: %w", err)
	}
	return mergeStoredEvidence(in, &stored, evidenceOmitted), nil
}

// upsert applies the write with the protection in the WHERE clause.
func (t *Tool) upsert(ctx context.Context, in memoryInput, incidentUUID string) (string, *database.Memory, error) {
	db := t.db.WithContext(ctx)
	update := func() (int64, error) {
		values := map[string]interface{}{
			"type": in.Type, "description": in.Description, "body": in.Body,
			"evidence": in.Evidence, "claim": in.Claim, "evidence_note": in.EvidenceNote,
			"updated_at": t.now().UTC(),
		}
		if incidentUUID != "" {
			values["incident_uuid"] = incidentUUID
		}
		res := db.Model(&database.Memory{}).
			Where(writableWhere, in.Scope, in.Name).
			Where("(type <> ? OR description <> ? OR body <> ? OR COALESCE(evidence, '') <> ? OR COALESCE(claim, '') <> ? OR COALESCE(evidence_note, '') <> ?)",
				in.Type, in.Description, in.Body, in.Evidence, in.Claim, in.EvidenceNote).
			Updates(values)
		return res.RowsAffected, res.Error
	}
	lookup := func() (*database.Memory, error) {
		var m database.Memory
		err := db.Where("scope = ? AND name = ?", in.Scope, in.Name).Take(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return &m, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		n, err := update()
		if err != nil {
			return "", nil, fmt.Errorf("update memory: %w", err)
		}
		if n > 0 {
			m, err := lookup()
			return "updated", m, err
		}
		existing, err := lookup()
		if err != nil {
			return "", nil, fmt.Errorf("read memory: %w", err)
		}
		if existing != nil {
			if isProtected(*existing) {
				return "refused_protected", existing, nil
			}
			return "unchanged", existing, nil
		}
		now := t.now().UTC()
		row := database.Memory{
			Scope: in.Scope, Name: in.Name, Type: in.Type, Description: in.Description, Body: in.Body,
			Evidence: in.Evidence, Claim: in.Claim, EvidenceNote: in.EvidenceNote,
			IncidentUUID: incidentUUID, CreatedBy: createdByAgent, CreatedAt: now, UpdatedAt: now,
		}
		res := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}, {Name: "name"}}, DoNothing: true}).Create(&row)
		if res.Error != nil {
			return "", nil, fmt.Errorf("create memory: %w", res.Error)
		}
		if res.RowsAffected > 0 {
			return "created", &row, nil
		}
		// Lost an insert race: run the update once more.
	}
	m, err := lookup()
	if err != nil || m == nil {
		return "", nil, errors.New("memory write raced with another writer; retry")
	}
	if isProtected(*m) {
		return "refused_protected", m, nil
	}
	return "unchanged", m, nil
}

// MemoryDelete removes the agent memory scope/name.
func (t *Tool) MemoryDelete(ctx context.Context, scopeID string, args map[string]interface{}) (interface{}, error) {
	if !t.memoryGenerationEnabled(ctx) {
		return nil, errMemoryGenerationOff
	}
	run, err := t.requireRun(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	scope, name := stringArg(args, "scope"), stringArg(args, "name")
	if scope == "" || name == "" {
		return nil, errors.New("scope and name are required")
	}
	if !t.hasQuota(run.RunID, true) {
		return nil, fmt.Errorf("delete limit reached: at most %d memory deletes per run", maxDeletesPerRun)
	}
	db := t.db.WithContext(ctx)
	var res *gorm.DB
	if err := db.Transaction(func(tx *gorm.DB) error {
		// The uses follow the memory (SQLite does not enforce the cascade).
		if err := tx.Where("memory_id IN (?)", tx.Model(&database.Memory{}).Select("id").Where(writableWhere, scope, name)).
			Delete(&database.MemoryUse{}).Error; err != nil {
			return err
		}
		res = tx.Where(writableWhere, scope, name).Delete(&database.Memory{})
		return res.Error
	}); err != nil {
		return nil, fmt.Errorf("delete memory: %w", err)
	}
	if res.RowsAffected > 0 {
		t.takeQuota(run.RunID, true)
	}
	action := "deleted"
	if res.RowsAffected == 0 {
		var n int64
		if err := db.Model(&database.Memory{}).Where("scope = ? AND name = ?", scope, name).Count(&n).Error; err != nil {
			return nil, err
		}
		action = "not_found"
		if n > 0 {
			action = "refused_protected"
		}
	}
	t.logger.Printf("memory.delete run=%s scope=%s name=%s action=%s", run.RunID, scope, name, action)
	return marshal(map[string]interface{}{"deleted": action == "deleted", "action": action, "scope": scope, "name": name})
}
