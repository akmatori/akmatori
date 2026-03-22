# Add Catchpoint Tool to MCP Gateway

## Overview

Add a Catchpoint Digital Experience Monitoring integration to the MCP Gateway following the native tool pattern (VictoriaMetrics reference). 12 tool methods: 10 read-only (cached) + 2 write operations. Catchpoint API v4.0, REST with static JWT bearer token auth.

## Context

- Files involved:
  - Create: `mcp-gateway/internal/tools/catchpoint/catchpoint.go`
  - Create: `mcp-gateway/internal/tools/catchpoint/catchpoint_test.go`
  - Modify: `mcp-gateway/internal/tools/schemas.go`
  - Modify: `mcp-gateway/internal/tools/registry.go`
  - Modify: `mcp-gateway/internal/database/db.go`
  - Modify: `internal/services/tool_service.go`
- Related patterns:
  - Follow `mcp-gateway/internal/tools/victoriametrics/victoriametrics.go` (REST + bearer token pattern)
  - Follow `mcp-gateway/internal/tools/zabbix/` for cache/rate-limit conventions
  - Use `mcp-gateway/internal/cache/cache.go` for TTL caching
  - Use `mcp-gateway/internal/ratelimit/limiter.go` for token bucket rate limiting
  - Use `mcp-gateway/internal/validation/` for parameter validation with typo suggestions
- Dependencies: Catchpoint API v4.0 (external, REST)

## Development Approach

- **Testing approach**: TDD for core methods (doRequest, caching), regular for registration/schema
- Complete each task fully before moving to the next
- Follow VictoriaMetrics as the reference implementation for REST + bearer token auth
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Database and service registration (scaffolding)

**Files:**
- Modify: `mcp-gateway/internal/database/db.go`
- Modify: `internal/services/tool_service.go`

- [x] Add `CatchpointEnabled bool` field to `ProxySettings` struct in db.go (around line 304, following VictoriaMetricsEnabled pattern): `CatchpointEnabled bool \`gorm:"default:false" json:"catchpoint_enabled"\``
- [x] Add catchpoint to `EnsureToolTypes()` slice in tool_service.go (around line 154): `{Name: "catchpoint", Description: "Catchpoint Digital Experience Monitoring integration"}`
- [x] Run `make test-mcp` and `make test` - must pass before task 2

### Task 2: Core tool implementation - struct, config, HTTP client

**Files:**
- Create: `mcp-gateway/internal/tools/catchpoint/catchpoint.go`

- [x] Create package `catchpoint` with CatchpointTool struct (4 fields: logger, configCache, responseCache, rateLimiter)
- [x] Define CatchpointConfig struct: URL (default `https://io.catchpoint.com/api`), APIToken, VerifySSL (default true), Timeout (default 30, clamp 5-300), UseProxy, ProxyURL
- [x] Implement `NewCatchpointTool(logger, limiter)` constructor with cache initialization and `Stop()` method
- [x] Implement `getConfig(ctx, incidentID, logicalName...)` using `database.ResolveToolCredentials` with 5-min config cache
- [x] Implement `doRequest(ctx, config, httpMethod, path, queryParams, body)` with: rate limiting first, bearer token auth header, proxy support (explicitly set `transport.Proxy = nil` when not using proxy), TLS verification toggle, `DisableKeepAlives: true`, 5MB response limit
- [x] Implement `cachedGet(ctx, incidentID, path, queryParams, ttl, logicalName...)` cache wrapper using SHA256 response cache keys
- [x] Implement helper: `extractLogicalName(args)`, `responseCacheKey(path, params)`, `getCachedProxySettings(config)`
- [x] Write tests for constructor, Stop, getConfig (with pre-populated configCache), doRequest (auth header, proxy, SSL, 5MB limit, error codes, rate limiting), cachedGet (cache hit/miss verification)
- [x] Run `make test-mcp` - must pass before task 3

### Task 3: Read-only tool methods (10 methods)

**Files:**
- Modify: `mcp-gateway/internal/tools/catchpoint/catchpoint.go`

All methods follow signature: `(ctx context.Context, incidentID string, args map[string]interface{}) (string, error)`

Snake_case params in args mapped to camelCase for Catchpoint API. Use `validation.SuggestParam()` for required param validation.

- [x] Implement `GetAlerts` - GET `/v4/tests/alerts` (15s cache) - params: severity, start_time, end_time, test_ids, page_number, page_size
- [x] Implement `GetAlertDetails` - GET `/v4/tests/alerts/{alertIds}` (15s cache) - required: alert_ids
- [x] Implement `GetTestPerformance` - GET `/v4/tests/explorer/aggregated` (30s cache) - required: test_ids; optional: start_time, end_time, metrics, dimensions
- [x] Implement `GetTestPerformanceRaw` - GET `/v4/tests/explorer/raw` (30s cache) - required: test_ids; optional: start_time, end_time, node_ids, page_number, page_size
- [x] Implement `GetTests` - GET `/v4/tests` (60s cache) - params: test_ids, test_type, folder_id, status, page_number, page_size
- [x] Implement `GetTestDetails` - GET `/v4/tests/{testIds}` (60s cache) - required: test_ids
- [x] Implement `GetTestErrors` - GET `/v4/tests/errors/raw` (15s cache) - params: test_ids, start_time, end_time, page_number, page_size
- [x] Implement `GetInternetOutages` - GET `/v4/iw/outages` (30s cache) - params: start_time, end_time, asn, country, page_number, page_size
- [x] Implement `GetNodes` - GET `/v4/nodes/all` (60s cache) - params: page_number, page_size
- [x] Implement `GetNodeAlerts` - GET `/v4/node/alerts` (15s cache) - params: node_ids, start_time, end_time, page_number, page_size
- [x] Enforce pageSize clamp to max 100 per Catchpoint API in all paginated methods
- [x] Write tests for each method: success case + error case (24 subtests), table-driven param validation, cache hit verification via request counter
- [x] Run `make test-mcp` - must pass before task 4

### Task 4: Write operation methods (2 methods)

**Files:**
- Modify: `mcp-gateway/internal/tools/catchpoint/catchpoint.go`

- [x] Implement `AcknowledgeAlerts` - PATCH `/v4/tests/alerts` - required: alert_ids, action (acknowledge/assign/drop); optional: assignee. Validate action enum. NOT cached.
- [x] Implement `RunInstantTest` - POST `/v4/instanttests/{testId}` - required: test_id. NOT cached.
- [x] Write tests verifying: correct HTTP method, required param validation, responses NOT cached, success and error cases
- [x] Run `make test-mcp` - must pass before task 5

### Task 5: Schema definition

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`

- [x] Add `"catchpoint": getCatchpointSchema()` to `GetToolSchemas()` map
- [x] Implement `getCatchpointSchema()` function (~70 lines) with:
  - Settings: `catchpoint_url` (string, default `https://io.catchpoint.com/api`), `catchpoint_api_token` (string, secret, required), `catchpoint_verify_ssl` (bool, advanced, default true), `catchpoint_timeout` (int, advanced, default 30)
  - Functions: 12 entries matching all public methods, with Parameters as comma-separated lists
  - Required: `["catchpoint_api_token"]`
- [x] Run `make test-mcp` - must pass before task 6

### Task 6: Registry integration

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [x] Add import for `catchpoint` package
- [x] Add constants: `CatchpointRatePerSecond = 10`, `CatchpointBurstCapacity = 20`
- [x] Add fields to Registry struct: `catchpointTool *catchpoint.CatchpointTool`, `catchpointLimit *ratelimit.Limiter`
- [x] Add to `RegisterAllTools()`: create limiter, call `r.registerCatchpointTools()`
- [x] Add to `Stop()`: stop catchpoint tool
- [x] Implement `registerCatchpointTools()` method (~250 lines): instantiate tool, register 12 MCP tools with `r.server.RegisterTool()` using proper `mcp.Tool{Name, Description, InputSchema}` and handler functions
- [x] Run `make test-mcp` - must pass before task 7

### Task 7: Verify acceptance criteria

- [x] Run full test suite: `make test-all`
- [x] Run linter: `golangci-lint run ./mcp-gateway/...`
- [x] Run vet: `go vet ./mcp-gateway/...`
- [x] Run `make verify`
- [x] Manual test: rebuild mcp-gateway container (`docker-compose build mcp-gateway && docker-compose up -d mcp-gateway`)
- [x] Manual test: create Catchpoint tool instance via web UI, verify schema renders correctly
- [x] Manual test: verify tool discovery works (list_tool_types shows catchpoint, list_tools_for_tool_type returns all 12 methods)
- [x] Verify test coverage for catchpoint package meets 80%+

### Task 8: Update documentation

- [ ] Update CLAUDE.md: add Catchpoint to MCP Gateway coverage table if test coverage data available
- [ ] Move this plan to `docs/plans/completed/`
