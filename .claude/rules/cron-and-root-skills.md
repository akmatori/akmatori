---
paths:
  - "**/internal/services/cron_runner.go"
  - "**/internal/services/cron_agent_prompt*"
  - "**/internal/services/incident_service.go"
  - "**/internal/services/skill_*"
  - "**/internal/handlers/api_cron_jobs.go"
  - "**/internal/database/models_cron*"
  - "**/web/src/components/cron/**"
---

# Cron jobs

Cron jobs run on a per-job schedule and always execute as a full agent investigation under the
`cron-agent` system skill. The legacy `oneshot` mode and `description` field are gone.

- every cron tick spawns the `cron-agent` skill via `SpawnAgentInvocation`, creating an `Incident`
  with `source_kind="cron"` and `source_uuid=<cron_job.uuid>`
- each cron carries its own `Tools []ToolInstance` allowlist (m2m via `cron_job_tools`) mapped to
  `ToolAllowlistEntry` — global skill/tool settings are NOT inherited
- `cron-agent` is a system skill (`IsSystem=true`), exempt from SKILL.md generation; prompt surfaces
  via `skill_prompt_service`
- system crons seed via `seedSystemCronJobs()` with per-seed `Enabled` defaults (`Dreaming` enabled,
  `improvement-evaluator` disabled); existing system rows are left untouched (operator edits survive
  restarts); legacy `memory-curator` rows rename to `Dreaming` in place; `DeleteJob` on a system row
  returns `ErrSystemCronImmutable` (409) — editable, not deletable
- `post_results` (default true) gates channel posting; when false the tick skips channel/provider
  resolution entirely and records its outcome only on the Incident row
- crons spawn with ONLY the `cron-agent` root prompt — do NOT wrap the task with
  `executor.PrependGuidance` (incident-triage framed); the task is prefixed only with the current UTC time
- tool-less crons (e.g. `Dreaming`) MUST send an explicit empty allowlist; `ToolAllowlist` on
  `WebSocketMessage` is intentionally NOT `omitempty` — `[]` means reject-all, `null` means allow-all
- the seeded `Dreaming` cron dedupes `/akmatori/memory/global/` via memory-writer tombstones
- cron expressions are validated at write time (invalid → 400); `CronRunner` survives tick failures,
  recording `LastRunStatus=error` + `LastRunError`
- manual fire is `POST /api/cron-jobs/{uuid}/run`; CRUD reloads the runner without restart
- `CronJobTool` is the explicit join-table model; include it alongside `CronJob` in all `AutoMigrate`
  calls and test schemas — GORM does not auto-discover it from the `many2many:` tag

# Root-skill agent runs

`SpawnAgentInvocation(rootSkillName, ctx)` in `incident_service.go` is the shared entrypoint for
root-skill agent runs. New system root skill = seed the skill row in `db.go`, add its prompt constant
alongside `DefaultCronAgentPrompt`, add the `rootSkillName` case to `GetSkillPrompt` /
`UpdateSkillPrompt` / `RegenerateSkillMd` / `RegenerateAllSkillMds` / `rootSkillHeader`.
Current root skills: `incident-manager`, `cron-agent`, `proposal-editor`.

Key files: `internal/services/cron_runner.go` (scheduler, per-cron agent tick, reload-on-CRUD),
`internal/services/incident_service.go` (agent spawning, AGENTS.md generation, root-skill prompts),
`internal/handlers/api_cron_jobs.go`, `web/src/components/cron/` (form/list helpers incl. `post_results`)
