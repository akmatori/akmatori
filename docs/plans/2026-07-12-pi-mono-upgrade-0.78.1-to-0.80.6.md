# pi-mono SDK Upgrade 0.78.1 → 0.80.6 (Core-Only pi-ai Root + Trust + New Models)

## Overview

Upgrade `@earendil-works/pi-coding-agent`, `pi-ai`, and `pi-agent-core` from `^0.78.1` to `^0.80.6` and bump `pi-subagents` from `^0.28.0` to `^0.34.0`. Handles pi-ai 0.80.0's core-only root entrypoint, fixes a latent models.json `apiKey` env-reference bug exposed by pi's config-resolver changes, documents pi 0.79.0 project-trust semantics for child `pi` processes, and adopts new capabilities: the `max` thinking level, Claude Fable 5 / Claude Sonnet 5 in the model picker, and pi-subagents compact tool descriptions.

## Version Table

| Package | Before | After |
|---|---|---|
| `@earendil-works/pi-coding-agent` | `^0.78.1` | `^0.80.6` |
| `@earendil-works/pi-ai` | `^0.78.1` | `^0.80.6` |
| `@earendil-works/pi-agent-core` | `^0.78.1` | `^0.80.6` |
| `pi-subagents` | `^0.28.0` | `^0.34.0` |
| `typebox` | `^1.1.24` (range unchanged) | lockfile → 1.1.38 |
| `undici` | `^8.0.0` (range unchanged) | lockfile refresh only |

## Key Findings from Inspection

### F1 — compat `complete()` is a drop-in; all types stay at root
pi-ai 0.80.0 made the root entrypoint core-only. `complete` moved to `@earendil-works/pi-ai/compat` (a strict superset of root; upstream-sanctioned temporary path) with the same `(model, context, options)` signature — `apiKey`, `temperature`, `maxTokens`, `timeoutMs`, `signal` all accepted. `Model`, `ThinkingLevel`, `Context`, `Message`, `AssistantMessage` types remain root exports, so type imports were unchanged.

### F2 — `getModel` → `getBuiltinModel`
`compat.getModel` is a deprecated alias for `getBuiltinModel` from `@earendil-works/pi-ai/providers/all` — a pure catalog lookup returning `undefined` on miss, matching resolveModel's existing handling. Migrated directly to the non-deprecated symbol.

### F3 — Models API not viable for synthesized custom specs
`builtinModels().complete()` throws `Unknown provider` for models whose provider isn't registered — exactly akmatori's synthesized specs for `custom`/unknown providers. compat dispatches on `model.api` via the api-registry, which is what akmatori needs. coding-agent 0.80.6 itself still runs on compat, so compat outlives at least this cycle. Follow-up: when upstream removes `/compat`, port `runOneshotLLM` to per-api `streamSimple` dispatch or a Models collection with a registered provider factory.

### F4 — models.json `apiKey` bare env-var name is a literal (latent bug fixed)
pi's `resolveConfigValue` requires explicit `$NAME`/`${NAME}` syntax for env references; a bare uppercase name is treated as a LITERAL (file-based configs lost env-name-first resolution at 0.77.0; the last legacy shim, which only covered extension `registerProvider()`, was removed at 0.79.4). `writeCustomProviderModelsJson` wrote `apiKey: "AKMATORI_CUSTOM_PROVIDER_API_KEY"`, so child `pi` processes on custom providers would authenticate with that literal string. Fixed to write `"$AKMATORI_CUSTOM_PROVIDER_API_KEY"`. The slot is marker-managed and rewritten each run, so existing bad files self-heal on the next session.

### F5 — Project trust (0.79.0): child `pi` ignores the workspace settings pin
`settings.json` is in pi's trust-requiring project resources, and akmatori writes `<workDir>/.pi/settings.json` — so every workspace is trust-requiring at 0.80.6. The headless child `pi` spawned by pi-subagents (no UI, no `--approve`, `defaultProjectTrust: "ask"`) resolves workspaces as UNTRUSTED and ignores the project-scope file. Not a functional break: the global `<agentDir>/settings.json` pin (user scope, never trust-gated) still controls children, and the same gate prevents anything written into workDir from shadowing it. Security improvement: a prompt-injected `workDir/.pi/extensions` write can no longer execute in children. Decision: keep writing both files (defense in depth for operators who explicitly trust workspaces); do NOT set `defaultProjectTrust: "always"`.

### F6 — coding-agent surface diffs are additive
`createAgentSession` options, all consumed `AgentSessionEvent` types, `SessionManager.create/continueRecent/newSession({id})/getSessionFile`, `SettingsManager.inMemory` + `retry.provider.*`, and every `DefaultResourceLoader` option survive unchanged. New events (`agent_settled`, `entry_appended`) are additive. `Model.cost` was refactored to `ModelCost` (adds optional `tiers`) — synthesized specs remain valid; no new required fields. `Provider` type renamed `ProviderId` — not imported by akmatori.

### F7 — pi-subagents 0.34.0 compatibility
The 0.80.6 extension loader aliases pi-ai root imports to the bundled compat entry for jiti/Bun, and pi-subagents' pi-ai imports are type-only anyway. Package manifest (`pi.extensions`) and builtin `agents/` dir layout are unchanged, so the Dockerfile bake at `/opt/pi-extensions/pi-subagents` keeps working. Notable inherited fixes: parent-session model inheritance for children (0.29 — load-bearing for akmatori's "omit `model:`" subagent design), failed-subagent output surfacing (0.29), enforced `timeoutMs`/`maxRuntimeMs` (0.31+). New extension config read from `<agentDir>/extensions/subagent/config.json` (STRICT JSON; parse failure falls back silently to defaults).

### F8 — Free wins arriving with the bump
Retry classification for NVIDIA NIM gRPC ResourceExhausted / Cloudflare 524 / socket-drop / "please retry" errors; provider HTTP errors include response bodies; Anthropic thinking-block preservation fix for newer Claude models; `streamSimple()` context-aware max-token cap; MiniMax/Z.AI/Moonshot compat payload fixes; compaction fixes; Claude Sonnet 5 / Fable 5 / GPT-5.6 catalogs; `max` thinking level.

## Changes Made

### agent-worker/package.json
- Bumped pi-* packages to `^0.80.6`, pi-subagents to `^0.34.0`; lockfile refresh (typebox 1.1.38, undici deduped under existing `^8.0.0`).

### agent-worker/src/agent-runner.ts
- Import split: `getBuiltinModel` from `@earendil-works/pi-ai/providers/all`; `Model`/`ThinkingLevel` stay type-only root imports. Both call sites (`resolveModel`, `isBuiltInModelKnown`) migrated.
- `writeCustomProviderModelsJson`: apiKey now written as `` `$${AKMATORI_CUSTOM_API_KEY_ENV}` `` (F4); comments updated.
- `mapThinkingLevel`: added `"max"` to the valid list.
- `writeSubagentDefaultsSettings` doc comment: documented 0.80.6 project-trust semantics (F5).

### agent-worker/src/oneshot-llm.ts
- `complete` imported from `@earendil-works/pi-ai/compat`; types stay at root. Follow-up note for the eventual compat removal recorded inline.

### agent-worker tests
- `oneshot-llm.test.ts` / `agent-runner.test.ts` / `orchestrator.test.ts`: pi-ai mocks split into `@earendil-works/pi-ai/compat` (`complete`) and `@earendil-works/pi-ai/providers/all` (`getBuiltinModel`); models.json assertions updated to `$AKMATORI_CUSTOM_PROVIDER_API_KEY` (4 sites). `extension-loader.test.ts` unchanged (integration-tests the real 0.80.6 loader).

### "max" thinking level
- `agent-worker/src/types.ts` (union), `agent-worker/src/orchestrator.ts` (`mapThinkingLevel` case), `internal/database/models_settings.go` (`ThinkingLevelMax` + `ValidThinkingLevels`), `internal/handlers/api_settings_llm.go` (both error strings), `web/src/types/index.ts` (union), `web/src/components/settings/LLMSettingsSection.tsx` (`Max` option).

### web/src/components/settings/llmModelSuggestions.ts
- Anthropic: `claude-fable-5` (Most capable) and `claude-sonnet-5` (Recommended) at top; dropped `claude-opus-4-6`/`claude-sonnet-4-5`.
- OpenRouter: `anthropic/claude-fable-5` (Most capable) + `anthropic/claude-sonnet-5` at top; dropped `anthropic/claude-opus-4.7`.

### akmatori_data/extensions/subagent/ (new, git-tracked via .gitignore exceptions)
- `config.json`: `{ "toolDescriptionMode": "compact" }` — trims the parent-facing `subagent` tool description (prompt-token savings per incident). `README.md` documents strict-JSON requirement and rollback.

### CLAUDE.md
- Version bumps + new SDK notes (core-only pi-ai root, `$ENV_VAR` apiKey syntax, project-trust semantics, `max` level mirror sites, subagent config location); trimmed stale/verbose notes to stay under the 30000-byte cap.

## Verification Checklist

- [ ] `npm install` — single deduped typebox/undici/pi-ai
- [ ] `npm run build` — TypeScript build passes
- [ ] `make test-agent`
- [ ] `make test` (Go), `make test-web`
- [ ] `make verify`
- [ ] Container rebuild (akmatori-agent, akmatori-api, frontend); `pi --version` in image = 0.80.6
- [ ] Staging smoke: subagent run resolves UI-selected model (no trust prompt / model fallback); custom-provider child auth via `$`-ref apiKey; oneshot title generation; `max` thinking level accepted end-to-end
