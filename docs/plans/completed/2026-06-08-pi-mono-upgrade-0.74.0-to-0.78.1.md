# pi-mono SDK Upgrade 0.74.0 → 0.78.1 (New Providers + Compat Flags)

## Overview

Upgrade `@earendil-works/pi-coding-agent`, `pi-ai`, and `pi-agent-core` from `^0.74.0` to `^0.78.1` and bump `pi-subagents` from `^0.24.2` to `^0.28.0`. Also adopts new capabilities: Claude Opus 4.8 in the model picker, three first-class providers (NVIDIA NIM, MiniMax-M3, Ant Ling), and `compat.forceAdaptiveThinking` for on-prem Anthropic-compatible models.

## Version Table

| Package | Before | After |
|---|---|---|
| `@earendil-works/pi-coding-agent` | `^0.74.0` | `^0.78.1` |
| `@earendil-works/pi-ai` | `^0.74.0` | `^0.78.1` |
| `@earendil-works/pi-agent-core` | `^0.74.0` | `^0.78.1` |
| `pi-subagents` | `^0.24.2` | `^0.28.0` |
| `undici` | `^7.22.0` | `^8.0.0` (peer dep for pi-coding-agent 0.78.1) |

## Key Findings from Inspection

### B1 — httpIdleTimeoutMs
`httpIdleTimeoutMs` is NOT a new field in pi-ai 0.78.1's retry settings type. `DEFAULT_PROVIDER_RETRY` in `agent-runner.ts` needed no changes — it already has `timeoutMs`, `maxRetries`, and `maxRetryDelayMs`.

### B2 — setRuntimeApiKey and `$` characters
`setRuntimeApiKey` stores values directly in `runtimeOverrides` and bypasses `resolveConfigValue`. Operator API keys containing literal `$` characters are safe — they are never interpreted as env var names at the runtime-key path.

### B3 — session.abort() double-dispose
`session.abort()` uses optional chaining (`activeRun?.abortController.abort()`). Double-dispose is safe and cannot throw. The existing compare-and-delete pattern in `activeSessions` needed no adjustment.

### B4/B5 — TypeScript build
Build succeeded on first attempt after bumping undici. No SDK API signatures changed in ways that required source edits beyond the undici peer dep bump.

### Undici peer dep conflict
`pi-coding-agent 0.78.1` requires undici 8.x. The prior pin `^7.22.0` caused a nested-dep version split. Bumped to `^8.0.0` in `package.json` and regenerated lockfile. Undici is used only for gateway HTTP calls; no API surface changed for the subset Akmatori uses.

## Changes Made

### agent-worker/package.json
- Bumped pi-* packages to `^0.78.1`
- Bumped pi-subagents to `^0.28.0`
- Bumped undici to `^8.0.0`

### agent-worker/src/agent-runner.ts
- Added NVIDIA NIM (`nvidia`, apiType `openai-completions`), MiniMax (`minimax`, apiType `anthropic-messages`), and Ant Ling (`ant-ling`, apiType `openai-completions`) to `apiMap` in `resolveModel`
- Added env var entries for the three new providers to `PROVIDER_ENV_KEY`
- Added `compat.forceAdaptiveThinking: true` to synthesized model specs for any provider resolving to `apiType === "anthropic-messages"` (covers minimax + unknown anthropic model IDs)

### internal/database/models_settings.go
- Added `LLMProviderNvidiaNIM = "nvidia"`, `LLMProviderMiniMax = "minimax"`, `LLMProviderAntLing = "ant-ling"` constants
- Extended `ValidLLMProviders()` and `ProviderDisplayName()` with the three new entries

### internal/database/db.go
- Added default models: nvidia → `meta/llama-3.3-70b-instruct`, minimax → `MiniMax-M3`, ant-ling → `Ling-2.6-flash`

### web/src/types/index.ts
- Extended `LLMProvider` union with `"nvidia" | "minimax" | "ant-ling"`

### agent-worker/src/types.ts
- Extended `LLMProvider` union with `"nvidia" | "minimax" | "ant-ling"`

### web/src/components/settings/LLMSettingsSection.tsx
- Added NVIDIA NIM, MiniMax, Ant Ling to `PROVIDER_OPTIONS` with API key placeholders

### web/src/components/settings/llmModelSuggestions.ts
- Added model suggestion arrays for nvidia, minimax, ant-ling
- Added `claude-opus-4-8 (Most capable)` as the top Anthropic entry; demoted claude-opus-4-7
- Added `anthropic/claude-opus-4.8 (Most capable)` as the top OpenRouter entry; demoted anthropic/claude-opus-4.7

### CLAUDE.md
- Updated `pi-coding-agent` version reference from `v0.74.0` to `v0.78.1`
- Added SDK Notes entries for: current version pins, setRuntimeApiKey `$` safety, and `compat.forceAdaptiveThinking` usage

## Verification Checklist

- [x] `npm run build` — TypeScript build passes with no errors
- [x] `make test-agent` — 369 agent worker tests pass
- [x] `make test` — all Go backend tests pass
- [x] `make test-web` — 100 frontend tests pass
- [x] `make verify` — full pre-commit gate passes
