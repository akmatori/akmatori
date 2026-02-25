# Replace TypeScript customTools with Python Script Wrappers

## Overview

Replace 8 TypeScript `customTools` (defined in `mcp-tools.ts` and `mcp-client.ts`) with Python script wrappers that the LLM agent calls via the bash tool. The pi-mono SDK's `createBashTool()` `spawnHook` injects `MCP_GATEWAY_URL`, `INCIDENT_ID`, and `PYTHONPATH=/tools` per-session, so Python wrappers can import `mcp_client` and call MCP Gateway over HTTP (JSON-RPC 2.0). The Go `generateSkillMd()` function is updated to emit Python usage examples per tool instance.

## Context

- Files involved:
  - Create: `agent-worker/tools/mcp_client.py` (MCP Gateway HTTP client)
  - Create: `agent-worker/tools/ssh/__init__.py` (SSH wrapper)
  - Create: `agent-worker/tools/zabbix/__init__.py` (Zabbix wrapper)
  - Modify: `agent-worker/Dockerfile` (add python3, COPY tools/)
  - Modify: `agent-worker/src/agent-runner.ts` (replace customTools with spawnHook)
  - Delete: `agent-worker/src/tools/mcp-tools.ts`
  - Delete: `agent-worker/src/tools/mcp-client.ts`
  - Modify: `agent-worker/tests/tools.test.ts` (rewrite for Python wrappers)
  - Modify: `agent-worker/tests/agent-runner.test.ts` (remove mcp-tools mock)
  - Modify: `internal/services/skill_service.go` (Python usage examples in SKILL.md)
  - Modify: `internal/services/skill_service_test.go` (update tests)
- Related patterns: Existing `tool_instance_id` routing in MCP Gateway registry, existing `extractToolDetails()` in skill_service.go
- Dependencies: `@mariozechner/pi-coding-agent` SDK (already provides `createBashTool` with `spawnHook`)

## Development Approach

- **Testing approach**: Regular (code first, then tests), except Task 8 which uses TDD
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Python wrappers use stdlib only (no pip installs)
- The design doc at `docs/plans/2026-02-25-script-wrappers-impl.md` contains the exact code to write for each file

## Implementation Steps

### Task 1: Create Python MCP Gateway Client

**Files:**
- Create: `agent-worker/tools/mcp_client.py`

- [x] Create `agent-worker/tools/` directory
- [x] Create `mcp_client.py` with `MCPClient` class, `MCPError` exception, `call()` convenience function
  - Uses only stdlib: `json`, `os`, `urllib.request`, `urllib.error`
  - Reads `MCP_GATEWAY_URL` and `INCIDENT_ID` from env
  - Sends JSON-RPC 2.0 to `{gateway_url}/mcp` with `X-Incident-ID` header
  - 300s timeout, auto-incrementing request IDs
  - Parses MCP content response format (extracts text from content array)
  - Full code provided in design doc Task 1
- [x] Verify syntax: `python3 -c "import ast; ast.parse(open('agent-worker/tools/mcp_client.py').read()); print('OK')"`

### Task 2: Create SSH Python Wrapper

**Files:**
- Create: `agent-worker/tools/ssh/__init__.py`

- [ ] Create `agent-worker/tools/ssh/` directory
- [ ] Create `__init__.py` with `execute_command()`, `test_connectivity()`, `get_server_info()`
  - All functions accept `tool_instance_id` kwarg
  - Uses `sys.path.insert` to resolve imports from parent dir
  - Calls `mcp_client.call()` with `ssh.` prefixed tool names
  - Full code provided in design doc Task 2
- [ ] Verify syntax: `python3 -c "import ast; ast.parse(open('agent-worker/tools/ssh/__init__.py').read()); print('OK')"`

### Task 3: Create Zabbix Python Wrapper

**Files:**
- Create: `agent-worker/tools/zabbix/__init__.py`

- [ ] Create `agent-worker/tools/zabbix/` directory
- [ ] Create `__init__.py` with `get_hosts()`, `get_problems()`, `get_history()`, `get_items_batch()`, `acknowledge_event()`
  - Only the 5 tools that exist in MCP Gateway (drops old `get_items`, `get_triggers`, `api_request`)
  - All functions accept `tool_instance_id` kwarg
  - Full code provided in design doc Task 3
- [ ] Verify syntax: `python3 -c "import ast; ast.parse(open('agent-worker/tools/zabbix/__init__.py').read()); print('OK')"`

### Task 4: Add Python tools to agent-worker Dockerfile

**Files:**
- Modify: `agent-worker/Dockerfile`

- [ ] Add `python3` to the `apt-get install` line in the runtime stage (~line 33)
- [ ] Add `COPY tools/ /tools/` before the `USER agent` line (~line 63)
- [ ] Verify Dockerfile syntax with a quick build check

### Task 5: Replace customTools with spawnHook in agent-runner.ts

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`

- [ ] Remove `import { createMCPTools } from "./tools/mcp-tools.js";`
- [ ] Add `createBashTool` to the pi-coding-agent import
- [ ] Remove `const mcpTools = createMCPTools(...)` call (~line 193)
- [ ] Create bash tool with `spawnHook` that injects `MCP_GATEWAY_URL`, `INCIDENT_ID`, `PYTHONPATH=/tools:...`
- [ ] Create coding tools array, replacing default bash with custom spawnHook bash
- [ ] Update `createAgentSession()` call: use custom `tools` array, remove `customTools` property
- [ ] Run build: `cd agent-worker && npm run build` - must compile without errors

### Task 6: Delete TypeScript MCP tool files

**Files:**
- Delete: `agent-worker/src/tools/mcp-tools.ts`
- Delete: `agent-worker/src/tools/mcp-client.ts`

- [ ] Delete both files
- [ ] Run build: `cd agent-worker && npm run build` - must compile (no other files import these)

### Task 7: Update agent-worker tests

**Files:**
- Modify: `agent-worker/tests/tools.test.ts` (full rewrite)
- Modify: `agent-worker/tests/agent-runner.test.ts` (update mock and assertions)

- [ ] Rewrite `tools.test.ts` to validate Python wrapper files:
  - `mcp_client.py`: exists, defines MCPClient, defines call(), reads env vars, sends X-Incident-ID header
  - `ssh/__init__.py`: exists, exports 3 functions, all accept tool_instance_id, uses ssh. prefix
  - `zabbix/__init__.py`: exists, exports 5 functions, all accept tool_instance_id, uses zabbix. prefix
- [ ] Update `agent-runner.test.ts`:
  - Remove `vi.mock("../src/tools/mcp-tools.js", ...)` block (~lines 146-150)
  - Replace "should pass MCP tools as customTools" test with "should NOT pass customTools" test
  - Add "should configure bash spawnHook with MCP env vars" test
- [ ] Run tests: `cd agent-worker && npm test` - all must pass

### Task 8: Enrich generateSkillMd() with Python usage examples (TDD)

**Files:**
- Modify: `internal/services/skill_service.go` (~line 556+)
- Modify: `internal/services/skill_service_test.go`

- [ ] Write failing test `TestGenerateSkillMd_ContainsPythonExamples`:
  - Creates SSH (ID=3) and Zabbix (ID=2) tool instances
  - Asserts SKILL.md contains ` ```python ` blocks
  - Asserts `from ssh import ...` and `tool_instance_id=3`
  - Asserts `from zabbix import ...` and `tool_instance_id=2`
- [ ] Run test to verify it fails: `go test -v -run TestGenerateSkillMd_ContainsPythonExamples ./internal/services/...`
- [ ] Update `TestGenerateSkillMd_NoPythonImports` (line 276): change to assert no OLD-style Python imports (`import sys`, `from scripts.`) but now EXPECTS `\`\`\`python` blocks
- [ ] Extract `generateToolUsageExample(tool)` function with switch on tool type:
  - `ssh`: returns Python code block with `from ssh import ...` and calls with `tool_instance_id=ID`
  - `zabbix`: returns Python code block with `from zabbix import ...` and calls with `tool_instance_id=ID`
  - default: returns text instruction with `tool_instance_id: ID`
- [ ] Update `generateSkillMd()` to call `generateToolUsageExample(tool)` for each enabled tool
- [ ] Run all skill service tests: `go test -v -run "TestGenerateSkillMd|TestExtractToolDetails|TestStripAutoGenerated|TestAssignTools" ./internal/services/...` - all must pass

### Task 9: Run all tests and verify

- [ ] Run Go tests: `make test` - all pass
- [ ] Run agent-worker tests: `make test-agent` - all pass
- [ ] Run MCP gateway tests: `make test-mcp` - all pass (should be unchanged)
- [ ] Build agent container: `docker-compose build akmatori-agent` - builds with python3 and /tools/

### Task 10: Final verification and cleanup

- [ ] Run `git status` - verify clean state
- [ ] Run `make verify` - full pre-commit verification passes
- [ ] Verify no regressions in existing functionality
