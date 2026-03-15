# Add VictoriaMetrics Tool Type

## Overview

Add a new "victoria_metrics" tool type to Akmatori, enabling AI agents to query VictoriaMetrics time-series databases during incident investigations. The tool exposes a core query set: `instant_query`, `range_query`, `label_values`, `series`, and a generic `api_request` fallback. It follows the same architecture as the existing Zabbix tool: MCP Gateway implementation with rate limiting and caching, Python agent wrapper, schema-driven UI configuration, and SKILL.md generation.

## Context

- Files involved:
  - `mcp-gateway/internal/tools/victoriametrics/victoriametrics.go` (new)
  - `mcp-gateway/internal/tools/victoriametrics/victoriametrics_test.go` (new)
  - `mcp-gateway/internal/tools/registry.go` (modify)
  - `mcp-gateway/internal/tools/schemas.go` (modify)
  - `agent-worker/tools/victoriametrics/__init__.py` (new)
  - `internal/services/tool_service.go` (modify)
  - `internal/services/skill_prompt_service.go` (modify)
- Related patterns: Zabbix tool implementation (`mcp-gateway/internal/tools/zabbix/`)
- Dependencies: VictoriaMetrics HTTP API (Prometheus-compatible), `net/http`, existing `cache` and `ratelimit` packages

## Architecture Notes

### VictoriaMetrics API

VictoriaMetrics exposes a Prometheus-compatible HTTP API over REST (not JSON-RPC like Zabbix). Key endpoints:

| Function | HTTP Method | Endpoint | Key Params |
|----------|-------------|----------|------------|
| `instant_query` | GET/POST | `/api/v1/query` | `query` (PromQL), `time`, `step` |
| `range_query` | GET/POST | `/api/v1/query_range` | `query`, `start`, `end`, `step` |
| `label_values` | GET | `/api/v1/label/{label_name}/values` | `match[]`, `start`, `end` |
| `series` | GET/POST | `/api/v1/series` | `match[]`, `start`, `end` |
| `api_request` | GET/POST | `{user-specified path}` | `{user-specified params}` |

### Authentication

VictoriaMetrics supports multiple auth methods. The tool should support:

1. **Bearer token** (`Authorization: Bearer <token>`) - user's current setup
2. **Basic auth** (`Authorization: Basic <base64>`) - common for vmauth proxy
3. **No auth** - for internal/trusted networks

The `auth_method` setting field with enum `["none", "bearer_token", "basic_auth"]` controls which auth header is sent.

### Response Format

All Prometheus-compatible endpoints return:
```json
{
  "status": "success" | "error",
  "data": { ... },
  "errorType": "...",
  "error": "..."
}
```

The tool should check `status == "success"` and return the `data` field contents, or return the error message.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Follow existing Zabbix tool patterns exactly (rate limiting, caching, credential resolution)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: MCP Gateway - VictoriaMetrics tool implementation

**Files:**
- Create: `mcp-gateway/internal/tools/victoriametrics/victoriametrics.go`

- [x] Create `VictoriaMetricsTool` struct with fields: `logger`, `configCache`, `responseCache`, `rateLimiter`
- [x] Define `VMConfig` struct with fields: `URL` (base URL), `AuthMethod` (none/bearer_token/basic_auth), `BearerToken`, `Username`, `Password`, `VerifySSL`, `Timeout`, `UseProxy`, `ProxyURL`
- [x] Implement `NewVictoriaMetricsTool(logger, limiter)` constructor (same pattern as `NewZabbixTool`)
- [x] Implement `Stop()` for cache cleanup
- [x] Implement `getConfig(ctx, incidentID, instanceID)` - resolve credentials via `database.ResolveToolCredentials`, parse settings (`vm_url`, `vm_auth_method`, `vm_bearer_token`, `vm_username`, `vm_password`, `vm_verify_ssl`, `vm_timeout`), apply defaults, cache result
- [x] Implement `doRequest(ctx, config, method, path, queryParams)` - generic HTTP request with rate limiting, proxy support, SSL config, auth header injection based on `config.AuthMethod`
- [x] Implement `cachedRequest(ctx, incidentID, method, path, params, ttl, instanceID)` - cached wrapper around `doRequest`
- [x] Implement `InstantQuery(ctx, incidentID, args)` - calls `/api/v1/query` with `query`, optional `time`, `step`, `timeout` params; cache 15s
- [x] Implement `RangeQuery(ctx, incidentID, args)` - calls `/api/v1/query_range` with `query`, `start`, `end`, `step`, `timeout` params; cache 30s
- [x] Implement `LabelValues(ctx, incidentID, args)` - calls `/api/v1/label/{label_name}/values` with optional `match[]`, `start`, `end` params; cache 60s
- [x] Implement `Series(ctx, incidentID, args)` - calls `/api/v1/series` with `match[]`, `start`, `end` params; cache 30s
- [x] Implement `APIRequest(ctx, incidentID, args)` - generic fallback: user provides `path`, `method` (GET/POST), `params`; no caching
- [x] Implement helper `extractInstanceID(args)` (reuse pattern from zabbix)
- [x] Implement `parsePrometheusResponse(body)` - check `status`, extract `data` or return error
- [x] Write tests for this task
- [x] Run `make test-mcp` - must pass before task 2

### Task 2: MCP Gateway - Tool registration and schema

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`
- Modify: `mcp-gateway/internal/tools/schemas.go`

- [ ] Add `victoriametrics` import to `registry.go`
- [ ] Add `vmTool *victoriametrics.VictoriaMetricsTool` and `vmLimit *ratelimit.Limiter` fields to `Registry` struct
- [ ] Add rate limiter constants: `VMRatePerSecond = 10`, `VMBurstCapacity = 20`
- [ ] Create `registerVictoriaMetricsTools()` method with 5 tool registrations:
  - `victoriametrics.instant_query` - required: `query`; optional: `time`, `step`, `timeout`, `tool_instance_id`
  - `victoriametrics.range_query` - required: `query`, `start`, `end`, `step`; optional: `timeout`, `tool_instance_id`
  - `victoriametrics.label_values` - required: `label_name`; optional: `match`, `start`, `end`, `tool_instance_id`
  - `victoriametrics.series` - required: `match`; optional: `start`, `end`, `tool_instance_id`
  - `victoriametrics.api_request` - required: `path`; optional: `method`, `params`, `tool_instance_id`
- [ ] Call `registerVictoriaMetricsTools()` from `RegisterAllTools()`
- [ ] Add `r.vmTool.Stop()` to `Stop()` method
- [ ] Add `getVictoriaMetricsSchema()` to `schemas.go` with:
  - Settings: `vm_url` (required), `vm_auth_method` (enum: none/bearer_token/basic_auth, default: bearer_token), `vm_bearer_token` (secret), `vm_username` (advanced), `vm_password` (secret, advanced), `vm_verify_ssl` (advanced, default: true), `vm_timeout` (advanced, default: 30)
  - Functions list for all 5 tools
- [ ] Register `"victoria_metrics"` in `GetToolSchemas()` map
- [ ] Write tests for schema validation
- [ ] Run `make test-mcp` - must pass before task 3

### Task 3: Agent worker - Python wrapper

**Files:**
- Create: `agent-worker/tools/victoriametrics/__init__.py`

- [ ] Create `__init__.py` following the exact pattern of `zabbix/__init__.py`
- [ ] Add module docstring with usage examples
- [ ] Add `sys.path.insert` and `from mcp_client import call` imports
- [ ] Implement `instant_query(query, time=None, step=None, timeout=None, tool_instance_id=None)` - calls `victoriametrics.instant_query`
- [ ] Implement `range_query(query, start, end, step, timeout=None, tool_instance_id=None)` - calls `victoriametrics.range_query`
- [ ] Implement `label_values(label_name, match=None, start=None, end=None, tool_instance_id=None)` - calls `victoriametrics.label_values`
- [ ] Implement `series(match, start=None, end=None, tool_instance_id=None)` - calls `victoriametrics.series`
- [ ] Implement `api_request(path, method="GET", params=None, tool_instance_id=None)` - calls `victoriametrics.api_request`
- [ ] Write tests for the Python wrapper
- [ ] Run `make test-agent` - must pass before task 4

### Task 4: API server - Tool type registration and SKILL.md generation

**Files:**
- Modify: `internal/services/tool_service.go`
- Modify: `internal/services/skill_prompt_service.go`

- [ ] Add `{Name: "victoria_metrics", Description: "VictoriaMetrics time-series database integration"}` to `EnsureToolTypes()` in `tool_service.go`
- [ ] Add `case "victoria_metrics":` to `generateToolUsageExample()` in `skill_prompt_service.go` with Python usage examples:
  ```
  from victoriametrics import instant_query, range_query, label_values, series, api_request
  result = instant_query("up", tool_instance_id=N)
  result = range_query("rate(http_requests_total[5m])", start="2h", end="now", step="1m", tool_instance_id=N)
  result = label_values("__name__", tool_instance_id=N)
  result = series(match=["up"], tool_instance_id=N)
  result = api_request("/api/v1/status/tsdb", tool_instance_id=N)
  ```
- [ ] No changes needed to `extractToolDetails()` (VM doesn't expose agent-relevant config, same as Zabbix)
- [ ] Write/update tests for the new switch cases
- [ ] Run `make test` - must pass before task 5

### Task 5: MCP Gateway - Integration tests

**Files:**
- Create: `mcp-gateway/internal/tools/victoriametrics/victoriametrics_test.go`

- [ ] Write unit tests for `getConfig()` - verify credential parsing, defaults, cache hits
- [ ] Write unit tests for `parsePrometheusResponse()` - success case, error case, malformed JSON
- [ ] Write unit tests for auth header injection: bearer token, basic auth, no auth
- [ ] Write unit tests for `InstantQuery` with httptest mock server - verify query params, response parsing
- [ ] Write unit tests for `RangeQuery` with httptest mock server - verify required params, step calculation
- [ ] Write unit tests for `LabelValues` with httptest mock server - verify URL path construction with label name
- [ ] Write unit tests for `Series` with httptest mock server
- [ ] Write unit tests for `APIRequest` with httptest mock server - verify custom path/method
- [ ] Write tests for response caching (call twice, verify only one HTTP request)
- [ ] Write tests for rate limiter integration
- [ ] Run `make test-mcp` - must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] Manual test: create a VictoriaMetrics tool instance via the web UI, verify form renders correctly with auth method dropdown
- [ ] Manual test: assign the tool to a skill, verify SKILL.md contains correct Python usage examples
- [ ] Manual test: run an instant query through the agent, verify results are returned
- [ ] Run full test suite: `make test-all`
- [ ] Run linter: `golangci-lint run`
- [ ] Verify test coverage meets 80%+ for new code

### Task 7: Update documentation

- [ ] Update CLAUDE.md:
  - Add `victoria_metrics` to the tool types list in the Agent Worker section
  - Add wrapper entry to the Python Wrappers table
  - Add `victoriametrics` to the Key Directories tools listing
- [ ] Move this plan to `docs/plans/completed/`
