# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform for SRE teams. It ingests alerts from monitoring systems, analyzes them with multi-provider LLM agents, and can execute remediation through approval-gated tools.

## Stack and Runtime

- Docker deployment: API, Agent Worker, MCP Gateway, PostgreSQL, QMD
- Backend: Go 1.24+
- Agent Worker: Node.js 22+ / TypeScript with `@mariozechner/pi-coding-agent` (`v0.73.0`)
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
internal/output/            structured agent output parsing
internal/services/          business logic and interfaces
internal/setup/             first-run bootstrap
internal/slack/             Slack client, typing, reload logic
internal/testhelpers/       builders, fixtures, mocks
agent-worker/src/           worker orchestrator and tool bridge
mcp-gateway/internal/       tool auth, rate limiting, MCP proxy, tool impls
qmd/                        runbook search sidecar
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
- feedback classification and memory extraction helpers

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

### Runbooks and QMD

Runbooks are stored in Postgres and synced to markdown files.

Rules:
- after runbook writes, trigger QMD reindex with `POST /update`
- keep DB state and on-disk runbook files in sync
- if you change runbook sync behavior, update tests around QMD reindexing

### Slack investigation UX

Rules:
- long investigations use the Slack typing/banner flow, not a placeholder reply
- typing state is driven by `assistant.threads.setStatus` plus the hourglass reaction
- progress banner content comes from the latest reasoning line via `SlackProgressStreamer`
- final thread output is summarized to fit Slack byte limits
- mention handling is classify-first: confident operator feedback is stored as memory; other mentions continue the investigation

### Memory system

Rules:
- incident learnings are extracted into long-lived memory through `MemoryExtractor`
- memory syncing is scope-aware and manifest-driven
- memory upserts must stay idempotent by incident identity or semantic name where applicable

## Important Files by Responsibility

### Handlers

- `internal/handlers/agent_ws.go` - worker transport and message types
- `internal/handlers/api.go` - REST route wiring
- `internal/handlers/api_settings_formatting*.go` - formatting settings API
- `internal/handlers/alert_processor.go` - main investigation path
- `internal/handlers/slack_processor.go` - Slack message and mention handling
- `internal/handlers/slack_progress.go` - reasoning-line streaming for Slack banner

### Services

- `internal/services/interfaces.go` - dependency interfaces used by handlers
- `internal/services/runbook_service.go` - runbook CRUD plus QMD sync and reindex
- `internal/services/response_formatter.go` - optional response rewrite stage
- `internal/services/memory_service.go` - cross-incident memory CRUD and sync
- `internal/services/memory_extractor.go` - memory distillation from completed incidents
- `internal/services/title_generator.go` - one-shot title generation
- `internal/services/slack_summarizer.go` - Slack-safe final output compression

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

### Preserve graceful degradation

Akmatori intentionally keeps working when optional AI pieces fail. When adding AI-dependent behavior, define the fallback path at the same time.

## SDK Notes (`@mariozechner/pi-coding-agent`)

- Use `ModelRegistry.inMemory(authStorage)`; there is no public `ModelRegistry` constructor
- Tool factories in `gateway-tools.ts` should return `defineTool({...})`
- The bash tool remains the local exception because of TypeScript variance friction
- `typebox` is imported from `typebox`, not `@sinclair/typebox`
- `DefaultResourceLoader` requires `agentDir`; pass `getAgentDir()` in production and mocks
- Provider SDKs are lazy-loaded; Akmatori forwards retry and timeout settings and uses long provider timeouts for slow models

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

Rebuild the affected container after runtime changes:

| Area changed | Rebuild |
|---|---|
| API (`cmd/`, `internal/`) | `docker-compose build akmatori-api && docker-compose up -d akmatori-api` |
| MCP Gateway | `docker-compose build mcp-gateway && docker-compose up -d mcp-gateway` |
| Agent worker | `docker-compose build akmatori-agent && docker-compose up -d akmatori-agent` |
| Frontend | `docker-compose build frontend && docker-compose up -d frontend` |
| QMD | `docker-compose build qmd && docker-compose up -d qmd` |

## Recent Features and Docs-Sensitive Areas

Keep this file aligned with these current realities:
- QMD is on `v2.1.0`; runbook sync triggers `/update` reindexing
- fresh Slack skill launches start fresh agent sessions unless the flow explicitly resumes
- response formatting settings are live and backed by `/api/settings/formatting`
- one-shot LLM calls share the worker transport and current provider settings
- Slack loading banners use real reasoning lines instead of generic placeholder text
- cross-incident memory extraction and scoped MEMORY sync are part of the normal incident lifecycle

## When Editing This File

- keep it concise and operational
- prefer rules over long examples
- remove duplicates instead of appending similar guidance
- verify size before committing: `wc -c CLAUDE.md`
- hard limit: `CLAUDE.md` must stay under 30000 bytes
