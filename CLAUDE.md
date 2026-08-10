# Claude Code Instructions for Akmatori

Akmatori is an AI-powered AIOps platform for SRE teams. It ingests alerts from monitoring systems,
analyzes them with multi-provider LLM agents, and can execute remediation through approval-gated tools.

## Stack and Runtime

- Docker deployment: API, Agent Worker, MCP Gateway, PostgreSQL
- Backend: Go 1.25 · Database: PostgreSQL 16 + GORM
- Agent Worker: Node.js 22+ / TypeScript with `@earendil-works/pi-coding-agent` (`v0.82.1`)
- Frontend: React 19 + TypeScript + Vite + Tailwind
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

Backend flow:

1. Adapters or Slack create/continue incidents.
2. Handlers call services through interfaces from `internal/services/interfaces.go`.
3. Agent runs happen through the worker WebSocket.
4. Tool execution goes through MCP Gateway with incident-scoped auth.
5. Final output is parsed, optionally reformatted, stored, and sent back to UI/Slack.

Entry points: `internal/handlers/agent_ws.go` (worker transport and message types),
`internal/handlers/api.go` (REST route wiring), `internal/services/interfaces.go` (handler
dependencies), `internal/services/incident_service.go` (agent spawning, AGENTS.md, root-skill prompts).

Detailed subsystem rules live in `.claude/rules/` and load automatically when you touch the matching
files: `alerts-correlation-and-monitor`, `cron-and-root-skills`, `formatting-rules`,
`memory-and-runbooks`, `messaging-channels-slack`, `mcp-gateway-tools`, `oneshot-llm`, `proposals`,
`agent-worker-pi-sdk`.

## Code Patterns

- **Interfaces at handler boundaries.** Handlers depend on `internal/services/interfaces.go`. New
  service dependency → wire it behind an interface first so handlers stay testable.
- **One-shot features stay provider-agnostic.** Single-completion work goes through
  `OneShotLLMCaller`, never a full agent session; keep fallback behavior explicit.
- **Messaging stays provider-agnostic.** Never call Slack APIs from handlers or services — resolve a
  `Channel` and go through `ProviderRegistry.Get(...)`.
- **Tool routing stays indirect.** Agents reach tools via `gateway_call`, never tool implementations.
- **Slack output stays budgeted.** Slack has hard byte limits; new summaries and banners must
  truncate safely.
- **Preserve graceful degradation.** Akmatori keeps working when optional AI pieces fail. Adding
  AI-dependent behavior means defining its fallback path at the same time.

## Testing

Run the smallest relevant test target, then the broad suite the change requires.

| Area changed | Primary command |
|---|---|
| Go backend | `make test` |
| Alert adapters | `make test-adapters` |
| MCP Gateway | `make test-mcp` |
| Agent worker | `make test-agent` |
| Frontend | `make test-web` |
| Pre-commit full gate | `make verify` |

- before quoting coverage, re-run `go test -coverprofile=coverage.out ./...`
- weak/regression-prone areas: `internal/handlers`, `internal/services`, `internal/slack`,
  main-module database logic, `mcp-gateway/internal/tools` (esp. `/zabbix`)

## Rebuilds

Rebuild the affected container after runtime changes. Source maintainers use the dev override
(`docker-compose.dev.yml`) so local `build:` blocks take effect; GHCR-image installs use only the
base file (`docker compose pull && docker compose up -d`) — never run `build` against a release install.

Command (with `-f docker-compose.yml -f docker-compose.dev.yml` on both):
`docker-compose … build <svc> && docker-compose … up -d <svc>`.
Services: API (`cmd/`, `internal/`) → `akmatori-api`; MCP Gateway → `mcp-gateway`; Agent worker →
`akmatori-agent`; Frontend → `frontend`.

## Known Stale Surfaces

- `/api/settings/slack` and `/api/settings/formatting` return 410 Gone
- `docs/CONTRIBUTING.md` and `docs/TOOL_ARCHITECTURE.md` still describe the retired Python
  tool/Quick Start path
- session resume is NOT used — Slack and proposal chat start fresh agent sessions per turn

## When Editing This File

- CLAUDE.md holds always-loaded facts: stack, layout, architecture map, conventions, commands
- subsystem-specific rules belong in `.claude/rules/<topic>.md` with a `paths:` frontmatter glob;
  procedures belong in `.claude/skills/`; "always do X after Y" belongs in a hook
- prefer rules over long examples; remove duplicates instead of appending similar guidance
- hard limit: keep this file under 200 lines (`wc -l CLAUDE.md`)
