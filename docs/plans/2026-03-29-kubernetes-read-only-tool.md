# Kubernetes Read-Only Diagnostics Tool

## Overview
Add a read-only Kubernetes tool type to the MCP Gateway, following the NetBox pattern. This tool enables the AI agent to query cluster state (pods, nodes, deployments, services, events, logs) for incident investigation without making any mutations.

## Context
- Files involved: `mcp-gateway/internal/tools/k8s/`, `mcp-gateway/internal/tools/registry.go`, `mcp-gateway/internal/tools/schemas.go`, `mcp-gateway/internal/database/db.go`
- Related patterns: NetBox tool (`mcp-gateway/internal/tools/netbox/`) is the closest reference — read-only, token auth, caching, rate limiting
- Dependencies: Kubernetes API (REST), no Go client-go library needed (raw HTTP like other tools)

## Development Approach
- **Testing approach**: TDD — write tests alongside each tool method
- Complete each task fully before moving to the next
- Follow existing NetBox patterns exactly for consistency
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Tool Methods (Read-Only)

| Method | K8s API Path | Cache TTL | Description |
|--------|-------------|-----------|-------------|
| `get_pods` | `/api/v1/namespaces/{ns}/pods` | 30s | List pods with label/field selectors |
| `get_pod_detail` | `/api/v1/namespaces/{ns}/pods/{name}` | 30s | Detailed pod info (status, containers, conditions) |
| `get_pod_logs` | `/api/v1/namespaces/{ns}/pods/{name}/log` | 15s | Container logs (tail lines, since, container) |
| `get_deployments` | `/apis/apps/v1/namespaces/{ns}/deployments` | 60s | List deployments with selectors |
| `get_deployment_detail` | `/apis/apps/v1/namespaces/{ns}/deployments/{name}` | 60s | Deployment detail (replicas, conditions, strategy) |
| `get_services` | `/api/v1/namespaces/{ns}/services` | 120s | List services |
| `get_nodes` | `/api/v1/nodes` | 120s | List nodes with conditions and allocatable resources |
| `get_node_detail` | `/api/v1/nodes/{name}` | 120s | Node detail (conditions, capacity, taints) |
| `get_events` | `/api/v1/namespaces/{ns}/events` | 15s | Namespace events (warnings, errors) |
| `get_namespaces` | `/api/v1/namespaces` | 120s | List all namespaces |
| `get_statefulsets` | `/apis/apps/v1/namespaces/{ns}/statefulsets` | 60s | List statefulsets |
| `get_daemonsets` | `/apis/apps/v1/namespaces/{ns}/daemonsets` | 60s | List daemonsets |
| `get_jobs` | `/apis/batch/v1/namespaces/{ns}/jobs` | 30s | List jobs |
| `get_cronjobs` | `/apis/batch/v1/namespaces/{ns}/cronjobs` | 60s | List cronjobs |
| `get_configmaps` | `/api/v1/namespaces/{ns}/configmaps` | 120s | List configmaps (names only, no data) |
| `get_ingresses` | `/apis/networking.k8s.io/v1/namespaces/{ns}/ingresses` | 120s | List ingresses |
| `api_request` | Any GET path | 60s | Generic GET for any K8s API endpoint |

## Implementation Steps

### Task 1: Schema and Database Foundation

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`
- Modify: `mcp-gateway/internal/database/db.go`

- [x] Add `K8sEnabled bool` field to `ProxySettings` struct in `database/db.go` with GORM column tag `k8s_enabled`
- [x] Add `"kubernetes": getK8sSchema()` entry in `GetToolSchemas()` map in `schemas.go`
- [x] Implement `getK8sSchema()` returning full `ToolTypeSchema` with:
  - Settings: `k8s_url` (required), `k8s_token` (required, secret), `k8s_ca_cert` (secret, advanced), `k8s_verify_ssl` (boolean, default true, advanced), `k8s_timeout` (integer, 5-300, default 30, advanced)
  - All tool function definitions listed above
- [x] Verify schema loads correctly: `make test-mcp` must pass

### Task 2: Core K8s Tool Implementation (Config, HTTP Client, Caching)

**Files:**
- Create: `mcp-gateway/internal/tools/k8s/k8s.go`
- Create: `mcp-gateway/internal/tools/k8s/k8s_test.go`

- [x] Create `k8s` package with `K8sTool` struct containing: logger, configCache (5min TTL), responseCache (default 60s TTL), rateLimiter
- [x] Implement `NewK8sTool(logger, limiter)` constructor and `Stop()` cleanup
- [x] Implement `getConfig(ctx, incidentID, args)` — resolve credentials from DB via `database.ResolveToolCredentials`, apply proxy settings, cache result
- [x] Implement `cachedGet(ctx, config, path, params, ttl)` — HTTP GET with Bearer token auth, TLS config, proxy support, response caching, rate limiting, 5MB response limit
- [x] Implement helper: `buildURL(baseURL, path, params)` for query string construction
- [x] Write tests: config caching, cache hits/misses, rate limiting wait, proxy toggle, TLS verify toggle, error responses (401, 403, 404, 500), response size limit
- [x] Run `make test-mcp` — must pass

### Task 3: Namespace, Pod, and Log Methods

**Files:**
- Modify: `mcp-gateway/internal/tools/k8s/k8s.go`
- Modify: `mcp-gateway/internal/tools/k8s/k8s_test.go`

- [ ] Implement `GetNamespaces(ctx, incidentID, args)` — list namespaces (120s cache)
- [ ] Implement `GetPods(ctx, incidentID, args)` — params: namespace (required), name, label_selector, field_selector, limit (30s cache)
- [ ] Implement `GetPodDetail(ctx, incidentID, args)` — params: namespace, name (both required) (30s cache)
- [ ] Implement `GetPodLogs(ctx, incidentID, args)` — params: namespace, name (required), container, tail_lines (default 100), since_seconds, previous (15s cache)
- [ ] Implement `GetEvents(ctx, incidentID, args)` — params: namespace (required), field_selector, limit (15s cache)
- [ ] Write tests for each method: success response, missing required params, filtering, pagination via limit, log tail lines
- [ ] Run `make test-mcp` — must pass

### Task 4: Workload Methods (Deployments, StatefulSets, DaemonSets, Jobs)

**Files:**
- Modify: `mcp-gateway/internal/tools/k8s/k8s.go`
- Modify: `mcp-gateway/internal/tools/k8s/k8s_test.go`

- [ ] Implement `GetDeployments(ctx, incidentID, args)` — params: namespace (required), name, label_selector, limit (60s cache)
- [ ] Implement `GetDeploymentDetail(ctx, incidentID, args)` — params: namespace, name (both required) (60s cache)
- [ ] Implement `GetStatefulSets(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (60s cache)
- [ ] Implement `GetDaemonSets(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (60s cache)
- [ ] Implement `GetJobs(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (30s cache)
- [ ] Implement `GetCronJobs(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (60s cache)
- [ ] Write tests for each method
- [ ] Run `make test-mcp` — must pass

### Task 5: Node, Service, Networking, and Generic Methods

**Files:**
- Modify: `mcp-gateway/internal/tools/k8s/k8s.go`
- Modify: `mcp-gateway/internal/tools/k8s/k8s_test.go`

- [ ] Implement `GetNodes(ctx, incidentID, args)` — params: label_selector, limit (120s cache)
- [ ] Implement `GetNodeDetail(ctx, incidentID, args)` — params: name (required) (120s cache)
- [ ] Implement `GetServices(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (120s cache)
- [ ] Implement `GetConfigMaps(ctx, incidentID, args)` — params: namespace (required), label_selector, limit — return names/metadata only, NOT data (120s cache)
- [ ] Implement `GetIngresses(ctx, incidentID, args)` — params: namespace (required), label_selector, limit (120s cache)
- [ ] Implement `APIRequest(ctx, incidentID, args)` — params: path (required), params (optional map) — generic GET for any K8s API endpoint (60s cache). Validate path starts with `/api` or `/apis`
- [ ] Write tests for each method, including APIRequest path validation
- [ ] Run `make test-mcp` — must pass

### Task 6: Registry Integration

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Add import for `k8s` package
- [ ] Add constants: `K8sRatePerSecond = 10`, `K8sBurstCapacity = 20`
- [ ] Add fields to Registry struct: `k8sTool *k8s.K8sTool`, `k8sLimit *ratelimit.Limiter`
- [ ] In `RegisterAllTools()`: create rate limiter, call `r.registerK8sTools()`
- [ ] In `Stop()`: add `r.k8sTool.Stop()` cleanup
- [ ] Implement `registerK8sTools()` — register all 17 tool methods with MCP Tool definitions (name, description, inputSchema with properties and required fields), wire each to the corresponding K8sTool method
- [ ] Run `make test-mcp` — must pass

### Task 7: Verify Acceptance Criteria

- [ ] Manual test: create a kubernetes tool instance via API, verify schema appears in `GET /api/tool-types`
- [ ] Run full test suite: `make test-mcp`
- [ ] Run linter: `cd mcp-gateway && go vet ./...`
- [ ] Verify test coverage for `k8s` package meets 80%+
- [ ] Run: `make verify`

### Task 8: Update Documentation

- [ ] Update CLAUDE.md:
  - Add `mcp-gateway/internal/tools/k8s/` to Key Directories section
  - Add `kubernetes` to gateway tools table
  - Add K8s cache TTLs to Cache TTLs table
  - Add kubernetes to implementation reference list
  - Add k8s test coverage to MCP Gateway coverage table
- [ ] Move this plan to `docs/plans/completed/`
