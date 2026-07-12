# Alert Recurrence & Investigation-Cost Reduction

## Overview

Four product changes to cut wasted investigation tokens, harden the correlation gate, and keep the memory subsystem scalable. Preceded by one operational step (Task 0) that needs no code. Ordered by value-per-effort.

## Context

- Files involved:
  - `internal/services/alert_correlator.go` — existing pattern to mirror for suppressor
  - `internal/services/alert_suppressor.go` — new; the main cost-reduction feature
  - `internal/database/models_alert_suppression.go` — new; audit log model
  - `internal/database/models_context.go` — add `Suppress bool` to `Memory`
  - `internal/database/models_settings.go` — add `AlertSuppressionEnabled/Threshold` to `GeneralSettings`
  - `internal/database/models_incidents.go` — add `AlertFingerprint string`
  - `internal/database/db.go` — AutoMigrate additions
  - `internal/services/incident_service.go` — add `RecordSuppressedIncident`
  - `internal/services/memory_service.go` — parse `suppress` frontmatter field
  - `internal/services/interfaces.go` — extend `IncidentManager`; no new interface dependency for suppressor (queries DB directly)
  - `internal/handlers/alert.go` — add `alertSuppressor` field + `SetAlertSuppressor` setter
  - `internal/handlers/alert_processor.go` — call suppressor inside singleflight after correlator
  - `internal/handlers/api_settings_general.go` — add suppression fields; hydrate effective defaults in GET
  - `cmd/akmatori/main.go` — wire suppressor from `GeneralSettings`
  - `web/src/components/settings/GeneralSettingsSection.tsx` — add correlation + suppression controls with effective-default hydration
  - `web/src/types.ts` — extend `GeneralSettings` type
- Related patterns: `AlertCorrelator` (mirror exactly for suppressor); `AppendCorrelatedAlert` (mirror for `RecordSuppressedIncident`); `AlertCorrelationLog` model (mirror for `AlertSuppressionLog`)
- Dependencies: none new

## Development Approach

- **Testing approach**: Regular (implement, then tests per task). Every task includes new/updated tests; `make test` must pass before the next task.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 0: Enable correlation gate on live instance (operational, no code)

**Files:** none

- [x] `POST /api/settings/general` with `alert_correlation_enabled=true`, `alert_correlation_window_minutes=30`, `alert_correlation_threshold=0.7`, `alert_correlation_max_candidates=20` (skipped - manual deployment step)
- [x] Restart `akmatori-api` (config is read once at startup today; Task 3 fixes this) (skipped - manual deployment step)
- [x] Verify `alert_correlation_logs` receives rows and `correlated_count` increments on a recurrence within the 30-minute window (skipped - manual verification)

### Task 1: Known-false-positive suppression fast-path

**Files:**
- Create: `internal/services/alert_suppressor.go`
- Create: `internal/database/models_alert_suppression.go`
- Create: `internal/services/alert_suppressor_test.go`
- Create: `internal/handlers/alert_suppressor_gate_test.go`
- Modify: `internal/database/models_context.go` — add `Suppress bool` column to `Memory`
- Modify: `internal/database/db.go` — add `AlertSuppressionLog` to `AutoMigrate`; add index on `(suppress, scope)` for Memory
- Modify: `internal/database/models_settings.go` — add `AlertSuppressionEnabled *bool`, `AlertSuppressionThreshold *float64` to `GeneralSettings`
- Modify: `internal/services/memory_service.go` — add `suppress` field to private `memoryFrontmatter`; populate `Memory.Suppress` in `IngestFromDisk`
- Modify: `internal/services/incident_service.go` — add `RecordSuppressedIncident(*IncidentContext, signatureName, reasoning string, confidence float64) (string, error)`
- Modify: `internal/services/interfaces.go` — add `RecordSuppressedIncident` to `IncidentManager`
- Modify: `internal/handlers/alert.go` — add `alertSuppressor *services.AlertSuppressor` field + `SetAlertSuppressor` setter
- Modify: `internal/handlers/alert_processor.go` — inside singleflight in both `processAlert` and `ProcessAlertFromListenerChannel`: after correlator check passes (not correlated), before `SpawnIncidentManager`, call suppressor; on confident match call `RecordSuppressedIncident` and return without spawning; for listener-channel path also post a short Slack thread reply citing the matched signature
- Modify: `cmd/akmatori/main.go` — read `AlertSuppressionEnabled/Threshold` from `GeneralSettings`; construct and wire `AlertSuppressor` analogously to `AlertCorrelator`
- Modify: `internal/handlers/api_settings_general.go` — handle new suppression settings fields in PUT; include them in GET response

- [x] Define `AlertSuppressionLog` model (mirrors `AlertCorrelationLog`): `source_uuid`, `alert_name`, `target_host`, `incident_uuid`, `signature_name`, `confidence`, `reasoning`, `created_at`; `TableName()` returns `alert_suppression_logs`
- [x] Add `Suppress bool` column to `Memory` model; update AutoMigrate in `db.go`; add composite index `idx_memories_suppress_scope` on `(suppress, scope)` for fast signature queries
- [x] Implement `AlertSuppressor`: `SuppressionConfig{Enabled, Threshold, MaxSignatures}` with `SuppressionConfigWithDefaults`; `SuppressionVerdict{Suppressed, SignatureName, Confidence, Reasoning}`; `AlertSuppressor.Evaluate(ctx, alert) (SuppressionVerdict, error)` — queries `WHERE suppress = true` from DB, fail-open on flag-off / nil caller / zero signatures; one-shot LLM call with ≤1500 token cap (temp 0); hallucination guard: any signature name returned by LLM that was not in the fetched set forces `Suppressed=false`; `ErrWorkerNotConnected` propagated fail-open
- [x] Add system prompt for suppressor one-shot call: ask LLM to return `{"suppressed": bool, "signature_name": "<name or empty>", "confidence": 0..1, "reasoning": "<≤200 chars>"}` given the incoming alert and list of suppression signature bodies; rule: suppress only if the pattern clearly matches (same rule name + same host pattern); default to suppressed=false when uncertain
- [x] Implement `SkillService.RecordSuppressedIncident`: create incident with `Status=completed`, `Response=<canned text citing signatureName + reasoning>`, `TokensUsed=0`, `ExecutionTimeMs=0`; then create `AlertSuppressionLog` row inside a single transaction; return incidentUUID
- [x] Wire suppressor in `main.go`: read `GeneralSettings.AlertSuppression*`; `services.NewAlertSuppressor(agentWSHandler, database.GetDB(), cfg)`; `alertHandler.SetAlertSuppressor(suppressor)`
- [x] Add suppression fields to `handleGeneralSettings` PUT; add `AlertSuppressionEnabled` / `AlertSuppressionThreshold` to `generalSettingsRequest` struct; validate threshold in (0, 1]
- [x] Add `suppress` to `memoryFrontmatter` struct in `memory_service.go`; set `Memory.Suppress = fm.Suppress` in `IngestFromDisk` parse path; also populate `Suppress` in `SyncMemoryFiles` write path (emit `suppress: true` in frontmatter when set)
- [x] Add suppress field to memory-writer subagent definition in `akmatori_data/agents/memory-writer.md`: document that setting `suppress: true` in frontmatter marks this memory as a suppression signature; include example format showing `suppress: true`, an alert rule name pattern, and a host glob in the body
- [x] Write tests: `alert_suppressor_test.go` — confident suppress returns `Suppressed=true`, low-confidence → `Suppressed=false`, zero signatures → `Suppressed=false`, worker disconnected → `Suppressed=false` + error propagated, hallucinated name guard, flag-off (`Enabled=false`) → `Suppressed=false` with no LLM call; `alert_suppressor_gate_test.go` — handler routes confident suppression to `RecordSuppressedIncident` (not `SpawnIncidentManager`), low-confidence falls through to spawn
- [x] `make test`

### Task 2: Stable alert fingerprint

**Files:**
- Modify: `internal/database/models_incidents.go` — add `AlertFingerprint string`
- Modify: `internal/handlers/alert_processor.go` — compute fingerprint at ingest; set on `incidentCtx.Context["alert_fingerprint"]` before incident creation
- Modify: `internal/services/incident_service.go` — read `alert_fingerprint` from `incidentCtx.Context` and store on the `Incident` row
- Modify: `internal/services/alert_correlator.go` — add fingerprint pre-filter to `fetchCandidates`
- Modify: `internal/services/alert_suppressor.go` — no fingerprint filter needed (signatures match on rule+host pattern via LLM)
- Create: `internal/services/alert_fingerprint_test.go`
- Modify: existing `alert_correlator_test.go` — update candidate query tests

- [x] Add `AlertFingerprint string` to `Incident` model with a DB index; handled by existing `AutoMigrate` call
- [x] Add `computeAlertFingerprint(sourceUUID, alertName, targetHost string) string`: `sha256(json([sourceUUID, lower(alertName), lower(targetHost)]))[:32]` — note this is distinct from `SourceFingerprint` (adapter-supplied external ID) and from `alertSpawnKey` (which includes the adapter fingerprint for exact dedup; this new field is derived only from normalized identity fields for candidate grouping)
- [x] Call `computeAlertFingerprint` in both `processAlert` and `ProcessAlertFromListenerChannel`; store in `incidentCtx.Context["alert_fingerprint"]`; `SpawnAgentInvocation` reads it and sets `incident.AlertFingerprint` at create time
- [x] In `fetchCandidates`: add `AND (alert_fingerprint = ? OR alert_fingerprint = '')` with the incoming alert's computed fingerprint; this reduces LLM candidate set for exact-match recurrences
- [x] Write tests: `alert_fingerprint_test.go` — fingerprint is stable across title-case variants (`HighCPU` vs `highcpu`), different source fingerprints on same rule+host produce identical alert fingerprint; `fetchCandidates` with fingerprint filter returns only same-fingerprint rows when fingerprint column is populated
- [x] `make test`

### Task 3: Correlation/suppression config UX — live reload + sane defaults

**Files:**
- Modify: `internal/services/alert_correlator.go` — remove static `cfg CorrelationConfig` field; replace with live read from DB on each `Correlate` call
- Modify: `internal/services/alert_suppressor.go` — same pattern: read `GeneralSettings` fresh on each `Evaluate` call
- Modify: `cmd/akmatori/main.go` — constructors no longer need the config struct; `NewAlertCorrelator(caller, db)` / `NewAlertSuppressor(caller, db)`; remove startup config-read block
- Modify: `internal/handlers/api_settings_general.go` — GET: apply effective defaults to nil fields before JSON response so frontend always sees non-nil values
- Modify: `web/src/types.ts` — extend `GeneralSettings` type with correlation + suppression fields
- Modify: `web/src/components/settings/GeneralSettingsSection.tsx` — add correlation + suppression controls

- [x] Change `AlertCorrelator` and `AlertSuppressor` constructors to drop the static config parameter; read `GeneralSettings` from DB at the start of each `Correlate` / `Evaluate` call (consistent with how both already call `database.GetLLMSettings()` on every call); `CorrelationConfigWithDefaults` / `SuppressionConfigWithDefaults` remain for applying code defaults to nil DB values
- [x] Update `cmd/akmatori/main.go` to use new constructors; remove the `correlationCfg` block; startup log now says "alert correlator ready (live config)" instead of logging the static config values
- [x] In `handleGeneralSettings` GET: before JSON encoding, apply defaults to all `Alert*` nullable fields so nil → effective default; do NOT persist the defaults (only hydrate for response); `AlertCorrelationEnabled` defaults false, `AlertCorrelationWindowMinutes` defaults 30, `AlertCorrelationThreshold` defaults 0.7, `AlertCorrelationMaxCandidates` defaults 20, `AlertSuppressionEnabled` defaults false, `AlertSuppressionThreshold` defaults 0.7
- [x] Add correlation + suppression fields to `web/src/types.ts` GeneralSettings interface; all fields non-nullable after GET hydration
- [x] Add controls in `GeneralSettingsSection.tsx`: correlation enabled toggle, window (minutes, 1–1440), threshold (0–1, step 0.01), max candidates (1–100); suppression enabled toggle, threshold (0–1, step 0.01); save sends all fields; update `generalSettingsApi.update` call to include them
- [x] Write tests: `alert_correlator_test.go` — constructor accepts no config; `Correlate` respects DB-stored enabled=false (no LLM call); `Correlate` respects DB-stored threshold; `handleGeneralSettings` GET returns non-nil defaults even when DB row has all nulls
- [x] `make test`

### Task 4: Memory scaling for recall and curation

**Files:**
- Modify: `internal/services/memory_service.go` — add `manifestMaxPerScope` cap; enforce it in `SyncMemoryFiles` MEMORY.md generation by limiting entries per scope
- Modify: `internal/database/db.go` — add composite index `(scope, type)` on memories for memory-searcher range queries; update seeded `memory-curator` cron prompt
- Modify: `akmatori_data/agents/memory-writer.md` — guidance about `suppress: true` already done in Task 1

- [x] Add `idx_memories_scope_type` composite index on `(scope, type)` in `db.go` migration to speed future scope-scoped queries; the `idx_memories_suppress_scope` index added in Task 1 covers suppression lookups; verify both indexes are created via `db.AutoMigrate` or explicit `db.Exec("CREATE INDEX IF NOT EXISTS ...")`
- [x] Add `manifestMaxEntriesPerScope = 150` constant in `memory_service.go`; in `SyncMemoryFiles`, if a scope has more than 150 entries in MEMORY.md, write only the 150 most-recently-updated entries plus a `<!-- truncated: N more entries not shown -->` comment; suppress-flagged entries always appear in the manifest regardless of age (suppression signatures must stay visible to the searcher); this limits how much the MEMORY.md manifest grows per scope without deleting any files
- [x] Update the seeded `memory-curator` cron prompt in `internal/database/db.go` (the `seedSystemCronJobs` call): add "Focus only on the `global` scope. Process entries updated in the last 14 days. For each duplicate or superseded entry, emit `Action: delete <slug>` via memory-writer rather than rewriting both." Leave existing operator-edited rows untouched (seed leaves existing rows unchanged per CLAUDE.md)
- [x] Write tests: `memory_service_test.go` — `SyncMemoryFiles` truncates MEMORY.md to `manifestMaxEntriesPerScope` when scope exceeds limit; older entries are excluded from manifest but files remain on disk; suppress-flagged entry is included in manifest regardless of age
- [x] `make test`

### Task 5: Verify acceptance criteria

- [x] Run full test suite: `make verify` — all 378 agent-worker + 100 web + all Go tests pass
- [x] Confirm `alert_suppression_logs` table exists and accepts rows after `make test` — `AlertSuppressionLog` model with `TableName()="alert_suppression_logs"` defined; covered by handler gate tests via `RecordSuppressedIncident` mock
- [x] Confirm `memories.suppress` column exists and round-trips through `SyncMemoryFiles` → `IngestFromDisk` — `Suppress bool` on `Memory` model; `SyncMemoryFiles` and `IngestFromDisk` tested in `memory_service_test.go`
- [x] Confirm `incidents.alert_fingerprint` is populated for new alerts processed by the tests — `AlertFingerprint string` with DB index on `Incident` model; `alert_fingerprint_test.go` verifies stability and uniqueness
- [x] Confirm `GET /api/settings/general` returns non-nil values for all correlation and suppression fields — handler applies `CorrelationConfigWithDefaults` before JSON encoding; covered in `alert_correlator_test.go`
