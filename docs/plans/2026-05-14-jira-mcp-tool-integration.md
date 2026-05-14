# Jira MCP Tool Integration

## Overview

Add a Jira tool to the MCP Gateway that supports both Atlassian Cloud and self-hosted Jira Server/Data Center via the REST API. The tool ships with read-only methods enabled by default; common write methods (`add_comment`, `transition_issue`, `create_issue`, `update_issue`) are gated by a per-instance `jira_allow_writes` boolean that defaults to `false`, so operators must explicitly opt in. Follows the existing NetBox/PagerDuty tool pattern (Go package, schema, registry, ProxySettings entry).

## Context

- Files involved:
  - `mcp-gateway/internal/tools/jira/jira.go` (new)
  - `mcp-gateway/internal/tools/jira/jira_test.go` (new)
  - `mcp-gateway/internal/tools/schemas.go` (modify — add `getJiraSchema()`, register in `GetToolSchemas`)
  - `mcp-gateway/internal/tools/registry.go` (modify — add limiter, tool field, `registerJiraTools()`, wire into `RegisterAllTools` and `Stop`)
  - `mcp-gateway/internal/database/db.go` (modify — add `JiraEnabled` field to `ProxySettings`)
  - `CLAUDE.md` (modify — add Jira to the tool list and patterns section)
- Related patterns: NetBox (`mcp-gateway/internal/tools/netbox/`) and PagerDuty (`mcp-gateway/internal/tools/pagerduty/`) — token auth, caching, rate limiting, proxy support, logical-name routing
- Dependencies: Jira REST API — Cloud uses `/rest/api/3`, Server/DC uses `/rest/api/2`. Both share the same endpoint shapes for the methods we expose. Self-hosted instances commonly authenticate via Personal Access Token (Bearer); Cloud authenticates via email + API token over Basic auth.

## Jira API Authentication

The schema exposes a `jira_auth_type` enum to cover both deployment models:

- `cloud_basic` — Atlassian Cloud: `Authorization: Basic base64(jira_username:jira_api_token)` (username = email, token = API token from id.atlassian.com)
- `server_bearer` — Self-hosted Jira Server / Data Center: `Authorization: Bearer <jira_api_token>` (Personal Access Token)
- `basic` — Generic Basic auth for self-hosted with username + password/token

API base path is derived from `jira_api_version` (`"2"` for self-hosted, `"3"` for Cloud; default `"3"`).

## Tool Methods

### Read-only (always available)

| Tool | Endpoint | Purpose |
|------|----------|---------|
| `jira.search_issues` | `GET /rest/api/{v}/search?jql=...` | JQL search with paging |
| `jira.get_issue` | `GET /rest/api/{v}/issue/{key}` | Issue detail (supports `expand`) |
| `jira.get_issue_comments` | `GET /rest/api/{v}/issue/{key}/comment` | Comments for an issue |
| `jira.get_issue_transitions` | `GET /rest/api/{v}/issue/{key}/transitions` | Available transitions for an issue |
| `jira.get_issue_changelog` | `GET /rest/api/{v}/issue/{key}/changelog` (or `expand=changelog`) | Changelog entries |
| `jira.get_projects` | `GET /rest/api/{v}/project/search` | List projects |
| `jira.get_project` | `GET /rest/api/{v}/project/{key}` | Project detail |
| `jira.search_users` | `GET /rest/api/{v}/user/search` | Search users by query |
| `jira.api_request` | `GET /rest/{path}` | Generic read-only GET passthrough |

### Write (gated by `jira_allow_writes=true`)

| Tool | Endpoint | Purpose |
|------|----------|---------|
| `jira.add_comment` | `POST /rest/api/{v}/issue/{key}/comment` | Add a comment to an issue |
| `jira.transition_issue` | `POST /rest/api/{v}/issue/{key}/transitions` | Move an issue through workflow |
| `jira.create_issue` | `POST /rest/api/{v}/issue` | Create a new issue |
| `jira.update_issue` | `PUT /rest/api/{v}/issue/{key}` | Update fields on an existing issue |

Write methods short-circuit with a clear error message (`"writes disabled for this Jira instance; enable jira_allow_writes to allow"`) when the flag is false. The error mentions the setting name so operators see the fix path in logs and agent output.

## Cache TTLs

- Config/credentials: 5 min
- Issue search results: 15 sec
- Issue detail / comments / transitions: 30 sec
- Changelog: 60 sec
- User search: 60 sec
- Project list/detail: 120 sec
- Write paths: not cached

## Rate Limiting

- `JiraRatePerSecond = 10`, `JiraBurstCapacity = 20` (matches NetBox/PagerDuty)

## Development Approach

- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Follow existing NetBox / PagerDuty patterns exactly (same file layout, naming, helper structure)
- CRITICAL: every task MUST include new/updated tests
- CRITICAL: all tests must pass before starting next task

## Implementation Steps

### Task 1: ProxySettings and schema

Files:
- Modify: `mcp-gateway/internal/database/db.go`
- Modify: `mcp-gateway/internal/tools/schemas.go`

- [x] Add `JiraEnabled bool` to `ProxySettings` with `gorm:"default:false" json:"jira_enabled"`
- [x] Add `getJiraSchema()` in `schemas.go` with required fields `jira_url`, `jira_auth_type` (enum: `cloud_basic`, `server_bearer`, `basic`), `jira_api_token`; optional `jira_username` (required for `cloud_basic`/`basic`), `jira_api_version` (enum `"2"`/`"3"`, default `"3"`), `jira_allow_writes` (boolean, default `false`, with `Warning` text explaining write operations will be enabled), `jira_verify_ssl` (advanced, default `true`), `jira_timeout` (advanced, default `30`, min 5, max 300)
- [x] List all 13 `Functions` entries (9 read + 4 write); write entries note "requires jira_allow_writes=true" in their `Description`
- [x] Register `"jira": getJiraSchema()` in `GetToolSchemas()`
- [x] Write schema tests (validation of required fields, enum values, write entries present)
- [x] Run `make test-mcp` — must pass before Task 2

### Task 2: Core Jira tool implementation

Files:
- Create: `mcp-gateway/internal/tools/jira/jira.go`

- [ ] Define `JiraConfig` struct (URL, AuthType, APIVersion, Username, APIToken, AllowWrites, VerifySSL, Timeout, UseProxy, ProxyURL)
- [ ] Define `JiraTool` struct (logger, configCache 5min, responseCache 30sec default, rateLimiter)
- [ ] Implement `NewJiraTool(logger, limiter)`, `Stop()`
- [ ] Implement `configCacheKey`, `responseCacheKey`, `clampTimeout`, `clampLimit` (max 100, Jira's `maxResults` cap), `extractLogicalName`
- [ ] Implement `getConfig()` — credential resolution via `database.ResolveToolCredentials(...)`, including logical-name routing; honor `JiraEnabled` proxy flag
- [ ] Implement `getCachedProxySettings()`
- [ ] Implement `doRequest(ctx, method, path, params, body)` — applies rate limit, TLS verify, proxy, request timeout, and the auth header derived from `AuthType`; enforces 5 MB response cap
- [ ] Implement `cachedGet(ctx, path, params, ttl)` wrapper around `doRequest`
- [ ] Implement helper `apiPath(version, suffix)` so callers don't string-concat the version
- [ ] Implement write-gate helper `requireWrites(config)` returning an error when `AllowWrites=false`
- [ ] Write constructor + config + auth-header tests (cover all three auth types) and a write-gate test
- [ ] Run `make test-mcp` — must pass before Task 3

### Task 3: Read tool methods

Files:
- Modify: `mcp-gateway/internal/tools/jira/jira.go`

- [ ] `SearchIssues()` — `jql` (required), `fields`, `expand`, `start_at`, `max_results`; clamp `max_results` to 100; cached 15s
- [ ] `GetIssue()` — `key` (required), optional `expand`, `fields`; cached 30s
- [ ] `GetIssueComments()` — `key`, paging; cached 30s
- [ ] `GetIssueTransitions()` — `key`; cached 30s
- [ ] `GetIssueChangelog()` — `key`, paging; cached 60s
- [ ] `GetProjects()` — `query`, paging; cached 120s
- [ ] `GetProject()` — `key`; cached 120s
- [ ] `SearchUsers()` — `query`, paging; cached 60s
- [ ] `APIRequest()` — `path` (required, must start with `/rest/`), optional `params`; cached 30s; rejects non-GET via method param fixed to GET
- [ ] Tests for each method with `httptest.NewServer` mocking the Jira API (success + error cases, params + auth header assertions)
- [ ] Run `make test-mcp` — must pass before Task 4

### Task 4: Write tool methods (gated)

Files:
- Modify: `mcp-gateway/internal/tools/jira/jira.go`

- [ ] `AddComment()` — `key` (required), `body` (required); `requireWrites` check first; not cached
- [ ] `TransitionIssue()` — `key` (required), `transition_id` (required), optional `comment`, optional `fields`; `requireWrites`; not cached
- [ ] `CreateIssue()` — `project_key` (required), `issue_type` (required), `summary` (required), optional `description`, `assignee`, `priority`, `labels`, raw `fields` object passthrough; `requireWrites`; not cached
- [ ] `UpdateIssue()` — `key` (required), `fields` (required object); `requireWrites`; not cached
- [ ] Tests: each method validates required args, write-gate error when `AllowWrites=false`, success case posts correct JSON body + auth header
- [ ] Run `make test-mcp` — must pass before Task 5

### Task 5: Registry integration

Files:
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Add constants `JiraRatePerSecond = 10`, `JiraBurstCapacity = 20`
- [ ] Add `jiraTool *jira.JiraTool` and `jiraLimit *ratelimit.Limiter` fields to `Registry`
- [ ] In `RegisterAllTools()`, construct `r.jiraLimit` and call `r.registerJiraTools()`
- [ ] In registry `Stop()`, call `r.jiraTool.Stop()`
- [ ] Implement `registerJiraTools()` registering all 13 MCP tools with full `InputSchema` definitions (mirroring the schemas.go function list, including `Required` arrays)
- [ ] Tests in `registry_test.go`: verify all 13 jira.* tools registered, write-tool descriptions mention `jira_allow_writes`
- [ ] Run `make test-mcp` — must pass before Task 6

### Task 6: Verify acceptance criteria

- [ ] Run `make test-mcp`
- [ ] Run `cd mcp-gateway && go vet ./...`
- [ ] Verify `mcp-gateway/internal/tools/jira/` test coverage ≥ 80%
- [ ] Run `make verify` from repo root
- [ ] Confirm Jira schema appears in `GetToolSchemas()` output (no frontend work needed — settings UI is schema-driven)

### Task 7: Update documentation

- [ ] Update CLAUDE.md: add `jira` to the tools list in `mcp-gateway/internal/tools/` directory map
- [ ] Update CLAUDE.md: add a "Jira Patterns" subsection in the "External API Integration" block describing auth modes, the `jira_allow_writes` default-off behavior, supported deployments (Cloud + Server/DC), and the API-version selector
- [ ] Update CLAUDE.md: add `jira` to the implementation reference list under `mcp-gateway/internal/tools/jira/`
- [ ] Move this plan to `docs/plans/completed/`
