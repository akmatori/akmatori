# Universal Integration Gateway Implementation Plan

## Overview

Replace the Python wrapper pattern with a TypeScript-native gateway client in the agent-worker, add tool discovery and programmatic scripting capabilities, implement skill-based authorization, declarative HTTP connectors, and MCP proxy support. This transforms the MCP Gateway from a simple tool dispatcher into a universal integration gateway.

## Context

- Files involved: `mcp-gateway/` (Go), `agent-worker/` (TypeScript), `internal/services/` (Go), `internal/database/` (Go), `internal/handlers/` (Go)
- Related patterns: Existing tool registry in `mcp-gateway/internal/tools/registry.go`, Python wrappers in `agent-worker/tools/`, SKILL.md generation in `internal/services/skill_prompt_service.go`
- Dependencies: `@mariozechner/pi-coding-agent` extension API, Node.js `vm` module

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Follow existing patterns: table-driven tests, test helpers/builders, `make verify`

---

## Phase 1: Core Gateway + TypeScript Client

### Task 1.1: Add logical name resolution to MCP Gateway

Add a `logical_name` field to `ToolInstance` model so agents can reference instances by name instead of numeric ID.

**Files:**
- Modify: `internal/database/models_skills.go` — add `LogicalName` field to `ToolInstance`
- Modify: `mcp-gateway/internal/database/db.go` — add `ResolveByLogicalName()` function, update `ResolveToolCredentials()` to accept logical name
- Modify: `internal/handlers/api_tools.go` — expose logical_name in API responses
- Modify: `internal/services/tool_service.go` — handle logical_name in CRUD

- [x] Add `LogicalName string` field to `ToolInstance` model with unique index
- [x] Add database migration for `logical_name` column (nullable, default to slugified Name)
- [x] Update `ResolveToolCredentials()` to accept `logicalName string` parameter alongside `instanceID`
- [x] Add `GetToolCredentialsByLogicalName(ctx, logicalName, toolType)` in mcp-gateway database package
- [x] Resolution priority: explicit instance ID > logical name > first enabled instance of type
- [x] Update ToolService CRUD to validate and persist logical_name
- [x] Write tests for logical name resolution (happy path, not found, type mismatch, fallback)
- [x] Run `make test-mcp && make test` — must pass before task 1.2

### Task 1.2: Add tool discovery endpoints to MCP Gateway

Add `/tools/search` and `/tools/detail` endpoints for tiered tool discovery.

**Files:**
- Modify: `mcp-gateway/internal/mcp/server.go` — add `handleSearchTools()` and `handleGetToolDetail()` methods
- Modify: `mcp-gateway/internal/mcp/protocol.go` — add request/response types for search and detail
- Modify: `mcp-gateway/internal/tools/registry.go` — add `SearchTools(query)` and `GetToolDetail(name)` methods
- Modify: `mcp-gateway/internal/tools/schemas.go` — ensure all tool schemas have descriptions and param schemas

- [x] Define `SearchToolsParams` (query string, optional tool_type filter) and `SearchToolsResult` (compact tool list with name, description, instances)
- [x] Define `GetToolDetailParams` (tool_name string) and `GetToolDetailResult` (full schema with params, instances, description)
- [x] Implement `handleSearchTools()` — fuzzy match on tool name and description, return compact results
- [x] Implement `handleGetToolDetail()` — return full JSON schema for a specific tool
- [x] Register new JSON-RPC methods: `tools/search` and `tools/detail` in request dispatch
- [x] Populate instance logical names in search/detail responses by querying enabled instances per tool type
- [x] Write tests for search (query matching, empty results, type filter) and detail (found, not found)
- [x] Run `make test-mcp` — must pass before task 1.3

### Task 1.3: Create TypeScript gateway client library

Build a reusable client library for the agent-worker to communicate with the MCP Gateway.

**Files:**
- Create: `agent-worker/src/gateway-client.ts` — HTTP client for MCP Gateway with output management
- Create: `agent-worker/src/gateway-client.test.ts` — unit tests

- [x] Implement `GatewayClient` class with constructor accepting `gatewayUrl` and `incidentId`
- [x] Implement `call(toolName, args, instanceHint?)` — JSON-RPC 2.0 POST to `/mcp` with `X-Incident-ID` header
- [x] Implement output management: if response < 4KB return inline, if >= 4KB write to `{workDir}/tool_outputs/{tool}_{timestamp}.json` and return truncated preview + file path
- [x] Implement `searchTools(query, toolType?)` — calls `tools/search` JSON-RPC method
- [x] Implement `getToolDetail(toolName)` — calls `tools/detail` JSON-RPC method
- [x] Handle JSON-RPC errors: parse error codes, throw typed `GatewayError` with code/message/data
- [x] Write unit tests with mocked HTTP (test call, search, detail, output truncation, error handling)
- [x] Run `make test-agent` — must pass before task 1.4

### Task 1.4: Register gateway_call tool in pi-mono extension

Register `gateway_call` as a custom tool in the pi-mono coding agent, replacing Python wrapper invocations.

**Files:**
- Create: `agent-worker/src/gateway-tools.ts` — tool definitions and handlers for pi-mono extension
- Modify: `agent-worker/src/agent-runner.ts` — register gateway tools via extension API, inject GatewayClient

- [x] Define `gateway_call` tool schema: params `tool_name` (string, required), `args` (object, required), `instance` (string, optional logical name)
- [x] Implement `gateway_call` handler: instantiate GatewayClient, call `client.call()`, return result (with output management)
- [x] Register tool via `ExtensionAPI.registerTool()` in agent-runner session setup
- [x] Pass `GatewayClient` instance to tool handler (created per-session with incidentId and workDir)
- [x] Update BASH_TOOL_GUIDELINES to mention `gateway_call` as the preferred tool invocation method
- [x] Write tests for tool registration and handler logic
- [x] Run `make test-agent` — must pass before task 1.5

### Task 1.5: Register search_tools and get_tool_detail tools

Register discovery tools in pi-mono extension.

**Files:**
- Modify: `agent-worker/src/gateway-tools.ts` — add search_tools and get_tool_detail definitions

- [x] Define `search_tools` tool schema: params `query` (string, required), `tool_type` (string, optional)
- [x] Define `get_tool_detail` tool schema: params `tool_name` (string, required)
- [x] Implement handlers using GatewayClient's `searchTools()` and `getToolDetail()` methods
- [x] Register both tools in agent-runner alongside gateway_call
- [x] Write tests for discovery tool handlers
- [x] Run `make test-agent` — must pass before task 1.6

### Task 1.6: Implement execute_script tool with isolated runtime

Register `execute_script` tool that runs agent-written scripts in an isolated runtime with built-in `gateway_call()`.

**Files:**
- Create: `agent-worker/src/script-executor.ts` — isolated script execution engine
- Modify: `agent-worker/src/gateway-tools.ts` — add execute_script tool definition
- Create: `agent-worker/src/script-executor.test.ts` — tests

- [ ] Implement `ScriptExecutor` class using Node.js `vm` module (or `vm2`/`isolated-vm` if security requires)
- [ ] Create execution context with injected globals: `gateway_call(toolName, args, instance?)` as async function, `search_tools()`, `get_tool_detail()`
- [ ] Inject `console.log()` that captures output for return value
- [ ] Support both `return` value and stdout capture as script output
- [ ] Enforce 5-minute timeout via `vm.Script` timeout option
- [ ] Provide `fs` access scoped to incident workspace directory only
- [ ] Define `execute_script` tool schema: params `code` (string, required)
- [ ] Register in agent-runner alongside other gateway tools
- [ ] Write tests: basic script execution, gateway_call within script, timeout enforcement, return value capture, error handling
- [ ] Run `make test-agent` — must pass before task 1.7

### Task 1.7: Update SKILL.md generation for new tool system

Update the skill prompt service to generate tool usage instructions for the new TypeScript-native tools instead of Python wrappers.

**Files:**
- Modify: `internal/services/skill_prompt_service.go` — replace Python examples with gateway_call examples using logical names

- [ ] Update `generateToolUsageExample()` to show `gateway_call` usage instead of Python import patterns
- [ ] Include logical name in examples: `gateway_call("ssh.execute_command", {command: "uptime"}, "prod-ssh")`
- [ ] Show `search_tools` and `get_tool_detail` usage in skill prompt
- [ ] Show `execute_script` usage example for batch operations
- [ ] Update tool assignment section to show logical names alongside IDs
- [ ] Write tests for updated SKILL.md generation
- [ ] Run `make test` — must pass before task 1.8

### Task 1.8: Remove Python wrappers

Remove the Python wrapper pattern entirely — the TypeScript gateway tools replace it.

**Files:**
- Delete: `agent-worker/tools/mcp_client.py`
- Delete: `agent-worker/tools/ssh/__init__.py`
- Delete: `agent-worker/tools/zabbix/__init__.py`
- Delete: `agent-worker/tools/victoriametrics/__init__.py`
- Modify: `agent-worker/Dockerfile` — remove Python installation and PYTHONPATH
- Modify: `agent-worker/src/agent-runner.ts` — remove PYTHONPATH and Python-related env vars from spawnHook

- [ ] Remove all Python wrapper files in `agent-worker/tools/`
- [ ] Remove Python3 installation from agent-worker Dockerfile
- [ ] Remove `PYTHONPATH` injection from spawnHook in agent-runner.ts
- [ ] Update BASH_TOOL_GUIDELINES to remove Python import references
- [ ] Verify no remaining references to Python wrappers in codebase
- [ ] Run `make test-agent && make verify` — must pass before Phase 2

---

## Phase 2: Authorization

### Task 2.1: Build instance allowlist at incident creation

When an incident is spawned, resolve the skill's tool instances into an allowlist and pass it to the gateway.

**Files:**
- Modify: `internal/services/incident_service.go` — build allowlist from skill's assigned tools
- Modify: `internal/handlers/agent_ws.go` — pass allowlist in WebSocket message to agent worker
- Modify: `agent-worker/src/types.ts` — add allowlist field to NewIncidentMessage type

- [ ] In `SpawnIncidentManager()`, resolve skill → tool instances → build allowlist: `[{instance_id, logical_name, tool_type}]`
- [ ] Add `ToolAllowlist` field to WebSocket `new_incident` message payload
- [ ] Update agent-worker types to parse allowlist from message
- [ ] Pass allowlist from orchestrator to agent-runner
- [ ] GatewayClient sends allowlist to gateway as `X-Tool-Allowlist` header or request body field
- [ ] Write tests for allowlist construction (skill with tools, skill with no tools, multiple skills)
- [ ] Run `make test && make test-agent` — must pass before task 2.2

### Task 2.2: Enforce authorization in MCP Gateway

Gateway checks every tool call against the incident's allowlist.

**Files:**
- Modify: `mcp-gateway/internal/mcp/server.go` — add allowlist enforcement middleware
- Create: `mcp-gateway/internal/auth/authorizer.go` — authorization logic
- Create: `mcp-gateway/internal/auth/authorizer_test.go` — tests

- [ ] Create `Authorizer` struct that stores per-incident allowlists (thread-safe map)
- [ ] Add `SetAllowlist(incidentID, allowlist)` endpoint or header-based registration
- [ ] In `handleCallTool()`, check resolved instance ID against incident's allowlist before executing
- [ ] Return JSON-RPC error code -32600 (unauthorized) if instance not in allowlist
- [ ] Add allowlist caching with TTL (match incident lifetime)
- [ ] Write tests: authorized call passes, unauthorized call rejected, no allowlist = allow all (backward compat), expired allowlist
- [ ] Run `make test-mcp` — must pass before task 2.3

### Task 2.3: Filter discovery by allowlist

`search_tools` and `get_tool_detail` only return tools/instances the incident is authorized to use.

**Files:**
- Modify: `mcp-gateway/internal/mcp/server.go` — pass allowlist to search/detail handlers
- Modify: `mcp-gateway/internal/tools/registry.go` — filter results by allowlist

- [ ] `handleSearchTools()` filters results to only include tools with at least one authorized instance
- [ ] `handleGetToolDetail()` filters instance list to only authorized instances
- [ ] If no authorized instances for a tool, tool is excluded from search results entirely
- [ ] Write tests for filtered discovery (partial authorization, full authorization, no authorization)
- [ ] Run `make test-mcp && make verify` — must pass before Phase 3

---

## Phase 3: Declarative HTTP Connectors

### Task 3.1: Define HTTP connector model and storage

Create database model for declarative HTTP connector definitions.

**Files:**
- Modify: `internal/database/models_skills.go` — add `HTTPConnector` model
- Create: `internal/database/migrations/add_http_connectors.go` — migration

- [ ] Define `HTTPConnector` model: `ID`, `ToolTypeName` (string, unique), `AuthConfig` (JSONB: method, token_field, header_name), `BaseURLField` (string), `Tools` (JSONB: array of tool definitions with name, method, path, params)
- [ ] Each tool definition: `name`, `http_method` (GET/POST/PUT/DELETE), `path` (with `{{param}}` templates), `params` (array with name, type, required, in: path/query/body/header, default), `read_only` (bool, default true)
- [ ] Add migration to create `http_connectors` table
- [ ] Add GORM AutoMigrate for HTTPConnector
- [ ] Write tests for model validation (valid connector, missing fields, duplicate tool names)
- [ ] Run `make test` — must pass before task 3.2

### Task 3.2: Build generic HTTP executor in MCP Gateway

Implement the engine that executes declarative HTTP connector tool calls.

**Files:**
- Create: `mcp-gateway/internal/tools/httpconnector/executor.go` — generic HTTP executor
- Create: `mcp-gateway/internal/tools/httpconnector/executor_test.go` — tests

- [ ] Implement `HTTPConnectorExecutor` struct with `Execute(ctx, connectorDef, toolName, args, credentials)` method
- [ ] Path template resolution: replace `{{param}}` with actual values from args
- [ ] Query parameter injection for `in: "query"` params
- [ ] Request body construction for `in: "body"` params (POST/PUT)
- [ ] Header injection for `in: "header"` params
- [ ] Auth injection based on connector's auth config: bearer_token, basic_auth, api_key
- [ ] Apply rate limiting (shared per connector instance) and response caching
- [ ] Read-only enforcement: reject non-GET requests unless tool has `read_only: false`
- [ ] Write tests: GET with path params, GET with query params, POST with body, auth injection (bearer, basic, api_key), read-only enforcement, error handling
- [ ] Run `make test-mcp` — must pass before task 3.3

### Task 3.3: Dynamic tool registration for HTTP connectors

Register HTTP connector tools in the gateway registry dynamically on startup and on connector CRUD.

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go` — add `RegisterHTTPConnectors()` and `ReloadHTTPConnectors()`
- Modify: `mcp-gateway/cmd/gateway/main.go` — call registration on startup

- [ ] On startup, query all HTTPConnector definitions from database
- [ ] For each connector, register tools with names `{connector.ToolTypeName}.{tool.Name}` (e.g., `internal-billing.get_invoice`)
- [ ] Tool handlers delegate to HTTPConnectorExecutor with connector definition and resolved credentials
- [ ] Add `ReloadHTTPConnectors()` to re-register after CRUD (called via API or signal)
- [ ] Ensure discovery endpoints include HTTP connector tools in search/detail results
- [ ] Write tests for dynamic registration and execution flow
- [ ] Run `make test-mcp` — must pass before task 3.4

### Task 3.4: Admin API and UI for HTTP connectors

CRUD API for managing declarative HTTP connectors.

**Files:**
- Create: `internal/handlers/api_http_connectors.go` — REST handlers
- Modify: `internal/services/tool_service.go` — add connector CRUD methods
- Modify: `internal/services/interfaces.go` — add ConnectorManager interface

- [ ] `POST /api/http-connectors` — create connector (validate schema, register tools)
- [ ] `GET /api/http-connectors` — list all connectors
- [ ] `GET /api/http-connectors/{id}` — get connector detail
- [ ] `PUT /api/http-connectors/{id}` — update connector (re-register tools)
- [ ] `DELETE /api/http-connectors/{id}` — delete connector (unregister tools)
- [ ] Add admin-only middleware to connector endpoints
- [ ] Trigger gateway tool reload on create/update/delete
- [ ] Write handler tests with mock service
- [ ] Run `make test && make verify` — must pass before Phase 4

---

## Phase 4: MCP Proxy

### Task 4.1: Implement MCP connection pool

Build a lazy connection pool for external MCP servers.

**Files:**
- Create: `mcp-gateway/internal/mcpproxy/pool.go` — connection pool manager
- Create: `mcp-gateway/internal/mcpproxy/pool_test.go` — tests

- [ ] Define `MCPConnectionPool` struct: map of `instanceID → *MCPConnection` (thread-safe)
- [ ] `GetOrConnect(ctx, instanceID, config)` — return existing connection or establish new one
- [ ] Support SSE transport: HTTP client connecting to external MCP server's SSE endpoint
- [ ] Support stdio transport: spawn subprocess, communicate via stdin/stdout
- [ ] Idle timeout: 5 minutes, background goroutine closes idle connections
- [ ] On connect, fetch `tools/list` from external server, cache tool schemas
- [ ] Connection health check with automatic reconnect on failure
- [ ] `Close(instanceID)` for manual connection teardown
- [ ] Write tests: lazy connection, reuse existing, idle cleanup, reconnect on failure
- [ ] Run `make test-mcp` — must pass before task 4.2

### Task 4.2: Define MCP server registration model

Database model and API for registering external MCP servers.

**Files:**
- Modify: `internal/database/models_skills.go` — add `MCPServerConfig` model or extend ToolType
- Modify: `internal/services/tool_service.go` — MCP server registration CRUD

- [ ] Define MCP server config: `transport` (sse/stdio), `url` or `command` + `args`, `namespace_prefix` (e.g., "ext.github"), `auth_config` (JSONB), `env_vars` (JSONB for stdio)
- [ ] Store as a ToolType with `backend: "mcp_proxy"` discriminator, or as separate MCPServerConfig model
- [ ] ToolInstance of MCP type stores connection-specific settings (URL, API keys)
- [ ] CRUD endpoints at `/api/mcp-servers` (admin-only)
- [ ] Write tests for model and CRUD
- [ ] Run `make test` — must pass before task 4.3

### Task 4.3: Implement MCP proxy handler in gateway

Route tool calls to external MCP servers through the connection pool.

**Files:**
- Create: `mcp-gateway/internal/mcpproxy/handler.go` — proxy handler
- Modify: `mcp-gateway/internal/tools/registry.go` — register MCP proxy tools dynamically

- [ ] On startup (and on config reload), fetch MCP server registrations from database
- [ ] For each registered server, connect lazily and discover tools via `tools/list`
- [ ] Register discovered tools with namespace prefix: `{prefix}.{tool_name}` (e.g., `ext.github.create_issue`)
- [ ] Proxy handler forwards `tools/call` to external server, injects credentials from ToolInstance settings
- [ ] Apply rate limiting per external server instance
- [ ] Apply response caching with configurable TTL per server
- [ ] Gateway injects auth into connection config — external servers never see raw agent credentials
- [ ] Include MCP proxy tools in `search_tools` and `get_tool_detail` discovery responses
- [ ] Write tests with mock external MCP server: tool discovery, call proxying, namespace prefixing, auth injection
- [ ] Run `make test-mcp` — must pass before task 4.4

### Task 4.4: Handle MCP proxy lifecycle and errors

Robust error handling and lifecycle management for external MCP connections.

**Files:**
- Modify: `mcp-gateway/internal/mcpproxy/pool.go` — add error handling and lifecycle hooks
- Modify: `mcp-gateway/internal/mcpproxy/handler.go` — add retry and fallback logic

- [ ] Handle connection failures gracefully: return clear error to agent, don't crash gateway
- [ ] Auto-reconnect on transient failures (network errors, server restarts) with exponential backoff
- [ ] Tool schema refresh: re-fetch `tools/list` periodically (every 5 min) to detect new tools
- [ ] Graceful shutdown: close all connections on gateway stop
- [ ] Health check endpoint: report status of all MCP connections
- [ ] Write tests for error scenarios: connection timeout, server crash, schema refresh
- [ ] Run `make test-mcp && make verify` — must pass before final verification

---

## Final Verification

### Task F.1: End-to-end integration testing

- [ ] Manual test: create a skill with SSH tool, start incident, verify `gateway_call("ssh.execute_command", ...)` works
- [ ] Manual test: use `search_tools("ssh")` to discover available tools
- [ ] Manual test: use `execute_script` to batch-query multiple servers
- [ ] Manual test: verify unauthorized tool instances are rejected
- [ ] Manual test: create an HTTP connector and invoke its tools via gateway_call
- [ ] Run full test suite: `make verify`
- [ ] Run linter: `golangci-lint run`
- [ ] Verify test coverage meets 80%+ for new packages

### Task F.2: Update documentation

- [ ] Update CLAUDE.md with new tool architecture (gateway-tools, execute_script, HTTP connectors, MCP proxy)
- [ ] Update CLAUDE.md tool wrapper table to reflect TypeScript-native tools
- [ ] Update CLAUDE.md "Key Directories" to include new packages
- [ ] Move this plan to `docs/plans/completed/`
