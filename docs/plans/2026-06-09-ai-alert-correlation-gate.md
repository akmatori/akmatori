# AI Alert Correlation Gate (Phase 1)

## Overview

Add an AI-powered one-shot LLM gate that runs before each alert spawns a new incident. When the LLM finds a confident match to a recent incident, the incoming alert is attached as a recurrence instead of spawning a new investigation. Singleflight collapse handles simultaneous bursts. Feature is flag-gated (default off) and fail-open.

## Context

- Files involved:
  - New: `internal/services/alert_correlator.go`
  - New: `internal/database/models_alert_correlation.go`
  - Modify: `internal/services/interfaces.go`
  - Modify: `internal/services/incident_service.go`
  - Modify: `internal/database/db.go`
  - Modify: `internal/handlers/alert.go`
  - Modify: `internal/handlers/alert_processor.go`
  - Modify: `cmd/akmatori/main.go`
- Related patterns: `FeedbackClassifier` one-shot pattern (`internal/services/feedback_classifier.go`); `SetResponseFormatter` / `SetSlackSummarizer` setter injection pattern; `IncidentManager.AppendCorrelatedAlert` modeled on existing Context JSONB updates
- Dependencies: `golang.org/x/sync` already in go.mod (indirect)

## Development Approach

- **Testing approach**: Regular (implement, then tests per task)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Database model and schema additions

**Files:**
- Create: `internal/database/models_alert_correlation.go`
- Modify: `internal/database/db.go`

- [x] Create `AlertCorrelationLog` model with fields: ID, SourceUUID, AlertName, TargetHost, MatchedIncidentUUID, Confidence, Reasoning, CreatedAt (indexed on SourceUUID and MatchedIncidentUUID)
- [x] Add 4 nullable columns to `GeneralSettings`: `AlertCorrelationEnabled bool`, `AlertCorrelationWindowMinutes int`, `AlertCorrelationThreshold float64`, `AlertCorrelationMaxCandidates int`
- [x] Add `AlertCorrelationLog` and updated `GeneralSettings` to the `AutoMigrate` call in `db.go`
- [x] Write migration smoke test confirming AutoMigrate succeeds with the new model and columns
- [x] Run `make test` — must pass before Task 2

### Task 2: AlertCorrelator service

**Files:**
- Create: `internal/services/alert_correlator.go`

- [ ] Define `CorrelationConfig` struct (Enabled, Window, MaxCandidates, Threshold with documented defaults: 30m, 20, 0.7)
- [ ] Define `CorrelationVerdict` struct (Correlated bool, IncidentUUID string, Confidence float64, Reasoning string) with `IsConfident(threshold float64) bool` helper
- [ ] Define `AlertCorrelator` struct with fields: caller OneShotLLMCaller, db *gorm.DB, cfg CorrelationConfig
- [ ] Implement `NewAlertCorrelator(caller, db, cfg)` constructor
- [ ] Implement `Correlate(ctx, sourceUUID string, alert alerts.NormalizedAlert) (CorrelationVerdict, error)`:
  - Short-circuit when cfg.Enabled == false or caller == nil → return `{Correlated: false}`
  - Fetch candidates: SELECT uuid,title,status,response,context,started_at FROM incidents WHERE source_kind='alert' AND started_at >= now()-window AND status IN ('pending','running','diagnosed','completed') ORDER BY started_at DESC LIMIT max; return `{Correlated: false}` when zero candidates (no LLM call)
  - Build system prompt (hardcoded constant) and user prompt (numbered candidate list with uuid, status, age, title, 200-char summary snippet from Response or context)
  - Call `caller.OneShotLLM(ctx, llmSettings, systemPrompt, userPrompt, 250, 0.0)` with 15-second timeout
  - Parse via `parseCorrelationVerdict` (strip code fences, unmarshal, clamp confidence to [0..1])
  - Hallucination guard: if IncidentUUID not in fetched candidate set → force `Correlated: false`
  - Return `ErrWorkerNotConnected` as-is; parse failures → `{Correlated: false}` + debug log; all other errors wrapped
- [ ] Write unit tests covering the full matrix: empty window (no LLM call), confident match ≥0.7, confident match <0.7, not correlated, hallucinated UUID, candidate query excludes failed incidents, worker not connected (ErrWorkerNotConnected), malformed JSON, flag off
- [ ] Run `make test` — must pass before Task 3

### Task 3: Interface additions and AppendCorrelatedAlert implementation

**Files:**
- Modify: `internal/services/interfaces.go`
- Modify: `internal/services/incident_service.go`

- [ ] Add `AppendCorrelatedAlert(ctx context.Context, incidentUUID string, alert alerts.NormalizedAlert, confidence float64, reasoning string, at time.Time) error` to the `IncidentManager` interface in `interfaces.go`
- [ ] Implement `AppendCorrelatedAlert` in `incident_service.go`: append entry to `incident.Context` JSONB under key `correlated_alerts` (slice of `{alert_name, target_host, at, confidence, reasoning}`), increment `correlated_count` field, write one `AlertCorrelationLog` row to DB
- [ ] Update any mock implementations of `IncidentManager` in `internal/testhelpers/` to add the stub method
- [ ] Write unit tests for `AppendCorrelatedAlert`: verifies JSONB append, count increment, and audit row creation
- [ ] Run `make test` — must pass before Task 4

### Task 4: AlertHandler wiring

**Files:**
- Modify: `internal/handlers/alert.go`
- Modify: `internal/handlers/alert_processor.go`

- [ ] Add `alertCorrelator *services.AlertCorrelator` field and `spawnGroup singleflight.Group` field to `AlertHandler` struct in `alert.go`
- [ ] Add `SetAlertCorrelator(c *services.AlertCorrelator)` setter (nil-safe)
- [ ] Add private `correlate(ctx, sourceUUID string, alert alerts.NormalizedAlert) (services.CorrelationVerdict, error)` helper that delegates to `h.alertCorrelator` when non-nil, else returns `{Correlated: false}`
- [ ] Add private `recordRecurrence(ctx, incidentUUID string, alert alerts.NormalizedAlert, verdict services.CorrelationVerdict)` helper that calls `h.skillService.AppendCorrelatedAlert`; logs on error but does not propagate
- [ ] In `alert_processor.go`, wrap the evaluate-and-spawn block in both `processAlert` and `ProcessAlertFromListenerChannel` with `h.spawnGroup.Do(key, ...)` where `key = sha256hex(sourceUUID + "|" + alert.AlertName + "|" + alert.TargetHost)`
- [ ] Inside the singleflight func, add the correlate-before-spawn gate: call `h.correlate`; if `verdict.IsConfident(threshold)`, call `h.recordRecurrence` and return (no spawn); otherwise fall through to the existing `SpawnIncidentManager` + `runInvestigation` path
- [ ] Singleflight followers (non-leaders): extract the returned incidentUUID from the leader result and call `h.recordRecurrence` on the same incident
- [ ] Write handler tests: 15 concurrent same-key alerts → 1 spawn + 14 recurrences; confident verdict → no spawn + AppendCorrelatedAlert called; below-threshold verdict → spawn; worker disconnected → spawn (fail-open)
- [ ] Run `make test` — must pass before Task 5

### Task 5: main.go construction and injection

**Files:**
- Modify: `cmd/akmatori/main.go`

- [ ] After the `agentWSHandler` is constructed, read `CorrelationConfig` from `database.GetOrCreateGeneralSettings()` (with nil-safe defaults: window 30m, threshold 0.7, maxCandidates 20, enabled false)
- [ ] Construct `services.NewAlertCorrelator(agentWSHandler, database.DB, cfg)`
- [ ] Wire via `alertHandler.SetAlertCorrelator(correlator)` alongside the other `Set*` calls
- [ ] Run `make test` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [ ] Run `make test` (Go backend full suite)
- [ ] Run `make test-adapters`
- [ ] Confirm flag-off behavior: with `AlertCorrelationEnabled=false`, no LLM calls made in any alert path
- [ ] Confirm fail-open: when `SetAlertCorrelator` is never called (nil), both `processAlert` paths behave identically to today
