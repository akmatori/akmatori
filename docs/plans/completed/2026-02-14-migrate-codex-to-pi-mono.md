# Migrate from Codex CLI to pi-mono coding-agent

## Overview

Replace the Go-based codex-worker (which spawns the OpenAI Codex CLI as a subprocess) with a Node.js/TypeScript worker that uses the `@mariozechner/pi-coding-agent` SDK directly in-process. This enables multi-provider LLM support (Anthropic, OpenAI, Google, OpenRouter, etc.), richer tool integration via pi-mono's `ToolDefinition` interface, and native session management. No backward compatibility is maintained - this is a clean cut-over.

## Context

**Current architecture (being removed):**
- `akmatori-codex` container runs a Go binary that connects to API via WebSocket
- Go worker spawns `codex exec` CLI as a subprocess, parses JSON events from stdout
- Tools are Python wrappers (`codex-tools/`) calling MCP Gateway over HTTP
- Only supports OpenAI models (API key or ChatGPT subscription OAuth)
- Skills use `.codex/AGENTS.md` + `.codex/skills/SKILL.md` format

**Target architecture:**
- `akmatori-agent` container runs a Node.js process using `@mariozechner/pi-coding-agent` SDK
- Node worker connects to API via the same WebSocket endpoint (`/ws/codex`)
- Tools are registered as pi-mono `ToolDefinition` objects (calling MCP Gateway over HTTP)
- Supports all LLM providers: Anthropic, OpenAI, Google, OpenRouter, custom
- Skills use `AGENTS.md` at workspace root + pi-mono's built-in tool system

**Files involved (modify):**
- `docker-compose.yml` - Replace `akmatori-codex` service with `akmatori-agent`
- `internal/database/models.go` - Replace `OpenAISettings` with `LLMSettings` (multi-provider)
- `internal/database/db.go` - Drop old table, create new one
- `internal/handlers/codex_ws.go` - Rename to `agent_ws.go`, update message types for multi-provider
- `internal/services/skill_service.go` - Update workspace setup for pi-mono format
- `web/` - Frontend settings page for multi-provider LLM config
- `.env.example` - New environment variables
- `Makefile` - Add agent-worker test targets

**Files involved (create):**
- `agent-worker/` - New Node.js/TypeScript worker module
- `agent-worker/package.json`
- `agent-worker/tsconfig.json`
- `agent-worker/src/index.ts` - Entry point
- `agent-worker/src/ws-client.ts` - WebSocket client
- `agent-worker/src/orchestrator.ts` - Message routing
- `agent-worker/src/agent-runner.ts` - pi-mono SDK integration
- `agent-worker/src/tools/mcp-tools.ts` - MCP Gateway tool definitions
- `agent-worker/src/types.ts` - Shared types
- `agent-worker/Dockerfile`
- `agent-worker/tests/`

**Files involved (delete):**
- `codex-worker/` - Entire Go worker module
- `codex-tools/` - Python MCP wrappers
- `Dockerfile.codex` - Old container build
- `entrypoint.sh` - Old container entrypoint

**Related patterns (from openclaw reference at /opt/hybrid/openclaw):**
- SDK mode: `createAgentSession()` with `customTools`, `authStorage`, `modelRegistry`
- Tool adapter: `toToolDefinitions()` converts tools to pi-mono `ToolDefinition` format
- Auth: `AuthStorage` + `ModelRegistry` for multi-provider key management
- Sessions: `SessionManager.inMemory()` for stateless or `.create(cwd)` for persistent
- Events: `session.agent.on("message_update", handler)` for streaming
- System prompt: `agentDir` parameter with `AGENTS.md` file
- Tool params: `Type.Object()` from `@sinclair/typebox` for schema definitions

**Dependencies:**
- `@mariozechner/pi-coding-agent` ^0.52.x
- `@mariozechner/pi-ai` ^0.52.x
- `@mariozechner/pi-agent-core` ^0.52.x
- `@sinclair/typebox` (for tool parameter schemas)
- `ws` (WebSocket client library)
- Node.js 22+

## Development Approach

- **Testing approach**: Regular (code first, then tests) for the Node.js worker; TDD for database migrations
- Complete each task fully before moving to the next
- The Go API server remains unchanged except for the WebSocket handler and database models
- **No backward compatibility** - old codex-worker, codex-tools, and OpenAI-only settings are deleted
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Create agent-worker Node.js project scaffold

**Files:**
- Create: `agent-worker/package.json`
- Create: `agent-worker/tsconfig.json`
- Create: `agent-worker/src/types.ts`
- Create: `agent-worker/.gitignore`

**Steps:**
- [x] Initialize `agent-worker/` as a Node.js project with TypeScript
- [x] Add dependencies: `@mariozechner/pi-coding-agent`, `@mariozechner/pi-ai`, `@mariozechner/pi-agent-core`, `@sinclair/typebox`, `ws`, `@types/ws`
- [x] Add dev dependencies: `vitest`, `typescript`, `@types/node`
- [x] Configure tsconfig for ESM output (Node 22+, `moduleResolution: "bundler"`, `target: "ES2022"`)
- [x] Define shared types in `src/types.ts` mirroring the Go WebSocket message protocol:
  - `WebSocketMessage` interface matching `CodexMessage` from Go
  - `MessageType` string union matching Go constants (new_incident, codex_output, etc.)
  - `LLMSettings` type (provider, api key, model, reasoning effort)
  - `ProxyConfig` type
  - `ExecuteResult` type for runner output
- [x] Add build scripts: `build`, `dev`, `test`
- [x] Write unit tests for type definitions (serialization round-trip matches Go JSON format)
- [x] Run `npm test` - must pass before task 2

### Task 2: Implement WebSocket client

**Files:**
- Create: `agent-worker/src/ws-client.ts`
- Create: `agent-worker/tests/ws-client.test.ts`

**Steps:**
- [x] Port `codex-worker/internal/ws/client.go` logic to TypeScript using the `ws` library
- [x] Implement `WebSocketClient` class with:
  - `connect(url: string)` - establish connection with reconnection logic
  - `send(msg: WebSocketMessage)` - send JSON message
  - `sendOutput(incidentId, output)` - stream progress to API
  - `sendCompleted(incidentId, sessionId, response, tokensUsed, executionTimeMs)` - completion
  - `sendError(incidentId, errorMsg)` - error notification
  - `sendHeartbeat()` - heartbeat (30s interval)
  - `onMessage(handler)` - register message handler
  - `close()` - graceful shutdown
  - Exponential backoff reconnection (matching Go behavior)
- [x] Match the exact JSON message format from Go (`CodexMessage` struct) so the API handler works without changes initially
- [x] Write tests using a mock WebSocket server:
  - Connection and reconnection
  - Message serialization matches Go format
  - Heartbeat sending
  - Error handling on connection loss
- [x] Run `npm test` - must pass before task 3

### Task 3: Implement MCP Gateway tool definitions

**Files:**
- Create: `agent-worker/src/tools/mcp-client.ts`
- Create: `agent-worker/src/tools/mcp-tools.ts`
- Create: `agent-worker/tests/tools.test.ts`

**Steps:**
- [x] Create `mcp-client.ts` - HTTP client for the MCP Gateway (port of Python `mcp_client.py`):
  - `callTool(gatewayUrl, incidentId, toolName, arguments)` - JSON-RPC 2.0 call to `/mcp`
  - Sets `X-Incident-ID` header for credential resolution
  - Bypasses proxy for internal MCP calls (NO_PROXY)
  - Timeout and error handling
- [x] Create `mcp-tools.ts` - factory that returns pi-mono `ToolDefinition[]`:
  - SSH tool: `name: "ssh_execute_command"`, params: `{command: string, servers?: string[]}`
  - SSH connectivity: `name: "ssh_test_connectivity"`, no params
  - SSH server info: `name: "ssh_get_server_info"`, params: `{servers?: string[]}`
  - Zabbix get_hosts: `name: "zabbix_get_hosts"`, params: `{search?: object, filter?: object, limit?: number}`
  - Zabbix get_problems: `name: "zabbix_get_problems"`, params: `{recent?: boolean, severity_min?: number}`
  - Zabbix get_history: `name: "zabbix_get_history"`, params: `{itemids: number[], history_type?: number, limit?: number}`
  - Zabbix get_items_batch: `name: "zabbix_get_items_batch"`, params: `{searches: object[], hostids?: string[]}`
  - Zabbix acknowledge_event: `name: "zabbix_acknowledge_event"`, params: `{eventids: string[], message: string}`
  - Each tool's `execute` calls `mcpClient.callTool()` and returns `{content: [{type: "text", text: result}]}`
  - Use `Type.Object()` from `@sinclair/typebox` for parameter schemas
- [x] Create `createMCPTools(gatewayUrl, incidentId)` factory function
- [x] Write tests with mocked HTTP responses:
  - SSH tool execute with successful command
  - SSH tool execute with connection error
  - Zabbix tool get_hosts with mock response
  - Tool parameter validation
  - MCP client error handling (timeout, network error)
- [x] Run `npm test` - must pass before task 4

### Task 4: Implement agent runner (pi-mono SDK integration)

**Files:**
- Create: `agent-worker/src/agent-runner.ts`
- Create: `agent-worker/tests/agent-runner.test.ts`

**Steps:**
- [x] Create `AgentRunner` class wrapping pi-mono SDK:
  - `execute(params)` where params = `{incidentId, task, llmSettings, proxyConfig, workDir, onOutput, onEvent}`:
    - Create `AuthStorage` and set runtime API key via `authStorage.setRuntimeApiKey(provider, key)`
    - Create `ModelRegistry` from auth storage
    - Resolve model from settings (map provider + model name to pi-mono model object)
    - Map reasoning effort: `low/medium/high/extra_high` -> pi-mono `ThinkingLevel` (`off/minimal/low/medium/high/xhigh`)
    - Build custom tools via `createMCPTools(gatewayUrl, incidentId)`
    - Get built-in coding tools via `codingTools` from pi-coding-agent (read, write, edit, bash)
    - Create `DefaultResourceLoader` or pass `agentDir` pointing to workspace `AGENTS.md`
    - Call `createAgentSession()` with all params
    - Subscribe to session events (see event mapping below)
    - Call `session.prompt(task)` and await completion
    - Collect result: response text, token usage, execution time
    - Return `ExecuteResult`
  - `resume(params)` where params = `{incidentId, sessionId, message, llmSettings, ...}`:
    - Open existing session via `SessionManager.open(sessionFile)`
    - Call `session.prompt(message)` for follow-up
  - `cancel(incidentId)`:
    - Call `session.abort()` on active session
  - `dispose()`:
    - Clean up all active sessions
- [x] Event mapping (pi-mono events -> WebSocket output):
  - `message_update` (text_delta) -> accumulate response text, stream to `onOutput`
  - `tool_execution_start` -> format as `[Tool: toolName]` in output
  - `tool_execution_update` -> stream tool output
  - `tool_execution_end` -> format tool result summary
  - `turn_end` -> extract token usage from event data
  - `agent_end` -> mark completion
- [x] Handle proxy configuration:
  - Set `HTTP_PROXY`/`HTTPS_PROXY` env vars before creating session if proxy enabled for LLM calls
  - Respect per-service proxy toggles from `ProxyConfig`
- [x] Write tests (with mocked pi-mono SDK):
  - Execute with API key for different providers
  - Resume existing session
  - Cancel active execution
  - Event streaming to output callback
  - Error handling (auth failure, model not found, timeout)
  - Proxy configuration application
- [x] Run `npm test` - must pass before task 5

### Task 5: Implement orchestrator and entry point

**Files:**
- Create: `agent-worker/src/orchestrator.ts`
- Create: `agent-worker/src/index.ts`
- Create: `agent-worker/tests/orchestrator.test.ts`

**Steps:**
- [x] Create `Orchestrator` class (port of Go `orchestrator.go`):
  - Constructor takes `config: {apiWsUrl, mcpGatewayUrl, workspaceDir}`
  - `start()` - connect WebSocket, register handler, start heartbeat, send "ready" status
  - `stop()` - cancel active runs, close WebSocket
  - `handleMessage(msg)` - route by message type:
    - `new_incident` -> extract LLM settings from message, call `agentRunner.execute()`
    - `continue_incident` -> call `agentRunner.resume()`
    - `cancel_incident` -> call `agentRunner.cancel()`
    - `proxy_config_update` -> update cached proxy config
  - Stream output back via `wsClient.sendOutput()`
  - Send completion via `wsClient.sendCompleted()`
  - Handle errors via `wsClient.sendError()`
- [x] Create `index.ts` entry point:
  - Read config from environment: `API_WS_URL`, `MCP_GATEWAY_URL`, `WORKSPACE_DIR`
  - Create and start orchestrator
  - Handle SIGTERM/SIGINT for graceful shutdown
  - Reconnection loop on WebSocket disconnect
- [x] Write tests:
  - Message routing for all message types
  - Output streaming through WebSocket
  - Completion with metrics
  - Error propagation
  - Graceful shutdown
- [x] Run `npm test` - must pass before task 6

### Task 6: Create Docker container and update docker-compose

**Files:**
- Create: `agent-worker/Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `.env.example`

**Steps:**
- [x] Create `agent-worker/Dockerfile`:
  - Base: `node:22-bookworm`
  - Install system deps: `ripgrep`, `git`, `jq` (needed by pi-mono's bash tool)
  - Create non-root user (UID 1001, matching old codex user for volume permissions)
  - Copy `agent-worker/`, `npm ci`, build TypeScript
  - Set environment defaults
  - Run as non-root user
  - Health check: `pgrep node`
- [x] Update `docker-compose.yml`:
  - Replace `akmatori-codex` service with `akmatori-agent`
  - Point to `agent-worker/Dockerfile`
  - Keep same networks (frontend, api-internal, codex-network)
  - Keep same volume mounts (sessions, workspaces, context)
  - Update environment variables
- [x] Add new env vars to `.env.example`:
  - Document that LLM provider/key are configured in web UI, not env vars
- [x] Test Docker build: `docker build -t akmatori-agent ./agent-worker`
- [x] Test container starts and connects to API WebSocket

### Task 7: Replace OpenAISettings with LLMSettings (database)

**Files:**
- Modify: `internal/database/models.go`
- Modify: `internal/database/db.go`
- Modify: `internal/database/openai.go` -> rename to `llm.go`
- Create: `internal/database/llm_test.go`

**Steps:**
- [x] Replace `OpenAISettings` model with `LLMSettings`:
  - `Provider` field: `openai`, `anthropic`, `google`, `openrouter`, `custom`
  - `APIKey` field (single field, stores key for selected provider)
  - `Model` field
  - `ThinkingLevel` field (replaces `ModelReasoningEffort`)
  - `BaseURL` field (for custom endpoints)
  - `Enabled` field
  - Drop all ChatGPT subscription/OAuth fields (no backward compat)
  - Drop `AuthMethod` field
  - Table name: `llm_settings` (new table)
- [x] Update `db.go`:
  - Drop `openai_settings` table in migration
  - AutoMigrate `LLMSettings`
  - Update `GetLLMSettings()` / `UpdateLLMSettings()` helpers
- [x] Remove all ChatGPT subscription auth code:
  - Remove `AuthMethod` type and constants
  - Remove `IsChatGPTTokenExpired()`
  - Remove `GetValidReasoningEfforts()` (pi-mono handles this per-provider)
  - Remove device auth handler code paths
- [x] Write tests:
  - Multi-provider configuration
  - Provider API key validation
  - Model/provider combinations
- [x] Run `go test ./internal/database/...` - must pass before task 8

### Task 8: Update WebSocket handler for multi-provider

**Files:**
- Rename: `internal/handlers/codex_ws.go` -> `internal/handlers/agent_ws.go`
- Modify: `internal/handlers/agent_ws.go`
- Update all references to the handler across the codebase

**Steps:**
- [x] Rename file and update all imports/references
- [x] Replace `OpenAISettings` struct in handler with `LLMSettings`:
  - `Provider` field
  - `APIKey` field
  - `Model` field
  - `ThinkingLevel` field
  - `BaseURL` field
- [x] Update `CodexMessage` -> `AgentMessage`:
  - Replace `OpenAIAPIKey` with `APIKey`
  - Replace `ReasoningEffort` with `ThinkingLevel`
  - Add `Provider` field
  - Remove all ChatGPT subscription fields
  - Remove device auth fields
- [x] Update `CodexWSHandler` -> `AgentWSHandler`:
  - Remove `persistRefreshedTokens()` (no OAuth)
  - Remove `handleDeviceAuthResponse()` (no device auth)
  - Remove `StartDeviceAuth()` / `CancelDeviceAuth()`
  - Update `StartIncident()` to include provider + API key from `LLMSettings`
- [x] Keep WebSocket endpoint as `/ws/codex` for now (rename later to avoid config churn)
- [x] Update all callers of the handler (alert handlers, settings handlers, etc.)
- [x] Update existing handler tests
- [x] Run `go test ./internal/handlers/...` - must pass before task 9

### Task 9: Update skill service for pi-mono workspace format

**Files:**
- Modify: `internal/services/skill_service.go`

**Steps:**
- [x] Update `SpawnIncidentManager()`:
  - Instead of creating `.codex/AGENTS.md`, create `AGENTS.md` at workspace root (pi-mono reads it from cwd upward)
  - Instead of copying skills to `.codex/skills/`, embed skill instructions directly in `AGENTS.md`
  - Remove the `copyDirPreserveSymlinks()` call for skills (tools are now native pi-mono `ToolDefinition` objects, not Python scripts)
  - Remove `.codex/` directory creation entirely
- [x] Update `generateIncidentAgentsMd()`:
  - Write to workspace root (not `.codex/`)
  - Remove Codex-specific structured output protocol (pi-mono handles output natively)
  - Include skill instructions inline: for each enabled skill, append its SKILL.md body content
  - Remove Python import instructions from Quick Start sections
- [x] Update `generateSkillMd()`:
  - Remove Python import code generation (no more `from scripts.ssh import ...`)
  - Tools are now pi-mono native `ToolDefinition` objects registered at session creation time
  - Keep YAML frontmatter format (useful for metadata)
  - Simplify to just frontmatter + user prompt body
- [x] Remove or simplify `AssignTools()`:
  - Remove symlink creation logic (no Python wrappers to symlink)
  - Keep database association for tracking which tools are assigned
  - Remove `mcp_client.py` symlink creation
- [x] Update tests
- [x] Run `go test ./internal/services/...` - must pass before task 10

### Task 10: Update frontend for multi-provider LLM settings

**Files:**
- Modify: `web/src/` - Settings page components

**Steps:**
- [x] Replace the OpenAI settings page with a "LLM Provider" settings page:
  - Provider selector dropdown: OpenAI, Anthropic, Google, OpenRouter, Custom
  - API key input field (single field, context changes per provider)
  - Model input/selector (free text with suggestions per provider)
  - Thinking level selector: off, minimal, low, medium, high, xhigh
  - Base URL field (visible when provider is "custom" or "openrouter")
  - Keep existing proxy configuration section
- [x] Remove ChatGPT subscription auth UI:
  - Remove device auth flow
  - Remove OAuth token display
  - Remove "ChatGPT Plus" auth method selector
- [x] Update API calls to use new LLM settings endpoints:
  - `GET/PUT /api/settings/llm` instead of `/api/settings/openai`
- [x] Update model suggestions per provider:
  - OpenAI: gpt-4o, o3, o4-mini, etc.
  - Anthropic: claude-opus-4-6, claude-sonnet-4-5, claude-haiku-4-5
  - Google: gemini-2.5-pro, gemini-2.5-flash
- [x] Test the settings page renders and saves correctly

### Task 11: Delete old codex code and cleanup

**Files:**
- Delete: `codex-worker/` (entire directory)
- Delete: `codex-tools/` (entire directory)
- Delete: `Dockerfile.codex`
- Delete: `entrypoint.sh`
- Modify: `Makefile`
- Modify: `.gitignore`
- Modify: `CLAUDE.md`

**Steps:**
- [x] Delete `codex-worker/` directory
- [x] Delete `codex-tools/` directory
- [x] Delete `Dockerfile.codex`
- [x] Delete `entrypoint.sh`
- [x] Update Makefile:
  - Add `make test-agent` - Run agent-worker tests (`cd agent-worker && npm test`)
  - Update `make test-all` to include agent-worker
  - Add `make build-agent` - Build agent-worker Docker image
  - Remove old codex-related targets
- [x] Update `.gitignore` for agent-worker artifacts (`agent-worker/node_modules/`, `agent-worker/dist/`)
- [x] Update `CLAUDE.md`:
  - New architecture description (pi-mono SDK instead of Codex CLI)
  - Updated test commands
  - Updated container mapping (akmatori-agent instead of akmatori-codex)
  - Updated directory structure

### Task 12: Integration testing and acceptance criteria

- [x] `docker-compose build && docker-compose up -d` - all containers start
- [x] Agent-worker connects to API via WebSocket and sends "ready" status
- [x] New incidents are processed: API sends task -> worker creates pi-mono session -> streams output -> sends completion
- [x] Continue incidents work: existing session is resumed with follow-up message
- [x] Cancel incidents work: active session is aborted
- [x] SSH tool works: agent can execute remote commands via MCP Gateway
- [x] Zabbix tool works: agent can query Zabbix API via MCP Gateway
- [x] Multi-provider support: can configure Anthropic, OpenAI, or Google as LLM provider
- [x] Streaming output format is displayed correctly in frontend
- [x] Session persistence works: sessions survive container restarts
- [x] Proxy configuration is respected for LLM API calls
- [x] Docker healthcheck works
- [x] Run full test suite: `make test-all` and `cd agent-worker && npm test`
- [x] Run linter: `go vet ./...` for Go code
- [x] Verify test coverage meets 80%+

### Task 13: Update documentation

- [x] Update CLAUDE.md with new architecture and test commands
- [x] Update `.env.example` with new environment variables
- [x] Move this plan to `docs/plans/completed/`
