# Runbook Search via QMD for AI Agent

## Overview

Integrate QMD (hybrid BM25 + vector + LLM reranking search engine) as a Docker sidecar service so the AI agent can semantically search runbooks during incident investigations, replacing the current filesystem-browse approach with intelligent search.

## Context

- **Current state**: Agent reads `/akmatori/runbooks/` via filesystem (`execute_script` with `fs.readdirSync`/`readFileSync`). No search — agent must manually scan filenames and content.
- **Goal**: Agent calls `gateway_call("qmd.query", {searches: [{type: "lex", query: "database timeout"}]})` to find relevant runbooks by meaning, not just filename.
- **QMD**: Local hybrid search engine at `/opt/qmd`. Supports BM25 + vector + LLM reranking. Has MCP server mode (stdio/HTTP) and REST API.
- **Integration path**: QMD runs as a Docker service → registered as external MCP server in the gateway's proxy system → agent discovers and calls QMD tools through existing `gateway_call` infrastructure.

### Files involved

| Component | Key files |
|-----------|-----------|
| QMD source | `/opt/qmd/` (external, not modified) |
| Docker setup | `docker-compose.yml` |
| MCP Gateway startup | `mcp-gateway/cmd/gateway/main.go` |
| MCP proxy | `mcp-gateway/internal/mcpproxy/` |
| Tool registry | `mcp-gateway/internal/tools/registry.go` |
| MCP proxy DB model | `mcp-gateway/internal/database/db.go` (`MCPServerConfig`) |
| Runbook service | `internal/services/runbook_service.go` |
| Incident prompt | `internal/database/db.go` (`DefaultIncidentManagerPrompt`) |
| Agent runner | `agent-worker/src/agent-runner.ts` |

### Related patterns

- External MCP servers are configured in DB (`mcp_server_configs` table), loaded by `DefaultMCPProxyLoader`, and proxied through the gateway with namespaced tool names (e.g., `qmd.query`).
- The gateway already has `/reload/mcp-servers` endpoint to hot-reload proxy tools.
- `SyncRunbookFiles()` is the single point where runbook files are written to disk — the natural trigger point for re-indexing.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add QMD Docker service

**Files:**
- Modify: `docker-compose.yml`
- Create: `qmd/Dockerfile`
- Create: `qmd/entrypoint.sh`
- Create: `qmd/qmd-config.yml` (QMD index.yml for the runbooks collection)

**Description**: Add a QMD container that indexes `/akmatori/runbooks/` and exposes its MCP HTTP server on the `codex-network`.

- [x] Create `qmd/Dockerfile` based on Node.js 22 image that installs QMD from `/opt/qmd` (copy or mount)
- [x] Create `qmd/qmd-config.yml` defining the runbooks collection:
  ```yaml
  collections:
    runbooks:
      path: /akmatori/runbooks
      pattern: "**/*.md"
      context:
        "/": "Runbook SOPs for incident investigation and remediation"
  ```
- [x] Create `qmd/entrypoint.sh` that:
  1. Copies `qmd-config.yml` to `~/.config/qmd/index.yml`
  2. Runs `qmd update` to scan files
  3. Runs `qmd embed` to generate vector embeddings
  4. Starts `qmd mcp --http --port 8181` (foreground)
- [x] Add `qmd` service to `docker-compose.yml`:
  - Mount `./akmatori_data/runbooks:/akmatori/runbooks:ro`
  - Mount a named volume for QMD cache (`qmd_cache:/root/.cache/qmd`)
  - Network: `codex-network` only (internal, same as agent ↔ gateway)
  - Expose port 8181 internally
  - Depends on: `init-dirs`
  - Health check: `curl -sf http://localhost:8181/health`
- [x] Verify: `docker-compose build qmd && docker-compose up qmd` starts and indexes successfully
- [x] No automated tests for this task (Docker infrastructure)

### Task 2: Auto-register QMD as MCP proxy server

**Files:**
- Modify: `mcp-gateway/cmd/gateway/main.go`
- Modify: `mcp-gateway/internal/mcpproxy/handler.go` (if needed for SSE/HTTP transport to QMD)

**Description**: On gateway startup, ensure QMD is registered as an external MCP server so its tools (`qmd.query`, `qmd.get`, etc.) are available through the gateway proxy.

- [ ] Add environment variable `QMD_URL` (default: `http://qmd:8181`) to gateway config
- [ ] In gateway `main.go`, after proxy handler initialization, register QMD as a system-level MCP proxy:
  - Check if `QMD_URL` is set and non-empty
  - Register with `proxyHandler.RegisterServer()` using SSE/HTTP transport to QMD's MCP endpoint
  - Namespace prefix: `qmd` (tools become `qmd.query`, `qmd.get`, `qmd.multi_get`, `qmd.status`)
  - Skip if QMD is unreachable (log warning, don't crash)
- [ ] Add `QMD_URL=http://qmd:8181` to mcp-gateway environment in `docker-compose.yml`
- [ ] Write tests: verify QMD tools appear in registry when QMD_URL is configured
- [ ] Run `make test-mcp` — must pass

### Task 3: Trigger QMD re-index on runbook changes

**Files:**
- Modify: `internal/services/runbook_service.go`

**Description**: After `SyncRunbookFiles()` writes runbook markdown files, notify QMD to re-index. This ensures the search index stays current when runbooks are created, updated, or deleted.

- [ ] Add a `qmdReindexURL` field to `RunbookService` (e.g., `http://qmd:8181/reindex` or call QMD's update mechanism)
- [ ] Since QMD's MCP server doesn't expose an update endpoint, implement re-indexing by calling QMD's REST search endpoint approach:
  - **Option A (preferred)**: Add a lightweight `/update` REST endpoint to QMD's HTTP server that triggers `store.update()` + `store.embed()`. This is a small upstream change to QMD.
  - **Option B (no QMD changes)**: Use Docker exec to run `qmd update && qmd embed` in the QMD container. Requires the API server to have Docker socket access (not ideal).
  - **Option C (simplest)**: Run a file watcher or periodic cron inside the QMD container that detects changes to `/akmatori/runbooks/` and re-indexes. Add `inotifywait` or a simple poll loop in the entrypoint.
- [ ] Recommended: Go with **Option A** — add `/update` endpoint to QMD, then call it from `SyncRunbookFiles()` via HTTP POST
- [ ] In `RunbookService`, after successful `SyncRunbookFiles()`, make a non-blocking HTTP POST to QMD's update endpoint. Log warnings on failure but don't fail the runbook operation.
- [ ] Add `QMD_URL` environment variable to the API server (or pass via constructor)
- [ ] Write tests: mock HTTP call, verify it's triggered after sync, verify runbook ops don't fail if QMD is down
- [ ] Run `make test` — must pass

### Task 4: Add /update endpoint to QMD MCP HTTP server

**Files:**
- Modify: `/opt/qmd/src/mcp/server.ts`

**Description**: Add a POST `/update` REST endpoint to QMD's HTTP server that triggers re-indexing (scan files + generate embeddings for new/changed documents).

- [ ] In the HTTP handler section of `server.ts`, add handler for `POST /update`:
  ```typescript
  if (pathname === "/update" && nodeReq.method === "POST") {
    await store.update();   // re-scan filesystem
    await store.embed();    // embed new/changed docs
    nodeRes.writeHead(200, { "Content-Type": "application/json" });
    nodeRes.end(JSON.stringify({ status: "updated" }));
    return;
  }
  ```
- [ ] Ensure the endpoint is idempotent and safe to call frequently (QMD already handles no-op updates efficiently via content hashing)
- [ ] Write test: call `/update` endpoint, verify 200 response
- [ ] Run QMD tests: `cd /opt/qmd && npm test`

### Task 5: Update agent incident prompt to use QMD search

**Files:**
- Modify: `internal/database/db.go` (`DefaultIncidentManagerPrompt`)

**Description**: Update the incident manager prompt so the agent uses `gateway_call("qmd.query", ...)` to search for relevant runbooks instead of manually browsing the filesystem.

- [ ] Replace the current runbook instruction block:
  ```
  ## Runbooks
  Before starting your investigation, check the /akmatori/runbooks/ directory for relevant runbooks.
  If a matching runbook exists, follow its procedures as your primary investigation guide.
  ```
  With:
  ```
  ## Runbooks
  Before starting your investigation, search for relevant runbooks using the QMD search tool:

  gateway_call("qmd.query", {
    "searches": [{"type": "lex", "query": "<keywords from the alert>"}],
    "limit": 5
  })

  If relevant runbooks are found (score > 0.3), retrieve the full content:

  gateway_call("qmd.get", {"path": "<file path from search result>"})

  Follow matching runbook procedures as your primary investigation guide.
  If no relevant runbooks are found, proceed with general investigation.
  ```
- [ ] Keep the `/akmatori/runbooks/` filesystem access as a fallback mention (in case QMD is unavailable)
- [ ] Write test: verify the prompt contains `qmd.query` reference
- [ ] Run `make test` — must pass

### Task 6: Ensure QMD tools bypass per-incident allowlist

**Files:**
- Modify: `mcp-gateway/internal/auth/authorizer.go` (if needed)

**Description**: QMD tools (like `qmd.query`) are system-level tools, not tied to specific tool instances. They should be available to all incidents without explicit allowlisting, similar to how MCP proxy tools already bypass the allowlist.

- [ ] Verify that MCP proxy tools (those with dots in the name, e.g., `qmd.query`) already bypass the per-incident allowlist check. Based on the existing code, proxy tools should bypass — confirm this.
- [ ] If proxy tools don't bypass, add QMD namespace (`qmd.*`) to the bypass list in the authorizer
- [ ] Write test: verify `qmd.query` is authorized for any incident without explicit allowlist entry
- [ ] Run `make test-mcp` — must pass

### Task 7: Verify acceptance criteria

- [ ] Manual test: Create a runbook about "database connection pool exhaustion"
- [ ] Manual test: Trigger an incident with a database-related alert
- [ ] Manual test: Verify agent calls `qmd.query` and finds the relevant runbook
- [ ] Manual test: Verify runbook updates trigger re-indexing (create/edit a runbook, search for new content)
- [ ] Manual test: Verify system works when QMD is down (graceful degradation)
- [ ] Run full test suite: `make verify`
- [ ] Run linter: `golangci-lint run`

### Task 8: Update documentation

- [ ] Update CLAUDE.md: add QMD service to architecture section, document `QMD_URL` env var
- [ ] Add QMD rebuild command to the Docker rebuild table in CLAUDE.md
- [ ] Move this plan to `docs/plans/completed/`
