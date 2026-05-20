# Cron Jobs Redesign — agent-only, per-cron tools, system crons for memory dreaming

## Overview

Redesign cron jobs into a single agent-based execution path that mirrors how incidents run, but using a new `cron-agent` system skill instead of `incident-manager`. Each cron job declares its own tool allowlist (separate from the global one used by incidents), can read/write/delete cross-incident memory, and may be flagged as a non-deletable system job so first-run can seed dreaming-style maintenance crons. The `description` and `mode` columns are removed entirely (oneshot mode is dropped).

## Context

- Files involved:
  - `internal/database/models_cron.go` — CronJob model (drop Description/Mode, add IsSystem, add Tools m2m)
  - `internal/database/db.go` — AutoMigrate + `InitializeSystemSkill` (add `InitializeCronAgentSkill` + system-cron seeding)
  - `internal/services/cron_runner.go` — drop oneshot path, switch agent path to cron-agent, accept per-cron tool allowlist, enforce IsSystem in CRUD
  - `internal/services/cron_runner_test.go` — update fakes + tests
  - `internal/services/incident_service.go` — extract AGENTS.md generation so cron-agent runs reuse it with a different root prompt
  - `internal/handlers/api_cron_jobs.go` — request/response shapes (no description/mode; add tool_instance_uuids + is_system)
  - `akmatori_data/agents/memory-writer.md` — extend contract with `Action: delete`
  - `web/src/components/cron/CronJobForm.tsx`, `web/src/components/cron/cronJobHelpers.ts`, `web/src/components/cron/CronJobsManager.tsx`, `web/src/types/index.ts` — form/listing/type changes
- Related patterns:
  - `internal/database/db.go:541` `InitializeSystemSkill` — pattern for cron-agent skill bootstrap
  - `internal/services/skill_service.go:239` `GetToolAllowlist` — pattern for deriving allowlist; per-cron version reuses `ToolAllowlistEntry` shape
  - `internal/handlers/alert_processor.go:442` `agentWSHandler.StartIncident(... skills, toolAllowlist ...)` — pattern that cron path will follow
  - `internal/services/cron_runner.go:373` `executeAgent` — main code path to refactor
- Dependencies: no new third-party deps. Reuses existing `pi-subagents`, the messaging registry, and the agent worker.

## Development Approach

- Testing approach: Regular (code first, then tests)
- Complete each task fully (code + tests + green suite) before moving on
- Drop oneshot mode and the Description field cleanly — no backwards-compat shims (per CLAUDE.md / project conventions)
- Existing `cron_jobs` rows in mode=oneshot are migrated to mode=agent by the schema migration (then the Mode column is dropped)
- Operator-facing UI uses the existing ToolPicker pattern from skills; no new shared components
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting the next task

## Implementation Steps

### Task 1: Cron job schema — drop description/mode, add is_system, add per-cron tool allowlist

Files:
- Modify: `internal/database/models_cron.go`
- Modify: `internal/database/db.go` (AutoMigrate hook + raw SQL migration helper)
- Modify: `internal/database/models_cron_test.go` if present, or add one alongside

- [x] remove `Description` and `Mode` fields from `CronJob`; remove `CronJobMode`, `CronJobModeOneshot`, `CronJobModeAgent`, `IsValidCronJobMode` constants
- [x] add `IsSystem bool` column to `CronJob` (default false; gormtag `default:false`)
- [x] add many-to-many relation `Tools []ToolInstance` via new `cron_job_tools` join table (mirror `skill_tools` shape: cron_job_id + tool_instance_id, both primary key)
- [x] write a one-shot migration step in `db.go` that runs BEFORE AutoMigrate's column drop: set all rows with `mode != 'agent'` to `mode=agent` (in case anyone had oneshot), then drop columns `description` and `mode` from `cron_jobs` if they exist
- [x] write unit test that confirms `CronJob` round-trips through GORM with `IsSystem` + `Tools` populated
- [x] run `make test` — must pass before Task 2

### Task 2: cron-agent system skill + system cron seed

Files:
- Modify: `internal/database/db.go` — add `DefaultCronAgentPrompt` constant + `InitializeCronAgentSkill()` + `seedSystemCronJobs()`; call both from `InitializeSchema`
- Modify: `internal/services/skill_service.go` / `skill_file_sync.go` — extend `incident-manager` system-skill carve-outs to also include `cron-agent` (no SKILL.md generation for it, similar exemptions)
- Modify: `internal/services/skill_prompt_service.go` — return `DefaultCronAgentPrompt` for `cron-agent` like incident-manager does
- Create: `internal/services/cron_agent_prompt_test.go` — pin prompt content (delegates to memory-searcher; mentions memory-writer for upsert/delete; no Slack-thread-specific framing)

- [x] write `DefaultCronAgentPrompt` (workflow: orient → optional runbook search → memory recall → use allowed tools → optionally write/dedupe memory → produce final summary; no "incident triage" framing)
- [x] `InitializeCronAgentSkill` upserts a skill row `{Name: "cron-agent", IsSystem: true, Enabled: true}` mirroring the incident-manager bootstrap
- [x] add `cron-agent` to every place that today special-cases `"incident-manager"` (skill_prompt_service, skill_file_sync, skill_service `GetEnabledSkillNames`/`GetToolAllowlist` is-system filter is already correct)
- [x] `seedSystemCronJobs()` upserts one row: name `memory-curator`, IsSystem=true, Enabled=false (operator opts in), schedule `0 2 * * *`, prompt that instructs the cron-agent to dedupe and consolidate `/akmatori/memory/global/` entries via memory-writer (upsert merged, delete duplicates)
- [x] tests: pin the cron-agent prompt's required directives, assert the memory-curator row is idempotently re-seeded on a second `InitializeSchema` call, assert it survives an operator disable across restarts
- [x] run `make test` — must pass before Task 3 (touched-area tests green; pre-existing TestAlertService_InitializeDefaultSourceTypes_IdempotentAndUpdates + TestAPIHandler_HandleAlertSources_CreateValidationAndConflict failures are unrelated and pre-date this task)

### Task 3: CronRunner — single agent path, per-cron tool allowlist, system-cron CRUD guards

Files:
- Modify: `internal/services/cron_runner.go`
- Modify: `internal/services/cron_runner_test.go`
- Modify: `internal/services/interfaces.go` — update `CronJobManager` signature (drop description from CreateJob, accept tool instance UUIDs; same for `CronJobUpdate`)
- Modify: `internal/services/incident_service.go` — factor a shared `generateAgentsMd(path, rootSkillName, incidentUUID)` so the cron-agent path injects its own root skill

- [x] delete `executeOneshot`, `callOneshot`, `formatCronOneshotMessage`, `cronOneshotTimeout` constants; rename `executeAgent` → `execute`
- [x] in `execute`, swap `SpawnIncidentManager` → new `SpawnAgentInvocation(rootSkillName="cron-agent", ctx)` so the AGENTS.md root prompt is cron-agent's instead of incident-manager's; keep `source_kind="cron"` + `source_uuid=<cron.uuid>`
- [x] replace `r.skills.GetToolAllowlist()` with a per-cron lookup that reads `job.Tools` (preloaded via `Preload("Tools.ToolType")`) and maps each enabled `ToolInstance` to `ToolAllowlistEntry{InstanceID, LogicalName, ToolType}`
- [x] introduce `ErrSystemCronImmutable` returned from `DeleteJob` when `job.IsSystem`
- [x] update `CreateJob` / `UpdateJob` signatures: remove `description` param, accept `toolInstanceIDs []uint` (tool instances are addressed by integer ID in this codebase — the plan's "UUID" naming was a slip; HTTP-level identifier shape is Task 4); replace `job.Tools` via `Association("Tools").Replace(...)`; operator-created jobs always have IsSystem=false
- [x] tests:
  - oneshot mode is gone (no `executeOneshot`)
  - tick uses cron-agent skill name + per-cron tool allowlist (not the global one)
  - `DeleteJob` of a system row returns `ErrSystemCronImmutable`; `UpdateJob` of a system row updating only `Enabled` succeeds
  - per-cron tools flow into `StartIncident`'s `toolAllowlist` argument
- [x] run `make test` — must pass before Task 4 (cron + spawn tests green; pre-existing TestAlertService_InitializeDefaultSourceTypes_IdempotentAndUpdates + TestAPIHandler_HandleAlertSources_CreateValidationAndConflict failures remain unrelated)

### Task 4: HTTP API — drop description/mode, accept tool_instance_uuids, expose is_system, block system delete

Files:
- Modify: `internal/handlers/api_cron_jobs.go`
- Modify: `internal/handlers/api_cron_jobs_test.go` if present (add otherwise)

- [x] remove `Description` and `Mode` from `cronJobResponse`, `CreateCronJobRequest`, `UpdateCronJobRequest` (already absent from prior tasks; verified by new TestCronJobResponse_OmitsLegacyFields + TestHandleCronJobs_Create_RejectsLegacyModeAndDescription)
- [x] add `IsSystem bool` and `Tools []toolInstanceSummary` (id/name/logical_name/tool_type/enabled) to `cronJobResponse` (ToolInstance has no UUID column in this codebase; summary uses id/name to match the existing handler convention)
- [x] add `ToolInstanceIDs []uint` to both request bodies (omitempty; pointer-slice on update for "leave alone" vs "clear" distinction); request shape uses `tool_instance_ids` to match the existing `/api/skills/:name/tools` request body (Task 3 noted the plan's "UUID" naming was a slip)
- [x] thread the new params through `services.CronJobUpdate` and the create call
- [x] map `ErrSystemCronImmutable` → 409 in `cronErrStatus`
- [x] tests: create job with tools → response carries them; update job swaps tools; PUT without `tool_instance_ids` leaves the patch nil (untouched); DELETE on system cron returns 409; create body that sets legacy `mode`/`description` is REJECTED with 400 (api.DecodeJSON uses DisallowUnknownFields — stricter than the plan's "ignored" assumption, but a cleaner contract); response shape never echoes legacy fields
- [x] run `make test` — touched-area tests green; pre-existing TestAlertService_InitializeDefaultSourceTypes_IdempotentAndUpdates + TestAPIHandler_HandleAlertSources_CreateValidationAndConflict failures remain unrelated to this task

### Task 5: memory-writer subagent — support deletion

Files:
- Modify: `akmatori_data/agents/memory-writer.md`
- Modify: `internal/services/memory_service.go` and/or `memory_service_ingest_test.go` to handle empty/tombstone files if needed
- Modify: existing memory-writer subagent tests under `internal/services/`

- [x] extend the subagent contract: `memory-writer.md` now documents an optional `Action: delete <slug>` line in the task body; the subagent emits a tombstone at `<scope>/<slug>.md` containing only `name:` + `deleted: true` frontmatter (used `write` rather than `edit` because the tombstone is a fresh file at a slot the prior sync had already purged — the plan's "edit" suggestion was a tool-name slip, the semantics match)
- [x] update `MemoryService.IngestFromDisk` to recognize `deleted: true` in frontmatter and `DELETE` the corresponding row + remove both the bare and canonical file once the DB row is gone (parser returns a tombstone flag; ingest dedup makes tombstones always win against a sibling canonical snapshot; the post-batch SyncMemoryFiles purges both the bare tombstone and the prior `<id>-<slug>.md` because neither is in expectedFiles)
- [x] tests: round-trip — write a memory, then have memory-writer write a tombstone, run IngestFromDisk, assert DB row is gone and both files cleaned (TestIngestFromDisk_TombstoneDeletesRowAndFiles + TestIngestFromDisk_TombstoneAndCanonicalSameScope + TestIngestFromDisk_TombstoneForUnknownSlugIsNoOp + TestParseMemoryFile_Tombstone* + subagent_files_test now asserts the prompt contains "Action: delete" and "deleted: true")
- [x] run `make test` — touched-area tests green; pre-existing TestAlertService_InitializeDefaultSourceTypes_IdempotentAndUpdates + TestAPIHandler_HandleAlertSources_CreateValidationAndConflict failures remain unrelated to this task

### Task 6: Frontend — CronJobForm/list updates

Files:
- Modify: `web/src/types/index.ts` — `CronJob` type (drop `description`, `mode`; add `is_system`, `tools[]`)
- Modify: `web/src/components/cron/cronJobHelpers.ts` — drop `MODE_OPTIONS`, drop description from form state
- Modify: `web/src/components/cron/CronJobForm.tsx` — remove Description field + Mode radio block; add tool-instance multi-select (reuse the picker pattern from `web/src/components/tools/`); pass `tool_instance_uuids` on submit
- Modify: `web/src/components/cron/CronJobsManager.tsx` — show "System" pill on rows with `is_system`; hide the Delete button (or render disabled with tooltip) when system
- Modify: tests under `web/src/components/cron/cronJobHelpers.test.ts`

- [ ] update types + helpers (drop mode/description)
- [ ] add tool picker to form (multi-select over `/api/tools/instances`)
- [ ] hide delete + add system badge in manager
- [ ] update / add component tests (helpers test asserts mode/description gone; submit payload includes `tool_instance_uuids`)
- [ ] run `make test-web` — must pass before Task 7

### Task 7: Verify acceptance criteria + docs

Files:
- Modify: `CLAUDE.md` — update the "Cron jobs" rules block (drop oneshot/agent split, drop description, mention per-cron tool allowlist + system crons + cron-agent skill)
- Modify: `docs/` OpenAPI if it covers `/api/cron-jobs`

- [ ] run `make verify`
- [ ] run `go test -coverprofile=coverage.out ./...` and confirm coverage in touched packages stays ≥ 80% (record in PR)
- [ ] update CLAUDE.md cron rules
- [ ] update OpenAPI spec for `/api/cron-jobs` request/response shape

## Post-Completion (out-of-scope notes for the implementer)

- Implementing the full OpenClaw scoring/promotion/cosine-dedupe pipeline is NOT part of this plan; the cron-agent + memory-curator system cron + memory-writer deletion give the operator a working dreaming surface that can be enriched later (REM/deep phases as additional seeded system crons, scoring logic as a dedicated service).
- Manual UI test of the redesigned cron form is required before declaring the task complete: create a non-system cron, attach tools, fire `RunNow`, confirm the agent worker uses the per-cron allowlist; enable the seeded `memory-curator` cron, confirm it cannot be deleted.
