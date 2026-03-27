# Grafana Full Observability Suite Tool

## Overview

Add a first-class Grafana MCP tool type to the MCP Gateway, providing the AI agent with access to Grafana dashboards, alerting (unified alerting API), data source proxy queries (Prometheus/Loki), and annotations. Follows the established Catchpoint/VictoriaMetrics tool patterns with rate limiting, TTL caching, and per-incident authorization.

## Context

- Files involved:
  - `mcp-gateway/internal/tools/grafana/grafana.go` (new - main implementation)
  - `mcp-gateway/internal/tools/grafana/grafana_test.go` (new - tests)
  - `mcp-gateway/internal/tools/schemas.go` (add Grafana schema)
  - `mcp-gateway/internal/tools/registry.go` (register Grafana tools)
- Related patterns: `mcp-gateway/internal/tools/catchpoint/` (closest reference - HTTP API tool with Bearer auth, caching, rate limiting)
- Dependencies: Grafana HTTP API (v9+), no new Go modules required

## Development Approach

- **Testing approach**: Regular (code first, then tests) - mirror catchpoint test patterns with httptest servers
- Complete each task fully before moving to the next
- Follow the existing Catchpoint tool structure exactly for consistency
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Core Grafana tool struct and HTTP helpers

**Files:**
- Create: `mcp-gateway/internal/tools/grafana/grafana.go`

- [x] Create `grafana` package with `GrafanaConfig` struct (URL, APIToken, VerifySSL, Timeout, UseProxy, ProxyURL)
- [x] Implement `GrafanaTool` struct with configCache, responseCache, rateLimiter (same as CatchpointTool)
- [x] Implement `NewGrafanaTool()`, `Stop()`, `getConfig()`, `getCachedProxySettings()`
- [x] Implement `doRequest()` with rate limiting, proxy support, SSL config, Bearer token auth
- [x] Implement `cachedGet()` helper with TTL-based response caching
- [x] Implement `doPost()` helper for write operations (annotations, silences) - no caching
- [x] Add helper functions: `configCacheKey()`, `responseCacheKey()`, `extractLogicalName()`, `clampTimeout()`
- [x] Write tests: constructor, lifecycle, config caching, HTTP helpers, rate limiting, error handling
- [x] Run `make test-mcp` - must pass

### Task 2: Dashboard and search tools

**Files:**
- Modify: `mcp-gateway/internal/tools/grafana/grafana.go`

- [ ] Implement `SearchDashboards()` - search/list dashboards (GET /api/search with type=dash-db, query, tag, folder filters)
- [ ] Implement `GetDashboardByUID()` - get full dashboard model by UID (GET /api/dashboards/uid/:uid)
- [ ] Implement `GetDashboardPanels()` - extract panel list from a dashboard for quick overview
- [ ] Write tests for each dashboard tool method with httptest mock server
- [ ] Run `make test-mcp` - must pass

### Task 3: Alerting tools (Grafana Unified Alerting)

**Files:**
- Modify: `mcp-gateway/internal/tools/grafana/grafana.go`

- [ ] Implement `GetAlertRules()` - list alert rules (GET /api/v1/provisioning/alert-rules)
- [ ] Implement `GetAlertInstances()` - get firing/pending alert instances (GET /api/alertmanager/grafana/api/v2/alerts)
- [ ] Implement `GetAlertRuleByUID()` - get specific rule details (GET /api/v1/provisioning/alert-rules/:uid)
- [ ] Implement `SilenceAlert()` - create a silence (POST /api/alertmanager/grafana/api/v2/silences) - no caching
- [ ] Write tests for each alerting tool method
- [ ] Run `make test-mcp` - must pass

### Task 4: Data source proxy and annotation tools

**Files:**
- Modify: `mcp-gateway/internal/tools/grafana/grafana.go`

- [ ] Implement `ListDataSources()` - list configured data sources (GET /api/datasources)
- [ ] Implement `QueryDataSource()` - query a data source via Grafana proxy (POST /api/ds/query) with datasource UID, expression, time range
- [ ] Implement `QueryPrometheus()` - convenience wrapper for Prometheus-type data sources (instant and range queries via proxy)
- [ ] Implement `QueryLoki()` - convenience wrapper for Loki-type data sources (log queries via proxy)
- [ ] Implement `CreateAnnotation()` - create annotation on a dashboard/panel (POST /api/annotations) - no caching
- [ ] Implement `GetAnnotations()` - list annotations with filters (GET /api/annotations)
- [ ] Write tests for data source and annotation tools
- [ ] Run `make test-mcp` - must pass

### Task 5: Schema and registry integration

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Add `getGrafanaSchema()` to schemas.go with settings schema (url, api_token, verify_ssl, timeout, use_proxy, proxy_url) and all function definitions
- [ ] Add `"grafana": getGrafanaSchema()` to `GetToolSchemas()`
- [ ] Add `grafanaTool` and `grafanaLimit` fields to `Registry` struct
- [ ] Implement `registerGrafanaTools()` method with MCP tool registrations for all Grafana functions (search_dashboards, get_dashboard, get_dashboard_panels, get_alert_rules, get_alert_instances, get_alert_rule, silence_alert, list_data_sources, query_data_source, query_prometheus, query_loki, create_annotation, get_annotations)
- [ ] Call `registerGrafanaTools()` from `RegisterAllTools()` with rate limiter (10 req/sec, burst 20)
- [ ] Write tests for schema completeness and registration
- [ ] Run `make test-mcp` - must pass

### Task 6: Verify acceptance criteria

- [ ] Run full test suite (`make test-mcp`)
- [ ] Run linter (`golangci-lint run ./mcp-gateway/...`)
- [ ] Verify test coverage for grafana package meets 80%+

### Task 7: Update documentation

- [ ] Update CLAUDE.md to add Grafana to the tool types list and coverage table
- [ ] Move this plan to `docs/plans/completed/`
