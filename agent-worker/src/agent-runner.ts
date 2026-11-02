/**
 * Agent runner wrapping the pi-mono SDK.
 *
 * Creates and manages pi-mono agent sessions for incident analysis and
 * remediation. Handles multi-provider authentication, event streaming,
 * session lifecycle (execute / resume / cancel), and proxy configuration.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import {
  createAgentSession,
  AgentSession,
  ModelRuntime,
  SessionManager,
  SettingsManager,
  DefaultResourceLoader,
  createBashToolDefinition,
  createCodemodeExtension,
  getAgentDir,
  type AgentSessionEvent,
} from "@earendil-works/pi-coding-agent";
import { getBuiltinModel } from "@earendil-works/pi-ai/providers/all";
import type { Model, ThinkingLevel as PiThinkingLevel } from "@earendil-works/pi-ai";
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel, ToolAllowlistEntry } from "./types.js";
import { applyProxyConfig } from "./proxy.js";
import { createSamplingPayloadHook, pickSamplingParams } from "./sampling.js";
import {
  formatToolArgs,
  formatToolOutput,
  extractToolText,
  type ToolExecutionTrace,
} from "./tool-output-formatter.js";
import { GatewayClient } from "./gateway-client.js";
import { createToolOutputLimitExtension } from "./tool-output-limit.js";
import { createGatewayCallTool, createGatewayFetchTool, createListToolsForToolTypeTool, createGetToolDetailTool, createListToolTypesTool, createExecuteScriptTool } from "./gateway-tools.js";

// ---------------------------------------------------------------------------
// Tool calling guidelines attached to the bash tool definition via typed
// promptGuidelines (ToolDefinition.promptGuidelines: string[]).
// ---------------------------------------------------------------------------

/**
 * Guidelines for infrastructure tool usage, attached to the bash tool
 * ToolDefinition via the typed `promptGuidelines` property (pi-mono 0.59.0+).
 * These appear in the system prompt's Guidelines section automatically when
 * the bash tool is active.
 *
 * Prior to 0.62.0, we used `(bashTool as any).promptGuidelines` on an
 * AgentTool instance. Since 0.62.0, built-in tools carry proper
 * ToolDefinition metadata, so we use `createBashToolDefinition()` which
 * returns a typed ToolDefinition with a `promptGuidelines: string[]` property.
 */
const BASH_TOOL_GUIDELINES: string[] = [
  "ALL infrastructure operations go through gateway_call. NEVER call tool names directly (e.g. victoria_metrics.label_values, ssh.execute_command, zabbix.get_hosts). Infrastructure tools: gateway_call, list_tools_for_tool_type, get_tool_detail, list_tool_types, execute_script.",
  "If you get a \"Tool not found\" error, you are calling it wrong — use gateway_call instead. Example: gateway_call(\"victoria_metrics.instant_query\", {query: \"up\"}, \"my-vm-instance\").",
  "ALWAYS use the read tool to load the relevant SKILL.md file first — it contains your instructions and output format requirements. Then read connected-tools/SKILL.md before the first gateway_call: it lists every connected tool with parameter schemas and gateway_call examples.",
  "Only use list_tools_for_tool_type for a tool that connected-tools/SKILL.md does not list. Use get_tool_detail to read the parameter descriptions of a listed function before its first call or after a refused call.",
  "For batch operations across multiple hosts or complex data processing, use execute_script. It runs JavaScript with built-in gateway_call(), list_tools_for_tool_type(), get_tool_detail(), and synchronous fs (readFileSync, writeFileSync). Do NOT use require() or import() in scripts.",
];

// ---------------------------------------------------------------------------
// Provider retry defaults (pi-mono 0.70.1+ retry.provider.* settings)
// ---------------------------------------------------------------------------

/**
 * Default provider-level retry/timeout settings forwarded to pi-mono via
 * SettingsManager. The 10-minute timeout protects long alert investigations
 * against slow on-prem/OpenRouter models that would otherwise abort mid-stream.
 */
const DEFAULT_PROVIDER_RETRY = {
  timeoutMs: 600_000,
  maxRetries: 3,
  maxRetryDelayMs: 60_000,
} as const;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ExecuteParams {
  incidentId: string;
  /** Agent run id from the WebSocket frame; sent to the gateway as X-Run-ID. */
  runId?: string;
  task: string;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  workDir: string;
  /** Names of enabled skills — only these will be loaded from the shared skills directory */
  enabledSkills?: string[];
  /** Tool instances the incident is authorized to use. When undefined, the gateway allows all tools (safe default for direct/debug calls). */
  toolAllowlist?: ToolAllowlistEntry[];
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
  /**
   * Called once the session has been added to activeSessions. The orchestrator
   * uses this to release a per-incident launch chain so the next launch's
   * abortInFlightSession sees the registered session instead of an empty slot
   * during the bootstrap window.
   */
  onRegistered?: () => void;
}

export interface ResumeParams {
  incidentId: string;
  /** See ExecuteParams.runId. */
  runId?: string;
  sessionId: string;
  message: string;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  workDir: string;
  /** Names of enabled skills — only these will be loaded from the shared skills directory */
  enabledSkills?: string[];
  /** Tool instances the incident is authorized to use. When undefined, the gateway allows all tools (safe default for direct/debug calls). */
  toolAllowlist?: ToolAllowlistEntry[];
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
  /** See ExecuteParams.onRegistered. */
  onRegistered?: () => void;
}

export interface AgentRunnerConfig {
  mcpGatewayUrl: string;
  /** Directory containing SKILL.md definitions for pi-mono resource loader */
  skillsDir?: string;
  /**
   * Spike flag (AKMATORI_CODEMODE=1): enable pi's built-in `codemode` tool
   * next to `execute_script`. Scripts run in pi's QuickJS sandbox and reach
   * the gateway through the codemode-only `gateway_fetch` tool, which returns
   * complete results instead of the 4 KB preview. Off by default until the
   * bench compares the two.
   */
  codemode?: boolean;
}

// ---------------------------------------------------------------------------
// Thinking level mapping
// ---------------------------------------------------------------------------

/**
 * Map our ThinkingLevel to pi-mono's. pi-mono accepts "off" at the
 * createAgentSession boundary (pi-agent-core ThinkingLevel includes it), even
 * though pi-ai's narrower ThinkingLevel does not. For OpenAI-compatible
 * providers without a thinkingLevelMap, pi-mono omits `reasoning_effort`
 * entirely when level is "off" — which matches what users mean by "off" and
 * avoids OpenAI-compatible gateways that reject "minimal" (some only accept
 * high|medium|low|none).
 */
/**
 * Extract the skill name from a read-tool path when it targets a SKILL.md
 * inside the shared skills directory (<skillsDir>/<name>/SKILL.md). Returns
 * undefined for any other path, a missing skillsDir, or non-string input.
 * Used to track the last skill the agent consulted during a run, which the
 * API matches against formatting rules.
 */
export function extractSkillNameFromReadPath(
  skillsDir: string | undefined,
  rawPath: unknown,
): string | undefined {
  if (!skillsDir || typeof rawPath !== "string" || rawPath.length === 0) {
    return undefined;
  }
  const base = path.resolve(skillsDir);
  const resolved = path.resolve(rawPath);
  if (!resolved.startsWith(base + path.sep)) {
    return undefined;
  }
  const rel = resolved.slice(base.length + 1).split(path.sep);
  if (rel.length !== 2 || rel[1] !== "SKILL.md" || rel[0] === "") {
    return undefined;
  }
  // Generated system skills (the tool list) are read by every run; they
  // must not replace the domain skill that formatting rules match on.
  if (GENERATED_SKILL_NAMES.has(rel[0])) {
    return undefined;
  }
  return rel[0];
}

/** Generated system skills that never latch last_skill. */
export const GENERATED_SKILL_NAMES: ReadonlySet<string> = new Set(["connected-tools"]);

export function mapThinkingLevel(level: ThinkingLevel): PiThinkingLevel | "off" {
  if (level === "off") return "off";
  const valid: PiThinkingLevel[] = ["minimal", "low", "medium", "high", "xhigh", "max"];
  return valid.includes(level as PiThinkingLevel) ? (level as PiThinkingLevel) : "medium";
}

// ---------------------------------------------------------------------------
// Usage accumulation
// ---------------------------------------------------------------------------

/**
 * The slice of pi-ai's Usage object the worker reads. `cost` is computed by
 * the SDK from the model's per-token pricing at request time — an all-zeros
 * cost table (synthesized custom models) yields cost.total 0.
 */
export interface RunUsage {
  input?: number;
  output?: number;
  cacheRead?: number;
  cacheWrite?: number;
  totalTokens?: number;
  cost?: { total?: number };
}

/** Per-run usage totals summed across turn_end and compaction_end events. */
export interface UsageTotals {
  totalTokens: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  costUsd: number;
}

export function addUsage(totals: UsageTotals, usage: RunUsage): void {
  totals.totalTokens += usage.totalTokens ?? 0;
  totals.input += usage.input ?? 0;
  totals.output += usage.output ?? 0;
  totals.cacheRead += usage.cacheRead ?? 0;
  totals.cacheWrite += usage.cacheWrite ?? 0;
  totals.costUsd += usage.cost?.total ?? 0;
}

/** Map accumulated totals onto ExecuteResult's wire-named usage fields. */
export function usageResultFields(totals: UsageTotals): {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_usd: number;
} {
  return {
    input_tokens: totals.input,
    output_tokens: totals.output,
    cache_read_tokens: totals.cacheRead,
    cache_write_tokens: totals.cacheWrite,
    cost_usd: totals.costUsd,
  };
}

// ---------------------------------------------------------------------------
// Model resolution
// ---------------------------------------------------------------------------

/**
 * Resolve a Model object from provider + model ID using pi-ai's registry.
 * Falls back to creating a custom model spec if the model isn't in the
 * built-in registry (e.g. custom endpoints or new models).
 */
/** Provider → pi API used when a model is not in the built-in registry. */
const PROVIDER_API_MAP: Record<string, string> = {
  openai: "openai-responses",
  anthropic: "anthropic-messages",
  google: "google-generative-ai",
  openrouter: "openai-completions",
  custom: "openai-completions",
  nvidia: "openai-completions",
  minimax: "anthropic-messages",
  "ant-ling": "openai-completions",
};

export function resolveModel(
  provider: string,
  modelId: string,
  baseUrl?: string,
): Model<any> {
  try {
    const builtInModel = getBuiltinModel(provider as any, modelId as any);
    // pi-ai may return undefined for unknown/custom models instead of throwing.
    // In that case, we must fall back to a custom model spec.
    if (builtInModel) {
      // Some anthropic-messages built-ins (e.g. MiniMax-M3) lack
      // forceAdaptiveThinking in the SDK registry. For providers in
      // ADAPTIVE_THINKING_REQUIRED_PROVIDERS, merge the flag so they use
      // effort-based adaptive thinking instead of the older budget-based
      // interleaved-thinking path, which MiniMax's endpoint does not support.
      // Native Anthropic models must NOT receive the flag: the SDK intentionally
      // leaves it off for models that use budget-based thinking (e.g.
      // claude-sonnet-4-5, claude-haiku-4-5).
      //
      // The compat check uses (model as any) because TypeScript 5.9's
      // Set.has() narrowing interacts with Model<any>'s generic conditional
      // compat type, causing the narrowed compat to evaluate incorrectly
      // when builtInModel.api === "anthropic-messages" is inside a complex
      // && chain. The cast is safe: we only read forceAdaptiveThinking,
      // which is defined on AnthropicMessagesCompat when api is anthropic-messages.
      let resolved: typeof builtInModel = builtInModel;
      if (
        ADAPTIVE_THINKING_REQUIRED_PROVIDERS.has(provider) &&
        builtInModel.api === "anthropic-messages" &&
        !(builtInModel as any).compat?.forceAdaptiveThinking
      ) {
        const existingCompat = (builtInModel as any).compat ?? {};
        resolved = { ...builtInModel, compat: { ...existingCompat, forceAdaptiveThinking: true } } as typeof builtInModel;
      }
      // When the operator configured a custom base URL, apply it even for
      // built-in models so on-prem/private endpoints are used instead of the
      // SDK's public default.
      return baseUrl ? { ...resolved, baseUrl } : resolved;
    }
  } catch {
    // Continue to fallback model spec below.
  }

  // Model not in built-in registry - create a custom model spec.
  // This handles custom providers, openrouter, and newly released models.
  const apiType = PROVIDER_API_MAP[provider] ?? "openai-completions";

  // Build compat flags for the synthesized model spec:
  // - Disable OpenAI-specific cache fields for custom (OpenAI-compatible) endpoints;
  //   many gateways reject prompt_cache_key / prompt_cache_retention with 400.
  // - Enable adaptive thinking format for any provider using the Anthropic Messages API
  //   (e.g. minimax, on-prem Anthropic-compatible endpoints). The flag tells pi-ai to
  //   use the adaptive thinking wire format even when the model is not in the built-in
  //   registry.
  // - Mirror ant-ling's built-in compat for unknown models: the endpoint uses
  //   reasoning: { effort } (not flat reasoning_effort) and rejects OpenAI-specific
  //   fields like store, developer role, and prompt_cache_retention.
  // - Send instructions as role "system" on custom endpoints. pi-ai sends
  //   "developer" for a reasoning model on any URL it does not recognize, and
  //   OpenAI-compatible gateways (vLLM/SGLang chat templates) can drop that
  //   role without an error: on gcore's DeepSeek endpoint every one-shot call
  //   ran with no system prompt, so correlation got prose instead of JSON.
  const compatFlags: Record<string, unknown> = {};
  if (provider === "custom") {
    compatFlags.supportsLongCacheRetention = false;
    compatFlags.supportsDeveloperRole = false;
  }
  if (provider === "ant-ling") {
    compatFlags.supportsStore = false;
    compatFlags.supportsDeveloperRole = false;
    compatFlags.supportsReasoningEffort = false;
    compatFlags.maxTokensField = "max_tokens";
    compatFlags.supportsLongCacheRetention = false;
    compatFlags.thinkingFormat = "ant-ling";
  }
  if (apiType === "anthropic-messages" && ADAPTIVE_THINKING_REQUIRED_PROVIDERS.has(provider)) {
    compatFlags.forceAdaptiveThinking = true;
  }
  const compat = Object.keys(compatFlags).length > 0 ? compatFlags : undefined;

  return {
    id: modelId,
    name: modelId,
    api: apiType,
    // Use the runtime id, not the Akmatori provider name: the runtime
    // credential and provider registration are keyed by it.
    provider: runtimeProviderId(provider),
    baseUrl: baseUrl ?? "",
    reasoning: true,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128_000,
    maxTokens: 16_384,
    ...(compat ? { compat } : {}),
    // pi's prompt-cache warming (default `cacheWarming: "streaming"`, 0.86.0+)
    // only acts on models that declare a cache lifetime. Built-in Anthropic
    // models carry one; a synthesized spec for an unlisted Claude id would
    // silently opt out, so declare Anthropic's 5-minute tier here.
    ...(apiType === "anthropic-messages" ? { promptCache: { short: 300 } } : {}),
  } as Model<any>;
}
// ---------------------------------------------------------------------------
// Provider ids and credential scrubbing
// ---------------------------------------------------------------------------

/**
 * Provider → env var name used by pi-ai's env-api-keys resolver. Mirrors the
 * single-key mapping from
 * @earendil-works/pi-ai/dist/env-api-keys.js#getApiKeyEnvVars. The worker
 * never sets these (the parent session holds its key in memory), but an
 * operator may set them on the container, so the bash tool strips them.
 * Only includes providers Akmatori actually configures.
 */
const PROVIDER_ENV_KEY: Record<string, string> = {
  anthropic: "ANTHROPIC_API_KEY",
  openai: "OPENAI_API_KEY",
  google: "GEMINI_API_KEY",
  openrouter: "OPENROUTER_API_KEY",
  nvidia: "NVIDIA_API_KEY",
  minimax: "MINIMAX_API_KEY",
  "ant-ling": "ANT_LING_API_KEY",
};

/**
 * Providers that use the Anthropic Messages API wire format but are NOT
 * native Anthropic — their endpoints require effort-based adaptive thinking
 * (forceAdaptiveThinking: true) rather than the older budget-based interleaved
 * thinking with the `interleaved-thinking` beta header, which they do not
 * support. Native Anthropic models only carry this flag when the SDK
 * explicitly marks them; we must not add it to unmarked built-ins.
 */
const ADAPTIVE_THINKING_REQUIRED_PROVIDERS = new Set(["minimax"]);

/**
 * Provider id the pi runtime uses for "custom" UI selections. A dedicated id
 * keeps akmatori's registration apart from any `custom` provider pi or an
 * operator may define.
 */
const AKMATORI_CUSTOM_PROVIDER_KEY = "akmatori-custom" as const;

/**
 * Map an Akmatori provider name to the provider id used by the pi runtime.
 *
 * Only "custom" is renamed, to the dedicated slot above. Use it for the
 * resolved model, the runtime credential and the provider registration alike:
 * pi looks up auth by the provider id on the model.
 */
function runtimeProviderId(provider: string): string {
  return provider === "custom" ? AKMATORI_CUSTOM_PROVIDER_KEY : provider;
}

/**
 * Legacy env var that older workers set for the custom provider's key. Still
 * stripped at the bash boundary in case an operator sets it.
 */
const AKMATORI_CUSTOM_API_KEY_ENV = "AKMATORI_CUSTOM_PROVIDER_API_KEY" as const;

/**
 * Additional credential-bearing env vars pi itself recognizes but akmatori
 * never sets. `ANTHROPIC_AUTH_TOKEN` (pi 0.82.1) authenticates against
 * Anthropic-compatible gateways with `Authorization: Bearer` — an operator
 * running an on-prem gateway may well set it on the agent container. We do not
 * populate it, but we must still strip it at the bash boundary so it cannot be
 * read back out through tool output.
 */
const EXTRA_CREDENTIAL_ENV_VARS: readonly string[] = ["ANTHROPIC_AUTH_TOKEN"];

/**
 * Env var names that may carry a provider API key in this process. Bash
 * spawns inherit process.env by default (pi-mono's getShellEnv() spreads it),
 * so without scrubbing, a prompt-injected `env` or `echo $ANTHROPIC_API_KEY`
 * tool call would exfiltrate an operator-set key through tool output. The
 * variables stay in this process's env and are stripped only on the bash
 * boundary.
 */
const PROVIDER_API_KEY_ENV_VARS: readonly string[] = [
  ...Object.values(PROVIDER_ENV_KEY),
  AKMATORI_CUSTOM_API_KEY_ENV,
  ...EXTRA_CREDENTIAL_ENV_VARS,
];

function scrubProviderApiKeysFromEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out = { ...env };
  for (const name of PROVIDER_API_KEY_ENV_VARS) {
    delete out[name];
  }
  return out;
}

/**
 * The subset of ModelRuntime this module needs to register a provider. Kept
 * structural so tests can supply a fake without constructing a real runtime.
 */
export interface ProviderRegisteringRuntime {
  getAuth(model: Model<any>): Promise<unknown>;
  registerProvider(providerId: string, config: Record<string, unknown>): void;
  // pi 0.84.0 changed the third argument from catalog-refresh options to auth
  // cancellation options (`AuthOperationOptions`). Mirror that here — a stale
  // shape would typecheck locally while diverging from the real runtime.
  setRuntimeApiKey(
    providerId: string,
    apiKey: string,
    options?: { signal?: AbortSignal },
  ): Promise<void>;
}

/**
 * Make the parent runtime able to authenticate `model`'s provider.
 *
 * pi resolves auth through the provider *registry*, not the credential store:
 * `Models.getAuth` bails with `if (!providers.get(id)) return undefined` before
 * it ever looks at credentials. `setRuntimeApiKey` only stores a credential —
 * it does not create a provider entry. Our parent runtime is built with
 * `modelsPath: null`, so the only providers it knows are pi's built-ins, and
 * `custom` is not one of them.
 *
 * The result was that an operator-configured custom endpoint failed its very
 * first turn with "No API key found for custom" while the key sat unused in
 * memory — one-shot calls kept working because they pass `apiKey` straight to
 * `complete()` and never consult the runtime.
 *
 * Probing with getAuth first keeps this self-limiting: built-in providers are
 * left untouched, and it degrades to a no-op if pi ever ships `custom` itself.
 */
export async function ensureProviderAuthResolvable(
  runtime: ProviderRegisteringRuntime,
  provider: string,
  apiKey: string,
  model: Model<any>,
): Promise<boolean> {
  let resolved: unknown;
  try {
    resolved = await runtime.getAuth(model);
  } catch (err) {
    // The probe runs for every provider, including the built-ins that already
    // work. If it ever throws, fall back to the pre-fix behaviour rather than
    // letting a diagnostic turn a narrow bug into an outage for everyone.
    console.warn(
      `[agent-runner] provider auth probe failed for "${provider}": ${(err as Error).message}`,
    );
    return false;
  }
  if (resolved) {
    return false;
  }
  runtime.registerProvider(provider, {
    name: provider,
    ...(model.baseUrl ? { baseUrl: model.baseUrl } : {}),
    api: model.api,
    models: [
      {
        id: model.id,
        name: model.name ?? model.id,
        api: model.api,
        ...(model.baseUrl ? { baseUrl: model.baseUrl } : {}),
        reasoning: model.reasoning ?? true,
        input: model.input ?? ["text"],
        cost: model.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: model.contextWindow ?? 128_000,
        maxTokens: model.maxTokens ?? 16_384,
        ...((model as { compat?: unknown }).compat
          ? { compat: (model as { compat?: unknown }).compat }
          : {}),
      },
    ],
  });
  // Re-assert the key so the runtime's snapshot (auth + available models) is
  // rebuilt with the provider now present. No third argument: pi 0.84.0 stopped
  // accepting catalog-refresh options here, and offline startup is guaranteed by
  // `allowModelNetwork: false` at ModelRuntime.create() instead.
  await setRuntimeApiKeyTolerantly(runtime, provider, apiKey);
  return true;
}

/**
 * Install a runtime API key, tolerating a post-commit synchronization failure.
 *
 * pi 0.84.0 routes `setRuntimeApiKey` through `synchronizeCredentialState`, which
 * can raise `CredentialSynchronizationError` — defined as "credential changes
 * that commit successfully but fail to synchronize local model state". The
 * credential is therefore already in place, and this call sits on the critical
 * path of every incident, so letting the throw escape would fail investigations
 * for a non-fatal bookkeeping problem.
 */
export async function setRuntimeApiKeyTolerantly(
  runtime: Pick<ProviderRegisteringRuntime, "setRuntimeApiKey">,
  provider: string,
  apiKey: string,
): Promise<void> {
  try {
    await runtime.setRuntimeApiKey(provider, apiKey);
  } catch (err) {
    // Matched by name rather than `instanceof`: the class is only reachable
    // through the package root, which the worker's tests replace with a mock,
    // and a duplicated package instance would defeat an identity check anyway.
    // pi sets `name` explicitly in the constructor.
    if ((err as Error)?.name !== "CredentialSynchronizationError") {
      throw err;
    }
    console.warn(
      `[agent-runner] credential state for "${provider}" committed but did not synchronize: ${(err as Error).message}`,
    );
  }
}


// ---------------------------------------------------------------------------
// AgentRunner
// ---------------------------------------------------------------------------

export class AgentRunner {
  private readonly mcpGatewayUrl: string;
  private readonly skillsDir?: string;
  private readonly codemode: boolean;
  private activeSessions = new Map<string, AgentSession>();

  constructor(config: AgentRunnerConfig) {
    this.mcpGatewayUrl = config.mcpGatewayUrl;
    this.skillsDir = config.skillsDir;
    this.codemode = config.codemode ?? false;
  }

  /**
   * Execute a new agent session for an incident.
   */
  async execute(params: ExecuteParams): Promise<ExecuteResult> {
    return this.runSession(params, params.task, false);
  }

  /**
   * Resume an existing session with a follow-up message.
   */
  async resume(params: ResumeParams): Promise<ExecuteResult> {
    return this.runSession(params, params.message, true);
  }

  /**
   * Common session setup and execution logic shared by execute() and resume().
   */
  private async runSession(
    params: ExecuteParams | ResumeParams,
    promptText: string,
    isResume: boolean,
  ): Promise<ExecuteResult> {
    const startTime = Date.now();

    // Set up proxy env vars before creating session
    applyProxyConfig(params.proxyConfig);

    // Auth & model runtime. `setRuntimeApiKey` stores the operator key in an
    // in-memory RuntimeCredentials overlay: never written to auth.json and taken
    // literally (no `$ENV` resolution), so keys containing `$` stay safe.
    // `modelsPath: null` keeps this parent runtime from reading agentDir
    // models.json (the parent drives the session with the explicit `model`
    // object resolved below); `allowModelNetwork: false` keeps startup offline —
    // since pi 0.84.0 that flag is the *only* thing gating network access here,
    // because setRuntimeApiKey no longer accepts catalog-refresh options.
    const modelRuntime = await ModelRuntime.create({
      modelsPath: null,
      allowModelNetwork: false,
    });
    // Key the credential by the runtime provider id, which is what the resolved
    // model carries and therefore what pi looks up during auth.
    await setRuntimeApiKeyTolerantly(
      modelRuntime as unknown as ProviderRegisteringRuntime,
      runtimeProviderId(params.llmSettings.provider),
      params.llmSettings.api_key,
    );

    // Model
    const model = resolveModel(
      params.llmSettings.provider,
      params.llmSettings.model,
      params.llmSettings.base_url,
    );

    // `setRuntimeApiKey` above stores the credential but does not create a
    // provider entry, and pi refuses to resolve auth for a provider it has no
    // registry entry for. Register one for provider ids pi does not ship —
    // in practice `custom`, the on-prem/OpenAI-compatible endpoint option.
    if (
      await ensureProviderAuthResolvable(
        modelRuntime as unknown as ProviderRegisteringRuntime,
        runtimeProviderId(params.llmSettings.provider),
        params.llmSettings.api_key,
        model,
      )
    ) {
      console.log(
        `[agent-runner] registered provider "${runtimeProviderId(params.llmSettings.provider)}" on the parent runtime (not a pi built-in)`,
      );
    }

    const thinkingLevel = mapThinkingLevel(params.llmSettings.thinking_level);

    // Session management: persist to disk so resume can restore conversation history.
    // For resume, use continueRecent to load the most recent session from the
    // incident's workspace directory. For new sessions, create a fresh one.
    //
    // Deterministic session IDs (pi-mono 0.58.0): For new sessions, we call
    // newSession({ id: incidentId }) to use the incident UUID as the pi-mono
    // session ID. This eliminates the separate incident_id ↔ session_id mapping
    // and makes debugging/audit simpler (grep by incident UUID finds everything).
    //
    // sessionDir (pi-mono 0.63.0): We pass a dedicated .sessions subdirectory
    // to isolate pi-mono session JSONL files from the agent's workspace files
    // (tool outputs, runbooks, SKILL.md, etc.). This makes cleanup easier and
    // avoids cluttering the workspace root with encoded-cwd session directories.
    const sessionDir = path.join(params.workDir, ".sessions");
    const sessionManager = isResume
      ? SessionManager.continueRecent(params.workDir, sessionDir)
      : SessionManager.create(params.workDir, sessionDir);
    if (!isResume) {
      sessionManager.newSession({ id: params.incidentId });
    }
    const settingsManager = SettingsManager.inMemory({
      retry: { provider: DEFAULT_PROVIDER_RETRY },
      // `+codemode` adds pi's codemode tool to the default built-in set
      // without touching the rest (0.99.0 `+name` syntax); customTools are
      // unaffected by defaultTools since 0.84.2.
      ...(this.codemode ? { defaultTools: ["+codemode"] } : {}),
    });

    // Create resource loader with skills directory for pi-mono's native skill system.
    // This discovers SKILL.md files and includes name+description in the system prompt,
    // loading full content on-demand when the agent invokes a skill.
    // Use skillsOverride to filter to only enabled skills — disabled skills may still
    // have SKILL.md files on disk but should not be available to the agent.
    //
    // Extensions stay enabled so operator extensions from
    // ~/.pi/agent/extensions (the akmatori_data/extensions mount) load.
    const enabledSkillNames = params.enabledSkills;
    const resourceLoader = new DefaultResourceLoader({
      cwd: params.workDir,
      agentDir: getAgentDir(),
      additionalSkillPaths: this.skillsDir ? [this.skillsDir] : [],
      // SDK sessions do not load pi's built-in extensions; codemode must be
      // added explicitly. `models: false` keeps the model catalog and
      // classifier API out of scripts — incidents never need them.
      // The output limit runs for every session: no single tool result may
      // flood the context (see tool-output-limit.ts).
      extensionFactories: [
        createToolOutputLimitExtension({ workDir: params.workDir }),
        ...(this.codemode ? [createCodemodeExtension({ mode: "on", models: false })] : []),
      ],
      noExtensions: false,
      noPromptTemplates: true,
      noThemes: true,
      ...(enabledSkillNames && enabledSkillNames.length > 0
        ? {
            skillsOverride: (base) => {
              const allowedSet = new Set(enabledSkillNames);
              return {
                skills: base.skills.filter((s) => allowedSet.has(s.name)),
                diagnostics: base.diagnostics,
              };
            },
          }
        : {}),
    });
    await resourceLoader.reload();
    // Surface extension load failures (operator extension syntax error, missing
    // dependency) as a startup signal instead of a silently missing tool.
    const extResult = resourceLoader.getExtensions();
    for (const extErr of extResult.errors ?? []) {
      console.warn(`[agent-runner] extension load error path=${extErr.path}: ${extErr.error}`);
    }

    // Create a typed bash ToolDefinition with spawnHook to inject MCP Gateway
    // env vars per-session, and promptGuidelines for system prompt inclusion.
    // Using createBashToolDefinition() (pi-mono 0.62.0+) returns a proper
    // ToolDefinition with typed promptGuidelines instead of requiring `as any`.
    // Passed via customTools so AgentSession picks up both the spawnHook and
    // the guidelines (the built-in bash tool is overridden by name match).
    //
    // We scrub provider API key env vars from the spawned shell so a
    // prompt-injected `env`/`curl $ANTHROPIC_API_KEY ...` cannot exfiltrate
    // the operator's LLM credentials via tool output.
    const bashToolDef = createBashToolDefinition(params.workDir, {
      spawnHook: (ctx) => ({
        ...ctx,
        env: {
          ...scrubProviderApiKeysFromEnv(ctx.env),
          MCP_GATEWAY_URL: this.mcpGatewayUrl,
          INCIDENT_ID: params.incidentId,
          ...(params.runId ? { AKMATORI_RUN_ID: params.runId } : {}),
        },
      }),
    });
    bashToolDef.promptGuidelines = BASH_TOOL_GUIDELINES;

    // Create gateway client for this session and register gateway tools as custom tools.
    const toolAllowlist = params.toolAllowlist;
    const gatewayClient = new GatewayClient({
      gatewayUrl: this.mcpGatewayUrl,
      incidentId: params.incidentId,
      runId: params.runId,
      workDir: params.workDir,
      toolAllowlist,
    });
    const gatewayToolCtx = { client: gatewayClient };
    const gatewayCallTool = createGatewayCallTool(gatewayToolCtx);
    const listToolsForToolTypeTool = createListToolsForToolTypeTool(gatewayToolCtx);
    const getToolDetailTool = createGetToolDetailTool(gatewayToolCtx);
    const listToolTypesTool = createListToolTypesTool(gatewayToolCtx);
    const executeScriptTool = createExecuteScriptTool({
      client: gatewayClient,
      workDir: params.workDir,
    });
    // Codemode spike: the script-only gateway_fetch tool (exposure "codemode")
    // plus a hint on gateway_call so the model knows the parallel path exists.
    const codemodeTools = this.codemode ? [createGatewayFetchTool(gatewayToolCtx)] : [];
    if (this.codemode) {
      gatewayCallTool.promptGuidelines = [
        ...(gatewayCallTool.promptGuidelines ?? []),
        "For batch work across many hosts or large result sets, prefer the codemode tool: write JavaScript that calls `await tools.gateway_fetch({ tool_name, args, instance })` (complete result, no file) — in parallel with Promise.allSettled when calls are independent — reduce the data in the script and return only what matters.",
      ];
    }

    const { session } = await createAgentSession({
      cwd: params.workDir,
      modelRuntime,
      model,
      thinkingLevel,
      // bashToolDef has specific type parameters (BashToolDetails, BashRenderState)
      // that are contravariant with ToolDefinition<TSchema, unknown, any> via renderCall/renderResult.
      // The cast is safe — AgentSession only reads name, execute, promptGuidelines, promptSnippet.
      customTools: [bashToolDef as unknown as import("@earendil-works/pi-coding-agent").ToolDefinition, gatewayCallTool, listToolsForToolTypeTool, getToolDetailTool, listToolTypesTool, executeScriptTool, ...codemodeTools],
      resourceLoader,
      sessionManager,
      settingsManager,
    });

    // Sampling overrides for the agent loop. pi-agent-core's createLoopConfig
    // carries no temperature/maxTokens at all — it forwards `agent.onPayload`
    // to every request instead — so this hook is the only place the operator's
    // settings can reach a provider on the agent path. Left unset (undefined)
    // when nothing is configured, which keeps requests byte-identical to the
    // pre-feature behaviour.
    const samplingHook = createSamplingPayloadHook(
      pickSamplingParams(params.llmSettings),
      (message) => console.warn(`[agent-runner] incident ${params.incidentId}: ${message}`),
    );
    if (samplingHook) {
      // createAgentSession already installs an onPayload of its own that
      // dispatches the `before_provider_request` extension event, so this must
      // chain rather than assign — a bare assignment would silently drop that
      // event for the whole session. Extensions transform the payload first;
      // the operator's sampling settings are applied to their result and win.
      const upstream = session.agent.onPayload;
      session.agent.onPayload = async (payload: unknown, model: Parameters<typeof samplingHook>[1]) => {
        const base = (await upstream?.(payload, model as never)) ?? payload;
        return samplingHook(base, model) ?? base;
      };
    }

    this.activeSessions.set(params.incidentId, session);
    // Signal the orchestrator that this launch has registered so it can
    // release the per-incident launch chain. Subsequent launches that were
    // queued behind us now see a populated activeSessions slot and their
    // abortInFlightSession can target this session instead of no-op'ing.
    params.onRegistered?.();

    // Accumulate response and token usage
    let responseText = "";
    let fullLog = "";
    const usageTotals: UsageTotals = {
      totalTokens: 0,
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      costUsd: 0,
    };
    const toolTraces = new Map<string, ToolExecutionTrace>();
    const thinkingBuffers = new Map<number, string>();

    let lastErrorMessage = "";
    let lastSkillName: string | undefined;
    const unsubscribe = session.subscribe((event: AgentSessionEvent) => {
      params.onEvent?.(event);

      // Track the last skill the agent consulted: pi-mono skills are invoked
      // by reading <skillsDir>/<name>/SKILL.md with the read tool. Latched on
      // tool_execution_start so even an aborted run keeps the observation.
      if (event.type === "tool_execution_start" && event.toolName === "read") {
        const args = event.args as Record<string, unknown> | undefined;
        const skill = extractSkillNameFromReadPath(
          this.skillsDir,
          args?.path ?? args?.file_path,
        );
        if (skill) {
          lastSkillName = skill;
        }
      }

      // Capture API-level errors from message_end / turn_end events.
      // The SDK surfaces provider errors (quota, auth, model not found, etc.)
      // as a message with stopReason "error" and an errorMessage field,
      // rather than throwing an exception.
      if (event.type === "message_end" || event.type === "turn_end") {
        const msg = event.message;
        if (msg && "role" in msg && msg.role === "assistant") {
          if (msg.stopReason === "error" && msg.errorMessage) {
            lastErrorMessage = msg.errorMessage;
          } else if (msg.stopReason !== "error") {
            // Clear any previously latched error — a successful retry or
            // subsequent turn means the session recovered from the transient
            // failure and the error should not be propagated.
            lastErrorMessage = "";
          }
        }
      }

      this.handleEvent(event, params.onOutput, (text) => {
        responseText += text;
        fullLog += text;
      }, (text) => {
        fullLog += text;
      }, (usage) => {
        addUsage(usageTotals, usage);
      }, toolTraces, thinkingBuffers);
    });

    try {
      await session.prompt(promptText);

      const sessionExportPath = this.exportSession(sessionManager, params.workDir);

      // Use SDK's getLastAssistantText() for a clean final response.
      // The accumulated responseText includes text from ALL turns (e.g.
      // "I'll investigate...", "Let me gather data...") which pollutes the
      // response field. We only want the last assistant message — the actual
      // investigation summary.
      const finalResponse = session.getLastAssistantText() ?? responseText;

      return {
        session_id: session.sessionId,
        response: finalResponse,
        full_log: fullLog,
        // Propagate API-level errors (quota, auth, model not found) even when
        // partial response text was collected from earlier turns.
        error: lastErrorMessage || undefined,
        tokens_used: usageTotals.totalTokens,
        execution_time_ms: Date.now() - startTime,
        ...usageResultFields(usageTotals),
        session_export: sessionExportPath,
        last_skill: lastSkillName,
      };
    } catch (err) {
      const sessionExportPath = this.exportSession(sessionManager, params.workDir);

      return {
        session_id: session.sessionId,
        response: responseText,
        full_log: fullLog,
        error: (err as Error).message,
        tokens_used: usageTotals.totalTokens,
        execution_time_ms: Date.now() - startTime,
        ...usageResultFields(usageTotals),
        session_export: sessionExportPath,
        last_skill: lastSkillName,
      };
    } finally {
      unsubscribe();
      // Only delete from activeSessions if THIS session is still the current
      // entry. When the orchestrator handles a second new_incident for the
      // same incident_id, it aborts the prior session and stores the
      // replacement here; if the prior session's finally still ran an
      // unconditional delete, it would remove the replacement and the next
      // cancel call would no-op. Same-session compare-and-delete keeps the
      // map honest under back-to-back starts.
      if (this.activeSessions.get(params.incidentId) === session) {
        this.activeSessions.delete(params.incidentId);
      }
    }
  }

  /**
   * Cancel an active execution for an incident.
   *
   * Compare-and-delete on the slot: if a replacement run has already
   * installed itself in activeSessions while this cancel awaited
   * session.abort(), do NOT delete — that would untrack the live
   * replacement and let later cancel/supersede requests miss it. The
   * superseded session's own finally block also compare-and-deletes,
   * so the slot still gets cleaned up by whichever owner sees itself
   * still in the map.
   */
  async cancel(incidentId: string): Promise<void> {
    const session = this.activeSessions.get(incidentId);
    if (session) {
      await session.abort();
      if (this.activeSessions.get(incidentId) === session) {
        this.activeSessions.delete(incidentId);
      }
    }
  }

  /**
   * Clean up all active sessions.
   */
  async dispose(): Promise<void> {
    for (const [id, session] of this.activeSessions) {
      try {
        await session.abort();
      } catch {
        // ignore errors during cleanup
      }
    }
    this.activeSessions.clear();
  }

  /**
   * Check if an incident has an active session.
   */
  hasActiveSession(incidentId: string): boolean {
    return this.activeSessions.has(incidentId);
  }

  // -------------------------------------------------------------------------
  // Private helpers
  // -------------------------------------------------------------------------

  /**
   * Export the session as JSONL to {workDir}/session_export.jsonl for
   * post-mortem analysis. Copies the pi-mono session file (already JSONL
   * format) to a well-known location. Returns the export path on success,
   * or undefined if export fails (non-fatal).
   */
  private exportSession(
    sessionManager: SessionManager,
    workDir: string,
  ): string | undefined {
    try {
      const sessionFile = sessionManager.getSessionFile();
      if (!sessionFile) return undefined;

      const exportPath = path.join(workDir, "session_export.jsonl");
      fs.copyFileSync(sessionFile, exportPath);
      return exportPath;
    } catch {
      // Export failure is non-fatal — the investigation result is more important
      return undefined;
    }
  }

  /**
   * Handle a pi-mono session event, dispatching to appropriate callbacks.
   */
  private handleEvent(
    event: AgentSessionEvent,
    onOutput: (text: string) => void,
    onResponseText: (text: string) => void,
    onLogText: (text: string) => void,
    onUsage: (usage: RunUsage) => void,
    toolTraces: Map<string, ToolExecutionTrace>,
    thinkingBuffers: Map<number, string>,
  ): void {
    switch (event.type) {
      case "message_update": {
        const assistantEvent = event.assistantMessageEvent;
        if (assistantEvent.type === "text_delta") {
          onOutput(assistantEvent.delta);
          onResponseText(assistantEvent.delta);
        } else if (assistantEvent.type === "thinking_start") {
          thinkingBuffers.set(assistantEvent.contentIndex, "");
        } else if (assistantEvent.type === "thinking_delta") {
          const current = thinkingBuffers.get(assistantEvent.contentIndex) ?? "";
          thinkingBuffers.set(assistantEvent.contentIndex, current + assistantEvent.delta);
        } else if (assistantEvent.type === "thinking_end") {
          const thought = (thinkingBuffers.get(assistantEvent.contentIndex) ?? assistantEvent.content ?? "").trim();
          thinkingBuffers.delete(assistantEvent.contentIndex);
          if (thought) {
            const thoughtLine = `\n🤔 ${thought}\n`;
            onOutput(thoughtLine);
            onLogText(thoughtLine);
          }
        }
        break;
      }

      case "tool_execution_start": {
        toolTraces.set(event.toolCallId, {
          toolName: event.toolName,
          args: event.args,
          updates: [],
        });
        const startLine = `\n🛠️ Running: ${event.toolName}\n`;
        onOutput(startLine);
        onLogText(startLine);
        break;
      }

      case "tool_execution_update": {
        const trace: ToolExecutionTrace = toolTraces.get(event.toolCallId) ?? {
          toolName: event.toolName,
          args: event.args,
          updates: [],
        };
        const updateText = extractToolText(event.partialResult);
        if (updateText) {
          trace.updates.push(updateText);
        }
        toolTraces.set(event.toolCallId, trace);
        break;
      }

      case "tool_execution_end": {
        const trace = toolTraces.get(event.toolCallId);
        const status = event.isError ? "❌ Failed:" : "✅ Ran:";
        const argsText = formatToolArgs(trace?.args);
        const outputText = formatToolOutput(trace?.updates ?? [], event.result);

        let resultSummary = `\n${status} ${event.toolName}`;
        if (argsText) {
          resultSummary += `\nArgs:\n${argsText}`;
        }
        if (outputText) {
          resultSummary += `\nOutput:\n${outputText}`;
        }
        resultSummary += "\n";

        onOutput(resultSummary);
        onLogText(resultSummary);
        toolTraces.delete(event.toolCallId);
        break;
      }

      case "turn_end": {
        // Extract token usage from the assistant message
        if (event.message && "usage" in event.message && event.message.usage) {
          onUsage(event.message.usage as RunUsage);
        }
        break;
      }

      case "compaction_start": {
        const compactLine = `\n📦 Compacting context (${event.reason})...\n`;
        onOutput(compactLine);
        onLogText(compactLine);
        break;
      }

      case "compaction_end": {
        // Compaction runs its own summarization LLM call, which `turn_end`
        // never sees (that only carries assistant-message usage). pi 0.81.0+
        // attributes those tokens to the compaction result, so without this
        // the incidents that actually compact — the long, expensive ones —
        // silently under-report `tokens_used`.
        //
        // Counted here rather than from session entries because this fires
        // once per live compaction: summing `sessionManager.getEntries()`
        // would re-count prior runs' compactions on every resume.
        const compactionUsage = event.result?.usage;
        if (compactionUsage) {
          onUsage(compactionUsage as RunUsage);
        }

        let compactResult: string;
        if (event.aborted) {
          compactResult = "\n📦 Context compaction aborted";
          if (event.errorMessage) {
            compactResult += `: ${event.errorMessage}`;
          }
          if (event.willRetry) {
            compactResult += " (will retry)";
          }
          compactResult += "\n";
        } else {
          compactResult = "\n📦 Context compaction complete\n";
        }
        onOutput(compactResult);
        onLogText(compactResult);
        break;
      }

      case "auto_retry_start": {
        const retryLine = `\n🔄 Retrying (attempt ${event.attempt}/${event.maxAttempts}): ${event.errorMessage}\n`;
        onOutput(retryLine);
        onLogText(retryLine);
        break;
      }

      case "auto_retry_end": {
        if (!event.success) {
          const failLine = `\n🔄 All retries exhausted after ${event.attempt} attempts: ${event.finalError ?? "unknown error"}\n`;
          onOutput(failLine);
          onLogText(failLine);
        }
        break;
      }

      default:
        // Other events (agent_start, agent_end, turn_start, etc.) - no output needed
        break;
    }
  }

}
