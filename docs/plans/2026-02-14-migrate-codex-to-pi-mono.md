# Migrate from Codex CLI to pi-mono coding-agent (SDK mode)

## Overview

Replace the Go-based codex-worker (which spawns the OpenAI Codex CLI as a subprocess) with a Node.js/TypeScript worker that uses the `@mariozechner/pi-coding-agent` SDK directly in-process. This enables multi-provider LLM support (Anthropic, OpenAI, Google, OpenRouter, etc.), richer tool integration, and aligns with the openclaw reference architecture.

## Context

**Current Architecture:**
- `akmatori-codex` container runs a Go binary (`codex-worker`) that connects to the API via WebSocket
- The Go worker spawns `codex exec` CLI as a subprocess, parsing JSON events from stdout
- Tools are Python wrappers calling the MCP Gateway over HTTP
- Only supports OpenAI models (API key or ChatGPT subscription auth)
- Skills use `SKILL.md` files with YAML frontmatter + markdown body

**Target Architecture:**
- `akmatori-agent` container runs a Node.js process using `@mariozechner/pi-coding-agent` SDK
- The Node worker connects to the API via the same WebSocket protocol (drop-in replacement)
- Tools are registered as pi-mono `ToolDefinition` custom tools (calling MCP Gateway)
- Supports all LLM providers: Anthropic, OpenAI, Google, OpenRouter, custom
- Skills map to pi-mono's system prompt override + AGENTS.md context files

**Files involved (modify):**
- `docker-compose.yml` - Replace `akmatori-codex` service with `akmatori-agent`
- `internal/database/models.go` - Evolve `OpenAISettings` to `LLMSettings` (multi-provider)
- `internal/database/db.go` - Migration for new LLM settings table
- `internal/handlers/codex_ws.go` - Rename to `agent_ws.go`, update message types for multi-provider
- `internal/services/skill_service.go` - Update workspace setup for pi-mono format
- `internal/config/config.go` - New config for agent container
- `web/` - Frontend settings page for multi-provider LLM config
- `.env.example` - New environment variables

**Files involved (create):**
- `agent-worker/` - New Node.js/TypeScript worker module
- `agent-worker/package.json` - Dependencies on pi-mono packages
- `agent-worker/tsconfig.json` - TypeScript config
- `agent-worker/src/index.ts` - Entry point
- `agent-worker/src/ws-client.ts` - WebSocket client (port of Go client)
- `agent-worker/src/orchestrator.ts` - Message routing and incident handling
- `agent-worker/src/agent-runner.ts` - pi-mono SDK integration (createAgentSession)
- `agent-worker/src/tools/mcp-tools.ts` - MCP Gateway tool definitions for pi-mono
- `agent-worker/src/tools/ssh-tool.ts` - SSH tool as pi-mono ToolDefinition
- `agent-worker/src/tools/zabbix-tool.ts` - Zabbix tool as pi-mono ToolDefinition
- `agent-worker/src/auth.ts` - Multi-provider auth storage adapter
- `agent-worker/src/types.ts` - Shared TypeScript types
- `agent-worker/Dockerfile` - Node.js container build
- `agent-worker/tests/` - Test suite

**Files involved (remove/deprecate):**
- `codex-worker/` - Entire Go worker module (replaced by agent-worker)
- `codex-tools/` - Python MCP wrappers (replaced by TypeScript tool definitions)
- `Dockerfile.codex` - Old container build
- `entrypoint.sh` - Old container entrypoint

**Related patterns (from openclaw reference):**
- SDK mode: `createAgentSession()` with `customTools`, `authStorage`, `modelRegistry`
- System prompt: `DefaultResourceLoader` with `systemPromptOverride`
- Tools: `ToolDefinition` interface with `execute` function, `Type.Object` params
- Auth: `AuthStorage.setRuntimeApiKey()` for per-request provider keys
- Sessions: `SessionManager.inMemory()` for stateless or `.create(cwd)` for persistent
- Events: `session.subscribe()` for streaming message/tool events
- Abort: `session.abort()` for cancellation

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
- The Go API server remains unchanged - only the worker container is replaced
- The WebSocket protocol between API and worker is preserved (backward compatible)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **Migration strategy**: Run old and new workers side-by-side during development, switch via env var

## Implementation Steps

### Task 1: Create agent-worker Node.js project scaffold

**Files:**
- Create: `agent-worker/package.json`
- Create: `agent-worker/tsconfig.json`
- Create: `agent-worker/src/types.ts`
- Create: `agent-worker/.gitignore`

**Steps:**
- [ ] Initialize `agent-worker/` as a Node.js project with TypeScript
- [ ] Add dependencies: `@mariozechner/pi-coding-agent`, `@mariozechner/pi-ai`, `@mariozechner/pi-agent-core`, `@sinclair/typebox`, `ws`, `@types/ws`
- [ ] Configure tsconfig for ESM output (Node 22+, moduleResolution: "bundler")
- [ ] Define shared types in `src/types.ts` mirroring the Go WebSocket message protocol:
  - `WebSocketMessage` interface matching `ws.Message` from Go
  - `MessageType` enum matching Go constants
  - `OpenAISettings`, `ProxyConfig`, `IncidentCallback` types
  - `ExecuteResult` type for runner output
- [ ] Add build scripts: `build`, `dev`, `test`
- [ ] Write unit tests for type definitions (serialization round-trip matches Go format)
- [ ] Run `npm test` - must pass before task 2

### Task 2: Implement WebSocket client

**Files:**
- Create: `agent-worker/src/ws-client.ts`
- Create: `agent-worker/tests/ws-client.test.ts`

**Steps:**
- [ ] Port `codex-worker/internal/ws/client.go` to TypeScript using the `ws` library
- [ ] Implement `WebSocketClient` class with:
  - `connect(url: string)` - establish connection with reconnection logic
  - `send(msg: WebSocketMessage)` - send JSON message
  - `sendOutput(incidentId, output)` - convenience for streaming output
  - `sendCompleted(incidentId, sessionId, response, tokensUsed, executionTimeMs)` - completion
  - `sendError(incidentId, errorMsg)` - error notification
  - `sendHeartbeat()` - heartbeat
  - `onMessage(handler)` - register message handler
  - `startHeartbeat(intervalMs)` - periodic heartbeat
  - `close()` - graceful shutdown
  - `reconnect()` - reconnection with exponential backoff
- [ ] Match the exact JSON message format from Go so the API handler doesn't need changes
- [ ] Write tests using a mock WebSocket server:
  - Connection and reconnection
  - Message serialization matches Go format
  - Heartbeat sending
  - Error handling on connection loss
- [ ] Run `npm test` - must pass before task 3

### Task 3: Implement MCP Gateway tool definitions

**Files:**
- Create: `agent-worker/src/tools/mcp-client.ts`
- Create: `agent-worker/src/tools/ssh-tool.ts`
- Create: `agent-worker/src/tools/zabbix-tool.ts`
- Create: `agent-worker/tests/tools.test.ts`

**Steps:**
- [ ] Create `mcp-client.ts` - HTTP client for the MCP Gateway (port of Python `mcp_client.py`):
  - `callTool(gatewayUrl, toolName, method, params)` → JSON result
  - Include timeout, error handling, and logging
- [ ] Create SSH tool as pi-mono `ToolDefinition`:
  - `name: "ssh"`, parameters: host, command, port, username, timeout
  - `execute` calls MCP Gateway `/ssh/execute` endpoint
  - Formats output for LLM consumption
- [ ] Create Zabbix tool as pi-mono `ToolDefinition`:
  - Multiple sub-tools or single tool with `action` parameter
  - Actions: get_hosts, get_problems, get_history, get_items, acknowledge_event
  - `execute` calls MCP Gateway `/zabbix/*` endpoints
  - Formats output for LLM consumption
- [ ] Create `createMCPTools(gatewayUrl, incidentId)` factory that returns all tool definitions
- [ ] Write tests with mocked HTTP responses:
  - SSH tool execute with successful command
  - SSH tool execute with connection error
  - Zabbix tool get_hosts with mock response
  - Zabbix tool get_problems with mock response
  - Tool parameter validation
- [ ] Run `npm test` - must pass before task 4

### Task 4: Implement agent runner (pi-mono SDK integration)

**Files:**
- Create: `agent-worker/src/agent-runner.ts`
- Create: `agent-worker/src/auth.ts`
- Create: `agent-worker/tests/agent-runner.test.ts`

**Steps:**
- [ ] Create `auth.ts` - adapter between Akmatori's per-request API keys and pi-mono's `AuthStorage`:
  - `createRuntimeAuthStorage(provider, apiKey)` - creates AuthStorage with runtime key
  - Support for OpenAI API key, Anthropic key, and other providers
  - Map Akmatori's `AuthMethod` to pi-mono provider names
- [ ] Create `AgentRunner` class wrapping pi-mono SDK:
  - `execute(incidentId, task, settings, proxyConfig, onOutput, onEvent)`:
    - Create workspace-scoped `AuthStorage` with runtime API key from settings
    - Create `ModelRegistry` from auth storage
    - Resolve model from settings (map `gpt-5.1-codex` → OpenAI model, or use Anthropic/Google)
    - Build custom tools via `createMCPTools()`
    - Create `DefaultResourceLoader` with `systemPromptOverride` (from AGENTS.md)
    - Call `createAgentSession()` with:
      - `cwd`: workspace directory
      - `tools`: `createCodingTools(cwd)` (read, bash, edit, write)
      - `customTools`: MCP tools (SSH, Zabbix, etc.)
      - `sessionManager`: `SessionManager.create(cwd)` for persistence or `.inMemory()`
      - `settingsManager`: `SettingsManager.inMemory()` with compaction enabled
      - `authStorage`, `modelRegistry`, `model`, `thinkingLevel`
    - Subscribe to session events and stream to `onOutput` callback
    - Call `session.prompt(task)` and await completion
    - Collect result: response text, token usage, execution time
    - Return `ExecuteResult`
  - `resume(incidentId, sessionId, message, settings, ...)`:
    - Open existing session via `SessionManager.open(sessionFile)`
    - Call `session.prompt(message)` for follow-up
  - `cancel(incidentId)`:
    - Call `session.abort()` on active session
  - `dispose()`:
    - Clean up all active sessions
- [ ] Handle event mapping:
  - `message_update` (text_delta) → accumulate response text
  - `tool_execution_start/end` → format as streaming output (matching current format)
  - `turn_end` → extract token usage
  - `agent_end` → mark completion
- [ ] Handle proxy configuration:
  - Set `HTTP_PROXY`/`HTTPS_PROXY` env vars before creating session if proxy enabled
  - Respect per-service proxy toggles
- [ ] Write tests (with mocked pi-mono SDK):
  - Execute with OpenAI API key
  - Execute with Anthropic API key
  - Resume existing session
  - Cancel active execution
  - Event streaming to output callback
  - Error handling (auth failure, model not found, timeout)
  - Proxy configuration application
- [ ] Run `npm test` - must pass before task 5

### Task 5: Implement orchestrator (message routing)

**Files:**
- Create: `agent-worker/src/orchestrator.ts`
- Create: `agent-worker/src/index.ts`
- Create: `agent-worker/tests/orchestrator.test.ts`

**Steps:**
- [ ] Create `Orchestrator` class (port of Go `orchestrator.go`):
  - Constructor takes `config: { apiWsUrl, mcpGatewayUrl, workspaceDir, sessionsDir }`
  - `start()` - connect WebSocket, register handler, start heartbeat, send "ready" status
  - `stop()` - cancel active runs, close WebSocket
  - `handleMessage(msg)` - route by message type:
    - `new_incident` → extract LLM settings, call `agentRunner.execute()`
    - `continue_incident` → call `agentRunner.resume()`
    - `cancel_incident` → call `agentRunner.cancel()`
    - `device_auth_start` → handle device auth (if needed, or skip for multi-provider)
  - Stream output back via WebSocket `sendOutput()`
  - Send completion via WebSocket `sendCompleted()`
  - Handle errors via WebSocket `sendError()`
- [ ] Create `index.ts` entry point:
  - Read config from environment variables (same names as Go: `API_WS_URL`, `MCP_GATEWAY_URL`, etc.)
  - Create and start orchestrator
  - Handle SIGTERM/SIGINT for graceful shutdown
  - Reconnection loop on WebSocket disconnect
- [ ] Write tests:
  - Message routing for all message types
  - Output streaming through WebSocket
  - Completion with metrics
  - Error propagation
  - Graceful shutdown
  - Reconnection behavior
- [ ] Run `npm test` - must pass before task 6

### Task 6: Create Docker container for agent-worker

**Files:**
- Create: `agent-worker/Dockerfile`
- Modify: `docker-compose.yml`

**Steps:**
- [ ] Create `agent-worker/Dockerfile`:
  - Base: `node:22-bookworm`
  - Install system deps: `ripgrep`, `git`, `jq`, `python3` (for tools that need it)
  - Create non-root user (UID 1001, matching old codex user)
  - Copy `agent-worker/`, install deps, build TypeScript
  - Set environment defaults matching Go container
  - Run as non-root user
  - Health check: `pgrep node` or HTTP health endpoint
- [ ] Update `docker-compose.yml`:
  - Rename `akmatori-codex` → `akmatori-agent` (or add as new service)
  - Point to new `agent-worker/Dockerfile`
  - Keep same network config (frontend, api-internal, codex-network)
  - Keep same volume mounts (sessions, workspaces, context)
  - Keep same environment variables (add new ones for multi-provider)
- [ ] Add new environment variables to `.env.example`:
  - `LLM_PROVIDER` (default: `openai`)
  - `ANTHROPIC_API_KEY` (optional)
  - `OPENAI_API_KEY` (optional)
  - `GOOGLE_API_KEY` (optional)
- [ ] Test Docker build: `docker build -t akmatori-agent ./agent-worker`
- [ ] Test container runs and connects to API WebSocket
- [ ] Verify health check works

### Task 7: Update database models for multi-provider LLM support

**Files:**
- Modify: `internal/database/models.go`
- Modify: `internal/database/db.go`
- Create: `internal/database/models_test.go` (new tests)

**Steps:**
- [ ] Evolve `OpenAISettings` to support multiple providers while maintaining backward compatibility:
  - Add `Provider` field: `openai`, `anthropic`, `google`, `openrouter`, `custom`
  - Add `AnthropicAPIKey` field for Anthropic provider
  - Add `GoogleAPIKey` field for Google provider
  - Add `OpenRouterAPIKey` field for OpenRouter provider
  - Add `CustomProviderURL` field for custom provider endpoints
  - Keep existing OpenAI fields for backward compatibility
  - Update `IsConfigured()` to check provider-specific keys
  - Update `IsActive()` accordingly
  - Keep table name as `openai_settings` to avoid migration complexity (rename later)
- [ ] Add database migration in `db.go`:
  - AutoMigrate will add new columns
  - Set default `Provider` to `openai` for existing rows
- [ ] Update helper functions:
  - `GetOpenAISettings()` → works as before, returns full settings
  - Add `GetLLMProvider()` helper
  - Add `GetProviderAPIKey(provider string)` helper
- [ ] Write tests:
  - Multi-provider configuration
  - Backward compatibility (existing OpenAI-only config still works)
  - Provider validation
  - API key retrieval by provider
- [ ] Run `go test ./internal/database/...` - must pass before task 8

### Task 8: Update API WebSocket handler for multi-provider

**Files:**
- Modify: `internal/handlers/codex_ws.go`
- Modify: `internal/handlers/alert.go`

**Steps:**
- [ ] Update `CodexMessage` struct to include multi-provider fields:
  - Add `Provider` field
  - Add `AnthropicAPIKey`, `GoogleAPIKey`, etc. fields
  - Keep existing OpenAI fields for backward compatibility
- [ ] Update `StartIncident()` to include provider-specific API key:
  - Read `Provider` from LLM settings
  - Include the correct API key based on provider
- [ ] Update `ContinueIncident()` similarly
- [ ] Ensure the WebSocket protocol changes are backward compatible:
  - If `Provider` is empty, default to `openai`
  - Old worker ignores unknown fields
  - New worker handles both old and new format
- [ ] Update relevant tests
- [ ] Run `go test ./internal/handlers/...` - must pass before task 9

### Task 9: Update skill service for pi-mono workspace format

**Files:**
- Modify: `internal/services/skill_service.go`

**Steps:**
- [ ] Update `SpawnIncidentManager()` to create pi-mono compatible workspace:
  - Instead of `.codex/AGENTS.md`, create `AGENTS.md` at workspace root (pi-mono walks up from cwd)
  - Instead of `.codex/skills/`, create `.pi/skills/` or embed skill content in AGENTS.md
  - Keep same tool symlink mechanism but adapt paths for Node.js
- [ ] Update `generateIncidentAgentsMd()` to output pi-mono compatible format:
  - pi-mono reads `AGENTS.md` files (same format, different location)
  - Include tool instructions adapted for pi-mono's tool calling
  - Remove Codex-specific structured output protocol, replace with pi-mono compatible format
- [ ] Update `generateSkillMd()`:
  - Keep YAML frontmatter format (pi-mono supports this)
  - Update tool import instructions for pi-mono tool names
  - Tools are now native pi-mono `ToolDefinition` objects, not Python scripts
- [ ] Ensure backward compatibility during migration:
  - If both old and new worker might run, create both `.codex/` and pi-mono compatible files
- [ ] Update tests
- [ ] Run `go test ./internal/services/...` - must pass before task 10

### Task 10: Update frontend for multi-provider LLM settings

**Files:**
- Modify: `web/src/` - Settings page components for LLM provider configuration

**Steps:**
- [ ] Update the OpenAI settings page to become a general "LLM Provider" settings page:
  - Provider selector: OpenAI, Anthropic, Google, OpenRouter, Custom
  - Per-provider API key input fields
  - Model selector that shows models for the selected provider
  - Thinking level / reasoning effort selector
  - Keep existing proxy configuration
- [ ] Update API calls to use the updated settings endpoints
- [ ] Keep backward compatibility: if only OpenAI is configured, it works as before
- [ ] Update model dropdown to show provider-appropriate models:
  - OpenAI: gpt-5.1-codex, gpt-5.2, etc.
  - Anthropic: claude-opus-4-5, claude-sonnet-4-5, etc.
  - Google: gemini-2.5-pro, gemini-2.5-flash, etc.
- [ ] Test the settings page renders and saves correctly

### Task 11: Integration testing and cleanup

**Files:**
- Modify: `Makefile` - Add agent-worker test targets
- Modify: `.gitignore` - Add agent-worker build artifacts
- Modify: `CLAUDE.md` - Update instructions for new architecture

**Steps:**
- [ ] Add Makefile targets:
  - `make test-agent` - Run agent-worker tests
  - `make test-all` - Include agent-worker in full test suite
  - `make build-agent` - Build agent-worker Docker image
- [ ] End-to-end integration test:
  - Start all containers with `docker-compose up`
  - Verify agent-worker connects to API via WebSocket
  - Send a test incident via API
  - Verify agent-worker receives and processes it
  - Verify streaming output reaches the API
  - Verify completion with metrics
- [ ] Update CLAUDE.md with:
  - New architecture description (pi-mono SDK instead of Codex CLI)
  - Updated test commands for agent-worker
  - Updated container mapping
  - Updated directory structure
- [ ] Remove or deprecate old codex-worker code:
  - Move `codex-worker/` to `_deprecated/codex-worker/` (keep for reference)
  - Move `codex-tools/` to `_deprecated/codex-tools/`
  - Move `Dockerfile.codex` to `_deprecated/`
- [ ] Update `.gitignore` for agent-worker artifacts

### Task 12: Verify acceptance criteria

- [ ] Agent-worker connects to API via WebSocket and sends "ready" status
- [ ] New incidents are processed: API sends task → worker creates pi-mono session → streams output → sends completion
- [ ] Continue incidents work: existing session is resumed with follow-up message
- [ ] Cancel incidents work: active session is aborted
- [ ] SSH tool works: agent can execute remote commands via MCP Gateway
- [ ] Zabbix tool works: agent can query Zabbix API via MCP Gateway
- [ ] Multi-provider support: can configure Anthropic, OpenAI, or Google as LLM provider
- [ ] Streaming output format is compatible with existing frontend display
- [ ] Session persistence works: sessions survive container restarts
- [ ] Proxy configuration is respected for LLM API calls
- [ ] Docker build and healthcheck work
- [ ] Run full test suite: `make test-all` and `npm test` in agent-worker
- [ ] Run linter: `go vet ./...` for Go code
- [ ] Verify test coverage meets 80%+

### Task 13: Update documentation

- [ ] Update CLAUDE.md with new architecture and test commands
- [ ] Update `.env.example` with new environment variables
- [ ] Move this plan to `docs/plans/completed/`
