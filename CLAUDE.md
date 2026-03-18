# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform that receives alerts from monitoring systems (Zabbix, Alertmanager, PagerDuty, Grafana, Datadog), analyzes them using multi-provider LLM agents (via the pi-mono coding-agent SDK), and executes automated remediation.

## Architecture

- **4-container Docker architecture**: API, Agent Worker, MCP Gateway, PostgreSQL
- **Backend**: Go 1.24+ (API server, MCP gateway)
- **Agent Worker**: Node.js 22+ / TypeScript using `@mariozechner/pi-coding-agent` SDK
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
│       └── tools/          # SSH, Zabbix, VictoriaMetrics, and HTTP connector implementations
├── web/                    # React frontend
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
| Any Go code | `make test` |
| Before committing | `make verify` |

```bash
# Quick reference
make test-adapters    # ~0.01s
make test-mcp         # ~0.01s
make test-all         # All tests including agent-worker
make verify           # go vet + all tests (pre-commit)
```

## CRITICAL: Rebuild Docker Containers After Changes

| After changing... | Rebuild command |
|-------------------|-----------------|
| API server (`cmd/`, `internal/`) | `docker-compose build akmatori-api && docker-compose up -d akmatori-api` |
| MCP Gateway (`mcp-gateway/`) | `docker-compose build mcp-gateway && docker-compose up -d mcp-gateway` |
| Agent worker (`agent-worker/`) | `docker-compose build akmatori-agent && docker-compose up -d akmatori-agent` |
| Frontend (`web/`) | `docker-compose build frontend && docker-compose up -d frontend` |

## Current Test Coverage (Mar 12, 2026)

| Package | Coverage | Status |
|---------|----------|--------|
| `internal/alerts` | 100.0% | ✅ |
| `internal/alerts/adapters` | 98.4% | ✅ |
| `internal/utils` | 94.2% | ✅ |
| `internal/api` | 92.3% | ✅ |
| `internal/setup` | 84.8% | ✅ |
| `internal/middleware` | 78.9% | ✅ |
| `internal/testhelpers` | 74.8% | ✅ |
| `internal/output` | 68.4% | ✅ |
| `internal/alerts/extraction` | 36.0% | ⚠️ |
| `internal/slack` | 32.3% | ⚠️ |
| `internal/services` | 28.8% | ⚠️ |
| `internal/database` | 20.2% | ⚠️ |
| `internal/handlers` | 10.2% | ⚠️ |

**Priority**: handlers (HTTP tests), services, extraction (LLM mocks)

## Agent Worker Architecture

The `agent-worker/` uses `@mariozechner/pi-coding-agent` SDK:

| Component | File | Purpose |
|-----------|------|---------|
| Entry Point | `src/index.ts` | Reads config, starts orchestrator |
| Orchestrator | `src/orchestrator.ts` | Routes WebSocket messages |
| Agent Runner | `src/agent-runner.ts` | Creates pi-mono sessions |
| Tool Formatter | `src/tool-output-formatter.ts` | Formats tool args/output for UI streaming |
| WS Client | `src/ws-client.ts` | WebSocket to API server |

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
| `list_tools_for_tool_type` | `src/gateway-tools.ts` | Discover available tools by query and optional type filter |
| `get_tool_detail` | `src/gateway-tools.ts` | Get full JSON schema for a specific tool |
| `execute_script` | `src/gateway-tools.ts` | Run JavaScript in isolated vm with injected `gateway_call()`, `list_tools_for_tool_type()`, scoped `fs` |

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
5. On completion, metrics (tokens, time) reported

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

## Alert Extraction (`internal/alerts/extraction/`)

AI-powered extraction of structured alert data from free-form text:

```go
extractor := extraction.NewAlertExtractor()
alert, err := extractor.Extract(ctx, messageText)
```

- Uses `gpt-4o-mini` (fast, cheap)
- Truncates to 3000 chars
- Graceful fallback on error (first line → alert name, full text → description)

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
| RunbookService | `runbook_service.go` | Runbook CRUD and file sync |

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
| `ContextManager` | Context file management |
| `HTTPConnectorManager` | Declarative HTTP connector CRUD |

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

**Agent Access**: Incident manager prompt instructs agent to check `/akmatori/runbooks/` for relevant procedures before starting investigation.

## API Package (`internal/api/`)

Standardized request/response helpers:

```go
api.WriteJSON(w, http.StatusOK, data)
api.WriteError(w, http.StatusBadRequest, "invalid input")
api.DecodeJSON(r, &request)
page, perPage, from, to := api.GetPaginationParams(r)
```

**API Documentation**: Swagger UI at `/api/docs` when enabled

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
```

Resolution priority: explicit instance ID > logical name > first enabled instance of type.

At incident creation, the skill's tool instances are resolved into an allowlist passed to the MCP Gateway. The gateway enforces authorization on every tool call — unauthorized instances return JSON-RPC error -32600.

## Test Helpers (`internal/testhelpers/`)

### HTTP Testing

```go
ctx := testhelpers.NewHTTPTestContext(t, http.MethodPost, "/api/v1/alerts", nil)
ctx.WithAPIKey("key").WithJSONBody(data).ExecuteFunc(handler).AssertStatus(200).AssertBodyContains("success")
var result Response
ctx.DecodeJSON(&result)
```

### Mock Alert Adapter

```go
mock := testhelpers.NewMockAlertAdapter("prometheus").WithAlerts(
    testhelpers.NewAlertBuilder().WithName("HighCPU").WithSeverity("critical").Build(),
)
// Or with error
mockErr := testhelpers.NewMockAlertAdapter("datadog").WithParseError(errors.New("invalid"))
```

### Data Builders

```go
alert := testhelpers.NewAlertBuilder().WithName("HighCPU").WithSeverity("critical").WithHost("server-1").Build()
incident := testhelpers.NewIncidentBuilder().WithTitle("DB outage").WithStatus("investigating").Build()
skill := testhelpers.NewSkillBuilder().WithName("zabbix-analyst").WithCategory("monitoring").Build()
toolInstance := testhelpers.NewToolInstanceBuilder().WithName("prod-zabbix").WithSetting("url", "https://...").Build()
llmSettings := testhelpers.NewLLMSettingsBuilder().WithProvider(database.LLMProviderAnthropic).Build()
```

**Available**: AlertBuilder, IncidentBuilder, SkillBuilder, ToolInstanceBuilder, ToolTypeBuilder, AlertSourceInstanceBuilder, LLMSettingsBuilder, SlackSettingsBuilder, RunbookBuilder, ContextFileBuilder

### Assertions

```go
// Basic
testhelpers.AssertEqual(t, expected, actual, "msg")
testhelpers.AssertNil(t, err, "msg")
testhelpers.AssertNotNil(t, result, "msg")
testhelpers.AssertContains(t, body, "success", "msg")

// String
testhelpers.AssertStringPrefix(t, s, "prefix", "msg")
testhelpers.AssertStringNotEmpty(t, s, "msg")

// Slice/Map (generic)
testhelpers.AssertSliceLen(t, slice, 5, "msg")
testhelpers.AssertMapContainsKey(t, m, "key", "msg")

// JSON
testhelpers.AssertJSONEqual(t, expected, actual, "msg")
testhelpers.AssertJSONContainsKey(t, jsonStr, "name", "msg")

// Error/Panic
testhelpers.AssertErrorContains(t, err, "not found", "msg")
testhelpers.AssertPanics(t, func() { panic("!") }, "msg")
testhelpers.AssertNoPanic(t, func() { safeFunc() }, "msg")

// HTTP
testhelpers.AssertStatusCode(t, resp.StatusCode, 200, "msg")
testhelpers.AssertContentType(t, contentType, "application/json", "msg")
```

### Async/Retry

```go
testhelpers.AssertEventually(t, 5*time.Second, 100*time.Millisecond, func() bool {
    return service.IsReady()
}, "should become ready")

success := testhelpers.RetryUntil(t, 5*time.Second, 100*time.Millisecond, func() bool {
    return checkCondition()
}, "waiting")
```

### Environment

```go
cleanup := testhelpers.WithEnv(t, "API_KEY", "test")
defer cleanup()

cleanup := testhelpers.WithEnvs(t, map[string]string{"KEY1": "val1", "KEY2": "val2"})
defer cleanup()
```

### Concurrent Testing

```go
testhelpers.ConcurrentTest(t, 10, func(workerID int) { /* ... */ })
testhelpers.ConcurrentTestWithTimeout(t, 5*time.Second, 10, func(workerID int) { /* ... */ })
```

### Call Counter (Thread-Safe)

```go
counter := testhelpers.NewCallCounter()
counter.Inc()
counter.AssertCount(t, 2, "should be called twice")
```

### Test Directory Utilities

```go
dir, cleanup := testhelpers.TempTestDir(t, "mytest-")
defer cleanup()
path := testhelpers.WriteTestFile(t, dir, "subdir/test.txt", "content")
content := testhelpers.ReadTestFile(t, path)
testhelpers.AssertFileExists(t, path, "msg")
```

### Fixtures

```go
payload := testhelpers.LoadFixture(t, "alerts/alertmanager_alert.json")
testhelpers.LoadJSONFixture(t, "alerts/zabbix_alert.json", &alert)
```

## Testing Patterns

### Table-Driven Tests

```go
tests := []struct{ name, input string; want Severity }{
    {"critical", "critical", SeverityCritical},
    {"unknown defaults", "xyz", SeverityWarning},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        if got := mapSeverity(tt.input); got != tt.want {
            t.Errorf("got %v, want %v", got, tt.want)
        }
    })
}
```

### Edge Cases to Cover

1. **Empty/nil inputs**: Empty strings, nil maps/slices
2. **Boundary conditions**: At limits, one over/under
3. **Unicode**: Non-ASCII, emojis, special chars
4. **Error conditions**: Invalid inputs, missing fields
5. **Concurrency**: Thread safety for shared state

### Benchmarks

```bash
go test -bench=. -benchmem ./internal/alerts/adapters/...
```

Benchmarked: Alert parsing, JSONB ops, auth middleware, title generation

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

### Implementation Reference

- `mcp-gateway/internal/cache/cache.go` - Generic TTL cache with background cleanup
- `mcp-gateway/internal/ratelimit/limiter.go` - Token bucket rate limiter
- `mcp-gateway/internal/tools/zabbix/` - Zabbix integration with caching and rate limiting
- `mcp-gateway/internal/tools/victoriametrics/` - VictoriaMetrics integration with caching and rate limiting
- `mcp-gateway/internal/tools/httpconnector/` - Declarative HTTP connector executor with auth injection
- `mcp-gateway/internal/mcpproxy/` - Connection pool and proxy handler for external MCP servers
- `mcp-gateway/internal/auth/` - Per-incident tool authorization (allowlist enforcement)

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
