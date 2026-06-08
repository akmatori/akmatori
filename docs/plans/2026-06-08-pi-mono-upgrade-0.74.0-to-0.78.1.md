# pi-mono SDK Upgrade 0.74.0 → 0.78.1 (+ New Features)

## Overview

Upgrade `@earendil-works/pi-coding-agent`, `pi-ai`, and `pi-agent-core` from `^0.74.0` to `^0.78.1`, bump `pi-subagents` from `^0.24.2` to `^0.28.0`, verify no-break areas in the existing worker code, and adopt the new capabilities: Claude Opus 4.8 in the model picker, three first-class providers (NVIDIA NIM, MiniMax-M3, Ant Ling), `compat.forceAdaptiveThinking` for on-prem Anthropic-compatible models, and automatic full subagent output.

## Context

- Files involved:
  - `agent-worker/package.json` — SDK version bumps
  - `agent-worker/package-lock.json` — regenerated
  - `agent-worker/src/agent-runner.ts` — DEFAULT_PROVIDER_RETRY (httpIdleTimeoutMs), resolveModel (apiMap + compat.forceAdaptiveThinking), PROVIDER_ENV_KEY
  - `web/src/components/settings/llmModelSuggestions.ts` — Opus 4.8, three new providers
  - `web/src/types/index.ts` — LLMProvider union extension
  - `agent-worker/src/types.ts` — LLMProvider union if present
  - `web/src/components/settings/LLMSettingsSection.tsx` — PROVIDER_OPTIONS
  - `internal/database/models_settings.go` — three new LLMProvider constants
  - `internal/database/db.go` — defaultModelsPerProvider entries
  - `CLAUDE.md` — version references and new notes
- Related patterns: `docs/plans/completed/2026-05-14-pi-mono-upgrade-0.73.0-to-0.74.0.md` for plan structure; use `git -C /opt/pi-mono checkout v0.78.1` to inspect SDK shapes
- Dependencies: pi-mono v0.78.1 must be available on npm; pi-subagents 0.28.0 uses `*` peer deps so it accepts pi 0.78.1

## Development Approach

- **Testing approach**: Regular (implement first, then verify tests pass before moving on)
- Use `git -C /opt/pi-mono checkout v0.78.1` and read installed node_modules to inspect SDK type shapes — do not guess
- Part B verification items (httpIdleTimeoutMs, API key $ENV_VAR behavior, session disposal) must be actively inspected before deciding on code changes
- For C2 new providers: confirm exact pi-ai provider identifiers and apiType values from installed node_modules before wiring; fall back to documenting custom+base_url path if keys are fiddly
- **CRITICAL: every task that modifies code must include test items**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Bump SDK versions and regenerate lockfile

**Files:**
- Modify: `agent-worker/package.json`
- Regenerate: `agent-worker/package-lock.json`

- [x] In `agent-worker/package.json` bump `@earendil-works/pi-agent-core`, `@earendil-works/pi-ai`, `@earendil-works/pi-coding-agent` to `^0.78.1` and `pi-subagents` to `^0.28.0`
- [x] Run `cd /opt/akmatori/agent-worker && npm install` to regenerate `package-lock.json`
- [x] Run `npm ls @earendil-works/pi-coding-agent @earendil-works/pi-ai @earendil-works/pi-agent-core pi-subagents` — confirm all resolved versions match targets
- [x] Run `npm ls undici @earendil-works/pi-tui typebox` — record any version conflicts for follow-up in Task 2; if pi now requires undici 8, note it

### Task 2: Build verification and no-break area inspection

**Files:**
- Modify: `agent-worker/src/agent-runner.ts` (if httpIdleTimeoutMs or session disposal API changed)

- [x] Run `cd /opt/akmatori/agent-worker && npm run build` — must succeed; fix any TypeScript errors from changed SDK signatures (B4/B5)
- [x] Inspect `node_modules/@earendil-works/pi-ai/dist/*.d.ts` (or equivalent) for the `retry.provider` settings shape — determine if `httpIdleTimeoutMs` is a new distinct field separate from `timeoutMs` (B1)
- [x] If `httpIdleTimeoutMs` is a new field in the SettingsManager retry type, add it to `DEFAULT_PROVIDER_RETRY` in `agent-runner.ts` alongside `timeoutMs` (both at 600_000 ms)
- [x] Inspect pi-ai 0.77.0 API key resolution: check if `setRuntimeApiKey` treats plain strings as literals or $ENV_VAR patterns; if plain strings can be misinterpreted, add a comment documenting the edge case for operator keys containing `$` (B2)
- [x] Inspect session disposal API (B3): confirm `session.abort()` still exists and double-dispose does not throw; check if `activeSessions` compare-and-delete pattern needs adjustment
- [x] If undici peer conflict was flagged in Task 1: run `npm ls undici`; if SDK requires undici 8, bump `undici` in `package.json` to `^8` and re-run `npm install`
- [x] Run `make test-agent` — all existing tests must pass before Task 3

### Task 3: Claude Opus 4.8 in model picker (C1)

**Files:**
- Modify: `web/src/components/settings/llmModelSuggestions.ts`

- [x] In the `anthropic` list, insert `claude-opus-4-8` as the top entry labeled `claude-opus-4-8 (Most capable)` and demote `claude-opus-4-7` to second position without the `(Most capable)` suffix
- [x] In the `openrouter` list, insert `anthropic/claude-opus-4.8` as the top entry labeled `anthropic/claude-opus-4.8 (Most capable)` and demote `anthropic/claude-opus-4.7`
- [x] Leave `db.go` `defaultModelsPerProvider` Anthropic default at `claude-sonnet-4-6` — do not auto-upgrade existing installs
- [x] Run `make test-web` — must pass

### Task 4: New providers — Go backend (C2)

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/database/db.go`

- [x] Add three `LLMProvider` constants to `models_settings.go`: `LLMProviderNvidiaNIM`, `LLMProviderMiniMax`, `LLMProviderAntLing` (use exact string values matching pi-ai's provider identifiers confirmed from installed node_modules)
- [x] Add the three new providers to `ValidLLMProviders()` and `ProviderDisplayName()` in `models_settings.go`
- [x] Add entries in `defaultModelsPerProvider` in `db.go` for the three new providers (use a sensible default model ID confirmed from pi-ai's built-in catalogue)
- [x] Run `make test` — all Go tests must pass

### Task 5: New providers — TypeScript (C2)

**Files:**
- Modify: `web/src/types/index.ts`
- Modify: `agent-worker/src/types.ts` (if it defines LLMProvider separately)
- Modify: `web/src/components/settings/LLMSettingsSection.tsx`
- Modify: `web/src/components/settings/llmModelSuggestions.ts`
- Modify: `agent-worker/src/agent-runner.ts`

- [x] Extend `LLMProvider` union in `web/src/types/index.ts` with the three new provider string values
- [x] Check `agent-worker/src/types.ts` for a separate LLMProvider definition and extend it if present
- [x] Add the three providers to `PROVIDER_OPTIONS` in `LLMSettingsSection.tsx`
- [x] Add model suggestion arrays for each new provider in `llmModelSuggestions.ts` (at minimum one suggested model each, confirmed from pi-ai catalogue)
- [x] In `agent-runner.ts`: add the three providers to `apiMap` in `resolveModel` with their correct `apiType` strings (confirmed from pi-ai's registered provider config); add env var mappings to `PROVIDER_ENV_KEY` for any providers that have a canonical key env var
- [x] Run `cd /opt/akmatori/agent-worker && npm run build` — no type errors
- [x] Run `make test-agent` — must pass
- [x] Run `make test-web` — must pass

### Task 6: compat.forceAdaptiveThinking for on-prem Anthropic-compatible models (C3)

**Files:**
- Modify: `agent-worker/src/agent-runner.ts`

- [ ] In `resolveModel`, when the provider resolves to `anthropic-messages` apiType AND the provider is `custom` (or a new on-prem Anthropic-compatible provider), add `compat: { forceAdaptiveThinking: true }` to the synthesized Model spec
- [ ] Gate behind `apiType === "anthropic-messages"` heuristic (already set for custom providers) rather than forcing it on all custom providers — this covers custom Anthropic-compatible endpoints without affecting OpenAI-compatible ones
- [ ] Run `cd /opt/akmatori/agent-worker && npm run build` — no type errors
- [ ] Run `make test-agent` — must pass

### Task 7: Full test and verification gate

- [ ] Run `make test-agent` — worker tests including extension-loader and tool-output-formatter
- [ ] Run `make test` — Go backend tests (provider enum, settings handlers)
- [ ] Run `make test-web` — frontend tests
- [ ] Run `make verify` — full pre-commit gate; fix any failures before Task 8

### Task 8: Documentation (Part E)

**Files:**
- Modify: `CLAUDE.md`
- Create: `docs/plans/completed/2026-06-08-pi-mono-upgrade-0.74.0-to-0.78.1.md`

- [ ] In `CLAUDE.md` update the Agent Worker line: `pi-coding-agent` (`v0.74.0`) → (`v0.78.1`) and pi-subagents version reference
- [ ] In the SDK Notes section of `CLAUDE.md`, add: note about `httpIdleTimeoutMs` if added to `DEFAULT_PROVIDER_RETRY`; note that pi-ai 0.77.0+ treats `setRuntimeApiKey` plain strings as literals (operator keys with literal `$` are safe); note `compat.forceAdaptiveThinking` usage for custom Anthropic-compatible endpoints; note pi-subagents 0.28.0 bump
- [ ] Run `wc -c CLAUDE.md` — must stay under 30000 bytes
- [ ] Create `docs/plans/completed/2026-06-08-pi-mono-upgrade-0.74.0-to-0.78.1.md` mirroring the 0.73.0→0.74.0 plan structure (version table of changelog items + verification checklist)
