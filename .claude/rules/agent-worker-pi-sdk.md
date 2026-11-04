---
paths:
  - "**/agent-worker/**"
---

# Agent worker

Flow: API sends `new_incident`, `continue_incident`, or `oneshot_llm_request` →
`orchestrator.ts` routes → `agent-runner.ts` creates pi-mono sessions for full investigations,
`oneshot-llm.ts` handles short provider-agnostic completions → results stream back over WebSocket;
session exports land in the worker work dir.

Key files: `orchestrator.ts` (message routing), `agent-runner.ts` (pi-mono session lifecycle),
`oneshot-llm.ts` (single-call LLM helper), `gateway-tools.ts` (tool registration + `gateway_call`),
`tool-output-formatter.ts` (streamed tool formatting).

Session resume is NOT used — Slack and proposal chat start fresh agent sessions per turn.

# SDK notes (`@earendil-works/pi-coding-agent`)

- Versions: pi-coding-agent/pi-ai/pi-agent-core `1.4.0`; the `pi` CLI is
  `node_modules/.bin/pi`. `pi auth check --provider <id>` / `--model <provider>/<id>` (0.84.1) is the
  fastest way to diagnose a provider or model that will not resolve
- `tokens_used` = `turn_end` usage + `compaction_end` `result.usage` (compaction's LLM call is
  invisible to `turn_end`); the same events also feed input/output/cacheRead/cacheWrite splits and
  `usage.cost.total` (SDK-computed from per-model pricing; 0 for synthesized custom specs) — sent on
  `agent_completed` as `input_tokens`/`output_tokens`/`cache_read_tokens`/`cache_write_tokens`/`cost_usd`
  and persisted per run in the API's `agent_runs` table (keyed by `run_id`; rows survive incident
  retention). Incident-level `tokens_used`/`execution_time_ms` ACCUMULATE across runs; per-run
  numbers live on `agent_runs`. One-shot calls remain untracked
- `PROVIDER_API_KEY_ENV_VARS` (bash scrub) must cover every credential env var pi reads — incl.
  `ANTHROPIC_AUTH_TOKEN` (0.82.1 bearer)
- pi-ai root is core-only: `complete` from `/compat`, `getBuiltinModel` from `/providers/all`; the
  Models API rejects our synthesized custom-provider specs (compat dispatches on `model.api`)
- models.json `apiKey` needs `$ENV_VAR` syntax — bare names are literals (pi 0.79.4+)
- Thinking level `max` (above `xhigh`); list mirrored in worker (agent-runner/types/orchestrator),
  Go (`models_settings.go`, `api_settings_llm.go`), web (types, `LLMSettingsSection.tsx`)
- pi 1.3.0+ pins its internal `@earendil-works/*` deps exactly: bump all three pi packages in one
  commit. pi needs Node ≥ 22.19 (Dockerfile pins `node:22.23-bookworm`)
- `undici` is forced to OUR version for the whole tree via `"overrides": {"undici": "$undici"}` in
  package.json: pi 1.4.0 pins `undici` 8.10.2 exactly, which has a high-severity DoS advisory
  (GHSA-xjj6-f33q-929c). Check `npm audit --omit=dev` and drop the override once pi pins ≥ 8.12.3
- Codemode runs at most 2 QuickJS workers per process across all sessions; on 1.4.0 a third script waits
  up to 60 s, then fails. `PI_CODEMODE_MAX_WORKERS` raises the cap — size it before codemode leaves spike
- Never pin pi `0.85.0`: it accidentally shipped internal experimental code; `0.85.1` fixed the SDK imports
- **agentDir ownership.** `/home/agent/.pi/agent` must belong to `agent`. Compose bind-mounts
  `extensions/` into it on top of the `agent_sessions` volume; if the image lacks the directory, Docker
  creates it as root on a FRESH volume. The Dockerfile creates it; volumes created before 2026-10-25
  keep the root dir until `docker exec -u root akmatori-agent chown agent:agent /home/agent/.pi/agent`
- **No subagents.** pi-subagents was removed: runbook and memory search and memory writes are the
  gateway tools `runbooks.*` / `memory.*` called through `gateway_call` (see the
  `memory-and-runbooks` rule). The worker writes no child `settings.json` / `models.json` and puts
  no provider key into `process.env`; `tokens_used` is the parent session only. The gateway client
  sends `X-Run-ID` (the frame's `run_id`) next to `X-Incident-ID`; bash gets `AKMATORI_RUN_ID`
- pi ≥ 1.0.1 ships no `npm-shrinkwrap.json`: pi's transitive deps are pinned by OUR lockfile only. The
  Dockerfile uses `npm ci`, so review the `package-lock.json` diff on every bump
- **Codemode spike** (`AKMATORI_CODEMODE=1`, off by default): adds `createCodemodeExtension({mode:"on",
  models:false})` to `extensionFactories`, `defaultTools: ["+codemode"]` to the in-memory settings, and
  the codemode-only `gateway_fetch` tool (`exposure: "codemode"`, namespace `akmatori`) which calls the
  gateway with `{ inline: true }` so scripts get the COMPLETE result (no 32 KB preview). Scripts run in
  pi's QuickJS sandbox: no fs, no network, no timers. `execute_script` stays registered until the bench
  decides. `tool_execution_*` events of nested calls carry `parentToolCallId`
- Cache warming is ON by default (`cacheWarming: "streaming"`, pi 0.86+) but only for models that declare
  `promptCache`; built-in Anthropic models do, and `resolveModel()` adds `{ short: 300 }` to synthesized
  `anthropic-messages` specs. OpenAI/Google/OpenRouter catalogs declare none (1.0.1)
- Model catalog drift: `gpt-6-astra-pro` / `-fast` exist only in the OpenRouter / Vercel catalogs, not at
  OpenAI; `gemini-3-pro-preview`, `gemini-2.0-flash`, `meta/llama-3.*` on NIM are no longer in pi's
  catalog (they still resolve via the synthesized spec, cost reported as 0). Check
  `getBuiltinModels(provider)` from `@earendil-works/pi-ai/providers/all` before adding a picker id
- Auth/model runtime: `ModelRuntime.create({modelsPath: null, allowModelNetwork: false})` +
  `setRuntimeApiKey(provider, key)` → pass `modelRuntime` to `createAgentSession`; key in-memory only
  (`RuntimeCredentials`, `$`-safe), `modelsPath: null` = parent uses explicit `model`. Since 0.84.0
  the third argument is auth-cancellation options (`{signal}`), NOT catalog-refresh options, so
  `allowModelNetwork: false` at `create()` is the ONLY thing keeping startup offline — verify that
  after any pi bump. The same call can now throw `CredentialSynchronizationError` (credential
  committed, local state did not sync); it sits on every incident's critical path, so
  `setRuntimeApiKeyTolerantly()` swallows exactly that error and rethrows everything else
- `runtimeProviderId()` maps `custom` → `akmatori-custom` (the dedicated slot that avoids clobbering
  an operator's own `providers.custom`) and is the single source of that mapping — use it for the
  model and the credential alike
- **A runtime key is NOT enough for a non-built-in provider.** pi resolves auth through the provider
  *registry*: `Models.getAuth` does `if (!providers.get(id)) return undefined` before it looks at
  credentials, and `setRuntimeApiKey` registers a credential, not a provider. With
  `modelsPath: null` the parent knows only pi's built-ins — every Akmatori provider except `custom`
  — so `custom` failed its first turn with "No API key found for custom" while the key sat unused.
  `ensureProviderAuthResolvable()` probes `getAuth` and calls `registerProvider(id, {baseUrl, api,
  models})` when it misses. Keep the probe: it no-ops for built-ins and self-heals if pi ever ships
  `custom`. One-shot calls never hit this — they pass `apiKey` straight to `complete()` — so this
  class of bug looks like "investigations fail but titles/summaries work"
- Sampling: per-model `temperature`/`top_p`/`top_k`/`max_tokens` on `LLMSettings` (NULL = send
  nothing, the pre-feature behaviour). pi models none of these in the agent loop, so `sampling.ts`
  maps them per-API via `onPayload`, which MUST **chain** `session.agent.onPayload` — never replace
  it, since `createAgentSession` installs its own hook there to dispatch `before_provider_request`.
  Wire fields on `AgentMessage` stay pointers: `omitempty` drops a deliberate `0`. Anthropic rejects
  temperature/top_p/top_k under extended thinking, and the Responses API rejects them under
  reasoning — `sampling.ts` drops them there rather than failing the request
- **Tool output limits** (`tool-output-limit.ts`): `gateway_call` previews ≥ 32 KB, `execute_script`
  cuts at 50 KB, and an always-on `tool_result` extension caps every top-level tool result at 256 KB
  (nested codemode calls skipped). Over-limit text goes to `<workDir>/tool_outputs/`. Reason: one
  tool result stays in context through compaction — a 9.4 MB minified-JS grep line printed by
  `execute_script` broke kimi-k3.1-pro (Moonshot 8 MB cap). Never return a tool result unbounded
- **System prompt.** `DefaultResourceLoader` gets `systemPrompt: AKMATORI_SYSTEM_PROMPT`
  (`system-prompt.ts`) instead of pi's "expert coding assistant" preamble. A custom prompt DROPS pi's
  tool list and every tool's `promptGuidelines`, so tool rules the model needs go into that prompt
- **Tool results are plain text** (`model-text.ts`): `gateway_call` objects and JSON returned by
  `execute_script` render YAML-like with multi-line strings as blocks, never escaped `\n` JSON
- Bash tool stays local (TypeScript variance friction)
- import `typebox` from `typebox`, not `@sinclair/typebox`
- `DefaultResourceLoader` requires `agentDir` (`getAgentDir()` in prod and mocks)
- Provider SDKs are lazy-loaded; akmatori forwards retry/timeout settings
- `compat.forceAdaptiveThinking: true` in synthesized specs for `anthropic-messages` providers
  (`minimax`, Anthropic-compatible endpoints) — extended-thinking wire format