# Agent Tool Usage Guardrails & Runbook Enforcement

## Overview

Fix agent execution issues observed in incident logs: agents skip runbook lookup, attempt direct tool calls instead of using `gateway_call`, and get confused by `tool_instance_id` in schemas. Five targeted improvements to prompts, error messages, tool discovery, and schemas.

## Context

- Root cause analysis: `~/.claude/plans/whimsical-beaming-harp.md`
- Files involved:
  - `internal/database/db.go` (DefaultIncidentManagerPrompt, lines 170-223)
  - `mcp-gateway/internal/mcp/server.go` (list_tool_types handler, lines 476-507)
  - `agent-worker/src/gateway-tools.ts` (tool registration, 344 lines)
  - `agent-worker/src/agent-runner.ts` (BASH_TOOL_GUIDELINES, lines 42-46)
  - `mcp-gateway/internal/tools/victoriametrics/` (tool schemas with tool_instance_id)
- Related patterns: prompt text is tested in `prompt_test.go`; gateway tools have tests in `agent-worker/`
- Dependencies: none (all changes are internal prompt/schema/error-message improvements)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Prompt changes require updating any snapshot/assertion tests that validate prompt content
- Schema changes require updating MCP gateway tests
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Strengthen Runbook Lookup in DefaultIncidentManagerPrompt

**Files:**
- Modify: `internal/database/db.go` (lines ~170-223)

- [x] Rewrite the Investigation Workflow section to make runbook search MANDATORY as step 2
- [x] Move the QMD usage instructions inline into the workflow step (not in a separate section at the bottom)
- [x] Add explicit language: "MANDATORY - Search runbooks FIRST before using any infrastructure tools"
- [x] Include the gateway_call example directly in the workflow step: `gateway_call("qmd.query", {"searches": [{"type": "lex", "query": "<alert text>"}], "limit": 5})`
- [x] Add: "Skip this step ONLY if QMD search returns an error (not if results are empty)"
- [x] Update or add tests that validate the prompt contains the mandatory runbook search language
- [x] Run `make test` - must pass before task 2

### Task 2: Verify QMD Appears in list_tool_types Output

**Files:**
- Modify: `mcp-gateway/internal/mcp/server.go` (lines ~476-507, if changes needed)
- Possibly modify: test files for list_tool_types

- [x] Trace the `handleListToolTypes` logic to confirm proxy-namespaced tools (like `qmd`) appear in output
- [x] The code at line 494 shows proxy namespaces bypass the allowlist filter - verify this works for QMD specifically
- [x] If QMD does NOT appear: fix the proxy namespace registration or the filter logic
- [x] If QMD DOES appear: add a test that explicitly asserts QMD proxy tools are included in list_tool_types output when registered
- [x] Run `make test-mcp` - must pass before task 3

### Task 3: Better Error Messages for Direct Tool Calls

**Files:**
- Modify: `agent-worker/src/gateway-tools.ts`

- [ ] When the agent calls a tool name that isn't one of the 5 registered tools, the current error is generic ("Tool not found" or gateway error -32600)
- [ ] Add a helper that detects tool-name-like patterns (contains a dot, e.g., `victoria_metrics.instant_query`) in error messages
- [ ] Enhance the error message to suggest: "Tool 'X' is not a direct agent tool. Use gateway_call({tool_name: 'X', args: {...}}) instead."
- [ ] This can be done in the gateway_call error handler or as a catch-all in the GatewayClient error path
- [ ] Write tests that verify the enhanced error message appears for dot-namespaced tool names
- [ ] Run `make test-agent` - must pass before task 4

### Task 4: Remove or Clarify tool_instance_id in Tool Schemas

**Files:**
- Modify: `mcp-gateway/internal/tools/victoriametrics/victoriametrics.go` (lines ~96-110)
- Modify: `mcp-gateway/internal/tools/victoriametrics/registry.go` (toolInstanceIDProperty and all tool registrations)
- Check: other tool implementations (SSH, Zabbix) for same pattern

- [ ] Remove `tool_instance_id` and `logical_name` from individual tool input schemas - routing is handled by `gateway_call`'s `instance` parameter
- [ ] Remove `extractInstanceID()` and `extractLogicalName()` helper functions
- [ ] Update tool handler functions to not accept these routing params (routing is done at gateway_call level)
- [ ] Check SSH and Zabbix tool schemas for the same pattern and remove there too
- [ ] Update cache key generation if it references instance ID from args
- [ ] Update all affected tests
- [ ] Run `make test-mcp` - must pass before task 5

### Task 5: Strengthen BASH_TOOL_GUIDELINES in Agent Runner

**Files:**
- Modify: `agent-worker/src/agent-runner.ts` (lines ~42-46)

- [ ] Add a stronger opening line: "CRITICAL: You only have 5 tools available: gateway_call, list_tools_for_tool_type, get_tool_detail, list_tool_types, execute_script. ALL infrastructure operations go through gateway_call."
- [ ] Add: "If you get 'Tool not found', you are calling it wrong - use gateway_call instead."
- [ ] Keep existing guidelines but reorder so the most critical instruction (use gateway_call) comes first
- [ ] Update any tests that validate the BASH_TOOL_GUIDELINES content
- [ ] Run `make test-agent` - must pass before task 6

### Task 6: Verify Acceptance Criteria

- [ ] Manual test: spawn a test incident and verify agent searches runbooks first before infrastructure tools
- [ ] Manual test: verify `list_tool_types` output includes QMD when QMD proxy is registered
- [ ] Manual test: verify enhanced error message when agent tries direct tool call
- [ ] Run full test suite: `make verify`
- [ ] Run linter: `golangci-lint run`

### Task 7: Update Documentation

- [ ] Update CLAUDE.md if prompt patterns or tool schema conventions changed
- [ ] Move this plan to `docs/plans/completed/`
