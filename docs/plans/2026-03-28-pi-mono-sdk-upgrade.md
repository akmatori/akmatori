---

# Pi-Mono SDK Upgrade: 0.58.1 to 0.63.1

## Overview

Upgrade the agent-worker's pi-mono SDK dependencies from 0.58.1 to 0.63.1 (latest), handle breaking changes, and adopt new features that benefit Akmatori's AIOps use case.

## Context

- Files involved: `agent-worker/package.json`, `agent-worker/src/agent-runner.ts`, `agent-worker/src/gateway-tools.ts`, `agent-worker/src/types.ts`, `agent-worker/src/orchestrator.ts`
- Current installed version: 0.58.1 (package.json specifies ^0.58.0)
- Latest version: 0.63.1 (released 2026-03-27)
- 5 minor versions behind with breaking changes in 0.59.0, 0.60.0, 0.61.0, 0.62.0, 0.63.0

## Version Gap Summary

### Breaking Changes We Must Address

1. **0.59.0**: Custom tools without `promptSnippet` are omitted from system prompt. Our gateway tools use `promptGuidelines` (array), not `promptSnippet`. Need to verify our tools still appear in the system prompt or add `promptSnippet` if needed.
2. **0.62.0**: `ToolDefinition.renderCall/renderResult` must return Component when defined. We don't define these, so no impact. `ResourceLoader.getPathMetadata()` removed - verify we don't use it. `extensionPath` removed from RegisteredCommand/RegisteredTool - no impact (we don't use extensions).
3. **0.63.0**: `ModelRegistry.getApiKey()` replaced by `getApiKeyAndHeaders()`. We create a ModelRegistry but don't call getApiKey() directly - verify no impact. Removed deprecated minimax model IDs - no impact for us.

### New Features Worth Adopting

1. **ctx.signal forwarding (unreleased/0.63.1)**: Cancellation signal propagated to nested model calls. Our tools already accept AbortSignal - this improves cancellation reliability for free.
2. **Edit tool multi-edit support (0.63.0)**: Agent can edit multiple disjoint regions in a single file operation. Available automatically after upgrade.
3. **beforeProviderRequest hook (pi-ai 0.63+)**: Allows inspecting/replacing provider payloads before they're sent. Could be used for request logging or custom header injection.
4. **Lazy-loaded provider SDKs (0.59.0)**: Faster startup by lazy-loading provider modules. Available automatically after upgrade.
5. **Session forking via --fork (0.60.0)**: Fork sessions for parallel investigation paths. Not directly applicable to our WebSocket-driven orchestration yet, but the underlying API could enable branching investigations.
6. **Auto-retry improvements (0.61.0)**: Better retry with tool-using responses. Available automatically after upgrade.
7. **JSONL session export/import (0.61.0)**: Could enable incident investigation history export for post-mortems.
8. **Built-in tools as extensible ToolDefinitions (0.62.0)**: Tools now support custom renderers. Not relevant for our headless use, but the unified tool metadata in `buildSystemPrompt()` could improve prompt construction.
9. **sessionDir setting (0.63.0)**: Configurable session directory location. Could simplify our workspace layout.
10. **New models**: Claude Opus/Sonnet 4.6 1M context, gpt-5.4-mini, gemini-3.1-pro - available automatically via model registry.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Upgrade Dependencies and Verify Compilation

**Files:**
- Modify: `agent-worker/package.json`
- Modify: `agent-worker/package-lock.json` (auto-generated)

- [x] Update `@mariozechner/pi-coding-agent` from `^0.58.0` to `^0.63.1`
- [x] Update `@mariozechner/pi-agent-core` from `^0.58.0` to `^0.63.1`
- [x] Update `@mariozechner/pi-ai` from `^0.58.0` to `^0.63.1`
- [x] Run `npm install` and resolve any dependency conflicts
- [x] Run `npm run build` (tsc) and fix any type errors
- [x] Run `make test-agent` to verify existing tests pass

### Task 2: Address Breaking Change - promptSnippet for Custom Tools

**Files:**
- Modify: `agent-worker/src/gateway-tools.ts`

Since 0.59.0, custom tools without `promptSnippet` are omitted from the system prompt. Our tools use `promptGuidelines` (array format) which was introduced in 0.55.4. Verify whether `promptGuidelines` still works as a system prompt inclusion mechanism in 0.63.1, or if `promptSnippet` (string) is now required.

- [ ] After upgrading, run a test to confirm gateway tools appear in the agent's system prompt
- [ ] If tools are missing from prompt: add `promptSnippet` property to each gateway tool definition (consolidate the `promptGuidelines` array into a single string)
- [ ] If `promptGuidelines` still works: no changes needed, document this in a code comment
- [ ] Run `make test-agent` to verify tests pass

### Task 3: Address Breaking Change - ModelRegistry.getApiKeyAndHeaders()

**Files:**
- Modify: `agent-worker/src/agent-runner.ts` (if needed)

- [ ] Verify that our `ModelRegistry` usage (constructor only, no direct getApiKey calls) compiles without changes
- [ ] If `ModelRegistry` constructor or `AuthStorage.setRuntimeApiKey()` API changed, update accordingly
- [ ] Check if `getApiKeyAndHeaders()` returns headers that should be forwarded to our proxy configuration
- [ ] Run `make test-agent` to verify tests pass

### Task 4: Adopt ctx.signal Cancellation Improvements

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`
- Modify: `agent-worker/src/gateway-tools.ts`

- [ ] Verify that the AbortSignal parameter in our custom tool execute() signatures matches the updated SDK type
- [ ] Ensure our `session.abort()` call in `cancel()` properly triggers signal propagation to active tool calls
- [ ] Add a test that verifies cancellation propagates to gateway tool calls (mock the gateway client, start execution, cancel, verify signal was aborted)
- [ ] Run `make test-agent` to verify tests pass

### Task 5: Adopt Session Export for Investigation History

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`
- Modify: `agent-worker/src/types.ts`

The JSONL session export (0.61.0) enables exporting investigation history for post-mortems.

- [ ] Add an `exportSession(incidentId: string, workDir: string): Promise<string>` method to AgentRunner that uses SessionManager to export the session as JSONL
- [ ] Add `session_export` field to `ExecuteResult` type (optional string containing the JSONL path)
- [ ] After each completed session, automatically export to `{workDir}/session_export.jsonl`
- [ ] Write tests for the export functionality
- [ ] Run `make test-agent` to verify tests pass

### Task 6: Leverage sessionDir Setting for Workspace Simplification

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`

- [ ] Investigate if `sessionDir` setting in SettingsManager can replace our manual `SessionManager.create(workDir)` pattern
- [ ] If beneficial, configure sessionDir via SettingsManager.inMemory() to point sessions to a dedicated subdirectory (e.g., `{workDir}/.sessions/`)
- [ ] This separates session data from agent workspace files, making cleanup easier
- [ ] Write tests to verify session files go to the correct directory
- [ ] Run `make test-agent` to verify tests pass

### Task 7: Update BASH_TOOL_GUIDELINES for New Built-in Tool Metadata

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`

Since 0.62.0, built-in tools carry their own ToolDefinition metadata used by `buildSystemPrompt()`. Our `BASH_TOOL_GUIDELINES` override (set via `(bashTool as any).promptGuidelines`) may conflict with the new metadata system.

- [ ] Verify that our `promptGuidelines` override on the bash tool still works correctly with the new ToolDefinition system
- [ ] If the bash tool now uses a proper ToolDefinition type, use the typed API instead of `(bashTool as any).promptGuidelines`
- [ ] Update any type assertions to use proper SDK types
- [ ] Run `make test-agent` to verify tests pass

### Task 8: Verify and Update Event Handling for New Event Types

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`

New event types may have been added since 0.58.0 (e.g., improved compaction events, retry events). Ensure our handleEvent switch covers all events.

- [ ] Check the AgentSessionEvent type union for any new event types added in 0.59.0-0.63.1
- [ ] Add handlers for any new event types that provide useful output for incident investigation logs
- [ ] Ensure the default case gracefully handles unknown events (already does)
- [ ] Write tests for any new event handlers
- [ ] Run `make test-agent` to verify tests pass

### Task 9: Verify Acceptance Criteria

- [ ] Run full test suite: `make test-all`
- [ ] Run linter: `go vet ./...` and `cd agent-worker && npm run build`
- [ ] Verify all existing agent-worker tests pass with the upgraded SDK
- [ ] Verify Docker build succeeds: `docker-compose build akmatori-agent`

### Task 10: Update Documentation

- [ ] Update CLAUDE.md to reflect the new pi-mono version (0.63.1) in relevant sections
- [ ] Note any new SDK features available in the agent worker architecture section
- [ ] Move this plan to `docs/plans/completed/`
