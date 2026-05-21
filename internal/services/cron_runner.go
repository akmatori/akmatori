package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// ErrCronJobNotFound is returned by CronRunner lookups when no CronJob row
// matches the supplied UUID. Surfacing a typed error lets handlers translate
// it to 404 without leaking GORM into the handler layer.
var ErrCronJobNotFound = errors.New("cron job not found")

// ErrInvalidCronSchedule is returned when a write-time schedule fails to parse
// against the standard 5-field crontab grammar (m h dom mon dow). The error
// message includes the parser's failure so the UI can surface it to operators.
var ErrInvalidCronSchedule = errors.New("invalid cron schedule")

// ErrChannelNotPostable is returned when a cron job or alert source tries to
// reference a Channel without the CanPost capability. Catching this at write
// time gives a clean validation error rather than a silent fall-through at
// fire time. Mirrors CLAUDE.md's "CanPost / CanListen capability flags gate
// which triggers may reference a channel" rule.
var ErrChannelNotPostable = errors.New("channel cannot be used for outbound posts (CanPost=false)")

// ErrSystemCronImmutable is returned from DeleteJob when the target row is a
// seeded system cron (IsSystem=true). System rows can be disabled but not
// deleted so dreaming-style maintenance jobs (memory-curator, future REM/deep
// phases) survive operator pruning. Surfacing a typed error lets the API map
// it to 409 without leaking schema details.
var ErrSystemCronImmutable = errors.New("system cron jobs cannot be deleted")

// cronChannelPostTimeout caps how long the outbound provider call can block
// the tick goroutine. A hung Slack API call (network outage, rate limit) would
// otherwise pin the per-job goroutine indefinitely; capping at 30s keeps the
// runner ticking and surfaces a clear LastRunError when the provider stalls.
const cronChannelPostTimeout = 30 * time.Second

// cronProviderResolveDefault is the messaging provider consulted when a cron
// job's Channel cannot be loaded — keeps ticks falling back to the workspace
// default rather than crashing the runner.
const cronProviderResolveDefault = database.MessagingProviderSlack

// CronJobUpdate is the patch shape applied to UpdateJob. Pointer fields make
// partial updates explicit so the handler can submit just the operator-edited
// columns rather than re-sending the entire row.
//
// ToolInstanceIDs is a pointer to a slice so the handler can distinguish
// "leave tools alone" (nil) from "replace with empty list" (&[]uint{}).
// Without the indirection, a missing JSON field would be indistinguishable
// from one that explicitly clears the allowlist.
type CronJobUpdate struct {
	Name            *string
	Schedule        *string
	Prompt          *string
	ChannelUUID     *string
	Enabled         *bool
	ToolInstanceIDs *[]uint
}

// cronScheduler is the slice of robfig/cron/v3.*Cron the runner depends on so
// tests can swap a fake (or a no-op) without touching real wall-clock time.
type cronScheduler interface {
	AddFunc(spec string, cmd func()) (cron.EntryID, error)
	Remove(id cron.EntryID)
	Entry(id cron.EntryID) cron.Entry
	Start()
	Stop() context.Context
}

// CronRunner owns the in-process cron scheduler and exposes the CronManager
// surface for the HTTP API. The scheduler is started in main() and runs for
// the lifetime of the process; CRUD calls re-register entries so changes take
// effect immediately without an API restart.
//
// Field ordering: ChannelManager + ProviderRegistry are wired before Start so
// the tick path can resolve channels at fire time (operators may rename or
// reassign channels while the runner is live). SkillIncidentManager +
// IncidentRunner drive the agent path: leaving them nil makes ticks surface
// a clean "agent runner not wired" LastRunError rather than crashing.
type CronRunner struct {
	db        *gorm.DB
	channels  ChannelManager
	registry  ProviderRegistry
	skills    SkillIncidentManager
	runner    IncidentRunner
	scheduler cronScheduler
	parser    cron.Parser

	mu       sync.Mutex
	entries  map[uint]cron.EntryID // cronJob.ID -> scheduler entry
	started  bool
	stopFn   context.CancelFunc
	inflight sync.WaitGroup // tracks ticks dispatched by RunNow so tests + Stop can join
}

// NewCronRunner constructs a CronRunner bound to the global DB and supplied
// dependencies. The scheduler is created in standard (5-field) parse mode so
// "*/2 * * * *" works without seconds.
func NewCronRunner(channels ChannelManager, registry ProviderRegistry, skills SkillIncidentManager, runner IncidentRunner) *CronRunner {
	return newCronRunnerWithDeps(database.GetDB(), channels, registry, skills, runner, cron.New())
}

// newCronRunnerWithDeps is the test seam: injects the DB, the scheduler, and
// the dependencies so unit tests can drive the runner without touching the
// global DB / wall-clock cron.
func newCronRunnerWithDeps(db *gorm.DB, channels ChannelManager, registry ProviderRegistry, skills SkillIncidentManager, runner IncidentRunner, scheduler cronScheduler) *CronRunner {
	return &CronRunner{
		db:        db,
		channels:  channels,
		registry:  registry,
		skills:    skills,
		runner:    runner,
		scheduler: scheduler,
		parser:    cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		entries:   map[uint]cron.EntryID{},
	}
}

// Start loads every enabled CronJob row, registers each in the scheduler, and
// begins ticking. Calling Start more than once is a no-op so main() can
// safely defer cancellation without worrying about double-start.
//
// The returned context is used only for cancellation: when the supplied
// parent is cancelled (or Stop is called), the scheduler is stopped and the
// runner stops firing ticks. Existing in-flight ticks are not interrupted —
// they complete on their own and write their LastRunStatus row.
func (r *CronRunner) Start(parent context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// Load before flipping `started` so a transient DB error at boot does not
	// leave the runner in a half-started state where Start is a no-op but the
	// scheduler never actually ticks.
	var jobs []database.CronJob
	if err := r.db.Where("enabled = ?", true).Find(&jobs).Error; err != nil {
		return fmt.Errorf("load cron jobs: %w", err)
	}

	r.mu.Lock()
	r.started = true
	r.mu.Unlock()

	for _, job := range jobs {
		if err := r.register(job); err != nil {
			slog.Warn("failed to register cron job", "uuid", job.UUID, "err", err)
			continue
		}
	}

	r.scheduler.Start()

	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.stopFn = cancel
	r.mu.Unlock()

	go func() {
		<-ctx.Done()
		r.Stop()
	}()
	return nil
}

// Stop halts the scheduler. Safe to call multiple times.
func (r *CronRunner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	stop := r.stopFn
	r.stopFn = nil
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
	r.scheduler.Stop()
}

// register adds (or replaces) a scheduler entry for the supplied job. Holds
// the runner lock so concurrent reloads do not double-register.
func (r *CronRunner) register(job database.CronJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerLocked(job)
}

func (r *CronRunner) registerLocked(job database.CronJob) error {
	// Replace any prior entry for this job. Safe even when absent.
	if prior, ok := r.entries[job.ID]; ok {
		r.scheduler.Remove(prior)
		delete(r.entries, job.ID)
	}
	if !job.Enabled {
		return nil
	}
	schedule, err := r.parser.Parse(job.Schedule)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCronSchedule, err)
	}
	jobID := job.ID
	entry, err := r.scheduler.AddFunc(job.Schedule, func() {
		r.fire(jobID)
	})
	if err != nil {
		return fmt.Errorf("schedule cron job: %w", err)
	}
	r.entries[job.ID] = entry

	// Update NextRunAt so the API can render the next firing time without
	// having to re-parse the schedule on every read. Use the scheduler's
	// Schedule.Next so the value matches what robfig/cron will actually fire.
	next := schedule.Next(time.Now())
	if err := r.db.Model(&database.CronJob{}).Where("id = ?", job.ID).Update("next_run_at", next).Error; err != nil {
		slog.Warn("failed to update next_run_at", "id", job.ID, "err", err)
	}
	return nil
}

// Reload re-registers a job after CRUD. It is safe to call with a job that no
// longer exists (Delete path) — the existing entry is removed and no new one
// is added.
func (r *CronRunner) Reload(jobID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var job database.CronJob
	err := r.db.First(&job, jobID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if prior, ok := r.entries[jobID]; ok {
			r.scheduler.Remove(prior)
			delete(r.entries, jobID)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload cron job %d: %w", jobID, err)
	}
	return r.registerLocked(job)
}

// fire executes a single tick for the supplied job ID. Reloads the row each
// time so an in-flight edit (channel change, prompt change, disable) takes
// effect on the very next firing. Preloads the per-cron tool allowlist
// (Tools.ToolType) so execute() does not have to re-query the join during
// the hot path.
func (r *CronRunner) fire(jobID uint) {
	var job database.CronJob
	if err := r.db.Preload("Channel.Integration").Preload("Tools.ToolType").First(&job, jobID).Error; err != nil {
		slog.Warn("cron tick: job vanished", "id", jobID, "err", err)
		return
	}
	if !job.Enabled {
		return
	}
	r.execute(&job)
}

// RunNow fires a manual tick for the supplied UUID. Returns ErrCronJobNotFound
// when the row is absent so the handler can surface a 404. The actual tick
// runs in a detached goroutine so the HTTP caller does not block on a
// multi-minute agent investigation; tick results land on the row via
// recordResult and operators can poll LastRunStatus / LastRunError for the
// outcome.
func (r *CronRunner) RunNow(uuidStr string) error {
	var job database.CronJob
	err := r.db.Preload("Channel.Integration").Preload("Tools.ToolType").Where("uuid = ?", uuidStr).First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCronJobNotFound
		}
		return fmt.Errorf("load cron job %s: %w", uuidStr, err)
	}
	r.inflight.Add(1)
	go func() {
		defer r.inflight.Done()
		r.execute(&job)
	}()
	return nil
}

// WaitForInflight blocks until every tick previously dispatched by RunNow has
// completed. Production callers do not need this; tests use it as a sync point
// because RunNow is fire-and-forget.
func (r *CronRunner) WaitForInflight() {
	r.inflight.Wait()
}

// execute runs a single cron tick through the full cron-agent system skill.
// The flow mirrors handlers/alert_processor.go but is deliberately stripped
// down: there is no Slack typing controller, progress streamer, or
// summarizer to budget — a cron tick posts a single fresh message to the
// configured channel when the run finishes. Provenance is recorded on the
// Incident row via source_kind="cron" + source_uuid=<cron_job.uuid>, so the
// timeline UI can group cron-spawned investigations.
//
// Failure modes are all captured as LastRunError so operators can debug
// without tailing API logs: missing dependencies, channel/provider
// resolution, worker disconnect, agent worker errors, and provider posting
// errors each fall into a distinct branch. Anything that crashes the agent
// run records LastRunStatus=error rather than propagating up — the runner
// must survive a misbehaving tick.
//
// Tool allowlist is per-cron: the runner derives a ToolAllowlistEntry list
// from job.Tools (preloaded by fire/RunNow) rather than calling the global
// SkillIncidentManager.GetToolAllowlist(). Each cron declares its own narrow
// set of infrastructure tools — a dreaming-style memory-curator cron, for
// example, ships with no infrastructure tools at all.
func (r *CronRunner) execute(job *database.CronJob) {
	if r.channels == nil || r.registry == nil {
		r.recordResult(job, database.CronJobRunStatusError, "cron runner is missing channel/provider wiring")
		return
	}
	if r.skills == nil || r.runner == nil {
		r.recordResult(job, database.CronJobRunStatusError, "cron runner is missing agent runner wiring")
		return
	}

	channel, err := r.resolveChannel(job)
	if err != nil {
		r.recordResult(job, database.CronJobRunStatusError, err.Error())
		return
	}
	provider, err := r.registry.Get(channel.Integration.Provider)
	if err != nil {
		r.recordResult(job, database.CronJobRunStatusError, fmt.Sprintf("provider unavailable: %v", err))
		return
	}
	if !r.runner.IsWorkerConnected() {
		r.recordResult(job, database.CronJobRunStatusError, "agent worker not connected")
		return
	}

	// Spawn a cron-agent invocation. The IncidentContext stamps
	// source_kind=cron and source_uuid=<cron_job.uuid> so the resulting
	// Incident row links back to this scheduled job in the UI. The root
	// skill name is "cron-agent" rather than "incident-manager" so the
	// generated AGENTS.md carries the scheduled-task framing.
	incCtx := &IncidentContext{
		Source:     "cron",
		SourceID:   job.UUID,
		SourceKind: database.IncidentSourceKindCron,
		SourceUUID: job.UUID,
		Context: database.JSONB{
			"cron_job_uuid":     job.UUID,
			"cron_job_name":     job.Name,
			"cron_job_schedule": job.Schedule,
			"cron_job_prompt":   job.Prompt,
		},
		Message: fmt.Sprintf("Cron: %s", job.Name),
	}
	incidentUUID, _, err := r.skills.SpawnAgentInvocation(cronAgentSkillName, incCtx)
	if err != nil {
		r.recordResult(job, database.CronJobRunStatusError, fmt.Sprintf("spawn incident: %v", err))
		return
	}
	if err := r.skills.UpdateIncidentStatus(incidentUUID, database.IncidentStatusRunning, "", ""); err != nil {
		slog.Warn("cron agent: failed to update incident status", "incident", incidentUUID, "err", err)
	}

	var llmSettings *LLMSettingsForWorker
	if dbSettings, err := database.GetLLMSettings(); err == nil && dbSettings != nil {
		llmSettings = BuildLLMSettingsForWorker(dbSettings)
	}
	// Only the cron-agent root skill is enabled for the run. The global
	// enabled-skills set (incident-manager + operator skills) is intentionally
	// NOT forwarded — a scheduled cron run should not pick up the alert-driven
	// incident-manager prompt nor inherit unrelated operator skills.
	skillNames := []string{cronAgentSkillName}
	toolAllowlist := buildCronToolAllowlist(job.Tools)

	taskHeader := fmt.Sprintf("Cron Investigation: %s\nSchedule: %s\n\n--- Execution Log ---\n\n", job.Name, job.Schedule)

	// Async result handling — matches alert_processor.go pattern. closeOnce
	// guards the done channel so a stray OnError after OnCompleted does not
	// double-close.
	done := make(chan struct{})
	var closeOnce sync.Once
	var response string
	var sessionID string
	var hasError bool
	var supersededFlag atomic.Bool
	var errorMsg string
	var lastStreamedLog string
	var finalTokensUsed int
	var finalExecutionTimeMs int64

	callback := IncidentCallback{
		OnOutput: func(output string) {
			lastStreamedLog += output
			if err := r.skills.UpdateIncidentLog(incidentUUID, taskHeader+lastStreamedLog); err != nil {
				slog.Warn("cron agent: failed to update incident log", "incident", incidentUUID, "err", err)
			}
		},
		OnCompleted: func(sid, output string, tokensUsed int, executionTimeMs int64) {
			sessionID = sid
			response = output
			finalTokensUsed = tokensUsed
			finalExecutionTimeMs = executionTimeMs
			closeOnce.Do(func() { close(done) })
		},
		OnError: func(em string) {
			hasError = true
			errorMsg = em
			response = fmt.Sprintf("Error: %s", em)
			closeOnce.Do(func() { close(done) })
		},
		OnSuperseded: func() {
			supersededFlag.Store(true)
			closeOnce.Do(func() { close(done) })
		},
	}

	// Do NOT wrap with executor.PrependGuidance: that helper is incident-triage
	// framed ("Original alert text", "Please help with the following incident
	// or request"), and it makes runbook + memory search MANDATORY. The
	// cron-agent system prompt (DefaultCronAgentPrompt) deliberately reframes
	// the agent as "not triaging an incident" and treats recall as optional —
	// the seeded memory-curator cron, for example, has no infrastructure tools
	// and no incident framing to lean on. Prepend only the current UTC time so
	// the model can reason about scheduling without inheriting the alert SOP.
	taskWithTime := fmt.Sprintf("Current time: %s\n\n%s",
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"), job.Prompt)
	runID, err := r.runner.StartIncident(incidentUUID, taskWithTime, llmSettings, skillNames, toolAllowlist, callback)
	if err != nil {
		errStr := fmt.Sprintf("start incident: %v", err)
		if updateErr := r.skills.UpdateIncidentComplete(incidentUUID, database.IncidentStatusFailed, "", "", errStr, 0, 0); updateErr != nil {
			slog.Warn("cron agent: failed to update incident on start error", "incident", incidentUUID, "err", updateErr)
		}
		r.recordResult(job, database.CronJobRunStatusError, errStr)
		return
	}

	<-done

	// A superseded run hands ownership to the replacement; exit silently so
	// the replacement run owns the DB finalize + channel post.
	if supersededFlag.Load() {
		slog.Info("cron agent: investigation superseded; leaving finalization to the new run", "incident", incidentUUID)
		return
	}

	// Claim ownership of finalization. A newer Start/Continue that arrived
	// between OnCompleted and here invalidates this run — return without
	// touching the DB or posting to the channel.
	if !r.runner.ReleaseRun(incidentUUID, runID) {
		slog.Info("cron agent: investigation displaced during finalization", "incident", incidentUUID)
		return
	}

	fullLog := taskHeader + lastStreamedLog
	if response != "" {
		fullLog += "\n\n--- Final Response ---\n\n" + response
	}

	finalStatus := database.IncidentStatusCompleted
	if hasError {
		finalStatus = database.IncidentStatusFailed
	}
	if err := r.skills.UpdateIncidentComplete(incidentUUID, finalStatus, sessionID, fullLog, response, finalTokensUsed, finalExecutionTimeMs); err != nil {
		slog.Warn("cron agent: failed to update incident complete", "incident", incidentUUID, "err", err)
	}

	// Post the final summary to the cron's channel as a fresh message. On
	// error, surface the failure into the channel so operators see the cron
	// tick failed without having to open the incident.
	body := formatCronAgentMessage(job, response, hasError, errorMsg)
	postCtx, cancel := context.WithTimeout(context.Background(), cronChannelPostTimeout)
	defer cancel()
	if _, err := provider.PostMessage(postCtx, channel, body); err != nil {
		r.recordResult(job, database.CronJobRunStatusError, fmt.Sprintf("post message: %v", err))
		return
	}
	if hasError {
		r.recordResult(job, database.CronJobRunStatusError, errorMsg)
		return
	}
	r.recordResult(job, database.CronJobRunStatusOK, "")
}

// formatCronAgentMessage renders the channel-bound summary for an agent-mode
// cron tick. On error the body still gets a header so the channel reader sees
// which cron failed; on success it carries the agent's final response. The
// body is byte-capped so a verbose agent transcript cannot exceed Slack's
// chat.postMessage limit; truncation appends a short suffix so the operator
// can tell their output was clipped.
func formatCronAgentMessage(job *database.CronJob, response string, hasError bool, errorMsg string) string {
	name := strings.TrimSpace(job.Name)
	if name == "" {
		name = "Scheduled investigation"
	}
	if hasError {
		msg := strings.TrimSpace(errorMsg)
		if msg == "" {
			msg = "Investigation failed"
		}
		return capCronMessageBytes(fmt.Sprintf("*%s*\nInvestigation failed: %s", name, msg))
	}
	body := strings.TrimSpace(response)
	if body == "" {
		body = "Investigation completed (no output)"
	}
	return capCronMessageBytes(fmt.Sprintf("*%s*\n%s", name, body))
}

// resolveChannel returns the Channel the supplied cron job should post into.
// The explicit ChannelID wins as long as the channel and its integration are
// both enabled and the channel can post; otherwise we fall back to the
// per-provider default so a disabled or non-posting channel does not silently
// black-hole the message. Matches the semantics of
// ChannelService.ResolveForAlertSource for the outbound-alert path.
//
// Fallback provider selection: prefer the explicit channel's Integration
// provider when the cron job referenced one (so a disabled Telegram channel
// falls back to the Telegram default, not Slack). When no explicit channel is
// referenced at all, default to Slack since that is the operator's most
// common configuration.
func (r *CronRunner) resolveChannel(job *database.CronJob) (*database.Channel, error) {
	if ch := r.usableExplicitChannel(job); ch != nil {
		return ch, nil
	}
	fallbackProvider := cronProviderResolveDefault
	if explicit := r.loadExplicitChannelAny(job); explicit != nil && explicit.Integration.Provider != "" {
		fallbackProvider = explicit.Integration.Provider
	}
	ch, err := r.channels.ResolveDefault(fallbackProvider)
	if err != nil {
		if errors.Is(err, ErrChannelNotFound) {
			return nil, fmt.Errorf("no channel configured for cron job and no default available")
		}
		return nil, err
	}
	return ch, nil
}

// loadExplicitChannelAny returns the cron's explicit Channel regardless of
// whether it is currently usable for posting. Used by resolveChannel to learn
// the operator's intended provider when falling back to that provider's
// workspace default. Nil return means no explicit channel was configured.
func (r *CronRunner) loadExplicitChannelAny(job *database.CronJob) *database.Channel {
	switch {
	case job.Channel != nil && job.Channel.ID != 0:
		return job.Channel
	case job.ChannelID != nil:
		ch, err := r.loadChannelByID(*job.ChannelID)
		if err != nil {
			return nil
		}
		return ch
	}
	return nil
}

// usableExplicitChannel returns the cron's explicit Channel when present AND
// usable for outbound posting. A nil return signals the caller to fall back to
// the workspace default; a non-recoverable DB error returns nil here too
// (resolveChannel will surface ResolveDefault's error in that case).
func (r *CronRunner) usableExplicitChannel(job *database.CronJob) *database.Channel {
	var candidate *database.Channel
	switch {
	case job.Channel != nil && job.Channel.ID != 0:
		candidate = job.Channel
	case job.ChannelID != nil:
		ch, err := r.loadChannelByID(*job.ChannelID)
		if err != nil {
			return nil
		}
		candidate = ch
	default:
		return nil
	}
	if !candidate.Enabled || !candidate.CanPost || !candidate.Integration.Enabled {
		return nil
	}
	return candidate
}

// loadChannelByID is a small helper around the DB so the resolver can keep its
// error branches readable.
func (r *CronRunner) loadChannelByID(id uint) (*database.Channel, error) {
	var ch database.Channel
	if err := r.db.Preload("Integration").First(&ch, id).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// cronAgentSkillName is the system-skill name used as the root prompt for
// scheduled cron runs. Centralised so the runner, AGENTS.md generator, and
// tests agree on a single literal.
const cronAgentSkillName = "cron-agent"

// buildCronToolAllowlist maps a CronJob's preloaded Tools slice into the
// ToolAllowlistEntry shape the agent worker expects. Disabled tool instances
// are filtered out so an operator-disabled tool cannot be silently invoked
// by a cron run; the ToolType name is taken from the preloaded relation,
// matching SkillService.GetToolAllowlist's behavior.
//
// Returns a non-nil empty slice when no tools are assigned so the gateway
// receives an explicit empty allowlist and rejects all infrastructure tool
// calls (memory-only / runbook-only cron runs are an intended deployment
// mode for dreaming-style maintenance jobs).
func buildCronToolAllowlist(tools []database.ToolInstance) []ToolAllowlistEntry {
	entries := make([]ToolAllowlistEntry, 0, len(tools))
	for i := range tools {
		t := tools[i]
		if !t.Enabled {
			continue
		}
		entries = append(entries, ToolAllowlistEntry{
			InstanceID:  t.ID,
			LogicalName: t.LogicalName,
			ToolType:    t.ToolType.Name,
		})
	}
	return entries
}

// cronChannelMaxMessageBytes caps the byte size of an outbound cron message.
// Slack's chat.postMessage hard limit is ~40,000 bytes; we keep the cap at
// 8000 to match the alert/Slack flow's slackMaxTextBytes so cron messages
// stay readable in a long thread and so the operator sees their output
// truncated before Slack truncates it silently.
const cronChannelMaxMessageBytes = 8000

// capCronMessageBytes truncates the supplied message to fit
// cronChannelMaxMessageBytes, appending a clipped-marker so the operator can
// tell the body was cut. Avoids slicing in the middle of a multi-byte rune.
func capCronMessageBytes(msg string) string {
	if len(msg) <= cronChannelMaxMessageBytes {
		return msg
	}
	const suffix = "\n\n_...truncated. See full response in the UI._"
	cutoff := cronChannelMaxMessageBytes - len(suffix)
	if cutoff < 100 {
		cutoff = 100
	}
	// Walk cutoff back while the first EXCLUDED byte (msg[cutoff]) is a UTF-8
	// continuation byte (high bits 10xxxxxx). After the loop msg[cutoff] is a
	// rune start byte (or end-of-string), so msg[:cutoff] is valid UTF-8 and
	// never ends inside a multi-byte rune.
	for cutoff > 0 && cutoff < len(msg) && (msg[cutoff]&0xC0) == 0x80 {
		cutoff--
	}
	truncated := msg[:cutoff]
	if idx := strings.LastIndex(truncated, "\n"); idx > cutoff/2 {
		truncated = truncated[:idx]
	}
	return truncated + suffix
}

// recordResult persists the outcome of a tick. Uses Updates with a map so
// LastRunError can be cleared (assigned to "") when a tick succeeds.
// NextRunAt is recomputed when possible so the API does not show a stale
// firing time after a manual RunNow.
func (r *CronRunner) recordResult(job *database.CronJob, status string, errMsg string) {
	now := time.Now()
	updates := map[string]interface{}{
		"last_run_at":     now,
		"last_run_status": status,
		"last_run_error":  errMsg,
	}
	if schedule, err := r.parser.Parse(job.Schedule); err == nil {
		updates["next_run_at"] = schedule.Next(now)
	}
	if err := r.db.Model(&database.CronJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
		slog.Warn("failed to persist cron tick result", "id", job.ID, "err", err)
		return
	}
	if status == database.CronJobRunStatusError {
		slog.Warn("cron tick failed", "uuid", job.UUID, "name", job.Name, "err", errMsg)
	} else {
		slog.Info("cron tick completed", "uuid", job.UUID, "name", job.Name)
	}
}

// ========== CRUD ==========

// ListJobs returns every CronJob ordered by name. Preloads Channel + its
// Integration so the API response can render the post destination without an
// N+1 query, and Tools.ToolType so the response can render the per-cron tool
// allowlist for the UI tool picker.
func (r *CronRunner) ListJobs() ([]database.CronJob, error) {
	var jobs []database.CronJob
	if err := r.db.Preload("Channel.Integration").Preload("Tools.ToolType").Order("name asc").Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	return jobs, nil
}

// GetJobByUUID resolves a CronJob by its public UUID handle. Returns
// ErrCronJobNotFound when missing so handlers can return 404. Preloads the
// per-cron tool allowlist so callers (handler, runner) can inspect tools
// without an extra round trip.
func (r *CronRunner) GetJobByUUID(uuidStr string) (*database.CronJob, error) {
	var job database.CronJob
	err := r.db.Preload("Channel.Integration").Preload("Tools.ToolType").Where("uuid = ?", uuidStr).First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCronJobNotFound
		}
		return nil, fmt.Errorf("get cron job %s: %w", uuidStr, err)
	}
	return &job, nil
}

// CreateJob inserts a new CronJob row, registers it with the scheduler, and
// returns the persisted row. Empty channelUUID is accepted; the runner falls
// back to the workspace default at tick time. toolInstanceIDs is the
// per-cron tool allowlist — empty slice or nil means the cron-agent runs
// with no infrastructure tools (memory + runbooks only). Operator-created
// jobs are always IsSystem=false; system rows are seeded exclusively via
// database.SeedSystemCronJobs.
func (r *CronRunner) CreateJob(name, schedule, prompt string, channelUUID string, enabled bool, toolInstanceIDs []uint) (*database.CronJob, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("cron job name cannot be empty")
	}
	schedule = strings.TrimSpace(schedule)
	if err := r.validateSchedule(schedule); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("cron job prompt cannot be empty")
	}

	var channelID *uint
	if channelUUID != "" {
		ch, err := r.channels.GetChannelByUUID(channelUUID)
		if err != nil {
			return nil, err
		}
		if !ch.CanPost {
			return nil, ErrChannelNotPostable
		}
		id := ch.ID
		channelID = &id
	}

	tools, err := r.resolveToolInstances(toolInstanceIDs)
	if err != nil {
		return nil, err
	}

	row := &database.CronJob{
		UUID:      uuid.New().String(),
		Name:      name,
		Schedule:  schedule,
		Prompt:    prompt,
		ChannelID: channelID,
		Enabled:   enabled,
	}
	if err := r.db.Create(row).Error; err != nil {
		return nil, fmt.Errorf("create cron job: %w", err)
	}
	// GORM v2 omits zero-value bools from INSERT, so the column-level
	// `default:true` flips a caller-requested Enabled=false back to true.
	// Without this guard a "create-disabled" cron job would start firing
	// immediately — a particularly bad surprise for the create-then-review
	// workflow.
	if !enabled {
		if err := r.db.Model(row).Update("enabled", false).Error; err != nil {
			return nil, fmt.Errorf("apply enabled=false on create: %w", err)
		}
	}
	if err := r.db.Model(row).Association("Tools").Replace(tools); err != nil {
		return nil, fmt.Errorf("attach cron job tools: %w", err)
	}
	if err := r.Reload(row.ID); err != nil {
		slog.Warn("failed to schedule newly created cron job", "uuid", row.UUID, "err", err)
	}
	return r.GetJobByUUID(row.UUID)
}

// resolveToolInstances loads ToolInstance rows for the supplied IDs, with
// ToolType preloaded so the agent allowlist mapping does not need a second
// query. An empty input returns an empty slice so an operator can explicitly
// clear a cron's tool allowlist. An unknown ID surfaces as a typed error so
// the API can map it to 400; partial resolution would silently drop tools
// without operator feedback.
func (r *CronRunner) resolveToolInstances(ids []uint) ([]database.ToolInstance, error) {
	if len(ids) == 0 {
		return []database.ToolInstance{}, nil
	}
	var tools []database.ToolInstance
	if err := r.db.Preload("ToolType").Where("id IN ?", ids).Find(&tools).Error; err != nil {
		return nil, fmt.Errorf("load cron job tools: %w", err)
	}
	if len(tools) != len(ids) {
		found := make(map[uint]bool, len(tools))
		for _, t := range tools {
			found[t.ID] = true
		}
		for _, id := range ids {
			if !found[id] {
				return nil, fmt.Errorf("tool instance %d not found", id)
			}
		}
	}
	return tools, nil
}

// UpdateJob applies the supplied patch. Re-registers the scheduler entry so a
// schedule change, channel change, or enable/disable takes effect on the next
// firing without an API restart. System rows can be patched (operators must
// be able to enable/disable seeded crons + repoint their channel) but the
// row itself remains undeletable; DeleteJob enforces that.
func (r *CronRunner) UpdateJob(uuidStr string, patch CronJobUpdate) (*database.CronJob, error) {
	job, err := r.GetJobByUUID(uuidStr)
	if err != nil {
		return nil, err
	}

	updates := map[string]interface{}{}
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" {
			return nil, fmt.Errorf("cron job name cannot be empty")
		}
		updates["name"] = trimmed
	}
	if patch.Schedule != nil {
		schedule := strings.TrimSpace(*patch.Schedule)
		if err := r.validateSchedule(schedule); err != nil {
			return nil, err
		}
		updates["schedule"] = schedule
	}
	if patch.Prompt != nil {
		if strings.TrimSpace(*patch.Prompt) == "" {
			return nil, fmt.Errorf("cron job prompt cannot be empty")
		}
		updates["prompt"] = *patch.Prompt
	}
	if patch.ChannelUUID != nil {
		if *patch.ChannelUUID == "" {
			updates["channel_id"] = nil
		} else {
			ch, err := r.channels.GetChannelByUUID(*patch.ChannelUUID)
			if err != nil {
				return nil, err
			}
			if !ch.CanPost {
				return nil, ErrChannelNotPostable
			}
			updates["channel_id"] = ch.ID
		}
	}
	if patch.Enabled != nil {
		updates["enabled"] = *patch.Enabled
	}
	// Pre-resolve the new tools BEFORE issuing the column update so an unknown
	// tool ID fails the whole patch cleanly rather than leaving column fields
	// partially applied with a stale Tools association.
	var newTools []database.ToolInstance
	replaceTools := false
	if patch.ToolInstanceIDs != nil {
		replaceTools = true
		resolved, rerr := r.resolveToolInstances(*patch.ToolInstanceIDs)
		if rerr != nil {
			return nil, rerr
		}
		newTools = resolved
	}
	if len(updates) == 0 && !replaceTools {
		return job, nil
	}
	if len(updates) > 0 {
		if err := r.db.Model(&database.CronJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update cron job: %w", err)
		}
	}
	if replaceTools {
		if err := r.db.Model(&database.CronJob{ID: job.ID}).Association("Tools").Replace(newTools); err != nil {
			return nil, fmt.Errorf("update cron job tools: %w", err)
		}
	}
	if err := r.Reload(job.ID); err != nil {
		slog.Warn("failed to reload cron job after update", "uuid", job.UUID, "err", err)
	}
	return r.GetJobByUUID(job.UUID)
}

// DeleteJob removes a CronJob row and unregisters its scheduler entry.
// System rows (IsSystem=true) return ErrSystemCronImmutable so dreaming-style
// maintenance crons cannot be removed by an operator click — they can be
// disabled via UpdateJob instead.
func (r *CronRunner) DeleteJob(uuidStr string) error {
	job, err := r.GetJobByUUID(uuidStr)
	if err != nil {
		return err
	}
	if job.IsSystem {
		return ErrSystemCronImmutable
	}
	if err := r.db.Delete(&database.CronJob{}, job.ID).Error; err != nil {
		return fmt.Errorf("delete cron job: %w", err)
	}
	if err := r.Reload(job.ID); err != nil {
		slog.Warn("failed to unregister cron job after delete", "uuid", job.UUID, "err", err)
	}
	return nil
}

// validateSchedule parses spec against the runner's standard 5-field parser
// and returns ErrInvalidCronSchedule when the parser rejects it. Surfaces the
// parser's own message so operators see "Expected 5 fields, found 4" rather
// than a generic "invalid".
func (r *CronRunner) validateSchedule(spec string) error {
	if spec == "" {
		return fmt.Errorf("%w: schedule cannot be empty", ErrInvalidCronSchedule)
	}
	if _, err := r.parser.Parse(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCronSchedule, err)
	}
	return nil
}

// ensure CronRunner satisfies the CronJobManager interface so wiring
// mismatches surface at compile-time.
var _ CronJobManager = (*CronRunner)(nil)
