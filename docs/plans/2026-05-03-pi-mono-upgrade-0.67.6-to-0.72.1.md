# pi-mono SDK Upgrade 0.67.6 → 0.72.1 Implementation Plan

## Overview

Upgrade `@mariozechner/pi-coding-agent`, `@mariozechner/pi-ai`, and `@mariozechner/pi-agent-core` from `^0.67.6` to `^0.72.1`. The single mandatory code change is the TypeBox 1.x migration (0.69.0 breaking change): replace `@sinclair/typebox` with `typebox` in `agent-worker/`. Additionally adopt the new `retry.provider.{timeoutMs,maxRetries,maxRetryDelayMs}` controls (0.70.1) so slow on-prem/OpenRouter models do not abort during long alert investigations, and pick up automatic improvements (Cloudflare AI Gateway provider, long local-LLM stream timeout fix, Anthropic stream hardening, more retryable error classes).

The upgrade touches only `agent-worker/`. Go API server, MCP Gateway, frontend, and database are unaffected. Akmatori does not use any of the other 0.68–0.72 breaking changes:
- 0.68.0 tool-name allowlist change → not used (we pass `customTools`, not `tools`)
- 0.68.0 cwd-bound tool removal → already use `createBashToolDefinition(workDir)` factory
- 0.68.0 explicit `cwd` requirement → already pass `cwd: params.workDir`
- 0.69.0 session-replacement context invalidation → `noExtensions: true`, no session forking
- 0.71.0 Gemini CLI/Antigravity provider removal → `google` provider in Akmatori means `google-generative-ai`, not Gemini CLI
- 0.72.0 `compat.reasoningEffortMap` → `thinkingLevelMap` → not used (no custom-provider registrations)

## Context

- Files involved:
  - `agent-worker/package.json` (bump SDK versions, swap `@sinclair/typebox` → `typebox`)
  - `agent-worker/package-lock.json` (regenerated)
  - `agent-worker/src/gateway-tools.ts` (TypeBox 1.x import path)
  - `agent-worker/src/agent-runner.ts` (optional: wire `retry.provider.*` settings)
  - `CLAUDE.md` (update SDK version reference and add upgrade notes)
- Related patterns: prior `docs/plans/completed/2026-04-17-pi-mono-upgrade-0.63.1-to-0.67.6.md` (same shape, smaller surface)
- Dependencies: pi-mono 0.72.1 source is at `/opt/pi-mono` (already checked out). Do not pull from npm to inspect — use the local repo per project memory.

## Development Approach

- Testing approach: Regular (code first, then tests). Each task ends by running `make test-agent` and `make verify`; do not move on until tests pass.
- Use `cd /opt/akmatori/agent-worker && npm run build` after each TypeScript change to catch type errors early.
- Use `git -C /opt/pi-mono show v0.72.1:packages/<pkg>/...` to verify SDK API shapes (do not download from npm).
- CRITICAL: every task MUST include new/updated tests where behavior changes.
- CRITICAL: all tests must pass before starting next task.

## Implementation Steps

### Task 1: Bump pi-mono SDK and swap typebox

**Files:**
- Modify: `agent-worker/package.json`
- Regenerate: `agent-worker/package-lock.json`

- [x] Update `agent-worker/package.json` dependencies:
  - `@mariozechner/pi-agent-core`: `^0.67.6` → `^0.72.1`
  - `@mariozechner/pi-ai`: `^0.67.6` → `^0.72.1`
  - `@mariozechner/pi-coding-agent`: `^0.67.6` → `^0.72.1`
  - Remove `@sinclair/typebox: ^0.34.48`
  - Add `typebox: ^1.1.24` (matches pi-mono 0.72.1's transitive version)
- [x] Run `cd /opt/akmatori/agent-worker && npm install`
- [x] Verify resolved versions with `npm ls @mariozechner/pi-coding-agent @mariozechner/pi-ai @mariozechner/pi-agent-core typebox`
- [x] Run `npm run build` (expect TypeScript errors in gateway-tools.ts — fixed in Task 2)

### Task 2: Migrate gateway-tools.ts to typebox 1.x

**Files:**
- Modify: `agent-worker/src/gateway-tools.ts`

- [x] Replace `import { Type, type Static } from "@sinclair/typebox";` with `import { Type, type Static } from "typebox";`
- [x] Verify all `Type.Object`, `Type.String`, `Type.Optional`, `Type.Record`, `Type.Unknown` calls compile under typebox 1.x. The schema-builder API is unchanged in 1.x; the migration is import-path only.
- [x] Run `npm run build` — must succeed with no TypeScript errors. (Also fixed an unrelated 0.72.1 breaking change: `DefaultResourceLoader` now requires `agentDir` — wired `getAgentDir()` from `@mariozechner/pi-coding-agent` and updated test mocks.)
- [x] Run `make test-agent` — all existing gateway-tools tests must still pass.
- [x] If any existing test exercises `Static` type inference or schema serialization, add one targeted test that validates a `gateway_call` schema round-trips through `JSON.stringify(schema)` since 0.69.0 explicitly tightens this path.

### Task 3: Add provider retry/timeout settings

**Files:**
- Modify: `agent-worker/src/types.ts` (extend internal LLM config with optional retry config)
- Modify: `agent-worker/src/agent-runner.ts` (forward retry config to `createAgentSession`)

- [x] Decide config surface scope: support hardcoded sensible defaults first (timeout 600_000ms = 10min, maxRetries 3) without exposing to DB/API. Only widen surface if user requests.
- [x] Pass `retry: { provider: { timeoutMs, maxRetries, maxRetryDelayMs } }` to `createAgentSession({...})`. Verify exact option name in `/opt/pi-mono/packages/coding-agent/src/core/agent-session.ts` for v0.72.1. (Note: in pi-mono 0.72.1 the SDK reads provider retry settings from `SettingsManager.getProviderRetrySettings()` rather than from a top-level `createAgentSession` option, so we forward via `SettingsManager.inMemory({ retry: { provider: {...} } })`.)
- [x] Add a unit test in `agent-worker/src/agent-runner.test.ts` that constructs the session config and asserts the retry block is forwarded.
- [x] Run `make test-agent` — all tests pass.

### Task 4: Rebuild and verify Docker container

- [x] Run `make verify` (Go vet + all tests).
- [x] Run `docker-compose build akmatori-agent` — must succeed.
- [x] Run `docker-compose up -d akmatori-agent`.
- [x] Run `docker-compose logs --tail=100 akmatori-agent` and confirm no startup errors, no module-resolution failures, no TypeBox warnings.
- [x] Confirm `docker-compose exec akmatori-agent node -e "console.log(require('./node_modules/@mariozechner/pi-coding-agent/package.json').version)"` reports `0.72.1` inside the container.

### Task 5: Update documentation

**Files:**
- Modify: `CLAUDE.md`

- [ ] Update `CLAUDE.md`:
  - Bump SDK version reference from `v0.67.6` to `v0.72.1`
  - Update "SDK API Conventions" section to mention typebox 1.x and the new import path
  - Add a one-line note about provider retry/timeout controls if Task 3 is shipped
- [ ] Move this plan from `docs/plans/` to `docs/plans/completed/` with status notes.
