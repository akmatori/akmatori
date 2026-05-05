# pi-mono SDK Upgrade 0.72.1 → 0.73.0 + Frontier Model Suggestions

## Overview

Bump `@mariozechner/pi-coding-agent`, `@mariozechner/pi-ai`, and `@mariozechner/pi-agent-core` from `^0.72.1` to `^0.73.0`, and refresh the LLM model suggestion list in the web UI to include the latest frontier models surfaced by pi-mono 0.73.0 (Claude Opus 4.7, GPT-5.5 / 5.5 Pro, Gemini 3 Pro / 3.1 Pro Preview).

The 0.72.1 → 0.73.0 release has no breaking changes that affect Akmatori:
- Xiaomi MiMo provider split (API billing vs. regional Token Plan) — Akmatori does not use Xiaomi
- `registerSessionResourceCleanup()` / `cleanupSessionResources()` — opt-in API; Akmatori registers no session-scoped resources
- Bedrock Opus 4.7 `xhigh` thinking fix — automatic
- OpenAI Codex WebSocket → SSE fallback + WS session shutdown — automatic
- Compact `read` tool rendering for SKILL.md / docs — interactive-only, not used by the worker
- Incremental bash output streaming — already surfaced through `tool_execution_update` events that `agent-runner.ts` already handles, so output appears live with no code change

So the SDK upgrade itself is a version bump only. The substantive work is the model-suggestion refresh.

## Context

- Files involved:
  - `agent-worker/package.json` (bump three SDK versions)
  - `agent-worker/package-lock.json` (regenerated)
  - `web/src/components/settings/LLMSettingsSection.tsx` (update `MODEL_SUGGESTIONS`)
  - `CLAUDE.md` (update version reference and SDK note)
- Related patterns: prior upgrades at `docs/plans/completed/2026-05-03-pi-mono-upgrade-0.67.6-to-0.72.1.md` and `docs/plans/completed/2026-04-17-pi-mono-upgrade-0.63.1-to-0.67.6.md`
- Dependencies: pi-mono 0.73.0 source is at `/opt/pi-mono` (already checked out at v0.73.0). Per project memory, inspect SDK API shapes locally rather than pulling from npm.
- New model IDs verified in `/opt/pi-mono/packages/ai/src/models.generated.ts`:
  - Anthropic: `claude-opus-4-7`
  - OpenAI: `gpt-5.5`, `gpt-5.5-pro`
  - Google: `gemini-3-pro-preview`, `gemini-3.1-pro-preview`, `gemini-3-flash-preview`
  - OpenRouter aliases: `anthropic/claude-opus-4-7`, `openai/gpt-5.5`, `google/gemini-3-pro-preview`

## Development Approach

- Testing approach: Regular (code first, then tests). Each task ends with the relevant test command; do not move on until tests pass.
- Use `cd /opt/akmatori/agent-worker && npm run build` after dependency changes to catch type errors.
- Use `git -C /opt/pi-mono show v0.73.0:packages/<pkg>/...` to verify SDK shapes — do not pull from npm.
- CRITICAL: every task MUST include new/updated tests where behavior changes.
- CRITICAL: all tests must pass before starting next task.

## Implementation Steps

### Task 1: Bump pi-mono SDK to 0.73.0

**Files:**
- Modify: `agent-worker/package.json`
- Regenerate: `agent-worker/package-lock.json`

- [x] Update `agent-worker/package.json` dependencies:
  - `@mariozechner/pi-agent-core`: `^0.72.1` → `^0.73.0`
  - `@mariozechner/pi-ai`: `^0.72.1` → `^0.73.0`
  - `@mariozechner/pi-coding-agent`: `^0.72.1` → `^0.73.0`
  - Leave `typebox` at `^1.1.24` (no change in 0.73.0)
- [x] Run `cd /opt/akmatori/agent-worker && npm install`
- [x] Verify resolved versions with `npm ls @mariozechner/pi-coding-agent @mariozechner/pi-ai @mariozechner/pi-agent-core`
- [x] Run `npm run build` — must succeed with no TypeScript errors
- [x] Run `make test-agent` — all existing tests must pass without modification (no API surface changes affect Akmatori)

### Task 2: Add frontier-model suggestions in LLM settings UI

**Files:**
- Modify: `web/src/components/settings/LLMSettingsSection.tsx`

- [ ] In `MODEL_SUGGESTIONS` (line 9), prepend new entries while keeping existing ones for backward compatibility:
  - openai: add `{ value: 'gpt-5.5', label: 'gpt-5.5 (Recommended)' }` and `{ value: 'gpt-5.5-pro', label: 'gpt-5.5-pro (Most capable)' }`; downgrade existing `gpt-5.4` label to remove "Recommended"
  - anthropic: add `{ value: 'claude-opus-4-7', label: 'claude-opus-4-7 (Most capable)' }`; demote `claude-opus-4-6` label
  - google: add `{ value: 'gemini-3-pro-preview', label: 'gemini-3-pro-preview (Recommended)' }`, `{ value: 'gemini-3.1-pro-preview', label: 'gemini-3.1-pro-preview (Preview)' }`, `{ value: 'gemini-3-flash-preview', label: 'gemini-3-flash-preview (Fast)' }`; demote `gemini-2.5-pro` label
  - openrouter: add `{ value: 'anthropic/claude-opus-4-7', ... }`, `{ value: 'openai/gpt-5.5', ... }`, `{ value: 'google/gemini-3-pro-preview', ... }`
- [ ] Add/extend a unit test (or component test if one exists) verifying the suggestion list contains the new IDs for each provider; use the project's existing UI testing pattern. If no existing tests cover `MODEL_SUGGESTIONS`, add a minimal test in `web/src/components/settings/` that imports the constant via `export` (export it if needed) and asserts the new IDs are present.
- [ ] Run web test suite (e.g. `cd /opt/akmatori/web && npm test` or the project equivalent) — must pass.

### Task 3: Rebuild Docker containers and verify runtime

- [ ] Run `make verify` (Go vet + all tests).
- [ ] Run `docker-compose build akmatori-agent && docker-compose up -d akmatori-agent`.
- [ ] Run `docker-compose build frontend && docker-compose up -d frontend`.
- [ ] Run `docker-compose logs --tail=100 akmatori-agent` and confirm no startup errors, no module resolution failures.
- [ ] Confirm via `docker-compose exec akmatori-agent node -e "console.log(require('./node_modules/@mariozechner/pi-coding-agent/package.json').version)"` that the container reports `0.73.0`.
- [ ] Manually verify in the web UI's LLM settings page that the new model suggestions appear for each provider dropdown.

### Task 4: Update documentation

**Files:**
- Modify: `CLAUDE.md`

- [ ] Update CLAUDE.md SDK version reference from `v0.72.1` to `v0.73.0` (search for "0.72.1" in the Agent Worker Architecture section).
- [ ] Add a one-line note to the "SDK Features" or "Recent Agent Behavior Notes" section that 0.73.0 brings incremental bash output streaming and Bedrock Opus 4.7 thinking fix — both automatic.
- [ ] Move this plan from `docs/plans/` to `docs/plans/completed/` after merge.
