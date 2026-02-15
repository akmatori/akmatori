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

## CRITICAL: Always Verify Changes with Tests

**After ANY code change, you MUST run the appropriate test command to verify your work.**

### Test Commands by Area

| After changing... | Run this command |
|-------------------|------------------|
| Alert adapters (`internal/alerts/adapters/`) | `make test-adapters` |
| MCP Gateway tools (`mcp-gateway/internal/tools/`) | `make test-mcp` |
| Agent worker (`agent-worker/`) | `make test-agent` |
| Database models (`internal/database/`) | `go test ./internal/database/...` |
| Middleware (`internal/middleware/`) | `go test ./internal/middleware/...` |
| Utilities (`internal/utils/`) | `go test ./internal/utils/...` |
| Any Go code | `make test` |
| Before committing | `make verify` |

### Quick Reference

```bash
# Fast feedback - test only what you changed
make test-adapters    # Alert adapter tests (~0.01s)
make test-mcp         # MCP gateway tests (~0.01s)
make test-agent       # Agent worker tests

# Full test suite
make test-all         # All tests including MCP gateway and agent-worker

# Pre-commit verification
make verify           # go vet + all tests + agent-worker tests
```

## Code Style

- Use standard Go testing patterns (see existing `*_test.go` files)
- Use `httptest` for HTTP handler testing
- Follow existing naming conventions: `TestComponentName_MethodName_Scenario`

## Key Directories

```
/opt/akmatori/
├── cmd/akmatori/           # Main API server entry point
├── internal/
│   ├── alerts/adapters/    # Alert source adapters (Zabbix, Alertmanager, etc.)
│   ├── database/           # GORM models and database logic
│   ├── handlers/           # HTTP/WebSocket handlers
│   ├── middleware/         # Auth, CORS middleware
│   ├── services/           # Business logic layer
│   └── utils/              # Utility functions
├── agent-worker/           # Node.js/TypeScript agent worker (pi-mono SDK)
│   ├── src/                # TypeScript source
│   │   ├── index.ts        # Entry point
│   │   ├── orchestrator.ts # Message routing
│   │   ├── agent-runner.ts # pi-mono SDK integration
│   │   ├── ws-client.ts    # WebSocket client
│   │   ├── types.ts        # Shared types
│   │   └── tools/          # MCP Gateway tool definitions
│   └── tests/              # Vitest tests
├── mcp-gateway/            # MCP protocol gateway (separate Go module)
│   └── internal/tools/     # SSH and Zabbix tool implementations
├── web/                    # React frontend
└── tests/fixtures/         # Test payloads and mock data
```

## Testing Workflow

1. **Before making changes**: Understand what you're modifying
2. **After making changes**: Run relevant tests immediately
3. **If tests fail**: Fix the issue before moving on
4. **Before committing**: Run `make verify` to ensure everything passes

## CRITICAL: Rebuild Docker Containers After Changes

**After making code changes, you MUST rebuild and restart the affected Docker containers.**

### Container Rebuild Commands by Area

| After changing... | Rebuild command |
|-------------------|-----------------|
| API server (`cmd/akmatori/`, `internal/`) | `docker-compose build akmatori-api && docker-compose up -d akmatori-api` |
| MCP Gateway (`mcp-gateway/`) | `docker-compose build mcp-gateway && docker-compose up -d mcp-gateway` |
| Frontend (`web/`) | `docker-compose build frontend && docker-compose up -d frontend` |
| Agent worker (`agent-worker/`) | `docker-compose build akmatori-agent && docker-compose up -d akmatori-agent` |
| Multiple components | `docker-compose build <service1> <service2> && docker-compose up -d <service1> <service2>` |

### Quick Reference

```bash
# Rebuild and restart specific services
docker-compose build mcp-gateway frontend
docker-compose up -d mcp-gateway frontend

# Rebuild all services (slower, use when needed)
docker-compose build
docker-compose up -d

# View logs after restart to verify
docker-compose logs -f mcp-gateway
docker-compose logs -f frontend

# Check container health
docker-compose ps
```

### Container-to-Code Mapping

| Container | Source Code |
|-----------|-------------|
| `akmatori-api` | `cmd/akmatori/`, `internal/`, `Dockerfile.api` |
| `mcp-gateway` | `mcp-gateway/`, `mcp-gateway/Dockerfile` |
| `frontend` | `web/`, `web/Dockerfile` |
| `akmatori-agent` | `agent-worker/`, `agent-worker/Dockerfile` |
| `postgres` | N/A (uses official image) |
| `proxy` | `proxy/nginx.conf` (config only, no rebuild needed) |

## CRITICAL: Write Tests for New Code

**When adding ANY new functionality, you MUST write corresponding tests.**

### Test Requirements for New Code

| When you create... | You must also create... |
|--------------------|-------------------------|
| New adapter in `internal/alerts/adapters/` | `<adapter>_test.go` with payload parsing tests |
| New tool in `mcp-gateway/internal/tools/` | `<tool>_test.go` with unit tests |
| New handler in `internal/handlers/` | Handler tests using `httptest` |
| New service in `internal/services/` | Service tests with mocked dependencies |
| New utility function | Unit tests covering edge cases |
| New API endpoint | Integration test for request/response |
| New agent-worker module in `agent-worker/src/` | Vitest test in `agent-worker/tests/` |

### Test Coverage Checklist

For each new function/feature, tests should cover:

- [ ] **Happy path**: Normal expected behavior
- [ ] **Edge cases**: Empty inputs, nil values, boundary conditions
- [ ] **Error cases**: Invalid input, malformed data, missing fields
- [ ] **JSON serialization**: If structs are serialized, test round-trip

### Example: Adding a New Alert Adapter

When adding a new adapter (e.g., `newrelic.go`), create `newrelic_test.go` with:

```go
func TestNewNewRelicAdapter(t *testing.T) { ... }
func TestNewRelicAdapter_ParsePayload_FiringAlert(t *testing.T) { ... }
func TestNewRelicAdapter_ParsePayload_ResolvedAlert(t *testing.T) { ... }
func TestNewRelicAdapter_ParsePayload_InvalidJSON(t *testing.T) { ... }
func TestNewRelicAdapter_ValidateWebhookSecret_NoSecret(t *testing.T) { ... }
func TestNewRelicAdapter_ValidateWebhookSecret_ValidSecret(t *testing.T) { ... }
func TestNewRelicAdapter_ValidateWebhookSecret_InvalidSecret(t *testing.T) { ... }
func TestNewRelicAdapter_GetDefaultMappings(t *testing.T) { ... }
```

Also add a test fixture: `tests/fixtures/alerts/newrelic_alert.json`

### Test File Location

Place test files next to the code they test:
```
internal/alerts/adapters/
├── alertmanager.go
├── alertmanager_test.go    # <- Tests go here
├── zabbix.go
├── zabbix_test.go
```

### Verify New Tests Work

After writing new tests:
```bash
# Run just your new tests
go test -v -run TestNewRelicAdapter ./internal/alerts/adapters/...

# Run all tests to ensure no regressions
make test-all
```

## Code Cleanup

Use the **code simplifier agent** at the end of a long coding session, or to clean up complex PRs. This helps reduce unnecessary complexity and ensures code remains maintainable.

## CRITICAL: External API Integration - Rate Limiting & Caching

**Akmatori integrates with enterprise monitoring systems (Zabbix, Datadog, PagerDuty, etc.). Flooding these systems with requests will destroy customer trust and can cause outages.**

### Mandatory Requirements for External API Calls

When adding or modifying code that calls external APIs (Zabbix, monitoring systems, customer infrastructure):

1. **Always implement rate limiting**
   - Use token bucket or similar algorithm
   - Default: 10 requests/second with burst of 20
   - Make limits configurable per integration

2. **Always implement caching for read operations**
   - Cache credentials/config: 5 minute TTL
   - Cache API responses: 15-60 second TTL (shorter for frequently changing data)
   - Cache auth tokens: 30 minute TTL
   - Use cache keys that include all relevant parameters

3. **Batch requests when possible**
   - Combine multiple similar queries into single API calls
   - Deduplicate repeated requests within an investigation
   - Example: `get_items_batch()` instead of multiple `get_items()` calls

4. **Log cache hits/misses for observability**
   - Helps identify if caching is working
   - Enables tuning of TTL values

### Current Implementation Reference

See `mcp-gateway/internal/` for examples:
- `cache/cache.go` - Generic TTL cache with background cleanup
- `ratelimit/limiter.go` - Token bucket rate limiter
- `tools/zabbix/zabbix.go` - Integration with caching and rate limiting

### Rate Limit Configuration

| External System | Rate Limit | Burst | Notes |
|-----------------|------------|-------|-------|
| Zabbix API | 10/sec | 20 | Configured in `registry.go` |
| SSH commands | 5/sec | 10 | Per-server limit |
| Future APIs | 10/sec | 20 | Default, adjust as needed |

### Cache TTL Guidelines

| Data Type | TTL | Rationale |
|-----------|-----|-----------|
| Credentials/Config | 5 min | Rarely changes, reduces DB load |
| Auth tokens | 30 min | Session tokens are long-lived |
| Host/inventory data | 30-60 sec | Changes infrequently |
| Problems/alerts | 15 sec | Changes frequently, needs freshness |
| Metrics/history | 30 sec | Point-in-time data, cacheable |

### Before Adding New External Integrations

Ask yourself:
- [ ] Does this code have rate limiting?
- [ ] Are read operations cached?
- [ ] Can multiple requests be batched?
- [ ] What happens if this runs in a loop or gets called 100x?
- [ ] Would I be comfortable if a customer saw these API logs?

### What NOT To Do

```go
// BAD: Unbounded API calls in a loop
for _, host := range hosts {
    items, _ := zabbix.GetItems(ctx, host.ID)  // N API calls!
    history, _ := zabbix.GetHistory(ctx, items) // N more API calls!
}

// GOOD: Batched with caching
items, _ := zabbix.GetItemsBatch(ctx, hostIDs, patterns) // 1 cached call
```

## Do NOT

- Skip running tests after changes
- Commit code without verifying tests pass
- Add new functionality without writing tests first or immediately after
- Modify test fixtures without updating related tests
- Write tests that depend on external services (use mocks instead)
- **Call external APIs without rate limiting** - This can flood customer systems
- **Make unbounded API calls in loops** - Always batch or cache
- **Skip caching for read-only external API calls** - Reduces load on customer systems
- **Assume external systems can handle unlimited requests** - They can't, and we'll lose trust
