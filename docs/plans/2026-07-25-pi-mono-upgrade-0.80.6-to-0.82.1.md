# pi-mono SDK Upgrade 0.80.6 → 0.82.1 (ModelRuntime Migration)

## Summary

Upgrade `@earendil-works/pi-coding-agent`, `pi-ai`, and `pi-agent-core` from `^0.80.6` to `^0.82.1` and bump `pi-subagents` from `^0.34.0` to `^0.36.0`. The one breaking change that touches akmatori is pi 0.80.8's **ModelRuntime migration**: `AuthStorage` and the `authStorage`/`modelRegistry` options to `createAgentSession` were removed and replaced by a single async `modelRuntime`. Everything else in the 0.80.7 → 0.82.1 range is non-breaking for akmatori or arrives as a free win; the 0.82.x releases needed no code changes beyond one security-hardening addition (F7).

| Package | From | To |
|---|---|---|
| `@earendil-works/pi-coding-agent` | `^0.80.6` | `^0.82.1` |
| `@earendil-works/pi-ai` | `^0.80.6` | `^0.82.1` |
| `@earendil-works/pi-agent-core` | `^0.80.6` | `^0.82.1` |
| `pi-subagents` | `^0.34.0` | `^0.36.0` |

## Findings

### F1 — ModelRuntime replaces AuthStorage + ModelRegistry (breaking, the only real work)
pi 0.80.8 dropped `AuthStorage` from the package root and removed the `authStorage` + `modelRegistry` options from `createAgentSession`, replacing both with an async `modelRuntime`. akmatori used all three in `agent-runner.ts` (`AuthStorage.inMemory()` + `setRuntimeApiKey`, `ModelRegistry.inMemory(authStorage)`, and both session options). Migration:

- `await ModelRuntime.create({ modelsPath: null, allowModelNetwork: false })`, then `await modelRuntime.setRuntimeApiKey(provider, key, { allowNetwork: false })`, then pass `modelRuntime` to `createAgentSession` (dropping `authStorage`/`modelRegistry`).
- **Invariants preserved.** `ModelRuntime.create()` always wraps its credential store in a `RuntimeCredentials` overlay; `setRuntimeApiKey` writes the key to an in-memory `Map` only (never `auth.json`) and `read()` returns it verbatim as `{ type: "api_key", key }` — no `resolveConfigValue`, so operator keys containing `$` stay safe. Same two guarantees the old `AuthStorage.setRuntimeApiKey` gave us. Verified against the installed 0.81.1 `runtime-credentials.js`.
- `modelsPath: null` keeps the parent runtime from loading `<agentDir>/models.json`, so the parent's behavior is identical to today: it drives the session with the explicit `model` object from `resolveModel`. `allowModelNetwork: false` keeps `create()` offline.
- **Custom-provider auth still works.** `setRuntimeApiKey` accepts any provider id (including `"custom"`) with no built-in-provider check; the runtime key is stored under that id and resolved by `getAuth(model)` for the synthesized custom `model.provider`. This mirrors the old `AuthStorage` path.

### F2 — `complete` and `getBuiltinModel` unchanged (no work)
`oneshot-llm.ts` imports `complete` from `@earendil-works/pi-ai/compat`; `resolveModel`/`isBuiltInModelKnown` import `getBuiltinModel` from `@earendil-works/pi-ai/providers/all`. Both entrypoints are still present and unchanged through 0.82.1 (verified in the installed `.d.ts`). No changes to `oneshot-llm.ts` or `resolveModel`. The custom-provider synthesis (`resolveModel`), `forceAdaptiveThinking` merge, and `writeCustomProviderModelsJson` all stay as-is.

### F3 — pi-subagents 0.35/0.36 peer deps (no code change)
0.35.x added `@earendil-works/pi-tui` as a peer; 0.36.0 keeps it and moves `typebox` from a peer to a bundled dependency (akmatori still imports `typebox` directly for its own tools, so its direct dep stays). `npm install` auto-resolves `@earendil-works/pi-tui@0.74.0`; no manifest change required and no peer warnings. Package layout (`pi.extensions` manifest, builtin `agents/` dir) is unchanged, so the Dockerfile bake at `/opt/pi-extensions/pi-subagents` keeps working.

### F4 — Docker image needs no version pin
The child `pi` CLI spawned by pi-subagents is `node_modules/.bin/pi` → `@earendil-works/pi-coding-agent/dist/cli.js` (PATH set in the runtime stage). It is installed by `npm ci` from the lockfile, so it tracks the bump automatically — `pi --version` in a fresh install resolves to 0.81.1 with no Dockerfile edit.

### F5 — 0.80.7 `sendSessionIdHeader` breaking change does not apply
0.80.7 removed the `openai-responses` `compat.sendSessionIdHeader` flag from `models.json`. akmatori never sets it (its synthesized specs use `baseUrl`, `apiKey`, `forceAdaptiveThinking`, and the ant-ling/custom compat flags only). No action.

### F7 — `ANTHROPIC_AUTH_TOKEN` widens the bash-boundary exfiltration surface (0.82.1, hardening applied)
0.82.1 added `ANTHROPIC_AUTH_TOKEN` bearer authentication for Anthropic-compatible gateways — directly relevant to akmatori's on-prem story, since an operator fronting Anthropic with a gateway may now set it on the agent container. akmatori never sets this var itself (`propagateApiKeyToEnv` only writes `ANTHROPIC_API_KEY` / `AKMATORI_CUSTOM_PROVIDER_API_KEY`), but the bash-boundary scrub existed precisely to stop a prompt-injected `env` / `curl -d "$KEY"` from exfiltrating operator credentials through tool output — and the scrub list only covered `*_API_KEY` names. Added `EXTRA_CREDENTIAL_ENV_VARS = ["ANTHROPIC_AUTH_TOKEN"]` to `PROVIDER_API_KEY_ENV_VARS` to keep the invariant intact. The caveat in the existing doc comment still applies: this hook only guards the parent agent's bash tool; child `pi` bash tools are covered by the system subagent definitions omitting `bash`.

### F8 — Behavior changes to watch (no code change)
- 0.82.0 restricts generated model catalogs to **provider-verified reasoning effort levels** from models.dev. akmatori's `mapThinkingLevel` still emits `max`/`xhigh`, and pi clamps to model capabilities, so this degrades gracefully — but a model that previously accepted `xhigh`/`max` may now clamp to a lower level. Worth eyeballing in staging on the configured production model.
- 0.82.0 exposes `PI_SESSION_ID`, `PI_SESSION_FILE`, `PI_PROVIDER`, `PI_MODEL`, `PI_REASONING_LEVEL` to bash-tool commands. Non-secret, and akmatori's `spawnHook` spread preserves them; no action.
- 0.82.0 fixed compaction/branch-summary requests to use fresh routing session IDs with prompt caching disabled where supported — expect slightly different cache accounting on long investigations.

### F9 — Compaction tokens were missing from `tokens_used` (fixed)
`handleEvent` accumulated tokens only from `turn_end` → `message.usage.totalTokens`. Compaction runs its own summarization LLM call that `turn_end` never reports, so **every incident that compacted under-reported its cost** — and compaction only kicks in on long investigations, i.e. the expensive ones. pi 0.81.0 added `usage` to the compaction/branch-summary paths; `compaction_end` carries it as `result.usage`. Now summed in the existing `compaction_end` handler.

Rejected alternatives, for the record:
- **`entry_appended` event** — only emitted from the extension API's `appendEntry` for *custom* entries (`agent-session.js:1868`); pi's own compaction entries never fire it. Would have silently captured nothing.
- **Summing `sessionManager.getEntries()` post-run** — `CompactionEntry.usage`/`BranchSummaryEntry.usage` are there, but `getEntries()` returns the whole session including prior runs, so every resume would re-count old compactions. `tokens_used` is per-run.
- **`getUsageCostBreakdown()` / `UsageTotals`** — exist in `dist/core/usage-totals.d.ts` but are **not importable**: the package `exports` map only exposes `"."` and `"./rpc-entry"`.

Branch summaries also carry usage, but akmatori never forks or branches sessions, so that path can't fire today.

### F10 — Dependency advisories (fixed; not caused by the pi bump)
`npm audit --omit=dev` reported 4 high-severity advisories against akmatori's own tree, all stale lockfile pins rather than range constraints — pi's bundled copies were already patched:

| Package | Was | Now | Advisory |
|---|---|---|---|
| `undici` | 8.4.1 | 8.9.0 | 7 incl. TLS cert bypass via ProxyAgent, response-queue poisoning |
| `ws` | 8.20.1 | 8.21.1 | GHSA-96hv-2xvq-fx4p memory-exhaustion DoS |
| `protobufjs` | 7.5.8 | 7.6.5 | incl. GHSA-j3f2-48v5-ccww |
| `brace-expansion` | 5.0.7 (nested under pi's minimatch) | 5.0.8 | GHSA-mh99-v99m-4gvg OOM DoS |

`package.json` ranges were already permissive (`^8.0.0`, `^8.18.0`) — **no declared range changed**; this is a lockfile-only fix. The nested `brace-expansion` needed a full lockfile regeneration to re-resolve (npm's "up to date" path kept the old pin). An `overrides` entry was tried and then **removed**: a fresh resolve picks 5.0.8 unaided, so the override was dead config. Verified: production audit is now **0 vulnerabilities**.

Dev-only advisories remain (`@vitest/coverage-v8` → `test-exclude` → `glob`), needing a breaking vitest major. Test tooling only, never shipped in the image — deliberately not taken here.

### F11 — pi-subagents peers are optional; `pi-tui` is not required
The lockfile regeneration dropped a top-level `@earendil-works/pi-tui@0.74.0`, which looked alarming since pi-subagents 0.35+ imports pi-tui in its extension entrypoint (`src/extension/index.ts`). It is a **non-issue**: pi-subagents marks *all* peers `optional` in `peerDependenciesMeta`, and pi's extension loader aliases `@earendil-works/pi-tui` (and siblings) to its own bundled copies for jiti (`loader.js:39,90`). The 0.74.0 copy was stale cruft from the 0.35.1 install that later installs never pruned. Verified empirically by loading the real pi-subagents extension through `DefaultResourceLoader` with akmatori's `additionalExtensionPaths` config: **1 extension loaded, 0 errors**. Do not "fix" this by adding a `pi-tui` dependency.

### F6 — Free wins arriving with the bump
Resilient compaction & branch-summary retry (0.81.1, with RPC/SDK retry lifecycle events); expanded usage accounting for tools/compaction/branch-summaries in session totals (0.81.0); cache-friendly dynamic tool loading (0.80.7/0.80.9); `get_available_thinking_levels` RPC (0.81.0); llama.cpp local model management (0.81.0, plus 0.82.x context-window and catalog-persistence fixes); full provider extensions (0.81.0); constrained tool sampling via strict JSON Schema / grammars (0.82.0); DNS-failure auto-retry (0.82.0); new model catalogs (**Claude Opus 5** on Anthropic + Bedrock in 0.82.1, Grok 4.5, Kimi K3, Qwen Token Plan). These are adopted separately — see the feature-adoption follow-ups below.

## Changes Made

### agent-worker/package.json
- Bumped pi-* packages to `^0.82.1`, pi-subagents to `^0.36.0`; lockfile refresh (adds `@earendil-works/pi-tui@0.74.0` peer, typebox 1.1.38).

### agent-worker/src/agent-runner.ts
- Import: dropped `AuthStorage`, `ModelRegistry`; added `ModelRuntime`.
- `runExecute`: replaced `AuthStorage.inMemory()` + `setRuntimeApiKey` and `ModelRegistry.inMemory(authStorage)` with `await ModelRuntime.create({ modelsPath: null, allowModelNetwork: false })` + `await modelRuntime.setRuntimeApiKey(provider, key, { allowNetwork: false })`.
- `createAgentSession` call: dropped `authStorage`/`modelRegistry`, pass `modelRuntime`.
- Comments updated (auth block + `writeCustomProviderModelsJson` doc) to describe the ModelRuntime path.
- Added `EXTRA_CREDENTIAL_ENV_VARS = ["ANTHROPIC_AUTH_TOKEN"]`, folded into `PROVIDER_API_KEY_ENV_VARS` so the bash-boundary scrub covers pi 0.82.1's gateway bearer token (F7).
- `compaction_end` handler now adds `event.result?.usage?.totalTokens` to the run's token total (F9).

### agent-worker/package-lock.json
- Regenerated to clear stale vulnerable pins: `undici` 8.9.0, `ws` 8.21.1, `protobufjs` 7.6.5, `brace-expansion` 5.0.8 (F10). No `package.json` range changes.

### agent-worker tests
- `agent-runner.test.ts` / `orchestrator.test.ts`: SDK mock's `AuthStorage`/`ModelRegistry` replaced with `ModelRuntime.create` returning `{ setRuntimeApiKey }`. Two assertion tests rewritten to check `ModelRuntime.create()`, the `setRuntimeApiKey(provider, key, { allowNetwork: false })` call, and that `modelRuntime` reaches `createAgentSession`.
- Extended the existing bash-scrub exfiltration test with `ANTHROPIC_AUTH_TOKEN`.
- Two new token-accounting tests: compaction usage is added to the total, and an aborted compaction (no `result`) neither throws nor miscounts.

### CLAUDE.md
- Version bumps (line 11 + SDK notes); replaced the `ModelRegistry.inMemory(authStorage)` note with the ModelRuntime call shape + invariants; folded the `$`-safe note into it (dedup); updated subagent-subprocess-auth wording; noted the pi-tui peer, the lockfile-tracked child CLI, and the scrub-list rule from F7. Trimmed to stay under the 30000-byte cap.

## Verification Checklist

- [x] `npm install` — clean, `@earendil-works/pi-tui@0.74.0` peer auto-resolved, no peer warnings
- [x] `npm run build` — TypeScript build passes
- [x] `make test-agent` — 391/391 pass
- [x] Child CLI check — `node_modules/.bin/pi --version` = 0.82.1
- [x] `npm audit --omit=dev` — 0 vulnerabilities (F10)
- [x] Real pi-subagents extension load via `DefaultResourceLoader` — 1 extension, 0 errors (F11)
- [ ] `make test` (Go), `make test-web` — unaffected by this change, run for the full gate
- [ ] `make verify`
- [ ] Container rebuild (akmatori-agent); `pi --version` in image = 0.82.1
- [ ] Staging smoke: full investigation on a built-in provider (Anthropic); custom/on-prem provider run (child auth via `$`-ref apiKey + `forceAdaptiveThinking`); a subagent-invoking run (runbook-searcher / memory-searcher / memory-writer resolve the parent model); oneshot title generation
- [ ] Staging: confirm the configured production model still accepts its selected thinking level after the 0.82.0 reasoning-effort catalog change (F8)

## Feature-Adoption Follow-ups (separate work)

Ranked by strategic fit; each is its own change, not part of this bump:
1. **llama.cpp local model management** (0.81.0 + 0.82.x fixes) — self-hosted / air-gapped LLM serving.
2. **Full provider extensions** (0.81.0) — first-class replacement for the `models.json` + `forceAdaptiveThinking` custom-provider synthesis.
3. ~~**Expanded usage accounting** (0.81.0)~~ — partially done in F9 (compaction tokens). Remaining: per-tool usage and cost (not just token counts) for value-pricing; note the SDK's cost helpers are not exported, so cost would have to be derived from `Model.cost` locally.
4. **Resilient compaction retry + lifecycle events** (0.81.1) — reliability on long investigations; events can drive the Slack progress banner.
5. **Native dynamic tool loading** (0.80.7/0.80.9) — cache-preserving alternative to the `gateway_call` + `list_tools_for_tool_type` pattern.
6. **`get_available_thinking_levels` RPC** (0.81.0) — retire the hand-mirrored thinking-level list (worker/Go/web).
7. **Claude Opus 5 in the model picker** (0.82.1) — add to `web/src/components/settings/llmModelSuggestions.ts` (Anthropic + OpenRouter lists), as the 0.78.1/0.80.6 upgrades did for their new flagships.
8. **`ANTHROPIC_AUTH_TOKEN` gateway bearer auth** (0.82.1) — lets operators front Anthropic with a bearer-auth gateway; would need a provider/auth-mode option in LLM settings rather than the API-key-only path.
9. **Constrained tool sampling** (0.82.0) — strict JSON Schema sampling for `gateway_call` args could cut malformed-tool-call retries.
