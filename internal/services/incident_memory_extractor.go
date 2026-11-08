package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// Job mechanics of the post-incident memory pass (as Codex's memory jobs:
// a claim, a lease, retries with a backoff).
const (
	// extractionTick is the poll interval; close paths also Kick the loop.
	extractionTick = 30 * time.Second
	// extractionLease: a row running for longer is claimable again, so a
	// job stuck in a live process is re-run without a restart.
	extractionLease = 15 * time.Minute
	// extractionMaxAttempts: a transient failure retries with a backoff
	// until this many attempts were made, then the row is failed.
	extractionMaxAttempts = 3
	// extractionTimeout caps the one-shot call (the worker's HTTP limit is
	// 60 s).
	extractionTimeout = 50 * time.Second
	// extractionMaxTokens is the answer budget: a timeline and at most five
	// memories.
	extractionMaxTokens = 4000
	// extractionErrMaxLen caps the error stored on the incident row.
	extractionErrMaxLen = 300
)

// extractionBackoff is the retry delay after the n-th failed attempt.
var extractionBackoff = []time.Duration{2 * time.Minute, 10 * time.Minute, time.Hour}

// Rerun errors (POST /api/incidents/{uuid}/extract-memory).
var (
	// ErrExtractionBusy: the incident's extraction is pending or running.
	ErrExtractionBusy = errors.New("memory extraction is already pending or running")
	// ErrExtractionNotClosed: only a closed incident is extracted (a merged
	// incident is read with its survivor).
	ErrExtractionNotClosed = errors.New("memory extraction needs a closed incident")
)

// extractionStore is the part of MemoryService the extractor writes to.
type extractionStore interface {
	ApplyExtraction(ctx context.Context, incidentUUID string, claimAt time.Time, res ExtractionResult) (int, error)
}

// manifestRefresher regenerates the SKILL.md manifests and the disk mirror
// after memories changed (SkillService.RefreshAfterMemoryChange).
type manifestRefresher interface {
	RefreshAfterMemoryChange(runID string)
}

// IncidentMemoryExtractor is the post-incident pass: when an alert incident
// closes, a one-shot LLM reads the facts (close reason, how each alert
// resolved, the runs' self-reports and timestamps), the transcripts and
// the final answers, and writes graded memories (D3 of the extractor plan).
// The investigating agent no longer writes memory during an alert
// investigation (the gateway refuses it); this pass knows the outcome.
//
// The queue is on the incident row: every close path sets
// memory_extraction = pending in the UPDATE that closes the row; Run claims
// one row at a time (memory_extraction_at is the claim time and the
// lease), retries transient failures with a backoff and records the outcome
// (succeeded, no_output, skipped, failed). The apply step re-checks the
// claim, so a reopened or re-claimed incident gets no write from a stale
// job.
type IncidentMemoryExtractor struct {
	db        *gorm.DB
	caller    OneShotLLMCaller
	memories  extractionStore
	refresher manifestRefresher
	kick      chan struct{}
	now       func() time.Time
	tick      time.Duration
	// incidentsDir is the work directory root; when set, the pass also reads
	// the tool outputs the worker spilled to <dir>/<uuid>/tool_outputs/.
	incidentsDir string
}

// SetIncidentsDir enables bounded reads of the spilled tool outputs of the
// family's work directories.
func (e *IncidentMemoryExtractor) SetIncidentsDir(dir string) {
	e.incidentsDir = dir
}

// NewIncidentMemoryExtractor returns the extractor. A nil caller fails every
// job as "no LLM" (retried, then failed). refresher may be nil.
func NewIncidentMemoryExtractor(db *gorm.DB, caller OneShotLLMCaller, memories extractionStore, refresher manifestRefresher) *IncidentMemoryExtractor {
	return &IncidentMemoryExtractor{
		db:        db,
		caller:    caller,
		memories:  memories,
		refresher: refresher,
		kick:      make(chan struct{}, 1),
		now:       time.Now,
		tick:      extractionTick,
	}
}

// Kick wakes the loop: a close path or the rerun endpoint queued a row.
func (e *IncidentMemoryExtractor) Kick() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// ResetInterrupted turns the rows a restart left running back into pending,
// so a crash mid-job re-runs it at once (the apply step is idempotent). Call
// it once at startup, before Run.
func (e *IncidentMemoryExtractor) ResetInterrupted() error {
	res := e.db.Model(&database.Incident{}).
		Where("memory_extraction = ?", database.MemoryExtractionRunning).
		Updates(map[string]interface{}{
			"memory_extraction":          database.MemoryExtractionPending,
			"memory_extraction_retry_at": nil,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		slog.Info("memory extraction: re-queued jobs interrupted by a restart", "count", res.RowsAffected)
	}
	return nil
}

// Run drains the queue, then waits for the ticker or a Kick, until ctx is
// cancelled.
func (e *IncidentMemoryExtractor) Run(ctx context.Context) {
	slog.Info("starting post-incident memory extractor")
	ticker := time.NewTicker(e.tick)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			did, err := e.RunOnce(ctx)
			if err != nil {
				slog.Warn("memory extraction: job failed", "err", err)
			}
			if !did {
				break
			}
		}
		select {
		case <-ctx.Done():
			slog.Info("post-incident memory extractor stopped")
			return
		case <-ticker.C:
		case <-e.kick:
		}
	}
}

// RunOnce claims and processes one queued incident. It returns false when
// nothing is claimable.
func (e *IncidentMemoryExtractor) RunOnce(ctx context.Context) (bool, error) {
	inc, claimAt, ok, err := e.claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	e.process(ctx, inc, claimAt)
	return true, nil
}

// claimableWhere is the claim predicate: a closed alert incident that is
// pending (and not waiting for its retry time) or whose running job
// outlived the lease, with no run still running on it (a manual close of a
// running incident waits for the run to finish).
const claimableWhere = "source_kind = ? AND status = ? AND " +
	"((memory_extraction = ? AND (memory_extraction_retry_at IS NULL OR memory_extraction_retry_at <= ?)) " +
	"OR (memory_extraction = ? AND memory_extraction_at < ?)) AND " +
	"NOT EXISTS (SELECT 1 FROM agent_runs WHERE agent_runs.incident_uuid = incidents.uuid AND agent_runs.status = ?)"

func (e *IncidentMemoryExtractor) claimArgs(now time.Time) []interface{} {
	return []interface{}{
		database.IncidentSourceKindAlert, database.IncidentStatusClosed,
		database.MemoryExtractionPending, now,
		database.MemoryExtractionRunning, now.Add(-extractionLease),
		database.AgentRunStatusRunning,
	}
}

// claim takes one claimable row with a conditional UPDATE (RowsAffected = 1
// is the claim). The claim time is truncated to microseconds so it compares
// equal after the round trip through the database.
func (e *IncidentMemoryExtractor) claim(ctx context.Context) (*database.Incident, time.Time, bool, error) {
	now := e.now().Truncate(time.Microsecond)
	db := e.db.WithContext(ctx)
	for attempt := 0; attempt < 5; attempt++ {
		var uuids []string
		if err := db.Model(&database.Incident{}).Where(claimableWhere, e.claimArgs(now)...).
			Order("resolved_at ASC").Limit(1).Pluck("uuid", &uuids).Error; err != nil {
			return nil, now, false, fmt.Errorf("find queued incident: %w", err)
		}
		if len(uuids) == 0 {
			return nil, now, false, nil
		}
		res := db.Model(&database.Incident{}).Where("uuid = ?", uuids[0]).Where(claimableWhere, e.claimArgs(now)...).
			Updates(map[string]interface{}{
				"memory_extraction":          database.MemoryExtractionRunning,
				"memory_extraction_at":       now,
				"memory_extraction_attempts": gorm.Expr("memory_extraction_attempts + 1"),
			})
		if res.Error != nil {
			return nil, now, false, fmt.Errorf("claim incident: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			continue // another worker took it; try the next row
		}
		var inc database.Incident
		if err := db.Where("uuid = ?", uuids[0]).Take(&inc).Error; err != nil {
			return nil, now, false, fmt.Errorf("load claimed incident: %w", err)
		}
		return &inc, now, true, nil
	}
	return nil, now, false, nil
}

// process runs one claimed job to its outcome.
func (e *IncidentMemoryExtractor) process(ctx context.Context, inc *database.Incident, claimAt time.Time) {
	log := slog.With("incident", inc.UUID, "attempt", inc.MemoryExtractionAttempts)
	if inc.MemoryExtractionAttempts > extractionMaxAttempts {
		e.finish(ctx, inc.UUID, claimAt, database.MemoryExtractionFailed,
			fmt.Sprintf("gave up after %d attempts: %s", extractionMaxAttempts, orDash(inc.MemoryExtractionError)))
		return
	}
	if !MemoryGenerationEnabled() {
		e.finish(ctx, inc.UUID, claimAt, database.MemoryExtractionSkipped, "memory generation is off")
		return
	}
	in, err := collectExtractionInput(e.db.WithContext(ctx), inc, e.incidentsDir)
	if err != nil {
		e.retryOrFail(ctx, inc, claimAt, err)
		return
	}
	if !in.hasAnswer() {
		e.finish(ctx, inc.UUID, claimAt, database.MemoryExtractionNoOutput, "nothing to extract: no finished run with an answer")
		return
	}
	raw, err := e.callLLM(ctx, in.Prompt)
	if err != nil {
		e.retryOrFail(ctx, inc, claimAt, err)
		return
	}
	out, err := parseExtractionOutput(raw)
	if err != nil {
		e.retryOrFail(ctx, inc, claimAt, err)
		return
	}
	build := buildExtractedMemories(out, inc.UUID, in.SkillScope, e.validScope)
	note := ""
	if n := in.Redacted + build.Redacted; n > 0 {
		note = fmt.Sprintf("redacted %d secrets", n)
		log.Info("memory extraction: redacted secrets", "count", n)
	}
	written, err := e.memories.ApplyExtraction(ctx, inc.UUID, claimAt, ExtractionResult{
		Memories: build.Memories, Timeline: build.Timeline, Note: note,
	})
	if errors.Is(err, ErrExtractionStale) {
		log.Info("memory extraction: claim no longer holds; nothing written")
		return
	}
	if err != nil {
		e.retryOrFail(ctx, inc, claimAt, err)
		return
	}
	log.Info("memory extraction done", "memories", written, "dropped", build.Dropped, "family", len(in.Family))
	if written > 0 && e.refresher != nil {
		e.refresher.RefreshAfterMemoryChange("extract:" + inc.UUID)
	}
}

// callLLM runs the one-shot call. No configured LLM or no worker is
// ErrWorkerNotConnected (retried, then failed).
func (e *IncidentMemoryExtractor) callLLM(ctx context.Context, user string) (string, error) {
	if e.caller == nil {
		return "", ErrWorkerNotConnected
	}
	settings, err := database.GetLLMSettings()
	if err != nil {
		return "", fmt.Errorf("load llm settings: %w", err)
	}
	if settings == nil || settings.APIKey == "" {
		return "", ErrWorkerNotConnected
	}
	worker := BuildLLMSettingsForWorker(settings)
	if worker == nil {
		return "", ErrWorkerNotConnected
	}
	callCtx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()
	raw, err := e.caller.OneShotLLM(callCtx, worker, extractionSystem(e.now()), user, extractionMaxTokens, 0.0)
	if err != nil {
		return "", fmt.Errorf("llm call: %w", err)
	}
	return raw, nil
}

// validScope reports whether scope is an existing non-system skill (the
// agent scope rule; global is handled by the caller).
func (e *IncidentMemoryExtractor) validScope(scope string) bool {
	var n int64
	if err := e.db.Model(&database.Skill{}).Where("name = ? AND is_system = ?", scope, false).Count(&n).Error; err != nil {
		return false
	}
	return n > 0
}

// finish records a terminal outcome, guarded by the claim.
func (e *IncidentMemoryExtractor) finish(ctx context.Context, uuid string, claimAt time.Time, status, msg string) {
	res := e.db.WithContext(ctx).Model(&database.Incident{}).
		Where("uuid = ? AND memory_extraction = ? AND memory_extraction_at = ?", uuid, database.MemoryExtractionRunning, claimAt).
		Updates(map[string]interface{}{
			"memory_extraction":       status,
			"memory_extraction_error": cutExtractionError(msg),
		})
	if res.Error != nil {
		slog.Warn("memory extraction: store outcome", "incident", uuid, "status", status, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		slog.Info("memory extraction: claim no longer holds; outcome not stored", "incident", uuid, "status", status)
		return
	}
	slog.Info("memory extraction finished", "incident", uuid, "status", status, "note", msg)
}

// retryOrFail re-queues the job with the backoff of its attempt, or fails
// it when the attempts are used up.
func (e *IncidentMemoryExtractor) retryOrFail(ctx context.Context, inc *database.Incident, claimAt time.Time, cause error) {
	attempt := inc.MemoryExtractionAttempts
	if attempt >= extractionMaxAttempts {
		e.finish(ctx, inc.UUID, claimAt, database.MemoryExtractionFailed, cause.Error())
		return
	}
	delay := extractionBackoff[len(extractionBackoff)-1]
	if attempt-1 < len(extractionBackoff) && attempt >= 1 {
		delay = extractionBackoff[attempt-1]
	}
	retryAt := e.now().Add(delay)
	res := e.db.WithContext(ctx).Model(&database.Incident{}).
		Where("uuid = ? AND memory_extraction = ? AND memory_extraction_at = ?", inc.UUID, database.MemoryExtractionRunning, claimAt).
		Updates(map[string]interface{}{
			"memory_extraction":          database.MemoryExtractionPending,
			"memory_extraction_retry_at": &retryAt,
			"memory_extraction_error":    cutExtractionError(cause.Error()),
		})
	if res.Error != nil {
		slog.Warn("memory extraction: re-queue", "incident", inc.UUID, "err", res.Error)
		return
	}
	slog.Warn("memory extraction failed; will retry", "incident", inc.UUID, "attempt", attempt, "retry_in", delay, "err", cause)
}

// Rerun queues a closed incident again, from the UI or a backfill loop. The
// prior output of this incident is replaced (same names update in place).
func (e *IncidentMemoryExtractor) Rerun(ctx context.Context, uuid string) error {
	var inc database.Incident
	if err := e.db.WithContext(ctx).Select("uuid, status, memory_extraction").Where("uuid = ?", uuid).Take(&inc).Error; err != nil {
		return err
	}
	if inc.Status != database.IncidentStatusClosed {
		return ErrExtractionNotClosed
	}
	if inc.MemoryExtraction == database.MemoryExtractionPending || inc.MemoryExtraction == database.MemoryExtractionRunning {
		return ErrExtractionBusy
	}
	res := e.db.WithContext(ctx).Model(&database.Incident{}).
		Where("uuid = ? AND status = ? AND memory_extraction NOT IN ?", uuid, database.IncidentStatusClosed,
			[]string{database.MemoryExtractionPending, database.MemoryExtractionRunning}).
		Updates(map[string]interface{}{
			"memory_extraction":          database.MemoryExtractionPending,
			"memory_extraction_at":       nil,
			"memory_extraction_error":    "",
			"memory_extraction_attempts": 0,
			"memory_extraction_retry_at": nil,
		})
	if res.Error != nil {
		return fmt.Errorf("queue extraction: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrExtractionBusy
	}
	e.Kick()
	return nil
}

// Backfill queues the closed alert incidents that were never extracted
// (closed before the pass existed) and resolved at or after since. It
// returns how many it queued; a rerun of an extracted incident goes through
// Rerun.
func (e *IncidentMemoryExtractor) Backfill(ctx context.Context, since time.Time) (int64, error) {
	res := e.db.WithContext(ctx).Model(&database.Incident{}).
		Where("source_kind = ? AND status = ? AND (memory_extraction IS NULL OR memory_extraction = '') AND resolved_at >= ?",
			database.IncidentSourceKindAlert, database.IncidentStatusClosed, since).
		Updates(map[string]interface{}{
			"memory_extraction":          database.MemoryExtractionPending,
			"memory_extraction_at":       nil,
			"memory_extraction_error":    "",
			"memory_extraction_attempts": 0,
			"memory_extraction_retry_at": nil,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("queue backfill: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		slog.Info("memory extraction: queued a backfill", "incidents", res.RowsAffected, "since", since.UTC().Format(time.RFC3339))
		e.Kick()
	}
	return res.RowsAffected, nil
}

func cutExtractionError(s string) string {
	s, _ = cutUTF8Bytes(s, extractionErrMaxLen)
	return s
}
