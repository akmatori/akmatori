# Contributing to Akmatori

Thanks for helping build Akmatori. Bug fixes, new integrations, tests, and docs are all welcome.

**Where to talk:**

- [Discord](https://discord.gg/fMbAqqp7nY): `#dev` for contributors, `#help` for questions.
- [GitHub Issues](https://github.com/akmatori/akmatori/issues): bugs, feature requests, and design discussion.

For a large change (a new subsystem, a schema change, a new provider), open an issue or post in `#dev` first so we can agree on the approach before you write the code.

## Your first PR in 30 minutes

```bash
# 1. Fork on GitHub, then clone your fork
git clone https://github.com/<you>/akmatori.git
cd akmatori

# 2. Start the full stack from local source
cp .env.example .env   # defaults are fine; secrets are generated on first run
make dev               # docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
# open http://localhost:8080 and finish the setup wizard

# 3. Run the tests
make verify            # go vet + Go tests (API and MCP Gateway) + agent-worker + web tests

# 4. Pick an issue and branch
#    https://github.com/akmatori/akmatori/issues?q=is:open+label:%22good+first+issue%22
git checkout -b fix/short-description

# 5. Make the change, add a test, run the smallest test target (see below), then open a PR
```

## Project structure

```text
akmatori/
├── cmd/akmatori/              API server entry point (wiring, adapter registration)
├── internal/
│   ├── alerts/adapters/       Inbound alert adapters (Alertmanager, Grafana, PagerDuty, Datadog, Zabbix)
│   ├── alerts/extraction/     One-shot LLM alert extraction
│   ├── api/                   API request/response helpers and types
│   ├── config/                Environment configuration
│   ├── database/              GORM models, seeding, and DB logic
│   ├── executor/              Prompt guidance helpers (plus legacy executor code)
│   ├── handlers/              HTTP, WebSocket, Slack, and webhook handlers
│   ├── logging/               slog setup
│   ├── messaging/             Messaging provider abstraction (Slack, Telegram stub)
│   ├── middleware/            Auth (JWT, API key), CORS, request IDs
│   ├── output/                Structured agent output parsing
│   ├── services/              Business logic; handler-facing interfaces in interfaces.go
│   ├── setup/                 First-run bootstrap
│   ├── slack/                 Slack client, typing indicators, reload logic
│   ├── testhelpers/           Builders, fixtures loader, mocks (see its README.md)
│   └── utils/                 Formatting and sanitizing helpers
├── agent-worker/              Node.js/TypeScript worker that runs pi-mono agent sessions
│   ├── src/                   Orchestrator, agent runner, one-shot LLM, gateway_call bridge
│   └── tests/                 Vitest tests
├── mcp-gateway/               Separate Go module: tool auth, rate limiting, MCP proxy
│   └── internal/tools/        Built-in tool implementations (ssh, zabbix, k8s, grafana, ...)
├── web/                       React 19 + TypeScript + Vite + Tailwind operator console
├── proxy/                     nginx config for the single published port
├── skills/akmatori/           Operator skill for Claude Code / Codex / OpenCode (install with skills/install.sh)
├── akmatori_data/agents/      System subagent definitions (memory/runbook searchers, memory writer)
├── docs/                      OpenAPI spec, SQL migrations, implementation plans
├── tests/fixtures/            Real alert payloads and runbook fixtures for tests
└── scripts/                   Container entrypoint scripts
```

How a request flows: an alert adapter or Slack creates an incident → handlers call services through `internal/services/interfaces.go` → the API sends the run to the agent worker over WebSocket → the agent calls tools with `gateway_call` → the MCP Gateway checks the incident's tool allowlist, loads credentials, and runs the tool → the result is parsed and sent to the UI and Slack. The agent never sees tool credentials.

## Development setup

**Requirements:** Docker with Compose, Go 1.25, Node.js 22+. Go tests use SQLite through `go-sqlite3`, so you need CGO and a C compiler (gcc or clang).

**Run the stack.** `make dev` builds all four images from your checkout and starts them. The base `docker-compose.yml` only has `image:` references (the end-user pull flow); `docker-compose.dev.yml` adds the `build:` blocks. Without it, `docker compose build` does nothing.

**Rebuild one service** after a change:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml build <svc>
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d <svc>
```

| You changed | Service |
|---|---|
| `cmd/`, `internal/` | `akmatori-api` |
| `mcp-gateway/` | `mcp-gateway` |
| `agent-worker/` | `akmatori-agent` |
| `web/` | `frontend` |

Running `make dev` again rebuilds everything and restarts `akmatori-proxy` so nginx picks up new container IPs. Use `make docker-logs` to follow logs.

**Frontend hot reload.** In `web/`, run `npm install`, then `npm run dev` (Vite on `:5173`). It proxies API calls to the `make dev` stack on `http://localhost:8080`. To use another install, set `AKMATORI_API_URL=http://host:8080`.

## Running tests

Run the smallest target for your change first, then `make verify` before you push.

| Area changed | Command |
|---|---|
| Go backend | `make test` |
| Alert adapters | `make test-adapters` |
| MCP Gateway | `make test-mcp` |
| Agent worker | `make test-agent` |
| Frontend | `make test-web` |
| Everything (pre-PR gate) | `make verify` |

`make test-coverage` writes `coverage.html`. `make help` lists every target.

CI (`.github/workflows/ci.yml`) runs the same checks on every pull request: Go vet and tests for the API and MCP Gateway, agent-worker build and tests, and web typecheck, lint, and tests. Run `make verify` locally first so CI stays green.

## Lint and format

- **Go:** `make fmt` (gofmt) and `make vet`. `make lint` runs `golangci-lint` if you have it installed. For the gateway, run `cd mcp-gateway && go vet ./...` (`make verify` does this).
- **Web:** in `web/`, `npx tsc -b && npx eslint . && npx vitest run` must be clean. The ESLint config is strict (no `any`, react-hooks rules).
- **Agent worker:** in `agent-worker/`, `npm run build` (tsc) must pass.

## Code conventions

- **Interfaces at handler boundaries.** Handlers depend on `internal/services/interfaces.go`. Put a new service dependency behind an interface first so handlers stay testable.
- **Stay provider-agnostic.** Never call Slack APIs from handlers or services; go through the messaging `ProviderRegistry`. One-shot LLM work goes through `OneShotLLMCaller`, not a full agent session.
- **Tools are reached only through `gateway_call`.** Do not teach agents to call tool implementations directly.
- **Degrade gracefully.** Akmatori must keep working when an optional AI piece fails. When you add AI-dependent behavior, add its fallback in the same PR.
- **Tests:** use the builders, `NewHTTPTestContext`, and `LoadFixture` from `internal/testhelpers` (see its [README](../internal/testhelpers/README.md)). Put real payload samples in `tests/fixtures/`.

Subsystem rules (alert correlation, memory and runbooks, formatting rules, cron and root skills, Slack, proposals, the agent worker SDK, the web frontend) are in [`.claude/rules/`](../.claude/rules/). They are written for AI coding agents but are useful for humans too. Read the matching file before you change that area.

## How-to guides

### Add a gateway tool

Before you write code, check whether you need to. Operators can add tools with no code: an **HTTP connector** (declarative REST endpoints) or an external **MCP server**, both configured in the UI. Write a built-in tool when you need real logic, a client library, or tight safety controls.

A built-in tool touches two Go modules. Use an existing tool such as `netbox` or `jira` as a template.

1. **Implementation:** `mcp-gateway/internal/tools/<name>/<name>.go` and `<name>_test.go`. Resolve credentials and settings at call time, use a timeout on every external call, and never log or return secrets. Test against an `httptest` server.
2. **Settings schema:** add `get<Name>Schema()` in `mcp-gateway/internal/tools/schemas.go` and register it in `GetToolSchemas()`. Mark secrets with `Secret: true`, and optional knobs with `Advanced: true`. The web UI renders the settings form from this schema, so you normally do not need frontend changes.
3. **Registration:** in `mcp-gateway/internal/tools/registry.go`, add the rate-limiter constants and fields, a `register<Name>Tools()` method that registers each `<name>.<function>` with its `InputSchema`, a call in `RegisterAllTools()`, and a `Stop()` cleanup if the tool caches.
4. **Tool type seed:** add the type to `EnsureToolTypes()` in `internal/services/tool_service.go`.
5. **Agent guidance:** add a `case "<name>":` to `generateToolUsageExample()` in `internal/services/skill_prompt_service.go`, with the parameters and `gateway_call(...)` examples that go into the generated `SKILL.md`.
6. **Optional:** if the tool must honor the outbound HTTP proxy, add a `<Name>Enabled` flag to `ProxySettings` (in both `internal/database/models_settings.go` and `mcp-gateway/internal/database/db.go`) and to the proxy settings handler and UI.
7. Run `make test-mcp` and `make test`, rebuild `mcp-gateway` and `akmatori-api`, create an instance in the UI, attach it to a skill, and run a test incident.

The gateway has its own module, so run `go` commands for it from `mcp-gateway/`. More gateway routing rules are in [`.claude/rules/mcp-gateway-tools.md`](../.claude/rules/mcp-gateway-tools.md).

### Add an alert source adapter

Alerts arrive at `POST /webhook/alert/{instance_uuid}`, and the source type of the instance selects the adapter.

1. **Adapter:** `internal/alerts/adapters/<source>.go`. Embed `alerts.BaseAdapter` and implement the `AlertAdapter` interface from `internal/alerts/adapter.go`: `ValidateWebhookSecret`, `ParsePayload` (one webhook can hold many alerts; return `[]NormalizedAlert`), and `GetDefaultMappings`.
2. **Register** it in `cmd/akmatori/main.go` next to the other `alertHandler.RegisterAdapter(...)` calls.
3. **Seed the source type** (name, display name, webhook secret header, default field mappings) in `InitializeDefaultSourceTypes()` in `internal/services/alert_service.go`.
4. **UI:** add a short badge for the source in `web/src/components/AlertSourcesManager.tsx`.
5. **Tests:** add a real sample payload to `tests/fixtures/alerts/` and write `<source>_test.go` that covers firing, resolved, and bad-secret cases. Run `make test-adapters`.

### Add an LLM provider

First check whether you need a new provider. An OpenAI-compatible endpoint (vLLM, Ollama, on-prem gateways) already works with the `custom` provider and a base URL. A new provider makes sense when the [pi-ai](https://www.npmjs.com/package/@earendil-works/pi-ai) SDK in the agent worker supports it as a built-in provider.

1. **Backend:** in `internal/database/models_settings.go`, add the `LLMProvider` constant, add it to `ValidLLMProviders`, and add its display name. Add a default model in `internal/database/db.go`. Update the valid-providers error message in `internal/handlers/api_settings_llm.go`. Extend `internal/database/llm_settings_test.go`.
2. **Agent worker:** add the provider to the union in `agent-worker/src/types.ts`, and to `PROVIDER_API_MAP` and `PROVIDER_ENV_KEY` in `agent-worker/src/agent-runner.ts`. `PROVIDER_ENV_KEY` lets subagent processes authenticate. Add cases to `agent-worker/tests/agent-runner.test.ts`.
3. **Web:** add the provider to `LLMProvider` in `web/src/types/index.ts`, to the provider list in `web/src/components/settings/LLMSettingsSection.tsx`, and add model suggestions in `web/src/components/settings/llmModelSuggestions.ts`.
4. Run `make test`, `make test-agent`, and `make test-web`, rebuild, then run a real incident with the new provider.

Inside the worker container, `node_modules/.bin/pi auth check --provider <id>` is the fastest way to find out why a provider or model does not resolve.

### Skills

The word "skill" means three different things in this repo:

- **Incident skills** (for example a "Linux SRE" skill with SSH and Zabbix attached) are user data. Operators create them in the UI or through the API, and they do not need code. The `SKILL.md` for each skill is generated from its prompt plus usage guidance for the tools attached to it (see `internal/services/skill_prompt_service.go`). If agents misuse a tool, improve its `generateToolUsageExample()` case.
- **System root skills** (`incident-manager`, `cron-agent`, `proposal-editor`) are built in. To add one, follow the checklist in [`.claude/rules/cron-and-root-skills.md`](../.claude/rules/cron-and-root-skills.md). System subagents (memory and runbook search, memory writer) are Markdown files in `akmatori_data/agents/`.
- **The operator skill** in `skills/akmatori/` teaches external coding agents (Claude Code, Codex, OpenCode) to run Akmatori through its REST API. If you change or add an API endpoint, update `skills/akmatori/references/` and `docs/openapi.yaml` in the same PR.

## Pull request guidelines

- **Keep PRs small and focused.** One fix or feature per PR. Split refactors from behavior changes.
- **Include tests.** New behavior needs tests, and a bug fix needs a regression test. Paste the `make` targets you ran in the PR description.
- **Update the docs you affect:** `docs/openapi.yaml` for API changes, `README.md` for user-visible features, and `skills/akmatori/` for operator workflows.
- **Database changes:** models migrate through GORM `AutoMigrate`. If a change needs manual SQL (drops, data fixes), add it to `docs/migrations/`.
- **No secrets** in code, logs, test fixtures, or screenshots.
- **Commit messages** use [Conventional Commits](https://www.conventionalcommits.org/) with an optional scope:

  ```text
  feat(alerts): auto-close inactive incidents
  fix(agent-worker): register custom providers so their API key resolves
  refactor(web): extract shared feed components
  test(slack): cover ResolveLabel and its label cache
  docs(readme): refresh model catalog
  chore(agent-worker): upgrade pi SDK 0.85.1 -> 1.0.1
  ```

  Use the imperative mood and a lowercase summary. Common scopes are `web`, `alerts`, `slack`, `llm`, `agent-worker`, `mcp-gateway`, `proxy`, and `readme`.

## License

Akmatori is licensed under the [Apache License 2.0](../LICENSE). By submitting a contribution, you agree that it is licensed under the same terms.
