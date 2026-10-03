# pi 1.0 follow-ups: assessment

Status: ASSESSMENT (no code). Prepared 2026-10-03 after the 0.85.1 → 1.0.1 upgrade. Covers the five
items the upgrade plan deferred. Facts checked against pi 1.0.1 and pi-subagents 0.75.0 as installed in
`agent-worker/node_modules`.

## Recommendation in one table

| # | Item | Verdict | Effort | Why |
|---|---|---|---|---|
| 1 | Cache warming | **Already on.** Verify, no code. | 0 | pi's default is `cacheWarming: "streaming"`. It only acts on models that declare a cache lifetime; in 1.0.1 that is the Anthropic built-ins only. |
| 2 | Cheap recon subagents | **Do it, without virtual models.** | small | pi-subagents has `subagents.defaultModel` / `agentOverrides.<name>.model` in the child `settings.json` we already write. Needs one setting in the API/UI and a bench pick. |
| 3 | Codemode instead of `execute_script` | **Spike.** Likely yes. | medium | Real sandbox (QuickJS) instead of `node:vm`, parallel tool calls, nested usage accounting, output caps. Prompt surface to migrate is one line plus tool descriptions. |
| 4 | Context edits instead of the 4 KB file dump | **Not yet.** Try a threshold change first. | small → medium | Context edits put the full result into context once and cost a cache re-write; the file dump never puts it in. Only wins in a band of mid-size results. Measure in the bench. |
| 5 | Native `tool_search` | **No for now.** | large | 133 gateway tools would have to become pi tools per session with allowlist and instance routing re-implemented. Our skills already tell the model exactly which tool to call. |

Order: 1 → 2 → 3 → 4. Revisit 5 when the catalog grows or operators add many external MCP servers.

## 1. Cache warming — already on

- `cacheWarming` default is `"streaming"` (pi settings doc). `"idle"` adds refreshes between runs; `"off"` disables.
- It runs "only when the model declares a cache lifetime and Pi estimates at least $0.05 in avoided
  cache-miss cost". The lifetime comes from `promptCache: { short, long }` on the model spec. In pi-ai 1.0.1
  only the Anthropic provider file declares it; OpenAI, Google and OpenRouter do not.
- Refresh usage counts toward session totals (so it shows up in our `tokens_used`) but never enters context.

What to do:
- Bench run with `showCacheMissNotices: true` in `SettingsManager.inMemory({...})` and a long SSH or
  script step (> 5 min) on Anthropic. Confirm a warming notice appears and that the next request reports
  cache reads, not writes.
- For the `custom` provider on an Anthropic-Messages endpoint, add `promptCache: { short: 300 }` to the
  managed model spec in `resolveModel()` / the `akmatori-custom` entry in `models.json`, otherwise warming
  never triggers there.
- Nothing to build. Add a note to the rule file after the bench confirms it.

## 2. Cheap recon subagents — use pi-subagents settings, not virtual models

Virtual models (`pi.registerVirtualModel()`, experimental) route *each request* to a physical model from
an extension, for classifier-driven routing. That is more machinery than we need. The three Akmatori
agents have fixed roles: `runbook-searcher` and `memory-searcher` are read-only grep/read loops,
`memory-writer` writes durable memory.

pi-subagents 0.75 resolves a child's model in this order: per-run override → `agentOverridesByProvider`
→ `agentOverrides.<name>.model` → agent frontmatter `model` → `subagents.defaultModel` → parent model.
`subagents.*` lives in pi `settings.json` (user or project scope). We already write the child's
`settings.json` in `writeSubagentSettingsFile()` (`defaultProvider` / `defaultModel` /
`defaultThinkingLevel`), so adding a `subagents` block is the same code path.

Design:
- New field on `LLMSettings` (Go `models_settings.go`, API, web `LLMSettingsSection.tsx`):
  `subagent_model` (nullable; NULL = same as parent) and `subagent_thinking_level` (nullable).
- Worker: when set, write `settings.subagents = { defaultModel: "<provider>/<id>", defaultThinking: ... }`
  and `agentOverrides: { "memory-writer": { model: "inherit" } }` so the writer keeps the parent model.
- Constraint: the child resolves the model from the same registry as the parent. Same provider ⇒ same
  API-key env var we already export, nothing else to do. `custom` ⇒ the cheap model must also be
  materialized in `models.json` (`writeCustomProviderModelsJson` already handles one model; extend to two).
- pi-subagents fails closed when a configured model is not in the registry (0.50/0.55/0.65), so a wrong id
  shows as a clear launch error, not a silent fallback.
- Bench: pick per-provider defaults among `claude-haiku-4-5`, `gpt-6-luna` / `gpt-5-mini`,
  `gemini-3.8-flash`, and the on-prem small model; measure recall of the runbook/memory search against
  the parent model. Ship the defaults as suggestions, not forced values.

Why it pays: the two search agents run on every incident and read many files; their tokens now count in
`agent_runs` (since the 0.85.1 upgrade), so the saving is measurable from day one.

## 3. Codemode instead of `execute_script` — spike

What we have: `execute_script` runs model-written JavaScript in `node:vm` inside the worker process with
`gateway_call`, discovery helpers, a path-confined sync `fs`, 5-minute timeout via `AbortController`.
Known weaknesses: `node:vm` is not a security boundary; a tight synchronous loop is not interruptible;
no memory cap; results of nested `gateway_call`s are invisible to pi (no events, no usage).

What pi 1.0 offers: the built-in `codemode` tool runs scripts in a QuickJS sandbox, calls registered tools
through `ctx.executeTool()` (events carry `parentToolCallId`, a bounded `nestedCalls` record and the
nested usage land on the calling tool's result), supports `Promise.allSettled` fan-out, caps output at
16 Mi characters / 100 000 items, and 1.0.0 cut its prompt cost by ~40 % (GPT-5.6 request with default
tools + codemode: ~3 300 tokens). Tools are exposed to scripts by `exposure` (`direct` tools are also
callable; `codemode` tools are callable but not declared). `store()`/`load()` persist JSON across calls
in the session.

What the spike must answer:
1. **Prompt cost.** Register our five tools with `namespace: { name: "akmatori", description }` and
   enable codemode via `createCodemodeExtension({ mode: "on" })` in `extensionFactories` plus
   `defaultTools: ["+codemode"]`. Measure the first-request token count against today. `mode: "only"`
   hides direct tools and forces scripts; probably wrong for us, since most calls are single.
2. **Large results inside scripts.** MCP tools hand scripts the *complete* result. Our `gateway_call`
   dumps ≥ 4 KB to a file inside the tool, so a script would get the preview. Options: detect the nested
   call (check whether the execute context exposes `parentToolCallId`; if not, register a second
   `gateway_call_raw` with `exposure: "codemode"` that skips the dump) and return the full payload to
   scripts only.
3. **No `fs` in QuickJS.** Scripts that today read `tool_outputs/*.json` would call `tools.read` (the
   built-in read tool is callable from codemode) or get the raw result per point 2.
4. **Timeouts and memory.** Confirm the QuickJS limits pi applies (the settings surface has only `mode`
   and `inlineBudget`), and that a runaway script cannot stall the worker.
5. **Prompt surface.** One line in `skill_prompt_service.go:180`, `BASH_TOOL_GUIDELINES` in
   `agent-runner.ts`, and the `execute_script` description in `gateway-tools.ts`. Small.
6. **Bench.** Same incidents, both tools available, compare success rate, turns and cost; then with
   `execute_script` removed. pi-subagents 0.74+ already blocks `subagent` from inside scripts, which
   matches our prompts (subagent calls are top-level).

Keep `execute_script` registered during the transition; remove it only after the bench.

## 4. Context edits instead of the 4 KB file dump — not yet

The API: `sessionManager.appendContextEdit(targetId, null)` omits one entry from future provider
context; `{ content }` replaces it. The target id is the session entry id of the tool-result message,
observable via the `entry_appended` session event (match `message.role === "toolResult"` and
`toolCallId`). The transcript on disk keeps the original.

Trade-off against today's mechanism:

| | File dump (today) | Context edit |
|---|---|---|
| Full result in model context | never | once (uncached input tokens) |
| Prompt cache | untouched | prefix invalid from the edited entry; the tail after it is re-written |
| Model can filter the data | only via an extra `execute_script` turn | in the same turn |
| Full data survives for later turns | yes, on disk | only if the edit keeps a preview + file path |
| After compaction | preview in summary (≤ 2 000 chars) | same |

So a context edit wins only for results in the band where the preview forces an extra turn but the full
payload is still cheap to send once, roughly 4–30 KB. Above that the file dump stays right; below 4 KB
nothing changes. The cheaper experiment is to move the threshold: `OUTPUT_SIZE_THRESHOLD` in
`gateway-client.ts` from 4 KB to 16 KB or 32 KB, bench the same incidents, and look at turns per incident
and cost. If that already removes most `execute_script` re-reads, context edits are not worth the
complexity. If mid-size results still dominate, implement: full result inline → on the next `turn_end`,
`appendContextEdit(entryId, { content: preview + path })`.

Design note if we do it: edit on `turn_end`, not on `tool_execution_end`, so the model sees the full
result for exactly one assistant turn; keep the file dump as the on-disk source of truth either way.

## 5. Native `tool_search` — no for now

`tool_search` (built-in, off by default) does BM25 over tools that are registered but not declared
(`exposure: "deferred"`) and declares matches for the next call. For it to help, every gateway tool would
have to be a pi tool: 133 today across 12 namespaces (netbox 19, kubernetes 17, pagerduty / jira /
grafana 13 each, catchpoint 12, postgresql / clickhouse 10, zabbix 9, victoria_metrics / ssh / proposals 5).
That means, per session, `tools/list` from the gateway filtered by the incident allowlist, name mangling
(dots are not allowed in pi tool names), and re-implementing instance routing (`logical_name`) and the
allowlist rewrite in each registered tool. Discovery would move from the gateway (`tools/list_by_type`,
`tools/detail`) to pi.

The benefit would be native per-tool schemas instead of `gateway_call({tool_name, args})`, which can
reduce malformed calls from weaker models. But our generated `SKILL.md` already names the exact tool and
arguments for each skill, so discovery is rarely the failure point. Revisit when the catalog passes a few
hundred tools or operators register several external MCP servers whose tools are unknown to the skills.
It shares the exposure/namespace registration work with the codemode spike, so do it after that, if at all.

## Sequencing

1. Bench: cache-warming notice check (1 run) and the 4 KB → 16/32 KB threshold experiment (same batch).
2. Subagent model setting: Go + API + web + worker, 1–2 days, then bench the per-provider defaults.
3. Codemode spike behind a worker env flag, bench, decide on `execute_script` removal.
4. Context edits only if the threshold experiment leaves a gap.
5. `tool_search`: parked.

## Changes Made (2026-10-04, branch `pi-upgrade-1.0.1`)

Items 1–3 implemented; items 4–5 untouched.

### 1. Cache warming
- `resolveModel()` adds `promptCache: { short: 300 }` to synthesized `anthropic-messages` specs so an
  unlisted Claude id does not silently opt out of warming. Built-in Anthropic models already declare it.
  Tests in `agent-runner.test.ts` ("resolveModel prompt-cache lifetime").
- The bench check (`showCacheMissNotices: true`, long tool step, confirm cache reads) still needs a live run.

### 2. Cheap recon subagents (settings end to end)
- Go: `LLMSettings.SubagentModel` / `SubagentThinkingLevel` (nullable varchar, AutoMigrate), create/update
  DTOs (`Nullable[string]` on update; null or "" clears), handler validation of the level, response keys,
  `LLMSettingsForWorker` + `applySubagentSettings()` onto the `new_incident` / `continue_incident` frame
  (`subagent_model`, `subagent_thinking_level`, omitempty). Tests: `api_settings_llm_subagent_test.go`.
- Worker: `LLMSettings.subagent_model` / `subagent_thinking_level`; orchestrator copies them;
  `buildSubagentsSettingsBlock()` writes `subagents.defaultModel` (provider-qualified), `defaultThinking`
  and pins `memory-writer` to `inherit` in both child settings files, preserving other operator keys;
  `writeCustomProviderModelsJson(..., extraModels)` materializes the extra id for `custom`. Tests in
  `agent-runner.test.ts` and `orchestrator.test.ts`.
- Web: `subagent_model` / `subagent_thinking_level` on `LLMConfig` and the request types; "Subagent model"
  text input with provider suggestions and a conditional "Subagent thinking level" select under Advanced
  settings; list rows show `Subagents: <model>`.
- OpenAPI: both fields documented; `thinking_level` enum gained the missing `max`.
- Not done: bench pick of per-provider defaults (suggested: `claude-haiku-4-5`, `gpt-6-luna`,
  `gemini-3.8-flash`); live check that a child on the override launches and `tokens_used` still includes
  its usage.

### 3. Codemode spike (behind `AKMATORI_CODEMODE=1`)
- `createGatewayFetchTool()` in `gateway-tools.ts`: `exposure: "codemode"`, namespace `akmatori`, calls
  `GatewayClient.call(..., { inline: true })` which skips the 4 KB file dump. Never declared to the model.
- `AgentRunner` with `codemode: true` adds `createCodemodeExtension({ mode: "on", models: false })` to the
  resource loader, `defaultTools: ["+codemode"]` to the settings, `gateway_fetch` to `customTools`, and a
  guideline on `gateway_call` pointing at `tools.gateway_fetch` for batch work. Env flag read in `index.ts`,
  threaded through `OrchestratorConfig`; `docker-compose.yml` exposes `AKMATORI_CODEMODE` (default 0).
- Offline probe on pi 1.0.1: codemode active, description ≈1 200 chars (~300 tokens), lists `gateway_fetch`
  under `## akmatori`, and each direct tool gains a one-line "Codemode: tools.<name>(args)" hint.
- Not done: the bench comparison (success rate, turns, cost) with and without the flag; the decision on
  removing `execute_script`; the Go skill prompt line that still says "use execute_script" (left as is
  because both tools coexist during the spike).
