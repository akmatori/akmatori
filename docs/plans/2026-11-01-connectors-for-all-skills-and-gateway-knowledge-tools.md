# Connectors for all skills, and gateway knowledge tools instead of subagents

Status: Part A merged 2026-11-02 (PR #60). B1 implemented 2026-11-02 (branch `feat/gateway-knowledge-tools`). B2 open.
Prepared 2026-11-01 against `main` at 6fe75f0 (Connectors, PR #58/#59).
Revision 2, same day, after the Codex review in
`2026-11-01-connectors-for-all-skills-and-gateway-knowledge-tools-codex-review.md`. Every review item
tagged [high] is addressed below; the changes are marked **(rev 2)**.

## Overview

Two changes, shipped as three PRs:

- **A. Every connected connector is available to every skill.** Drop the `skill_tools` and
  `cron_job_tools` mappings. The per-run allowlist is defined by a written authorization policy (A0):
  all enabled *connector* tools plus a fixed set of *built-in* capabilities per run kind. Skills stop
  carrying an "Assigned Tools" section; one generated system skill, `connected-tools`, lists every
  tool once. Cron jobs keep two fields: `tools_enabled` (connector tools on or off) and
  `builtin_tools` (which built-ins), so maintenance crons keep their narrow set.
- **B1. Replace the three pi-subagents with built-in gateway tools.** `runbooks.search` / `runbooks.get`
  and `memory.search` / `memory.list` / `memory.get` / `memory.write` / `memory.delete` run in the MCP
  Gateway against the database, with no LLM in the loop. The memory manifest is rendered from the
  database. The agent worker drops `pi-subagents`, the agent definitions, the subagent config and the
  subagent model override. The disk mirrors stay, read-only for the agent, as a transition.
- **B2. Remove the disk mirrors.** The DB → disk sync, the disk → DB ingest, tombstones and the on-disk
  `MEMORY.md` go, and memory references stop pointing at files.

### Why

- The skill ↔ tool mapping does not isolate skills from each other. `SkillService.GetToolAllowlist`
  (`internal/services/skill_service.go:256`) unions the tools of every enabled skill into one
  allowlist for every incident run, so any skill can already call any assigned tool. The mapping
  does decide the incident-wide set of authorized instances **(rev 2: this is an authorization
  expansion, not a no-op; see A0)**, (a) picks which tools get a `gateway_call` example in each
  SKILL.md and (b) makes a connector that nobody assigned unreachable. (a) copies the same schema
  into every SKILL.md the agent reads, (b) causes "I connected it and the agent does not see it".
- The subagents are the least reliable part of the stack: every pi upgrade needed a subagent section
  (lazy `subagents_enable` loader, `asyncByDefault`, `bg_wait`, package root env), the `EACCES
  auth.json` bug ran the whole published SRE Bench leaderboard with broken runbook and memory search,
  compose-only installs missed `config.json` and the parent ended its turn early. Each incident pays
  three subprocess system prompts. The scoping they promise is prompt-only: the main agent has bash and
  the same mounts.
- A gateway tool keeps the benefit (search noise stays out of the main context), costs zero tokens per
  search, is unit-testable in Go, and works in the bench from day one.

### What we lose, and accept

- A small model that extracts keywords from the alert. The main agent does this with two or three
  `*.search` calls, which the prompts already ask for.
- The subagent model override (`subagent_model`, `subagent_thinking_level`). Searches no longer cost
  tokens, so the lever has no use.
- Semantic recall. Search is lexical (BM25 over name, description, body). Revisit embeddings only if
  the bench shows misses on real libraries.

### Not in scope

- Per-skill tool restriction as a security feature. If it ever returns, it must be enforced in the
  gateway per run, not through SKILL.md text.
- Native pi `tool_search` (see `docs/plans/2026-10-03-pi-followups-assessment.md` §5).

## Context

Facts checked in the tree (rev 2 corrections included):

- Allowlist callers: `GetToolAllowlist` is passed to `StartIncident` in `alert_processor.go:755,997`,
  `slack_processor.go:272` and `api_incidents.go:499`. Cron runs use `buildCronToolAllowlist(job.Tools)`
  (`cron_runner.go:685`) plus `withEventsTool`. Proposal chat uses `ProposalService.ChatToolAllowlist()`
  (`proposal_service.go:505`: incidents + proposals). The gateway picks the first authorized instance
  when the call has no instance hint (`mcp-gateway/internal/mcp/server.go:339`).
- Built-ins: `incidents`, `proposals`, `events` are credential-less (`auth/authorizer.go:19`), seeded
  as enabled instances by `EnsureToolTypes` (`tool_service.go:194,217`), hidden from the Connectors
  catalog (`connector_catalog.go:82`, `hiddenToolTypes`), and reserved on the API side in
  `reservedToolNamespaces` (`http_connector_service.go:10`, checked by `mcp_server_service.go:93`).
- Mapping surfaces: `database.SkillTool` (`models_skills.go:62`), `Skill.Tools`, `ToolInstance.Skills`,
  `SkillService.AssignTools` / `getSkillTools`, `GET/PUT /api/skills/{name}/tools` (`api_skills.go:186`),
  `regenerateSkillsForTools` (`api_mcp_servers.go:472`), `tool_service.go:136` and
  `mcp_server_service.go:521` clear join rows on delete, `testhelpers/builders.go:72`. Gateway mirrors:
  `Skill.Tools` (`mcp-gateway/internal/database/db.go:119`), `CronJob.Tools` and `CronJobTool`
  (`mcp-gateway/internal/database/models_proposals.go:65,81`), preloaded by the proposals tool
  (`tools/proposals/proposals.go:130,191,594`). Proposals carry `tool_logical_names` for cron jobs
  (`proposal_service.go:51,288,383,431`). UI: `Skills.tsx` "Tool Connections", `CronJobForm.tsx`,
  copy in `AddMCPConnectorModal.tsx:100`, `MCPServerPanel.tsx:235`, `ConnectorPartsPanel.tsx:90`,
  `ConnectorDetail.tsx:230`, `connector_catalog.go:77`.
- System crons: `SeedSystemCronJobs` and `SeedImprovementEvaluatorCron` (`database/db.go:1089-1251`)
  preserve existing rows (schedule, prompt, tools, enabled). The evaluator needs the `incidents` and
  `proposals` instances (`db.go:1237`). The Dreaming prompt (`db.go:1065`) and the evaluator prompt
  name `memory-searcher`, `memory-writer` and `/akmatori/memory/global/`; they are persisted in
  installed rows.
- SKILL.md generation: `generateSkillMd` (`skill_prompt_service.go:151`) appends "## Assigned Tools"
  with `extractToolDetails` + `generateToolUsageExample`; for MCP tool types the example only points at
  `list_tools_for_tool_type` (`:869`). Skill discovery: the worker filters pi's discovered skills
  against the DB enabled list (`agent-runner.ts:1498`, `skillsOverride`); `last_skill` is latched from
  the SKILL.md read path (`agent-runner.ts:1648`, `extractSkillNameFromReadPath`) and feeds formatting
  rules (`formatting_rule_matcher.go:91`). Filesystem import creates DB rows for unknown skill dirs
  (`skill_file_sync.go:267,301`). `gateway_call`'s own description tells the model to use each
  skill's assigned-tools list (`agent-worker/src/gateway-tools.ts:135,143`).
- Gateway built-in pattern: `incidents` and `events` tools, `registerEventsTools` (`registry.go:4835`),
  `builtInToolNamespaces` (`registry.go:238`), type-only allowlist entry added by the API
  (`withEventsTool`, `event_service.go:468`). `events.report_alert` writes rows with `database.DB`
  and keeps a per-scope counter keyed by `X-Incident-ID` (`events.go:235-249`). The worker sends only
  `X-Incident-ID` (`gateway-client.ts:356`); the run id is known to the worker from the frame
  (`agent_ws.go:133`). Gateway mirror models `Runbook` and `Memory` already exist
  (`models_proposals.go:36,49`); `ContextFile` does not.
- Memory model and rules: `Memory{Scope, Type, Name, Description, Body, IncidentUUID, CreatedBy,
  SourceFileID}`; `validate` (`memory_service.go:310`) trims, rejects empty description, enforces
  `MemoryNameMaxLen` 200, `MemoryDescriptionMaxLen` 500 **bytes**, `MemoryBodyMaxBytes` 8 KiB, and
  rejects unknown types (`NormalizeMemoryType` only folds the legacy names); file ingest maps unknown
  types to fact and cuts long descriptions with `draftDescription` (UTF-8-safe byte cut,
  `memory_drafter.go:153`). `UpsertByName` preserves `created_by` but replaces content
  (`memory_service.go:951`); proposal approval upserts with `created_by=operator` without updating
  authorship (`proposal_service.go:367`). Context-file summaries are `created_by=agent` with
  `source_file_id` set (`memory_source_files.go:18-56`) and call `SyncMemoryFiles` (`:40,69`).
  `ResolveRefs` returns `/akmatori/memory/<scope>/<name>.md` paths for memory links
  (`memory_refs.go:98-122`). `ExportZip` reads the database, not the directory
  (`memory_export.go:15`). `MemoryGenerationEnabled` is nullable, nil = on, checked at ingest time
  (`models_settings.go:316,355`, `memory_service.go:287,616`).
- Disk mirrors: `SyncMemoryFiles` / `SyncRunbookFiles` at start (`cmd/akmatori/main.go:160,183`) and
  on every write; `IngestFromDisk` in a goroutine from `SkillService.FinishRun`
  (`incident_service.go:792-818`, every run kind), `MemoryIngester` setter in `skill_service.go:26-55`;
  manifest `<memoryDir>/<scope>/MEMORY.md` read by `renderMemoryRecallSection`
  (`skill_prompt_service.go:335`), rendered with feedback-first ordering, 50 reserved feedback slots
  of 150, byte and line caps (`memory_service.go:1136-1191`). Operator memory writes regenerate skill
  files (`api_memories.go:395`); gateway writes would not.
- Prompts naming subagents: `DefaultIncidentManagerPrompt` (`database/db.go:770`, hardcoded),
  `DefaultProposalEditorPrompt` (`db.go:1035`), `PrependGuidance` (`executor.go:140`),
  `memoryRecallInstruction`, `memoryWriteInstructionTemplate`, `memoryGenerationOffInstruction`
  (`skill_prompt_service.go:205-315`), the two system cron prompts (persisted), the three agent files
  in `akmatori_data/agents/`, rules `.claude/rules/{memory-and-runbooks,agent-worker-pi-sdk,proposals,
  oneshot-llm}.md`, `skills/akmatori/references/knowledge.md:195,211`, `.gitignore:6`,
  `.dockerignore:6`.
- Worker: `pi-subagents` dependency; Dockerfile lines 78-98 (extension copy, agent `COPY`) and
  110-119 (`PI_SUBAGENTS_*` env) are subagent-only; lines 57-77 and 100-107 (dirs, deps, compiled
  code, `USER agent`) are not. In `agent-runner.ts`: `PARENT_EXCLUDED_TOOLS`, `logActiveToolSet`,
  `subagentAsyncConfigWarning`, child `settings.json` / `models.json` writers (`:505-770`),
  `subagentChildUsage` (`:230`, used at `:1893`), `SubagentModelOverride` plumbing (`:1164-1259`).
  Shared with the parent and one-shot path, must stay: `runtimeProviderId`, custom-provider
  registration, adaptive thinking, credential scrubbing (`:387,447,527,553`), `resolveModel`
  (imported by `oneshot-llm.ts:20`), `agentDir` creation and ownership (the SDK resource loader
  creates a disk-backed settings manager, `resource-loader.js:244`). Frame fields `subagent_model` /
  `subagent_thinking_level` (`agent_ws.go:130`, `applySubagentSettings`). Compose: mounts
  (`docker-compose.yml:193-196`), `init-dirs` seeds the subagent config (`:38-41`), perms (`:49`).
- Settings: `LLMSettings.SubagentModel` / `SubagentThinkingLevel` (`models_settings.go:171`),
  handler (`api_settings_llm.go:150-174,236`), OpenAPI `docs/openapi.yaml:1269-1280`, UI
  `LLMSettingsSection.tsx`, `llmModelSuggestions.ts`, `types/index.ts`, akm
  `skills/akmatori/references/settings.md:23-27`.
- Migrations: `preMigrate*` functions run from `runMigrations` **before** AutoMigrate
  (`database/db.go:108,160`); existing column drops use `ALTER TABLE … DROP COLUMN` on both engines
  (`db.go:372,432`); migration tests run on SQLite (`models_cron_test.go:140`); there is no Postgres
  job in CI. SQLite rejects `ILIKE` and multi-table `DROP TABLE`. Cron create pins zero-value bools
  with explicit updates because GORM omits them from INSERT (`cron_runner.go:827-850`).
- CI gates (`.github/workflows/ci.yml`): Go vet + test for both modules, worker `npm run build` +
  test, web `tsc -b` + lint + test.
- Bench: the harness counts `Ran: subagent` versus `EACCES` per cell (memory note
  `bench-subagents-eacces`); the worker log serializes the gateway operation inside the tool
  arguments (`agent-runner.ts:1873`, `tool-output-formatter.ts:25`).

## Development Approach

- Three PRs in order: A → B1 → B2. Each is independently shippable. B1 may start once A is on main.
- Regular (code first, then tests) for wiring and prompt changes. TDD for the search scorer, the
  `memory.write` storage contract and the migrations: fixtures and expected results first.
- **CRITICAL: every task MUST include new or updated tests.**
- **CRITICAL: all tests must pass before starting the next task.** The gates are the CI gates:
  `make test` and `go vet ./...` (API), `make test-mcp` and `go vet` in `mcp-gateway`,
  `cd agent-worker && npm run build && npm test`, `cd web && npx tsc -b && npm run lint && npm test`.
- Prompt text and tool descriptions are part of the product. Keep them short, concrete, with one
  worked example each.
- Breaking API changes are listed in "Release notes" below and must land in the PR description.
- **(rev 2)** API, gateway and worker change together in A and B1. All three images ship under one
  release tag and must be upgraded together; the release note says so, and says to drain active runs
  and take a `pg_dump` first because the mapping drop makes rollback lossy.

---

## Part A — Connectors for all skills

Implementation notes (2026-11-02), where the code differs from the text below:

- The backfill adds `runbooks` and `memory` to every migrated cron, not only `events`: today every
  cron reaches runbooks and memory through the subagents, which the allowlist does not gate, so
  Dreaming would lose memory access after B1 without them.
- `connected-tools` stays out of the generic system-skill exclusion by name (`IsGeneratedSkill`);
  PUT on it is 403 (DELETE of any system skill was already 403, not 400).
- MCP and HTTP connector functions come from a new internal gateway endpoint
  `/registry/tools?namespace=`. Because the gateway reloads asynchronously, the file is rendered
  on change, again 15 s later, and every 5 minutes (written only when the content changed).
- The 410 text does not name a version.
- Postgres check: run with an embedded Postgres 16 (no Docker access on the dev box): backfill,
  AutoMigrate afterwards, second start, jsonb round trip all pass.
- Not done here: bench token measurement (A3) — needs a bench run.

### Task A0 (rev 2): Authorization policy, written before code

**Files:**
- Create: `.claude/rules/tool-authorization.md` (the policy; linked from `connectors`,
  `mcp-gateway-tools`, `cron-and-root-skills`)
- Modify: `internal/services/tool_authz.go` (new; `ConnectorToolEntries()`, `BuiltinEntries(set)`,
  `IncidentRunAllowlist()`, `CronAllowlist(job)`), tests

Policy:

- Tool instances are of two kinds. **Connector tools** are every tool type not in the credential-less
  set; they appear on the Connectors page. **Built-ins** are the credential-less namespaces
  `incidents`, `proposals`, `events`, and from B1 `runbooks`, `memory`; they never appear as
  connectors and are addressed by type-only allowlist entries.
- Per run kind:

  | Run kind | Connector tools | Built-ins |
  |---|---|---|
  | Incident (alert, Slack, API request) | all enabled | `events`, `incidents`, (`runbooks`, `memory` from B1) |
  | Proposal chat (`proposal-editor`) | none (unchanged) | `incidents`, `proposals` (`runbooks`, `memory` from B1) |
  | Cron, `tools_enabled=true` | all enabled | the job's `builtin_tools` |
  | Cron, `tools_enabled=false` | none | the job's `builtin_tools` |

  `proposals` is never granted to incident runs. `incidents` is granted to incident runs because it is
  read-only and the evaluator already relies on it; note it in the release notes.
- Default `builtin_tools`: operator-created crons `["events","runbooks","memory"]`; Dreaming
  `["events","runbooks","memory"]`; improvement evaluator `["events","runbooks","memory","incidents","proposals"]`.
  Before B1 the two knowledge names are accepted and ignored.
- Lookup errors fail closed: every builder returns a non-nil empty slice (reject all), as today.
- Instance selection stays explicit: the allowlist is ordered by instance ID ascending so the
  gateway's "first authorized instance" fallback is stable, and `connected-tools` tells the agent to
  pass `logical_name` whenever a tool type has more than one instance.

- [ ] Tests: each row of the table; a disabled connector instance is excluded; `proposals` absent from
      incident runs; ordering by ID.

### Task A1: Allowlists from the policy

**Files:**
- Modify: `internal/services/skill_service.go` (`GetToolAllowlist` delegates to
  `IncidentRunAllowlist()`; the three handler call sites plus `api_incidents.go:499` are unchanged)
- Modify: `internal/services/cron_runner.go` (`buildCronToolAllowlist(job)`, `CreateJob`, `UpdateJob`)
- Modify: `internal/database/models_cron.go` (`ToolsEnabled bool`, `BuiltinTools database.StringList`
  JSON column), `internal/database/db.go` (seeds set both fields)
- Tests: `skill_service_test.go`, `cron_runner_test.go`, `models_cron_test.go`

- [ ] **(rev 2)** Zero-value bool: `CreateJob` pins `tools_enabled=false` with an explicit update in the
      create transaction, exactly like `enabled` and `post_results` (`cron_runner.go:827-850`). Update
      requests carry `*bool`: omitted = unchanged.
- [ ] **(rev 2)** Seeds: `SeedSystemCronJobs` and `SeedImprovementEvaluatorCron` set the defaults from A0
      on insert and keep preserving operator edits on existing rows.
- [ ] Tests: create with `tools_enabled=false` stays false; both seed paths; partial update of another
      field leaves both new fields alone; evaluator allowlist contains `incidents` and `proposals`.

### Task A2: Remove the mapping from the model, services, proposals and API

**Files:**
- Modify: `internal/database/models_skills.go` (drop `Skill.Tools`, `ToolInstance.Skills`,
  `SkillTool`), `models_cron.go` (drop `CronJob.Tools`, `CronJobTool`), `db.go` (AutoMigrate list,
  `preMigrateToolMappings`)
- Modify: `internal/services/skill_service.go` (drop `AssignTools`, `getSkillTools`), `interfaces.go`,
  `tool_service.go:129-140`, `mcp_server_service.go:521`, `skill_file_sync.go:348,400`
- Modify: `internal/services/proposal_service.go` (`proposalCronContent`: `tool_logical_names` →
  `tools_enabled`, `builtin_tools`; staleness compares the new fields; `resolveToolLogicalNames`
  becomes `translateLegacyToolNames`), `internal/database/models_proposals.go` (snapshot shape)
- Modify: `mcp-gateway/internal/database/models_proposals.go:65,81` (drop `CronJob.Tools`,
  `CronJobTool`; add the two columns), `mcp-gateway/internal/database/db.go:119`,
  `mcp-gateway/internal/tools/proposals/proposals.go:130,191,594` (no `Tools` preload; snapshots and
  `cron_new`/`cron_update` content use the new fields)
- Modify: `internal/handlers/api_skills.go` (`/tools` sub-route), `api_cron_jobs.go`,
  `api_mcp_servers.go:470`, `api_connectors.go:181`, `internal/api/types.go`, `docs/openapi.yaml`
- Modify: `internal/testhelpers/builders.go:72`; delete assignment-only tests, update the rest

- [ ] **(rev 2)** Migration `preMigrateToolMappings`, one transaction, idempotent, runs before AutoMigrate
      so it creates what it needs itself:
      1. If `cron_job_tools` does not exist → return (already migrated or fresh install; AutoMigrate adds
         the columns with their defaults, seeds set system rows).
      2. `ALTER TABLE cron_jobs ADD COLUMN tools_enabled BOOLEAN NOT NULL DEFAULT TRUE` and
         `… ADD COLUMN builtin_tools TEXT` when missing (single-column `ADD COLUMN` works on both engines).
      3. Backfill per cron row from the join: `tools_enabled` = at least one joined instance whose tool
         type is a connector type; `builtin_tools` = the credential-less types among the joined
         instances plus `events`; a row with no joined rows → `false`, `["events"]` (today's behaviour).
         System rows follow the same rule, so operator customisation is kept and the evaluator keeps
         `incidents`+`proposals`.
      4. `DROP TABLE skill_tools` and `DROP TABLE cron_job_tools` as two statements.
      Tests on SQLite for: fresh install, upgrade with tools, upgrade without tools, system crons, second
      start (no-op). Manual check on the Postgres dev compose, recorded in the PR; a Postgres service job
      in CI is a follow-up issue, not part of this PR.
- [ ] **(rev 2)** Pending proposals: `cron_new` / `cron_update` rows whose `proposed_content` still has
      `tool_logical_names` are translated at list, approve and staleness time with the same rule as the
      backfill. Snapshots (`current_snapshot`) are compared on the translated shape. Test one pending
      proposal of each kind across the upgrade.
- [ ] `PUT /api/skills/{name}/tools` and `GET` → `410 Gone`,
      `{"error":"tools are available to all skills since 0.4; manage them under /connectors"}`.
- [ ] Cron API: `tools_enabled` (bool), `builtin_tools` (array of namespace strings, validated against
      the credential-less set). `tool_instance_ids` in a request → 400 "unknown field".
- [ ] Skill DTOs stop carrying `tools`.
- [ ] Tests: 410 on both verbs; cron create defaults; invalid `builtin_tools` name → 400; gateway
      proposals tool creates and lists cron proposals with the new fields.

### Task A3: `connected-tools` system skill replaces per-skill "Assigned Tools"

**Files:**
- Modify: `internal/services/skill_prompt_service.go` (`generateSkillMd` drops the tools section;
  new `generateConnectedToolsSkillMd`), `skill_file_sync.go` (render on every tool instance, HTTP
  connector or MCP server change and at start; the import pass treats the name as system),
  `skill_service.go` (seed the system skill row; `GetEnabledSkillNames` includes it; memory scopes
  exclude system skills), `internal/database/db.go` (seed, collision rename)
- Modify: `internal/services/mcp_server_service.go` or a new `MCPGatewayClient.ListTools(namespace)`
  call to fetch upstream MCP tool schemas for the render
- Modify: `agent-worker/src/agent-runner.ts:59-64` (`BASH_TOOL_GUIDELINES`), `:1648`
  (`extractSkillNameFromReadPath` ignores system skills), `agent-worker/src/gateway-tools.ts:135,143`
  (`gateway_call` description and guidelines)
- Modify: `DefaultIncidentManagerPrompt`, `DefaultCronAgentPrompt`, `DefaultProposalEditorPrompt`
  (one line: "Tool schemas and examples: read `connected-tools/SKILL.md`")
- Tests: `skill_prompt_test.go`, `skill_file_sync_test.go`, worker tests for the read-path filter

- [ ] **(rev 2)** `connected-tools` is a **system skill row** (`IsSystem=true`, enabled, not deletable,
      prompt not editable: the body is generated). This way it passes the worker's enabled-skill filter,
      the import pass does not create a duplicate, and `GetSkill`/list show it with a "generated"
      badge. Memory scopes: `memory.write` and the UI scope picker accept `global` and non-system skill
      names only, so `connected-tools` is never a scope.
- [ ] **(rev 2)** `last_skill`: reading `connected-tools/SKILL.md` (or any system skill) does not latch
      `last_skill`, so formatting rules keep the domain skill.
- [ ] **(rev 2)** Collision: an existing non-system skill named `connected-tools` is renamed to
      `connected-tools-1` by the seed, with a warning log and a release-note line.
- [ ] Content: frontmatter `name: connected-tools`, `description: Every tool the agent may call
      through gateway_call, with parameter schemas and examples. Read before the first infrastructure
      call.` Body: per enabled instance the existing `extractToolDetails` + `generateToolUsageExample`
      blocks, grouped by tool type; a "Built-ins" group for `incidents`, `events` (and later
      `runbooks`, `memory`); a note "Pass logical_name when a type has several instances"; header
      "Tools disabled in Connectors are not listed and are refused by the gateway."
- [ ] **(rev 2)** MCP namespaces: the render fetches the upstream tool list and schemas from the gateway
      (the UI already reads `/mcp/tools*`) and prints name, description and parameters per function.
      When the gateway is unreachable the block falls back to today's discovery hint.
- [ ] Worker guideline and `gateway_call` text: "Read your skill's SKILL.md, then
      `connected-tools/SKILL.md`, before the first gateway_call. Use list_tools_for_tool_type /
      get_tool_detail only for a tool missing there."
- [ ] Measure prompt tokens per incident on the bench before and after; record in the PR. If the file is
      large on gcore, shorten rarely used blocks to header + `get_tool_detail` hint.
- [ ] Tests: regenerated after `PUT /api/tools/{id}` toggles `enabled`; a disabled instance absent; an
      MCP namespace renders its functions from a fake gateway response and the fallback without one;
      the row survives the import pass; `DELETE /api/skills/connected-tools` → 400.

### Task A4: UI

**Files:**
- Modify: `web/src/pages/Skills.tsx` (remove "Tool Connections", `toolIds`, `toggleToolSelection`,
  the `/tools` calls in `web/src/api/client.ts`; render system skill `connected-tools` read-only)
- Modify: `web/src/components/cron/CronJobForm.tsx`, `cronJobHelpers.ts`, `CronJobsManager.tsx`
  ("Allow infrastructure tools" switch; "Built-in tools" multi-select from the credential-less set)
- Modify copy: `AddMCPConnectorModal.tsx:100`, `MCPServerPanel.tsx:235`, `ConnectorPartsPanel.tsx:90`,
  `ConnectorDetail.tsx:230`, `connector_catalog.go:77` ("Its tools become available to the agent as
  soon as the connector is enabled")
- Modify: `web/src/pages/Proposals*.tsx` (cron proposal diff shows the two fields),
  `web/src/types/index.ts`, tests

- [ ] Tests: cron form sends both fields; Skills page renders without the tools block; proposal detail
      renders a translated legacy proposal.

### Task A5: Docs and operator skill

- [ ] `.claude/rules/connectors.md`, `mcp-gateway-tools.md`, `cron-and-root-skills.md` (lines 20-44),
      `proposals.md` (cron fields), new `tool-authorization.md`, `CLAUDE.md` (rules list, layout line
      for `akmatori_data/agents/` stays until B1), `README.md`, `docs/CONTRIBUTING.md`.
- [ ] `skills/akmatori/SKILL.md:117`, `references/knowledge.md:43-44`, `references/operations.md`
      (cron fields), remove "A tool is usable only if assigned".
- [ ] Release notes (see the end).

---

## Part B1 — Gateway knowledge tools replace the subagents

Implementation notes (2026-11-02), where the code differs from the text below:

- Proposal chat gets `runbooks` and `memory` for reading; the gateway refuses `memory.write` /
  `memory.delete` from runs with `source_kind=proposal` (changes go through proposals).
- Writes are refused for finished runs (`completed`, `failed`, `superseded`), not only for
  superseded ones.
- Approval authorship: `UpsertByName` takes ownership whenever the caller passes
  `created_by=operator` (proposal approval, Slack feedback, from-text), with no new parameter.
- `FinishRun` refreshes when a fingerprint (row count + newest `updated_at`) changed, so deletes
  count too; the refresh is in-process state, so the first run after a start always refreshes once.
- Collision handling disables the colliding HTTP connector / MCP server and logs a warning; there
  is no special `error: namespace reserved` status (the Connectors page shows `disabled`).
- The repo-wide grep test is limited to the default prompts (`TestDefaultPrompts_NoRetiredKnowledgeRefs`)
  and `PrependGuidance`: the disk-mirror code that B2 deletes still names the old flow in comments.
- No `runbooks` / `memory` tool type rows or instances are seeded: built-ins are type-only entries.
- Bench: local commit 807389c on `akmatori-bench` branch `knowledge-tools` (not pushed): accept the
  410, save `full_log.txt`, count knowledge calls and hits per cell. The Dreaming maintenance
  scenario and the A/B are not done yet.

### Task B1.1: Run identity and gateway mirrors

**Files:**
- Modify: `agent-worker/src/gateway-client.ts:356` (send `X-Run-ID`), `agent-runner.ts` (pass the
  run id into the client), `internal/handlers/agent_ws.go` (no change: `run_id` is already in the
  frame)
- Modify: `mcp-gateway/internal/mcp/server.go` (read `X-Run-ID` into the call context),
  `mcp-gateway/internal/database/models_proposals.go` (extend `Runbook`, `Memory`; add `GeneralSettings`
  with `MemoryGenerationEnabled *bool` only, `ContextFile{Filename}`, `AgentRun{RunID, IncidentUUID,
  SourceKind, StartedAt}` or reuse the existing mirror), tests

- [ ] **(rev 2)** `X-Incident-ID` is a scope that follow-up turns reuse. Mutating knowledge tools require
      `X-Run-ID` and check that the `agent_runs` row exists and its `incident_uuid` equals the scope;
      otherwise 403 "run does not belong to this scope". Read tools accept a missing run id.
- [ ] Per-run caps are keyed by run id, not scope. Counters live in memory with a TTL (the run cannot
      outlive the worker session); a gateway restart resets them, which is accepted and logged.
- [ ] Provenance: `memory.incident_uuid` = the scope when the scope's `incidents` row is a real
      incident (`source_kind` not `proposal`, `cron`, `request`); empty otherwise. `created_by =
      "agent"` for all agent writes, including cron runs.
- [ ] Tests: repeated Slack turns on one scope get independent caps; API request and cron scopes
      produce empty `incident_uuid`; a superseded run id (not the newest for the scope) is still
      accepted for reads and refused for writes; missing header on write → 403.

### Task B1.2: Search scorer (TDD)

**Files:**
- Create: `mcp-gateway/internal/tools/knowledge/search.go`, `search_test.go`, `testdata/` corpus
  (10 runbooks, 30 memories with realistic names, hosts, error strings)

- [ ] Tokeniser: lowercase; split on whitespace and punctuation except `.`, `-`, `_`, `/`, `:` inside
      a token (hostnames, IPs, `nginx-edge-03`, `/var/lib` survive); drop tokens shorter than 2 and a
      small English stopword list; also emit the split parts of a compound token so partial matches
      score.
- [ ] Scorer: BM25 (k1=1.2, b=0.75) over two fields, title/name+description weight 3, body weight 1.
      Documents with zero matching terms are dropped. Ties → newest `updated_at`, then lowest id.
- [ ] Excerpt: ±200 runes around the densest window of matching terms, line-trimmed, `…` at the cut.
- [ ] Alert payloads as queries: cap the query at 4,000 runes; keep at most 40 distinct terms, chosen
      by lowest positive document frequency in the loaded corpus (terms that match nothing are
      dropped first).
- [ ] **(rev 2)** Corpus loading, no SQL prefilter: one `SELECT` with a per-model projection (runbooks
      `id, title, content, updated_at`; memories `id, scope, name, type, description, body, created_by,
      source_file_id, updated_at`), optional `scope IN (?)` and `type = ?` filters, ordered by
      `updated_at DESC`, capped at 5,000 rows. Bodies are at most 8 KiB, so the worst case is 40 MB per
      call; realistic libraries are hundreds of rows. Above the cap the tool adds
      `"truncated_corpus": true` to the response and the gateway logs a warning once per hour. Document
      frequencies are computed over the loaded rows on each call. No `ILIKE`; nothing engine-specific.
- [ ] Tests pin the top-3 for 12 named queries (host alone, error string alone, full alert payload,
      operator-feedback topic, a query with no match → empty, a query where only the body matches).

### Task B1.3: `runbooks.*` and `memory.*` tools

**Files:**
- Create: `mcp-gateway/internal/tools/knowledge/runbooks.go`, `memory.go`, tests
- Modify: `mcp-gateway/internal/tools/registry.go` (`registerKnowledgeTools`, `builtInToolNamespaces`
  += `runbooks`, `memory`), `mcp-gateway/internal/auth/authorizer.go:19` (`credentiallessNamespaces`)
- Modify: `internal/services/http_connector_service.go:10` (`reservedToolNamespaces` += both),
  `internal/services/tool_authz.go` (A0 table gains `runbooks`, `memory`), `connector_catalog.go:82`
  (`hiddenToolTypes`), `tool_service.go` (seed instances for both, same pattern as `events`)
- Create: `tests/fixtures/memory_validation_cases.json` (shared behavioural fixture, read by
  `internal/services/memory_service_test.go` and the gateway's `memory_test.go`)

Tool contract:

| Tool | Args | Returns |
|---|---|---|
| `runbooks.search` | `query` (required), `limit` (default 5, max 20) | `[{id, title, excerpt, updated_at}]` |
| `runbooks.get` | `id` | `{id, title, content, updated_at}` |
| `memory.search` | `query` (required), `scopes` (array; default all), `type` (fact / feedback), `limit` (default 5, max 20) | `[{id, scope, name, type, description, excerpt, created_by, updated_at}]` |
| `memory.list` **(rev 2)** | `scopes`, `type`, `since` (ISO-8601 or Unix seconds), `limit` (default 50, max 200), `cursor` | `{items: [{id, scope, name, type, description, created_by, updated_at}], next_cursor}`; ordered by `updated_at DESC, id DESC`; no bodies |
| `memory.get` | `id` or `scope`+`name` | full row plus `references`: memory links as `{kind: memory, scope, name, id, exists}`, context files as `{kind: file, path: /akmatori/context/<file>, exists}` |
| `memory.write` | `scope`, `name`, `type`, `description`, `body` | `{id, scope, name, action: created / updated / unchanged / refused_protected}` |
| `memory.delete` | `scope`, `name` | `{deleted: bool, action}` |

- [ ] **(rev 2)** `memory.list` exists because the Dreaming cron must enumerate the global scope by date
      and the evaluator wants recent feedback; search with a required query cannot do that.
- [ ] **(rev 2)** Validation, split in two groups and documented in the tool description:
      *Shared with the API* (`validate`): trim; scope and name slugs `^[a-z0-9]+(?:-[a-z0-9]+)*$`;
      name ≤ 200 bytes; description non-empty and ≤ 500 **bytes**; body ≤ `MemoryBodyMaxBytes`
      (8 KiB); type is `fact` or `feedback` after `NormalizeMemoryType` (legacy names fold, unknown →
      error). *Agent-only policy*: a description over 500 bytes is cut at a UTF-8-safe byte boundary
      (the same `draftDescription` rule) and the response says `description_truncated: true`; the
      agent is told to keep the full text in the body itself. `scope` must be `global` or an existing
      non-system skill name; the error lists the valid scopes. `name` must not start with `file-`.
      Both modules run the shared fixture file, which holds inputs and expected outcome per case, so a
      drift fails both test suites. Agent-only cases are tagged and skipped by the API test.
- [ ] **(rev 2)** Protected rows: `created_by = "operator"` **or** `source_file_id IS NOT NULL`
      (context-file summaries, even after an operator rename). Legacy rows with empty `created_by` are
      agent-writable. Protection is in the statement, not a prior SELECT:
      `UPDATE memories SET … WHERE scope=? AND name=? AND (created_by IS NULL OR created_by <> 'operator')
      AND source_file_id IS NULL`; zero rows → look the row up → exists ⇒ `refused_protected`, else
      insert (`ON CONFLICT DO NOTHING`, then re-run the update once for the race). Delete uses the same
      `WHERE`. Updates never touch `source_file_id` or `created_by`.
- [ ] **(rev 2)** Proposal approval of a memory marks it operator-owned: `UpsertByName` gets an
      `authorship` parameter and `ProposalService.Approve` passes `operator`
      (`proposal_service.go:367`). Test that an approved memory is then refused to the agent.
- [ ] Upsert updates only when type/description/body differ (keeps `updated_at` stable, which the
      manifest ordering depends on).
- [ ] **(rev 2)** Gate: mirror `general_settings.memory_generation_enabled` as `*bool`; nil, missing
      row or read error = enabled (fail open, same as the API). Off → `memory.write` and `memory.delete`
      return "memory generation is off for this install; findings are not stored". Timing change,
      documented in the rule file: today the gate is applied at ingest after the run, so turning it off
      mid-run discards that run's writes; now each write is checked when it happens, so writes made
      before the toggle stay. Operator writes (UI, API, from-text, Slack feedback, proposals, uploads)
      are not gated, as today.
- [ ] Caps per run (B1.1 counters): 20 writes, 10 deletes.
- [ ] Logging: one line per write/delete with run id, scope, name, action.
- [ ] **(rev 2)** Namespace collision on upgrade: an existing HTTP connector or MCP server whose
      namespace is `memory` or `runbooks` is disabled by the migration, its connector status shows
      `error: namespace reserved`, and a warning is logged; the operator renames it. Tested.
- [ ] Tests: every fixture case; protection for operator, source-file and legacy rows; concurrent
      operator edit versus agent upsert (two goroutines, SQLite and the race detector); gate nil /
      false / missing row / read error; caps; `memory.list` paging and `since`; `memory.get`
      references; scope filter in search.

### Task B1.4: Prompts, manifest from the database, prompt freshness

**Files:**
- Modify: `internal/database/db.go` (`DefaultIncidentManagerPrompt` steps 1-2, `DefaultCronAgentPrompt`,
  `DefaultProposalEditorPrompt`, `dreamingCronPrompt`, evaluator prompt; new `legacySystemCronPrompts`),
  `internal/executor/executor.go` (`PrependGuidance`), `internal/services/skill_prompt_service.go`
  (`memoryRecallInstruction`, `memoryWriteInstructionTemplate`, `memoryGenerationOffInstruction`,
  `renderMemoryRecallSection` uses `MemoryService.RenderManifest(scope)`; drop `readMemoryManifest`),
  `internal/services/memory_service.go` (`RenderManifest` from `ListMemoriesByScope`, same rules as the
  disk renderer: feedback first, 50 reserved feedback slots of 150 with unused slots returned to
  facts, byte and line caps), `internal/services/context_summarizer.go`
- Modify: `internal/services/incident_service.go:792-818` (`FinishRun`: replace the ingest goroutine
  with "if `MAX(updated_at)` over memories moved since the run started, regenerate every SKILL.md and
  run `SyncMemoryFiles`"), `skill_service.go:26-55` (`MemoryIngester` → `MemoryRefresher`)
- Tests: `prompt_test.go`, `memory_prompt_test.go`, `executor_test.go`, `alert_test.go`,
  `memory_service_sync_test.go` (move the manifest cap and feedback-budget tests to `RenderManifest`)

- [ ] Step 1: `gateway_call("runbooks.search", {"query": "<Original alert text verbatim when present,
      else one sentence>"})`; retry up to 2 times with a narrower query (host, or error string) when
      the top result is unrelated; then `runbooks.get`. Keep "Follow matching runbook procedures as
      your PRIMARY investigation guide."
- [ ] Step 2: `memory.search` with the same query, `scopes: ["<skill>", "global"]`, then `memory.get`.
- [ ] Record durable findings: the rules from `akmatori_data/agents/memory-writer.md` move into
      `memoryWriteInstructionTemplate` and the `memory.write` description, condensed: search first and
      update the memory that covers the subject instead of adding a name; correct or delete a memory
      the new facts prove wrong; absolute dates only; feedback bodies are rule + `**Why:**` + `**How
      to apply:**`; never save what runbooks, alerts or context files already say; description ≤ 500
      bytes, details in the body. One worked example.
- [ ] Off instruction: "Memory generation is off. Do not call memory.write or memory.delete."
- [ ] Remove the filesystem fallback paragraphs and every `/akmatori/runbooks`, `/akmatori/memory`
      path from prompts.
- [ ] **(rev 2)** Dreaming prompt uses `memory.list` with `scopes: ["global"], since: <now-14d>` and
      pages with `next_cursor`; the evaluator uses `memory.list` with `type: feedback` for recent
      feedback. Both drop subagent names.
- [ ] **(rev 2)** Persisted system cron prompts: `SeedSystemCronJobs` and the evaluator seed compare an
      existing row's prompt with `legacySystemCronPrompts` (every default shipped so far, verbatim) and
      replace a match with the new default; a custom prompt is kept and a warning names the job. Test
      both branches.
- [ ] **(rev 2)** Startup warning lists skills, runbooks, memories **and cron prompts** whose text
      contains `subagent(`, `memory-searcher`, `runbook-searcher`, `memory-writer`,
      `/akmatori/runbooks` or `/akmatori/memory`, once per start.
- [ ] **(rev 2)** Freshness: AGENTS.md is rendered per run from the DB (unchanged mechanism, new
      source). SKILL.md embeds its scope manifest, so after a run that changed memories the API
      regenerates all SKILL.md files in `FinishRun` (cheap: one file per skill) and after every operator
      write (`api_memories.go:395`, unchanged). Test: a gateway write followed by `FinishRun` updates the
      skill's SKILL.md manifest.
- [ ] Tests assert the new tool names and that no prompt, rule or data file contains the old names or
      paths (a repo-wide grep test over `internal/`, `agent-worker/src`, `akmatori_data`, `.claude/rules`,
      excluding `docs/plans`).

### Task B1.5: Agent worker without pi-subagents; disk mirror read-only

**Files:**
- Modify: `agent-worker/package.json` (drop `pi-subagents`), `package-lock.json`
- Modify: `agent-worker/Dockerfile` (**rev 2: only** lines 78-98 extension copy and agent `COPY`,
  lines 110-119 `PI_SUBAGENTS_*` env, and `fzf`; keep dirs, ownership, deps, compiled-code copy,
  `USER agent`, `ripgrep`), `docker-compose.dev.yml:39-43` comment (repo-root context stays for other
  reasons or shrinks to `./agent-worker`; decide when touching it)
- Modify: `agent-worker/src/agent-runner.ts` (remove `PARENT_EXCLUDED_TOOLS`, `logActiveToolSet`,
  `subagentAsyncConfigWarning`, the child `settings.json` / `models.json` writers and their EACCES
  warning, `subagentChildUsage` and its call at `:1893`, `SubagentModelOverride` and the signatures
  that carry it; **rev 2: keep** `runtimeProviderId`, custom-provider registration, adaptive
  thinking, credential scrubbing, `resolveModel`, `agentDir` creation and ownership, operator
  extension loading and its error log), `index.ts`, `orchestrator.ts`, `types.ts`, `sampling.ts`,
  `tool-output-limit.ts`, tests
- Modify: `internal/handlers/agent_ws.go` (frame fields, `applySubagentSettings`), tests
- Delete: `akmatori_data/agents/*.md`, `akmatori_data/extensions/subagent/`,
  `internal/services/subagent_files_test.go`; `.gitignore:6`, `.dockerignore:6` entries
- Modify: `docker-compose.yml` (`init-dirs` lines 38-41 and 49; the `agents` mount line 195; the
  memory mount line 194 becomes `:ro`; keep the `extensions` mount), `docker-compose.dev.yml`

- [ ] **(rev 2)** Disk-only edits: on the first start of the B1 version the API runs one final
      `IngestFromDisk` **before** the first `SyncMemoryFiles`, so memory files an operator edited by hand
      reach the database before the directory becomes a pure mirror. Guarded by a settings flag
      (`memory_disk_ingested_at`) so it runs once. The release note still says "export after upgrading
      if you want a copy".
- [ ] **(rev 2)** Smoke tests before the dependency is removed, recorded in the PR: custom provider run
      (gcore DeepSeek gateway), one-shot LLM call, operator extension in `akmatori_data/extensions`
      loading with `errors: []`, fresh volume start, persisted volume start.
- [ ] Usage accounting: without `totalChildUsage`, `tokens_used` is the parent's only. Note in the PR
      that bench and UI token numbers drop for this reason.
- [ ] Tests: worker starts with no `subagent` tool; a `subagent(...)` call from an operator-edited
      prompt yields the normal unknown-tool error.

### Task B1.6: Remove the subagent model override

**Files:**
- Modify: `internal/database/models_settings.go:163-172` (`preMigrateLLMSettingsDropSubagent` with
  `ALTER TABLE … DROP COLUMN`, as the existing dead-column migrations), `internal/services/llm_settings.go:87-110`,
  `internal/handlers/api_settings_llm.go`, `internal/api/types.go`, `docs/openapi.yaml:1269-1280`
- Modify: `web/src/components/settings/LLMSettingsSection.tsx`, `llmModelSuggestions.ts`,
  `web/src/types/index.ts`, `skills/akmatori/references/settings.md:23-27`
- Delete: `internal/handlers/api_settings_llm_subagent_test.go`

- [ ] `subagent_model` / `subagent_thinking_level` in a PUT/POST → 400 "unknown field". Responses stop
      carrying them. Migration idempotent on both engines.

### Task B1.7: Docs, bench, release

- [ ] Rules: rewrite `.claude/rules/memory-and-runbooks.md`; update `agent-worker-pi-sdk.md`,
      `mcp-gateway-tools.md` (new built-in section, same shape as Incidents / Events, plus the run-id
      header), `proposals.md` (approval sets authorship), `oneshot-llm.md:29` (no child `models.json`),
      `tool-authorization.md` (A0 table gains the two namespaces), `CLAUDE.md` (layout line for
      `akmatori_data/agents/`), `README.md`, `docs/CONTRIBUTING.md`,
      `skills/akmatori/references/knowledge.md:195,211`.
- [ ] Bench repo (separate PR, lands before the A/B): the per-cell parser reads the gateway operation
      inside the `gateway_call` arguments and the result, counts `runbooks.search` / `memory.search`
      calls and non-empty results; the EACCES check goes. Add one maintenance scenario that seeds a
      global memory library and runs the Dreaming prompt, since incident scores do not exercise
      `memory.list`.
- [ ] A/B on the bench before merge, same protocol as the EACCES fix (memory note
      `bench-subagents-eacces`): baseline = main image, candidate = PR image; claude-haiku-5-5,
      claude-sonnet-5, deepseek-v4.1-flash-1017; Linux + k8s; compare identification totals, wall
      time, prompt tokens, search calls and hits per cell. Do not publish A/B numbers. After merge and
      release tag, decide on a new leaderboard edition.

---

## Part B2 — Remove the disk mirrors (a week after B1 is live on the bench and on gcore/SDGroup)

### Task B2.1: Delete sync, ingest and tombstones

**Files:**
- Modify: `internal/services/memory_service.go` (`SyncMemoryFiles`, `IngestFromDisk`, tombstone
  parsing, `legacyIngestName`, `deleteByScopeAndNameNoSync`, `upsertByNameNoSync`, fchmod; keep
  `UpsertByName`, `CreateWithUniqueName`, `ResolveRefs`, `RenderManifest`), `memory_types.go` comments,
  `memory_export.go` (keep; move the file formatter next to it), `memory_source_files.go:40,69`
  (drop the sync calls), `runbook_service.go` (`SyncRunbookFiles`, `runbooksDir`), `interfaces.go`,
  `cmd/akmatori/main.go:160,183`, `incident_service.go` / `skill_service.go` (drop the one-time ingest
  flag and refresher's sync call)
- Modify: `docker-compose.yml` (drop runbooks and memory mounts and their `init-dirs` lines),
  `docker-compose.dev.yml`, `.env.example`, `README.md`
- Tests: delete the disk parts of `memory_service_sync_test.go`, `memory_service_ingest_test.go`,
  runbook file tests (the manifest tests already moved in B1.4)

### Task B2.2 (rev 2): Memory references without files

**Files:**
- Modify: `internal/services/memory_refs.go:98-122` (`ResolveRefs` returns memory links as
  `{kind: memory, scope, name, id, exists}` and keeps `/akmatori/context/<file>` paths only for
  context files), `internal/handlers/api_memories.go` and `web/src/pages/Memor*.tsx` (render the new
  shape), `mcp-gateway/internal/tools/knowledge/memory.go` (`memory.get` already returns this shape
  from B1.3), `skills/akmatori/references/knowledge.md`
- [ ] Tests: a body with `[[slug]]`, `[[scope/slug]]`, `[[file.ext]]` and an invalid ref.

- [ ] The `/akmatori/context` mount stays.
- [ ] Release note: `akmatori_data/runbooks` and `akmatori_data/memory` are no longer written or read.
      `GET /api/memories/export` is the way to get files.

---

## Risks and how the plan handles them

| Risk | Handling |
|---|---|
| A is an authorization expansion: enabled-but-unassigned tools become callable | A0 policy written first; release note; the migration never disables; `proposals` stays out of incident runs; Connectors page shows every connector tool |
| Built-ins leaking into incident runs | A0 table; only `incidents` (read-only) is added, and it is named in the release note |
| System crons losing their built-ins | `builtin_tools` per cron, backfilled from the join; evaluator keeps `incidents`+`proposals` |
| `connected-tools` invisible to the worker, or latched as `last_skill` | System skill row; read-path filter ignores system skills |
| Token cost of `connected-tools/SKILL.md` on installs with many tools | Measured on the bench in A3; shorten rare blocks if needed |
| Migration retried or partially applied | Single transaction keyed on the join table's existence; SQLite tests for every path; Postgres checked on the dev compose |
| Pending cron proposals break | Translation of `tool_logical_names` at list, approve and staleness |
| Lexical search misses | Retry guidance in the prompt; fixture corpus with alert payloads as queries; bench A/B |
| Maintenance crons cannot enumerate | `memory.list` with `since` and cursor |
| Memory writes bypass API validation | Shared behavioural fixture run by both modules; agent-only rules documented |
| Agent overwrites operator or summary memories | Protection in the `WHERE` clause; `source_file_id` protected; approval sets authorship |
| Scope reuse across turns defeats caps and provenance | `X-Run-ID` validated against `agent_runs` |
| Stale manifests in SKILL.md after gateway writes | Regenerate SKILL.md in `FinishRun` when memories changed; AGENTS.md per run from the DB |
| Hand-edited memory files lost | One final `IngestFromDisk` on first B1 start; mount becomes read-only |
| Worker cleanup removes shared code | Explicit keep-list in B1.5; smoke tests before removing the dependency |
| Operator prompts and persisted cron prompts still name subagents | Known defaults replaced by the seed; custom ones warned at startup |
| Reserved namespace collision with an existing connector | Disabled with a visible status and warning |
| Token totals drop because child usage is gone | Called out in the PR |
| Rollback after the mapping drop | `pg_dump` before upgrade, stated in the release note |

## Release notes (draft, to paste into the PR descriptions)

- **A**: Connectors are available to all skills. Every enabled connector tool is callable by every
  incident run at once; disable a tool instance to withhold it. Incident runs also get the read-only
  `incidents` built-in. `PUT/GET /api/skills/{name}/tools` → 410. Cron jobs: `tool_instance_ids`
  replaced by `tools_enabled` (bool) and `builtin_tools` (array). Tables `skill_tools`,
  `cron_job_tools` dropped (take a `pg_dump` first; rollback is lossy). New generated system skill
  `connected-tools`; an existing skill with that name is renamed `connected-tools-1`. Pending cron
  proposals are translated. Upgrade API, gateway and worker together; drain active runs.
- **B1**: Runbook and memory search and memory writes run as gateway tools `runbooks.*`, `memory.*`
  (`runbooks` and `memory` are reserved namespaces; a connector using them is disabled until renamed).
  Memory writes happen during the run and are visible at once; memory generation off now blocks
  each write instead of discarding at the end. pi-subagents, `akmatori_data/agents`,
  `akmatori_data/extensions/subagent` removed. LLM settings `subagent_model`,
  `subagent_thinking_level` removed (400 if sent). System cron prompts that were never edited are
  updated; edited ones are listed in a startup warning. Run token totals no longer include child
  usage. The memory directory is read-only for the agent; hand edits made before the upgrade are
  imported once.
- **B2**: `akmatori_data/runbooks` and `akmatori_data/memory` no longer used. Memory references in the
  API and UI point at memories by scope and name, not at files.
