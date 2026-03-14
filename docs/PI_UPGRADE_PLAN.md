# Pi-Coding-Agent SDK Upgrade Plan: 0.55.3 → 0.58.0

## Current State

| Package | Current | Latest |
|---------|---------|--------|
| `@mariozechner/pi-coding-agent` | 0.55.3 | 0.58.0 |
| `@mariozechner/pi-ai` | 0.55.3 | 0.58.0 |
| `@mariozechner/pi-agent-core` | 0.55.3 | 0.58.0 |

New dependency in 0.58.0: `@mariozechner/pi-tui` (^0.58.0) — only needed for TUI features, not our headless usage.

---

## Part 1: Complete Changelog (0.49.0 → 0.58.0) — Akmatori-Relevant Items

This section covers the full changelog from 0.49.0 (earliest available) through 0.58.0, highlighting items relevant to our headless SDK integration.

### 0.49.0 (2026-01-17)
- `ctx.compact()` and `ctx.getContextUsage()` exported for programmatic compaction control
- `VERSION` exported from package index

### 0.49.1 (2026-01-18)
- Shell command resolution for API keys in `models.json` using `!` prefix

### 0.49.2 (2026-01-19)
- AWS credential detection for ECS/Kubernetes environments
- Fixed OpenAI Responses replay for aborted turns

### 0.49.3 (2026-01-22)
- No significant SDK API changes for our use case

### 0.50.0 (2026-01-26) — Major Release
- **BREAKING**: External packages now configured via `packages` array instead of `extensions` in settings.json
- **BREAKING**: Resource loading uses `ResourceLoader` only; `discoverAuthStorage` and `discoverModels` removed from SDK
- **BREAKING**: `models.json` header values now resolve environment variables
- Custom providers via `pi.registerProvider()` — could allow us to register custom LLM endpoints dynamically
- Hot reload (`/reload`) of all resources
- Azure OpenAI Responses provider support
- OpenRouter routing support via `openRouterRouting`
- HTTP proxy environment variable support for API requests
- Skill invocation messages now collapsible with `disable-model-invocation` frontmatter
- Fixed 429 rate limit errors incorrectly triggering auto-compaction
- Fixed cross-provider handoff failing when switching from OpenAI Responses API providers

### 0.50.2 (2026-01-29)
- Hugging Face provider support
- `PI_CACHE_RETENTION=long` enables extended prompt caching (1hr Anthropic, 24hr OpenAI)
- Fixed auto-retry counter reset after successful LLM response

### 0.50.3 (2026-01-29)
- Kimi For Coding provider support

### 0.50.4 (2026-01-30)
- Vercel AI Gateway routing support
- OSC 52 clipboard support (not relevant for headless)

### 0.51.0 (2026-02-01) — Tool Signature Change
- **BREAKING**: `ToolDefinition.execute` parameter order changed: `(toolCallId, params, signal, onUpdate, ctx)` — **relevant if we create custom ToolDefinitions**
- Android/Termux support
- **Bash spawn hook via `pi.setBashSpawnHook()`** — alternative approach to our current `createBashTool` spawnHook
- Linux ARM64 musl support (Alpine Linux)
- Typed tool call events with `isToolCallEventType()` type guard
- `discoverAndLoadExtensions` exported for extension testing
- Extension event forwarding for message and tool execution lifecycles

### 0.51.1 (2026-02-02)
- Extensions can programmatically switch sessions via `switchSession()`

### 0.51.2 (2026-02-03)
- Extension tool output expansion controls

### 0.51.3 (2026-02-03)
- **BREAKING**: RPC `get_commands` response type renamed `"template"` to `"prompt"` (no impact, we don't use RPC)
- Local path support for `pi install`/`pi remove`

### 0.52.0 (2026-02-05) — Claude Opus 4.6
- Claude Opus 4.6 model support
- GPT-5.3 Codex model support
- SSH URL support for git packages
- `auth.json` API keys support shell command resolution (`!command`) and env var lookup
- Model selectors display selected model name
- Fixed images silently dropped when `prompt()` called with both images and streaming
- Skill loader now respects .gitignore when scanning directories

### 0.52.2 (2026-02-05)
- Default model for `anthropic` provider updated to `claude-opus-4-6`
- Default model for `openai-codex` updated to `gpt-5.3-codex`

### 0.52.5 (2026-02-05)
- Thinking level capability detection: Anthropic Opus 4.6 models expose `xhigh`

### 0.52.7 (2026-02-06)
- **BREAKING**: `models.json` provider `models` behavior changed from full replacement to merge-by-id
- Per-model overrides in `models.json` via `modelOverrides`
- Bedrock proxy support for unauthenticated endpoints
- Fixed queued steering/follow-up messages stuck after auto-compaction
- OpenAI Responses API now uses `store: false` by default (privacy improvement)

### 0.52.8 (2026-02-07)
- Claude Opus 4.5 replaced with Opus 4.6 as default model

### 0.52.9 (2026-02-08)
- Extensions can trigger full runtime reload via `ctx.reload()`
- `pi.getAllTools()` now exposes tool parameters
- Fixed 429 rate limit errors incorrectly triggering auto-compaction (again)

### 0.52.10 (2026-02-12)
- **BREAKING**: `ContextUsage.tokens` and `ContextUsage.percent` are now `number | null` (after compaction, unknown until next response)
- **BREAKING**: Removed `usageTokens`, `trailingTokens`, `lastUsageIndex` from `ContextUsage`
- Extension event forwarding for `message_start/update/end`, `tool_execution_start/update/end`
- `terminal_input` extension event for intercepting raw input
- Context overflow recovery: `model_context_window_exceeded` errors trigger auto-compaction

### 0.52.12 (2026-02-13)
- `transport` setting (`"sse"`, `"websocket"`, `"auto"`) for providers supporting multiple transports

### 0.53.0 (2026-02-17) — Auth Storage Breaking Change
- **BREAKING**: `AuthStorage` constructor is no longer public; must use static factories (`AuthStorage.create()`, `AuthStorage.fromStorage()`, `AuthStorage.inMemory()`)
- **BREAKING**: `SettingsManager` persistence changed — setters update in-memory immediately, queue disk writes; need `flush()` for durable persistence
- Auth storage backends (`FileAuthStorageBackend`, `InMemoryAuthStorageBackend`) and `AuthStorage.fromStorage()`
- `SettingsManager.drainErrors()` for caller-controlled error handling
- Auth/settings now preserve external edits via merge-on-write

### 0.53.1 (2026-02-19)
- Gemini 3.1 model catalog entries added across providers
- Claude Opus 4.6 Thinking added to google-antigravity

### 0.54.0 (2026-02-19)
- Default skill auto-discovery for `.agents/skills` directories

### 0.54.2 (2026-02-23)
- Incremental syntax highlighting fixes for large streaming operations

### 0.55.0 (2026-02-24)
- **BREAKING**: Resource precedence changed to project-first (`cwd/.pi`) before user-global (`~/.pi/agent`)
- Extension registration conflicts resolved by first-registration precedence

### 0.55.1 (2026-02-26)
- Offline startup mode via `--offline` or `PI_OFFLINE`
- Dynamic provider registration/unregistration (`pi.registerProvider()`, `pi.unregisterProvider()`)
- Fixed adaptive thinking for Claude Sonnet 4.6 in Anthropic and Bedrock providers

### 0.55.2 (2026-02-27)
- `pi.registerProvider()` takes effect immediately after initial load phase
- `pi.unregisterProvider(name)` for removing custom providers

### 0.55.3 (2026-02-27) — **Our Current Version**
- Fixed image paste keybinding on Windows (no impact)

### 0.55.4 (2026-03-02) — Dynamic Tool Registration
- **Runtime tool registration** via `pi.registerTool()` applies immediately without `/reload`
- **`promptSnippet`** and **`promptGuidelines`** on `ToolDefinition` — tools inject text into system prompt
- Fixed `session.prompt()` returning before retry completion

### 0.56.0 (2026-03-04) — Breaking: OAuth Imports
- **BREAKING**: OAuth exports moved to `@mariozechner/pi-ai/oauth` (we don't use OAuth, no impact)
- **BREAKING**: Scoped model thinking semantics changed — entries without `:<thinking>` suffix inherit session thinking level
- OpenCode Go provider added
- Compaction fixes for non-reasoning models

### 0.56.1 (2026-03-05) — Patch
- Extension alias resolution fix (no impact)

### 0.56.2 (2026-03-05) — GPT-5.4 Support
- GPT-5.4 model available across openai/openai-codex/azure providers
- Mistral native conversations integration

### 0.56.3 (2026-03-06) — Claude Sonnet 4.6
- Claude Sonnet 4.6 model added
- Auto-compaction resilience improvements — no longer retriggered spuriously
- Fixed parallel processes failing with false "No API key found" errors (**relevant for concurrent investigations**)
- Fixed OpenAI Responses reasoning replay regression (multi-turn continuity)

### 0.57.0 (2026-03-07) — Extension Hooks
- **`before_provider_request`** extension hook for intercepting/modifying provider payloads
- **BREAKING**: RPC mode strict LF-delimited JSONL framing (we don't use RPC mode, no impact)

### 0.57.1 (2026-03-07) — Session Directory Events
- **`session_directory`** extension event for customizing session directory paths
- Context overflow recovery: `model_context_window_exceeded` errors trigger auto-compaction

### 0.58.0 (2026-03-14) — 1M Context Windows
- **Claude Opus 4.6, Sonnet 4.6 context window expanded to 1M tokens**
- **Extension tool calls execute in parallel** by default
- Tool interception moved to agent-core `beforeToolCall`/`afterToolCall` hooks
- `GOOGLE_CLOUD_API_KEY` environment variable support for google-vertex
- Extensions can supply deterministic session IDs via `newSession()`
- Fixed retry regex to match `server_error` and `internal_error` error types
- Fixed usage statistics for OpenAI-compatible providers returning usage in `choice.usage`
- Fixed tool result images not sent in `function_call_output` for OpenAI Responses API
- Fixed assistant content sent as structured blocks instead of strings in `openai-completions`

---

## Part 2: All Breaking Changes Impact Assessment (0.49.0 → 0.58.0)

| Breaking Change | Version | Impact on Akmatori | Action Required |
|---|---|---|---|
| `packages` array replaces `extensions` in settings.json | 0.50.0 | **None** — we don't use settings.json, we use programmatic API | No action |
| `discoverAuthStorage`/`discoverModels` removed from SDK | 0.50.0 | **None** — we use `AuthStorage.inMemory()` and `ModelRegistry` directly | No action |
| `models.json` header env var resolution | 0.50.0 | **None** — we don't use models.json | No action |
| `ToolDefinition.execute` param order changed | 0.51.0 | **Potential** — affects us if/when we create native `ToolDefinition` objects | Must use new signature for Phase 3+ |
| `models.json` provider models merge-by-id | 0.52.7 | **None** — we don't use models.json | No action |
| `ContextUsage.tokens`/`.percent` now `number \| null` | 0.52.10 | **Low** — we don't currently read `ContextUsage` | No action (but consider for future) |
| `usageTokens`/`trailingTokens`/`lastUsageIndex` removed from `ContextUsage` | 0.52.10 | **None** — we don't use these fields | No action |
| `AuthStorage` constructor no longer public | 0.53.0 | **None** — we already use `AuthStorage.inMemory()` factory | No action |
| `SettingsManager` persistence semantics changed | 0.53.0 | **Low** — we use `SettingsManager.inMemory()` | Verify in-memory behavior unchanged |
| Resource precedence: project-first before user-global | 0.55.0 | **None** — we use `additionalSkillPaths`, not auto-discovery | No action |
| OAuth exports moved to `@mariozechner/pi-ai/oauth` | 0.56.0 | **None** — we don't use OAuth | No action |
| Scoped model thinking semantics changed | 0.56.0 | **Low** — we set thinking level explicitly | Verify `mapThinkingLevel()` works |
| RPC mode strict JSONL framing | 0.57.0 | **None** — we don't use RPC mode | No action |
| Extension tool interception API changed | 0.58.0 | **None** — we don't use extension tool interception | No action |

**Assessment: No breaking changes directly affect our current integration.** The `ToolDefinition.execute` signature change (0.51.0) only matters when we implement native tools in Phase 3+.

---

## Part 3: Upgrade Steps

### Step 1: Update Dependencies
```bash
cd agent-worker
npm install @mariozechner/pi-coding-agent@^0.58.0 @mariozechner/pi-ai@^0.58.0 @mariozechner/pi-agent-core@^0.58.0
```

### Step 2: Verify TypeScript Compilation
```bash
npx tsc --noEmit
```
Check for any type errors from API changes. Key areas to verify:
- `createAgentSession()` options — new optional fields added
- `AgentSessionEvent` — new event types (auto_compaction, auto_retry)
- `createBashTool()` — verify spawnHook signature unchanged
- `DefaultResourceLoader` options — verify `skillsOverride` signature unchanged
- `AuthStorage.inMemory()` — should be unchanged (was already a static factory)
- `SettingsManager.inMemory()` — verify still works as expected

### Step 3: Verify Event Handling
Our `agent-runner.ts` subscribes to events. New event types added since 0.55.3:
- `auto_compaction_start` / `auto_compaction_end`
- `auto_retry_start` / `auto_retry_end`

These are additive and won't break our existing switch statement, but we should consider handling them for better observability.

### Step 4: Run Tests
```bash
make test-agent
```

### Step 5: Integration Test
- Start the stack with `docker-compose up`
- Trigger an incident investigation
- Verify agent session creation, tool calling, and output streaming still work
- Test with Claude Opus 4.6 to verify 1M context window
- Test with GPT-5.4 if available

### Step 6: Update Dockerfile
Verify the Docker build still works with the new dependencies. No Dockerfile changes expected unless new system dependencies are required.

---

## Part 4: New Features We Can Adopt

### Priority 1: High Value, Low Effort (Free with upgrade)

#### 1.1 — 1M Context Window for Claude Models
**Version**: 0.58.0
**What**: Claude Opus 4.6 and Sonnet 4.6 now support 1M token context windows (up from 200K).
**Benefit**: Agents can handle much longer investigations — large log dumps, many tool calls, extended multi-turn conversations — before needing compaction.
**Effort**: Zero — comes automatically with model registry update.

#### 1.2 — Extended Prompt Caching
**Version**: 0.50.2+
**What**: `PI_CACHE_RETENTION=long` enables 1-hour caching for Anthropic and 24-hour for OpenAI.
**Benefit**: Significant cost reduction for repeated investigations with similar system prompts.
**Implementation**: Set `PI_CACHE_RETENTION=long` in agent-worker container environment.
**Effort**: One line in docker-compose.yml.

#### 1.3 — Improved Auto-Compaction and Retry
**Version**: 0.56.3, 0.57.1, 0.58.0
**What**:
- Auto-compaction resilient to persistent API errors
- Context overflow (`model_context_window_exceeded`) triggers auto-compaction
- Retry logic matches more error types (`server_error`, `internal_error`)
- 429 rate limit errors no longer incorrectly trigger auto-compaction
**Benefit**: More reliable long-running investigations.
**Effort**: Zero — comes with upgrade.

#### 1.4 — New Model Support
**Version**: Various
**What**: Claude Opus 4.6, Claude Sonnet 4.6, GPT-5.4, GPT-5.3 Codex, Gemini 3.1, MiniMax M2.5, and more.
**Benefit**: Users can select latest models in Akmatori UI with correct metadata.
**Effort**: Zero — comes with model registry update.

#### 1.5 — Parallel Process Fix
**Version**: 0.56.3
**What**: Fixed parallel processes failing with false "No API key found" errors due to lockfile contention.
**Benefit**: Critical for us — we run concurrent investigations. This eliminates spurious auth failures.
**Effort**: Zero — comes with upgrade.

#### 1.6 — OpenAI Responses API Fixes
**Version**: 0.58.0
**What**: Multiple fixes for OpenAI-compatible providers — usage statistics, tool result images, assistant content format.
**Benefit**: Better reliability when using OpenAI, Azure OpenAI, and OpenAI-compatible custom endpoints.
**Effort**: Zero — comes with upgrade.

### Priority 2: High Value, Small Effort

#### 2.1 — Dynamic Tool Registration (`promptSnippet` / `promptGuidelines`)
**Version**: 0.55.4+
**What**: Tool definitions can include `promptSnippet` (appears in "Available tools" section) and `promptGuidelines` (appended as guidelines to system prompt).
**How to use**: Refactor `TOOL_CALLING_INSTRUCTIONS` from a monolithic `appendSystemPrompt` string into per-tool `promptGuidelines` attached to the bash tool.

```typescript
const bashTool = createBashTool(workDir, {
  spawnHook: (ctx) => ({ /* existing spawnHook */ }),
});
bashTool.promptGuidelines = `
- Use python3 -c "from ssh import ..." for SSH operations
- Use python3 -c "from zabbix import ..." for Zabbix operations
- Always pass tool_instance_id from SKILL.md
`;
```

**Effort**: Small — refactor existing code.
**Benefit**: Cleaner system prompt, tools self-document.

#### 2.2 — New Event Types for Observability
**Version**: 0.58.0
**What**: `auto_compaction_start/end` and `auto_retry_start/end` events.
**How to use**: Stream compaction/retry status back to the UI.

```typescript
case "auto_compaction_start":
  onOutput(`[COMPACTION] Starting context compaction (reason: ${event.reason})\n`);
  break;
case "auto_compaction_end":
  onOutput(`[COMPACTION] Compaction ${event.aborted ? 'aborted' : 'complete'}\n`);
  break;
case "auto_retry_start":
  onOutput(`[RETRY] Attempt ${event.attempt}/${event.maxAttempts}: ${event.errorMessage}\n`);
  break;
case "auto_retry_end":
  if (!event.success) onOutput(`[RETRY] All retries exhausted: ${event.finalError}\n`);
  break;
```

**Effort**: Small.
**Benefit**: Better visibility into agent behavior during long investigations.

#### 2.3 — Extension Event Forwarding
**Version**: 0.52.10+
**What**: `message_start/update/end` and `tool_execution_start/update/end` extension events.
**How to use**: More granular event streaming. We already handle these via `subscribe()`, but the extension event system provides a cleaner abstraction if we build extensions.
**Effort**: Small to evaluate, medium to implement.

### Priority 3: Medium Value, Medium Effort

#### 3.1 — `before_provider_request` Extension Hook
**Version**: 0.57.0
**What**: Extensions can intercept and modify provider request payloads before they're sent.
**Use cases**:
  - Request logging/auditing (log all LLM API calls for compliance)
  - Token budget enforcement (reject requests exceeding a budget)
  - Request modification (inject context, modify temperature)
  - Custom proxy header injection
**Effort**: Medium — need to understand extension lifecycle in headless mode.

#### 3.2 — `session_directory` Extension Event
**Version**: 0.57.1
**What**: Extensions can customize session directory paths before session manager creation.
**How to use**: Organize sessions by date, severity, or other incident metadata.
**Effort**: Medium.

#### 3.3 — Deterministic Session IDs
**Version**: 0.58.0
**What**: Extensions can supply deterministic session IDs via `newSession()`.
**How to use**: Use incident ID as session ID — trivial lookup, no separate mapping needed.
**Effort**: Medium.

#### 3.4 — Custom Provider Registration
**Version**: 0.50.0+
**What**: `pi.registerProvider()` and `pi.unregisterProvider()` for dynamic provider management.
**How to use**: When users configure a "custom" LLM provider in the Akmatori UI, we could register it as a proper pi-mono provider rather than building a custom Model object manually. Would get proper model metadata, thinking level detection, etc.
**Effort**: Medium.

#### 3.5 — `beforeToolCall` / `afterToolCall` Hooks
**Version**: 0.58.0
**What**: Agent-core level hooks for intercepting tool calls.
**Use cases**:
  - Audit logging of all tool invocations
  - Rate limiting tool calls
  - Permission enforcement (block dangerous commands)
  - Tool call metrics collection
**Effort**: Medium.

### Priority 4: High Value, High Effort (Future)

#### 4.1 — Native Tool Definitions (Replacing Python Wrappers)
**What**: Register SSH/Zabbix as native `ToolDefinition` objects instead of `python3 -c` via bash.
**Benefits**:
  - JSON Schema input definitions → better type safety
  - `promptSnippet`/`promptGuidelines` → automatic documentation
  - Parallel execution support (0.58.0)
  - Tool result images/structured data
  - No Python runtime in agent container
  - Better agent behavior (native tools vs bash-invoked scripts)
**Note**: Must use new `execute(toolCallId, params, signal, onUpdate, ctx)` signature from 0.51.0.
**Effort**: High — rearchitect tool layer, update Go SKILL.md generation, update tests.

#### 4.2 — `ctx.compact()` for Programmatic Compaction Control
**Version**: 0.49.0+
**What**: Extensions/SDK can trigger compaction programmatically.
**How to use**: Could implement a "compact before continue" strategy — when resuming a long investigation, compact first to maximize available context for the follow-up.
**Effort**: Medium-High.

---

## Part 5: Implementation Phases

### Phase 1: Core Upgrade (1-2 hours)
1. Bump all three pi packages to `^0.58.0`
2. Run `npm install` and verify lockfile
3. Run `npx tsc --noEmit` — fix any type errors
4. Update event handler in `agent-runner.ts` to handle new event types
5. Run `make test-agent`
6. Docker build + integration test

### Phase 2: Quick Wins (2-4 hours)
1. Add auto-compaction/retry event streaming to the UI
2. Refactor `TOOL_CALLING_INSTRUCTIONS` into `promptGuidelines` on the bash tool
3. Set `PI_CACHE_RETENTION=long` in docker-compose.yml
4. Verify 1M context window works with Claude models

### Phase 3: Architecture Improvements (1-2 days, optional)
1. Implement deterministic session IDs via extension
2. Explore `before_provider_request` for request auditing
3. Explore custom provider registration for "custom" LLM endpoints
4. Prototype native tool definitions (replacing one Python wrapper as POC)

### Phase 4: Full Tool Migration (3-5 days, future)
1. Migrate all Python tool wrappers to native `ToolDefinition` objects
2. Update Go SKILL.md generation to not include Python examples
3. Remove Python runtime from agent container Dockerfile
4. Implement `beforeToolCall`/`afterToolCall` for audit logging
5. Update tests

---

## Part 6: Risks and Mitigations

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| Type incompatibilities in SDK | Low | `tsc --noEmit` check before deploying |
| Behavioral changes in tool execution | Low | Integration test with real incident |
| New dependencies causing Docker build issues | Low | Test Docker build in CI |
| `pi-tui` dependency pulled in unnecessarily | Low | Verify it's not required for headless usage; add to devDependencies if needed |
| Auto-compaction changes affecting investigation quality | Medium | Monitor first few investigations post-upgrade |
| Extension API not working well in headless mode | Medium | Test extension hooks in headless before committing to architecture |
| SettingsManager.inMemory() behavior changes | Low | Unit test settings behavior after upgrade |

---

## Part 7: Files Requiring Changes

### Phase 1 (Core Upgrade)
- `agent-worker/package.json` — version bump
- `agent-worker/package-lock.json` — regenerated
- `agent-worker/src/agent-runner.ts` — new event types in switch statement

### Phase 2 (Quick Wins)
- `agent-worker/src/agent-runner.ts` — refactor TOOL_CALLING_INSTRUCTIONS to promptGuidelines
- `agent-worker/src/orchestrator.ts` — forward new event types to API
- `docker-compose.yml` — add `PI_CACHE_RETENTION=long`

### Phase 3+ (Architecture)
- `agent-worker/src/agent-runner.ts` — extension hooks, native tools
- `agent-worker/Dockerfile` — potentially remove Python if tools fully migrated
- `internal/services/skill_service.go` — update SKILL.md generation
- `agent-worker/tools/` — deprecate Python wrappers
