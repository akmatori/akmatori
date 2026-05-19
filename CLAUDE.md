# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform for SRE teams. It ingests alerts from monitoring systems, analyzes them with multi-provider LLM agents, and can execute remediation through approval-gated tools.

## Stack and Runtime

- Docker deployment: API, Agent Worker, MCP Gateway, PostgreSQL
- Backend: Go 1.24+
- Agent Worker: Node.js 22+ / TypeScript with `@earendil-works/pi-coding-agent` (`v0.74.0`)
- Frontend: React 19 + TypeScript + Vite + Tailwind
- Database: PostgreSQL 16 + GORM
- LLM providers: Anthropic, OpenAI, Google, OpenRouter, custom/on-prem

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

Rules:
- API frame type is `oneshot_llm_request`
- Worker replies with `oneshot_llm_response`
- Go callers should depend on `services.OneShotLLMCaller`, not concrete worker code
- If the worker is disconnected, callers must fail gracefully and use deterministic fallbacks

### Response formatting

Formatting settings live behind `/api/settings/formatting`.

Rules:
- `ResponseFormatter` is optional and must be passthrough-safe
- skip formatting on explicit error responses
- preserve raw fallback behavior when worker or LLM formatting fails
- handler wiring happens via `SetResponseFormatter(...)`

### Runbooks and memory search/write

Runbooks live in Postgres and sync to markdown under `akmatori_data/runbooks/` (mounted read-only into the agent). Cross-incident memory lives in markdown under `akmatori_data/memory/` (mounted read-write so the memory-writer subagent can edit in place). The agent reaches both via pi-mono subagents (`runbook-searcher`, `memory-searcher`, `memory-writer`).

Rules:
- keep DB state and on-disk runbook files in sync (the runbook service writes both directions)
- the incident-manager prompt invokes `subagent({agent: "runbook-searcher", task: ...})` for SOP lookup — do not introduce direct grep loops in the main agent
- memory recall goes through `memory-searcher`; durable findings get written by `memory-writer` near end-of-investigation
- the memory-writer subagent is invoked with `{agent: "memory-writer", task}` only — pi-subagents silently drops any extra top-level keys, so scope and incident UUID are embedded as the first two header lines of `task` (`Scope: <slug>\nIncident UUID: <uuid>\n\n<reasoning>`); the subagent parses them out before writing so `IngestFromDisk` upserts route to the correct row
- on incident completion the API runs `MemoryService.IngestFromDisk` to materialize new memory files into Postgres (idempotent by scope + `name:` slug); operator-authored rows carry `created_by: operator` in their frontmatter and ingest preserves that

### Slack investigation UX

Rules:
- long investigations use the Slack typing/banner flow, not a placeholder reply
- typing state is driven by `assistant.threads.setStatus` plus the hourglass reaction
- progress banner content comes from the latest reasoning line via `SlackProgressStreamer`
- final thread output is summarized to fit Slack byte limits
- mention handling is classify-first: confident operator feedback is stored as memory; other mentions continue the investigation

### Memory system

Rules:
- incident learnings are recorded directly by the `memory-writer` subagent into `akmatori_data/memory/<scope>/`
- `MemoryService.IngestFromDisk` re-materializes those files into Postgres after each incident
- memory syncing is scope-aware and manifest-driven
- memory upserts must stay idempotent by `name:` slug + scope

### Channels, Integrations, and outbound routing

Operators configure a messaging `Integration` (provider credentials) and one or more `Channel` rows under it. Triggers — alert sources, cron jobs, the workspace default — reference Channels by UUID. Slack is implemented; Telegram is a registry stub so the data model is ready when it lands.

Rules:
- outbound posting goes through `ProviderRegistry.Get(channel.Integration.Provider).PostMessage(ctx, channel, ...)`, never the legacy `SlackSettings.AlertsChannel`
- alert routing uses `ChannelService.ResolveForAlertSource(asi)`: explicit `notification_channel_id` wins, otherwise fall back to the provider's `is_default_post=true` Channel
- at most one `is_default_post=true` per provider (enforced by a partial-unique index and a service-layer check)
- inbound listening reads `Channel.ExtractionPrompt` and `Channel.ProcessHumanMessages`, not alert-source `Settings` JSONB; `slack_processor.go` must honour this
- `Channel.CanPost` / `Channel.CanListen` capability flags gate which triggers may reference a channel
- the `slack_channel` AlertSourceInstance type is deprecated and hidden from the UI; do not reintroduce it for new flows
- Telegram requests must surface `ErrNotImplemented` from the registry — never silently no-op

### Cron jobs

Cron jobs run on a per-job schedule, target a Channel, and execute either a one-shot LLM call or a full agent investigation.

Rules:
- `oneshot` mode: route through `OneShotLLMCaller` and post the formatted result to `Channel` via the registry
- `agent` mode: spawn the incident-manager skill mirroring `alert_processor.go`; create an `Incident` with `source_kind="cron"` and `source_uuid=<cron_job.uuid>` so provenance is queryable
- cron expressions are validated at write time; invalid schedules surface as 400
- `CronRunner` survives tick failures and records `LastRunStatus=error` + `LastRunError`; never let one bad job take the runner down
- crons inherit global LLM/skill/tool settings — per-cron overrides are intentionally out of scope
- manual fire is `POST /api/cron-jobs/{uuid}/run`; CRUD reloads the runner so schedule changes apply without restart

## Important Files by Responsibility

### Handlers

- `internal/handlers/agent_ws.go` - worker transport and message types
- `internal/handlers/api.go` - REST route wiring
- `internal/handlers/api_settings_formatting*.go` - formatting settings API
- `internal/handlers/api_integrations.go` - Integrations CRUD
- `internal/handlers/api_channels.go` - Channels CRUD (with filters)
- `internal/handlers/api_cron_jobs.go` - Cron jobs CRUD + manual `/run` fire
- `internal/handlers/alert_processor.go` - main investigation path; sets `source_kind`/`source_uuid`
- `internal/handlers/alert_slack.go` - outbound routing via `ChannelService` + `ProviderRegistry`
- `internal/handlers/slack_processor.go` - Slack message and mention handling; reads `Channel.ExtractionPrompt`
- `internal/handlers/slack_progress.go` - reasoning-line streaming for Slack banner

### Services

- `internal/services/interfaces.go` - dependency interfaces used by handlers
- `internal/services/runbook_service.go` - runbook CRUD and DB↔disk sync
- `internal/services/response_formatter.go` - optional response rewrite stage
- `internal/services/memory_service.go` - cross-incident memory CRUD, DB↔disk sync, and `IngestFromDisk`
- `internal/services/title_generator.go` - one-shot title generation
- `internal/services/slack_summarizer.go` - Slack-safe final output compression
- `internal/services/channel_service.go` - Integrations/Channels CRUD, `ResolveDefault`, `ResolveForAlertSource`
- `internal/services/cron_runner.go` - cron scheduler, oneshot + agent tick paths, reload-on-CRUD
- `internal/messaging/` - `Provider`, `ProviderRegistry`, slack provider, telegram stub
- `akmatori_data/agents/` - `runbook-searcher`, `memory-searcher`, `memory-writer` subagent definitions

### Agent worker

- `agent-worker/src/orchestrator.ts` - routing of worker message types
- `agent-worker/src/agent-runner.ts` - pi-mono session lifecycle
- `agent-worker/src/oneshot-llm.ts` - single-call LLM helper
- `agent-worker/src/gateway-tools.ts` - tool registration and `gateway_call`
- `agent-worker/src/tool-output-formatter.ts` - streamed tool formatting

## Code Patterns

### Prefer interfaces at handler boundaries

Handlers should depend on interfaces from `internal/services/interfaces.go`. If you add a new service dependency, wire it behind an interface first so handlers stay testable.

### Keep one-shot features provider-agnostic

If a feature only needs a single completion, do not spin up a full agent session. Route it through `OneShotLLMCaller` and keep fallback behavior explicit.

### Keep Slack output budgeted

Slack has hard byte limits. Any new Slack-facing summary or banner text must truncate safely and degrade cleanly.

### Keep tool routing indirect

Do not teach agents to call tool implementations directly. They should go through `gateway_call`, with routing handled by logical instance names or instance hints.

### Keep messaging provider-agnostic

Do not call Slack APIs directly from handlers or services. Resolve a `Channel`, fetch the provider through `ProviderRegistry.Get(...)`, and call `PostMessage` / `PostThreadReply` / `UpdateMessage`. When you add a new messaging provider, register it in `internal/messaging/` and the rest of the system should pick it up by provider name without further changes.

### Preserve graceful degradation

Akmatori intentionally keeps working when optional AI pieces fail. When adding AI-dependent behavior, define the fallback path at the same time.

## SDK Notes (`@earendil-works/pi-coding-agent`)

- As of v0.74.0, pi-mono packages moved from the `@mariozechner/*` scope to `@earendil-works/*` (pi-coding-agent, pi-ai, pi-agent-core)
- Use `ModelRegistry.inMemory(authStorage)`; there is no public `ModelRegistry` constructor
- Tool factories in `gateway-tools.ts` should return `defineTool({...})`
- The bash tool remains the local exception because of TypeScript variance friction
- `typebox` is imported from `typebox`, not `@sinclair/typebox`
- `DefaultResourceLoader` requires `agentDir`; pass `getAgentDir()` in production and mocks
- Provider SDKs are lazy-loaded; Akmatori forwards retry and timeout settings and uses long provider timeouts for slow models
- Subagent support: `agent-runner.ts` keeps `noExtensions: false` and passes `additionalExtensionPaths: ["/opt/pi-extensions/pi-subagents"]`. The pi-subagents extension is baked into the image at that path; `~/.pi/agent/extensions` is a thin operator-supplied mount. The agent image must have `pi` on `PATH` and `ripgrep`/`fzf` installed for subagent recon to function
- Subagent subprocess auth: pi-subagents spawns each subagent in a child `pi` process whose AuthStorage is independent — `agent-runner.ts` mirrors the active API key into `process.env[<provider env var>]` so the child inherits it. Subagent `.md` files intentionally omit `model:` so the child inherits the parent provider/model (hard-coding a model name would break non-Anthropic deployments)

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

Maintainers running from source use the dev override (`docker-compose.dev.yml`) so the local `build:` blocks take effect. End users pulling published GHCR images use only the base `docker-compose.yml` (`docker compose pull && docker compose up -d`) — never run these `build` commands against a release install.

| Area changed | Rebuild |
|---|---|
| API (`cmd/`, `internal/`) | `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build akmatori-api && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d akmatori-api` |
| MCP Gateway | `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build mcp-gateway && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d mcp-gateway` |
| Agent worker | `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build akmatori-agent && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d akmatori-agent` |
| Frontend | `docker-compose -f docker-compose.yml -f docker-compose.dev.yml build frontend && docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d frontend` |

## Recent Features and Docs-Sensitive Areas

Keep this file aligned with these current realities:
- runbook and cross-incident memory recall run through pi-mono subagents (`runbook-searcher`, `memory-searcher`); QMD is gone
- the agent records durable findings via the `memory-writer` subagent; the API re-ingests `akmatori_data/memory/` into Postgres at incident completion
- fresh Slack skill launches start fresh agent sessions unless the flow explicitly resumes
- response formatting settings are live and backed by `/api/settings/formatting`
- one-shot LLM calls share the worker transport and current provider settings
- Slack loading banners use real reasoning lines instead of generic placeholder text
- messaging is now Integrations + Channels; outbound posting routes through `ProviderRegistry`; the legacy `SlackSettings.AlertsChannel` fallback is gone and `/api/settings/slack` returns 410 Gone
- cron jobs (`/api/cron-jobs`) schedule oneshot LLM ticks or full agent investigations against a Channel; `CronRunner` boots from `cmd/akmatori/main.go`

## When Editing This File

- keep it concise and operational
- prefer rules over long examples
- remove duplicates instead of appending similar guidance
- verify size before committing: `wc -c CLAUDE.md`
- hard limit: `CLAUDE.md` must stay under 30000 bytes
