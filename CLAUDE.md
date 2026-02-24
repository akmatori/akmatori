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
│   ├── alerts/
│   │   ├── adapters/       # Alert source adapters (Zabbix, Alertmanager, etc.)
│   │   └── extraction/     # AI-powered alert extraction from free-form text
│   ├── database/           # GORM models and database logic
│   ├── handlers/           # HTTP/WebSocket handlers
│   ├── middleware/         # Auth, CORS middleware
│   ├── output/             # Agent output parsing (structured blocks)
│   ├── services/           # Business logic layer
│   ├── slack/              # Slack integration (Socket Mode, hot-reload)
│   └── utils/              # Utility functions
├── codex-worker/           # Codex CLI execution worker (separate Go module)
│   └── internal/
│       ├── codex/          # Codex CLI runner
│       ├── orchestrator/   # WebSocket message handling and execution
│       ├── session/        # Session state persistence
│       └── ws/             # WebSocket client for API communication
├── mcp-gateway/            # MCP protocol gateway (separate Go module)
│   └── internal/
│       ├── cache/          # Generic TTL cache implementation
│       ├── database/       # Credential and config retrieval
│       ├── mcp/            # MCP protocol handling
│       ├── ratelimit/      # Token bucket rate limiter
│       └── tools/          # SSH and Zabbix tool implementations
├── web/                    # React frontend
└── tests/fixtures/         # Test payloads and mock data
```

## Codex Worker Architecture

The `codex-worker/` is a **separate Go module** that manages Codex CLI execution:

### Components

| Component | File | Purpose |
|-----------|------|---------|
| Orchestrator | `internal/orchestrator/orchestrator.go` | Main coordinator - handles WebSocket messages, dispatches work |
| Runner | `internal/codex/runner.go` | Executes Codex CLI, manages process lifecycle |
| Session Store | `internal/session/store.go` | Persists session IDs for conversation continuity |
| WS Client | `internal/ws/client.go` | WebSocket communication with API server |

### Message Flow

1. API server sends `new_incident` or `continue_incident` via WebSocket
2. Orchestrator receives message, extracts OpenAI settings and proxy config
3. Runner executes Codex CLI with streaming output
4. Output is streamed back to API via WebSocket
5. On completion, metrics (tokens, execution time) are reported

### WebSocket Message Types

| Type | Direction | Purpose |
|------|-----------|---------|
| `new_incident` | API → Worker | Start new investigation |
| `continue_incident` | API → Worker | Continue existing session |
| `cancel_incident` | API → Worker | Cancel running investigation |
| `device_auth_start` | API → Worker | Start OAuth device flow |
| `output` | Worker → API | Streaming output chunk |
| `completed` | Worker → API | Investigation finished with metrics |
| `error` | Worker → API | Execution failed |

### OpenAI Authentication

The worker supports multiple auth methods:
- **API Key**: Direct `OPENAI_API_KEY` authentication
- **ChatGPT OAuth**: Device code flow with token refresh (for ChatGPT Plus accounts)

OAuth tokens are refreshed automatically and returned to API for storage.

## Slack Integration

The `internal/slack/` package provides real-time Slack monitoring:

### Manager (`manager.go`)

Hot-reloadable Slack connection manager:

```go
// Create and start manager
manager := slack.NewManager()
manager.SetEventHandler(myEventHandler)
manager.Start(ctx)

// Hot-reload on settings change (non-blocking)
manager.TriggerReload()

// Watch for reloads in background
go manager.WatchForReloads(ctx)
```

**Key features:**
- Socket Mode for real-time events (no public webhook needed)
- Hot-reload without restart when settings change in database
- Proxy support for both HTTP API and WebSocket connections
- Thread-safe client access via `GetClient()` and `GetSocketClient()`

### Channel Management (`channels.go`)

Maps Slack channels to alert sources:
- Channels can be designated as "alert channels"
- Bot messages in alert channels trigger investigations
- @mentions in threads allow follow-up questions
- Thread parent messages are fetched for context

### Event Types Handled

| Event | Behavior |
|-------|----------|
| Bot message in alert channel | Create incident, start investigation |
| @mention in alert thread | Continue investigation with question |
| @mention in general channel | Direct response (not investigation) |

## Alert Extraction (`internal/alerts/extraction/`)

AI-powered extraction of structured alert data from free-form text:

### How It Works

1. Slack message (or any text) comes in
2. `AlertExtractor` sends to GPT-4o-mini with extraction prompt
3. Returns structured `NormalizedAlert` with:
   - Alert name, severity, status
   - Summary, description
   - Target host/service
   - Source system identification

### Fallback Mode

If OpenAI is not configured or API fails:
- First line becomes alert name (stripped of emoji prefixes)
- Full text becomes description
- Defaults to `warning` severity, `firing` status

### Usage

```go
extractor := extraction.NewAlertExtractor()
alert, err := extractor.Extract(ctx, messageText)
// Or with custom prompt:
alert, err := extractor.ExtractWithPrompt(ctx, messageText, customPrompt)
```

### Cost Optimization

- Uses `gpt-4o-mini` (fast, cheap)
- Low temperature (0.1) for consistent results
- Message truncated to 3000 chars
- Graceful fallback on any error

## Output Parser (`internal/output/`)

Parses structured blocks from agent output for machine-readable results:

### Structured Block Types

```
[FINAL_RESULT]
status: resolved|unresolved|escalate
summary: One-line summary
actions_taken:
- Action 1
- Action 2
recommendations:
- Recommendation 1
[/FINAL_RESULT]

[ESCALATE]
reason: Why escalation is needed
urgency: low|medium|high|critical
context: Additional context
suggested_actions:
- Suggested action 1
[/ESCALATE]

[PROGRESS]
step: Current investigation step
completed: What's been done
findings_so_far: Current findings
[/PROGRESS]
```

### Usage

```go
parsed := output.Parse(agentOutput)

if parsed.FinalResult != nil {
    // Investigation complete
    fmt.Printf("Status: %s\n", parsed.FinalResult.Status)
}

if parsed.Escalation != nil {
    // Needs human attention
    notifyOnCall(parsed.Escalation.Urgency, parsed.Escalation.Reason)
}

// Clean output has structured blocks stripped
fmt.Println(parsed.CleanOutput)
```

### Slack Formatter (`slack_formatter.go`)

Converts parsed output to Slack Block Kit format for rich messages.

## Current Test Coverage

**Last updated: Feb 24, 2026**

| Package | Coverage | Status |
|---------|----------|--------|
| `internal/alerts/adapters` | 98.4% | ✅ Excellent |
| `internal/utils` | 98.5% | ✅ Excellent |
| `internal/testhelpers` | 59.2% | ⚠️ Needs work |
| `internal/jobs` | 58.1% | ✅ Good (improved from 20.2%) |
| `internal/alerts/extraction` | 40.2% | ⚠️ Needs work |
| `internal/middleware` | 37.9% | ⚠️ Needs work |
| `internal/slack` | 34.6% | ⚠️ Needs work |
| `internal/database` | 32.2% | ⚠️ Needs work |
| `internal/services` | 13.3% | ⚠️ Needs work |
| `internal/handlers` | 9.4% | ⚠️ Needs work |
| `internal/output` | 0.0% | ❌ No tests |

**Priority areas for test improvement:**
1. `internal/output` - Add parser tests
2. `internal/handlers` - Add HTTP handler tests (DB integration tests needed for higher coverage)
3. `internal/services` - Add business logic tests

**Note:** Many handlers require database connections for testing. The current tests focus on:
- Method validation (405 responses)
- Path parameter extraction
- Helper function unit tests (maskToken, maskProxyURL, isValidURL, splitPath)
- Investigation prompt building
- Alert aggregation footer formatting
- Log truncation for Slack
- Mock adapter integration

## Testing Infrastructure

### Test Helpers Package (`internal/testhelpers/`)

The `testhelpers` package provides reusable utilities for testing:

#### HTTP Test Helpers

```go
import "github.com/akmatori/akmatori/internal/testhelpers"

func TestMyHandler(t *testing.T) {
    // Fluent API for HTTP testing
    ctx := testhelpers.NewHTTPTestContext(t, http.MethodPost, "/api/v1/alerts", nil)
    ctx.
        WithAPIKey("test-key").
        WithJSONBody(map[string]string{"name": "test"}).
        ExecuteFunc(myHandler).
        AssertStatus(http.StatusOK).
        AssertBodyContains("success")
    
    // Decode response JSON
    var result MyResponse
    ctx.DecodeJSON(&result)
}
```

#### Mock Alert Adapter

```go
import "github.com/akmatori/akmatori/internal/testhelpers"

func TestAlertProcessing(t *testing.T) {
    // Create mock adapter with predefined responses
    mock := testhelpers.NewMockAlertAdapter("prometheus").
        WithAlerts(
            testhelpers.NewAlertBuilder().
                WithName("HighCPU").
                WithSeverity("critical").
                Build(),
        )
    
    // Or configure it to return an error
    mockWithError := testhelpers.NewMockAlertAdapter("datadog").
        WithParseError(errors.New("invalid payload"))
    
    // Use in tests
    alerts, err := mock.ParsePayload(payload, instance)
}
```

#### Data Builders

```go
import "github.com/akmatori/akmatori/internal/testhelpers"

// Build test alerts
alert := testhelpers.NewAlertBuilder().
    WithName("HighMemory").
    WithSeverity("warning").
    WithHost("prod-server-1").
    WithService("nginx").
    WithLabel("env", "production").
    Build()

// Build test incidents
incident := testhelpers.NewIncidentBuilder().
    WithID(42).
    WithUUID("custom-uuid").
    WithTitle("Database outage").
    WithStatus("investigating").
    Build()
```

#### Assertion Helpers

```go
import "github.com/akmatori/akmatori/internal/testhelpers"

testhelpers.AssertEqual(t, expected, actual, "values should match")
testhelpers.AssertNil(t, err, "operation should succeed")
testhelpers.AssertNotNil(t, result, "result should be returned")
testhelpers.AssertError(t, err, "invalid input should error")
testhelpers.AssertContains(t, body, "success", "response body check")
```

### Benchmark Tests

Critical paths have benchmark tests. Run benchmarks with:

```bash
# Run all benchmarks
go test -bench=. ./...

# Run specific package benchmarks with memory stats
go test -bench=. -benchmem ./internal/alerts/adapters/...

# Run benchmarks matching a pattern
go test -bench=ParsePayload ./internal/alerts/adapters/...
```

**Benchmarked areas:**
- Alert adapter payload parsing (`internal/alerts/adapters/`)
- JSONB operations (`internal/database/`)
- Auth middleware validation (`internal/middleware/`)
- Title generation (`internal/services/`)

### Test Fixture Location

Test fixtures are in `tests/fixtures/`:

```
tests/fixtures/
├── alerts/
│   ├── alertmanager_alert.json
│   ├── grafana_alert.json
│   └── zabbix_alert.json
└── ...
```

Load fixtures in tests:

```go
import "github.com/akmatori/akmatori/internal/testhelpers"

func TestAlertParsing(t *testing.T) {
    // Load raw bytes
    payload := testhelpers.LoadFixture(t, "alerts/alertmanager_alert.json")
    
    // Or load and unmarshal JSON
    var alert AlertPayload
    testhelpers.LoadJSONFixture(t, "alerts/alertmanager_alert.json", &alert)
}
```

### Testing Patterns

#### Table-Driven Tests

Use table-driven tests for comprehensive coverage:

```go
func TestSeverityMapping(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected Severity
    }{
        {"critical maps correctly", "critical", SeverityCritical},
        {"warning maps correctly", "warning", SeverityWarning},
        {"unknown defaults to warning", "unknown", SeverityWarning},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := mapSeverity(tt.input)
            if result != tt.expected {
                t.Errorf("mapSeverity(%q) = %v, want %v", tt.input, result, tt.expected)
            }
        })
    }
}
```

#### HTTP Handler Tests

Use `httptest` for handler tests:

```go
func TestAPIHandler(t *testing.T) {
    handler := NewHandler(deps)
    
    req := httptest.NewRequest(http.MethodPost, "/api/v1/resource", body)
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    
    handler.ServeHTTP(rec, req)
    
    if rec.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", rec.Code)
    }
}
```

#### Mocking External Services

For external API calls, use interfaces and mocks:

```go
type ZabbixClient interface {
    GetHosts(ctx context.Context) ([]Host, error)
}

// In tests:
type mockZabbixClient struct {
    hosts []Host
    err   error
}

func (m *mockZabbixClient) GetHosts(ctx context.Context) ([]Host, error) {
    return m.hosts, m.err
}
```

#### Edge Case Testing

When writing unit tests, cover these edge cases:

1. **Empty/nil inputs**: Empty strings, nil maps, nil slices
2. **Boundary conditions**: Exactly at limits, one over/under limits
3. **Unicode and special characters**: Non-ASCII, emojis, special chars
4. **Error conditions**: Invalid inputs, missing required fields
5. **Concurrency**: Thread safety for shared state

Example pattern for edge case coverage:

```go
func TestFunction_EdgeCases(t *testing.T) {
    tests := []struct {
        name      string
        input     string
        wantErr   bool
        checkFunc func(result) bool // Custom verification
    }{
        {"empty input", "", false, nil},
        {"whitespace only", "   ", false, nil},
        {"unicode chars", "你好世界", false, nil},
        {"exact boundary", strings.Repeat("a", 100), false, nil},
        {"over boundary", strings.Repeat("a", 101), false, func(r result) bool {
            return len(r.Value) <= 100
        }},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := Function(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("unexpected error: %v", err)
            }
            if tt.checkFunc != nil && !tt.checkFunc(result) {
                t.Error("custom check failed")
            }
        })
    }
}
```

#### Database-Free Testing

For services that try to access the database, test only the paths that don't require DB:

```go
func TestService_NoDB(t *testing.T) {
    svc := NewService() // No DB connection
    
    // Test methods that don't need DB
    if !svc.ValidateInput("test") {
        t.Error("validation should pass")
    }
    
    // Test that DB-requiring methods fail gracefully
    _, err := svc.GetData()
    if err == nil {
        t.Error("should fail without DB")
    }
}
```

### Running Tests

```bash
# Run all tests
make test

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out  # View in browser

# Run specific package
go test -v ./internal/handlers/...

# Run single test
go test -v -run TestAlertHandler_HandleWebhook ./internal/handlers/...

# Run with race detection
go test -race ./...
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

## Code Quality & Linting

**Run linting tools regularly to catch issues early.**

### Linting Commands

```bash
# Basic vet check (fast)
go vet ./...

# Staticcheck for deeper analysis (recommended)
staticcheck ./...

# golangci-lint for comprehensive linting (requires Go version matching project)
golangci-lint run --timeout 5m
```

### Common Staticcheck Fixes

| Issue | Fix |
|-------|-----|
| S1031: unnecessary nil check around range | Remove `if x != nil` - ranging over nil map/slice is safe |
| U1000: unused function | Remove function or add `//nolint:unused` if kept for future use |
| SA5011: possible nil pointer dereference | Use `t.Fatal()` instead of `t.Error()` before dereferencing |
| SA4006: value is never used | Remove assignment or use blank identifier `_` |
| SA1019: deprecated function | Replace with recommended alternative (e.g., `strings.Title` → `cases.Title`) |

### Go Idioms to Follow

```go
// Nil check around range is unnecessary - ranging over nil is safe
// BAD:
if myMap != nil {
    for k, v := range myMap { ... }
}
// GOOD:
for k, v := range myMap { ... }

// Use t.Fatal for nil checks in tests to prevent nil pointer dereference
// BAD:
if svc == nil {
    t.Error("service is nil")  // continues, then crashes on next line
}
// GOOD:
if svc == nil {
    t.Fatal("service is nil")  // stops test immediately
}

// Remove unused code rather than leaving it commented
// If keeping for future use, add clear NOTE comment explaining why
```

### Error Handling Patterns

**Always check return values from functions that can fail.** Golangci-lint's `errcheck` will flag unchecked errors.

#### HTTP Response Writing

```go
// BAD: w.Write error not checked
w.Write([]byte(`{"error":"message"}`))

// GOOD: Log if write fails (client disconnected, etc.)
if _, err := w.Write([]byte(`{"error":"message"}`)); err != nil {
    log.Printf("Failed to write error response: %v", err)
}

// For json.Encode:
if err := json.NewEncoder(w).Encode(response); err != nil {
    log.Printf("Failed to encode response: %v", err)
}
```

#### Slack API Calls (Fire-and-Forget)

```go
// BAD: Reaction/message errors not handled
slackClient.AddReaction("white_check_mark", itemRef)
slackClient.PostMessage(channelID, options...)

// GOOD: Log failures but don't abort on non-critical operations
if err := slackClient.AddReaction("white_check_mark", itemRef); err != nil {
    log.Printf("Failed to add reaction: %v", err)
}
if _, _, err := slackClient.PostMessage(channelID, options...); err != nil {
    log.Printf("Failed to post message: %v", err)
}
```

#### Database/Service Updates in Callbacks

```go
// BAD: UpdateIncidentLog error ignored
callback := IncidentCallback{
    OnOutput: func(output string) {
        skillService.UpdateIncidentLog(uuid, output)
    },
}

// GOOD: Log errors in callbacks
callback := IncidentCallback{
    OnOutput: func(output string) {
        if err := skillService.UpdateIncidentLog(uuid, output); err != nil {
            log.Printf("Failed to update incident log: %v", err)
        }
    },
}
```

#### Filesystem Operations

```go
// BAD: MkdirAll error ignored
os.MkdirAll(scriptsDir, 0755)

// GOOD: Log non-critical filesystem errors
if err := os.MkdirAll(scriptsDir, 0755); err != nil {
    log.Printf("Failed to create scripts directory %s: %v", scriptsDir, err)
}
```

#### Tests: Always Check Decode/Unmarshal Errors

```go
// BAD: Decode errors not checked in tests
json.NewDecoder(w.Body).Decode(&response)

// GOOD: Fail test if decode fails
if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
    t.Fatalf("Failed to decode response: %v", err)
}
```

#### Map Nil Checks in Conditions

```go
// BAD: Unnecessary nil check (len() on nil map returns 0)
if decoded.TargetLabels != nil && len(decoded.TargetLabels) > 0 {
    // ...
}

// GOOD: Just check length
if len(decoded.TargetLabels) > 0 {
    // ...
}
```

### Pre-Commit Quality Checklist

Before committing, verify:
- [ ] `go vet ./...` passes with no output
- [ ] `staticcheck ./...` passes with no output
- [ ] `go test ./...` passes
- [ ] No unused imports (goimports or IDE will catch these)

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
