# Claude Code Instructions for Akmatori

## Project Overview

Akmatori is an AI-powered AIOps platform that receives alerts from monitoring systems (Zabbix, Alertmanager, PagerDuty, Grafana, Datadog), analyzes them using OpenAI's Codex CLI, and executes automated remediation.

## Architecture

- **4-container Docker architecture**: API, Codex Worker, MCP Gateway, PostgreSQL
- **Backend**: Go 1.24+ (API server, Codex worker, MCP gateway)
- **Frontend**: React 19 + TypeScript + Vite + Tailwind
- **Database**: PostgreSQL 16 with GORM

## CRITICAL: Always Verify Changes with Tests

**After ANY code change, you MUST run the appropriate test command to verify your work.**

### Test Commands by Area

| After changing... | Run this command |
|-------------------|------------------|
| Alert adapters (`internal/alerts/adapters/`) | `make test-adapters` |
| MCP Gateway tools (`mcp-gateway/internal/tools/`) | `make test-mcp` |
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

# Full test suite
make test-all         # All tests including MCP gateway

# Pre-commit verification
make verify           # go vet + all tests
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
| Codex worker (`Dockerfile.codex`, skills) | `docker-compose build akmatori-codex && docker-compose up -d akmatori-codex` |
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
| `akmatori-codex` | `Dockerfile.codex`, `.codex/skills/` |
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

## Do NOT

- Skip running tests after changes
- Commit code without verifying tests pass
- Add new functionality without writing tests first or immediately after
- Modify test fixtures without updating related tests
- Write tests that depend on external services (use mocks instead)
