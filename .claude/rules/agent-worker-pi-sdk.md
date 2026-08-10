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

- Versions: pi-coding-agent/pi-ai/pi-agent-core `0.84.1`, pi-subagents `0.45.1`; child `pi` CLI =
  `node_modules/.bin/pi`. `pi auth check --provider <id>` / `--model <provider>/<id>` (0.84.1) is the
  fastest way to diagnose a provider or model that will not resolve
- pi-subagents peers are `optional`; loader aliases `@earendil-works/*` to pi's bundled copies —
  don't add `pi-tui`
- `tokens_used` = `turn_end` usage + `compaction_end` `result.usage` (compaction's LLM call is
  invisible to `turn_end`); the same events also feed input/output/cacheRead/cacheWrite splits and
  `usage.cost.total` (SDK-computed from per-model pricing; 0 for synthesized custom specs) — sent on
  `agent_completed` as `input_tokens`/`output_tokens`/`cache_read_tokens`/`cache_write_tokens`/`cost_usd`
  and persisted per run in the API's `agent_runs` table (keyed by `run_id`; rows survive incident
  retention). Incident-level `tokens_used`/`execution_time_ms` ACCUMULATE across runs; per-run
  numbers live on `agent_runs`. One-shot calls and subagent child processes remain untracked
- `PROVIDER_API_KEY_ENV_VARS` (bash scrub) must cover every credential env var pi reads — incl.
  `ANTHROPIC_AUTH_TOKEN` (0.82.1 bearer)
- pi-ai root is core-only: `complete` from `/compat`, `getBuiltinModel` from `/providers/all`; the
  Models API rejects our synthesized custom-provider specs (compat dispatches on `model.api`)
- models.json `apiKey` needs `$ENV_VAR` syntax — bare names are literals (pi 0.79.4+)
- Project trust: headless child `pi` treats workspaces as untrusted (we write
  `<workDir>/.pi/settings.json`) — children use the global `<agentDir>/settings.json` pin, never
  workspace `.pi/extensions`; never set `defaultProjectTrust: "always"`
- Thinking level `max` (above `xhigh`); list mirrored in worker (agent-runner/types/orchestrator),
  Go (`models_settings.go`, `api_settings_llm.go`), web (types, `LLMSettingsSection.tsx`)
- pi-subagents reads `<agentDir>/extensions/subagent/config.json` (strict JSON); repo ships it with
  `toolDescriptionMode: "compact"`
- Auth/model runtime: `ModelRuntime.create({modelsPath: null, allowModelNetwork: false})` +
  `setRuntimeApiKey(provider, key)` → pass `modelRuntime` to `createAgentSession`; key in-memory only
  (`RuntimeCredentials`, `$`-safe), `modelsPath: null` = parent uses explicit `model`. Since 0.84.0
  the third argument is auth-cancellation options (`{signal}`), NOT catalog-refresh options, so
  `allowModelNetwork: false` at `create()` is the ONLY thing keeping startup offline — verify that
  after any pi bump. The same call can now throw `CredentialSynchronizationError` (credential
  committed, local state did not sync); it sits on every incident's critical path, so
  `setRuntimeApiKeyTolerantly()` swallows exactly that error and rethrows everything else
- **One provider id, parent and child.** pi-subagents starts each child with
  `--model ${parentModel.provider}/${parentModel.id}`, so the provider on the resolved model MUST
  equal the key in the child's `models.json` and `settings.json`. `runtimeProviderId()` maps `custom`
  → `akmatori-custom` (the dedicated slot that avoids clobbering an operator's own
  `providers.custom`) and is the single source of that mapping — use it for the model, the parent
  credential, and the settings pin alike. When the ids drifted apart, every subagent failed with
  "model <id> not found" while the parent ran normally, because the parent drives its session with an
  explicit model object and never parses that string. Built-in providers are unaffected: their ids
  already match on both sides
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
  reasoning — `sampling.ts` drops them there rather than failing the request. `onPayload` reaches the
  parent only; child subagents get their values from `samplingParams` on the managed `models.json`
  entry (pi 0.84.0+, OpenAI-compatible adapters only — Anthropic/Google/Bedrock ignore it). Keep
  `max_tokens` OUT of `samplingParams`: the merge is verbatim and the field name differs per adapter
  (`max_tokens` vs `max_output_tokens`), so output length stays with the per-API mapping
- Bash tool stays local (TypeScript variance friction)
- import `typebox` from `typebox`, not `@sinclair/typebox`
- `DefaultResourceLoader` requires `agentDir` (`getAgentDir()` in prod and mocks)
- Provider SDKs are lazy-loaded; akmatori forwards retry/timeout settings
- `compat.forceAdaptiveThinking: true` in synthesized specs for `anthropic-messages` providers
  (`minimax`, Anthropic-compatible endpoints) — extended-thinking wire format
- Subagents: `noExtensions: false` + `additionalExtensionPaths: ["/opt/pi-extensions/pi-subagents"]`
  (baked in image; `~/.pi/agent/extensions` is an operator mount); image needs `ripgrep`/`fzf`
- Subagent auth: child `pi` has its own model runtime — `agent-runner.ts` mirrors the API key into
  `process.env[<provider env var>]`; subagent `.md` omits `model:` so children inherit parent
  provider/model
