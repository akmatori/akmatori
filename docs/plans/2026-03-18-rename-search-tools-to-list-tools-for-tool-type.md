# Rename `search_tools` to `list_tools_for_tool_type`

## Overview

Rename the agent-facing `search_tools` tool to `list_tools_for_tool_type` across the entire codebase. This makes the tool's purpose clearer — it lists available tools filtered by tool type, not a free-text search engine.

## Context

- Files involved: See each task below
- Related patterns: Other tool names follow verb_noun convention (`gateway_call`, `get_tool_detail`, `list_tool_types`, `execute_script`)
- Dependencies: None — pure rename, no logic changes

## Development Approach

- **Testing approach**: Regular (rename first, update tests to match)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Rename in agent-worker TypeScript source

**Files:**
- Modify: `agent-worker/src/gateway-tools.ts`
- Modify: `agent-worker/src/script-executor.ts`
- Modify: `agent-worker/src/agent-runner.ts`

- [ ] In `gateway-tools.ts`: rename `SearchToolsParams` → `ListToolsForToolTypeParams`, `SearchToolsInput` → `ListToolsForToolTypeInput`
- [ ] In `gateway-tools.ts`: rename the factory function `createSearchToolsTool` → `createListToolsForToolTypeTool`
- [ ] In `gateway-tools.ts`: change `name: "search_tools"` → `name: "list_tools_for_tool_type"`
- [ ] In `gateway-tools.ts`: update all `promptGuidelines` strings referencing `search_tools` to `list_tools_for_tool_type`
- [ ] In `gateway-tools.ts`: update the `execute_script` params description (injected globals list)
- [ ] In `gateway-tools.ts`: update `get_tool_detail` guidelines that reference `search_tools`
- [ ] In `gateway-tools.ts`: update `list_tool_types` guidelines that reference `search_tools`
- [ ] In `gateway-tools.ts`: update `execute_script` guidelines that reference `search_tools`
- [ ] In `script-executor.ts`: rename `globalThis.search_tools` → `globalThis.list_tools_for_tool_type`
- [ ] In `script-executor.ts`: update all JSDoc and error message strings referencing `search_tools`
- [ ] In `agent-runner.ts`: update the system prompt / bash tool guidelines referencing `search_tools`
- [ ] Run `make test-agent` — must pass before task 2

### Task 2: Rename in agent-worker tests

**Files:**
- Modify: `agent-worker/tests/gateway-tools.test.ts`
- Modify: `agent-worker/tests/script-executor.test.ts`
- Modify: `agent-worker/tests/agent-runner.test.ts`

- [ ] In `gateway-tools.test.ts`: update test descriptions and assertions checking for `"search_tools"` → `"list_tools_for_tool_type"`
- [ ] In `script-executor.test.ts`: update `search_tools` calls in test script strings and describe blocks
- [ ] In `agent-runner.test.ts`: update `toContain("search_tools")` assertions
- [ ] Run `make test-agent` — must pass before task 3

### Task 3: Rename in Go backend

**Files:**
- Modify: `internal/services/skill_prompt_service.go`
- Modify: `internal/database/db.go`
- Modify: `mcp-gateway/internal/mcp/server.go`

- [ ] In `skill_prompt_service.go` (~line 138): change `search_tools` → `list_tools_for_tool_type` in the tools section hint
- [ ] In `db.go` (~line 185): change `search_tools` → `list_tools_for_tool_type` in the system prompt text
- [ ] In `mcp-gateway/internal/mcp/server.go` (~line 401): change `search_tools` → `list_tools_for_tool_type` in the error hint
- [ ] Run `make test` — must pass
- [ ] Run `make test-mcp` — must pass before task 4

### Task 4: Update Go tests

**Files:**
- Modify: `internal/services/skill_service_test.go`
- Modify: `internal/services/skill_prompt_service_test.go`

- [ ] In `skill_service_test.go` (~line 568-569): change `"search_tools"` → `"list_tools_for_tool_type"` in assertion string and error message
- [ ] In `skill_prompt_service_test.go` (~line 608): update comment referencing `search_tools`
- [ ] Run `make test` — must pass before task 5

### Task 5: Update documentation

**Files:**
- Modify: `CLAUDE.md`

- [ ] Update the Gateway Tools table: `search_tools` → `list_tools_for_tool_type`
- [ ] Update the execute_script description mentioning injected `search_tools()`
- [ ] Update any other references in CLAUDE.md

### Task 6: Verify acceptance criteria

- [ ] Run full test suite: `make test-all`
- [ ] Run linter: `golangci-lint run`
- [ ] Grep for any remaining `search_tools` references (excluding `docs/plans/completed/` which is historical): `grep -r "search_tools" --include="*.go" --include="*.ts" --include="*.md" . | grep -v "docs/plans/completed/" | grep -v node_modules`
- [ ] Verify no stale references remain
- [ ] Move this plan to `docs/plans/completed/`
