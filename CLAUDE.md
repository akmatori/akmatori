# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform for SRE teams. It ingests alerts from monitoring systems, analyzes them with multi-provider LLM agents, and can execute remediation through approval-gated tools.

## Stack and Runtime

- Docker deployment: API, Agent Worker, MCP Gateway, PostgreSQL
- Backend: Go 1.24+
- Agent Worker: Node.js 22+ / TypeScript with `@earendil-works/pi-coding-agent` (`v0.78.1`)
- Frontend: React 19 + TypeScript + Vite + Tailwind
- Database: PostgreSQL 16 + GORM
- LLM providers: Anthropic, OpenAI, Google, OpenRouter, NVIDIA NIM, MiniMax, Ant Ling, custom/on-prem

## Repository Layout

```text
cmd/akmatori/               main API entrypoint
internal/alerts/adapters/   inbound alert adapters
internal/alerts/extraction/ one-shot LLM alert extraction
internal/api/               API request/response helpers
internal/database/          GORM models and DB logic
internal/handlers/          HTTP, WebSocket, Slack handlers
internal/logging/           slog setup
internal/messaging/         provider abstraction (Slack, Telegram stub)
internal/output/            structured agent output parsing
internal/services/          business logic and interfaces
internal/setup/             first-run bootstrap
internal/slack/             Slack client, typing, reload logic
internal/testhelpers/       builders, fixtures, mocks
agent-worker/src/           worker orchestrator and tool bridge
mcp-gateway/internal/       tool auth, rate limiting, MCP proxy, tool impls
akmatori_data/agents/       system-supplied pi-mono subagent definitions
docs/                       OpenAPI specs
tests/fixtures/             payloads and test data
web/                        React frontend
```

## Core Architecture

### Backend flow

1. Adapters or Slack create/continue incidents.
2. Handlers call services through interfaces from `internal/services/interfaces.go`.
3. Agent runs happen through the worker WebSocket.
4. Tool execution goes through MCP Gateway with incident-scoped auth.
5. Final output is parsed, optionally reformatted, stored, and sent back to UI/Slack.

### Agent Worker flow

1. API sends `new_incident`, `continue_incident`, or `oneshot_llm_request`.
2. `agent-worker/src/orchestrator.ts` routes the message.
3. `agent-runner.ts` creates pi-mono sessions for full investigations.
4. `oneshot-llm.ts` handles short provider-agnostic completions.
5. Results stream back over WebSocket; session exports land in the worker work dir.

### MCP Gateway flow

1. Agent reads generated `SKILL.md` guidance.
2. Agent calls `gateway_call(toolName, args, instanceHint?)`.
3. Worker sends JSON-RPC to MCP Gateway with `X-Incident-ID`.
4. Gateway resolves routing, enforces allowlists, executes, and returns output.

## Current Behavior You Must Preserve

### One-shot LLM path

Use the one-shot path for short non-agent calls such as:
- incident title generation
- free-form alert extraction
- Slack final-message summarization
- response formatting
- feedback classification
- alert correlation (deciding whether an incoming alert is a recurrence of a recent incident)

Rules:
- API frame type is `oneshot_llm_request`
- Worker replies with `oneshot_llm_response`
- Go callers should depend on `services.OneShotLLMCaller`, not concrete worker code
- If the worker is disconnected, callers must fail gracefully and use deterministic fallbacks

### Response formatting (per-flow rules)

Ordered `FormattingRule` rows (`/api/formatting-rules`, CRUD + `PUT /reorder`) are the ONLY formatting mechanism; no match → raw response for every flow kind. `/api/settings/formatting` is 410 Gone; `migrateGlobalFormattingToRule()` converts an enabled legacy row into a catch-all rule once (rules-table-empty guard).

Rules:
- match fields (empty = wildcard, ANDed): `match_source_kind`, `match_source_uuid` (trigger), `match_channel_uuid` (destination), `match_last_skill` — OR a `match_expression` (`==/!=/&&/||/!`, parens, `and/or/not`; either/or with simple fields, API-enforced; invalid stored expr → rule skipped; Go parser + TS mirror `matchExpression.ts` must stay in sync)
- `FormatForFlow(ctx, raw, fullLog, FormatFlow)` never errors — failures collapse to passthrough; skips error responses; cron formats only on match (`full_log` keeps raw); proposal chat never formats
- flow identity via `BuildFormatFlow(incidentUUID, channelUUID)`; Slack chat channel via `FindByExternalID`
- `Incident.LastSkillUsed`: worker latches the last `<skillsDir>/<name>/SKILL.md` read, sends `last_skill` on `agent_completed`; `agent_ws.go` persists it BEFORE `dispatchOnCompleted` (guard: `isCurrentRun`)
- blank rule fields fall back: prompt → `DefaultFormattingPrompt`, schema → four-key default (`status`/`summary`/`actions_taken`/`recommendations`), `MaxTokens<=0` → 1500; NO gorm default tags (explicit false/0 must persist)
- `inferSchema` derives specs from the example; schema instruction appended automatically (never repeat it in the prompt); `validateAgainstSpecs` + one retry, then raw; renders via `output.RenderForSlack` (empty → raw)
- rule editor `hydrateField`/`dehydrateField` keep backend fallbacks authoritative; `output.FormatForSlack` unchanged — keep it

### Runbooks and memory search/write

Runbooks live in Postgres and sync to markdown under `akmatori_data/runbooks/` (mounted read-only). Cross-incident memory lives in markdown under `akmatori_data/memory/` (read-write so memory-writer can edit in place). The agent reaches both via pi-mono subagents (`runbook-searcher`, `memory-searcher`, `memory-writer`).

Rules:
- keep DB state and on-disk runbook files in sync (the runbook service writes both directions)
- the incident-manager prompt invokes `subagent({agent: "runbook-searcher", task: ...})` for SOP lookup — do not introduce direct grep loops in the main agent
- memory recall goes through `memory-searcher`; `memory-writer` persists durable findings near end-of-investigation
- memory-writer is invoked with `{agent: "memory-writer", task}` only — pi-subagents drops extra top-level keys, so scope and incident UUID are the first two header lines of `task` (`Scope: <slug>\nIncident UUID: <uuid>\n\n<reasoning>`); the subagent parses them so `IngestFromDisk` upserts route correctly
- on incident completion the API runs `MemoryService.IngestFromDisk` to materialize new memory files into Postgres (idempotent by scope + `name:` slug); operator-authored rows carry `created_by: operator` in their frontmatter and ingest preserves that

### Slack investigation UX

Rules:
- long investigations use the Slack typing/banner flow, no placeholder reply
- typing state = `assistant.threads.setStatus` plus the hourglass reaction
- progress banner content comes from the latest reasoning line via `SlackProgressStreamer`
- final thread output is summarized to fit Slack byte limits
- mention handling is classify-first: confident operator feedback is stored as memory; other mentions continue the investigation
- feedback ack is split by trigger: non-mention confident feedback → 👍 reaction only; @mention confident feedback → emoji + short text ack (Akmatori only posts thread text when explicitly @mentioned); both route through the injectable `feedbackAcker` seam, best-effort (failures never roll back the persisted memory)

### Memory system

Rules (see also "Runbooks and memory search/write" — write/ingest basics live there):
- memory syncing is scope-aware and manifest-driven; upserts idempotent by `name:` slug + scope
- memory deletions: `memory-writer` accepts `Action: delete <slug>` and writes a tombstone (`name:` + `deleted: true` frontmatter only); `IngestFromDisk` deletes the matching row and the post-batch sync purges the tombstone and prior `<id>-<slug>.md` snapshot; unknown-slug tombstones are a no-op but still trigger sync

### Channels, Integrations, and outbound routing

Operators configure a messaging `Integration` (provider credentials) and `Channel` rows under it. Triggers (alert sources, cron jobs, workspace default) reference Channels by UUID. Slack is implemented; Telegram is a registry stub.

Rules:
- outbound posting goes through `ProviderRegistry.Get(channel.Integration.Provider).PostMessage(...)`, never legacy `SlackSettings.AlertsChannel`
- alert routing: `ChannelService.ResolveForAlertSource(asi)` — explicit `notification_channel_id` wins, else the provider's `is_default_post=true` Channel
- at most one `is_default_post=true` per provider (partial-unique index + service-layer check)
- inbound listening reads `Channel.ExtractionPrompt` + the `ProcessBotMessages`/`ProcessHumanMessages` source gates (`slack.go` dispatch); `process_bot_messages` backfills true on upgrade (preMigrate in db.go)
- `Channel.CanPost`/`CanListen` gate which triggers may reference a channel; `can_listen=true, can_post=false` = silent listener: alerts investigated, results UI-only, no replies/reactions/banner (listener flow + `incidentThreadPostable`)
- the `slack_channel` AlertSourceInstance type is deprecated and UI-hidden; do not reintroduce it
- Telegram requests surface `ErrNotImplemented` from the registry — never silently no-op

### Alert correlation gate

Before spawning a new incident, `AlertHandler` runs `AlertCorrelator.Correlate` to ask the LLM whether the incoming alert belongs to a recent open or monitor-mode incident. On a confident match, `LinkAlertToIncident` is called instead of `SpawnIncidentManager`.

Rules:
- gate is flag-gated (`AlertCorrelationEnabled` in `GeneralSettings`, default false); when disabled, no LLM call and all alerts spawn normally (fail-open)
- `NewAlertCorrelator(caller, db)`, wired via `alertHandler.SetAlertCorrelator(c)`; reads config live (no restart needed)
- both `processAlert` and `ProcessAlertFromListenerChannel` wrap evaluate-and-spawn in `h.spawnGroup.Do(key, ...)`; singleflight followers are no-ops — the partial-unique index on `alerts` handles burst dedup
- confident match → `LinkAlertToIncident(ctx, incidentUUID, sourceUUID, alert, confidence, reasoning)` attaches the alert row (persisting `Correlated`, `CorrelationConfidence`, `CorrelationReasoning`), extends `monitor_until` for monitor incidents, spawns nothing
- no-match or error (fail-open) → `SpawnIncidentManager` then `InsertFiringAlert`; resolved alerts go to `processResolvedAlert`
- `fetchCandidates` single query: `source_kind='alert' AND (status IN ('pending','running','diagnosed') OR (status='monitor' AND monitor_until >= NOW()) OR (status='completed' AND EXISTS unresolved firing alert))`, `ORDER BY started_at DESC LIMIT 25`; the completed clause covers incidents held out of monitor mode by a still-firing alert
- `ErrWorkerNotConnected` is fail-open (alert spawns normally)
- hallucination guard: any UUID not in the fetched candidate set forces `Correlated=false`
- `CorrelationConfig` holds only `Enabled bool`; `correlationMaxCandidates=25` and `correlationThreshold=0.7` are package-level constants
- alert fingerprint: `ComputeAlertFingerprint(sourceUUID, lower(alertName), lower(targetHost))` stored as `alert_fingerprint` (32-char sha256) on each `Incident`

### Alert monitor mode

After an alert-sourced incident completes, it enters `monitor` status for a configurable window so that recurrences are correlated rather than spawning duplicate investigations.

Rules:
- `UpdateIncidentComplete` sets `status=monitor` and `monitor_until = completedAt + GetAlertMonitorWindow()` for all `source_kind='alert'` incidents; non-alert incidents (cron, etc.) are unaffected
- `AlertMonitorWindowMinutes` is configured in `GeneralSettings` (default 60, valid 1–10080); read via `gs.GetAlertMonitorWindow()`; exposed in `PUT /api/settings/general`
- `processResolvedAlert` (tx + row lock): finds the `alerts` row by `source_fingerprint` (then `fingerprint` fallback), marks it `resolved_at=now`; when no firing alerts remain on a completed/monitor incident, sets `monitor_until = min(monitor_until, resolved_at + window)`; no-match is logged and dropped
- `InsertFiringAlert` inserts the initial `alerts` row (status=firing) for a newly spawned incident
- `LinkAlertToIncident` attaches an alert to an existing incident; extends `monitor_until` if the incident is in monitor status
- manifest capped at `manifestMaxEntriesPerScope` (150) entries per scope by `UpdatedAt` descending

### Post-investigation incident merge

`IncidentMerger` (`internal/services/incident_merger.go`): when an alert-sourced investigation completes, a one-shot LLM compares its diagnosed root cause against earlier investigated incidents and merges the newer incident into the earlier survivor on a confident match.

Rules:
- flag-gated (`IncidentMergeEnabled` in `GeneralSettings`, default false, read live); fail-open everywhere; fired as a detached goroutine from `UpdateIncidentComplete` via `SkillService.SetIncidentMerger`
- candidates: alert-sourced `completed`/`monitor`, non-empty `response`, `completed_at` within 24h, `started_at` earlier than the completing incident (newer→older only), LIMIT 25; `mergeThreshold=0.8` + hallucination guard
- merge tx: lock both rows in UUID order, revalidate statuses, re-point `alerts` rows (safe: `uniq_firing_alert` is global), extend survivor `monitor_until`, set merged row `status=merged` + `merged_into_uuid`; merged incidents drop out of all candidate pools
- Slack: best-effort note only in the merged incident's thread; failure never rolls back the merge
- `LinkAlertToIncident` follows `merged_into_uuid` (bounded hops, each row locked) so a correlator verdict targeting a just-merged incident attaches to the survivor

### Alert sources and webhook adapters

Webhook alert sources are still `AlertSourceInstance` rows, while message destinations are Channels. Keep those responsibilities separate.

Rules:
- `GET /api/alert-source-types` hides deprecated types; `slack_channel` exists only for historical rows
- creating deprecated `slack_channel` sources must fail; inbound listening belongs to `can_listen=true` Channels
- `notification_channel_uuid` is optional on alert sources; when set, resolve to a post-capable Channel before create/update
- webhook handlers: fetch instance, reject disabled rows, find adapter by source type, validate secret, then parse body
- adapter integration tests: real `AlertService` + real adapter for at least one happy, bad-secret, and malformed-payload path

### Incidents tool (built-in, credential-less)

The `incidents` tool exposes `incidents.list` and `incidents.get` for read-only access to Akmatori's own incident records. It is the only built-in tool that queries the gateway's own DB connection (`database.DB`) directly rather than proxying to an external service.

Rules:
- `EnsureToolTypes()` seeds both the `ToolType` and a single `ToolInstance` (logical name `"incidents"`, Name `"Incidents"`, empty Settings) so the tool appears in all pickers with zero operator configuration
- the seeded instance never requires credentials — do not add auth fields to it
- the tool is registered in `registry.go` via `registerIncidentsTools()` with no rate limiter
- `incidents` is in `builtInToolNamespaces`; the auth allowlist entry shape is `{ToolType: "incidents"}` (no InstanceID/LogicalName)
- `List` returns summary fields only (no `full_log`/`response`); `Get` returns the full record with `full_log` truncated to 50,000 bytes
- when adding another credential-less built-in tool, follow the same seed pattern in `EnsureToolTypes()` and the same `registerXxxTools()` pattern in `registry.go`

### Cron jobs

Cron jobs run on a per-job schedule and always execute as a full agent investigation under the `cron-agent` system skill. The legacy `oneshot` mode and `description` field are gone.

Rules:
- every cron tick spawns the `cron-agent` skill via `SpawnAgentInvocation`, creating an `Incident` with `source_kind="cron"` and `source_uuid=<cron_job.uuid>`
- each cron carries its own `Tools []ToolInstance` allowlist (m2m via `cron_job_tools`) mapped to `ToolAllowlistEntry` — global skill/tool settings are NOT inherited
- `cron-agent` is a system skill (`IsSystem=true`), exempt from SKILL.md generation; prompt surfaces via `skill_prompt_service`
- system crons seed via `seedSystemCronJobs()` with per-seed `Enabled` defaults (`Dreaming` enabled, `improvement-evaluator` disabled); existing system rows are left untouched (operator edits survive restarts); legacy `memory-curator` rows rename to `Dreaming` in place; `DeleteJob` on a system row returns `ErrSystemCronImmutable` (409) — editable, not deletable
- `post_results` (default true) gates channel posting; when false the tick skips channel/provider resolution entirely and records its outcome only on the Incident row
- crons spawn with ONLY the `cron-agent` root prompt — do NOT wrap the task with `executor.PrependGuidance` (incident-triage framed); the task is prefixed only with the current UTC time
- tool-less crons (e.g. `Dreaming`) MUST send an explicit empty allowlist; `ToolAllowlist` on `WebSocketMessage` is intentionally NOT `omitempty` — `[]` means reject-all, `null` means allow-all
- the seeded `Dreaming` cron dedupes `/akmatori/memory/global/` via memory-writer tombstones
- cron expressions are validated at write time (invalid → 400); `CronRunner` survives tick failures, recording `LastRunStatus=error` + `LastRunError`
- manual fire is `POST /api/cron-jobs/{uuid}/run`; CRUD reloads the runner without restart
- `CronJobTool` is the explicit join-table model; include it alongside `CronJob` in all `AutoMigrate` calls and test schemas — GORM does not auto-discover it from the `many2many:` tag
- `SpawnAgentInvocation(rootSkillName, ctx)` in `incident_service.go` is the shared entrypoint for root-skill agent runs; new system root skill = seed the skill row in `db.go`, add its prompt constant alongside `DefaultCronAgentPrompt`, add the `rootSkillName` case to `GetSkillPrompt`/`UpdateSkillPrompt`/`RegenerateSkillMd`/`RegenerateAllSkillMds`/`rootSkillHeader`. Current: `incident-manager`, `cron-agent`, `proposal-editor`

### Self-improving proposals

The `improvement-evaluator` system cron reviews recent incidents + operator feedback and emits `Proposal` rows (kinds: `runbook_new/update`, `memory_new/update`, `cron_new/update`, `skill_prompt_update`) via the credential-less `proposals` gateway tool. Operators review them in the Proposals tab, refine via chat, and approve (auto-apply) or reject.

Rules:
- statuses: `pending | approved | rejected | apply_failed | superseded`; chat never changes status; re-approving `apply_failed` retries
- `ProposalService.Approve` applies through the existing managers (`RunbookManager`, `MemoryManager.UpsertByName`, `CronJobManager`, `SkillManager.UpdateSkillPrompt`) — never raw DB writes; status write last so a failed apply never yields an approved row
- staleness: gateway captures `current_snapshot` at create (runbook/memory/cron); skill prompts backfilled lazily by the API on first list/get (disk-only in the API container); approve compares live vs snapshot, mismatch → `superseded` + `ErrProposalStale` (409)
- `skill_prompt_update` targets non-system skills ONLY (enforced at gateway create AND apply — `UpdateSkillPrompt` silently no-ops for system skills); `cron_new` applies `Enabled=false`, no channel
- proposal chat = fresh `StartIncident` per turn on the same chat incident (`source_kind="proposal"`, root skill `proposal-editor`), NEVER `ContinueIncident`; proposal state + transcript rebuilt into each task; transcript in `proposal_chat_messages` written by the handler; allowlist = `ChatToolAllowlist()` (incidents+proposals; non-nil empty on failure); no `executor.PrependGuidance`
- `SeedImprovementEvaluatorCron()` runs from `main.go` AFTER `EnsureToolTypes()` (attaches the seeded tool instances); seeds disabled, same preserve-edits/shadow-check semantics as `SeedSystemCronJobs`
- gateway `proposals.create` dedups against pending rows (kind+target_ref, or kind+title for `*_new`) and drops hallucinated `source_incident_uuids`; `proposals` is in `builtInToolNamespaces` and `credentiallessNamespaces`
- key files: `internal/database/models_proposals.go`, `internal/services/proposal_service.go`, `internal/handlers/api_proposals.go`, `mcp-gateway/internal/tools/proposals/`, `web/src/pages/Proposals.tsx` + `ProposalDetail.tsx`
- incident feedback UI (`IncidentFeedbackStrip`, Response tab) posts to `POST /api/incidents/{uuid}/feedback`; feedback memories feed the next evaluator run

## Important Files by Responsibility

### Handlers

- `internal/handlers/agent_ws.go` - worker transport and message types
- `internal/handlers/api.go` - REST route wiring
- `internal/handlers/api_formatting_rules.go` - formatting rules CRUD + reorder (`/api/settings/formatting` is 410)
- `internal/handlers/api_integrations.go` - Integrations CRUD
- `internal/handlers/api_channels.go` - Channels CRUD (with filters)
- `internal/handlers/api_cron_jobs.go` - Cron jobs CRUD + manual `/run` fire
- `internal/handlers/alert_processor.go` - main investigation path; sets `source_kind`/`source_uuid`
- `internal/handlers/api_incidents.go` - incidents list (GET, paginated, enriched with `alert_count`/`first_seen`/`last_seen`/`trend`); `GET /api/incidents/{uuid}/alerts` returns alert rows ordered by `fired_at ASC`; accepts `?trend_window=1h|3h`
- `internal/handlers/alert_slack.go` - outbound routing via `ChannelService` + `ProviderRegistry`
- `internal/handlers/slack_processor.go` - Slack message and mention handling; reads `Channel.ExtractionPrompt`
- `internal/handlers/slack_progress.go` - reasoning-line streaming for Slack banner

### Services

- `internal/services/interfaces.go` - dependency interfaces used by handlers
- `internal/services/runbook_service.go` - runbook CRUD and DB↔disk sync
- `internal/services/response_formatter.go` - rule-driven response rewrite stage (`FormatForFlow`)
- `internal/services/formatting_rule_matcher.go` - `FormatFlow`, `MatchFormattingRule`, `BuildFormatFlow`
- `internal/services/formatter_schema.go` - schema inference (`inferSchema`, `buildSchemaInstruction`, `validateAgainstSpecs`) and built-in default schema example
- `internal/output/schema_render.go` - `RenderForSlack(parsed, specs)`: walks `FieldSpec` slice in key order to produce Slack mrkdwn; defines the exported `FieldSpec` type used by both renderer and schema helpers
- `internal/services/memory_service.go` - cross-incident memory CRUD, DB↔disk sync, and `IngestFromDisk`
- `internal/services/title_generator.go` - one-shot title generation
- `internal/services/slack_summarizer.go` - Slack-safe final output compression
- `internal/services/alert_correlator.go` - LLM-powered correlation gate; defines `CorrelationConfig`, `CorrelationVerdict`, and `AlertCorrelator`
- `internal/services/alert_fingerprint.go` - `ComputeAlertFingerprint(sourceUUID, alertName, targetHost)` — stable 32-char hex digest for correlation candidate pre-filtering
- `internal/database/models_alerts.go` - `Alert` model: one row per firing/resolved alert linked to an incident; `AlertStatus` constants; unique index prevents duplicate concurrent fires
- `internal/services/channel_service.go` - Integrations/Channels CRUD, `ResolveDefault`, `ResolveForAlertSource`
- `internal/services/cron_runner.go` - cron scheduler, per-cron agent tick path, reload-on-CRUD
- `internal/services/incident_service.go` - `SpawnIncidentManager` / `SpawnAgentInvocation`, AGENTS.md generation (`generateAgentsMd`), per-root-skill prompt injection
- `internal/messaging/` - `Provider`, `ProviderRegistry`, slack provider, telegram stub
- `akmatori_data/agents/` - `runbook-searcher`, `memory-searcher`, `memory-writer` subagent definitions

### Agent worker

- `agent-worker/src/orchestrator.ts` - routing of worker message types
- `agent-worker/src/agent-runner.ts` - pi-mono session lifecycle
- `agent-worker/src/oneshot-llm.ts` - single-call LLM helper
- `agent-worker/src/gateway-tools.ts` - tool registration and `gateway_call`
- `agent-worker/src/tool-output-formatter.ts` - streamed tool formatting

### Frontend

- `web/src/components/settings/FormattingRulesSection.tsx` - formatting rules list/editor (shared fields in `FormattingConfigFields.tsx`)
- `web/src/components/settings/formattingSettingsHelpers.ts` - formatter default hydrate/dehydrate helpers; keep constants aligned with Go defaults

## Code Patterns

### Prefer interfaces at handler boundaries

Handlers should depend on interfaces from `internal/services/interfaces.go`. If you add a new service dependency, wire it behind an interface first so handlers stay testable.

### Keep one-shot features provider-agnostic

If a feature only needs a single completion, do not spin up a full agent session. Route it through `OneShotLLMCaller` and keep fallback behavior explicit.

### Keep Slack output budgeted

Slack has hard byte limits. Any new Slack-facing summary or banner text must truncate safely and degrade cleanly.

### Keep tool routing indirect

Do not teach agents to call tool implementations directly. They should go through `gateway_call`, with routing handled by logical instance names or instance hints. (`ToolAllowlist` JSON-tag rule: see the cron section — never `omitempty`.)

### Keep messaging provider-agnostic

Do not call Slack APIs directly from handlers or services — resolve a `Channel` and go through `ProviderRegistry.Get(...)` (`PostMessage` / `PostThreadReply` / `UpdateMessage`). New providers register in `internal/messaging/` and are picked up by provider name.

### Preserve graceful degradation

Akmatori intentionally keeps working when optional AI pieces fail. When adding AI-dependent behavior, define the fallback path at the same time.

## SDK Notes (`@earendil-works/pi-coding-agent`)

- Current versions: pi-coding-agent, pi-ai, pi-agent-core `0.80.6`; pi-subagents `0.34.0`; undici `^8`
- pi-ai 0.80.0 root is core-only (types stay at root): `complete` from `/compat`, `getBuiltinModel` from `/providers/all`; the new Models API rejects akmatori's synthesized custom-provider specs (compat dispatches on `model.api`)
- models.json `apiKey` needs `$ENV_VAR` syntax — bare names are literals since pi 0.79.4
- Project trust (0.79.0+): headless child `pi` treats workspaces as untrusted (we write `<workDir>/.pi/settings.json`) — children use the global `<agentDir>/settings.json` pin and never run workspace `.pi/extensions`; never set `defaultProjectTrust: "always"`
- Thinking level `max` (above `xhigh`); list mirrored in worker (agent-runner/types/orchestrator), Go (`models_settings.go`, `api_settings_llm.go`), web (types, `LLMSettingsSection.tsx`)
- pi-subagents reads `<agentDir>/extensions/subagent/config.json` (strict JSON); repo ships it with `toolDescriptionMode: "compact"`
- Use `ModelRegistry.inMemory(authStorage)` (no public constructor)
- `gateway-tools.ts` tool factories return `defineTool({...})`
- The bash tool stays local (TypeScript variance friction)
- import `typebox` from `typebox`, not `@sinclair/typebox`
- `DefaultResourceLoader` requires `agentDir` (`getAgentDir()` in production and mocks)
- Provider SDKs are lazy-loaded; Akmatori forwards retry/timeout settings (long provider timeouts for slow models)
- `setRuntimeApiKey` bypasses `resolveConfigValue` — operator API keys with literal `$` characters are safe
- `compat.forceAdaptiveThinking: true` is set in synthesized model specs for providers resolving to `anthropic-messages` (`minimax`, fallback Anthropic-compatible endpoints) to enable extended-thinking wire format
- Subagent support: `agent-runner.ts` keeps `noExtensions: false` + `additionalExtensionPaths: ["/opt/pi-extensions/pi-subagents"]` (baked into the image; `~/.pi/agent/extensions` is an operator mount); the image needs `pi` on `PATH` plus `ripgrep`/`fzf`
- Subagent subprocess auth: the child `pi` has independent AuthStorage — `agent-runner.ts` mirrors the active API key into `process.env[<provider env var>]`; subagent `.md` files omit `model:` so children inherit the parent provider/model

## Testing Rules

### Minimum verification

After changing code, run the smallest relevant test target and then the broad suite required by the change.

| Area changed | Primary command |
|---|---|
| Go backend | `make test` |
| Alert adapters | `make test-adapters` |
| MCP Gateway | `make test-mcp` |
| Agent worker | `make test-agent` |
| Frontend | `make test-web` |
| Pre-commit full gate | `make verify` |

Extra rule:
- before quoting coverage, re-run `go test -coverprofile=coverage.out ./...`

### Current testing focus

Historically weak or regression-prone areas:
- `internal/handlers`
- `internal/services`
- `internal/slack`
- main-module database logic
- `mcp-gateway/internal/tools`
- `mcp-gateway/internal/tools/zabbix`

## Rebuild Rules

Rebuild the affected container after runtime changes.

Source maintainers use the dev override (`docker-compose.dev.yml`) so local `build:` blocks take effect. GHCR-image installs use only the base file (`docker compose pull && docker compose up -d`) — never run `build` against a release install.

Command: `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build <svc> && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d <svc>` with `<svc>`:

| Area changed | `<svc>` |
|---|---|
| API (`cmd/`, `internal/`) | `akmatori-api` |
| MCP Gateway | `mcp-gateway` |
| Agent worker | `akmatori-agent` |
| Frontend | `frontend` |

## Recent Features and Docs-Sensitive Areas

- session resume is NOT used anywhere — Slack launches and proposal chat start fresh agent sessions per turn
- `/api/settings/slack` returns 410 Gone

## When Editing This File

- keep it concise and operational
- prefer rules over long examples
- remove duplicates instead of appending similar guidance
- verify size before committing: `wc -c CLAUDE.md`
- hard limit: `CLAUDE.md` must stay under 30000 bytes
