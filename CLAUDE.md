# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform that receives alerts from monitoring systems (Zabbix, Alertmanager, PagerDuty, Grafana, Datadog), analyzes them using multi-provider LLM agents (via the pi-mono coding-agent SDK), and executes automated remediation.

## Architecture

- **5-container Docker architecture**: API, Agent Worker, MCP Gateway, PostgreSQL, QMD (runbook search)
- **Backend**: Go 1.24+ (API server, MCP gateway)
- **Agent Worker**: Node.js 22+ / TypeScript using `@mariozechner/pi-coding-agent` SDK (v0.73.0)
- **Frontend**: React 19 + TypeScript + Vite + Tailwind
- **Database**: PostgreSQL 16 with GORM
- **LLM Providers**: Anthropic, OpenAI, Google, OpenRouter, Custom (configured via web UI)

## Key Directories

```
/opt/akmatori/
├── cmd/akmatori/           # Main API server entry point
├── internal/
│   ├── alerts/adapters/    # Alert source adapters (Zabbix, Alertmanager, etc.)
│   ├── alerts/extraction/  # AI-powered alert extraction from free-form text
│   ├── api/                # Request/response helpers, pagination
│   ├── database/           # GORM models and database logic
│   ├── handlers/           # HTTP/WebSocket handlers
│   ├── middleware/         # Auth, CORS middleware
│   ├── output/             # Agent output parsing (structured blocks)
│   ├── logging/           # Structured logging (slog) initialization
│   ├── services/           # Business logic layer (+ interfaces.go for testability)
│   ├── setup/              # Zero-config first-run setup
│   ├── slack/              # Slack integration (Socket Mode, hot-reload)
│   ├── testhelpers/        # Test utilities, builders, mocks
│   └── utils/              # Utility functions
├── agent-worker/           # Node.js/TypeScript agent worker
│   └── src/                # TypeScript source (gateway-client, gateway-tools, script-executor)
├── mcp-gateway/            # MCP protocol gateway (separate Go module)
│   └── internal/
│       ├── auth/           # Per-incident tool authorization (allowlist enforcement)
│       ├── cache/          # Generic TTL cache
│       ├── mcpproxy/       # MCP proxy: connection pool + handler for external MCP servers
│       ├── ratelimit/      # Token bucket rate limiter
│       └── tools/          # SSH, Zabbix, VictoriaMetrics, PostgreSQL, ClickHouse, Grafana, Catchpoint, PagerDuty, NetBox, Kubernetes, and HTTP connector implementations
├── web/                    # React frontend
├── qmd/                    # QMD search sidecar (Dockerfile, config, entrypoint)
├── docs/                   # OpenAPI specs (swagger at /api/docs)
└── tests/fixtures/         # Test payloads and mock data
```

## CRITICAL: Always Verify Changes with Tests

**After ANY code change, run the appropriate test command:**

| After changing... | Run command |
|-------------------|-------------|
| Alert adapters (`internal/alerts/adapters/`) | `make test-adapters` |
| MCP Gateway (`mcp-gateway/`) | `make test-mcp` |
| Agent worker (`agent-worker/`) | `make test-agent` |
| Web frontend (`web/`) | `make test-web` |
| Any Go code | `make test` |
| Before committing | `make verify` |

```bash
# Quick reference
make test-adapters    # ~0.01s
make test-mcp         # ~0.01s
make test-web         # Web frontend (vitest)
make test-all         # All tests including agent-worker and web
make verify           # go vet + all tests (pre-commit)
```

## CRITICAL: Rebuild Docker Containers After Changes

| After changing... | Rebuild command |
|-------------------|-----------------|
| API server (`cmd/`, `internal/`) | `docker-compose build akmatori-api && docker-compose up -d akmatori-api` |
| MCP Gateway (`mcp-gateway/`) | `docker-compose build mcp-gateway && docker-compose up -d mcp-gateway` |
| Agent worker (`agent-worker/`) | `docker-compose build akmatori-agent && docker-compose up -d akmatori-agent` |
| Frontend (`web/`) | `docker-compose build frontend && docker-compose up -d frontend` |
| QMD search (`qmd/`) | `docker-compose build qmd && docker-compose up -d qmd` |

## Current Testing Priorities

Coverage moves quickly; re-run `go test -coverprofile=coverage.out ./...` before quoting numbers.
Focus new tests on historically weak areas: `internal/handlers`, `internal/services`, `internal/slack`, main-module database paths, and MCP Gateway `internal/tools` / `internal/tools/zabbix`.

## Recent Changes (Apr 2026)

- Slack skill launches now start fresh agent sessions; skill prompt trimming/output-format handling was tightened.
- Alert webhook error paths, setup-state edge cases, and Slack settings integration helpers all gained focused tests.
- When updating docs, prefer small pattern notes over long examples so this file stays under the 30k limit.

## Agent Worker Architecture

The `agent-worker/` uses `@mariozechner/pi-coding-agent` SDK (v0.73.0):

| Component | File | Purpose |
|-----------|------|---------|
| Entry Point | `src/index.ts` | Reads config, starts orchestrator |
| Orchestrator | `src/orchestrator.ts` | Routes WebSocket messages |
| Agent Runner | `src/agent-runner.ts` | Creates pi-mono sessions |
| Tool Formatter | `src/tool-output-formatter.ts` | Formats tool args/output for UI streaming |
| WS Client | `src/ws-client.ts` | WebSocket to API server |

### SDK Features (v0.73.0)

- Cancellation signals propagate to nested model calls and tool executions
- Investigation history exports to `{workDir}/session_export.jsonl`
- Session files can live in `{workDir}/.sessions/` via `SessionManager.create/continueRecent(..., sessionDir)`
- Custom tools use typed `ToolDefinition` metadata; `promptSnippet` is required for prompt inclusion
- Typed compaction/retry session events replaced older untyped names
- Provider SDKs are lazy-loaded; auto-retry and multi-edit are supported
- Provider retry/timeout settings (`retry.provider.{timeoutMs,maxRetries,maxRetryDelayMs}`) are forwarded via `SettingsManager.inMemory({...})`; Akmatori uses 10-minute timeouts so slow on-prem/OpenRouter models do not abort during long alert investigations
- 0.73.0: incremental bash output streaming surfaces via `tool_execution_update` events (already handled by `agent-runner.ts`); Bedrock Opus 4.7 `xhigh` thinking fix is automatic

### SDK API Conventions

- `ModelRegistry` has no public constructor since 0.64.0. Always use `ModelRegistry.inMemory(authStorage)`.
- All `createXxxTool()` factory functions in `gateway-tools.ts` must wrap their return with `defineTool({...})` (imported from `@mariozechner/pi-coding-agent`). The bash tool is the sole exception — it retains `as unknown as ToolDefinition` due to contravariant generics in `renderCall`/`renderResult`.
- TypeBox is now imported from `typebox` (1.x), not `@sinclair/typebox` (0.x). The schema-builder API (`Type.Object`, `Type.String`, `Type.Optional`, etc.) is unchanged; only the package name differs.
- `DefaultResourceLoader` now requires `agentDir`. Pass `getAgentDir()` from `@mariozechner/pi-coding-agent` whenever constructing it directly (production callsite is `agent-runner.ts`; test mocks must also expose `getAgentDir`).

### Recent Agent Behavior Notes

- Fresh incidents start new agent sessions; only explicit resume flows call `continueRecent(...)`
- Agents should read the relevant `SKILL.md` first because it carries output-format instructions and `gateway_call(...)` examples

### Tool Architecture (TypeScript Gateway Tools)

Tools are registered as pi-mono custom tools via `gateway-tools.ts`, communicating with the MCP Gateway through a TypeScript client:

1. `generateSkillMd()` in Go writes `gateway_call` usage examples in SKILL.md with logical instance names
2. pi-mono discovers SKILL.md files
3. Agent calls `gateway_call("ssh.execute_command", {command: "uptime"}, "prod-ssh")`
4. `GatewayClient` sends JSON-RPC 2.0 POST to MCP Gateway with `X-Incident-ID` header
5. MCP Gateway resolves credentials by logical name or instance ID, checks authorization, and executes
6. Large responses (>4KB) are written to `{workDir}/tool_outputs/` with a truncated preview returned inline

### Gateway Tools

| Tool | File | Purpose |
|------|------|---------|
| `gateway_call` | `src/gateway-tools.ts` | Call any MCP Gateway tool by name with optional instance hint |
| `list_tool_types` | `src/gateway-tools.ts` | List all available tool types (e.g., `ssh`, `zabbix`, `victoria_metrics`, `postgresql`, `clickhouse`, `grafana`, `pagerduty`, `netbox`, `kubernetes`, `qmd`) |
| `list_tools_for_tool_type` | `src/gateway-tools.ts` | List all tools of a given type (e.g., `ssh`, `zabbix`, `victoria_metrics`, `postgresql`, `clickhouse`, `grafana`, `pagerduty`, `netbox`, `kubernetes`) |
| `get_tool_detail` | `src/gateway-tools.ts` | Get full JSON schema for a specific tool |
| `execute_script` | `src/gateway-tools.ts` | Run JavaScript in isolated vm with injected `gateway_call()`, `list_tools_for_tool_type()`, scoped `fs` |

**Note**: Tool discovery is type-based, not query-based. Use `list_tool_types` first, then `list_tools_for_tool_type({ tool_type: "ssh" })`.

**Note**: Tool schemas do NOT include routing parameters (`tool_instance_id`, `logical_name`). Instance routing is handled by `gateway_call`'s `instance` parameter. If an agent tries to call a tool directly (e.g., `ssh.execute_command`), the error message guides it to use `gateway_call` instead.

### Supporting Modules

| Module | File | Purpose |
|--------|------|---------|
| GatewayClient | `src/gateway-client.ts` | JSON-RPC 2.0 HTTP client with output management and allowlist support |
| ScriptExecutor | `src/script-executor.ts` | Isolated `vm` runtime with 5-minute timeout, scoped fs, captured console |

### Message Flow

1. API sends `new_incident` or `continue_incident` via WebSocket
2. Orchestrator extracts LLM settings and proxy config
3. AgentRunner creates pi-mono session with multi-provider auth
4. Output streamed back to API via WebSocket
5. On completion, metrics (tokens, time) reported, session exported to JSONL

### One-Shot LLM Path

For short, non-agent LLM calls (incident titles, alert extraction, Slack final-message summaries) the API sends `oneshot_llm_request` over the same worker WebSocket. The orchestrator routes it through `agent-worker/src/oneshot-llm.ts`, which calls pi-ai's `complete()` with the active LLM settings + proxy config and returns a single `oneshot_llm_response` correlated by `request_id`. `AgentWSHandler.OneShotLLM(...)` is the Go-side caller; `services.OneShotLLMCaller` is the interface that `TitleGenerator`, `extraction.AlertExtractor`, and `SlackSummarizer` all depend on, so every callsite is provider-agnostic and falls back deterministically when the worker is disconnected (`ErrWorkerNotConnected`).

## Slack Integration (`internal/slack/`)

### Manager (`manager.go`)

Hot-reloadable Slack connection manager:

```go
manager := slack.NewManager()
manager.SetEventHandler(myEventHandler)
manager.Start(ctx)
manager.TriggerReload()  // Hot-reload on settings change
go manager.WatchForReloads(ctx)
```

**Features**: Socket Mode, hot-reload without restart, proxy support, thread-safe `GetClient()`

### Event Types

| Event | Behavior |
|-------|----------|
| Bot message in alert channel | Create incident, start investigation |
| @mention in alert thread | Continue investigation with question |
| @mention in general channel | Direct response (not investigation) |

### Live Progress + Summarized Final Output

- `internal/handlers/slack_progress.go` — `SlackProgressStreamer` extracts the agent's latest reasoning (🤔) line from output deltas and forwards it to a `sink func(string)` callback subject to `slackAppendInterval` throttling (~2s). In production the sink is `TypingController.UpdateLoadingMessage`, which pipes the line into Slack's `assistant.threads.setStatus` `loading_messages` rotation banner. Tool start/end markers are filtered out. There is no "Thinking…" placeholder message — the typing banner + hourglass reaction are the activity signal during the run, and the final result is posted as a fresh thread reply when the agent completes.
- `internal/services/slack_summarizer.go` — final agent output is summarized through `OneShotLLMCaller` to fit `slackMaxTextBytes` (8000); `internal/output/slack_budget.go` provides a deterministic byte-truncation fallback when the worker is unavailable or returns over-budget output. The result is posted as a single thread message with the existing footer.

### Typing Indicator (`internal/slack/typing.go`)

`TypingController` owns the lifecycle of the "is investigating..." thread-header banner (`assistant.threads.setStatus`), its `loading_messages` rotation content, and the `hourglass_flowing_sand` reaction during agent runs. Constants are hardcoded — there is no DB column or UI for typing config:

- Status text: `"is investigating..."`
- Reaction emoji: `hourglass_flowing_sand`
- `loading_messages` content: `[]string{latest 🤔 line}`, fed via `UpdateLoadingMessage(line)` from `SlackProgressStreamer`. Replaces Slack's default rotation phrases ("searching…", "evaluating…", etc.) with the agent's actual reasoning. Each entry is byte-truncated with a UTF-8-safe ellipsis to `loadingMessageMaxBytes = 50` because Slack rejects longer entries with `invalid_arguments` (response detail: `must be less than 51 characters`). The leading 🤔 marker is stripped to free up space. Empty/whitespace input and consecutive duplicates are dropped — one Slack call per fresh non-duplicate line.
- Keepalive interval: 30s (also pushes the latest loading message); safety TTL: 60min; circuit-breaker after 2 consecutive setStatus failures (also trips immediately on `feature_not_enabled` / `not_allowed_token_type`, which means the Slack app is not registered as an AI Assistant — the reaction continues working).

Wired into three handler flows; each follows the `Start(ctx)` + `defer Stop()` pattern and relies on the surrounding handler blocking on `<-done` so the deferred Stop fires after the agent run finishes:

- `slack_processor.go::processMessage` (mention/DM flow)
- `alert_processor.go::runInvestigation` (webhook-alert flow)
- `alert_processor.go::runSlackChannelInvestigation` (Slack-channel-alert flow)

## Alert Extraction (`internal/alerts/extraction/`)

AI-powered extraction of structured alert data from free-form text:

```go
extractor := extraction.NewAlertExtractor(oneShotCaller) // OneShotLLMCaller (e.g. AgentWSHandler)
alert, err := extractor.Extract(ctx, messageText)
```

- Calls pi-ai's `complete()` via the agent worker (`oneshot_llm_request`), so any configured provider works
- Truncates input to 3000 chars
- Graceful fallback (first line → alert name, full text → description) when the worker is disconnected, the API key is missing, or JSON parsing fails

## Output Parser (`internal/output/`)

Parses structured blocks from agent output:

```
[FINAL_RESULT]
status: resolved|unresolved|escalate
summary: One-line summary
actions_taken:
- Action 1
recommendations:
- Recommendation 1
[/FINAL_RESULT]

[ESCALATE]
reason: Why escalation is needed
urgency: low|medium|high|critical
context: Additional context
[/ESCALATE]

[PROGRESS]
step: Current investigation step
completed: What's been done
[/PROGRESS]
```

Usage:
```go
parsed := output.Parse(agentOutput)
if parsed.FinalResult != nil { /* complete */ }
if parsed.Escalation != nil { notifyOnCall(parsed.Escalation.Urgency) }
fmt.Println(parsed.CleanOutput)  // Structured blocks stripped
```

## Services (`internal/services/`)

| Service | File(s) | Purpose |
|---------|---------|---------|
| SkillService | `skill_service.go`, `skill_file_sync.go`, `skill_prompt_service.go`, `incident_service.go` | Skill CRUD, file sync, prompt building, incident lifecycle |
| ToolService | `tool_service.go` | Tool instances, SSH key management |
| ContextService | `context_service.go` | Context file management |
| AlertService | `alert_service.go` | Alert processing and normalization |
| TitleGenerator | `title_generator.go` | AI-powered incident title generation |
| SlackSummarizer | `slack_summarizer.go` | Provider-agnostic compression of agent output to fit Slack's byte cap, with deterministic structured fallback |
| RunbookService | `runbook_service.go` | Runbook CRUD and file sync |
| MemoryService | `memory_service.go` | Cross-incident memory CRUD + per-scope MEMORY.md sync |
| MemoryExtractor | `memory_extractor.go` | One-shot LLM distillation of completed incidents into long-lived memory (idempotent by `incident_uuid`) |
| FeedbackClassifier | `feedback_classifier.go` | One-shot LLM classifier deciding whether a Slack thread reply is operator feedback worth storing |
| RetentionService | `retention_service.go` | Automated incident data cleanup (expired + orphaned) |
| ResponseFormatter | `response_formatter.go` | Optional one-shot LLM reformat of agent output (raw response + full reasoning) before it lands in `incident.response` and Slack; passthrough fallback when disabled, worker offline, or LLM call fails |

### Service Interfaces (`internal/services/interfaces.go`)

Handlers depend on interfaces for testability:

| Interface | Purpose |
|-----------|---------|
| `SkillManager` | Skill CRUD + lifecycle |
| `IncidentManager` | Incident spawn/update/get |
| `SkillIncidentManager` | Combines SkillManager + IncidentManager (used by handlers) |
| `ToolManager` | Tool instance CRUD + SSH keys |
| `AlertManager` | Alert source operations |
| `RunbookManager` | Runbook CRUD + file sync |
| `MemoryManager` | Cross-incident memory CRUD + idempotent `UpsertByName` + scope manifest sync |
| `ContextManager` | Context file management |
| `HTTPConnectorManager` | Declarative HTTP connector CRUD |
| `OneShotLLMCaller` | Provider-agnostic one-shot LLM completion via the agent worker (lives in `llm_settings.go`); implemented by handlers, consumed by `TitleGenerator`, `extraction.AlertExtractor`, and `SlackSummarizer` |

## Runbook System (`internal/services/runbook_service.go`)

Runbooks (SOPs) guide AI agent investigations. Stored in PostgreSQL, synced as markdown to `/akmatori/runbooks/`.

**Flow**: DB → markdown files → agent reads during investigation

**API**: REST at `/api/runbooks`
- `GET /api/runbooks` - List all
- `POST /api/runbooks` - Create (`{title, content}`)
- `GET /api/runbooks/{id}` - Get one
- `PUT /api/runbooks/{id}` - Update
- `DELETE /api/runbooks/{id}` - Delete

**File Sync**: On any CRUD operation, `SyncRunbookFiles()` writes all runbooks as `{id}-{slug}.md` and removes stale files.

**Agent Access**: The seeded `incident-manager` prompt (`DefaultIncidentManagerPrompt` in `internal/database/db.go`) instructs the agent to issue ONE `gateway_call("qmd.query", ...)` against `collection: "runbooks"` with TWO `searches[]` entries — sub-query 1 is the verbatim "Original alert text" excerpt (auto-2x-weighted by RRF fusion) and sub-query 2 is short keywords from the alert name. Up to 2 retries with different angles (3 total calls). Filesystem fallback to `/akmatori/runbooks/` only on QMD error, not empty results. The `executor.PrependGuidance` user-turn reminder mirrors this shape — keep them in sync.

**QMD Re-indexing**: `SyncRunbookFiles()` triggers a non-blocking HTTP POST to QMD's `/update` endpoint after writing files, keeping the search index current.

### QMD Search Service

QMD is a hybrid search engine (BM25 + vector + LLM reranking) running as a Docker sidecar. It indexes runbook markdown files and exposes tools via MCP HTTP server.

- **Config**: `qmd/qmd-config.yml` defines the `runbooks` and `memories` collections
- **Docker service**: `qmd` on `codex-network` + `api-internal`, port 8181 (internal only)
- **Environment variable**: `QMD_URL` (default: `http://qmd:8181`) — configured on both the API server (for re-index triggers) and the MCP Gateway (for auto-registration as proxy)
- **Environment variable**: `QMD_BIND_HOST` (default: `localhost`, set to `0.0.0.0` in Docker) — bind address for the QMD HTTP server
- **REST endpoints**: `/health` (GET, container health check), `/update` (POST, trigger re-index)
- **MCP tools**: `qmd.query` (search), `qmd.get` (retrieve), `qmd.multi_get`, `qmd.status` — registered automatically on gateway startup via `mcpproxy`
- **Bypass**: QMD proxy tools bypass the per-incident tool allowlist (registered proxy namespaces and multi-segment namespaces with dots bypass)

## Memory System (`internal/services/memory_service.go`, `mcp-gateway/internal/tools/memory/`)

Cross-incident memory: long-lived facts the agent and operators accumulate. Same DB → markdown → QMD shape as runbooks; separate `memories` QMD collection.

**Storage**: PostgreSQL `memories` table (model in `internal/database/models_context.go`). Filesystem mirror at `/akmatori/memory/<scope>/{MEMORY.md,<id>-<name>.md}`. Mounted on api + agent + qmd containers (NOT mcp-gateway — by design, memory tools are QMD-only).

**Scopes**: `"global"` (always injected into incident-manager `AGENTS.md`) or `<skill_name>` (injected into that skill's `SKILL.md`). Scope is a string identity, not a foreign key — skills can be renamed/deleted without breaking memory.

**Types** (codified as Go consts in `internal/services/memory_types.go`): `host`, `incident_pattern`, `tool_quirk`, `feedback`. The same consts feed validation, prompt rendering, and the extractor's JSON schema.

**API**: REST at `/api/memories` (list/scope+type filters, create, get/update/delete) and `/api/memories/scopes`. Operator UI feedback at `POST /api/incidents/{uuid}/feedback` writes scope=global, type=feedback memories.

**Agent recall**: `gateway_call("memory.search", {query, scope?, type?, limit?})` and `gateway_call("memory.get", {file, lines?})` — registered in `mcp-gateway/internal/tools/memory/`. Both proxy to QMD's `qmd.query` / `qmd.get` against the `memories` collection. The `memory` namespace bypasses the per-incident allowlist (`AddProxyNamespace("memory")`), so any agent can recall any time.

**Per-scope manifests**: `MEMORY.md` per scope, hard-capped at 200 lines / 25KB with explicit truncation marker. Injected verbatim into AGENTS.md (global) and SKILL.md (skill scope) via `renderMemoryRecallSection` in `skill_prompt_service.go`.

**Auto-extraction**: `MemoryExtractor.Extract()` fires from `UpdateIncidentComplete` in a goroutine when status flips to `completed`. One-shot LLM call distills the incident response/log tail into upsert/delete edits; idempotent via `CountByIncidentUUID` cursor.

**Slack feedback capture**: Non-mention thread replies on incident threads route through `FeedbackClassifier` (one-shot LLM, strict JSON, threshold 0.6). Confident hits are persisted as scope=global feedback memories with a thumbs-up reaction + brief threaded ack. Silent on negatives. Wired in `internal/handlers/slack_feedback.go`; mention path is unchanged (still routes to investigation continuation).

## API Package (`internal/api/`)

Standardized request/response helpers:

```go
api.RespondJSON(w, http.StatusOK, data)
api.RespondError(w, http.StatusBadRequest, "invalid input")
api.DecodeJSON(r, &request)
```

Use `api.RespondErrorWithCode()` when the frontend needs a stable machine-readable error code in addition to the message.

**API Documentation**: Swagger UI at `/api/docs` when enabled

### Retention Settings API

Incident retention is configured via `/api/settings/retention`:

- `GET /api/settings/retention` → returns the singleton `retention_settings` record
- `PUT /api/settings/retention` → partial update of `enabled`, `retention_days`, `cleanup_interval_hours`
- Validation: `retention_days` must be `1..3650`, `cleanup_interval_hours` must be `1..8760`

Keep handler validation aligned with `internal/api/types.go` and database defaults.

### Formatting Settings API

Optional post-processing of the agent's final response is configured via `/api/settings/formatting` (singleton `formatting_settings` table). `GET` returns the record; `PUT` accepts partial updates of `enabled`, `system_prompt`, `max_tokens` (1..8000), and `temperature` (0..2); `system_prompt` is capped at 8KB. When `enabled=true`, `ResponseFormatter` reformats `incident.response` via a one-shot LLM call before persistence and Slack delivery; `incident.full_log` keeps the unformatted reasoning.

### LLM Settings API

Multi-config LLM settings allow multiple configurations per provider (e.g., two OpenAI setups with different models/keys). Only one config is globally active at a time.

- `GET /api/settings/llm` → `{"configs": [...], "active_id": 3}` — list all configs with active indicator
- `POST /api/settings/llm` → create new config (`{provider, name, api_key, model, thinking_level, base_url}`)
- `GET /api/settings/llm/{id}` → get single config
- `PUT /api/settings/llm/{id}` → partial update (name uniqueness validated if changed)
- `DELETE /api/settings/llm/{id}` → delete config (rejected if active or last remaining)
- `PUT /api/settings/llm/{id}/activate` → set config as globally active

Each config response includes: id, name, provider, model, thinking_level, base_url, is_configured, masked api_key, enabled, active, created_at, updated_at.

Provider is set at creation time and cannot be changed via update. The update endpoint accepts: name, api_key, model, thinking_level, base_url.

The `LLMSettings` model has a unique `Name` field and allows multiple rows per provider (no unique constraint on Provider).

## Setup Package (`internal/setup/`)

Zero-config first-run experience:
1. No `.env` required for `docker compose up`
2. Credential resolution: env → DB → generate/setup wizard
3. First access triggers setup wizard for admin password

## Tool Instance Routing

Skills target specific tool instances via logical name or numeric ID:

```yaml
# In SKILL.md
tools:
  - type: zabbix
    logical_name: prod-zabbix  # Human-readable logical name (preferred)
    instance_id: 1
  - type: ssh
    logical_name: prod-ssh
    instance_id: 2
  - type: clickhouse
    logical_name: prod-clickhouse
    instance_id: 3
```

Resolution priority: explicit instance ID > logical name > first enabled instance of type.

At incident creation, the skill's tool instances are resolved into an allowlist passed to the MCP Gateway. The gateway enforces authorization on every tool call — unauthorized instances return JSON-RPC error -32600.

## Test Helpers (`internal/testhelpers/`)

Use the shared helpers instead of hand-rolled setup/assertion code when possible.

- `NewHTTPTestContext(...)` for handler tests
- `NewMockAlertAdapter(...)` for adapter success/error paths
- Builders for alerts, incidents, skills, tool/tool-type instances, alert sources, LLM settings, Slack settings, runbooks, and context files
- Assertions for equality, JSON, errors, HTTP responses, and panic/no-panic checks
- `AssertEventually` / `RetryUntil` for async flows
- `WithEnv` / `WithEnvs` for temporary env overrides
- `ConcurrentTest*`, `CallCounter`, temp-dir helpers, and fixture loaders for concurrency + filesystem tests

## Testing Patterns

- Prefer table-driven tests for parsers, validators, and service branching.
- Cover empty/nil input, boundaries, Unicode, invalid payloads, and concurrency where state is shared.
- Add benchmarks only for hot paths such as alert parsing, auth middleware, JSONB-heavy code, and title generation.

## Logging Convention

All logging uses Go's `log/slog` (structured JSON logging). **Never use `log.Printf`, `log.Fatalf`, or `log.Println`.**

- Initialized in `cmd/akmatori/main.go` via `logging.Init()` (`internal/logging/logging.go`)
- Use `slog.Info()`, `slog.Warn()`, `slog.Error()` with structured key-value pairs:
  ```go
  slog.Info("incident created", "uuid", incident.UUID, "title", incident.Title)
  slog.Error("failed to process alert", "error", err, "source", sourceName)
  ```
- Output format: JSON to stdout (container-friendly for log aggregation)

## Code Quality & Linting

```bash
go vet ./...              # Fast check
golangci-lint run         # PREFERRED - respects //nolint directives
```

**Note**: Standalone `staticcheck` uses different directive format (`//lint:ignore`), so prefer `golangci-lint`.

### Error Handling

Always check errors:

```go
// HTTP writes
if _, err := w.Write(data); err != nil {
    slog.Error("write failed", "error", err)
}

// External APIs (log non-critical)
if err := slackClient.AddReaction(...); err != nil {
    slog.Warn("reaction failed", "error", err)
}

// Tests - use Fatal for nil checks before dereference
if svc == nil {
    t.Fatal("service is nil")  // Stops immediately
}
```

### Go Idioms

```go
// Nil check around range is unnecessary
for k, v := range myMap { ... }  // Safe even if myMap is nil

// len() on nil returns 0
if len(decoded.Labels) > 0 { ... }  // No nil check needed
```

### Nolint Directives

For intentionally kept unused code:

```go
//nolint:unused // Legacy fallback - may be re-enabled
func legacyHandler() { ... }
```

## CRITICAL: External API Integration

**Never flood customer systems with API requests.**

### Requirements

1. **Rate limiting**: Default 10 req/sec, burst 20
2. **Caching**: Credentials 5min, responses 15-60sec
3. **Batching**: Use `get_items_batch()` not loops

### Cache TTLs

| Data Type | TTL |
|-----------|-----|
| Credentials/Config | 5 min |
| Auth tokens | 30 min |
| Host/inventory data | 30-60 sec |
| Problems/alerts | 15 sec |
| Metrics/history | 30 sec |
| CMDB device/IP/VM data | 60 sec |
| CMDB circuits/tenancy | 120 sec |
| K8s pods/jobs | 30 sec |
| K8s events/logs | 15 sec |
| K8s deployments/workloads | 60 sec |
| K8s nodes/services/namespaces | 120 sec |

### Catchpoint Patterns

Catchpoint is a first-class MCP tool type with a shared rate limiter and cached GET helpers.

- Tool namespace: `catchpoint.*`
- Read paths use `cachedGet(...)`; write paths (`acknowledge_alerts`, `run_instant_test`) must not be cached
- Reuse `addPaginationParams()` and `addTimeParams()` for optional query args
- `page_size` must be clamped to the API max of `100`
- Honor proxy settings only when `ProxySettings.CatchpointEnabled` is true
- Keep error messages parameter-specific and use `validation.SuggestParam()` for typo hints on required args

### PagerDuty Patterns

PagerDuty is a first-class MCP tool type following the Catchpoint pattern with token auth, caching, and rate limiting.

- Tool namespace: `pagerduty.*`
- Auth: API token via `Authorization: Token token={api_token}` header
- Read paths (`get_incidents`, `get_services`, `get_on_calls`, etc.) use `cachedGet(...)` with 15-30s TTL
- Write paths (`acknowledge_incident`, `resolve_incident`, `reassign_incident`, `add_incident_note`) must not be cached
- Events API v2 (`send_event`) posts to `https://events.pagerduty.com/v2/enqueue` with separate `routing_key`
- Honor proxy settings only when `ProxySettings.PagerDutyEnabled` is true

### NetBox Patterns

NetBox is a read-only CMDB tool type following the Catchpoint/PagerDuty pattern with token auth, caching, and rate limiting.

- Tool namespace: `netbox.*`
- Auth: API token via `Authorization: Token <api_token>` header
- All endpoints are read-only GET requests using `cachedGet(...)` with 60-120s TTL (CMDB data is mostly static)
- Modules: DCIM (devices, interfaces, sites, racks, cables, device types), IPAM (IPs, prefixes, VLANs, VRFs), Circuits, Virtualization (VMs, clusters, VM interfaces), Tenancy (tenants, tenant groups)
- Generic `api_request` method allows querying any NetBox API endpoint
- Honor proxy settings only when `ProxySettings.NetBoxEnabled` is true

### Kubernetes Patterns

Kubernetes is a read-only diagnostics tool type following the NetBox pattern with Bearer token auth, caching, and rate limiting.

- Tool namespace: `kubernetes.*`
- Auth: Bearer token via `Authorization: Bearer <k8s_token>` header
- All endpoints are read-only GET requests using `cachedGet(...)` with 15-120s TTL depending on resource volatility
- Resources: Namespaces, Pods (list/detail/logs), Events, Deployments (list/detail), StatefulSets, DaemonSets, Jobs, CronJobs, Nodes (list/detail), Services, ConfigMaps (metadata only), Ingresses
- Generic `api_request` method allows querying any K8s API GET endpoint (path must start with `/api` or `/apis`)
- Honor proxy settings only when `ProxySettings.K8sEnabled` is true

### Implementation Reference

- `mcp-gateway/internal/cache/cache.go` - Generic TTL cache with background cleanup
- `mcp-gateway/internal/ratelimit/limiter.go` - Token bucket rate limiter
- `mcp-gateway/internal/tools/zabbix/` - Zabbix integration with caching and rate limiting
- `mcp-gateway/internal/tools/victoriametrics/` - VictoriaMetrics integration with caching and rate limiting
- `mcp-gateway/internal/tools/postgresql/` - PostgreSQL read-only query and diagnostics integration
- `mcp-gateway/internal/tools/clickhouse/` - ClickHouse read-only query and OLAP diagnostics integration
- `mcp-gateway/internal/tools/grafana/` - Grafana integration with caching and rate limiting (dashboards, alerting, data source proxy, annotations)
- `mcp-gateway/internal/tools/catchpoint/` - Catchpoint synthetic monitoring integration with caching and rate limiting
- `mcp-gateway/internal/tools/pagerduty/` - PagerDuty integration with caching and rate limiting (incidents, services, on-call, events)
- `mcp-gateway/internal/tools/netbox/` - NetBox CMDB integration with caching and rate limiting (DCIM, IPAM, circuits, virtualization, tenancy)
- `mcp-gateway/internal/tools/k8s/` - Kubernetes read-only diagnostics with caching and rate limiting (pods, deployments, nodes, services, events, logs)
- `mcp-gateway/internal/tools/httpconnector/` - Declarative HTTP connector executor with auth injection
- `mcp-gateway/internal/mcpproxy/` - Connection pool and proxy handler for external MCP servers
- `mcp-gateway/internal/auth/` - Per-incident tool authorization (allowlist enforcement)
- `mcp-gateway/internal/validation/` - Parameter validation with typo suggestions for better error messages

### What NOT To Do

```go
// BAD: N API calls in loop
for _, host := range hosts {
    items, _ := zabbix.GetItems(ctx, host.ID)  // N calls!
}

// GOOD: Batched with caching
items, _ := zabbix.GetItemsBatch(ctx, hostIDs, patterns)  // 1 cached call
```

### Before Adding New External Integrations

- [ ] Does this code have rate limiting?
- [ ] Are read operations cached?
- [ ] Can multiple requests be batched?
- [ ] What happens if called 100x in a loop?

## Do NOT

- Skip running tests after changes
- Commit without `make verify`
- Add features without tests
- Call external APIs without rate limiting
- Make unbounded API calls in loops
- Skip caching for read operations
- Use nolint to hide actual bugs
- Leave tests that depend on external services (use mocks)
